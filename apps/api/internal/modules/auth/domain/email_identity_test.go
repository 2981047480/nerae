package domain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeEmailPreservesLocalPartAndRejectsInjection(t *testing.T) {
	got, err := NormalizeEmail("Name+tag@EXAMPLE.COM")
	if err != nil || got != "Name+tag@example.com" {
		t.Fatalf("unexpected normalized address: %v", err)
	}
	for _, input := range []string{"", "a@@example.com", "a@example.com\r\nBcc:other@example.com", "测试@example.com", "Name <a@example.com>", "a @example.com"} {
		_, err := NormalizeEmail(input)
		if !errors.Is(err, ErrEmailIdentity) || input != "" && strings.Contains(err.Error(), input) {
			t.Fatalf("invalid address accepted or exposed: %v", err)
		}
	}
}

func TestEmailIdentityProtectionIsBoundAndNonDeterministic(t *testing.T) {
	lookupKey := bytes.Repeat([]byte{1}, 32)
	encryptionKey := bytes.Repeat([]byte{2}, 32)
	protector, err := NewEmailIdentityProtector(lookupKey, encryptionKey, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	address := "Name+tag@EXAMPLE.COM"
	index, ciphertext, err := protector.Protect(address)
	if err != nil {
		t.Fatal(err)
	}
	otherIndex, otherCiphertext, err := protector.Protect(address)
	if err != nil || index != otherIndex || bytes.Equal(ciphertext, otherCiphertext) {
		t.Fatal("lookup must be stable and encryption must use a fresh nonce")
	}
	if bytes.Contains(ciphertext, []byte("Name+tag@example.com")) {
		t.Fatal("ciphertext contains the email in plaintext")
	}
	got, err := protector.Open(index, ciphertext)
	if err != nil || got != "Name+tag@example.com" {
		t.Fatalf("could not decrypt normalized address: %v", err)
	}
	lowerIndex, err := protector.Index("name+tag@example.com")
	if err != nil || index == lowerIndex {
		t.Fatal("local-part case was silently merged")
	}
	aliasIndex, err := protector.Index("Name@example.com")
	if err != nil || index == aliasIndex {
		t.Fatal("plus alias was silently merged")
	}
	lookupKey[0] ^= 1
	encryptionKey[0] ^= 1
	if got, err := protector.Open(index, ciphertext); err != nil || got != "Name+tag@example.com" {
		t.Fatal("mutating caller keys changed protector")
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 1
	wrongIndex := index
	wrongIndex[0] ^= 1
	for _, item := range []struct {
		index [32]byte
		value []byte
	}{{index, tampered}, {wrongIndex, ciphertext}, {index, []byte("short")}} {
		if _, err := protector.Open(item.index, item.value); !errors.Is(err, ErrEmailIdentity) {
			t.Fatal("tampered identity accepted")
		}
	}
	otherKeyID, _ := NewEmailIdentityProtector(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), "key-2")
	if _, err := otherKeyID.Open(index, ciphertext); !errors.Is(err, ErrEmailIdentity) {
		t.Fatal("ciphertext accepted under a different key identifier")
	}
}

func TestEmailIdentityProtectorRejectsUnsafeKeys(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	for _, input := range []struct {
		index, encryption []byte
		keyID             string
	}{
		{key[:31], bytes.Repeat([]byte{2}, 32), "key-1"},
		{key, key, "key-1"},
		{key, bytes.Repeat([]byte{2}, 31), "key-1"},
		{key, bytes.Repeat([]byte{2}, 32), ""},
		{key, bytes.Repeat([]byte{2}, 32), "bad key\n"},
	} {
		if _, err := NewEmailIdentityProtector(input.index, input.encryption, input.keyID); !errors.Is(err, ErrEmailIdentity) {
			t.Fatal("unsafe key configuration accepted")
		}
	}
}
