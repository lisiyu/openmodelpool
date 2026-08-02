package main

import (
	"encoding/json"
	"testing"
)

// ============================================================
// ChatMessage JSON deserialization tests
// ============================================================

func TestChatMessage_UnmarshalJSON_StringContent(t *testing.T) {
	data := []byte(`{"role":"user","content":"Hello world"}`)
	var msg ChatMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if msg.Role != "user" {
		t.Errorf("Role = %q, want user", msg.Role)
	}
	if msg.Content != "Hello world" {
		t.Errorf("Content = %q, want 'Hello world'", msg.Content)
	}
}

func TestChatMessage_UnmarshalJSON_ArrayContent(t *testing.T) {
	data := []byte(`{"role":"assistant","content":[{"type":"text","text":"First part"},{"type":"text","text":"Second part"}]}`)
	var msg ChatMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", msg.Role)
	}
	if msg.Content != "First part\nSecond part" {
		t.Errorf("Content = %q, want 'First part\\nSecond part'", msg.Content)
	}
}

func TestChatMessage_UnmarshalJSON_ArrayContentWithNonText(t *testing.T) {
	data := []byte(`{"role":"user","content":[{"type":"image_url","image_url":"http://example.com/img.png"},{"type":"text","text":"Describe this image"}]}`)
	var msg ChatMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if msg.Content != "Describe this image" {
		t.Errorf("Content = %q, want 'Describe this image'", msg.Content)
	}
}

func TestChatMessage_UnmarshalJSON_EmptyArray(t *testing.T) {
	data := []byte(`{"role":"user","content":[]}`)
	var msg ChatMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if msg.Content != "" {
		t.Errorf("Content = %q, want empty", msg.Content)
	}
}

// ============================================================
// extractContentText tests
// ============================================================

func TestExtractContentText_String(t *testing.T) {
	raw := json.RawMessage(`"simple string"`)
	got := extractContentText(raw)
	if got != "simple string" {
		t.Errorf("extractContentText = %q, want 'simple string'", got)
	}
}

func TestExtractContentText_Array(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"hello"},{"type":"text","text":"world"}]`)
	got := extractContentText(raw)
	if got != "hello\nworld" {
		t.Errorf("extractContentText = %q, want 'hello\\nworld'", got)
	}
}

func TestExtractContentText_Invalid(t *testing.T) {
	raw := json.RawMessage(`["not","an","object","array"]`)
	got := extractContentText(raw)
	if got != "" {
		t.Errorf("extractContentText = %q, want empty", got)
	}
}

// ============================================================
// Provider.Safe() tests
// ============================================================

func TestProvider_Safe_MasksAPIKey(t *testing.T) {
	p := Provider{
		ID:     "test-provider",
		APIKey: "sk-this-is-a-very-long-api-key-12345",
	}
	safe := p.Safe()
	if safe.APIKey == p.APIKey {
		t.Error("APIKey should be masked in Safe()")
	}
	if safe.APIKey == "" {
		t.Error("masked APIKey should not be empty")
	}
	if len(safe.APIKey) < 7 {
		t.Errorf("masked APIKey too short: %q", safe.APIKey)
	}
}

func TestProvider_Safe_ShortAPIKey(t *testing.T) {
	p := Provider{
		ID:     "test-provider",
		APIKey: "short",
	}
	safe := p.Safe()
	if safe.APIKey != "***" {
		t.Errorf("short APIKey should be '***', got %q", safe.APIKey)
	}
}

func TestProvider_Safe_EmptyAPIKey(t *testing.T) {
	p := Provider{
		ID:     "test-provider",
		APIKey: "",
	}
	safe := p.Safe()
	if safe.APIKey != "" {
		t.Errorf("empty APIKey should remain empty, got %q", safe.APIKey)
	}
}

func TestProvider_Safe_MasksAPIKeysArray(t *testing.T) {
	p := Provider{
		ID: "test-provider",
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-long-key-one-abcdefghij"},
			{ID: "k2", Key: "abc"},
		},
	}
	safe := p.Safe()
	if len(safe.APIKeys) != 2 {
		t.Fatalf("expected 2 API keys, got %d", len(safe.APIKeys))
	}
	if safe.APIKeys[0].Key == "sk-long-key-one-abcdefghij" {
		t.Error("first key should be masked")
	}
	if safe.APIKeys[1].Key != "***" {
		t.Errorf("short second key should be '***', got %q", safe.APIKeys[1].Key)
	}
}

func TestProvider_Safe_MasksVMessProxy(t *testing.T) {
	p := Provider{
		ID:    "test-provider",
		Proxy: "vmess://some-long-uuid-value",
	}
	safe := p.Safe()
	if safe.Proxy != "vmess://***" {
		t.Errorf("vmess proxy should be masked, got %q", safe.Proxy)
	}
}

func TestProvider_Safe_PreservesOtherFields(t *testing.T) {
	p := Provider{
		ID:   "test-provider",
		Name: "My Provider",
		Type: "openai_compatible",
	}
	safe := p.Safe()
	if safe.ID != "test-provider" {
		t.Errorf("ID should be preserved, got %q", safe.ID)
	}
	if safe.Name != "My Provider" {
		t.Errorf("Name should be preserved, got %q", safe.Name)
	}
}

// ============================================================
// ProviderAccessControl UnmarshalJSON tests
// ============================================================

func TestProviderAccessControl_UnmarshalJSON_Migration(t *testing.T) {
	data := []byte(`{"allow_public":true}`)
	var ac ProviderAccessControl
	if err := json.Unmarshal(data, &ac); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !ac.ShareToPool {
		t.Error("ShareToPool should be true after migration from allow_public")
	}
}

func TestProviderAccessControl_UnmarshalJSON_NoOverride(t *testing.T) {
	data := []byte(`{"share_to_pool":true,"allow_public":false}`)
	var ac ProviderAccessControl
	if err := json.Unmarshal(data, &ac); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !ac.ShareToPool {
		t.Error("ShareToPool should remain true when already set")
	}
}

func TestProviderAccessControl_UnmarshalJSON_Defaults(t *testing.T) {
	data := []byte(`{}`)
	var ac ProviderAccessControl
	if err := json.Unmarshal(data, &ac); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if ac.ShareToPool {
		t.Error("ShareToPool should default to false")
	}
}

// ============================================================
// ChatRequest UnmarshalJSON tests
// ============================================================

func TestChatRequest_UnmarshalJSON_ExtraFields(t *testing.T) {
	data := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":false,"custom_field":"custom_value"}`)
	var req ChatRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if req.Model != "gpt-4" {
		t.Errorf("Model = %q, want gpt-4", req.Model)
	}
	if req.Stream {
		t.Error("Stream should be false")
	}
	if len(req.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(req.Messages))
	}
	if req.Extra == nil {
		t.Fatal("Extra should not be nil")
	}
	if req.Extra["custom_field"] != "custom_value" {
		t.Errorf("extra custom_field = %v, want 'custom_value'", req.Extra["custom_field"])
	}
}

