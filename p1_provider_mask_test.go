package main

// Regression tests for review fix 5: PUT /api/providers/{id} (handleUpdateProvider)
// and the create-merge path (handleCreateProvider) must not permanently
// overwrite stored upstream keys with Safe() mask placeholders ("abcd...wxyz"
// for long keys, "***" for short keys) when the request body echoes a masked
// GET response. Masked entries keep the stored real key; only non-masked
// entries update it.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	maskTestKeyLong  = "sk-real-first-key-AAAA1111" // >8 chars → Safe() shows "sk-r...1111"
	maskTestKeyShort = "shortkey"                   // ≤8 chars → Safe() shows "***"
)

// seedMaskTestProvider stores a provider with two real upstream keys: one long
// (masked with "...") and one short (masked as "***").
func seedMaskTestProvider(t *testing.T, id string) {
	t.Helper()
	p := Provider{
		ID:      id,
		Name:    "MaskTest",
		Type:    "openai_compatible",
		BaseURL: "https://api.test.com/v1",
		Enabled: true,
		APIKeys: []APIKeyConfig{
			{ID: "key-1", Key: maskTestKeyLong, Alias: "first", AccessControl: "private", Priority: 1, Enabled: true},
			{ID: "key-2", Key: maskTestKeyShort, Alias: "second", AccessControl: "private", Priority: 2, Enabled: true},
		},
	}
	pm.Add(p)
}

// storedKeys returns the raw (unmasked) keys currently stored for id.
func storedKeys(t *testing.T, id string) []string {
	t.Helper()
	p, ok := pm.GetRaw(id)
	if !ok {
		t.Fatalf("provider %s not found", id)
	}
	keys := make([]string, len(p.APIKeys))
	for i, k := range p.APIKeys {
		keys[i] = k.Key
	}
	return keys
}

// maskedEchoBody returns the JSON body a UI sends when it round-trips the
// Safe()-masked GET representation back through PUT/POST.
func maskedEchoBody(t *testing.T, id string) string {
	t.Helper()
	p, ok := pm.GetRaw(id)
	if !ok {
		t.Fatalf("provider %s not found", id)
	}
	b, err := json.Marshal(p.Safe())
	if err != nil {
		t.Fatalf("marshal safe provider: %v", err)
	}
	return string(b)
}

// A PUT that echoes the masked GET response must leave the stored keys alone.
func TestUpdateProvider_MaskedAPIKeysEchoKeepsRealKeys(t *testing.T) {
	setupTestEnv(t)
	seedMaskTestProvider(t, "mask-p1")

	echo := maskedEchoBody(t, "mask-p1")
	if strings.Contains(echo, maskTestKeyLong) || strings.Contains(echo, maskTestKeyShort) {
		t.Fatalf("test setup broken: masked echo leaks a real key: %s", echo)
	}
	if !strings.Contains(echo, "...") || !strings.Contains(echo, "***") {
		t.Fatalf("test setup broken: echo does not look masked: %s", echo)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/providers/mask-p1", strings.NewReader(echo))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "mask-p1")
	w := httptest.NewRecorder()
	handleUpdateProvider(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	got := storedKeys(t, "mask-p1")
	want := []string{maskTestKeyLong, maskTestKeyShort}
	if len(got) != len(want) {
		t.Fatalf("stored key count changed: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key %d overwritten by mask placeholder: got %q, want %q", i, got[i], want[i])
		}
	}
}

// A genuinely new (non-masked) key in the PUT must be applied, while the
// masked entry keeps its stored real key.
func TestUpdateProvider_NewAPIKeyAppliesMaskedOnePreserved(t *testing.T) {
	setupTestEnv(t)
	seedMaskTestProvider(t, "mask-p2")

	p, _ := pm.GetRaw("mask-p2")
	safe := p.Safe()
	safe.APIKeys[1].Key = "sk-brand-new-key-BBBB2222"
	b, err := json.Marshal(safe)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/providers/mask-p2", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "mask-p2")
	w := httptest.NewRecorder()
	handleUpdateProvider(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	got := storedKeys(t, "mask-p2")
	if len(got) != 2 {
		t.Fatalf("stored key count changed: got %d, want 2", len(got))
	}
	if got[0] != maskTestKeyLong {
		t.Fatalf("masked entry lost its real key: got %q, want %q", got[0], maskTestKeyLong)
	}
	if got[1] != "sk-brand-new-key-BBBB2222" {
		t.Fatalf("new key was not applied: got %q", got[1])
	}
}

// The create-merge path (POST with an existing id) has the same flaw: a
// masked echo must not overwrite the stored keys.
func TestCreateProvider_MergeMaskedAPIKeysKeepsRealKeys(t *testing.T) {
	setupTestEnv(t)
	seedMaskTestProvider(t, "mask-p3")

	echo := maskedEchoBody(t, "mask-p3")

	req := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader(echo))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCreateProvider(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	got := storedKeys(t, "mask-p3")
	want := []string{maskTestKeyLong, maskTestKeyShort}
	if len(got) != len(want) {
		t.Fatalf("stored key count changed: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key %d overwritten by mask placeholder: got %q, want %q", i, got[i], want[i])
		}
	}
}

// Unit coverage for the mask logic: restoreMaskedAPIKeys restores the stored
// key only when the submitted value exactly matches Safe()'s mask of it.
// A genuine new key — even one containing "..." — must be kept.
func TestIsMaskedKeyValue(t *testing.T) {
	const realKey = "sk-real-secret-key-12345"
	masked := maskKeyValue(realKey) // "sk-r...2345"

	// Echoing the exact mask → restored to real key.
	got := restoreMaskedAPIKeys(
		[]APIKeyConfig{{ID: "k1", Key: realKey}},
		[]APIKeyConfig{{ID: "k1", Key: masked}},
	)
	if got[0].Key != realKey {
		t.Errorf("masked echo not restored: got %q, want %q", got[0].Key, realKey)
	}

	// "***" short-key mask → restored.
	const shortKey = "abc123"
	got = restoreMaskedAPIKeys(
		[]APIKeyConfig{{ID: "k2", Key: shortKey}},
		[]APIKeyConfig{{ID: "k2", Key: "***"}},
	)
	if got[0].Key != shortKey {
		t.Errorf("*** mask not restored: got %q, want %q", got[0].Key, shortKey)
	}

	// Genuine new keys must NOT be treated as masks, even with "..." inside.
	newKeys := []string{
		"sk-new-key...with-dots-inside-xyz",
		"sk-real-first-key-AAAA1111",
		"shortkey",
		"your-api-key-here",
		"eyJhbGciOi...session-cookie-value",
	}
	for _, nk := range newKeys {
		got := restoreMaskedAPIKeys(
			[]APIKeyConfig{{ID: "k1", Key: realKey}},
			[]APIKeyConfig{{ID: "k1", Key: nk}},
		)
		if got[0].Key != nk {
			t.Errorf("new key %q was wrongly replaced by mask logic: got %q", nk, got[0].Key)
		}
	}
}
