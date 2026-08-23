package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================
// B10-WL: web-login fixes — bookmarklet CORS scope, socks egress probe
// ============================================================

func TestP10_CORS_BookmarkletWildcardScope(t *testing.T) {
	if cfg == nil {
		cfg = &Config{path: "test", data: make(map[string]any)}
	}
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Bookmarklet preflight from an arbitrary provider site: PUT on
	// /api/providers/{id} must get ACAO:* even though sider.ai is not in the
	// whitelist — otherwise the bookmarklet fails with "Failed to fetch".
	req := httptest.NewRequest("OPTIONS", "https://node.example.com/api/providers/p1", nil)
	req.Header.Set("Origin", "https://sider.ai")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("preflight PUT /api/providers: want ACAO=*, got %q", got)
	}

	// The actual cross-origin PUT must also carry ACAO:*.
	req = httptest.NewRequest("PUT", "https://node.example.com/api/providers/p1", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://sider.ai")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("PUT /api/providers: want ACAO=*, got %q", got)
	}

	// Non-provider paths keep strict whitelist behavior: unknown origin gets
	// no ACAO header at all.
	req = httptest.NewRequest("GET", "https://node.example.com/api/keys", nil)
	req.Header.Set("Origin", "https://evil.example.net")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("GET unknown origin: want empty ACAO, got %q", got)
	}
}

func TestP10_SocksEgress_DeadTunnelFails(t *testing.T) {
	// Nothing listens here — the probe must fail fast with an error rather
	// than silently passing (the old code returned the proxy addr after a
	// blind 500ms sleep, leaving Chrome on a dead tunnel).
	err := checkSocksEgress("127.0.0.1:1")
	if err == nil {
		t.Fatalf("expected egress check against closed port to fail")
	}
}
