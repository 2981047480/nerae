package application

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/idempotency"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/repository"
)

const recordingV2AccessSQL = `SELECT source FROM workout_session WHERE id = ? AND user_id = ? AND record_format = 2 FOR UPDATE`
const recordingV2FenceInsertSQL = `INSERT INTO workout_recording_v2_fence (user_id, key_hash) VALUES (?, ?)`
const maxRecordingV2WireVersion = uint64(9007199254740991)

func recordingV2Policy() idempotency.StatementPolicy {
	reads, writes := repository.RecordingV2Statements()
	reads = append(reads, recordingV2AccessSQL)
	writes = append(writes, recordingV2FenceInsertSQL)
	return idempotency.StatementPolicy{Reads: reads, Writes: writes, ValidateResult: validateRecordingV2Result}
}

// RecordingV2Executor does not register HTTP routes. Every callback remains
// bound to the authenticated actor and an allowlisted SQL transaction.
type RecordingV2Executor struct {
	store    *idempotency.Store
	fenceKey [32]byte
}

func (RecordingV2Executor) String() string               { return "RecordingV2Executor{redacted}" }
func (RecordingV2Executor) GoString() string             { return "RecordingV2Executor{redacted}" }
func (RecordingV2Executor) MarshalJSON() ([]byte, error) { return nil, idempotency.ErrUnavailable }
func (RecordingV2Executor) LogValue() slog.Value {
	return slog.StringValue("RecordingV2Executor{redacted}")
}

func NewRecordingV2Executor(sessions *session.Store, hashKey, cipherKey []byte, keyID string, ttl time.Duration) (*RecordingV2Executor, error) {
	store, err := idempotency.NewStore(sessions, hashKey, cipherKey, keyID, ttl, recordingV2Policy())
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, hashKey)
	_, _ = mac.Write([]byte("nerae/workout/v2-operation-fence-key/v1\x00" + keyID))
	result := &RecordingV2Executor{store: store}
	copy(result.fenceKey[:], mac.Sum(nil))
	return result, nil
}

// The transport receipt is deliberately short-lived. This separate, permanent
// digest is committed in the same transaction as a successful mutation, so an
// old key cannot execute a second operation after the receipt has expired.
func (e *RecordingV2Executor) fenced(key string, mutate idempotency.Mutate) idempotency.Mutate {
	return func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		if e == nil || actor.SubjectID() == 0 || key == "" || mutate == nil {
			return idempotency.Reply{}, idempotency.ErrUnavailable
		}
		mac := hmac.New(sha256.New, e.fenceKey[:])
		_, _ = mac.Write([]byte("nerae/workout/v2-operation-key/v1\x00"))
		var subject [8]byte
		binary.BigEndian.PutUint64(subject[:], actor.SubjectID())
		_, _ = mac.Write(subject[:])
		_, _ = mac.Write([]byte(key))
		if _, err := tx.ExecContext(ctx, recordingV2FenceInsertSQL, actor.SubjectID(), mac.Sum(nil)); err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return idempotency.Reply{}, idempotency.ErrConflict
			}
			return idempotency.Reply{}, idempotency.ErrUnavailable
		}
		return mutate(ctx, tx, actor)
	}
}
func (e *RecordingV2Executor) InitializeKey(ctx context.Context, tx *sql.Tx) error {
	if e == nil || e.store == nil {
		return idempotency.ErrUnavailable
	}
	if err := e.store.InitializeKey(ctx, tx); err != nil {
		return err
	}
	// A missing migration or missing runtime SELECT privilege must fail startup,
	// not leave an apparently healthy API unable to protect v2 writes.
	var unused []byte
	if err := tx.QueryRowContext(ctx, `SELECT key_hash FROM workout_recording_v2_fence WHERE 1 = 0`).Scan(&unused); !errors.Is(err, sql.ErrNoRows) {
		return idempotency.ErrUnavailable
	}
	return nil
}

const recordingV2CreatePath = "/me/recordings"

