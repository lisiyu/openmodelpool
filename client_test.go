package main

import (
	"testing"
)

// ============================================================
// Client adapter tests
// ============================================================

func TestAnthropicBuildMessages(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "How are you?"},
	}

	anthMessages, systemMsg := anthropicBuildMessages(messages)

	if systemMsg != "You are helpful" {
		t.Fatalf("system message not extracted: %s", systemMsg)
	}
	if len(anthMessages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(anthMessages))
	}
	if anthMessages[0]["role"] != "user" {
		t.Fatalf("first message should be user, got %s", anthMessages[0]["role"])
	}
	if anthMessages[1]["role"] != "assistant" {
		t.Fatalf("second message should be assistant, got %s", anthMessages[1]["role"])
	}
}

func TestAnthropicBuildMessages_NoSystem(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
	}

	anthMessages, systemMsg := anthropicBuildMessages(messages)

	if systemMsg != "" {
		t.Fatalf("system message should be empty: %s", systemMsg)
	}
	if len(anthMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(anthMessages))
	}
}

func TestAnthropicBuildMessages_OnlySystem(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "Be concise"},
		{Role: "user", Content: "Hi"},
	}

	anthMessages, systemMsg := anthropicBuildMessages(messages)

	if systemMsg != "Be concise" {
		t.Fatalf("system message not extracted: %s", systemMsg)
	}
	if len(anthMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(anthMessages))
	}
}

func TestSiderBuildPayload(t *testing.T) {
	messages := []ChatMessage{
		{Role: "system", Content: "Be helpful"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!"},
	}

	payload := siderBuildPayload("gpt-4o", messages, false)

	if payload["model"] != "gpt-4o" {
		t.Fatalf("model not set: %v", payload["model"])
	}
	if payload["stream"] != false {
		t.Fatalf("stream should be false")
	}
	prompt, ok := payload["prompt"].(string)
	if !ok || prompt == "" {
		t.Fatal("prompt should be non-empty string")
	}
	// Check prompt contains system instructions
	if !contains(prompt, "[System Instructions]") {
		t.Fatal("prompt should contain system instructions section")
	}
	if !contains(prompt, "[User]: Hello") {
		t.Fatal("prompt should contain user message")
	}
	if !contains(prompt, "[Assistant]: Hi!") {
		t.Fatal("prompt should contain assistant message")
	}
}

func TestSiderBuildPayload_Stream(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "Test"},
	}

	payload := siderBuildPayload("deepseek-chat", messages, true)
	if payload["stream"] != true {
		t.Fatal("stream should be true")
	}
}

func TestSiderBuildHeaders(t *testing.T) {
	token := "test-sider-token"
	headers := siderBuildHeaders(token)

	if headers.Get("Authorization") != "Bearer "+token {
		t.Fatalf("Authorization header wrong: %s", headers.Get("Authorization"))
	}
	if headers.Get("Content-Type") != "application/json" {
		t.Fatal("Content-Type should be application/json")
	}
}

func TestBuildOpenAIBody(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "Hello"},
	}
	extra := map[string]any{"temperature": 0.7}

	body := buildOpenAIBody("gpt-4o", messages, false, extra)

	if body["model"] != "gpt-4o" {
		t.Fatalf("model wrong: %v", body["model"])
	}
	if body["stream"] != false {
		t.Fatal("stream should be false")
	}
	if body["temperature"] != 0.7 {
		t.Fatalf("temperature not passed through: %v", body["temperature"])
	}
}

func TestBuildOpenAIBody_Stream(t *testing.T) {
	messages := []ChatMessage{
		{Role: "user", Content: "Hi"},
	}
	body := buildOpenAIBody("gpt-4o", messages, true, nil)
	if body["stream"] != true {
		t.Fatal("stream should be true")
	}
}

func TestWriteSSEChunk(t *testing.T) {
	// Just verify it doesn't panic
	stop := "stop"
	chunk := ChatChunk{
		ID: "test-id", Object: "chat.completion.chunk",
		Created: 12345, Model: "gpt-4o",
		Choices: []Choice{{Delta: &Msg{Content: strPtr("hello")}, FinishReason: &stop}},
	}
	// Use a bytes.Buffer as io.Writer
	var buf testWriter
	writeSSEChunk(&buf, chunk)
	if buf.data == "" {
		t.Fatal("SSE chunk should write data")
	}
	if !contains(buf.data, "data: ") {
		t.Fatal("SSE chunk should start with 'data: '")
	}
}

func TestWriteSSEError(t *testing.T) {
	var buf testWriter
	writeSSEError(&buf, "gpt-4o", "test error")
	if buf.data == "" {
		t.Fatal("SSE error should write data")
	}
	if !contains(buf.data, "test error") {
		t.Fatal("SSE error should contain error message")
	}
	if !contains(buf.data, "[DONE]") {
		t.Fatal("SSE error should end with [DONE]")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello"},
		{"", 5, ""},
		{"abc", 3, "abc"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestStrPtr(t *testing.T) {
	p := strPtr("hello")
	if *p != "hello" {
		t.Fatalf("strPtr wrong: %s", *p)
	}
}

// testWriter is a simple io.Writer for testing.
type testWriter struct {
	data string
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	w.data += string(p)
	return len(p), nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
