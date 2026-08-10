package main

// config_import_qa_test.go — regression for the QA-minor ShareToPool merge
// finding in handleImportConfig, plus the QA-blocking save-in-lock deadlock:
//
//   - an import entry that OMITS access_control.share_to_pool preserves the
//     existing value (round-trip safety — the official export omits the field),
//   - an import entry that EXPLICITLY sets access_control.share_to_pool
//     (nested, matching the Provider struct binding) is honored, and
//   - a top-level share_to_pool key is NOT bound by Provider and must not be
//     treated as an explicit value.
//
// The deadlock regression is implicit: without the fix, handleImportConfig
// called pm.save() while holding pm.mu.Lock(), which self-deadlocks on
// save()'s pm.mu.RLock() — this test would hang forever.

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// importConfigRequest builds a multipart POST /api/config/import carrying the
// given raw JSON config payload as the "config" file.
func importConfigRequest(t *testing.T, payload string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("config", "config.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(payload)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/config/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestImportConfig_ShareToPoolMerge(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Seed an existing provider with ShareToPool=true.
	pm.Add(Provider{
		ID:      "p1",
		Name:    "P1",
		BaseURL: "https://provider.example.com",
		APIKey:  "sk-live-key",
		AccessControl: ProviderAccessControl{
			ShareToPool: true,
		},
	})

	// Case 1: import entry WITHOUT share_to_pool (and WITHOUT api_key) →
	// preserve existing true and existing key.
	rec := httptest.NewRecorder()
	handleImportConfig(rec, importConfigRequest(t,
		`{"providers":[{"id":"p1","name":"P1","base_url":"https://provider.example.com"}]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("import without share_to_pool: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if p, _ := pm.GetRaw("p1"); !p.AccessControl.ShareToPool {
		t.Fatalf("absent share_to_pool must preserve existing true, got false")
	}
	if p, _ := pm.GetRaw("p1"); p.APIKey != "sk-live-key" {
		t.Fatalf("absent api_key must preserve existing key, got %q", p.APIKey)
	}

	// Case 2: import entry with nested access_control.share_to_pool=false → honored.
	rec2 := httptest.NewRecorder()
	handleImportConfig(rec2, importConfigRequest(t,
		`{"providers":[{"id":"p1","name":"P1","base_url":"https://provider.example.com","access_control":{"share_to_pool":false}}]}`))
	if rec2.Code != http.StatusOK {
		t.Fatalf("import with share_to_pool=false: status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if p, _ := pm.GetRaw("p1"); p.AccessControl.ShareToPool {
		t.Fatalf("explicit share_to_pool=false must be honored, got true")
	}

	// Case 3: import entry with nested access_control.share_to_pool=true →
	// honored (Provider binds ShareToPool only under access_control; a
	// top-level share_to_pool key is NOT part of the struct contract).
	rec3 := httptest.NewRecorder()
	handleImportConfig(rec3, importConfigRequest(t,
		`{"providers":[{"id":"p1","name":"P1","base_url":"https://provider.example.com","access_control":{"share_to_pool":true}}]}`))
	if rec3.Code != http.StatusOK {
		t.Fatalf("import with share_to_pool=true: status=%d body=%s", rec3.Code, rec3.Body.String())
	}
	if p, _ := pm.GetRaw("p1"); !p.AccessControl.ShareToPool {
		t.Fatalf("explicit access_control.share_to_pool=true must be honored, got false")
	}

	// Case 4: a top-level (unbound) share_to_pool key is NOT explicit — the
	// existing value must be preserved (the struct cannot carry it).
	rec4 := httptest.NewRecorder()
	handleImportConfig(rec4, importConfigRequest(t,
		`{"providers":[{"id":"p1","name":"P1","base_url":"https://provider.example.com","share_to_pool":false}]}`))
	if rec4.Code != http.StatusOK {
		t.Fatalf("import with unbound top-level share_to_pool: status=%d body=%s", rec4.Code, rec4.Body.String())
	}
	if p, _ := pm.GetRaw("p1"); !p.AccessControl.ShareToPool {
		t.Fatalf("top-level share_to_pool is not bound by Provider; existing true must be preserved")
	}

	// Case 5: round-trip shape (no share_to_pool anywhere) keeps the current
	// value regardless of the last explicit state.
	rec5 := httptest.NewRecorder()
	handleImportConfig(rec5, importConfigRequest(t,
		`{"providers":[{"id":"p1","name":"P1","base_url":"https://provider.example.com"}]}`))
	if rec5.Code != http.StatusOK {
		t.Fatalf("round-trip import: status=%d body=%s", rec5.Code, rec5.Body.String())
	}
	if p, _ := pm.GetRaw("p1"); !p.AccessControl.ShareToPool {
		t.Fatalf("round-trip import must preserve current share_to_pool=true")
	}
}
