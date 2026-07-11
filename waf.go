package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ============================================================
// §10A WAF Four-Layer Protection Framework
// ============================================================
//
// Layer 1: Rate Limit — global QPS + per-NodeID QPS + per-IP QPS
// Layer 2: Token Limit — pre-request token estimation guardrails
// Layer 3: Content Safety — L1 hard block / L2 soft block / L3 log-only
// Layer 4: Behavioral — high-frequency repetition / anomaly detection
//
// Escalating enforcement:
//   1 violation → warn (log + Warning header)
//   2 violations → record (write to violation log)
//   3 violations → temp ban (2h)
//   5+ violations → long ban (7d)
//   Extreme violations → permanent ban + gossip broadcast

// maxViolationLog caps the in-memory violation log to prevent unbounded memory growth.
const maxViolationLog = 10000

// WAFManager is the top-level Web Application Firewall manager.
type WAFManager struct {
	mu              sync.RWMutex
	rateLimiter     *WAFRateLimiter
	tokenLimiter    *TokenLimiter
	contentFilter   *ContentFilter
	behaviorMonitor *BehaviorMonitor
	violationLog    []ViolationRecord
	banList         map[string]*BanEntry // key: NodeID or IP → ban
	dataPath        string
	banListPath     string
}

// ViolationRecord captures a single WAF violation event.
type ViolationRecord struct {
	Timestamp time.Time `json:"timestamp"`
	NodeID    string    `json:"node_id,omitempty"`
	IP        string    `json:"ip"`
	Layer     string    `json:"layer"`    // "rate" / "token" / "content" / "behavior"
	Severity  string    `json:"severity"` // "L1" / "L2" / "L3"
	Detail    string    `json:"detail"`
	Action    string    `json:"action"` // "warn" / "record" / "temp_ban" / "long_ban" / "perm_ban"
}

// BanEntry tracks an active ban on a NodeID or IP.
type BanEntry struct {
	NodeID     string        `json:"node_id,omitempty"`
	IP         string        `json:"ip,omitempty"`
	Reason     string        `json:"reason"`
	StartTime  time.Time     `json:"start_time"`
	Duration   time.Duration `json:"duration"` // 0 = permanent
	Violations int           `json:"violations"`
}

// ============================================================
// WAF Configuration (config-driven with sensible defaults)
// ============================================================

type WAFConfig struct {
	// Layer 1: Rate limits
	GlobalQPS     float64
	PerNodeQPS    float64
	PerIPQPM      float64 // queries per minute per IP

	// Layer 2: Token limits
	LargeRequestThreshold int64 // tokens — triggers confirmation (>100K)
	HugeRequestThreshold  int64 // tokens — direct reject (>1M)

	// Layer 3: Content filter
	EnableContentFilter bool

	// Layer 4: Behavioral
	RepetitionWindow   time.Duration // window to count repetitions
	RepetitionThreshold int          // requests before flagging
	AnomalyQPMSThreshold float64     // queries-per-minute anomaly threshold

	// Ban durations
	TempBanDuration time.Duration
	LongBanDuration time.Duration
}

func defaultWAFConfig() WAFConfig {
	return WAFConfig{
		GlobalQPS:     200,
		PerNodeQPS:    50,
		PerIPQPM:      120,
		LargeRequestThreshold:    100000,
		HugeRequestThreshold:     1000000,
		EnableContentFilter:      true,
		RepetitionWindow:         1 * time.Minute,
		RepetitionThreshold:      30,
		AnomalyQPMSThreshold:     100,
		TempBanDuration:          2 * time.Hour,
		LongBanDuration:          7 * 24 * time.Hour,
	}
}

