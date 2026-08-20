package main

// p4_fixes_test.go — regression tests for batch-5 fixes (v4.5.22):
//   - SEC-B5-1: usage endpoints are scoped to the requesting consumer.
//   - SEC-B5-2: relay-to-local strips spoofed X-Request-Owner/X-Request-Role.
//   - SEC-B5-3: discovery platform update is admin-only.
//   - SEC-B5-5: heartbeat region accepts only the coarse label (self-reported
//     lat/long discarded).
//   - SEC-B5-7: per-IP rate limiter uses X-Forwarded-For under a trusted proxy.
//   - SEC-B5-9: heartbeat malformed body does not produce a double write.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SEC-B5-1: a consumer must not see another consumer's (or public) usage.
func TestB5x_UsageRecords_ScopedToOwner(t *testing.T) {
	setupTestEnv(t)

	tracker.RecordWithOwner("p1", "P1", "gpt-4", 100, 50, 200.0, true, "", false, 0, "private", "consumer-a")
	tracker.RecordWithOwner("p2", "P2", "gpt-4", 10, 5, 100.0, true, "", false, 0, "private", "consumer-b")
	tracker.RecordWithOwner("p3", "P3", "gpt-4o", 20, 10, 50.0, true, "", false, 0, "public", "") // public traffic

	// Consumer A sees only its own record.
	rA := consumerRequest("GET", "/api/usage/records", "", "consumer-a")
	wA := httptest.NewRecorder()
	handleUsageRecords(wA, rA)
	var outA struct {
		Records []UsageRecord `json:"records"`
	}
	if err := json.Unmarshal(wA.Body.Bytes(), &outA); err != nil {
		t.Fatalf("consumer A response not JSON: %v", err)
	}
	if len(outA.Records) != 1 || outA.Records[0].Owner != "consumer-a" {
		t.Fatalf("SEC-B5-1: consumer A expected exactly 1 owned record, got %d (body=%s)", len(outA.Records), wA.Body.String())
	}

	// Admin sees all three.
	rAdmin := consumerRequest("GET", "/api/usage/records", "", "")
	wAdmin := httptest.NewRecorder()
	handleUsageRecords(wAdmin, rAdmin)
	var outAdmin struct {
		Records []UsageRecord `json:"records"`
	}
	if err := json.Unmarshal(wAdmin.Body.Bytes(), &outAdmin); err != nil {
		t.Fatalf("admin response not JSON: %v", err)
	}
	if len(outAdmin.Records) != 3 {
		t.Fatalf("SEC-B5-1: admin expected 3 records, got %d", len(outAdmin.Records))
	}
}

// SEC-B5-1: usage summary scoped to a consumer reflects only that consumer.
func TestB5x_UsageSummary_ScopedToOwner(t *testing.T) {
	setupTestEnv(t)
	tracker.RecordWithOwner("p1", "P1", "gpt-4", 100, 0, 100.0, true, "", false, 0, "private", "consumer-a")
	tracker.RecordWithOwner("p2", "P2", "gpt-4", 900, 0, 900.0, true, "", false, 0, "private", "consumer-b")

	rA := consumerRequest("GET", "/api/usage/summary", "", "consumer-a")
	wA := httptest.NewRecorder()
	handleUsageSummary(wA, rA)
	var outA map[string]any
	if err := json.Unmarshal(wA.Body.Bytes(), &outA); err != nil {
		t.Fatalf("summary response not JSON: %v", err)
	}
	if n := outA["total_records"].(float64); n != 1 {
		t.Fatalf("SEC-B5-1: consumer A total_records expected 1, got %v", n)
	}
}

// SEC-B5-2: handleRelayToLocal strips attacker-supplied X-Request-Owner before
// dispatching, so per-consumer rate limiting / logging attribute to the real
// (or no) owner.
func TestB5x_RelayToLocal_StripsSpoofedOwnerHeader(t *testing.T) {
	relaySecurityTestEnv(t)

	origDispatch := relayDispatchHandler
	defer func() { relayDispatchHandler = origDispatch }()

	var captured *http.Request
	relayDispatchHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(context.Background())
		w.WriteHeader(http.StatusOK)
	})

	// Public key via relay with a forged owner header.
	req := httptest.NewRequest(http.MethodPost, "/network/mmx-self/v1/chat/completions", strings.NewReader(`{"model":"gpt-4"}`))
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("Authorization", "Bearer "+PublicKeyValue)
	req.Header.Set("X-Request-Owner", "victim-consumer")
	req.Header.Set("X-Request-Role", "consumer")
	rec := httptest.NewRecorder()
	handleRelayToLocal(rec, req, []string{"mmx-self", "v1/chat/completions"}, 0)

	if captured == nil {
		t.Fatal("relay dispatch handler was not invoked")
	}
	if owner := captured.Header.Get("X-Request-Owner"); owner != "" {
		t.Fatalf("SEC-B5-2: spoofed X-Request-Owner=%q survived relay stripping", owner)
	}
	if role := captured.Header.Get("X-Request-Role"); role != "" {
		t.Fatalf("SEC-B5-2: spoofed X-Request-Role=%q survived relay stripping", role)
	}
}

