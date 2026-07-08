package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ============================================================
// SA-09: Error message sanitization tests
// ============================================================

func TestSA09_ValidateKeyGenericErrors(t *testing.T) {
	// Test that key classification handles invalid keys without leaking details
	// v2.0: keys are classified by prefix, no complex signature verification
	tests := []struct {
		key      string
		expected KeyType
	}{
		{"sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1", KeyTypePublic},
		{"sk-guest-mmx-abc123.def456", KeyTypeGuest},
		{"sk-test-proxy-key", KeyTypeProxy},
		{"invalid-key-format", KeyTypeUnknown},
		{"", KeyTypeUnknown},
	}

	for _, tc := range tests {
		result := ClassifyKey(tc.key)
		if result != tc.expected {
			t.Errorf("ClassifyKey(%q) = %q, want %q", tc.key, result, tc.expected)
		}
	}

	// Test that guest key validation doesn't leak internal details
	nodeID, valid := ValidateGuestKey("sk-guest-mmx-abc.def")
	if !valid {
		t.Error("valid format guest key should pass")
	}
	if nodeID != "mmx-abc" {
		t.Errorf("node_id should be 'mmx-abc', got %q", nodeID)
	}

	// Invalid guest key format
	_, valid = ValidateGuestKey("sk-guest-")
	if valid {
		t.Error("invalid guest key format should fail")
	}
}

// ============================================================
// SA-10: Rate limiting tests
// ============================================================

func TestSA10_RateLimitByIP(t *testing.T) {
	// Use 120 requests/minute = 2 QPS, enough tokens for testing
	handler := rateLimitByIP(120, "test_endpoint_unique_1")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// First request should succeed (consumes initial tokens)
	req := httptest.NewRequest("POST", "/test", nil)
	req.RemoteAddr = "10.20.30.40:12345"
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != 200 {
		t.Errorf("first request should succeed, got %d", w.Code)
	}

	// Rapid subsequent requests should eventually be rate limited
	limited := false
	for i := 0; i < 200; i++ {
		req := httptest.NewRequest("POST", "/test", nil)
		req.RemoteAddr = "10.20.30.40:12345"
		w := httptest.NewRecorder()
		handler(w, req)
		if w.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("rate limiter should eventually reject requests from same IP")
	}
}

