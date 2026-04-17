package gitlab

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/pkg/secrets"
)

// TestNewCipherDecrypter_RoundTrip verifies the helper adapts a *secrets.Cipher
// into a TokenDecrypter that returns the original plaintext. Cheap insurance
// against the adapter ever growing a subtle transformation.
func TestNewCipherDecrypter_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, err := secrets.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	plaintext := "glpat-xxxxxxxxxxxxxxxxxxxx"
	encrypted, err := cipher.Encrypt([]byte(plaintext))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	decrypter := NewCipherDecrypter(cipher)
	got, err := decrypter(context.Background(), encrypted)
	if err != nil {
		t.Fatalf("decrypter: %v", err)
	}
	if got != plaintext {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

// TestNewCipherDecrypter_PropagatesError verifies corrupted ciphertext surfaces
// an error rather than returning a garbage string.
func TestNewCipherDecrypter_PropagatesError(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cipher, err := secrets.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}

	decrypter := NewCipherDecrypter(cipher)
	if _, err := decrypter(context.Background(), []byte("too-short")); err == nil {
		t.Fatal("expected error for bogus ciphertext, got nil")
	}
}
