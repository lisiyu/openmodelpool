package ledger

import (
	"testing"
)

// ============================================================
// TrustManager - RoutePriority tests
// ============================================================

func TestTrustManager_RoutePriority_NewPeer(t *testing.T) {
	tm := NewTrustManager(3)
	tm.RecordProbe("peer-new", true, 50)

	priority := tm.RoutePriority("peer-new")
	// New peer with 1 probe: TrustLevelNew(0) * 20 = 0 base + success bonus = 20
	if priority <= 0 || priority > 100 {
		t.Errorf("RoutePriority for new peer out of range: %f", priority)
	}
}

func TestTrustManager_RoutePriority_VerifiedPeer(t *testing.T) {
	tm := NewTrustManager(3)
	for i := 0; i < 100; i++ {
		tm.RecordProbe("peer-verified", true, 50)
	}

	priority := tm.RoutePriority("peer-verified")
	if priority < 80 {
		t.Errorf("RoutePriority for verified peer should be high, got %f", priority)
	}
	if priority > 100 {
		t.Errorf("RoutePriority should not exceed 100, got %f", priority)
	}
}

func TestTrustManager_RoutePriority_FailedPeer(t *testing.T) {
	tm := NewTrustManager(3)
	for i := 0; i < 20; i++ {
		tm.RecordProbe("peer-bad", false, 500)
	}

	priority := tm.RoutePriority("peer-bad")
	if priority >= 50 {
		t.Errorf("RoutePriority for bad peer should be low, got %f", priority)
	}
}

// ============================================================
// TrustManager - TopPeers tests
// ============================================================

func TestTrustManager_TopPeers_EmptySorter(t *testing.T) {
	tm := NewTrustManager(3)
	peers := tm.TopPeers(5)
	if len(peers) != 0 {
		t.Errorf("TopPeers from empty manager should return empty, got %d", len(peers))
	}
}

func TestTrustManager_TopPeers_SortsCorrectly(t *testing.T) {
	tm := NewTrustManager(3)

	// Add peers with varying quality
	for i := 0; i < 100; i++ {
		tm.RecordProbe("peer-best", true, 10)
	}
	for i := 0; i < 50; i++ {
		tm.RecordProbe("peer-good", i < 48, 50)
	}
	for i := 0; i < 20; i++ {
		tm.RecordProbe("peer-bad", i < 5, 500)
	}

	peers := tm.TopPeers(3)
	if len(peers) != 3 {
		t.Fatalf("expected 3 peers, got %d", len(peers))
	}

	// peer-best should be first
	if peers[0] != "peer-best" {
		t.Errorf("expected peer-best first, got %s", peers[0])
	}
	// peer-bad should be last
	if peers[2] != "peer-bad" {
		t.Errorf("expected peer-bad last, got %s", peers[2])
	}
}

func TestTrustManager_TopPeers_RequestMoreThanAvailable(t *testing.T) {
	tm := NewTrustManager(3)
	for i := 0; i < 10; i++ {
		tm.RecordProbe("peer-1", true, 50)
	}

	peers := tm.TopPeers(10)
	if len(peers) != 1 {
		t.Errorf("expected 1 peer when requesting more than available, got %d", len(peers))
	}
}

// ============================================================
// TrustManager - EvaluatePenalty edge cases
// ============================================================

func TestTrustManager_EvaluatePenalty_Downgrade(t *testing.T) {
	tm := NewTrustManager(3)

	// Peer with 35% success rate should be downgraded (30% <= rate < 50%)
	for i := 0; i < 20; i++ {
		tm.RecordProbe("peer-weak", i < 7, 200)
	}

	action := tm.EvaluatePenalty("peer-weak")
	if action != "downgrade" {
		t.Errorf("expected 'downgrade', got %q", action)
	}
}

func TestTrustManager_EvaluatePenalty_NotEnoughProbes(t *testing.T) {
	tm := NewTrustManager(5)

	// Only 3 probes, below minProbes of 5
	for i := 0; i < 3; i++ {
		tm.RecordProbe("peer-few", false, 500)
	}

	action := tm.EvaluatePenalty("peer-few")
	if action != "" {
		t.Errorf("expected no penalty when below minProbes, got %q", action)
	}
}

