package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

// ============================================================
// QA supplementary tests for Gateway mark feature
// (White-box, package main. Complements gateway_test.go)
// ============================================================

// reloadConfigForRestart simulates a process restart: a brand-new Config
// instance loads the persisted data/config.json from disk and is queried.
func reloadConfigForRestart(t *testing.T, path string) *Config {
	t.Helper()
	c := &Config{path: path, data: make(map[string]any)}
	c.load()
	return c
}

// TestQAGateway_SetPersistAndRestart verifies that after marking the node,
// the value is (a) returned by GET, (b) present on disk immediately
// (saveSync), and (c) re-read correctly by a freshly-loaded Config instance
// — i.e. it survives a "restart". Toggling back to false is also verified.
func TestQAGateway_SetPersistAndRestart(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)
	cfgPath := filepath.Join(env.dir, "config.json")

	// --- Mark true ---
	body, _ := json.Marshal(map[string]any{"is_gateway": true})
	req := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetGateway(w, req)
	if w.Code != 200 {
		t.Fatalf("POST /api/gateway (true) expected 200, got %d", w.Code)
	}

	// GET reflects true in memory
	reqG := httptest.NewRequest("GET", "/api/gateway", nil)
	reqG.Header.Set("Authorization", "Bearer "+token)
	wG := httptest.NewRecorder()
	handleGetGateway(wG, reqG)
	var g map[string]any
	json.Unmarshal(wG.Body.Bytes(), &g)
	if g["is_gateway"] != true {
		t.Errorf("GET expected is_gateway=true, got %v", g["is_gateway"])
	}

	// Disk (immediate saveSync) and restart-read both show true
	disk1 := make(map[string]any)
	if err := loadWithIntegrity(cfgPath, &disk1); err != nil {
		t.Fatalf("loadWithIntegrity failed: %v", err)
	}
	if disk1["is_gateway"] != "true" {
		t.Errorf("disk is_gateway expected \"true\", got %v", disk1["is_gateway"])
	}
	restarted := reloadConfigForRestart(t, cfgPath)
	if restarted.Get("is_gateway", "false") != "true" {
		t.Errorf("restarted config expected is_gateway=true, got %q", restarted.Get("is_gateway", "false"))
	}

	// --- Toggle back to false ---
	body2, _ := json.Marshal(map[string]any{"is_gateway": false})
	req2 := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body2))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	handleSetGateway(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("POST /api/gateway (false) expected 200, got %d", w2.Code)
	}
	var r2 map[string]any
	json.Unmarshal(w2.Body.Bytes(), &r2)
	if r2["is_gateway"] != false {
		t.Errorf("response expected is_gateway=false, got %v", r2["is_gateway"])
	}

	// Disk + restart-read both show false
	disk2 := make(map[string]any)
	if err := loadWithIntegrity(cfgPath, &disk2); err != nil {
		t.Fatalf("loadWithIntegrity failed: %v", err)
	}
	if disk2["is_gateway"] != "false" {
		t.Errorf("disk is_gateway expected \"false\", got %v", disk2["is_gateway"])
	}
	restarted2 := reloadConfigForRestart(t, cfgPath)
	if restarted2.Get("is_gateway", "false") != "false" {
		t.Errorf("restarted config expected is_gateway=false, got %q", restarted2.Get("is_gateway", "false"))
	}
}

// TestQAGateway_RouteUnauthenticated401 exercises the REAL route registration
// in server.go (setupRoutes). Unauthenticated GET/POST must be rejected with
// 401 by the withAuth middleware wired around the handler.
func TestQAGateway_RouteUnauthenticated401(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	mux := setupRoutes()

	// GET without token
	req := httptest.NewRequest("GET", "/api/gateway", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("GET /api/gateway (no token) via mux expected 401, got %d", w.Code)
	}

	// POST without token
	body, _ := json.Marshal(map[string]any{"is_gateway": true})
	req2 := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != 401 {
		t.Errorf("POST /api/gateway (no token) via mux expected 401, got %d", w2.Code)
	}
}

