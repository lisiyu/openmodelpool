package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics collects and exposes Prometheus-compatible metrics.
// Uses a lightweight implementation without the prometheus client library.
type Metrics struct {
	mu             sync.RWMutex
	requestTotal   atomic.Int64
	requestErrors  atomic.Int64
	requestByModel map[string]*atomic.Int64
	requestByProvider map[string]*atomic.Int64
	latencySum     map[string]*atomic.Int64 // provider -> sum of latencies in ms
	latencyCount   map[string]*atomic.Int64 // provider -> count
	tokenUsage     atomic.Int64
	startTime      time.Time
}

var metrics *Metrics

func initMetrics() {
	metrics = &Metrics{
		requestByModel:    make(map[string]*atomic.Int64),
		requestByProvider: make(map[string]*atomic.Int64),
		latencySum:        make(map[string]*atomic.Int64),
		latencyCount:      make(map[string]*atomic.Int64),
		startTime:         time.Now(),
	}
	slog.Info("metrics collector initialized")
}

// RecordRequest records a request metric.
func (m *Metrics) RecordRequest(model, providerID string, latencyMS int64, success bool, tokens int) {
	m.requestTotal.Add(1)
	if !success {
		m.requestErrors.Add(1)
	}

	// Per-model counter
	m.mu.RLock()
	counter, ok := m.requestByModel[model]
	m.mu.RUnlock()
	if !ok {
		m.mu.Lock()
		counter = &atomic.Int64{}
		m.requestByModel[model] = counter
		m.mu.Unlock()
	}
	counter.Add(1)

	// Per-provider counter
	m.mu.RLock()
	pCounter, ok := m.requestByProvider[providerID]
	m.mu.RUnlock()
	if !ok {
		m.mu.Lock()
		pCounter = &atomic.Int64{}
		m.requestByProvider[providerID] = pCounter
		m.mu.Unlock()
	}
	pCounter.Add(1)

	// Latency tracking per provider
	if latencyMS > 0 && providerID != "" {
		m.mu.RLock()
		lSum, ok := m.latencySum[providerID]
		lCount, ok2 := m.latencyCount[providerID]
		m.mu.RUnlock()
		if !ok || !ok2 {
			m.mu.Lock()
			if _, ok := m.latencySum[providerID]; !ok {
				m.latencySum[providerID] = &atomic.Int64{}
				m.latencyCount[providerID] = &atomic.Int64{}
			}
			lSum = m.latencySum[providerID]
			lCount = m.latencyCount[providerID]
			m.mu.Unlock()
		}
		lSum.Add(latencyMS)
		lCount.Add(1)
	}

	// Token usage
	if tokens > 0 {
		m.tokenUsage.Add(int64(tokens))
	}
}

// handleMetrics serves the /metrics endpoint in Prometheus text format.
func handleMetrics(w http.ResponseWriter, r *http.Request) {
	if metrics == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	var b strings.Builder

	// HELP and TYPE headers
	b.WriteString("# HELP openmodelpool_requests_total Total number of requests processed.\n")
	b.WriteString("# TYPE openmodelpool_requests_total counter\n")
	b.WriteString(fmt.Sprintf("openmodelpool_requests_total %d\n", metrics.requestTotal.Load()))

	b.WriteString("# HELP openmodelpool_request_errors_total Total number of failed requests.\n")
	b.WriteString("# TYPE openmodelpool_request_errors_total counter\n")
	b.WriteString(fmt.Sprintf("openmodelpool_request_errors_total %d\n", metrics.requestErrors.Load()))

	b.WriteString("# HELP openmodelpool_tokens_total Total tokens consumed.\n")
	b.WriteString("# TYPE openmodelpool_tokens_total counter\n")
	b.WriteString(fmt.Sprintf("openmodelpool_tokens_total %d\n", metrics.tokenUsage.Load()))

	b.WriteString("# HELP openmodelpool_uptime_seconds Uptime in seconds.\n")
	b.WriteString("# TYPE openmodelpool_uptime_seconds gauge\n")
	b.WriteString(fmt.Sprintf("openmodelpool_uptime_seconds %.0f\n", time.Since(metrics.startTime).Seconds()))

	// Per-model requests
	b.WriteString("# HELP openmodelpool_requests_by_model Requests by model.\n")
	b.WriteString("# TYPE openmodelpool_requests_by_model counter\n")
	metrics.mu.RLock()
	models := make([]string, 0, len(metrics.requestByModel))
	for k := range metrics.requestByModel {
		models = append(models, k)
	}
	sort.Strings(models)
	for _, model := range models {
		b.WriteString(fmt.Sprintf("openmodelpool_requests_by_model{model=%q} %d\n", model, metrics.requestByModel[model].Load()))
	}

	// Per-provider requests
	b.WriteString("# HELP openmodelpool_requests_by_provider Requests by provider.\n")
	b.WriteString("# TYPE openmodelpool_requests_by_provider counter\n")
	providers := make([]string, 0, len(metrics.requestByProvider))
	for k := range metrics.requestByProvider {
		providers = append(providers, k)
	}
	sort.Strings(providers)
	for _, pid := range providers {
		b.WriteString(fmt.Sprintf("openmodelpool_requests_by_provider{provider=%q} %d\n", pid, metrics.requestByProvider[pid].Load()))
	}

	// Average latency per provider
	b.WriteString("# HELP openmodelpool_avg_latency_ms Average latency per provider in milliseconds.\n")
	b.WriteString("# TYPE openmodelpool_avg_latency_ms gauge\n")
	for _, pid := range providers {
		sum := metrics.latencySum[pid].Load()
		count := metrics.latencyCount[pid].Load()
		if count > 0 {
			avg := float64(sum) / float64(count)
			b.WriteString(fmt.Sprintf("openmodelpool_avg_latency_ms{provider=%q} %.2f\n", pid, avg))
		}
	}
	metrics.mu.RUnlock()

	// Active providers
	enabledCount := len(pm.Enabled())
	b.WriteString("# HELP openmodelpool_active_providers Number of enabled providers.\n")
	b.WriteString("# TYPE openmodelpool_active_providers gauge\n")
	b.WriteString(fmt.Sprintf("openmodelpool_active_providers %d\n", enabledCount))

	// Federation info
	if fed != nil && fed.IsEnabled() {
		pool := fed.GetTrustPool()
		b.WriteString("# HELP openmodelpool_federation_nodes Number of federation nodes.\n")
		b.WriteString("# TYPE openmodelpool_federation_nodes gauge\n")
		b.WriteString(fmt.Sprintf("openmodelpool_federation_nodes %d\n", len(pool.Nodes)))
	}

	// SSE clients
	if eventBus != nil {
		stats := GetEventBusStats()
		if clients, ok := stats["connected_clients"].(int); ok {
			b.WriteString("# HELP openmodelpool_sse_clients Number of connected SSE clients.\n")
			b.WriteString("# TYPE openmodelpool_sse_clients gauge\n")
			b.WriteString(fmt.Sprintf("openmodelpool_sse_clients %d\n", clients))
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(b.String()))
}
