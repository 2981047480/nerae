package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"nerae/api/internal/idempotency"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

const workoutWriteAccessSQL = `SELECT source FROM workout_session WHERE id = ? AND user_id = ? FOR UPDATE`

// workoutWritePolicy is fixed server code. The policy itself is not authorization.
func workoutWritePolicy() idempotency.StatementPolicy {
	createReads, createWrites := repository.FreeSessionStatements()
	recordReads, recordWrites := repository.RecordStatements()
	orderReads, orderWrites := repository.OrderStatements()
	reads := append([]string{workoutWriteAccessSQL}, createReads...)
	reads = append(reads, recordReads...)
	reads = append(reads, repository.FreeDetailStatements()...)
	reads = append(reads, orderReads...)
	writes := append([]string{}, createWrites...)
	writes = append(writes, recordWrites...)
	writes = append(writes, orderWrites...)
	writes = append(writes, repository.CompletionStatements()...)
	return idempotency.StatementPolicy{Reads: reads, Writes: writes, ValidateResult: validateWorkoutResult}
}

// WorkoutExecutor only constructs idempotency storage with the workout policy.
// It installs resource authorization and limits each operation to its route and
// success status. Trusted HTTP code must still validate and bind input to the
// request bytes before passing the mutation callback.
type WorkoutExecutor struct{ store *idempotency.Store }

func NewWorkoutExecutor(sessions *session.Store, hashKey, cipherKey []byte, keyID string, ttl time.Duration) (*WorkoutExecutor, error) {
	store, err := idempotency.NewStore(sessions, hashKey, cipherKey, keyID, ttl, workoutWritePolicy())
	if err != nil {
		return nil, err
	}
	return &WorkoutExecutor{store: store}, nil
}

// InitializeKey is for deployment or isolated test setup, never an HTTP route.
func (e *WorkoutExecutor) InitializeKey(ctx context.Context, tx *sql.Tx) error {
	if e == nil || e.store == nil {
		return idempotency.ErrUnavailable
	}
	return e.store.InitializeKey(ctx, tx)
}

func expectedWorkoutStatus(mutate idempotency.Mutate, expected int) idempotency.Mutate {
	return func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		reply, err := mutate(ctx, tx, actor)
		if err != nil {
			return idempotency.Reply{}, err
		}
		if reply.Status >= 200 && reply.Status <= 299 && reply.Status != expected {
			return idempotency.Reply{}, idempotency.ErrUnavailable
		}
		return reply, nil
	}
}

