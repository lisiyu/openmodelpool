package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ============================================================
// discovery.go: parseDurationSecs
// ============================================================

func TestParseDurationSecs_ValidPositive(t *testing.T) {
	got := parseDurationSecs("30", 10)
	if got != 30*time.Second {
		t.Errorf("parseDurationSecs(\"30\", 10) = %v, want 30s", got)
	}
}

func TestParseDurationSecs_Zero(t *testing.T) {
	got := parseDurationSecs("0", 10)
	if got != 10*time.Second {
		t.Errorf("parseDurationSecs(\"0\", 10) = %v, want 10s (default)", got)
	}
}

func TestParseDurationSecs_Negative(t *testing.T) {
	got := parseDurationSecs("-5", 10)
	if got != 10*time.Second {
		t.Errorf("parseDurationSecs(\"-5\", 10) = %v, want 10s (default)", got)
	}
}

func TestParseDurationSecs_InvalidString(t *testing.T) {
	got := parseDurationSecs("abc", 10)
	if got != 10*time.Second {
		t.Errorf("parseDurationSecs(\"abc\", 10) = %v, want 10s (default)", got)
	}
}

func TestParseDurationSecs_EmptyString(t *testing.T) {
	got := parseDurationSecs("", 60)
	if got != 60*time.Second {
		t.Errorf("parseDurationSecs(\"\", 60) = %v, want 60s (default)", got)
	}
}

// ============================================================
// vmess.go: ParseVMessLink
// ============================================================

