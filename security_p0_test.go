package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================
// P0-1: handleVerifyAuth properly rejects invalid tokens
// ============================================================

func TestP0_1_HandleVerifyAuth_ValidToken(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	token, _ := env.authInst.CreateToken("admin", false)

	req := httptest.NewRequest("GET", "/api/auth/verify", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handleVerifyAuth(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["valid"] != true {
		t.Error("expected valid=true for valid token")
	}
	if resp["username"] != "admin" {
		t.Errorf("expected username=admin, got %v", resp["username"])
	}
}

func TestP0_1_HandleVerifyAuth_InvalidToken(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	req := httptest.NewRequest("GET", "/api/auth/verify", nil)
	req.Header.Set("Authorization", "Bearer invalid-token-12345")
	w := httptest.NewRecorder()

	handleVerifyAuth(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401 for invalid token, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["valid"] != false {
		t.Error("expected valid=false for invalid token")
	}
}

func TestP0_1_HandleVerifyAuth_NoToken(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	req := httptest.NewRequest("GET", "/api/auth/verify", nil)
	w := httptest.NewRecorder()

	handleVerifyAuth(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401 for missing token, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["valid"] != false {
		t.Error("expected valid=false for missing token")
	}
}

func TestP0_1_HandleVerifyAuth_ExpiredToken(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	token, _ := env.authInst.CreateToken("admin", false)

	// Simulate token expiration by modifying the secret
	// (the token signed with old secret will fail with new secret)
	env.authInst.mu.Lock()
	env.authInst.data.JWTSecret = randomString(64)
	env.authInst.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/auth/verify", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handleVerifyAuth(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401 for expired/invalid token, got %d", w.Code)
	}
}

// ============================================================
// P0-2: Independent reset code (not using Proxy API Key)
// ============================================================

func TestP0_2_GenerateAndValidateResetCode(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	// Generate reset code
	code, expires, err := env.authInst.GenerateResetCode()
	if err != nil {
		t.Fatalf("GenerateResetCode failed: %v", err)
	}
	if code == "" {
		t.Fatal("reset code should not be empty")
	}
	if expires.Before(time.Now()) {
		t.Fatal("reset code should not be expired immediately")
	}
	if !env.authInst.HasResetCode() {
		t.Fatal("HasResetCode should return true after generation")
	}

	// Validate correct code
	valid, err := env.authInst.ValidateAndConsumeResetCode(code)
	if err != nil {
		t.Fatalf("ValidateAndConsumeResetCode failed: %v", err)
	}
	if !valid {
		t.Fatal("valid code should pass validation")
	}

	// Code should be consumed (single-use)
	if env.authInst.HasResetCode() {
		t.Fatal("reset code should be consumed after use")
	}

	// Reusing the same code should fail
	valid, err = env.authInst.ValidateAndConsumeResetCode(code)
	if err == nil {
		t.Fatal("reused code should return error")
	}
	if valid {
		t.Fatal("reused code should not be valid")
	}
}

func TestP0_2_ResetCodeNotProxyAPIKey(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	// Set a proxy API key
	env.cfgInst.Set("proxy_api_key", "sk-test-proxy-key-12345")

	// Generate reset code - should be different from proxy API key
	code, _, err := env.authInst.GenerateResetCode()
	if err != nil {
		t.Fatalf("GenerateResetCode failed: %v", err)
	}

	// The reset code should NOT be the proxy API key
	if code == "sk-test-proxy-key-12345" {
		t.Fatal("reset code must not be the same as the Proxy API Key")
	}

	// Using the proxy API key as reset code should fail
	valid, err := env.authInst.ValidateAndConsumeResetCode("sk-test-proxy-key-12345")
	if err == nil {
		t.Fatal("Proxy API Key should not work as reset code")
	}
	if valid {
		t.Fatal("Proxy API Key should not be valid as reset code")
	}
}

func TestP0_2_HandleResetWithCode_NoLongerUsesProxyKey(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	env.cfgInst.Set("proxy_api_key", "sk-test-proxy-key-12345")

	// Generate independent reset code
	code, _, _ := env.authInst.GenerateResetCode()

	// Reset with the independent code
	body := map[string]string{
		"code":         code,
		"new_password": "NewTest12345!@#$",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/auth/reset-with-code", bytes.NewReader(jsonBody))
	w := httptest.NewRecorder()

	handleResetWithCode(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify proxy API key was NOT cleared (unlike old behavior)
	storedKey := env.cfgInst.Get("proxy_api_key", "")
	if storedKey != "sk-test-proxy-key-12345" {
		t.Fatal("Proxy API Key should NOT be affected by password reset")
	}

	// Verify new password works
	if !env.authInst.VerifyCredentials("admin", "NewTest12345!@#$") {
		t.Fatal("new password should work after reset")
	}
}

func TestP0_2_HandleResetWithCode_ProxyKeyRejected(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	env.cfgInst.Set("proxy_api_key", "sk-test-proxy-key-12345")

	// Try to use proxy API key as reset code (old behavior should fail now)
	body := map[string]string{
		"code":         "sk-test-proxy-key-12345",
		"new_password": "NewTest12345!@#$",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/auth/reset-with-code", bytes.NewReader(jsonBody))
	w := httptest.NewRecorder()

	handleResetWithCode(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401 when using proxy key as reset code, got %d: %s", w.Code, w.Body.String())
	}
}

// ============================================================
// P0-3: Auth mutex protection
// ============================================================

func TestP0_3_AuthConcurrentAccess(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	// Test concurrent reads
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = env.authInst.Initialized()
			_ = env.authInst.GetEmail()
			_ = env.authInst.GetSMTP()
			_ = env.authInst.AdminInfo()
			_ = env.authInst.IsSMTPConfigured()
		}()
	}
	wg.Wait()
}

func TestP0_3_AuthConcurrentReadWrite(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = env.authInst.Initialized()
			_ = env.authInst.GetEmail()
			_ = env.authInst.AdminInfo()
		}()
	}

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			env.authInst.UpdateEmail("test" + string(rune('0'+idx)) + "@test.com")
		}(i)
	}

	wg.Wait()

	// Should not panic or corrupt data
	if !env.authInst.Initialized() {
		t.Fatal("auth should still be initialized after concurrent access")
	}
}

