package main

import (
	"testing"
	"time"
)

// ============================================================
// RouteTable Tests
// ============================================================

func newTestRouteTable() *RouteTable {
	return &RouteTable{entries: make(map[string]*RouteEntry)}
}

func TestRouteTable_PutAndGet(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})

	entry := rt.Get("mmx-node1")
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.NodeID != "mmx-node1" {
		t.Errorf("NodeID = %q, want %q", entry.NodeID, "mmx-node1")
	}
	if entry.NodeName != "Node One" {
		t.Errorf("NodeName = %q, want %q", entry.NodeName, "Node One")
	}
	if len(entry.Addresses) != 1 || entry.Addresses[0] != "https://node1.example.com" {
		t.Errorf("Addresses = %v, want [https://node1.example.com]", entry.Addresses)
	}
	if entry.Status != "online" {
		t.Errorf("Status = %q, want %q", entry.Status, "online")
	}
}

func TestRouteTable_GetReturnsCopy(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})

	entry1 := rt.Get("mmx-node1")
	entry1.Addresses[0] = "modified"

	entry2 := rt.Get("mmx-node1")
	if entry2.Addresses[0] == "modified" {
		t.Error("Get should return a copy, modifications should not affect original")
	}
}

func TestRouteTable_GetNonExistent(t *testing.T) {
	rt := newTestRouteTable()
	entry := rt.Get("mmx-nonexistent")
	if entry != nil {
		t.Error("expected nil for non-existent entry")
	}
}

func TestRouteTable_GetExpired(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})

	// Manually expire the entry
	rt.mu.Lock()
	rt.entries["mmx-node1"].UpdatedAt = time.Now().Add(-routeTTL - time.Second)
	rt.mu.Unlock()

	entry := rt.Get("mmx-node1")
	if entry != nil {
		t.Error("expected nil for expired entry")
	}
}

func TestRouteTable_Remove(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	rt.Remove("mmx-node1")

	entry := rt.Get("mmx-node1")
	if entry != nil {
		t.Error("entry should be nil after removal")
	}
}

func TestRouteTable_RemoveNonExistent(t *testing.T) {
	rt := newTestRouteTable()
	rt.Remove("mmx-nonexistent") // should not panic
}

func TestRouteTable_GetAll(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	rt.Put("mmx-node2", "Node Two", []string{"https://node2.example.com"})

	entries := rt.GetAll()
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestRouteTable_GetAllExcludesExpired(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	rt.Put("mmx-node2", "Node Two", []string{"https://node2.example.com"})

	// Expire node1
	rt.mu.Lock()
	rt.entries["mmx-node1"].UpdatedAt = time.Now().Add(-routeTTL - time.Second)
	rt.mu.Unlock()

	entries := rt.GetAll()
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (expired should be excluded), got %d", len(entries))
	}
	if entries[0].NodeID != "mmx-node2" {
		t.Errorf("expected remaining entry to be node2, got %q", entries[0].NodeID)
	}
}

func TestRouteTable_PurgeExpired(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	rt.Put("mmx-node2", "Node Two", []string{"https://node2.example.com"})
	rt.Put("mmx-node3", "Node Three", []string{"https://node3.example.com"})

	// Expire node1 and node2
	rt.mu.Lock()
	rt.entries["mmx-node1"].UpdatedAt = time.Now().Add(-routeTTL - time.Second)
	rt.entries["mmx-node2"].UpdatedAt = time.Now().Add(-routeTTL - time.Second)
	rt.mu.Unlock()

	purged := rt.PurgeExpired()
	if purged != 2 {
		t.Errorf("expected 2 purged, got %d", purged)
	}
	if rt.Count() != 1 {
		t.Errorf("expected 1 remaining, got %d", rt.Count())
	}
}

func TestRouteTable_Count(t *testing.T) {
	rt := newTestRouteTable()
	if rt.Count() != 0 {
		t.Errorf("expected 0, got %d", rt.Count())
	}

	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	if rt.Count() != 1 {
		t.Errorf("expected 1, got %d", rt.Count())
	}

	rt.Put("mmx-node2", "Node Two", []string{"https://node2.example.com"})
	if rt.Count() != 2 {
		t.Errorf("expected 2, got %d", rt.Count())
	}

	// Count includes all entries, even expired ones (unlike GetAll)
	rt.mu.Lock()
	rt.entries["mmx-node1"].UpdatedAt = time.Now().Add(-routeTTL - time.Second)
	rt.mu.Unlock()
	if rt.Count() != 2 {
		t.Errorf("Count should include expired entries, got %d", rt.Count())
	}
}

