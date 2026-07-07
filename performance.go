package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================
// Performance Optimization Layer
// ============================================================
//
// This file implements resource optimization for ModelMux Agent:
// - Memory monitoring & usage tracking
// - Periodic cleanup of expired data (heartbeats, keys, routes)
// - sync.Pool for HTTP request/response buffer reuse
// - Shared HTTP client with connection pooling
// - Goroutine worker pool for relay/proxy tasks
// - Concurrency limiter (semaphore) for request handling
// - /api/metrics endpoint for runtime monitoring
//

// ============================================================
// Memory Monitoring
// ============================================================

// MemoryStats holds current memory usage statistics.
type MemoryStats struct {
	AllocMB      uint64 `json:"alloc_mb"`       // Currently allocated memory (MB)
	TotalAllocMB uint64 `json:"total_alloc_mb"` // Cumulative allocated memory (MB)
	SysMB        uint64 `json:"sys_mb"`         // Memory obtained from OS (MB)
	NumGC        uint32 `json:"num_gc"`         // Number of GC cycles completed
	NumGoroutine int    `json:"goroutines"`     // Current goroutine count
}

// getMemoryUsage returns current memory statistics.
func getMemoryUsage() MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return MemoryStats{
		AllocMB:      m.Alloc / 1024 / 1024,
		TotalAllocMB: m.TotalAlloc / 1024 / 1024,
		SysMB:        m.Sys / 1024 / 1024,
		NumGC:        m.NumGC,
		NumGoroutine: runtime.NumGoroutine(),
	}
}

// ============================================================
// Shared HTTP Client (Connection Pool Reuse)
// ============================================================
//
// client.go already defines sharedTransport and sharedHTTPClient for proxy
// requests. Here we add a dedicated client for internal operations (bootstrap
// queries, gossip, health checks) that is separate from proxy traffic to avoid
// head-of-line blocking between internal and external traffic.

var internalHTTPClient *http.Client

func initSharedHTTPClient() {
	internalHTTPClient = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    false,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		Timeout: 30 * time.Second,
	}
	slog.Info("internal HTTP client initialized", "max_idle_conns", 100, "idle_timeout", "90s")
}

// GetSharedHTTPClient returns the shared HTTP client for internal operations.
// For proxy requests, use proxyHTTPClient() from client.go.
func GetSharedHTTPClient() *http.Client {
	if internalHTTPClient == nil {
		initSharedHTTPClient()
	}
	return internalHTTPClient
}

// ============================================================
// Buffer Pool (sync.Pool for HTTP buffers)
// ============================================================
//
// Reuses byte buffers for JSON encoding, request building, etc.
// Reduces GC pressure under high concurrency.

var bufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 0, 4096)
		return bytes.NewBuffer(buf)
	},
}

// GetBuffer returns a buffer from the pool.
func GetBuffer() *bytes.Buffer {
	return bufPool.Get().(*bytes.Buffer)
}

// PutBuffer returns a buffer to the pool after resetting it.
func PutBuffer(buf *bytes.Buffer) {
	buf.Reset()
	bufPool.Put(buf)
}

// jsonEncodePool encodes v to JSON using a pooled buffer.
// Returns the encoded bytes (caller must not retain the slice after use).
func jsonEncodePool(v interface{}) ([]byte, error) {
	buf := GetBuffer()
	defer PutBuffer(buf)
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, err
	}
	// Copy because buf will be returned to pool
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	return result, nil
}

// ============================================================
// Worker Pool (Goroutine Pooling)
// ============================================================
//
// Limits the number of concurrent goroutines for relay/proxy tasks.
// Prevents goroutine explosion under high load.

const (
	defaultWorkerPoolSize = 50
	defaultWorkerQueue    = 200
)

// WorkerPool manages a fixed pool of worker goroutines.
type WorkerPool struct {
	taskCh  chan func()
	workers int
	active  atomic.Int64
	total   atomic.Int64
}

var relayWorkerPool *WorkerPool