func TestParseVMessLink_ValidLink(t *testing.T) {
	config := map[string]any{
		"add":  "server.example.com",
		"port": "443",
		"id":   "a3482e88-686a-4a58-8126-99c9df64b7bf",
		"aid":  "0",
		"net":  "ws",
		"type": "none",
		"tls":  "tls",
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"add":"%s","port":"%s","id":"%s","aid":"%s","net":"%s","type":"%s","tls":"%s"}`,
		config["add"], config["port"], config["id"], config["aid"], config["net"], config["type"], config["tls"])))
	link := "vmess://" + b64

	parsed, err := ParseVMessLink(link)
	if err != nil {
		t.Fatalf("ParseVMessLink error: %v", err)
	}
	if parsed.Add != "server.example.com" {
		t.Errorf("Add = %q, want server.example.com", parsed.Add)
	}
	if parsed.Port != "443" {
		t.Errorf("Port = %q, want 443", parsed.Port)
	}
	if parsed.ID != "a3482e88-686a-4a58-8126-99c9df64b7bf" {
		t.Errorf("ID = %q, want UUID", parsed.ID)
	}
}

func TestParseVMessLink_MissingPrefix(t *testing.T) {
	_, err := ParseVMessLink("https://example.com")
	if err == nil {
		t.Error("expected error for non-vmess link")
	}
	if !strings.Contains(err.Error(), "must start with vmess://") {
		t.Errorf("error = %q, want prefix error", err.Error())
	}
}

func TestParseVMessLink_InvalidBase64(t *testing.T) {
	_, err := ParseVMessLink("vmess://!!!invalid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestParseVMessLink_MissingRequiredFields(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte(`{"port":"443","id":"uuid"}`))
	_, err := ParseVMessLink("vmess://" + b64)
	if err == nil {
		t.Error("expected error for missing required fields")
	}
	if !strings.Contains(err.Error(), "missing required fields") {
		t.Errorf("error = %q, want missing fields error", err.Error())
	}
}

func TestParseVMessLink_EmptyString(t *testing.T) {
	_, err := ParseVMessLink("")
	if err == nil {
		t.Error("expected error for empty string")
	}
}

func TestParseVMessLink_WhitespaceTrimmed(t *testing.T) {
	config := `{"add":"s.example.com","port":"443","id":"uuid-1234","aid":"0","net":"tcp","type":"none","tls":""}`
	b64 := base64.StdEncoding.EncodeToString([]byte(config))
	link := "  vmess://" + b64 + "  "
	parsed, err := ParseVMessLink(link)
	if err != nil {
		t.Fatalf("ParseVMessLink with whitespace: %v", err)
	}
	if parsed.Add != "s.example.com" {
		t.Errorf("Add = %q, want s.example.com", parsed.Add)
	}
}

// ============================================================
// message.go: generateMsgID
// ============================================================

func TestGenerateMsgID_ProducesUniqueIDs(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := generateMsgID()
		if err != nil {
			t.Fatalf("generateMsgID error: %v", err)
		}
		if len(id) != 32 {
			t.Errorf("generateMsgID length = %d, want 32 (16 bytes hex)", len(id))
		}
		if ids[id] {
			t.Errorf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestGenerateMsgID_IsHex(t *testing.T) {
	id, err := generateMsgID()
	if err != nil {
		t.Fatalf("generateMsgID error: %v", err)
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("generateMsgID produced non-hex char: %c", c)
		}
	}
}

// ============================================================
// message.go: verifyMessageSignature
// ============================================================

func TestVerifyMessageSignature_EmptyInputs(t *testing.T) {
	if verifyMessageSignature("", "payload", "sig") {
		t.Error("should return false for empty pubKeyB64")
	}
	if verifyMessageSignature("key", "payload", "") {
		t.Error("should return false for empty signature")
	}
}

func TestVerifyMessageSignature_InvalidBase64PubKey(t *testing.T) {
	if verifyMessageSignature("!!!not-base64!!!", "payload", "sig") {
		t.Error("should return false for invalid base64 pubKey")
	}
}

func TestVerifyMessageSignature_InvalidBase64Signature(t *testing.T) {
	pubKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Skip("ed25519 not available")
	}
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)
	if verifyMessageSignature(pubKeyB64, "payload", "!!!not-base64!!!") {
		t.Error("should return false for invalid base64 signature")
	}
}

func TestVerifyMessageSignature_ValidSignature(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Skip("ed25519 not available")
	}
	payload := "test-payload"
	sig := ed25519.Sign(privKey, []byte(payload))
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	if !verifyMessageSignature(pubKeyB64, payload, sigB64) {
		t.Error("should return true for valid signature")
	}
}

func TestVerifyMessageSignature_WrongPayload(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Skip("ed25519 not available")
	}
	sig := ed25519.Sign(privKey, []byte("correct-payload"))
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	if verifyMessageSignature(pubKeyB64, "wrong-payload", sigB64) {
		t.Error("should return false for wrong payload")
	}
}

func TestVerifyMessageSignature_RawEd25519Bytes(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Skip("ed25519 not available")
	}
	payload := "raw-key-test"
	sig := ed25519.Sign(privKey, []byte(payload))
	pubKeyB64 := base64.StdEncoding.EncodeToString(pubKey)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	if !verifyMessageSignature(pubKeyB64, payload, sigB64) {
		t.Error("should return true for raw ed25519 public key bytes")
	}
}

// ============================================================
// types.go: ToNodeInfo
// ============================================================

func TestPeer_ToNodeInfo(t *testing.T) {
	p := Peer{
		PeerID:   "peer-1",
		NodeID:   "node-1",
		Endpoint: "https://example.com",
		PubKey:   "cHVibGlj",
		Capabilities: PeerCapabilities{
			CanRelay: true,
			CanSeed:  true,
		},
		Status:          "active",
		JoinedAt:        "2024-01-01",
		LastSeen:        "2024-06-01",
		Version:         "1.0",
		SharedModels:    []string{"gpt-4"},
		SharedProviders: []SharedProvider{{ProviderID: "p1", Platform: "openai", Models: []string{"gpt-4"}, Capacity: 80}},
		Reputation:      50,
		TokenBudget:     1000,
		TokenUsed:       200,
		InviteBy:        "inviter",
		GitHubUser:      "testuser",
		GitHubID:        12345,
	}

	ni := p.ToNodeInfo()
	if ni.NodeID != "peer-1" {
		t.Errorf("NodeID = %q, want peer-1 (uses PeerID first)", ni.NodeID)
	}
	if ni.Endpoint != "https://example.com" {
		t.Errorf("Endpoint = %q, want https://example.com", ni.Endpoint)
	}
	if !ni.SeedNode {
		t.Error("SeedNode should be derived from CanSeed capability")
	}
	if ni.Reputation != 50 {
		t.Errorf("Reputation = %d, want 50", ni.Reputation)
	}
	if ni.GitHubUser != "testuser" {
		t.Errorf("GitHubUser = %q, want testuser", ni.GitHubUser)
	}
}

func TestPeer_ToNodeInfo_FallbackToNodeID(t *testing.T) {
	p := Peer{
		NodeID:   "node-fallback",
		PeerID:   "",
		Endpoint: "https://fallback.example.com",
	}
	ni := p.ToNodeInfo()
	if ni.NodeID != "node-fallback" {
		t.Errorf("NodeID = %q, want node-fallback (falls back to NodeID)", ni.NodeID)
	}
}

// ============================================================
// types.go: NodeInfoToPeer
// ============================================================

func TestNodeInfoToPeer(t *testing.T) {
	ni := NodeInfo{
		NodeID:          "node-1",
		Endpoint:        "https://example.com",
		PubKey:          "cHVibGlj",
		SeedNode:        true,
		Status:          "active",
		JoinedAt:        "2024-01-01",
		LastSeen:        "2024-06-01",
		Version:         "1.0",
		SharedModels:    []string{"gpt-4"},
		SharedProviders: []SharedProvider{{ProviderID: "p1"}},
		Reputation:      50,
		TokenBudget:     1000,
		TokenUsed:       200,
		InviteBy:        "inviter",
		GitHubUser:      "testuser",
		GitHubID:        12345,
	}

	p := NodeInfoToPeer(ni)
	if p.PeerID != "node-1" {
		t.Errorf("PeerID = %q, want node-1", p.PeerID)
	}
	if p.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want node-1", p.NodeID)
	}
	if !p.Capabilities.CanSeed {
		t.Error("CanSeed should be derived from SeedNode")
	}
	if !p.Capabilities.CanRelay {
		t.Error("CanRelay should default to true")
	}
	if p.Reputation != 50 {
		t.Errorf("Reputation = %d, want 50", p.Reputation)
	}
}

// ============================================================
// relay.go: rateLimitCheck
// ============================================================

func TestRateLimitCheck_AllowsUnderLimit(t *testing.T) {
	rateLimitMu.Lock()
	rateLimitMap = make(map[string]*rateLimitEntry)
	rateLimitMu.Unlock()

	nodeID := "test-node-allow"
	for i := 0; i < rateLimitMax; i++ {
		if !rateLimitCheck(nodeID) {
			t.Fatalf("request %d should be allowed (under limit)", i+1)
		}
	}
}

func TestRateLimitCheck_BlocksOverLimit(t *testing.T) {
	rateLimitMu.Lock()
	rateLimitMap = make(map[string]*rateLimitEntry)
	rateLimitMu.Unlock()

	nodeID := "test-node-block"
	for i := 0; i < rateLimitMax; i++ {
		rateLimitCheck(nodeID)
	}
	if rateLimitCheck(nodeID) {
		t.Error("request over limit should be blocked")
	}
}

func TestRateLimitCheck_DifferentNodesIndependent(t *testing.T) {
	rateLimitMu.Lock()
	rateLimitMap = make(map[string]*rateLimitEntry)
	rateLimitMu.Unlock()

	nodeA := "node-a"
	nodeB := "node-b"
	for i := 0; i < rateLimitMax; i++ {
		rateLimitCheck(nodeA)
	}
	if !rateLimitCheck(nodeB) {
		t.Error("different nodes should have independent rate limits")
	}
}

func TestRateLimitCheck_WindowReset(t *testing.T) {
	rateLimitMu.Lock()
	rateLimitMap = make(map[string]*rateLimitEntry)
	rateLimitMu.Unlock()

	nodeID := "test-node-reset"
	for i := 0; i < rateLimitMax; i++ {
		rateLimitCheck(nodeID)
	}
	rateLimitMu.Lock()
	if e, ok := rateLimitMap[nodeID]; ok {
		e.windowStart = time.Now().Add(-2 * rateLimitWin)
	}
	rateLimitMu.Unlock()

	if !rateLimitCheck(nodeID) {
		t.Error("should allow after window expires")
	}
}

// ============================================================
// client.go: mustParseURL
// ============================================================

func TestMustParseURL_Valid(t *testing.T) {
	u := mustParseURL("https://example.com/path")
	if u == nil {
		t.Fatal("mustParseURL returned nil for valid URL")
	}
	if u.Host != "example.com" {
		t.Errorf("Host = %q, want example.com", u.Host)
	}
}

func TestMustParseURL_Invalid(t *testing.T) {
	u := mustParseURL("://invalid")
	t.Logf("mustParseURL on invalid input returned: %v", u)
}

// ============================================================
// client.go: buildOpenAIBody
// ============================================================

func TestBuildOpenAIBody_Basic(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: "hello"}}
	body := buildOpenAIBody("gpt-4", msgs, true, nil)
	if body["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	if body["messages"] == nil {
		t.Error("messages should not be nil")
	}
}

func TestBuildOpenAIBody_WithExtra(t *testing.T) {
	extra := map[string]any{"temperature": 0.7, "max_tokens": 100}
	body := buildOpenAIBody("gpt-4", nil, false, extra)
	if body["temperature"] != 0.7 {
		t.Errorf("temperature = %v, want 0.7", body["temperature"])
	}
	if body["max_tokens"] != 100 {
		t.Errorf("max_tokens = %v, want 100", body["max_tokens"])
	}
}

// ============================================================
// client.go: setOpenAIHeaders
// ============================================================

func TestSetOpenAIHeaders_WithKey(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	setOpenAIHeaders(req, "sk-test-key")
	if req.Header.Get("Authorization") != "Bearer sk-test-key" {
		t.Errorf("Authorization = %q, want Bearer sk-test-key", req.Header.Get("Authorization"))
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", req.Header.Get("Content-Type"))
	}
}

func TestSetOpenAIHeaders_EmptyKey(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	setOpenAIHeaders(req, "")
	if req.Header.Get("Authorization") != "" {
		t.Error("Authorization should be empty for empty key")
	}
}

func TestSetOpenAIHeaders_FreeAnonymous(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	setOpenAIHeaders(req, "free-anonymous")
	if req.Header.Get("Authorization") != "" {
		t.Error("Authorization should be skipped for free-anonymous key")
	}
}

// ============================================================
// client.go: siderBuildHeaders
// ============================================================

func TestSiderBuildHeaders(t *testing.T) {
	h := siderBuildHeaders("test-token")
	if h.Get("Authorization") != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", h.Get("Authorization"))
	}
	if h.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", h.Get("Content-Type"))
	}
	if !strings.Contains(h.Get("Cookie"), "token=Bearer%20test-token") {
		t.Errorf("Cookie should contain token, got %q", h.Get("Cookie"))
	}
}

// ============================================================
// client.go: siderBuildPayload
// ============================================================

func TestSiderBuildPayload(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "hello"},
	}
	payload := siderBuildPayload("gpt-4", msgs, false)
	if payload["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", payload["model"])
	}
	if payload["stream"] != false {
		t.Errorf("stream = %v, want false", payload["stream"])
	}
	prompt, ok := payload["prompt"].(string)
	if !ok {
		t.Fatal("prompt is not a string")
	}
	if !strings.Contains(prompt, "[System Instructions]") {
		t.Error("prompt should contain system instructions")
	}
	if !strings.Contains(prompt, "hello") {
		t.Error("prompt should contain user message")
	}
}

// ============================================================
// client.go: anthropicBuildMessages
// ============================================================

func TestAnthropicBuildMessages_SystemExtracted(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "hi"},
	}
	out, systemMsg := anthropicBuildMessages(msgs)
	if systemMsg != "You are helpful" {
		t.Errorf("systemMsg = %q, want You are helpful", systemMsg)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1 (system excluded)", len(out))
	}
	if out[0]["role"] != "user" {
		t.Errorf("out[0] role = %v, want user", out[0]["role"])
	}
}

func TestAnthropicBuildMessages_NoSystem(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hi"},
	}
	out, systemMsg := anthropicBuildMessages(msgs)
	if systemMsg != "" {
		t.Errorf("systemMsg = %q, want empty", systemMsg)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
}

func TestAnthropicBuildMessages_Alternating(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "how are you"},
	}
	out, _ := anthropicBuildMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3", len(out))
	}
	if out[1]["role"] != "assistant" {
		t.Errorf("out[1] role = %v, want assistant", out[1]["role"])
	}
}

// ============================================================
// client.go: webSessionBuildHeaders
// ============================================================

func TestWebSessionBuildHeaders_BearerMode(t *testing.T) {
	cfg := &WebSessionConfig{
		AuthMode:     "bearer",
		ExtraHeaders: map[string]string{"X-Custom": "value"},
	}
	h := webSessionBuildHeaders(cfg, "my-token")
	if h.Get("Authorization") != "Bearer my-token" {
		t.Errorf("Authorization = %q, want Bearer my-token", h.Get("Authorization"))
	}
	if h.Get("X-Custom") != "value" {
		t.Errorf("X-Custom = %q, want value", h.Get("X-Custom"))
	}
}

func TestWebSessionBuildHeaders_CookieMode(t *testing.T) {
	cfg := &WebSessionConfig{
		AuthMode:        "cookie",
		TokenCookieName: "session",
	}
	h := webSessionBuildHeaders(cfg, "my-token")
	if h.Get("Cookie") == "" {
		t.Error("Cookie header should be set in cookie mode")
	}
	if !strings.Contains(h.Get("Cookie"), "session=") {
		t.Error("Cookie should contain session cookie name")
	}
}

// ============================================================
// client.go: webSessionBuildPayload
// ============================================================

func TestWebSessionBuildPayload_Defaults(t *testing.T) {
	cfg := &WebSessionConfig{
		ExtraBody: map[string]any{"key1": "val1"},
	}
	msgs := []ChatMessage{{Role: "user", Content: "hello"}}
	payload := webSessionBuildPayload(cfg, "gpt-4", msgs, true)
	if payload["key1"] != "val1" {
		t.Errorf("extra body key1 = %v, want val1", payload["key1"])
	}
	if payload["prompt"] == nil {
		t.Error("prompt field should be set (default field name)")
	}
}

func TestWebSessionBuildPayload_CustomFields(t *testing.T) {
	cfg := &WebSessionConfig{
		PromptField: "input",
		ModelField:  "model_name",
		StreamField: "is_stream",
	}
	msgs := []ChatMessage{{Role: "user", Content: "hello"}}
	payload := webSessionBuildPayload(cfg, "gpt-4", msgs, false)
	if payload["input"] == nil {
		t.Error("custom prompt field input should be set")
	}
	if payload["model_name"] != "gpt-4" {
		t.Errorf("model_name = %v, want gpt-4", payload["model_name"])
	}
	if payload["is_stream"] != false {
		t.Errorf("is_stream = %v, want false", payload["is_stream"])
	}
}

// ============================================================
// client.go: webSessionExtractText
// ============================================================

func TestWebSessionExtractText_SimplePath(t *testing.T) {
	data := map[string]any{
		"data": map[string]any{
			"text": "hello world",
		},
	}
	got := webSessionExtractText(data, "data.text")
	if got != "hello world" {
		t.Errorf("webSessionExtractText = %q, want hello world", got)
	}
}

func TestWebSessionExtractText_DeepPath(t *testing.T) {
	data := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep-value",
			},
		},
	}
	got := webSessionExtractText(data, "a.b.c")
	if got != "deep-value" {
		t.Errorf("webSessionExtractText = %q, want deep-value", got)
	}
}

func TestWebSessionExtractText_MissingKey(t *testing.T) {
	data := map[string]any{"x": "y"}
	got := webSessionExtractText(data, "missing.key")
	if got != "" {
		t.Errorf("webSessionExtractText = %q, want empty for missing key", got)
	}
}

func TestWebSessionExtractText_NonStringLeaf(t *testing.T) {
	data := map[string]any{"count": 42}
	got := webSessionExtractText(data, "count")
	if got != "" {
		t.Errorf("webSessionExtractText = %q, want empty for non-string leaf", got)
	}
}

// ============================================================
// client.go: parseOpenAISubscription
// ============================================================

func TestParseOpenAISubscription_Valid(t *testing.T) {
	body := []byte(`{"hard_limit_usd": 120.0, "soft_limit_usd": 100.0, "access_until": 1735689600}`)
	result := parseOpenAISubscription(body)
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result["hard_limit_usd"] != 120.0 {
		t.Errorf("hard_limit_usd = %v, want 120.0", result["hard_limit_usd"])
	}
	if result["soft_limit_usd"] != 100.0 {
		t.Errorf("soft_limit_usd = %v, want 100.0", result["soft_limit_usd"])
	}
}

func TestParseOpenAISubscription_InvalidJSON(t *testing.T) {
	result := parseOpenAISubscription([]byte("not json"))
	if result != nil {
		t.Error("should return nil for invalid JSON")
	}
}

func TestParseOpenAISubscription_NoUsefulFields(t *testing.T) {
	result := parseOpenAISubscription([]byte(`{"irrelevant": true}`))
	if result != nil {
		t.Error("should return nil when no useful fields present")
	}
}

func TestParseOpenAISubscription_WithUsed(t *testing.T) {
	body := []byte(`{"hard_limit_usd": 120.0, "used": 50.5}`)
	result := parseOpenAISubscription(body)
	if result["used_usd"] != 50.5 {
		t.Errorf("used_usd = %v, want 50.5", result["used_usd"])
	}
}

// ============================================================
// client.go: parseOpenAIUsage
// ============================================================

func TestParseOpenAIUsage_Valid(t *testing.T) {
	body := []byte(`{"total_usage": 12345.67, "balance": 100.0, "remaining": 50.0}`)
	result := parseOpenAIUsage(body)
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if result["total_usage_cents"] != 12345.67 {
		t.Errorf("total_usage_cents = %v, want 12345.67", result["total_usage_cents"])
	}
	if result["balance"] != 100.0 {
		t.Errorf("balance = %v, want 100.0", result["balance"])
	}
	if result["remaining"] != 50.0 {
		t.Errorf("remaining = %v, want 50.0", result["remaining"])
	}
}

func TestParseOpenAIUsage_InvalidJSON(t *testing.T) {
	result := parseOpenAIUsage([]byte("not json"))
	if result != nil {
		t.Error("should return nil for invalid JSON")
	}
}

func TestParseOpenAIUsage_NoUsefulFields(t *testing.T) {
	result := parseOpenAIUsage([]byte(`{"irrelevant": true}`))
	if result != nil {
		t.Error("should return nil when no useful fields present")
	}
}

// ============================================================
// anthropic_api.go: extractTextFromAnthropicContent
// ============================================================

func TestExtractTextFromAnthropicContent_String(t *testing.T) {
	raw := json.RawMessage(`"hello world"`)
	got := extractTextFromAnthropicContent(raw)
	if got != "hello world" {
		t.Errorf("extractTextFromAnthropicContent = %q, want hello world", got)
	}
}

func TestExtractTextFromAnthropicContent_Array(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"first"},{"type":"text","text":"second"}]`)
	got := extractTextFromAnthropicContent(raw)
	if got != "first\nsecond" {
		t.Errorf("extractTextFromAnthropicContent = %q, want first\\nsecond", got)
	}
}

