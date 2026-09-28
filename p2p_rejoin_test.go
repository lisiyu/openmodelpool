package main

import (
	"encoding/json"
	"testing"
)

// newRejoinTestFed builds an enabled FederationManager without starting any
// background loops (the enabled flag is set directly, bypassing SetEnabled).
func newRejoinTestFed(t *testing.T) *FederationManager {
	t.Helper()
	f := &FederationManager{
		localPeers:     make(map[string]*NodeInfo),
		discoveryHints: make(map[string][]string),
		dhtHints:       make(map[string]string),
		dataDir:        t.TempDir(),
		stopCh:         make(chan struct{}),
	}
	f.mu.Lock()
	f.enabled = true
	f.mu.Unlock()
	return f
}

func TestAddKnownNodes_BatchSingleBump(t *testing.T) {
	fed := newRejoinTestFed(t)

	nodes := []NodeInfo{
		{NodeID: "mmx-peer-a", Endpoint: "http://10.0.0.2:8080", Addresses: []string{"http://10.0.0.2:8080"}},
		{NodeID: "mmx-peer-b", Endpoint: "http://10.0.0.3:8080", Addresses: []string{"http://10.0.0.3:8080"}},
		{NodeID: "mmx-peer-c", Endpoint: "http://10.0.0.4:8080", Addresses: []string{"http://10.0.0.4:8080"}},
		{NodeID: ""}, // skipped
	}
	before := fed.GetTrustPool().Version
	fed.AddKnownNodes(nodes)
	after := fed.GetTrustPool().Version
	if after-before != 1 {
		t.Fatalf("batch add bumped version by %d, want exactly 1", after-before)
	}
	active := fed.GetActiveNodes()
	if len(active) != 3 {
		t.Fatalf("active nodes = %d, want 3", len(active))
	}
	for _, n := range active {
		if n.Status != "active" {
			t.Fatalf("node %s status = %q, want active", n.NodeID, n.Status)
		}
		if n.LastSeen == "" {
			t.Fatalf("node %s missing LastSeen", n.NodeID)
		}
	}
}

func TestAddKnownNodes_UpdatePreservesRichFields(t *testing.T) {
	fed := newRejoinTestFed(t)
	fed.AddKnownNode(NodeInfo{
		NodeID:       "mmx-peer-a",
		PubKey:       "pubkey-aaa",
		SharedModels: []string{"model-x"},
		Addresses:    []string{"http://10.0.0.2:8080"},
	})
	// Re-add with sparse info: rich fields must survive.
	fed.AddKnownNodes([]NodeInfo{{NodeID: "mmx-peer-a", Addresses: []string{"http://10.0.0.9:8080"}}})
	n, ok := fed.GetNode("mmx-peer-a")
	if !ok {
		t.Fatal("node missing after re-add")
	}
	if n.PubKey != "pubkey-aaa" {
		t.Fatalf("PubKey = %q, want preserved pubkey-aaa", n.PubKey)
	}
	if len(n.SharedModels) != 1 || n.SharedModels[0] != "model-x" {
		t.Fatalf("SharedModels = %v, want preserved", n.SharedModels)
	}
	// New address wins.
	if len(n.Addresses) != 1 || n.Addresses[0] != "http://10.0.0.9:8080" {
		t.Fatalf("Addresses = %v, want refreshed", n.Addresses)
	}
}

func TestAddKnownNodes_DisabledNoop(t *testing.T) {
	f := &FederationManager{
		localPeers:     make(map[string]*NodeInfo),
		discoveryHints: make(map[string][]string),
		dhtHints:       make(map[string]string),
		dataDir:        t.TempDir(),
		stopCh:         make(chan struct{}),
	}
	// enabled stays false: must be a no-op and must not panic.
	f.AddKnownNodes([]NodeInfo{{NodeID: "mmx-peer-a", Addresses: []string{"http://10.0.0.2:8080"}}})
	if len(f.GetActiveNodes()) != 0 {
		t.Fatal("disabled fed must not accept nodes")
	}
}

