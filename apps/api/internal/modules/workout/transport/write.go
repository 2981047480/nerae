// Package transport contains explicit HTTP projections for workout operations.
// Deployment, public ingress and release review remain separate concerns.
package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nerae/api/internal/idempotency"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/workout/application"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

type writeHandler struct {
	db        *sql.DB
	service   *application.Service
	validator *middleware.SchemaValidator
	executor  *application.WorkoutExecutor
}

// NewWriteHandler constructs the routes; its caller controls mounting and
// network access. It does not provide request rate limiting.
func NewWriteHandler(db *sql.DB, service *application.Service, validator *middleware.SchemaValidator, executor *application.WorkoutExecutor) (http.Handler, error) {
	if db == nil || service == nil || validator == nil || executor == nil {
		return nil, middleware.ErrContract
	}
	h := &writeHandler{db: db, service: service, validator: validator, executor: executor}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /me/sessions", h.create)
	mux.HandleFunc("POST /me/sessions/replay", h.replayCreate)
	mux.HandleFunc("POST /me/sessions/{id}/sets", h.updateSets)
	mux.HandleFunc("POST /me/sessions/{id}/sets/replay", h.replaySets)
	mux.HandleFunc("POST /me/sessions/{id}/finish", h.finish)
	mux.HandleFunc("POST /me/sessions/{id}/finish/replay", h.replayFinish)
	return mux, nil
}

func (h *writeHandler) fail(w http.ResponseWriter, id middleware.RequestID, kind middleware.ErrorKind) {
	if err := middleware.WriteError(w, h.validator, id, kind); err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func newWriteRequestID(w http.ResponseWriter) (middleware.RequestID, bool) {
	id, err := middleware.NewRequestID()
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
		return middleware.RequestID{}, false
	}
	return id, true
}

func writeFailureKind(err error) middleware.ErrorKind {
	switch {
	case errors.Is(err, middleware.ErrInvalidBearer), errors.Is(err, idempotency.ErrAuthentication):
		return middleware.Unauthenticated
	case errors.Is(err, idempotency.ErrForbidden), errors.Is(err, application.ErrAgeRequired):
		return middleware.Forbidden
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress), errors.Is(err, repository.ErrSetConflict), errors.Is(err, repository.ErrConflict):
		return middleware.Conflict
	case errors.Is(err, repository.ErrVersionConflict):
		return middleware.VersionConflict
	case errors.Is(err, idempotency.ErrInvalid), errors.Is(err, middleware.ErrInvalidRequest), errors.Is(err, repository.ErrInvalid), errors.Is(err, domain.ErrInvalid):
		return middleware.ValidationError
	case errors.Is(err, idempotency.ErrReplayUnavailable), errors.Is(err, idempotency.ErrUnavailable), errors.Is(err, idempotency.ErrOutcomeUnknown), errors.Is(err, application.ErrUnavailable), errors.Is(err, repository.ErrUnavailable), errors.Is(err, repository.ErrUnsupportedSource), errors.Is(err, middleware.ErrContract):
		return middleware.Unavailable
	default:
		return middleware.InternalError
	}
}

// Replay routes are separate from writes: if a proxy drops a header or a
// receipt has expired, a recovery request still cannot turn into a mutation.
func (h *writeHandler) replayCreate(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	token, req, _, ok := h.request(w, r, "WorkoutCreate", "/me/sessions/replay", id)
	if !ok {
		return
	}
	req.Path = "/me/sessions"
	result, err := h.executor.ReplayCreation(r.Context(), h.db, token, req)
	if err != nil {
		h.fail(w, id, writeFailureKind(err))
		return
	}
	h.writeResult(w, id, result)
}

func (h *writeHandler) replaySets(w http.ResponseWriter, r *http.Request) {
	h.replayExisting(w, r, "sets", "SetBatchUpdate")
}

func (h *writeHandler) replayFinish(w http.ResponseWriter, r *http.Request) {
	h.replayExisting(w, r, "finish", "WorkoutFinish")
}

func (h *writeHandler) replayExisting(w http.ResponseWriter, r *http.Request, operation, schema string) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	target, err := parseSessionID(r.PathValue("id"))
	if err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	path := "/me/sessions/" + strconv.FormatUint(target, 10) + "/" + operation
	token, req, _, ok := h.request(w, r, schema, path+"/replay", id)
	if !ok {
		return
	}
	req.Path = path
	result, err := h.executor.ReplayExisting(r.Context(), h.db, token, target, operation, req)
	if err != nil {
		h.fail(w, id, writeFailureKind(err))
		return
	}
	h.writeResult(w, id, result)
}