func (e *RecordingV2Executor) ExecuteCreation(ctx context.Context, db *sql.DB, token string, req idempotency.Request, mutate idempotency.Mutate) (idempotency.Result, error) {
	if e == nil || e.store == nil || mutate == nil {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	if req.Method != "POST" || req.Path != recordingV2CreatePath || req.RawQuery != "" {
		return idempotency.Result{}, idempotency.ErrInvalid
	}
	return e.store.Execute(ctx, db, token, req, authorizeRecordingV2Creation, e.fenced(req.Key, expectedWorkoutStatus(mutate, 201)))
}
func (e *RecordingV2Executor) ReplayCreation(ctx context.Context, db *sql.DB, token string, req idempotency.Request) (idempotency.Result, error) {
	if e == nil || e.store == nil {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	if req.Method != "POST" || req.Path != recordingV2CreatePath || req.RawQuery != "" {
		return idempotency.Result{}, idempotency.ErrInvalid
	}
	return e.store.ReplayExisting(ctx, db, token, req, authorizeRecordingV2Creation)
}
func validateRecordingV2Route(target uint64, operation string, req idempotency.Request) error {
	if target == 0 || (operation != "sets" && operation != "finish") || req.Method != "POST" || req.RawQuery != "" || req.Path != recordingV2CreatePath+"/"+strconv.FormatUint(target, 10)+"/"+operation {
		return idempotency.ErrInvalid
	}
	return nil
}
func (e *RecordingV2Executor) ExecuteExisting(ctx context.Context, db *sql.DB, token string, target uint64, operation string, req idempotency.Request, mutate idempotency.Mutate) (idempotency.Result, error) {
	if e == nil || e.store == nil || mutate == nil {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	if err := validateRecordingV2Route(target, operation, req); err != nil {
		return idempotency.Result{}, err
	}
	return e.store.Execute(ctx, db, token, req, authorizeRecordingV2Target(target), e.fenced(req.Key, expectedWorkoutStatus(mutate, 200)))
}
func (e *RecordingV2Executor) ReplayExisting(ctx context.Context, db *sql.DB, token string, target uint64, operation string, req idempotency.Request) (idempotency.Result, error) {
	if e == nil || e.store == nil {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	if err := validateRecordingV2Route(target, operation, req); err != nil {
		return idempotency.Result{}, err
	}
	return e.store.ReplayExisting(ctx, db, token, req, authorizeRecordingV2Target(target))
}

func authorizeRecordingV2Owned(ctx context.Context, tx idempotency.ReadTransaction, subject, target uint64) error {
	var source string
	err := tx.QueryRowContext(ctx, recordingV2AccessSQL, target, subject).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) || err == nil && source != "free" {
		return idempotency.ErrForbidden
	}
	if err != nil {
		return idempotency.ErrUnavailable
	}
	return nil
}
func authorizeRecordingV2Creation(ctx context.Context, tx idempotency.ReadTransaction, actor idempotency.Actor, ref string, status int) error {
	if ctx == nil || tx == nil {
		return idempotency.ErrUnavailable
	}
	if actor.SubjectID() == 0 || !actor.AgeVerified() {
		return idempotency.ErrForbidden
	}
	if status == 0 {
		if ref == "" {
			return nil
		}
		return idempotency.ErrUnavailable
	}
	if status == 409 || status == 412 || status == 422 {
		if ref == NoWorkoutResultRef {
			return nil
		}
	} else if status < 200 || status > 299 {
		return idempotency.ErrUnavailable
	}
	target, ok := workoutRefID(ref)
	if !ok {
		return idempotency.ErrUnavailable
	}
	return authorizeRecordingV2Owned(ctx, tx, actor.SubjectID(), target)
}
func authorizeRecordingV2Target(target uint64) idempotency.Authorize {
	return func(ctx context.Context, tx idempotency.ReadTransaction, actor idempotency.Actor, ref string, status int) error {
		if ctx == nil || tx == nil || target == 0 {
			return idempotency.ErrUnavailable
		}
		if actor.SubjectID() == 0 || !actor.AgeVerified() {
			return idempotency.ErrForbidden
		}
		if status == 0 {
			if ref != "" {
				return idempotency.ErrUnavailable
			}
		} else if !((status >= 200 && status <= 299) || status == 409 || status == 412 || status == 422) || ref != WorkoutResultRef(target) {
			return idempotency.ErrUnavailable
		}
		return authorizeRecordingV2Owned(ctx, tx, actor.SubjectID(), target)
	}
}

// Successful receipts contain only the bound id and committed version. The
// caller fetches current details separately; cached responses never stand in
// for later edits and never include body metrics.
func validateRecordingV2Result(status int, ref string, body []byte) error {
	if status < 200 || status > 299 {
		return validateWorkoutResult(status, ref, body)
	}
	if status != 200 && status != 201 {
		return idempotency.ErrInvalid
	}
	target, ok := workoutRefID(ref)
	if !ok {
		return idempotency.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return idempotency.ErrInvalid
	}
	var id string
	var version uint64
	seen := map[string]bool{}
	for decoder.More() {
		raw, err := decoder.Token()
		key, ok := raw.(string)
		if err != nil || !ok || seen[key] {
			return idempotency.ErrInvalid
		}
		seen[key] = true
		switch key {
		case "id":
			if decoder.Decode(&id) != nil {
				return idempotency.ErrInvalid
			}
		case "version":
			var number json.Number
			if decoder.Decode(&number) != nil {
				return idempotency.ErrInvalid
			}
			version, err = strconv.ParseUint(number.String(), 10, 64)
			if err != nil {
				return idempotency.ErrInvalid
			}
		default:
			return idempotency.ErrInvalid
		}
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(seen) != 2 || id != strconv.FormatUint(target, 10) || version == 0 || version > maxRecordingV2WireVersion {
		return idempotency.ErrInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return idempotency.ErrInvalid
	}
	return nil
}
