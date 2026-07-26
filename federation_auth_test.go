package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFederationAuth_TrustedSeedGETPoolAllowed verifies P1-1: a GET to
// /federation/pool whose Host matches a configured bootstrap seed node is
// allowed through withFederationAuth without presenting credentials.
func TestFederationAuth_TrustedSeedGETPoolAllowed(t *testing.T) {
	_ = setupTestEnv(t)
	netMgr = &NetworkManager{config: NetworkConfig{BootstrapNodes: []string{"https://openmodelpool.com"}}}
	t.Cleanup(func() { netMgr = nil })
	fed = &FederationManager{enabled: true}
	t.Cleanup(func() { fed = nil })

	handler := withFederationAuth(handleFederationPool)
	req := httptest.NewRequest(http.MethodGet, "/api/federation/pool", nil)
	req.Host = "openmodelpool.com"
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("trusted seed GET /federation/pool: expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestFederationAuth_NonSeedGETPoolForbidden verifies that a GET to
// /federation/pool from a Host NOT in the seed list is still rejected (403),
// preserving the strict SA-12 posture.
func TestFederationAuth_NonSeedGETPoolForbidden(t *testing.T) {
	_ = setupTestEnv(t)
	netMgr = &NetworkManager{config: NetworkConfig{BootstrapNodes: []string{"https://openmodelpool.com"}}}
	t.Cleanup(func() { netMgr = nil })
	fed = &FederationManager{enabled: true}
	t.Cleanup(func() { fed = nil })

	handler := withFederationAuth(handleFederationPool)
	req := httptest.NewRequest(http.MethodGet, "/api/federation/pool", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-seed GET /federation/pool: expected 403, got %d", rec.Code)
	}
}

// TestFederationAuth_NonGETPoolForbidden verifies that only GET is allowed for
// the seed bypass; a POST to /federation/pool (even from a trusted seed Host)
// must still be rejected.
func TestFederationAuth_NonGETPoolForbidden(t *testing.T) {
	_ = setupTestEnv(t)
	netMgr = &NetworkManager{config: NetworkConfig{BootstrapNodes: []string{"https://openmodelpool.com"}}}
	t.Cleanup(func() { netMgr = nil })
	fed = &FederationManager{enabled: true}
	t.Cleanup(func() { fed = nil })

	handler := withFederationAuth(handleFederationPool)
	req := httptest.NewRequest(http.MethodPost, "/api/federation/pool", nil)
	req.Host = "openmodelpool.com"
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("POST /federation/pool from trusted seed: expected 403, got %d", rec.Code)
	}
}

// TestFederationAuth_OtherProtectedPathForbidden verifies that the seed bypass
// does NOT weaken any other federation path (SA-12 preserved).
func TestFederationAuth_OtherProtectedPathForbidden(t *testing.T) {
	_ = setupTestEnv(t)
	netMgr = &NetworkManager{config: NetworkConfig{BootstrapNodes: []string{"https://openmodelpool.com"}}}
	t.Cleanup(func() { netMgr = nil })
	fed = &FederationManager{enabled: true}
	t.Cleanup(func() { fed = nil })

	dummy := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	handler := withFederationAuth(dummy)
	req := httptest.NewRequest(http.MethodGet, "/api/federation/registry", nil)
	req.Host = "openmodelpool.com" // trusted seed, but path is not /federation/pool
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("trusted seed GET on other protected path: expected 403, got %d", rec.Code)
	}
}