func writeInputError(err error) middleware.ErrorKind {
	if errors.Is(err, middleware.ErrInvalidRequest) {
		return middleware.ValidationError
	}
	if errors.Is(err, middleware.ErrContract) {
		return middleware.Unavailable
	}
	return middleware.BadRequest
}

func writeKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !utf8.ValidString(values[0]) || utf8.RuneCountInString(values[0]) < 8 || utf8.RuneCountInString(values[0]) > 128 || strings.ContainsAny(values[0], "\x00\r\n") {
		return "", idempotency.ErrInvalid
	}
	return values[0], nil
}

func (h *writeHandler) request(w http.ResponseWriter, r *http.Request, schema string, canonicalPath string, id middleware.RequestID) (string, idempotency.Request, []byte, bool) {
	if r.URL == nil || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Path != canonicalPath {
		h.fail(w, id, middleware.BadRequest)
		return "", idempotency.Request{}, nil, false
	}
	token, err := middleware.BearerToken(r.Header)
	if err != nil {
		h.fail(w, id, writeFailureKind(err))
		return "", idempotency.Request{}, nil, false
	}
	key, err := writeKey(r)
	if err != nil {
		h.fail(w, id, middleware.BadRequest)
		return "", idempotency.Request{}, nil, false
	}
	body, err := h.validator.ReadRequest(r, schema)
	if err != nil {
		h.fail(w, id, writeInputError(err))
		return "", idempotency.Request{}, nil, false
	}
	req := idempotency.Request{Method: http.MethodPost, Path: canonicalPath, Key: key, Body: body}
	return token, req, body, true
}

func (h *writeHandler) create(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	token, req, body, ok := h.request(w, r, "WorkoutCreate", "/me/sessions", id)
	if !ok {
		return
	}
	var head map[string]json.RawMessage
	if err := decodeStrict(body, &head); err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	var source string
	if err := json.Unmarshal(head["source"], &source); err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	if source != "free" {
		// Plan sessions are valid contract input but have no implementation here.
		h.rejectUnsupportedCreation(w, r, token, req, id)
		return
	}
	var input struct {
		Source        string  `json:"source"`
		Date          string  `json:"date"`
		TimeZone      *string `json:"timeZone"`
		ClientEventID string  `json:"clientEventId"`
	}
	if err := decodeStrict(body, &input); err != nil || input.Source != "free" {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	result, err := h.executor.ExecuteCreation(r.Context(), h.db, token, req, func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		snapshot, err := h.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: input.Date, TimeZone: input.TimeZone, ClientEventID: input.ClientEventID,
		})
		if err != nil {
			if reply, handled := failureReply(err, id, application.NoWorkoutResultRef); handled {
				return reply, nil
			}
			return idempotency.Reply{}, err
		}
		projection, err := detail(snapshot)
		if err != nil {
			return idempotency.Reply{}, err
		}
		response, err := h.validator.EncodeResponse("WorkoutSessionDetail", projection)
		if err != nil {
			return idempotency.Reply{}, idempotency.ErrUnavailable
		}
		return idempotency.Reply{Status: http.StatusCreated, Body: response, ResultRef: application.WorkoutResultRef(snapshot.ID)}, nil
	})
	if err != nil {
		h.fail(w, id, writeFailureKind(err))
		return
	}
	h.writeResult(w, id, result)
}

