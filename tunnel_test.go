package main

// tunnel_test.go — Independent QA validation suite for the
// "域名绑定引导" (Domain Binding Guide) read-only feature.
//
// These tests supplement (and do not modify) the engineer's backend changes in
// tunnel.go / server.go / admin.html. They target the skeptical QA mandate:
// prove the handler actually works, the route is wired with auth, the domain
// resolution priority chain is correct, the health probe behaves, and the
// frontend references are consistent.
//
// Coverage:
//   * GET /api/domain/binding-status — auth required (401 w/o token).
//   * GET /api/domain/binding-status — 200 + full JSON field set + value types.
//   * Route registration verified through the real mux (setupRoutes).
//   * resolveBoundDomain priority chain (bound_domain > public_domain/PUBLIC_DOMAIN env
//     > public_url > federation_endpoint > request Host) — exercised on the helper,
//     no network.
//   * hostOf / ensureHTTPS unit behaviour.
//   * probeDomainHealth unreachable path returns (false, error) and never panics.
//   * handler JSON shape with a live tunnel (quick vs manual mode) and with a
//     bound (unreachable) domain — exercises the http_reachable assignment.
//   * admin.html wiring consistency (card id, refresh button -> JS fn, endpoint,
//     tag balance).

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// qaDomainResetConfig clears all config keys that resolveBoundDomain consults,
// so each priority sub-case starts from a known-empty baseline.
func qaDomainResetConfig() {
	for _, k := range []string{"bound_domain", "public_domain", "public_url", "federation_endpoint"} {
		cfg.Set(k, "")
	}
}

// qaDomainToken returns a valid admin bearer token for the test auth instance.
func qaDomainToken(t *testing.T) string {
	t.Helper()
	tok := auth.CreateAccessToken("admin", false)
	if tok == "" {
		t.Fatal("failed to mint test auth token")
	}
	return tok
}

// qaRequireFields fails the test if any of the given JSON keys is absent.
func qaRequireFields(t *testing.T, m map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("response missing field %q (body=%v)", k, m)
		}
	}
}

// ---------------------------------------------------------------------------
// T-1: Authentication is required (wrapped by withAuth)
// ---------------------------------------------------------------------------

// A request WITHOUT a token must be rejected with 401. If it returned 200, the
// route was registered WITHOUT auth (a source bug).
func TestQADomainBindingStatusRequiresAuth(t *testing.T) {
	_ = setupTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/domain/binding-status", nil)
	rec := httptest.NewRecorder()

	withAuth(handleDomainBindingStatus)(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	// Body should be JSON describing the auth failure.
	qaRequireFields(t, qaParseJSON(t, rec.Body.String()), "error")
}

// ---------------------------------------------------------------------------
// T-2: Happy path — 200 + full JSON field set with correct value types
// ---------------------------------------------------------------------------

func TestQADomainBindingStatusStructure(t *testing.T) {
	_ = setupTestEnv(t)
	qaDomainResetConfig()
	// Ensure no PUBLIC_DOMAIN leakage from the environment.
	origEnv := os.Getenv("PUBLIC_DOMAIN")
	os.Unsetenv("PUBLIC_DOMAIN")
	defer os.Setenv("PUBLIC_DOMAIN", origEnv)

	// Empty Host + empty config => no domain resolved => http_reachable is null
	// and no health probe is attempted. (httptest.NewRequest defaults Host to
	// "example.com", which would otherwise trigger the priority-5 Host fallback
	// and a real network probe.)
	req := httptest.NewRequest(http.MethodGet, "/api/domain/binding-status", nil)
	req.Host = ""
	req.Header.Set("Authorization", "Bearer "+qaDomainToken(t))
	rec := httptest.NewRecorder()

	withAuth(handleDomainBindingStatus)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json content-type, got %q", ct)
	}

	m := qaParseJSON(t, rec.Body.String())
	qaRequireFields(t, m,
		"domain", "bound", "public_url",
		"tunnel_running", "tunnel_managed", "tunnel_mode", "tunnel_url",
		"http_reachable", "reach_error", "version",
	)

	// domain must be a string (empty here because nothing is bound).
	if v, ok := m["domain"].(string); !ok || v != "" {
		t.Errorf("domain: want empty string, got %#v", m["domain"])
	}
	// bound must be a bool=false.
	if v, ok := m["bound"].(bool); !ok || v {
		t.Errorf("bound: want false, got %#v", m["bound"])
	}
	// public_url must be a string (empty).
	if v, ok := m["public_url"].(string); !ok || v != "" {
		t.Errorf("public_url: want empty string, got %#v", m["public_url"])
	}
	// tunnel_* must be bools.
	if v, ok := m["tunnel_running"].(bool); !ok || v {
		t.Errorf("tunnel_running: want false bool, got %#v", m["tunnel_running"])
	}
	if v, ok := m["tunnel_managed"].(bool); !ok || v {
		t.Errorf("tunnel_managed: want false bool, got %#v", m["tunnel_managed"])
	}
	if v, ok := m["tunnel_mode"].(string); !ok || v != "" {
		t.Errorf("tunnel_mode: want empty string, got %#v", m["tunnel_mode"])
	}
	if v, ok := m["tunnel_url"].(string); !ok || v != "" {
		t.Errorf("tunnel_url: want empty string, got %#v", m["tunnel_url"])
	}
	// With no domain set, http_reachable must serialize as JSON null.
	if m["http_reachable"] != nil {
		t.Errorf("http_reachable: want null, got %#v", m["http_reachable"])
	}
	// reach_error must be a (here empty) string.
	if v, ok := m["reach_error"].(string); !ok || v != "" {
		t.Errorf("reach_error: want empty string, got %#v", m["reach_error"])
	}
	// version must be a non-empty string.
	if v, ok := m["version"].(string); !ok || v == "" {
		t.Errorf("version: want non-empty string, got %#v", m["version"])
	}
}