// TestQAGateway_RouteAuthenticatedOK verifies the full HTTP route path:
// with a valid token, GET returns 200 + correct flag, and POST both returns
// 200/success AND persists to disk (end-to-end through the registered route).
func TestQAGateway_RouteAuthenticatedOK(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)
	mux := setupRoutes()
	cfgPath := filepath.Join(env.dir, "config.json")

	// GET default (false)
	reqG := httptest.NewRequest("GET", "/api/gateway", nil)
	reqG.Header.Set("Authorization", "Bearer "+token)
	wG := httptest.NewRecorder()
	mux.ServeHTTP(wG, reqG)
	if wG.Code != 200 {
		t.Fatalf("GET /api/gateway (token) expected 200, got %d", wG.Code)
	}
	var g map[string]any
	json.Unmarshal(wG.Body.Bytes(), &g)
	if g["is_gateway"] != false {
		t.Errorf("GET default expected is_gateway=false, got %v", g["is_gateway"])
	}

	// POST set true via real route
	body, _ := json.Marshal(map[string]any{"is_gateway": true})
	reqP := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body))
	reqP.Header.Set("Authorization", "Bearer "+token)
	reqP.Header.Set("Content-Type", "application/json")
	wP := httptest.NewRecorder()
	mux.ServeHTTP(wP, reqP)
	if wP.Code != 200 {
		t.Fatalf("POST /api/gateway (token) expected 200, got %d", wP.Code)
	}
	var p map[string]any
	json.Unmarshal(wP.Body.Bytes(), &p)
	if p["success"] != true || p["is_gateway"] != true {
		t.Errorf("POST response unexpected: %v", p)
	}

	// Disk persistence via the real route path
	saved := make(map[string]any)
	if err := loadWithIntegrity(cfgPath, &saved); err != nil {
		t.Fatalf("loadWithIntegrity failed: %v", err)
	}
	if saved["is_gateway"] != "true" {
		t.Errorf("disk is_gateway expected \"true\", got %v", saved["is_gateway"])
	}
}

// TestQAGateway_InvalidBodies asserts the handler never panics and returns a
// 4xx on genuinely malformed payloads: (1) valid JSON but wrong type for
// is_gateway, (2) empty body. In both cases the persisted state must be
// untouched. Behavior is asserted per the engineer's actual implementation.
func TestQAGateway_InvalidBodies(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)

	cases := []struct {
		name    string
		payload string
	}{
		{"wrong_type_string", `{"is_gateway":"yes"}`},
		{"empty_body", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader([]byte(tc.payload)))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			// Must not panic
			handleSetGateway(w, req)
			if w.Code < 400 {
				t.Errorf("expected 4xx for payload %q, got %d (body=%s)", tc.payload, w.Code, w.Body.String())
			}
			// And must not have changed persisted state
			if env.cfgInst.Get("is_gateway", "false") != "false" {
				t.Errorf("invalid body should not change is_gateway, got %q", env.cfgInst.Get("is_gateway", "false"))
			}
		})
	}
}