func TestExtractTextFromAnthropicContent_ArrayWithNonText(t *testing.T) {
	raw := json.RawMessage(`[{"type":"image","image_url":"http://x"},{"type":"text","text":"visible"}]`)
	got := extractTextFromAnthropicContent(raw)
	if got != "visible" {
		t.Errorf("extractTextFromAnthropicContent = %q, want visible", got)
	}
}

func TestExtractTextFromAnthropicContent_Invalid(t *testing.T) {
	raw := json.RawMessage(`42`)
	got := extractTextFromAnthropicContent(raw)
	if got != "" {
		t.Errorf("extractTextFromAnthropicContent = %q, want empty for non-string/array", got)
	}
}

// ============================================================
// platform_discovery.go: parseMarkdownForPlatforms
// ============================================================

func TestParseMarkdownForPlatforms_WithLinks(t *testing.T) {
	content := "Some text\n- https://api.example.com/v1 Some API service\nMore text"
	platforms := parseMarkdownForPlatforms(content, "test")
	if len(platforms) == 0 {
		t.Fatal("should find at least one platform")
	}
	if platforms[0].Source != "test" {
		t.Errorf("Source = %q, want test", platforms[0].Source)
	}
}

func TestParseMarkdownForPlatforms_NoLinks(t *testing.T) {
	content := "Just some plain text without any API links"
	platforms := parseMarkdownForPlatforms(content, "test")
	if len(platforms) != 0 {
		t.Errorf("should find 0 platforms, got %d", len(platforms))
	}
}