// ExecuteCreation always checks the authenticated actor and reauthorizes the
// created record before commit or replay. A callback cannot choose that check.
func (e *WorkoutExecutor) ExecuteCreation(ctx context.Context, db *sql.DB, token string, req idempotency.Request, mutate idempotency.Mutate) (idempotency.Result, error) {
	if e == nil || e.store == nil || mutate == nil {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	if req.Method != "POST" || req.Path != "/me/sessions" || req.RawQuery != "" {
		return idempotency.Result{}, idempotency.ErrInvalid
	}
	return e.store.Execute(ctx, db, token, req, authorizeFreeCreation, expectedWorkoutStatus(mutate, 201))
}

// ReplayCreation returns only a live receipt for the exact create request. It
// has no mutation callback, so an absent or expired receipt cannot create data.
func (e *WorkoutExecutor) ReplayCreation(ctx context.Context, db *sql.DB, token string, req idempotency.Request) (idempotency.Result, error) {
	if e == nil || e.store == nil {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	if req.Method != "POST" || req.Path != "/me/sessions" || req.RawQuery != "" {
		return idempotency.Result{}, idempotency.ErrInvalid
	}
	return e.store.ReplayExisting(ctx, db, token, req, authorizeFreeCreation)
}

// ExecuteExisting accepts only set or finish writes for the exact target.
func (e *WorkoutExecutor) ExecuteExisting(ctx context.Context, db *sql.DB, token string, target uint64, operation string, req idempotency.Request, mutate idempotency.Mutate) (idempotency.Result, error) {
	if e == nil || e.store == nil || mutate == nil {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	if err := validateExistingWorkoutRoute(target, operation, req); err != nil {
		return idempotency.Result{}, err
	}
	return e.store.Execute(ctx, db, token, req, authorizeExistingTarget(target), expectedWorkoutStatus(mutate, 200))
}

// ReplayExisting returns only a live receipt for the exact sets or finish
// request. It shares ExecuteExisting's route and authorization constraints and
// deliberately has no mutation callback.
func (e *WorkoutExecutor) ReplayExisting(ctx context.Context, db *sql.DB, token string, target uint64, operation string, req idempotency.Request) (idempotency.Result, error) {
	if e == nil || e.store == nil {
		return idempotency.Result{}, idempotency.ErrUnavailable
	}
	if err := validateExistingWorkoutRoute(target, operation, req); err != nil {
		return idempotency.Result{}, err
	}
	return e.store.ReplayExisting(ctx, db, token, req, authorizeExistingTarget(target))
}

func validateExistingWorkoutRoute(target uint64, operation string, req idempotency.Request) error {
	if target == 0 || (operation != "sets" && operation != "finish") || req.Method != "POST" || req.RawQuery != "" ||
		req.Path != "/me/sessions/"+strconv.FormatUint(target, 10)+"/"+operation {
		return idempotency.ErrInvalid
	}
	return nil
}

func authorizeExistingTarget(target uint64) idempotency.Authorize {
	return func(ctx context.Context, tx idempotency.ReadTransaction, actor idempotency.Actor, ref string, status int) error {
		return authorizeExistingFree(ctx, tx, actor, target, ref, status)
	}
}

// WorkoutResultRef identifies the resource whose permission must be rechecked
// before an idempotent response is returned. It is not a credential.
func WorkoutResultRef(id uint64) string {
	if id == 0 {
		return ""
	}
	return "workout:" + strconv.FormatUint(id, 10)
}

// NoWorkoutResultRef is only for a deterministic creation failure that has no
// resource or sensitive response. Successful creation must use WorkoutResultRef.
const NoWorkoutResultRef = "no-workout"

func workoutFailureMessage(status int) (string, string, bool) {
	switch status {
	case 409:
		return "CONFLICT", "状态已变化，请检查当前内容", true
	case 412:
		return "VERSION_CONFLICT", "记录已更新，请先查看最新内容", true
	case 422:
		return "VALIDATION_ERROR", "填写内容不符合要求", true
	default:
		return "", "", false
	}
}

func validWorkoutRequestID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// SafeUnscopedWorkoutFailure constructs the only permitted unscoped error
// payload. requestID must be generated by the server, not supplied by a client.
func SafeUnscopedWorkoutFailure(status int, requestID string) (idempotency.Reply, error) {
	code, message, ok := workoutFailureMessage(status)
	if !ok || !validWorkoutRequestID(requestID) {
		return idempotency.Reply{}, idempotency.ErrInvalid
	}
	body, err := json.Marshal(struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId"`
	}{code, message, requestID})
	if err != nil {
		return idempotency.Reply{}, idempotency.ErrUnavailable
	}
	return idempotency.Reply{Status: status, Body: body, ResultRef: NoWorkoutResultRef}, nil
}

// Successful responses must name the same resource used for authorization.
// Every cacheable failure is limited to fixed, non-sensitive error text.
func validateWorkoutResult(status int, ref string, body []byte) error {
	if status >= 200 && status <= 299 {
		if status != 200 && status != 201 {
			return idempotency.ErrInvalid
		}
		id, ok := workoutRefID(ref)
		if !ok {
			return idempotency.ErrInvalid
		}
		var object map[string]json.RawMessage
		var responseID string
		if json.Unmarshal(body, &object) != nil || object == nil || json.Unmarshal(object["id"], &responseID) != nil || responseID != strconv.FormatUint(id, 10) {
			return idempotency.ErrInvalid
		}
		// This executor currently supports free records and their actual sets.
		// Presentation must still validate the complete OpenAPI response.
		allowed := map[string]bool{"id": true, "date": true, "timeZone": true, "status": true,
			"source": true, "version": true, "createdAt": true, "updatedAt": true,
			"recordedAt": true, "clientEventId": true, "exerciseEntryKeys": true, "sets": true}
		for key := range object {
			if !allowed[key] {
				return idempotency.ErrInvalid
			}
		}
		if rawSets, exists := object["sets"]; exists {
			var sets []map[string]json.RawMessage
			if json.Unmarshal(rawSets, &sets) != nil || sets == nil {
				return idempotency.ErrInvalid
			}
			allowedSet := map[string]bool{"recordKey": true, "exerciseEntryKey": true, "setIndex": true,
				"kind": true, "resolvedExerciseName": true, "completed": true, "exerciseName": true,
				"exerciseId": true, "weightKg": true, "reps": true, "seconds": true, "rpe": true}
			for _, item := range sets {
				if item == nil {
					return idempotency.ErrInvalid
				}
				for key := range item {
					if !allowedSet[key] {
						return idempotency.ErrInvalid
					}
				}
			}
		}
		return nil
	}
	code, message, ok := workoutFailureMessage(status)
	if !ok {
		return idempotency.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return idempotency.ErrInvalid
	}
	fields := make(map[string]string, 3)
	for decoder.More() {
		token, err := decoder.Token()
		key, isString := token.(string)
		if err != nil || !isString || (key != "code" && key != "message" && key != "requestId") {
			return idempotency.ErrInvalid
		}
		if _, exists := fields[key]; exists {
			return idempotency.ErrInvalid
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return idempotency.ErrInvalid
		}
		fields[key] = value
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(fields) != 3 || fields["code"] != code || fields["message"] != message || !validWorkoutRequestID(fields["requestId"]) {
		return idempotency.ErrInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return idempotency.ErrInvalid
	}
	return nil
}

func workoutRefID(ref string) (uint64, bool) {
	value := strings.TrimPrefix(ref, "workout:")
	if value == ref || value == "" {
		return 0, false
	}
	id, err := strconv.ParseUint(value, 10, 64)
	return id, err == nil && id != 0 && strconv.FormatUint(id, 10) == value
}

// authorizeFreeCreation checks the authenticated, age-declared actor on every
// execution and rechecks ownership for a successful saved response. status 0
// denotes the pre-mutation check; every terminal status requires a reference. It must be
// called as the actual idempotency executor's Authorize callback. A failed
// creation may use NoWorkoutResultRef only for a non-sensitive response.
func authorizeFreeCreation(ctx context.Context, tx idempotency.ReadTransaction, actor idempotency.Actor, ref string, status int) error {
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
	id, ok := workoutRefID(ref)
	if !ok {
		return idempotency.ErrUnavailable
	}
	return authorizeOwnedFree(ctx, tx, actor.SubjectID(), id)
}

// authorizeExistingFree checks actor, target and current ownership both before
// execution and before replay. ref must be empty on the initial check and match
// the exact target thereafter, including for deterministic failure responses.
func authorizeExistingFree(ctx context.Context, tx idempotency.ReadTransaction, actor idempotency.Actor, target uint64, ref string, status int) error {
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
	} else {
		if !((status >= 200 && status <= 299) || status == 409 || status == 412 || status == 422) || ref != WorkoutResultRef(target) {
			return idempotency.ErrUnavailable
		}
	}
	return authorizeOwnedFree(ctx, tx, actor.SubjectID(), target)
}

func authorizeOwnedFree(ctx context.Context, tx idempotency.ReadTransaction, subjectID, target uint64) error {
	var source string
	err := tx.QueryRowContext(ctx, workoutWriteAccessSQL, target, subjectID).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) || err == nil && source != "free" {
		return idempotency.ErrForbidden
	}
	if err != nil {
		return idempotency.ErrUnavailable
	}
	return nil
}

func validateWorkoutActor(s *Service, ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) error {
	if s == nil || s.free == nil || s.records == nil || ctx == nil || tx == nil {
		return ErrUnavailable
	}
	if actor.SubjectID() == 0 || !actor.AgeVerified() {
		return ErrAgeRequired
	}
	return nil
}

// CreateFreeForIdempotency is for the executor's mutation callback only. The
// caller must validate the request body and use WorkoutExecutor.ExecuteCreation; this
// method neither authenticates a request nor commits the supplied transaction.
func (s *Service) CreateFreeForIdempotency(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor, input repository.FreeSessionInput) (repository.OrderedSnapshot, error) {
	if err := validateWorkoutActor(s, ctx, tx, actor); err != nil {
		return repository.OrderedSnapshot{}, err
	}
	ref, err := s.free.CreateFreeTx(ctx, tx, actor.SubjectID(), input)
	if err != nil {
		return repository.OrderedSnapshot{}, err
	}
	return s.records.ReadOrderedFreeTx(ctx, tx, actor.SubjectID(), ref.ID)
}

// SaveFreeForIdempotency requires WorkoutExecutor.ExecuteExisting for the exact target in
// the same executor. It persists groups and their complete order atomically.
func (s *Service) SaveFreeForIdempotency(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor, target, version uint64, batch domain.Batch, order []string) (repository.OrderedSnapshot, error) {
	if err := validateWorkoutActor(s, ctx, tx, actor); err != nil {
		return repository.OrderedSnapshot{}, err
	}
	if _, _, err := s.records.ApplyOrderedBatchTx(ctx, tx, actor.SubjectID(), target, version, batch, order); err != nil {
		return repository.OrderedSnapshot{}, err
	}
	return s.records.ReadOrderedFreeTx(ctx, tx, actor.SubjectID(), target)
}

// FinishFreeForIdempotency confirms existing actual sets without inventing
// training start, end or duration. Optional finish fields are not implemented.
func (s *Service) FinishFreeForIdempotency(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor, target, version uint64) (repository.OrderedSnapshot, error) {
	if err := validateWorkoutActor(s, ctx, tx, actor); err != nil {
		return repository.OrderedSnapshot{}, err
	}
	if _, err := s.records.CompleteFreeTx(ctx, tx, actor.SubjectID(), target, version); err != nil {
		return repository.OrderedSnapshot{}, err
	}
	return s.records.ReadOrderedFreeTx(ctx, tx, actor.SubjectID(), target)
}
