package domain

import (
	"errors"
	"testing"
)

func orderedSet(record, entry string, index int, name string) Set {
	return Set{RecordKey: record, ExerciseEntryKey: entry, Exercise: Exercise{Source: Custom, Name: name}, Index: index, Kind: Weight, WeightKg: "10", Reps: 5}
}
func orderedSession(sets ...Set) Session {
	s := Session{Status: InProgress, Sets: map[string]Set{}, Keys: map[string]Key{}, Entries: map[string]Exercise{}}
	for _, set := range sets {
		s.Sets[set.RecordKey] = set
		s.Keys[set.RecordKey] = Key{ExerciseEntryKey: set.ExerciseEntryKey}
		s.Entries[set.ExerciseEntryKey] = set.Exercise
	}
	return s
}

func TestApplyOrderedBatchReorderAndPartialSetUpdate(t *testing.T) {
	a1, a2 := orderedSet("record01", "entrykeyA", 1, "Squat"), orderedSet("record02", "entrykeyA", 2, "Squat")
	b1 := orderedSet("record03", "entrykeyB", 1, "Press")
	cur := orderedSession(a1, a2, b1)
	out, order, err := ApplyOrderedBatch(cur, []string{"entrykeyA", "entrykeyB"}, Batch{Sets: []Set{func() Set { s := a1; s.Index = 2; return s }(), func() Set { s := a2; s.Index = 1; return s }()}}, []string{"entrykeyB", "entrykeyA"})
	if err != nil {
		t.Fatal(err)
	}
	if order[0] != "entrykeyB" || out.Sets["record01"].Index != 2 || out.Sets["record02"].Index != 1 {
		t.Fatalf("unexpected result: order=%v sets=%v", order, out.Sets)
	}
	if cur.Sets["record01"].Index != 1 {
		t.Fatal("input snapshot mutated")
	}
}

func TestApplyOrderedBatchRejectsIncompleteOrDuplicateOrder(t *testing.T) {
	cur := orderedSession(orderedSet("record01", "entrykeyA", 1, "A"), orderedSet("record02", "entrykeyB", 1, "B"))
	for name, order := range map[string][]string{"nil": nil, "empty": {}, "missing": {"entrykeyA"}, "extra": {"entrykeyA", "entrykeyB", "entrykeyC"}, "duplicate": {"entrykeyA", "entrykeyA"}, "case-sensitive": {"entrykeyA", "EntrykeyB"}} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ApplyOrderedBatch(cur, []string{"entrykeyA", "entrykeyB"}, Batch{}, order); !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid order, got %v", err)
			}
		})
	}
}

func TestApplyOrderedBatchAllowsEmptySessionAndSortOnly(t *testing.T) {
	cur := orderedSession()
	out, order, err := ApplyOrderedBatch(cur, []string{}, Batch{}, []string{})
	if err != nil || len(order) != 0 || len(out.Sets) != 0 {
		t.Fatalf("empty order: %v %v", order, err)
	}
	cur = orderedSession(orderedSet("record01", "entrykeyA", 1, "A"), orderedSet("record02", "entrykeyB", 1, "B"))
	out, order, err = ApplyOrderedBatch(cur, []string{"entrykeyA", "entrykeyB"}, Batch{}, []string{"entrykeyB", "entrykeyA"})
	if err != nil || order[0] != "entrykeyB" || len(out.Sets) != 2 {
		t.Fatalf("sort only: %v %v", order, err)
	}
}

func TestApplyOrderedBatchDeletesAndRestoresWithExplicitOrder(t *testing.T) {
	a := orderedSet("record01", "entrykeyA", 1, "A")
	b := orderedSet("record02", "entrykeyB", 1, "B")
	cur := orderedSession(a, b)
	deleted, order, err := ApplyOrderedBatch(cur, []string{"entrykeyA", "entrykeyB"}, Batch{DeleteKeys: []string{"record01"}}, []string{"entrykeyB"})
	if err != nil || len(deleted.Sets) != 1 || order[0] != "entrykeyB" {
		t.Fatalf("delete: %v %v", order, err)
	}
	restored, order, err := ApplyOrderedBatch(deleted, []string{"entrykeyB"}, Batch{Sets: []Set{a}, RestoreKeys: []string{"record01"}}, []string{"entrykeyA", "entrykeyB"})
	if err != nil || len(restored.Sets) != 2 || order[0] != "entrykeyA" {
		t.Fatalf("restore: %v %v", order, err)
	}
}

