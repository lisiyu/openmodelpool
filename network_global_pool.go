package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ============================================================
// Phase 4: Global Computing Pool
// ============================================================
//
// The Global Pool aggregates computing resources (token quotas)
// contributed by all participating nodes into a single virtual
// pool. A Global Key (mk_open_global_*) can then route requests
// to any available node in the pool.
//
// Key design:
//   - Nodes must join the pool and maintain minimum contribution
//   - Each node's share is tracked (contributed vs consumed)
//   - Routing综合考虑: load, latency, reputation, contribution ratio
//   - Pool state is persisted and periodically synced via gossip

// GlobalPoolNode represents a node participating in the global pool.
type GlobalPoolNode struct {
	NodeID        string    `json:"node_id"`
	Region        string    `json:"region"`
	Contributed   int64     `json:"contributed"`
	Consumed      int64     `json:"consumed"`
	Ratio         float64   `json:"ratio"`       // contributed / (consumed + 1)
	Reputation    float64   `json:"reputation"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	JoinedAt      string    `json:"joined_at"`
	Status        string    `json:"status"` // active | degraded | offline
}

// GlobalPool holds the aggregated state of all contributing nodes.
type GlobalPool struct {
	mu sync.RWMutex

	// Global counters
	TotalContributed int64            `json:"total_contributed"`
	TotalConsumed    int64            `json:"total_consumed"`
	AvailableQuota   int64            `json:"available_quota"`

	// Per-node tracking
	NodeContributions map[string]int64 `json:"node_contributions"`
	NodeConsumptions  map[string]int64 `json:"node_consumptions"`

	// Participant list
	ParticipantNodes []GlobalPoolNode `json:"participant_nodes"`

	// Metadata
	LastUpdated time.Time `json:"last_updated"`
	dataPath    string
}

var globalPool *GlobalPool

// ============================================================
// Global Pool Configuration
// ============================================================

const (
	// Minimum contribution required to join the global pool
	globalPoolMinJoinContribution int64 = 10000

	// Minimum contribution ratio to remain active
	globalPoolMinRatio float64 = 0.1

	// Global key signing threshold — node must have contributed at least this much
	globalKeySigningThreshold int64 = 5000

	// Global pool refresh interval
	globalPoolRefreshInterval = 2 * time.Minute

)

// ============================================================
// Initialization
// ============================================================

func initGlobalPool(dataDir string) {
	globalPool = &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          filepath.Join(dataDir, "global_pool.json"),
	}
	globalPool.load()
	go globalPool.refreshLoop()
	slog.Info("global pool initialized",
		"participants", len(globalPool.ParticipantNodes),
		"total_contributed", globalPool.TotalContributed,
	)
}

// ============================================================
// Persistence
// ============================================================

type globalPoolStore struct {
	TotalContributed  int64            `json:"total_contributed"`
	TotalConsumed     int64            `json:"total_consumed"`
	AvailableQuota    int64            `json:"available_quota"`
	NodeContributions map[string]int64 `json:"node_contributions"`
	NodeConsumptions  map[string]int64 `json:"node_consumptions"`
	ParticipantNodes  []GlobalPoolNode `json:"participant_nodes"`
	LastUpdated       time.Time        `json:"last_updated"`
}

func (gp *GlobalPool) load() {
	b, err := os.ReadFile(gp.dataPath)
	if err != nil {
		return
	}
	var store globalPoolStore
	if err := json.Unmarshal(b, &store); err != nil {
		slog.Warn("global pool load failed", "error", err)
		return
	}
	gp.mu.Lock()
	defer gp.mu.Unlock()
	gp.TotalContributed = store.TotalContributed
	gp.TotalConsumed = store.TotalConsumed
	gp.AvailableQuota = store.AvailableQuota
	if store.NodeContributions != nil {
		gp.NodeContributions = store.NodeContributions
	}
	if store.NodeConsumptions != nil {
		gp.NodeConsumptions = store.NodeConsumptions
	}
	if store.ParticipantNodes != nil {
		gp.ParticipantNodes = store.ParticipantNodes
	}
	gp.LastUpdated = store.LastUpdated
}

func (gp *GlobalPool) save() {
	gp.mu.RLock()
	defer gp.mu.RUnlock()
	gp.doSave()
}

func (gp *GlobalPool) doSave() {
	store := globalPoolStore{
		TotalContributed:  gp.TotalContributed,
		TotalConsumed:     gp.TotalConsumed,
		AvailableQuota:    gp.AvailableQuota,
		NodeContributions: gp.NodeContributions,
		NodeConsumptions:  gp.NodeConsumptions,
		ParticipantNodes:  gp.ParticipantNodes,
		LastUpdated:       gp.LastUpdated,
	}
	b, _ := json.MarshalIndent(store, "", "  ")
	os.MkdirAll(filepath.Dir(gp.dataPath), 0755)
	os.WriteFile(gp.dataPath, b, 0600)
}

// ============================================================
// Pool Operations
// ============================================================

// JoinPool adds a node to the global pool.
func (gp *GlobalPool) JoinPool(nodeID, region string, initialContribution int64) error {
	if nodeID == "" {
		return fmt.Errorf("node_id is required")
	}
	if initialContribution < globalPoolMinJoinContribution {
		return fmt.Errorf("minimum contribution to join: %d", globalPoolMinJoinContribution)
	}

	gp.mu.Lock()
	defer gp.mu.Unlock()

	// Check if already a participant
	for i, n := range gp.ParticipantNodes {
		if n.NodeID == nodeID {
			// Already in pool — update contribution
			gp.NodeContributions[nodeID] += initialContribution
			gp.ParticipantNodes[i].Contributed = gp.NodeContributions[nodeID]
			gp.ParticipantNodes[i].LastHeartbeat = time.Now()
			gp.ParticipantNodes[i].Status = "active"
			if gp.NodeContributions[nodeID] > 0 {
				gp.ParticipantNodes[i].Ratio = float64(gp.NodeContributions[nodeID]) / float64(gp.NodeConsumptions[nodeID]+1)
			}
			gp.recalculateLocked()
			gp.doSave()
			slog.Info("global pool: node updated", "node_id", nodeID, "new_contribution", gp.NodeContributions[nodeID])
			return nil
		}
	}

	// New participant
	now := time.Now()
	gp.NodeContributions[nodeID] = initialContribution
	gp.ParticipantNodes = append(gp.ParticipantNodes, GlobalPoolNode{
		NodeID:        nodeID,
		Region:        region,
		Contributed:   initialContribution,
		Consumed:      0,
		Ratio:         float64(initialContribution),
		LastHeartbeat: now,
		JoinedAt:      now.Format(time.RFC3339),
		Status:        "active",
	})

	gp.recalculateLocked()
	gp.doSave()

	slog.Info("global pool: node joined",
		"node_id", nodeID,
		"region", region,
		"contribution", initialContribution,
		"total_participants", len(gp.ParticipantNodes),
	)

	return nil
}

// Contribute adds tokens to the global pool on behalf of a node.
func (gp *GlobalPool) Contribute(nodeID string, amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("contribution amount must be positive")
	}

	gp.mu.Lock()
	defer gp.mu.Unlock()

	found := false
	for _, n := range gp.ParticipantNodes {
		if n.NodeID == nodeID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("node %s is not a pool participant", nodeID)
	}

	gp.NodeContributions[nodeID] += amount
	gp.recalculateLocked()
	gp.doSave()

	slog.Info("global pool: contribution recorded",
		"node_id", nodeID,
		"amount", amount,
		"total_from_node", gp.NodeContributions[nodeID],
	)

	return nil
}

// RecordConsumption records that a node has consumed tokens via the global pool.
func (gp *GlobalPool) RecordConsumption(nodeID string, amount int64) {
	gp.mu.Lock()
	defer gp.mu.Unlock()

	gp.NodeConsumptions[nodeID] += amount
	gp.TotalConsumed += amount
	gp.recalculateLocked()

	// Async save (avoid blocking on every request)
	go gp.doSave()
}

// recalculateLocked recomputes aggregate values. Caller must hold gp.mu.
func (gp *GlobalPool) recalculateLocked() {
	var totalContrib, totalConsumed int64

	for i, n := range gp.ParticipantNodes {
		contrib := gp.NodeContributions[n.NodeID]
		consumed := gp.NodeConsumptions[n.NodeID]
		totalContrib += contrib
		totalConsumed += consumed

		gp.ParticipantNodes[i].Contributed = contrib
		gp.ParticipantNodes[i].Consumed = consumed
		if contrib > 0 {
			gp.ParticipantNodes[i].Ratio = float64(contrib) / float64(consumed+1)
		} else {
			gp.ParticipantNodes[i].Ratio = 0
		}

		// Update heartbeat status
		if time.Since(n.LastHeartbeat) > 10*time.Minute {
			gp.ParticipantNodes[i].Status = "offline"
		} else if time.Since(n.LastHeartbeat) > 5*time.Minute {
			gp.ParticipantNodes[i].Status = "degraded"
		} else {
			gp.ParticipantNodes[i].Status = "active"
		}
	}

	gp.TotalContributed = totalContrib
	gp.TotalConsumed = totalConsumed
	gp.AvailableQuota = totalContrib - totalConsumed
	if gp.AvailableQuota < 0 {
		gp.AvailableQuota = 0
	}
	gp.LastUpdated = time.Now()
}

// GetStatus returns a snapshot of the global pool state.
func (gp *GlobalPool) GetStatus() map[string]any {
	gp.mu.RLock()
	defer gp.mu.RUnlock()

	activeCount := 0
	for _, n := range gp.ParticipantNodes {
		if n.Status == "active" {
			activeCount++
		}
	}

	return map[string]any{
		"total_contributed":  gp.TotalContributed,
		"total_consumed":     gp.TotalConsumed,
		"available_quota":    gp.AvailableQuota,
		"participant_count":  len(gp.ParticipantNodes),
		"active_count":       activeCount,
		"utilization":        gp.utilizationLocked(),
		"last_updated":       gp.LastUpdated.Format(time.RFC3339),
	}
}

// utilizationLocked returns the pool utilization ratio.
func (gp *GlobalPool) utilizationLocked() float64 {
	if gp.TotalContributed == 0 {
		return 0
	}
	return float64(gp.TotalConsumed) / float64(gp.TotalContributed)
}

// GetNodes returns all participant nodes.
func (gp *GlobalPool) GetNodes() []GlobalPoolNode {
	gp.mu.RLock()
	defer gp.mu.RUnlock()
	result := make([]GlobalPoolNode, len(gp.ParticipantNodes))
	copy(result, gp.ParticipantNodes)
	return result
}

// GetStats returns aggregate statistics.
func (gp *GlobalPool) GetStats() map[string]any {
	gp.mu.RLock()
	defer gp.mu.RUnlock()

	var (
		totalContrib, totalConsumed int64
		regionCounts               = make(map[string]int)
		activeCount, degradedCount  int
		avgRatio                    float64
	)

	for _, n := range gp.ParticipantNodes {
		contrib := gp.NodeContributions[n.NodeID]
		consumed := gp.NodeConsumptions[n.NodeID]
		totalContrib += contrib
		totalConsumed += consumed

		if n.Region != "" {
			regionCounts[n.Region]++
		}
		switch n.Status {
		case "active":
			activeCount++
		case "degraded":
			degradedCount++
		}
		avgRatio += n.Ratio
	}

	nodeCount := len(gp.ParticipantNodes)
	if nodeCount > 0 {
		avgRatio /= float64(nodeCount)
	}

	return map[string]any{
		"total_contributed":     totalContrib,
		"total_consumed":        totalConsumed,
		"available_quota":       totalContrib - totalConsumed,
		"total_participants":    nodeCount,
		"active_nodes":          activeCount,
		"degraded_nodes":        degradedCount,
		"average_contrib_ratio": avgRatio,
		"utilization":           gp.utilizationLocked(),
		"regions":               regionCounts,
		"top_contributors":      gp.topContributorsLocked(10),
	}
}

// topContributorsLocked returns the top N contributors.
func (gp *GlobalPool) topContributorsLocked(n int) []map[string]any {
	type nodeContrib struct {
		NodeID string
		Amount int64
	}
	var nodes []nodeContrib
	for id, amount := range gp.NodeContributions {
		nodes = append(nodes, nodeContrib{id, amount})
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Amount > nodes[j].Amount
	})
	if len(nodes) > n {
		nodes = nodes[:n]
	}
	result := make([]map[string]any, len(nodes))
	for i, nc := range nodes {
		result[i] = map[string]any{
			"node_id":     nc.NodeID,
			"contributed": nc.Amount,
			"consumed":    gp.NodeConsumptions[nc.NodeID],
		}
	}
	return result
}

// SelectBestNode selects the best node for routing a global key request.
//综合考虑: contribution ratio, reputation, latency, load.
func (gp *GlobalPool) SelectBestNode(requestedRegion string) *GlobalPoolNode {
	gp.mu.RLock()
	defer gp.mu.RUnlock()

	var bestNode *GlobalPoolNode
	bestScore := 0.0

	for i, n := range gp.ParticipantNodes {
		if n.Status != "active" {
			continue
		}

		// Score components:
		// 1. Contribution ratio (higher = more generous node, preferred)
		ratioScore := n.Ratio
		if ratioScore > 10 {
			ratioScore = 10 // cap
		}
		ratioScore /= 10.0 // normalize to 0-1

		// 2. Available quota (more = better)
		available := gp.NodeContributions[n.NodeID] - gp.NodeConsumptions[n.NodeID]
		quotaScore := 0.0
		if gp.TotalContributed > 0 {
			quotaScore = float64(available) / float64(gp.TotalContributed)
		}
		if quotaScore > 1 {
			quotaScore = 1
		}

		// 3. Region match bonus
		regionScore := 0.5
		if requestedRegion != "" && n.Region == requestedRegion {
			regionScore = 1.0
		}

		// 4. Reputation (if available)
		repScore := 0.5
		if n.Reputation > 0 {
			repScore = n.Reputation
		} else if repMgr != nil {
			if rep := repMgr.GetReputation(n.NodeID); rep != nil {
				repScore = rep.OverallScore
			}
		}

		// Weighted composite score
		score := 0.3*ratioScore + 0.25*quotaScore + 0.15*regionScore + 0.3*repScore

		if score > bestScore {
			bestScore = score
			bestNode = &gp.ParticipantNodes[i]
		}
	}

	return bestNode
}

// Heartbeat updates a node's heartbeat timestamp in the pool.
func (gp *GlobalPool) Heartbeat(nodeID string) {
	gp.mu.Lock()
	defer gp.mu.Unlock()

	for i, n := range gp.ParticipantNodes {
		if n.NodeID == nodeID {
			gp.ParticipantNodes[i].LastHeartbeat = time.Now()
			gp.ParticipantNodes[i].Status = "active"
			return
		}
	}
}

// refreshLoop periodically refreshes pool state and cleans stale entries.
func (gp *GlobalPool) refreshLoop() {
	ticker := time.NewTicker(globalPoolRefreshInterval)
	defer ticker.Stop()
	for range ticker.C {
		gp.mu.Lock()
		now := time.Now()
		for i, n := range gp.ParticipantNodes {
			if now.Sub(n.LastHeartbeat) > 10*time.Minute {
				gp.ParticipantNodes[i].Status = "offline"
			} else if now.Sub(n.LastHeartbeat) > 5*time.Minute {
				gp.ParticipantNodes[i].Status = "degraded"
			}
		}
		gp.recalculateLocked()
		gp.mu.Unlock()
		gp.doSave()
	}
}

// ============================================================
// API Handlers — Global Pool
// ============================================================

// GET /api/network/global-pool — view global pool status
func handleGlobalPoolStatus(w http.ResponseWriter, r *http.Request) {
	if globalPool == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "message": "global pool not initialized"})
		return
	}
	status := globalPool.GetStatus()
	status["enabled"] = true
	status["min_join_contribution"] = globalPoolMinJoinContribution
	status["min_signing_threshold"] = globalKeySigningThreshold
	writeJSON(w, 200, status)
}

// POST /api/network/global-pool/join — join the global pool
func handleGlobalPoolJoin(w http.ResponseWriter, r *http.Request) {
	if globalPool == nil {
		writeError(w, 500, "global pool not initialized")
		return
	}

	var body struct {
		NodeID     string `json:"node_id"`
		Region     string `json:"region"`
		Amount     int64  `json:"amount"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if body.NodeID == "" {
		if netMgr != nil {
			body.NodeID = netMgr.GetNodeID()
		}
	}
	if body.NodeID == "" {
		writeError(w, 400, "node_id is required")
		return
	}
	if body.Amount <= 0 {
		body.Amount = globalPoolMinJoinContribution
	}

	if err := globalPool.JoinPool(body.NodeID, body.Region, body.Amount); err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"status":   "joined",
		"node_id":  body.NodeID,
		"region":   body.Region,
		"amount":   body.Amount,
	})
}

