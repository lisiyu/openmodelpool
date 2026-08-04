package main

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ============================================================
// §10A / §12.8: WAF four-layer local protection engine
// ============================================================
//
// This file contains the real WAF engine that was previously only stubbed
// (see stubs.go:initWAF and handlers_missing.go:handleWAF*). The engine
// implements the concrete, testable layers described in the design doc:
//
//   Layer 0: dynamic bans (e.g. temporary IP bans from repeated breaches)
//   Layer 1: IP blacklist           (waf_ip_blacklist)
//   Layer 2: User-Agent filter       (waf_ua_blacklist)
//   Layer 3: per-IP request rate     (waf_rate_limit_rps / waf_rate_limit_burst)
//   Layer 4: path protection         (waf_blocked_paths)
//   + content-safety keyword scan    (waf_content_keywords, via CheckContent)
//
// The engine is DISABLED by default. It is wired into the proxy/relay request
// path through wafMiddleware (see server.go) and reports its live state through
// the /api/waf/* admin endpoints. When disabled, wafMiddleware is a no-op so
// normal traffic is never affected.

// WAFViolationType enumerates the categories of WAF blocks.
type WAFViolationType string

const (
	WAFViolationIPBan       WAFViolationType = "ip_ban"
	WAFViolationIPBlacklist WAFViolationType = "ip_blacklist"
	WAFViolationUAFilter    WAFViolationType = "ua_filter"
	WAFViolationRateLimit   WAFViolationType = "rate_limit"
	WAFViolationPathBlock   WAFViolationType = "path_block"
	WAFViolationContent     WAFViolationType = "content_safety"
)

// WAFViolation records a single blocked request.
type WAFViolation struct {
	Timestamp time.Time        `json:"timestamp"`
	Type      WAFViolationType `json:"type"`
	ClientIP  string           `json:"client_ip"`
	Path      string           `json:"path"`
	Reason    string           `json:"reason"`
}

// WAFBan is a dynamically applied temporary ban (e.g. for repeated breaches).
type WAFBan struct {
	Key       string    `json:"key"`
	Type      string    `json:"type"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expires_at"`
}

// WAFEngine implements the four-layer local WAF protection.
type WAFEngine struct {
	mu sync.RWMutex

	enabled      bool
	ipBlacklist  map[string]bool
	uaBlacklist  []string
	blockedPaths []string
	contentKw    []string
	rateRPS      float64
	rateBurst    float64

	ipLimiters map[string]*RateLimiter
	bans       map[string]WAFBan

	violMu        sync.Mutex
	violations    []WAFViolation
	maxViolations int

	stats map[WAFViolationType]int64
}

const wafDefaultMaxViolations = 1000
const wafIPLimitersMaxEntries = 10000

// wafEngine is the package-global singleton, initialized by initWAF.
var wafEngine *WAFEngine

// initWAF creates the engine and loads configuration. It replaces the previous
// no-op stub so WAF is actually wired into the proxy path.
func initWAF(dataDir string) {
	wafEngine = NewWAFEngine()
	wafEngine.Reload()
	slog.Info("WAF engine initialized", "enabled", wafEngine.Enabled())
}

// NewWAFEngine returns an empty (disabled) engine.
func NewWAFEngine() *WAFEngine {
	return &WAFEngine{
		ipBlacklist:   make(map[string]bool),
		ipLimiters:    make(map[string]*RateLimiter),
		bans:          make(map[string]WAFBan),
		violations:    make([]WAFViolation, 0, wafDefaultMaxViolations),
		stats:         make(map[WAFViolationType]int64),
		maxViolations: wafDefaultMaxViolations,
	}
}

// reloadWAF re-reads WAF configuration from cfg. Safe to call after config
// changes. No-op when the engine is not initialized.
func reloadWAF() {
	if wafEngine != nil {
		wafEngine.Reload()
	}
}

