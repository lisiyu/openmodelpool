package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// Gateway mark handlers tests (P0: admin.html "Gateway 标记")
// ============================================================

// TestGatewayHandlers_DefaultFalse verifies GET /api/gateway returns
// is_gateway=false when the node has never been marked.
func TestGatewayHandlers_DefaultFalse(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)

	req := httptest.NewRequest("GET", "/api/gateway", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handleGetGateway(w, req)

	if w.Code != 200 {
		t.Fatalf("GET /api/gateway expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp["is_gateway"] != false {
		t.Errorf("expected is_gateway=false by default, got %v", resp["is_gateway"])
	}
}

// TestGatewayHandlers_SetAndPersist verifies POST /api/gateway marks the node,
// reflects immediately in memory, is persisted to data/config.json on disk,
// and is reported by a subsequent GET. It also verifies toggling back to false.
func TestGatewayHandlers_SetAndPersist(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)
	cfgPath := filepath.Join(env.dir, "config.json")

	// --- Set to true ---
	body, _ := json.Marshal(map[string]any{"is_gateway": true})
	req := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetGateway(w, req)

	if w.Code != 200 {
		t.Fatalf("POST /api/gateway expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp["success"] != true {
		t.Error("expected success=true")
	}
	if resp["is_gateway"] != true {
		t.Errorf("expected is_gateway=true in response, got %v", resp["is_gateway"])
	}

	// In-memory read-back via cfg.Get
	if env.cfgInst.Get("is_gateway", "false") != "true" {
		t.Error("expected cfg.Get is_gateway=true in memory")
	}

	// Disk persistence read-back (saveSync flushes to data/config.json)
	saved := make(map[string]any)
	if err := loadWithIntegrity(cfgPath, &saved); err != nil {
		t.Fatalf("loadWithIntegrity failed: %v", err)
	}
	if saved["is_gateway"] != "true" {
		t.Errorf("expected persisted is_gateway=true, got %v", saved["is_gateway"])
	}

	// GET reflects the new value
	req2 := httptest.NewRequest("GET", "/api/gateway", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	handleGetGateway(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("GET after set expected 200, got %d", w2.Code)
	}
	var resp2 map[string]any
	json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2["is_gateway"] != true {
		t.Errorf("expected GET is_gateway=true, got %v", resp2["is_gateway"])
	}

	// --- Toggle back to false ---
	body2, _ := json.Marshal(map[string]any{"is_gateway": false})
	req3 := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body2))
	req3.Header.Set("Authorization", "Bearer "+token)
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	handleSetGateway(w3, req3)
	if w3.Code != 200 {
		t.Fatalf("POST toggle false expected 200, got %d", w3.Code)
	}
	var resp3 map[string]any
	json.Unmarshal(w3.Body.Bytes(), &resp3)
	if resp3["is_gateway"] != false {
		t.Errorf("expected is_gateway=false after toggle, got %v", resp3["is_gateway"])
	}
}

// TestGatewayHandlers_RequiresAuth verifies both endpoints reject
// unauthenticated requests with 401 (auth is enforced by the withAuth
// middleware wired in server.go, so we invoke the handler through it).
func TestGatewayHandlers_RequiresAuth(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")

	// GET without token
	req := httptest.NewRequest("GET", "/api/gateway", nil)
	w := httptest.NewRecorder()
	withAuth(handleGetGateway)(w, req)
	if w.Code != 401 {
		t.Errorf("GET /api/gateway without auth expected 401, got %d", w.Code)
	}

	// POST without token
	body, _ := json.Marshal(map[string]any{"is_gateway": true})
	req2 := httptest.NewRequest("POST", "/api/gateway", bytes.NewReader(body))
	w2 := httptest.NewRecorder()
	withAuth(handleSetGateway)(w2, req2)
	if w2.Code != 401 {
		t.Errorf("POST /api/gateway without auth expected 401, got %d", w2.Code)
	}
}

// TestGatewayHandlers_InvalidBody verifies a malformed JSON body returns 400.
func TestGatewayHandlers_InvalidBody(t *testing.T) {
	env := setupTestEnv(t)
	env.authInst.SetupAdmin("admin", "Test12345!@#$", "admin@test.com")
	token, _ := env.authInst.CreateToken("admin", false)

	req := httptest.NewRequest("POST", "/api/gateway", strings.NewReader("not-json"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetGateway(w, req)
	if w.Code != 400 {
		t.Errorf("POST with invalid body expected 400, got %d", w.Code)
	}
}
