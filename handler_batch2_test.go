package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHB2_ShareInfo_Default(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/share/info", nil)
	w := httptest.NewRecorder()
	handleShareInfo(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["proxy_api_url"] == nil {
		t.Error("expected proxy_api_url field")
	}
}

func TestHB2_ChangePassword_WrongOld(t *testing.T) {
	setupTestEnv(t)
	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	body := `{"old_password":"wrongpass1!","new_password":"NewPass#4567"}`
	req := httptest.NewRequest("POST", "/api/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleChangePassword(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_ChangePassword_Success(t *testing.T) {
	setupTestEnv(t)
	if err := auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com"); err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	body := `{"old_password":"Str0ng!Pass#123","new_password":"NewStr0ng!Pass#9"}`
	req := httptest.NewRequest("POST", "/api/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleChangePassword(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHB2_UpdateEmail_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/admin/email", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleUpdateEmail(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_CreateProvider_NoID(t *testing.T) {
	setupTestEnv(t)
	body := `{"name":"test","api_key":"sk-123","base_url":"https://api.test.com/v1"}`
	req := httptest.NewRequest("POST", "/api/providers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCreateProvider(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_CreateProvider_Success(t *testing.T) {
	setupTestEnv(t)
	body := `{"id":"testprov","name":"TestProv","api_key":"sk-1234567890","base_url":"https://api.test.com/v1","type":"openai_compatible","models":["gpt-4"]}`
	req := httptest.NewRequest("POST", "/api/providers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCreateProvider(w, req)
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

func TestHB2_CreateProvider_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCreateProvider(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_GetProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/providers/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleGetProvider(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_GetProvider_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(makeProvider("myprov", "MyProv", makeModelDef("gpt-4"), 1, true))
	req := httptest.NewRequest("GET", "/api/providers/myprov", nil)
	req.SetPathValue("id", "myprov")
	w := httptest.NewRecorder()
	handleGetProvider(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_DeleteProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("DELETE", "/api/providers/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleDeleteProvider(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_DeleteProvider_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(makeProvider("delprov", "DelProv", makeModelDef("gpt-4"), 1, true))
	req := httptest.NewRequest("DELETE", "/api/providers/delprov", nil)
	req.SetPathValue("id", "delprov")
	w := httptest.NewRecorder()
	handleDeleteProvider(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_GetProviderModels_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/providers/nonexistent/models", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleGetProviderModels(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_SyncModels_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/sync-models", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleSyncModels(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_GetProviderAccessControl_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/providers/nonexistent/access-control", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleGetProviderAccessControl(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_GetProviderAccessControl_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(makeProvider("acprov", "ACProv", makeModelDef("gpt-4"), 1, true))
	req := httptest.NewRequest("GET", "/api/providers/acprov/access-control", nil)
	req.SetPathValue("id", "acprov")
	w := httptest.NewRecorder()
	handleGetProviderAccessControl(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_UpdateProviderAccessControl_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(makeProvider("acprov2", "ACProv2", makeModelDef("gpt-4"), 1, true))
	body := `{"share_to_pool":true}`
	req := httptest.NewRequest("PUT", "/api/providers/acprov2/access-control", strings.NewReader(body))
	req.SetPathValue("id", "acprov2")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleUpdateProviderAccessControl(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHB2_UpdateProviderAccessControl_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	pm.Add(makeProvider("acprov3", "ACProv3", makeModelDef("gpt-4"), 1, true))
	req := httptest.NewRequest("PUT", "/api/providers/acprov3/access-control", strings.NewReader("bad"))
	req.SetPathValue("id", "acprov3")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleUpdateProviderAccessControl(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_SiderTest_NotConfigured(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/sider/test", nil)
	w := httptest.NewRecorder()
	handleSiderTest(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["valid"] != false {
		t.Errorf("expected valid=false for unconfigured sider, got %v", resp["valid"])
	}
}

func TestHB2_RoutingAdvice_EmptyModel(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/routing/advice/", nil)
	req.SetPathValue("model", "")
	w := httptest.NewRecorder()
	handleRoutingAdvice(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_HealthStatus_WithHealthChecker(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origHC := healthChecker
	healthChecker = &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
	}
	t.Cleanup(func() { healthChecker = origHC })
	req := httptest.NewRequest("GET", "/api/health/status", nil)
	w := httptest.NewRecorder()
	handleHealthStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["providers"] == nil {
		t.Error("expected providers field")
	}
	if resp["node_stats"] == nil {
		t.Error("expected node_stats field")
	}
}

func TestHB2_SyncProviderURL_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/sync-url", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleSyncProviderURL(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_SyncAllURLs(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers/sync-urls", nil)
	w := httptest.NewRecorder()
	handleSyncAllURLs(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["changed"] == nil {
		t.Error("expected changed field")
	}
}

func TestHB2_SMTPTest_NotConfigured(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/smtp/test", nil)
	w := httptest.NewRecorder()
	handleSMTPTest(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_ListAPIKeys_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/providers/nonexistent/keys", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleListAPIKeys(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_AddAPIKey_NotFound(t *testing.T) {
	setupTestEnv(t)
	body := `{"key":"sk-123","alias":"test key"}`
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/keys", strings.NewReader(body))
	req.SetPathValue("id", "nonexistent")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAddAPIKey(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_AddAPIKey_NoKeyValue(t *testing.T) {
	setupTestEnv(t)
	pm.Add(makeProvider("keyprov", "KeyProv", makeModelDef("gpt-4"), 1, true))
	body := `{"alias":"test key"}`
	req := httptest.NewRequest("POST", "/api/providers/keyprov/keys", strings.NewReader(body))
	req.SetPathValue("id", "keyprov")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleAddAPIKey(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_UpdateAPIKey_NotFound(t *testing.T) {
	setupTestEnv(t)
	body := `{"enabled":false}`
	req := httptest.NewRequest("PUT", "/api/providers/nonexistent/keys/k1", strings.NewReader(body))
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("key_id", "k1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleUpdateAPIKey(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_DeleteAPIKey_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("DELETE", "/api/providers/nonexistent/keys/k1", nil)
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("key_id", "k1")
	w := httptest.NewRecorder()
	handleDeleteAPIKey(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_ResetKeyQuota_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/keys/k1/reset-quota", nil)
	req.SetPathValue("id", "nonexistent")
	req.SetPathValue("key_id", "k1")
	w := httptest.NewRecorder()
	handleResetKeyQuota(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_ChatCompletions_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleChatCompletions(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_ChatCompletions_EmptyMessages(t *testing.T) {
	setupTestEnv(t)
	body := `{"model":"gpt-4","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleChatCompletions(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_ChatCompletions_NoProvider(t *testing.T) {
	setupTestEnv(t)
	body := `{"model":"nonexistent-model","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleChatCompletions(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHB2_ListInviteCodes_Admin(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/invite-codes", nil)
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleListInviteCodes(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_ListInviteCodes_NonAdmin(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/invite-codes", nil)
	req.Header.Set("X-Request-Role", "consumer")
	w := httptest.NewRecorder()
	handleListInviteCodes(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHB2_CreateInviteCode_Admin(t *testing.T) {
	setupTestEnv(t)
	body := `{"max_uses":5,"role":"consumer"}`
	req := httptest.NewRequest("POST", "/api/invite-codes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleCreateInviteCode(w, req)
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
	if resp["code"] == nil || resp["code"] == "" {
		t.Error("expected code field in response")
	}
}

func TestHB2_CreateInviteCode_InvalidRole(t *testing.T) {
	setupTestEnv(t)
	body := `{"max_uses":1,"role":"invalid_role"}`
	req := httptest.NewRequest("POST", "/api/invite-codes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleCreateInviteCode(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_DeleteInviteCode_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("DELETE", "/api/invite-codes/nonexistent", nil)
	req.SetPathValue("code", "nonexistent")
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleDeleteInviteCode(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_ListConsumers_Admin(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/consumers", nil)
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleListConsumers(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_ListConsumers_NonAdmin(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/consumers", nil)
	req.Header.Set("X-Request-Role", "consumer")
	w := httptest.NewRecorder()
	handleListConsumers(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHB2_CreateConsumer_MissingFields(t *testing.T) {
	setupTestEnv(t)
	body := `{"name":"user1"}`
	req := httptest.NewRequest("POST", "/api/consumers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleCreateConsumer(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_DeleteConsumer_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("DELETE", "/api/consumers/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleDeleteConsumer(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_ToggleConsumer_NotFound(t *testing.T) {
	setupTestEnv(t)
	body := `{"enabled":false}`
	req := httptest.NewRequest("POST", "/api/consumers/nonexistent/toggle", strings.NewReader(body))
	req.SetPathValue("id", "nonexistent")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleToggleConsumer(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_ConsumerRegister_MissingFields(t *testing.T) {
	setupTestEnv(t)
	body := `{"name":"user1"}`
	req := httptest.NewRequest("POST", "/api/consumer/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleConsumerRegister(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB2_UpdateConsumer_NotFound(t *testing.T) {
	setupTestEnv(t)
	body := `{"disabled":true}`
	req := httptest.NewRequest("PUT", "/api/consumers/nonexistent", strings.NewReader(body))
	req.SetPathValue("id", "nonexistent")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Role", "admin")
	w := httptest.NewRecorder()
	handleUpdateConsumer(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB2_MultiUser_ValidateInviteCode_Invalid(t *testing.T) {
	setupTestEnv(t)
	if multiUser.ValidateInviteCode("nonexistent") {
		t.Error("expected invalid code to return false")
	}
}

func TestHB2_MultiUser_CreateAndValidateInviteCode(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(5, "consumer")
	if code == "" {
		t.Fatal("expected non-empty code")
	}
	if !multiUser.ValidateInviteCode(code) {
		t.Error("expected valid code to return true")
	}
}

func TestHB2_MultiUser_InviteCode_MaxUses(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(1, "consumer")
	if !multiUser.UseInviteCode(code, "consumer-1") {
		t.Error("expected first use to succeed")
	}
	if multiUser.ValidateInviteCode(code) {
		t.Error("expected code to be invalid after max uses reached")
	}
	if multiUser.UseInviteCode(code, "consumer-2") {
		t.Error("expected second use to fail")
	}
}

func TestHB2_MultiUser_ValidateAPIKey_Invalid(t *testing.T) {
	setupTestEnv(t)
	_, ok := multiUser.ValidateAPIKey("sk-invalid-key")
	if ok {
		t.Error("expected invalid key to return false")
	}
}

func TestHB2_MultiUser_DeleteConsumer_NotFound(t *testing.T) {
	setupTestEnv(t)
	if multiUser.DeleteConsumer("nonexistent") {
		t.Error("expected delete of nonexistent consumer to return false")
	}
}

func TestHB2_MultiUser_ToggleConsumer_NotFound(t *testing.T) {
	setupTestEnv(t)
	if multiUser.ToggleConsumer("nonexistent", false) {
		t.Error("expected toggle of nonexistent consumer to return false")
	}
}

func TestHB2_MultiUser_DeleteInviteCode_NotFound(t *testing.T) {
	setupTestEnv(t)
	if multiUser.DeleteInviteCode("nonexistent") {
		t.Error("expected delete of nonexistent code to return false")
	}
}

func TestHB2_MultiUser_ListInviteCodes_Empty(t *testing.T) {
	setupTestEnv(t)
	codes := multiUser.ListInviteCodes()
	if len(codes) != 0 {
		t.Errorf("expected 0 codes, got %d", len(codes))
	}
}

func TestHB2_MultiUser_ListConsumers_Empty(t *testing.T) {
	setupTestEnv(t)
	consumers := multiUser.ListConsumers()
	if len(consumers) != 0 {
		t.Errorf("expected 0 consumers, got %d", len(consumers))
	}
}

func TestHB2_MultiUser_GetConsumerFull_NotFound(t *testing.T) {
	setupTestEnv(t)
	_, ok := multiUser.GetConsumerFull("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestHB2_MultiUser_CreateConsumer_InvalidCode(t *testing.T) {
	setupTestEnv(t)
	_, err := multiUser.CreateConsumer("user1", "badcode")
	if err == nil {
		t.Error("expected error for invalid invite code")
	}
}

func TestHB2_MultiUser_FullFlow(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(1, "consumer")
	consumer, err := multiUser.CreateConsumer("testuser", code)
	if err != nil {
		t.Fatalf("CreateConsumer: %v", err)
	}
	if consumer.Name != "testuser" {
		t.Errorf("expected name=testuser, got %s", consumer.Name)
	}
	if consumer.APIKey == "" {
		t.Error("expected non-empty API key")
	}
	validated, ok := multiUser.ValidateAPIKey(consumer.APIKey)
	if !ok {
		t.Error("expected API key to be valid")
	}
	if validated.ID != consumer.ID {
		t.Errorf("expected ID=%s, got %s", consumer.ID, validated.ID)
	}
}

func TestHB2_BalanceEngine_RecordContribution(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	be.RecordContributionBalance("node-1", 1000)
	be.mu.RLock()
	nb := be.nodeBalance["node-1"]
	be.mu.RUnlock()
	if nb == nil || nb.TotalContributed != 1000 {
		t.Errorf("expected TotalContributed=1000, got %v", nb)
	}
}

func TestHB2_BalanceEngine_RecordConsumption(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	be.RecordConsumptionBalance("node-1", 500)
	be.mu.RLock()
	nb := be.nodeBalance["node-1"]
	be.mu.RUnlock()
	if nb == nil || nb.TotalConsumed != 500 {
		t.Errorf("expected TotalConsumed=500, got %v", nb)
	}
}

func TestHB2_BalanceEngine_CalculateAdjustment_Balanced(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	be.RecordContributionBalance("node-1", 1000)
	be.RecordConsumptionBalance("node-1", 1000)
	be.recalculateBalancesLocked()
	adj := be.CalculateAdjustment("node-1")
	if adj.Type != "balanced" {
		t.Errorf("expected balanced, got %s", adj.Type)
	}
	if adj.RoutingWeightMultiplier != 1.0 {
		t.Errorf("expected multiplier=1.0, got %f", adj.RoutingWeightMultiplier)
	}
}

func TestHB2_BalanceEngine_CalculateAdjustment_OverConsumer(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	be.RecordContributionBalance("node-2", 100)
	be.RecordConsumptionBalance("node-2", 1000)
	be.recalculateBalancesLocked()
	adj := be.CalculateAdjustment("node-2")
	if adj.Type != "reduce_priority" {
		t.Errorf("expected reduce_priority, got %s", adj.Type)
	}
	if adj.PriorityDelta != -1 {
		t.Errorf("expected PriorityDelta=-1, got %d", adj.PriorityDelta)
	}
}

func TestHB2_BalanceEngine_CalculateAdjustment_OverContributor(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	be.RecordContributionBalance("node-3", 10000)
	be.RecordConsumptionBalance("node-3", 100)
	be.recalculateBalancesLocked()
	adj := be.CalculateAdjustment("node-3")
	if adj.Type != "boost_priority" {
		t.Errorf("expected boost_priority, got %s", adj.Type)
	}
	if adj.PriorityDelta != 1 {
		t.Errorf("expected PriorityDelta=1, got %d", adj.PriorityDelta)
	}
}

func TestHB2_BalanceEngine_CalculateAdjustment_UnknownNode(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	adj := be.CalculateAdjustment("unknown")
	if adj.Type != "balanced" {
		t.Errorf("expected balanced, got %s", adj.Type)
	}
}

func TestHB2_BalanceEngine_RunBalanceCycle(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	be.RecordContributionBalance("n1", 5000)
	be.RecordConsumptionBalance("n1", 1000)
	ctx := context.Background()
	be.RunBalanceCycle(ctx)
	if len(be.history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(be.history))
	}
	if len(be.adjustments) != 1 {
		t.Fatalf("expected 1 adjustment, got %d", len(be.adjustments))
	}
}

func TestHB2_BalanceEngine_GetBalanceStatus(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	status := be.GetBalanceStatus()
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status["node_count"] != 0 {
		t.Errorf("expected node_count=0, got %v", status["node_count"])
	}
}

func TestHB2_BalanceEngine_GetAllNodeBalances_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	balances := be.GetAllNodeBalances()
	if len(balances) != 0 {
		t.Errorf("expected 0 balances, got %d", len(balances))
	}
}

func TestHB2_BalanceEngine_GetAllAdjustments_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	adjs := be.GetAllAdjustments()
	if len(adjs) != 0 {
		t.Errorf("expected 0 adjustments, got %d", len(adjs))
	}
}

func TestHB2_BalanceEngine_GetAdjustmentForNode_None(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	adj := be.GetAdjustmentForNode("unknown")
	if adj.Type != "balanced" {
		t.Errorf("expected balanced, got %s", adj.Type)
	}
}

func TestHB2_BalanceEngine_GetRoutingWeightMultiplier_Nil(t *testing.T) {
	var be *BalanceEngine
	if w := be.GetRoutingWeightMultiplier("n1"); w != 1.0 {
		t.Errorf("expected 1.0 for nil engine, got %f", w)
	}
}

func TestHB2_BalanceEngine_GetPriorityDelta_Nil(t *testing.T) {
	var be *BalanceEngine
	if d := be.GetPriorityDelta("n1"); d != 0 {
		t.Errorf("expected 0 for nil engine, got %d", d)
	}
}

func TestHB2_BalanceEngine_DefaultConfig(t *testing.T) {
	cfg := DefaultBalanceConfig()
	if cfg.TargetRatio != 1.0 {
		t.Errorf("expected TargetRatio=1.0, got %f", cfg.TargetRatio)
	}
	if cfg.UnderConsumerThreshold != 0.5 {
		t.Errorf("expected UnderConsumerThreshold=0.5, got %f", cfg.UnderConsumerThreshold)
	}
	if cfg.OverContributorThreshold != 3.0 {
		t.Errorf("expected OverContributorThreshold=3.0, got %f", cfg.OverContributorThreshold)
	}
}

func TestHB2_BalanceEngine_UpdateConfig(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	newCfg := BalanceConfig{
		TargetRatio:              1.5,
		UnderConsumerThreshold:   0.3,
		OverContributorThreshold: 5.0,
		AdjustmentStrength:       0.5,
	}
	be.UpdateConfig(newCfg)
	got := be.GetConfig()
	if got.TargetRatio != 1.5 {
		t.Errorf("expected TargetRatio=1.5, got %f", got.TargetRatio)
	}
}

func TestHB2_BalanceEngine_UpdateConfig_InvalidValues(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	badCfg := BalanceConfig{
		TargetRatio:              -1,
		UnderConsumerThreshold:   -1,
		OverContributorThreshold: -1,
		AdjustmentStrength:       5.0,
	}
	be.UpdateConfig(badCfg)
	got := be.GetConfig()
	if got.TargetRatio != 1.0 {
		t.Errorf("expected default TargetRatio=1.0, got %f", got.TargetRatio)
	}
	if got.UnderConsumerThreshold != 0.5 {
		t.Errorf("expected default UnderConsumerThreshold=0.5, got %f", got.UnderConsumerThreshold)
	}
	if got.OverContributorThreshold != 3.0 {
		t.Errorf("expected default OverContributorThreshold=3.0, got %f", got.OverContributorThreshold)
	}
	if got.AdjustmentStrength != 0.3 {
		t.Errorf("expected default AdjustmentStrength=0.3, got %f", got.AdjustmentStrength)
	}
}

func TestHB2_BalanceEngine_GetBalanceHistory_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	hist := be.GetBalanceHistory(10)
	if len(hist) != 0 {
		t.Errorf("expected 0 history, got %d", len(hist))
	}
}

func TestHB2_BalanceEngine_RecordNilGuard(t *testing.T) {
	var be *BalanceEngine
	be.RecordContributionBalance("n1", 100)
	be.RecordConsumptionBalance("n1", 100)
}

func TestHB2_LoadBalancer_NewDefault(t *testing.T) {
	cfg := DefaultLBConfig()
	lb := NewLoadBalancer(cfg)
	if lb == nil {
		t.Fatal("expected non-nil LoadBalancer")
	}
	if lb.config.TrustWeight != 0.25 {
		t.Errorf("expected TrustWeight=0.25, got %f", lb.config.TrustWeight)
	}
}

func TestHB2_LoadBalancer_ScoreNode_Unknown(t *testing.T) {
	cfg := DefaultLBConfig()
	lb := NewLoadBalancer(cfg)
	score := lb.ScoreNode("unknown-node")
	if score != 50.0 {
		t.Errorf("expected neutral score 50.0 for unknown node, got %f", score)
	}
}

func TestHB2_LoadBalancer_RecordRequest_Success(t *testing.T) {
	cfg := DefaultLBConfig()
	lb := NewLoadBalancer(cfg)
	lb.RecordRequest("node-1", 100*time.Millisecond, true)
	lb.mu.RLock()
	m := lb.nodeMetrics["node-1"]
	lb.mu.RUnlock()
	if m == nil || !m.Healthy {
		t.Error("expected node-1 to be healthy after success")
	}
	if m.RequestCount != 1 || m.SuccessCount != 1 {
		t.Errorf("expected 1/1 request/success, got %d/%d", m.RequestCount, m.SuccessCount)
	}
}

func TestHB2_LoadBalancer_RecordRequest_Failure(t *testing.T) {
	cfg := DefaultLBConfig()
	lb := NewLoadBalancer(cfg)
	lb.RecordRequest("node-2", 0, false)
	lb.mu.RLock()
	m := lb.nodeMetrics["node-2"]
	lb.mu.RUnlock()
	if m == nil {
		t.Fatal("expected metrics for node-2")
	}
	if m.ErrorRate != 1.0 {
		t.Errorf("expected error_rate=1.0, got %f", m.ErrorRate)
	}
}

func TestHB2_LoadBalancer_UpdateNodeMetrics(t *testing.T) {
	cfg := DefaultLBConfig()
	lb := NewLoadBalancer(cfg)
	lb.UpdateNodeMetrics("node-3", 0.5, 0.6, 10, 1000)
	lb.mu.RLock()
	m := lb.nodeMetrics["node-3"]
	lb.mu.RUnlock()
	if m == nil {
		t.Fatal("expected metrics for node-3")
	}
	if m.CPUUsage != 0.5 || m.MemUsage != 0.6 {
		t.Errorf("expected cpu=0.5 mem=0.6, got cpu=%f mem=%f", m.CPUUsage, m.MemUsage)
	}
	if m.ActiveConns != 10 || m.Bandwidth != 1000 {
		t.Errorf("expected conns=10 bw=1000, got conns=%d bw=%d", m.ActiveConns, m.Bandwidth)
	}
}

func TestHB2_LoadBalancer_Stop(t *testing.T) {
	cfg := DefaultLBConfig()
	lb := NewLoadBalancer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lb.StartHealthCheck(ctx)
	lb.Stop()
}

func TestHB2_MaskKey_Short(t *testing.T) {
	if maskKey("abc") != "***" {
		t.Error("expected *** for short key")
	}
}

func TestHB2_MaskKey_Long(t *testing.T) {
	result := maskKey("sk-1234567890abcdef")
	if result[:4] != "sk-1" {
		t.Errorf("expected prefix sk-1, got %s", result[:4])
	}
	if result[len(result)-4:] != "cdef" {
		t.Errorf("expected suffix cdef, got %s", result[len(result)-4:])
	}
}

func TestHB2_Clamp(t *testing.T) {
	if clamp(-1, 0, 1) != 0 {
		t.Error("expected 0 for clamped -1")
	}
	if clamp(2, 0, 1) != 1 {
		t.Error("expected 1 for clamped 2")
	}
	if clamp(0.5, 0, 1) != 0.5 {
		t.Error("expected 0.5 for clamped 0.5")
	}
}

func TestHB2_FilterPlaceholder(t *testing.T) {
	if filterPlaceholder("api.example.com") != "" {
		t.Error("expected empty for placeholder")
	}
	if filterPlaceholder("https://api.example.com") != "" {
		t.Error("expected empty for placeholder URL")
	}
	if filterPlaceholder("") != "" {
		t.Error("expected empty for empty input")
	}
	if filterPlaceholder("real.api.com") != "real.api.com" {
		t.Error("expected original for real input")
	}
}

func TestHB2_BalanceStatus_Nil(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/network/balance/status", nil)
	w := httptest.NewRecorder()
	handleBalanceStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "not_initialized" {
		t.Errorf("expected not_initialized, got %v", resp["status"])
	}
}

func TestHB2_BalanceNodes_Nil(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/network/balance/nodes", nil)
	w := httptest.NewRecorder()
	handleBalanceNodes(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_BalanceAdjustments_Nil(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/network/balance/adjustments", nil)
	w := httptest.NewRecorder()
	handleBalanceAdjustments(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_LBStatus_Nil(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/network/loadbalancer/status", nil)
	w := httptest.NewRecorder()
	handleLBStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["enabled"] != false {
		t.Errorf("expected enabled=false, got %v", resp["enabled"])
	}
}

func TestHB2_LBNodes_Nil(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/network/loadbalancer/nodes", nil)
	w := httptest.NewRecorder()
	handleLBNodes(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB2_LBNodeMetrics_Nil(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/network/loadbalancer/metrics/test", nil)
	req.SetPathValue("node_id", "test")
	w := httptest.NewRecorder()
	handleLBNodeMetrics(w, req)
	if w.Code != 503 {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHB2_hashAPIKey(t *testing.T) {
	h1 := hashAPIKey("test-key")
	h2 := hashAPIKey("test-key")
	if h1 != h2 {
		t.Error("expected same hash for same input")
	}
	h3 := hashAPIKey("different-key")
	if h1 == h3 {
		t.Error("expected different hashes for different inputs")
	}
}