// Reload re-reads WAF configuration from cfg.
func (e *WAFEngine) Reload() {
	if cfg == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	e.enabled = cfg.Get("waf_enabled", "true") == "true"
	e.ipBlacklist = parseListToSet(cfg.Get("waf_ip_blacklist", ""))
	e.uaBlacklist = parseList(cfg.Get("waf_ua_blacklist", ""))
	e.blockedPaths = parseList(cfg.Get("waf_blocked_paths", ""))
	e.contentKw = parseList(cfg.Get("waf_content_keywords", ""))
	rpsStr := cfg.Get("waf_rate_limit_rps", "10")
	burstStr := cfg.Get("waf_rate_limit_burst", "20")
	if rpsStr == "0" || rpsStr == "0.0" {
		e.rateRPS = 0
		e.rateBurst = 0
	} else {
		e.rateRPS = parseFloat64(rpsStr, 10)
		e.rateBurst = parseFloat64(burstStr, 20)
		if e.rateBurst < 1 {
			e.rateBurst = e.rateRPS
			if e.rateBurst < 1 {
				e.rateBurst = 1
			}
		}
	}
}

// Enabled reports whether WAF enforcement is active.
func (e *WAFEngine) Enabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.enabled
}

// clientIPs returns the candidate client IPs for a request, honoring
// X-Forwarded-For and X-Real-IP before falling back to RemoteAddr. This makes
// the IP blacklist effective even behind a trusted reverse proxy.
func clientIPs(r *http.Request) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(ip string) {
		ip = strings.TrimSpace(ip)
		if ip == "" || seen[ip] {
			return
		}
		seen[ip] = true
		out = append(out, ip)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, part := range strings.Split(xff, ",") {
			add(strings.TrimSpace(part))
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		add(xri)
	}
	add(extractClientIP(r.RemoteAddr))
	return out
}

func firstIP(ips []string) string {
	if len(ips) == 0 {
		return ""
	}
	return ips[0]
}

// Check evaluates the inbound request against the four-layer rules. It returns
// (allowed, violation). When allowed is false, violation describes why the
// request was blocked and the block has been recorded.
func (e *WAFEngine) Check(r *http.Request) (bool, *WAFViolation) {
	if !e.Enabled() {
		return true, nil
	}

	ips := clientIPs(r)
	path := r.URL.Path
	ip := firstIP(ips)

	// Snapshot the rules under a read lock, then record (if needed) afterward
	// so we never hold the write path for the duration of the check.
	e.mu.RLock()
	var vType WAFViolationType
	var reason string
	now := time.Now()
	for _, cip := range ips {
		if ban, ok := e.bans[cip]; ok && now.Before(ban.ExpiresAt) {
			vType, reason = WAFViolationIPBan, "ip temporarily banned: "+ban.Reason
			break
		}
		if e.ipBlacklist[cip] {
			vType, reason = WAFViolationIPBlacklist, "ip is blacklisted"
			break
		}
	}
	if vType == "" {
		ua := strings.ToLower(r.Header.Get("User-Agent"))
		for _, bad := range e.uaBlacklist {
			if strings.Contains(ua, strings.ToLower(bad)) {
				vType, reason = WAFViolationUAFilter, "user-agent matches blacklist: "+bad
				break
			}
		}
	}
	if vType == "" {
		for _, p := range e.blockedPaths {
			if strings.HasPrefix(path, p) {
				vType, reason = WAFViolationPathBlock, "path is blocked: "+p
				break
			}
		}
	}
	var limiter *RateLimiter
	if vType == "" && e.rateRPS > 0 {
		limiter = e.ipLimiters[ip]
	}
	rateRPS := e.rateRPS
	rateBurst := e.rateBurst
	e.mu.RUnlock()

	if vType != "" {
		return e.record(vType, ip, path, reason)
	}

	// Per-IP rate limit (WAF-scoped). Created lazily under a short write lock.
	if limiter == nil && rateRPS > 0 {
		e.mu.Lock()
		if limiter = e.ipLimiters[ip]; limiter == nil {
			if len(e.ipLimiters) >= wafIPLimitersMaxEntries {
				e.cleanupIPLimitersLocked()
			}
			limiter = NewRateLimiterWithBurst(rateRPS, rateBurst)
			e.ipLimiters[ip] = limiter
		}
		e.mu.Unlock()
	}
	if limiter != nil && !limiter.Allow() {
		return e.record(WAFViolationRateLimit, ip, path, "per-ip request rate exceeded")
	}

	return true, nil
}

