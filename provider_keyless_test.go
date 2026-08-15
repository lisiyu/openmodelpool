package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTestKeylessConnectivity_AnonymousNoToken verifies the v4.5.2 fix
// (3fb07e4 "test anonymous free-pool providers without a token"): an anonymous
// free-pool provider is probed WITHOUT any Authorization header and, when the
// upstream answers 2xx, is reported reachable.
func TestTestKeylessConnectivity_AnonymousNoToken(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	p := Provider{ID: "free-ovhcloud", APIKey: "free-anonymous", BaseURL: srv.URL, Type: "openai_compatible"}
	res := testKeylessConnectivity(p)

	if success, _ := res["success"].(bool); !success {
		t.Fatalf("expected success, got %v (error=%v)", res["success"], res["error"])
	}
	if gotAuth != "" {
		t.Fatalf("keyless probe must NOT send Authorization header, got %q", gotAuth)
	}
	if gotPath != "/models" {
		t.Fatalf("expected probe path /models, got %q", gotPath)
	}
}

// TestHandleTestAllKeys_KeylessFreePool verifies the v4.5.2 fix
// (ee9938f "allow test-all-keys for keyless free-pool providers"): a free-pool
// provider with no operator key still passes the test-all-keys endpoint and is
// reported as a keyless channel (no "未配置任何 Key" failure), probing without
// an Authorization header.
func TestHandleTestAllKeys_KeylessFreePool(t *testing.T) {
	setupTestEnv(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	// Register a keyless free-pool provider (no APIKeys, free-anonymous key).
	p := Provider{ID: "free-ovhcloud", APIKey: "free-anonymous", BaseURL: srv.URL, Type: "openai_compatible", Owner: ""}
	pm.providers["free-ovhcloud"] = p

	req := httptest.NewRequest("POST", "/api/providers/free-ovhcloud/test-all", nil)
	req.SetPathValue("id", "free-ovhcloud")
	w := httptest.NewRecorder()
	handleTestAllKeys(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if keyless, _ := out["keyless"].(bool); !keyless {
		t.Fatalf("expected keyless:true, got %v", out["keyless"])
	}
	if success, _ := out["success"].(bool); !success {
		t.Fatalf("expected success:true, got %v", out["success"])
	}
	if gotAuth != "" {
		t.Fatalf("keyless test-all-keys must NOT send Authorization, got %q", gotAuth)
	}
}
