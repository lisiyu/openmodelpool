package main

import (
	"path/filepath"
	"testing"
	"time"
)

// TestAddPeer_BridgesToTrustPool verifies P0-2: a successful manual AddPeer
// upserts the peer into the federation trust pool as an active node, carries the
// embedded public key, and bumps the trust-pool version so peers discover it via
// the version-driven gossip pull.
func TestAddPeer_BridgesToTrustPool(t *testing.T) {
	setupDiscoveryTestEnv(t)

	before := fed.GetTrustPool().Version
	peer := PeerInfo{
		NodeID:    "mmx-bridge01",
		Name:      "bridge",
		Addresses: []string{"https://bridge.example.com"},
		Status:    "online",
		LastSeen:  time.Now().Format(time.RFC3339),
		TrustScore: 0.5,
		PubKey:    "cHVibGlj", // arbitrary base64
	}
	if err := netMgr.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	n, ok := fed.GetNode("mmx-bridge01")
	if !ok {
		t.Fatal("peer was not bridged into the trust pool")
	}
	if n.Status != "active" {
		t.Errorf("bridged node status = %q, want active", n.Status)
	}
	if n.PubKey != "cHVibGlj" {
		t.Errorf("bridged node pub_key = %q, want cHVibGlj", n.PubKey)
	}
	if n.Endpoint != "https://bridge.example.com" {
		t.Errorf("bridged node endpoint = %q, want https://bridge.example.com", n.Endpoint)
	}
	if fed.GetTrustPool().Version <= before {
		t.Errorf("trust pool version did not increase after bridge: before=%d after=%d", before, fed.GetTrustPool().Version)
	}
	// The bridged peer is now visible to gossip as an active node.
	foundActive := false
	for _, a := range fed.GetActiveNodes() {
		if a.NodeID == "mmx-bridge01" {
			foundActive = true
		}
	}
	if !foundActive {
		t.Errorf("bridged peer not returned by GetActiveNodes")
	}
}

// TestAddKnownNode_UpsertBumpsVersion verifies the bridge primitive directly:
// AddKnownNode upserts (no duplicate on repeat) and strictly increments version.
func TestAddKnownNode_UpsertBumpsVersion(t *testing.T) {
	setupDiscoveryTestEnv(t)

	fed.AddKnownNode(NodeInfo{NodeID: "mmx-up1", Addresses: []string{"https://up1.example.com"}, Status: "active"})
	v1 := fed.GetTrustPool().Version
	if v1 != 1 {
		t.Fatalf("version after first AddKnownNode = %d, want 1", v1)
	}
	// Repeat with same id should upsert (not duplicate) and bump again.
	fed.AddKnownNode(NodeInfo{NodeID: "mmx-up1", Addresses: []string{"https://up1.example.com"}, Status: "active"})
	if len(fed.GetTrustPool().Nodes) != 1 {
		t.Errorf("AddKnownNode should upsert (no duplicate); nodes=%d", len(fed.GetTrustPool().Nodes))
	}
	if fed.GetTrustPool().Version != 2 {
		t.Errorf("version after upsert = %d, want 2", fed.GetTrustPool().Version)
	}
}

// TestRemovePeer_BridgesOut verifies P0-2: removing a peer also removes it from
// the federation trust pool so it drops out of the gossip candidate set.
func TestRemovePeer_BridgesOut(t *testing.T) {
	setupDiscoveryTestEnv(t)

	peer := PeerInfo{
		NodeID:    "mmx-rm01",
		Addresses: []string{"https://rm.example.com"},
		Status:    "online",
	}
	if err := netMgr.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	if _, ok := fed.GetNode("mmx-rm01"); !ok {
		t.Fatal("peer was not bridged in on add")
	}
	if err := netMgr.RemovePeer("mmx-rm01"); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	if _, ok := fed.GetNode("mmx-rm01"); ok {
		t.Error("peer still in trust pool after RemovePeer (bridge-out failed)")
	}
	if len(netMgr.GetPeers()) != 0 {
		t.Errorf("peer still in config.Peers after RemovePeer")
	}
}

// TestBridgeDisabledInPersonalMode verifies the bridge is a no-op when federation
// is disabled (personal mode), so manual peers are never written to the trust pool.
func TestBridgeDisabledInPersonalMode(t *testing.T) {
	env := setupTestEnv(t)
	nm := &NetworkManager{
		dataPath: filepath.Join(env.dir, "network.json"),
		config:   NetworkConfig{NetworkEnabled: true, Mode: NetworkModeShared},
	}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })
	// Test-only: AddPeer calls routeTable.Put; in production routeTable is
	// initialized at startup by initNetworkManager, but here we must seed it
	// (mirrors network_peer_test.go) so the local add doesn't nil-panic.
	ensureRouteTable()
	t.Cleanup(func() { routeTable = nil })
	// Federation NOT initialized → fed == nil path: bridge must no-op.
	if fed != nil {
		t.Skip("fed already initialized; personal-mode guard not exercised")
	}
	peer := PeerInfo{NodeID: "mmx-personal", Addresses: []string{"https://p.example.com"}, Status: "online"}
	if err := netMgr.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer in personal mode: %v", err)
	}
	if len(netMgr.GetPeers()) != 1 {
		t.Errorf("peer should still be added locally in personal mode")
	}
}