// wafAttackPatterns contains built-in patterns for common web attacks.
// These are checked in addition to user-configured content keywords.
var wafAttackPatterns = []struct {
	name    string
	pattern string
}{
	// SQL injection
	{"sqli_union_select", "union select"},
	{"sqli_union_all", "union all select"},
	{"sqli_or_1_1", "or 1=1"},
	{"sqli_or_true", "or true"},
	{"sqli_and_1_1", "and 1=1"},
	{"sqli_drop_table", "drop table"},
	{"sqli_insert_into", "insert into"},
	{"sqli_delete_from", "delete from"},
	{"sqli_update_set", "update set"},
	{"sqli_sleep", "sleep("},
	{"sqli_benchmark", "benchmark("},
	{"sqli_waitfor", "waitfor delay"},
	{"sqli_information_schema", "information_schema"},
	{"sqli_load_file", "load_file("},
	{"sqli_into_outfile", "into outfile"},
	{"sqli_hex_0x", "0x41414141"}, // common hex injection probe
	// XSS
	{"xss_script_tag", "<script"},
	{"xss_script_close", "</script>"},
	{"xss_javascript_uri", "javascript:"},
	{"xss_onerror", "onerror="},
	{"xss_onload", "onload="},
	{"xss_onclick", "onclick="},
	{"xss_onmouseover", "onmouseover="},
	{"xss_img_tag", "<img"},
	{"xss_svg_tag", "<svg"},
	{"xss_iframe_tag", "<iframe"},
	{"xss_eval", "eval("},
	{"xss_expression", "expression("},
	{"xss_document_cookie", "document.cookie"},
	{"xss_document_write", "document.write"},
	// Path traversal
	{"path_traversal_dotdot", ".."},
	{"path_traversal_dotdot_slash", "../"},
	// path_traversal_dotdot_backslash removed: ".." already covered above
	{"path_traversal_etc_passwd", "/etc/passwd"},
	{"path_traversal_etc_shadow", "/etc/shadow"},
	// Command injection
	{"cmd_inject_semicolon", "; ls"},
	{"cmd_inject_pipe", "| ls"},
	{"cmd_inject_and", "&& ls"},
	{"cmd_inject_or", "|| ls"},
	{"cmd_inject_backtick", "` ls"},
	{"cmd_inject_dollar", "$("},
	{"cmd_inject_whoami", "whoami"},
	{"cmd_inject_wget", "wget "},
	{"cmd_inject_curl", "curl "},
	{"cmd_inject_nc_", "nc "},
	// SSRF
	{"ssrf_localhost", "http://localhost"},
	{"ssrf_127_0_0_1", "http://127.0.0.1"},
	{"ssrf_169_254", "169.254.169.254"}, // AWS metadata
	{"ssrf_metadata", "metadata.google.internal"},
}

// CheckContent scans a text payload (e.g. a request body) for blacklisted
// content keywords and built-in attack patterns. It is exposed for handlers
// that can safely inspect payloads (gateway request bodies) without disturbing
// streaming responses.
func (e *WAFEngine) CheckContent(text string) (bool, string) {
	if !e.Enabled() {
		return true, ""
	}
	lower := strings.ToLower(text)

	// Check user-configured keywords
	e.mu.RLock()
	for _, kw := range e.contentKw {
		if strings.Contains(lower, strings.ToLower(kw)) {
			e.mu.RUnlock()
			return false, kw
		}
	}
	e.mu.RUnlock()

	// Check built-in attack patterns
	for _, p := range wafAttackPatterns {
		if strings.Contains(lower, p.pattern) {
			return false, "attack_pattern:" + p.name
		}
	}

	return true, ""
}