func TestTrustManager_EvaluatePenalty_UnknownPeer(t *testing.T) {
	tm := NewTrustManager(3)

	action := tm.EvaluatePenalty("unknown-peer")
	if action != "" {
		t.Errorf("expected no penalty for unknown peer, got %q", action)
	}
}

// ============================================================
// TrustManager - GetTrustLevel edge cases
// ============================================================

func TestTrustManager_GetTrustLevel_BelowMinProbes(t *testing.T) {
	tm := NewTrustManager(5)

	// Record 3 probes, below minProbes(5) threshold
	for i := 0; i < 3; i++ {
		tm.RecordProbe("peer-few", true, 50)
	}

	level := tm.GetTrustLevel("peer-few")
	if level != TrustLevelNew {
		t.Errorf("expected TrustLevelNew when below minProbes, got %s", level.String())
	}
}

func TestTrustManager_GetTrustLevel_HighBoundary(t *testing.T) {
	tm := NewTrustManager(3)

	// 20 probes, 90% success -> TrustLevelHigh
	for i := 0; i < 20; i++ {
		tm.RecordProbe("peer-hb", i < 18, 100)
	}

	level := tm.GetTrustLevel("peer-hb")
	if level != TrustLevelHigh {
		t.Errorf("expected TrustLevelHigh, got %s", level.String())
	}
}

func TestTrustManager_GetTrustLevel_MediumBoundary(t *testing.T) {
	tm := NewTrustManager(3)

	// 10 probes, 70% success -> TrustLevelMedium
	for i := 0; i < 10; i++ {
		tm.RecordProbe("peer-mb", i < 7, 100)
	}

	level := tm.GetTrustLevel("peer-mb")
	if level != TrustLevelMedium {
		t.Errorf("expected TrustLevelMedium, got %s", level.String())
	}
}

// ============================================================
// TrustManager - GetReliability edge cases
// ============================================================

func TestTrustManager_GetReliability_UnknownPeer(t *testing.T) {
	tm := NewTrustManager(3)

	rel := tm.GetReliability("unknown")
	if rel.PeerID != "unknown" {
		t.Errorf("PeerID = %q, want unknown", rel.PeerID)
	}
	if rel.ReputationScore != 50.0 {
		t.Errorf("ReputationScore = %f, want 50.0 default", rel.ReputationScore)
	}
	if rel.TotalProbes != 0 {
		t.Errorf("TotalProbes = %d, want 0", rel.TotalProbes)
	}
}

func TestTrustManager_GetReliability_ZeroProbes(t *testing.T) {
	tm := NewTrustManager(3)

	// Record and then... actually the map entry doesn't exist for 0 probes.
	// Unknown peer with zero probes.
	rel := tm.GetReliability("no-such-peer")
	if rel.SuccessRate != 0 {
		t.Errorf("SuccessRate should be 0 for unknown peer")
	}
}

// ============================================================
// NewTrustManager tests
// ============================================================

func TestNewTrustManager_DefaultMinProbes(t *testing.T) {
	tm := NewTrustManager(0)
	if tm.minProbes != 3 {
		t.Errorf("minProbes should default to 3 when given 0, got %d", tm.minProbes)
	}
}

func TestNewTrustManager_NegativeMinProbes(t *testing.T) {
	tm := NewTrustManager(-5)
	if tm.minProbes != 3 {
		t.Errorf("minProbes should default to 3 when given negative, got %d", tm.minProbes)
	}
}

func TestNewTrustManager_CustomMinProbes(t *testing.T) {
	tm := NewTrustManager(10)
	if tm.minProbes != 10 {
		t.Errorf("minProbes should be 10, got %d", tm.minProbes)
	}
}

// ============================================================
// TrustManager - concurrent access
// ============================================================

func TestTrustManager_ConcurrentProbes(t *testing.T) {
	tm := NewTrustManager(3)

	done := make(chan bool)
	for i := 0; i < 50; i++ {
		go func(id int) {
			tm.RecordProbe("concurrent-peer", id%2 == 0, int64(100-id))
			tm.GetReliability("concurrent-peer")
			tm.GetTrustLevel("concurrent-peer")
			done <- true
		}(i)
	}
	for i := 0; i < 50; i++ {
		<-done
	}

	rel := tm.GetReliability("concurrent-peer")
	if rel.TotalProbes != 50 {
		t.Errorf("TotalProbes = %d, want 50", rel.TotalProbes)
	}
}
