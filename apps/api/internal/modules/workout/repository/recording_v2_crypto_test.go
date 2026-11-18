package repository

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRecordingV2ProtectionBindsOwnerSessionAndVersion(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	p, err := newRecordingV2Protector(key, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"private":"深蹲 70kg"}`)
	sealed, err := p.seal(7, 42, 3, original)
	if err != nil || bytes.Contains(sealed, original) {
		t.Fatal("snapshot was not encrypted")
	}
	second, err := p.seal(7, 42, 3, original)
	if err != nil || bytes.Equal(sealed, second) {
		t.Fatal("nonces must be unpredictable and unique")
	}
	got, err := p.open(7, 42, 3, sealed)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("encrypted snapshot failed to round trip")
	}
	wrongKey, _ := newRecordingV2Protector(bytes.Repeat([]byte{8}, 32), "test-key")
	wrongID, _ := newRecordingV2Protector(key, "other-key")
	tampered := append([]byte{}, sealed...)
	tampered[len(tampered)-1] ^= 1
	for i, test := range []struct {
		p                       *recordingV2Protector
		owner, session, version uint64
		ciphertext              []byte
	}{
		{p, 8, 42, 3, sealed}, {p, 7, 43, 3, sealed}, {p, 7, 42, 4, sealed},
		{wrongKey, 7, 42, 3, sealed}, {wrongID, 7, 42, 3, sealed}, {p, 7, 42, 3, tampered},
		{p, 7, 42, 3, sealed[:8]}, {p, 0, 42, 3, sealed},
	} {
		if _, err := test.p.open(test.owner, test.session, test.version, test.ciphertext); err == nil {
			t.Fatalf("accepted invalid case %d", i)
		}
	}
	if _, err := p.seal(7, 42, 0, original); err == nil {
		t.Fatal("accepted missing revision")
	}
	if _, err := newRecordingV2Protector(key[:31], "test-key"); err == nil {
		t.Fatal("accepted short key")
	}
	if _, err := newRecordingV2Protector(key, "invalid key"); err == nil {
		t.Fatal("accepted invalid key id")
	}
	if _, err := json.Marshal(p); err == nil {
		t.Fatal("protector must not be serialized")
	}
	if strings.Contains(fmt.Sprintf("%#v", p), "test-key") {
		t.Fatal("protector details leaked to logs")
	}
}

func TestRecordingV2VersionFitsAllClients(t *testing.T) {
	if next, err := nextRecordingV2Version(9007199254740990); err != nil || next != 9007199254740991 {
		t.Fatal("largest representable version rejected")
	}
	if _, err := nextRecordingV2Version(9007199254740991); err == nil {
		t.Fatal("unrepresentable client version accepted")
	}
	if _, err := nextRecordingV2Version(0); err == nil {
		t.Fatal("missing version accepted")
	}
}
