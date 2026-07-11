package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers specific to federation tests
// ---------------------------------------------------------------------------

// initTestNode creates a fresh NodeIdentity with generated keys.
func initTestNode(t *testing.T, dir string) *NodeIdentity {
	t.Helper()
	initNode(dir)
	// If no existing identity, generate one for testing
	if node == nil || !node.IsInitialized() {
		node = &NodeIdentity{keyPath: dir + "/node.key"}
		if err := node.generate(); err != nil {
			t.Fatalf("failed to generate test node identity: %v", err)
		}
		node.save()
	}
	if node == nil || !node.IsInitialized() {
		t.Fatal("node identity failed to initialize")
	}
	return node
}

// initTestFederation creates a FederationManager without starting background loops.
func initTestFederation(t *testing.T, dir string) *FederationManager {
	t.Helper()
	f := &FederationManager{
		localPeers: make(map[string]*NodeInfo),
		dataDir:    dir,
		stopCh:     make(chan struct{}),
		enabled:    true,
	}
	f.trustPool = TrustPool{}
	fed = f
	return f
}

// initTestReputation creates a ReputationManager with isolated storage.
func initTestReputation(t *testing.T, dir string) *ReputationManager {
	t.Helper()
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	return repMgr
}

// initTestAllocation creates an AllocationManager with isolated storage (v2.0).
func initTestAllocation(t *testing.T, dir string) *AllocationManager {
	t.Helper()
	allocMgr = &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}
	return allocMgr
}

// initTestMessages creates a MessageManager with isolated storage.
func initTestMessages(t *testing.T, dir string) *MessageManager {
	t.Helper()
	msgMgr = &MessageManager{
		inbox:   make([]FederationMessage, 0),
		outbox:  make([]FederationMessage, 0),
		dataDir: dir,
	}
	return msgMgr
}

// makeTestNodeInfo is a convenience builder for NodeInfo used in trust pool tests.
func makeTestNodeInfo(id, status string, models []string) NodeInfo {
	return NodeInfo{
		NodeID:       id,
		Endpoint:     "http://" + id + ":8000",
		PubKey:       base64.StdEncoding.EncodeToString([]byte("fakepubkey-" + id + "-padding-to-32b!!")),
		SharedModels: models,
		Status:       status,
		JoinedAt:     time.Now().UTC().Format(time.RFC3339),
		LastSeen:     time.Now().UTC().Format(time.RFC3339),
		Version:      "3.2.0",
	}
}

// ===================================================================
// 1. TestNode_Identity
// ===================================================================

func TestNode_Identity(t *testing.T) {
	env := setupTestEnv(t)
	dir := env.dir

	// --- NodeID format: "mm-" prefix + base58 ---
	t.Run("NodeID_format", func(t *testing.T) {
		initTestNode(t, dir)
		nid := node.NodeID()
		if !strings.HasPrefix(nid, "mm-") {
			t.Fatalf("NodeID should start with 'mm-', got %q", nid)
		}
		payload := nid[3:]
		if len(payload) == 0 {
			t.Fatal("NodeID base58 part should not be empty")
		}
		for _, c := range payload {
			if !strings.ContainsRune(base58Alphabet, c) {
				t.Fatalf("NodeID contains non-base58 char %q", c)
			}
		}
	})

	// --- Sign and Verify ---
	t.Run("Sign_Verify", func(t *testing.T) {
		initTestNode(t, dir)
		msg := []byte("hello federation")
		sig := node.Sign(msg)
		if sig == "" {
			t.Fatal("Sign returned empty string")
		}
		pubB64 := node.PubKeyB64()
		if !VerifySignature(pubB64, msg, sig) {
			t.Fatal("VerifySignature should succeed for valid signature")
		}
		// Tamper with message
		tampered := []byte("hello federation TAMPERED")
		if VerifySignature(pubB64, tampered, sig) {
			t.Fatal("VerifySignature should fail for tampered message")
		}
	})

	// --- SignJSON / VerifyJSONSig ---
	t.Run("SignJSON_VerifyJSONSig", func(t *testing.T) {
		initTestNode(t, dir)
		type sample struct {
			Foo string `json:"foo"`
			Bar int    `json:"bar"`
		}
		v := sample{Foo: "hello", Bar: 42}
		sig := node.SignJSON(v)
		if sig == "" {
			t.Fatal("SignJSON returned empty string")
		}
		pubB64 := node.PubKeyB64()
		if !VerifyJSONSig(pubB64, v, sig) {
			t.Fatal("VerifyJSONSig should succeed")
		}
		// Different struct should fail
		v2 := sample{Foo: "different", Bar: 99}
		if VerifyJSONSig(pubB64, v2, sig) {
			t.Fatal("VerifyJSONSig should fail for different struct")
		}
	})

	// --- Key persistence: save then load, NodeID unchanged ---
	t.Run("Key_persistence", func(t *testing.T) {
		initTestNode(t, dir)
		origID := node.NodeID()
		node.save()

		// Re-initialize from disk
		initNode(dir)
		if node.NodeID() != origID {
			t.Fatalf("NodeID changed after reload: %q -> %q", origID, node.NodeID())
		}
	})

	// --- PubKeyB64 non-empty ---
	t.Run("PubKeyB64_nonempty", func(t *testing.T) {
		initTestNode(t, dir)
		pk := node.PubKeyB64()
		if pk == "" {
			t.Fatal("PubKeyB64 should not be empty")
		}
		// Should be valid base64
		decoded, err := base64.StdEncoding.DecodeString(pk)
		if err != nil {
			t.Fatalf("PubKeyB64 is not valid base64: %v", err)
		}
		if len(decoded) != 32 {
			t.Fatalf("ed25519 public key should be 32 bytes, got %d", len(decoded))
		}
	})

	// --- GetInfo returns correct NodeInfo ---
	t.Run("GetInfo", func(t *testing.T) {
		initTestNode(t, dir)
		initTestFederation(t, dir)
		info := node.GetInfo()
		if info.NodeID != node.NodeID() {
			t.Fatalf("GetInfo NodeID mismatch: %q vs %q", info.NodeID, node.NodeID())
		}
		if info.Status != "active" {
			t.Fatalf("GetInfo Status should be 'active', got %q", info.Status)
		}
		if info.Version != "3.2.0" {
			t.Fatalf("GetInfo Version should be '3.1.0', got %q", info.Version)
		}
		if info.PubKey == "" {
			t.Fatal("GetInfo PubKey should not be empty")
		}
		if info.JoinedAt == "" {
			t.Fatal("GetInfo JoinedAt should not be empty")
		}
	})
}

