package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestInternalResultsAreNotLoggedOrSerialized(t *testing.T) {
	input := FreeSessionInput{"2026-10-29", timeZone("Asia/Shanghai"), "private-event"}
	ref := SessionRef{ID: 987654321, Version: 4321}
	for _, value := range []any{input, &input, ref, &ref} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("internal workout value was JSON-serializable")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "workout", value)
		var logEntry map[string]any
		if err := json.Unmarshal(log.Bytes(), &logEntry); err != nil {
			t.Fatal("could not decode test log")
		}
		output := fmt.Sprintf("%+v %#v %v", value, value, logEntry["workout"])
		for _, secret := range []string{"2026-10-29", "Asia/Shanghai", "private-event", "987654321", "4321"} {
			if strings.Contains(output, secret) {
				t.Fatal("internal workout value leaked into output")
			}
		}
	}
}

func timeZone(value string) *string { return &value }

func TestValidateFreeInput(t *testing.T) {
	now := time.Date(2026, 10, 29, 16, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   FreeSessionInput
		ok   bool
	}{
		{"UTC today", FreeSessionInput{"2026-10-29", timeZone("UTC"), "event-001"}, true},
		{"local tomorrow", FreeSessionInput{"2026-10-30", timeZone("Asia/Shanghai"), "event-001"}, true},
		{"default timezone", FreeSessionInput{"2026-10-30", nil, "event-001"}, true},
		{"explicit empty timezone", FreeSessionInput{"2026-10-29", timeZone(""), "event-001"}, false},
		{"future in UTC", FreeSessionInput{"2026-10-30", timeZone("UTC"), "event-001"}, false},
		{"invalid date", FreeSessionInput{"2026-02-30", timeZone("UTC"), "event-001"}, false},
		{"outside MySQL date range", FreeSessionInput{"0999-01-01", timeZone("UTC"), "event-001"}, false},
		{"invalid timezone", FreeSessionInput{"2026-10-29", timeZone("Mars/Olympus"), "event-001"}, false},
		{"host local timezone", FreeSessionInput{"2026-10-29", timeZone("Local"), "event-001"}, false},
		{"short event", FreeSessionInput{"2026-10-29", timeZone("UTC"), "event"}, false},
		{"long event", FreeSessionInput{"2026-10-29", timeZone("UTC"), strings.Repeat("x", 65)}, false},
		{"invalid UTF-8", FreeSessionInput{"2026-10-29", timeZone("UTC"), "event-00\xff"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateFreeInput(tt.in, now)
			if ok := err == nil; ok != tt.ok {
				t.Fatalf("validity = %v, want %v", ok, tt.ok)
			}
		})
	}
}

