package main

// relay_security_test.go — regression tests for SEC-P0-1 (relay-to-self
// authentication bypass) and SEC-P0-2 (client-spoofable X-OMP-KeyType header).
//
// Coverage:
//   ① X-OMP-KeyType is never trusted from the wire (RequestKeyType + strip).
//   ② relayAuthMiddleware rejects anonymous /network requests and accepts
//     recognized API keys; __punch stays exempt.
//   ③ handleRelayToLocal enforces the path whitelist and dispatches in-process
//     preserving the original RemoteAddr.
//   ④ withProxyAuth / localOnly treat relay-dispatched requests as untrusted
//     remote (no anonymous-admin, no loopback trust).
//   ⑤ relayToRemote strips X-OMP-KeyType from outbound forwards.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// relaySecurityTestEnv wires the minimal globals needed by the relay path.
func relaySecurityTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := setupTestEnv(t)

	origNetMgr := netMgr
	origDispatch := relayDispatchHandler
	nm := &NetworkManager{
		config: NetworkConfig{
			NodeID:         "mmx-self",
			NetworkEnabled: true,
			Mode:           NetworkModeShared,
		},
	}
	netMgr = nm
	relayDispatchHandler = setupRoutes()
	t.Cleanup(func() {
		netMgr = origNetMgr
		relayDispatchHandler = origDispatch
	})
	return env
}

// ① RequestKeyType must ignore the client-supplied X-OMP-KeyType header and
// instead use the internal context value (or the verified token).
func TestRequestKeyType_IgnoresWireHeader(t *testing.T) {
	relaySecurityTestEnv(t)

	// A forged header must NOT be trusted.
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-OMP-KeyType", "admin")
	if kt := RequestKeyType(req); kt != "unknown" {
		t.Fatalf("forged X-OMP-KeyType=admin produced key type %q, want unknown (fail-closed)", kt)
	}

	// The internal context value IS trusted (set by our own relay).
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req2.Header.Set("X-OMP-KeyType", "admin") // forged header must still lose
	req2 = withInternalKeyType(req2, "public")
	if kt := RequestKeyType(req2); kt != "public" {
		t.Fatalf("internal key type expected public, got %q", kt)
	}
}

// stripInternalHeadersMiddleware removes X-OMP-KeyType before any handler.
func TestStripInternalHeadersMiddleware(t *testing.T) {
	relaySecurityTestEnv(t)

	var got string
	h := stripInternalHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-OMP-KeyType")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-OMP-KeyType", "admin")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "" {
		t.Fatalf("strip middleware left X-OMP-KeyType=%q", got)
	}
}

// ② relayAuthMiddleware gates the /network route.
func TestRelayAuthMiddleware(t *testing.T) {
	relaySecurityTestEnv(t)

	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

	// Anonymous /network request -> 401.
	anon := httptest.NewRequest(http.MethodGet, "/network/mmx-self/v1/models", nil)
	rec := httptest.NewRecorder()
	relayAuthMiddleware(ok)(rec, anon)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous relay expected 401, got %d", rec.Code)
	}

	// Public key -> allowed.
	pub := httptest.NewRequest(http.MethodGet, "/network/mmx-self/v1/models", nil)
	pub.Header.Set("Authorization", "Bearer "+PublicKeyValue)
	rec2 := httptest.NewRecorder()
	relayAuthMiddleware(ok)(rec2, pub)
	if rec2.Code != http.StatusOK {
		t.Fatalf("public-key relay expected 200, got %d", rec2.Code)
	}

	// Proxy key (structural sk-{random}) -> allowed.
	proxy := httptest.NewRequest(http.MethodGet, "/network/mmx-self/v1/models", nil)
	proxy.Header.Set("Authorization", "Bearer sk-some-proxy-key")
	rec3 := httptest.NewRecorder()
	relayAuthMiddleware(ok)(rec3, proxy)
	if rec3.Code != http.StatusOK {
		t.Fatalf("proxy-key relay expected 200, got %d", rec3.Code)
	}

	// Guest key -> allowed at the middleware layer (relay handler validates it).
	guest := httptest.NewRequest(http.MethodGet, "/network/mmx-self/v1/models", nil)
	guest.Header.Set("Authorization", "Bearer sk-guest-mmx-self-random")
	rec4 := httptest.NewRecorder()
	relayAuthMiddleware(ok)(rec4, guest)
	if rec4.Code != http.StatusOK {
		t.Fatalf("guest-key relay expected 200, got %d", rec4.Code)
	}

	// __punch stays exempt (peer discovery primitive).
	punch := httptest.NewRequest(http.MethodPost, "/network/__punch", strings.NewReader(`{}`))
	rec5 := httptest.NewRecorder()
	relayAuthMiddleware(ok)(rec5, punch)
	if rec5.Code != http.StatusOK {
		t.Fatalf("__punch must remain exempt from relay auth, got %d", rec5.Code)
	}
}

