package repository

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/workout/domain"
)

// This test only accepts the task-named, private MySQL socket checked by
// privateV2DB; it never connects to a developer or production database.
func TestPersonalExercisePrivateMySQL(t *testing.T) {
	db := privateV2DB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	newUser := func() uint64 {
		result, err := db.ExecContext(ctx, `INSERT INTO user (created_at, updated_at) VALUES (UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`)
		if err != nil {
			t.Fatal("create synthetic owner:", err)
		}
		id, err := result.LastInsertId()
		if err != nil || id <= 0 {
			t.Fatal("read synthetic owner")
		}
		return uint64(id)
	}
	owner, other := newUser(), newUser()
	root := bytes.Repeat([]byte{7}, 32)
	names, err := NewExerciseNameProtector(root, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := names.InitializeExerciseKey(ctx, tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	store, err := NewPersonalExerciseStore(root, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	epochTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitializeKey(ctx, epochTx); err != nil {
		epochTx.Rollback()
		t.Fatal(err)
	}
	if err := epochTx.Commit(); err != nil {
		t.Fatal(err)
	}
	put := func(actor uint64, value domain.PersonalExercise, expected uint64) (domain.PersonalExercise, bool, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return domain.PersonalExercise{}, false, err
		}
		defer tx.Rollback()
		result, changed, err := store.PutTx(ctx, dbtx.Wrap(tx), actor, value, expected)
		if err != nil {
			return domain.PersonalExercise{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return domain.PersonalExercise{}, false, err
		}
		return result, changed, nil
	}
	read := func(actor uint64, id string) (domain.PersonalExercise, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return domain.PersonalExercise{}, err
		}
		defer tx.Rollback()
		value, err := store.ReadTx(ctx, dbtx.Wrap(tx), actor, id)
		if err != nil {
			return domain.PersonalExercise{}, err
		}
		if err := tx.Commit(); err != nil {
			return domain.PersonalExercise{}, err
		}
		return value, nil
	}
	first := domain.PersonalExercise{ID: "97ab0210c56f48ae930de1629b761f09", Name: "Café", MuscleGroups: []string{"腿部"}, Equipment: []string{"杠铃"}}
	created, changed, err := put(owner, first, 0)
	if err != nil || !changed || created.Version != 1 {
		t.Fatal("create failed", err)
	}
	replayed, changed, err := put(owner, first, 0)
	if err != nil || changed || replayed.Version != 1 {
		t.Fatal("create replay failed", err)
	}
	duplicate := domain.PersonalExercise{ID: "97ab0210c56f48ae930de1629b761f08", Name: "CAFE\u0301"}
	if _, _, err := put(owner, duplicate, 0); !errors.Is(err, ErrPersonalExerciseNameTaken) {
		t.Fatal("canonical duplicate not rejected", err)
	}
	if _, _, err := put(other, duplicate, 0); err != nil {
		t.Fatal("different owner name should be allowed", err)
	}
	if _, err := read(other, first.ID); !errors.Is(err, ErrPersonalExerciseNotFound) {
		t.Fatal("another owner could read action", err)
	}
	renamed := first
	renamed.Name = "上斜推举"
	updated, changed, err := put(owner, renamed, 1)
	if err != nil || !changed || updated.ID != first.ID || updated.Version != 2 {
		t.Fatal("rename failed", err)
	}
	replayed, changed, err = put(owner, renamed, 1)
	if err != nil || changed || replayed.Version != 2 {
		t.Fatal("update replay failed", err)
	}
	stale := renamed
	stale.Name = "其他名称"
	if _, _, err := put(owner, stale, 1); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("stale edit not rejected", err)
	}
	got, err := read(owner, first.ID)
	if err != nil || got.Name != renamed.Name || got.ID != first.ID || got.Version != 2 {
		t.Fatal("read failed", err)
	}
	readVersion := func(actor uint64, id string, version uint64) (domain.PersonalExercise, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return domain.PersonalExercise{}, err
		}
		defer tx.Rollback()
		value, err := store.ReadVersionTx(ctx, dbtx.Wrap(tx), actor, id, version)
		if err != nil {
			return domain.PersonalExercise{}, err
		}
		if err := tx.Commit(); err != nil {
			return domain.PersonalExercise{}, err
		}
		return value, nil
	}
	oldVersion, err := readVersion(owner, first.ID, 1)
	if err != nil || oldVersion.Name != first.Name || oldVersion.ID != first.ID {
		t.Fatal("rename changed the historical snapshot", err)
	}
	currentVersion, err := readVersion(owner, first.ID, 2)
	if err != nil || currentVersion.Name != renamed.Name {
		t.Fatal("current snapshot unavailable", err)
	}
	if _, err := readVersion(other, first.ID, 1); !errors.Is(err, ErrPersonalExerciseNotFound) {
		t.Fatal("other owner could read the historical snapshot", err)
	}
	if _, err := readVersion(owner, first.ID, 3); !errors.Is(err, ErrPersonalExerciseNotFound) {
		t.Fatal("nonexistent future snapshot accepted", err)
	}
	// The training store checks ownership and the frozen name itself, within
	// the same transaction as the workout write; callers cannot forge either.
	workouts, err := NewRecordingV2Store(root, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	createRecording := func(actor uint64, event string) SessionRef {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		ref, err := workouts.CreateFreeV2Tx(ctx, dbtx.Wrap(tx), actor, FreeSessionInput{Date: time.Now().UTC().Format("2006-01-02"), TimeZone: timeZone("UTC"), ClientEventID: event}, domain.RecordingAfterWorkoutV2)
		if err != nil {
			t.Fatal("create isolated workout", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return ref
	}
	ownerRecord := createRecording(owner, "personal-record-owner-001")
	otherRecord := createRecording(other, "personal-record-other-001")
	applyPersonal := func(actor, session uint64, name string, version uint64) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		exercise := domain.Exercise{Source: domain.Personal, ID: first.ID, Name: name, Version: version}
		group := domain.SetV2{RecordKey: "record-001", ExerciseEntryKey: "entry-0001", Exercise: exercise, Index: 1, Measurement: domain.MeasurementV2{Method: domain.MeasurementTotal}}
		batch := domain.RecordingBatchV2{AddEntries: []domain.EntryV2{{Key: "entry-0001", Exercise: exercise}}, Upserts: []domain.SetV2{group}, Order: []string{"entry-0001"}}
		_, _, err = workouts.ApplyBatchV2Tx(ctx, dbtx.Wrap(tx), actor, session, 1, batch)
		if err == nil {
			err = tx.Commit()
		}
		return err
	}
	if err := applyPersonal(other, otherRecord.ID, first.Name, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign personal action was accepted", err)
	}
	if err := applyPersonal(owner, ownerRecord.ID, "伪造名称", 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("forged historical name was accepted", err)
	}
	if err := applyPersonal(owner, ownerRecord.ID, first.Name, 3); !errors.Is(err, ErrInvalid) {
		t.Fatal("future action version was accepted", err)
	}
	if err := applyPersonal(owner, ownerRecord.ID, first.Name, 1); err != nil {
		t.Fatal("owned historical action rejected", err)
	}
	verifyTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workouts.ReadFreeV2Tx(ctx, dbtx.Wrap(verifyTx), owner, ownerRecord.ID)
	_ = verifyTx.Rollback()
	if err != nil || snapshot.Recording.Entries["entry-0001"] != (domain.Exercise{Source: domain.Personal, ID: first.ID, Name: first.Name, Version: 1}) {
		t.Fatal("training did not retain the owned historical action", err)
	}
	t.Log("PASS: training transaction enforces personal action ownership and immutable name")
	// A history-write failure must roll back the new current version too.
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER test_personal_snapshot_block BEFORE INSERT ON workout_personal_exercise_snapshot
		FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic snapshot failure'`); err != nil {
		t.Fatal("create isolated failure trigger:", err)
	}
	defer db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS test_personal_snapshot_block`)
	failedRename := renamed
	failedRename.Name = "不应保存"
	if _, _, err := put(owner, failedRename, 2); !errors.Is(err, ErrUnavailable) {
		t.Fatal("history insert failure was not reported", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER test_personal_snapshot_block`); err != nil {
		t.Fatal(err)
	}
	afterFailure, err := read(owner, first.ID)
	if err != nil || afterFailure.Name != renamed.Name || afterFailure.Version != 2 {
		t.Fatal("failed history insert changed current action", err)
	}
	id, _ := hex.DecodeString(first.ID)
	var cipher, digest []byte
	if err := db.QueryRowContext(ctx, `SELECT snapshot_enc, name_digest FROM workout_personal_exercise WHERE user_id = ? AND exercise_id = ?`, owner, id).Scan(&cipher, &digest); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cipher, []byte(renamed.Name)) || bytes.Contains(cipher, []byte("杠铃")) || bytes.Contains(digest, []byte(renamed.Name)) {
		t.Fatal("stored personal fields in plaintext")
	}

	// Concurrent names within one owner serialize before checking uniqueness.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"97ab0210c56f48ae930de1629b761f11", "97ab0210c56f48ae930de1629b761f12"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _, err := put(owner, domain.PersonalExercise{ID: id, Name: "平板支撑"}, 0)
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for result := range results {
		if result == nil {
			success++
		} else if errors.Is(result, ErrPersonalExerciseNameTaken) {
			conflicts++
		} else {
			t.Fatal("concurrent create:", result)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("unexpected concurrent outcomes: %d %d", success, conflicts)
	}
	list := func(actor uint64, query, cursor string, limit int) (PersonalExercisePage, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return PersonalExercisePage{}, err
		}
		defer tx.Rollback()
		page, err := store.ListTx(ctx, dbtx.Wrap(tx), actor, query, cursor, limit)
		if err != nil {
			return PersonalExercisePage{}, err
		}
		if err := tx.Commit(); err != nil {
			return PersonalExercisePage{}, err
		}
		return page, nil
	}
	page1, err := list(owner, "", "", 1)
	if err != nil || len(page1.Items) != 1 || page1.NextCursor == "" {
		t.Fatal("first library page failed", err)
	}
	page2, err := list(owner, "", page1.NextCursor, 1)
	if err != nil || len(page2.Items) != 1 || page2.Items[0].ID == page1.Items[0].ID || page2.NextCursor != "" {
		t.Fatal("second library page failed", err)
	}
	matches, err := list(owner, "推举", "", 50)
	if err != nil || len(matches.Items) != 1 || matches.Items[0].ID != first.ID {
		t.Fatal("private search failed", err)
	}
	foreign, err := list(other, "推举", "", 50)
	if err != nil || len(foreign.Items) != 0 {
		t.Fatal("search leaked another owner's action", err)
	}
	if _, err := list(owner, "", "invalid", 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid cursor accepted")
	}
	corruptOwner := newUser()
	corrupt := domain.PersonalExercise{ID: "97ab0210c56f48ae930de1629b761f20", Name: "合成动作"}
	if _, _, err := put(corruptOwner, corrupt, 0); err != nil {
		t.Fatal("create synthetic corruption fixture", err)
	}
	corruptID, _ := hex.DecodeString(corrupt.ID)
	if _, err := db.ExecContext(ctx, `DELETE FROM workout_personal_exercise_snapshot WHERE user_id = ? AND exercise_id = ?`,
		corruptOwner, corruptID); err != nil {
		t.Fatal(err)
	}
	if _, err := read(corruptOwner, corrupt.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing current snapshot was accepted", err)
	}
	if _, err := list(corruptOwner, "", "", 20); !errors.Is(err, ErrUnavailable) {
		t.Fatal("list hid missing current snapshot", err)
	}
	// More than one scan window must not make a later matching action disappear.
	manyOwner := newUser()
	seed, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 201; i++ {
		id := fmt.Sprintf("%032x", i)
		name := fmt.Sprintf("动作%03d", i)
		if i == 201 {
			name = "特殊目标"
		}
		value := domain.PersonalExercise{ID: id, Version: 1, Name: name}
		raw, _ := hex.DecodeString(id)
		digest, err := store.protector.nameHash(manyOwner, name)
		if err != nil {
			seed.Rollback()
			t.Fatal(err)
		}
		sealed, err := store.protector.seal(manyOwner, value)
		if err != nil {
			seed.Rollback()
			t.Fatal(err)
		}
		if _, err := seed.ExecContext(ctx, personalExerciseInsertSQL, manyOwner, raw, 1, []byte("test-key"), digest, sealed,
			"2026-11-16 00:00:00.000000", "2026-11-16 00:00:00.000000"); err != nil {
			seed.Rollback()
			t.Fatal("seed synthetic scan window:", err)
		}
		if _, err := seed.ExecContext(ctx, personalExerciseInsertSnapshotSQL, manyOwner, raw, 1, []byte("test-key"), digest, sealed); err != nil {
			seed.Rollback()
			t.Fatal("seed synthetic historical snapshot:", err)
		}
	}
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}
	firstWindow, err := list(manyOwner, "目标", "", 20)
	if err != nil || len(firstWindow.Items) != 0 || firstWindow.NextCursor == "" {
		t.Fatal("search lost its continuation", err)
	}
	lastWindow, err := list(manyOwner, "目标", firstWindow.NextCursor, 20)
	if err != nil || len(lastWindow.Items) != 1 || lastWindow.Items[0].Name != "特殊目标" || lastWindow.NextCursor != "" {
		t.Fatal("search skipped a later match", err)
	}
	t.Log("PASS: personal actions paginate and search without exposing another owner's entries")
	t.Log("PASS: renamed personal actions retain owner-scoped historical name snapshots")
	wrong, err := NewPersonalExerciseStore(bytes.Repeat([]byte{8}, 32), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	badTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrong.ReadTx(ctx, dbtx.Wrap(badTx), owner, first.ID)
	badTx.Rollback()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("wrong encryption key did not fail closed", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_personal_exercise_key_epoch SET key_id=NULL, key_tag=NULL WHERE slot=1`); err != nil {
		t.Fatal(err)
	}
	resetTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = store.InitializeKey(ctx, resetTx)
	resetTx.Rollback()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("reset key epoch over existing data did not fail closed", err)
	}
	t.Log("PASS: personal exercise ownership, encrypted names, duplicate names, version and replay with private MySQL")
	t.Log("PASS: existing personal actions prevent key epoch reinitialization")
}