func TestParseMarkdownForPlatforms_LimitsTo20(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("- https://api%d.example.com/v1 API %d", i, i))
	}
	content := strings.Join(lines, "\n")
	platforms := parseMarkdownForPlatforms(content, "test")
	if len(platforms) > 20 {
		t.Errorf("should limit to 20 platforms, got %d", len(platforms))
	}
}

func TestParseMarkdownForPlatforms_EmptyContent(t *testing.T) {
	platforms := parseMarkdownForPlatforms("", "test")
	if len(platforms) != 0 {
		t.Errorf("should find 0 platforms in empty content, got %d", len(platforms))
	}
}

// ============================================================
// stubs.go: GetDHTStats
// ============================================================

func TestGetDHTStats(t *testing.T) {
	stats := GetDHTStats()
	if stats == nil {
		t.Fatal("GetDHTStats should not return nil")
	}
	if _, ok := stats["enabled"]; !ok {
		t.Error("stats should contain enabled key")
	}
	if _, ok := stats["total_nodes"]; !ok {
		t.Error("stats should contain total_nodes key")
	}
}

// ============================================================
// tunnel.go: newTunnelManager
// ============================================================

func TestNewTunnelManager_QuickMode(t *testing.T) {
	tm := newTunnelManager("", "", "")
	if tm == nil {
		t.Fatal("newTunnelManager should not return nil")
	}
	if tm.mode != "quick" {
		t.Errorf("mode = %q, want quick (no domain/tunnelID)", tm.mode)
	}
}

