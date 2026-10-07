package main

// Regression tests for deterministic provider ordering.
//
// GetAllRaw/GetAll iterated the providers map directly, so Go's randomized
// map order leaked into: AllModels/AllModelsFiltered model order and
// model->provider attribution on shared model IDs, FindCandidates order, and
// (via stable sort) routing tie-breaks in sortCandidates. Two providers
// serving the same model ID (e.g. sider + deepseek both listing
// deepseek-v4-pro) could flip which one served a request from call to call.

import (
	"fmt"
	"testing"
)

func determinismTestPM() *ProviderManager {
	mk := func(id string, models ...string) Provider {
		defs := make([]ModelDef, 0, len(models))
		for _, m := range models {
			defs = append(defs, ModelDef{ID: m, Name: m, Enabled: true})
		}
		return Provider{
			ID: id, Name: id, Type: "openai_compatible",
			BaseURL: "https://example.com/v1",
			APIKey:  "test-key", Enabled: true,
			Priority: 5, Models: defs,
		}
	}
	return &ProviderManager{
		providers: map[string]Provider{
			"sider":    mk("sider", "shared-model", "sider-only"),
			"deepseek": mk("deepseek", "shared-model", "deepseek-only"),
			"aaa":      mk("aaa", "aaa-only"),
		},
	}
}

func idSeq(ids []string) string { return fmt.Sprintf("%q", ids) }

// FindCandidates order must be identical across calls: routing tie-breaks
// (stable sort in sortCandidates) inherit this order.
func TestFindCandidatesDeterministicOrder(t *testing.T) {
	pm := determinismTestPM()
	var first string
	for i := 0; i < 100; i++ {
		cands := pm.FindCandidates("shared-model")
		if len(cands) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(cands))
		}
		order := []string{cands[0].Provider.ID, cands[1].Provider.ID}
		if first == "" {
			first = idSeq(order)
			continue
		}
		if idSeq(order) != first {
			t.Fatalf("FindCandidates order not deterministic: first=%s now=%s (iter %d)",
				first, idSeq(order), i)
		}
	}
}

// AllModelsFiltered must list models in the same order on every call.
func TestAllModelsFilteredDeterministicOrder(t *testing.T) {
	pm := determinismTestPM()
	var first string
	for i := 0; i < 100; i++ {
		models := pm.AllModelsFiltered("admin")
		var ids []string
		for _, m := range models {
			ids = append(ids, m.ID)
		}
		if first == "" {
			first = idSeq(ids)
			continue
		}
		if idSeq(ids) != first {
			t.Fatalf("AllModelsFiltered order not deterministic: first=%s now=%s (iter %d)",
				first, idSeq(ids), i)
		}
	}
}

// GetAllRaw must return configured providers in deterministic (ID-sorted)
// order, with presets appended after.
func TestGetAllRawDeterministicOrder(t *testing.T) {
	pm := determinismTestPM()
	var first string
	for i := 0; i < 100; i++ {
		all := pm.GetAllRaw()
		var ids []string
		for _, p := range all {
			if p.ID == "sider" || p.ID == "deepseek" || p.ID == "aaa" {
				ids = append(ids, p.ID)
			}
		}
		if first == "" {
			first = idSeq(ids)
			if first != `["aaa" "deepseek" "sider"]` {
				t.Fatalf("expected ID-sorted configured providers, got %s", first)
			}
			continue
		}
		if idSeq(ids) != first {
			t.Fatalf("GetAllRaw order not deterministic: first=%s now=%s (iter %d)",
				first, idSeq(ids), i)
		}
	}
}
