package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/dbtx"
	"nerae/api/internal/idempotency"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

func recordingV2IntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_RECORDING_V2_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_RECORDING_V2_TEST_DSN using an isolated MySQL harness")
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
	sockInfo, sockErr := os.Lstat(socket)
	if err != nil || tmpErr != nil || statErr != nil || sockErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_recording_v2_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(socket) != "mysql.sock" ||
		filepath.Dir(resolved) != tmp || !strings.HasPrefix(filepath.Base(resolved), "nerae-workout-session-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || sockInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("cannot open isolated database")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if db.Ping() != nil {
		t.Fatal("cannot connect to isolated database")
	}
	return db
}

func TestRecordingV2AuthenticatedIdempotency(t *testing.T) {
	db := recordingV2IntegrationDB(t)
	f := newWorkoutIdempotencyFixtureOnDB(t, db)
	executor, err := NewRecordingV2Executor(f.auth, bytes.Repeat([]byte{0x77}, 32), bytes.Repeat([]byte{0x88}, 32), "a04-08-idempotency-test-v1", 30*time.Minute)
	if err != nil {
		t.Fatal("initialize v2 executor")
	}
	store, err := repository.NewRecordingV2Store(bytes.Repeat([]byte{0x66}, 32), "a04-08-workout-test")
	if err != nil {
		t.Fatal("initialize v2 store")
	}
	date := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	zone := "UTC"
	input := repository.FreeSessionInput{Date: date, TimeZone: &zone, ClientEventID: "v2-idempotency-event-001"}
	body, _ := json.Marshal(map[string]any{"source": "free", "date": date, "timeZone": zone, "clientEventId": input.ClientEventID, "mode": "after_workout"})
	req := idempotency.Request{Method: "POST", Path: recordingV2CreatePath, Key: "v2-create-key-001", Body: body}
	var calls atomic.Int32
	create := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		calls.Add(1)
		ref, err := store.CreateFreeV2Tx(ctx, tx, actor.SubjectID(), input, domain.RecordingAfterWorkoutV2)
		if err != nil {
			return idempotency.Reply{}, err
		}
		reply := []byte(fmt.Sprintf(`{"id":"%d","version":%d}`, ref.ID, ref.Version))
		return idempotency.Reply{Status: 201, Body: reply, ResultRef: WorkoutResultRef(ref.ID)}, nil
	}
	first, err := executor.ExecuteCreation(f.ctx, db, f.tokenA, req, create)
	if err != nil || first.Status != 201 || first.Replayed || calls.Load() != 1 {
		t.Fatal("create authenticated v2 record")
	}
	var receipt struct {
		ID      string `json:"id"`
		Version uint64 `json:"version"`
	}
	if json.Unmarshal(first.Body, &receipt) != nil || receipt.Version != 1 {
		t.Fatal("invalid creation receipt")
	}
	target, ok := workoutRefID(first.ResultRef)
	if !ok || fmt.Sprint(target) != receipt.ID {
		t.Fatal("wrong resource binding")
	}
	replay, err := executor.ReplayCreation(f.ctx, db, f.tokenA, req)
	if err != nil || !replay.Replayed || !bytes.Equal(first.Body, replay.Body) || calls.Load() != 1 {
		t.Fatal("creation replay failed")
	}
	altered := req
	altered.Body = []byte(`{"different":true}`)
	if _, err := executor.ExecuteCreation(f.ctx, db, f.tokenA, altered, create); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatal("same key with different body accepted")
	}
	var count int
	if db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM workout_session WHERE client_event_id = ?`, []byte(input.ClientEventID)).Scan(&count) != nil || count != 1 {
		t.Fatal("idempotent create duplicated session")
	}

	exercise := domain.Exercise{Source: domain.Custom, Name: "深蹲"}
	weight, reps := "45", 8
	set := domain.SetV2{RecordKey: "record-001", ExerciseEntryKey: "entry-0001", Exercise: exercise, Index: 1, Measurement: domain.MeasurementV2{Method: domain.MeasurementTotal, WeightKg: &weight, Reps: &reps}}
	blank := domain.SetV2{RecordKey: "record-002", ExerciseEntryKey: "entry-0001", Exercise: exercise, Index: 2, Measurement: domain.MeasurementV2{Method: domain.MeasurementTotal}}
	batch := domain.RecordingBatchV2{AddEntries: []domain.EntryV2{{Key: "entry-0001", Exercise: exercise}}, Upserts: []domain.SetV2{set, blank}, Order: []string{"entry-0001"}}
	writeReq := idempotency.Request{Method: "POST", Path: fmt.Sprintf("%s/%d/sets", recordingV2CreatePath, target), Key: "v2-sets-key-001", Body: []byte(`{"expectedVersion":1}`)}
	var writeCalls atomic.Int32
	write := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		writeCalls.Add(1)
		v, _, err := store.ApplyBatchV2Tx(ctx, tx, actor.SubjectID(), target, 1, batch)
		if err != nil {
			return idempotency.Reply{}, err
		}
		return idempotency.Reply{Status: 200, Body: []byte(fmt.Sprintf(`{"id":"%d","version":%d}`, target, v)), ResultRef: WorkoutResultRef(target)}, nil
	}
	saved, err := executor.ExecuteExisting(f.ctx, db, f.tokenA, target, "sets", writeReq, write)
	if err != nil || saved.Status != 200 || writeCalls.Load() != 1 {
		t.Fatal("authenticated v2 save")
	}
	repeated, err := executor.ExecuteExisting(f.ctx, db, f.tokenA, target, "sets", writeReq, write)
	if err != nil || !repeated.Replayed || !bytes.Equal(saved.Body, repeated.Body) || writeCalls.Load() != 1 {
		t.Fatal("save replay duplicated mutation")
	}
	if _, err := executor.ExecuteExisting(f.ctx, db, f.tokenB, target, "sets", writeReq, write); !errors.Is(err, idempotency.ErrForbidden) {
		t.Fatal("other subject accessed v2 record")
	}
	oldReq := writeReq
	oldReq.Path = fmt.Sprintf("/me/sessions/%d/sets", target)
	oldReq.Key = "legacy-write-key"
	if _, err := f.executor.ExecuteExisting(f.ctx, db, f.tokenA, target, "sets", oldReq, write); !errors.Is(err, idempotency.ErrUpgradeRequired) {
		t.Fatal("legacy idempotency path did not request an upgrade")
	}
	tx, err := db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ReadFreeV2Tx(f.ctx, dbtx.Wrap(tx), targetOwner(t, f.ctx, db, target), target)
	_ = tx.Rollback()
	if err != nil || snapshot.Version != 2 || len(snapshot.Recording.Sets) != 2 || snapshot.Recording.Sets["record-002"].Measurement.WeightKg != nil {
		t.Fatal("replayed save changed or lost data")
	}
	service, err := NewRecordingV2Service(f.auth, store)
	if err != nil {
		t.Fatal("initialize read service")
	}
	ownerSnapshot, err := service.ReadOwn(f.ctx, db, f.tokenA, target)
	if err != nil || ownerSnapshot.Version != 2 || len(ownerSnapshot.Recording.Sets) != 2 {
		t.Fatal("owner cannot read saved draft")
	}
	if _, err := service.ReadOwn(f.ctx, db, f.tokenB, target); !errors.Is(err, ErrWorkoutNotFound) {
		t.Fatal("different account read private draft")
	}
	if _, err := service.ReadOwn(f.ctx, db, "invalid", target); !errors.Is(err, ErrAuthentication) {
		t.Fatal("invalid session read private draft")
	}
	// Simulate a short-lived receipt being purged. A committed operation key
	// must not execute again, even with another body or a different v2 route.
	owner := targetOwner(t, f.ctx, db, target)
	if _, err := db.ExecContext(f.ctx, `DELETE FROM transport_idempotency_record WHERE actor_id = ? AND request_key = ?`, owner, []byte(writeReq.Key)); err != nil {
		t.Fatal("purge synthetic receipt")
	}
	if _, err := executor.ExecuteExisting(f.ctx, db, f.tokenA, target, "sets", writeReq, write); !errors.Is(err, idempotency.ErrConflict) || writeCalls.Load() != 1 {
		t.Fatal("committed set key was executed again after receipt expiry")
	}
	changed := writeReq
	changed.Body = []byte(`{"expectedVersion":2}`)
	if _, err := executor.ExecuteExisting(f.ctx, db, f.tokenA, target, "sets", changed, write); !errors.Is(err, idempotency.ErrConflict) || writeCalls.Load() != 1 {
		t.Fatal("committed key was accepted with changed body after expiry")
	}
	changed.Path = fmt.Sprintf("%s/%d/finish", recordingV2CreatePath, target)
	if _, err := executor.ExecuteExisting(f.ctx, db, f.tokenA, target, "finish", changed, write); !errors.Is(err, idempotency.ErrConflict) || writeCalls.Load() != 1 {
		t.Fatal("committed key was accepted on another v2 route after expiry")
	}
	transient := writeReq
	transient.Key = "v2-transient-key-001"
	if _, err := executor.ExecuteExisting(f.ctx, db, f.tokenA, target, "sets", transient,
		func(context.Context, idempotency.Transaction, idempotency.Actor) (idempotency.Reply, error) {
			return idempotency.Reply{}, idempotency.ErrUnavailable
		}); !errors.Is(err, idempotency.ErrUnavailable) {
		t.Fatal("transient failure was not returned")
	}
	if _, err := executor.ExecuteExisting(f.ctx, db, f.tokenA, target, "sets", transient,
		func(context.Context, idempotency.Transaction, idempotency.Actor) (idempotency.Reply, error) {
			return idempotency.Reply{Status: 200, Body: []byte(fmt.Sprintf(`{"id":"%d","version":2}`, target)), ResultRef: WorkoutResultRef(target)}, nil
		}); err != nil {
		t.Fatal("rolled-back key cannot be retried")
	}
	if _, err := f.service.ReadVisibleOrderedFree(f.ctx, db, f.tokenA, target); !errors.Is(err, repository.ErrClientUpgradeRequired) {
		t.Fatal("legacy detail did not request upgrade")
	}
	if _, err := f.service.ListFree(f.ctx, db, f.tokenA, repository.ListFilter{Page: 1, PageSize: 10}); !errors.Is(err, repository.ErrClientUpgradeRequired) {
		t.Fatal("legacy list silently included unsupported records")
	}
	oldInput := repository.FreeSessionInput{Date: date, TimeZone: &zone, ClientEventID: "legacy-list-event-001"}
	oldRef, err := f.service.CreateFree(f.ctx, db, f.tokenA, oldInput)
	if err != nil {
		t.Fatal("create synthetic legacy record")
	}
	page, err := service.ListOwn(f.ctx, db, f.tokenA, repository.ListFilter{Page: 1, PageSize: 10})
	if err != nil || page.Total != 2 || len(page.Items) != 2 {
		t.Fatal("mixed-format list is incomplete")
	}
	formats := map[uint64]uint8{}
	for _, item := range page.Items {
		formats[item.ID] = item.RecordFormat
	}
	if formats[oldRef.ID] != 1 || formats[target] != 2 {
		t.Fatal("record formats were not identified")
	}
	otherPage, err := service.ListOwn(f.ctx, db, f.tokenB, repository.ListFilter{Page: 1, PageSize: 10})
	if err != nil || otherPage.Total != 0 {
		t.Fatal("other account saw private records")
	}
}
func targetOwner(t *testing.T, ctx context.Context, db *sql.DB, target uint64) uint64 {
	t.Helper()
	var owner uint64
	if db.QueryRowContext(ctx, `SELECT user_id FROM workout_session WHERE id = ?`, target).Scan(&owner) != nil || owner == 0 {
		t.Fatal("cannot read synthetic target owner")
	}
	return owner
}
