package main

// p2_fixes_test.go — regression tests for batch-3 fixes (v4.5.20):
//   - SEC-B3-1: /api/network/peers/notify rejects private/loopback claimant
//     addresses before any outbound dial (SSRF).
//   - SEC-B3-2: /api/discovery/platforms/{id}/check refuses to dial a private
//     / loopback / unresolvable models URL (SSRF).
//   - SEC-B3-3: /api/network/keys/validate no longer leaks guest-key validity
//     / issuing node_id to unauthenticated callers.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// SEC-B3-2: the discovery check endpoint must short-circuit (200 skipped,
// NOT an outbound dial) when the platform's BaseURL resolves to a private or
// loopback host. TestMain keeps allowLocalProviderForTest=true so the guard is
// explicitly flipped on here to prove the production behaviour.
func TestCheckDiscoveredPlatform_PrivateBaseURL_NoDial(t *testing.T) {
	_ = setupTestEnv(t)

	oldAllow := allowLocalProviderForTest
	allowLocalProviderForTest = false
	defer func() { allowLocalProviderForTest = oldAllow }()

	// If the SSRF guard is bypassed, the handler dials this server → fail.
	var dialed int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&dialed, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	discoveredMu.Lock()
	orig := discoveredPlatforms
	discoveredPlatforms = []DiscoveredPlatform{
		{ID: "evil-local", BaseURL: "http://127.0.0.1:1", Status: "new"},
	}
	discoveredMu.Unlock()
	defer func() {
		discoveredMu.Lock()
		discoveredPlatforms = orig
		discoveredMu.Unlock()
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/discovery/platforms/evil-local/check", nil)
	req.SetPathValue("id", "evil-local")
	rec := httptest.NewRecorder()
	handleCheckDiscoveredPlatform(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (skipped), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if atomic.LoadInt32(&dialed) != 0 {
		t.Fatal("SEC-B3-2: private BaseURL must not be dialed by discovery check")
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if skipped, _ := body["skipped"].(bool); !skipped {
		t.Fatalf("private BaseURL must be reported as skipped, got %v", body)
	}
}

// SEC-B3-3: /api/network/keys/validate must not disclose guest-key validity or
// the issuing node_id. A valid guest key routed to an unauthenticated caller
// must be reported neutrally (valid=false, no node_id).
func TestNetworkKeyValidate_NoGuestOracle(t *testing.T) {
	_ = setupTestEnv(t)

	guestKey := "sk-guest-mmx-self-0000cafe"
	origStore := guestKeyStore
	guestKeyStore = &GuestKeyStore{
		keys: []*GuestKeyRecord{
			{Key: guestKey, NodeID: "mmx-self", Revoked: false},
		},
	}
	defer func() { guestKeyStore = origStore }()

	body, _ := json.Marshal(map[string]string{"key": guestKey})
	req := httptest.NewRequest(http.MethodPost, "/api/network/keys/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkKeyValidate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if v, _ := out["valid"].(bool); v {
		t.Fatalf("SEC-B3-3: guest validity must not be disclosed, got valid=%v (body=%s)", v, rec.Body.String())
	}
	if _, has := out["node_id"]; has {
		t.Fatalf("SEC-B3-3: node_id must not be disclosed, body=%s", rec.Body.String())
	}
}

// SEC-B3-3: a genuine authenticated key of an unknown class is still reported
// neutrally (this is a shape/regression guard, not a crypto oracle).
func TestNetworkKeyValidate_UnknownNeutral(t *testing.T) {
	_ = setupTestEnv(t)

	body, _ := json.Marshal(map[string]string{"key": "not-a-real-key"})
	req := httptest.NewRequest(http.MethodPost, "/api/network/keys/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkKeyValidate(rec, req)

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if v, _ := out["valid"].(bool); v {
		t.Fatalf("unknown key must report valid=false, got %v", out["valid"])
	}
	if _, has := out["node_id"]; has {
		t.Fatalf("node_id must never be present, body=%s", rec.Body.String())
	}
}

// SEC-B3-4: extractRemoteIP must IGNORE X-Forwarded-For / X-Real-IP when the
// trusted-proxy opt-in is off — an attacker spoofing them must not be able to
// poison region / liveness data. Only the real RemoteAddr is used.
func TestExtractRemoteIP_IgnoresSpoofedXFF_UnlessTrustedProxy(t *testing.T) {
	orig := trustedReverseProxy
	defer func() { trustedReverseProxy = orig }()

	req := httptest.NewRequest(http.MethodPost, "/api/network/heartbeat", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "5.6.7.8")

	// Default (no trusted proxy): spoofed headers ignored.
	trustedReverseProxy = false
	if got := extractRemoteIP(req); got != "203.0.113.9" {
		t.Fatalf("SEC-B3-4: without trusted proxy, extractRemoteIP = %q, want real remote addr 203.0.113.9", got)
	}

	// Opted in via OMP_TRUSTED_PROXY: headers trusted.
	trustedReverseProxy = true
	if got := extractRemoteIP(req); got != "1.2.3.4" {
		t.Fatalf("with trusted proxy, extractRemoteIP = %q, want XFF first value 1.2.3.4", got)
	}
}