// Package transport contains explicit HTTP projections for workout reads.
// It is not mounted on the production router until its dependencies and the
// remaining login, write-idempotency, and release boundaries are complete.
package transport

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/workout/application"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

type readHandler struct {
	db        *sql.DB
	service   *application.Service
	validator *middleware.SchemaValidator
}

// NewReadHandler exposes read-only routes in the supplied process. Callers
// must not mount it publicly until authentication, rate limiting, full source
// coverage, and the remaining release gates have been reviewed.
func NewReadHandler(db *sql.DB, service *application.Service, validator *middleware.SchemaValidator) (http.Handler, error) {
	if db == nil || service == nil || validator == nil {
		return nil, middleware.ErrContract
	}
	h := &readHandler{db: db, service: service, validator: validator}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /me/sessions", h.list)
	mux.HandleFunc("GET /me/sessions/{id}", h.detail)
	return mux, nil
}

func (h *readHandler) fail(w http.ResponseWriter, id middleware.RequestID, kind middleware.ErrorKind) {
	// If encoding the public error fails, do not expose the original error.
	if err := middleware.WriteError(w, h.validator, id, kind); err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func errorKind(err error) middleware.ErrorKind {
	switch {
	case errors.Is(err, application.ErrAuthentication), errors.Is(err, middleware.ErrInvalidBearer):
		return middleware.Unauthenticated
	case errors.Is(err, application.ErrAgeRequired):
		return middleware.Forbidden
	case errors.Is(err, application.ErrWorkoutNotFound):
		return middleware.NotFound
	case errors.Is(err, repository.ErrInvalid):
		return middleware.ValidationError
	case errors.Is(err, application.ErrUnavailable), errors.Is(err, repository.ErrUnavailable):
		return middleware.Unavailable
	default:
		return middleware.InternalError
	}
}

func requestID(w http.ResponseWriter) (middleware.RequestID, bool) {
	id, err := middleware.NewRequestID()
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
		return middleware.RequestID{}, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, id middleware.RequestID, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", id.String())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func listFilter(raw string) (repository.ListFilter, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return repository.ListFilter{}, repository.ErrInvalid
	}
	for name, values := range query {
		if (name != "page" && name != "pageSize" && name != "from" && name != "to") || len(values) != 1 {
			return repository.ListFilter{}, repository.ErrInvalid
		}
	}
	result := repository.ListFilter{Page: 1, PageSize: 20}
	for _, field := range []struct {
		name string
		dest *uint64
	}{{"page", &result.Page}, {"pageSize", &result.PageSize}} {
		if rawValue, ok := query[field.name]; ok {
			value, err := strconv.ParseUint(rawValue[0], 10, 64)
			if err != nil || value == 0 {
				return repository.ListFilter{}, repository.ErrInvalid
			}
			*field.dest = value
		}
	}
	if values, ok := query["from"]; ok {
		result.From = values[0]
		if result.From == "" {
			return repository.ListFilter{}, repository.ErrInvalid
		}
	}
	if values, ok := query["to"]; ok {
		result.To = values[0]
		if result.To == "" {
			return repository.ListFilter{}, repository.ErrInvalid
		}
	}
	return result, nil
}

func (h *readHandler) list(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w)
	if !ok {
		return
	}
	token, err := middleware.BearerToken(r.Header)
	if err != nil {
		h.fail(w, id, errorKind(err))
		return
	}
	filter, err := listFilter(r.URL.RawQuery)
	if err != nil {
		h.fail(w, id, errorKind(err))
		return
	}
	result, err := h.service.ListFree(r.Context(), h.db, token, filter)
	if err != nil {
		h.fail(w, id, errorKind(err))
		return
	}
	items := make([]json.RawMessage, 0, len(result.Items))
	for _, row := range result.Items {
		body, err := h.validator.EncodeResponse("WorkoutSession", summary(row))
		if err != nil {
			h.fail(w, id, middleware.Unavailable)
			return
		}
		items = append(items, body)
	}
	body, err := h.validator.EncodeResponse("Paginated", map[string]any{
		"items": items, "page": result.Page, "pageSize": result.PageSize, "total": result.Total,
	})
	if err != nil {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	writeJSON(w, id, body)
}