func loadWAFConfig() WAFConfig {
	c := defaultWAFConfig()
	if cfg == nil {
		return c
	}
	c.GlobalQPS = parseFloat64(cfg.Get("waf_global_qps", strconv.FormatFloat(c.GlobalQPS, 'f', -1, 64)), c.GlobalQPS)
	c.PerNodeQPS = parseFloat64(cfg.Get("waf_per_node_qps", strconv.FormatFloat(c.PerNodeQPS, 'f', -1, 64)), c.PerNodeQPS)
	c.PerIPQPM = parseFloat64(cfg.Get("waf_per_ip_qpm", strconv.FormatFloat(c.PerIPQPM, 'f', -1, 64)), c.PerIPQPM)
	c.LargeRequestThreshold, _ = strconv.ParseInt(cfg.Get("waf_large_request_threshold", strconv.FormatInt(c.LargeRequestThreshold, 10)), 10, 64)
	c.HugeRequestThreshold, _ = strconv.ParseInt(cfg.Get("waf_huge_request_threshold", strconv.FormatInt(c.HugeRequestThreshold, 10)), 10, 64)
	c.EnableContentFilter = cfg.Get("waf_enable_content_filter", "true") == "true"
	c.RepetitionThreshold, _ = strconv.Atoi(cfg.Get("waf_repetition_threshold", strconv.Itoa(c.RepetitionThreshold)))
	c.TempBanDuration = parseDurationOrDefault(cfg.Get("waf_temp_ban_duration", "2h"), c.TempBanDuration)
	c.LongBanDuration = parseDurationOrDefault(cfg.Get("waf_long_ban_duration", "168h"), c.LongBanDuration)
	return c
}

// ============================================================
// Global WAF instance
// ============================================================

var wafMgr *WAFManager

func initWAF(dataDir string) {
	config := loadWAFConfig()
	wafMgr = &WAFManager{
		rateLimiter:     newWAFRateLimiter(config),
		tokenLimiter:    newTokenLimiter(config),
		contentFilter:   newContentFilter(config),
		behaviorMonitor: newBehaviorMonitor(config),
		violationLog:    make([]ViolationRecord, 0),
		banList:         make(map[string]*BanEntry),
		banListPath:     filepath.Join(dataDir, "ban_list.json"),
	}
	wafMgr.loadBanList()
	// Start periodic cleanup of expired bans
	go wafMgr.banCleanupLoop()
	slog.Info("WAF manager initialized",
		"global_qps", config.GlobalQPS,
		"per_node_qps", config.PerNodeQPS,
		"per_ip_qpm", config.PerIPQPM,
		"large_request_threshold", config.LargeRequestThreshold,
		"huge_request_threshold", config.HugeRequestThreshold,
		"content_filter", config.EnableContentFilter,
	)
}

// ============================================================
// Layer 1: WAF Rate Limiter
// ============================================================

// WAFRateLimiter manages three tiers of rate limiting.
type WAFRateLimiter struct {
	mu         sync.RWMutex
	global     *RateLimiter
	perNode    map[string]*RateLimiter
	perIP      map[string]*RateLimiter
	config     WAFConfig
}

func newWAFRateLimiter(config WAFConfig) *WAFRateLimiter {
	return &WAFRateLimiter{
		global:  NewRateLimiter(config.GlobalQPS),
		perNode: make(map[string]*RateLimiter),
		perIP:   make(map[string]*RateLimiter),
		config:  config,
	}
}

// CheckRateLimit checks all three rate limit tiers.
// Returns: (allowed, reason, retryAfterSeconds)
func (r *WAFRateLimiter) CheckRateLimit(nodeID string, ip string) (bool, string, int) {
	// Global QPS
	if !r.global.Allow() {
		return false, "global rate limit exceeded", 1
	}

	// Per-NodeID QPS
	if nodeID != "" {
		limiter := r.getNodeLimiter(nodeID)
		if !limiter.Allow() {
			return false, "per-node rate limit exceeded", 1
		}
	}

	// Per-IP QPM
	if ip != "" {
		limiter := r.getIPLimiter(ip)
		if !limiter.Allow() {
			return false, "per-IP rate limit exceeded", 1
		}
	}

	return true, "", 0
}