// ===================================================================
// 2. TestFederation_TrustPool
// ===================================================================

func TestFederation_TrustPool(t *testing.T) {
	env := setupTestEnv(t)
	dir := env.dir
	initTestNode(t, dir)

	// --- Init federation manager ---
	t.Run("init", func(t *testing.T) {
		f := initTestFederation(t, dir)
		if !f.IsEnabled() {
			t.Fatal("federation should be enabled")
		}
		pool := f.GetTrustPool()
		if len(pool.Nodes) != 0 {
			t.Fatalf("fresh pool should have 0 nodes, got %d", len(pool.Nodes))
		}
	})

	// --- UpdateTrustPool: add nodes ---
	t.Run("UpdateTrustPool", func(t *testing.T) {
		f := initTestFederation(t, dir)
		pool := TrustPool{
			Version:   1,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Nodes: []NodeInfo{
				makeTestNodeInfo("mm-aaa", "active", []string{"gpt-4"}),
				makeTestNodeInfo("mm-bbb", "active", []string{"claude-3"}),
			},
		}
		f.UpdateTrustPool(pool)

		got := f.GetTrustPool()
		if got.Version != 1 {
			t.Fatalf("pool version should be 1, got %d", got.Version)
		}
		if len(got.Nodes) != 2 {
			t.Fatalf("pool should have 2 nodes, got %d", len(got.Nodes))
		}

		// Older version should be rejected
		oldPool := TrustPool{Version: 0, Nodes: []NodeInfo{makeTestNodeInfo("mm-old", "active", nil)}}
		f.UpdateTrustPool(oldPool)
		got2 := f.GetTrustPool()
		if len(got2.Nodes) != 2 {
			t.Fatalf("older pool version should be rejected, still expect 2 nodes, got %d", len(got2.Nodes))
		}
	})

	// --- GetNode ---
	t.Run("GetNode", func(t *testing.T) {
		f := initTestFederation(t, dir)
		f.UpdateTrustPool(TrustPool{
			Version: 1,
			Nodes: []NodeInfo{
				makeTestNodeInfo("mm-aaa", "active", []string{"gpt-4"}),
			},
		})

		n, ok := f.GetNode("mm-aaa")
		if !ok || n == nil {
			t.Fatal("GetNode should find mm-aaa")
		}
		if n.NodeID != "mm-aaa" {
			t.Fatalf("GetNode returned wrong node: %q", n.NodeID)
		}

		_, ok = f.GetNode("mm-nonexistent")
		if ok {
			t.Fatal("GetNode should return false for nonexistent node")
		}
	})

	// --- UpdateNodeInfo ---
	t.Run("UpdateNodeInfo", func(t *testing.T) {
		f := initTestFederation(t, dir)
		f.UpdateTrustPool(TrustPool{
			Version: 1,
			Nodes: []NodeInfo{
				makeTestNodeInfo("mm-aaa", "active", []string{"gpt-4"}),
			},
		})

		// Update existing node in trust pool
		updated := makeTestNodeInfo("mm-aaa", "active", []string{"gpt-4", "gpt-3.5"})
		updated.Status = "active"
		f.UpdateNodeInfo(updated)
		n, _ := f.GetNode("mm-aaa")
		if len(n.SharedModels) != 2 {
			t.Fatalf("UpdateNodeInfo should update models, got %v", n.SharedModels)
		}

		// Insert new node via gossip (goes to localPeers)
		newNode := makeTestNodeInfo("mm-ccc", "active", []string{"llama-3"})
		f.UpdateNodeInfo(newNode)
		n2, ok := f.GetNode("mm-ccc")
		if !ok {
			t.Fatal("UpdateNodeInfo should add new node to localPeers")
		}
		if n2.NodeID != "mm-ccc" {
			t.Fatalf("expected mm-ccc, got %q", n2.NodeID)
		}
	})

	// --- RemoveNode ---
	t.Run("RemoveNode", func(t *testing.T) {
		f := initTestFederation(t, dir)
		f.UpdateTrustPool(TrustPool{
			Version: 1,
			Nodes: []NodeInfo{
				makeTestNodeInfo("mm-aaa", "active", nil),
				makeTestNodeInfo("mm-bbb", "active", nil),
			},
		})
		// Also add to localPeers
		f.mu.Lock()
		n := makeTestNodeInfo("mm-ccc", "active", nil)
		f.localPeers["mm-ccc"] = &n
		f.mu.Unlock()

		f.RemoveNode("mm-aaa")
		_, ok := f.GetNode("mm-aaa")
		if ok {
			t.Fatal("mm-aaa should be removed from trust pool")
		}

		f.RemoveNode("mm-ccc")
		_, ok = f.GetNode("mm-ccc")
		if ok {
			t.Fatal("mm-ccc should be removed from localPeers")
		}

		// mm-bbb should still exist
		_, ok = f.GetNode("mm-bbb")
		if !ok {
			t.Fatal("mm-bbb should still exist after removing others")
		}
	})

	// --- GetActiveNodes ---
	t.Run("GetActiveNodes", func(t *testing.T) {
		f := initTestFederation(t, dir)
		f.UpdateTrustPool(TrustPool{
			Version: 1,
			Nodes: []NodeInfo{
				makeTestNodeInfo("mm-active1", "active", nil),
				makeTestNodeInfo("mm-inactive", "inactive", nil),
				makeTestNodeInfo("mm-suspended", "suspended", nil),
				makeTestNodeInfo("mm-active2", "active", nil),
			},
		})
		active := f.GetActiveNodes()
		if len(active) != 2 {
			t.Fatalf("expected 2 active nodes, got %d", len(active))
		}
		for _, n := range active {
			if n.Status != "active" {
				t.Fatalf("GetActiveNodes returned non-active node: %q status=%q", n.NodeID, n.Status)
			}
		}
	})

	// --- FindProvidersForModel ---
	t.Run("FindProvidersForModel", func(t *testing.T) {
		f := initTestFederation(t, dir)
		f.UpdateTrustPool(TrustPool{
			Version: 1,
			Nodes: []NodeInfo{
				makeTestNodeInfo("mm-n1", "active", []string{"gpt-4", "gpt-3.5"}),
				makeTestNodeInfo("mm-n2", "active", []string{"claude-3"}),
				makeTestNodeInfo("mm-n3", "inactive", []string{"gpt-4"}), // inactive, should not match
			},
		})

		gpt4Providers := f.FindProvidersForModel("gpt-4")
		if len(gpt4Providers) != 1 {
			t.Fatalf("expected 1 active provider for gpt-4, got %d", len(gpt4Providers))
		}
		if gpt4Providers[0].NodeID != "mm-n1" {
			t.Fatalf("expected mm-n1, got %q", gpt4Providers[0].NodeID)
		}

		claudeProviders := f.FindProvidersForModel("claude-3")
		if len(claudeProviders) != 1 {
			t.Fatalf("expected 1 provider for claude-3, got %d", len(claudeProviders))
		}

		unknownProviders := f.FindProvidersForModel("unknown-model")
		if len(unknownProviders) != 0 {
			t.Fatalf("expected 0 providers for unknown model, got %d", len(unknownProviders))
		}
	})

	// --- save / load persistence ---
	t.Run("save_load", func(t *testing.T) {
		f := initTestFederation(t, dir)
		f.UpdateTrustPool(TrustPool{
			Version:   5,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Nodes: []NodeInfo{
				makeTestNodeInfo("mm-persist1", "active", []string{"gpt-4"}),
				makeTestNodeInfo("mm-persist2", "active", []string{"claude-3"}),
			},
		})

		// Create a new federation manager loading from same dir
		f2 := &FederationManager{
			localPeers: make(map[string]*NodeInfo),
			dataDir:    dir,
			stopCh:     make(chan struct{}),
			enabled:    true,
		}
		if err := f2.load(); err != nil {
			t.Fatalf("load failed: %v", err)
		}
		if f2.trustPool.Version != 5 {
			t.Fatalf("loaded pool version should be 5, got %d", f2.trustPool.Version)
		}
		if len(f2.trustPool.Nodes) != 2 {
			t.Fatalf("loaded pool should have 2 nodes, got %d", len(f2.trustPool.Nodes))
		}
	})
}

