package main

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

// ============================================================
// Phase 4: Dynamic Balance Engine
// ============================================================
//
// The BalanceEngine tracks each node's contribution vs. consumption
// of network resources and applies adjustments to maintain fairness:
//   - Over-consumers (ratio < 0.5) get lower priority and routing weight.
//   - Over-contributors (ratio > 3.0) get boosted priority and extra quota.
//   - Balanced nodes operate normally.
//
// Adjustments are recalculated every 5 minutes and recorded for audit.

// NodeBalance tracks a single node's contribution/consumption balance.
type NodeBalance struct {
	NodeID           string  `json:"node_id"`
	TotalContributed int64   `json:"total_contributed"` // cumulative tokens contributed
	TotalConsumed    int64   `json:"total_consumed"`    // cumulative tokens consumed
	Balance          float64 `json:"balance"`           // contrib/consumed ratio (>1 = net contributor)
	ContributionRate float64 `json:"contribution_rate"` // tokens/hour
	ConsumptionRate  float64 `json:"consumption_rate"`  // tokens/hour
	LastCalculated   string  `json:"last_calculated"`
}

// GlobalBalance holds network-wide balance statistics.
type GlobalBalance struct {
	TotalNetworkContribution int64   `json:"total_network_contribution"`
	TotalNetworkConsumption  int64   `json:"total_network_consumption"`
	NetworkBalanceRatio      float64 `json:"network_balance_ratio"`  // total contrib / total consumed
	AverageNodeBalance       float64 `json:"average_node_balance"`
	ImbalanceNodes           int     `json:"imbalance_nodes"`        // count of nodes with ratio < 0.5 or > 3.0
}

// BalanceConfig controls the balance engine behavior.
type BalanceConfig struct {
	TargetRatio              float64 `json:"target_ratio"`                // target contrib/consumed ratio (default 1.0)
	UnderConsumerThreshold   float64 `json:"under_consumer_threshold"`    // below this = over-consumer (0.5)
	OverContributorThreshold float64 `json:"over_contributor_threshold"`  // above this = over-contributor (3.0)
	AdjustmentStrength       float64 `json:"adjustment_strength"`         // 0-1, how aggressively to adjust (0.3)
	QuotaAdjustment          bool    `json:"quota_adjustment"`            // enable quota modulation
	PriorityAdjustment       bool    `json:"priority_adjustment"`         // enable priority modulation
	RoutingPreference        bool    `json:"routing_preference"`          // enable routing weight modulation
}

// DefaultBalanceConfig returns sensible defaults.
func DefaultBalanceConfig() BalanceConfig {
	return BalanceConfig{
		TargetRatio:              1.0,
		UnderConsumerThreshold:   0.5,
		OverContributorThreshold: 3.0,
		AdjustmentStrength:       0.3,
		QuotaAdjustment:          true,
		PriorityAdjustment:       true,
		RoutingPreference:        true,
	}
}

// BalanceAdjustment is the computed adjustment for a node.
type BalanceAdjustment struct {
	NodeID                  string  `json:"node_id"`
	Type                    string  `json:"type"`                       // "reduce_priority", "boost_priority", "balanced"
	PriorityDelta           int     `json:"priority_delta"`
	RoutingWeightMultiplier float64 `json:"routing_weight_multiplier"`
	QuotaMultiplier         float64 `json:"quota_multiplier"`
	Suggestion              string  `json:"suggestion,omitempty"`
	AppliedAt               string  `json:"applied_at"`
}

// BalanceHistory records a past balance cycle for audit.
type BalanceHistory struct {
	Timestamp    string                `json:"timestamp"`
	GlobalSnap   GlobalBalance         `json:"global"`
	Adjustments  []*BalanceAdjustment  `json:"adjustments"`
	CycleDurationMS int64             `json:"cycle_duration_ms"`
}

// BalanceEngine is the main dynamic balance controller.
type BalanceEngine struct {
	mu          sync.RWMutex
	nodeBalance map[string]*NodeBalance
	globalBal   GlobalBalance
	config      BalanceConfig
	adjustments map[string]*BalanceAdjustment // nodeID -> current adjustment
	history     []BalanceHistory              // recent cycle history
	stopCh      chan struct{}
}

var balanceEngine *BalanceEngine

// initBalanceEngine creates and starts the balance engine.
func initBalanceEngine() {
	balanceEngine = &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	slog.Info("balance engine initialized")
}

