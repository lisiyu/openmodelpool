package main

// Regression tests for review fix 4: the Anthropic / Gemini / Azure downstream
// compatibility layers rebuild the inbound request as a /v1/chat/completions
// request before handing it to handleGatewayRequest. Rebuilding with
// &http.Request{} dropped the inbound request's context, which carries the
// verified guest key (withGuestKey); handlers.go's relayGuestKey(r) then saw
// nothing and the per-key daily/hourly/per-request/RPM quotas were bypassed
// on /v1/messages, /v1beta/models/* and /openai/deployments/*.
//
// These tests stub the gateway hop, drive each real compat handler with a
// request whose context carries a guest key, and assert the forwarded request
// still exposes it via relayGuestKey.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubGatewayForCompatTest replaces handleGatewayRequest for the duration of
// the test; onCapture receives the request the compat layer forwards to it.
func stubGatewayForCompatTest(t *testing.T, onCapture func(*http.Request)) {
	t.Helper()
	orig := handleGatewayRequest
	handleGatewayRequest = func(w http.ResponseWriter, r *http.Request) {
		onCapture(r)
		w.WriteHeader(http.StatusOK)
	}
	t.Cleanup(func() { handleGatewayRequest = orig })
}

// assertGuestKeyForwarded fails unless the compat handler forwarded its
// request to the gateway with the guest key still retrievable from context.
func assertGuestKeyForwarded(t *testing.T, captured *http.Request, wantKey string) {
	t.Helper()
	if captured == nil {
		t.Fatal("compat layer did not forward the request to the gateway")
	}
	if got := relayGuestKey(captured); got != wantKey {
		t.Fatalf("guest key lost in compat forwarding: relayGuestKey=%q, want %q", got, wantKey)
	}
	if captured.URL.Path != "/v1/chat/completions" {
		t.Fatalf("unexpected forwarded path %q, want /v1/chat/completions", captured.URL.Path)
	}
}

func TestCompatAnthropic_PreservesGuestKeyContext(t *testing.T) {
	var captured *http.Request
	stubGatewayForCompatTest(t, func(r *http.Request) { captured = r })

	body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withGuestKey(req, "sk-guest-4a")

	rec := httptest.NewRecorder()
	handleAnthropicMessages(rec, req)

	assertGuestKeyForwarded(t, captured, "sk-guest-4a")
}

func TestCompatGemini_PreservesGuestKeyContext(t *testing.T) {
	var captured *http.Request
	stubGatewayForCompatTest(t, func(r *http.Request) { captured = r })

	body := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("model", "gemini-2.0-flash:generateContent")
	req = withGuestKey(req, "sk-guest-4b")

	rec := httptest.NewRecorder()
	handleGeminiGenerateContent(rec, req)

	assertGuestKeyForwarded(t, captured, "sk-guest-4b")
}

func TestCompatAzure_PreservesGuestKeyContext(t *testing.T) {
	var captured *http.Request
	stubGatewayForCompatTest(t, func(r *http.Request) { captured = r })

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/openai/deployments/gpt-4o/chat/completions?api-version=2024-02-01", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("deployment", "gpt-4o")
	req = withGuestKey(req, "sk-guest-4c")

	rec := httptest.NewRecorder()
	handleAzureChatCompletions(rec, req)

	assertGuestKeyForwarded(t, captured, "sk-guest-4c")
}
