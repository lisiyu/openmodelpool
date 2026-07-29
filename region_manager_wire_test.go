package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// NOTE: every test below drives the process-global `regionManager` (the same
// instance the production handlers read). It is reset to a fresh
// NewRegionManager() at the start of each test for isolation; network_region_test.go
// never touches this global, so these tests do not interfere with the
// TestDetectRegion*/TestRegionManager* suite.

// TestRegionsHandlerEmptyWhenNoNodes verifies that GET /api/network/regions
// returns an empty (non-error) result when no nodes have registered a region.
// This is the personal-mode / pre-wiring safe-degradation path.
func TestRegionsHandlerEmptyWhenNoNodes(t *testing.T) {
	regionManager = NewRegionManager()

	req := httptest.NewRequest(http.MethodGet, "/api/network/regions", nil)
	w := httptest.NewRecorder()
	handleNetworkRegions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp struct {
		Regions    []Region       `json:"regions"`
		NodeCounts map[string]int `json:"node_counts"`
		Config     RegionConfig   `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Regions) != 0 {
		t.Errorf("expected empty regions list, got %v", resp.Regions)
	}
	if len(resp.NodeCounts) != 0 {
		t.Errorf("expected empty node_counts map, got %v", resp.NodeCounts)
	}
	// Config should still be the default (wired, not nil).
	if resp.Config.CrossRegionThreshold == 0 {
		t.Errorf("expected default config to be present, got %+v", resp.Config)
	}
}

// TestRegionsHandlerReflectsJoinPool verifies G4's JoinPool wiring: when a node
// joins the global pool with a region, that region (and its node count) becomes
// visible through GET /api/network/regions.
func TestRegionsHandlerReflectsJoinPool(t *testing.T) {
	regionManager = NewRegionManager()

	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          filepath.Join(t.TempDir(), "global_pool.json"),
	}
	if err := gp.JoinPool("node-ap-1", "ap", 10000); err != nil {
		t.Fatalf("JoinPool ap: %v", err)
	}
	if err := gp.JoinPool("node-eu-1", "eu", 10000); err != nil {
		t.Fatalf("JoinPool eu: %v", err)
	}
	// A node with no region must NOT appear in any region.
	if err := gp.JoinPool("node-none-1", "", 10000); err != nil {
		t.Fatalf("JoinPool no-region: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/network/regions", nil)
	w := httptest.NewRecorder()
	handleNetworkRegions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp struct {
		Regions    []Region       `json:"regions"`
		NodeCounts map[string]int `json:"node_counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Regions) != 2 {
		t.Errorf("expected 2 distinct regions, got %d: %v", len(resp.Regions), resp.Regions)
	}
	if got := resp.NodeCounts["ap"]; got != 1 {
		t.Errorf("expected node_counts[ap]=1, got %d", got)
	}
	if got := resp.NodeCounts["eu"]; got != 1 {
		t.Errorf("expected node_counts[eu]=1, got %d", got)
	}
	if _, present := resp.NodeCounts[""]; present {
		t.Errorf("region-less node should not create an empty-string region bucket")
	}

	// And the region manager should report the ap node via GetNodesByRegion.
	apNodes := regionManager.GetNodesByRegion(RegionAsiaPacific)
	if len(apNodes) != 1 || apNodes[0] != "node-ap-1" {
		t.Errorf("expected [node-ap-1] in ap, got %v", apNodes)
	}
}

