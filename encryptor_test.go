package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// ============================================================
// Encryptor tests
// ============================================================

// newTestEncryptor creates a dummy Encryptor with a random 32-byte key.
func newTestEncryptor() *Encryptor {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &Encryptor{key: key, ready: true}
}

func TestEncryptor_EncryptDecrypt_Roundtrip(t *testing.T) {
	e := newTestEncryptor()

	tests := []string{
		"hello world",
		"",
		"你好世界",
		"!@#$%^&*()_+",
		strings.Repeat("long plaintext with a lot of data ", 50),
		"multi\nline\ntext",
		"emoji test 🎉🚀💻",
	}

	for _, plaintext := range tests {
		t.Run("roundtrip_"+truncateStr(plaintext, 20), func(t *testing.T) {
			encrypted, err := e.Encrypt(plaintext)
			if err != nil {
				t.Fatalf("Encrypt(%q) error: %v", plaintext, err)
			}

			// Encrypted values should have the prefix
			if !strings.HasPrefix(encrypted, encPrefix) {
				t.Errorf("encrypted value does not have prefix %q: %q", encPrefix, encrypted)
			}

			decrypted, err := e.Decrypt(encrypted)
			if err != nil {
				t.Fatalf("Decrypt(%q) error: %v", encrypted, err)
			}

			if decrypted != plaintext {
				t.Errorf("roundtrip mismatch: got %q, want %q", decrypted, plaintext)
			}
		})
	}
}

func TestEncryptor_Decrypt_Unprefixed(t *testing.T) {
	e := newTestEncryptor()

	plaintext := "some plain text"
	// Decrypting a value without prefix should return unchanged
	result, err := e.Decrypt(plaintext)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != plaintext {
		t.Errorf("Decrypt(unprefixed) = %q, want %q", result, plaintext)
	}
}

func TestEncryptor_Decrypt_InvalidBase64(t *testing.T) {
	e := newTestEncryptor()

	result, err := e.Decrypt("omp:e:!!!not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
	// Returns original on error
	if result != "omp:e:!!!not-valid-base64!!!" {
		t.Errorf("expected original ciphertext on error, got %q", result)
	}
}

func TestEncryptor_Decrypt_TooShort(t *testing.T) {
	e := newTestEncryptor()

	// Create a valid block to determine minimum nonce size
	block, _ := aes.NewCipher(e.key)
	gcm, _ := cipher.NewGCM(block)
	ns := gcm.NonceSize()

	// Craft too-short ciphertext: prefix + base64(bytes < nonce size)
	shortBytes := make([]byte, ns-1)
	_, _ = rand.Read(shortBytes)

	ciphertext := encPrefix + base64.StdEncoding.EncodeToString(shortBytes)
	result, err := e.Decrypt(ciphertext)
	if err == nil {
		t.Error("expected error for ciphertext too short")
	}
	if result != ciphertext {
		t.Errorf("expected original ciphertext on error, got %q", result)
	}
}

func TestEncryptor_Decrypt_WrongKey(t *testing.T) {
	enc1 := newTestEncryptor()
	enc2 := newTestEncryptor()

	plaintext := "secret message"
	encrypted, err := enc1.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt error: %v", err)
	}

	// Decrypting with a different key should fail
	_, err = enc2.Decrypt(encrypted)
	if err == nil {
		t.Error("expected decryption failure with wrong key")
	}
}

func TestEncryptor_DifferentEncryptions(t *testing.T) {
	e := newTestEncryptor()
	plaintext := "same plaintext"

	enc1, err := e.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	enc2, err := e.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// Same plaintext should produce different ciphertexts (nonce)
	if enc1 == enc2 {
		t.Error("expected different ciphertexts due to random nonce")
	}
}

func TestIsEncrypted(t *testing.T) {
	tests := []struct {
		s        string
		expected bool
	}{
		{"omp:e:abc123", true},
		{"plaintext", false},
		{"", false},
		{"omp:e:", true},
		{"OMP:E:uppercase", false},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			if got := IsEncrypted(tt.s); got != tt.expected {
				t.Errorf("IsEncrypted(%q) = %v, want %v", tt.s, got, tt.expected)
			}
		})
	}
}

func TestEncryptField_DecryptField(t *testing.T) {
	// Save and restore global 'enc'
	oldEnc := enc
	defer func() { enc = oldEnc }()

	enc = newTestEncryptor()

	t.Run("roundtrip", func(t *testing.T) {
		original := "sensitive-api-key-12345"
		encrypted := encryptField(original)
		if encrypted == original {
			t.Error("encryptField should produce different value")
		}
		if !IsEncrypted(encrypted) {
			t.Error("encryptField result should be encrypted")
		}

		decrypted := decryptField(encrypted)
		if decrypted != original {
			t.Errorf("decryptField = %q, want %q", decrypted, original)
		}
	})

	t.Run("empty string unchanged", func(t *testing.T) {
		if encryptField("") != "" {
			t.Error("encryptField should return empty for empty input")
		}
		if decryptField("") != "" {
			t.Error("decryptField should return empty for empty input")
		}
	})

	t.Run("nil encryptor", func(t *testing.T) {
		enc = nil
		if encryptField("test") != "test" {
			t.Error("encryptField should return unchanged when enc is nil")
		}
		if decryptField("test") != "test" {
			t.Error("decryptField should return unchanged when enc is nil")
		}
	})
}

func TestDecryptAPIKey(t *testing.T) {
	oldEnc := enc
	defer func() { enc = oldEnc }()

	enc = newTestEncryptor()

	original := "api-key-value"
	encrypted, _ := enc.Encrypt(original)

	decrypted, err := decryptAPIKey(encrypted)
	if err != nil {
		t.Fatalf("decryptAPIKey error: %v", err)
	}
	if decrypted != original {
		t.Errorf("decryptAPIKey = %q, want %q", decrypted, original)
	}

	// With nil encryptor
	enc = nil
	decrypted, err = decryptAPIKey("some-key")
	if err != nil {
		t.Fatalf("decryptAPIKey error with nil enc: %v", err)
	}
	if decrypted != "some-key" {
		t.Errorf("expected unchanged with nil enc")
	}
}

// ============================================================
// NewEncryptor tests
// ============================================================

func TestNewEncryptor_GeneratesKey(t *testing.T) {
	// Should generate a new key (no env var, no key file in tmp)
	e, err := NewEncryptor()
	if err != nil {
		t.Fatalf("NewEncryptor error: %v", err)
	}
	if e == nil {
		t.Fatal("NewEncryptor returned nil")
	}
	if len(e.key) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(e.key))
	}

	// Test basic encrypt/decrypt works
	encrypted, err := e.Encrypt("test")
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := e.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "test" {
		t.Errorf("got %q, want 'test'", decrypted)
	}
}

// ============================================================
// Helpers
// ============================================================

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
