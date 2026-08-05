package service

import (
	"bytes"
	"strings"
	"testing"
)

func TestComputeSHA256Hash(t *testing.T) {
	input := "https://example.com/articles/123"

	first := computeSHA256Hash(input)
	second := computeSHA256Hash(input)

	if !bytes.Equal(first, second) {
		t.Fatal("expected same input to produce the same hash")
	}

	if len(first) != 32 {
		t.Fatalf("expected SHA-256 hash length to be 32 bytes, got %d", len(first))
	}

	other := computeSHA256Hash(input + "?ref=other")
	if bytes.Equal(first, other) {
		t.Fatal("expected different inputs to produce different hashes")
	}
}

func TestEncodeToBase58(t *testing.T) {
	encoded, err := encodeToBase58([]byte("1234567890"))
	if err != nil {
		t.Fatalf("expected Base58 encoding to succeed: %v", err)
	}

	if encoded == "" {
		t.Fatal("expected Base58 encoding to return a non-empty string")
	}

	const bitcoinBase58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	for _, ch := range encoded {
		if !strings.ContainsRune(bitcoinBase58Alphabet, ch) {
			t.Fatalf("expected encoded string to contain only Bitcoin Base58 characters, got %q in %q", ch, encoded)
		}
	}
}

func TestGenerateWithSalt(t *testing.T) {
	url := "https://example.com/articles/123"
	userID := "user-42"

	first, err := generateWithSalt(url, userID, 0)
	if err != nil {
		t.Fatalf("expected short link generation to succeed: %v", err)
	}

	second, err := generateWithSalt(url, userID, 0)
	if err != nil {
		t.Fatalf("expected repeated short link generation to succeed: %v", err)
	}

	if len(first) != 8 {
		t.Fatalf("expected short link length to be 8, got %d", len(first))
	}

	if first != second {
		t.Fatal("expected same url, user id, and attempt to produce a stable short link")
	}

	withDifferentAttempt, err := generateWithSalt(url, userID, 1)
	if err != nil {
		t.Fatalf("expected salted retry short link generation to succeed: %v", err)
	}

	if first == withDifferentAttempt {
		t.Fatal("expected different attempts to produce different short links")
	}
}
