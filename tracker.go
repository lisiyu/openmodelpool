package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/smtp"
	"os"
	"sync"
	"time"
)

// Tracker records API usage with batched disk writes and EWMA latency cache.
type Tracker struct {
	mu           sync.Mutex
	records      []UsageRecord
	dirtyCount   int
	lastFlush    time.Time
	ewmaCache    map[string]float64
	dataPath     string
	stopCh       chan struct{}

	// Request log ring buffer
	reqLogMu     sync.RWMutex
	reqLog       []RequestLogEntry
	reqLogMax    int

	// Token budget alert thresholds (percentage)
	alertThresholds []float64
	alertedTokens   map[string]map[float64]bool // providerID -> threshold -> alerted
}

const (
	trackerFlushInterval = 3 * time.Second
	trackerFlushThreshold = 50
	trackerMaxRecords    = 5000
	ewmaAlpha            = 0.3
)

var tracker *Tracker

func initTracker(path string) {
	tracker = &Tracker{
		dataPath:        path,
		ewmaCache:       make(map[string]float64),
		lastFlush:       time.Now(),
		stopCh:          make(chan struct{}),
		reqLogMax:       1000,
		alertThresholds: []float64{0.8, 0.9, 1.0},
		alertedTokens:   make(map[string]map[float64]bool),
	}
	tracker.load()
	go tracker.periodicFlush()
	go tracker.monthlyArchiveLoop()
}

func (t *Tracker) load() {
	b, err := os.ReadFile(t.dataPath)
	if err != nil {
		return
	}
	json.Unmarshal(b, &t.records)
	slog.Info("usage records loaded", "count", len(t.records))
	t.rebuildEWMA()
}

func (t *Tracker) rebuildEWMA() {
	// Group latencies by provider
	providerLats := make(map[string][]float64)
	for _, r := range t.records {
		if r.Success && r.LatencyMS > 0 {
			providerLats[r.ProviderID] = append(providerLats[r.ProviderID], r.LatencyMS)
		}
	}
	for pid, lats := range providerLats {
		recent := lats
		if len(recent) > 20 {
			recent = recent[len(recent)-20:]
		}
		ewma := recent[0]
		for _, v := range recent[1:] {
			ewma = ewmaAlpha*v + (1-ewmaAlpha)*ewma
		}
		t.ewmaCache[pid] = round1(ewma)
	}
}

func (t *Tracker) save() {
	if len(t.records) > trackerMaxRecords {
		t.records = t.records[len(t.records)-trackerMaxRecords:]
	}
	b, _ := json.MarshalIndent(t.records, "", "  ")
	os.MkdirAll("data", 0755)
	os.WriteFile(t.dataPath, b, 0600)
	t.dirtyCount = 0
	t.lastFlush = time.Now()
}

func (t *Tracker) periodicFlush() {
	ticker := time.NewTicker(trackerFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.mu.Lock()
			if t.dirtyCount > 0 {
				t.save()
			}
			t.mu.Unlock()
		case <-t.stopCh:
			return
		}
	}
}

// Record logs one API call.
func (t *Tracker) Record(providerID, providerName, model string, promptTokens, completionTokens int, latencyMS float64, success bool, errMsg string) {
	t.RecordWithRetry(providerID, providerName, model, promptTokens, completionTokens, latencyMS, success, errMsg, false, 0, "")
}

// RecordWithAccessType logs one API call with access type (private/public/guest/relay).
func (t *Tracker) RecordWithAccessType(providerID, providerName, model string, promptTokens, completionTokens int, latencyMS float64, success bool, errMsg string, isStream bool, retryCount int, accessType string) {
	t.RecordWithRetry(providerID, providerName, model, promptTokens, completionTokens, latencyMS, success, errMsg, isStream, retryCount, accessType)
}

// RecordWithRetry logs one API call with retry and stream info.
func (t *Tracker) RecordWithRetry(providerID, providerName, model string, promptTokens, completionTokens int, latencyMS float64, success bool, errMsg string, isStream bool, retryCount int, accessType string) {
	cost := 0.0
	if success {
		cost = estimateCost(model, promptTokens, completionTokens, providerID)
	}

	now := time.Now().Format(time.RFC3339)
	totalTokens := promptTokens + completionTokens

	entry := UsageRecord{
		Timestamp:        now,
		ProviderID:       providerID,
		ProviderName:     providerName,
		Model:            model,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		CostUSD:          cost,
		LatencyMS:        round1(latencyMS),
		Success:          success,
		Error:            errMsg,
		AccessType:        accessType,
	}

	t.mu.Lock()
	t.records = append(t.records, entry)
	t.dirtyCount++

	// Realtime EWMA update
	if success && latencyMS > 0 {
		prev, ok := t.ewmaCache[providerID]
		if ok {
			t.ewmaCache[providerID] = round1(ewmaAlpha*latencyMS + (1-ewmaAlpha)*prev)
		} else {
			t.ewmaCache[providerID] = round1(latencyMS)
		}
	}

	shouldFlush := t.dirtyCount >= trackerFlushThreshold || time.Since(t.lastFlush) >= trackerFlushInterval
	t.mu.Unlock()

	// Flush outside lock to avoid holding lock during IO
	if shouldFlush {
		t.mu.Lock()
		t.save()
		t.mu.Unlock()
	}

	// Record metrics
	if metrics != nil {
		metrics.RecordRequest(model, providerID, int64(latencyMS), success, totalTokens)
	}

	// Add to request log ring buffer
	t.addRequestLog(RequestLogEntry{
		Timestamp:    now,
		Method:       model,
		Model:        model,
		ProviderID:   providerID,
		ProviderName: providerName,
		Success:      success,
		LatencyMS:    round1(latencyMS),
		Tokens:       totalTokens,
		CostUSD:      cost,
		Error:        errMsg,
		Stream:       isStream,
		RetryCount:   retryCount,
	})

	// Check token budget alerts
	t.checkTokenBudget(providerID, providerName)
}