func TestRouteTable_PutOverwrite(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://old.example.com"})
	rt.Put("mmx-node1", "Node One Updated", []string{"https://new.example.com"})

	entry := rt.Get("mmx-node1")
	if entry.NodeName != "Node One Updated" {
		t.Errorf("expected updated name, got %q", entry.NodeName)
	}
	if entry.Addresses[0] != "https://new.example.com" {
		t.Errorf("expected updated address, got %q", entry.Addresses[0])
	}
	if rt.Count() != 1 {
		t.Error("overwrite should not increase count")
	}
}

func TestRouteTable_GetByModel(t *testing.T) {
	rt := newTestRouteTable()

	// Node with specific models
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	rt.mu.Lock()
	rt.entries["mmx-node1"].Models = []string{"gpt-4", "gpt-3.5"}
	rt.mu.Unlock()

	// Node with no models (serves all)
	rt.Put("mmx-node2", "Node Two", []string{"https://node2.example.com"})

	// Node with different models
	rt.Put("mmx-node3", "Node Three", []string{"https://node3.example.com"})
	rt.mu.Lock()
	rt.entries["mmx-node3"].Models = []string{"claude-3"}
	rt.mu.Unlock()

	// Query for gpt-4 — should match node1 (has it) and node2 (serves all)
	results := rt.GetByModel("gpt-4")
	if len(results) != 2 {
		t.Errorf("expected 2 results for gpt-4, got %d", len(results))
	}

	// Query for claude-3 — should match node3 and node2
	results = rt.GetByModel("claude-3")
	if len(results) != 2 {
		t.Errorf("expected 2 results for claude-3, got %d", len(results))
	}

	// Query for unknown model — should only match node2 (serves all)
	results = rt.GetByModel("unknown-model")
	if len(results) != 1 {
		t.Errorf("expected 1 result for unknown-model, got %d", len(results))
	}
}

func TestRouteTable_GetByModelEmpty(t *testing.T) {
	rt := newTestRouteTable()
	results := rt.GetByModel("gpt-4")
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty table, got %d", len(results))
	}
}

func TestRouteTable_GetByModelExcludesExpired(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})

	// Expire it
	rt.mu.Lock()
	rt.entries["mmx-node1"].UpdatedAt = time.Now().Add(-routeTTL - time.Second)
	rt.mu.Unlock()

	results := rt.GetByModel("any-model")
	if len(results) != 0 {
		t.Errorf("expected 0 results (expired), got %d", len(results))
	}
}

func TestRouteTable_SelectBestNode(t *testing.T) {
	rt := newTestRouteTable()

	// Add nodes with different latencies and loads
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	rt.mu.Lock()
	e1 := rt.entries["mmx-node1"]
	e1.Models = []string{"gpt-4"}
	e1.LatencyMS = 50
	e1.LoadScore = 0.2
	e1.LastSeen = time.Now()
	rt.mu.Unlock()

	rt.Put("mmx-node2", "Node Two", []string{"https://node2.example.com"})
	rt.mu.Lock()
	e2 := rt.entries["mmx-node2"]
	e2.Models = []string{"gpt-4"}
	e2.LatencyMS = 200
	e2.LoadScore = 0.8
	e2.LastSeen = time.Now()
	rt.mu.Unlock()

	best := rt.SelectBestNode("gpt-4")
	if best == nil {
		t.Fatal("expected a node, got nil")
	}
	// Node1 should be better (lower latency, lower load)
	if best.NodeID != "mmx-node1" {
		t.Errorf("expected mmx-node1 as best, got %q", best.NodeID)
	}
}

func TestRouteTable_SelectBestNodeNoMatch(t *testing.T) {
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	rt.mu.Lock()
	rt.entries["mmx-node1"].Models = []string{"gpt-4"}
	rt.mu.Unlock()

	best := rt.SelectBestNode("nonexistent-model")
	if best != nil {
		t.Error("expected nil for non-matching model")
	}
}

func TestRouteTable_SelectBestNodeEmptyTable(t *testing.T) {
	rt := newTestRouteTable()
	best := rt.SelectBestNode("gpt-4")
	if best != nil {
		t.Error("expected nil from empty table")
	}
}

// ============================================================
// initRouteTable Tests
// ============================================================

