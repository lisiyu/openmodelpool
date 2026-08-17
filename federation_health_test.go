package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleFederationHealth reports per-node health fields from the trust
// pool: status, freshness, version, reputation and shared counts.
func TestHandleFederationHealth_ReportsNodeHealth(t *testing.T) {
	setupDiscoveryTestEnv(t)

	fed.AddKnownNode(NodeInfo{
		NodeID:          "mmx-peer-1",
		GitHubUser:      "peer-user",
		Endpoint:        "https://n1.example.com",
		Version:         "4.5.16",
		Reputation:      42,
		LastSeen:        nowRFC3339(),
		SharedProviders: []SharedProvider{{ProviderID: "p1", Models: []string{"m1"}}},
	})
	fed.AddKnownNode(NodeInfo{
		NodeID:          "mmx-peer-2",
		GitHubUser:      "peer-two",
		Endpoint:        "https://n2.example.com",
		SharedProviders: []SharedProvider{{ProviderID: "p2", Models: []string{"m2", "m3"}}},
		SharedModels:    []string{"m4"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/federation/health", nil)
	handleFederationHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		Enabled      bool   `json:"enabled"`
		TotalNodes   int    `json:"total_nodes"`
		ActiveNodes  int    `json:"active_nodes"`
		SelfNodeID   string `json:"self_node_id"`
		SelfVersion  string `json:"self_version"`
		Nodes        []struct {
			NodeID          string `json:"node_id"`
			Status          string `json:"status"`
			Freshness       string `json:"freshness"`
			Version         string `json:"version"`
			Reputation      int    `json:"reputation"`
			SharedProviders int    `json:"shared_providers"`
			SharedModels    int    `json:"shared_models"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if out.TotalNodes != 2 {
		t.Errorf("total_nodes = %d, want 2", out.TotalNodes)
	}
	// AddKnownNode forces active status, so both peers count as active.
	if out.ActiveNodes != 2 {
		t.Errorf("active_nodes = %d, want 2", out.ActiveNodes)
	}

	byID := map[string]map[string]any{}
	for _, n := range out.Nodes {
		byID[n.NodeID] = map[string]any{
			"status": n.Status, "freshness": n.Freshness, "version": n.Version,
			"reputation": n.Reputation, "providers": n.SharedProviders, "models": n.SharedModels,
		}
	}

	p1, ok := byID["mmx-peer-1"]
	if !ok {
		t.Fatal("peer-1 missing")
	}
	if p1["version"] != "4.5.16" {
		t.Errorf("peer-1 version = %v, want 4.5.16", p1["version"])
	}
	if p1["reputation"] != 42 {
		t.Errorf("peer-1 reputation = %v, want 42", p1["reputation"])
	}
	if p1["providers"] != 1 || p1["models"] != 1 {
		t.Errorf("peer-1 shares = %v/%v, want 1 provider 1 model", p1["providers"], p1["models"])
	}
	if p1["freshness"] != "fresh" {
		t.Errorf("peer-1 freshness = %v, want fresh", p1["freshness"])
	}

	p2, ok := byID["mmx-peer-2"]
	if !ok {
		t.Fatal("peer-2 missing")
	}
	if p2["providers"] != 1 || p2["models"] != 3 {
		t.Errorf("peer-2 shares = %v/%v, want 1 provider 3 models", p2["providers"], p2["models"])
	}

	// Self node identity is surfaced at the top level (version + id) even if
	// the node is not (yet) present in the trust pool.
	if out.SelfVersion != AppVersion {
		t.Errorf("self_version = %q, want %q", out.SelfVersion, AppVersion)
	}
	if out.SelfNodeID == "" {
		t.Error("self_node_id must not be empty")
	}
}