func (t *Tracker) addRequestLog(entry RequestLogEntry) {
	t.reqLogMu.Lock()
	defer t.reqLogMu.Unlock()
	if len(t.reqLog) >= t.reqLogMax {
		t.reqLog = t.reqLog[len(t.reqLog)-t.reqLogMax+1:]
	}
	t.reqLog = append(t.reqLog, entry)
}

// GetRequestLog returns recent request log entries.
func (t *Tracker) GetRequestLog(limit int) []RequestLogEntry {
	t.reqLogMu.RLock()
	defer t.reqLogMu.RUnlock()
	if limit <= 0 || limit > len(t.reqLog) {
		limit = len(t.reqLog)
	}
	start := len(t.reqLog) - limit
	result := make([]RequestLogEntry, limit)
	copy(result, t.reqLog[start:])
	return result
}

// checkTokenBudget checks if any provider has exceeded token budget thresholds.
func (t *Tracker) checkTokenBudget(providerID, providerName string) {
	raw, ok := pm.GetRaw(providerID)
	if !ok || raw.TokenLimit <= 0 {
		return
	}

	used := t.tokensUsedByProvider(providerID)
	ratio := float64(used) / float64(raw.TokenLimit)

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.alertedTokens[providerID] == nil {
		t.alertedTokens[providerID] = make(map[float64]bool)
	}

	for _, threshold := range t.alertThresholds {
		if ratio >= threshold && !t.alertedTokens[providerID][threshold] {
			t.alertedTokens[providerID][threshold] = true
			pct := int(threshold * 100)
			msg := fmt.Sprintf("⚠️ Token 预算告警：平台 [%s] 已使用 %d%% Token 配额（%d/%d）",
				providerName, pct, used, raw.TokenLimit)
			slog.Warn("token budget alert", "provider", providerID, "threshold", pct, "used", used, "limit", raw.TokenLimit)
			go sendBudgetAlert(providerName, msg)
		}
	}
}

func (t *Tracker) tokensUsedByProvider(providerID string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	var total int64
	for _, r := range t.records {
		if r.ProviderID == providerID {
			total += int64(r.TotalTokens)
		}
	}
	return total
}

// sendBudgetAlert sends a token budget alert email if SMTP is configured.
func sendBudgetAlert(providerName, message string) {
	if !auth.IsSMTPConfigured() {
		slog.Info("token budget alert (SMTP not configured, skipping email)", "message", message)
		return
	}
	adminEmail := auth.GetEmail()
	if adminEmail == "" {
		return
	}
	s := auth.GetSMTP()
	subject := "OpenModelPool Agent Token 预算告警"
	msgBody := fmt.Sprintf("Subject: %s\r\nFrom: %s\r\nTo: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		subject, s.FromEmail, adminEmail, message)
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	var smtpAuth smtp.Auth
	if s.Username != "" {
		smtpAuth = smtp.PlainAuth("", s.Username, s.Password, s.Host)
	}
	var err error
	if s.UseTLS && s.Port == 465 {
		err = sendMailTLS(addr, smtpAuth, s.FromEmail, []string{adminEmail}, []byte(msgBody))
	} else {
		err = smtp.SendMail(addr, smtpAuth, s.FromEmail, []string{adminEmail}, []byte(msgBody))
	}
	if err != nil {
		slog.Error("failed to send budget alert email", "error", err)
	}
}

// monthlyArchiveLoop checks monthly and archives old usage data.
func (t *Tracker) monthlyArchiveLoop() {
	for {
		now := time.Now()
		// Next archive at 1st of next month, 00:05
		next := time.Date(now.Year(), now.Month()+1, 1, 0, 5, 0, 0, now.Location())
		sleepDuration := next.Sub(now)

		timer := time.NewTimer(sleepDuration)
		select {
		case <-timer.C:
			t.archiveUsage()
		case <-t.stopCh:
			timer.Stop()
			return
		}
	}
}

func (t *Tracker) archiveUsage() {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	archiveMonth := now.AddDate(0, -1, 0).Format("2006-01")
	cutoff := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	var toArchive []UsageRecord
	var toKeep []UsageRecord
	for _, r := range t.records {
		ts, _ := time.Parse(time.RFC3339, r.Timestamp)
		if ts.Before(cutoff) {
			toArchive = append(toArchive, r)
		} else {
			toKeep = append(toKeep, r)
		}
	}

	if len(toArchive) == 0 {
		slog.Info("no records to archive")
		return
	}

	archivePath := fmt.Sprintf("data/usage_%s.json", archiveMonth)
	b, _ := json.MarshalIndent(toArchive, "", "  ")
	os.MkdirAll("data", 0755)
	if err := os.WriteFile(archivePath, b, 0644); err != nil {
		slog.Error("failed to archive usage", "error", err)
		return
	}

	t.records = toKeep
	t.dirtyCount = 0
	t.lastFlush = time.Now()
	t.save()
	slog.Info("usage archived", "month", archiveMonth, "archived_count", len(toArchive), "remaining", len(toKeep))
}

// GetEWMA returns cached EWMA latency for a provider (O(1)).
func (t *Tracker) GetEWMA(providerID string) float64 {
	return t.ewmaCache[providerID]
}

// TotalTokensByProvider returns total tokens consumed per provider.
func (t *Tracker) TotalTokensByProvider() map[string]int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	totals := make(map[string]int64)
	for _, r := range t.records {
		totals[r.ProviderID] += int64(r.TotalTokens)
	}
	return totals
}