func initWorkerPool(workers, queueSize int) {
	if workers <= 0 {
		workers = defaultWorkerPoolSize
	}
	if queueSize <= 0 {
		queueSize = defaultWorkerQueue
	}
	wp := &WorkerPool{
		taskCh:  make(chan func(), queueSize),
		workers: workers,
	}
	for i := 0; i < workers; i++ {
		go wp.worker()
	}
	relayWorkerPool = wp
	slog.Info("worker pool initialized", "workers", workers, "queue_size", queueSize)
}

func (wp *WorkerPool) worker() {
	for f := range wp.taskCh {
		wp.active.Add(1)
		f()
		wp.active.Add(-1)
	}
}

// Submit adds a task to the worker pool. Returns false if the queue is full.
func (wp *WorkerPool) Submit(f func()) bool {
	wp.total.Add(1)
	select {
	case wp.taskCh <- f:
		return true
	default:
		// Queue full — execute in a new goroutine to avoid blocking
		go f()
		return false
	}
}

// ActiveWorkers returns the current number of active workers.
func (wp *WorkerPool) ActiveWorkers() int64 {
	return wp.active.Load()
}

// TotalSubmitted returns the total number of tasks submitted.
func (wp *WorkerPool) TotalSubmitted() int64 {
	return wp.total.Load()
}

// ============================================================
// Concurrency Limiter (Request Semaphore)
// ============================================================
//
// Limits the total number of concurrent HTTP requests being processed.
// Prevents resource exhaustion under heavy load.

const defaultMaxConcurrentRequests = 100

var requestSemaphore chan struct{}

func initConcurrencyLimiter(maxConcurrent int) {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentRequests
	}
	requestSemaphore = make(chan struct{}, maxConcurrent)
	slog.Info("concurrency limiter initialized", "max_concurrent", maxConcurrent)
}

// acquireSemaphore blocks until a slot is available. Returns false if context expires.
func acquireSemaphore() {
	requestSemaphore <- struct{}{}
}

// releaseSemaphore frees a concurrency slot.
func releaseSemaphore() {
	<-requestSemaphore
}

// concurrencyMiddleware limits concurrent request processing.
func concurrencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		totalRequestCount.Add(1)
		acquireSemaphore()
		defer releaseSemaphore()
		next.ServeHTTP(w, r)
	})
}

// ============================================================
// Periodic Cleanup (Memory Leak Prevention)
// ============================================================
//
// Regularly purges expired data from in-memory maps to prevent
// unbounded growth over long uptime periods.

var cleanupStartTime time.Time

func startCleanupLoop() {
	cleanupStartTime = time.Now()
	// Run cleanup every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			runCleanup()
		}
	}()
	slog.Info("cleanup loop started", "interval", "5m")
}

func runCleanup() {
	// 1. Purge expired route table entries
	if routeTable != nil {
		purged := routeTable.PurgeExpired()
		if purged > 0 {
			slog.Debug("cleanup: purged expired route entries", "count", purged)
		}
	}

	// 2. Purge expired gossip dedup cache
	if gossip != nil {
		gossip.mu.Lock()
		now := time.Now()
		purged := 0
		for hash, seenAt := range gossip.seen {
			if now.Sub(seenAt) > 30*time.Minute { // dedup entries expire after 30min
				delete(gossip.seen, hash)
				purged++
			}
		}
		gossip.mu.Unlock()
		if purged > 0 {
			slog.Debug("cleanup: purged expired gossip dedup entries", "count", purged)
		}
	}

	// 3. Compact metrics maps — remove stale model/provider entries
	if metrics != nil {
		metrics.cleanupStaleEntries()
	}

	// 4. Compact contribution records (keep last 1000)
	compactContribRecords()

	// 5. Force GC if memory is above threshold
	mem := getMemoryUsage()
	if mem.AllocMB > 150 {
		runtime.GC()
		slog.Debug("cleanup: forced GC due to high memory", "alloc_mb", mem.AllocMB)
	}
}