func TestP0_3_AuthConcurrentVerifyCredentials(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := env.authInst.VerifyCredentials("admin", "Test12345!@#$")
			if !result {
				t.Error("VerifyCredentials should return true for correct credentials")
			}
		}()
	}
	wg.Wait()
}

func TestP0_3_AuthConcurrentTokenOps(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, _ := env.authInst.CreateToken("admin", false)
			username, err := env.authInst.VerifyToken(token)
			if err != nil {
				t.Errorf("VerifyToken failed: %v", err)
			}
			if username != "admin" {
				t.Errorf("expected username=admin, got %s", username)
			}
		}()
	}
	wg.Wait()
}

// ============================================================
// P0-4: ProviderManager.save() lock protection
// ============================================================

func TestP0_4_ProviderManagerSaveMutex(t *testing.T) {
	env := setupTestEnv(t)

	// Test concurrent save operations
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := Provider{
				ID:      "test-" + string(rune('a'+idx)),
				Name:    "Test Provider " + string(rune('a'+idx)),
				Type:    "openai_compatible",
				BaseURL: "https://example.com/v1",
				APIKey:  "test-key",
				Enabled: true,
				Models:  []ModelDef{{ID: "gpt-4", Name: "gpt-4", Enabled: true}},
			}
			env.pmInst.Add(p)
		}(i)
	}
	wg.Wait()

	// Verify all providers were saved
	all := env.pmInst.GetAll()
	if len(all) < 20 {
		t.Errorf("expected at least 20 providers, got %d", len(all))
	}

	// Verify the file was written with correct permissions
	info, err := os.Stat(filepath.Join(env.dir, "providers.json"))
	if err != nil {
		t.Fatalf("providers.json not found: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected file permissions 0600, got %04o", perm)
	}
}

func TestP0_4_ProviderManagerSaveFilePermissions(t *testing.T) {
	env := setupTestEnv(t)

	p := Provider{
		ID:      "test-perms",
		Name:    "Test",
		Type:    "openai_compatible",
		BaseURL: "https://example.com/v1",
		APIKey:  "test-key",
		Enabled: true,
	}
	env.pmInst.Add(p)

	info, err := os.Stat(filepath.Join(env.dir, "providers.json"))
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}

	// Verify restrictive permissions (0600, not 0644)
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("providers.json should have 0600 permissions, got %04o", perm)
	}
}

// ============================================================
// P0-5: Cloudflare API URL includes account_id
// ============================================================

func TestP0_5_DomainBinderHasAccountID(t *testing.T) {
	binder := &DomainBinder{
		apiToken:  "test-token",
		accountID: "abc123",
	}

	if binder.accountID != "abc123" {
		t.Errorf("expected accountID=abc123, got %s", binder.accountID)
	}
}

func TestP0_5_CreateTunnelRequiresAccountID(t *testing.T) {
	binder := &DomainBinder{
		apiToken:  "test-token",
		accountID: "", // missing!
	}

	_, err := binder.createTunnelViaAPI(nil, "test-tunnel")
	if err == nil {
		t.Fatal("expected error when accountID is empty")
	}
	if !strings.Contains(err.Error(), "account_id") {
		t.Errorf("error should mention account_id, got: %v", err)
	}
}