// ProviderStats returns per-provider aggregated stats for the last N days.
func (t *Tracker) ProviderStats(days int) map[string]map[string]any {
	t.mu.Lock()
	snapshot := make([]UsageRecord, len(t.records))
	copy(snapshot, t.records)
	t.mu.Unlock()

	cutoff := time.Now().AddDate(0, 0, -days)
	type agg struct {
		count, success, promptTok, compTok, totalTok int
		costSum, latSum                              float64
		latCount                                     int
		minLat                                       float64
		maxLat                                       float64
		lastReq                                      string
		providerName                                 string
		// Per access type
		privReqs, pubReqs, guestReqs                int
		privTokens, pubTokens, guestTokens           int
	}
	stats := make(map[string]*agg)

	for _, r := range snapshot {
		ts, _ := time.Parse(time.RFC3339, r.Timestamp)
		if ts.Before(cutoff) {
			continue
		}
		s, ok := stats[r.ProviderID]
		if !ok {
			s = &agg{minLat: 1e18}
			stats[r.ProviderID] = s
		}
		if r.ProviderName != "" {
			s.providerName = r.ProviderName
		}
		s.count++
		if r.Success { s.success++ }
		s.promptTok += r.PromptTokens
		s.compTok += r.CompletionTokens
		s.totalTok += r.TotalTokens
		s.costSum += r.CostUSD
		if r.LatencyMS > 0 {
			s.latSum += r.LatencyMS
			s.latCount++
			if r.LatencyMS < s.minLat { s.minLat = r.LatencyMS }
			if r.LatencyMS > s.maxLat { s.maxLat = r.LatencyMS }
		}
		s.lastReq = r.Timestamp
		// Per access type
		switch r.AccessType {
		case "private":
			s.privReqs++
			s.privTokens += r.TotalTokens
		case "public":
			s.pubReqs++
			s.pubTokens += r.TotalTokens
		case "guest":
			s.guestReqs++
			s.guestTokens += r.TotalTokens
		}
	}

	out := make(map[string]map[string]any)
	for pid, s := range stats {
		avgLat := 0.0
		if s.latCount > 0 { avgLat = s.latSum / float64(s.latCount) }
		minLat := s.minLat
		if minLat == 1e18 { minLat = 0 }
		succRate := 0.0
		if s.count > 0 { succRate = float64(s.success) / float64(s.count) * 100 }

		out[pid] = map[string]any{
			"provider_id":     pid,
			"provider_name":   s.providerName,
			"request_count":   s.count,
			"success_count":   s.success,
			"success_rate":    round1(succRate),
			"total_tokens":    s.totalTok,
			"total_cost_usd":  round4(s.costSum),
			"avg_latency_ms":  round1(avgLat),
			"min_latency_ms":  round1(minLat),
			"max_latency_ms":  round1(s.maxLat),
			"last_request_at": s.lastReq,
			"private_reqs":    s.privReqs,
			"public_reqs":     s.pubReqs,
			"guest_reqs":      s.guestReqs,
			"private_tokens":  s.privTokens,
			"public_tokens":   s.pubTokens,
			"guest_tokens":    s.guestTokens,
		}
	}
	return out
}

// Flush forces a disk write.
func (t *Tracker) Flush() {
	t.mu.Lock()
	t.save()
	t.mu.Unlock()
}

// Stop shuts down the flush goroutine.
func (t *Tracker) Stop() {
	close(t.stopCh)
	t.Flush()
}

// Reset clears all records.
func (t *Tracker) Reset() {
	t.mu.Lock()
	t.records = nil
	t.dirtyCount = 0
	t.ewmaCache = make(map[string]float64)
	t.save()
	t.mu.Unlock()
}

func round1(f float64) float64 {
	if f < 0 { return 0 }
	return float64(int(f*10+0.5)) / 10
}
func round4(f float64) float64 {
	if f < 0 { return 0 }
	return float64(int(f*10000+0.5)) / 10000
}
