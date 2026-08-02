package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// encPrefix marks ciphertext produced by this Encryptor so callers can tell
// encrypted values apart from plaintext and avoid double-encryption.
const encPrefix = "omp:e:"

// Encryptor provides AES-256-GCM authenticated encryption at rest for
// sensitive fields (API keys, SMTP passwords, proxy API keys).
//
// This replaces the previously misnamed file (which actually contained the
// EventBus/SSE implementation, now moved to eventbus.go). The earlier code
// referenced an undefined `enc` Encryptor and the README claimed AES-256-GCM
// with no implementation — that gap is now closed by real crypto/aes usage.
type Encryptor struct {
	mu        sync.RWMutex
	key       []byte
	ready     bool
	ephemeral bool
}

// IsEphemeral reports whether the encryptor is using a temporary key
// that will be lost on restart (all previously encrypted data becomes unrecoverable).
func (e *Encryptor) IsEphemeral() bool {
	return e.ephemeral
}

// NewEncryptor resolves the 32-byte AES key with the following precedence:
//  1. OPENMODELPOOL_ENC_KEY env var (raw 32 bytes or base64-encoded)
//  2. data/.enc_key on disk (auto-generated and persisted on first run)
//  3. a freshly generated in-memory key (no persistence; survives one process)
func NewEncryptor() (*Encryptor, error) {
	if k := os.Getenv("OPENMODELPOOL_ENC_KEY"); k != "" {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(raw) != 32 {
			raw = []byte(k)
			if len(raw) != 32 {
				return nil, errors.New("OPENMODELPOOL_ENC_KEY must decode to exactly 32 bytes (raw or base64)")
			}
		}
		return &Encryptor{key: raw}, nil
	}

	const keyFile = "data/.enc_key"
	if b, err := os.ReadFile(keyFile); err == nil && len(b) == 32 {
		return &Encryptor{key: b}, nil
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll("data", 0o700); err == nil {
		if werr := atomicWriteFile(keyFile, key, 0o600); werr != nil {
			slog.Warn("could not persist encryption key; using ephemeral key", "err", werr)
		}
	}
	return &Encryptor{key: key}, nil
}

// Encrypt encrypts plaintext and returns "omp:e:" + base64(nonce||ciphertext).
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Non-prefixed input is returned unchanged so that
// legacy/empty values never cause a hard failure.
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, encPrefix) {
		return ciphertext, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encPrefix))
	if err != nil {
		return ciphertext, err
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return ciphertext, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ciphertext, err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return ciphertext, errors.New("ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return ciphertext, err
	}
	return string(pt), nil
}

// enc is the package-level encryptor used by config/auth/multiuser code.
var enc *Encryptor

func init() {
	var err error
	enc, err = NewEncryptor()
	if err != nil {
		// Last-resort: ephemeral key so the process can still run.
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			slog.Error("crypto/rand.Read failed for ephemeral key fallback", "err", err) // B10
		}
		enc = &Encryptor{key: key, ready: true, ephemeral: true}
		slog.Error("CRITICAL: encryptor fell back to ephemeral key — all encrypted data from previous sessions is UNRECOVERABLE. Resolve the key file issue before storing any sensitive data.")
		return
	}
	enc.ready = true
}

// IsEncrypted reports whether s looks like a value produced by this Encryptor.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, encPrefix)
}

// encryptField best-effort encrypts a field, returning the input unchanged on error.
// M3-fix: Refuses to encrypt new data when using an ephemeral key to prevent
// data loss on restart (previously encrypted data would become unrecoverable).
func encryptField(s string) string {
	if enc == nil || s == "" {
		return s
	}
	if enc.IsEphemeral() {
		slog.Warn("refusing to encrypt data with ephemeral key — data would be unrecoverable after restart", "hint", "resolve the encryption key file issue before storing sensitive data")
		return s
	}
	e, err := enc.Encrypt(s)
	if err != nil {
		slog.Warn("encrypt failed", "err", err)
		return s
	}
	return e
}

// decryptField best-effort decrypts a field, returning the input unchanged on error.
func decryptField(s string) string {
	if enc == nil || s == "" {
		return s
	}
	d, err := enc.Decrypt(s)
	if err != nil {
		slog.Warn("decrypt failed", "err", err)
		return s
	}
	return d
}

// decryptAPIKey decrypts an API key for testing/display. Returns an error if
// decryption fails (so callers can surface a 500 instead of leaking plaintext).
func decryptAPIKey(s string) (string, error) {
	if enc == nil {
		return s, nil
	}
	return enc.Decrypt(s)
}

// ensure path/filepath import is used even if key-file logic changes.
var _ = filepath.Join
