package domain

import "testing"

func stringValue(value string) *string { return &value }
func intValue(value int) *int          { return &value }

func testV2Set(key string, measurement MeasurementV2) SetV2 {
	return SetV2{RecordKey: key, ExerciseEntryKey: "entry-0001", Exercise: Exercise{Source: Custom, Name: "深蹲"}, Index: 1, Measurement: measurement}
}

func TestV2IncompleteGroupsAreKeptAndNotTreatedAsZero(t *testing.T) {
	sets := []SetV2{
		testV2Set("record-001", MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("12"), Reps: intValue(12)}),
		testV2Set("record-002", MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("14"), Reps: intValue(10)}),
		testV2Set("record-003", MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("20")}),
		testV2Set("record-004", MeasurementV2{Method: MeasurementTotal}),
	}
	for i := range sets {
		sets[i].Index = i + 1
	}
	result, err := SummarizeV2(sets)
	if err != nil || result.Kilograms == nil || *result.Kilograms != "284.00" || result.FullSets != 2 || result.PartialSets != 0 || result.ExcludedSets != 2 {
		t.Fatalf("unexpected partial summary: %#v, %v", result, err)
	}
	blank, err := SummarizeV2(sets[2:])
	if err != nil || blank.Kilograms != nil || blank.ExcludedSets != 2 {
		t.Fatalf("missing input must not become 0: %#v, %v", blank, err)
	}
	zero, err := SummarizeV2([]SetV2{testV2Set("record-zero", MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("0"), Reps: intValue(8)})})
	if err != nil || zero.Kilograms == nil || *zero.Kilograms != "0.00" || zero.FullSets != 1 {
		t.Fatalf("explicit 0 and missing must remain distinct: %#v, %v", zero, err)
	}
}

func TestV2PairAndSeparateSidesUseExplicitMeaning(t *testing.T) {
	pair := testV2Set("record-pair", MeasurementV2{Method: MeasurementPairEach, WeightKg: stringValue("12"), Reps: intValue(10)})
	result, err := SummarizeV2([]SetV2{pair})
	if err != nil || result.Kilograms == nil || *result.Kilograms != "240.00" || result.FullSets != 1 {
		t.Fatalf("two implements: %#v, %v", result, err)
	}
	sides := testV2Set("record-side", MeasurementV2{Method: MeasurementSides,
		Left:  &SideMeasurementV2{State: SidePerformed, WeightKg: stringValue("12"), Reps: intValue(10)},
		Right: &SideMeasurementV2{State: SideNotPerformed, WeightKg: stringValue("10"), Reps: intValue(9)},
	})
	result, err = SummarizeV2([]SetV2{sides})
	if err != nil || result.Kilograms == nil || *result.Kilograms != "120.00" || result.FullSets != 1 {
		t.Fatalf("not-performed side counted: %#v, %v", result, err)
	}
	sides.Measurement.Right = nil
	result, err = SummarizeV2([]SetV2{sides})
	if err != nil || result.Kilograms == nil || *result.Kilograms != "120.00" || result.PartialSets != 1 {
		t.Fatalf("unknown side must remain partial: %#v, %v", result, err)
	}
}

func TestV2BodyweightExtraAssistanceAndDirectAreDistinct(t *testing.T) {
	cases := []struct {
		measurement MeasurementV2
		expected    string
	}{
		{MeasurementV2{Method: MeasurementBodyweight, BodyweightKg: stringValue("70"), Reps: intValue(10)}, "700.00"},
		{MeasurementV2{Method: MeasurementBodyweightExtra, BodyweightKg: stringValue("70"), AdjustmentKg: stringValue("10"), Reps: intValue(10)}, "800.00"},
		{MeasurementV2{Method: MeasurementBodyweightAssisted, BodyweightKg: stringValue("70"), AdjustmentKg: stringValue("20"), Reps: intValue(10)}, "500.00"},
		{MeasurementV2{Method: MeasurementDirect, WeightKg: stringValue("50"), Reps: intValue(10)}, "500.00"},
	}
	for _, tc := range cases {
		result, err := SummarizeV2([]SetV2{testV2Set("record-body", tc.measurement)})
		if err != nil || result.Kilograms == nil || *result.Kilograms != tc.expected || result.FullSets != 1 {
			t.Errorf("%s: %#v, %v", tc.measurement.Method, result, err)
		}
	}
	incomplete := testV2Set("record-body", MeasurementV2{Method: MeasurementBodyweightExtra, BodyweightKg: stringValue("70"), Reps: intValue(10)})
	result, err := SummarizeV2([]SetV2{incomplete})
	if err != nil || result.Kilograms != nil || result.ExcludedSets != 1 {
		t.Fatalf("missing extra weight: %#v, %v", result, err)
	}
}