func (r *WAFRateLimiter) getNodeLimiter(nodeID string) *RateLimiter {
	r.mu.RLock()
	limiter, ok := r.perNode[nodeID]
	r.mu.RUnlock()
	if ok {
		return limiter
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.perNode[nodeID]; ok {
		return l
	}
	limiter = NewRateLimiter(r.config.PerNodeQPS)
	r.perNode[nodeID] = limiter
	return limiter
}

func (r *WAFRateLimiter) getIPLimiter(ip string) *RateLimiter {
	qps := r.config.PerIPQPM / 60.0
	r.mu.RLock()
	limiter, ok := r.perIP[ip]
	r.mu.RUnlock()
	if ok {
		return limiter
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.perIP[ip]; ok {
		return l
	}
	limiter = NewRateLimiterWithBurst(qps, r.config.PerIPQPM)
	r.perIP[ip] = limiter
	return limiter
}

// ============================================================
// Layer 2: Token Limiter
// ============================================================

// TokenLimiter enforces pre-request token estimation limits.
type TokenLimiter struct {
	mu     sync.RWMutex
	config WAFConfig
}

func newTokenLimiter(config WAFConfig) *TokenLimiter {
	return &TokenLimiter{config: config}
}

// CheckTokenLimit evaluates estimated token count against thresholds.
// Returns: (allowed, reason, httpStatus)
//   - <= largeRequestThreshold: allowed
//   - > largeRequestThreshold but <= hugeRequestThreshold: allowed with warning (needs confirmation)
//   - > hugeRequestThreshold: rejected
func (t *TokenLimiter) CheckTokenLimit(estimatedTokens int64) (bool, string, int) {
	if estimatedTokens > t.config.HugeRequestThreshold {
		return false, fmtTokenExceeded("huge", estimatedTokens, t.config.HugeRequestThreshold), http.StatusRequestEntityTooLarge
	}
	if estimatedTokens > t.config.LargeRequestThreshold {
		// Allow but flag for confirmation
		return true, fmtTokenWarning(estimatedTokens, t.config.LargeRequestThreshold), 0
	}
	return true, "", 0
}

func fmtTokenExceeded(category string, got, limit int64) string {
	return category + " request rejected: estimated " + strconv.FormatInt(got, 10) +
		" tokens exceeds limit " + strconv.FormatInt(limit, 10)
}

func fmtTokenWarning(got, threshold int64) string {
	return "large request warning: estimated " + strconv.FormatInt(got, 10) +
		" tokens exceeds threshold " + strconv.FormatInt(threshold, 10)
}

// ============================================================
// Layer 3: Content Filter
// ============================================================

// ContentFilter performs keyword-based content safety checks.
// Phase 1: simple keyword matching.
// Phase 2: AC automaton (future).
type ContentFilter struct {
	mu         sync.RWMutex
	l1Patterns []string // hard block (暴力/违法/CSAM)
	l2Patterns []string // soft block (controversial)
	l3Patterns []string // log-only (edge)
	enabled    bool
}

func newContentFilter(config WAFConfig) *ContentFilter {
	cf := &ContentFilter{
		l1Patterns: defaultL1Patterns(),
		l2Patterns: defaultL2Patterns(),
		l3Patterns: defaultL3Patterns(),
		enabled:    config.EnableContentFilter,
	}
	return cf
}

// CheckContent checks the content against all pattern levels.
// Returns the highest severity level matched and the matched keyword.
// Levels: "L1" (hard block), "L2" (soft block), "L3" (log only), "" (clean).
func (f *ContentFilter) CheckContent(content string) (level string, matched string) {
	if !f.enabled || content == "" {
		return "", ""
	}

	lower := strings.ToLower(content)

	// L1: hard block — check first (highest priority)
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, pattern := range f.l1Patterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return "L1", pattern
		}
	}

	// L2: soft block
	for _, pattern := range f.l2Patterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return "L2", pattern
		}
	}

	// L3: log only
	for _, pattern := range f.l3Patterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return "L3", pattern
		}
	}

	return "", ""
}

