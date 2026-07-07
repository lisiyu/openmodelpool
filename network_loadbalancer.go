package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"
)

// ============================================================
// Phase 4 — Dynamic Load Balancer with Health-Aware Routing
// ============================================================
//
// The LoadBalancer tracks real-time metrics for every known peer
// (latency, CPU, memory, error rate, active connections) and uses
// a weighted scoring algorithm to select the optimal next-hop node
// when relaying requests. A background health-check loop
// periodically pings all known nodes to keep metrics fresh.

// NodeMetrics holds real-time performance data for a single node.
type NodeMetrics struct {
	NodeID      string        `json:"node_id"`
	Latency     time.Duration `json:"latency_ns"`
	ActiveConns int           `json:"active_conns"`
	CPUUsage    float64       `json:"cpu_usage"`
	MemUsage    float64       `json:"mem_usage"`
	Bandwidth   int64         `json:"bandwidth_bps"`
	ErrorRate   float64       `json:"error_rate"`
	LastUpdate  time.Time     `json:"last_update"`
	Healthy     bool          `json:"healthy"`

	// Sliding-window history
	LatencyHistory []time.Duration `json:"latency_history,omitempty"`
	RequestCount   int64           `json:"request_count"`
	SuccessCount   int64           `json:"success_count"`
}

// LBConfig holds tunable parameters for the load balancer.
type LBConfig struct {
	// Scoring weights (should sum to 1.0)
	LatencyWeight    float64 `json:"latency_weight"`
	LoadWeight       float64 `json:"load_weight"`
	ErrorRateWeight  float64 `json:"error_rate_weight"`
	FairnessWeight   float64 `json:"fairness_weight"`
	ReputationWeight float64 `json:"reputation_weight"`

	// Operational parameters
	HealthCheckInterval time.Duration `json:"health_check_interval"`
	MetricsWindow       int           `json:"metrics_window"`
	MaxLatency          time.Duration `json:"max_latency"`
}

// RouteRequirements describes constraints for node selection.
type RouteRequirements struct {
	TargetNodeID string   // specific target (if any)
	Models       []string // required model capabilities
	MaxLatency   time.Duration
	MinScore     float64
}

// LoadBalancer is the central scoring & routing engine.
type LoadBalancer struct {
	mu sync.RWMutex

	// Per-node real-time metrics
	nodeMetrics map[string]*NodeMetrics

	// Routing history for fairness (nodeID → recent route count)
	routeHistory map[string]int64

	// Configuration
	config LBConfig

	// Lifecycle
	cancel context.CancelFunc
}

// lbInstance is the package-level singleton.
var lbInstance *LoadBalancer

// DefaultLBConfig returns sensible defaults.
func DefaultLBConfig() LBConfig {
	return LBConfig{
		LatencyWeight:    0.35,
		LoadWeight:       0.25,
		ErrorRateWeight:  0.20,
		FairnessWeight:   0.10,
		ReputationWeight: 0.10,

		HealthCheckInterval: 30 * time.Second,
		MetricsWindow:       20,
		MaxLatency:          5 * time.Second,
	}
}

// NewLoadBalancer creates and returns a new LoadBalancer.
func NewLoadBalancer(cfg LBConfig) *LoadBalancer {
	return &LoadBalancer{
		nodeMetrics:  make(map[string]*NodeMetrics),
		routeHistory: make(map[string]int64),
		config:       cfg,
	}
}

// ============================================================
// Lifecycle
// ============================================================

// StartHealthCheck begins the periodic health-probe loop.
func (lb *LoadBalancer) StartHealthCheck(ctx context.Context) {
	ctx, lb.cancel = context.WithCancel(ctx)
	go lb.healthCheckLoop(ctx)
	slog.Info("load balancer health check started",
		"interval", lb.config.HealthCheckInterval)
}

// Stop cancels the health-check loop.
func (lb *LoadBalancer) Stop() {
	if lb.cancel != nil {
		lb.cancel()
	}
}

func (lb *LoadBalancer) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(lb.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("load balancer health check stopped")
			return
		case <-ticker.C:
			lb.probeAllNodes()
		}
	}
}

