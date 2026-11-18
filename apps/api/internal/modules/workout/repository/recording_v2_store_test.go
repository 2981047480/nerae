package repository

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"errors"
	"github.com/go-sql-driver/mysql"
	"reflect"
	"testing"
	"time"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/workout/domain"
)

// The socket must be in a private, task-named directory inside the system
// temporary directory. This allows the same isolated test on macOS and Linux.
func privateV2DB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_WORKOUT_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_WORKOUT_TEST_DSN using the isolated MySQL harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated test DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	tmp, tmpErr := filepath.EvalSymlinks(os.TempDir())
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || tmpErr != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_workout_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != tmp ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-workout-session-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL test target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("cannot open isolated MySQL")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal("cannot connect to isolated MySQL")
	}
	return db
}

// Uses the project's isolated MySQL harness. A skipped test is not an integration pass.
func TestRecordingV2Transactions(t *testing.T) {
	db := privateV2DB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	insertUser := func() uint64 {
		result, err := db.ExecContext(ctx, `INSERT INTO user (created_at, updated_at) VALUES (UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`)
		if err != nil {
			t.Fatal("create isolated user")
		}
		id, err := result.LastInsertId()
		if err != nil || id <= 0 {
			t.Fatal("read isolated user")
		}
		return uint64(id)
	}
	owner, other := insertUser(), insertUser()
	key := bytes.Repeat([]byte{7}, 32)
	names, err := NewExerciseNameProtector(key, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := names.InitializeExerciseKey(ctx, tx); err != nil {
		t.Fatal("initialize key")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	store, err := NewRecordingV2Store(key, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	input := FreeSessionInput{Date: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), TimeZone: timeZone("UTC"), ClientEventID: "v2-record-event-001"}
	create := func(input FreeSessionInput) (SessionRef, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return SessionRef{}, err
		}
		defer tx.Rollback()
		ref, err := store.CreateFreeV2Tx(ctx, dbtx.Wrap(tx), owner, input, domain.RecordingAfterWorkoutV2)
		if err == nil {
			err = tx.Commit()
		}
		return ref, err
	}
	ref, err := create(input)
	if err != nil || ref.ID == 0 || ref.Version != 1 {
		t.Fatal("create v2 recording")
	}
	// An older endpoint cannot read a v2 record as empty or overwrite it.
	legacy, err := NewRecordStore(names)
	if err != nil {
		t.Fatal(err)
	}
	legacyTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := legacy.ReadOrderedFree(ctx, legacyTx, owner, ref.ID)
	_ = legacyTx.Rollback()
	if !errors.Is(readErr, ErrUnavailable) {
		t.Fatal("legacy reader accessed the v2 snapshot")
	}
	legacyTx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacySet := domain.Set{RecordKey: "legacy-001", ExerciseEntryKey: "entry-old1", Exercise: domain.Exercise{Source: domain.Custom, Name: "旧动作"}, Index: 1, Kind: domain.Weight, WeightKg: "45", Reps: 8}
	_, _, writeErr := legacy.ApplyOrderedBatch(ctx, legacyTx, owner, ref.ID, 1, domain.Batch{Sets: []domain.Set{legacySet}}, []string{"entry-old1"})
	_ = legacyTx.Rollback()
	if !errors.Is(writeErr, ErrUnavailable) {
		t.Fatal("legacy writer accessed the v2 snapshot")
	}
	replay, err := create(input)
	if err != nil || replay != ref {
		t.Fatal("same event must find existing recording")
	}
	mismatch := input
	mismatch.Date = time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	if _, err := create(mismatch); !errors.Is(err, ErrConflict) {
		t.Fatal("different creation request reused an event")
	}
	read := func(user uint64) (RecordingV2Snapshot, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return RecordingV2Snapshot{}, err
		}
		defer tx.Rollback()
		got, err := store.ReadFreeV2Tx(ctx, dbtx.Wrap(tx), user, ref.ID)
		if err == nil {
			err = tx.Commit()
		}
		return got, err
	}
	if _, err := read(other); !errors.Is(err, ErrUnavailable) {
		t.Fatal("different user accessed recording")
	}
	initial, err := read(owner)
	if err != nil || initial.Version != 1 || initial.Date != input.Date || initial.TimeZone != "UTC" || initial.ClientEventID != input.ClientEventID || initial.RecordedAt != nil || initial.Recording.Status != domain.InProgress || len(initial.Recording.Sets) != 0 {
		t.Fatal("new recording invalid")
	}
	squat := domain.Exercise{Source: domain.Custom, Name: "深蹲"}
	weight, reps := "45", 8
	filled := domain.SetV2{RecordKey: "record-001", ExerciseEntryKey: "entry-0001", Exercise: squat, Index: 1, Measurement: domain.MeasurementV2{Method: domain.MeasurementTotal, WeightKg: &weight, Reps: &reps}}
	blank := domain.SetV2{RecordKey: "record-002", ExerciseEntryKey: "entry-0001", Exercise: squat, Index: 2, Measurement: domain.MeasurementV2{Method: domain.MeasurementTotal}}
	apply := func(version uint64, batch domain.RecordingBatchV2) (uint64, bool, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, false, err
		}
		defer tx.Rollback()
		v, changed, err := store.ApplyBatchV2Tx(ctx, dbtx.Wrap(tx), owner, ref.ID, version, batch)
		if err == nil {
			err = tx.Commit()
		}
		return v, changed, err
	}
	batch := domain.RecordingBatchV2{AddEntries: []domain.EntryV2{{Key: "entry-0001", Exercise: squat}}, Upserts: []domain.SetV2{filled, blank}, Order: []string{"entry-0001"}}
	version, changed, err := apply(1, batch)
	if err != nil || !changed || version != 2 {
		t.Fatal("save groups")
	}
	if _, _, err := apply(1, batch); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("stale writer was accepted")
	}
	snapshot, err := read(owner)
	if err != nil || snapshot.Version != 2 || len(snapshot.Recording.Sets) != 2 || snapshot.Recording.Sets["record-002"].Measurement.WeightKg != nil {
		t.Fatal("saved empty group lost")
	}
	complete := func(version uint64) (uint64, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, err
		}
		defer tx.Rollback()
		v, err := store.CompleteV2Tx(ctx, dbtx.Wrap(tx), owner, ref.ID, version)
		if err == nil {
			err = tx.Commit()
		}
		return v, err
	}
	version, err = complete(2)
	if err != nil || version != 3 {
		t.Fatal("complete with empty group")
	}
	finished, err := read(owner)
	if err != nil || finished.RecordedAt == nil || finished.Recording.Status != domain.Completed {
		t.Fatal("completion metadata not preserved")
	}
	version, changed, err = apply(3, domain.RecordingBatchV2{RemoveEntryKeys: []string{"entry-0001"}, Order: []string{"entry-0001"}})
	if err != nil || !changed || version != 4 {
		t.Fatal("remove action")
	}
	removed, err := read(owner)
	if err != nil || removed.RecordedAt != nil || removed.Recording.Status != domain.InProgress || removed.Recording.RemovedEntries["entry-0001"] != 1 {
		t.Fatal("removed action not preserved as draft")
	}
	version, changed, err = apply(4, domain.RecordingBatchV2{RestoreEntryKeys: []string{"entry-0001"}, Order: []string{"entry-0001"}})
	if err != nil || !changed || version != 5 {
		t.Fatal("restore action")
	}
	restored, err := read(owner)
	if err != nil || len(restored.Recording.Sets) != 2 || !reflect.DeepEqual(restored.Recording.Order, []string{"entry-0001"}) {
		t.Fatal("restore lost data")
	}
	mode := domain.RecordingPerSetV2
	modeBatch := domain.RecordingBatchV2{Mode: &mode, Order: []string{"entry-0001"}}
	version, changed, err = apply(5, modeBatch)
	if err != nil || !changed || version != 6 {
		t.Fatal("switch mode")
	}
	version, changed, err = apply(6, modeBatch)
	if err != nil || changed || version != 6 {
		t.Fatal("repeated mode update must be a no-op")
	}
	modeSnapshot, err := read(owner)
	if err != nil || modeSnapshot.Recording.Mode != mode || len(modeSnapshot.Recording.Sets) != 2 {
		t.Fatal("mode switch changed group data")
	}

	// Two devices submit competing edits of the same base version. Exactly
	// one may commit; the other must retain its local draft after a conflict.
	type outcome struct {
		version uint64
		changed bool
		err     error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	for _, candidate := range []string{"40", "50"} {
		candidate := candidate
		go func() {
			local := filled
			local.Measurement.WeightKg = &candidate
			<-start
			v, c, e := apply(6, domain.RecordingBatchV2{Upserts: []domain.SetV2{local}, Order: []string{"entry-0001"}})
			results <- outcome{v, c, e}
		}()
	}
	close(start)
	first, second := <-results, <-results
	success := func(o outcome) bool { return o.err == nil && o.changed && o.version == 7 }
	conflict := func(o outcome) bool { return errors.Is(o.err, ErrVersionConflict) }
	if !((success(first) && conflict(second)) || (success(second) && conflict(first))) {
		t.Fatal("concurrent writes did not produce one winner and one conflict")
	}
	beforeFailure, err := read(owner)
	if err != nil || beforeFailure.Version != 7 {
		t.Fatal("read winning edit")
	}

	// The snapshot update happens before the session update; a failure in the
	// second statement must roll back both inside the caller's transaction.
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER nerae_v2_injected BEFORE UPDATE ON workout_session FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected rollback'`); err != nil {
		t.Fatal("install isolated failure trigger")
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS nerae_v2_injected`) })
	changedSet := filled
	replacement := "55"
	changedSet.Measurement.WeightKg = &replacement
	if _, _, err := apply(7, domain.RecordingBatchV2{Upserts: []domain.SetV2{changedSet}, Order: []string{"entry-0001"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("injected failure did not abort save")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER nerae_v2_injected`); err != nil {
		t.Fatal("remove isolated failure trigger")
	}
	afterFailure, err := read(owner)
	if err != nil || afterFailure.Version != beforeFailure.Version || !reflect.DeepEqual(afterFailure.Recording, beforeFailure.Recording) {
		t.Fatal("failed transaction changed stored recording")
	}

	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT snapshot_enc FROM workout_recording_v2 WHERE session_id = ?`, ref.ID).Scan(&raw); err != nil || bytes.Contains(raw, []byte("深蹲")) || bytes.Contains(raw, []byte("weightKg")) {
		t.Fatal("database stores plaintext or no snapshot")
	}
	corrupt := append([]byte{}, raw...)
	corrupt[len(corrupt)-1] ^= 1
	if _, err := db.ExecContext(ctx, `UPDATE workout_recording_v2 SET snapshot_enc = ? WHERE session_id = ?`, corrupt, ref.ID); err != nil {
		t.Fatal("inject isolated corruption")
	}
	if _, err := read(owner); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corrupt snapshot was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_recording_v2 SET snapshot_enc = ? WHERE session_id = ?`, raw, ref.ID); err != nil {
		t.Fatal("restore isolated fixture")
	}
}