// Default content safety pattern lists (Phase 1: keyword matching).
// These are intentionally broad and should be refined in production.

func defaultL1Patterns() []string {
	// L1: Hard block — CSAM, violence, illegal activity instructions
	return []string{
		"child sexual abuse",
		"csam",
		"child exploitation material",
		"how to make a bomb",
		"how to build a bomb",
		"bomb making instructions",
		"how to manufacture illegal drugs",
		"how to synthesize illegal drugs",
		"terrorism attack plan",
		"mass shooting plan",
	}
}

func defaultL2Patterns() []string {
	// L2: Soft block — controversial content requiring rate-limiting
	return []string{
		"hack into",
		"exploit vulnerability",
		"social engineering attack",
		"phishing campaign",
		"ddos attack",
		"credential stuffing",
	}
}

func defaultL3Patterns() []string {
	// L3: Log only — edge content, monitor only
	return []string{
		"jailbreak",
		"prompt injection",
		"system prompt leak",
		"ignore previous instructions",
	}
}

// ============================================================
// Layer 4: Behavioral Monitor
// ============================================================

// BehaviorMonitor tracks request patterns for anomaly detection.
type BehaviorMonitor struct {
	mu          sync.RWMutex
	requestLog  map[string]*requestPattern // key: NodeID or IP
	config      WAFConfig
}

type requestPattern struct {
	RecentRequests []time.Time
	LastContent    string
	RepeatCount    int
}

func newBehaviorMonitor(config WAFConfig) *BehaviorMonitor {
	return &BehaviorMonitor{
		requestLog: make(map[string]*requestPattern),
		config:     config,
	}
}