func TestApplyOrderedBatchSnapshotAndIndexValidation(t *testing.T) {
	a := orderedSet("record01", "entrykeyA", 1, "A")
	b := orderedSet("record02", "entrykeyA", 1, "A")
	cur := orderedSession(a, b)
	if _, _, err := ApplyOrderedBatch(cur, []string{"entrykeyA"}, Batch{}, []string{"entrykeyA"}); !errors.Is(err, ErrInvalidOrderedSnapshot) {
		t.Fatalf("duplicate index accepted: %v", err)
	}
	b.Index = 2
	cur = orderedSession(a, b, orderedSet("record03", "entrykeyB", 1, "B"))
	if _, _, err := ApplyOrderedBatch(cur, []string{"entrykeyA", "entrykeyB"}, Batch{}, []string{"entrykeyA", "entrykeyB"}); err != nil {
		t.Fatalf("different entries may share index: %v", err)
	}
	bad := orderedSession(a)
	bad.Status = Status("unknown")
	if _, _, err := ApplyOrderedBatch(bad, []string{"entrykeyA"}, Batch{}, []string{"entrykeyA"}); !errors.Is(err, ErrInvalidOrderedSnapshot) {
		t.Fatalf("unknown status accepted: %v", err)
	}
}

func TestApplyOrderedBatchProjectedIndexAndSameNameEntries(t *testing.T) {
	a := orderedSet("record01", "entrykeyA", 1, "Same name")
	b := orderedSet("record02", "entrykeyA", 2, "Same name")
	c := orderedSet("record03", "entrykeyB", 1, "Same name")
	cur := orderedSession(a, b, c)
	// Equal names do not collapse distinct entry keys.
	if _, order, err := ApplyOrderedBatch(cur, []string{"entrykeyA", "entrykeyB"}, Batch{}, []string{"entrykeyB", "entrykeyA"}); err != nil || len(order) != 2 {
		t.Fatalf("same-name entries must remain distinct: order=%v err=%v", order, err)
	}
	b.Index = 1
	if _, _, err := ApplyOrderedBatch(cur, []string{"entrykeyA", "entrykeyB"}, Batch{Sets: []Set{b}}, []string{"entrykeyA", "entrykeyB"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("projected duplicate setIndex accepted: %v", err)
	}
}

func TestApplyOrderedBatchRejectsDamagedPersistedSnapshots(t *testing.T) {
	a := orderedSet("record01", "entrykeyA", 1, "A")
	base := orderedSession(a)
	tests := map[string]func(*Session){
		"completed without groups": func(s *Session) {
			s.Status = Completed
			s.Sets = map[string]Set{}
			s.Keys = map[string]Key{}
			s.Entries = map[string]Exercise{}
		},
		"orphan active key":        func(s *Session) { s.Keys["record02"] = Key{ExerciseEntryKey: "entrykeyA"} },
		"deleted key still active": func(s *Session) { s.Keys["record01"] = Key{ExerciseEntryKey: "entrykeyA", Deleted: true} },
		"deleted key missing entry": func(s *Session) {
			delete(s.Entries, "entrykeyA")
			delete(s.Sets, "record01")
			s.Keys["record01"] = Key{ExerciseEntryKey: "entrykeyA", Deleted: true}
		},
	}
	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			s := cloneSession(base)
			corrupt(&s)
			currentOrder, targetOrder := []string{"entrykeyA"}, []string{"entrykeyA"}
			if name == "completed without groups" || name == "deleted key missing entry" {
				currentOrder, targetOrder = []string{}, []string{}
			}
			if _, _, err := ApplyOrderedBatch(s, currentOrder, Batch{}, targetOrder); !errors.Is(err, ErrInvalidOrderedSnapshot) {
				t.Fatalf("expected invalid snapshot error, got %v", err)
			}
		})
	}
}