// probeAllNodes pings every known peer and updates metrics.
func (lb *LoadBalancer) probeAllNodes() {
	if netMgr == nil || !netMgr.IsSharedMode() {
		return
	}

	entries := routeTable.GetAll()
	selfID := netMgr.GetNodeID()
	client := GetSharedHTTPClient()

	for _, entry := range entries {
		if entry.NodeID == selfID {
			continue
		}
		addr := pickBestAddress(entry.Addresses)
		if addr == "" {
			continue
		}

		go func(nodeID, addr string) {
			pingURL := fmt.Sprintf("%s/api/network/heartbeat/ping", trimTrailingSlash(addr))
			start := time.Now()
			req, err := http.NewRequest("GET", pingURL, nil)
			if err != nil {
				lb.recordProbe(nodeID, 0, false)
				return
			}
			req.Header.Set("X-Node-ID", selfID)

			resp, err := client.Do(req)
			elapsed := time.Since(start)

			if err != nil || resp == nil || resp.StatusCode >= 500 {
				lb.recordProbe(nodeID, elapsed, false)
				if err != nil {
					slog.Debug("health probe failed", "node", nodeID, "error", err)
				}
				return
			}
			resp.Body.Close()
			lb.recordProbe(nodeID, elapsed, resp.StatusCode < 400)
		}(entry.NodeID, addr)
	}
}

// recordProbe updates latency and health status for a node.
func (lb *LoadBalancer) recordProbe(nodeID string, latency time.Duration, success bool) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	m, ok := lb.nodeMetrics[nodeID]
	if !ok {
		m = &NodeMetrics{
			NodeID:         nodeID,
			LatencyHistory: make([]time.Duration, 0, lb.config.MetricsWindow),
		}
		lb.nodeMetrics[nodeID] = m
	}

	m.RequestCount++
	if success {
		m.SuccessCount++
		m.Healthy = true
		m.Latency = latency

		// Update sliding window
		m.LatencyHistory = append(m.LatencyHistory, latency)
		if len(m.LatencyHistory) > lb.config.MetricsWindow {
			m.LatencyHistory = m.LatencyHistory[len(m.LatencyHistory)-lb.config.MetricsWindow:]
		}
	} else {
		// Consecutive failures degrade health
		if !success {
			m.Healthy = false
		}
	}
	m.LastUpdate = time.Now()

	// Compute error rate from sliding window
	if m.RequestCount > 0 {
		m.ErrorRate = 1.0 - float64(m.SuccessCount)/float64(m.RequestCount)
	}
}

// ============================================================
// Node Scoring
// ============================================================

// ScoreNode computes a 0–100 composite score for a node.
func (lb *LoadBalancer) ScoreNode(nodeID string) float64 {
	lb.mu.RLock()
	m := lb.nodeMetrics[nodeID]
	histCount := lb.routeHistory[nodeID]
	lb.mu.RUnlock()

	// If we have no metrics yet, return a neutral score so the node
	// still gets a chance (exploration vs exploitation).
	if m == nil {
		return 50.0
	}

	// --- latency_score (0-100) ---
	avgLatency := m.Latency
	if avgLatency <= 0 {
		avgLatency = lb.config.MaxLatency // treat unknown as worst
	}
	maxMs := float64(lb.config.MaxLatency.Milliseconds())
	curMs := float64(avgLatency.Milliseconds())
	latencyScore := 100.0 * (1.0 - math.Min(curMs/maxMs, 1.0))
	// Sharply penalize nodes that exceed the threshold
	if avgLatency > lb.config.MaxLatency {
		latencyScore = 0
	}

	// --- load_score (0-100) ---
	// Assume max 100 concurrent connections per node
	const assumedMaxConns = 100
	loadRatio := math.Min(float64(m.ActiveConns)/float64(assumedMaxConns), 1.0)
	loadScore := 100.0 * (1.0 - loadRatio)

	// --- error_score (0-100) ---
	errorScore := 100.0 * (1.0 - math.Min(m.ErrorRate, 1.0))

	// --- fairness_score (0-100) ---
	// Fewer recent routes → higher score
	var maxHist int64 = 1
	lb.mu.RLock()
	for _, v := range lb.routeHistory {
		if v > maxHist {
			maxHist = v
		}
	}
	lb.mu.RUnlock()
	fairnessScore := 100.0 * (1.0 - float64(histCount)/float64(maxHist+1))

	// --- reputation_score (0-100) ---
	reputationScore := lb.getReputationScore(nodeID)

	// --- composite ---
	score := latencyScore*lb.config.LatencyWeight +
		loadScore*lb.config.LoadWeight +
		errorScore*lb.config.ErrorRateWeight +
		fairnessScore*lb.config.FairnessWeight +
		reputationScore*lb.config.ReputationWeight

	return math.Round(score*100) / 100 // two decimal places
}

