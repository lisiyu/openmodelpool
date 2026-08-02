package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_SetupStatus_NotInitialized(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/setup/status", nil)
	w := httptest.NewRecorder()
	handleSetupStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["initialized"] != false {
		t.Errorf("expected initialized=false, got %v", resp["initialized"])
	}
}

func TestHandler_SetupStatus_AfterSetup(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/setup/status", nil)
	w := httptest.NewRecorder()
	handleSetupStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["initialized"] != true {
		t.Errorf("expected initialized=true, got %v", resp["initialized"])
	}
}

func TestHandler_Setup_Success(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"username":"admin","password":"Str0ng!Pass#123","email":"admin@test.com"}`
	req := httptest.NewRequest("POST", "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetup(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
}

func TestHandler_Setup_AlreadyInitialized(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}

	body := `{"username":"admin2","password":"Str0ng!Pass#456","email":"admin2@test.com"}`
	req := httptest.NewRequest("POST", "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetup(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_Setup_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/setup", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetup(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_Login_Success(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}

	body := `{"username":"admin","password":"Str0ng!Pass#123"}`
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleLogin(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["access_token"] == nil || resp["access_token"] == "" {
		t.Error("expected access_token in response")
	}
}

func TestHandler_Login_InvalidCredentials(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}

	body := `{"username":"admin","password":"wrongpassword1!"}`
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleLogin(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_Login_MissingFields(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"username":"admin"}`
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleLogin(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_Login_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/login", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleLogin(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_VerifyAuth_NoToken(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/auth/verify", nil)
	w := httptest.NewRecorder()
	handleVerifyAuth(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["valid"] != false {
		t.Errorf("expected valid=false, got %v", resp["valid"])
	}
}

