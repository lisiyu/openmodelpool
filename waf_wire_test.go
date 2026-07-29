package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newWAFTestEngine installs a fresh WAF engine into the package globals using an
// isolated config, runs configure() to set WAF config keys, then Reload()s so the
// engine reflects them. The original wafEngine global is restored on cleanup.
func newWAFTestEngine(t *testing.T, configure func(c *Config)) *WAFEngine {
	t.Helper()
	env := setupTestEnv(t)
	orig := wafEngine
	eng := NewWAFEngine()
	wafEngine = eng
	t.Cleanup(func() { wafEngine = orig })

	if configure != nil {
		configure(env.cfgInst)
	}
	eng.Reload()
	return eng
}

// ① WAF enabled + blacklisted IP => request is blocked (engine + middleware).
func TestWAFBlocksBlacklistedIP(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		c.Set("waf_enabled", "true")
		c.Set("waf_ip_blacklist", "1.2.3.4, 5.6.7.8")
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	r.RemoteAddr = "1.2.3.4:9999"
	allowed, v := eng.Check(r)
	if allowed {
		t.Fatalf("expected blacklisted IP to be blocked, got allowed")
	}
	if v.Type != WAFViolationIPBlacklist {
		t.Errorf("expected ip_blacklist violation, got %s", v.Type)
	}

	// Integration through the wired middleware.
	rec := httptest.NewRecorder()
	handler := wafMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 from wafMiddleware, got %d", rec.Code)
	}

	// A clean IP must pass.
	r2 := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	r2.RemoteAddr = "9.9.9.9:9999"
	if allowed, _ := eng.Check(r2); !allowed {
		t.Errorf("expected non-blacklisted IP to be allowed")
	}
}

// ① WAF enabled + blocked User-Agent => request is blocked.
func TestWAFBlocksUserAgent(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		c.Set("waf_enabled", "true")
		c.Set("waf_ua_blacklist", "BadBot, Scrapy")
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.RemoteAddr = "198.51.100.1:5555"
	r.Header.Set("User-Agent", "Mozilla/5.0 (compatible; BadBot/1.0)")
	allowed, v := eng.Check(r)
	if allowed {
		t.Fatalf("expected UA-matched request to be blocked")
	}
	if v.Type != WAFViolationUAFilter {
		t.Errorf("expected ua_filter violation, got %s", v.Type)
	}

	// Clean UA passes.
	r2 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r2.RemoteAddr = "198.51.100.1:5555"
	r2.Header.Set("User-Agent", "curl/8.0")
	if allowed, _ := eng.Check(r2); !allowed {
		t.Errorf("expected clean UA to be allowed")
	}
}

// ① WAF enabled + per-IP rate limit => excess requests are blocked.
func TestWAFRateLimitBlocksExcess(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		c.Set("waf_enabled", "true")
		c.Set("waf_rate_limit_rps", "1")
		c.Set("waf_rate_limit_burst", "1")
	})

	mkReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r.RemoteAddr = "203.0.113.9:1234"
		return r
	}

	if allowed, _ := eng.Check(mkReq()); !allowed {
		t.Fatalf("first request should be allowed")
	}
	if allowed, _ := eng.Check(mkReq()); allowed {
		t.Errorf("second immediate request should be blocked by rate limit")
	}
}

// ① WAF enabled + blocked path prefix => request is blocked.
func TestWAFBlocksBlockedPath(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		c.Set("waf_enabled", "true")
		c.Set("waf_blocked_paths", "/admin/internal,/debug")
	})

	r := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	allowed, v := eng.Check(r)
	if allowed {
		t.Fatalf("expected blocked path to be rejected")
	}
	if v.Type != WAFViolationPathBlock {
		t.Errorf("expected path_block violation, got %s", v.Type)
	}
}

// ② WAF disabled => all requests pass even with rules configured.
func TestWAFDisabledAllowsAll(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		// waf_enabled left as default false, but a blacklist is present.
		c.Set("waf_ip_blacklist", "1.2.3.4")
	})

	if eng.Enabled() {
		t.Fatalf("expected WAF to be disabled")
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.RemoteAddr = "1.2.3.4:9999"
	if allowed, _ := eng.Check(r); !allowed {
		t.Errorf("expected request to be allowed when WAF disabled")
	}
}

// ② WAF enabled but no rules => all requests pass (no false positives).
func TestWAFEnabledNoRulesAllows(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		c.Set("waf_enabled", "true")
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.RemoteAddr = "192.0.2.5:9999"
	if allowed, _ := eng.Check(r); !allowed {
		t.Errorf("expected request to be allowed with no rules configured")
	}
}

// ③ /api/waf/status reflects the real enabled state.
func TestWAFStatusReflectsEnabled(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		c.Set("waf_enabled", "true")
		c.Set("waf_ip_blacklist", "10.0.0.1")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/waf/status", nil)
	handleWAFStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status endpoint returned %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode status body: %v", err)
	}
	if body["enabled"] != true {
		t.Errorf("expected enabled=true, got %v", body["enabled"])
	}
	if ipc, ok := body["ip_blacklist"].(float64); !ok || int(ipc) != 1 {
		t.Errorf("expected ip_blacklist=1, got %v", body["ip_blacklist"])
	}

	// Disabling is reflected immediately.
	eng.cfgSet("waf_enabled", "false")
	eng.Reload()
	rec2 := httptest.NewRecorder()
	handleWAFStatus(rec2, req)
	var body2 map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &body2)
	if body2["enabled"] != false {
		t.Errorf("expected enabled=false after disable, got %v", body2["enabled"])
	}
}

// ③ Violations are recorded and exposed via /api/waf/violations.
func TestWAFViolationsRecorded(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		c.Set("waf_enabled", "true")
		c.Set("waf_ip_blacklist", "1.2.3.4")
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.RemoteAddr = "1.2.3.4:9999"
	eng.Check(r) // blocked -> recorded

	vs := eng.Violations()
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation recorded, got %d", len(vs))
	}
	if vs[0].Type != WAFViolationIPBlacklist {
		t.Errorf("expected ip_blacklist violation, got %s", vs[0].Type)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/waf/violations", nil)
	handleWAFViolations(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	arr, ok := body["violations"].([]any)
	if !ok || len(arr) != 1 {
		t.Errorf("expected 1 violation in endpoint response, got %v", body["violations"])
	}
}

// ③ /api/waf/unban/{key} removes a dynamic ban.
func TestWAFUnbanRemovesBan(t *testing.T) {
	eng := newWAFTestEngine(t, func(c *Config) {
		c.Set("waf_enabled", "true")
	})

	eng.AddBan("1.2.3.4", "manual test ban", time.Hour)
	if len(eng.Bans()) != 1 {
		t.Fatalf("expected 1 active ban")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/waf/unban/1.2.3.4", nil)
	req.SetPathValue("key", "1.2.3.4")
	handleWAFUnban(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unban returned %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["removed"] != true {
		t.Errorf("expected removed=true, got %v", body["removed"])
	}
	if len(eng.Bans()) != 0 {
		t.Errorf("expected 0 active bans after unban, got %d", len(eng.Bans()))
	}
}

// cfgSet is a tiny helper so tests can mutate the engine's backing config and
// then Reload without reaching into globals directly.
func (e *WAFEngine) cfgSet(key, value string) {
	if cfg != nil {
		cfg.Set(key, value)
	}
}