// getReputationScore derives reputation from the Phase 2/3 contribution system.
func (lb *LoadBalancer) getReputationScore(nodeID string) float64 {
	if netMgr == nil {
		return 50.0
	}
	netMgr.mu.RLock()
	defer netMgr.mu.RUnlock()

	// Check direct peer trust score
	for _, p := range netMgr.config.Peers {
		if p.NodeID == nodeID {
			return p.TrustScore * 100.0
		}
	}

	// Check unlock state contribution points as proxy
	if state, ok := netMgr.config.NodeUnlockStates[nodeID]; ok && state != nil {
		// Normalize contrib points: 0-1000 → 0-100
		return math.Min(float64(state.ContribPoints)/10.0, 100.0)
	}

	// Unknown node — neutral
	return 50.0
}

// ============================================================
// Intelligent Routing
// ============================================================

// SelectNode picks the best node according to scoring & requirements.
func (lb *LoadBalancer) SelectNode(reqs RouteRequirements) (string, error) {
	// 1. Collect candidate entries from the route table
	entries := routeTable.GetAll()
	selfID := ""
	if netMgr != nil {
		selfID = netMgr.GetNodeID()
	}

	type candidate struct {
		nodeID string
		score  float64
	}

	var candidates []candidate
	maxLatency := lb.config.MaxLatency
	if reqs.MaxLatency > 0 && reqs.MaxLatency < maxLatency {
		maxLatency = reqs.MaxLatency
	}

	for _, e := range entries {
		if e.NodeID == selfID {
			continue
		}

		// If a specific target is requested, skip others
		if reqs.TargetNodeID != "" && e.NodeID != reqs.TargetNodeID {
			continue
		}

		// Basic health filter
		lb.mu.RLock()
		m := lb.nodeMetrics[e.NodeID]
		lb.mu.RUnlock()

		if m != nil {
			// Skip unhealthy nodes
			if !m.Healthy && m.LastUpdate.After(time.Now().Add(-2*lb.config.HealthCheckInterval)) {
				continue
			}
			// Skip nodes with excessive error rate (>80%)
			if m.ErrorRate > 0.8 {
				continue
			}
			// Skip nodes exceeding latency threshold
			if m.Latency > maxLatency && m.Latency > 0 {
				continue
			}
		}

		score := lb.ScoreNode(e.NodeID)
		if score < reqs.MinScore {
			continue
		}

		candidates = append(candidates, candidate{nodeID: e.NodeID, score: score})
	}

	if len(candidates) == 0 {
		// Fallback: if a specific target was requested and no candidates passed
		// filters, still try it (the node may simply be unmeasured).
		if reqs.TargetNodeID != "" {
			entry := routeTable.Get(reqs.TargetNodeID)
			if entry != nil {
				lb.recordRoute(reqs.TargetNodeID)
				return reqs.TargetNodeID, nil
			}
		}
		return "", fmt.Errorf("no suitable node found for routing")
	}

	// 2. Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// 3. Weighted random from top-N (N = min(5, len(candidates)))
	topN := 5
	if len(candidates) < topN {
		topN = len(candidates)
	}
	top := candidates[:topN]

	// Compute total weight for top-N
	totalWeight := 0.0
	for _, c := range top {
		totalWeight += c.score
	}
	if totalWeight <= 0 {
		totalWeight = 1.0
	}

	// Weighted random selection
	r := rand.Float64() * totalWeight
	cumulative := 0.0
	selected := top[0]
	for _, c := range top {
		cumulative += c.score
		if r <= cumulative {
			selected = c
			break
		}
	}

	// 4. Record routing history
	lb.recordRoute(selected.nodeID)

	return selected.nodeID, nil
}

// recordRoute increments the route counter for fairness tracking.
func (lb *LoadBalancer) recordRoute(nodeID string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.routeHistory[nodeID]++

	// Periodically decay history to prevent stale counts from dominating
	total := int64(0)
	for _, v := range lb.routeHistory {
		total += v
	}
	if total > 1000 {
		for k := range lb.routeHistory {
			lb.routeHistory[k] = lb.routeHistory[k] / 2
			if lb.routeHistory[k] == 0 {
				delete(lb.routeHistory, k)
			}
		}
	}
}