func TestInitRouteTable(t *testing.T) {
	rt := initRouteTable()
	if rt == nil {
		t.Fatal("initRouteTable returned nil")
	}
	if rt.entries == nil {
		t.Error("entries map should be initialized")
	}
	if rt.Count() != 0 {
		t.Error("new route table should be empty")
	}
}

// ============================================================
// NetworkMode Tests
// ============================================================

func TestNetworkModeValues(t *testing.T) {
	if NetworkModePersonal != "personal" {
		t.Errorf("NetworkModePersonal = %q, want %q", NetworkModePersonal, "personal")
	}
	if NetworkModeShared != "shared" {
		t.Errorf("NetworkModeShared = %q, want %q", NetworkModeShared, "shared")
	}
}

// ============================================================
// NetworkManager Tests
// ============================================================

func newTestNetworkManager(t *testing.T) *NetworkManager {
	t.Helper()
	tmpDir := t.TempDir()
	nm := &NetworkManager{
		dataPath: tmpDir + "/network.json",
		config: NetworkConfig{
			Mode:             NetworkModeShared,
			ConsentAccepted:  true,
			NodeID:           "mmx-test-node",
			NodeName:         "Test Node",
			BootstrapNodes:   []string{},
			SharedModels:     []string{},
			Peers:            []PeerInfo{},
			MaxDailyRequests: 1000,
			Addresses:        []string{},
			RelayEnabled:     true,
			QuotaAllocation:  DefaultQuotaAllocation(),
		},
		startTime: time.Now(),
	}
	return nm
}

func TestNetworkManager_IsSharedMode(t *testing.T) {
	nm := newTestNetworkManager(t)
	if !nm.IsSharedMode() {
		t.Error("expected shared mode")
	}

	nm.config.Mode = NetworkModePersonal
	if nm.IsSharedMode() {
		t.Error("expected personal mode")
	}
}

func TestNetworkManager_GetNodeID(t *testing.T) {
	nm := newTestNetworkManager(t)
	if nm.GetNodeID() != "mmx-test-node" {
		t.Errorf("GetNodeID = %q, want %q", nm.GetNodeID(), "mmx-test-node")
	}
}

func TestNetworkManager_AddPeer(t *testing.T) {
	nm := newTestNetworkManager(t)

	peer := PeerInfo{
		NodeID: "mmx-peer1",
		Name:   "Peer One",
		Status: "online",
		Models: []string{"gpt-4"},
	}
	if err := nm.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer failed: %v", err)
	}

	peers := nm.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].NodeID != "mmx-peer1" {
		t.Errorf("peer NodeID = %q, want %q", peers[0].NodeID, "mmx-peer1")
	}
}

func TestNetworkManager_AddPeerUpdate(t *testing.T) {
	nm := newTestNetworkManager(t)

	peer1 := PeerInfo{NodeID: "mmx-peer1", Name: "Peer One", Status: "online", Unlocked: true}
	nm.AddPeer(peer1)

	// Update same peer
	peer2 := PeerInfo{NodeID: "mmx-peer1", Name: "Peer One Updated", Status: "offline"}
	nm.AddPeer(peer2)

	peers := nm.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer after update, got %d", len(peers))
	}
	if peers[0].Name != "Peer One Updated" {
		t.Errorf("name should be updated, got %q", peers[0].Name)
	}
	// Unlocked state should be preserved
	if !peers[0].Unlocked {
		t.Error("Unlocked state should be preserved from original peer")
	}
}

func TestNetworkManager_AddPeerPersonalMode(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.config.Mode = NetworkModePersonal

	peer := PeerInfo{NodeID: "mmx-peer1", Name: "Peer One", Status: "online"}
	err := nm.AddPeer(peer)
	if err == nil {
		t.Error("AddPeer should fail in personal mode")
	}
}

func TestNetworkManager_RemovePeer(t *testing.T) {
	nm := newTestNetworkManager(t)

	nm.AddPeer(PeerInfo{NodeID: "mmx-peer1", Name: "Peer One", Status: "online"})
	nm.AddPeer(PeerInfo{NodeID: "mmx-peer2", Name: "Peer Two", Status: "online"})

	if err := nm.RemovePeer("mmx-peer1"); err != nil {
		t.Fatalf("RemovePeer failed: %v", err)
	}

	peers := nm.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer after removal, got %d", len(peers))
	}
	if peers[0].NodeID != "mmx-peer2" {
		t.Errorf("remaining peer should be peer2, got %q", peers[0].NodeID)
	}
}

