package repository

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"nerae/api/internal/modules/workout/domain"
)

func TestExerciseNameProtection(t *testing.T) {
	p, err := NewExerciseNameProtector(bytes.Repeat([]byte{7}, 32), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := p.seal(10, 20, "entry-001", "自定义动作")
	if err != nil || bytes.Contains(ciphertext, []byte("自定义动作")) {
		t.Fatal("exercise name was not protected")
	}
	name, err := p.open(10, 20, "entry-001", ciphertext)
	if err != nil || name != "自定义动作" {
		t.Fatal("could not recover exercise name")
	}
	for _, item := range []struct {
		user, session uint64
		entry         string
	}{{11, 20, "entry-001"}, {10, 21, "entry-001"}, {10, 20, "entry-002"}} {
		if _, err := p.open(item.user, item.session, item.entry, ciphertext); err == nil {
			t.Fatal("ciphertext accepted for a different owner or entry")
		}
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := p.open(10, 20, "entry-001", ciphertext); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestWorkoutRecordTransactions(t *testing.T) {
	db := privateDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	createUser := func() uint64 {
		t.Helper()
		result, err := db.ExecContext(ctx, `INSERT INTO user (created_at, updated_at) VALUES (UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`)
		if err != nil {
			t.Fatal("could not create isolated subject")
		}
		id, err := result.LastInsertId()
		if err != nil || id <= 0 {
			t.Fatal("could not identify isolated subject")
		}
		return uint64(id)
	}
	user, otherUser := createUser(), createUser()
	p, err := NewExerciseNameProtector(bytes.Repeat([]byte{7}, 32), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil || p.InitializeExerciseKey(ctx, initTx) != nil || initTx.Commit() != nil {
		t.Fatal("could not initialize isolated encryption epoch")
	}
	wrong, _ := NewExerciseNameProtector(bytes.Repeat([]byte{8}, 32), "test-key")
	wrongInit, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not test encryption epoch")
	}
	if err := wrong.InitializeExerciseKey(ctx, wrongInit); !errors.Is(err, ErrUnavailable) {
		_ = wrongInit.Rollback()
		t.Fatal("wrong key initialized an existing epoch")
	}
	_ = wrongInit.Rollback()
	store, _ := NewRecordStore(p)
	free := NewSessionStore()
	createTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not create workout")
	}
	session, err := free.CreateFree(ctx, createTx, user, FreeSessionInput{time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), timeZone("UTC"), "record-event-001"})
	if err != nil || createTx.Commit() != nil {
		t.Fatal("could not commit workout")
	}
	apply := func(owner, version uint64, batch domain.Batch, commit bool) (uint64, bool, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, false, err
		}
		defer tx.Rollback()
		v, changed, err := store.applyBatchWithoutOrder(ctx, tx, owner, session.ID, version, batch)
		if err != nil {
			return 0, false, err
		}
		if commit {
			err = tx.Commit()
		}
		return v, changed, err
	}
	first := domain.Set{RecordKey: "record-001", ExerciseEntryKey: "entry-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "自定义动作"},
		Index: 1, Kind: domain.Weight, WeightKg: "42.5", Reps: 8}
	second := first
	second.RecordKey, second.Index, second.WeightKg = "record-002", 2, "45"
	third := domain.Set{RecordKey: "record-003", ExerciseEntryKey: "entry-002", Exercise: domain.Exercise{Source: domain.Custom, Name: "静态支撑"},
		Index: 1, Kind: domain.Duration, Seconds: 45, RPE: "7.5"}
	v, changed, err := apply(user, 1, domain.Batch{Sets: []domain.Set{first, second, third}}, true)
	if err != nil || !changed || v != 2 {
		t.Fatal("could not save actual sets")
	}
	var ciphertext []byte
	if err := db.QueryRowContext(ctx, `SELECT name_snapshot_enc FROM workout_exercise WHERE session_id = ? AND exercise_entry_key = ?`, session.ID, []byte(first.ExerciseEntryKey)).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte(first.Exercise.Name)) {
		t.Fatal("stored exercise name was not encrypted")
	}
	if v, changed, err := apply(user, 2, domain.Batch{Sets: []domain.Set{first}}, true); err != nil || changed || v != 2 {
		t.Fatal("equivalent decimal update changed the version")
	}
	duplicate := domain.Batch{Sets: []domain.Set{first, first}}
	if _, _, err := apply(user, 2, duplicate, true); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate set key was accepted")
	}
	invalidName := first
	invalidName.RecordKey, invalidName.ExerciseEntryKey, invalidName.Exercise.Name = "record-bad", "entry-bad", string([]byte{0xff})
	if _, _, err := apply(user, 2, domain.Batch{Sets: []domain.Set{invalidName}}, true); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid UTF-8 exercise name was accepted")
	}
	validChange := first
	validChange.WeightKg = "50"
	invalidChange := second
	invalidChange.Reps = 0
	if _, _, err := apply(user, 2, domain.Batch{Sets: []domain.Set{validChange, invalidChange}}, true); !errors.Is(err, ErrInvalid) {
		t.Fatal("partially invalid batch was accepted")
	}
	if _, _, err := apply(user, 2, domain.Batch{Sets: []domain.Set{validChange}}, false); err != nil {
		t.Fatal("could not exercise rollback")
	}
	var persistedWeight string
	var persistedVersion uint64
	if err := db.QueryRowContext(ctx, `SELECT weight_kg FROM set_record WHERE session_id = ? AND record_key = ?`, session.ID, []byte(first.RecordKey)).Scan(&persistedWeight); err != nil || persistedWeight != "42.50" {
		t.Fatal("invalid or rolled-back batch changed a set")
	}
	if err := db.QueryRowContext(ctx, `SELECT version FROM workout_session WHERE id = ?`, session.ID).Scan(&persistedVersion); err != nil || persistedVersion != 2 {
		t.Fatal("invalid or rolled-back batch changed the version")
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_set_insert BEFORE INSERT ON set_record FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'isolated test failure'`); err != nil {
		t.Fatal("could not install isolated write failure trigger")
	}
	failing := first
	failing.RecordKey, failing.ExerciseEntryKey = "record-fail", "entry-fail"
	if _, _, err := apply(user, 2, domain.Batch{Sets: []domain.Set{failing}}, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("injected database failure was not reported")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_set_insert`); err != nil {
		t.Fatal("could not remove isolated write failure trigger")
	}
	var partial int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_exercise WHERE session_id = ? AND exercise_entry_key = ?`, session.ID, []byte(failing.ExerciseEntryKey)).Scan(&partial); err != nil || partial != 0 {
		t.Fatal("mid-write failure left an exercise entry")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM set_record_key WHERE session_id = ? AND record_key = ?`, session.ID, []byte(failing.RecordKey)).Scan(&partial); err != nil || partial != 0 {
		t.Fatal("mid-write failure left a group key")
	}
	if err := db.QueryRowContext(ctx, `SELECT version FROM workout_session WHERE id = ?`, session.ID).Scan(&persistedVersion); err != nil || persistedVersion != 2 {
		t.Fatal("mid-write failure changed the version")
	}
	if _, err := db.ExecContext(ctx, `UPDATE set_record SET reps = 0 WHERE session_id = ? AND record_key = ?`, session.ID, []byte(third.RecordKey)); err == nil {
		t.Fatal("database accepted repetitions on a duration set")
	}
	badDecimal := first
	badDecimal.WeightKg = "1/2"
	if _, _, err := apply(user, 2, domain.Batch{Sets: []domain.Set{badDecimal}}, true); !errors.Is(err, ErrInvalid) {
		t.Fatal("non-contract decimal was accepted")
	}
	if _, _, err := apply(otherUser, 2, domain.Batch{Sets: []domain.Set{first}}, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("different subject changed the workout")
	}
	changedName := first
	changedName.Exercise.Name = "另一个动作"
	if _, _, err := apply(user, 2, domain.Batch{Sets: []domain.Set{changedName}}, true); !errors.Is(err, ErrInvalid) {
		t.Fatal("frozen exercise entry was changed")
	}
	if v, changed, err := apply(user, 2, domain.Batch{DeleteKeys: []string{first.RecordKey}}, true); err != nil || !changed || v != 3 {
		t.Fatal("could not delete the first set")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM set_record WHERE session_id = ? AND record_key = ?`, session.ID, []byte(first.RecordKey)).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted set retained its actual values")
	}
	if _, _, err := apply(user, 3, domain.Batch{Sets: []domain.Set{first}}, true); !errors.Is(err, ErrSetConflict) {
		t.Fatal("deleted key was reused without explicit restoration")
	}
	if v, changed, err := apply(user, 3, domain.Batch{Sets: []domain.Set{first}, RestoreKeys: []string{first.RecordKey}}, true); err != nil || !changed || v != 4 {
		t.Fatal("explicit restoration failed")
	}
	badStore, _ := NewRecordStore(wrong)
	badTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not test wrong key")
	}
	if _, _, err := badStore.applyBatchWithoutOrder(ctx, badTx, user, session.ID, 4, domain.Batch{Sets: []domain.Set{first}}); !errors.Is(err, ErrUnavailable) {
		_ = badTx.Rollback()
		t.Fatal("wrong key epoch was accepted")
	}
	_ = badTx.Rollback()
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 1
	if _, err := db.ExecContext(ctx, `UPDATE workout_exercise SET name_snapshot_enc = ? WHERE session_id = ? AND exercise_entry_key = ?`, tampered, session.ID, []byte(first.ExerciseEntryKey)); err != nil {
		t.Fatal("could not tamper with test ciphertext")
	}
	if _, _, err := apply(user, 4, domain.Batch{Sets: []domain.Set{first}}, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("tampered exercise ciphertext was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_exercise SET name_snapshot_enc = ? WHERE session_id = ? AND exercise_entry_key = ?`, ciphertext, session.ID, []byte(first.ExerciseEntryKey)); err != nil {
		t.Fatal("could not restore test ciphertext")
	}
	// Hold the subject row and verify both versioned writes reach its lock.
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin concurrency gate")
	}
	var locked uint64
	if err := gate.QueryRowContext(ctx, `SELECT id FROM user WHERE id = ? FOR UPDATE`, user).Scan(&locked); err != nil {
		_ = gate.Rollback()
		t.Fatal("could not lock isolated user")
	}
	type outcome struct {
		version uint64
		err     error
	}
	results := make(chan outcome, 2)
	for _, weight := range []string{"50", "55"} {
		go func(weight string) {
			item := first
			item.WeightKg = weight
			version, _, err := apply(user, 4, domain.Batch{Sets: []domain.Set{item}}, true)
			results <- outcome{version, err}
		}(weight)
	}
	deadline := time.Now().Add(5 * time.Second)
	blocked := 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
            FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
            WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'user'`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not observe lock waits")
		}
		if blocked >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 2 {
		_ = gate.Rollback()
		t.Fatal("both versioned updates did not reach the lock")
	}
	if err := gate.Rollback(); err != nil {
		t.Fatal("could not release lock")
	}
	a, b := <-results, <-results
	if !((a.err == nil && a.version == 5 && errors.Is(b.err, ErrVersionConflict)) || (b.err == nil && b.version == 5 && errors.Is(a.err, ErrVersionConflict))) {
		t.Fatal("same-version updates did not produce one success and one conflict")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_session SET status = 'completed' WHERE id = ?`, session.ID); err != nil {
		t.Fatal("could not set completed fixture")
	}
	if v, changed, err := apply(user, 5, domain.Batch{DeleteKeys: []string{first.RecordKey, second.RecordKey, third.RecordKey}}, true); err != nil || !changed || v != 6 {
		t.Fatal("could not delete last actual sets")
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM workout_session WHERE id = ?`, session.ID).Scan(&status); err != nil || status != "in_progress" {
		t.Fatal("empty completed workout did not return to draft")
	}
}