func TestHandler_VerifyAuth_ValidToken(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	accessToken, _ := auth.CreateToken("admin", false)

	req := httptest.NewRequest("GET", "/api/auth/verify", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	w := httptest.NewRecorder()
	handleVerifyAuth(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["valid"] != true {
		t.Errorf("expected valid=true, got %v", resp["valid"])
	}
}

func TestHandler_VerifyAuth_InvalidToken(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/auth/verify", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()
	handleVerifyAuth(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_AdminInfo(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/admin/info", nil)
	w := httptest.NewRecorder()
	handleAdminInfo(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["username"] != "admin" {
		t.Errorf("expected username=admin, got %v", resp["username"])
	}
}

func TestHandler_GetConfig(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	handleGetConfig(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil config response")
	}
}

func TestHandler_GetGateway_Default(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/gateway", nil)
	w := httptest.NewRecorder()
	handleGetGateway(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["is_gateway"] != false {
		t.Errorf("expected is_gateway=false, got %v", resp["is_gateway"])
	}
}

func TestHandler_SetGateway_True(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"is_gateway":true}`
	req := httptest.NewRequest("POST", "/api/gateway", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetGateway(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["is_gateway"] != true {
		t.Errorf("expected is_gateway=true, got %v", resp["is_gateway"])
	}
}

func TestHandler_SetGateway_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/gateway", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetGateway(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_GetRoutingMode_Default(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/routing/mode", nil)
	w := httptest.NewRecorder()
	handleGetRoutingMode(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	current, ok := resp["current"].(map[string]any)
	if !ok {
		t.Fatal("expected current field to be a map")
	}
	if current["id"] != "priority" {
		t.Errorf("expected default mode=priority, got %v", current["id"])
	}
}

func TestHandler_SetRoutingMode_Valid(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"mode":"cheapest"}`
	req := httptest.NewRequest("POST", "/api/routing/mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetRoutingMode(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["mode"] != "cheapest" {
		t.Errorf("expected mode=cheapest, got %v", resp["mode"])
	}
}

func TestHandler_SetRoutingMode_Invalid(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"mode":"nonexistent"}`
	req := httptest.NewRequest("POST", "/api/routing/mode", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetRoutingMode(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_SMTPStatus_NotConfigured(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/smtp/status", nil)
	w := httptest.NewRecorder()
	handleSMTPStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["configured"] != false {
		t.Errorf("expected configured=false, got %v", resp["configured"])
	}
}

func TestHandler_GetSMTPConfig(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/smtp", nil)
	w := httptest.NewRecorder()
	handleGetSMTPConfig(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp SMTPConfig
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestHandler_SaveSMTPConfig(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"host":"smtp.test.com","port":587,"username":"user","password":"pass","from_email":"test@test.com","use_tls":true}`
	req := httptest.NewRequest("POST", "/api/smtp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveSMTPConfig(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
}

func TestHandler_SaveSMTPConfig_DefaultPort(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"host":"smtp.test.com","username":"user","password":"pass","from_email":"test@test.com"}`
	req := httptest.NewRequest("POST", "/api/smtp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveSMTPConfig(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	s := auth.GetSMTP()
	if s.Port != 587 {
		t.Errorf("expected default port 587, got %d", s.Port)
	}
}

func TestHandler_UsageSummary_Empty(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/usage/summary", nil)
	w := httptest.NewRecorder()
	handleUsageSummary(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["today_requests"] != float64(0) {
		t.Errorf("expected today_requests=0, got %v", resp["today_requests"])
	}
}

func TestHandler_UsageProviders_Empty(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/usage/providers", nil)
	w := httptest.NewRecorder()
	handleUsageProviders(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_UsageRecords_Empty(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/usage/records", nil)
	w := httptest.NewRecorder()
	handleUsageRecords(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	recs, ok := resp["records"].([]any)
	if !ok {
		t.Fatal("expected records field to be an array")
	}
	if len(recs) != 0 {
		t.Errorf("expected 0 records, got %d", len(recs))
	}
}

func TestHandler_UsageReset(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/usage/reset", nil)
	w := httptest.NewRecorder()
	handleUsageReset(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
}

func TestHandler_Health(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
}

func TestHandler_Version(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/version", nil)
	w := httptest.NewRecorder()
	handleVersion(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["version"] == nil {
		t.Error("expected version field in response")
	}
	if resp["go_version"] == nil {
		t.Error("expected go_version field in response")
	}
}

func TestHandler_ListModels_Empty(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	handleListModels(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp ModelListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("expected object=list, got %v", resp.Object)
	}
}

func TestHandler_ListProviders_Empty(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	handleListProviders(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	providers, ok := resp["providers"].([]any)
	if !ok {
		t.Fatal("expected providers field to be an array")
	}
	if len(providers) != len(presetProviders) {
		t.Errorf("expected %d preset providers, got %d", len(presetProviders), len(providers))
	}
}

func TestHandler_ListProviders_Lite(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	pm.Add(makeProvider("test-prov", "Test", makeModelDef("gpt-4"), 1, true))

	req := httptest.NewRequest("GET", "/api/providers?lite=true", nil)
	w := httptest.NewRecorder()
	handleListProviders(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	providers, ok := resp["providers"].([]any)
	if !ok {
		t.Fatal("expected providers field to be an array")
	}
	found := false
	for _, pr := range providers {
		p := pr.(map[string]any)
		if p["id"] == "test-prov" {
			found = true
			if _, hasModels := p["models"]; hasModels {
				t.Error("lite mode should not include models")
			}
		}
	}
	if !found {
		t.Error("expected to find test-prov in lite provider list")
	}
}

func TestHandler_GetPresets(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/providers/presets", nil)
	w := httptest.NewRecorder()
	handleGetPresets(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["presets"] == nil {
		t.Error("expected presets field in response")
	}
}

func TestHandler_SiderStatus(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/sider/status", nil)
	w := httptest.NewRecorder()
	handleSiderStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_FederationStatus_Nil(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/federation/status", nil)
	w := httptest.NewRecorder()
	handleFederationStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["enabled"] != false {
		t.Errorf("expected enabled=false when fed is nil, got %v", resp["enabled"])
	}
}

func TestHandler_AlgorithmCurrent_NilChain(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/network/algorithm/current", nil)
	w := httptest.NewRecorder()
	handleAlgorithmCurrent(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["params"] == nil {
		t.Error("expected params field in response")
	}
}

func TestHandler_AlgorithmValidate(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/network/algorithm/validate", nil)
	w := httptest.NewRecorder()
	handleAlgorithmValidate(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["valid"] != true {
		t.Errorf("expected valid=true, got %v", resp["valid"])
	}
}

func TestHandler_AlgorithmGossip_NilChain(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/network/algorithm/gossip", nil)
	w := httptest.NewRecorder()
	handleAlgorithmGossip(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "gossiped" {
		t.Errorf("expected status=gossiped, got %v", resp["status"])
	}
}

func TestHandler_WAFStatus_NilEngine(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/waf/status", nil)
	w := httptest.NewRecorder()
	handleWAFStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["enabled"] != false {
		t.Errorf("expected enabled=false when wafEngine is nil, got %v", resp["enabled"])
	}
}

func TestHandler_WAFBans_NilEngine(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/waf/bans", nil)
	w := httptest.NewRecorder()
	handleWAFBans(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	bans, ok := resp["bans"].([]any)
	if !ok {
		t.Fatal("expected bans field to be an array")
	}
	if len(bans) != 0 {
		t.Errorf("expected 0 bans, got %d", len(bans))
	}
}

func TestHandler_WAFViolations_NilEngine(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/waf/violations", nil)
	w := httptest.NewRecorder()
	handleWAFViolations(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	violations, ok := resp["violations"].([]any)
	if !ok {
		t.Fatal("expected violations field to be an array")
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d", len(violations))
	}
}

func TestHandler_NodeInfo(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/node/info", nil)
	w := httptest.NewRecorder()
	handleNodeInfo(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
}

func TestHandler_NodePubKey(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/node/pubkey", nil)
	w := httptest.NewRecorder()
	handleNodePubKey(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["public_key"]; !ok {
		t.Error("expected public_key field in response")
	}
}

func TestHandler_NetworkRegions_NilManager(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/network/regions", nil)
	w := httptest.NewRecorder()
	handleNetworkRegions(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["regions"] == nil {
		t.Error("expected regions field in response")
	}
}

func TestHandler_GetGenesis(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/genesis", nil)
	w := httptest.NewRecorder()
	handleGetGenesis(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_Status(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()
	handleStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "running" {
		t.Errorf("expected status=running, got %v", resp["status"])
	}
}

func TestHandler_ExportConfig(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/config/export", nil)
	w := httptest.NewRecorder()
	handleExportConfig(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["version"] != "1.0" {
		t.Errorf("expected version=1.0, got %v", resp["version"])
	}
}

func TestHandler_CollaboratorCheckKey_MissingKey(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/collaborator/check-key", nil)
	w := httptest.NewRecorder()
	handleCollaboratorCheckKey(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_CollaboratorRegister_MissingFields(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"username":"user1"}`
	req := httptest.NewRequest("POST", "/api/collaborator/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCollaboratorRegister(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_RefreshToken_MissingToken(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{}`
	req := httptest.NewRequest("POST", "/api/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleRefreshToken(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_RefreshToken_InvalidToken(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"refresh_token":"invalid-token"}`
	req := httptest.NewRequest("POST", "/api/auth/refresh", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleRefreshToken(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_ResetPassword_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/auth/reset-password", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleResetPassword(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_VerifyResetToken_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/auth/verify-reset-token", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleVerifyResetToken(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ForgotPassword_NotInitialized(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"email":"admin@test.com"}`
	req := httptest.NewRequest("POST", "/api/auth/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleForgotPassword(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ChangePassword_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/auth/change-password", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleChangePassword(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_UpdateEmail(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "old@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}

	body := `{"email":"new@test.com"}`
	req := httptest.NewRequest("POST", "/api/admin/email", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleUpdateEmail(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if auth.GetEmail() != "new@test.com" {
		t.Errorf("expected email=new@test.com, got %s", auth.GetEmail())
	}
}

func TestHandler_SaveConfig_GenericKeys(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"node_name":"test-node","region":"us"}`
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveConfig(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if cfg.Get("node_name", "") != "test-node" {
		t.Errorf("expected node_name=test-node, got %s", cfg.Get("node_name", ""))
	}
}

func TestHandler_SaveConfig_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/config", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveConfig(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_GetRoutingWeights(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/routing/weights", nil)
	w := httptest.NewRecorder()
	handleGetRoutingWeights(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_SetRoutingWeights(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"priority":0.5,"cost":0.3,"latency":0.1,"tokens":0.1}`
	req := httptest.NewRequest("POST", "/api/routing/weights", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetRoutingWeights(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
}

func TestHandler_RequestLogs_Empty(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/logs/requests", nil)
	w := httptest.NewRecorder()
	handleRequestLogs(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["count"] != float64(0) {
		t.Errorf("expected count=0, got %v", resp["count"])
	}
}

func TestHandler_FreePoolStatus_Nil(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("GET", "/api/free-pool/status", nil)
	w := httptest.NewRecorder()
	handleFreePoolStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandler_ResetWithCode_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	req := httptest.NewRequest("POST", "/api/auth/reset-with-code", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleResetWithCode(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ResetWithCode_MissingFields(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"code":"abc"}`
	req := httptest.NewRequest("POST", "/api/auth/reset-with-code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleResetWithCode(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ResetWithCode_ShortPassword(t *testing.T) {
	te := setupTestEnv(t)
	_ = te

	body := `{"code":"somecode","new_password":"short"}`
	req := httptest.NewRequest("POST", "/api/auth/reset-with-code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleResetWithCode(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
