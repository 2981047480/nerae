package domain

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// This test is supplied by a Go overlay; the fixture has an ephemeral software
// test key only, never a phone key or user data.
func TestIOSDeviceProofInterop(t *testing.T) {
	data, err := os.ReadFile(os.Getenv("NERAE_DEVICE_PROOF_FIXTURE"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Key        DevicePublicJWK `json:"key"`
		Proof      string          `json:"proof"`
		ResourceID string          `json:"resourceId"`
		Nonce      string          `json:"nonce"`
		IAT        int64           `json:"iat"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	expected := ProofExpectation{Purpose: "email_login", ResourceID: fixture.ResourceID, Nonce: fixture.Nonce}
	policy := ProofPolicy{MaxLifetime: time.Minute, ClockSkew: 0}
	now := time.Unix(fixture.IAT+1, 0)
	verified, err := VerifyDeviceProof(fixture.Proof, fixture.Key, expected, now, policy)
	if err != nil || len(verified.JTI) != 43 || !verified.ExpiresAt.Equal(time.Unix(fixture.IAT+60, 0)) {
		t.Fatal("valid Swift proof rejected or claims differ")
	}
	wrongPurpose := expected
	wrongPurpose.Purpose = "approval_exchange"
	wrongNonce := expected
	wrongNonce.Nonce += "x"
	for _, item := range []ProofExpectation{wrongPurpose, wrongNonce} {
		if _, err := VerifyDeviceProof(fixture.Proof, fixture.Key, item, now, policy); !errors.Is(err, ErrInvalidDeviceProof) {
			t.Fatal("incorrect transaction binding accepted")
		}
	}
	parts := strings.Split(fixture.Proof, ".")
	first := "A"
	if parts[1][0] == 'A' {
		first = "B"
	}
	parts[1] = first + parts[1][1:]
	if _, err := VerifyDeviceProof(strings.Join(parts, "."), fixture.Key, expected, now, policy); !errors.Is(err, ErrInvalidDeviceProof) {
		t.Fatal("altered proof accepted")
	}
	if _, err := VerifyDeviceProof(fixture.Proof, fixture.Key, expected, time.Unix(fixture.IAT+60, 0), policy); !errors.Is(err, ErrInvalidDeviceProof) {
		t.Fatal("expired proof accepted")
	}
}