// ===================================================================
// 3. TestReputation_Scoring
// ===================================================================

func TestReputation_Scoring(t *testing.T) {
	env := setupTestEnv(t)
	dir := env.dir
	initTestNode(t, dir)

	// --- RecordCall ---
	t.Run("RecordCall", func(t *testing.T) {
		r := initTestReputation(t, dir)
		r.RecordCall("mm-node1", true, 100)

		rep := r.GetReputation("mm-node1")
		if rep == nil {
			t.Fatal("reputation should exist after RecordCall")
		}
		if rep.TotalRequests != 1 {
			t.Fatalf("TotalRequests should be 1, got %d", rep.TotalRequests)
		}
		if rep.FailedRequests != 0 {
			t.Fatalf("FailedRequests should be 0, got %d", rep.FailedRequests)
		}

		// Record a failure
		r.RecordCall("mm-node1", false, 500)
		rep2 := r.GetReputation("mm-node1")
		if rep2.TotalRequests != 2 {
			t.Fatalf("TotalRequests should be 2, got %d", rep2.TotalRequests)
		}
		if rep2.FailedRequests != 1 {
			t.Fatalf("FailedRequests should be 1, got %d", rep2.FailedRequests)
		}
	})

	// --- EWMA calculation (α=0.3) ---
	t.Run("EWMA_calculation", func(t *testing.T) {
		r := initTestReputation(t, dir)

		// Initial availability is 50.0 (getOrCreate default)
		// After one success (sample=100): 0.3*100 + 0.7*50 = 30+35 = 65
		r.RecordCall("mm-ewma", true, 0)
		rep := r.GetReputation("mm-ewma")
		expectedAvail := 0.3*100.0 + 0.7*50.0 // = 65.0
		if math.Abs(rep.Availability-expectedAvail) > 0.01 {
			t.Fatalf("EWMA availability: expected %.2f, got %.2f", expectedAvail, rep.Availability)
		}

		// After another success: 0.3*100 + 0.7*65 = 30+45.5 = 75.5
		r.RecordCall("mm-ewma", true, 0)
		rep2 := r.GetReputation("mm-ewma")
		expectedAvail2 := 0.3*100.0 + 0.7*65.0 // = 75.5
		if math.Abs(rep2.Availability-expectedAvail2) > 0.01 {
			t.Fatalf("EWMA availability step2: expected %.2f, got %.2f", expectedAvail2, rep2.Availability)
		}

		// After a failure (sample=0): 0.3*0 + 0.7*75.5 = 52.85
		r.RecordCall("mm-ewma", false, 0)
		rep3 := r.GetReputation("mm-ewma")
		expectedAvail3 := 0.3*0.0 + 0.7*75.5 // = 52.85
		if math.Abs(rep3.Availability-expectedAvail3) > 0.01 {
			t.Fatalf("EWMA availability step3: expected %.2f, got %.2f", expectedAvail3, rep3.Availability)
		}
	})

	// --- CalculateGrade thresholds ---
	t.Run("CalculateGrade", func(t *testing.T) {
		r := initTestReputation(t, dir)

		tests := []struct {
			score     float64
			expectGrade string
		}{
			{200, "S"},
			{250, "S"},
			{100, "S"},
			{150, "S"},
			{95, "S"},
			{80, "A"},
			{94, "A"},
			{60, "B"},
			{75, "B"},
			{40, "C"},
			{50, "C"},
			{20, "D"},
			{35, "D"},
			{0, "D"},
			{39.99, "D"},
		}

		for _, tc := range tests {
			rep := &NodeReputation{OverallScore: tc.score}
			grade := r.CalculateGrade(rep)
			if grade != tc.expectGrade {
				t.Errorf("score=%.2f: expected grade %q, got %q", tc.score, tc.expectGrade, grade)
			}
		}
	})

	// --- AddPeerScore / GetOurScore / SetOurScore ---
	t.Run("PeerScores", func(t *testing.T) {
		r := initTestReputation(t, dir)

		// AddPeerScore
		score := PeerScore{
			FromNode:     "mm-remote",
			TargetNode:   "mm-target",
			Availability: 80,
			Latency:      70,
			Accuracy:     90,
			Comment:      "good node",
			Timestamp:    time.Now().UTC().Format(time.RFC3339),
		}
		r.AddPeerScore(score)
		rep := r.GetReputation("mm-target")
		if rep == nil {
			t.Fatal("reputation should exist after AddPeerScore")
		}
		if len(rep.PeerScores) != 1 {
			t.Fatalf("expected 1 peer score, got %d", len(rep.PeerScores))
		}
		if rep.PeerScores[0].Availability != 80 {
			t.Fatalf("peer score availability should be 80, got %.0f", rep.PeerScores[0].Availability)
		}

		// Update existing peer score (same FromNode)
		score.Availability = 95
		r.AddPeerScore(score)
		rep2 := r.GetReputation("mm-target")
		if len(rep2.PeerScores) != 1 {
			t.Fatalf("updating peer score should not add new entry, got %d", len(rep2.PeerScores))
		}
		if rep2.PeerScores[0].Availability != 95 {
			t.Fatalf("peer score availability should be updated to 95, got %.0f", rep2.PeerScores[0].Availability)
		}

		// SetOurScore / GetOurScore
		r.SetOurScore("mm-target", 85, 75, 90, "my assessment")
		ourScore := r.GetOurScore("mm-target")
		if ourScore == nil {
			t.Fatal("GetOurScore should return non-nil after SetOurScore")
		}
		if ourScore.Availability != 85 {
			t.Fatalf("our availability should be 85, got %.0f", ourScore.Availability)
		}
		if ourScore.TargetNode != "mm-target" {
			t.Fatalf("target node should be mm-target, got %q", ourScore.TargetNode)
		}
		if ourScore.FromNode != node.NodeID() {
			t.Fatalf("from node should be our node ID, got %q", ourScore.FromNode)
		}
		if ourScore.Signature == "" {
			t.Fatal("our score should be signed")
		}

		// GetOurScore for unknown target
		unknown := r.GetOurScore("mm-unknown")
		if unknown != nil {
			t.Fatal("GetOurScore should return nil for unknown target")
		}
	})

	// --- ShouldRemoveNode ---
	t.Run("ShouldRemoveNode", func(t *testing.T) {
		r := initTestReputation(t, dir)

		// Non-existent node
		if r.ShouldRemoveNode("mm-nope") {
			t.Fatal("ShouldRemoveNode should return false for unknown node")
		}

		// Node with grade C
		rep := &NodeReputation{
			NodeID:      "mm-gradeC",
			Grade:       "C",
			DGradeSince: time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339),
		}
		r.mu.Lock()
		r.scores["mm-gradeC"] = rep
		r.mu.Unlock()
		if r.ShouldRemoveNode("mm-gradeC") {
			t.Fatal("ShouldRemoveNode should return false for non-D grade")
		}

		// D grade for less than 7 days
		repD1 := &NodeReputation{
			NodeID:      "mm-dgrade-new",
			Grade:       "D",
			DGradeSince: time.Now().Add(-3 * 24 * time.Hour).UTC().Format(time.RFC3339),
		}
		r.mu.Lock()
		r.scores["mm-dgrade-new"] = repD1
		r.mu.Unlock()
		if r.ShouldRemoveNode("mm-dgrade-new") {
			t.Fatal("ShouldRemoveNode should return false when D grade < 7 days")
		}

		// D grade for more than 7 days
		repD2 := &NodeReputation{
			NodeID:      "mm-dgrade-old",
			Grade:       "D",
			DGradeSince: time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339),
		}
		r.mu.Lock()
		r.scores["mm-dgrade-old"] = repD2
		r.mu.Unlock()
		if !r.ShouldRemoveNode("mm-dgrade-old") {
			t.Fatal("ShouldRemoveNode should return true when D grade > 7 days")
		}
	})

	// --- CalculateOverallScore ---
	t.Run("CalculateOverallScore", func(t *testing.T) {
		r := initTestReputation(t, dir)

		// No peer scores: base = 0.4*avail + 0.3*latency + 0.2*accuracy + 0.1*0
		rep := &NodeReputation{
			Availability: 80,
			Latency:      60,
			Accuracy:     70,
			PeerScores:   []PeerScore{},
		}
		score := r.CalculateOverallScore(rep)
		expected := 0.4*80 + 0.3*60 + 0.2*70 // = 32 + 18 + 14 = 64
		if math.Abs(score-expected) > 0.01 {
			t.Fatalf("overall score: expected %.2f, got %.2f", expected, score)
		}

		// With peer scores: peer consensus = avg(peer availability)
		rep2 := &NodeReputation{
			Availability: 80,
			Latency:      60,
			Accuracy:     70,
			PeerScores: []PeerScore{
				{FromNode: "mm-a", Availability: 90},
				{FromNode: "mm-b", Availability: 70},
			},
		}
		score2 := r.CalculateOverallScore(rep2)
		peerConsensus := (90.0 + 70.0) / 2.0 // = 80
		expected2 := 0.4*80 + 0.3*60 + 0.2*70 + 0.1*peerConsensus // = 64 + 8 = 72
		if math.Abs(score2-expected2) > 0.01 {
			t.Fatalf("overall score with peers: expected %.2f, got %.2f", expected2, score2)
		}
	})

	// --- Persistence ---
	t.Run("save_load", func(t *testing.T) {
		r := initTestReputation(t, dir)
		r.RecordCall("mm-persist", true, 200)
		r.SetOurScore("mm-persist", 80, 70, 90, "test")

		// Create new manager and load
		r2 := &ReputationManager{
			scores:   make(map[string]*NodeReputation),
			myScores: make(map[string]*PeerScore),
			dataDir:  dir,
		}
		r2.load()
		if len(r2.scores) != 1 {
			t.Fatalf("loaded scores should have 1 entry, got %d", len(r2.scores))
		}
		rep := r2.scores["mm-persist"]
		if rep == nil {
			t.Fatal("loaded reputation for mm-persist should not be nil")
		}
		if rep.TotalRequests != 1 {
			t.Fatalf("loaded TotalRequests should be 1, got %d", rep.TotalRequests)
		}
		if len(r2.myScores) != 1 {
			t.Fatalf("loaded myScores should have 1 entry, got %d", len(r2.myScores))
		}
	})
}