// ============================================================
// Metric Updates (called from relay / heartbeat paths)
// ============================================================

// RecordRequest records the outcome of a relayed request.
func (lb *LoadBalancer) RecordRequest(nodeID string, latency time.Duration, success bool) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	m, ok := lb.nodeMetrics[nodeID]
	if !ok {
		m = &NodeMetrics{
			NodeID:         nodeID,
			LatencyHistory: make([]time.Duration, 0, lb.config.MetricsWindow),
		}
		lb.nodeMetrics[nodeID] = m
	}

	m.RequestCount++
	if success {
		m.SuccessCount++
		m.Healthy = true
	} else {
		// Two consecutive failures mark unhealthy
		if m.RequestCount-m.SuccessCount > 2 {
			m.Healthy = false
		}
	}

	if latency > 0 {
		m.Latency = latency
		m.LatencyHistory = append(m.LatencyHistory, latency)
		if len(m.LatencyHistory) > lb.config.MetricsWindow {
			m.LatencyHistory = m.LatencyHistory[len(m.LatencyHistory)-lb.config.MetricsWindow:]
		}
	}
	m.LastUpdate = time.Now()

	if m.RequestCount > 0 {
		m.ErrorRate = 1.0 - float64(m.SuccessCount)/float64(m.RequestCount)
	}
}

// UpdateNodeMetrics allows external callers (e.g. heartbeat handler) to push
// CPU/memory/bandwidth data for a node.
func (lb *LoadBalancer) UpdateNodeMetrics(nodeID string, cpu, mem float64, conns int, bw int64) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	m, ok := lb.nodeMetrics[nodeID]
	if !ok {
		m = &NodeMetrics{
			NodeID:         nodeID,
			LatencyHistory: make([]time.Duration, 0, lb.config.MetricsWindow),
		}
		lb.nodeMetrics[nodeID] = m
	}
	m.CPUUsage = cpu
	m.MemUsage = mem
	m.ActiveConns = conns
	m.Bandwidth = bw
	m.LastUpdate = time.Now()
}

// ============================================================
// API Handlers
// ============================================================

// handleLBStatus returns overall load balancer status.
func handleLBStatus(w http.ResponseWriter, r *http.Request) {
	if lbInstance == nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}
	lbInstance.mu.RLock()
	defer lbInstance.mu.RUnlock()

	healthy := 0
	total := len(lbInstance.nodeMetrics)
	for _, m := range lbInstance.nodeMetrics {
		if m.Healthy {
			healthy++
		}
	}

	writeJSON(w, 200, map[string]any{
		"enabled":        true,
		"total_nodes":    total,
		"healthy_nodes":  healthy,
		"config":         lbInstance.config,
		"route_history":  len(lbInstance.routeHistory),
	})
}

// handleLBNodes returns all nodes ranked by score.
func handleLBNodes(w http.ResponseWriter, r *http.Request) {
	if lbInstance == nil {
		writeJSON(w, 200, map[string]any{"nodes": []any{}})
		return
	}

	type nodeScore struct {
		NodeID  string  `json:"node_id"`
		Score   float64 `json:"score"`
		Healthy bool    `json:"healthy"`
		Latency int64   `json:"latency_ms"`
	}

	lbInstance.mu.RLock()
	nodeIDs := make([]string, 0, len(lbInstance.nodeMetrics))
	for id := range lbInstance.nodeMetrics {
		nodeIDs = append(nodeIDs, id)
	}
	lbInstance.mu.RUnlock()

	results := make([]nodeScore, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		score := lbInstance.ScoreNode(id)
		lbInstance.mu.RLock()
		m := lbInstance.nodeMetrics[id]
		lbInstance.mu.RUnlock()
		ns := nodeScore{
			NodeID:  id,
			Score:   score,
			Healthy: m.Healthy,
			Latency: m.Latency.Milliseconds(),
		}
		results = append(results, ns)
	}

	// Sort descending by score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	writeJSON(w, 200, map[string]any{"nodes": results})
}

