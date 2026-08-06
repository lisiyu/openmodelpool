package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ============================================================
// G5: admin usage / reliability statistics integration tests
//
// These tests verify that the admin health/status endpoint surfaces REAL
// values sourced from the existing trackers (usage tracker + connection
// tracker) instead of the previously hardcoded placeholder zeros/nils.
// ============================================================

// callHealth invokes the admin health/status handler through the withAuth
// middleware (mirroring server.go routing) and returns the decoded payload.
func callHealth(t *testing.T, token string) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	withAuth(handleHealthStatus)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/health expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}
	return resp
}

// findProvider extracts a single provider entry from the health response.
func findProvider(t *testing.T, resp map[string]any, id string) map[string]any {
	t.Helper()
	providers, ok := resp["providers"].([]any)
	if !ok {
		t.Fatal("providers field missing or wrong type")
	}
	for _, pr := range providers {
		m, ok := pr.(map[string]any)
		if !ok {
			continue
		}
		if m["provider_id"] == id {
			return m
		}
	}
	t.Fatalf("provider %q not found in health response", id)
	return nil
}

// setupStatsEnv builds an isolated test env with an admin, a configured
// provider, and a running health checker (global used by handleHealthStatus).
func setupStatsEnv(t *testing.T) *testEnv {
	t.Helper()
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	// Add a configured provider with an API key so it appears in health output.
	p := makeProvider("p1", "TestProvider", makeModelDef("gpt-4o", "gpt-3.5-turbo"), 1, true)
	env.pmInst.Add(p)

	// Initialize the health checker (global used by handleHealthStatus).
	// We only need a non-nil instance with an empty status map; the periodic
	// probe goroutine is intentionally NOT started to keep the test hermetic.
	healthChecker = &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
		interval: time.Minute,
		stopCh:   make(chan struct{}),
	}
	t.Cleanup(func() { close(healthChecker.stopCh) })

	return env
}

// TestAdminStats_SuccessRateFromTracker verifies that the per-pool and
// node-level success rate are computed from the usage tracker (9/10 = 90%),
// not a hardcoded nil.
func TestAdminStats_SuccessRateFromTracker(t *testing.T) {
	env := setupStatsEnv(t)
	token, _ := env.authInst.CreateToken("admin", false)

	// Record 9 successful + 1 failed request for p1 this month.
	for i := 0; i < 9; i++ {
		env.tkInst.RecordWithAccessType("p1", "TestProvider", "gpt-4o", 10, 20, 100, true, "", false, 0, "private")
	}
	env.tkInst.RecordWithAccessType("p1", "TestProvider", "gpt-4o", 10, 20, 100, false, "boom", false, 0, "private")

	resp := callHealth(t, token)
	p1 := findProvider(t, resp, "p1")

	sr, ok := p1["success_rate"]
	if !ok || sr == nil {
		t.Fatal("expected non-nil success_rate for p1")
	}
	srVal := sr.(float64)
	if srVal < 89.9 || srVal > 90.1 {
		t.Fatalf("expected success_rate ~90.0, got %v", srVal)
	}

	nodeStats, ok := resp["node_stats"].(map[string]any)
	if !ok {
		t.Fatal("node_stats missing")
	}
	nsSR, ok := nodeStats["success_rate"]
	if !ok || nsSR == nil {
		t.Fatal("expected non-nil node_stats.success_rate")
	}
	nsSRVal := nsSR.(float64)
	if nsSRVal < 89.9 || nsSRVal > 90.1 {
		t.Fatalf("expected node_stats.success_rate ~90.0, got %v", nsSRVal)
	}
}

// TestAdminStats_NoTrafficSuccessRateIsNil verifies the honest "no data"
// behaviour: with zero requests the success rate must be nil (not a fabricated
// 0% or 100%).
func TestAdminStats_NoTrafficSuccessRateIsNil(t *testing.T) {
	env := setupStatsEnv(t)
	token, _ := env.authInst.CreateToken("admin", false)

	resp := callHealth(t, token)
	p1 := findProvider(t, resp, "p1")
	if p1["success_rate"] != nil {
		t.Errorf("expected nil success_rate when no traffic, got %v", p1["success_rate"])
	}
	nodeStats, _ := resp["node_stats"].(map[string]any)
	if nodeStats["success_rate"] != nil {
		t.Errorf("expected nil node_stats.success_rate when no traffic, got %v", nodeStats["success_rate"])
	}
}

