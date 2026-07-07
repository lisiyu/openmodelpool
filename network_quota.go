package main

import (
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ============================================================
// Phase 3: Open Key Quota Dynamic Calculation
// ============================================================
//
// Open key quotas are dynamically calculated based on:
//   1. Total network resources (sum of all active nodes' available tokens)
//   2. The openKeyRatio parameter (default 30% of total)
//   3. Each user's share = TrustWeight * reputation_share + ContribWeight * contribution_share
//
// Quotas are refreshed every 5 minutes to adapt to network changes.

// OpenKeyQuotaManager handles dynamic quota calculation.
type OpenKeyQuotaManager struct {
	mu            sync.RWMutex
	chain         *AlgorithmChain
	quotaCache    map[string]*QuotaInfo // nodeID -> cached quota
	lastRefresh   time.Time
}

// QuotaInfo holds the computed quota for a node.
type QuotaInfo struct {
	NodeID           string  `json:"node_id"`
	GlobalQuota      int64   `json:"global_quota"`
	UserQuota        int64   `json:"user_quota"`
	ReputationShare  float64 `json:"reputation_share"`
	ContributionShare float64 `json:"contribution_share"`
	LastUpdated      string  `json:"last_updated"`
}

var quotaMgr *OpenKeyQuotaManager

// initQuotaManager creates the quota manager and starts auto-refresh.
func initQuotaManager(chain *AlgorithmChain) {
	quotaMgr = &OpenKeyQuotaManager{
		chain:      chain,
		quotaCache: make(map[string]*QuotaInfo),
	}
	go quotaMgr.startAutoRefresh()
	slog.Info("open key quota manager initialized")
}

// ============================================================
// Quota Calculation
// ============================================================

// CalculateGlobalQuota computes the total open key quota for the network.
// Formula: globalQuota = totalNetworkResources * openKeyRatio
func (m *OpenKeyQuotaManager) CalculateGlobalQuota() int64 {
	if netMgr == nil {
		return 0
	}

	netMgr.mu.RLock()
	defer netMgr.mu.RUnlock()

	// Sum up all nodes' available tokens
	var totalResources int64

	// Self resources
	if netMgr.config.ContribPoints > 0 {
		totalResources += netMgr.config.ContribPoints * 1000 // contribution points ~ tokens
	}

	// Peer resources
	for _, peer := range netMgr.config.Peers {
		if peer.Status == "online" {
			// Estimate peer resources from trust score and reputation
			peerResources := int64(peer.TrustScore * 10000)
			totalResources += peerResources
		}
	}

	// Also check federation trust pool for additional nodes
	if fed != nil {
		pool := fed.GetTrustPool()
		for _, n := range pool.Nodes {
			if n.TokenBudget > 0 {
				totalResources += n.TokenBudget
			} else if n.Status == "active" {
				// Default estimated resources for active nodes
				totalResources += 50000
			}
		}
	}

	// Minimum baseline
	if totalResources < 10000 {
		totalResources = 10000
	}

	// Apply openKeyRatio from algorithm chain
	params := m.chain.GetCurrentParams()
	globalQuota := int64(float64(totalResources) * params.OpenKeyRatio)

	return globalQuota
}

// CalculateUserQuota computes the quota for a specific node.
// Formula: userQuota = globalQuota * (trustWeight * repShare + contribWeight * contribShare)
func (m *OpenKeyQuotaManager) CalculateUserQuota(nodeID string) *QuotaInfo {
	globalQuota := m.CalculateGlobalQuota()
	params := m.chain.GetCurrentParams()

	// Get network-wide totals
	var (
		totalReputation float64
		totalContrib    float64
		nodeReputation  float64
		nodeContrib     float64
	)

	if netMgr != nil {
		netMgr.mu.RLock()

		// Self stats
		selfContrib := float64(netMgr.config.ContribPoints)
		totalContrib += selfContrib
		totalReputation += 50.0 // baseline reputation for self

		if nodeID == netMgr.config.NodeID {
			nodeContrib = selfContrib
			nodeReputation = 50.0
		}

		// Peer stats
		for _, peer := range netMgr.config.Peers {
			peerContrib := peer.TrustScore * 100 // approximate contribution from trust
			peerRep := peer.TrustScore

			totalContrib += peerContrib
			totalReputation += peerRep

			if peer.NodeID == nodeID {
				nodeContrib = peerContrib
				nodeReputation = peerRep
			}
		}

		netMgr.mu.RUnlock()
	}

	// Also check reputation manager for more accurate data
	if repMgr != nil {
		allReps := repMgr.GetAllReputations()
		for nid, rep := range allReps {
			if nid == nodeID {
				nodeReputation = rep.OverallScore
			}
			totalReputation += rep.OverallScore
		}
		// If this node has a reputation entry, use the more accurate value
		if rep := repMgr.GetReputation(nodeID); rep != nil {
			nodeReputation = rep.OverallScore
		}
	}

	// Avoid division by zero
	if totalReputation == 0 {
		totalReputation = 1
	}
	if totalContrib == 0 {
		totalContrib = 1
	}

	reputationShare := nodeReputation / totalReputation
	contributionShare := nodeContrib / totalContrib

	// Calculate weighted share
	weightedShare := params.TrustWeight*reputationShare + params.ContribWeight*contributionShare

	// Apply to global quota
	userQuota := int64(float64(globalQuota) * weightedShare)

	// Enforce minimum and maximum bounds
	if userQuota < 100 {
		userQuota = 100
	}
	if userQuota > globalQuota {
		userQuota = globalQuota
	}

	info := &QuotaInfo{
		NodeID:            nodeID,
		GlobalQuota:       globalQuota,
		UserQuota:         userQuota,
		ReputationShare:   reputationShare,
		ContributionShare: contributionShare,
		LastUpdated:       time.Now().UTC().Format(time.RFC3339),
	}

	// Cache the result
	m.mu.Lock()
	m.quotaCache[nodeID] = info
	m.mu.Unlock()

	return info
}

// GetCachedQuota returns the cached quota for a node, or calculates if not cached.
func (m *OpenKeyQuotaManager) GetCachedQuota(nodeID string) *QuotaInfo {
	m.mu.RLock()
	info, ok := m.quotaCache[nodeID]
	m.mu.RUnlock()
	if ok {
		return info
	}
	return m.CalculateUserQuota(nodeID)
}

// RefreshAllQuotas recalculates quotas for all known nodes.
func (m *OpenKeyQuotaManager) RefreshAllQuotas() {
	if netMgr == nil {
		return
	}

	nodes := make([]string, 0)

	netMgr.mu.RLock()
	// Add self
	if netMgr.config.NodeID != "" {
		nodes = append(nodes, netMgr.config.NodeID)
	}
	// Add peers
	for _, peer := range netMgr.config.Peers {
		nodes = append(nodes, peer.NodeID)
	}
	netMgr.mu.RUnlock()

	// Add nodes from unlock states
	if netMgr != nil {
		netMgr.mu.RLock()
		for nodeID := range netMgr.config.NodeUnlockStates {
			found := false
			for _, n := range nodes {
				if n == nodeID {
					found = true
					break
				}
			}
			if !found {
				nodes = append(nodes, nodeID)
			}
		}
		netMgr.mu.RUnlock()
	}

	for _, nodeID := range nodes {
		m.CalculateUserQuota(nodeID)
	}

	m.mu.Lock()
	m.lastRefresh = time.Now()
	m.mu.Unlock()

	slog.Debug("refreshed all open key quotas", "nodes", len(nodes))
}

// startAutoRefresh refreshes quotas every 5 minutes.
func (m *OpenKeyQuotaManager) startAutoRefresh() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.RefreshAllQuotas()
	}
}

