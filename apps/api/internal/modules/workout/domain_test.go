package workout

import (
	"errors"
	"strings"
	"testing"
)

func validSet(key string) Set {
	return Set{RecordKey: key, ExerciseEntryKey: "entrykey1", Exercise: Exercise{Source: Custom, Name: "Squat"}, Index: 1, Kind: Weight, WeightKg: "12.50", Reps: 8}
}
func TestValidateSetMetricKinds(t *testing.T) {
	cases := []struct {
		name string
		set  Set
		ok   bool
	}{
		{"weight", validSet("record01"), true},
		{"per-side", func() Set { s := validSet("record02"); s.Kind = PerSide; return s }(), true},
		{"bodyweight", func() Set { s := validSet("record03"); s.Kind = Bodyweight; s.WeightKg = ""; return s }(), true},
		{"bodyweight-per-side", func() Set { s := validSet("record04"); s.Kind = BodyweightPerSide; s.WeightKg = ""; return s }(), true},
		{"breath", func() Set { s := validSet("record05"); s.Kind = Breath; s.WeightKg = ""; return s }(), true},
		{"duration", func() Set {
			s := validSet("record06")
			s.Kind = Duration
			s.WeightKg = ""
			s.Reps = 0
			s.Seconds = 5
			return s
		}(), true},
		{"duration-per-side", func() Set {
			s := validSet("record07")
			s.Kind = DurationPerSide
			s.WeightKg = ""
			s.Reps = 0
			s.Seconds = 600
			return s
		}(), true},
		{"weight precision", func() Set { s := validSet("record08"); s.WeightKg = "12.501"; return s }(), false},
		{"fraction", func() Set { s := validSet("record08"); s.WeightKg = "1/2"; return s }(), false},
		{"plus sign", func() Set { s := validSet("record08"); s.WeightKg = "+1"; return s }(), false},
		{"hex", func() Set { s := validSet("record08"); s.WeightKg = "0x10"; return s }(), false},
		{"exponent", func() Set { s := validSet("record08"); s.WeightKg = "1e2"; return s }(), false},
		{"extreme length", func() Set { s := validSet("record08"); s.WeightKg = strings.Repeat("9", 10000); return s }(), false},
		{"rpe precision", func() Set { s := validSet("record09"); s.RPE = "7.25"; return s }(), false},
		{"duration low", func() Set {
			s := validSet("record10")
			s.Kind = Duration
			s.WeightKg = ""
			s.Reps = 0
			s.Seconds = 4
			return s
		}(), false},
		{"wrong metric", func() Set { s := validSet("record11"); s.Seconds = 10; return s }(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSet(c.set)
			if (err == nil) != c.ok {
				t.Fatalf("ValidateSet() error=%v want valid=%v", err, c.ok)
			}
		})
	}
}
func TestApplyBatchAtomicRelationshipsAndStatus(t *testing.T) {
	base := Session{Status: Completed, Sets: map[string]Set{"record01": validSet("record01")}, Keys: map[string]Key{"record01": {ExerciseEntryKey: "entrykey1"}}, Entries: map[string]Exercise{"entrykey1": {Source: Custom, Name: "Squat"}}}
	bad := Batch{Sets: []Set{validSet("record02"), validSet("record02")}}
	if _, err := ApplyBatch(base, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate keys: %v", err)
	}
	if _, err := ApplyBatch(base, Batch{Sets: []Set{validSet("record01")}, DeleteKeys: []string{"record01"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("update/delete overlap: %v", err)
	}
	if _, err := ApplyBatch(base, Batch{Sets: []Set{validSet("record02")}, RestoreKeys: []string{"record02"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("restore nondeleted key: %v", err)
	}
	changedExercise := validSet("record02")
	changedExercise.Exercise.Name = "Bench press"
	if _, err := ApplyBatch(base, Batch{Sets: []Set{changedExercise}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("changing an existing exercise entry must be invalid input: %v", err)
	}
	if len(base.Sets) != 1 || base.Status != Completed {
		t.Fatal("failed batch mutated source session")
	}
	out, err := ApplyBatch(base, Batch{DeleteKeys: []string{"record01"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != InProgress || len(out.Sets) != 0 || !out.Keys["record01"].Deleted {
		t.Fatalf("last group removal: %+v", out)
	}
	if _, err := Complete(out); !errors.Is(err, ErrConflict) {
		t.Fatalf("empty completion: %v", err)
	}
	restored, err := ApplyBatch(out, Batch{Sets: []Set{validSet("record01")}, RestoreKeys: []string{"record01"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Sets) != 1 || restored.Keys["record01"].Deleted {
		t.Fatal("restore failed")
	}
	if _, err := ApplyBatch(Session{Status: Skipped}, Batch{Sets: []Set{validSet("record12")}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("skipped session accepted sets: %v", err)
	}
}
func TestApplyBatchLimitAndComplete(t *testing.T) {
	base := Session{Status: InProgress}
	sets := make([]Set, 201)
	for i := range sets {
		sets[i] = validSet("record-key-" + string(rune('a'+i%26)) + string(rune('A'+i/26)))
	}
	if _, err := ApplyBatch(base, Batch{Sets: sets}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over limit: %v", err)
	}
	s := Session{Status: InProgress, Sets: map[string]Set{"record01": validSet("record01")}}
	done, err := Complete(s)
	if err != nil || done.Status != Completed {
		t.Fatalf("complete=%+v err=%v", done, err)
	}
	if _, err := Complete(Session{Status: Status("unknown"), Sets: s.Sets}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown status completed: %v", err)
	}
}

func TestStableKeysCountUnicodeRunesAndRejectInvalidUTF8(t *testing.T) {
	base := validSet("记录键甲乙丙丁") // 7 runes, 21 bytes
	if err := ValidateSet(base); !errors.Is(err, ErrInvalid) {
		t.Fatalf("7-rune key accepted: %v", err)
	}
	base.RecordKey = "记录键甲乙丙丁戊"        // 8 runes
	base.ExerciseEntryKey = "条目键甲乙丙丁戊" // 8 runes
	if err := ValidateSet(base); err != nil {
		t.Fatalf("8-rune keys rejected: %v", err)
	}
	base.RecordKey = string([]byte{0xff, 'a', 'b', 'c', 'd', 'e', 'f', 'g'})
	if err := ValidateSet(base); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid UTF-8 key accepted: %v", err)
	}
}