func (h *writeHandler) updateSets(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	target, err := parseSessionID(r.PathValue("id"))
	if err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	path := "/me/sessions/" + strconv.FormatUint(target, 10) + "/sets"
	token, req, body, ok := h.request(w, r, "SetBatchUpdate", path, id)
	if !ok {
		return
	}
	input, unsupported, err := parseSetBatch(body)
	if err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	if unsupported {
		// A valid catalog action is not reinterpreted as a custom action.
		h.rejectUnsupportedExisting(w, r, token, target, "sets", req, id)
		return
	}
	result, err := h.executor.ExecuteExisting(r.Context(), h.db, token, target, "sets", req, func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		snapshot, err := h.service.SaveFreeForIdempotency(ctx, tx, actor, target, input.ExpectedVersion, input.Batch, input.Order)
		if err != nil {
			if reply, handled := failureReply(err, id, application.WorkoutResultRef(target)); handled {
				return reply, nil
			}
			return idempotency.Reply{}, err
		}
		projection, err := detail(snapshot)
		if err != nil {
			return idempotency.Reply{}, err
		}
		response, err := h.validator.EncodeResponse("WorkoutSessionDetail", projection)
		if err != nil {
			return idempotency.Reply{}, idempotency.ErrUnavailable
		}
		return idempotency.Reply{Status: http.StatusOK, Body: response, ResultRef: application.WorkoutResultRef(snapshot.ID)}, nil
	})
	if err != nil {
		h.fail(w, id, writeFailureKind(err))
		return
	}
	h.writeResult(w, id, result)
}

