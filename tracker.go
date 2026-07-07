package main

import (
	"encoding/json"
	"log/slog"
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
		dataPath:  path,
		ewmaCache: make(map[string]float64),
		lastFlush: time.Now(),
		stopCh:    make(chan struct{}),
	}
	tracker.load()
	go tracker.periodicFlush()
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
	os.WriteFile(t.dataPath, b, 0644)
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
	cost := 0.0
	if success {
		cost = estimateCost(model, promptTokens, completionTokens, providerID)
	}

	entry := UsageRecord{
		Timestamp:        time.Now().Format(time.RFC3339),
		ProviderID:       providerID,
		ProviderName:     providerName,
		Model:            model,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		CostUSD:          cost,
		LatencyMS:        round1(latencyMS),
		Success:          success,
		Error:            errMsg,
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

	// Check flush threshold
	if t.dirtyCount >= trackerFlushThreshold || time.Since(t.lastFlush) >= trackerFlushInterval {
		t.save()
	}
	t.mu.Unlock()
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
			"request_count":   s.count,
			"success_count":   s.success,
			"success_rate":    round1(succRate),
			"total_tokens":    s.totalTok,
			"total_cost_usd":  round4(s.costSum),
			"avg_latency_ms":  round1(avgLat),
			"min_latency_ms":  round1(minLat),
			"max_latency_ms":  round1(s.maxLat),
			"last_request_at": s.lastReq,
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

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
func round4(f float64) float64 { return float64(int(f*10000+0.5)) / 10000 }