// TestQAGateway_MissingFieldDefaultsFalse documents actual behavior: a
// well-formed JSON request that omits the is_gateway field is currently
// accepted (200) and treated as "set false". QA observation: this means a
// partial/empty update silently clobbers an existing true mark — flagged as a
// robustness recommendation for the engineer, not a hard failure.
func TestQAGateway_MissingFieldDefaultsFalse(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)
	cfgPath := filepath.Join(env.dir, "config.json")

	// Pre-mark true, then send a request with no is_gateway field.
	bodyTrue, _ := json.Marshal(map[string]any{"is_gateway": true})
	reqT := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(bodyTrue))
	reqT.Header.Set("Authorization", "Bearer "+token)
	reqT.Header.Set("Content-Type", "application/json")
	wT := httptest.NewRecorder()
	handleSetGateway(wT, reqT)
	if wT.Code != 200 || env.cfgInst.Get("is_gateway", "false") != "true" {
		t.Fatalf("pre-mark failed: code=%d is_gateway=%q", wT.Code, env.cfgInst.Get("is_gateway", "false"))
	}

	// Send {"foo":1} — field omitted.
	req := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader([]byte(`{"foo":1}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetGateway(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 for omitted-field JSON, got %d", w.Code)
	}
	// Actual behavior: clobbered to false.
	if env.cfgInst.Get("is_gateway", "false") != "false" {
		t.Errorf("omitted-field request left is_gateway=%q, expected false (current behavior)", env.cfgInst.Get("is_gateway", "false"))
	}
	disk := make(map[string]any)
	loadWithIntegrity(cfgPath, &disk)
	if disk["is_gateway"] != "false" {
		t.Errorf("disk is_gateway expected \"false\", got %v", disk["is_gateway"])
	}
	t.Logf("OBSERVATION: POST /api/gateway with omitted is_gateway field is accepted and sets is_gateway=false (clobbers existing true). Recommend requiring the field or no-op on omission.")
}

// TestQAGateway_SeedPeersReflectsMark is the integration check: after marking
// the node as gateway, the /api/peers (seed) endpoint's self entry must carry
// is_gateway=true; after unmarking, it must be false.
func TestQAGateway_SeedPeersReflectsMark(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)
	mux := setupRoutes()

	// Wire up minimal network globals required by handleSeedPeers.
	origNetMgr := netMgr
	origRT := routeTable
	origCached := cachedSelfAddresses
	netMgr = &NetworkManager{config: NetworkConfig{NodeID: "qa-gw-node", NodeName: "QA Node"}}
	routeTable = initRouteTable()
	cachedSelfAddresses = nil
	// Avoid external IP detection: force a known public_url.
	env.cfgInst.Set("public_url", "https://self.qa.example.com")
	t.Cleanup(func() {
		netMgr = origNetMgr
		routeTable = origRT
		cachedSelfAddresses = origCached
	})

	callPeers := func() *SeedPeersResponse {
		req := httptest.NewRequest("GET", "/api/peers", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("/api/peers expected 200, got %d", w.Code)
		}
		var resp SeedPeersResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode /api/peers failed: %v", err)
		}
		return &resp
	}

	// Default: self.is_gateway should be false
	if callPeers().Self.IsGateway {
		t.Errorf("before marking, self.is_gateway expected false")
	}

	// Mark true
	body, _ := json.Marshal(map[string]any{"is_gateway": true})
	reqSet := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body))
	reqSet.Header.Set("Authorization", "Bearer "+token)
	reqSet.Header.Set("Content-Type", "application/json")
	wSet := httptest.NewRecorder()
	mux.ServeHTTP(wSet, reqSet)
	if wSet.Code != 200 {
		t.Fatalf("POST /api/gateway (true) expected 200, got %d", wSet.Code)
	}
	if !callPeers().Self.IsGateway {
		t.Errorf("after marking true, self.is_gateway expected true")
	}

	// Unmark false
	body2, _ := json.Marshal(map[string]any{"is_gateway": false})
	reqUnset := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body2))
	reqUnset.Header.Set("Authorization", "Bearer "+token)
	reqUnset.Header.Set("Content-Type", "application/json")
	wUnset := httptest.NewRecorder()
	mux.ServeHTTP(wUnset, reqUnset)
	if wUnset.Code != 200 {
		t.Fatalf("POST /api/gateway (false) expected 200, got %d", wUnset.Code)
	}
	if callPeers().Self.IsGateway {
		t.Errorf("after unmarking, self.is_gateway expected false")
	}
}

// TestQAGateway_ConcurrentToggleConsistency performs many concurrent writes
// (all to the same target value) and verifies that the in-memory config and
// the on-disk file end up consistent with the target — no corruption or
// torn writes. Intended to be run under -race.
func TestQAGateway_ConcurrentToggleConsistency(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)
	cfgPath := filepath.Join(env.dir, "config.json")

	runConcurrent := func(target bool, iterations int) {
		var wg sync.WaitGroup
		for i := 0; i < iterations; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				body, _ := json.Marshal(map[string]any{"is_gateway": target})
				req := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				handleSetGateway(w, req)
			}()
		}
		wg.Wait()
	}

	const N = 30
	// All-true burst
	runConcurrent(true, N)
	if env.cfgInst.Get("is_gateway", "false") != "true" {
		t.Errorf("after concurrent true writes, in-memory is_gateway expected true, got %q", env.cfgInst.Get("is_gateway", "false"))
	}
	disk := make(map[string]any)
	if err := loadWithIntegrity(cfgPath, &disk); err != nil {
		t.Fatalf("loadWithIntegrity failed: %v", err)
	}
	if disk["is_gateway"] != "true" {
		t.Errorf("after concurrent true writes, disk is_gateway expected \"true\", got %v", disk["is_gateway"])
	}

	// All-false burst
	runConcurrent(false, N)
	if env.cfgInst.Get("is_gateway", "false") != "false" {
		t.Errorf("after concurrent false writes, in-memory is_gateway expected false, got %q", env.cfgInst.Get("is_gateway", "false"))
	}
	disk2 := make(map[string]any)
	if err := loadWithIntegrity(cfgPath, &disk2); err != nil {
		t.Fatalf("loadWithIntegrity failed: %v", err)
	}
	if disk2["is_gateway"] != "false" {
		t.Errorf("after concurrent false writes, disk is_gateway expected \"false\", got %v", disk2["is_gateway"])
	}
}