func (h *writeHandler) finish(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	target, err := parseSessionID(r.PathValue("id"))
	if err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	path := "/me/sessions/" + strconv.FormatUint(target, 10) + "/finish"
	token, req, body, ok := h.request(w, r, "WorkoutFinish", path, id)
	if !ok {
		return
	}
	var fields map[string]json.RawMessage
	if err := decodeStrict(body, &fields); err != nil {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	for _, field := range []string{"date", "durationMin", "rpe", "note"} {
		if _, exists := fields[field]; exists {
			// Preserve valid but currently unsupported finish semantics explicitly.
			h.rejectUnsupportedExisting(w, r, token, target, "finish", req, id)
			return
		}
	}
	version, err := exactUint(fields["expectedVersion"])
	if err != nil || version == 0 {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	result, err := h.executor.ExecuteExisting(r.Context(), h.db, token, target, "finish", req, func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		snapshot, err := h.service.FinishFreeForIdempotency(ctx, tx, actor, target, version)
		if err != nil {
			if reply, handled := failureReply(err, id, application.WorkoutResultRef(target)); handled {
				return reply, nil
			}
			return idempotency.Reply{}, err
		}
		projection, err := detail(snapshot)
		if err != nil {
			return idempotency.Reply{}, err
		}
		response, err := h.validator.EncodeResponse("WorkoutSessionDetail", projection)
		if err != nil {
			return idempotency.Reply{}, idempotency.ErrUnavailable
		}
		return idempotency.Reply{Status: http.StatusOK, Body: response, ResultRef: application.WorkoutResultRef(snapshot.ID)}, nil
	})
	if err != nil {
		h.fail(w, id, writeFailureKind(err))
		return
	}
	h.writeResult(w, id, result)
}

func (h *writeHandler) rejectUnsupportedCreation(w http.ResponseWriter, r *http.Request, token string, req idempotency.Request, id middleware.RequestID) {
	_, err := h.executor.ExecuteCreation(r.Context(), h.db, token, req, func(context.Context, idempotency.Transaction, idempotency.Actor) (idempotency.Reply, error) {
		return idempotency.Reply{}, idempotency.ErrUnavailable
	})
	if err != nil && !errors.Is(err, idempotency.ErrUnavailable) {
		h.fail(w, id, writeFailureKind(err))
		return
	}
	h.fail(w, id, middleware.Unavailable)
}

func (h *writeHandler) rejectUnsupportedExisting(w http.ResponseWriter, r *http.Request, token string, target uint64, operation string, req idempotency.Request, id middleware.RequestID) {
	_, err := h.executor.ExecuteExisting(r.Context(), h.db, token, target, operation, req, func(context.Context, idempotency.Transaction, idempotency.Actor) (idempotency.Reply, error) {
		return idempotency.Reply{}, idempotency.ErrUnavailable
	})
	if err != nil && !errors.Is(err, idempotency.ErrUnavailable) {
		h.fail(w, id, writeFailureKind(err))
		return
	}
	h.fail(w, id, middleware.Unavailable)
}

func (h *writeHandler) writeResult(w http.ResponseWriter, id middleware.RequestID, result idempotency.Result) {
	// A cached response can outlive a deployment. Check it against the current
	// schema before releasing it, but preserve its exact bytes for replay.
	schema := "Error"
	if result.Status >= 200 && result.Status <= 299 {
		schema = "WorkoutSessionDetail"
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Body))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	if _, err := h.validator.EncodeResponse(schema, value); err != nil {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	requestIDValue := id.String()
	if result.Status < 200 || result.Status > 299 {
		var failure struct {
			RequestID string `json:"requestId"`
		}
		if json.Unmarshal(result.Body, &failure) != nil || !validRequestID(failure.RequestID) {
			h.fail(w, id, middleware.Unavailable)
			return
		}
		requestIDValue = failure.RequestID
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", requestIDValue)
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	if result.Status >= 200 && result.Status <= 299 && !result.ExpiresAt.IsZero() {
		w.Header().Set("Idempotency-Key-Expires-At", result.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

// exactNumber parses JSON number syntax without converting through float64.
// Bounded token/exponent sizes avoid unbounded big-integer work on hostile input.
func exactNumber(raw json.RawMessage) (*big.Rat, error) {
	if len(raw) == 0 || len(raw) > 128 || !json.Valid(raw) || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return nil, idempotency.ErrInvalid
	}
	text := string(raw)
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponent, err := strconv.ParseInt(text[index+1:], 10, 16)
		if err != nil || exponent < -128 || exponent > 128 {
			return nil, idempotency.ErrInvalid
		}
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, idempotency.ErrInvalid
	}
	return value, nil
}

func exactUint(raw json.RawMessage) (uint64, error) {
	value, err := exactNumber(raw)
	if err != nil || !value.IsInt() || !value.Num().IsUint64() {
		return 0, idempotency.ErrInvalid
	}
	return value.Num().Uint64(), nil
}

func parseSessionID(raw string) (uint64, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 || strconv.FormatUint(value, 10) != raw {
		return 0, idempotency.ErrInvalid
	}
	return value, nil
}

func validRequestID(value string) bool {
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

type setBatchWire struct {
	ExpectedVersion   json.RawMessage `json:"expectedVersion"`
	ExerciseEntryKeys []string        `json:"exerciseEntryKeys"`
	Sets              *[]setWriteWire `json:"sets,omitempty"`
	DeleteRecordKeys  *[]string       `json:"deleteRecordKeys,omitempty"`
	RestoreRecordKeys *[]string       `json:"restoreRecordKeys,omitempty"`
}

type setWriteWire struct {
	RecordKey        string          `json:"recordKey"`
	ExerciseEntryKey string          `json:"exerciseEntryKey"`
	Completed        *bool           `json:"completed,omitempty"`
	ExerciseID       *string         `json:"exerciseId,omitempty"`
	ExerciseName     *string         `json:"exerciseName,omitempty"`
	Kind             domain.Kind     `json:"kind"`
	SetIndex         json.RawMessage `json:"setIndex"`
	WeightKg         json.RawMessage `json:"weightKg,omitempty"`
	Reps             json.RawMessage `json:"reps,omitempty"`
	Seconds          json.RawMessage `json:"seconds,omitempty"`
	RPE              json.RawMessage `json:"rpe,omitempty"`
}

type parsedSetBatch struct {
	ExpectedVersion uint64
	Batch           domain.Batch
	Order           []string
}

func parseSetBatch(body []byte) (parsedSetBatch, bool, error) {
	var wire setBatchWire
	if err := decodeStrict(body, &wire); err != nil {
		return parsedSetBatch{}, false, err
	}
	version, err := exactUint(wire.ExpectedVersion)
	if err != nil || version == 0 {
		return parsedSetBatch{}, false, idempotency.ErrInvalid
	}
	if wire.ExerciseEntryKeys == nil {
		return parsedSetBatch{}, false, idempotency.ErrInvalid
	}
	parsed := parsedSetBatch{ExpectedVersion: version, Order: append([]string{}, wire.ExerciseEntryKeys...)}
	if wire.DeleteRecordKeys != nil {
		parsed.Batch.DeleteKeys = append([]string{}, (*wire.DeleteRecordKeys)...)
	}
	if wire.RestoreRecordKeys != nil {
		parsed.Batch.RestoreKeys = append([]string{}, (*wire.RestoreRecordKeys)...)
	}
	if wire.Sets != nil {
		parsed.Batch.Sets = make([]domain.Set, 0, len(*wire.Sets))
		for _, item := range *wire.Sets {
			if item.ExerciseID != nil {
				return parsedSetBatch{}, true, nil
			}
			if item.ExerciseName == nil {
				return parsedSetBatch{}, false, idempotency.ErrInvalid
			}
			index, err := exactUint(item.SetIndex)
			if err != nil || index == 0 || index > uint64(^uint(0)>>1) {
				return parsedSetBatch{}, false, idempotency.ErrInvalid
			}
			reps, err := optionalExactInt(item.Reps)
			if err != nil {
				return parsedSetBatch{}, false, err
			}
			seconds, err := optionalExactInt(item.Seconds)
			if err != nil {
				return parsedSetBatch{}, false, err
			}
			weight, err := optionalDecimal(item.WeightKg, false, 2)
			if err != nil {
				return parsedSetBatch{}, false, err
			}
			rpe, err := optionalDecimal(item.RPE, true, 1)
			if err != nil {
				return parsedSetBatch{}, false, err
			}
			parsed.Batch.Sets = append(parsed.Batch.Sets, domain.Set{
				RecordKey: item.RecordKey, ExerciseEntryKey: item.ExerciseEntryKey,
				Exercise: domain.Exercise{Source: domain.Custom, Name: *item.ExerciseName},
				Index:    int(index), Kind: item.Kind, WeightKg: weight, Reps: reps, Seconds: seconds, RPE: rpe,
			})
		}
	}
	return parsed, false, nil
}

func optionalExactInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	value, err := exactUint(raw)
	if err != nil || value > uint64(^uint(0)>>1) {
		return 0, idempotency.ErrInvalid
	}
	return int(value), nil
}

// optionalDecimal converts a finite JSON decimal to a non-exponent string only
// if its exact value has no more than the domain's supported decimal places.
func optionalDecimal(raw json.RawMessage, nullable bool, places int) (string, error) {
	if len(raw) == 0 || nullable && bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	value, err := exactNumber(raw)
	if err != nil || places < 0 || places > 2 {
		return "", idempotency.ErrInvalid
	}
	// Enforce the user's stated decimal precision after shifting an exponent;
	// e.g. 4.250e1 has two decimal places, while 42.500 has three.
	text := string(raw)
	mantissa, exponent := text, int64(0)
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		mantissa = text[:index]
		exponent, err = strconv.ParseInt(text[index+1:], 10, 16)
		if err != nil {
			return "", idempotency.ErrInvalid
		}
	}
	precision := int64(0)
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		precision = int64(len(mantissa) - index - 1)
	}
	if precision-exponent > int64(places) {
		return "", idempotency.ErrInvalid
	}
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	scaled := new(big.Rat).Mul(value, new(big.Rat).SetInt(factor))
	if !scaled.IsInt() {
		return "", idempotency.ErrInvalid
	}
	n := new(big.Int).Set(scaled.Num())
	negative := n.Sign() < 0
	n.Abs(n)
	digits := n.String()
	if places > 0 {
		if len(digits) <= places {
			digits = strings.Repeat("0", places+1-len(digits)) + digits
		}
		position := len(digits) - places
		digits = digits[:position] + "." + digits[position:]
		digits = strings.TrimRight(strings.TrimRight(digits, "0"), ".")
	}
	if negative {
		digits = "-" + digits
	}
	return digits, nil
}

func decodeStrict(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return idempotency.ErrInvalid
	}
	return nil
}

func failureReply(err error, requestID middleware.RequestID, resultRef string) (idempotency.Reply, bool) {
	var status int
	var code, message string
	switch {
	case errors.Is(err, repository.ErrVersionConflict):
		status, code, message = http.StatusPreconditionFailed, "VERSION_CONFLICT", "记录已更新，请先查看最新内容"
	case errors.Is(err, repository.ErrSetConflict), errors.Is(err, repository.ErrConflict), errors.Is(err, domain.ErrConflict):
		status, code, message = http.StatusConflict, "CONFLICT", "状态已变化，请检查当前内容"
	case errors.Is(err, repository.ErrInvalid), errors.Is(err, domain.ErrInvalid):
		status, code, message = http.StatusUnprocessableEntity, "VALIDATION_ERROR", "填写内容不符合要求"
	default:
		return idempotency.Reply{}, false
	}
	body, marshalErr := json.Marshal(struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId"`
	}{code, message, requestID.String()})
	if marshalErr != nil {
		return idempotency.Reply{}, false
	}
	return idempotency.Reply{Status: status, Body: body, ResultRef: resultRef}, true
}