func TestV2RejectsInvalidOrAmbiguousInputs(t *testing.T) {
	cases := []MeasurementV2{
		{Method: MeasurementTotal, WeightKg: stringValue("-1"), Reps: intValue(8)},
		{Method: MeasurementTotal, WeightKg: stringValue("12.345"), Reps: intValue(8)},
		{Method: MeasurementTotal, WeightKg: stringValue("12"), Reps: intValue(0)},
		{Method: MeasurementTotal, WeightKg: stringValue("12"), BodyweightKg: stringValue("70")},
		{Method: MeasurementSides, Left: &SideMeasurementV2{State: "invalid"}},
		{Method: MeasurementBodyweight, BodyweightKg: stringValue("0"), Reps: intValue(8)},
		{Method: MeasurementBodyweightAssisted, BodyweightKg: stringValue("70"), AdjustmentKg: stringValue("80"), Reps: intValue(8)},
		{Method: "unknown"},
	}
	for _, measurement := range cases {
		if _, err := SummarizeV2([]SetV2{testV2Set("record-bad", measurement)}); err == nil {
			t.Errorf("accepted invalid measurement: %#v", measurement)
		}
	}
}

func TestV2PreservesExistingVersionOneValidation(t *testing.T) {
	legacy := Set{RecordKey: "record-001", ExerciseEntryKey: "entry-0001", Exercise: Exercise{Source: Custom, Name: "深蹲"}, Index: 1, Kind: Weight, WeightKg: "12", Reps: 0}
	if ValidateSet(legacy) == nil {
		t.Fatal("legacy endpoint must not silently accept incomplete v2 groups")
	}
}

func TestV2BodyweightUpdateIsAtomicAndManualAdjustmentIsExplicit(t *testing.T) {
	sets := []SetV2{
		testV2Set("record-bw01", MeasurementV2{Method: MeasurementBodyweightExtra, BodyweightKg: stringValue("70"), AdjustmentKg: stringValue("10"), Reps: intValue(10)}),
		testV2Set("record-bw02", MeasurementV2{Method: MeasurementBodyweightAssisted, BodyweightKg: stringValue("70"), AdjustmentKg: stringValue("20"), Reps: intValue(10)}),
		testV2Set("record-bw03", MeasurementV2{Method: MeasurementDirect, WeightKg: stringValue("50"), Reps: intValue(10)}),
	}
	for i := range sets {
		sets[i].Index = i + 1
	}
	updated, err := UpdateBodyweightV2(sets, stringValue("70"), stringValue("72"), false)
	if err != nil || *updated[0].Measurement.BodyweightKg != "72" || *updated[1].Measurement.BodyweightKg != "72" || *updated[2].Measurement.WeightKg != "50" {
		t.Fatalf("bodyweight update without manual change: %#v, %v", updated, err)
	}
	if *sets[0].Measurement.BodyweightKg != "70" || *sets[2].Measurement.WeightKg != "50" {
		t.Fatal("source data was mutated")
	}
	*updated[0].Measurement.AdjustmentKg = "11"
	if *sets[0].Measurement.AdjustmentKg != "10" {
		t.Fatal("updated group shares mutable measurement data with the source")
	}
	updated, err = UpdateBodyweightV2(sets, stringValue("70"), stringValue("72"), true)
	if err != nil || *updated[2].Measurement.WeightKg != "52.00" {
		t.Fatalf("manual weight should move by selected bodyweight delta: %#v, %v", updated, err)
	}
	if _, err = UpdateBodyweightV2(sets, nil, stringValue("72"), true); err != ErrBodyweightBaselineRequired {
		t.Fatalf("missing original weight must not invent a delta: %v", err)
	}
	if *sets[0].Measurement.BodyweightKg != "70" || *sets[2].Measurement.WeightKg != "50" {
		t.Fatal("failed change mutated the original")
	}
}

func TestV2SummaryRejectsDuplicateIdentityOrSetPosition(t *testing.T) {
	first := testV2Set("record-001", MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("12"), Reps: intValue(10)})
	second := testV2Set("record-002", MeasurementV2{Method: MeasurementTotal, WeightKg: stringValue("14"), Reps: intValue(10)})
	if _, err := SummarizeV2([]SetV2{first, second}); err == nil {
		t.Fatal("duplicate set position accepted")
	}
	second.Index = 2
	second.RecordKey = first.RecordKey
	if _, err := SummarizeV2([]SetV2{first, second}); err == nil {
		t.Fatal("duplicate record key accepted")
	}
	second.RecordKey = "record-002"
	second.ExerciseEntryKey = "entry-0002"
	second.Index = 1
	if _, err := SummarizeV2([]SetV2{first, second}); err != nil {
		t.Fatalf("independent action entries were merged: %v", err)
	}
}
