package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ============================================================
// Seed Peer Info Tests
// ============================================================

func TestSeedPeerInfoStruct(t *testing.T) {
	info := SeedPeerInfo{
		NodeID:    "mmx-node1",
		NodeName:  "Test Node",
		Addresses: []string{"https://node1.example.com"},
		Status:    "online",
		Models:    []string{"gpt-4"},
		LatencyMS: 42.5,
		LoadScore: 0.3,
		IsGateway: true,
		IsSeed:    true,
		Region:    "ap",
		Version:   "3.2.0",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded SeedPeerInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.NodeID != "mmx-node1" {
		t.Errorf("NodeID = %q, want %q", decoded.NodeID, "mmx-node1")
	}
	if !decoded.IsSeed {
		t.Error("IsSeed should be true")
	}
}

func TestSeedRegisterRequestStruct(t *testing.T) {
	req := SeedRegisterRequest{
		NodeID:    "mmx-node1",
		NodeName:  "Test Node",
		Addresses: []string{"https://node1.example.com"},
		Models:    []string{"gpt-4"},
		Secret:    "shared-secret",
		Capabilities: PeerCapabilities{
			Providers: []string{"openai"},
			CanRelay:  true,
		},
		ShareToPool: true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded SeedRegisterRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.NodeID != "mmx-node1" {
		t.Errorf("NodeID mismatch")
	}
	if !decoded.ShareToPool {
		t.Error("ShareToPool should be true")
	}
}

func TestSeedPeersResponseStruct(t *testing.T) {
	resp := SeedPeersResponse{
		Peers: []SeedPeerInfo{
			{NodeID: "mmx-node1", IsSeed: true},
			{NodeID: "mmx-node2", IsSeed: true},
		},
		Total:    2,
		Online:   1,
		ServedAt: time.Now().Unix(),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded SeedPeersResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.Total != 2 {
		t.Errorf("Total = %d, want 2", decoded.Total)
	}
}

// ============================================================
// Seed Handler Tests
// ============================================================

func TestHandleSeedPeersNoRouteTable(t *testing.T) {
	// Save and restore global state
	oldRT := routeTable
	routeTable = nil
	defer func() { routeTable = oldRT }()

	req := httptest.NewRequest("GET", "/api/peers", nil)
	w := httptest.NewRecorder()

	handleSeedPeers(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp SeedPeersResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("Total = %d, want 0", resp.Total)
	}
}

func TestHandleSeedPeersWithEntries(t *testing.T) {
	oldRT := routeTable
	rt := newTestRouteTable()
	rt.Put("mmx-node1", "Node One", []string{"https://node1.example.com"})
	rt.mu.Lock()
	rt.entries["mmx-node1"].Status = "online"
	rt.entries["mmx-node1"].LastSeen = time.Now()
	rt.mu.Unlock()

	routeTable = rt
	defer func() { routeTable = oldRT }()

	req := httptest.NewRequest("GET", "/api/peers", nil)
	w := httptest.NewRecorder()

	handleSeedPeers(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp SeedPeersResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total < 1 {
		t.Errorf("expected at least 1 peer, got %d", resp.Total)
	}
}

func TestHandleSeedPeersFiltersOldNodes(t *testing.T) {
	oldRT := routeTable
	rt := newTestRouteTable()
	rt.Put("mmx-old", "Old Node", []string{"https://old.example.com"})
	rt.mu.Lock()
	rt.entries["mmx-old"].LastSeen = time.Now().Add(-31 * time.Minute) // > 30 min
	rt.entries["mmx-old"].Status = "online"
	rt.mu.Unlock()

	routeTable = rt
	defer func() { routeTable = oldRT }()

	req := httptest.NewRequest("GET", "/api/peers", nil)
	w := httptest.NewRecorder()

	handleSeedPeers(w, req)

	var resp SeedPeersResponse
	json.NewDecoder(w.Body).Decode(&resp)
	// Old node should be filtered out
	if resp.Total != 0 {
		t.Errorf("expected 0 peers (old node filtered), got %d", resp.Total)
	}
}

func TestHandleSeedRegister(t *testing.T) {
	oldRT := routeTable
	rt := newTestRouteTable()
	routeTable = rt
	defer func() { routeTable = oldRT }()

	body := `{"node_id":"mmx-new","node_name":"New Node","addresses":["https://new.example.com"],"models":["gpt-4"]}`
	req := httptest.NewRequest("POST", "/api/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleSeedRegister(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Verify node was added to route table
	entry := rt.Get("mmx-new")
	if entry == nil {
		t.Fatal("node should be in route table after registration")
	}
	if entry.NodeName != "New Node" {
		t.Errorf("NodeName = %q, want %q", entry.NodeName, "New Node")
	}
}

func TestHandleSeedRegisterMissingNodeID(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/register", strings.NewReader(`{"node_name":"No ID"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleSeedRegister(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleSeedRegisterInvalidBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/register", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleSeedRegister(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleSeedRegisterMethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/register", nil)
	w := httptest.NewRecorder()

	handleSeedRegister(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleSeedHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handleSeedHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	if resp["role"] != "seed" {
		t.Errorf("role = %v, want seed", resp["role"])
	}
}

// ============================================================
// getSelfAddresses Tests
// ============================================================

func TestGetSelfAddressesCaching(t *testing.T) {
	// Reset cache
	oldCache := cachedSelfAddresses
	cachedSelfAddresses = nil
	defer func() { cachedSelfAddresses = oldCache }()

	// Set cache directly
	cachedSelfAddresses = []string{"https://cached.example.com"}
	addrs := getSelfAddresses()
	if len(addrs) != 1 || addrs[0] != "https://cached.example.com" {
		t.Errorf("should return cached addresses, got %v", addrs)
	}
}

// ============================================================
// Heartbeat Payload Tests
// ============================================================

func TestHeartbeatPayloadJSON(t *testing.T) {
	hb := HeartbeatPayload{
		NodeID:    "mmx-node1",
		NodeName:  "Test Node",
		Addresses: []string{"https://node1.example.com"},
		Models:    []string{"gpt-4", "claude-3"},
		Uptime:    3600,
		Version:   "3.2.0",
		Timestamp: time.Now().Unix(),
	}

	data, err := json.Marshal(hb)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded HeartbeatPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.NodeID != "mmx-node1" {
		t.Errorf("NodeID mismatch")
	}
	if decoded.Uptime != 3600 {
		t.Errorf("Uptime = %d, want 3600", decoded.Uptime)
	}
}

func TestHeartbeatResponseJSON(t *testing.T) {
	resp := HeartbeatResponse{
		Status: "ok",
		Peers: []HeartbeatPeerInfo{
			{NodeID: "mmx-peer1", Status: "online"},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded HeartbeatResponse
	json.Unmarshal(data, &decoded)
	if decoded.Status != "ok" {
		t.Errorf("Status = %q, want ok", decoded.Status)
	}
	if len(decoded.Peers) != 1 {
		t.Errorf("Peers count = %d, want 1", len(decoded.Peers))
	}
}

// ============================================================
// Heartbeat State Tracking Tests
// ============================================================

func TestRecordSuccessfulHeartbeat(t *testing.T) {
	// Reset global state
	oldStates := heartbeatStates
	heartbeatStates = make(map[string]*peerHeartbeatState)
	defer func() { heartbeatStates = oldStates }()

	recordSuccessfulHeartbeat("mmx-node1")

	state, ok := heartbeatStates["mmx-node1"]
	if !ok {
		t.Fatal("state should exist for mmx-node1")
	}
	if state.missedCount != 0 {
		t.Errorf("missedCount = %d, want 0", state.missedCount)
	}
	if state.lastHeartbeat.IsZero() {
		t.Error("lastHeartbeat should be set")
	}
}

func TestRecordMissedHeartbeat(t *testing.T) {
	oldStates := heartbeatStates
	heartbeatStates = make(map[string]*peerHeartbeatState)
	defer func() { heartbeatStates = oldStates }()

	recordMissedHeartbeat("mmx-node1")
	recordMissedHeartbeat("mmx-node1")

	state := heartbeatStates["mmx-node1"]
	if state.missedCount != 2 {
		t.Errorf("missedCount = %d, want 2", state.missedCount)
	}
}

func TestRecordSuccessfulHeartbeatResetsMissed(t *testing.T) {
	oldStates := heartbeatStates
	heartbeatStates = make(map[string]*peerHeartbeatState)
	defer func() { heartbeatStates = oldStates }()

	recordMissedHeartbeat("mmx-node1")
	recordMissedHeartbeat("mmx-node1")
	recordSuccessfulHeartbeat("mmx-node1")

	state := heartbeatStates["mmx-node1"]
	if state.missedCount != 0 {
		t.Errorf("missedCount should be reset to 0, got %d", state.missedCount)
	}
}

// ============================================================
// verifyHeartbeatAuth Tests
// ============================================================

func TestVerifyHeartbeatAuthEmptySignature(t *testing.T) {
	result := verifyHeartbeatAuth("mmx-node1", "")
	if result {
		t.Error("empty signature should fail verification")
	}
}

func TestVerifyHeartbeatAuthInvalidBase64(t *testing.T) {
	result := verifyHeartbeatAuth("mmx-node1", "not-valid-base64!!!")
	if result {
		t.Error("invalid base64 should fail verification")
	}
}

func TestVerifyHeartbeatAuthWrongLength(t *testing.T) {
	// Valid base64 but wrong signature length
	result := verifyHeartbeatAuth("mmx-node1", "c2hvcnQ=") // "short" in base64
	if result {
		t.Error("wrong-length signature should fail verification")
	}
}

func TestVerifyHeartbeatAuthSelfNodeID(t *testing.T) {
	// Self heartbeat should be accepted
	oldNetMgr := netMgr
	nm := newTestNetworkManager(t)
	netMgr = nm
	defer func() { netMgr = oldNetMgr }()

	// Create a fake but valid-length signature (64 bytes base64)
	fakeSig := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="
	result := verifyHeartbeatAuth(nm.GetNodeID(), fakeSig)
	// Self node should always return true
	if !result {
		t.Error("self-node heartbeat should be accepted")
	}
}

func TestVerifyHeartbeatAuthUnknownNode(t *testing.T) {
	// Unknown nodes should be accepted for discovery
	result := verifyHeartbeatAuth("mmx-unknown", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==")
	if !result {
		t.Error("unknown node heartbeat should be accepted for discovery")
	}
}

// ============================================================
// Heartbeat Constants Tests
// ============================================================

func TestHeartbeatConstants(t *testing.T) {
	if heartbeatInterval != 60*time.Second {
		t.Errorf("heartbeatInterval = %v, want 60s", heartbeatInterval)
	}
	if maxMissedHeartbeats != 3 {
		t.Errorf("maxMissedHeartbeats = %d, want 3", maxMissedHeartbeats)
	}
}
