package main

// p1_fixes_test.go — regression tests for batch-2 fixes:
//   - P1-1: withProxyAuth accepts a signed relay/gateway forward (no Authorization
//     header) so inter-node forwarding can reach the inner gateway handler; forged
//     or unsigned forwards are still rejected.
//   - P1-2: GetNode returns a deep copy so callers cannot mutate federation
//     state concurrently.
//   - P1-5: a guest key relayed to self survives Authorization stripping via
//     the request context, so the D-4 per-key quota check still accounts for it.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A legitimately signed gateway forward must pass through withProxyAuth and
// reach the inner handler (previously it died with 401 at the middleware).
func TestWithProxyAuth_AcceptsSignedForward(t *testing.T) {
	relayTestEnv(t)

	reached := false
	h := withProxyAuth(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	body := []byte(`{"model":"gpt-4","messages":[]}`)
	req := newSignedRelayRequest(t, http.MethodPost, "/v1/chat/completions", body)
	rec := httptest.NewRecorder()
	h(rec, req)

	if !reached {
		t.Fatalf("signed relay forward must reach the proxy handler, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// A forged relay identity claim (X-Node-ID set but garbage signature) must be
// rejected at withProxyAuth.
func TestWithProxyAuth_RejectsForgedForward(t *testing.T) {
	relayTestEnv(t)

	reached := false
	h := withProxyAuth(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	body := []byte(`{"model":"gpt-4"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("X-Node-ID", node.NodeID())
	req.Header.Set("X-Node-Auth", node.NodeID())
	req.Header.Set(headerRelaySig, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==")
	req.Header.Set(headerRelayTs, "2026-01-01T00:00:00Z")

	rec := httptest.NewRecorder()
	h(rec, req)

	if reached {
		t.Fatal("forged relay forward must NOT reach the handler")
	}
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("forged relay forward expected 401/403, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// P1-2: GetNode must not hand out the internal NodeInfo pointer. Mutating the
// returned value must not corrupt federation state.
func TestGetNode_ReturnsCopy(t *testing.T) {
	relayTestEnv(t) // registers node in fed trust pool

	nodeID := node.NodeID()
	before, ok := fed.GetNode(nodeID)
	if !ok {
		t.Fatal("expected node in federation set")
	}

	// Mutate the returned struct.
	before.Status = "hacked"
	before.Endpoint = "http://evil.local"

	// A second read must be unaffected.
	after, ok := fed.GetNode(nodeID)
	if !ok {
		t.Fatal("node vanished after mutation")
	}
	if after.Status == "hacked" {
		t.Fatal("P1-2: GetNode returned the internal pointer; caller mutation corrupted federation state")
	}
	if after.Status != "active" {
		t.Fatalf("status corrupted: got %q want active", after.Status)
	}
}

// P1-5: a guest key must survive the relay-to-self dispatch. handleRelayToLocal
// strips the Authorization header (so the key never reaches provider code), but
// the D-4 per-key quota check still needs the key — it is carried via context.
// Without this, a guest key could drain the shared pool with no quota accounting.
func TestHandleRelayToLocal_GuestKeySurvivesViaContext(t *testing.T) {
	env := relaySecurityTestEnv(t)
	_ = env

	guestKey := "sk-guest-mmx-self-0000deadbeef"

	origStore := guestKeyStore
	guestKeyStore = &GuestKeyStore{
		keys: []*GuestKeyRecord{
			{Key: guestKey, NodeID: "mmx-self", Revoked: false, Quota: 1000},
		},
	}
	defer func() { guestKeyStore = origStore }()

	// Personal mode: guest keys stay "guest" (no public-pool promotion), so the
	// D-4 per-key quota branch is the one that must see the context key.
	origMode := netMgr.config.Mode
	netMgr.config.Mode = NetworkModePersonal
	defer func() { netMgr.config.Mode = origMode }()

	origDispatch := relayDispatchHandler
	defer func() { relayDispatchHandler = origDispatch }()

	var innerReq *http.Request
	var innerKeyType string
	relayDispatchHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerReq = r
		innerKeyType = RequestKeyType(r)
		w.WriteHeader(http.StatusOK)
	})

	// A regular (non-public-pool) guest key routed to its issuing node.
	req := httptest.NewRequest(http.MethodGet, "/network/mmx-self/v1/models", nil)
	req.RemoteAddr = "203.0.113.50:1234"
	req.Header.Set("Authorization", "Bearer "+guestKey)
	rec := httptest.NewRecorder()

	handleRelayToLocal(rec, req, []string{"mmx-self", "v1/models"}, 0)

	if innerReq == nil {
		t.Fatalf("request did not dispatch to relayDispatchHandler (code=%d)", rec.Code)
	}
	// The key must be available to the inner handler via context…
	if got := relayGuestKey(innerReq); got != guestKey {
		t.Fatalf("P1-5: relayGuestKey(inner) = %q, want %q — quota bypass over relay path", got, guestKey)
	}
	// …while the Authorization header itself is gone.
	if h := innerReq.Header.Get("Authorization"); h != "" {
		t.Fatalf("Authorization header must be stripped before dispatch, got %q", h)
	}
	// The effective key type derives from context, so the D-4 branch activates.
	if innerKeyType != "guest" {
		t.Fatalf("RequestKeyType(inner) = %q, want guest — D-4 quota check would be skipped", innerKeyType)
	}
}

// P1-5 (D-4): the quota check must consume the context-carried key when the
// Authorization header is absent — i.e. the exact wire shape of a relayed guest
// request. Simulates the decision the D-4 block makes.
func TestGuestQuota_UsesContextKey_WhenHeaderStripped(t *testing.T) {
	relaySecurityTestEnv(t)

	guestKey := "sk-guest-mmx-self-0000beef"

	origStore := guestKeyStore
	origUsage := guestKeyUsage
	guestKeyStore = &GuestKeyStore{
		keys: []*GuestKeyRecord{
			{Key: guestKey, NodeID: "mmx-self", Revoked: false, Quota: 100},
		},
	}
	guestKeyUsage = &guestKeyUsageTracker{usage: make(map[string]int64)}
	defer func() {
		guestKeyStore = origStore
		guestKeyUsage = origUsage
	}()

	// Mimic handleRelayToLocal: Authorization stripped, key in context.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4"}`))
	req = withInternalKeyType(req, "guest")
	req = withGuestKey(req, guestKey)
	req.Header.Del("Authorization")

	// D-4 decision logic (as implemented in handlers.go): resolve the key.
	auth := req.Header.Get("Authorization")
	key := strings.TrimPrefix(auth, "Bearer ")
	if ctxKey := relayGuestKey(req); ctxKey != "" {
		key = ctxKey
	}
	if key != guestKey {
		t.Fatalf("D-4 resolved key = %q, want %q", key, guestKey)
	}

	record := guestKeyStore.GetGuestKeyRecord(key)
	if record == nil || record.Quota == 0 {
		t.Fatal("guest key record must be found")
	}
	allowed, _ := guestKeyUsage.CheckAndReserve(key, record.Quota, 80)
	if !allowed {
		t.Fatal("first request within quota should be allowed")
	}
	// A second draw pushes the tracked usage past the quota — must be denied.
	allowed2, _ := guestKeyUsage.CheckAndReserve(key, record.Quota, 80)
	if allowed2 {
		t.Fatal("quota must be enforced over the relay path (P1-5)")
	}
}