func (h *readHandler) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w)
	if !ok {
		return
	}
	token, err := middleware.BearerToken(r.Header)
	if err != nil {
		h.fail(w, id, errorKind(err))
		return
	}
	path := r.PathValue("id")
	sessionID, err := strconv.ParseUint(path, 10, 64)
	if err != nil || sessionID == 0 || strconv.FormatUint(sessionID, 10) != path {
		h.fail(w, id, middleware.ValidationError)
		return
	}
	result, err := h.service.ReadVisibleOrderedFree(r.Context(), h.db, token, sessionID)
	if err != nil {
		h.fail(w, id, errorKind(err))
		return
	}
	projection, err := detail(result)
	if err != nil {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	body, err := h.validator.EncodeResponse("WorkoutSessionDetail", projection)
	if err != nil {
		h.fail(w, id, middleware.Unavailable)
		return
	}
	writeJSON(w, id, body)
}

func summary(row repository.SessionSummary) map[string]any {
	var recordedAt any
	if row.RecordedAt != nil {
		recordedAt = row.RecordedAt.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		"id": strconv.FormatUint(row.ID, 10), "date": row.Date, "timeZone": row.TimeZone,
		"status": row.Status, "source": "free", "version": row.Version,
		"createdAt": row.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updatedAt": row.UpdatedAt.UTC().Format(time.RFC3339Nano), "recordedAt": recordedAt,
	}
}

func detail(row repository.OrderedSnapshot) (map[string]any, error) {
	out := summary(repository.SessionSummary{ID: row.ID, Version: row.Version, Date: row.Date, TimeZone: row.TimeZone,
		Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, RecordedAt: row.RecordedAt})
	order := append([]string{}, row.ExerciseEntryKeys...)
	positions := make(map[string]int, len(order))
	for i, key := range order {
		if _, exists := positions[key]; exists {
			return nil, middleware.ErrInvalidResponse
		}
		positions[key] = i
	}
	sets := make([]domain.Set, 0, len(row.Sets))
	for _, set := range row.Sets {
		if _, exists := positions[set.ExerciseEntryKey]; !exists || domain.ValidateSet(set) != nil {
			return nil, middleware.ErrInvalidResponse
		}
		sets = append(sets, set)
	}
	sort.Slice(sets, func(i, j int) bool {
		left, right := positions[sets[i].ExerciseEntryKey], positions[sets[j].ExerciseEntryKey]
		if left != right {
			return left < right
		}
		return sets[i].Index < sets[j].Index
	})
	projected := make([]map[string]any, 0, len(sets))
	for _, set := range sets {
		item := map[string]any{"recordKey": set.RecordKey, "exerciseEntryKey": set.ExerciseEntryKey,
			"setIndex": set.Index, "kind": set.Kind, "resolvedExerciseName": set.Exercise.Name, "completed": true}
		switch set.Exercise.Source {
		case domain.Custom:
			item["exerciseName"] = set.Exercise.Name
		case domain.Catalog:
			item["exerciseId"] = set.Exercise.ID
		default:
			return nil, middleware.ErrInvalidResponse
		}
		if set.WeightKg != "" {
			item["weightKg"] = json.Number(set.WeightKg)
		}
		if set.Reps != 0 {
			item["reps"] = set.Reps
		}
		if set.Seconds != 0 {
			item["seconds"] = set.Seconds
		}
		if set.RPE != "" {
			item["rpe"] = json.Number(set.RPE)
		}
		projected = append(projected, item)
	}
	out["clientEventId"] = row.ClientEventID
	out["exerciseEntryKeys"] = order
	out["sets"] = projected
	return out, nil
}