// CheckBehavior analyzes request patterns for anomalies.
// Returns: (suspicious, reason)
func (b *BehaviorMonitor) CheckBehavior(key string, contentHash string) (bool, string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	pattern, ok := b.requestLog[key]
	if !ok {
		b.requestLog[key] = &requestPattern{
			RecentRequests: []time.Time{time.Now()},
			LastContent:    contentHash,
			RepeatCount:    0,
		}
		return false, ""
	}

	now := time.Now()
	pattern.RecentRequests = append(pattern.RecentRequests, now)

	// Prune requests outside the window
	cutoff := now.Add(-b.config.RepetitionWindow)
	pruned := make([]time.Time, 0, len(pattern.RecentRequests))
	for _, t := range pattern.RecentRequests {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	pattern.RecentRequests = pruned

	// Check: high-frequency repetition
	if len(pattern.RecentRequests) >= b.config.RepetitionThreshold {
		return true, "high-frequency repetitive requests detected"
	}

	// Check: same content repeated many times
	if contentHash == pattern.LastContent {
		pattern.RepeatCount++
		if pattern.RepeatCount >= 10 {
			return true, "identical request repeated excessively"
		}
	} else {
		pattern.RepeatCount = 0
		pattern.LastContent = contentHash
	}

	return false, ""
}

// ============================================================
// WAF Manager — Main Check Entry Point
// ============================================================

// CheckRequest performs all four WAF layers on an incoming request.
// Returns: (allowed, rejectReason, httpStatusCode).
// If allowed, rejectReason may contain a warning (non-blocking).
func (w *WAFManager) CheckRequest(r *http.Request, nodeID string) (bool, string, int) {
	if w == nil {
		return true, "", 0
	}

	ip := extractClientIP(r.RemoteAddr)

	// Check ban list first
	if banned, entry := w.IsBanned(nodeID, ip); banned {
		slog.Warn("WAF: request from banned entity",
			"node_id", nodeID,
			"ip", ip,
			"reason", entry.Reason,
		)
		return false, "access denied: " + entry.Reason, http.StatusForbidden
	}

	// Layer 1: Rate Limit
	if allowed, reason, _ := w.rateLimiter.CheckRateLimit(nodeID, ip); !allowed {
		w.RecordViolation(ViolationRecord{
			Timestamp: time.Now(),
			NodeID:    nodeID,
			IP:        ip,
			Layer:     "rate",
			Severity:  "L2",
			Detail:    reason,
		})
		return false, reason, http.StatusTooManyRequests
	}

	// Layer 2: Token Limit (checked if content-length suggests a large request)
	// Note: actual token estimation happens at the handler level;
	// here we check the Content-Length header as a proxy.
	if r.ContentLength > 0 {
		// Rough heuristic: 1 byte ≈ 0.25 tokens for ASCII text
		estimatedTokens := int64(r.ContentLength) / 4
		allowed, limitReason, status := w.tokenLimiter.CheckTokenLimit(estimatedTokens)
		if !allowed {
			w.RecordViolation(ViolationRecord{
				Timestamp: time.Now(),
				NodeID:    nodeID,
				IP:        ip,
				Layer:     "token",
				Severity:  "L2",
				Detail:    limitReason,
			})
			return false, limitReason, status
		}
		// If warning, we still allow but the caller can add a header
		if limitReason != "" {
			r.Header.Set("X-WAF-Warning", limitReason)
		}
	}

	// Layer 3: Content Safety
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		// For POST/PUT, we check the URL path as a lightweight proxy.
		// Full body scanning should be done at the handler level with CheckContent().
		contentHint := r.URL.Path
		if level, matched := w.contentFilter.CheckContent(contentHint); level == "L1" {
			w.RecordViolation(ViolationRecord{
				Timestamp: time.Now(),
				NodeID:    nodeID,
				IP:        ip,
				Layer:     "content",
				Severity:  "L1",
				Detail:    "L1 content match: " + matched,
			})
			return false, "request blocked by content safety policy", http.StatusBadRequest
		}
	}

	// Layer 4: Behavioral Analysis
	key := ip
	if nodeID != "" {
		key = nodeID
	}
	if suspicious, reason := w.behaviorMonitor.CheckBehavior(key, r.URL.Path); suspicious {
		w.RecordViolation(ViolationRecord{
			Timestamp: time.Now(),
			NodeID:    nodeID,
			IP:        ip,
			Layer:     "behavior",
			Severity:  "L2",
			Detail:    reason,
		})
		// Don't block, but flag for rate reduction
		r.Header.Set("X-WAF-Warning", "behavioral anomaly: "+reason)
	}

	return true, "", 0
}

// CheckContentBody allows handlers to check request body content against content filter.
// This should be called after reading the request body.
func (w *WAFManager) CheckContentBody(content string, nodeID string, ip string) (bool, string, int) {
	if w == nil || !w.contentFilter.enabled {
		return true, "", 0
	}

	level, matched := w.contentFilter.CheckContent(content)
	switch level {
	case "L1":
		w.RecordViolation(ViolationRecord{
			Timestamp: time.Now(),
			NodeID:    nodeID,
			IP:        ip,
			Layer:     "content",
			Severity:  "L1",
			Detail:    "L1 content match: " + matched,
		})
		return false, "request blocked by content safety policy (L1: " + matched + ")", http.StatusBadRequest
	case "L2":
		w.RecordViolation(ViolationRecord{
			Timestamp: time.Now(),
			NodeID:    nodeID,
			IP:        ip,
			Layer:     "content",
			Severity:  "L2",
			Detail:    "L2 content match: " + matched,
		})
		// Soft block: allow but flag and rate-limit
		return true, "content flagged (L2: " + matched + ")", 0
	case "L3":
		// Log only
		slog.Info("WAF: L3 content detected",
			"node_id", nodeID,
			"ip", ip,
			"matched", matched,
		)
	}
	return true, "", 0
}