// ============================================================
// Phase 4: Global Pool Quota Calculation
// ============================================================

// CalculateGlobalPoolQuota computes the effective quota available through the global pool.
// This takes into account:
//   - Total contributions across all nodes
//   - Total consumptions
//   - The globalPoolAvailabilityRatio (default 0.8 — keep 20% reserve)
func CalculateGlobalPoolQuota() int64 {
	if globalPool == nil {
		return 0
	}
	globalPool.mu.RLock()
	defer globalPool.mu.RUnlock()

	available := globalPool.AvailableQuota
	if available <= 0 {
		return 0
	}

	// Apply availability ratio to keep a reserve
	ratio := 0.8
	if algoChain != nil {
		params := algoChain.GetCurrentParams()
		if params.GlobalPoolAvailabilityRatio > 0 {
			ratio = params.GlobalPoolAvailabilityRatio
		}
	}

	return int64(float64(available) * ratio)
}

// CalculateNodeGlobalPoolShare calculates a specific node's effective share in the global pool.
// share = nodeContribution / totalContribution * globalPoolQuota
func CalculateNodeGlobalPoolShare(nodeID string) int64 {
	if globalPool == nil {
		return 0
	}
	globalPool.mu.RLock()
	defer globalPool.mu.RUnlock()

	if globalPool.TotalContributed <= 0 {
		return 0
	}

	nodeContrib := globalPool.NodeContributions[nodeID]
	if nodeContrib <= 0 {
		return 0
	}

	totalQuota := CalculateGlobalPoolQuota()
	share := float64(nodeContrib) / float64(globalPool.TotalContributed) * float64(totalQuota)
	return int64(share)
}

// ============================================================
// API Handlers
// ============================================================

// GET /api/network/open-key-quota?node_id=mmx-xxx
func handleOpenKeyQuota(w http.ResponseWriter, r *http.Request) {
	if quotaMgr == nil || algoChain == nil {
		writeError(w, 500, "quota manager not initialized")
		return
	}

	nodeID := r.URL.Query().Get("node_id")
	if nodeID == "" {
		// Default to this node
		if netMgr != nil {
			nodeID = netMgr.GetNodeID()
		}
	}
	if nodeID == "" {
		writeError(w, 400, "node_id is required")
		return
	}

	quotaInfo := quotaMgr.CalculateUserQuota(nodeID)
	globalQuota := quotaMgr.CalculateGlobalQuota()

	writeJSON(w, 200, map[string]any{
		"node_id":             quotaInfo.NodeID,
		"global_quota":        globalQuota,
		"user_quota":          quotaInfo.UserQuota,
		"reputation_share":    quotaInfo.ReputationShare,
		"contribution_share":  quotaInfo.ContributionShare,
		"algorithm_params":    algoChain.GetCurrentParams(),
		"last_updated":        quotaInfo.LastUpdated,
	})
}

// GET /api/network/open-key-quota/all — get quotas for all nodes
func handleOpenKeyQuotaAll(w http.ResponseWriter, r *http.Request) {
	if quotaMgr == nil || algoChain == nil {
		writeError(w, 500, "quota manager not initialized")
		return
	}

	quotaMgr.RefreshAllQuotas()

	quotaMgr.mu.RLock()
	quotas := make([]*QuotaInfo, 0, len(quotaMgr.quotaCache))
	for _, info := range quotaMgr.quotaCache {
		quotas = append(quotas, info)
	}
	quotaMgr.mu.RUnlock()

	writeJSON(w, 200, map[string]any{
		"quotas":       quotas,
		"global_quota": quotaMgr.CalculateGlobalQuota(),
		"params":       algoChain.GetCurrentParams(),
	})
}
