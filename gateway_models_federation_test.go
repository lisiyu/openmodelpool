package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleGatewayModels_AggregatesTrustPoolProviders verifies the
// federation-wide /v1/models view: models advertised by trust-pool peers
// (via their SharedProviders / SharedModels) are merged with local models,
// self is excluded, and the list is deduplicated.
func TestHandleGatewayModels_AggregatesTrustPoolProviders(t *testing.T) {
	setupDiscoveryTestEnv(t)

	// A peer advertising two providers.
	fed.AddKnownNode(NodeInfo{
		NodeID: "mmx-peer-1",
		Status: "active",
		SharedProviders: []SharedProvider{
			{ProviderID: "peer-llm", Platform: "openai", Models: []string{"peer-gpt4", "peer-claude"}},
		},
		SharedModels: []string{"peer-extra"},
	})
	// A peer with no models.
	fed.AddKnownNode(NodeInfo{NodeID: "mmx-peer-2", Status: "active"})
	// Self must be ignored even if it somehow carries providers.
	fed.AddKnownNode(NodeInfo{
		NodeID: node.NodeID(),
		Status: "active",
		SharedProviders: []SharedProvider{
			{ProviderID: "self-llm", Platform: "openai", Models: []string{"self-model"}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handleGatewayModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out ModelListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := map[string]bool{}
	for _, m := range out.Data {
		got[m.ID] = true
	}

	for _, want := range []string{"peer-gpt4", "peer-claude", "peer-extra"} {
		if !got[want] {
			t.Errorf("missing peer model %q in aggregated list", want)
		}
	}
	if got["self-model"] {
		t.Error("self node's providers must not be aggregated")
	}
}

// TestHandleGatewayModels_NoTrustPoolStillServesLocal verifies the endpoint
// degrades gracefully to local-only when federation has no peers.
func TestHandleGatewayModels_NoTrustPoolStillServesLocal(t *testing.T) {
	setupDiscoveryTestEnv(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handleGatewayModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out ModelListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// At minimum local models are served (no error, non-nil list).
	if out.Object != "list" {
		t.Errorf("object = %q, want list", out.Object)
	}
}

// TestHandleGatewayModels_MeshSourcesAnnotated verifies that each aggregated
// model carries the node source(s) it came from (GitHubUser / local).
func TestHandleGatewayModels_MeshSourcesAnnotated(t *testing.T) {
	setupDiscoveryTestEnv(t)

	fed.AddKnownNode(NodeInfo{
		NodeID:          "mmx-peer-1",
		GitHubUser:      "peer-user",
		Status:          "active",
		SharedProviders: []SharedProvider{{ProviderID: "peer-llm", Models: []string{"peer-gpt4"}}},
	})
	fed.AddKnownNode(NodeInfo{
		NodeID:   "mmx-peer-2",
		Endpoint: "https://n2.example.com",
		Status:   "active",
		SharedModels: []string{"peer-extra"},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handleGatewayModels(rec, req)

	var out ModelListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	sources := map[string][]string{}
	for _, m := range out.Data {
		sources[m.ID] = m.MeshSources
	}

	// peer-gpt4 comes from mmx-peer-1 (GitHubUser label).
	got, ok := sources["peer-gpt4"]
	if !ok {
		t.Fatal("peer-gpt4 missing from aggregated list")
	}
	found := false
	for _, s := range got {
		if s == "peer-user" {
			found = true
		}
	}
	if !found {
		t.Errorf("peer-gpt4 sources = %v, want to include peer-user", got)
	}

	// peer-extra comes from mmx-peer-2 (Endpoint label).
	got2, ok := sources["peer-extra"]
	if !ok {
		t.Fatal("peer-extra missing from aggregated list")
	}
	found2 := false
	for _, s := range got2 {
		if s == "https://n2.example.com" {
			found2 = true
		}
	}
	if !found2 {
		t.Errorf("peer-extra sources = %v, want to include https://n2.example.com", got2)
	}

	// No self node (node.NodeID()) may appear in any source list.
	for id, srcs := range sources {
		for _, s := range srcs {
			if s == node.NodeID() {
				t.Errorf("model %s lists self node_id %q as a source", id, s)
			}
		}
	}
}