// SEC-B5-3: discovery platform update requires admin auth (withAuth). Verify
// the route registration changed — consumer-role request without an admin JWT
// must be rejected by the middleware.
func TestB5x_DiscoveryPlatformUpdate_AdminOnly(t *testing.T) {
	setupTestEnv(t)
	r := httptest.NewRequest(http.MethodPut, "/api/discovery/platforms/x", strings.NewReader(`{"status":"dismissed"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withAuth(handleUpdateDiscoveredPlatform)(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("SEC-B5-3: discovery update without admin token expected 401, got %d", w.Code)
	}
}

// SEC-B5-5: heartbeat region processing must ignore self-reported latitude /
// longitude (only the coarse region label is honored).
func TestB5x_HeartbeatRegion_IgnoresSelfReportedCoords(t *testing.T) {
	setupTestEnv(t)

	old := regionManager
	defer func() { regionManager = old }()
	regionManager = NewRegionManager()

	cfg.Set("federation_secret", "shared-secret")
	body := `{"node_id":"mmx-peer","region":"ap","sub_region":"shanghai","latitude":31.23,"longitude":121.47}`
	r := httptest.NewRequest(http.MethodPost, "/api/network/heartbeat", strings.NewReader(body))
	r.Header.Set("X-Federation-Secret", "shared-secret")
	w := httptest.NewRecorder()
	handleNetworkHeartbeat(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	nr, ok := regionManager.nodes["mmx-peer"]
	if !ok {
		t.Fatal("node region was not registered")
	}
	if nr.Latitude != 0 || nr.Longitude != 0 || nr.SubRegion != "" {
		t.Fatalf("SEC-B5-5: self-reported coords must be discarded, got %+v", nr)
	}
	if nr.Region != RegionAsiaPacific {
		t.Fatalf("coarse region label expected ap, got %q", nr.Region)
	}
}

// SEC-B5-7: under a trusted reverse proxy the per-IP limiter uses the real
// client IP from X-Forwarded-For, not the shared proxy RemoteAddr.
func TestB5x_RateLimitByIP_TrustedProxyXFF(t *testing.T) {
	old := trustedReverseProxy
	defer func() { trustedReverseProxy = old }()
	trustedReverseProxy = true

	// A tiny quota so a single "client" exhausts it fast.
	handler := rateLimitByIP(1.0, "b5x_test")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Same proxy RemoteAddr, distinct XFF clients must have SEPARATE buckets.
	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	r1.RemoteAddr = "10.0.0.9:1234"
	r1.Header.Set("X-Forwarded-For", "203.0.113.1")
	w1 := httptest.NewRecorder()
	handler(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("client 1 first request expected 200, got %d", w1.Code)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "10.0.0.9:1234" // same proxy IP
	r2.Header.Set("X-Forwarded-For", "203.0.113.2")
	w2 := httptest.NewRecorder()
	handler(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("SEC-B5-7: distinct XFF client shared the proxy IP bucket, got %d", w2.Code)
	}

	// Second request from the SAME XFF client is now rate-limited.
	r1b := httptest.NewRequest(http.MethodGet, "/", nil)
	r1b.RemoteAddr = "10.0.0.9:1234"
	r1b.Header.Set("X-Forwarded-For", "203.0.113.1")
	w1b := httptest.NewRecorder()
	handler(w1b, r1b)
	if w1b.Code != http.StatusTooManyRequests {
		t.Fatalf("same XFF client second request expected 429, got %d", w1b.Code)
	}
}

// SEC-B5-9: a malformed heartbeat body must not produce a corrupted double
// response (it should simply be treated as an empty body).
func TestB5x_Heartbeat_MalformedBody_NoDoubleWrite(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("federation_secret", "shared-secret")
	r := httptest.NewRequest(http.MethodPost, "/api/network/heartbeat", strings.NewReader("not json {{"))
	r.Header.Set("X-Federation-Secret", "shared-secret")
	r.Header.Set("X-Node-ID", "mmx-peer")
	w := httptest.NewRecorder()
	handleNetworkHeartbeat(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("heartbeat with malformed body expected 200 (treated as empty), got %d (body=%s)", w.Code, w.Body.String())
	}
}