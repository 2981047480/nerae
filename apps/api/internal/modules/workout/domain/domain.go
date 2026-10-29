package domain

import (
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"
)

const MaxBatchItems = 200

type Kind string

const (
	Weight            Kind = "weight"
	PerSide           Kind = "perSide"
	Bodyweight        Kind = "bodyweight"
	BodyweightPerSide Kind = "bodyweightPerSide"
	Duration          Kind = "duration"
	DurationPerSide   Kind = "durationPerSide"
	Breath            Kind = "breath"
)

type Status string

const (
	InProgress Status = "in_progress"
	Completed  Status = "completed"
	Skipped    Status = "skipped"
)

type Source string

const (
	Catalog Source = "catalog"
	Custom  Source = "custom"
)

var (
	ErrInvalid  = errors.New("invalid workout data")
	ErrConflict = errors.New("workout state conflict")
)

// Exercise is an identity snapshot. Catalog IDs do not prove authorization.
type Exercise struct {
	Source   Source
	ID, Name string
}
type Set struct {
	RecordKey, ExerciseEntryKey string
	Exercise                    Exercise
	Index                       int
	Kind                        Kind
	WeightKg                    string
	Reps, Seconds               int
	RPE                         string
}
type Key struct {
	ExerciseEntryKey string
	Deleted          bool
}
type Session struct {
	Status  Status
	Sets    map[string]Set
	Keys    map[string]Key
	Entries map[string]Exercise
}
type Batch struct {
	Sets                    []Set
	DeleteKeys, RestoreKeys []string
}