// POST /api/network/global-pool/contribute — contribute tokens to the pool
func handleGlobalPoolContribute(w http.ResponseWriter, r *http.Request) {
	if globalPool == nil {
		writeError(w, 500, "global pool not initialized")
		return
	}

	var body struct {
		NodeID string `json:"node_id"`
		Amount int64  `json:"amount"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if body.NodeID == "" {
		if netMgr != nil {
			body.NodeID = netMgr.GetNodeID()
		}
	}
	if body.NodeID == "" {
		writeError(w, 400, "node_id is required")
		return
	}
	if body.Amount <= 0 {
		writeError(w, 400, "amount must be positive")
		return
	}

	if err := globalPool.Contribute(body.NodeID, body.Amount); err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"status":  "contributed",
		"node_id": body.NodeID,
		"amount":  body.Amount,
	})
}

// GET /api/network/global-pool/nodes — list participant nodes
func handleGlobalPoolNodes(w http.ResponseWriter, r *http.Request) {
	if globalPool == nil {
		writeJSON(w, 200, map[string]any{"nodes": []any{}})
		return
	}

	nodes := globalPool.GetNodes()
	writeJSON(w, 200, map[string]any{
		"nodes": nodes,
		"count": len(nodes),
	})
}

// GET /api/network/global-pool/stats — global pool statistics
func handleGlobalPoolStats(w http.ResponseWriter, r *http.Request) {
	if globalPool == nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}

	stats := globalPool.GetStats()
	writeJSON(w, 200, stats)
}