// ============================================================
// Violation & Ban Management
// ============================================================

// RecordViolation records a WAF violation and applies escalating enforcement.
func (w *WAFManager) RecordViolation(record ViolationRecord) {
	if w == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.violationLog = append(w.violationLog, record)

	// Cap violation log size: discard oldest entries when over capacity
	if len(w.violationLog) > maxViolationLog {
		w.violationLog = w.violationLog[len(w.violationLog)-maxViolationLog:]
	}

	// Count recent violations for the entity
	key := record.IP
	if record.NodeID != "" {
		key = record.NodeID
	}

	recentCount := w.countRecentViolationsLocked(key, 24*time.Hour)

	// Determine enforcement action
	action := "record"
	switch {
	case record.Severity == "L1" && recentCount >= 3:
		action = "perm_ban"
	case recentCount >= 5:
		action = "long_ban"
	case recentCount >= 3:
		action = "temp_ban"
	case recentCount >= 2:
		action = "record"
	default:
		action = "warn"
	}

	record.Action = action

	slog.Warn("WAF violation recorded",
		"layer", record.Layer,
		"severity", record.Severity,
		"detail", record.Detail,
		"action", action,
		"key", key,
		"recent_count", recentCount,
	)

	// Apply ban if needed
	banApplied := false
	switch action {
	case "temp_ban":
		w.banList[key] = &BanEntry{
			NodeID:    record.NodeID,
			IP:        record.IP,
			Reason:    record.Detail,
			StartTime: time.Now(),
			Duration:  w.rateLimiter.config.TempBanDuration,
			Violations: recentCount,
		}
		banApplied = true
	case "long_ban":
		w.banList[key] = &BanEntry{
			NodeID:    record.NodeID,
			IP:        record.IP,
			Reason:    record.Detail,
			StartTime: time.Now(),
			Duration:  w.rateLimiter.config.LongBanDuration,
			Violations: recentCount,
		}
		banApplied = true
	case "perm_ban":
		w.banList[key] = &BanEntry{
			NodeID:    record.NodeID,
			IP:        record.IP,
			Reason:    "extreme violation: " + record.Detail,
			StartTime: time.Now(),
			Duration:  0, // permanent
			Violations: recentCount,
		}
		banApplied = true
		// TODO: Gossip broadcast NodeID for permanent bans
		slog.Error("WAF: permanent ban applied",
			"node_id", record.NodeID,
			"ip", record.IP,
			"reason", record.Detail,
		)
	}
	if banApplied {
		w.saveBanList()
	}
}

// IsBanned checks whether a NodeID or IP is currently banned.
func (w *WAFManager) IsBanned(nodeID string, ip string) (bool, *BanEntry) {
	if w == nil {
		return false, nil
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	// Check by NodeID first
	if nodeID != "" {
		if entry, ok := w.banList[nodeID]; ok {
			if w.isBanActive(entry) {
				return true, entry
			}
		}
	}

	// Check by IP
	if ip != "" {
		if entry, ok := w.banList[ip]; ok {
			if w.isBanActive(entry) {
				return true, entry
			}
		}
	}

	return false, nil
}

func (w *WAFManager) isBanActive(entry *BanEntry) bool {
	if entry.Duration == 0 {
		return true // permanent
	}
	return time.Since(entry.StartTime) < entry.Duration
}

// countRecentViolationsLocked counts violations for a key within the given duration.
// Caller must hold w.mu.
func (w *WAFManager) countRecentViolationsLocked(key string, window time.Duration) int {
	cutoff := time.Now().Add(-window)
	count := 0
	// Iterate from newest to oldest; stop once we exit the time window
	for i := len(w.violationLog) - 1; i >= 0; i-- {
		v := w.violationLog[i]
		if v.Timestamp.Before(cutoff) {
			break // all remaining entries are even older
		}
		if v.NodeID == key || v.IP == key {
			count++
		}
	}
	return count
}

// GetViolationLog returns recent violation records.
func (w *WAFManager) GetViolationLog(limit int) []ViolationRecord {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()

	n := len(w.violationLog)
	if n > limit {
		n = limit
	}
	result := make([]ViolationRecord, n)
	copy(result, w.violationLog[len(w.violationLog)-n:])
	return result
}

// GetBanList returns all active bans.
func (w *WAFManager) GetBanList() []BanEntry {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()

	var result []BanEntry
	for _, entry := range w.banList {
		if w.isBanActive(entry) {
			result = append(result, *entry)
		}
	}
	return result
}

// CleanupExpiredBans removes expired bans from the ban list.
func (w *WAFManager) CleanupExpiredBans() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	for key, entry := range w.banList {
		if entry.Duration > 0 && time.Since(entry.StartTime) >= entry.Duration {
			delete(w.banList, key)
			slog.Info("WAF: expired ban removed", "key", key)
		}
	}
}