// TestRegionConfigUpdateReflectsInGetConfig verifies PUT /api/network/regions/config
// parses a RegionConfig, applies it via UpdateConfig, and that a subsequent
// regionManager.GetConfig() reflects the new values.
func TestRegionConfigUpdateReflectsInGetConfig(t *testing.T) {
	regionManager = NewRegionManager()

	body := map[string]any{
		"PreferLocal":          false,
		"CrossRegionThreshold": 5.0,
		"RegionWeights": map[string]float64{
			"ap":       1.0,
			"eu":       0.5,
			"americas": 0.3,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/network/regions/config", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleNetworkRegionConfigUpdate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	cfg := regionManager.GetConfig()
	if cfg.PreferLocal {
		t.Errorf("expected PreferLocal=false after update, got %v", cfg.PreferLocal)
	}
	if cfg.CrossRegionThreshold != 5.0 {
		t.Errorf("expected CrossRegionThreshold=5.0, got %v", cfg.CrossRegionThreshold)
	}
	if cfg.RegionWeights[RegionAsiaPacific] != 1.0 {
		t.Errorf("expected RegionWeights[ap]=1.0, got %v", cfg.RegionWeights[RegionAsiaPacific])
	}
	if cfg.RegionWeights[RegionEurope] != 0.5 {
		t.Errorf("expected RegionWeights[eu]=0.5, got %v", cfg.RegionWeights[RegionEurope])
	}
}

// TestGetNodesByRegionReturnsCorrectNodes verifies the RegionManager method
// added for G4 returns the correct node IDs per region (and nothing for an
// empty/absent region).
func TestGetNodesByRegionReturnsCorrectNodes(t *testing.T) {
	regionManager = NewRegionManager()
	regionManager.RegisterNodeSelfReport("n-ap-1", "ap", "", 0, 0)
	regionManager.RegisterNodeSelfReport("n-ap-2", "ap", "", 0, 0)
	regionManager.RegisterNodeSelfReport("n-eu-1", "eu", "", 0, 0)
	regionManager.RegisterNodeSelfReport("n-us-1", "americas", "", 0, 0)

	apNodes := regionManager.GetNodesByRegion(RegionAsiaPacific)
	if len(apNodes) != 2 {
		t.Errorf("expected 2 ap nodes, got %v", apNodes)
	}
	euNodes := regionManager.GetNodesByRegion(RegionEurope)
	if len(euNodes) != 1 || euNodes[0] != "n-eu-1" {
		t.Errorf("expected [n-eu-1] in eu, got %v", euNodes)
	}
	usNodes := regionManager.GetNodesByRegion(RegionAmericas)
	if len(usNodes) != 1 || usNodes[0] != "n-us-1" {
		t.Errorf("expected [n-us-1] in americas, got %v", usNodes)
	}
	// Unknown region: non-nil, empty slice.
	unknownNodes := regionManager.GetNodesByRegion(RegionUnknown)
	if len(unknownNodes) != 0 {
		t.Errorf("expected 0 unknown nodes, got %v", unknownNodes)
	}
}

// TestRegionNodesHandlerAliasMatching verifies GET /api/network/regions/{region}/nodes
// canonicalizes region aliases (e.g. "asia" -> "ap") and returns the matching
// node IDs. The request is routed through a ServeMux so PathValue("region") is
// populated exactly as server.go registers it.
func TestRegionNodesHandlerAliasMatching(t *testing.T) {
	regionManager = NewRegionManager()
	regionManager.RegisterNodeSelfReport("n-ap-1", "ap", "", 0, 0)
	regionManager.RegisterNodeSelfReport("n-ap-2", "ap", "", 0, 0)
	regionManager.RegisterNodeSelfReport("n-eu-1", "eu", "", 0, 0)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/network/regions/{region}/nodes", handleNetworkRegionNodes)

	req := httptest.NewRequest(http.MethodGet, "/api/network/regions/asia/nodes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp struct {
		Region Region   `json:"region"`
		Nodes  []string `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// "asia" alias must resolve to the canonical RegionAsiaPacific ("ap").
	if resp.Region != RegionAsiaPacific {
		t.Errorf("expected region canonicalized to %q, got %q", RegionAsiaPacific, resp.Region)
	}
	if len(resp.Nodes) != 2 {
		t.Errorf("expected 2 nodes for 'asia' alias, got %v", resp.Nodes)
	}
}
