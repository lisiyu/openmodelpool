package main

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ============================================================
// admin.go tests
// ============================================================

func TestHB4_MapKeys(t *testing.T) {
	m := map[string]string{"a": "1", "b": "2", "c": "3"}
	keys := mapKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	set := map[string]bool{}
	for _, k := range keys {
		set[k] = true
	}
	for _, expected := range []string{"a", "b", "c"} {
		if !set[expected] {
			t.Errorf("missing key %s", expected)
		}
	}
}

func TestHB4_MapKeys_Empty(t *testing.T) {
	keys := mapKeys(map[string]string{})
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestHB4_SaveConfig_ClearProxyKey(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("proxy_api_key", "secret123")
	body := `{"proxy_api_key":""}`
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveConfig(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if cfg.Get("proxy_api_key", "") != "" {
		t.Error("proxy_api_key should be cleared")
	}
}

func TestHB4_SaveConfig_CozeAPIToken(t *testing.T) {
	setupTestEnv(t)
	body := `{"coze_api_token":"tok-123"}`
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveConfig(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if cfg.Get("coze_api_token", "") != "tok-123" {
		t.Error("coze_api_token not saved")
	}
}

func TestHB4_SaveConfig_PublicURL(t *testing.T) {
	setupTestEnv(t)
	body := `{"public_url":"https://example.com"}`
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveConfig(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if cfg.Get("public_url", "") != "https://example.com" {
		t.Error("public_url not saved")
	}
}

func TestHB4_ForgotPassword_NotInitialized(t *testing.T) {
	setupTestEnv(t)
	body := `{"email":"test@example.com"}`
	req := httptest.NewRequest("POST", "/api/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleForgotPassword(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB4_ForgotPassword_WrongEmail(t *testing.T) {
	setupTestEnv(t)
	auth.SetupAdmin("admin", "Str0ng!Passwd", "admin@test.com")
	body := `{"email":"wrong@test.com"}`
	req := httptest.NewRequest("POST", "/api/forgot-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleForgotPassword(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200 (no enumeration), got %d", w.Code)
	}
}

func TestHB4_ResetPassword_Success(t *testing.T) {
	setupTestEnv(t)
	auth.SetupAdmin("admin", "Str0ng!Passwd", "admin@test.com")
	token := auth.CreateResetToken()
	body := `{"token":"` + token + `","new_password":"N3wStr0ng!Pass"}`
	req := httptest.NewRequest("POST", "/api/reset-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleResetPassword(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHB4_VerifyResetToken_Valid(t *testing.T) {
	setupTestEnv(t)
	auth.SetupAdmin("admin", "Str0ng!Passwd", "admin@test.com")
	token := auth.CreateResetToken()
	body := `{"token":"` + token + `"}`
	req := httptest.NewRequest("POST", "/api/verify-reset-token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleVerifyResetToken(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleImportConfig_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/config/import", strings.NewReader("not multipart"))
	w := httptest.NewRecorder()
	handleImportConfig(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ============================================================
// handlers.go tests
// ============================================================

func TestHB4_IsPrivateIPv4_10(t *testing.T) {
	ip := net.ParseIP("10.0.0.1")
	if !isPrivateIPv4(ip) {
		t.Error("10.x.x.x should be private")
	}
}

func TestHB4_IsPrivateIPv4_172(t *testing.T) {
	ip := net.ParseIP("172.16.0.1")
	if !isPrivateIPv4(ip) {
		t.Error("172.16.x.x should be private")
	}
}

func TestHB4_IsPrivateIPv4_172OutOfRange(t *testing.T) {
	ip := net.ParseIP("172.15.0.1")
	if isPrivateIPv4(ip) {
		t.Error("172.15.x.x should not be private")
	}
}

func TestHB4_IsPrivateIPv4_192(t *testing.T) {
	ip := net.ParseIP("192.168.1.1")
	if !isPrivateIPv4(ip) {
		t.Error("192.168.x.x should be private")
	}
}

func TestHB4_IsPrivateIPv4_Public(t *testing.T) {
	ip := net.ParseIP("8.8.8.8")
	if isPrivateIPv4(ip) {
		t.Error("8.8.8.8 should not be private")
	}
}

func TestHB4_IsUsableLANIP_Loopback(t *testing.T) {
	ip := net.ParseIP("127.0.0.1")
	if isUsableLANIP(ip) {
		t.Error("loopback should not be usable LAN IP")
	}
}

func TestHB4_IsUsableLANIP_Unspecified(t *testing.T) {
	ip := net.IPv4zero
	if isUsableLANIP(ip) {
		t.Error("unspecified should not be usable LAN IP")
	}
}

func TestHB4_IsUsableLANIP_LinkLocal(t *testing.T) {
	ip := net.ParseIP("169.254.1.1")
	if isUsableLANIP(ip) {
		t.Error("link-local should not be usable LAN IP")
	}
}

func TestHB4_IsUsableLANIP_Multicast(t *testing.T) {
	ip := net.ParseIP("224.0.0.1")
	if isUsableLANIP(ip) {
		t.Error("multicast should not be usable LAN IP")
	}
}

func TestHB4_IsUsableLANIP_Broadcast(t *testing.T) {
	if isUsableLANIP(net.IPv4bcast) {
		t.Error("broadcast should not be usable LAN IP")
	}
}

func TestHB4_IsUsableLANIP_PrivateIP(t *testing.T) {
	ip := net.ParseIP("192.168.1.1")
	if !isUsableLANIP(ip) {
		t.Error("private IP should be usable LAN IP")
	}
}

func TestHB4_IsUsableLANIP_Nil(t *testing.T) {
	if isUsableLANIP(nil) {
		t.Error("nil should not be usable LAN IP")
	}
}

func TestHB4_PickLANIP_Private(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("8.8.8.8"),
		net.ParseIP("192.168.1.1"),
	}
	result := pickLANIP(ips)
	if result != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", result)
	}
}

func TestHB4_PickLANIP_Fallback(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("8.8.8.8"),
	}
	result := pickLANIP(ips)
	if result != "8.8.8.8" {
		t.Errorf("expected 8.8.8.8, got %s", result)
	}
}

func TestHB4_PickLANIP_Empty(t *testing.T) {
	result := pickLANIP(nil)
	if result != "" {
		t.Errorf("expected empty, got %s", result)
	}
}

func TestHB4_PickLANIP_SkipsUnusable(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("169.254.1.1"),
		net.ParseIP("10.0.0.1"),
	}
	result := pickLANIP(ips)
	if result != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", result)
	}
}

func TestHB4_FilterPlaceholder_Empty(t *testing.T) {
	if filterPlaceholder("") != "" {
		t.Error("empty should return empty")
	}
}

func TestHB4_FilterPlaceholder_Example(t *testing.T) {
	if filterPlaceholder("api.example.com") != "" {
		t.Error("placeholder should return empty")
	}
}

func TestHB4_FilterPlaceholder_HttpsExample(t *testing.T) {
	if filterPlaceholder("https://api.example.com") != "" {
		t.Error("placeholder should return empty")
	}
}

func TestHB4_FilterPlaceholder_Real(t *testing.T) {
	if filterPlaceholder("real.domain.com") != "real.domain.com" {
		t.Error("real value should be preserved")
	}
}

func TestHB4_HandleSaveFederationConfig_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/federation/config", strings.NewReader("invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveFederationConfig(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB4_HandleSaveFederationConfig_Success(t *testing.T) {
	setupTestEnv(t)
	origFed := fed
	initFederation(t.TempDir())
	defer func() { fed = origFed }()
	body := `{"gossip_interval_s":"60","heartbeat_interval_s":"120"}`
	req := httptest.NewRequest("POST", "/api/federation/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSaveFederationConfig(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleGetFederationConfig_Defaults(t *testing.T) {
	setupTestEnv(t)
	origFed := fed
	initFederation(t.TempDir())
	defer func() { fed = origFed }()
	req := httptest.NewRequest("GET", "/api/federation/config", nil)
	w := httptest.NewRecorder()
	handleGetFederationConfig(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleGetNodeWeights_Nil(t *testing.T) {
	setupTestEnv(t)
	nwm = nil
	req := httptest.NewRequest("GET", "/api/network/weights", nil)
	w := httptest.NewRecorder()
	handleGetNodeWeights(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleSetNodeWeight_Nil(t *testing.T) {
	setupTestEnv(t)
	nwm = nil
	body := `{"node_id":"n1","weight":1.5}`
	req := httptest.NewRequest("POST", "/api/network/weights", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetNodeWeight(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHB4_HandleGetApprovals_Nil(t *testing.T) {
	setupTestEnv(t)
	nwm = nil
	req := httptest.NewRequest("GET", "/api/network/approvals", nil)
	w := httptest.NewRecorder()
	handleGetApprovals(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleSetTokenBudget_Nil(t *testing.T) {
	setupTestEnv(t)
	nwm = nil
	body := `{"budget":1000}`
	req := httptest.NewRequest("POST", "/api/network/token-budget", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleSetTokenBudget(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHB4_HandleInitNode_Nil(t *testing.T) {
	setupTestEnv(t)
	node = nil
	body := `{"github_user":"testuser","github_id":123}`
	req := httptest.NewRequest("POST", "/api/network/init", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleInitNode(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHB4_HandleInitNode_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	node = nil
	body := `{"github_user":"testuser","github_id":123}`
	req := httptest.NewRequest("POST", "/api/network/init", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleInitNode(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500 (node nil), got %d", w.Code)
	}
}

func TestHB4_HandleJoinNetwork_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/network/join", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleJoinNetwork(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB4_HandleListInvites_Nil(t *testing.T) {
	setupTestEnv(t)
	invMgr = nil
	req := httptest.NewRequest("GET", "/api/invites", nil)
	w := httptest.NewRecorder()
	handleListInvites(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleCreateInvite_Nil(t *testing.T) {
	setupTestEnv(t)
	invMgr = nil
	body := `{"invitee_pub":"*","type":"public"}`
	req := httptest.NewRequest("POST", "/api/invites", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCreateInvite(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHB4_HandleVerifyInvite_Nil(t *testing.T) {
	setupTestEnv(t)
	invMgr = nil
	body := `{"encoded":"test"}`
	req := httptest.NewRequest("POST", "/api/invites/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleVerifyInvite(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ============================================================
// client.go tests
// ============================================================

func TestHB4_Truncate_Short(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should be unchanged")
	}
}

func TestHB4_Truncate_Long(t *testing.T) {
	result := truncate("hello world foo bar", 5)
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
}

func TestHB4_Truncate_Exact(t *testing.T) {
	if truncate("hello", 5) != "hello" {
		t.Error("exact length should be unchanged")
	}
}

func TestHB4_JsonBody(t *testing.T) {
	r := jsonBody(map[string]string{"key": "value"})
	if r == nil {
		t.Fatal("jsonBody returned nil")
	}
}

func TestHB4_StrPtr(t *testing.T) {
	p := strPtr("test")
	if p == nil || *p != "test" {
		t.Error("strPtr should return pointer to string")
	}
}

func TestHB4_WebSessionFormatMessages_Chat(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	result := webSessionFormatMessages(msgs, "chat", "")
	if !strings.Contains(result, "hello") || !strings.Contains(result, "hi") {
		t.Errorf("chat format should contain content, got: %s", result)
	}
}

func TestHB4_WebSessionFormatMessages_Plain(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
	}
	result := webSessionFormatMessages(msgs, "plain", "\n")
	if !strings.Contains(result, "hello") {
		t.Errorf("plain format should contain content, got: %s", result)
	}
}

func TestHB4_WebSessionExtractText_Simple(t *testing.T) {
	data := map[string]any{
		"data": map[string]any{
			"text": "hello world",
		},
	}
	result := webSessionExtractText(data, "data.text")
	if result != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", result)
	}
}

func TestHB4_WebSessionExtractText_MissingPath(t *testing.T) {
	data := map[string]any{"data": "not a map"}
	result := webSessionExtractText(data, "data.text")
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestHB4_WebSessionExtractText_NotString(t *testing.T) {
	data := map[string]any{"data": 123}
	result := webSessionExtractText(data, "data")
	if result != "" {
		t.Errorf("expected empty string for non-string value, got '%s'", result)
	}
}

func TestHB4_WebSessionBuildPayload_Defaults(t *testing.T) {
	cfg := &WebSessionConfig{}
	msgs := []ChatMessage{{Role: "user", Content: "test"}}
	result := webSessionBuildPayload(cfg, "gpt-4", msgs, false)
	if result["prompt"] == nil {
		t.Error("default prompt field should be set")
	}
}

func TestHB4_WebSessionBuildPayload_CustomFields(t *testing.T) {
	cfg := &WebSessionConfig{
		PromptField: "query",
		ModelField:  "model_name",
		StreamField: "streaming",
	}
	msgs := []ChatMessage{{Role: "user", Content: "test"}}
	result := webSessionBuildPayload(cfg, "gpt-4", msgs, true)
	if result["query"] == nil {
		t.Error("custom prompt field should be set")
	}
	if result["model_name"] != "gpt-4" {
		t.Error("model field should be set")
	}
	if result["streaming"] != true {
		t.Error("stream field should be true")
	}
}

func TestHB4_BuildOpenAIBody(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: "hello"}}
	result := buildOpenAIBody("gpt-4", msgs, false, nil)
	if result["model"] != "gpt-4" {
		t.Error("model should be set")
	}
	if result["stream"] != false {
		t.Error("stream should be false")
	}
}

func TestHB4_ParseOpenAISubscription_Valid(t *testing.T) {
	body := `{"hard_limit_usd":120.0,"soft_limit_usd":100.0}`
	result := parseOpenAISubscription([]byte(body))
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["hard_limit_usd"] != 120.0 {
		t.Errorf("expected hard_limit_usd=120.0, got %v", result["hard_limit_usd"])
	}
}

func TestHB4_ParseOpenAISubscription_Empty(t *testing.T) {
	body := `{}`
	result := parseOpenAISubscription([]byte(body))
	if result != nil {
		t.Error("expected nil for empty subscription")
	}
}

func TestHB4_ParseOpenAISubscription_InvalidJSON(t *testing.T) {
	result := parseOpenAISubscription([]byte("not json"))
	if result != nil {
		t.Error("expected nil for invalid JSON")
	}
}

func TestHB4_ParseOpenAIUsage_Valid(t *testing.T) {
	body := `{"total_usage":1234.56,"balance":50.0}`
	result := parseOpenAIUsage([]byte(body))
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["total_usage_cents"] != 1234.56 {
		t.Errorf("expected total_usage_cents=1234.56, got %v", result["total_usage_cents"])
	}
}

func TestHB4_ParseOpenAIUsage_Empty(t *testing.T) {
	result := parseOpenAIUsage([]byte(`{}`))
	if result != nil {
		t.Error("expected nil for empty usage")
	}
}

func TestHB4_QueryKeyBalance_EmptyURL(t *testing.T) {
	result := queryKeyBalance("", "key123")
	if result["available"] != false {
		t.Error("empty URL should return available=false")
	}
}

func TestHB4_QueryKeyBalance_EmptyKey(t *testing.T) {
	result := queryKeyBalance("https://api.example.com", "")
	if result["available"] != false {
		t.Error("empty key should return available=false")
	}
}

func TestHB4_MustParseURL_Valid(t *testing.T) {
	u := mustParseURL("https://example.com/path")
	if u == nil {
		t.Fatal("expected non-nil URL")
	}
	if u.Host != "example.com" {
		t.Errorf("expected host example.com, got %s", u.Host)
	}
}

// ============================================================
// tracker.go tests
// ============================================================

func TestHB4_Round1_Positive(t *testing.T) {
	if round1(1.25) != 1.3 {
		t.Errorf("expected 1.3, got %f", round1(1.25))
	}
}

func TestHB4_Round1_Negative(t *testing.T) {
	if round1(-1.5) != 0 {
		t.Errorf("expected 0 for negative, got %f", round1(-1.5))
	}
}

func TestHB4_Round4_Positive(t *testing.T) {
	if round4(1.23456) != 1.2346 {
		t.Errorf("expected 1.2346, got %f", round4(1.23456))
	}
}

func TestHB4_Round4_Negative(t *testing.T) {
	if round4(-0.5) != 0 {
		t.Errorf("expected 0 for negative, got %f", round4(-0.5))
	}
}

func TestHB4_Tracker_GetEWMA_Empty(t *testing.T) {
	setupTestEnv(t)
	v := tracker.GetEWMA("nonexistent")
	if v != 0 {
		t.Errorf("expected 0 for unknown provider, got %f", v)
	}
}

func TestHB4_Tracker_TotalTokensByProvider_Empty(t *testing.T) {
	setupTestEnv(t)
	totals := tracker.TotalTokensByProvider()
	if len(totals) != 0 {
		t.Errorf("expected empty map, got %d entries", len(totals))
	}
}

func TestHB4_Tracker_RecordAndGetEWMA(t *testing.T) {
	setupTestEnv(t)
	tracker.Record("p1", "Provider1", "gpt-4", 100, 50, 200.0, true, "")
	ewma := tracker.GetEWMA("p1")
	if ewma <= 0 {
		t.Errorf("expected positive EWMA, got %f", ewma)
	}
}

func TestHB4_Tracker_TotalTokensByProvider_AfterRecord(t *testing.T) {
	setupTestEnv(t)
	tracker.Record("p1", "Provider1", "gpt-4", 100, 50, 200.0, true, "")
	time.Sleep(50 * time.Millisecond)
	totals := tracker.TotalTokensByProvider()
	if totals["p1"] != 150 {
		t.Errorf("expected 150, got %d", totals["p1"])
	}
}

func TestHB4_Tracker_ProviderStats_Empty(t *testing.T) {
	setupTestEnv(t)
	stats := tracker.ProviderStats(30)
	if len(stats) != 0 {
		t.Errorf("expected empty stats, got %d", len(stats))
	}
}

func TestHB4_Tracker_Flush(t *testing.T) {
	setupTestEnv(t)
	tracker.Record("p1", "Provider1", "gpt-4", 10, 5, 100.0, true, "")
	tracker.Flush()
}

func TestHB4_Tracker_GetRequestLog_Empty(t *testing.T) {
	setupTestEnv(t)
	log := tracker.GetRequestLog(10)
	if len(log) != 0 {
		t.Errorf("expected empty log, got %d", len(log))
	}
}

func TestHB4_Tracker_RecordCreatesLog(t *testing.T) {
	setupTestEnv(t)
	tracker.Record("p1", "Provider1", "gpt-4", 10, 5, 100.0, true, "")
	log := tracker.GetRequestLog(10)
	if len(log) != 1 {
		t.Errorf("expected 1 log entry, got %d", len(log))
	}
}

func TestHB4_Tracker_RecordWithAccessType(t *testing.T) {
	setupTestEnv(t)
	tracker.RecordWithAccessType("p1", "Provider1", "gpt-4", 10, 5, 100.0, true, "", false, 0, "guest")
	log := tracker.GetRequestLog(10)
	if len(log) != 1 {
		t.Errorf("expected 1 log entry, got %d", len(log))
	}
}

// ============================================================
// performance.go tests
// ============================================================

func TestHB4_GetMemoryUsage(t *testing.T) {
	stats := getMemoryUsage()
	if stats.NumGoroutine <= 0 {
		t.Error("should have at least 1 goroutine")
	}
}

func TestHB4_WorkerPool_Submit(t *testing.T) {
	wp := &WorkerPool{
		taskCh:  make(chan func(), 10),
		workers: 1,
	}
	go wp.worker()
	done := make(chan struct{})
	ok := wp.Submit(func() { close(done) })
	if !ok {
		t.Error("Submit should succeed with room in queue")
	}
	<-done
}

func TestHB4_WorkerPool_ActiveWorkers(t *testing.T) {
	wp := &WorkerPool{
		taskCh:  make(chan func(), 10),
		workers: 1,
	}
	if wp.ActiveWorkers() != 0 {
		t.Error("no tasks submitted yet, should be 0 active")
	}
}

func TestHB4_WorkerPool_TotalSubmitted(t *testing.T) {
	wp := &WorkerPool{
		taskCh:  make(chan func(), 10),
		workers: 1,
	}
	go wp.worker()
	wp.Submit(func() {})
	time.Sleep(50 * time.Millisecond)
	if wp.TotalSubmitted() != 1 {
		t.Errorf("expected 1 total submitted, got %d", wp.TotalSubmitted())
	}
}

func TestHB4_BufferPool_GetPut(t *testing.T) {
	buf := GetBuffer()
	if buf == nil {
		t.Fatal("GetBuffer returned nil")
	}
	buf.WriteString("hello")
	PutBuffer(buf)
	if buf.Len() != 0 {
		t.Error("PutBuffer should reset the buffer")
	}
}

func TestHB4_JsonEncodePool(t *testing.T) {
	data := map[string]string{"key": "value"}
	b, err := jsonEncodePool(data)
	if err != nil {
		t.Fatalf("jsonEncodePool failed: %v", err)
	}
	if len(b) == 0 {
		t.Error("expected non-empty bytes")
	}
	var parsed map[string]string
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if parsed["key"] != "value" {
		t.Error("key mismatch after encode/decode")
	}
}

func TestHB4_GetRouteTableCount_Nil(t *testing.T) {
	orig := routeTable
	routeTable = nil
	defer func() { routeTable = orig }()
	if getRouteTableCount() != 0 {
		t.Error("nil route table should return 0")
	}
}

func TestHB4_GetSSEClientCount_Nil(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()
	if getSSEClientCount() != 0 {
		t.Error("nil event bus should return 0")
	}
}

// ============================================================
// federation.go tests
// ============================================================

func TestHB4_FederationManager_IsEnabled_Default(t *testing.T) {
	f := &FederationManager{enabled: false}
	if f.IsEnabled() {
		t.Error("should be disabled by default")
	}
}

func TestHB4_FederationManager_IsRelayEnabled_Default(t *testing.T) {
	f := &FederationManager{relayEnabled: false}
	if f.IsRelayEnabled() {
		t.Error("relay should be disabled by default")
	}
}

func TestHB4_FederationManager_GetTrustPool_Empty(t *testing.T) {
	f := &FederationManager{
		trustPool:   TrustPool{},
		localPeers:  make(map[string]*NodeInfo),
	}
	pool := f.GetTrustPool()
	if len(pool.Nodes) != 0 {
		t.Error("empty pool should have 0 nodes")
	}
}

func TestHB4_FederationManager_GetActiveNodes_Empty(t *testing.T) {
	f := &FederationManager{
		trustPool:   TrustPool{},
		localPeers:  make(map[string]*NodeInfo),
	}
	active := f.GetActiveNodes()
	if len(active) != 0 {
		t.Error("empty pool should have 0 active nodes")
	}
}

func TestHB4_FederationManager_GetActiveNodes_WithActive(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{
			Nodes: []NodeInfo{
				{NodeID: "n1", Status: "active"},
				{NodeID: "n2", Status: "inactive"},
			},
		},
		localPeers: make(map[string]*NodeInfo),
	}
	active := f.GetActiveNodes()
	if len(active) != 1 {
		t.Errorf("expected 1 active node, got %d", len(active))
	}
}

func TestHB4_FederationManager_GetNode_Found(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{
			Nodes: []NodeInfo{{NodeID: "n1", Status: "active"}},
		},
		localPeers: make(map[string]*NodeInfo),
	}
	n, ok := f.GetNode("n1")
	if !ok || n.NodeID != "n1" {
		t.Error("should find node n1")
	}
}

func TestHB4_FederationManager_GetNode_NotFound(t *testing.T) {
	f := &FederationManager{
		trustPool:  TrustPool{},
		localPeers: make(map[string]*NodeInfo),
	}
	_, ok := f.GetNode("nonexistent")
	if ok {
		t.Error("should not find nonexistent node")
	}
}

func TestHB4_FederationManager_UpdateTrustPool_Newer(t *testing.T) {
	dir := t.TempDir()
	f := &FederationManager{
		trustPool:   TrustPool{Version: 1},
		localPeers:  make(map[string]*NodeInfo),
		dataDir:     dir,
		stopCh:      make(chan struct{}),
	}
	f.UpdateTrustPool(TrustPool{Version: 2, Nodes: []NodeInfo{{NodeID: "n1"}}})
	if f.trustPool.Version != 2 {
		t.Error("should update to newer version")
	}
}

func TestHB4_FederationManager_UpdateTrustPool_Older(t *testing.T) {
	f := &FederationManager{
		trustPool:   TrustPool{Version: 5},
		localPeers:  make(map[string]*NodeInfo),
	}
	f.UpdateTrustPool(TrustPool{Version: 3})
	if f.trustPool.Version != 5 {
		t.Error("should not downgrade")
	}
}

func TestHB4_FederationManager_RemoveNode(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{
			Nodes: []NodeInfo{{NodeID: "n1"}, {NodeID: "n2"}},
		},
		localPeers: map[string]*NodeInfo{
			"n3": {NodeID: "n3"},
		},
	}
	f.RemoveNode("n1")
	if len(f.trustPool.Nodes) != 1 {
		t.Errorf("expected 1 node after removal, got %d", len(f.trustPool.Nodes))
	}
}

func TestHB4_FederationManager_MergePeerHints_Empty(t *testing.T) {
	f := &FederationManager{
		discoveryHints: make(map[string][]string),
	}
	f.MergePeerHints(nil)
	if len(f.discoveryHints) != 0 {
		t.Error("nil hints should not change state")
	}
}

func TestHB4_FederationManager_MergePeerHints_New(t *testing.T) {
	f := &FederationManager{
		discoveryHints: make(map[string][]string),
	}
	hints := []PeerHint{
		{NodeID: "n1", Addresses: []string{"https://n1.example.com"}},
	}
	f.MergePeerHints(hints)
	if len(f.discoveryHints["n1"]) != 1 {
		t.Error("should record new hint")
	}
}

func TestHB4_FederationManager_MergePeerHints_Duplicate(t *testing.T) {
	f := &FederationManager{
		discoveryHints: map[string][]string{
			"n1": {"https://old.example.com"},
		},
	}
	hints := []PeerHint{
		{NodeID: "n1", Addresses: []string{"https://new.example.com"}},
	}
	f.MergePeerHints(hints)
	if f.discoveryHints["n1"][0] != "https://old.example.com" {
		t.Error("existing hints should be preserved (first-known wins)")
	}
}

func TestHB4_FederationManager_HintAddresses_NotFound(t *testing.T) {
	f := &FederationManager{
		discoveryHints: make(map[string][]string),
	}
	if len(f.HintAddresses("n1")) != 0 {
		t.Error("unknown node should have no hints")
	}
}

func TestHB4_FederationManager_FindProvidersForModel(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{
			Nodes: []NodeInfo{
				{NodeID: "n1", Status: "active", SharedModels: []string{"gpt-4", "gpt-3.5"}},
				{NodeID: "n2", Status: "active", SharedModels: []string{"claude-3"}},
				{NodeID: "n3", Status: "inactive", SharedModels: []string{"gpt-4"}},
			},
		},
		localPeers: make(map[string]*NodeInfo),
	}
	result := f.FindProvidersForModel("gpt-4")
	if len(result) != 1 {
		t.Errorf("expected 1 active provider for gpt-4, got %d", len(result))
	}
}

func TestHB4_FederationManager_AllKnownEndpoints(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{
			Nodes: []NodeInfo{
				{NodeID: "n1", Endpoint: "https://n1.com"},
				{NodeID: "n2", Endpoint: ""},
			},
		},
		localPeers: map[string]*NodeInfo{
			"n3": {NodeID: "n3", Endpoint: "https://n3.com"},
		},
	}
	endpoints := f.allKnownEndpoints()
	if len(endpoints) != 2 {
		t.Errorf("expected 2 endpoints, got %d", len(endpoints))
	}
}

func TestHB4_FederationManager_HasActivePeers_Empty(t *testing.T) {
	f := &FederationManager{
		trustPool:  TrustPool{},
		localPeers: make(map[string]*NodeInfo),
	}
	if f.hasActivePeers() {
		t.Error("empty pool should have no active peers")
	}
}

func TestHB4_FederationManager_HasActivePeers_Active(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{
			Nodes: []NodeInfo{{NodeID: "n1", Status: "active", Endpoint: "https://n1.com"}},
		},
		localPeers: make(map[string]*NodeInfo),
	}
	if !f.hasActivePeers() {
		t.Error("should find active peer")
	}
}

func TestHB4_FederationManager_SetEnabled(t *testing.T) {
	f := &FederationManager{
		localPeers:     make(map[string]*NodeInfo),
		discoveryHints: make(map[string][]string),
		stopCh:         make(chan struct{}),
	}
	f.enabled = true
	if !f.IsEnabled() {
		t.Error("should be enabled")
	}
	f.enabled = false
	if f.IsEnabled() {
		t.Error("should be disabled")
	}
	// Idempotent same-state
	f.enabled = false
	if f.IsEnabled() {
		t.Error("should still be disabled")
	}
}

// ============================================================
// network.go tests
// ============================================================

func TestHB4_FirstAddress_Empty(t *testing.T) {
	if firstAddress(nil) != "" {
		t.Error("nil should return empty")
	}
	if firstAddress([]string{}) != "" {
		t.Error("empty slice should return empty")
	}
}

func TestHB4_FirstAddress_NonEmpty(t *testing.T) {
	result := firstAddress([]string{"https://a.com", "https://b.com"})
	if result != "https://a.com" {
		t.Errorf("expected https://a.com, got %s", result)
	}
}

func TestHB4_HandleNetworkStatus_Nil(t *testing.T) {
	setupTestEnv(t)
	netMgr = nil
	req := httptest.NewRequest("GET", "/api/network/status", nil)
	w := httptest.NewRecorder()
	handleNetworkStatus(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleNetworkStats_Nil(t *testing.T) {
	setupTestEnv(t)
	netMgr = nil
	req := httptest.NewRequest("GET", "/api/network/stats", nil)
	w := httptest.NewRecorder()
	handleNetworkStats(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleNetworkConsent_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/network/consent", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleNetworkConsent(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB4_HandleNetworkDisclaimer(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/network/disclaimer", nil)
	w := httptest.NewRecorder()
	handleNetworkDisclaimer(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp DisclaimerResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Title == "" {
		t.Error("disclaimer should have a title")
	}
}

func TestHB4_HandleNetworkPeers_NotSharedMode(t *testing.T) {
	setupTestEnv(t)
	initNetworkManager(t.TempDir())
	req := httptest.NewRequest("GET", "/api/network/peers", nil)
	w := httptest.NewRecorder()
	handleNetworkPeers(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleNetworkToggle_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/network/toggle", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleNetworkToggle(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB4_HandleNetworkConfigUpdate_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/network/config", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleNetworkConfigUpdate(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB4_HandleNetworkEnable_Nil(t *testing.T) {
	setupTestEnv(t)
	netMgr = nil
	req := httptest.NewRequest("POST", "/api/network/enable", nil)
	w := httptest.NewRecorder()
	handleNetworkEnable(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ============================================================
// browser_login.go tests
// ============================================================

func TestHB4_SanitizeProfileName_Valid(t *testing.T) {
	if sanitizeProfileName("my-provider") != "my-provider" {
		t.Error("valid name should pass through")
	}
}

func TestHB4_SanitizeProfileName_Special(t *testing.T) {
	result := sanitizeProfileName("my provider!@#")
	if strings.ContainsAny(result, "!@# ") {
		t.Errorf("special chars should be replaced, got: %s", result)
	}
}

func TestHB4_SanitizeProfileName_Empty(t *testing.T) {
	if sanitizeProfileName("") != "default" {
		t.Error("empty name should return 'default'")
	}
}

func TestHB4_SanitizeProfileName_Unicode(t *testing.T) {
	result := sanitizeProfileName("中文")
	if result == "中文" {
		t.Error("unicode should be replaced with underscores")
	}
}

func TestHB4_BrowserLaunchFlags_Basic(t *testing.T) {
	flags := browserLaunchFlags("/tmp/ud", "", "")
	if flags["headless"] != "new" {
		t.Error("headless should be 'new'")
	}
	if flags["disable-gpu"] != true {
		t.Error("disable-gpu should be true")
	}
}

func TestHB4_BrowserLaunchFlags_WithProxy(t *testing.T) {
	flags := browserLaunchFlags("/tmp/ud", "socks5://127.0.0.1:1080", "")
	if flags["proxy-server"] != "socks5://127.0.0.1:1080" {
		t.Error("proxy-server should be set")
	}
}

func TestHB4_GetSession_NotFound(t *testing.T) {
	browserSessionsMu.Lock()
	delete(browserSessions, "nonexistent")
	browserSessionsMu.Unlock()
	_, ok := getSession("nonexistent")
	if ok {
		t.Error("should not find nonexistent session")
	}
}

func TestHB4_HandleBrowserLoginStatus_NoSession(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/providers/nonexistent/browser-login/status", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleBrowserLoginStatus(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHB4_HandleBrowserLoginLogin_NoSession(t *testing.T) {
	setupTestEnv(t)
	body := `{"email":"test@test.com","password":"pass"}`
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/browser-login/login", strings.NewReader(body))
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleBrowserLoginLogin(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHB4_HandleBrowserLoginFinish_NoSession(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/browser-login/finish", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleBrowserLoginFinish(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHB4_HandleBrowserLoginCancel_NoSession(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/browser-login/cancel", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleBrowserLoginCancel(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200 (idempotent), got %d", w.Code)
	}
}

func TestHB4_HandleBrowserLoginAction_NoSession(t *testing.T) {
	setupTestEnv(t)
	body := `{"action":"screenshot"}`
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/browser-login/action", strings.NewReader(body))
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleBrowserLoginAction(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ============================================================
// Integration-style handler tests
// ============================================================

func TestHB4_HandleUsageProviders_CustomDays(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/usage/providers?days=7", nil)
	w := httptest.NewRecorder()
	handleUsageProviders(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleUsageRecords_CustomLimit(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/usage/records?limit=50", nil)
	w := httptest.NewRecorder()
	handleUsageRecords(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleRequestLogs_CustomLimit(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/request-logs?limit=50", nil)
	w := httptest.NewRecorder()
	handleRequestLogs(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleGetProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/providers/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleGetProvider(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHB4_HandleDeleteProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("DELETE", "/api/providers/nonexistent", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleDeleteProvider(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHB4_HandleUpdateProvider_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	p := makeProvider("test-p", "Test", makeModelDef("gpt-4"), 1, true)
	pm.Add(p)
	req := httptest.NewRequest("PUT", "/api/providers/test-p", strings.NewReader("bad"))
	req.SetPathValue("id", "test-p")
	w := httptest.NewRecorder()
	handleUpdateProvider(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB4_HandleTestProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/test", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleTestProvider(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHB4_HandleTestProvider_NoAPIKey(t *testing.T) {
	setupTestEnv(t)
	p := makeProvider("test-p", "Test", makeModelDef("gpt-4"), 1, true)
	p.APIKey = ""
	pm.Add(p)
	req := httptest.NewRequest("POST", "/api/providers/test-p/test", nil)
	req.SetPathValue("id", "test-p")
	w := httptest.NewRecorder()
	handleTestProvider(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleTestAllKeys_NotFound(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/providers/nonexistent/test-all", nil)
	req.SetPathValue("id", "nonexistent")
	w := httptest.NewRecorder()
	handleTestAllKeys(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHB4_HandleRoutingAdvice_WithModel(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("GET", "/api/routing/advice/gpt-4", nil)
	req.SetPathValue("model", "gpt-4")
	w := httptest.NewRecorder()
	handleRoutingAdvice(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHB4_HandleNetworkAddPeer_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/network/peers", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleNetworkAddPeer(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB4_ResolvePeerNodeID_InvalidScheme(t *testing.T) {
	_, err := resolvePeerNodeID("ftp://bad.com")
	if err == nil {
		t.Error("should reject non-http(s) scheme")
	}
}

func TestHB4_ResolvePublicEndpoint_FederationEndpoint(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("federation_endpoint", "https://fed.example.com")
	result := resolvePublicEndpoint("")
	if result != "https://fed.example.com" {
		t.Errorf("expected federation_endpoint, got %s", result)
	}
}

func TestHB4_ResolvePublicEndpoint_PublicDomain(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("federation_endpoint", "")
	cfg.Set("public_domain", "https://my.domain.com")
	result := resolvePublicEndpoint("")
	if result != "https://my.domain.com" {
		t.Errorf("expected public_domain, got %s", result)
	}
}

func TestHB4_ResolvePublicEndpoint_Host(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("federation_endpoint", "")
	cfg.Set("public_domain", "")
	result := resolvePublicEndpoint("myhost:8000")
	if result != "https://myhost:8000" {
		t.Errorf("expected https://myhost:8000, got %s", result)
	}
}
