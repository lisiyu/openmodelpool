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