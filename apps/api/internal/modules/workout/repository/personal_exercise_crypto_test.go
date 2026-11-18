package repository

import (
	"bytes"
	"testing"

	"nerae/api/internal/modules/workout/domain"
)

func TestPersonalExerciseProtectionBindsOwnerIdentityAndVersion(t *testing.T) {
	p, err := newPersonalExerciseProtector(bytes.Repeat([]byte{7}, 32), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	value := domain.PersonalExercise{ID: "97ab0210c56f48ae930de1629b761f09", Version: 1, Name: "深蹲",
		MuscleGroups: []string{"腿部"}, Equipment: []string{"杠铃"}}
	first, err := p.seal(1, value)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.seal(1, value)
	if err != nil || bytes.Equal(first, second) || bytes.Contains(first, []byte(value.Name)) {
		t.Fatal("encryption must be randomized and hide the name")
	}
	got, err := p.open(1, value.ID, value.Version, first)
	if err != nil || got.Name != value.Name || got.ID != value.ID || got.Version != value.Version {
		t.Fatal("round trip failed")
	}
	for _, tc := range []struct {
		owner, version uint64
		id             string
	}{
		{2, 1, value.ID}, {1, 2, value.ID}, {1, 1, "97ab0210c56f48ae930de1629b761f08"},
	} {
		if _, err := p.open(tc.owner, tc.id, tc.version, first); err == nil {
			t.Fatal("ciphertext accepted under another owner, id or version")
		}
	}
	corrupt := append([]byte(nil), first...)
	corrupt[len(corrupt)-1] ^= 1
	if _, err := p.open(1, value.ID, 1, corrupt); err == nil {
		t.Fatal("corrupt ciphertext accepted")
	}
}

func TestPersonalExerciseNameHashIsOwnerScopedAndCanonical(t *testing.T) {
	p, err := newPersonalExerciseProtector(bytes.Repeat([]byte{7}, 32), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.nameHash(1, "Café")
	b, err2 := p.nameHash(1, "CAFE\u0301")
	c, err3 := p.nameHash(2, "Café")
	if err != nil || err2 != nil || err3 != nil || !bytes.Equal(a, b) || bytes.Equal(a, c) {
		t.Fatal("name hash must be canonical and owner-scoped")
	}
}