func privateDB(t *testing.T) *sql.DB {
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
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_workout_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != "/private/tmp" ||
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

func TestFreeSessionTransactions(t *testing.T) {
	db := privateDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO user (created_at, updated_at) VALUES (UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))`); err != nil {
			t.Fatal("cannot create isolated test user")
		}
	}
	store := NewSessionStore()
	create := func(user uint64, input FreeSessionInput, commit bool) (SessionRef, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return SessionRef{}, err
		}
		defer tx.Rollback()
		ref, err := store.CreateFree(ctx, tx, user, input)
		if err != nil {
			return SessionRef{}, err
		}
		if commit {
			err = tx.Commit()
		}
		return ref, err
	}
	date := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	in := FreeSessionInput{date, timeZone("UTC"), "event-original"}
	first, err := create(1, in, true)
	if err != nil || first.ID == 0 || first.Version != 1 {
		t.Fatal("could not create a free workout")
	}
	replayed, err := create(1, in, true)
	if err != nil || replayed != first {
		t.Fatal("same subject and event did not return the existing workout")
	}
	var started, recorded sql.NullTime
	var createdAt, updatedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT started_at, recorded_at, created_at, updated_at FROM workout_session WHERE id = ?`, first.ID).
		Scan(&started, &recorded, &createdAt, &updatedAt); err != nil || started.Valid || recorded.Valid || !createdAt.Equal(updatedAt) {
		t.Fatal("creation or replay invented a time or changed updated_at")
	}
	changed := in
	changed.TimeZone = timeZone("Asia/Shanghai")
	if _, err := create(1, changed, true); !errors.Is(err, ErrConflict) {
		t.Fatal("same event with a different creation request was accepted")
	}
	otherUser, err := create(2, in, true)
	if err != nil || otherUser.ID == first.ID {
		t.Fatal("event identifier was incorrectly shared between subjects")
	}
	withDefault := FreeSessionInput{date, nil, "event-default-zone"}
	defaultRef, err := create(1, withDefault, true)
	if err != nil {
		t.Fatal("omitted timezone was not accepted")
	}
	var storedZone string
	if err := db.QueryRowContext(ctx, `SELECT time_zone FROM workout_session WHERE id = ?`, defaultRef.ID).Scan(&storedZone); err != nil || storedZone != "Asia/Shanghai" {
		t.Fatal("default timezone was not frozen")
	}
	withDefault.TimeZone = timeZone("Asia/Shanghai")
	if explicit, err := create(1, withDefault, true); err != nil || explicit != defaultRef {
		t.Fatal("explicit default timezone did not recover the same event")
	}
	caseVariant := in
	caseVariant.ClientEventID = "EVENT-original"
	caseRef, err := create(1, caseVariant, true)
	if err != nil || caseRef.ID == first.ID {
		t.Fatal("case-sensitive event identifiers collided")
	}
	rolledBack := in
	rolledBack.ClientEventID = "event-rollback"
	if _, err := create(1, rolledBack, false); err != nil {
		t.Fatal("could not create rollback fixture")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_session WHERE user_id = 1 AND client_event_id = ?`, []byte(rolledBack.ClientEventID)).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled-back workout remained in the database")
	}
	if _, err := create(1, rolledBack, true); err != nil {
		t.Fatal("retry after rollback failed")
	}
	if _, err := create(123456, in, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("nonexistent subject was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'disabled' WHERE id = 2`); err != nil {
		t.Fatal("could not disable test subject")
	}
	if _, err := create(2, in, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("disabled subject was accepted")
	}

	// Hold the subject lock until both writers are observed waiting on it.
	// Rolling back the fixture lets the two requests contend for creation.
	type outcome struct {
		ref SessionRef
		err error
	}
	runRace := func(a, b FreeSessionInput) []outcome {
		t.Helper()
		gate, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin test gate")
		}
		if _, err := store.CreateFree(ctx, gate, 1, a); err != nil {
			_ = gate.Rollback()
			t.Fatal("could not create uncommitted test event")
		}
		results := make(chan outcome, 2)
		for _, input := range []FreeSessionInput{a, b} {
			go func(input FreeSessionInput) {
				ref, err := create(1, input, true)
				results <- outcome{ref, err}
			}(input)
		}
		deadline := time.Now().Add(5 * time.Second)
		blocked := 0
		for time.Now().Before(deadline) {
			err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT w.REQUESTING_ENGINE_TRANSACTION_ID)
                FROM performance_schema.data_lock_waits w
                JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID
                WHERE l.OBJECT_SCHEMA = DATABASE() AND l.OBJECT_NAME = 'user'`).Scan(&blocked)
			if err != nil {
				_ = gate.Rollback()
				t.Fatal("could not observe isolated database lock waits")
			}
			if blocked >= 2 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if blocked < 2 {
			_ = gate.Rollback()
			t.Fatal("both writers did not reach the controlled database lock")
		}
		if err := gate.Rollback(); err != nil {
			t.Fatal("could not release test gate")
		}
		return []outcome{<-results, <-results}
	}
	race := in
	race.ClientEventID = "event-concurrent"
	same := runRace(race, race)
	if same[0].err != nil || same[1].err != nil || same[0].ref.ID == 0 || same[0].ref.ID != same[1].ref.ID {
		t.Fatalf("same event did not converge: %v / %v", same[0].err, same[1].err)
	}
	race.ClientEventID = "event-conflicting"
	different := race
	different.TimeZone = timeZone("Asia/Shanghai")
	conflicting := runRace(race, different)
	if !((conflicting[0].err == nil && errors.Is(conflicting[1].err, ErrConflict)) ||
		(conflicting[1].err == nil && errors.Is(conflicting[0].err, ErrConflict))) {
		t.Fatalf("different requests did not produce one conflict: %v / %v", conflicting[0].err, conflicting[1].err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_session WHERE user_id = 1 AND client_event_id = ?`, []byte(race.ClientEventID)).Scan(&count); err != nil || count != 1 {
		t.Fatal("conflicting event created multiple workouts")
	}
}
