package domain

import (
	"errors"
	"log/slog"
	"math/big"
	"unicode/utf8"
)

// The v2 model is an internal candidate, not an exposed HTTP or database format.
// Values remain pointers so an omitted measurement cannot become a recorded zero.
type MeasurementMethodV2 string

const (
	MeasurementTotal              MeasurementMethodV2 = "total"
	MeasurementPairEach           MeasurementMethodV2 = "pair_each"
	MeasurementSides              MeasurementMethodV2 = "sides"
	MeasurementBodyweight         MeasurementMethodV2 = "bodyweight"
	MeasurementBodyweightExtra    MeasurementMethodV2 = "bodyweight_extra"
	MeasurementBodyweightAssisted MeasurementMethodV2 = "bodyweight_assisted"
	MeasurementDirect             MeasurementMethodV2 = "direct"
)

type SideStateV2 string

const (
	SideUnknown      SideStateV2 = "unknown"
	SidePerformed    SideStateV2 = "performed"
	SideNotPerformed SideStateV2 = "not_performed"
)

type SideMeasurementV2 struct {
	State    SideStateV2
	WeightKg *string
	Reps     *int
}

type MeasurementV2 struct {
	Method       MeasurementMethodV2
	WeightKg     *string
	Reps         *int
	BodyweightKg *string
	AdjustmentKg *string
	Left, Right  *SideMeasurementV2
}

type SetV2 struct {
	RecordKey, ExerciseEntryKey string
	Exercise                    Exercise
	Index                       int
	RPE                         *string
	// Confirmed records an optional per-set action, not a prerequisite for completion.
	Confirmed   bool
	Measurement MeasurementV2
}

// Internal values may include health data and action names. Transport must
// construct a separately authorized response instead of serializing them.
func (SetV2) String() string               { return "SetV2{redacted}" }
func (SetV2) GoString() string             { return "SetV2{redacted}" }
func (SetV2) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (SetV2) LogValue() slog.Value         { return slog.StringValue("SetV2{redacted}") }

func (MeasurementV2) String() string               { return "MeasurementV2{redacted}" }
func (MeasurementV2) GoString() string             { return "MeasurementV2{redacted}" }
func (MeasurementV2) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (MeasurementV2) LogValue() slog.Value         { return slog.StringValue("MeasurementV2{redacted}") }

func (SideMeasurementV2) String() string               { return "SideMeasurementV2{redacted}" }
func (SideMeasurementV2) GoString() string             { return "SideMeasurementV2{redacted}" }
func (SideMeasurementV2) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (SideMeasurementV2) LogValue() slog.Value {
	return slog.StringValue("SideMeasurementV2{redacted}")
}

// Kilograms is nil when no contribution can be calculated. It can explicitly
// be "0.00" when a measured zero, rather than a missing value, was supplied.
type VolumeSummaryV2 struct {
	Kilograms                           *string
	FullSets, PartialSets, ExcludedSets int
}

var ErrBodyweightBaselineRequired = errors.New("original and updated bodyweight are required to adjust manual weights")

func ValidateSetV2(set SetV2) error {
	if !validKey(set.RecordKey) || !validKey(set.ExerciseEntryKey) || set.Index < 1 ||
		validateExerciseV2(set.Exercise) != nil || !utf8.ValidString(set.Exercise.Name) {
		return ErrInvalid
	}
	if set.RPE != nil && !decimal(*set.RPE, "1", "10", 1, false) {
		return ErrInvalid
	}
	return validateMeasurementV2(set.Measurement)
}

func validV2Weight(value *string, positive bool) bool {
	return value == nil || decimal(*value, "0", "999999.99", 2, positive)
}

func validV2Reps(value *int) bool {
	return value == nil || (*value >= 1 && *value <= 100)
}

func validateSideV2(side *SideMeasurementV2) bool {
	if side == nil {
		return true
	}
	if side.State != SideUnknown && side.State != SidePerformed && side.State != SideNotPerformed {
		return false
	}
	return validV2Weight(side.WeightKg, false) && validV2Reps(side.Reps)
}

