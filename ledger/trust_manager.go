package ledger

import (
	"math"
	"sort"
	"sync"
)

// TrustManager manages peer reputation, trust levels, and penalty assessment.
type TrustManager struct {
	mu sync.RWMutex

	// peerID -> reliability stats
	peerStats map[string]*peerStatsInternal

	// Minimum probes before computing trust.
	minProbes int64
}

type peerStatsInternal struct {
	records     []*TrustRecord
	totalProbes int64
	success     int64
	failed      int64
	totalLatMS  int64
}

// NewTrustManager creates a TrustManager.
func NewTrustManager(minProbes int64) *TrustManager {
	if minProbes <= 0 {
		minProbes = 3
	}
	return &TrustManager{
		peerStats: make(map[string]*peerStatsInternal),
		minProbes: minProbes,
	}
}

// RecordProbe records the result of a trust probe.
func (tm *TrustManager) RecordProbe(subjectPeerID string, success bool, latencyMS int64) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	stats, ok := tm.peerStats[subjectPeerID]
	if !ok {
		stats = &peerStatsInternal{}
		tm.peerStats[subjectPeerID] = stats
	}

	stats.totalProbes++
	stats.totalLatMS += latencyMS
	if success {
		stats.success++
	} else {
		stats.failed++
	}
}

// GetReliability returns the aggregated reliability stats for a peer.
func (tm *TrustManager) GetReliability(peerID string) *NodeReliability {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	stats, ok := tm.peerStats[peerID]
	if !ok || stats.totalProbes == 0 {
		return &NodeReliability{
			PeerID:          peerID,
			ReputationScore: 50.0, // default for new peers
		}
	}

	successRate := float64(stats.success) / float64(stats.totalProbes)
	avgLatency := stats.totalLatMS / stats.totalProbes
	uptimePct := successRate * 100.0

	return &NodeReliability{
		PeerID:          peerID,
		TotalProbes:     stats.totalProbes,
		SuccessProbes:   stats.success,
		FailedProbes:    stats.failed,
		SuccessRate:     successRate,
		AvgLatencyMS:    avgLatency,
		UptimePercent:   uptimePct,
		ReputationScore: tm.computeReputation(stats),
	}
}

// GetTrustLevel determines the progressive trust level for a peer.
func (tm *TrustManager) GetTrustLevel(peerID string) TrustLevel {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	stats, ok := tm.peerStats[peerID]
	if !ok || stats.totalProbes < tm.minProbes {
		return TrustLevelNew
	}

	rate := float64(stats.success) / float64(stats.totalProbes)
	switch {
	case rate >= 0.95 && stats.totalProbes >= 50:
		return TrustLevelVerified
	case rate >= 0.90 && stats.totalProbes >= 20:
		return TrustLevelHigh
	case rate >= 0.70 && stats.totalProbes >= 10:
		return TrustLevelMedium
	case rate >= 0.50:
		return TrustLevelLow
	default:
		return TrustLevelLow
	}
}

// RoutePriority returns a routing priority (0-100, higher is better)
// based on trust level and reliability.
func (tm *TrustManager) RoutePriority(peerID string) float64 {
	level := tm.GetTrustLevel(peerID)
	reliability := tm.GetReliability(peerID)

	base := float64(level) * 20.0 // 0-80
	bonus := reliability.SuccessRate * 20.0
	return math.Min(base+bonus, 100.0)
}

// EvaluatePenalty decides what penalty action to take based on probe statistics.
// Returns the action string: "", "downgrade", "isolate", "ban".
func (tm *TrustManager) EvaluatePenalty(peerID string) string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	stats, ok := tm.peerStats[peerID]
	if !ok || stats.totalProbes < tm.minProbes {
		return ""
	}

	rate := float64(stats.success) / float64(stats.totalProbes)

	switch {
	case rate < 0.10:
		return "ban"
	case rate < 0.30:
		return "isolate"
	case rate < 0.50:
		return "downgrade"
	default:
		return ""
	}
}

// TopPeers returns the top N peers sorted by routing priority.
func (tm *TrustManager) TopPeers(n int) []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	type peerPriority struct {
		PeerID   string
		Priority float64
	}

	var peers []peerPriority
	for pid := range tm.peerStats {
		tm.mu.RUnlock()
		priority := tm.RoutePriority(pid)
		tm.mu.RLock()
		peers = append(peers, peerPriority{pid, priority})
	}

	sort.Slice(peers, func(i, j int) bool {
		return peers[i].Priority > peers[j].Priority
	})

	if n > len(peers) {
		n = len(peers)
	}

	result := make([]string, n)
	for i := 0; i < n; i++ {
		result[i] = peers[i].PeerID
	}
	return result
}

// computeReputation calculates a 0-100 reputation score.
// Must be called with tm.mu held (read lock).
func (tm *TrustManager) computeReputation(stats *peerStatsInternal) float64 {
	if stats.totalProbes == 0 {
		return 50.0
	}

	successRate := float64(stats.success) / float64(stats.totalProbes)

	// Base score from success rate (0-70).
	base := successRate * 70.0

	// Volume bonus (up to 15 points, saturating at 100 probes).
	volumeBonus := math.Min(float64(stats.totalProbes)/100.0, 1.0) * 15.0

	// Latency bonus (up to 15 points). Lower latency = higher bonus.
	avgLat := float64(stats.totalLatMS) / float64(stats.totalProbes)
	latencyBonus := 0.0
	if avgLat < 100 {
		latencyBonus = 15.0
	} else if avgLat < 500 {
		latencyBonus = 15.0 * (1.0 - (avgLat-100.0)/400.0)
	}

	score := base + volumeBonus + latencyBonus
	return math.Min(math.Max(score, 0.0), 100.0)
}
