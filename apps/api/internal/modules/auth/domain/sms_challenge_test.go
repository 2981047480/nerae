package domain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("private random source detail") }

func TestChallengeMaterialUsesSecureSourceAndExpectedLengths(t *testing.T) {
	id, err := GenerateChallengeID()
	if err != nil || !validChallengeID(id) {
		t.Fatalf("challenge ID invalid: %v", err)
	}
	nonce, err := GenerateChallengeNonce()
	if err != nil || len(nonce) != 43 {
		t.Fatalf("challenge nonce invalid: %v", err)
	}
	for _, digits := range []int{6, 10} {
		code, err := randomSMSCode(bytes.NewReader(make([]byte, 32)), digits)
		if err != nil || len(code) != digits || !validSMSCode(code) {
			t.Fatalf("code length %d invalid: %v", digits, err)
		}
	}
	if _, err := randomSMSCode(failedReader{}, 6); !errors.Is(err, ErrInvalidSMSChallenge) || strings.Contains(err.Error(), "private") {
		t.Fatalf("random failure must not expose details: %v", err)
	}
	if _, err := randomOpaque(failedReader{}, 24); !errors.Is(err, ErrInvalidSMSChallenge) {
		t.Fatalf("opaque ID source failure not rejected: %v", err)
	}
	for _, digits := range []int{5, 11} {
		if _, err := GenerateSMSCode(digits); !errors.Is(err, ErrInvalidSMSChallenge) {
			t.Fatalf("invalid code length accepted: %v", err)
		}
	}
}

func TestSMSDigestIsBoundToChallengeAndPurpose(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	hasher, err := NewSMSCodeHasher(key)
	if err != nil {
		t.Fatal(err)
	}
	challenge := "challenge-abcdefghijklmn"
	digest, err := hasher.Digest(challenge, "phone_login", "001234")
	if err != nil || len(digest) != 32 {
		t.Fatalf("digest failed: %v", err)
	}
	key[0] ^= 1 // the hasher must not share the caller's mutable key buffer
	verified, err := hasher.Verify(challenge, "phone_login", "001234", digest)
	if err != nil || !verified {
		t.Fatalf("correct code not verified: %v", err)
	}
	for _, candidate := range []struct{ id, purpose, code string }{
		{challenge, "phone_login", "001235"},
		{"challenge-abcdefghijklmo", "phone_login", "001234"},
		{challenge, "phone_link", "001234"},
	} {
		verified, err := hasher.Verify(candidate.id, candidate.purpose, candidate.code, digest)
		if err != nil || verified {
			t.Fatalf("mismatched challenge input verified: %v", err)
		}
	}
}

func TestSMSDigestRejectsInvalidInputsWithoutLeakingThem(t *testing.T) {
	if _, err := NewSMSCodeHasher(make([]byte, 31)); !errors.Is(err, ErrInvalidSMSChallenge) {
		t.Fatalf("short server key accepted: %v", err)
	}
	hasher, err := NewSMSCodeHasher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ id, purpose, code string }{
		{"short", "phone_login", "123456"},
		{"challenge-abcdefghijklmn", "wrong", "123456"},
		{"challenge-abcdefghijklmn", "phone_login", "12 456"},
		{"challenge-abcdefghijklmn", "phone_login", "12345"},
		{"challenge-abcdefghijklmn", "phone_login", "12345678901"},
	} {
		if _, err := hasher.Digest(input.id, input.purpose, input.code); !errors.Is(err, ErrInvalidSMSChallenge) || strings.Contains(err.Error(), input.code) {
			t.Fatalf("invalid input accepted or leaked: %v", err)
		}
	}
	if _, err := hasher.Verify("challenge-abcdefghijklmn", "phone_login", "123456", []byte("short")); !errors.Is(err, ErrInvalidSMSChallenge) {
		t.Fatalf("invalid digest length accepted: %v", err)
	}
}
