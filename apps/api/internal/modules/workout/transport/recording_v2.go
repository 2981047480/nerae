package transport

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"nerae/api/internal/idempotency"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/workout/application"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

const recordingV2Path = "/me/recordings"

type recordingV2Handler struct {
	db       *sql.DB
	service  *application.RecordingV2Service
	store    *repository.RecordingV2Store
	executor *application.RecordingV2Executor
	common   *writeHandler
}

func NewRecordingV2Handler(db *sql.DB, service *application.RecordingV2Service, store *repository.RecordingV2Store, executor *application.RecordingV2Executor, validator *middleware.SchemaValidator) (http.Handler, error) {
	if db == nil || service == nil || store == nil || executor == nil || validator == nil {
		return nil, middleware.ErrContract
	}
	h := &recordingV2Handler{db: db, service: service, store: store, executor: executor, common: &writeHandler{validator: validator}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /me/recordings", h.list)
	mux.HandleFunc("POST /me/recordings", h.create)
	mux.HandleFunc("GET /me/recordings/{id}", h.detail)
	mux.HandleFunc("POST /me/recordings/{id}/sets", h.sets)
	mux.HandleFunc("POST /me/recordings/{id}/finish", h.finish)
	mux.HandleFunc("POST /me/recordings/replay", h.replayCreate)
	mux.HandleFunc("POST /me/recordings/{id}/sets/replay", h.replaySets)
	mux.HandleFunc("POST /me/recordings/{id}/finish/replay", h.replayFinish)
	return mux, nil
}

type recordingV2Receipt struct {
	ID      string `json:"id"`
	Version uint64 `json:"version"`
}
type recordingV2CreateInput struct {
	Source        string                 `json:"source"`
	Date          string                 `json:"date"`
	TimeZone      *string                `json:"timeZone,omitempty"`
	ClientEventID string                 `json:"clientEventId"`
	Mode          domain.RecordingModeV2 `json:"mode"`
}
type recordingV2FinishInput struct {
	ExpectedVersion uint64 `json:"expectedVersion"`
}
type recordingV2SummaryJSON struct {
	ID           string        `json:"id"`
	Version      uint64        `json:"version"`
	RecordFormat uint8         `json:"recordFormat"`
	Date         string        `json:"date"`
	TimeZone     string        `json:"timeZone"`
	Status       domain.Status `json:"status"`
}
type recordingV2PageJSON struct {
	Items    []recordingV2SummaryJSON `json:"items"`
	Page     uint64                   `json:"page"`
	PageSize uint64                   `json:"pageSize"`
	Total    uint64                   `json:"total"`
}

func pageV2JSON(v repository.RecordingV2ListPage) recordingV2PageJSON {
	items := make([]recordingV2SummaryJSON, 0, len(v.Items))
	for _, item := range v.Items {
		items = append(items, recordingV2SummaryJSON{ID: strconv.FormatUint(item.ID, 10), Version: item.Version, RecordFormat: item.RecordFormat, Date: item.Date, TimeZone: item.TimeZone, Status: item.Status})
	}
	return recordingV2PageJSON{Items: items, Page: v.Page, PageSize: v.PageSize, Total: v.Total}
}
func (h *recordingV2Handler) getToken(w http.ResponseWriter, r *http.Request, id middleware.RequestID) (string, bool) {
	token, err := middleware.BearerToken(r.Header)
	if err != nil {
		h.common.fail(w, id, writeFailureKind(err))
		return "", false
	}
	return token, true
}
func (h *recordingV2Handler) list(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	token, ok := h.getToken(w, r, id)
	if !ok {
		return
	}
	filter, err := listFilter(r.URL.RawQuery)
	if err != nil {
		h.common.fail(w, id, middleware.BadRequest)
		return
	}
	page, err := h.service.ListOwn(r.Context(), h.db, token, filter)
	if err != nil {
		h.common.fail(w, id, errorKind(err))
		return
	}
	body, err := h.common.validator.EncodeResponse("RecordingV2Page", pageV2JSON(page))
	if err != nil {
		h.common.fail(w, id, middleware.Unavailable)
		return
	}
	writeJSON(w, id, body)
}
func (h *recordingV2Handler) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	if r.URL == nil || r.URL.RawQuery != "" || r.URL.ForceQuery {
		h.common.fail(w, id, middleware.BadRequest)
		return
	}
	token, ok := h.getToken(w, r, id)
	if !ok {
		return
	}
	target, err := parseSessionID(r.PathValue("id"))
	if err != nil {
		h.common.fail(w, id, middleware.BadRequest)
		return
	}
	snapshot, err := h.service.ReadOwn(r.Context(), h.db, token, target)
	if err != nil {
		h.common.fail(w, id, errorKind(err))
		return
	}
	view, err := recordingV2Detail(snapshot)
	if err != nil {
		h.common.fail(w, id, middleware.Unavailable)
		return
	}
	body, err := h.common.validator.EncodeResponse("RecordingV2Detail", view)
	if err != nil {
		h.common.fail(w, id, middleware.Unavailable)
		return
	}
	writeJSON(w, id, body)
}
func (h *recordingV2Handler) request(w http.ResponseWriter, r *http.Request, schema, path string, id middleware.RequestID) (string, idempotency.Request, []byte, bool) {
	token, req, body, ok := h.common.request(w, r, schema, path, id)
	if !ok {
		return "", idempotency.Request{}, nil, false
	}
	if !validV2UnicodeEscapes(body) {
		h.common.fail(w, id, middleware.ValidationError)
		return "", idempotency.Request{}, nil, false
	}
	return token, req, body, true
}
func (h *recordingV2Handler) makeReceipt(status int, id, version uint64) (idempotency.Reply, error) {
	value := recordingV2Receipt{ID: strconv.FormatUint(id, 10), Version: version}
	raw, err := h.common.validator.EncodeResponse("RecordingV2Receipt", value)
	if err != nil {
		return idempotency.Reply{}, idempotency.ErrUnavailable
	}
	return idempotency.Reply{Status: status, Body: raw, ResultRef: application.WorkoutResultRef(id)}, nil
}
func (h *recordingV2Handler) create(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	token, req, body, ok := h.request(w, r, "RecordingV2Create", recordingV2Path, id)
	if !ok {
		return
	}
	var input recordingV2CreateInput
	if decodeStrict(body, &input) != nil || input.Source != "free" {
		h.common.fail(w, id, middleware.ValidationError)
		return
	}
	result, err := h.executor.ExecuteCreation(r.Context(), h.db, token, req, func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		ref, err := h.store.CreateFreeV2Tx(ctx, tx, actor.SubjectID(), repository.FreeSessionInput{Date: input.Date, TimeZone: input.TimeZone, ClientEventID: input.ClientEventID}, input.Mode)
		if err != nil {
			if reply, handled := failureReply(err, id, application.NoWorkoutResultRef); handled {
				return reply, nil
			}
			return idempotency.Reply{}, err
		}
		return h.makeReceipt(http.StatusCreated, ref.ID, ref.Version)
	})
	if err != nil {
		h.common.fail(w, id, writeFailureKind(err))
		return
	}
	h.common.writeResultWithSchema(w, id, result, "RecordingV2Receipt")
}
func (h *recordingV2Handler) sets(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	target, err := parseSessionID(r.PathValue("id"))
	if err != nil {
		h.common.fail(w, id, middleware.ValidationError)
		return
	}
	path := recordingV2Path + "/" + strconv.FormatUint(target, 10) + "/sets"
	token, req, body, ok := h.request(w, r, "RecordingV2Batch", path, id)
	if !ok {
		return
	}
	var input recordingV2BatchInput
	if decodeStrict(body, &input) != nil {
		h.common.fail(w, id, middleware.ValidationError)
		return
	}
	result, err := h.executor.ExecuteExisting(r.Context(), h.db, token, target, "sets", req, func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		ref := application.WorkoutResultRef(target)
		snapshot, err := h.store.ReadFreeV2Tx(ctx, tx, actor.SubjectID(), target)
		if err != nil {
			return idempotency.Reply{}, err
		}
		if snapshot.Version != input.ExpectedVersion {
			reply, _ := failureReply(repository.ErrVersionConflict, id, ref)
			return reply, nil
		}
		batch, err := buildRecordingV2Batch(snapshot.Recording, input)
		if err != nil {
			if reply, handled := failureReply(err, id, ref); handled {
				return reply, nil
			}
			return idempotency.Reply{}, err
		}
		version, _, err := h.store.ApplyBatchV2Tx(ctx, tx, actor.SubjectID(), target, input.ExpectedVersion, batch)
		if err != nil {
			if reply, handled := failureReply(err, id, ref); handled {
				return reply, nil
			}
			return idempotency.Reply{}, err
		}
		return h.makeReceipt(http.StatusOK, target, version)
	})
	if err != nil {
		h.common.fail(w, id, writeFailureKind(err))
		return
	}
	h.common.writeResultWithSchema(w, id, result, "RecordingV2Receipt")
}
func (h *recordingV2Handler) finish(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	target, err := parseSessionID(r.PathValue("id"))
	if err != nil {
		h.common.fail(w, id, middleware.ValidationError)
		return
	}
	path := recordingV2Path + "/" + strconv.FormatUint(target, 10) + "/finish"
	token, req, body, ok := h.request(w, r, "RecordingV2Finish", path, id)
	if !ok {
		return
	}
	var input recordingV2FinishInput
	if decodeStrict(body, &input) != nil {
		h.common.fail(w, id, middleware.ValidationError)
		return
	}
	result, err := h.executor.ExecuteExisting(r.Context(), h.db, token, target, "finish", req, func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		version, err := h.store.CompleteV2Tx(ctx, tx, actor.SubjectID(), target, input.ExpectedVersion)
		if err != nil {
			if reply, handled := failureReply(err, id, application.WorkoutResultRef(target)); handled {
				return reply, nil
			}
			return idempotency.Reply{}, err
		}
		return h.makeReceipt(http.StatusOK, target, version)
	})
	if err != nil {
		h.common.fail(w, id, writeFailureKind(err))
		return
	}
	h.common.writeResultWithSchema(w, id, result, "RecordingV2Receipt")
}
func (h *recordingV2Handler) replayCreate(w http.ResponseWriter, r *http.Request) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	token, req, _, ok := h.request(w, r, "RecordingV2Create", recordingV2Path+"/replay", id)
	if !ok {
		return
	}
	req.Path = recordingV2Path
	result, err := h.executor.ReplayCreation(r.Context(), h.db, token, req)
	if err != nil {
		h.common.fail(w, id, writeFailureKind(err))
		return
	}
	h.common.writeResultWithSchema(w, id, result, "RecordingV2Receipt")
}
func (h *recordingV2Handler) replaySets(w http.ResponseWriter, r *http.Request) {
	h.replayExisting(w, r, "sets", "RecordingV2Batch")
}
func (h *recordingV2Handler) replayFinish(w http.ResponseWriter, r *http.Request) {
	h.replayExisting(w, r, "finish", "RecordingV2Finish")
}
func (h *recordingV2Handler) replayExisting(w http.ResponseWriter, r *http.Request, op, schema string) {
	id, ok := newWriteRequestID(w)
	if !ok {
		return
	}
	target, err := parseSessionID(r.PathValue("id"))
	if err != nil {
		h.common.fail(w, id, middleware.ValidationError)
		return
	}
	path := recordingV2Path + "/" + strconv.FormatUint(target, 10) + "/" + op
	token, req, _, ok := h.request(w, r, schema, path+"/replay", id)
	if !ok {
		return
	}
	req.Path = path
	result, err := h.executor.ReplayExisting(r.Context(), h.db, token, target, op, req)
	if err != nil {
		h.common.fail(w, id, writeFailureKind(err))
		return
	}
	h.common.writeResultWithSchema(w, id, result, "RecordingV2Receipt")
}