// handleLBNodeMetrics returns detailed metrics for a single node.
func handleLBNodeMetrics(w http.ResponseWriter, r *http.Request) {
	if lbInstance == nil {
		writeError(w, 503, "load balancer not initialized")
		return
	}

	// Extract node_id from path: /api/network/loadbalancer/metrics/{node_id}
	nodeID := r.PathValue("node_id")
	if nodeID == "" {
		writeError(w, 400, "node_id is required")
		return
	}

	lbInstance.mu.RLock()
	m, ok := lbInstance.nodeMetrics[nodeID]
	hist := lbInstance.routeHistory[nodeID]
	lbInstance.mu.RUnlock()

	if !ok {
		writeJSON(w, 200, map[string]any{
			"node_id": nodeID,
			"message": "no metrics recorded for this node",
			"score":   lbInstance.ScoreNode(nodeID),
		})
		return
	}

	writeJSON(w, 200, map[string]any{
		"node_id":        m.NodeID,
		"score":          lbInstance.ScoreNode(nodeID),
		"latency_ms":     m.Latency.Milliseconds(),
		"active_conns":   m.ActiveConns,
		"cpu_usage":      m.CPUUsage,
		"mem_usage":      m.MemUsage,
		"bandwidth_bps":  m.Bandwidth,
		"error_rate":     m.ErrorRate,
		"healthy":        m.Healthy,
		"last_update":    m.LastUpdate.Format(time.RFC3339),
		"request_count":  m.RequestCount,
		"success_count":  m.SuccessCount,
		"route_count":    hist,
		"latency_window": m.LatencyHistory,
	})
}

// handleLBConfigUpdate allows updating scoring weights.
func handleLBConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if lbInstance == nil {
		writeError(w, 503, "load balancer not initialized")
		return
	}

	if r.Method != http.MethodPut {
		writeError(w, 405, "method not allowed")
		return
	}

	var newCfg LBConfig
	if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Validate weights sum to ~1.0
	totalWeight := newCfg.LatencyWeight + newCfg.LoadWeight + newCfg.ErrorRateWeight +
		newCfg.FairnessWeight + newCfg.ReputationWeight
	if totalWeight < 0.9 || totalWeight > 1.1 {
		writeError(w, 400, fmt.Sprintf("weights must sum to ~1.0 (got %.2f)", totalWeight))
		return
	}

	// Apply sane bounds
	if newCfg.HealthCheckInterval < 5*time.Second {
		newCfg.HealthCheckInterval = 5 * time.Second
	}
	if newCfg.MetricsWindow < 5 {
		newCfg.MetricsWindow = 5
	}
	if newCfg.MetricsWindow > 100 {
		newCfg.MetricsWindow = 100
	}
	if newCfg.MaxLatency < 500*time.Millisecond {
		newCfg.MaxLatency = 500 * time.Millisecond
	}

	lbInstance.mu.Lock()
	lbInstance.config = newCfg
	lbInstance.mu.Unlock()

	slog.Info("load balancer config updated",
		"latency_w", newCfg.LatencyWeight,
		"load_w", newCfg.LoadWeight,
		"error_w", newCfg.ErrorRateWeight,
		"fairness_w", newCfg.FairnessWeight,
		"reputation_w", newCfg.ReputationWeight)

	writeJSON(w, 200, map[string]any{
		"status": "updated",
		"config": newCfg,
	})
}

// ============================================================
// Helpers
// ============================================================

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// initLoadBalancer creates the global LB instance and starts health checks.
// Called from main startup after netMgr is ready.
func initLoadBalancer(ctx context.Context) {
	lbInstance = NewLoadBalancer(DefaultLBConfig())
	lbInstance.StartHealthCheck(ctx)
	slog.Info("load balancer initialized")
}

// handleHeartbeatPing responds to health probe pings from other nodes.
func handleHeartbeatPing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status":    "ok",
		"node_id":   netMgr.GetNodeID(),
		"timestamp": time.Now().Unix(),
	})
}

// selectRelayNode uses the load balancer to choose a relay target.
// Falls back to direct route table lookup if LB is unavailable.
func selectRelayNode(targetNodeID string) (string, *RouteEntry, error) {
	// First try the load balancer
	if lbInstance != nil {
		selectedID, err := lbInstance.SelectNode(RouteRequirements{
			TargetNodeID: targetNodeID,
		})
		if err == nil && selectedID != "" {
			entry := routeTable.Get(selectedID)
			if entry != nil {
				return selectedID, entry, nil
			}
		}
	}

	// Fallback: direct route table lookup
	entry := routeTable.Get(targetNodeID)
	if entry == nil {
		entry = queryBootstrapForNode(targetNodeID)
	}
	if entry == nil {
		return "", nil, fmt.Errorf("node %s not found", targetNodeID)
	}
	return entry.NodeID, entry, nil
}