// ③ handleRelayToLocal enforces the whitelist: only /v1/* and
// /api/network/heartbeat/ping may be relayed to self.
func TestRelayToLocal_PathWhitelist(t *testing.T) {
	relaySecurityTestEnv(t)

	// Non-whitelisted admin path must be refused BEFORE dispatch.
	req := httptest.NewRequest(http.MethodPost, "/network/mmx-self/api/forgot-password", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("Authorization", "Bearer "+PublicKeyValue)
	rec := httptest.NewRecorder()
	handleRelayToLocal(rec, req, []string{"mmx-self", "api/forgot-password"}, 0)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("relay to /api/forgot-password expected 403 (whitelist), got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// Whitelisted heartbeat ping dispatches in-process to the mux -> 200.
	ping := httptest.NewRequest(http.MethodGet, "/network/mmx-self/api/network/heartbeat/ping", nil)
	ping.RemoteAddr = "203.0.113.10:1234"
	ping.Header.Set("Authorization", "Bearer "+PublicKeyValue)
	rec2 := httptest.NewRecorder()
	handleRelayToLocal(rec2, ping, []string{"mmx-self", "api/network/heartbeat/ping"}, 0)
	if rec2.Code != http.StatusOK {
		t.Fatalf("relay to heartbeat ping expected 200, got %d (body=%s)", rec2.Code, rec2.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &body); err != nil {
		t.Fatalf("heartbeat ping response not JSON: %v", err)
	}
	if body["node_id"] != "mmx-self" {
		t.Errorf("heartbeat ping node_id = %v, want mmx-self", body["node_id"])
	}
}

// ④ withProxyAuth must NOT grant the C3 anonymous-admin fallback to a
// relay-dispatched request, even when the preserved RemoteAddr is localhost.
func TestWithProxyAuth_RelayDispatched_NoAnonymousAdmin(t *testing.T) {
	relaySecurityTestEnv(t)

	// Ensure no proxy key and no consumers: the C3 fallback would otherwise
	// accept a localhost anonymous request.
	cfg.Set("proxy_api_key", "")
	cfg.Set("seed_secret", "") // ensure fail-closed seed state unrelated here

	called := false
	h := withProxyAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Genuine localhost anonymous request -> anonymous admin (existing C3).
	local := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	h(rec, local)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("genuine localhost anonymous request must still pass C3 (code=%d called=%v)", rec.Code, called)
	}

	// Relay-dispatched request with preserved localhost RemoteAddr -> 401.
	called = false
	relayed := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	relayed.RemoteAddr = "127.0.0.1:1234"
	relayed = relayed.WithContext(context.WithValue(relayed.Context(), ctxKeyRelayDispatch, true))
	rec2 := httptest.NewRecorder()
	h(rec2, relayed)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("relay-dispatched request must NOT get anonymous admin, got %d (called=%v)", rec2.Code, called)
	}
	if called {
		t.Fatalf("relay-dispatched request reached the handler")
	}
}

// localOnly must reject a relay-dispatched request even from localhost.
func TestLocalOnly_RelayDispatched_Rejected(t *testing.T) {
	relaySecurityTestEnv(t)

	called := false
	h := localOnly(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Genuine localhost -> allowed.
	local := httptest.NewRequest(http.MethodGet, "/api/forgot-password", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	h(rec, local)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("genuine localhost localOnly must pass (code=%d called=%v)", rec.Code, called)
	}

	// Relay-dispatched with localhost RemoteAddr -> 403.
	called = false
	relayed := httptest.NewRequest(http.MethodGet, "/api/forgot-password", nil)
	relayed.RemoteAddr = "127.0.0.1:1234"
	relayed = relayed.WithContext(context.WithValue(relayed.Context(), ctxKeyRelayDispatch, true))
	rec2 := httptest.NewRecorder()
	h(rec2, relayed)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("relay-dispatched localOnly must be rejected, got %d (called=%v)", rec2.Code, called)
	}
	if called {
		t.Fatalf("relay-dispatched request reached the localOnly handler")
	}
}

// ⑤ relayToRemote strips X-OMP-KeyType from the outbound forward.
func TestRelayToRemote_StripsKeyTypeHeader(t *testing.T) {
	relaySecurityTestEnv(t)

	var captured http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Allow localhost relay targets for the test server (SSRF guard bypass).
	oldAllow := allowLocalRelayForTest
	allowLocalRelayForTest = true
	t.Cleanup(func() { allowLocalRelayForTest = oldAllow })

	// Trust the test server's self-signed cert for the shared client.
	client := GetSharedHTTPClient()
	prevTransport := client.Transport
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { client.Transport = prevTransport }()

	body := []byte(`{"model":"gpt-4"}`)
	req := httptest.NewRequest(http.MethodPost, "/network/mmx-remote/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+PublicKeyValue)
	req.Header.Set("X-OMP-KeyType", "admin") // forged header must be stripped
	rec := httptest.NewRecorder()

	entry := &RouteEntry{NodeID: "mmx-remote", Addresses: []string{srv.URL}}
	relayToRemote(rec, req, entry, []string{"mmx-remote", "v1/chat/completions"}, 0)

	if rec.Code != http.StatusOK {
		t.Fatalf("relay forward returned %d (body=%s)", rec.Code, rec.Body.String())
	}
	if got := captured.Get("X-OMP-KeyType"); got != "" {
		t.Fatalf("relayToRemote forwarded X-OMP-KeyType=%q; must be stripped", got)
	}
	if got := captured.Get("Authorization"); got != "" {
		t.Fatalf("relayToRemote must strip Authorization, got %q", got)
	}
}

// E2E: the original attack (relay-to-self reaching loopback-trusted endpoints)
// must be closed through the real mux wiring.
func TestRelayToSelf_AttackClosed_ThroughMux(t *testing.T) {
	relaySecurityTestEnv(t)

	mux := relayDispatchHandler

	// Attack 1: anonymous relay to a localOnly endpoint -> 401 (auth gate).
	req := httptest.NewRequest(http.MethodPost, "/network/mmx-self/api/forgot-password", nil)
	req.RemoteAddr = "203.0.113.11:1234"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous relay-to-self must be 401, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// Attack 2: authenticated relay to a non-whitelisted localOnly endpoint
	// -> 403 (path whitelist).
	req2 := httptest.NewRequest(http.MethodPost, "/network/mmx-self/api/forgot-password", nil)
	req2.RemoteAddr = "203.0.113.12:1234"
	req2.Header.Set("Authorization", "Bearer "+PublicKeyValue)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("authenticated relay-to-self to /api/forgot-password must be 403, got %d (body=%s)", rec2.Code, rec2.Body.String())
	}

	// Attack 3: relay-dispatched request must NOT reach the localOnly handler
	// even if the whitelist were bypassed (handled by localOnly marker).
	// (Covered by TestLocalOnly_RelayDispatched_Rejected at unit level.)

	// Legitimate: authenticated relay to heartbeat ping -> 200.
	req3 := httptest.NewRequest(http.MethodGet, "/network/mmx-self/api/network/heartbeat/ping", nil)
	req3.RemoteAddr = "203.0.113.13:1234"
	req3.Header.Set("Authorization", "Bearer "+PublicKeyValue)
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("authenticated relay to heartbeat ping expected 200, got %d (body=%s)", rec3.Code, rec3.Body.String())
	}
}