func TestChatRequest_UnmarshalJSON_NoExtraFields(t *testing.T) {
	data := []byte(`{"model":"gpt-4","messages":[],"stream":true}`)
	var req ChatRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if req.Extra == nil {
		t.Error("Extra should still be initialized")
	}
	if len(req.Extra) != 0 {
		t.Errorf("Extra should be empty, got %v", req.Extra)
	}
}

// ============================================================
// Usage / Choice / Msg marshaling tests
// ============================================================

func TestUsage_MarshalJSON(t *testing.T) {
	u := Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded map[string]int
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded["prompt_tokens"] != 100 {
		t.Errorf("prompt_tokens = %d, want 100", decoded["prompt_tokens"])
	}
}

func TestChatResponse_JSON(t *testing.T) {
	content := "Hello!"
	finish := "stop"
	resp := ChatResponse{
		ID:      "chat-123",
		Object:  "chat.completion",
		Created: 1720000000,
		Model:   "gpt-4",
		Choices: []Choice{
			{
				Index:        0,
				Message:      &Msg{Role: "assistant", Content: &content},
				FinishReason: &finish,
			},
		},
		Usage: &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded ChatResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.ID != "chat-123" {
		t.Errorf("ID = %q, want chat-123", decoded.ID)
	}
	if decoded.Choices[0].Index != 0 {
		t.Errorf("Index = %d, want 0", decoded.Choices[0].Index)
	}
	if *decoded.Choices[0].Message.Content != "Hello!" {
		t.Errorf("Content = %q, want Hello!", *decoded.Choices[0].Message.Content)
	}
	if *decoded.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", *decoded.Choices[0].FinishReason)
	}
}

func TestErrorResponse_JSON(t *testing.T) {
	resp := ErrorResponse{
		Error: ErrorDetail{
			Message: "Something went wrong",
			Type:    "server_error",
			Code:    "500",
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded ErrorResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.Error.Message != "Something went wrong" {
		t.Errorf("Message mismatch: got %q", decoded.Error.Message)
	}
}

func TestModelListResponse_JSON(t *testing.T) {
	resp := ModelListResponse{
		Object: "list",
		Data: []ModelInfo{
			{ID: "gpt-4", Object: "model", Created: 1720000000, OwnedBy: "openai"},
			{ID: "gpt-3.5", Object: "model", Created: 1700000000, OwnedBy: "openai"},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded ModelListResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(decoded.Data) != 2 {
		t.Errorf("expected 2 models, got %d", len(decoded.Data))
	}
}

// ============================================================
// APIKeyConfig type tests
// ============================================================

func TestAPIKeyConfig_Fields(t *testing.T) {
	config := APIKeyConfig{
		ID:          "key-1",
		Alias:       "My Key",
		Quota:       1000000,
		QuotaDaily:  100000,
		QuotaMonthly: 3000000,
		AccessControl: "private",
		Enabled:     true,
		Priority:    10,
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded APIKeyConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.ID != "key-1" {
		t.Errorf("ID mismatch: got %q", decoded.ID)
	}
	if decoded.Quota != 1000000 {
		t.Errorf("Quota mismatch: got %d", decoded.Quota)
	}
	if decoded.AccessControl != "private" {
		t.Errorf("AccessControl mismatch: got %q", decoded.AccessControl)
	}
}