func ValidateSet(s Set) error {
	if !validKey(s.RecordKey) || !validKey(s.ExerciseEntryKey) || s.Index < 1 || validateExercise(s.Exercise) != nil {
		return ErrInvalid
	}
	if s.RPE != "" && !decimal(s.RPE, "1", "10", 1, false) {
		return ErrInvalid
	}
	switch s.Kind {
	case Weight, PerSide:
		if !decimal(s.WeightKg, "0", "300", 2, true) || s.Reps < 1 || s.Reps > 100 || s.Seconds != 0 {
			return ErrInvalid
		}
	case Bodyweight, BodyweightPerSide, Breath:
		if s.WeightKg != "" || s.Reps < 1 || s.Reps > 100 || s.Seconds != 0 {
			return ErrInvalid
		}
	case Duration, DurationPerSide:
		if s.WeightKg != "" || s.Reps != 0 || s.Seconds < 5 || s.Seconds > 600 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// ApplyBatch validates against a snapshot and returns a new value only on success.
// Caller must provide trusted persisted key/entry snapshots; this function performs no authorization or persistence.
func ApplyBatch(cur Session, b Batch) (Session, error) {
	if cur.Status != InProgress && cur.Status != Completed && cur.Status != Skipped {
		return Session{}, ErrInvalid
	}
	if cur.Status == Skipped && (len(b.Sets) > 0 || len(b.DeleteKeys) > 0 || len(b.RestoreKeys) > 0) {
		return Session{}, ErrConflict
	}
	if len(b.Sets)+len(b.DeleteKeys) > MaxBatchItems || (len(b.Sets) == 0 && len(b.DeleteKeys) == 0) {
		return Session{}, ErrInvalid
	}
	sets, keys, entries := cloneSets(cur.Sets), cloneKeys(cur.Keys), cloneEntries(cur.Entries)
	updates := map[string]Set{}
	for _, s := range b.Sets {
		if ValidateSet(s) != nil {
			return Session{}, ErrInvalid
		}
		if _, ok := updates[s.RecordKey]; ok {
			return Session{}, ErrInvalid
		}
		updates[s.RecordKey] = s
	}
	deletes := map[string]bool{}
	for _, k := range b.DeleteKeys {
		if !validKey(k) || deletes[k] {
			return Session{}, ErrInvalid
		}
		if _, ok := updates[k]; ok {
			return Session{}, ErrInvalid
		}
		deletes[k] = true
	}
	restores := map[string]bool{}
	for _, k := range b.RestoreKeys {
		if !validKey(k) || restores[k] {
			return Session{}, ErrInvalid
		}
		if _, ok := updates[k]; !ok {
			return Session{}, ErrInvalid
		}
		restores[k] = true
	}
	for k, s := range updates {
		old, registered := keys[k]
		if restores[k] && (!registered || !old.Deleted) {
			return Session{}, ErrConflict
		}
		if registered && old.Deleted && (!restores[k] || old.ExerciseEntryKey != s.ExerciseEntryKey) {
			return Session{}, ErrConflict
		}
		if registered && !old.Deleted && old.ExerciseEntryKey != s.ExerciseEntryKey {
			return Session{}, ErrConflict
		}
		if e, ok := entries[s.ExerciseEntryKey]; ok && e != s.Exercise {
			// The same exercise entry cannot change its frozen source or name.
			// The API contract classifies this as invalid input (HTTP 422).
			return Session{}, ErrInvalid
		}
		entries[s.ExerciseEntryKey] = s.Exercise
		keys[k] = Key{ExerciseEntryKey: s.ExerciseEntryKey}
		sets[k] = s
		delete(restores, k)
	}
	if len(restores) > 0 {
		return Session{}, ErrInvalid
	}
	for k := range deletes {
		if old, ok := keys[k]; ok && !old.Deleted {
			delete(sets, k)
			old.Deleted = true
			keys[k] = old
		}
	}
	status := cur.Status
	if len(sets) == 0 && status == Completed {
		status = InProgress
	}
	return Session{Status: status, Sets: sets, Keys: keys, Entries: entries}, nil
}
func Complete(s Session) (Session, error) {
	if (s.Status != InProgress && s.Status != Completed) || len(s.Sets) == 0 {
		return Session{}, ErrConflict
	}
	n := cloneSession(s)
	n.Status = Completed
	return n, nil
}
func validateExercise(e Exercise) error {
	switch e.Source {
	case Catalog:
		if !numericID(e.ID) || e.Name != "" {
			return ErrInvalid
		}
	case Custom:
		if e.ID != "" || strings.TrimSpace(e.Name) == "" || len([]rune(e.Name)) > 60 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func decimal(v, min, max string, places int, exclusiveMin bool) bool {
	if len(v) == 0 || len(v) > 16 || strings.TrimSpace(v) != v {
		return false
	}
	// Accept only a plain decimal token; Rat.SetString also accepts fractions and bases.
	start := 0
	if v[0] == '-' {
		start = 1
	} else if v[0] == '+' {
		return false
	}
	if start == len(v) {
		return false
	}
	dot, digits := false, 0
	for _, c := range v[start:] {
		if c == '.' && !dot {
			dot = true
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
		digits++
	}
	if digits == 0 || v[len(v)-1] == '.' {
		return false
	}
	r, ok := new(big.Rat).SetString(v)
	if !ok {
		return false
	}
	lo, _ := new(big.Rat).SetString(min)
	hi, _ := new(big.Rat).SetString(max)
	c := r.Cmp(lo)
	if (exclusiveMin && c <= 0) || (!exclusiveMin && c < 0) || r.Cmp(hi) > 0 {
		return false
	}
	if i := strings.IndexByte(v, '.'); i >= 0 && len(v)-i-1 > places {
		return false
	}
	return true
}
func validKey(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) >= 8 && utf8.RuneCountInString(s) <= 64
}
func numericID(s string) bool {
	if s == "" || len(s) > 20 || s[0] == '0' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func cloneSets(m map[string]Set) map[string]Set {
	n := map[string]Set{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
func cloneKeys(m map[string]Key) map[string]Key {
	n := map[string]Key{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
func cloneEntries(m map[string]Exercise) map[string]Exercise {
	n := map[string]Exercise{}
	for k, v := range m {
		n[k] = v
	}
	return n
}
func cloneSession(s Session) Session {
	return Session{Status: s.Status, Sets: cloneSets(s.Sets), Keys: cloneKeys(s.Keys), Entries: cloneEntries(s.Entries)}
}
