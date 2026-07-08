package main

import (
	"testing"
)

func TestProviderManager_AddAndGet(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	p := makeProvider("test-p1", "Test Provider 1", makeModelDef("gpt-4o"), 5, true)
	saved := pm.Add(p)

	if saved.ID != "test-p1" {
		t.Fatalf("expected id test-p1, got %s", saved.ID)
	}
	// API key should be masked in Safe() return
	if saved.APIKey == "test-key-test-p1" {
		t.Fatal("Add should return masked API key")
	}

	// GetRaw should have the full key
	raw, ok := pm.GetRaw("test-p1")
	if !ok {
		t.Fatal("GetRaw returned false")
	}
	if raw.APIKey != "test-key-test-p1" {
		t.Fatalf("GetRaw APIKey mismatch: %s", raw.APIKey)
	}

	// Get should return masked
	masked, ok := pm.Get("test-p1")
	if !ok {
		t.Fatal("Get returned false")
	}
	if masked.APIKey == "test-key-test-p1" {
		t.Fatal("Get should mask API key")
	}
}

func TestProviderManager_Delete(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	p := makeProvider("del-p", "Delete Me", makeModelDef("gpt-4o"), 1, true)
	pm.Add(p)

	if !pm.Delete("del-p") {
		t.Fatal("Delete should return true for existing provider")
	}
	if pm.Delete("del-p") {
		t.Fatal("Delete should return false for non-existent provider")
	}

	_, ok := pm.GetRaw("del-p")
	if ok {
		t.Fatal("provider should be gone after delete")
	}
}

func TestProviderManager_Update(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	p := makeProvider("upd-p", "Original", makeModelDef("gpt-4o"), 5, true)
	first := pm.Add(p)

	// Update name and priority
	p2 := makeProvider("upd-p", "Updated Name", makeModelDef("gpt-4o", "gpt-4o-mini"), 3, true)
	pm.Add(p2)

	got, ok := pm.GetRaw("upd-p")
	if !ok {
		t.Fatal("provider not found after update")
	}
	if got.Name != "Updated Name" {
		t.Fatalf("name not updated: %s", got.Name)
	}
	if got.Priority != 3 {
		t.Fatalf("priority not updated: %d", got.Priority)
	}
	if got.CreatedAt != first.CreatedAt {
		t.Fatal("CreatedAt should be preserved on update")
	}
	// UpdatedAt should be set (string format)
	if got.UpdatedAt == "" {
		t.Fatal("UpdatedAt should be set")
	}
}

func TestProviderManager_FindCandidates(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Add two providers with overlapping models
	pm.Add(makeProvider("p1", "P1", makeModelDef("gpt-4o", "gpt-3.5"), 5, true))
	pm.Add(makeProvider("p2", "P2", makeModelDef("gpt-4o"), 10, true))
	pm.Add(makeProvider("p3", "P3", makeModelDef("gpt-4o"), 1, false)) // disabled

	cands := pm.FindCandidates("gpt-4o")
	// Should find p1 and p2, but not p3 (disabled)
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates for gpt-4o, got %d", len(cands))
	}

	// gpt-3.5 only in p1
	cands = pm.FindCandidates("gpt-3.5")
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate for gpt-3.5, got %d", len(cands))
	}
	if cands[0].Provider.ID != "p1" {
		t.Fatalf("expected p1, got %s", cands[0].Provider.ID)
	}

	// Non-existent model
	cands = pm.FindCandidates("nonexistent")
	if len(cands) != 0 {
		t.Fatalf("expected 0 candidates for nonexistent, got %d", len(cands))
	}
}

func TestProviderManager_EnableDisable(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	p := makeProvider("en-p", "Enable Test", makeModelDef("gpt-4o"), 1, true)
	pm.Add(p)

	cands := pm.FindCandidates("gpt-4o")
	found := false
	for _, c := range cands {
		if c.Provider.ID == "en-p" {
			found = true
		}
	}
	if !found {
		t.Fatal("enabled provider should be a candidate")
	}

	// Disable
	p.Enabled = false
	pm.Add(p)

	cands = pm.FindCandidates("gpt-4o")
	for _, c := range cands {
		if c.Provider.ID == "en-p" {
			t.Fatal("disabled provider should NOT be a candidate")
		}
	}
}

func TestProviderManager_PrioritySort(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(makeProvider("low-pri", "Low", makeModelDef("test-model"), 10, true))
	pm.Add(makeProvider("high-pri", "High", makeModelDef("test-model"), 1, true))
	pm.Add(makeProvider("mid-pri", "Mid", makeModelDef("test-model"), 5, true))

	route, _, ok := pm.ResolveRoute("test-model", "priority")
	if !ok {
		t.Fatal("ResolveRoute should find a candidate")
	}
	if route.ID != "high-pri" {
		t.Fatalf("priority sort: expected high-pri (priority=1), got %s", route.ID)
	}
}

func TestProviderManager_ResolveRoute_ExplicitProvider(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(makeProvider("myprovider", "My Provider", makeModelDef("my-model"), 1, true))

	// Explicit provider/model format
	route, model, ok := pm.ResolveRoute("myprovider/my-model", "priority")
	if !ok {
		t.Fatal("explicit provider route should succeed")
	}
	if route.ID != "myprovider" {
		t.Fatalf("expected myprovider, got %s", route.ID)
	}
	if model != "my-model" {
		t.Fatalf("expected my-model, got %s", model)
	}
}