// ===================================================================
// 4. TestCredits_Balance
// ===================================================================

// TestAllocation_Balance tests the v2.0 quota allocation system.
func TestAllocation_Balance(t *testing.T) {
	env := setupTestEnv(t)
	dir := env.dir

	// --- Default allocation ---
	t.Run("DefaultAllocation", func(t *testing.T) {
		am := initTestAllocation(t, dir)
		alloc := am.GetAllocation()
		if alloc.GuestKeyPercent != 50 {
			t.Fatalf("default guest_key_percent should be 50, got %d", alloc.GuestKeyPercent)
		}
		if alloc.PublicKeyPercent != 50 {
			t.Fatalf("default public_key_percent should be 50, got %d", alloc.PublicKeyPercent)
		}
	})

	// --- SetAllocation ---
	t.Run("SetAllocation", func(t *testing.T) {
		am := initTestAllocation(t, dir)
		err := am.SetAllocation(70)
		if err != nil {
			t.Fatalf("SetAllocation should succeed: %v", err)
		}
		alloc := am.GetAllocation()
		if alloc.GuestKeyPercent != 70 {
			t.Fatalf("guest_key_percent should be 70, got %d", alloc.GuestKeyPercent)
		}
		if alloc.PublicKeyPercent != 30 {
			t.Fatalf("public_key_percent should be 30, got %d", alloc.PublicKeyPercent)
		}
	})

	// --- SetAllocation boundary: 0 ---
	t.Run("SetAllocation_zero", func(t *testing.T) {
		am := initTestAllocation(t, dir)
		err := am.SetAllocation(0)
		if err != nil {
			t.Fatalf("SetAllocation(0) should succeed: %v", err)
		}
		alloc := am.GetAllocation()
		if alloc.GuestKeyPercent != 0 {
			t.Fatalf("guest_key_percent should be 0, got %d", alloc.GuestKeyPercent)
		}
		if alloc.PublicKeyPercent != 100 {
			t.Fatalf("public_key_percent should be 100, got %d", alloc.PublicKeyPercent)
		}
	})

	// --- SetAllocation boundary: 100 ---
	t.Run("SetAllocation_100", func(t *testing.T) {
		am := initTestAllocation(t, dir)
		err := am.SetAllocation(100)
		if err != nil {
			t.Fatalf("SetAllocation(100) should succeed: %v", err)
		}
		alloc := am.GetAllocation()
		if alloc.GuestKeyPercent != 100 {
			t.Fatalf("guest_key_percent should be 100, got %d", alloc.GuestKeyPercent)
		}
		if alloc.PublicKeyPercent != 0 {
			t.Fatalf("public_key_percent should be 0, got %d", alloc.PublicKeyPercent)
		}
	})

	// --- SetAllocation invalid ---
	t.Run("SetAllocation_invalid", func(t *testing.T) {
		am := initTestAllocation(t, dir)
		err := am.SetAllocation(-1)
		if err == nil {
			t.Fatal("SetAllocation(-1) should fail")
		}
		err = am.SetAllocation(101)
		if err == nil {
			t.Fatal("SetAllocation(101) should fail")
		}
	})

	// --- RecordUsage ---
	t.Run("RecordUsage", func(t *testing.T) {
		am := initTestAllocation(t, dir)
		am.RecordUsage(true, 1000)
		am.RecordUsage(false, 2000)
		stats := am.GetUsageStats()
		if stats["used_guest_tokens"] != int64(1000) {
			t.Fatalf("used_free_tokens should be 1000, got %v", stats["used_guest_tokens"])
		}
		if stats["used_public_tokens"] != int64(2000) {
			t.Fatalf("used_network_tokens should be 2000, got %v", stats["used_public_tokens"])
		}
	})

	// --- save / load persistence ---
	t.Run("save_load", func(t *testing.T) {
		am := initTestAllocation(t, dir)
		am.SetAllocation(80)

		// New manager loading from same dir
		am2 := &AllocationManager{
			config:  QuotaAllocation{},
			dataDir: dir,
		}
		am2.load()
		if am2.config.GuestKeyPercent != 80 {
			t.Fatalf("loaded guest_key_percent should be 80, got %d", am2.config.GuestKeyPercent)
		}
		if am2.config.PublicKeyPercent != 20 {
			t.Fatalf("loaded public_key_percent should be 20, got %d", am2.config.PublicKeyPercent)
		}
	})
}

