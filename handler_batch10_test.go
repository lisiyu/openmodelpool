package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================
// admin.go tests
// ============================================================

func TestHB10_MaskKey_Short(t *testing.T) {
	if got := maskKey("abc"); got != "***" {
		t.Errorf("maskKey short = %q, want ***", got)
	}
}

func TestHB10_MaskKey_Long(t *testing.T) {
	if got := maskKey("sk-1234567890abcdef"); got != "sk-1***cdef" {
		t.Errorf("maskKey long = %q, want sk-1***cdef", got)
	}
}

func TestHB10_MaskKey_Exactly8(t *testing.T) {
	if got := maskKey("12345678"); got != "***" {
		t.Errorf("maskKey 8 chars = %q, want ***", got)
	}
}

func TestHB10_MaskKey_9Chars(t *testing.T) {
	if got := maskKey("123456789"); got != "1234***6789" {
		t.Errorf("maskKey 9 chars = %q", got)
	}
}

func TestHB10_MapKeys(t *testing.T) {
	m := map[string]string{"a": "1", "b": "2", "c": "3"}
	keys := mapKeys(m)
	if len(keys) != 3 {
		t.Errorf("mapKeys len = %d, want 3", len(keys))
	}
}

func TestHB10_Clamp_BelowMin(t *testing.T) {
	if got := clamp(-1, 0, 1); got != 0 {
		t.Errorf("clamp = %f, want 0", got)
	}
}

func TestHB10_Clamp_AboveMax(t *testing.T) {
	if got := clamp(2, 0, 1); got != 1 {
		t.Errorf("clamp = %f, want 1", got)
	}
}

func TestHB10_Clamp_InRange(t *testing.T) {
	if got := clamp(0.5, 0, 1); got != 0.5 {
		t.Errorf("clamp = %f, want 0.5", got)
	}
}

