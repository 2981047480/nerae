package issuance

import (
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const vectorChallenge = "Q1w2E3r4T5y6U7i8O9p0Aa"

func TestDeriveKnownAnswersAndEpochTag(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	deriver, err := NewDeriver(key)
	if err != nil {
		t.Fatal("valid synthetic key was rejected")
	}
	// The constructor owns its copy, even if the caller later reuses its buffer.
	for i := range key {
		key[i] = 0xff
	}

	cases := []struct {
		challenge string
		purpose   string
		digits    int
		want      string
	}{
		{vectorChallenge, "email_login", 6, "930575"},
		{vectorChallenge, "email_link", 6, "800364"},
		{"Q1w2E3r4T5y6U7i8O9p0Ab", "email_login", 6, "888352"},
		{vectorChallenge, "email_login", 10, "2932066539"},
	}
	for _, tc := range cases {
		got, err := deriver.Derive(tc.challenge, tc.purpose, tc.digits)
		if err != nil || got != tc.want {
			t.Fatalf("known answer mismatch for %s/%s/%d: got=%q err=%v", tc.challenge, tc.purpose, tc.digits, got, err)
		}
		if len(got) != tc.digits {
			t.Fatalf("code was not zero-padded to its requested length: got=%q", got)
		}
		again, err := deriver.Derive(tc.challenge, tc.purpose, tc.digits)
		if err != nil || again != got {
			t.Fatalf("same inputs did not deterministically reproduce the code: %v", err)
		}
	}
	tag, err := deriver.KeyEpochTag()
	if err != nil || hex.EncodeToString(tag[:]) != "4403f4c65ed8d149ec66189032bdd2511a526b308d201bcea76b170ac48d6964" {
		t.Fatalf("key epoch tag known answer mismatch: err=%v", err)
	}
	otherKey := bytes.Repeat([]byte{0x7a}, 32)
	other, err := NewDeriver(otherKey)
	if err != nil {
		t.Fatal("second synthetic key was rejected")
	}
	otherTag, _ := other.KeyEpochTag()
	if hmac.Equal(tag[:], otherTag[:]) {
		t.Fatal("different independent keys produced the same epoch tag")
	}
}

func TestInputBoundsAndPurposeSeparation(t *testing.T) {
	d, err := NewDeriver(bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal("synthetic key rejected")
	}
	valid := []string{strings.Repeat("A", minChallengeSize), strings.Repeat("z", maxChallengeSize)}
	for _, id := range valid {
		if _, err := d.Derive(id, "email_login", 6); err != nil {
			t.Fatalf("boundary challenge ID length %d rejected: %v", len(id), err)
		}
	}
	for _, id := range []string{
		strings.Repeat("A", minChallengeSize-1), strings.Repeat("A", maxChallengeSize+1),
		strings.Repeat("A", minChallengeSize-1) + "=", "Q1w2E3r4T5y6U7i8O9p0A/", "Q1w2E3r4T5y6U7i8O9p0A+",
		"Q1w2E3r4T5y6U7i8O9p0Aé",
	} {
		if _, err := d.Derive(id, "email_login", 6); !errors.Is(err, ErrInvalidDerivation) {
			t.Fatalf("invalid challenge identifier was accepted: %q", id)
		}
	}
	for _, purpose := range []string{"", "email_login ", "phone_login", "email_link\x00"} {
		if _, err := d.Derive(vectorChallenge, purpose, 6); !errors.Is(err, ErrInvalidDerivation) {
			t.Fatalf("invalid purpose was accepted: %q", purpose)
		}
	}
	for _, digits := range []int{0, 5, 11, 100} {
		if _, err := d.Derive(vectorChallenge, "email_login", digits); !errors.Is(err, ErrInvalidDerivation) {
			t.Fatalf("invalid digit count was accepted: %d", digits)
		}
	}
	login, _ := d.Derive(vectorChallenge, "email_login", 6)
	link, _ := d.Derive(vectorChallenge, "email_link", 6)
	otherChallenge, _ := d.Derive("Q1w2E3r4T5y6U7i8O9p0Ab", "email_login", 6)
	if login == link || login == otherChallenge {
		t.Fatal("fixed test vectors failed to demonstrate purpose/challenge separation")
	}
}

func TestKeyLengthAndNilDeriverFailClosed(t *testing.T) {
	for _, size := range []int{0, 31, 33, 64} {
		if _, err := NewDeriver(make([]byte, size)); !errors.Is(err, ErrInvalidDerivation) {
			t.Fatalf("key size %d was accepted", size)
		}
	}
	var nilDeriver *Deriver
	if _, err := nilDeriver.Derive(vectorChallenge, "email_login", 6); !errors.Is(err, ErrInvalidDerivation) {
		t.Fatal("nil deriver did not fail closed")
	}
	if _, err := nilDeriver.KeyEpochTag(); !errors.Is(err, ErrInvalidDerivation) {
		t.Fatal("nil deriver returned a key epoch tag")
	}
	zeroValue := &Deriver{}
	if _, err := zeroValue.Derive(vectorChallenge, "email_login", 6); !errors.Is(err, ErrInvalidDerivation) {
		t.Fatal("zero-value deriver derived a code without an initialized key")
	}
	if _, err := zeroValue.KeyEpochTag(); !errors.Is(err, ErrInvalidDerivation) {
		t.Fatal("zero-value deriver returned a key epoch tag")
	}
}

func TestRejectionSamplingThresholdAndBound(t *testing.T) {
	n := decimalRange(6)
	threshold := -n % n
	if threshold == 0 {
		t.Fatal("test vector expected a non-zero rejection threshold")
	}
	values := []uint64{threshold - 1, threshold, ^uint64(0)}
	calls := 0
	got, err := sampleUniform(n, func(counter uint64) (uint64, error) {
		if counter != uint64(calls) {
			t.Fatalf("candidate counter is not monotonically increasing: %d", counter)
		}
		value := values[calls]
		calls++
		return value, nil
	})
	if err != nil || calls != 2 || got != threshold%n {
		t.Fatalf("threshold boundary was not rejected then accepted: got=%d calls=%d err=%v", got, calls, err)
	}
	maxValue, err := sampleUniform(n, func(uint64) (uint64, error) { return ^uint64(0), nil })
	if err != nil || maxValue != ^uint64(0)%n {
		t.Fatalf("maximum uint64 candidate did not map uniformly: value=%d err=%v", maxValue, err)
	}
	failedCalls := 0
	if _, err := sampleUniform(n, func(uint64) (uint64, error) {
		failedCalls++
		return threshold - 1, nil
	}); !errors.Is(err, ErrInvalidDerivation) || failedCalls != maxAttempts {
		t.Fatalf("candidate loop was not bounded and fail-closed: calls=%d err=%v", failedCalls, err)
	}
	if _, err := sampleUniform(n, func(uint64) (uint64, error) { return 0, ErrInvalidDerivation }); !errors.Is(err, ErrInvalidDerivation) {
		t.Fatal("candidate-source error was not propagated as a generic failure")
	}
	if _, err := sampleUniform(0, func(uint64) (uint64, error) { return 1, nil }); !errors.Is(err, ErrInvalidDerivation) {
		t.Fatal("zero range was accepted")
	}
}

func TestFormattingLoggingAndErrorsAreRedacted(t *testing.T) {
	secret := bytes.Repeat([]byte{0x91}, 32)
	secretHex := hex.EncodeToString(secret)
	d, err := NewDeriver(secret)
	if err != nil {
		t.Fatal("synthetic key rejected")
	}
	for _, value := range []string{fmt.Sprint(d), fmt.Sprintf("%#v", d)} {
		if strings.Contains(value, secretHex) || strings.Contains(value, "email_login") || strings.Contains(value, vectorChallenge) {
			t.Fatalf("formatter exposed sensitive context: %s", value)
		}
	}
	if data, err := json.Marshal(d); err == nil || len(data) != 0 {
		t.Fatal("JSON marshaling returned sensitive deriver data")
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	logger.Info("test", "deriver", d)
	if strings.Contains(logs.String(), secretHex) || strings.Contains(logs.String(), vectorChallenge) || strings.Contains(logs.String(), "email_login") {
		t.Fatalf("slog exposed sensitive context: %s", logs.String())
	}
	_, err = d.Derive("private invalid challenge", "email_login", 6)
	if err == nil || strings.Contains(err.Error(), "private invalid challenge") || strings.Contains(err.Error(), "email_login") {
		t.Fatal("validation error exposed input context")
	}
}