// saveBanList persists the current ban list to disk.
// Caller must hold w.mu (or call from a context where locking is handled).
func (w *WAFManager) saveBanList() {
	if w.banListPath == "" {
		return
	}
	type banEntryStore struct {
		NodeID     string        `json:"node_id,omitempty"`
		IP         string        `json:"ip,omitempty"`
		Reason     string        `json:"reason"`
		StartTime  time.Time     `json:"start_time"`
		Duration   int64         `json:"duration_ns"` // duration in nanoseconds; 0 = permanent
		Violations int           `json:"violations"`
	}
	type banListStore struct {
		Bans []banEntryStore `json:"bans"`
	}
	store := banListStore{Bans: make([]banEntryStore, 0, len(w.banList))}
	for _, entry := range w.banList {
		store.Bans = append(store.Bans, banEntryStore{
			NodeID:     entry.NodeID,
			IP:         entry.IP,
			Reason:     entry.Reason,
			StartTime:  entry.StartTime,
			Duration:   int64(entry.Duration),
			Violations: entry.Violations,
		})
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		slog.Error("WAF: failed to marshal ban list", "error", err)
		return
	}
	os.MkdirAll(filepath.Dir(w.banListPath), 0755)
	if err := atomicWriteFile(w.banListPath, data, 0600); err != nil {
		slog.Error("WAF: failed to write ban list", "error", err)
	}
}

// loadBanList restores the ban list from disk on startup.
func (w *WAFManager) loadBanList() {
	if w.banListPath == "" {
		return
	}
	data, err := os.ReadFile(w.banListPath)
	if err != nil {
		// File doesn't exist yet — that's fine
		return
	}
	type banEntryStore struct {
		NodeID     string    `json:"node_id,omitempty"`
		IP         string    `json:"ip,omitempty"`
		Reason     string    `json:"reason"`
		StartTime  time.Time `json:"start_time"`
		Duration   int64     `json:"duration_ns"`
		Violations int       `json:"violations"`
	}
	type banListStore struct {
		Bans []banEntryStore `json:"bans"`
	}
	var store banListStore
	if err := json.Unmarshal(data, &store); err != nil {
		slog.Warn("WAF: failed to parse ban list file", "error", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, bs := range store.Bans {
		key := bs.NodeID
		if key == "" {
			key = bs.IP
		}
		if key == "" {
			continue
		}
		// Only restore non-expired bans
		entry := &BanEntry{
			NodeID:     bs.NodeID,
			IP:         bs.IP,
			Reason:     bs.Reason,
			StartTime:  bs.StartTime,
			Duration:   time.Duration(bs.Duration),
			Violations: bs.Violations,
		}
		// Skip expired temporary bans
		if entry.Duration > 0 && time.Since(entry.StartTime) >= entry.Duration {
			continue
		}
		w.banList[key] = entry
	}
	slog.Info("WAF: ban list loaded", "count", len(w.banList))
}

// banCleanupLoop periodically cleans up expired bans and re-saves the list.
func (w *WAFManager) banCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		w.mu.Lock()
		changed := false
		for key, entry := range w.banList {
			if entry.Duration > 0 && time.Since(entry.StartTime) >= entry.Duration {
				delete(w.banList, key)
				slog.Info("WAF: expired ban removed", "key", key)
				changed = true
			}
		}
		if changed {
			w.saveBanList()
		}
		w.mu.Unlock()
	}
}