func TestHB10_HandleSetupStatus_NotInitialized(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/setup/status", nil)
	handleSetupStatus(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleAdminInfo(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/admin/info", nil)
	handleAdminInfo(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleGetGateway_Default(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/gateway", nil)
	handleGetGateway(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["is_gateway"] != false {
		t.Errorf("is_gateway = %v, want false", body["is_gateway"])
	}
}

func TestHB10_HandleSetGateway_True(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/gateway", strings.NewReader(`{"is_gateway":true}`))
	r.Header.Set("Content-Type", "application/json")
	handleSetGateway(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleSetGateway_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/gateway", strings.NewReader(`invalid`))
	r.Header.Set("Content-Type", "application/json")
	handleSetGateway(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleListProviders_Empty(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/providers", nil)
	handleListProviders(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleListProviders_Lite(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "test", Name: "Test", Enabled: true})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/providers?lite=true", nil)
	handleListProviders(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleGetPresets(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/presets", nil)
	handleGetPresets(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleCreateProvider_NoID(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/providers", strings.NewReader(`{"name":"test"}`))
	r.Header.Set("Content-Type", "application/json")
	handleCreateProvider(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleCreateProvider_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/providers", strings.NewReader(`invalid`))
	r.Header.Set("Content-Type", "application/json")
	handleCreateProvider(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleGetProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/providers/missing", nil)
	r.SetPathValue("id", "missing")
	handleGetProvider(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleDeleteProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/providers/missing", nil)
	r.SetPathValue("id", "missing")
	handleDeleteProvider(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleGetProviderModels_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/providers/missing/models", nil)
	r.SetPathValue("id", "missing")
	handleGetProviderModels(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleSyncModels_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/providers/missing/sync-models", nil)
	r.SetPathValue("id", "missing")
	handleSyncModels(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleGetProviderAccessControl_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/providers/missing/access-control", nil)
	r.SetPathValue("id", "missing")
	handleGetProviderAccessControl(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleUpdateProviderAccessControl_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/api/providers/missing/access-control", strings.NewReader(`{}`))
	r.SetPathValue("id", "missing")
	handleUpdateProviderAccessControl(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleUpdateProviderAccessControl_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "test", Name: "Test", Enabled: true, APIKey: "sk-test"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/api/providers/test/access-control", strings.NewReader(`{"share_to_pool":true}`))
	r.SetPathValue("id", "test")
	handleUpdateProviderAccessControl(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleSiderStatus(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/sider/status", nil)
	handleSiderStatus(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleUsageSummary_Empty(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/usage/summary", nil)
	handleUsageSummary(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleUsageProviders_Empty(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/usage/providers", nil)
	handleUsageProviders(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleUsageRecords_Empty(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/usage/records", nil)
	handleUsageRecords(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleUsageReset(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/usage/reset", nil)
	handleUsageReset(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleGetRoutingMode_Default(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/routing/mode", nil)
	handleGetRoutingMode(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleSetRoutingMode_Valid(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/routing/mode", strings.NewReader(`{"mode":"cheapest"}`))
	r.Header.Set("Content-Type", "application/json")
	handleSetRoutingMode(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleSetRoutingMode_Invalid(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/routing/mode", strings.NewReader(`{"mode":"invalid"}`))
	r.Header.Set("Content-Type", "application/json")
	handleSetRoutingMode(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleGetRoutingWeights(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/routing/weights", nil)
	handleGetRoutingWeights(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleSetRoutingWeights(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/routing/weights", strings.NewReader(`{"priority":0.5,"cost":0.2,"latency":0.2,"tokens":0.1}`))
	r.Header.Set("Content-Type", "application/json")
	handleSetRoutingWeights(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleSMTPStatus(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/smtp/status", nil)
	handleSMTPStatus(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleGetSMTPConfig(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/smtp/config", nil)
	handleGetSMTPConfig(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleSaveSMTPConfig(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/smtp/config", strings.NewReader(`{"host":"smtp.example.com","port":587,"username":"user","from_email":"test@example.com"}`))
	r.Header.Set("Content-Type", "application/json")
	handleSaveSMTPConfig(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleSaveSMTPConfig_DefaultPort(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/smtp/config", strings.NewReader(`{"host":"smtp.example.com","username":"user","from_email":"test@example.com"}`))
	r.Header.Set("Content-Type", "application/json")
	handleSaveSMTPConfig(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleRequestLogs_Empty(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/request-logs", nil)
	handleRequestLogs(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleStatus(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/status", nil)
	handleStatus(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "running" {
		t.Errorf("status = %v, want running", body["status"])
	}
}

func TestHB10_HandleSyncProviderURL_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/providers/missing/sync-url", nil)
	r.SetPathValue("id", "missing")
	handleSyncProviderURL(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleSyncAllURLs(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/providers/sync-all-urls", nil)
	handleSyncAllURLs(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleListAPIKeys_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/providers/missing/keys", nil)
	r.SetPathValue("id", "missing")
	handleListAPIKeys(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleAddAPIKey_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/providers/missing/keys", strings.NewReader(`{"key":"sk-test"}`))
	r.SetPathValue("id", "missing")
	handleAddAPIKey(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleUpdateAPIKey_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/api/providers/missing/keys/k1", strings.NewReader(`{}`))
	r.SetPathValue("id", "missing")
	r.SetPathValue("key_id", "k1")
	handleUpdateAPIKey(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleDeleteAPIKey_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/providers/missing/keys/k1", nil)
	r.SetPathValue("id", "missing")
	r.SetPathValue("key_id", "k1")
	handleDeleteAPIKey(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleResetKeyQuota_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/providers/missing/keys/k1/reset-quota", nil)
	r.SetPathValue("id", "missing")
	r.SetPathValue("key_id", "k1")
	handleResetKeyQuota(w, r)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHB10_HandleRefreshToken_EmptyBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/refresh", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	handleRefreshToken(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleRefreshToken_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/refresh", strings.NewReader(`invalid`))
	r.Header.Set("Content-Type", "application/json")
	handleRefreshToken(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleCollaboratorCheckKey_MissingKey(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/collaborator/check-key", nil)
	handleCollaboratorCheckKey(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleCollaboratorRegister_MissingFields(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/collaborator/register", strings.NewReader(`{"username":"test"}`))
	r.Header.Set("Content-Type", "application/json")
	handleCollaboratorRegister(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleCollaboratorRegister_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/collaborator/register", strings.NewReader(`invalid`))
	r.Header.Set("Content-Type", "application/json")
	handleCollaboratorRegister(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleResetWithCode_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/reset-with-code", strings.NewReader(`invalid`))
	r.Header.Set("Content-Type", "application/json")
	handleResetWithCode(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleResetWithCode_MissingFields(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/reset-with-code", strings.NewReader(`{"code":"abc"}`))
	r.Header.Set("Content-Type", "application/json")
	handleResetWithCode(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleResetWithCode_ShortPassword(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/reset-with-code", strings.NewReader(`{"code":"abc","new_password":"short"}`))
	r.Header.Set("Content-Type", "application/json")
	handleResetWithCode(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleExportConfig(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/config/export", nil)
	handleExportConfig(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleShareInfo(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/share/info", nil)
	handleShareInfo(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleHealthStatus(t *testing.T) {
	setupTestEnv(t)
	orig := healthChecker
	healthChecker = &HealthChecker{}
	defer func() { healthChecker = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/health", nil)
	handleHealthStatus(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ============================================================
// multiuser.go tests
// ============================================================

func TestHB10_HashAPIKey(t *testing.T) {
	h := hashAPIKey("test-key")
	if len(h) != 64 {
		t.Errorf("hashAPIKey length = %d, want 64", len(h))
	}
}

func TestHB10_HashAPIKey_Deterministic(t *testing.T) {
	h1 := hashAPIKey("test-key")
	h2 := hashAPIKey("test-key")
	if h1 != h2 {
		t.Errorf("hashAPIKey not deterministic")
	}
}

func TestHB10_MultiUser_CreateInviteCode(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	if code == "" {
		t.Error("CreateInviteCode returned empty code")
	}
}

func TestHB10_MultiUser_ValidateInviteCode_Invalid(t *testing.T) {
	setupTestEnv(t)
	if multiUser.ValidateInviteCode("nonexistent") {
		t.Error("ValidateInviteCode should return false for nonexistent code")
	}
}

func TestHB10_MultiUser_ValidateInviteCode_Valid(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	if !multiUser.ValidateInviteCode(code) {
		t.Error("ValidateInviteCode should return true for valid code")
	}
}

func TestHB10_MultiUser_CreateConsumer(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, err := multiUser.CreateConsumer("test", code)
	if err != nil {
		t.Fatalf("CreateConsumer failed: %v", err)
	}
	if consumer.ID == "" {
		t.Error("consumer ID is empty")
	}
	if consumer.APIKey == "" {
		t.Error("consumer APIKey is empty")
	}
}

func TestHB10_MultiUser_ValidateAPIKey(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, _ := multiUser.CreateConsumer("test", code)
	_, ok := multiUser.ValidateAPIKey(consumer.APIKey)
	if !ok {
		t.Error("ValidateAPIKey should return true for valid key")
	}
}

func TestHB10_MultiUser_ValidateAPIKey_Invalid(t *testing.T) {
	setupTestEnv(t)
	_, ok := multiUser.ValidateAPIKey("sk-invalid-key")
	if ok {
		t.Error("ValidateAPIKey should return false for invalid key")
	}
}

func TestHB10_MultiUser_ListConsumers(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	multiUser.CreateConsumer("test", code)
	list := multiUser.ListConsumers()
	if len(list) != 1 {
		t.Errorf("ListConsumers len = %d, want 1", len(list))
	}
}

func TestHB10_MultiUser_GetConsumerFull(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, _ := multiUser.CreateConsumer("test", code)
	full, ok := multiUser.GetConsumerFull(consumer.ID)
	if !ok {
		t.Error("GetConsumerFull should return true")
	}
	if full.ID != consumer.ID {
		t.Error("GetConsumerFull ID mismatch")
	}
}

func TestHB10_MultiUser_GetConsumerFull_NotFound(t *testing.T) {
	setupTestEnv(t)
	_, ok := multiUser.GetConsumerFull("nonexistent")
	if ok {
		t.Error("GetConsumerFull should return false for nonexistent")
	}
}

func TestHB10_MultiUser_ToggleConsumer(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, _ := multiUser.CreateConsumer("test", code)
	if !multiUser.ToggleConsumer(consumer.ID, false) {
		t.Error("ToggleConsumer should return true")
	}
}

func TestHB10_MultiUser_ToggleConsumer_NotFound(t *testing.T) {
	setupTestEnv(t)
	if multiUser.ToggleConsumer("nonexistent", true) {
		t.Error("ToggleConsumer should return false for nonexistent")
	}
}

func TestHB10_MultiUser_DeleteConsumer(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, _ := multiUser.CreateConsumer("test", code)
	if !multiUser.DeleteConsumer(consumer.ID) {
		t.Error("DeleteConsumer should return true")
	}
}

func TestHB10_MultiUser_DeleteConsumer_NotFound(t *testing.T) {
	setupTestEnv(t)
	if multiUser.DeleteConsumer("nonexistent") {
		t.Error("DeleteConsumer should return false for nonexistent")
	}
}

func TestHB10_MultiUser_DeleteInviteCode(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	if !multiUser.DeleteInviteCode(code) {
		t.Error("DeleteInviteCode should return true")
	}
}

func TestHB10_MultiUser_DeleteInviteCode_NotFound(t *testing.T) {
	setupTestEnv(t)
	if multiUser.DeleteInviteCode("nonexistent") {
		t.Error("DeleteInviteCode should return false for nonexistent")
	}
}

func TestHB10_MultiUser_RecordConsumerUsage(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, _ := multiUser.CreateConsumer("test", code)
	multiUser.RecordConsumerUsage(consumer.ID, 100)
}

func TestHB10_MultiUser_UseInviteCode(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(1, "consumer")
	if !multiUser.UseInviteCode(code, "consumer-1") {
		t.Error("UseInviteCode should return true for valid code")
	}
}

func TestHB10_MultiUser_UseInviteCode_Exhausted(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(1, "consumer")
	multiUser.UseInviteCode(code, "consumer-1")
	if multiUser.UseInviteCode(code, "consumer-2") {
		t.Error("UseInviteCode should return false for exhausted code")
	}
}

func TestHB10_MultiUser_FlushSaves(t *testing.T) {
	setupTestEnv(t)
	multiUser.FlushSaves()
}

func TestHB10_MultiUser_ListInviteCodes(t *testing.T) {
	setupTestEnv(t)
	multiUser.CreateInviteCode(0, "consumer")
	codes := multiUser.ListInviteCodes()
	if len(codes) != 1 {
		t.Errorf("ListInviteCodes len = %d, want 1", len(codes))
	}
}

func TestHB10_HandleListInviteCodes_NotAdmin(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/invite-codes", nil)
	handleListInviteCodes(w, r)
	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestHB10_HandleListInviteCodes_Admin(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/invite-codes", nil)
	r.Header.Set("X-Request-Role", "admin")
	handleListInviteCodes(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleCreateInviteCode_Admin(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/invite-codes", strings.NewReader(`{"max_uses":5,"role":"consumer"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-Role", "admin")
	handleCreateInviteCode(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleCreateInviteCode_InvalidRole(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/invite-codes", strings.NewReader(`{"max_uses":5,"role":"invalid"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-Role", "admin")
	handleCreateInviteCode(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleDeleteInviteCode_Admin(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/invite-codes/"+code, nil)
	r.SetPathValue("code", code)
	r.Header.Set("X-Request-Role", "admin")
	handleDeleteInviteCode(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleListConsumers_Admin(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/consumers", nil)
	r.Header.Set("X-Request-Role", "admin")
	handleListConsumers(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleCreateConsumer_Admin(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	w := httptest.NewRecorder()
	body := `{"name":"test","invite_code":"` + code + `"}`
	r := httptest.NewRequest("POST", "/api/consumers", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-Role", "admin")
	handleCreateConsumer(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleDeleteConsumer_Admin(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, _ := multiUser.CreateConsumer("test", code)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/consumers/"+consumer.ID, nil)
	r.SetPathValue("id", consumer.ID)
	r.Header.Set("X-Request-Role", "admin")
	handleDeleteConsumer(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleToggleConsumer_Admin(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, _ := multiUser.CreateConsumer("test", code)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/consumers/"+consumer.ID+"/toggle", strings.NewReader(`{"enabled":false}`))
	r.SetPathValue("id", consumer.ID)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-Role", "admin")
	handleToggleConsumer(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleConsumerRegister_MissingFields(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/consumers/register", strings.NewReader(`{"name":"test"}`))
	r.Header.Set("Content-Type", "application/json")
	handleConsumerRegister(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleUpdateConsumer_Admin(t *testing.T) {
	setupTestEnv(t)
	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, _ := multiUser.CreateConsumer("test", code)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/api/consumers/"+consumer.ID, strings.NewReader(`{"disabled":true}`))
	r.SetPathValue("id", consumer.ID)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-Role", "admin")
	handleUpdateConsumer(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_GetRequestOwner(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-Owner", "owner-1")
	if got := getRequestOwner(r); got != "owner-1" {
		t.Errorf("getRequestOwner = %q, want owner-1", got)
	}
}

func TestHB10_IsAdmin(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-Role", "admin")
	if !isAdmin(r) {
		t.Error("isAdmin should return true for admin role")
	}
}

func TestHB10_IsAdmin_False(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Request-Role", "consumer")
	if isAdmin(r) {
		t.Error("isAdmin should return false for consumer role")
	}
}

// ============================================================
// encryptor.go tests
// ============================================================

func TestHB10_Encryptor_EncryptDecrypt(t *testing.T) {
	setupTestEnv(t)
	plain := "hello-world-secret"
	encrypted, err := enc.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if encrypted == plain {
		t.Error("Encrypt should produce different output")
	}
	decrypted, err := enc.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if decrypted != plain {
		t.Errorf("Decrypt = %q, want %q", decrypted, plain)
	}
}

func TestHB10_Encryptor_DecryptNonPrefixed(t *testing.T) {
	setupTestEnv(t)
	plain := "not-encrypted"
	result, err := enc.Decrypt(plain)
	if err != nil {
		t.Fatalf("Decrypt non-prefixed failed: %v", err)
	}
	if result != plain {
		t.Errorf("Decrypt non-prefixed = %q, want %q", result, plain)
	}
}

func TestHB10_IsEncrypted(t *testing.T) {
	setupTestEnv(t)
	if IsEncrypted("plaintext") {
		t.Error("IsEncrypted should return false for plaintext")
	}
	encrypted, _ := enc.Encrypt("test")
	if !IsEncrypted(encrypted) {
		t.Error("IsEncrypted should return true for encrypted value")
	}
}

func TestHB10_EncryptField(t *testing.T) {
	setupTestEnv(t)
	result := encryptField("secret")
	if result == "secret" {
		t.Error("encryptField should encrypt the value")
	}
}

func TestHB10_EncryptField_Empty(t *testing.T) {
	setupTestEnv(t)
	if got := encryptField(""); got != "" {
		t.Errorf("encryptField empty = %q, want empty", got)
	}
}

func TestHB10_DecryptField(t *testing.T) {
	setupTestEnv(t)
	encrypted := encryptField("secret")
	decrypted := decryptField(encrypted)
	if decrypted != "secret" {
		t.Errorf("decryptField = %q, want secret", decrypted)
	}
}

func TestHB10_DecryptAPIKey(t *testing.T) {
	setupTestEnv(t)
	encrypted := encryptField("sk-test-key")
	decrypted, err := decryptAPIKey(encrypted)
	if err != nil {
		t.Fatalf("decryptAPIKey failed: %v", err)
	}
	if decrypted != "sk-test-key" {
		t.Errorf("decryptAPIKey = %q, want sk-test-key", decrypted)
	}
}

// ============================================================
// data_integrity.go tests
// ============================================================

func TestHB10_ComputeHMAC(t *testing.T) {
	setupTestEnv(t)
	data := []byte("test data")
	mac := computeHMAC(data)
	if len(mac) != 32 {
		t.Errorf("computeHMAC len = %d, want 32", len(mac))
	}
}

func TestHB10_VerifyHMAC_Valid(t *testing.T) {
	setupTestEnv(t)
	data := []byte("test data")
	mac := computeHMAC(data)
	if !verifyHMAC(data, mac) {
		t.Error("verifyHMAC should return true for valid MAC")
	}
}

func TestHB10_VerifyHMAC_Tampered(t *testing.T) {
	setupTestEnv(t)
	data := []byte("test data")
	mac := computeHMAC(data)
	tampered := []byte("test data!")
	if verifyHMAC(tampered, mac) {
		t.Error("verifyHMAC should return false for tampered data")
	}
}

func TestHB10_SaveLoadWithIntegrity(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	path := dir + "/test.json"
	original := map[string]string{"key": "value"}
	if err := saveWithIntegrity(path, original); err != nil {
		t.Fatalf("saveWithIntegrity failed: %v", err)
	}
	var loaded map[string]string
	if err := loadWithIntegrity(path, &loaded); err != nil {
		t.Fatalf("loadWithIntegrity failed: %v", err)
	}
	if loaded["key"] != "value" {
		t.Errorf("loaded = %v, want key=value", loaded)
	}
}

// ============================================================
// genesis.go tests
// ============================================================

func TestHB10_ComputeNetworkID(t *testing.T) {
	g := GenesisBlock{
		NetworkName: "test-net",
		GenesisNode: "mmx-test",
		CreatedAt:   "2026-01-01T00:00:00Z",
		Version:     1,
	}
	id := computeNetworkID(g)
	if !strings.HasPrefix(id, "0x") {
		t.Errorf("computeNetworkID = %q, want 0x prefix", id)
	}
}

func TestHB10_VerifyNetworkID(t *testing.T) {
	if !VerifyNetworkID(NetworkID) {
		t.Error("VerifyNetworkID should return true for our own NetworkID")
	}
}

func TestHB10_VerifyNetworkID_Wrong(t *testing.T) {
	if VerifyNetworkID("0xdeadbeef") {
		t.Error("VerifyNetworkID should return false for wrong ID")
	}
}

func TestHB10_GenesisJSON(t *testing.T) {
	j := GenesisJSON()
	if !strings.Contains(j, "openmodelpool") {
		t.Errorf("GenesisJSON = %q, want to contain openmodelpool", j)
	}
}

func TestHB10_GenesisInfo(t *testing.T) {
	info := GenesisInfo()
	if info["network_id"] == nil {
		t.Error("GenesisInfo should have network_id")
	}
	if info["network_name"] == nil {
		t.Error("GenesisInfo should have network_name")
	}
}

func TestHB10_HandleJoinRequest_BadNodeID(t *testing.T) {
	req := NodeJoinRequest{
		NetworkID: NetworkID,
		NodeID:    "bad",
		PubKey:    "test-key",
	}
	resp := HandleJoinRequest(req)
	if resp.Accepted {
		t.Error("HandleJoinRequest should reject bad node_id format")
	}
}

func TestHB10_HandleJoinRequest_NoPubKey(t *testing.T) {
	req := NodeJoinRequest{
		NetworkID: NetworkID,
		NodeID:    "mmx-testnode",
		PubKey:    "",
	}
	resp := HandleJoinRequest(req)
	if resp.Accepted {
		t.Error("HandleJoinRequest should reject missing pub_key")
	}
}

func TestHB10_HandleJoinRequest_WrongNetworkID(t *testing.T) {
	req := NodeJoinRequest{
		NetworkID: "0xdeadbeef",
		NodeID:    "mmx-testnode",
		PubKey:    "test-key",
	}
	resp := HandleJoinRequest(req)
	if resp.Accepted {
		t.Error("HandleJoinRequest should reject wrong network_id")
	}
}

func TestHB10_HandleJoinRequest_Valid(t *testing.T) {
	req := NodeJoinRequest{
		NetworkID: NetworkID,
		NodeID:    "mmx-testnode",
		PubKey:    "test-key",
		Endpoint:  "http://example.com:8000",
	}
	resp := HandleJoinRequest(req)
	if !resp.Accepted {
		t.Errorf("HandleJoinRequest should accept valid request: %s", resp.Reason)
	}
}

// ============================================================
// credits.go tests
// ============================================================

func TestHB10_DefaultQuotaAllocation(t *testing.T) {
	a := DefaultQuotaAllocation()
	if a.GuestKeyPercent != 50 || a.PublicKeyPercent != 50 {
		t.Errorf("DefaultQuotaAllocation = %+v, want 50/50", a)
	}
}

func TestHB10_AllocationManager_SetAllocation(t *testing.T) {
	dir := t.TempDir()
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}
	if err := am.SetAllocation(30); err != nil {
		t.Fatalf("SetAllocation failed: %v", err)
	}
	a := am.GetAllocation()
	if a.GuestKeyPercent != 30 || a.PublicKeyPercent != 70 {
		t.Errorf("SetAllocation = %+v, want 30/70", a)
	}
}

func TestHB10_AllocationManager_SetAllocation_Invalid(t *testing.T) {
	dir := t.TempDir()
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}
	if err := am.SetAllocation(101); err == nil {
		t.Error("SetAllocation should reject > 100")
	}
	if err := am.SetAllocation(-1); err == nil {
		t.Error("SetAllocation should reject < 0")
	}
}

func TestHB10_AllocationManager_RecordUsage(t *testing.T) {
	dir := t.TempDir()
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}
	am.RecordUsage(true, 100)
	am.RecordUsage(false, 200)
	stats := am.GetUsageStats()
	if stats["used_guest_tokens"].(int64) != 100 {
		t.Errorf("used_guest_tokens = %v, want 100", stats["used_guest_tokens"])
	}
	if stats["used_public_tokens"].(int64) != 200 {
		t.Errorf("used_public_tokens = %v, want 200", stats["used_public_tokens"])
	}
}

// ============================================================
// algorithm_chain.go tests
// ============================================================

func TestHB10_DefaultAlgorithmParams(t *testing.T) {
	p := DefaultAlgorithmParams()
	if p.OpenKeyRatio != 0.30 {
		t.Errorf("OpenKeyRatio = %f, want 0.30", p.OpenKeyRatio)
	}
}

func TestHB10_NewAlgorithmChain(t *testing.T) {
	c := NewAlgorithmChain()
	if c == nil {
		t.Error("NewAlgorithmChain returned nil")
	}
}

func TestHB10_AlgorithmChain_GetCurrentParams(t *testing.T) {
	c := NewAlgorithmChain()
	p := c.GetCurrentParams()
	if p.TrustWeight != 0.25 {
		t.Errorf("TrustWeight = %f, want 0.25", p.TrustWeight)
	}
}

func TestHB10_AlgorithmChain_UpdateParams(t *testing.T) {
	c := NewAlgorithmChain()
	newParams := DefaultAlgorithmParams()
	newParams.TrustWeight = 0.5
	c.UpdateParams(newParams)
	if c.GetCurrentParams().TrustWeight != 0.5 {
		t.Errorf("TrustWeight = %f, want 0.5", c.GetCurrentParams().TrustWeight)
	}
}

// ============================================================
// eventbus.go tests
// ============================================================

func TestHB10_EventBus_SubscribeUnsubscribe(t *testing.T) {
	eb := &EventBus{clients: make(map[string]chan SSEEvent)}
	id, ch := eb.Subscribe()
	if id == "" {
		t.Error("Subscribe returned empty ID")
	}
	if ch == nil {
		t.Error("Subscribe returned nil channel")
	}
	eb.Unsubscribe(id)
}

func TestHB10_EventBus_Broadcast(t *testing.T) {
	eb := &EventBus{clients: make(map[string]chan SSEEvent)}
	id, ch := eb.Subscribe()
	defer eb.Unsubscribe(id)

	eb.Broadcast(SSEEvent{Type: "test", Data: "hello"})
	select {
	case evt := <-ch:
		if evt.Type != "test" {
			t.Errorf("event type = %q, want test", evt.Type)
		}
	case <-time.After(time.Second):
		t.Error("Broadcast timed out")
	}
}

func TestHB10_EventBus_BroadcastAutoTime(t *testing.T) {
	eb := &EventBus{clients: make(map[string]chan SSEEvent)}
	id, ch := eb.Subscribe()
	defer eb.Unsubscribe(id)

	eb.Broadcast(SSEEvent{Type: "test"})
	evt := <-ch
	if evt.Time == "" {
		t.Error("Broadcast should auto-fill Time")
	}
}

func TestHB10_EventBus_BroadcastNoClients(t *testing.T) {
	eb := &EventBus{clients: make(map[string]chan SSEEvent)}
	eb.Broadcast(SSEEvent{Type: "test"})
}

func TestHB10_GetEventBusStats_Nil(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()
	stats := GetEventBusStats()
	if stats["enabled"] != false {
		t.Error("GetEventBusStats should return enabled=false when nil")
	}
}

func TestHB10_BroadcastProviderStatus_Nil(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()
	BroadcastProviderStatus("test", "healthy")
}

func TestHB10_BroadcastHealthChange_Nil(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()
	BroadcastHealthChange("test", "healthy", "degraded")
}

func TestHB10_BroadcastConfigUpdate_Nil(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()
	BroadcastConfigUpdate("test_key")
}

// ============================================================
// config.go tests
// ============================================================

func TestHB10_Config_Get_Set(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("test_key", "test_value")
	if got := cfg.Get("test_key", ""); got != "test_value" {
		t.Errorf("Get = %q, want test_value", got)
	}
}

func TestHB10_Config_Get_Default(t *testing.T) {
	setupTestEnv(t)
	if got := cfg.Get("nonexistent", "default"); got != "default" {
		t.Errorf("Get default = %q, want default", got)
	}
}

func TestHB10_Config_SetMany(t *testing.T) {
	setupTestEnv(t)
	cfg.SetMany(map[string]any{"key1": "val1", "key2": "val2"})
	if got := cfg.Get("key1", ""); got != "val1" {
		t.Errorf("Get key1 = %q, want val1", got)
	}
	if got := cfg.Get("key2", ""); got != "val2" {
		t.Errorf("Get key2 = %q, want val2", got)
	}
}

func TestHB10_Config_Masked(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("proxy_api_key", "sk-1234567890abcdef")
	m := cfg.Masked()
	if _, ok := m["proxy_api_key"]; ok {
		t.Error("Masked should not contain raw proxy_api_key")
	}
	if _, ok := m["proxy_api_key_masked"]; !ok {
		t.Error("Masked should contain proxy_api_key_masked")
	}
}

func TestHB10_MaskToken_Short(t *testing.T) {
	if got := maskToken("short"); got != "***" {
		t.Errorf("maskToken short = %q, want ***", got)
	}
}

func TestHB10_MaskToken_Long(t *testing.T) {
	if got := maskToken("sk-1234567890abcdef"); got != "sk-123...cdef" {
		t.Errorf("maskToken long = %q, want sk-123...cdef", got)
	}
}

func TestHB10_ToUpper(t *testing.T) {
	if got := toUpper("hello_world"); got != "HELLO_WORLD" {
		t.Errorf("toUpper = %q, want HELLO_WORLD", got)
	}
}

func TestHB10_AtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.json"
	if err := atomicWriteFile(path, []byte(`{"ok":true}`), 0644); err != nil {
		t.Fatalf("atomicWriteFile failed: %v", err)
	}
}

// ============================================================
// middleware.go tests
// ============================================================

func TestHB10_IsOriginAllowed_Exact(t *testing.T) {
	if !isOriginAllowed("http://localhost:8000", "http://localhost:8000,http://other.com") {
		t.Error("isOriginAllowed should match exact origin")
	}
}

func TestHB10_IsOriginAllowed_Wildcard(t *testing.T) {
	if isOriginAllowed("http://sub.example.com", "*.example.com") {
		t.Error("isOriginAllowed should NOT match wildcard subdomain (wildcards removed for security)")
	}
	if !isOriginAllowed("http://example.com", "http://example.com") {
		t.Error("isOriginAllowed should match exact origin")
	}
}

func TestHB10_IsOriginAllowed_NotAllowed(t *testing.T) {
	if isOriginAllowed("http://evil.com", "http://localhost:8000") {
		t.Error("isOriginAllowed should reject non-matching origin")
	}
}

func TestHB10_IsLocalOrPrivateIP_Loopback(t *testing.T) {
	if !isLocalOrPrivateIP("127.0.0.1") {
		t.Error("127.0.0.1 should be local")
	}
}

func TestHB10_IsLocalOrPrivateIP_Private10(t *testing.T) {
	if !isLocalOrPrivateIP("10.0.0.1") {
		t.Error("10.0.0.1 should be private")
	}
}

func TestHB10_IsLocalOrPrivateIP_Private172(t *testing.T) {
	if !isLocalOrPrivateIP("172.16.0.1") {
		t.Error("172.16.0.1 should be private")
	}
}

func TestHB10_IsLocalOrPrivateIP_Private192(t *testing.T) {
	if !isLocalOrPrivateIP("192.168.1.1") {
		t.Error("192.168.1.1 should be private")
	}
}

func TestHB10_IsLocalOrPrivateIP_Public(t *testing.T) {
	if isLocalOrPrivateIP("8.8.8.8") {
		t.Error("8.8.8.8 should not be local/private")
	}
}

func TestHB10_IsLocalOrPrivateIP_Invalid(t *testing.T) {
	if isLocalOrPrivateIP("not-an-ip") {
		t.Error("Invalid IP should not be local/private")
	}
}

func TestHB10_ExtractToken_Bearer(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer test-token")
	if got := extractToken(r); got != "test-token" {
		t.Errorf("extractToken = %q, want test-token", got)
	}
}

func TestHB10_ExtractToken_Cookie(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "admin_token", Value: "cookie-token"})
	if got := extractToken(r); got != "cookie-token" {
		t.Errorf("extractToken = %q, want cookie-token", got)
	}
}

func TestHB10_ExtractToken_None(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := extractToken(r); got != "" {
		t.Errorf("extractToken = %q, want empty", got)
	}
}

// ============================================================
// metrics.go tests
// ============================================================

func TestHB10_Metrics_RecordRequest(t *testing.T) {
	m := &Metrics{
		requestByModel:    make(map[string]*atomic.Int64),
		requestByProvider: make(map[string]*atomic.Int64),
		latencySum:        make(map[string]*atomic.Int64),
		latencyCount:      make(map[string]*atomic.Int64),
		startTime:         time.Now(),
	}
	m.RecordRequest("gpt-4", "openai", 100, true, 50)
	if m.requestTotal.Load() != 1 {
		t.Errorf("requestTotal = %d, want 1", m.requestTotal.Load())
	}
	if m.tokenUsage.Load() != 50 {
		t.Errorf("tokenUsage = %d, want 50", m.tokenUsage.Load())
	}
}

func TestHB10_Metrics_RecordRequest_Error(t *testing.T) {
	m := &Metrics{
		requestByModel:    make(map[string]*atomic.Int64),
		requestByProvider: make(map[string]*atomic.Int64),
		latencySum:        make(map[string]*atomic.Int64),
		latencyCount:      make(map[string]*atomic.Int64),
		startTime:         time.Now(),
	}
	m.RecordRequest("gpt-4", "openai", 100, false, 0)
	if m.requestErrors.Load() != 1 {
		t.Errorf("requestErrors = %d, want 1", m.requestErrors.Load())
	}
}

func TestHB10_HandleMetrics_Nil(t *testing.T) {
	orig := metrics
	metrics = nil
	defer func() { metrics = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/metrics", nil)
	handleMetrics(w, r)
	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// ============================================================
// handlers_missing.go tests
// ============================================================

func TestHB10_RequireHTTPS_PlainHTTP(t *testing.T) {
	handler := requireHTTPS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler(w, r)
	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestHB10_RequireHTTPS_WithForwardedProto(t *testing.T) {
	handler := requireHTTPS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	handler(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleNodeInfo(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/node/info", nil)
	handleNodeInfo(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleNodePubKey(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/node/pubkey", nil)
	handleNodePubKey(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleAlgorithmValidate(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/algorithm/validate", nil)
	handleAlgorithmValidate(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleAlgorithmGossip_NilChain(t *testing.T) {
	orig := algoChain
	algoChain = nil
	defer func() { algoChain = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/algorithm/gossip", nil)
	handleAlgorithmGossip(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleAlgorithmCurrent_NilChain(t *testing.T) {
	orig := algoChain
	algoChain = nil
	defer func() { algoChain = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/algorithm/current", nil)
	handleAlgorithmCurrent(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleNetworkRegions_NilManager(t *testing.T) {
	orig := regionManager
	regionManager = nil
	defer func() { regionManager = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/regions", nil)
	handleNetworkRegions(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleNetworkRegionNodes_NilManager(t *testing.T) {
	orig := regionManager
	regionManager = nil
	defer func() { regionManager = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/regions/eu/nodes", nil)
	r.SetPathValue("region", "eu")
	handleNetworkRegionNodes(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleNetworkRegionConfigUpdate_NilManager(t *testing.T) {
	orig := regionManager
	regionManager = nil
	defer func() { regionManager = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/api/network/regions/config", strings.NewReader(`{}`))
	handleNetworkRegionConfigUpdate(w, r)
	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestHB10_HandleWAFStatus_NilEngine(t *testing.T) {
	orig := wafEngine
	wafEngine = nil
	defer func() { wafEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/waf/status", nil)
	handleWAFStatus(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleWAFBans_NilEngine(t *testing.T) {
	orig := wafEngine
	wafEngine = nil
	defer func() { wafEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/waf/bans", nil)
	handleWAFBans(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleWAFViolations_NilEngine(t *testing.T) {
	orig := wafEngine
	wafEngine = nil
	defer func() { wafEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/waf/violations", nil)
	handleWAFViolations(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleWAFUnban_NilEngine(t *testing.T) {
	orig := wafEngine
	wafEngine = nil
	defer func() { wafEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/waf/bans/test-key", nil)
	r.SetPathValue("key", "test-key")
	handleWAFUnban(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestHB10_HandleWAFUnban_MissingKey(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/api/waf/bans/", nil)
	r.SetPathValue("key", "")
	handleWAFUnban(w, r)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHB10_HandleAlgorithmHistory_NilGovernor(t *testing.T) {
	orig := governor
	governor = nil
	defer func() { governor = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/algorithm/history", nil)
	handleAlgorithmHistory(w, r)
	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestHB10_HandleAlgorithmProposals_NilGovernor(t *testing.T) {
	orig := governor
	governor = nil
	defer func() { governor = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/algorithm/proposals", nil)
	handleAlgorithmProposals(w, r)
	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestHB10_HandleAlgorithmPropose_NilGovernor(t *testing.T) {
	orig := governor
	governor = nil
	defer func() { governor = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/algorithm/propose", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	handleAlgorithmPropose(w, r)
	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestHB10_HandleAlgorithmVote_NilGovernor(t *testing.T) {
	orig := governor
	governor = nil
	defer func() { governor = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/algorithm/vote", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	handleAlgorithmVote(w, r)
	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestHB10_HandleAlgorithmProposalResolve_NilGovernor(t *testing.T) {
	orig := governor
	governor = nil
	defer func() { governor = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/algorithm/proposals/1/resolve", strings.NewReader(`{}`))
	r.SetPathValue("id", "1")
	r.Header.Set("Content-Type", "application/json")
	handleAlgorithmProposalResolve(w, r)
	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// ============================================================
// stubs.go tests
// ============================================================

func TestHB10_GetDHTStats(t *testing.T) {
	stats := GetDHTStats()
	if stats == nil {
		t.Fatal("GetDHTStats should not return nil")
	}
	if _, ok := stats["enabled"]; !ok {
		t.Error("GetDHTStats should contain enabled key")
	}
}

func TestHB10_GetHeartbeatInterval_Default(t *testing.T) {
	setupTestEnv(t)
	interval := getHeartbeatInterval()
	if interval != 60*time.Second {
		t.Errorf("getHeartbeatInterval = %v, want 60s", interval)
	}
}

func TestHB10_GetHeartbeatInterval_Custom(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("heartbeat_interval_s", "30")
	interval := getHeartbeatInterval()
	if interval != 30*time.Second {
		t.Errorf("getHeartbeatInterval = %v, want 30s", interval)
	}
}

func TestHB10_TouchPeerLastSeen_NilMgr(t *testing.T) {
	orig := netMgr
	netMgr = nil
	defer func() { netMgr = orig }()
	touchPeerLastSeen("test-node")
}

func TestHB10_CollectPeerEndpoints_Empty(t *testing.T) {
	origFed := fed
	origNetMgr := netMgr
	fed = nil
	netMgr = nil
	defer func() { fed = origFed; netMgr = origNetMgr }()
	endpoints := collectPeerEndpoints("http://self:8000")
	if len(endpoints) != 0 {
		t.Errorf("collectPeerEndpoints = %v, want empty", endpoints)
	}
}

// ============================================================
// network_balance.go tests
// ============================================================

func TestHB10_BalanceEngine_RecordContribution(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
	}
	be.RecordContributionBalance("node-1", 1000)
	be.mu.RLock()
	nb := be.nodeBalance["node-1"]
	be.mu.RUnlock()
	if nb == nil || nb.TotalContributed != 1000 {
		t.Errorf("RecordContributionBalance failed: %+v", nb)
	}
}

func TestHB10_BalanceEngine_RecordConsumption(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
	}
	be.RecordConsumptionBalance("node-1", 500)
	be.mu.RLock()
	nb := be.nodeBalance["node-1"]
	be.mu.RUnlock()
	if nb == nil || nb.TotalConsumed != 500 {
		t.Errorf("RecordConsumptionBalance failed: %+v", nb)
	}
}

func TestHB10_BalanceEngine_GetBalanceStatus(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
	}
	status := be.GetBalanceStatus()
	if status == nil {
		t.Error("GetBalanceStatus returned nil")
	}
}

func TestHB10_BalanceEngine_GetAllNodeBalances(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
	}
	balances := be.GetAllNodeBalances()
	if balances == nil {
		t.Error("GetAllNodeBalances returned nil")
	}
}

func TestHB10_BalanceEngine_GetRoutingWeightMultiplier(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		adjustments: make(map[string]*BalanceAdjustment),
		config:      DefaultBalanceConfig(),
	}
	mult := be.GetRoutingWeightMultiplier("node-1")
	if mult != 1.0 {
		t.Errorf("GetRoutingWeightMultiplier = %f, want 1.0", mult)
	}
}

func TestHB10_BalanceEngine_GetPriorityDelta(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		adjustments: make(map[string]*BalanceAdjustment),
		config:      DefaultBalanceConfig(),
	}
	delta := be.GetPriorityDelta("node-1")
	if delta != 0 {
		t.Errorf("GetPriorityDelta = %d, want 0", delta)
	}
}

// ============================================================
// network_loadbalancer.go tests
// ============================================================

func TestHB10_LB_DefaultConfig(t *testing.T) {
	cfg := DefaultLBConfig()
	if cfg.TrustWeight <= 0 || cfg.TrustWeight > 1 {
		t.Errorf("TrustWeight = %f, want (0,1]", cfg.TrustWeight)
	}
}

func TestHB10_LB_NewLoadBalancer(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	if lb == nil {
		t.Error("NewLoadBalancer returned nil")
	}
}

func TestHB10_LB_ScoreNode_Unknown(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	score := lb.ScoreNode("unknown-node")
	if score < 0 {
		t.Errorf("ScoreNode = %f, want >= 0", score)
	}
}

func TestHB10_LB_UpdateNodeMetrics(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	lb.UpdateNodeMetrics("node-1", 0.5, 0.6, 10, 1000)
	score := lb.ScoreNode("node-1")
	if score <= 0 {
		t.Errorf("ScoreNode = %f, want > 0", score)
	}
}

func TestHB10_LB_RecordRequest(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	lb.RecordRequest("node-1", 100*time.Millisecond, true)
}

func TestHB10_LB_RecordRoute(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	lb.recordRoute("node-1")
}

func TestHB10_TrimTrailingSlash(t *testing.T) {
	if got := trimTrailingSlash("http://example.com/"); got != "http://example.com" {
		t.Errorf("trimTrailingSlash = %q, want http://example.com", got)
	}
	if got := trimTrailingSlash("http://example.com"); got != "http://example.com" {
		t.Errorf("trimTrailingSlash = %q, want http://example.com", got)
	}
}

// ============================================================
// gossip.go tests
// ============================================================

func TestHB10_MessageHash(t *testing.T) {
	msg := &GossipMessage{
		Type:     "sync",
		FromNode: "mmx-test",
	}
	h := messageHash(msg)
	if len(h) != 64 {
		t.Errorf("messageHash len = %d, want 64", len(h))
	}
}

func TestHB10_CryptoShuffle(t *testing.T) {
	nodes := []NodeInfo{
		{NodeID: "mmx-1"},
		{NodeID: "mmx-2"},
		{NodeID: "mmx-3"},
	}
	cryptoShuffle(nodes)
	if len(nodes) != 3 {
		t.Errorf("cryptoShuffle changed length to %d", len(nodes))
	}
}

// ============================================================
// platform_discovery.go tests
// ============================================================

func TestHB10_DiscoveredPlatform_Struct(t *testing.T) {
	p := DiscoveredPlatform{
		ID:     "test",
		Name:   "Test Platform",
		Status: "new",
	}
	if p.ID != "test" {
		t.Errorf("DiscoveredPlatform.ID = %q", p.ID)
	}
}

// ============================================================
// network_quota.go tests
// ============================================================

func TestHB10_QuotaInfo_Struct(t *testing.T) {
	qi := QuotaInfo{
		NodeID:      "mmx-test",
		GlobalQuota: 10000,
		UserQuota:   3000,
	}
	if qi.NodeID != "mmx-test" {
		t.Errorf("QuotaInfo.NodeID = %q", qi.NodeID)
	}
}

// ============================================================
// client.go tests
// ============================================================

func TestHB10_Truncate(t *testing.T) {
	if got := truncate("hello world", 5); got != "hello" {
		t.Errorf("truncate = %q, want hello", got)
	}
	if got := truncate("hi", 10); got != "hi" {
		t.Errorf("truncate = %q, want hi", got)
	}
}

func TestHB10_StrPtr(t *testing.T) {
	p := strPtr("test")
	if p == nil || *p != "test" {
		t.Error("strPtr failed")
	}
}

func TestHB10_WriteSSEChunk(t *testing.T) {
	var buf strings.Builder
	chunk := ChatChunk{
		ID:      "chatcmpl-1",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
	}
	writeSSEChunk(&buf, chunk)
	if !strings.Contains(buf.String(), "chatcmpl-1") {
		t.Error("writeSSEChunk should contain chunk ID")
	}
}

func TestHB10_WriteSSEError(t *testing.T) {
	var buf strings.Builder
	writeSSEError(&buf, "gpt-4", "test error")
	if !strings.Contains(buf.String(), "test error") {
		t.Error("writeSSEError should contain error message")
	}
}

// ============================================================
// network.go tests
// ============================================================

func TestHB10_RouteTable_Put(t *testing.T) {
	rt := initRouteTable()
	rt.Put("node-1", "test", []string{"http://example.com:8000"})
	if rt.Count() != 1 {
		t.Errorf("RouteTable.Count = %d, want 1", rt.Count())
	}
}

func TestHB10_RouteTable_Get(t *testing.T) {
	rt := initRouteTable()
	rt.Put("node-1", "test", []string{"http://example.com:8000"})
	e := rt.Get("node-1")
	if e == nil {
		t.Error("RouteTable.Get returned nil")
	}
}

func TestHB10_RouteTable_GetMissing(t *testing.T) {
	rt := initRouteTable()
	if e := rt.Get("missing"); e != nil {
		t.Error("RouteTable.Get should return nil for missing")
	}
}

func TestHB10_RouteTable_Remove(t *testing.T) {
	rt := initRouteTable()
	rt.Put("node-1", "test", []string{"http://example.com:8000"})
	rt.Remove("node-1")
	if rt.Count() != 0 {
		t.Errorf("RouteTable.Count = %d, want 0", rt.Count())
	}
}

func TestHB10_RouteTable_GetAll(t *testing.T) {
	rt := initRouteTable()
	rt.Put("node-1", "test", []string{"http://example.com:8000"})
	rt.Put("node-2", "test2", []string{"http://example.com:8001"})
	all := rt.GetAll()
	if len(all) != 2 {
		t.Errorf("RouteTable.GetAll len = %d, want 2", len(all))
	}
}

func TestHB10_FirstAddress_NonEmpty(t *testing.T) {
	if got := firstAddress([]string{"a", "b"}); got != "a" {
		t.Errorf("firstAddress = %q, want a", got)
	}
}

func TestHB10_FirstAddress_Empty(t *testing.T) {
	if got := firstAddress(nil); got != "" {
		t.Errorf("firstAddress = %q, want empty", got)
	}
}

// ============================================================
// update.go tests
// ============================================================

func TestHB10_IsInFlightPhase(t *testing.T) {
	if !isInFlightPhase(PhaseDownloading) {
		t.Error("PhaseDownloading should be in-flight")
	}
	if isInFlightPhase(PhaseIdle) {
		t.Error("PhaseIdle should not be in-flight")
	}
}

func TestHB10_ShortNodeID(t *testing.T) {
	result := shortNodeID("mmx-1234567890abcdef")
	if result != "mmx-1234…cdef" {
		t.Errorf("shortNodeID = %q, want mmx-1234…cdef", result)
	}
}

func TestHB10_PeerDisplayName(t *testing.T) {
	peer := NodeInfo{NodeID: "mmx-test", GitHubUser: "Test Node"}
	if got := peerDisplayName(peer); got != "Test Node" {
		t.Errorf("peerDisplayName = %q, want Test Node", got)
	}
}

func TestHB10_PeerDisplayName_Fallback(t *testing.T) {
	peer := NodeInfo{NodeID: "mmx-test"}
	if got := peerDisplayName(peer); got != "mmx-test" {
		t.Errorf("peerDisplayName = %q, want mm-test", got)
	}
}

func TestHB10_DedupeStrings(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b"}
	result := dedupeStrings(input)
	if len(result) != 3 {
		t.Errorf("dedupeStrings len = %d, want 3", len(result))
	}
}

func TestHB10_DedupeStrings_Empty(t *testing.T) {
	result := dedupeStrings(nil)
	if len(result) != 0 {
		t.Errorf("dedupeStrings nil len = %d, want 0", len(result))
	}
}

func TestHB10_ValidTimestamp(t *testing.T) {
	ts := time.Now().Format(time.RFC3339)
	if !validTimestamp(ts) {
		t.Error("validTimestamp should accept current RFC3339 timestamp")
	}
}

func TestHB10_ValidTimestamp_Empty(t *testing.T) {
	if validTimestamp("") {
		t.Error("validTimestamp should reject empty string")
	}
}

func TestHB10_PlatformAssetName(t *testing.T) {
	name := platformAssetName()
	if name == "" {
		t.Error("platformAssetName should not be empty")
	}
}

// ============================================================
// node_weight.go tests
// ============================================================

func TestHB10_GenerateReqID(t *testing.T) {
	id := generateReqID()
	if id == "" {
		t.Error("generateReqID returned empty")
	}
}

func TestHB10_SimpleError(t *testing.T) {
	e := &simpleError{msg: "test error"}
	if e.Error() != "test error" {
		t.Errorf("simpleError.Error = %q, want test error", e.Error())
	}
}

// ============================================================
// waf.go tests
// ============================================================

func TestHB10_ParseList(t *testing.T) {
	result := parseList("a, b, c")
	if len(result) != 3 {
		t.Errorf("parseList len = %d, want 3", len(result))
	}
}

func TestHB10_ParseList_Empty(t *testing.T) {
	result := parseList("")
	if len(result) != 0 {
		t.Errorf("parseList empty len = %d, want 0", len(result))
	}
}

func TestHB10_ParseListToSet(t *testing.T) {
	result := parseListToSet("a, b, c")
	if len(result) != 3 {
		t.Errorf("parseListToSet len = %d, want 3", len(result))
	}
	if !result["a"] || !result["b"] || !result["c"] {
		t.Error("parseListToSet missing expected keys")
	}
}

func TestHB10_WAFEngine_New(t *testing.T) {
	e := NewWAFEngine()
	if e == nil {
		t.Error("NewWAFEngine returned nil")
	}
}

func TestHB10_WAFEngine_Check_Disabled(t *testing.T) {
	e := NewWAFEngine()
	ok, _ := e.Check(httptest.NewRequest("GET", "/", nil))
	if !ok {
		t.Error("Disabled WAF should allow all requests")
	}
}

func TestHB10_WAFEngine_AddBan(t *testing.T) {
	e := NewWAFEngine()
	e.AddBan("test-key", "test reason", time.Hour)
	bans := e.Bans()
	if len(bans) != 1 {
		t.Errorf("Bans len = %d, want 1", len(bans))
	}
}

func TestHB10_WAFEngine_RemoveBan(t *testing.T) {
	e := NewWAFEngine()
	e.AddBan("test-key", "test reason", time.Hour)
	if !e.RemoveBan("test-key") {
		t.Error("RemoveBan should return true for existing ban")
	}
	if e.RemoveBan("test-key") {
		t.Error("RemoveBan should return false for already removed ban")
	}
}

func TestHB10_WAFEngine_Status(t *testing.T) {
	e := NewWAFEngine()
	status := e.Status()
	if status == nil {
		t.Error("Status returned nil")
	}
}

func TestHB10_WAFEngine_Violations_Empty(t *testing.T) {
	e := NewWAFEngine()
	vs := e.Violations()
	if len(vs) != 0 {
		t.Errorf("Violations len = %d, want 0", len(vs))
	}
}

func TestHB10_WAFEngine_CheckContent_Disabled(t *testing.T) {
	e := NewWAFEngine()
	ok, _ := e.CheckContent("test content")
	if !ok {
		t.Error("Disabled WAF should allow all content")
	}
}

// ============================================================
// algorithm_governance.go tests
// ============================================================

func TestHB10_ProposalStatus_IsTerminal(t *testing.T) {
	if !ProposalStatusPassed.isTerminal() {
		t.Error("ProposalStatusPassed should be terminal")
	}
	if !ProposalStatusRejected.isTerminal() {
		t.Error("ProposalStatusRejected should be terminal")
	}
	if ProposalStatusOpen.isTerminal() {
		t.Error("ProposalStatusOpen should not be terminal")
	}
}

func TestHB10_VoteChoice_IsValid(t *testing.T) {
	if !VoteYes.isValid() {
		t.Error("VoteYes should be valid")
	}
	if !VoteNo.isValid() {
		t.Error("VoteNo should be valid")
	}
	if VoteChoice("invalid").isValid() {
		t.Error("Invalid vote choice should not be valid")
	}
}

func TestHB10_NowRFC3339(t *testing.T) {
	s := nowRFC3339()
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		t.Errorf("nowRFC3339 = %q, parse error: %v", s, err)
	}
}

func TestHB10_NewProposalID(t *testing.T) {
	id := newProposalID()
	if id == "" {
		t.Error("newProposalID returned empty")
	}
}

func TestHB10_TrimSpace(t *testing.T) {
	if got := trimSpace("  hello  "); got != "hello" {
		t.Errorf("trimSpace = %q, want hello", got)
	}
}

// ============================================================
// network_global_pool.go tests
// ============================================================

func TestHB10_GlobalPool_JoinPool(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		dataPath:          dir + "/global_pool.json",
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	err := gp.JoinPool("node-1", "us", 10000)
	if err != nil {
		t.Fatalf("JoinPool failed: %v", err)
	}
	nodes := gp.GetNodes()
	if len(nodes) != 1 {
		t.Errorf("GetNodes len = %d, want 1", len(nodes))
	}
}

func TestHB10_GlobalPool_JoinPool_EmptyNodeID(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		dataPath:          dir + "/global_pool.json",
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	err := gp.JoinPool("", "us", 1000)
	if err == nil {
		t.Error("JoinPool should reject empty nodeID")
	}
}

func TestHB10_GlobalPool_Contribute(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		dataPath:          dir + "/global_pool.json",
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	gp.JoinPool("node-1", "us", 10000)
	err := gp.Contribute("node-1", 500)
	if err != nil {
		t.Fatalf("Contribute failed: %v", err)
	}
}

func TestHB10_GlobalPool_GetStats(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		dataPath:          dir + "/global_pool.json",
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	gp.JoinPool("node-1", "us", 10000)
	stats := gp.GetStats()
	if stats == nil {
		t.Error("GetStats returned nil")
	}
}

func TestHB10_GlobalPool_SelectBestNode(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		dataPath:          dir + "/global_pool.json",
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	gp.JoinPool("node-1", "us", 10000)
	node := gp.SelectBestNode("us")
	if node == nil {
		t.Error("SelectBestNode should find node for matching region")
	}
}

func TestHB10_GlobalPool_Heartbeat(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		dataPath:          dir + "/global_pool.json",
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	gp.JoinPool("node-1", "us", 10000)
	gp.Heartbeat("node-1")
}

// ============================================================
// auth.go tests
// ============================================================

func TestHB10_ValidatePasswordStrength_Valid(t *testing.T) {
	if err := validatePasswordStrength("StrongPass123!"); err != nil {
		t.Errorf("validatePasswordStrength should accept strong password: %v", err)
	}
}

func TestHB10_ValidatePasswordStrength_Short(t *testing.T) {
	if err := validatePasswordStrength("short"); err == nil {
		t.Error("validatePasswordStrength should reject short password")
	}
}

func TestHB10_RandomString(t *testing.T) {
	s := randomString(16)
	if len(s) != 16 {
		t.Errorf("randomString len = %d, want 16", len(s))
	}
}

func TestHB10_RandomString_Unique(t *testing.T) {
	s1 := randomString(16)
	s2 := randomString(16)
	if s1 == s2 {
		t.Error("randomString should produce unique strings")
	}
}

// ============================================================
// reputation.go tests
// ============================================================

func TestHB10_ReputationManager_RecordCall(t *testing.T) {
	dir := t.TempDir()
	rm := &ReputationManager{
		dataDir:  dir,
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
	}
	rm.RecordCall("node-1", true, 50)
	rep := rm.GetReputation("node-1")
	if rep == nil {
		t.Error("GetReputation returned nil after RecordCall")
	}
}

func TestHB10_ReputationManager_GetReputation_Missing(t *testing.T) {
	dir := t.TempDir()
	rm := &ReputationManager{
		dataDir:  dir,
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
	}
	rep := rm.GetReputation("missing")
	if rep != nil {
		t.Error("GetReputation should return nil for missing")
	}
}

func TestHB10_ReputationManager_GetAllReputations(t *testing.T) {
	dir := t.TempDir()
	rm := &ReputationManager{
		dataDir:  dir,
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
	}
	rm.RecordCall("node-1", true, 50)
	all := rm.GetAllReputations()
	if len(all) != 1 {
		t.Errorf("GetAllReputations len = %d, want 1", len(all))
	}
}

// ============================================================
// message.go tests
// ============================================================

func TestHB10_GenerateMsgID(t *testing.T) {
	id, err := generateMsgID()
	if err != nil {
		t.Fatalf("generateMsgID failed: %v", err)
	}
	if id == "" {
		t.Error("generateMsgID returned empty")
	}
}

func TestHB10_ValidMsgType(t *testing.T) {
	if !validMsgType("request") {
		t.Error("request should be valid msg type")
	}
	if !validMsgType("collaboration") {
		t.Error("collaboration should be valid msg type")
	}
	if !validMsgType("system") {
		t.Error("system should be valid msg type")
	}
	if !validMsgType("general") {
		t.Error("general should be valid msg type")
	}
	if validMsgType("info") {
		t.Error("info should not be valid msg type")
	}
	if validMsgType("invalid") {
		t.Error("invalid should not be valid msg type")
	}
}

// ============================================================
// tracker.go tests
// ============================================================

func TestHB10_Round1(t *testing.T) {
	if got := round1(1.2345); got != 1.2 {
		t.Errorf("round1 = %f, want 1.2", got)
	}
}

func TestHB10_Round4(t *testing.T) {
	if got := round4(1.23456); got != 1.2346 {
		t.Errorf("round4 = %f, want 1.2346", got)
	}
}

// ============================================================
// node_registry.go tests
// ============================================================

func TestHB10_CloneStrings(t *testing.T) {
	orig := []string{"a", "b", "c"}
	cloned := cloneStrings(orig)
	if len(cloned) != len(orig) {
		t.Errorf("cloneStrings len = %d, want %d", len(cloned), len(orig))
	}
}

func TestHB10_OrDefault(t *testing.T) {
	if got := orDefault("", "fallback"); got != "fallback" {
		t.Errorf("orDefault = %q, want fallback", got)
	}
	if got := orDefault("value", "fallback"); got != "value" {
		t.Errorf("orDefault = %q, want value", got)
	}
}

// ============================================================
// network_relay.go tests
// ============================================================

func TestHB10_Sha256Hex(t *testing.T) {
	h := sha256Hex([]byte("test"))
	if len(h) != 64 {
		t.Errorf("sha256Hex len = %d, want 64", len(h))
	}
}

// ============================================================
// invite.go tests
// ============================================================

func TestHB10_EncodeDecodeInvite_Roundtrip(t *testing.T) {
	inv := &FederationInvite{
		NetworkID:   NetworkID,
		Inviter:     "mmx-test",
		InviterKey:  "pubkey123",
		InviteePub:  "*",
		InviteeName: "Test User",
		Endpoint:    "http://example.com:8000",
		ExpiresAt:   time.Now().Add(time.Hour).Format(time.RFC3339),
		Type:        FederationInvitePublic,
		CreatedAt:   time.Now().Format(time.RFC3339),
	}
	encoded, err := EncodeInvite(inv)
	if err != nil {
		t.Fatalf("EncodeInvite failed: %v", err)
	}
	decoded, err := DecodeInvite(encoded)
	if err != nil {
		t.Fatalf("DecodeInvite failed: %v", err)
	}
	if decoded.Inviter != inv.Inviter {
		t.Errorf("Decoded Inviter = %q, want %q", decoded.Inviter, inv.Inviter)
	}
}