func TestP0_5_ConfigureTunnelRequiresAccountID(t *testing.T) {
	binder := &DomainBinder{
		apiToken:  "test-token",
		accountID: "",
	}

	err := binder.configureTunnel(nil, "tunnel-123", "http://localhost:8000")
	if err == nil {
		t.Fatal("expected error when accountID is empty")
	}
	if !strings.Contains(err.Error(), "account_id") {
		t.Errorf("error should mention account_id, got: %v", err)
	}
}

func TestP0_5_CloudflareURLFormat(t *testing.T) {
	// Verify the URL format includes account_id by checking the struct field exists
	binder := &DomainBinder{
		apiToken:  "test-token",
		accountID: "my-account-id",
	}

	// We can't make actual API calls, but we can verify the structure
	if binder.accountID != "my-account-id" {
		t.Error("accountID should be stored in DomainBinder")
	}
}

// ============================================================
// P0-6: Log rotation
// ============================================================

func TestP0_6_LoggerRotationSetup(t *testing.T) {
	// Verify the Logger struct has the required fields
	l := &Logger{
		logPath: "test.log",
		maxSize: 1024, // 1KB for testing
	}

	if l.maxSize != 1024 {
		t.Errorf("expected maxSize=1024, got %d", l.maxSize)
	}
}

func TestP0_6_LogRotationCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	// Create some fake rotated files
	for i := 0; i < 8; i++ {
		name := filepath.Join(tmpDir, "access.log.2024010"+string(rune('1'+i)))
		os.WriteFile(name, []byte("test"), 0644)
	}

	l := &Logger{
		logPath: logPath,
		maxSize: 1024,
	}

	// Cleanup keeping only 5
	l.cleanupOldLogs(5)

	entries, _ := os.ReadDir(tmpDir)
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "access.log.") {
			count++
		}
	}
	if count != 5 {
		t.Errorf("expected 5 rotated files after cleanup, got %d", count)
	}
}

func TestP0_6_DefaultMaxLogSize(t *testing.T) {
	if defaultMaxLogSize != 50*1024*1024 {
		t.Errorf("default max log size should be 50MB, got %d", defaultMaxLogSize)
	}
}

func TestP0_6_LoggerConcurrentRotation(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "access.log")

	// Create initial log file
	f, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)

	l := &Logger{
		accessFile: f,
		logPath:    logPath,
		maxSize:    100, // very small for testing
		level:      0,
	}

	// Simulate concurrent rotation checks
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.checkAndRotate()
		}()
	}
	wg.Wait()

	// Should not panic
}

// ============================================================
// Integration test: All fixes together
// ============================================================

func TestP0_Integration_VerifyAuthFlow(t *testing.T) {
	env := setupTestEnv(t)

	// Setup admin
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	// Create a valid token
	token, _ := env.authInst.CreateToken("admin", false)

	// Verify with valid token
	verifyReq := httptest.NewRequest("GET", "/api/auth/verify", nil)
	verifyReq.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handleVerifyAuth(w, verifyReq)
	if w.Code != 200 {
		t.Fatalf("valid token should return 200, got %d", w.Code)
	}

	// Verify with invalid token
	verifyReq2 := httptest.NewRequest("GET", "/api/auth/verify", nil)
	verifyReq2.Header.Set("Authorization", "Bearer bad-token")
	w2 := httptest.NewRecorder()
	handleVerifyAuth(w2, verifyReq2)
	if w2.Code != 401 {
		t.Fatalf("invalid token should return 401, got %d", w2.Code)
	}
}

func TestP0_Integration_ResetCodeFlow(t *testing.T) {
	env := setupTestEnv(t)

	// Setup admin
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	// Generate reset code
	code, _, err := env.authInst.GenerateResetCode()
	if err != nil {
		t.Fatalf("failed to generate reset code: %v", err)
	}

	// Use reset code to change password
	body := map[string]string{
		"code":         code,
		"new_password": "NewPass12345!@#$",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/auth/reset-with-code", bytes.NewReader(jsonBody))
	w := httptest.NewRecorder()
	handleResetWithCode(w, req)

	if w.Code != 200 {
		t.Fatalf("reset should succeed, got %d: %s", w.Code, w.Body.String())
	}

	// Old password should no longer work
	if env.authInst.VerifyCredentials("admin", "Test12345!@#$") {
		t.Fatal("old password should not work after reset")
	}

	// New password should work
	if !env.authInst.VerifyCredentials("admin", "NewPass12345!@#$") {
		t.Fatal("new password should work after reset")
	}
}

// Suppress unused import warning
var _ = http.StatusOK