func validateMeasurementV2(value MeasurementV2) error {
	if !validV2Weight(value.WeightKg, false) || !validV2Weight(value.BodyweightKg, true) ||
		!validV2Weight(value.AdjustmentKg, false) || !validV2Reps(value.Reps) ||
		!validateSideV2(value.Left) || !validateSideV2(value.Right) {
		return ErrInvalid
	}
	switch value.Method {
	case MeasurementTotal, MeasurementPairEach, MeasurementDirect:
		if value.BodyweightKg != nil || value.AdjustmentKg != nil || value.Left != nil || value.Right != nil {
			return ErrInvalid
		}
	case MeasurementSides:
		if value.WeightKg != nil || value.Reps != nil || value.BodyweightKg != nil || value.AdjustmentKg != nil {
			return ErrInvalid
		}
	case MeasurementBodyweight:
		if value.WeightKg != nil || value.AdjustmentKg != nil || value.Left != nil || value.Right != nil {
			return ErrInvalid
		}
	case MeasurementBodyweightExtra, MeasurementBodyweightAssisted:
		if value.WeightKg != nil || value.Left != nil || value.Right != nil {
			return ErrInvalid
		}
		if value.Method == MeasurementBodyweightAssisted && value.BodyweightKg != nil && value.AdjustmentKg != nil &&
			weightRatV2(*value.AdjustmentKg).Cmp(weightRatV2(*value.BodyweightKg)) > 0 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// weightRatV2 is only called after validation has accepted the decimal token.
func weightRatV2(value string) *big.Rat {
	result, _ := new(big.Rat).SetString(value)
	return result
}

func sideVolumeV2(side *SideMeasurementV2) (volume *big.Rat, incomplete bool) {
	if side == nil || side.State == SideUnknown {
		return nil, true
	}
	if side.State == SideNotPerformed {
		return nil, false
	}
	if side.WeightKg == nil || side.Reps == nil {
		return nil, true
	}
	return new(big.Rat).Mul(weightRatV2(*side.WeightKg), big.NewRat(int64(*side.Reps), 1)), false
}

func volumeV2(value MeasurementV2) (volume *big.Rat, incomplete bool) {
	if value.Method == MeasurementSides {
		left, leftIncomplete := sideVolumeV2(value.Left)
		right, rightIncomplete := sideVolumeV2(value.Right)
		if left == nil && right == nil {
			return nil, leftIncomplete || rightIncomplete
		}
		result := new(big.Rat)
		if left != nil {
			result.Add(result, left)
		}
		if right != nil {
			result.Add(result, right)
		}
		return result, leftIncomplete || rightIncomplete
	}
	if value.Reps == nil {
		return nil, true
	}
	var weight *big.Rat
	switch value.Method {
	case MeasurementTotal, MeasurementDirect, MeasurementPairEach:
		if value.WeightKg == nil {
			return nil, true
		}
		weight = weightRatV2(*value.WeightKg)
		if value.Method == MeasurementPairEach {
			weight.Mul(weight, big.NewRat(2, 1))
		}
	case MeasurementBodyweight:
		if value.BodyweightKg == nil {
			return nil, true
		}
		weight = weightRatV2(*value.BodyweightKg)
	case MeasurementBodyweightExtra, MeasurementBodyweightAssisted:
		if value.BodyweightKg == nil || value.AdjustmentKg == nil {
			return nil, true
		}
		weight = weightRatV2(*value.BodyweightKg)
		adjustment := weightRatV2(*value.AdjustmentKg)
		if value.Method == MeasurementBodyweightExtra {
			weight.Add(weight, adjustment)
		} else {
			weight.Sub(weight, adjustment)
		}
	default:
		return nil, true
	}
	return weight.Mul(weight, big.NewRat(int64(*value.Reps), 1)), false
}

// SummarizeV2 adds only recorded/calculable contributions using exact decimal
// arithmetic. It never silently counts a missing measurement as zero.
func SummarizeV2(sets []SetV2) (VolumeSummaryV2, error) {
	var result VolumeSummaryV2
	total := new(big.Rat)
	measured := false
	seenKeys := make(map[string]bool, len(sets))
	seenIndexes := make(map[string]map[int]bool)
	for _, set := range sets {
		if ValidateSetV2(set) != nil || seenKeys[set.RecordKey] {
			return VolumeSummaryV2{}, ErrInvalid
		}
		if seenIndexes[set.ExerciseEntryKey] == nil {
			seenIndexes[set.ExerciseEntryKey] = make(map[int]bool)
		}
		if seenIndexes[set.ExerciseEntryKey][set.Index] {
			return VolumeSummaryV2{}, ErrInvalid
		}
		seenKeys[set.RecordKey] = true
		seenIndexes[set.ExerciseEntryKey][set.Index] = true
		volume, incomplete := volumeV2(set.Measurement)
		if volume == nil {
			result.ExcludedSets++
			continue
		}
		total.Add(total, volume)
		measured = true
		if incomplete {
			result.PartialSets++
		} else {
			result.FullSets++
		}
	}
	if measured {
		value := total.FloatString(2)
		result.Kilograms = &value
	}
	return result, nil
}

// UpdateBodyweightV2 applies one explicit same-session decision atomically.
// Manual effective weights change only when requested and when both values
// needed to calculate a delta are known. Neither input nor stored pointers are
// mutated; persistence and the optional body-metric entry are separate concerns.
func UpdateBodyweightV2(sets []SetV2, previous, next *string, adjustManual bool) ([]SetV2, error) {
	if !validV2Weight(previous, true) || !validV2Weight(next, true) {
		return nil, ErrInvalid
	}
	if _, err := SummarizeV2(sets); err != nil {
		return nil, err
	}
	var delta *big.Rat
	if adjustManual {
		for _, set := range sets {
			if set.Measurement.Method == MeasurementDirect && set.Measurement.WeightKg != nil {
				if previous == nil || next == nil {
					return nil, ErrBodyweightBaselineRequired
				}
				delta = new(big.Rat).Sub(weightRatV2(*next), weightRatV2(*previous))
				break
			}
		}
	}
	result := make([]SetV2, len(sets))
	for i, set := range sets {
		result[i] = cloneSetV2(set)
	}
	for i := range result {
		measurement := result[i].Measurement
		switch measurement.Method {
		case MeasurementBodyweight, MeasurementBodyweightExtra, MeasurementBodyweightAssisted:
			if next == nil {
				measurement.BodyweightKg = nil
			} else {
				value := *next
				measurement.BodyweightKg = &value
			}
		case MeasurementDirect:
			if delta != nil && measurement.WeightKg != nil {
				value := new(big.Rat).Add(weightRatV2(*measurement.WeightKg), delta).FloatString(2)
				measurement.WeightKg = &value
			}
		}
		result[i].Measurement = measurement
	}
	if _, err := SummarizeV2(result); err != nil {
		return nil, err
	}
	return result, nil
}
