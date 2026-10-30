package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"nerae/api/internal/dbtx"
	"nerae/api/internal/modules/workout/domain"
)

func TestOrderStatementsReturnsDefensiveCopies(t *testing.T) {
	reads, writes := OrderStatements()
	if len(reads) != 4 || len(writes) != 3 {
		t.Fatalf("statement list sizes = %d reads, %d writes", len(reads), len(writes))
	}
	for _, statement := range reads {
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "SELECT ") {
			t.Fatalf("invalid order read statement: %q", statement)
		}
	}
	for _, statement := range writes {
		verb := strings.Fields(strings.ToUpper(strings.TrimSpace(statement)))
		if len(verb) == 0 || (verb[0] != "INSERT" && verb[0] != "UPDATE" && verb[0] != "DELETE") {
			t.Fatalf("invalid order write statement: %q", statement)
		}
	}
	readFirst, writeFirst := reads[0], writes[0]
	reads[0], writes[0] = "SELECT 1", "DELETE FROM user"
	currentReads, currentWrites := OrderStatements()
	if currentReads[0] != readFirst || currentWrites[0] != writeFirst {
		t.Fatal("caller mutation changed the order SQL statements")
	}
}

func TestOrderedWorkoutTransactions(t *testing.T) {
	db := privateDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	newUser := func() uint64 {
		t.Helper()
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
	user, other := newUser(), newUser()
	protector, err := NewExerciseNameProtector(bytes.Repeat([]byte{7}, 32), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := db.BeginTx(ctx, nil)
	if err != nil || protector.InitializeExerciseKey(ctx, initial) != nil || initial.Commit() != nil {
		t.Fatal("initialize isolated key epoch")
	}
	store, _ := NewRecordStore(protector)
	create, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := NewSessionStore().CreateFree(ctx, create, user, FreeSessionInput{
		Date: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), TimeZone: timeZone("UTC"), ClientEventID: "order-event-01",
	})
	if err != nil || create.Commit() != nil {
		t.Fatal("create isolated workout")
	}
	applyTx := func(owner, version uint64, batch domain.Batch, order []string) (uint64, bool, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, false, err
		}
		defer tx.Rollback()
		v, changed, err := store.ApplyOrderedBatchTx(ctx, dbtx.Wrap(tx), owner, ref.ID, version, batch, order)
		if err != nil {
			return 0, false, err
		}
		if err := tx.Commit(); err != nil {
			return 0, false, err
		}
		return v, changed, nil
	}
	apply := func(owner, version uint64, batch domain.Batch, order []string) (uint64, bool, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return 0, false, err
		}
		defer tx.Rollback()
		v, changed, err := store.ApplyOrderedBatch(ctx, tx, owner, ref.ID, version, batch, order)
		if err != nil {
			return 0, false, err
		}
		if err = tx.Commit(); err != nil {
			return 0, false, err
		}
		return v, changed, nil
	}
	readTx := func(owner uint64) (OrderedSnapshot, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return OrderedSnapshot{}, err
		}
		defer tx.Rollback()
		value, err := store.ReadOrderedFreeTx(ctx, dbtx.Wrap(tx), owner, ref.ID)
		if err == nil {
			err = tx.Commit()
		}
		return value, err
	}
	read := func(owner uint64) (OrderedSnapshot, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return OrderedSnapshot{}, err
		}
		defer tx.Rollback()
		value, err := store.ReadOrderedFree(ctx, tx, owner, ref.ID)
		if err == nil {
			err = tx.Commit()
		}
		return value, err
	}
	if _, err := store.ReadOrderedFreeTx(ctx, nil, user, ref.ID); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil ordered read capability was not rejected")
	}
	if _, _, err := store.ApplyOrderedBatchTx(ctx, nil, user, ref.ID, 1, domain.Batch{}, []string{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil ordered write capability was not rejected")
	}
	a := domain.Set{RecordKey: "record-001", ExerciseEntryKey: "entry-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "自定义深蹲"}, Index: 1, Kind: domain.Weight, WeightKg: "42.5", Reps: 8}
	b := domain.Set{RecordKey: "record-002", ExerciseEntryKey: "entry-002", Exercise: domain.Exercise{Source: domain.Custom, Name: "自定义卧推"}, Index: 1, Kind: domain.Weight, WeightKg: "25", Reps: 10}
	empty, err := readTx(user)
	if err != nil || empty.ExerciseEntryKeys == nil || len(empty.ExerciseEntryKeys) != 0 {
		t.Fatal("new session must have an explicit empty order")
	}
	if _, err := json.Marshal(empty); err == nil {
		t.Fatal("internal ordered snapshot was serializable")
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "snapshot", empty)
	if strings.Contains(fmt.Sprintf("%+v %#v %s", empty, empty, log.String()), "entry-001") {
		t.Fatal("ordered snapshot leaked")
	}
	if v, changed, err := applyTx(user, 1, domain.Batch{Sets: []domain.Set{a, b}}, []string{"entry-002", "entry-001"}); err != nil || !changed || v != 2 {
		t.Fatal("save initial order and groups")
	}
	got, err := readTx(user)
	if err != nil || !reflect.DeepEqual(got.ExerciseEntryKeys, []string{"entry-002", "entry-001"}) || len(got.Sets) != 2 {
		t.Fatal("ordered read lost groups or order")
	}
	if _, _, err := applyTx(user, 1, domain.Batch{}, []string{"entry-002", "entry-001"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("transaction capability accepted a stale version")
	}
	if _, _, err := applyTx(user, 2, domain.Batch{}, []string{"entry-002"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("transaction capability accepted an incomplete order")
	}
	if _, err := json.Marshal(got); err == nil {
		t.Fatal("populated ordered snapshot was serializable")
	}
	log.Reset()
	slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "snapshot", got)
	for _, secret := range []string{"entry-001", "自定义深蹲", "record-001"} {
		if strings.Contains(fmt.Sprintf("%+v %#v %s", got, got, log.String()), secret) {
			t.Fatal("populated ordered snapshot leaked")
		}
	}
	aEquivalent := a
	aEquivalent.WeightKg = "42.50"
	if v, changed, err := apply(user, 2, domain.Batch{Sets: []domain.Set{aEquivalent}}, []string{"entry-002", "entry-001"}); err != nil || changed || v != 2 {
		t.Fatal("equivalent set and order must be a no-op")
	}
	if v, changed, err := apply(user, 2, domain.Batch{}, []string{"entry-001", "entry-002"}); err != nil || !changed || v != 3 {
		t.Fatal("order-only update failed")
	}
	if _, _, err := apply(user, 2, domain.Batch{}, []string{"entry-002", "entry-001"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("old version was accepted")
	}
	if _, _, err := apply(other, 3, domain.Batch{}, []string{"entry-001", "entry-002"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("different user accessed workout")
	}
	if _, _, err := apply(user, 3, domain.Batch{}, []string{"entry-001"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("incomplete target order accepted")
	}
	duplicateIndex := a
	duplicateIndex.RecordKey = "record-003"
	if _, _, err := apply(user, 3, domain.Batch{Sets: []domain.Set{duplicateIndex}}, []string{"entry-001", "entry-002"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate index within entry accepted")
	}
	if v, changed, err := apply(user, 3, domain.Batch{DeleteKeys: []string{b.RecordKey}}, []string{"entry-001"}); err != nil || !changed || v != 4 {
		t.Fatal("last group deletion did not update order")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM set_record WHERE session_id = ? AND record_key = ?`, ref.ID, []byte(b.RecordKey)).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted group values retained")
	}
	if v, changed, err := apply(user, 4, domain.Batch{Sets: []domain.Set{b}, RestoreKeys: []string{b.RecordKey}}, []string{"entry-002", "entry-001"}); err != nil || !changed || v != 5 {
		t.Fatal("restore did not restore explicit order")
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_order_insert BEFORE INSERT ON workout_exercise_order FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'isolated test failure'`); err != nil {
		t.Fatal("install failure trigger")
	}
	changedA := a
	changedA.WeightKg = "50"
	if _, _, err := apply(user, 5, domain.Batch{Sets: []domain.Set{changedA}}, []string{"entry-001", "entry-002"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("injected order failure was not returned")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_order_insert`); err != nil {
		t.Fatal("remove failure trigger")
	}
	got, err = read(user)
	if err != nil || got.Version != 5 || got.Sets[a.RecordKey].WeightKg != "42.50" || !reflect.DeepEqual(got.ExerciseEntryKeys, []string{"entry-002", "entry-001"}) {
		t.Fatal("failed transaction partially changed workout")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM workout_exercise_order WHERE session_id = ? AND exercise_entry_key = ?`, ref.ID, []byte("entry-001")); err != nil {
		t.Fatal("corrupt isolated order")
	}
	if _, err := read(user); !errors.Is(err, ErrUnavailable) {
		t.Fatal("incomplete stored order was accepted")
	}
	if _, _, err := apply(user, 5, domain.Batch{}, []string{"entry-002", "entry-001"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("corrupt prior snapshot blamed on request")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO workout_exercise_order (session_id, exercise_entry_key, position) VALUES (?, ?, 2)`, ref.ID, []byte("entry-001")); err != nil {
		t.Fatal("repair isolated fixture")
	}

	// Both stale-version writers must reach the actual user-row lock before release.
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var locked uint64
	if err := gate.QueryRowContext(ctx, `SELECT id FROM user WHERE id = ? FOR UPDATE`, user).Scan(&locked); err != nil {
		_ = gate.Rollback()
		t.Fatal("lock test user")
	}
	type outcome struct {
		version uint64
		err     error
	}
	results := make(chan outcome, 2)
	for _, weight := range []string{"50", "55"} {
		go func(weight string) {
			set := a
			set.WeightKg = weight
			v, _, err := apply(user, 5, domain.Batch{Sets: []domain.Set{set}}, []string{"entry-001", "entry-002"})
			results <- outcome{v, err}
		}(weight)
	}
	deadline, blocked := time.Now().Add(5*time.Second), 0
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
			FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
			WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'user'`).Scan(&blocked); err != nil {
			_ = gate.Rollback()
			t.Fatal("observe lock waits")
		}
		if blocked >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blocked < 2 {
		_ = gate.Rollback()
		t.Fatal("writers did not both reach the lock")
	}
	if err := gate.Rollback(); err != nil {
		t.Fatal("release test gate")
	}
	x, y := <-results, <-results
	if !((x.err == nil && x.version == 6 && errors.Is(y.err, ErrVersionConflict)) || (y.err == nil && y.version == 6 && errors.Is(x.err, ErrVersionConflict))) {
		t.Fatal("concurrent stale writes did not produce one winner")
	}
	got, err = read(user)
	if err != nil || got.Version != 6 || !reflect.DeepEqual(got.ExerciseEntryKeys, []string{"entry-001", "entry-002"}) {
		t.Fatal("concurrent result lost atomic order")
	}
}