// StartBalanceLoop begins the periodic balance check cycle.
func StartBalanceLoop(ctx context.Context) {
	if balanceEngine == nil {
		return
	}
	balanceEngine.stopCh = make(chan struct{})

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		slog.Info("balance loop started", "interval", "5m")

		for {
			select {
			case <-ticker.C:
				balanceEngine.RunBalanceCycle(ctx)
			case <-balanceEngine.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopBalanceLoop stops the balance loop.
func StopBalanceLoop() {
	if balanceEngine != nil && balanceEngine.stopCh != nil {
		close(balanceEngine.stopCh)
		balanceEngine.stopCh = nil
	}
}

// ============================================================
// Contribution / Consumption Recording
// ============================================================

// RecordContributionBalance records tokens contributed by a node (e.g. relay serving).
func (be *BalanceEngine) RecordContributionBalance(nodeID string, tokens int64) {
	if be == nil || tokens <= 0 {
		return
	}
	be.mu.Lock()
	defer be.mu.Unlock()

	nb, ok := be.nodeBalance[nodeID]
	if !ok {
		nb = &NodeBalance{NodeID: nodeID}
		be.nodeBalance[nodeID] = nb
	}
	nb.TotalContributed += tokens
}

// RecordConsumptionBalance records tokens consumed by a node (e.g. making relay requests).
func (be *BalanceEngine) RecordConsumptionBalance(nodeID string, tokens int64) {
	if be == nil || tokens <= 0 {
		return
	}
	be.mu.Lock()
	defer be.mu.Unlock()

	nb, ok := be.nodeBalance[nodeID]
	if !ok {
		nb = &NodeBalance{NodeID: nodeID}
		be.nodeBalance[nodeID] = nb
	}
	nb.TotalConsumed += tokens
}

// ============================================================
// Balance Calculation
// ============================================================

// recalculateBalancesLocked recalculates all node balances and global stats.
// Caller must hold be.mu.
func (be *BalanceEngine) recalculateBalancesLocked() {
	now := time.Now()

	totalContrib := int64(0)
	totalConsumed := int64(0)
	sumBalance := 0.0
	imbalanceCount := 0

	for _, nb := range be.nodeBalance {
		totalContrib += nb.TotalContributed
		totalConsumed += nb.TotalConsumed

		// Calculate balance ratio
		if nb.TotalConsumed > 0 {
			nb.Balance = float64(nb.TotalContributed) / float64(nb.TotalConsumed)
		} else if nb.TotalContributed > 0 {
			nb.Balance = 999.0 // very high if contributed but never consumed
		} else {
			nb.Balance = 1.0 // neutral if no activity
		}

		// Calculate rates (rough: total / estimated hours online)
		// Use a simplified estimate: assume 1 hour if no better data
		hoursOnline := 1.0
		nb.ContributionRate = float64(nb.TotalContributed) / hoursOnline
		nb.ConsumptionRate = float64(nb.TotalConsumed) / hoursOnline
		nb.LastCalculated = now.Format(time.RFC3339)

		sumBalance += nb.Balance

		if nb.Balance < be.config.UnderConsumerThreshold || nb.Balance > be.config.OverContributorThreshold {
			imbalanceCount++
		}
	}

	nodeCount := len(be.nodeBalance)
	if nodeCount == 0 {
		nodeCount = 1
	}

	be.globalBal = GlobalBalance{
		TotalNetworkContribution: totalContrib,
		TotalNetworkConsumption:  totalConsumed,
		NetworkBalanceRatio:      0,
		AverageNodeBalance:       sumBalance / float64(nodeCount),
		ImbalanceNodes:           imbalanceCount,
	}
	if totalConsumed > 0 {
		be.globalBal.NetworkBalanceRatio = float64(totalContrib) / float64(totalConsumed)
	}
}

// ============================================================
// Adjustment Calculation
// ============================================================

// CalculateAdjustment computes the adjustment for a specific node.
func (be *BalanceEngine) CalculateAdjustment(nodeID string) *BalanceAdjustment {
	be.mu.RLock()
	defer be.mu.RUnlock()

	balance, ok := be.nodeBalance[nodeID]
	if !ok {
		return &BalanceAdjustment{
			NodeID:                  nodeID,
			Type:                    "balanced",
			RoutingWeightMultiplier: 1.0,
			QuotaMultiplier:         1.0,
			AppliedAt:               time.Now().Format(time.RFC3339),
		}
	}

	now := time.Now().Format(time.RFC3339)

	if balance.Balance < be.config.UnderConsumerThreshold {
		// Over-consumer: reduce priority, reduce routing weight
		multiplier := balance.Balance
		if multiplier < 0.1 {
			multiplier = 0.1 // floor
		}
		// Apply adjustment strength
		if be.config.AdjustmentStrength > 0 {
			// Blend between 1.0 and the raw multiplier
			multiplier = 1.0 + (multiplier-1.0)*be.config.AdjustmentStrength
		}

		return &BalanceAdjustment{
			NodeID:                  nodeID,
			Type:                    "reduce_priority",
			PriorityDelta:           -1,
			RoutingWeightMultiplier: multiplier,
			QuotaMultiplier:         multiplier,
			Suggestion:              "需要贡献更多算力以维持网络平衡",
			AppliedAt:               now,
		}
	}

	if balance.Balance > be.config.OverContributorThreshold {
		// Over-contributor: boost priority, increase routing weight
		boostFactor := 1.5
		if be.config.AdjustmentStrength > 0 {
			boostFactor = 1.0 + (boostFactor-1.0)*be.config.AdjustmentStrength
		}

		return &BalanceAdjustment{
			NodeID:                  nodeID,
			Type:                    "boost_priority",
			PriorityDelta:           1,
			RoutingWeightMultiplier: boostFactor,
			QuotaMultiplier:         boostFactor,
			Suggestion:              "高贡献者，享受优先路由和额外消费额度",
			AppliedAt:               now,
		}
	}

	return &BalanceAdjustment{
		NodeID:                  nodeID,
		Type:                    "balanced",
		PriorityDelta:           0,
		RoutingWeightMultiplier: 1.0,
		QuotaMultiplier:         1.0,
		AppliedAt:               now,
	}
}

// ============================================================
// Balance Cycle
// ============================================================

// RunBalanceCycle runs a complete balance check and adjustment cycle.
func (be *BalanceEngine) RunBalanceCycle(ctx context.Context) {
	startTime := time.Now()

	be.mu.Lock()

	// 1. Recalculate all balances
	be.recalculateBalancesLocked()

	// 2. Compute adjustments for all nodes
	newAdjustments := make(map[string]*BalanceAdjustment)
	for nodeID := range be.nodeBalance {
		adj := be.calculateAdjustmentLocked(nodeID)
		newAdjustments[nodeID] = adj
	}

	// 3. Apply adjustments
	be.adjustments = newAdjustments

	// 4. Record history
	historyEntry := BalanceHistory{
		Timestamp:       startTime.Format(time.RFC3339),
		GlobalSnap:      be.globalBal,
		Adjustments:     make([]*BalanceAdjustment, 0, len(newAdjustments)),
		CycleDurationMS: time.Since(startTime).Milliseconds(),
	}
	for _, adj := range newAdjustments {
		historyEntry.Adjustments = append(historyEntry.Adjustments, adj)
	}
	be.history = append(be.history, historyEntry)
	// Keep only last 100 entries
	if len(be.history) > 100 {
		be.history = be.history[len(be.history)-100:]
	}

	globalSnap := be.globalBal
	be.mu.Unlock()

	// 5. Log summary
	imbalanceSummary := ""
	if globalSnap.ImbalanceNodes > 0 {
		imbalanceSummary = " (⚠ imbalance detected)"
	}
	slog.Info("balance cycle completed",
		"nodes", len(be.nodeBalance),
		"global_ratio", globalSnap.NetworkBalanceRatio,
		"avg_balance", globalSnap.AverageNodeBalance,
		"imbalance", globalSnap.ImbalanceNodes,
		"duration_ms", time.Since(startTime).Milliseconds(),
		"note", imbalanceSummary,
	)

	// 6. Optionally record to algorithm chain (Phase 3 consensus)
	if algoChain != nil && globalSnap.ImbalanceNodes > 0 {
		slog.Info("balance imbalance detected, consider algorithm parameter adjustment",
			"imbalance_nodes", globalSnap.ImbalanceNodes,
			"avg_balance", globalSnap.AverageNodeBalance,
		)
	}
}

// calculateAdjustmentLocked computes adjustment while lock is held.
func (be *BalanceEngine) calculateAdjustmentLocked(nodeID string) *BalanceAdjustment {
	balance, ok := be.nodeBalance[nodeID]
	if !ok {
		return &BalanceAdjustment{
			NodeID:                  nodeID,
			Type:                    "balanced",
			RoutingWeightMultiplier: 1.0,
			QuotaMultiplier:         1.0,
			AppliedAt:               time.Now().Format(time.RFC3339),
		}
	}

	now := time.Now().Format(time.RFC3339)

	if balance.Balance < be.config.UnderConsumerThreshold {
		multiplier := balance.Balance
		if multiplier < 0.1 {
			multiplier = 0.1
		}
		if be.config.AdjustmentStrength > 0 {
			multiplier = 1.0 + (multiplier-1.0)*be.config.AdjustmentStrength
		}
		return &BalanceAdjustment{
			NodeID:                  nodeID,
			Type:                    "reduce_priority",
			PriorityDelta:           -1,
			RoutingWeightMultiplier: multiplier,
			QuotaMultiplier:         multiplier,
			Suggestion:              "需要贡献更多算力以维持网络平衡",
			AppliedAt:               now,
		}
	}

	if balance.Balance > be.config.OverContributorThreshold {
		boostFactor := 1.5
		if be.config.AdjustmentStrength > 0 {
			boostFactor = 1.0 + (boostFactor-1.0)*be.config.AdjustmentStrength
		}
		return &BalanceAdjustment{
			NodeID:                  nodeID,
			Type:                    "boost_priority",
			PriorityDelta:           1,
			RoutingWeightMultiplier: boostFactor,
			QuotaMultiplier:         boostFactor,
			Suggestion:              "高贡献者，享受优先路由和额外消费额度",
			AppliedAt:               now,
		}
	}

	return &BalanceAdjustment{
		NodeID:                  nodeID,
		Type:                    "balanced",
		PriorityDelta:           0,
		RoutingWeightMultiplier: 1.0,
		QuotaMultiplier:         1.0,
		AppliedAt:               now,
	}
}

// ============================================================
// Query Methods
// ============================================================

// GetBalanceStatus returns the current global balance status.
func (be *BalanceEngine) GetBalanceStatus() map[string]any {
	be.mu.RLock()
	defer be.mu.RUnlock()

	return map[string]any{
		"global":     be.globalBal,
		"node_count": len(be.nodeBalance),
		"config":     be.config,
		"last_cycle": len(be.history) > 0,
	}
}

// GetAllNodeBalances returns balance info for all nodes.
func (be *BalanceEngine) GetAllNodeBalances() []*NodeBalance {
	be.mu.RLock()
	defer be.mu.RUnlock()

	result := make([]*NodeBalance, 0, len(be.nodeBalance))
	for _, nb := range be.nodeBalance {
		cp := *nb
		result = append(result, &cp)
	}

	// Sort by balance ratio
	sort.Slice(result, func(i, j int) bool {
		return result[i].Balance < result[j].Balance
	})
	return result
}

// GetAllAdjustments returns current adjustments for all nodes.
func (be *BalanceEngine) GetAllAdjustments() []*BalanceAdjustment {
	be.mu.RLock()
	defer be.mu.RUnlock()

	result := make([]*BalanceAdjustment, 0, len(be.adjustments))
	for _, adj := range be.adjustments {
		cp := *adj
		result = append(result, &cp)
	}

	// Sort: reduce_priority first, then balanced, then boost_priority
	sort.Slice(result, func(i, j int) bool {
		order := map[string]int{"reduce_priority": 0, "balanced": 1, "boost_priority": 2}
		return order[result[i].Type] < order[result[j].Type]
	})
	return result
}

// GetAdjustmentForNode returns the current adjustment for a specific node.
func (be *BalanceEngine) GetAdjustmentForNode(nodeID string) *BalanceAdjustment {
	be.mu.RLock()
	defer be.mu.RUnlock()

	adj, ok := be.adjustments[nodeID]
	if !ok {
		return &BalanceAdjustment{
			NodeID:                  nodeID,
			Type:                    "balanced",
			RoutingWeightMultiplier: 1.0,
			QuotaMultiplier:         1.0,
		}
	}
	cp := *adj
	return &cp
}

// GetBalanceHistory returns recent balance cycle history.
func (be *BalanceEngine) GetBalanceHistory(limit int) []BalanceHistory {
	be.mu.RLock()
	defer be.mu.RUnlock()

	if limit <= 0 || limit > len(be.history) {
		limit = len(be.history)
	}
	start := len(be.history) - limit
	if start < 0 {
		start = 0
	}
	result := make([]BalanceHistory, limit)
	copy(result, be.history[start:])
	return result
}

// GetRoutingWeightMultiplier returns the balance-based routing weight multiplier for a node.
func (be *BalanceEngine) GetRoutingWeightMultiplier(nodeID string) float64 {
	if be == nil {
		return 1.0
	}
	be.mu.RLock()
	defer be.mu.RUnlock()

	adj, ok := be.adjustments[nodeID]
	if !ok {
		return 1.0
	}
	return adj.RoutingWeightMultiplier
}

// GetPriorityDelta returns the priority delta for a node from balance adjustments.
func (be *BalanceEngine) GetPriorityDelta(nodeID string) int {
	if be == nil {
		return 0
	}
	be.mu.RLock()
	defer be.mu.RUnlock()

	adj, ok := be.adjustments[nodeID]
	if !ok {
		return 0
	}
	return adj.PriorityDelta
}

// UpdateConfig updates the balance configuration.
func (be *BalanceEngine) UpdateConfig(cfg BalanceConfig) {
	be.mu.Lock()
	defer be.mu.Unlock()

	if cfg.TargetRatio <= 0 {
		cfg.TargetRatio = 1.0
	}
	if cfg.UnderConsumerThreshold <= 0 {
		cfg.UnderConsumerThreshold = 0.5
	}
	if cfg.OverContributorThreshold <= 0 {
		cfg.OverContributorThreshold = 3.0
	}
	if cfg.AdjustmentStrength < 0 || cfg.AdjustmentStrength > 1 {
		cfg.AdjustmentStrength = 0.3
	}

	be.config = cfg
	slog.Info("balance config updated",
		"target_ratio", cfg.TargetRatio,
		"under_threshold", cfg.UnderConsumerThreshold,
		"over_threshold", cfg.OverContributorThreshold,
	)
}

// GetConfig returns the current balance configuration.
func (be *BalanceEngine) GetConfig() BalanceConfig {
	be.mu.RLock()
	defer be.mu.RUnlock()
	return be.config
}

// ============================================================
// API Handlers
// ============================================================

// GET /api/network/balance/status — global balance status
func handleBalanceStatus(w http.ResponseWriter, r *http.Request) {
	if balanceEngine == nil {
		writeJSON(w, 200, map[string]any{"status": "not_initialized"})
		return
	}
	writeJSON(w, 200, balanceEngine.GetBalanceStatus())
}

// GET /api/network/balance/nodes — all node balances
func handleBalanceNodes(w http.ResponseWriter, r *http.Request) {
	if balanceEngine == nil {
		writeJSON(w, 200, map[string]any{"nodes": []any{}})
		return
	}
	balances := balanceEngine.GetAllNodeBalances()
	writeJSON(w, 200, map[string]any{
		"nodes": balances,
		"count": len(balances),
	})
}

// GET /api/network/balance/adjustments — current adjustments
func handleBalanceAdjustments(w http.ResponseWriter, r *http.Request) {
	if balanceEngine == nil {
		writeJSON(w, 200, map[string]any{"adjustments": []any{}})
		return
	}
	adjustments := balanceEngine.GetAllAdjustments()
	writeJSON(w, 200, map[string]any{
		"adjustments": adjustments,
		"count":       len(adjustments),
	})
}

// POST /api/network/balance/recalculate — manually trigger balance calculation
func handleBalanceRecalculate(w http.ResponseWriter, r *http.Request) {
	if balanceEngine == nil {
		writeError(w, 500, "balance engine not initialized")
		return
	}

	ctx := context.Background()
	balanceEngine.RunBalanceCycle(ctx)

	writeJSON(w, 200, map[string]any{
		"status": "recalculated",
		"global": balanceEngine.GetBalanceStatus(),
	})
}

// ============================================================
// Integration helpers
// ============================================================

// GetBalanceRoutingWeight returns the combined routing weight multiplier
// from the balance engine for a given node.
func GetBalanceRoutingWeight(nodeID string) float64 {
	if balanceEngine == nil {
		return 1.0
	}
	return balanceEngine.GetRoutingWeightMultiplier(nodeID)
}

// GetBalancePriorityDelta returns the priority delta from balance engine.
func GetBalancePriorityDelta(nodeID string) int {
	if balanceEngine == nil {
		return 0
	}
	return balanceEngine.GetPriorityDelta(nodeID)
}