func TestApplyOrderedBatchRejectsInvalidHistoricalExerciseBeforeRestore(t *testing.T) {
	set := orderedSet("record01", "entrykeyA", 1, "A")
	cur := orderedSession(set)
	delete(cur.Sets, set.RecordKey)
	cur.Keys[set.RecordKey] = Key{ExerciseEntryKey: set.ExerciseEntryKey, Deleted: true}
	cur.Entries[set.ExerciseEntryKey] = Exercise{Source: Source("invalid")}
	_, _, err := ApplyOrderedBatch(cur, []string{}, Batch{Sets: []Set{set}, RestoreKeys: []string{set.RecordKey}}, []string{set.ExerciseEntryKey})
	if !errors.Is(err, ErrInvalidOrderedSnapshot) {
		t.Fatalf("invalid historical exercise must be snapshot error before restore, got %v", err)
	}
}

func TestApplyOrderedBatchSnapshotAndRequestErrorClassification(t *testing.T) {
	valid := orderedSession(orderedSet("record01", "entrykeyA", 1, "A"))
	if _, _, err := ApplyOrderedBatch(valid, nil, Batch{}, []string{"entrykeyA"}); !errors.Is(err, ErrInvalidOrderedSnapshot) {
		t.Fatalf("nil current order must be snapshot error, got %v", err)
	}
	invalidStatus := cloneSession(valid)
	invalidStatus.Status = Status("unknown")
	if _, _, err := ApplyOrderedBatch(invalidStatus, []string{"entrykeyA"}, Batch{}, []string{"entrykeyA"}); !errors.Is(err, ErrInvalidOrderedSnapshot) {
		t.Fatalf("invalid persisted status must be snapshot error, got %v", err)
	}
	if _, _, err := ApplyOrderedBatch(valid, []string{"entrykeyA"}, Batch{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil target order must be request error, got %v", err)
	}
}

func TestApplyOrderedBatchEntryLimitAndNoPartialMutation(t *testing.T) {
	sets := make([]Set, 2001)
	cur := Session{Status: InProgress, Sets: map[string]Set{}, Keys: map[string]Key{}, Entries: map[string]Exercise{}}
	order := make([]string, 2001)
	for i := range sets {
		entry := "entry" + keySuffix(i)
		sets[i] = orderedSet("record"+keySuffix(i), entry, 1, "Lift")
		cur.Sets[sets[i].RecordKey] = sets[i]
		cur.Keys[sets[i].RecordKey] = Key{ExerciseEntryKey: entry}
		cur.Entries[entry] = sets[i].Exercise
		order[i] = entry
	}
	if _, _, err := ApplyOrderedBatch(cur, order, Batch{}, order); !errors.Is(err, ErrInvalidOrderedSnapshot) {
		t.Fatalf("2001 entries accepted: %v", err)
	}
	delete(cur.Sets, sets[2000].RecordKey)
	delete(cur.Keys, sets[2000].RecordKey)
	delete(cur.Entries, sets[2000].ExerciseEntryKey)
	before := len(cur.Sets)
	badOrder := append(append([]string{}, order[:1999]...), "missing-entry")
	if _, _, err := ApplyOrderedBatch(cur, order[:2000], Batch{DeleteKeys: []string{"record" + keySuffix(0)}}, badOrder); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid target accepted: %v", err)
	}
	if len(cur.Sets) != before {
		t.Fatal("invalid ordered batch mutated original")
	}
	if _, _, err := ApplyOrderedBatch(cur, order[:2000], Batch{}, order[:2000]); err != nil {
		t.Fatalf("2000 entries rejected: %v", err)
	}
}

func keySuffix(i int) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	for j := 7; j >= 0; j-- {
		out[j] = digits[i&15]
		i >>= 4
	}
	return string(out)
}