func TestNetworkManager_RemovePeerNotFound(t *testing.T) {
	nm := newTestNetworkManager(t)
	err := nm.RemovePeer("mmx-nonexistent")
	if err == nil {
		t.Error("RemovePeer should fail for non-existent peer")
	}
}

func TestNetworkManager_RemovePeerPersonalMode(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.config.Mode = NetworkModePersonal
	err := nm.RemovePeer("mmx-peer1")
	if err == nil {
		t.Error("RemovePeer should fail in personal mode")
	}
}

func TestNetworkManager_GetPeersReturnsCopy(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.AddPeer(PeerInfo{NodeID: "mmx-peer1", Name: "Peer One", Status: "online"})

	peers := nm.GetPeers()
	peers[0].Name = "Modified"

	original := nm.GetPeers()
	if original[0].Name == "Modified" {
		t.Error("GetPeers should return a copy")
	}
}

func TestNetworkManager_RecordRelayResult(t *testing.T) {
	nm := newTestNetworkManager(t)

	nm.RecordRelayResult(true)
	nm.RecordRelayResult(true)
	nm.RecordRelayResult(false)

	if nm.config.Stats.RequestsRelayed != 3 {
		t.Errorf("RequestsRelayed = %d, want 3", nm.config.Stats.RequestsRelayed)
	}
	if nm.config.Stats.RelaySuccess != 2 {
		t.Errorf("RelaySuccess = %d, want 2", nm.config.Stats.RelaySuccess)
	}
	if nm.config.Stats.RelayFailed != 1 {
		t.Errorf("RelayFailed = %d, want 1", nm.config.Stats.RelayFailed)
	}
}

func TestNetworkManager_RecordReceived(t *testing.T) {
	nm := newTestNetworkManager(t)

	nm.RecordReceived()
	nm.RecordReceived()

	if nm.config.Stats.RequestsReceived != 2 {
		t.Errorf("RequestsReceived = %d, want 2", nm.config.Stats.RequestsReceived)
	}
}

func TestNetworkManager_UpdateConfig(t *testing.T) {
	nm := newTestNetworkManager(t)

	relayEnabled := false
	err := nm.UpdateConfig("New Name", []string{"gpt-4", "claude-3"}, 500, &relayEnabled)
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}

	if nm.config.NodeName != "New Name" {
		t.Errorf("NodeName = %q, want %q", nm.config.NodeName, "New Name")
	}
	if len(nm.config.SharedModels) != 2 {
		t.Errorf("SharedModels length = %d, want 2", len(nm.config.SharedModels))
	}
	if nm.config.MaxDailyRequests != 500 {
		t.Errorf("MaxDailyRequests = %d, want 500", nm.config.MaxDailyRequests)
	}
	if nm.config.RelayEnabled != false {
		t.Error("RelayEnabled should be false")
	}
}

func TestNetworkManager_UpdateConfigPersonalMode(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.config.Mode = NetworkModePersonal

	err := nm.UpdateConfig("New Name", nil, 0, nil)
	if err == nil {
		t.Error("UpdateConfig should fail in personal mode")
	}
}

func TestNetworkManager_UpdateConfigPartial(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.config.NodeName = "Original"

	// Only update name
	err := nm.UpdateConfig("Updated", nil, 0, nil)
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if nm.config.NodeName != "Updated" {
		t.Errorf("NodeName = %q, want %q", nm.config.NodeName, "Updated")
	}
}

func TestNetworkManager_SetShareToPool(t *testing.T) {
	nm := newTestNetworkManager(t)
	if nm.IsSharingToPool() {
		t.Error("ShareToPool should be false by default")
	}

	nm.SetShareToPool(true)
	if !nm.IsSharingToPool() {
		t.Error("ShareToPool should be true after setting")
	}

	nm.SetShareToPool(false)
	if nm.IsSharingToPool() {
		t.Error("ShareToPool should be false after unsetting")
	}
}

func TestNetworkManager_SetCapabilities(t *testing.T) {
	nm := newTestNetworkManager(t)

	caps := PeerCapabilities{
		Providers: []string{"openai", "anthropic"},
		CanRelay:  true,
		CanSeed:   false,
		Bandwidth: "1Gbps",
	}
	nm.SetCapabilities(caps)

	if len(nm.config.Capabilities.Providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(nm.config.Capabilities.Providers))
	}
	if !nm.config.Capabilities.CanRelay {
		t.Error("CanRelay should be true")
	}
}

