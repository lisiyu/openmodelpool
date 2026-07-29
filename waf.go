package main

import (
	"encoding/json"
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

	e.enabled = cfg.Get("waf_enabled", "false") == "true"
	e.ipBlacklist = parseListToSet(cfg.Get("waf_ip_blacklist", ""))
	e.uaBlacklist = parseList(cfg.Get("waf_ua_blacklist", ""))
	e.blockedPaths = parseList(cfg.Get("waf_blocked_paths", ""))
	e.contentKw = parseList(cfg.Get("waf_content_keywords", ""))
	e.rateRPS = parseFloat64(cfg.Get("waf_rate_limit_rps", "0"), 0)
	e.rateBurst = parseFloat64(cfg.Get("waf_rate_limit_burst", "0"), 0)
	if e.rateBurst < 1 {
		e.rateBurst = e.rateRPS
		if e.rateBurst < 1 {
			e.rateBurst = 1
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

// CheckContent scans a text payload (e.g. a request body) for blacklisted
// content keywords. It is exposed for handlers that can safely inspect payloads
// (gateway request bodies) without disturbing streaming responses.
func (e *WAFEngine) CheckContent(text string) (bool, string) {
	if !e.Enabled() {
		return true, ""
	}
	lower := strings.ToLower(text)
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, kw := range e.contentKw {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return false, kw
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(ErrorResponse{Error: ErrorDetail{
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
