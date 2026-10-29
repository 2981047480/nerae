package domain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEmailCodeChallengePurposeAndChannelIsolation(t *testing.T) {
	key := bytes.Repeat([]byte{8}, 32)
	email, err := NewEmailCodeHasher(key)
	if err != nil {
		t.Fatal(err)
	}
	sms, err := NewSMSCodeHasher(key)
	if err != nil {
		t.Fatal(err)
	}
	challenge := "challenge-abcdefghijklmn"
	digest, err := email.Digest(challenge, "email_login", "001234")
	if err != nil {
		t.Fatal(err)
	}
	key[0] ^= 1
	ok, err := email.Verify(challenge, "email_login", "001234", digest)
	if err != nil || !ok {
		t.Fatalf("valid code not verified: %v", err)
	}
	for _, input := range []struct{ id, purpose, code string }{
		{challenge, "email_login", "001235"},
		{challenge, "email_link", "001234"},
		{"challenge-abcdefghijklmo", "email_login", "001234"},
	} {
		ok, err := email.Verify(input.id, input.purpose, input.code, digest)
		if err != nil || ok {
			t.Fatalf("wrong code, challenge, or purpose verified: %v", err)
		}
	}
	smsDigest, err := sms.Digest(challenge, "phone_login", "001234")
	if err != nil || bytes.Equal(digest, smsDigest) {
		t.Fatalf("email and SMS digests must be isolated: %v", err)
	}
	if _, err := email.Digest(challenge, "phone_login", "001234"); !errors.Is(err, ErrInvalidEmailChallenge) {
		t.Fatalf("phone purpose accepted for email: %v", err)
	}
}

func TestEmailCodeGenerationAndInvalidInputs(t *testing.T) {
	for _, digits := range []int{6, 10} {
		code, err := GenerateEmailCode(digits)
		if err != nil || len(code) != digits || !validSMSCode(code) {
			t.Fatalf("invalid generated email code: %v", err)
		}
	}
	for _, digits := range []int{5, 11} {
		if _, err := GenerateEmailCode(digits); !errors.Is(err, ErrInvalidEmailChallenge) {
			t.Fatalf("invalid code length accepted: %v", err)
		}
	}
	if _, err := NewEmailCodeHasher(make([]byte, 31)); !errors.Is(err, ErrInvalidEmailChallenge) {
		t.Fatalf("short key accepted: %v", err)
	}
	hasher, _ := NewEmailCodeHasher(make([]byte, 32))
	for _, input := range []struct{ id, purpose, code string }{
		{"short", "email_login", "123456"},
		{"challenge-abcdefghijklmn", "other", "123456"},
		{"challenge-abcdefghijklmn", "email_login", "12 456"},
	} {
		if _, err := hasher.Digest(input.id, input.purpose, input.code); !errors.Is(err, ErrInvalidEmailChallenge) || strings.Contains(err.Error(), input.code) {
			t.Fatalf("invalid input accepted or leaked: %v", err)
		}
	}
	if _, err := hasher.Verify("challenge-abcdefghijklmn", "email_login", "123456", []byte("short")); !errors.Is(err, ErrInvalidEmailChallenge) {
		t.Fatalf("invalid digest length accepted: %v", err)
	}
}
