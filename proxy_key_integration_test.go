package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProxyKeyClient_NonStream reproduces the "其他客户端配置了 OMP 的 proxy
// api key 调用模型" path end-to-end: an external OpenAI-compatible client sets
// base_url=OMP and api_key=<proxy_api_key>, then POSTs /v1/chat/completions.
// It must authenticate (role=admin), route to a local provider, and forward to
// the upstream, returning 200 with the model echoed back. This guards the
// proxy-key call path against regressions.
func TestProxyKeyClient_NonStream(t *testing.T) {
	_ = setupTestEnv(t)

	const proxyKey = "sk-omp-proxy-test-0123456789abcdef"
	cfg.Set("proxy_api_key", proxyKey)

	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
	}))
	defer srv.Close()

	pm.providers["p-local"] = Provider{
		ID: "p-local", Name: "Local", Type: "openai_compatible",
		BaseURL: srv.URL, APIKey: "sk-provider-secret", Enabled: true, Owner: "",
		Models: []ModelDef{{ID: "gpt-4", Enabled: true}},
	}

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+proxyKey)
	w := httptest.NewRecorder()

	withProxyAuth(handleGatewayRequest)(w, req)

	if w.Code != 200 {
		t.Fatalf("proxy-key client expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp["model"] != "gpt-4" {
		t.Errorf("expected echoed model gpt-4, got %v", resp["model"])
	}
	if gotPath != "/chat/completions" {
		t.Errorf("expected upstream path /chat/completions, got %q", gotPath)
	}
	if gotAuth != "Bearer sk-provider-secret" {
		t.Errorf("expected upstream Authorization 'Bearer sk-provider-secret', got %q", gotAuth)
	}
}

// TestProxyKeyClient_Stream exercises the same proxy-key client path with
// stream=true, ensuring SSE is forwarded correctly (most clients default to
// streaming, so a break here is the most likely cause of a client-side error).
func TestProxyKeyClient_Stream(t *testing.T) {
	_ = setupTestEnv(t)

	const proxyKey = "sk-omp-proxy-test-0123456789abcdef"
	cfg.Set("proxy_api_key", proxyKey)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		write := func(s string) {
			_, _ = w.Write([]byte(s))
			if flusher != nil {
				flusher.Flush()
			}
		}
		write("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		write("data: [DONE]\n\n")
	}))
	defer srv.Close()

	pm.providers["p-local"] = Provider{
		ID: "p-local", Name: "Local", Type: "openai_compatible",
		BaseURL: srv.URL, APIKey: "sk-provider-secret", Enabled: true, Owner: "",
		Models: []ModelDef{{ID: "gpt-4", Enabled: true}},
	}

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+proxyKey)
	w := httptest.NewRecorder()

	withProxyAuth(handleGatewayRequest)(w, req)

	if w.Code != 200 {
		t.Fatalf("proxy-key stream client expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "chatcmpl-1") {
		t.Errorf("expected SSE chunk with chatcmpl-1 in body, got %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("expected [DONE] sentinel in streamed body, got %q", w.Body.String())
	}
}

// TestProxyKeyClient_StreamLargeLine guards against the bufio.Scanner 1 MiB
// single-line cap: a provider that emits a large delta in one SSE `data:` line
// (big tool_calls argument, long reasoning delta, or inline media) must NOT
// cause bufio.ErrTooLong to surface as a client-side stream error. This is the
// exact class of failure a real OpenAI-compatible proxy client would hit.
func TestProxyKeyClient_StreamLargeLine(t *testing.T) {
	_ = setupTestEnv(t)

	const proxyKey = "sk-omp-proxy-test-0123456789abcdef"
	cfg.Set("proxy_api_key", proxyKey)

	// Build a >1 MiB content payload to exceed the old default scanner cap.
	big := strings.Repeat("x", 2*1024*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		chunk := `{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"` + big + `"},\"finish_reason":null}]}`
		_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	pm.providers["p-local"] = Provider{
		ID: "p-local", Name: "Local", Type: "openai_compatible",
		BaseURL: srv.URL, APIKey: "sk-provider-secret", Enabled: true, Owner: "",
		Models: []ModelDef{{ID: "gpt-4", Enabled: true}},
	}

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+proxyKey)
	w := httptest.NewRecorder()

	withProxyAuth(handleGatewayRequest)(w, req)

	if w.Code != 200 {
		t.Fatalf("proxy-key large-line stream expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("expected [DONE] after large line, got truncated body len=%d", w.Body.Len())
	}
	if !strings.Contains(w.Body.String(), big) {
		t.Errorf("large content not forwarded intact (body len=%d)", w.Body.Len())
	}
}