// cleanupStaleEntries removes models/providers with zero requests from metrics maps
// if they are no longer in the active provider list.
func (m *Metrics) cleanupStaleEntries() {
	m.mu.Lock()
	defer m.mu.Unlock()

	activeProviders := make(map[string]bool)
	for _, p := range pm.Enabled() {
		activeProviders[p.ID] = true
	}

	// Clean up provider metrics for removed providers
	for pid := range m.requestByProvider {
		if !activeProviders[pid] {
			delete(m.requestByProvider, pid)
			delete(m.latencySum, pid)
			delete(m.latencyCount, pid)
		}
	}

	// Note: we don't clean up requestByModel because model names can be reused
}

// compactContribRecords limits the number of contribution records per node
// to prevent unbounded growth.
func compactContribRecords() {
	if netMgr == nil {
		return
	}
	netMgr.mu.Lock()
	defer netMgr.mu.Unlock()

	// Keep only the last 500 contribution records
	const maxRecords = 500
	records := netMgr.config.ContribRecords
	if len(records) > maxRecords {
		netMgr.config.ContribRecords = records[len(records)-maxRecords:]
	}
}

// ============================================================
// /api/metrics Endpoint (Lightweight Monitoring)
// ============================================================

// handleAPIMetrics returns runtime performance metrics as JSON.
// This is separate from the Prometheus /metrics endpoint.
func handleAPIMetrics(w http.ResponseWriter, r *http.Request) {
	mem := getMemoryUsage()

	activeNodes := 0
	if fed != nil && fed.IsEnabled() {
		activeNodes = len(fed.GetActiveNodes())
	}

	// Worker pool stats
	workerPoolActive := int64(0)
	workerPoolTotal := int64(0)
	if relayWorkerPool != nil {
		workerPoolActive = relayWorkerPool.ActiveWorkers()
		workerPoolTotal = relayWorkerPool.TotalSubmitted()
	}

	// Concurrency limiter usage
	semUsed := int64(0)
	semCap := int64(defaultMaxConcurrentRequests)
	if requestSemaphore != nil {
		semUsed = int64(len(requestSemaphore))
		semCap = int64(cap(requestSemaphore))
	}

	// Request metrics (safe even if metrics not yet initialized)
	var reqTotal, reqErrors, tokenUsage int64
	if metrics != nil {
		reqTotal = metrics.requestTotal.Load()
		reqErrors = metrics.requestErrors.Load()
		tokenUsage = metrics.tokenUsage.Load()
	}

	resp := map[string]interface{}{
		"memory":     mem,
		"uptime_s":   time.Since(cleanupStartTime).Seconds(),
		"goroutines": mem.NumGoroutine,

		// Request stats
		"http_request_total": totalRequestCount.Load(),
		"request_total":      reqTotal,
		"request_errors":     reqErrors,
		"token_usage":        tokenUsage,

		// Resource usage
		"concurrent_requests_used": semUsed,
		"concurrent_requests_max":  semCap,
		"worker_pool_active":       workerPoolActive,
		"worker_pool_total":        workerPoolTotal,

		// Component counts
		"providers_enabled": len(pm.Enabled()),
		"models_available":  len(pm.AllModels()),
		"active_federation_nodes": activeNodes,

		// Route table
		"route_table_entries": getRouteTableCount(),

		// SSE clients
		"sse_clients": getSSEClientCount(),

		// Health
		"status": "ok",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// getSSEClientCount returns the number of connected SSE clients (thread-safe).
func getSSEClientCount() int {
	if eventBus == nil {
		return 0
	}
	eventBus.mu.RLock()
	defer eventBus.mu.RUnlock()
	return len(eventBus.clients)
}

// getRouteTableCount returns the route table entry count (nil-safe).
func getRouteTableCount() int {
	if routeTable == nil {
		return 0
	}
	return routeTable.Count()
}

// ============================================================
// Request Counter (Atomic, for /api/metrics)
// ============================================================

var totalRequestCount atomic.Int64

// ============================================================
// Performance Initialization
// ============================================================

// initPerformance initializes all performance optimization subsystems.
// Call this early in main(), before starting the HTTP server.
func initPerformance() {
	initSharedHTTPClient()
	initWorkerPool(defaultWorkerPoolSize, defaultWorkerQueue)
	initConcurrencyLimiter(defaultMaxConcurrentRequests)
	startCleanupLoop()
	slog.Info("performance optimization layer initialized")
}