func TestNewTunnelManager_NamedMode(t *testing.T) {
	tm := newTunnelManager("example.com", "tunnel-123", "token-abc")
	if tm.mode != "named" {
		t.Errorf("mode = %q, want named", tm.mode)
	}
	if tm.domain != "example.com" {
		t.Errorf("domain = %q, want example.com", tm.domain)
	}
	if tm.tunnelID != "tunnel-123" {
		t.Errorf("tunnelID = %q, want tunnel-123", tm.tunnelID)
	}
}

// ============================================================
// update.go: platformAssetName
// ============================================================

func TestPlatformAssetName_Format(t *testing.T) {
	name := platformAssetName()
	if !strings.HasPrefix(name, "openmodelpool-") {
		t.Errorf("platformAssetName = %q, should start with openmodelpool-", name)
	}
	if !strings.Contains(name, "linux") && !strings.Contains(name, "windows") && !strings.Contains(name, "darwin") {
		t.Errorf("platformAssetName = %q, should contain OS", name)
	}
}

// ============================================================
// browser_login.go: isWindows
// ============================================================

func TestIsWindows(t *testing.T) {
	result := isWindows()
	if result {
		t.Log("isWindows() = true (running on Windows)")
	} else {
		t.Log("isWindows() = false (running on non-Windows)")
	}
}

// ============================================================
// message.go: validMsgType
// ============================================================

func TestValidMsgType_ValidTypes(t *testing.T) {
	for _, mt := range []string{"request", "collaboration", "system", "general"} {
		if !validMsgType(mt) {
			t.Errorf("validMsgType(%q) should be true", mt)
		}
	}
}

func TestValidMsgType_InvalidType(t *testing.T) {
	if validMsgType("invalid") {
		t.Error("validMsgType should return false for unknown type")
	}
}

func TestValidMsgType_Empty(t *testing.T) {
	if validMsgType("") {
		t.Error("validMsgType should return false for empty string")
	}
}