// encoding/json replaces unpaired UTF-16 escape surrogates with U+FFFD. Reject
// those bytes instead of silently changing an entered action name or key.
func validV2UnicodeEscapes(body []byte) bool {
	inside := false
	hex := func(c byte) (uint16, bool) {
		switch {
		case c >= '0' && c <= '9':
			return uint16(c - '0'), true
		case c >= 'a' && c <= 'f':
			return uint16(c - 'a' + 10), true
		case c >= 'A' && c <= 'F':
			return uint16(c - 'A' + 10), true
		}
		return 0, false
	}
	read := func(at int) (uint16, bool) {
		if at+4 > len(body) {
			return 0, false
		}
		var v uint16
		for i := 0; i < 4; i++ {
			d, ok := hex(body[at+i])
			if !ok {
				return 0, false
			}
			v = v*16 + d
		}
		return v, true
	}
	for i := 0; i < len(body); i++ {
		if !inside {
			if body[i] == '"' {
				inside = true
			}
			continue
		}
		if body[i] == '"' {
			inside = false
			continue
		}
		if body[i] != '\\' {
			continue
		}
		i++
		if i >= len(body) {
			return false
		}
		if body[i] != 'u' {
			continue
		}
		value, ok := read(i + 1)
		if !ok {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
				return false
			}
			low, ok := read(i + 3)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inside
}
