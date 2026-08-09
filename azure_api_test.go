package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInjectModelIntoBody(t *testing.T) {
	// Empty body -> just the model field.
	b, err := injectModelIntoBody([]byte{}, "gpt-4o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["model"] != "gpt-4o" {
		t.Fatalf("expected model=gpt-4o, got %v", m["model"])
	}

	// Existing OpenAI body keeps other fields, deployment overrides model.
	b, err = injectModelIntoBody([]byte(`{"messages":[{"role":"user","content":"hi"}],"temperature":0.5}`), "deployment-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["model"] != "deployment-1" {
		t.Fatalf("expected model=deployment-1, got %v", m["model"])
	}
	if _, ok := m["messages"]; !ok {
		t.Fatalf("messages field was dropped")
	}
	if m["temperature"] != 0.5 {
		t.Fatalf("temperature field was dropped or changed")
	}
}

func TestAzureAuthAdapter(t *testing.T) {
	var got string
	h := azureAuthAdapter(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	})
	req := httptest.NewRequest(http.MethodPost, "/openai/deployments/gpt-4/chat/completions", nil)
	req.Header.Set("api-key", "mykey")
	rec := httptest.NewRecorder()
	h(rec, req)
	if got != "Bearer mykey" {
		t.Fatalf("expected Authorization 'Bearer mykey', got %q", got)
	}
}

func TestAzureAuthAdapter_Passthrough(t *testing.T) {
	var got string
	h := azureAuthAdapter(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	})
	req := httptest.NewRequest(http.MethodPost, "/openai/deployments/gpt-4/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer existing")
	rec := httptest.NewRecorder()
	h(rec, req)
	if got != "Bearer existing" {
		t.Fatalf("adapter should not overwrite existing Authorization, got %q", got)
	}
}
