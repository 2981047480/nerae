package domain

import (
	"errors"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// PersonalExercise is owned by one account. Its stable ID is independent of
// the name frozen into a training record.
type PersonalExercise struct {
	ID           string
	Version      uint64
	Name         string
	MuscleGroups []string
	Equipment    []string
}

var ErrPersonalExercise = errors.New("invalid personal exercise")

func (PersonalExercise) String() string               { return "PersonalExercise{redacted}" }
func (PersonalExercise) GoString() string             { return "PersonalExercise{redacted}" }
func (PersonalExercise) MarshalJSON() ([]byte, error) { return nil, ErrPersonalExercise }
func (PersonalExercise) LogValue() slog.Value {
	return slog.StringValue("PersonalExercise{redacted}")
}

// PersonalExerciseNameKey is used only for equality; the original spelling
// remains available for display and historical snapshots.
func PersonalExerciseNameKey(value string) (string, error) {
	return personalExerciseTextKey(value, 60)
}

func personalExerciseTextKey(value string, maximum int) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || value == "" || utf8.RuneCountInString(value) > maximum {
		return "", ErrPersonalExercise
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", ErrPersonalExercise
		}
	}
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(value))), nil
}

func personalExerciseTags(tags []string) ([]string, error) {
	if len(tags) > 10 {
		return nil, ErrPersonalExercise
	}
	result := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		key, err := personalExerciseTextKey(tag, 64)
		if err != nil || seen[key] {
			return nil, ErrPersonalExercise
		}
		seen[key] = true
		result = append(result, tag)
	}
	return result, nil
}

// NormalizePersonalExercise validates the untrusted input without changing its
// identity or version. The caller owns authorization and persistence.
func NormalizePersonalExercise(value PersonalExercise) (PersonalExercise, error) {
	if len(value.ID) != 32 {
		return PersonalExercise{}, ErrPersonalExercise
	}
	zero := true
	for _, c := range value.ID {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return PersonalExercise{}, ErrPersonalExercise
		}
		if c != '0' {
			zero = false
		}
	}
	if zero {
		return PersonalExercise{}, ErrPersonalExercise
	}
	if _, err := PersonalExerciseNameKey(value.Name); err != nil {
		return PersonalExercise{}, err
	}
	muscle, err := personalExerciseTags(value.MuscleGroups)
	if err != nil {
		return PersonalExercise{}, err
	}
	equipment, err := personalExerciseTags(value.Equipment)
	if err != nil {
		return PersonalExercise{}, err
	}
	value.Name = strings.TrimSpace(value.Name)
	value.MuscleGroups = muscle
	value.Equipment = equipment
	return value, nil
}
