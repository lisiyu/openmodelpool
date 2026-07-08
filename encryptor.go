package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// encryptor provides AES-256-GCM encryption for sensitive data at rest.
type encryptor struct {
	mu       sync.RWMutex
	key      []byte // 32 bytes AES-256 key
	keyPath  string
	ready    bool
}

var enc *encryptor

func initEncryptor(keyPath string) {
	enc = &encryptor{keyPath: keyPath}
	enc.loadOrCreateKey()
}

func (e *encryptor) loadOrCreateKey() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if b, err := os.ReadFile(e.keyPath); err == nil && len(b) >= 32 {
		decoded, err := base64.StdEncoding.DecodeString(string(b))
		if err == nil && len(decoded) == 32 {
			e.key = decoded
			e.ready = true
			slog.Info("encryption key loaded")
			return
		}
	}

	// Generate new key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		slog.Error("failed to generate encryption key", "error", err)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	os.MkdirAll(filepath.Dir(e.keyPath), 0755)
	if err := os.WriteFile(e.keyPath, []byte(encoded), 0600); err != nil {
		slog.Error("failed to save encryption key", "error", err)
		return
	}
	e.key = key
	e.ready = true
	slog.Info("encryption key generated and saved")
}

// Encrypt encrypts plaintext with AES-256-GCM, returns base64-encoded ciphertext.
// Returns empty string if input is empty or encryption is not ready.
func (e *encryptor) Encrypt(plaintext string) string {
	if plaintext == "" || !e.ready {
		return plaintext
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return plaintext
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return plaintext
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return plaintext
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "enc:" + base64.StdEncoding.EncodeToString(ciphertext)
}

// Decrypt decrypts base64-encoded ciphertext produced by Encrypt().
// If the value is not encrypted (no "enc:" prefix), returns as-is for backward compatibility.
func (e *encryptor) Decrypt(ciphertext string) string {
	if ciphertext == "" || !e.ready {
		return ciphertext
	}
	if len(ciphertext) < 4 || ciphertext[:4] != "enc:" {
		// Not encrypted (legacy plaintext), return as-is
		return ciphertext
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	data, err := base64.StdEncoding.DecodeString(ciphertext[4:])
	if err != nil {
		slog.Warn("failed to decode encrypted value")
		return ""
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		slog.Warn("encrypted data too short")
		return ""
	}
	nonce, data := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		slog.Warn("failed to decrypt value", "error", err)
		return ""
	}
	return string(plaintext)
}

// IsEncrypted checks if a string is encrypted (has "enc:" prefix).
func IsEncrypted(s string) bool {
	return len(s) >= 4 && s[:4] == "enc:"
}

// Ensure no unused import errors
var _ = errors.New
// decryptAPIKey decrypts an API key if it's encrypted, otherwise returns it as-is.
func decryptAPIKey(key string) (string, error) {
	if !IsEncrypted(key) {
		return key, nil
	}
	if enc == nil {
		return "", fmt.Errorf("encryptor not initialized")
	}
	decrypted := enc.Decrypt(key)
	if decrypted == "" {
		return "", fmt.Errorf("failed to decrypt API key")
	}
	return decrypted, nil
}