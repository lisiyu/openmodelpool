package main

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestEncryptor creates a fresh encryptor with a temp key file.
func newTestEncryptor(t *testing.T) *encryptor {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, ".key")
	e := &encryptor{keyPath: keyPath}
	e.loadOrCreateKey()
	if !e.ready {
		t.Fatal("encryptor not ready after init")
	}
	return e
}

func TestEncryptor_RoundTrip(t *testing.T) {
	e := newTestEncryptor(t)

	tests := []struct {
		name      string
		plaintext string
	}{
		{"hello", "hello world"},
		{"unicode", "你好世界 🌍"},
		{"long", "a]b[c{d}e(f)g!@#$%^&*()_+-=0123456789"},
		{"single_char", "x"},
		{"json_payload", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := e.Encrypt(tt.plaintext)
			if enc == "" {
				t.Fatal("Encrypt returned empty string")
			}
			if enc == tt.plaintext {
				t.Fatal("Encrypt returned plaintext unchanged")
			}
			if !IsEncrypted(enc) {
				t.Fatalf("encrypted result missing enc: prefix: %q", enc)
			}
			dec := e.Decrypt(enc)
			if dec != tt.plaintext {
				t.Fatalf("roundtrip mismatch: got %q, want %q", dec, tt.plaintext)
			}
		})
	}
}

func TestEncryptor_EmptyString(t *testing.T) {
	e := newTestEncryptor(t)
	enc := e.Encrypt("")
	if enc != "" {
		t.Fatalf("Encrypt(\"\") should return \"\", got %q", enc)
	}
	dec := e.Decrypt("")
	if dec != "" {
		t.Fatalf("Decrypt(\"\") should return \"\", got %q", dec)
	}
}

func TestEncryptor_RandomIV_DifferentCiphertext(t *testing.T) {
	e := newTestEncryptor(t)
	plaintext := "same plaintext every time"

	c1 := e.Encrypt(plaintext)
	c2 := e.Encrypt(plaintext)

	if c1 == c2 {
		t.Fatal("two encryptions of the same plaintext should differ (random IV)")
	}

	// Both must decrypt to the same value
	if e.Decrypt(c1) != plaintext || e.Decrypt(c2) != plaintext {
		t.Fatal("decryption of both ciphertexts should yield original plaintext")
	}
}

func TestEncryptor_DecryptInvalidData(t *testing.T) {
	e := newTestEncryptor(t)

	// Non-encrypted plaintext (legacy) - returns as-is
	legacy := "not-encrypted-at-all"
	if got := e.Decrypt(legacy); got != legacy {
		t.Fatalf("non-encrypted should pass through, got %q", got)
	}

	// Corrupted enc: prefix
	corrupted := "enc:not-valid-base64!!!###"
	if got := e.Decrypt(corrupted); got != "" {
		t.Fatalf("corrupted data should decrypt to \"\", got %q", got)
	}

	// Valid base64 but wrong content (not an actual GCM ciphertext)
	bad := "enc:aGVsbG8=" // "hello" in base64 - too short for nonce
	if got := e.Decrypt(bad); got != "" {
		t.Fatalf("short encrypted data should decrypt to \"\", got %q", got)
	}
}

func TestEncryptor_NotReady(t *testing.T) {
	e := &encryptor{ready: false}
	if got := e.Encrypt("hello"); got != "hello" {
		t.Fatalf("not-ready encryptor should return plaintext, got %q", got)
	}
	if got := e.Decrypt("anything"); got != "anything" {
		t.Fatalf("not-ready decryptor should return as-is, got %q", got)
	}
}

func TestEncryptor_KeyPersistence(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, ".key")

	// First encryptor creates key
	e1 := &encryptor{keyPath: keyPath}
	e1.loadOrCreateKey()
	cipher1 := e1.Encrypt("test")

	// Second encryptor loads same key
	e2 := &encryptor{keyPath: keyPath}
	e2.loadOrCreateKey()

	dec := e2.Decrypt(cipher1)
	if dec != "test" {
		t.Fatalf("key persistence failed: got %q", dec)
	}
}

func TestIsEncrypted(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"enc:abc", true},
		{"enc:", true},
		{"enc", false},
		{"en:", false},
		{"", false},
		{"hello", false},
		{"ENc:abc", false},
	}
	for _, tt := range tests {
		if got := IsEncrypted(tt.input); got != tt.want {
			t.Errorf("IsEncrypted(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestEncryptor_KeyFileCreated(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "subdir", ".key")
	e := &encryptor{keyPath: keyPath}
	e.loadOrCreateKey()

	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file should exist at %s: %v", keyPath, err)
	}
}