// ---------------------------------------------------------------------------
// T-3: Route is actually registered (served through the real mux)
// ---------------------------------------------------------------------------

// Dispatching through setupRoutes() proves three things at once: the pattern
// "GET /api/domain/binding-status" is registered, it matches, and it is wrapped
// with withAuth (valid token -> 200, not 401/404).
func TestQADomainBindingStatusRouteRegistered(t *testing.T) {
	_ = setupTestEnv(t)
	qaDomainResetConfig()
	origEnv := os.Getenv("PUBLIC_DOMAIN")
	os.Unsetenv("PUBLIC_DOMAIN")
	defer os.Setenv("PUBLIC_DOMAIN", origEnv)

	mux := setupRoutes()

	// With a valid token -> 200 (route exists + auth passes).
	req := httptest.NewRequest(http.MethodGet, "/api/domain/binding-status", nil)
	req.Header.Set("Authorization", "Bearer "+qaDomainToken(t))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("route GET /api/domain/binding-status not served (expected 200, got %d, body=%s)",
			rec.Code, rec.Body.String())
	}

	// With no token -> 401 (route present AND auth enforced via mux path).
	req2 := httptest.NewRequest(http.MethodGet, "/api/domain/binding-status", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("route should enforce auth (expected 401, got %d)", rec2.Code)
	}
}

// ---------------------------------------------------------------------------
// T-4: resolveBoundDomain priority chain (helper, no network)
// ---------------------------------------------------------------------------