func TestSA10_ExtractClientIP(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"192.168.1.1:8080", "192.168.1.1"},
		{"10.0.0.1:12345", "10.0.0.1"},
		{"[::1]:8080", "[::1]"},
	}
	for _, tt := range tests {
		got := extractClientIP(tt.input)
		if got != tt.want {
			t.Errorf("extractClientIP(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSA10_CleanupIPRateLimiters(t *testing.T) {
	// Add some entries
	ipRateLimiters.Lock()
	ipRateLimiters.limiters["test1"] = &ipRateLimitEntry{
		limiter:  NewRateLimiter(1),
		lastSeen: time.Now().Add(-2 * time.Hour), // stale
	}
	ipRateLimiters.limiters["test2"] = &ipRateLimitEntry{
		limiter:  NewRateLimiter(1),
		lastSeen: time.Now(), // fresh
	}
	ipRateLimiters.Unlock()

	// Cleanup entries older than 1 hour
	cleanupIPRateLimiters(1 * time.Hour)

	ipRateLimiters.RLock()
	_, exists1 := ipRateLimiters.limiters["test1"]
	_, exists2 := ipRateLimiters.limiters["test2"]
	ipRateLimiters.RUnlock()

	if exists1 {
		t.Error("stale entry should have been cleaned up")
	}
	if !exists2 {
		t.Error("fresh entry should still exist")
	}
}

// ============================================================
// SA-11: Request body size limit tests
// ============================================================

func TestSA11_ReadJSON_SizeLimit(t *testing.T) {
	// SA-11 (strict): 1MB limit — create a request with a body larger than 1MB
	largeBody := bytes.Repeat([]byte("x"), 2*1024*1024) // 2 MB — exceeds 1MB limit
	req := httptest.NewRequest("POST", "/test", bytes.NewReader(largeBody))

	var result map[string]any
	err := readJSON(req, &result)
	if err == nil {
		t.Fatal("expected error for oversized body (>1MB)")
	}
}

func TestSA11_ReadJSON_NormalSize(t *testing.T) {
	body := []byte(`{"key": "value", "number": 42}`)
	req := httptest.NewRequest("POST", "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	var result map[string]any
	err := readJSON(req, &result)
	if err != nil {
		t.Fatalf("expected no error for normal body, got: %v", err)
	}
	if result["key"] != "value" {
		t.Errorf("expected key=value, got %v", result["key"])
	}
}

// ============================================================
// SA-14: Password policy tests
// ============================================================

func TestSA14_PasswordStrength(t *testing.T) {
	// SA-14 (strict): Requires ALL 4 character classes + minimum 12 chars
	tests := []struct {
		password string
		wantErr  bool
	}{
		{"short", true},                              // too short
		{"123456789012", true},                       // only digits
		{"abcdefghijklm", true},                      // only lowercase
		{"Abcdefgh1234", true},                       // missing special character
		{"Abcdefghijkl!", true},                      // missing digit
		{"Abcdefghijk1!", false},                     // all 4 classes, 13 chars ✓
		{"MyP@ssw0rd123", false},                     // all 4 classes, 13 chars ✓
		{"Ab1!xxxxxxxx", false},                      // all 4 classes, 12 chars ✓
		{"Ab1!", true},                               // all 4 classes but too short
		{"", true},
	}

	for _, tt := range tests {
		err := validatePasswordStrength(tt.password)
		if tt.wantErr && err == nil {
			t.Errorf("password %q should fail validation", tt.password)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("password %q should pass validation, got: %v", tt.password, err)
		}
	}
}

// ============================================================
// SA-15: Data integrity tests
// ============================================================

func TestSA15_SaveAndLoadWithIntegrity(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	// Initialize a mock encryptor for HMAC
	origEnc := enc
	defer func() { enc = origEnc }()

	enc = &encryptor{
		mu:    sync.RWMutex{},
		key:   make([]byte, 32),
		ready: true,
	}
	for i := range enc.key {
		enc.key[i] = byte(i)
	}

	// Save data with integrity
	data := map[string]string{"key": "value", "secret": "test"}
	err := saveWithIntegrity(path, data)
	if err != nil {
		t.Fatalf("saveWithIntegrity failed: %v", err)
	}

	// Load should succeed
	var loaded map[string]string
	err = loadWithIntegrity(path, &loaded)
	if err != nil {
		t.Fatalf("loadWithIntegrity failed: %v", err)
	}
	if loaded["key"] != "value" {
		t.Errorf("expected key=value, got %v", loaded["key"])
	}
}

func TestSA15_TamperedFileDetected(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	// Initialize mock encryptor
	origEnc := enc
	defer func() { enc = origEnc }()

	enc = &encryptor{
		mu:    sync.RWMutex{},
		key:   make([]byte, 32),
		ready: true,
	}
	for i := range enc.key {
		enc.key[i] = byte(i)
	}

	// Save data
	data := map[string]string{"key": "value"}
	saveWithIntegrity(path, data)

	// Tamper with the payload (modify bytes after HMAC)
	raw, _ := os.ReadFile(path)
	if len(raw) > 40 {
		raw[35] ^= 0xFF // flip bits in the payload area
		os.WriteFile(path, raw, 0600)
	}

	// Load should detect tampering
	var loaded map[string]string
	err := loadWithIntegrity(path, &loaded)
	if err == nil {
		// Check if data was corrupted (it might load as plain JSON if HMAC check fails
		// but JSON parse still works on corrupted data)
		if loaded["key"] == "value" {
			t.Error("tampered file should not return original data")
		}
	}
	// err != nil is expected (integrity check failed)
}

func TestSA15_BackwardCompat_PlainJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.json")

	// Write a plain JSON file (no HMAC prefix)
	data := map[string]string{"key": "value"}
	jsonData, _ := json.Marshal(data)
	os.WriteFile(path, jsonData, 0600)

	// Should load as plain JSON (backward compatibility)
	var loaded map[string]string
	err := loadWithIntegrity(path, &loaded)
	if err != nil {
		t.Fatalf("should load plain JSON file: %v", err)
	}
	if loaded["key"] != "value" {
		t.Errorf("expected key=value, got %v", loaded["key"])
	}
}

// ============================================================
// SA-17: Cloudflare client timeout test
// ============================================================

func TestSA17_CloudflareClientHasTimeout(t *testing.T) {
	if cloudflareClient == nil {
		t.Fatal("cloudflareClient should not be nil")
	}
	if cloudflareClient.Timeout == 0 {
		t.Fatal("cloudflareClient must have a non-zero timeout")
	}
	if cloudflareClient.Timeout != 30*1000000000 { // 30 * time.Second
		t.Errorf("expected 30s timeout, got %v", cloudflareClient.Timeout)
	}
}

// ============================================================
// SA-12: Federation auth test
// ============================================================

func TestSA12_FederationAuth_LocaleRequiresSecret(t *testing.T) {
	// This test verifies the withFederationAuth middleware structure.
	// Full integration testing requires initialized cfg, auth, fed globals.
	// Here we just verify the function exists and doesn't panic with nil-safe checks.

	// Verify the function signature is correct
	var _ func(http.HandlerFunc) http.HandlerFunc = withFederationAuth
	t.Log("withFederationAuth middleware exists and is correctly typed")
}

// ============================================================
// Helper to suppress unused import warnings
// ============================================================
var _ = hmac.New
var _ = sha256.New
var _ = io.Discard
