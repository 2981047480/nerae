package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPersonalExerciseNormalizeAndIdentity(t *testing.T) {
	input := PersonalExercise{ID: "97ab0210c56f48ae930de1629b761f09", Name: "  深蹲  ", MuscleGroups: []string{" 腿部 "}}
	got, err := NormalizePersonalExercise(input)
	if err != nil || got.Name != "深蹲" || got.ID != input.ID || len(got.MuscleGroups) != 1 || got.MuscleGroups[0] != "腿部" || got.Equipment == nil {
		t.Fatalf("normalization failed: %#v %v", got, err)
	}
	if input.Name != "  深蹲  " || input.MuscleGroups[0] != " 腿部 " {
		t.Fatal("mutated caller input")
	}
	first, err := PersonalExerciseNameKey(" Café ")
	second, err2 := PersonalExerciseNameKey("CAFE\u0301")
	if err != nil || err2 != nil || first != second {
		t.Fatal("case and canonical equivalents must collide")
	}
	if _, err := json.Marshal(got); err == nil || strings.Contains(got.String(), got.Name) {
		t.Fatal("private data must not be formatted or marshalled by accident")
	}
}

func TestPersonalExerciseRejectsInvalidInputs(t *testing.T) {
	base := PersonalExercise{ID: "97ab0210c56f48ae930de1629b761f09", Name: "划船"}
	tests := []PersonalExercise{
		{ID: "", Name: base.Name},
		{ID: strings.Repeat("0", 32), Name: base.Name},
		{ID: strings.ToUpper(base.ID), Name: base.Name},
		{ID: base.ID, Name: "  "},
		{ID: base.ID, Name: strings.Repeat("动", 61)},
		{ID: base.ID, Name: "深\n蹲"},
		{ID: base.ID, Name: string([]byte{0xff})},
		{ID: base.ID, Name: base.Name, Equipment: []string{" 哑铃 ", "哑铃"}},
		{ID: base.ID, Name: base.Name, MuscleGroups: make([]string, 11)},
	}
	for i, value := range tests {
		if _, err := NormalizePersonalExercise(value); err == nil {
			t.Errorf("case %d accepted invalid input", i)
		}
	}
}