// TestAdminStats_PerPoolAndPerGuest verifies that per-pool today stats reflect
// the real access-type split and that the per-guest connection count is a real
// value from the connection tracker (no longer a hardcoded 0).
func TestAdminStats_PerPoolAndPerGuest(t *testing.T) {
	env := setupStatsEnv(t)
	token, _ := env.authInst.CreateToken("admin", false)

	// Record private/public/guest requests for p1 (guest has 1 success + 1 fail).
	env.tkInst.RecordWithAccessType("p1", "TestProvider", "gpt-4o", 10, 20, 100, true, "", false, 0, "private")
	env.tkInst.RecordWithAccessType("p1", "TestProvider", "gpt-4o", 10, 20, 100, true, "", false, 0, "public")
	env.tkInst.RecordWithAccessType("p1", "TestProvider", "gpt-4o", 10, 20, 100, true, "", false, 0, "guest")
	env.tkInst.RecordWithAccessType("p1", "TestProvider", "gpt-4o", 10, 20, 100, false, "err", false, 0, "guest")

	// Simulate 2 active guest connections.
	IncrConn("p1", "guest")
	IncrConn("p1", "guest")
	defer func() {
		DecrConn("p1", "guest")
		DecrConn("p1", "guest")
	}()

	resp := callHealth(t, token)
	p1 := findProvider(t, resp, "p1")

	if got := int(p1["today_reqs_private"].(float64)); got != 1 {
		t.Errorf("today_reqs_private expected 1, got %d", got)
	}
	if got := int(p1["today_reqs_public"].(float64)); got != 1 {
		t.Errorf("today_reqs_public expected 1, got %d", got)
	}
	if got := int(p1["today_reqs_guest"].(float64)); got != 2 {
		t.Errorf("today_reqs_guest expected 2, got %d", got)
	}
	// 30 tokens (10 prompt + 20 completion) per private record.
	if got := int(p1["today_tokens_private"].(float64)); got != 30 {
		t.Errorf("today_tokens_private expected 30, got %d", got)
	}

	// conns_guest must be the real counter value, not a hardcoded 0.
	if GetGuestConns() != 2 {
		t.Fatalf("GetGuestConns expected 2, got %d", GetGuestConns())
	}
	nodeStats, _ := resp["node_stats"].(map[string]any)
	if got := int(nodeStats["conns_guest"].(float64)); got != 2 {
		t.Errorf("conns_guest expected 2, got %d", got)
	}
}

// TestAdminStats_QuotaReflectsUsage verifies that quota "used" fields mirror
// real tracker usage (legacy single-key provider maps tracker total tokens to
// quota_private_used / token_used / today_tokens).
func TestAdminStats_QuotaReflectsUsage(t *testing.T) {
	env := setupStatsEnv(t)
	token, _ := env.authInst.CreateToken("admin", false)

	// Record 5 successful requests, each 30 tokens -> 150 total tokens.
	for i := 0; i < 5; i++ {
		env.tkInst.RecordWithAccessType("p1", "TestProvider", "gpt-4o", 10, 20, 100, true, "", false, 0, "private")
	}

	resp := callHealth(t, token)
	p1 := findProvider(t, resp, "p1")

	tokenUsed := int(p1["token_used"].(float64))
	if tokenUsed != 150 {
		t.Errorf("token_used expected 150, got %d", tokenUsed)
	}
	quotaPrivUsed := int(p1["quota_private_used"].(float64))
	if quotaPrivUsed != 150 {
		t.Errorf("quota_private_used expected 150 (mirrors tracker), got %d", quotaPrivUsed)
	}
	todayTokens := int(p1["today_tokens"].(float64))
	if todayTokens != 150 {
		t.Errorf("today_tokens expected 150, got %d", todayTokens)
	}
}