func TestQAResolveBoundDomainPriority(t *testing.T) {
	_ = setupTestEnv(t)
	origEnv := os.Getenv("PUBLIC_DOMAIN")
	defer os.Setenv("PUBLIC_DOMAIN", origEnv)

	cases := []struct {
		name      string
		setup     func()
		host      string // request Host
		wantDom   string
		wantBound bool
		wantPU    string
	}{
		{
			name: "1_bound_domain_wins",
			setup: func() {
				qaDomainResetConfig()
				cfg.Set("bound_domain", "https://bound.example.com")
			},
			host:      "ignored.example.com",
			wantDom:   "bound.example.com",
			wantBound: true,
			wantPU:    "https://bound.example.com",
		},
		{
			name: "2_public_domain_config",
			setup: func() {
				qaDomainResetConfig()
				cfg.Set("public_domain", "https://pool.example.com")
			},
			host:      "ignored.example.com",
			wantDom:   "pool.example.com",
			wantBound: false,
			wantPU:    "https://pool.example.com",
		},
		{
			name: "2b_public_domain_env_PUBLIC_DOMAIN",
			setup: func() {
				qaDomainResetConfig()
				os.Unsetenv("PUBLIC_DOMAIN")
				os.Setenv("PUBLIC_DOMAIN", "openmodelpool.io")
			},
			host:      "ignored.example.com",
			wantDom:   "openmodelpool.io",
			wantBound: false,
			wantPU:    "https://openmodelpool.io",
		},
		{
			name: "3_public_url_config",
			setup: func() {
				qaDomainResetConfig()
				cfg.Set("public_url", "https://pub.example.com:9000")
			},
			host:      "ignored.example.com",
			wantDom:   "pub.example.com",
			wantBound: false,
			wantPU:    "https://pub.example.com:9000",
		},
		{
			name: "4_federation_endpoint_config",
			setup: func() {
				qaDomainResetConfig()
				cfg.Set("federation_endpoint", "https://fed.example.com/v1")
			},
			host:      "ignored.example.com",
			wantDom:   "fed.example.com",
			wantBound: false,
			wantPU:    "https://fed.example.com/v1",
		},
		{
			name: "5_request_host_fallback",
			setup: func() {
				qaDomainResetConfig()
				os.Unsetenv("PUBLIC_DOMAIN")
			},
			host:      "host.example.com:8000",
			wantDom:   "host.example.com",
			wantBound: false,
			wantPU:    "https://host.example.com",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.setup()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.host != "" {
				req.Host = c.host
			}
			dom, bound, pu := resolveBoundDomain(req)
			if dom != c.wantDom {
				t.Errorf("domain = %q, want %q", dom, c.wantDom)
			}
			if bound != c.wantBound {
				t.Errorf("bound = %v, want %v", bound, c.wantBound)
			}
			if pu != c.wantPU {
				t.Errorf("public_url = %q, want %q", pu, c.wantPU)
			}
			if c.wantDom != "" && !strings.Contains(dom, c.wantDom) {
				t.Errorf("domain %q does not contain expected %q", dom, c.wantDom)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// T-5: hostOf / ensureHTTPS unit behaviour
// ---------------------------------------------------------------------------

func TestQAHostOfEnsureHTTPS(t *testing.T) {
	hostCases := []struct {
		in, want string
	}{
		{"https://a.example.com:8080/path?x=1", "a.example.com"},
		{"http://example.com", "example.com"},
		{"example.com:443", "example.com"},
		// Per hostOf's own comment, IPv6 is kept WITH its port intact.
		{"[::1]:8000", "[::1]:8000"},
		{"  https://b.example.com  ", "b.example.com"},
		{"", ""},
	}
	for _, c := range hostCases {
		if got := hostOf(c.in); got != c.want {
			t.Errorf("hostOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	httpsCases := []struct {
		in, want string
	}{
		{"example.com", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"http://example.com", "http://example.com"}, // scheme kept as-is
		{"", ""},
	}
	for _, c := range httpsCases {
		if got := ensureHTTPS(c.in); got != c.want {
			t.Errorf("ensureHTTPS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// T-6: probeDomainHealth unreachable path (no panic, returns false+error)
// ---------------------------------------------------------------------------

func TestQAProbeDomainHealthUnreachable(t *testing.T) {
	// Empty domain -> early return with a descriptive error, never panics.
	if ok, msg := probeDomainHealth(""); ok || msg == "" {
		t.Errorf("probeDomainHealth(\"\") = (%v, %q), want (false, non-empty)", ok, msg)
	}
	// localhost is explicitly skipped.
	if ok, msg := probeDomainHealth("localhost"); ok || msg != "未配置可探测的域名" {
		t.Errorf("probeDomainHealth(\"localhost\") = (%v, %q), want (false, 未配置可探测的域名)", ok, msg)
	}
	// A host with nothing listening on 443 must be reported unreachable.
	// Uses 127.0.0.1 to avoid DNS dependence; keep deterministic & fast.
	if ok, msg := probeDomainHealth("127.0.0.1"); ok || msg == "" {
		t.Errorf("probeDomainHealth(\"127.0.0.1\") = (%v, %q), want (false, non-empty error)", ok, msg)
	}
}

// ---------------------------------------------------------------------------
// T-7: handler JSON shape with a live tunnel (quick vs manual)
// ---------------------------------------------------------------------------

func TestQADomainBindingStatusWithTunnel(t *testing.T) {
	_ = setupTestEnv(t)
	qaDomainResetConfig()
	origEnv := os.Getenv("PUBLIC_DOMAIN")
	os.Unsetenv("PUBLIC_DOMAIN")
	defer os.Setenv("PUBLIC_DOMAIN", origEnv)

	origTunnel := tunnel
	defer func() { tunnel = origTunnel }()

	run := func(tm *TunnelManager) map[string]any {
		tunnel = tm
		req := httptest.NewRequest(http.MethodGet, "/api/domain/binding-status", nil)
		req.Header.Set("Authorization", "Bearer "+qaDomainToken(t))
		rec := httptest.NewRecorder()
		withAuth(handleDomainBindingStatus)(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		return qaParseJSON(t, rec.Body.String())
	}

	// Quick mode, running.
	m := run(&TunnelManager{port: 8000, mode: "quick", running: true, url: "https://abc.trycloudflare.com"})
	if v, ok := m["tunnel_running"].(bool); !ok || !v {
		t.Errorf("quick running: tunnel_running = %#v, want true", m["tunnel_running"])
	}
	if v, ok := m["tunnel_managed"].(bool); !ok || !v {
		t.Errorf("quick running: tunnel_managed = %#v, want true", m["tunnel_managed"])
	}
	if v, ok := m["tunnel_mode"].(string); !ok || v != "quick" {
		t.Errorf("tunnel_mode = %#v, want \"quick\"", m["tunnel_mode"])
	}
	if v, ok := m["tunnel_url"].(string); !ok || v != "https://abc.trycloudflare.com" {
		t.Errorf("tunnel_url = %#v, want https://abc.trycloudflare.com", m["tunnel_url"])
	}

	// Manual mode: spec says tunnel_running=false / tunnel_managed=false even
	// if a process is conceptually "up" (handled externally).
	m = run(&TunnelManager{port: 8000, mode: "manual", running: true, url: "https://m.example.com"})
	if v, ok := m["tunnel_running"].(bool); !ok || v {
		t.Errorf("manual: tunnel_running = %#v, want false", m["tunnel_running"])
	}
	if v, ok := m["tunnel_managed"].(bool); !ok || v {
		t.Errorf("manual: tunnel_managed = %#v, want false", m["tunnel_managed"])
	}
	if v, ok := m["tunnel_mode"].(string); !ok || v != "manual" {
		t.Errorf("tunnel_mode = %#v, want \"manual\"", m["tunnel_mode"])
	}
	if v, ok := m["tunnel_url"].(string); !ok || v != "https://m.example.com" {
		t.Errorf("tunnel_url = %#v, want https://m.example.com", m["tunnel_url"])
	}
}

// ---------------------------------------------------------------------------
// T-8: handler with a bound (unreachable) domain -> http_reachable assigned
// ---------------------------------------------------------------------------

// When a domain is bound, the handler must probe it and assign http_reachable
// to a non-null bool. The network outcome (true/false) is environment-dependent,
// so we only assert the invariant: http_reachable is a bool AND
// (reachable == (reach_error == "")).
func TestQADomainBindingStatusBoundDomainProbe(t *testing.T) {
	_ = setupTestEnv(t)
	qaDomainResetConfig()
	cfg.Set("bound_domain", "test.invalid") // DNS-fail TLD, probe returns quickly
	origEnv := os.Getenv("PUBLIC_DOMAIN")
	os.Unsetenv("PUBLIC_DOMAIN")
	defer os.Setenv("PUBLIC_DOMAIN", origEnv)

	req := httptest.NewRequest(http.MethodGet, "/api/domain/binding-status", nil)
	req.Header.Set("Authorization", "Bearer "+qaDomainToken(t))
	rec := httptest.NewRecorder()
	withAuth(handleDomainBindingStatus)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	m := qaParseJSON(t, rec.Body.String())
	qaRequireFields(t, m, "domain", "bound", "http_reachable", "reach_error")

	if v, ok := m["domain"].(string); !ok || v != "test.invalid" {
		t.Errorf("domain = %#v, want test.invalid", m["domain"])
	}
	if v, ok := m["bound"].(bool); !ok || !v {
		t.Errorf("bound = %#v, want true", m["bound"])
	}
	// http_reachable must be a concrete bool (not null) because a domain is set.
	reach, ok := m["http_reachable"].(bool)
	if !ok {
		t.Fatalf("http_reachable = %#v, want a bool (probe must have run)", m["http_reachable"])
	}
	errMsg, _ := m["reach_error"].(string)
	// Invariant: reachable iff no error detail.
	if reach == (errMsg != "") {
		t.Errorf("invariant broken: http_reachable=%v but reach_error=%q", reach, errMsg)
	}
}

// ---------------------------------------------------------------------------
// T-9: admin.html frontend wiring consistency
// ---------------------------------------------------------------------------

func TestQADomainBindingFrontendWiring(t *testing.T) {
	htmlBytes, err := os.ReadFile("admin.html")
	if err != nil {
		t.Fatalf("read admin.html: %v", err)
	}
	h := string(htmlBytes)

	for _, need := range []string{
		`id="domainBindingCard"`,
		`id="domainBindingBody"`,
		`onclick="refreshDomainBinding()"`,
		`function refreshDomainBinding`,
		`/api/domain/binding-status`,
	} {
		if !strings.Contains(h, need) {
			t.Errorf("admin.html missing required fragment: %q", need)
		}
	}

	// The refresh button's onclick must reference a function that is actually
	// defined in the same file. JS function declarations are hoisted and the
	// button only fires at runtime, so a definition that appears later in the
	// document is valid; we only assert name consistency, not source order.

	// Basic tag balance sanity: <script>/</script> and <details>/</details>.
	count := func(sub string) int {
		return strings.Count(h, sub)
	}
	if count("<script") != count("</script>") {
		t.Errorf("unbalanced <script> tags: open=%d close=%d", count("<script"), count("</script>"))
	}
	if count("<details") != count("</details>") {
		t.Errorf("unbalanced <details> tags: open=%d close=%d", count("<details"), count("</details>"))
	}
}