func TestNetworkManager_RecordConsent(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.config.ConsentAccepted = false

	if err := nm.RecordConsent(); err != nil {
		t.Fatalf("RecordConsent failed: %v", err)
	}
	if !nm.config.ConsentAccepted {
		t.Error("ConsentAccepted should be true")
	}
	if nm.config.ConsentTime == "" {
		t.Error("ConsentTime should be set")
	}
}

func TestNetworkManager_updateOnlineCount(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.config.Peers = []PeerInfo{
		{NodeID: "mmx-peer1", Status: "online"},
		{NodeID: "mmx-peer2", Status: "offline"},
		{NodeID: "mmx-peer3", Status: "online"},
	}
	nm.updateOnlineCount()
	if nm.config.Stats.OnlinePeers != 2 {
		t.Errorf("OnlinePeers = %d, want 2", nm.config.Stats.OnlinePeers)
	}
}

func TestNetworkManager_countOnlinePeers(t *testing.T) {
	nm := newTestNetworkManager(t)
	now := time.Now()
	nm.config.Peers = []PeerInfo{
		{NodeID: "mmx-peer1", LastSeen: now.Format(time.RFC3339)},
		{NodeID: "mmx-peer2", LastSeen: now.Add(-10 * time.Minute).Format(time.RFC3339)},
		{NodeID: "mmx-peer3", LastSeen: now.Add(-1 * time.Hour).Format(time.RFC3339)},
	}
	count := nm.countOnlinePeers()
	// Only peer1 (now) and peer2 (-10min, within 5min? No, 10 > 5) are online
	if count != 1 {
		t.Errorf("countOnlinePeers = %d, want 1", count)
	}
}

// ============================================================
// EnableSharedNetwork / DisableSharedNetwork Tests
// ============================================================

func TestNetworkManager_EnableSharedNetwork(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.config.Mode = NetworkModePersonal
	nm.config.ConsentAccepted = false

	// Should fail without consent
	err := nm.EnableSharedNetwork()
	if err == nil {
		t.Error("EnableSharedNetwork should fail without consent")
	}

	// Give consent first
	nm.config.ConsentAccepted = true
	err = nm.EnableSharedNetwork()
	if err != nil {
		t.Fatalf("EnableSharedNetwork failed: %v", err)
	}
	if nm.config.Mode != NetworkModeShared {
		t.Errorf("Mode = %q, want %q", nm.config.Mode, NetworkModeShared)
	}
	if nm.config.NodeID == "" {
		t.Error("NodeID should be assigned")
	}
}

func TestNetworkManager_DisableSharedNetwork(t *testing.T) {
	nm := newTestNetworkManager(t)
	nm.config.Mode = NetworkModeShared

	err := nm.DisableSharedNetwork()
	if err != nil {
		t.Fatalf("DisableSharedNetwork failed: %v", err)
	}
	if nm.config.Mode != NetworkModePersonal {
		t.Errorf("Mode = %q, want %q", nm.config.Mode, NetworkModePersonal)
	}
}

// ============================================================
// Disclaimer Tests
// ============================================================

func TestGetDisclaimer(t *testing.T) {
	d := GetDisclaimer()
	if d.Title == "" {
		t.Error("Disclaimer title should not be empty")
	}
	if len(d.Sections) == 0 {
		t.Error("Disclaimer should have sections")
	}
	if d.ConfirmationText == "" {
		t.Error("Disclaimer should have confirmation text")
	}

	// Check that at least one section is a risk warning
	hasRisk := false
	for _, s := range d.Sections {
		if s.IsRisk {
			hasRisk = true
			break
		}
	}
	if !hasRisk {
		t.Error("Disclaimer should have at least one risk section")
	}
}

// ============================================================
// Constants Tests
// ============================================================

func TestNetworkConstants(t *testing.T) {
	if p2pNodeIDPrefix != "mmx-" {
		t.Errorf("p2pNodeIDPrefix = %q, want %q", p2pNodeIDPrefix, "mmx-")
	}
	if maxRelayHops != 3 {
		t.Errorf("maxRelayHops = %d, want 3", maxRelayHops)
	}
	if routeTTL != 10*time.Minute {
		t.Errorf("routeTTL = %v, want 10m", routeTTL)
	}
}
