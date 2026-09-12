package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Phase 4 eval-benchmark data plane: the public model directory must expose
// exactly the community-visible surface (free pool + federated shares) and
// never leak private provider configs.

func setupDirectoryEnv(t *testing.T) {
	t.Helper()
	setupTestEnv(t)
}

func TestModelDirectory_OmitsPrivateProviders(t *testing.T) {
	setupDirectoryEnv(t)
	pm.Add(Provider{ID: "my-private-provider", Name: "private", BaseURL: "https://secret.example.com", Enabled: true, Models: []ModelDef{{ID: "secret-model"}}})
	pm.Add(Provider{ID: "free-kilo-code", Name: "kilo", BaseURL: "https://api.kilo.ai/api/gateway", Enabled: true, Models: []ModelDef{{ID: "openai/gpt-4o-mini"}, {ID: "openai/gpt-4o"}}})

	snap := collectModelDirectory()
	if len(snap.Models) != 2 {
		t.Fatalf("models = %+v, want only the free-pool models", snap.Models)
	}
	for _, m := range snap.Models {
		if m.ID == "secret-model" {
			t.Error("private provider model leaked into public directory")
		}
		if !m.FreePool {
			t.Errorf("free-pool model %q should be flagged free_pool", m.ID)
		}
	}
	if snap.FreePoolCount != 2 {
		t.Errorf("FreePoolCount = %d, want 2", snap.FreePoolCount)
	}
}

func TestModelDirectory_MeshSharedIncluded(t *testing.T) {
	setupDirectoryEnv(t)
	fedInst := &FederationManager{}
	fedInst.enabled = true
	fedInst.trustPool = TrustPool{
		Nodes: []NodeInfo{
			{NodeID: "peer-1", GitHubUser: "edu-peer", Endpoint: "https://edu.example.com",
				SharedProviders: []SharedProvider{{ProviderID: "edu-shared", Models: []string{"edu/math-tutor"}}},
				SharedModels:    []string{"community/helper"}},
		},
	}
	oldFed := fed
	fed = fedInst
	defer func() { fed = oldFed }()

	snap := collectModelDirectory()
	var ids []string
	for _, m := range snap.Models {
		ids = append(ids, m.ID)
		if m.ID == "edu/math-tutor" {
			has := false
			for _, s := range m.Sources {
				if s == "edu-peer" {
					has = true
				}
			}
			if !has {
				t.Errorf("meshed model sources = %v, want edu-peer", m.Sources)
			}
		}
	}
	if !containsStr(ids, "edu/math-tutor") || !containsStr(ids, "community/helper") {
		t.Errorf("model ids = %v, want meshed models present", ids)
	}
	if len(snap.MeshSources) != 1 || snap.MeshSources[0] != "edu-peer" {
		t.Errorf("MeshSources = %v", snap.MeshSources)
	}
}

func TestModelDirectory_EmptySafe(t *testing.T) {
	setupDirectoryEnv(t)
	oldFed := fed
	fed = nil
	defer func() { fed = oldFed }()

	rec := httptest.NewRecorder()
	handleModelDirectory(rec, httptest.NewRequest(http.MethodGet, "/api/public/model-directory", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var snap ModelDirectorySnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.TotalModels != 0 {
		t.Errorf("TotalModels = %d, want 0 with no providers", snap.TotalModels)
	}
	if snap.SnapshotAt == "" {
		t.Error("snapshot_at missing")
	}
}

func TestModelDirectory_NonFreeProviderOmittedEvenWhenEnabled(t *testing.T) {
	setupDirectoryEnv(t)
	pm.Add(Provider{ID: "provider-custom", Name: "custom", BaseURL: "https://custom.example.com", Enabled: true, Models: []ModelDef{{ID: "custom/model-x"}}})
	snap := collectModelDirectory()
	if len(snap.Models) != 0 {
		t.Fatalf("models = %+v, non-free provider must be omitted", snap.Models)
	}
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

var _ = strings.Contains