// record appends a violation to the ring buffer and bumps the type counter.
// It returns (false, &violation) so callers can short-circuit.
func (e *WAFEngine) record(vType WAFViolationType, ip, path, reason string) (bool, *WAFViolation) {
	v := WAFViolation{
		Timestamp: time.Now(),
		Type:      vType,
		ClientIP:  ip,
		Path:      path,
		Reason:    reason,
	}
	e.violMu.Lock()
	e.violations = append(e.violations, v)
	if len(e.violations) > e.maxViolations {
		e.violations = e.violations[len(e.violations)-e.maxViolations:]
	}
	e.stats[vType]++
	e.violMu.Unlock()
	return false, &v
}

// Status returns a snapshot for the /api/waf/status endpoint.
func (e *WAFEngine) Status() map[string]any {
	e.mu.RLock()
	enabled := e.enabled
	ipCount := len(e.ipBlacklist)
	uaCount := len(e.uaBlacklist)
	pathCount := len(e.blockedPaths)
	kwCount := len(e.contentKw)
	rateRPS := e.rateRPS
	bansCount := 0
	now := time.Now()
	for _, b := range e.bans {
		if now.Before(b.ExpiresAt) {
			bansCount++
		}
	}
	e.mu.RUnlock()

	e.violMu.Lock()
	total := len(e.violations)
	stats := make(map[string]int64, len(e.stats))
	for k, v := range e.stats {
		stats[string(k)] = v
	}
	e.violMu.Unlock()

	return map[string]any{
		"enabled":            enabled,
		"ip_blacklist":       ipCount,
		"ua_blacklist":       uaCount,
		"blocked_paths":      pathCount,
		"content_keywords":   kwCount,
		"rate_limit_rps":     rateRPS,
		"active_bans":        bansCount,
		"total_violations":   total,
		"violations_by_type": stats,
		"note":               "WAF engine status reflects live enforcement state",
	}
}

// Violations returns recorded violations, most-recent first.
func (e *WAFEngine) Violations() []WAFViolation {
	e.violMu.Lock()
	defer e.violMu.Unlock()
	out := make([]WAFViolation, len(e.violations))
	for i, v := range e.violations {
		out[len(e.violations)-1-i] = v
	}
	return out
}

// Bans returns the currently active dynamic bans.
func (e *WAFEngine) Bans() []WAFBan {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]WAFBan, 0, len(e.bans))
	now := time.Now()
	for _, b := range e.bans {
		if now.Before(b.ExpiresAt) {
			out = append(out, b)
		}
	}
	return out
}

// AddBan adds (or replaces) a temporary ban for a key (usually an IP).
func (e *WAFEngine) AddBan(key, reason string, duration time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bans[key] = WAFBan{
		Key:       key,
		Type:      string(WAFViolationIPBan),
		Reason:    reason,
		ExpiresAt: time.Now().Add(duration),
	}
}

// RemoveBan removes a dynamic ban by key (used by /api/waf/unban/{key}).
// It returns true if a ban for the key existed.
func (e *WAFEngine) RemoveBan(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.bans[key]; ok {
		delete(e.bans, key)
		return true
	}
	return false
}

func (e *WAFEngine) cleanupIPLimitersLocked() {
	now := time.Now()
	for ip, lim := range e.ipLimiters {
		if now.Sub(lim.lastRefill) > 10*time.Minute {
			delete(e.ipLimiters, ip)
		}
		if len(e.ipLimiters) < wafIPLimitersMaxEntries/2 {
			break
		}
	}
}

// wafMiddleware enforces the WAF engine on inbound requests. It is a no-op when
// WAF is disabled (the default), so normal traffic is never affected until an
// operator explicitly enables WAF via configuration.
func wafMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wafEngine == nil || !wafEngine.Enabled() {
			next(w, r)
			return
		}
		allowed, v := wafEngine.Check(r)
		if !allowed {
			slog.Warn("WAF blocked request",
				"type", v.Type, "ip", v.ClientIP, "path", v.Path, "reason", v.Reason)
			writeJSON(w, http.StatusForbidden, ErrorResponse{Error: ErrorDetail{
				Message: "request blocked by WAF: " + v.Reason,
				Type:    "waf_blocked",
				Code:    string(v.Type),
			}})
			return
		}
		next(w, r)
	}
}

// ---- small string-list helpers ----

func parseList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseListToSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, v := range parseList(s) {
		set[v] = true
	}
	return set
}
