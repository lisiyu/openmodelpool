package main

// p1_stream_ssrf_test.go — regression tests for two P1 fixes in client.go:
//
//   - P1-A: streaming adapters must return an error (not nil) when the
//     upstream answers 401/403/>=400 or the transport fails. Returning nil
//     made handlers.go misjudge the stream as successful: recordProviderSuccess
//     fired, fallback was skipped, and Guest Key/public quota settled at the
//     full estimate — so a provider with an expired token was never switched.
//     Nothing may be written to w before the error either, otherwise
//     handleStreamProxy reports dataSent and fallback is still skipped.
//   - P1-B (SEC-SSRF-1): the SSRF guard must inspect the actual request
//     endpoint. proxyHTTPClient only checked p.BaseURL, but web_session
//     requests cfg.APIEndpoint — a consumer-controlled field (POST
//     /api/providers). A consumer could set a public BaseURL to pass the
//     check while pointing APIEndpoint at 169.254.169.254/127.0.0.1.

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testStreamMessages() []ChatMessage {
	return []ChatMessage{{Role: "user", Content: "hi"}}
}

// Each streaming adapter must surface an upstream auth/HTTP error as a
// returned error, and must not write anything to the client stream first
// (so the handler can still fall back to the next provider).
func TestStreamAdapters_UpstreamErrorReturnsError(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		adapter string
	}{
		{"web_session 401", 401, "web_session"},
		{"web_session 403", 403, "web_session"},
		{"web_session 500", 500, "web_session"},
		{"sider 401", 401, "sider"},
		{"anthropic 401", 401, "anthropic"},
		{"gemini 401", 401, "gemini"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			p := Provider{ID: "p1", Name: "p1", Type: tc.adapter, APIKey: "test-key", BaseURL: srv.URL}
			var buf bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			var err error
			switch tc.adapter {
			case "web_session":
				p.WebSession = &WebSessionConfig{APIEndpoint: srv.URL}
				err = webSessionStream(ctx, p, "test-model", testStreamMessages(), &buf)
			case "sider":
				old := siderChatURL
				siderChatURL = srv.URL
				defer func() { siderChatURL = old }()
				// siderMon is only initialized in production init; give the
				// test an isolated monitor so RecordFailure doesn't nil-panic.
				oldMon := siderMon
				initSiderMonitor(t.TempDir() + "/sider.json")
				defer func() { siderMon = oldMon }()
				err = siderStream(ctx, p, "test-model", testStreamMessages(), &buf)
			case "anthropic":
				err = anthropicStream(ctx, p, "test-model", testStreamMessages(), &buf)
			case "gemini":
				err = geminiStream(ctx, p, "test-model", testStreamMessages(), nil, &buf)
			}
			if err == nil {
				t.Fatalf("%s: upstream HTTP %d must return an error, got nil", tc.adapter, tc.status)
			}
			if buf.Len() != 0 {
				t.Fatalf("%s: nothing may be written before the error (got %d bytes), or fallback is skipped", tc.adapter, buf.Len())
			}
		})
	}
}

// A transport failure in cozeStream must also surface as an error.
func TestCozeStream_TransportErrorReturnsError(t *testing.T) {
	// Port 1 is closed: connection refused, no real upstream involved.
	p := Provider{ID: "coze1", Name: "coze", Type: "coze", APIKey: "test-key", BaseURL: "http://127.0.0.1:1"}
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := cozeStream(ctx, p, "coze-123", testStreamMessages(), &buf); err == nil {
		t.Fatal("cozeStream: transport failure must return an error, got nil")
	}
}

// SEC-SSRF-1: a consumer can set a public BaseURL (passes the old guard) while
// pointing web_session APIEndpoint at an internal address. The client built
// for the actual endpoint must refuse to dial.
func TestProxyHTTPClientForURL_BlocksPrivateAPIEndpoint(t *testing.T) {
	oldAllow := allowLocalProviderForTest
	allowLocalProviderForTest = false
	defer func() { allowLocalProviderForTest = oldAllow }()

	attackTargets := []string{
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"http://127.0.0.1:9/internal",
	}
	for _, target := range attackTargets {
		p := Provider{
			ID:      "evil",
			Name:    "evil",
			Type:    "web_session",
			APIKey:  "k",
			BaseURL: "https://example.com", // public: passes the BaseURL-only check
			WebSession: &WebSessionConfig{
				APIEndpoint: target,
			},
		}
		client := proxyHTTPClientForURL(p, p.WebSession.APIEndpoint, 5*time.Second)
		req, _ := http.NewRequest("GET", target, nil)
		_, err := client.Do(req)
		if err == nil {
			t.Fatalf("APIEndpoint %s: request to internal address must be blocked, got nil error", target)
		}
		if !strings.Contains(err.Error(), "ssrf blocked") {
			t.Fatalf("APIEndpoint %s: expected ssrf-blocked error, got: %v", target, err)
		}
	}
}

