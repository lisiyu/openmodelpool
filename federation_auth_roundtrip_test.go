package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestWithFederationAuth_SharedSecretRoundTrip exercises the full path that was
// 403-ing in production: a cross-node client hitting a withFederationAuth-
// protected endpoint. With a shared federation_secret configured on both sides,
// attachFederationAuth's X-Federation-Secret must satisfy path-3.
func TestWithFederationAuth_SharedSecretRoundTrip(t *testing.T) {
	env := setupTestEnv(t)
	node = newTestNodeIdentity(t, env.dir)
	t.Cleanup(func() { node = nil })

	const secret = "s3cret-shared-for-roundtrip"
	env.cfgInst.Set("federation_secret", secret)
	time.Sleep(200 * time.Millisecond)

	// Receiving side: a protected handler under withFederationAuth.
	handler := withFederationAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("admitted"))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Sending side: same node identity and secret via attachFederationAuth.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/ledger/__manifest", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	attachFederationAuth(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (federation auth must pass with shared secret)", resp.StatusCode)
	}
}

// Sanity: without the secret header (or with a wrong one) the same protected
// handler must still reject with 403 — proving the round-trip test above
// passes because of the secret, not because the handler is open.
func TestWithFederationAuth_NoSecretRejects(t *testing.T) {
	env := setupTestEnv(t)
	node = newTestNodeIdentity(t, env.dir)
	t.Cleanup(func() { node = nil })

	env.cfgInst.Set("federation_secret", "s3cret-shared-for-roundtrip")
	time.Sleep(200 * time.Millisecond)

	handler := withFederationAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("admitted"))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/federation/pool", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// Deliberately NO attachFederationAuth: bare request must be rejected.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for unauthenticated request", resp.StatusCode)
	}
}