// ===================================================================
// 5. TestMessage_SendReceive
// ===================================================================

func TestMessage_SendReceive(t *testing.T) {
	env := setupTestEnv(t)
	dir := env.dir

	// --- SendMessage: basic send to self ---
	t.Run("SendMessage_to_self", func(t *testing.T) {
		initTestNode(t, dir)
		initTestFederation(t, dir)
		initTestAllocation(t, dir)
		m := initTestMessages(t, dir)

		// Register self in federation so GetNode succeeds
		myID := node.NodeID()
		selfInfo := NodeInfo{
			NodeID:   myID,
			Endpoint: "http://localhost:19999",
			Status:   "active",
			PubKey:   node.PubKeyB64(),
		}
		fed.UpdateNodeInfo(selfInfo)

		err := m.SendMessage(myID, "Test Subject", "Test Body", "general")
		if err != nil {
			t.Fatalf("SendMessage to self failed: %v", err)
		}

		// Message should be in inbox (delivered locally)
		inbox := m.GetInbox(10)
		if len(inbox) != 1 {
			t.Fatalf("inbox should have 1 message, got %d", len(inbox))
		}
		if inbox[0].Subject != "Test Subject" {
			t.Fatalf("subject mismatch: %q", inbox[0].Subject)
		}
		if inbox[0].Body != "Test Body" {
			t.Fatalf("body mismatch: %q", inbox[0].Body)
		}
		if inbox[0].FromNode != myID {
			t.Fatalf("from_node should be ourselves, got %q", inbox[0].FromNode)
		}
		if inbox[0].Signature == "" {
			t.Fatal("message should have a signature")
		}
	})

	// --- SendMessage: validation errors ---
	t.Run("SendMessage_validation", func(t *testing.T) {
		initTestNode(t, dir)
		initTestFederation(t, dir)
		initTestAllocation(t, dir)
		m := initTestMessages(t, dir)

		// Empty subject
		err := m.SendMessage("mm-recipient", "", "body", "general")
		if err == nil {
			t.Fatal("should fail with empty subject")
		}

		// Empty body
		err = m.SendMessage("mm-recipient", "subject", "", "general")
		if err == nil {
			t.Fatal("should fail with empty body")
		}

		// Invalid message type
		err = m.SendMessage("mm-recipient", "subject", "body", "invalid_type")
		if err == nil {
			t.Fatal("should fail with invalid message type")
		}

		// Unknown recipient
		err = m.SendMessage("mm-unknown-node", "subject", "body", "general")
		if err == nil {
			t.Fatal("should fail with unknown recipient")
		}
	})

	// --- GetInbox / GetOutbox ---
	t.Run("GetInbox_GetOutbox", func(t *testing.T) {
		m := initTestMessages(t, dir)

		// Empty inbox/outbox
		inbox := m.GetInbox(10)
		if len(inbox) != 0 {
			t.Fatalf("empty inbox should return 0, got %d", len(inbox))
		}
		outbox := m.GetOutbox(10)
		if len(outbox) != 0 {
			t.Fatalf("empty outbox should return 0, got %d", len(outbox))
		}

		// Add messages directly
		now := time.Now().UTC().Format(time.RFC3339)
		m.mu.Lock()
		m.inbox = append(m.inbox, FederationMessage{
			ID: "msg-1", FromNode: "mm-a", ToNode: "mm-us",
			Subject: "Hello", Body: "World", Timestamp: now, Read: false,
		})
		m.inbox = append(m.inbox, FederationMessage{
			ID: "msg-2", FromNode: "mm-b", ToNode: "mm-us",
			Subject: "Second", Body: "Message", Timestamp: now, Read: false,
		})
		m.outbox = append(m.outbox, FederationMessage{
			ID: "msg-3", FromNode: "mm-us", ToNode: "mm-c",
			Subject: "Out", Body: "Going", Timestamp: now,
		})
		m.mu.Unlock()

		inbox = m.GetInbox(10)
		if len(inbox) != 2 {
			t.Fatalf("inbox should have 2 messages, got %d", len(inbox))
		}
		// Most recent first
		if inbox[0].ID != "msg-2" {
			t.Fatalf("most recent inbox should be msg-2, got %q", inbox[0].ID)
		}

		outbox = m.GetOutbox(10)
		if len(outbox) != 1 {
			t.Fatalf("outbox should have 1 message, got %d", len(outbox))
		}
	})

	// --- MarkAsRead ---
	t.Run("MarkAsRead", func(t *testing.T) {
		m := initTestMessages(t, dir)
		now := time.Now().UTC().Format(time.RFC3339)
		m.mu.Lock()
		m.inbox = []FederationMessage{
			{ID: "read-1", Subject: "Unread", Timestamp: now, Read: false},
			{ID: "read-2", Subject: "Also Unread", Timestamp: now, Read: false},
		}
		m.mu.Unlock()

		m.MarkAsRead("read-1")

		m.mu.RLock()
		for _, msg := range m.inbox {
			if msg.ID == "read-1" && !msg.Read {
				m.mu.RUnlock()
				t.Fatal("read-1 should be marked as read")
			}
			if msg.ID == "read-2" && msg.Read {
				m.mu.RUnlock()
				t.Fatal("read-2 should still be unread")
			}
		}
		m.mu.RUnlock()

		// MarkAsRead for non-existent ID should not panic
		m.MarkAsRead("nonexistent")
	})

	// --- GetUnreadCount ---
	t.Run("GetUnreadCount", func(t *testing.T) {
		m := initTestMessages(t, dir)
		now := time.Now().UTC().Format(time.RFC3339)
		m.mu.Lock()
		m.inbox = []FederationMessage{
			{ID: "u-1", Read: false, Timestamp: now},
			{ID: "u-2", Read: false, Timestamp: now},
			{ID: "u-3", Read: true, Timestamp: now},
			{ID: "u-4", Read: false, Timestamp: now},
		}
		m.mu.Unlock()

		if m.GetUnreadCount() != 3 {
			t.Fatalf("expected 3 unread, got %d", m.GetUnreadCount())
		}
	})

	// --- trimInbox / trimOutbox beyond 500 ---
	t.Run("trimInbox_trimOutbox", func(t *testing.T) {
		m := initTestMessages(t, dir)

		// Create 510 inbox messages
		baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		m.mu.Lock()
		m.inbox = make([]FederationMessage, 510)
		for i := 0; i < 510; i++ {
			ts := baseTime.Add(time.Duration(i) * time.Second)
			m.inbox[i] = FederationMessage{
				ID:        fmt.Sprintf("trim-%d", i),
				Subject:   fmt.Sprintf("msg-%d", i),
				Timestamp: ts.Format(time.RFC3339),
			}
		}
		m.mu.Unlock()

		m.mu.Lock()
		m.trimInbox()
		m.mu.Unlock()

		if len(m.inbox) != maxInboxSize {
			t.Fatalf("inbox should be trimmed to %d, got %d", maxInboxSize, len(m.inbox))
		}
		// Should keep the most recent 500 (trim-10 through trim-509)
		if m.inbox[0].ID != "trim-10" {
			t.Fatalf("after trim, oldest should be trim-10, got %q", m.inbox[0].ID)
		}

		// Same for outbox
		m.mu.Lock()
		m.outbox = make([]FederationMessage, 520)
		for i := 0; i < 520; i++ {
			ts := baseTime.Add(time.Duration(i) * time.Second)
			m.outbox[i] = FederationMessage{
				ID:        fmt.Sprintf("otrim-%d", i),
				Subject:   fmt.Sprintf("out-%d", i),
				Timestamp: ts.Format(time.RFC3339),
			}
		}
		m.mu.Unlock()

		m.mu.Lock()
		m.trimOutbox()
		m.mu.Unlock()

		if len(m.outbox) != maxOutboxSize {
			t.Fatalf("outbox should be trimmed to %d, got %d", maxOutboxSize, len(m.outbox))
		}
		if m.outbox[0].ID != "otrim-20" {
			t.Fatalf("after trim, oldest outbox should be otrim-20, got %q", m.outbox[0].ID)
		}
	})

	// --- Message persistence (save/load) ---
	t.Run("save_load", func(t *testing.T) {
		m := initTestMessages(t, dir)
		now := time.Now().UTC().Format(time.RFC3339)
		m.mu.Lock()
		m.inbox = append(m.inbox, FederationMessage{
			ID: "persist-1", Subject: "Persistent", Body: "Hello",
			FromNode: "mm-a", ToNode: "mm-b", Timestamp: now,
		})
		m.outbox = append(m.outbox, FederationMessage{
			ID: "persist-2", Subject: "Sent", Body: "World",
			FromNode: "mm-us", ToNode: "mm-c", Timestamp: now,
		})
		m.save()
		m.mu.Unlock()

		// Load in new manager
		m2 := &MessageManager{
			inbox:   make([]FederationMessage, 0),
			outbox:  make([]FederationMessage, 0),
			dataDir: dir,
		}
		m2.load()
		if len(m2.inbox) != 1 {
			t.Fatalf("loaded inbox should have 1, got %d", len(m2.inbox))
		}
		if len(m2.outbox) != 1 {
			t.Fatalf("loaded outbox should have 1, got %d", len(m2.outbox))
		}
		if m2.inbox[0].ID != "persist-1" {
			t.Fatalf("loaded inbox msg ID mismatch: %q", m2.inbox[0].ID)
		}
	})
}

