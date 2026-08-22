package main

// p3_fixes_test.go — regression tests for batch-4 fixes (v4.5.21):
//   - SEC-B4-1: provider sync/models endpoints enforce ownership. A consumer
//     (X-Request-Owner set) must not read or mutate another consumer's
//     provider; only the owner (or admin, empty owner) passes.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func addOwnedProvider(t *testing.T, id, owner string) {
	t.Helper()
	pm.Add(Provider{
		ID:      id,
		Name:    id,
		BaseURL: "https://example.invalid",
		Enabled: false,
		Owner:   owner,
		APIKey:  "sk-none",
		Models:  []ModelDef{},
	})
}

func consumerRequest(method, path, id, owner string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	if id != "" {
		r.SetPathValue("id", id)
	}
	if owner != "" {
		r.Header.Set("X-Request-Owner", owner)
		r.Header.Set("X-Request-Role", "consumer")
	}
	return r
}

// SEC-B4-1: GET /api/providers/{id}/models must 404 for a consumer who does
// not own the provider.
func TestB4x_GetProviderModels_CrossOwner_Rejected(t *testing.T) {
	setupTestEnv(t)
	addOwnedProvider(t, "p-alice", "consumer-alice")

	w := httptest.NewRecorder()
	handleGetProviderModels(w, consumerRequest("GET", "/api/providers/p-alice/models", "p-alice", "consumer-mallory"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-owner GET models expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// SEC-B4-1: GET /api/providers/{id}/models must succeed for the owner.
func TestB4x_GetProviderModels_Owner_Allowed(t *testing.T) {
	setupTestEnv(t)
	addOwnedProvider(t, "p-alice", "consumer-alice")

	w := httptest.NewRecorder()
	handleGetProviderModels(w, consumerRequest("GET", "/api/providers/p-alice/models", "p-alice", "consumer-alice"))
	if w.Code != http.StatusOK {
		t.Fatalf("owner GET models expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// SEC-B4-1: POST /api/providers/{id}/sync-url must 404 for a non-owner consumer.
func TestB4x_SyncProviderURL_CrossOwner_Rejected(t *testing.T) {
	setupTestEnv(t)
	addOwnedProvider(t, "p-bob", "consumer-bob")

	w := httptest.NewRecorder()
	handleSyncProviderURL(w, consumerRequest("POST", "/api/providers/p-bob/sync-url", "p-bob", "consumer-mallory"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-owner sync-url expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// SEC-B4-1: POST /api/providers/{id}/sync-models must 404 for a non-owner consumer.
func TestB4x_SyncModels_CrossOwner_Rejected(t *testing.T) {
	setupTestEnv(t)
	addOwnedProvider(t, "p-bob", "consumer-bob")

	w := httptest.NewRecorder()
	handleSyncModels(w, consumerRequest("POST", "/api/providers/p-bob/sync-models", "p-bob", "consumer-mallory"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-owner sync-models expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// SEC-B4-1: admin (no owner header) still sees every provider.
func TestB4x_SyncProviderURL_Admin_Allowed(t *testing.T) {
	setupTestEnv(t)
	addOwnedProvider(t, "p-alice", "consumer-alice")

	w := httptest.NewRecorder()
	handleSyncProviderURL(w, consumerRequest("POST", "/api/providers/p-alice/sync-url", "p-alice", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("admin sync-url expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// SEC-B4-1: system presets are readable by any consumer.
// B8-2: "preset" now means an entry in the presetProviders catalog — a bare
// unowned provider is admin property and no longer consumer-readable.
func TestB4x_GetProviderModels_SystemPreset_ConsumerAllowed(t *testing.T) {
	setupTestEnv(t)
	origPresets := presetProviders
	presetProviders = append(presetProviders, Provider{ID: "p-preset", Name: "p-preset"})
	t.Cleanup(func() { presetProviders = origPresets })
	addOwnedProvider(t, "p-preset", "")

	w := httptest.NewRecorder()
	handleGetProviderModels(w, consumerRequest("GET", "/api/providers/p-preset/models", "p-preset", "consumer-alice"))
	if w.Code != http.StatusOK {
		t.Fatalf("consumer GET system preset models expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
}