// End-to-end: webSessionStream with a public BaseURL but an internal
// APIEndpoint must fail with the SSRF error instead of dialing.
func TestWebSessionStream_PrivateAPIEndpointRejected(t *testing.T) {
	oldAllow := allowLocalProviderForTest
	allowLocalProviderForTest = false
	defer func() { allowLocalProviderForTest = oldAllow }()

	p := Provider{
		ID:      "evil",
		Name:    "evil",
		Type:    "web_session",
		APIKey:  "k",
		BaseURL: "https://example.com",
		WebSession: &WebSessionConfig{
			APIEndpoint: "http://169.254.169.254/latest/meta-data/",
		},
	}
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := webSessionStream(ctx, p, "test-model", testStreamMessages(), &buf)
	if err == nil {
		t.Fatal("webSessionStream: internal APIEndpoint must be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "ssrf blocked") {
		t.Fatalf("expected ssrf-blocked error, got: %v", err)
	}
}

// ============================================================
// SSRF follow-ups: redirect re-validation, dial-time validation,
// relay-side DNS resolution.
// ============================================================

// ssrfGuardOn pins the fail-closed guard for these assertions (TestMain
// disables it globally so httptest loopback servers work).
func ssrfGuardOn(t *testing.T) {
	t.Helper()
	oldAllow := allowLocalProviderForTest
	allowLocalProviderForTest = false
	t.Cleanup(func() { allowLocalProviderForTest = oldAllow })
}

func TestSSRFIPBlocked_Classification(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1",
		"10.0.0.5", "172.16.9.9", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "100.127.255.255",
		"224.0.0.251", "0.0.0.0", "::",
		"fc00::1", "fe80::1", "ff02::1",
	}
	for _, s := range blocked {
		if !ssrfIPBlocked(net.ParseIP(s)) {
			t.Errorf("ssrfIPBlocked(%s) = false, want true", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2001:4860:4860::8888"}
	for _, s := range allowed {
		if ssrfIPBlocked(net.ParseIP(s)) {
			t.Errorf("ssrfIPBlocked(%s) = true, want false", s)
		}
	}
	if !ssrfIPBlocked(nil) {
		t.Error("ssrfIPBlocked(nil) = false, want true")
	}
}

func TestSSRFCheckRedirect_BlocksPrivateHop(t *testing.T) {
	ssrfGuardOn(t)

	mkReq := func(target string) *http.Request {
		req, err := http.NewRequest("GET", target, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		return req
	}
	for _, target := range []string{
		"http://127.0.0.1/secret",
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost:8000/admin",
		"http://10.0.0.5/",
	} {
		if err := ssrfCheckRedirect(mkReq(target), nil); err == nil || !strings.Contains(err.Error(), "ssrf blocked") {
			t.Errorf("redirect to %s must be blocked, got %v", target, err)
		}
	}
	// Public IP literals need no DNS and must pass (deterministic).
	if err := ssrfCheckRedirect(mkReq("http://93.184.216.34/next"), nil); err != nil {
		t.Errorf("redirect to public IP must pass, got %v", err)
	}
	// Default redirect cap preserved.
	vias := make([]*http.Request, 10)
	if err := ssrfCheckRedirect(mkReq("http://93.184.216.34/next"), vias); err == nil {
		t.Error("10+ redirects must be stopped")
	}
	// Test escape hatch still respected.
	allowLocalProviderForTest = true
	if err := ssrfCheckRedirect(mkReq("http://127.0.0.1/secret"), nil); err != nil {
		t.Errorf("test escape must bypass the redirect check, got %v", err)
	}
}

func TestSSRFGuardedDial_BlocksPrivateWithoutDialing(t *testing.T) {
	ssrfGuardOn(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Port 9 is closed, but the guard must fire before any dial is attempted:
	// the error text proves the block, not a connection refusal.
	_, err := ssrfGuardedDialContext(ctx, "tcp", "127.0.0.1:9")
	if err == nil || !strings.Contains(err.Error(), "ssrf blocked") {
		t.Fatalf("dial to loopback must be ssrf-blocked, got %v", err)
	}
	_, err = ssrfGuardedDialContext(ctx, "tcp", "not a dial address")
	if err == nil || !strings.Contains(err.Error(), "ssrf blocked") {
		t.Fatalf("malformed dial address must be ssrf-blocked, got %v", err)
	}
	// A hostname that cannot resolve must fail closed without dialing.
	_, err = ssrfGuardedDialContext(ctx, "tcp", "nonexistent.invalid:80")
	if err == nil || !strings.Contains(err.Error(), "ssrf blocked") {
		t.Fatalf("unresolvable host must be ssrf-blocked, got %v", err)
	}
	// Test escape hatch: validation skipped (would dial for real, so only
	// assert that the guard no longer rejects upfront — use a closed port and
	// expect a dial error instead of an ssrf error).
	allowLocalProviderForTest = true
	_, err = ssrfGuardedDialContext(ctx, "tcp", "127.0.0.1:9")
	if err == nil || strings.Contains(err.Error(), "ssrf blocked") {
		t.Fatalf("test escape must skip validation, got %v", err)
	}
}

func TestRelayTargetBlocked_ResolvesNames(t *testing.T) {
	blocked := []string{
		"", "localhost", "LOCALHOST",
		"127.0.0.1", "::1", "10.0.0.5", "172.16.0.9", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "0.0.0.0",
	}
	for _, h := range blocked {
		if !relayTargetBlocked(h) {
			t.Errorf("relayTargetBlocked(%q) = false, want true", h)
		}
	}
	// Public IP literals need no DNS and must pass (deterministic).
	for _, h := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34"} {
		if relayTargetBlocked(h) {
			t.Errorf("relayTargetBlocked(%q) = true, want false", h)
		}
	}
}