func TestProviderManager_ResolveRoute_NotFound(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	_, _, ok := pm.ResolveRoute("nonexistent-model", "priority")
	if ok {
		t.Fatal("nonexistent model should not resolve")
	}
}

func TestProviderManager_Enabled(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(makeProvider("e1", "E1", makeModelDef("m1"), 1, true))
	pm.Add(makeProvider("e2", "E2", makeModelDef("m2"), 1, false))
	pm.Add(makeProvider("e3", "E3", makeModelDef("m3"), 1, true))

	enabled := pm.Enabled()
	// Filter to only user-configured (non-preset) enabled ones
	userEnabled := 0
	for _, p := range enabled {
		if p.ID == "e1" || p.ID == "e3" {
			userEnabled++
		}
	}
	if userEnabled != 2 {
		t.Fatalf("expected 2 enabled user providers, got %d", userEnabled)
	}
}

func TestProviderManager_GetVisible(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	p1 := makeProvider("own1", "Owned", makeModelDef("m1"), 1, true)
	p1.Owner = "consumer-abc"
	pm.Add(p1)

	p2 := makeProvider("own2", "Other", makeModelDef("m2"), 1, true)
	p2.Owner = "consumer-xyz"
	pm.Add(p2)

	// Consumer "consumer-abc" should see own1 + presets
	visible := pm.GetVisible("consumer-abc")
	found := false
	for _, v := range visible {
		if v.ID == "own1" {
			found = true
		}
		if v.ID == "own2" {
			t.Fatal("consumer should not see other consumer's provider")
		}
	}
	if !found {
		t.Fatal("consumer should see own provider")
	}

	// Admin (owner="") should see everything
	adminVisible := pm.GetVisible("")
	adminOwn := 0
	for _, v := range adminVisible {
		if v.ID == "own1" || v.ID == "own2" {
			adminOwn++
		}
	}
	if adminOwn != 2 {
		t.Fatalf("admin should see both providers, got %d", adminOwn)
	}
}

func TestProviderManager_DeleteByOwner(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	p1 := makeProvider("delown1", "D1", makeModelDef("m1"), 1, true)
	p1.Owner = "victim"
	pm.Add(p1)

	p2 := makeProvider("delown2", "D2", makeModelDef("m2"), 1, true)
	p2.Owner = "victim"
	pm.Add(p2)

	p3 := makeProvider("delown3", "D3", makeModelDef("m3"), 1, true)
	p3.Owner = "other"
	pm.Add(p3)

	count := pm.DeleteByOwner("victim")
	if count != 2 {
		t.Fatalf("expected 2 deleted, got %d", count)
	}

	_, ok := pm.GetRaw("delown1")
	if ok {
		t.Fatal("victim's provider should be deleted")
	}
	_, ok = pm.GetRaw("delown3")
	if !ok {
		t.Fatal("other's provider should still exist")
	}
}

func TestProviderManager_Safe_MasksAPIKey(t *testing.T) {
	longKey := Provider{APIKey: "sk-1234567890abcdef"}
	safe := longKey.Safe()
	if safe.APIKey != "sk-1...cdef" {
		t.Fatalf("long key mask wrong: %s", safe.APIKey)
	}

	shortKey := Provider{APIKey: "short"}
	safe = shortKey.Safe()
	if safe.APIKey != "***" {
		t.Fatalf("short key mask wrong: %s", safe.APIKey)
	}

	emptyKey := Provider{APIKey: ""}
	safe = emptyKey.Safe()
	if safe.APIKey != "" {
		t.Fatalf("empty key should stay empty: %s", safe.APIKey)
	}
}

func TestProviderManager_Safe_MasksVMessProxy(t *testing.T) {
	p := Provider{Proxy: "vmess://some-uuid@host:port"}
	safe := p.Safe()
	if safe.Proxy != "vmess://***" {
		t.Fatalf("vmess proxy should be masked, got: %s", safe.Proxy)
	}

	// Non-vmess proxy should not be masked
	p2 := Provider{Proxy: "http://proxy.example.com:8080"}
	safe2 := p2.Safe()
	if safe2.Proxy != "http://proxy.example.com:8080" {
		t.Fatalf("http proxy should not be masked, got: %s", safe2.Proxy)
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		val, min, max, want float64
	}{
		{5, 0, 10, 0.5},
		{0, 0, 10, 0},
		{10, 0, 10, 1},
		{5, 5, 5, 0}, // min==max
	}
	for _, tt := range tests {
		got := normalize(tt.val, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("normalize(%v,%v,%v) = %v, want %v", tt.val, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestMinMax(t *testing.T) {
	min, max := minMax([]float64{3, 1, 4, 1, 5, 9})
	if min != 1 || max != 9 {
		t.Fatalf("minMax got (%v, %v), want (1, 9)", min, max)
	}

	min, max = minMax([]float64{})
	if min != 0 || max != 1 {
		t.Fatalf("minMax empty got (%v, %v), want (0, 1)", min, max)
	}

	min, max = minMax([]float64{42})
	if min != 42 || max != 42 {
		t.Fatalf("minMax single got (%v, %v), want (42, 42)", min, max)
	}
}

func TestIsOrgPrefix(t *testing.T) {
	if !isOrgPrefix("deepseek-ai") {
		t.Fatal("deepseek-ai should be org prefix")
	}
	if isOrgPrefix("myprovider") {
		t.Fatal("myprovider should not be org prefix")
	}
}