// ===================================================================
// TestGossip_Dedup (basic gossip dedup logic)
// ===================================================================

func TestGossip_Dedup(t *testing.T) {
	env := setupTestEnv(t)
	dir := env.dir
	initTestNode(t, dir)
	initTestFederation(t, dir)

	g := &GossipManager{
		seen:   make(map[string]time.Time),
		stopCh: make(chan struct{}),
	}

	// First time should not be seen
	if g.isSeen("hash-abc") {
		t.Fatal("first occurrence should return false")
	}

	// Second time should be seen
	if !g.isSeen("hash-abc") {
		t.Fatal("duplicate should return true")
	}

	// Different hash should not be seen
	if g.isSeen("hash-def") {
		t.Fatal("different hash should return false")
	}

	// Cleanup should remove old entries
	g.seen["old-hash"] = time.Now().Add(-2 * time.Hour)
	g.cleanup()
	if _, exists := g.seen["old-hash"]; exists {
		t.Fatal("old hash should be cleaned up")
	}
	// Recent entries should survive
	if _, exists := g.seen["hash-abc"]; !exists {
		t.Fatal("recent hash should survive cleanup")
	}

	_ = dir // used for initTestFederation
}

// ===================================================================
// Ensure file-level imports are used (avoid compile errors)
// ===================================================================

var _ = json.Marshal
var _ = os.WriteFile
var _ = filepath.Join
var _ = fmt.Sprintf
