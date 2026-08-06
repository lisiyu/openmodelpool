package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These are independent QA supplement tests for G4 (Region Manager wiring).
// They cover the two branches the engineer's region_manager_wire_test.go never
// exercises: (1) the withAuth 401 guard on PUT /api/network/regions/config, and
// (2) the true personal-mode / nil-manager safe-degradation paths where
// `regionManager` is nil. Each test resets the global so it stays isolated.

// TestRegionConfigUpdateRequiresAuth verifies that PUT /api/network/regions/config
// is protected by withAuth: a request WITHOUT a token must be rejected with 401
// before the handler body ever runs. The engineer's test calls the handler
// directly and therefore never asserts this gate.
func TestRegionConfigUpdateRequiresAuth(t *testing.T) {
	regionManager = NewRegionManager()
	defer func() { regionManager = NewRegionManager() }()

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/network/regions/config", withAuth(handleNetworkRegionConfigUpdate))

	body, _ := json.Marshal(map[string]any{
		"PreferLocal":          false,
		"CrossRegionThreshold": 5.0,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/network/regions/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated PUT, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestRegionsEndpointsNilManagerSafeDegradation verifies the personal-mode /
// uninitialized path: when regionManager is nil, the GET endpoints degrade to
// safe empty (200) responses instead of panicking or returning 500.
func TestRegionsEndpointsNilManagerSafeDegradation(t *testing.T) {
	regionManager = nil
	defer func() { regionManager = NewRegionManager() }()

	// GET /api/network/regions -> 200, empty regions, default config present.
	req := httptest.NewRequest(http.MethodGet, "/api/network/regions", nil)
	w := httptest.NewRecorder()
	handleNetworkRegions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /regions: expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Regions    []Region       `json:"regions"`
		NodeCounts map[string]int `json:"node_counts"`
		Config     RegionConfig   `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode /regions: %v", err)
	}
	if len(resp.Regions) != 0 {
		t.Errorf("expected empty regions when manager nil, got %v", resp.Regions)
	}
	if len(resp.NodeCounts) != 0 {
		t.Errorf("expected empty node_counts when manager nil, got %v", resp.NodeCounts)
	}
	// Config must still be the default (wired, not nil) so the UI renders.
	if resp.Config.CrossRegionThreshold != 2.0 {
		t.Errorf("expected default config when manager nil, got %+v", resp.Config)
	}

	// GET /api/network/regions/asia/nodes -> 200, empty nodes, no panic.
	reqN := httptest.NewRequest(http.MethodGet, "/api/network/regions/asia/nodes", nil)
	wN := httptest.NewRecorder()
	handleNetworkRegionNodes(wN, reqN)
	if wN.Code != http.StatusOK {
		t.Fatalf("GET /regions/asia/nodes: expected 200, got %d (body=%s)", wN.Code, wN.Body.String())
	}
	var nresp struct {
		Nodes []string `json:"nodes"`
	}
	if err := json.Unmarshal(wN.Body.Bytes(), &nresp); err != nil {
		t.Fatalf("decode /regions/asia/nodes: %v", err)
	}
	if len(nresp.Nodes) != 0 {
		t.Errorf("expected empty nodes when manager nil, got %v", nresp.Nodes)
	}
}

// TestRegionConfigUpdateNilManagerNoPanic documents the degradation of the PUT
// endpoint when regionManager is nil: the handler must return 500 (NOT panic).
// This is the one region endpoint that intentionally does not degrade to 200,
// because it mutates state that requires a live manager.
func TestRegionConfigUpdateNilManagerNoPanic(t *testing.T) {
	regionManager = nil
	defer func() { regionManager = NewRegionManager() }()

	body, _ := json.Marshal(map[string]any{"PreferLocal": false})
	req := httptest.NewRequest(http.MethodPut, "/api/network/regions/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	// Direct call (bypass withAuth) to target the handler's own nil branch.
	handleNetworkRegionConfigUpdate(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when manager nil, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestStartRegionSyncLoopSmoke confirms the G4 goroutine starter returns
// promptly and does not panic. It must not alter the existing manager.
func TestStartRegionSyncLoopSmoke(t *testing.T) {
	regionManager = NewRegionManager()
	defer func() { regionManager = NewRegionManager() }()

	startRegionSyncLoop() // spawns a 30s ticker goroutine; returns immediately.

	if regionManager == nil {
		t.Fatalf("startRegionSyncLoop must not nil the manager")
	}
	// Manager must remain usable after the loop starts.
	regionManager.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	if got := regionManager.GetNodesByRegion(RegionAsiaPacific); len(got) != 1 {
		t.Errorf("expected manager usable after sync loop start, got %v", got)
	}
}