func TestBridgeRestoredPeersToFederation(t *testing.T) {
	// Save/restore globals.
	oldFed, oldNode, oldRestored := fed, node, restoredRouteEntries
	t.Cleanup(func() {
		fed, node, restoredRouteEntries = oldFed, oldNode, oldRestored
	})

	fed = newRejoinTestFed(t)
	node = &NodeIdentity{}
	node.mu.Lock()
	node.nodeID = "mmx-self"
	node.mu.Unlock()

	restoredRouteEntries = []*RouteEntry{
		{NodeID: "mmx-peer-a", Addresses: []string{"http://10.0.0.2:8080"}},
		{NodeID: "mmx-peer-b", Addresses: []string{"http://10.0.0.3:8080"}},
		{NodeID: "mmx-self", Addresses: []string{"http://10.0.0.1:8080"}}, // excluded
		{NodeID: "mmx-noaddr"}, // excluded: no addresses
		nil,                    // excluded
	}

	nm := &NetworkManager{}
	nm.bridgeRestoredPeersToFederation()

	active := fed.GetActiveNodes()
	if len(active) != 2 {
		t.Fatalf("bridged active nodes = %d, want 2", len(active))
	}
	for _, n := range active {
		if n.NodeID == "mmx-self" {
			t.Fatal("self must not be bridged")
		}
	}
	if _, ok := fed.GetNode("mmx-peer-a"); !ok {
		t.Fatal("mmx-peer-a missing from trust pool")
	}
	if _, ok := fed.GetNode("mmx-peer-b"); !ok {
		t.Fatal("mmx-peer-b missing from trust pool")
	}
}

func TestBridgeRestoredPeersToFederation_DisabledNoop(t *testing.T) {
	oldFed, oldRestored := fed, restoredRouteEntries
	t.Cleanup(func() { fed, restoredRouteEntries = oldFed, oldRestored })

	// fed disabled (or nil): must not panic, must not bridge.
	fed = &FederationManager{
		localPeers:     make(map[string]*NodeInfo),
		discoveryHints: make(map[string][]string),
		dhtHints:       make(map[string]string),
		dataDir:        t.TempDir(),
		stopCh:         make(chan struct{}),
	}
	restoredRouteEntries = []*RouteEntry{{NodeID: "mmx-peer-a", Addresses: []string{"http://10.0.0.2:8080"}}}
	(&NetworkManager{}).bridgeRestoredPeersToFederation()
	if len(fed.GetActiveNodes()) != 0 {
		t.Fatal("disabled fed must not bridge restored peers")
	}
}

func TestDHTHints_MergeAndBootstrapAddrs(t *testing.T) {
	fed := newRejoinTestFed(t)

	fed.MergePeerHints([]PeerHint{
		{NodeID: "mmx-peer-a", Addresses: []string{"http://10.0.0.2:8080"}, DHTAddr: "10.0.0.2:19001"},
		{NodeID: "mmx-peer-b", Addresses: []string{"http://10.0.0.3:8080"}}, // no DHT addr
	})
	fed.NoteDHTHint("mmx-peer-c", "10.0.0.4:19001")
	fed.NoteDHTHint("", "10.0.0.5:19001")          // ignored
	fed.NoteDHTHint("mmx-peer-d", "")              // ignored
	fed.NoteDHTHint("mmx-peer-a", "9.9.9.9:19001") // first-known wins

	if got := fed.DHTHint("mmx-peer-a"); got != "10.0.0.2:19001" {
		t.Fatalf("DHTHint(peer-a) = %q, want 10.0.0.2:19001", got)
	}
	if got := fed.DHTHint("mmx-peer-b"); got != "" {
		t.Fatalf("DHTHint(peer-b) = %q, want empty", got)
	}

	addrs := fed.DHTBootstrapAddrs("mmx-self")
	if len(addrs) != 2 {
		t.Fatalf("bootstrap addrs = %v, want 2", addrs)
	}
	// Self exclusion.
	addrs = fed.DHTBootstrapAddrs("mmx-peer-a")
	for _, a := range addrs {
		if a == "10.0.0.2:19001" {
			t.Fatal("self DHT addr must be excluded")
		}
	}
}

func TestGossipMessage_DHTAddrJSONRoundTrip(t *testing.T) {
	msg := GossipMessage{
		Type:      "sync",
		FromNode:  "mmx-peer-a",
		Timestamp: "2026-09-28T00:00:00Z",
		DHTAddr:   "10.0.0.2:19001",
		KnownPeers: []PeerHint{
			{NodeID: "mmx-peer-b", Addresses: []string{"http://10.0.0.3:8080"}, DHTAddr: "10.0.0.3:19001"},
		},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var back GossipMessage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.DHTAddr != "10.0.0.2:19001" {
		t.Fatalf("DHTAddr = %q after round-trip", back.DHTAddr)
	}
	if len(back.KnownPeers) != 1 || back.KnownPeers[0].DHTAddr != "10.0.0.3:19001" {
		t.Fatalf("KnownPeers DHTAddr lost: %+v", back.KnownPeers)
	}

	// Backward compat: a pre-upgrade message without dht_addr must decode cleanly.
	var old GossipMessage
	if err := json.Unmarshal([]byte(`{"type":"sync","from_node":"mmx-x","timestamp":"2026-09-28T00:00:00Z"}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.DHTAddr != "" {
		t.Fatalf("old message DHTAddr = %q, want empty", old.DHTAddr)
	}
}