// ============================================================
// WAF Middleware
// ============================================================

// wafMiddleware wraps a handler with WAF checking.
// It should be placed after auth middleware so nodeID is available.
func wafMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wafMgr == nil {
			next(w, r)
			return
		}

		// Extract nodeID from request context (set by auth middleware)
		nodeID := r.Header.Get("X-Request-Owner")

		allowed, reason, status := wafMgr.CheckRequest(r, nodeID)
		if !allowed {
			// Add warning header even on rejection
			w.Header().Set("X-WAF-Status", "blocked")
			writeJSON(w, status, ErrorResponse{Error: ErrorDetail{
				Message: reason,
				Type:    "waf_error",
				Code:    "waf_blocked",
			}})
			return
		}

		// Pass through any warnings
		if warning := r.Header.Get("X-WAF-Warning"); warning != "" {
			w.Header().Set("X-WAF-Warning", warning)
		}
		w.Header().Set("X-WAF-Status", "passed")

		next(w, r)
	}
}

// ============================================================
// Helpers
// ============================================================

// parseDurationOrDefault parses a Go duration string, falling back to default.
func parseDurationOrDefault(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

// contentHash returns a simple hash of content for behavioral analysis.
// This is intentionally lightweight — not cryptographic.
func contentHash(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// ============================================================
// WAF API Handlers
// ============================================================

// GET /api/waf/status — WAF status and configuration
func handleWAFStatus(w http.ResponseWriter, r *http.Request) {
	if wafMgr == nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}

	wafMgr.mu.RLock()
	activeBans := 0
	for _, entry := range wafMgr.banList {
		if wafMgr.isBanActive(entry) {
			activeBans++
		}
	}
	wafMgr.mu.RUnlock()

	writeJSON(w, 200, map[string]any{
		"enabled":          true,
		"violation_count":  len(wafMgr.GetViolationLog(1000)),
		"active_bans":      activeBans,
	})
}

// GET /api/waf/violations — recent WAF violations
func handleWAFViolations(w http.ResponseWriter, r *http.Request) {
	if wafMgr == nil {
		writeJSON(w, 200, map[string]any{"violations": []any{}})
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	violations := wafMgr.GetViolationLog(limit)
	writeJSON(w, 200, map[string]any{
		"violations": violations,
		"count":      len(violations),
	})
}

// GET /api/waf/bans — active WAF bans
func handleWAFBans(w http.ResponseWriter, r *http.Request) {
	if wafMgr == nil {
		writeJSON(w, 200, map[string]any{"bans": []any{}})
		return
	}

	bans := wafMgr.GetBanList()
	writeJSON(w, 200, map[string]any{
		"bans":  bans,
		"count": len(bans),
	})
}

// POST /api/waf/unban/{key} — remove a ban entry
func handleWAFUnban(w http.ResponseWriter, r *http.Request) {
	if wafMgr == nil {
		writeError(w, 500, "WAF not initialized")
		return
	}

	key := r.PathValue("key")
	if key == "" {
		writeError(w, 400, "key parameter is required")
		return
	}

	wafMgr.mu.Lock()
	delete(wafMgr.banList, key)
	wafMgr.saveBanList()
	wafMgr.mu.Unlock()

	slog.Info("WAF: ban removed by admin", "key", key)
	writeJSON(w, 200, map[string]any{"status": "unbanned", "key": key})
}
