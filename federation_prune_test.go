package main

import (
	"testing"
)

// TestPruneSharedProviders_RemovesUnshared verifies the "取消共享后要剔除"
// rule: providers not in the sender's current sync list are dropped, kept
// ones retain their details, and unknown IDs in the sync list do not invent
// entries.
func TestPruneSharedProviders_RemovesUnshared(t *testing.T) {
	providers := []SharedProvider{
		{ProviderID: "sider", Platform: "sider", Models: []string{"m1"}},
		{ProviderID: "openrouter", Platform: "openrouter", Models: []string{"m2", "m3"}},
		{ProviderID: "stale-one", Platform: "x", Models: []string{"m4"}},
	}

	got := pruneSharedProviders(providers, []string{"sider", "openrouter"})
	if len(got) != 2 {
		t.Fatalf("expected 2 providers kept, got %d", len(got))
	}
	if got[0].ProviderID != "sider" || got[1].ProviderID != "openrouter" {
		t.Fatalf("unexpected survivors: %+v", got)
	}
	// Details of kept providers must be preserved untouched.
	if len(got[1].Models) != 2 || got[1].Models[0] != "m2" {
		t.Fatalf("kept provider details mutated: %+v", got[1])
	}
}

// TestPruneSharedProviders_EmptySyncPrunesAll verifies that a sender sharing
// nothing prunes everything on the receiver.
func TestPruneSharedProviders_EmptySyncPrunesAll(t *testing.T) {
	providers := []SharedProvider{
		{ProviderID: "sider", Models: []string{"m1"}},
	}
	got := pruneSharedProviders(providers, []string{})
	if len(got) != 0 {
		t.Fatalf("expected all pruned, got %d", len(got))
	}
	// Nil (sender without prune support) must not prune: backward compatible.
	got = pruneSharedProviders(providers, nil)
	if len(got) != 1 {
		t.Fatalf("nil sync list must not prune, got %d", len(got))
	}
}

// TestPruneSharedProviders_DoesNotMutateInput verifies the input slice is
// not modified in place (the handler works on a shallow copy of NodeInfo).
func TestPruneSharedProviders_DoesNotMutateInput(t *testing.T) {
	providers := []SharedProvider{
		{ProviderID: "keep", Models: []string{"m1"}},
		{ProviderID: "drop", Models: []string{"m2"}},
	}
	_ = pruneSharedProviders(providers, []string{"keep"})
	if len(providers) != 2 || providers[1].ProviderID != "drop" {
		t.Fatalf("input slice was mutated: %+v", providers)
	}
}
