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
// RouteTable tests
// ============================================================

func TestHB8_RouteTable_UpsertEntry_Nil(t *testing.T) {
	rt := initRouteTable()
	rt.UpsertEntry(nil)
	if rt.Count() != 0 {
		t.Fatalf("expected 0 entries after nil upsert, got %d", rt.Count())
	}
}

func TestHB8_RouteTable_UpsertEntry_Valid(t *testing.T) {
	rt := initRouteTable()
	e := &RouteEntry{
		NodeID:    "node-1",
		NodeName:  "test",
		Addresses: []string{"https://1.2.3.4:8000"},
		Status:    "online",
		UpdatedAt: time.Now(),
		Models:    []string{"gpt-4o"},
		LatencyMS: 50,
		LoadScore: 0.3,
	}
	rt.UpsertEntry(e)
	if rt.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", rt.Count())
	}
	got := rt.Get("node-1")
	if got == nil {
		t.Fatal("expected to find node-1")
	}
	if got.NodeName != "test" {
		t.Fatalf("expected NodeName=test, got %s", got.NodeName)
	}
	if got.LatencyMS != 50 {
		t.Fatalf("expected LatencyMS=50, got %f", got.LatencyMS)
	}
}

func TestHB8_RouteTable_Get_Expired(t *testing.T) {
	rt := initRouteTable()
	rt.mu.Lock()
	rt.entries["old-node"] = &RouteEntry{
		NodeID:    "old-node",
		UpdatedAt: time.Now().Add(-11 * time.Minute),
	}
	rt.mu.Unlock()
	got := rt.Get("old-node")
	if got != nil {
		t.Fatal("expired entry should return nil")
	}
}

func TestHB8_RouteTable_GetByModel_NoMatch(t *testing.T) {
	rt := initRouteTable()
	e := &RouteEntry{
		NodeID:    "n1",
		UpdatedAt: time.Now(),
		Models:    []string{"gpt-4o"},
	}
	rt.UpsertEntry(e)
	results := rt.GetByModel("claude-3")
	if len(results) != 0 {
		t.Fatalf("expected 0 results for unmatched model, got %d", len(results))
	}
}

func TestHB8_RouteTable_GetByModel_EmptyModelsServesAny(t *testing.T) {
	rt := initRouteTable()
	e := &RouteEntry{
		NodeID:    "n1",
		UpdatedAt: time.Now(),
		Models:    nil,
	}
	rt.UpsertEntry(e)
	results := rt.GetByModel("any-model")
	if len(results) != 1 {
		t.Fatalf("expected 1 result for empty models node, got %d", len(results))
	}
}

func TestHB8_RouteTable_SelectBestNode_NoCandidates(t *testing.T) {
	rt := initRouteTable()
	got := rt.SelectBestNode("gpt-4o")
	if got != nil {
		t.Fatal("expected nil for no candidates")
	}
}

func TestHB8_RouteTable_SelectBestNode_Single(t *testing.T) {
	rt := initRouteTable()
	e := &RouteEntry{
		NodeID:    "n1",
		UpdatedAt: time.Now(),
		Models:    []string{"gpt-4o"},
		LatencyMS: 100,
		LoadScore: 0.5,
	}
	rt.UpsertEntry(e)
	got := rt.SelectBestNode("gpt-4o")
	if got == nil || got.NodeID != "n1" {
		t.Fatal("expected n1 as best node")
	}
}

func TestHB8_RouteTable_SelectBestNode_Multiple(t *testing.T) {
	rt := initRouteTable()
	rt.UpsertEntry(&RouteEntry{
		NodeID:    "slow-node",
		UpdatedAt: time.Now(),
		Models:    []string{"gpt-4o"},
		LatencyMS: 500,
		LoadScore: 0.9,
	})
	rt.UpsertEntry(&RouteEntry{
		NodeID:    "fast-node",
		UpdatedAt: time.Now(),
		Models:    []string{"gpt-4o"},
		LatencyMS: 10,
		LoadScore: 0.1,
	})
	got := rt.SelectBestNode("gpt-4o")
	if got == nil || got.NodeID != "fast-node" {
		t.Fatalf("expected fast-node, got %v", got)
	}
}

// ============================================================
// NetworkManager unit tests
// ============================================================

func TestHB8_NetworkManager_IsSharedMode_Default(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{Mode: NetworkModePersonal}}
	if nm.IsSharedMode() {
		t.Fatal("default should be personal mode")
	}
}

func TestHB8_NetworkManager_IsSharedMode_Shared(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{Mode: NetworkModeShared}}
	if !nm.IsSharedMode() {
		t.Fatal("should be shared mode")
	}
}

func TestHB8_NetworkManager_GetNodeID(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{NodeID: "test-node-123"}}
	if nm.GetNodeID() != "test-node-123" {
		t.Fatalf("expected test-node-123, got %s", nm.GetNodeID())
	}
}

func TestHB8_NetworkManager_IsSharingToPool(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{ShareToPool: true}}
	if !nm.IsSharingToPool() {
		t.Fatal("should be sharing to pool")
	}
}

func TestHB8_NetworkManager_IsSharingToPool_False(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{ShareToPool: false}}
	if nm.IsSharingToPool() {
		t.Fatal("should not be sharing to pool")
	}
}

func TestHB8_NetworkManager_RecordRelayResult_Success(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{}}
	nm.RecordRelayResult(true)
	nm.mu.RLock()
	relayed := nm.config.Stats.RequestsRelayed
	success := nm.config.Stats.RelaySuccess
	nm.mu.RUnlock()
	if relayed != 1 || success != 1 {
		t.Fatalf("expected relayed=1 success=1, got relayed=%d success=%d", relayed, success)
	}
}

func TestHB8_NetworkManager_RecordRelayResult_Failure(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{}}
	nm.RecordRelayResult(false)
	nm.mu.RLock()
	relayed := nm.config.Stats.RequestsRelayed
	failed := nm.config.Stats.RelayFailed
	nm.mu.RUnlock()
	if relayed != 1 || failed != 1 {
		t.Fatalf("expected relayed=1 failed=1, got relayed=%d failed=%d", relayed, failed)
	}
}

func TestHB8_NetworkManager_RecordReceived(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{}}
	nm.RecordReceived()
	nm.mu.RLock()
	received := nm.config.Stats.RequestsReceived
	nm.mu.RUnlock()
	if received != 1 {
		t.Fatalf("expected received=1, got %d", received)
	}
}

func TestHB8_NetworkManager_HasPeer_True(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{
		Peers: []PeerInfo{{NodeID: "peer-1"}},
	}}
	if !nm.HasPeer("peer-1") {
		t.Fatal("should have peer-1")
	}
}

func TestHB8_NetworkManager_HasPeer_False(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{Peers: []PeerInfo{}}}
	if nm.HasPeer("peer-1") {
		t.Fatal("should not have peer-1")
	}
}

func TestHB8_NetworkManager_GetPeers(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{
		Peers: []PeerInfo{{NodeID: "p1"}, {NodeID: "p2"}},
	}}
	peers := nm.GetPeers()
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
}

func TestHB8_NetworkManager_UpdateConfig_NotShared(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{Mode: NetworkModePersonal}}
	err := nm.UpdateConfig("test", []string{"m1"}, 100, nil)
	if err == nil {
		t.Fatal("expected error when not in shared mode")
	}
}

func TestHB8_NetworkManager_SetCapabilities(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{}}
	caps := PeerCapabilities{CanRelay: true, CanSeed: false}
	nm.SetCapabilities(caps)
	nm.mu.RLock()
	got := nm.config.Capabilities.CanRelay
	nm.mu.RUnlock()
	if !got {
		t.Fatal("expected CanRelay=true")
	}
}

func TestHB8_NetworkManager_countOnlinePeers(t *testing.T) {
	now := time.Now().Format(time.RFC3339)
	old := time.Now().Add(-10 * time.Minute).Format(time.RFC3339)
	nm := &NetworkManager{config: NetworkConfig{
		Peers: []PeerInfo{
			{NodeID: "p1", LastSeen: now},
			{NodeID: "p2", LastSeen: old},
			{NodeID: "p3", LastSeen: now},
		},
	}}
	count := nm.countOnlinePeers()
	if count != 2 {
		t.Fatalf("expected 2 online peers, got %d", count)
	}
}

func TestHB8_NetworkManager_RemovePeer_NotShared(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{Mode: NetworkModePersonal}}
	err := nm.RemovePeer("p1")
	if err == nil {
		t.Fatal("expected error when not in shared mode")
	}
}

func TestHB8_NetworkManager_RemovePeer_NotFound(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{Mode: NetworkModeShared, Peers: []PeerInfo{}}}
	err := nm.RemovePeer("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent peer")
	}
}

func TestHB8_NetworkManager_AddPeer_NotShared(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{Mode: NetworkModePersonal}}
	err := nm.AddPeer(PeerInfo{NodeID: "p1"})
	if err == nil {
		t.Fatal("expected error when not in shared mode")
	}
}

func TestHB8_NetworkManager_AddPeer_NewPeer(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{
		Mode:  NetworkModeShared,
		Peers: []PeerInfo{},
	}}
	err := nm.AddPeer(PeerInfo{NodeID: "p1", Status: "online"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nm.HasPeer("p1") {
		t.Fatal("should have peer p1")
	}
}

func TestHB8_NetworkManager_AddPeer_UpdateExisting(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{
		Mode:  NetworkModeShared,
		Peers: []PeerInfo{{NodeID: "p1", Name: "old"}},
	}}
	err := nm.AddPeer(PeerInfo{NodeID: "p1", Name: "new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	peers := nm.GetPeers()
	if len(peers) != 1 || peers[0].Name != "new" {
		t.Fatalf("expected name=new, got %s", peers[0].Name)
	}
}

func TestHB8_NetworkManager_SetNetworkEnabled_Enable(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{}}
	nm.SetNetworkEnabled(true)
	nm.mu.RLock()
	enabled := nm.config.NetworkEnabled
	mode := nm.config.Mode
	nm.mu.RUnlock()
	if !enabled {
		t.Fatal("expected network_enabled=true")
	}
	if mode != NetworkModeShared {
		t.Fatal("expected shared mode")
	}
}

func TestHB8_NetworkManager_SetNetworkEnabled_Disable(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{NetworkEnabled: true, Mode: NetworkModeShared, ShareToPool: true}}
	nm.SetNetworkEnabled(false)
	nm.mu.RLock()
	enabled := nm.config.NetworkEnabled
	share := nm.config.ShareToPool
	nm.mu.RUnlock()
	if enabled {
		t.Fatal("expected network_enabled=false")
	}
	if share {
		t.Fatal("expected share_to_pool=false when network disabled")
	}
}

func TestHB8_NetworkManager_SetShareToPool_Enable(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{}}
	nm.SetShareToPool(true)
	nm.mu.RLock()
	share := nm.config.ShareToPool
	nm.mu.RUnlock()
	if !share {
		t.Fatal("expected share_to_pool=true")
	}
}

// ============================================================
// Network handlers
// ============================================================

func TestHB8_HandleNetworkDisclaimer(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/network/disclaimer", nil)
	handleNetworkDisclaimer(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp DisclaimerResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Title == "" {
		t.Fatal("expected non-empty title")
	}
	if len(resp.Sections) == 0 {
		t.Fatal("expected non-empty sections")
	}
}

func TestHB8_HandleNetworkStatus_NilManager(t *testing.T) {
	orig := netMgr
	netMgr = nil
	defer func() { netMgr = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/network/status", nil)
	handleNetworkStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleNetworkStats_NilManager(t *testing.T) {
	orig := netMgr
	netMgr = nil
	defer func() { netMgr = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/network/stats", nil)
	handleNetworkStats(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleNetworkConsent_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	orig := netMgr
	netMgr = &NetworkManager{config: NetworkConfig{}}
	defer func() { netMgr = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/consent", strings.NewReader(`{}`))
	handleNetworkConsent(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleNetworkConsent_Accepted(t *testing.T) {
	setupTestEnv(t)
	orig := netMgr
	nm := &NetworkManager{config: NetworkConfig{}}
	netMgr = nm
	defer func() { netMgr = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/consent", strings.NewReader(`{"accepted":true}`))
	handleNetworkConsent(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleNetworkEnable_NilManager(t *testing.T) {
	orig := netMgr
	netMgr = nil
	defer func() { netMgr = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/enable", nil)
	handleNetworkEnable(w, r)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB8_HandleNetworkToggle_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	orig := netMgr
	netMgr = &NetworkManager{config: NetworkConfig{}}
	defer func() { netMgr = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/toggle", strings.NewReader("bad"))
	handleNetworkToggle(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleNetworkToggle_Enable(t *testing.T) {
	setupTestEnv(t)
	orig := netMgr
	nm := &NetworkManager{config: NetworkConfig{}}
	netMgr = nm
	defer func() { netMgr = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/toggle", strings.NewReader(`{"enabled":true}`))
	handleNetworkToggle(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleNetworkConfigUpdate_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	orig := netMgr
	netMgr = &NetworkManager{config: NetworkConfig{Mode: NetworkModeShared}}
	defer func() { netMgr = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/config", strings.NewReader("bad"))
	handleNetworkConfigUpdate(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleNetworkPeers_NotShared(t *testing.T) {
	setupTestEnv(t)
	orig := netMgr
	netMgr = &NetworkManager{config: NetworkConfig{Mode: NetworkModePersonal}}
	defer func() { netMgr = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/network/peers", nil)
	handleNetworkPeers(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ============================================================
// Admin handler tests
// ============================================================

func TestHB8_MaskKey_Short(t *testing.T) {
	result := maskKey("abc")
	if result != "***" {
		t.Fatalf("expected ***, got %s", result)
	}
}

func TestHB8_MaskKey_Exactly8(t *testing.T) {
	result := maskKey("12345678")
	if result != "***" {
		t.Fatalf("expected *** for exactly 8 chars, got %s", result)
	}
}

func TestHB8_MaskKey_Long(t *testing.T) {
	result := maskKey("sk-1234567890abcdef")
	if result != "sk-1***cdef" {
		t.Fatalf("expected sk-1***cdef, got %s", result)
	}
}

func TestHB8_HandleSetupStatus(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	handleSetupStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleSetup_AlreadyInitialized(t *testing.T) {
	e := setupTestEnv(t)
	e.authInst.SetupAdmin("admin", "Str0ng!Pass1!", "admin@test.com")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(`{"username":"new","password":"Str0ng!Pass2!"}`))
	handleSetup(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleSetup_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader("bad"))
	handleSetup(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleLogin_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("bad"))
	handleLogin(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleLogin_EmptyFields(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"","password":""}`))
	handleLogin(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleLogin_BadCredentials(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
	handleLogin(w, r)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHB8_HandleAdminInfo(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/admin/info", nil)
	handleAdminInfo(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleChangePassword_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/change-password", strings.NewReader("bad"))
	handleChangePassword(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleUpdateEmail_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/update-email", strings.NewReader("bad"))
	handleUpdateEmail(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleUpdateEmail_Success(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/update-email", strings.NewReader(`{"email":"new@test.com"}`))
	handleUpdateEmail(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleGetConfig(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	handleGetConfig(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleSaveConfig_InvalidJSON(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader("bad"))
	handleSaveConfig(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleGetGateway(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/gateway", nil)
	handleGetGateway(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleSetGateway_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/gateway", strings.NewReader("bad"))
	handleSetGateway(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleSetGateway_Enable(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/gateway", strings.NewReader(`{"is_gateway":true}`))
	handleSetGateway(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleListProviders(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	handleListProviders(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleListProviders_Lite(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/providers?lite=true", nil)
	handleListProviders(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if _, ok := resp["providers"]; !ok {
		t.Fatal("expected providers key in response")
	}
}

func TestHB8_HandleGetPresets(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/providers/presets", nil)
	handleGetPresets(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleCreateProvider_NoID(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader(`{"name":"test"}`))
	handleCreateProvider(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleCreateProvider_InvalidJSON(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader("bad"))
	handleCreateProvider(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleCreateProvider_Valid(t *testing.T) {
	setupTestEnv(t)
	origHC := healthChecker
	healthChecker = &HealthChecker{statuses: make(map[string]*ProviderHealth)}
	defer func() { healthChecker = origHC }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader(`{"id":"test-p","name":"Test Provider","type":"openai_compatible","base_url":"https://api.test.com/v1","api_key":"sk-test123456","enabled":true}`))
	handleCreateProvider(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHB8_HandleRoutingMode(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/routing/mode", nil)
	handleGetRoutingMode(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleSetRoutingMode_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/routing/mode", strings.NewReader("bad"))
	handleSetRoutingMode(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleSetRoutingMode_InvalidMode(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/routing/mode", strings.NewReader(`{"mode":"invalid"}`))
	handleSetRoutingMode(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleSetRoutingMode_Valid(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/routing/mode", strings.NewReader(`{"mode":"cheapest"}`))
	handleSetRoutingMode(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleGetRoutingWeights(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/routing/weights", nil)
	handleGetRoutingWeights(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleSetRoutingWeights_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/routing/weights", strings.NewReader("bad"))
	handleSetRoutingWeights(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleSetRoutingWeights_Valid(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/routing/weights", strings.NewReader(`{"priority":0.5,"cost":0.3,"latency":0.1,"tokens":0.1}`))
	handleSetRoutingWeights(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleSMTPStatus(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/smtp/status", nil)
	handleSMTPStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleGetSMTPConfig(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/smtp/config", nil)
	handleGetSMTPConfig(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleSaveSMTPConfig_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/smtp/config", strings.NewReader("bad"))
	handleSaveSMTPConfig(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleSaveSMTPConfig_Valid(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/smtp/config", strings.NewReader(`{"host":"smtp.test.com","port":587,"username":"user","password":"pass","from_email":"test@test.com"}`))
	handleSaveSMTPConfig(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleRequestLogs(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	handleRequestLogs(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleUsageSummary(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/usage/summary", nil)
	handleUsageSummary(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleUsageProviders(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/usage/providers", nil)
	handleUsageProviders(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleUsageRecords(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/usage/records", nil)
	handleUsageRecords(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleUsageReset(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/usage/reset", nil)
	handleUsageReset(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleSiderStatus(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/sider/status", nil)
	handleSiderStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_Clamp(t *testing.T) {
	tests := []struct {
		v, min, max, want float64
	}{
		{0.5, 0, 1, 0.5},
		{-1, 0, 1, 0},
		{2, 0, 1, 1},
		{0, 0, 0, 0},
	}
	for _, tt := range tests {
		got := clamp(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clamp(%v,%v,%v)=%v, want %v", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}

// ============================================================
// Provider tests
// ============================================================

func TestHB8_ProviderManager_GetConfigured(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", Enabled: true, APIKey: "sk-test"})
	result := pm.GetConfigured()
	if len(result) != 1 {
		t.Fatalf("expected 1 configured provider, got %d", len(result))
	}
}

func TestHB8_ProviderManager_GetRaw_Found(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "raw-test", Name: "Raw", Enabled: true, APIKey: "sk-test123456"})
	_, ok := pm.GetRaw("raw-test")
	if !ok {
		t.Fatal("expected to find raw-test")
	}
}

func TestHB8_ProviderManager_GetRaw_NotFound(t *testing.T) {
	setupTestEnv(t)
	_, ok := pm.GetRaw("nonexistent")
	if ok {
		t.Fatal("expected not to find nonexistent")
	}
}

func TestHB8_ProviderManager_Get_Found(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "get-test", Name: "Get", Enabled: true, APIKey: "sk-test123456"})
	_, ok := pm.Get("get-test")
	if !ok {
		t.Fatal("expected to find get-test")
	}
}

func TestHB8_ProviderManager_Get_NotFound(t *testing.T) {
	setupTestEnv(t)
	_, ok := pm.Get("nonexistent")
	if ok {
		t.Fatal("expected not to find nonexistent")
	}
}

func TestHB8_Provider_SelectAPIKey_NoKeys(t *testing.T) {
	p := Provider{APIKey: ""}
	_, err := p.SelectAPIKey("")
	if err == nil {
		t.Fatal("expected error for no keys")
	}
}

func TestHB8_Provider_SelectAPIKey_LegacyKey(t *testing.T) {
	p := Provider{APIKey: "sk-legacy123456"}
	key, err := p.SelectAPIKey("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.ID != "legacy" {
		t.Fatalf("expected legacy key, got %s", key.ID)
	}
}

func TestHB8_Provider_SelectAPIKey_DisabledKey(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-test", Enabled: false, AccessControl: "private", Priority: 1},
		},
	}
	_, err := p.SelectAPIKey("private")
	if err == nil {
		t.Fatal("expected error when all keys disabled")
	}
}

func TestHB8_Provider_SelectAPIKey_QuotaExceeded(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-test", Enabled: true, AccessControl: "private", Priority: 1, Quota: 100, Used: 200},
		},
	}
	_, err := p.SelectAPIKey("private")
	if err == nil {
		t.Fatal("expected error when quota exceeded")
	}
}

func TestHB8_Provider_SelectAPIKey_ExpiredKey(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-test", Enabled: true, AccessControl: "private", Priority: 1, ExpiresAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339)},
		},
	}
	_, err := p.SelectAPIKey("private")
	if err == nil {
		t.Fatal("expected error when key expired")
	}
}

func TestHB8_Provider_SelectAPIKey_ValidKey(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-test", Enabled: true, AccessControl: "private", Priority: 1},
		},
	}
	key, err := p.SelectAPIKey("private")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.ID != "k1" {
		t.Fatalf("expected k1, got %s", key.ID)
	}
}

func TestHB8_Provider_SelectAPIKey_PriorityOrder(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-low", Enabled: true, AccessControl: "private", Priority: 1},
			{ID: "k2", Key: "sk-high", Enabled: true, AccessControl: "private", Priority: 10},
		},
	}
	key, err := p.SelectAPIKey("private")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key.Priority != 10 {
		t.Fatalf("expected highest priority key, got priority %d", key.Priority)
	}
}

func TestHB8_Provider_GetEffectiveAPIKey_WithKeys(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-effective", Enabled: true, AccessControl: "private", Priority: 1},
		},
	}
	key := p.GetEffectiveAPIKey()
	if key != "sk-effective" {
		t.Fatalf("expected sk-effective, got %s", key)
	}
}

func TestHB8_Provider_GetEffectiveAPIKey_Fallback(t *testing.T) {
	p := Provider{APIKey: "sk-fallback"}
	key := p.GetEffectiveAPIKey()
	if key != "sk-fallback" {
		t.Fatalf("expected sk-fallback, got %s", key)
	}
}

func TestHB8_ProviderManager_ResetKeyQuota_NotFound(t *testing.T) {
	setupTestEnv(t)
	err := pm.ResetKeyQuota("nonexistent", "k1")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestHB8_ProviderManager_ResetKeyQuota_KeyNotFound(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-test", Used: 100}}})
	err := pm.ResetKeyQuota("p1", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestHB8_ProviderManager_ResetKeyQuota_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-test", Used: 100}}})
	err := pm.ResetKeyQuota("p1", "k1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHB8_ProviderManager_DeleteAPIKey_ProviderNotFound(t *testing.T) {
	setupTestEnv(t)
	err := pm.DeleteAPIKey("nonexistent", "k1")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestHB8_ProviderManager_DeleteAPIKey_KeyNotFound(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-test"}}})
	err := pm.DeleteAPIKey("p1", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestHB8_ProviderManager_DeleteAPIKey_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKeys: []APIKeyConfig{
		{ID: "k1", Key: "sk-test1"},
		{ID: "k2", Key: "sk-test2"},
	}})
	err := pm.DeleteAPIKey("p1", "k1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHB8_ProviderManager_GetAPIKeys_NotFound(t *testing.T) {
	setupTestEnv(t)
	_, err := pm.GetAPIKeys("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestHB8_ProviderManager_GetAPIKeys_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKeys: []APIKeyConfig{
		{ID: "k1", Key: "sk-test1234567890"},
	}})
	keys, err := pm.GetAPIKeys("p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
}

func TestHB8_ProviderManager_UpdateAPIKey_ProviderNotFound(t *testing.T) {
	setupTestEnv(t)
	err := pm.UpdateAPIKey("nonexistent", "k1", map[string]any{"alias": "test"})
	if err == nil {
		t.Fatal("expected error for nonexistent provider")
	}
}

func TestHB8_ProviderManager_UpdateAPIKey_KeyNotFound(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-test"}}})
	err := pm.UpdateAPIKey("p1", "nonexistent", map[string]any{"alias": "test"})
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestHB8_ProviderManager_UpdateAPIKey_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-test", Priority: 1}}})
	err := pm.UpdateAPIKey("p1", "k1", map[string]any{"priority": float64(5), "alias": "updated"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHB8_ProviderManager_RecordKeyUsage_NotFound(t *testing.T) {
	setupTestEnv(t)
	pm.RecordKeyUsage("nonexistent", "k1", 100)
}

func TestHB8_ProviderManager_RecordKeyUsage_Success(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-test", Used: 0}}})
	pm.RecordKeyUsage("p1", "k1", 50)
}

func TestHB8_ProviderManager_ClearAllAPIKeys(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", APIKey: "sk-test1"})
	pm.Add(Provider{ID: "p2", Name: "P2", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-test2"}}})
	count := pm.ClearAllAPIKeys()
	if count != 2 {
		t.Fatalf("expected 2 cleared, got %d", count)
	}
}

func TestHB8_KeyAllowedForAccess_Public(t *testing.T) {
	if !keyAllowedForAccess("public", "private") {
		t.Fatal("public key should be allowed for any access type")
	}
}

func TestHB8_KeyAllowedForAccess_Shared(t *testing.T) {
	if !keyAllowedForAccess("shared", "shared") {
		t.Fatal("shared key should be allowed for shared access")
	}
	if !keyAllowedForAccess("shared", "private") {
		t.Fatal("shared key should be allowed for private access")
	}
	if keyAllowedForAccess("shared", "guest") {
		t.Fatal("shared key should not be allowed for guest access")
	}
}

func TestHB8_KeyAllowedForAccess_Private(t *testing.T) {
	if !keyAllowedForAccess("private", "private") {
		t.Fatal("private key should be allowed for private access")
	}
	if keyAllowedForAccess("private", "shared") {
		t.Fatal("private key should not be allowed for shared access")
	}
}

func TestHB8_KeyAllowedForAccess_Unknown(t *testing.T) {
	if keyAllowedForAccess("unknown", "private") {
		t.Fatal("unknown access should be denied")
	}
}

func TestHB8_FilterByAccessControl_Admin(t *testing.T) {
	cands := []candidate{
		{Provider: Provider{ID: "p1"}, Model: "m1"},
	}
	result := FilterByAccessControl(cands, "admin")
	if len(result) != 1 {
		t.Fatal("admin should see all candidates")
	}
}

func TestHB8_FilterByAccessControl_Proxy(t *testing.T) {
	cands := []candidate{
		{Provider: Provider{ID: "p1"}, Model: "m1"},
	}
	result := FilterByAccessControl(cands, "proxy")
	if len(result) != 1 {
		t.Fatal("proxy should see all candidates")
	}
}

func TestHB8_FilterByAccessControl_Guest(t *testing.T) {
	cands := []candidate{
		{Provider: Provider{ID: "p1", APIKey: "sk-shared", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-s", Enabled: true, AccessControl: "shared"}}}, Model: "m1"},
		{Provider: Provider{ID: "p2", APIKey: "sk-private", APIKeys: []APIKeyConfig{{ID: "k2", Key: "sk-p", Enabled: true, AccessControl: "private"}}}, Model: "m2"},
	}
	result := FilterByAccessControl(cands, "guest")
	if len(result) != 1 {
		t.Fatalf("guest should see providers with non-private keys, got %d", len(result))
	}
}

// ============================================================
// Global Pool tests
// ============================================================

func TestHB8_GlobalPool_JoinPool_EmptyNodeID(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	err := gp.JoinPool("", "us", 10000)
	if err == nil {
		t.Fatal("expected error for empty node ID")
	}
}

func TestHB8_GlobalPool_JoinPool_BelowMin(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	err := gp.JoinPool("node-1", "us", 100)
	if err == nil {
		t.Fatal("expected error for below minimum contribution")
	}
}

func TestHB8_GlobalPool_JoinPool_Success(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  []GlobalPoolNode{},
	}
	err := gp.JoinPool("node-1", "us", 50000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gp.TotalContributed != 50000 {
		t.Fatalf("expected 50000 total, got %d", gp.TotalContributed)
	}
}

func TestHB8_GlobalPool_JoinPool_UpdateExisting(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  []GlobalPoolNode{},
	}
	gp.JoinPool("node-1", "us", 50000)
	gp.JoinPool("node-1", "us", 30000)
	if gp.NodeContributions["node-1"] != 80000 {
		t.Fatalf("expected 80000, got %d", gp.NodeContributions["node-1"])
	}
}

func TestHB8_GlobalPool_Contribute_ZeroAmount(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  []GlobalPoolNode{{NodeID: "n1"}},
	}
	err := gp.Contribute("n1", 0)
	if err == nil {
		t.Fatal("expected error for zero contribution")
	}
}

func TestHB8_GlobalPool_Contribute_NonParticipant(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	err := gp.Contribute("unknown", 1000)
	if err == nil {
		t.Fatal("expected error for non-participant")
	}
}

func TestHB8_GlobalPool_Contribute_Success(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  []GlobalPoolNode{{NodeID: "n1"}},
	}
	err := gp.Contribute("n1", 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gp.NodeContributions["n1"] != 1000 {
		t.Fatalf("expected 1000, got %d", gp.NodeContributions["n1"])
	}
}

func TestHB8_GlobalPool_RecordConsumption(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  []GlobalPoolNode{{NodeID: "n1"}},
	}
	gp.RecordConsumption("n1", 500)
	if gp.NodeConsumptions["n1"] != 500 {
		t.Fatalf("expected 500, got %d", gp.NodeConsumptions["n1"])
	}
	if gp.TotalConsumed != 500 {
		t.Fatalf("expected total consumed 500, got %d", gp.TotalConsumed)
	}
}

func TestHB8_GlobalPool_GetStatus(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes: []GlobalPoolNode{
			{NodeID: "n1", Status: "active"},
		},
		TotalContributed: 10000,
		AvailableQuota:   10000,
	}
	status := gp.GetStatus()
	if status["participant_count"] != 1 {
		t.Fatalf("expected 1 participant, got %v", status["participant_count"])
	}
}

func TestHB8_GlobalPool_GetNodes(t *testing.T) {
	gp := &GlobalPool{
		ParticipantNodes: []GlobalPoolNode{{NodeID: "n1"}, {NodeID: "n2"}},
	}
	nodes := gp.GetNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestHB8_GlobalPool_Heartbeat(t *testing.T) {
	gp := &GlobalPool{
		ParticipantNodes: []GlobalPoolNode{{NodeID: "n1", LastHeartbeat: time.Now().Add(-10 * time.Minute)}},
	}
	gp.Heartbeat("n1")
	if gp.ParticipantNodes[0].Status != "active" {
		t.Fatal("expected active status after heartbeat")
	}
}

func TestHB8_GlobalPool_SelectBestNode_NoActive(t *testing.T) {
	gp := &GlobalPool{
		ParticipantNodes: []GlobalPoolNode{{NodeID: "n1", Status: "offline"}},
	}
	got := gp.SelectBestNode("")
	if got != nil {
		t.Fatal("expected nil for no active nodes")
	}
}

func TestHB8_GlobalPool_SelectBestNode_Active(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: map[string]int64{"n1": 50000},
		NodeConsumptions:  map[string]int64{"n1": 100},
		TotalContributed:  50000,
		ParticipantNodes: []GlobalPoolNode{{
			NodeID:        "n1",
			Status:        "active",
			Ratio:         5.0,
			LastHeartbeat: time.Now(),
			Reputation:    0.8,
		}},
	}
	got := gp.SelectBestNode("")
	if got == nil || got.NodeID != "n1" {
		t.Fatal("expected n1 as best node")
	}
}

func TestHB8_GlobalPool_utilizationLocked_Zero(t *testing.T) {
	gp := &GlobalPool{}
	if gp.utilizationLocked() != 0 {
		t.Fatal("expected 0 utilization with no contributions")
	}
}

func TestHB8_GlobalPool_utilizationLocked_NonZero(t *testing.T) {
	gp := &GlobalPool{TotalContributed: 100, TotalConsumed: 50}
	u := gp.utilizationLocked()
	if u != 0.5 {
		t.Fatalf("expected 0.5, got %f", u)
	}
}

func TestHB8_GlobalPool_GetStats(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: map[string]int64{"n1": 50000},
		NodeConsumptions:  map[string]int64{"n1": 1000},
		ParticipantNodes: []GlobalPoolNode{{
			NodeID: "n1", Region: "us", Status: "active", Ratio: 5.0,
		}},
		TotalContributed: 50000,
		TotalConsumed:    1000,
	}
	stats := gp.GetStats()
	if stats["total_participants"] != 1 {
		t.Fatalf("expected 1 participant, got %v", stats["total_participants"])
	}
}

// ============================================================
// Global Pool handler tests
// ============================================================

func TestHB8_HandleGlobalPoolStatus_Nil(t *testing.T) {
	orig := globalPool
	globalPool = nil
	defer func() { globalPool = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/network/global-pool", nil)
	handleGlobalPoolStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleGlobalPoolJoin_Nil(t *testing.T) {
	orig := globalPool
	globalPool = nil
	defer func() { globalPool = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/global-pool/join", strings.NewReader(`{}`))
	handleGlobalPoolJoin(w, r)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB8_HandleGlobalPoolJoin_InvalidBody(t *testing.T) {
	orig := globalPool
	globalPool = &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  []GlobalPoolNode{},
	}
	defer func() { globalPool = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/global-pool/join", strings.NewReader("bad"))
	handleGlobalPoolJoin(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleGlobalPoolContribute_Nil(t *testing.T) {
	orig := globalPool
	globalPool = nil
	defer func() { globalPool = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/network/global-pool/contribute", strings.NewReader(`{}`))
	handleGlobalPoolContribute(w, r)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB8_HandleGlobalPoolNodes_Nil(t *testing.T) {
	orig := globalPool
	globalPool = nil
	defer func() { globalPool = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/network/global-pool/nodes", nil)
	handleGlobalPoolNodes(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_HandleGlobalPoolStats_Nil(t *testing.T) {
	orig := globalPool
	globalPool = nil
	defer func() { globalPool = orig }()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/network/global-pool/stats", nil)
	handleGlobalPoolStats(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ============================================================
// Update Manager tests
// ============================================================

func TestHB8_IsInFlightPhase_Idle(t *testing.T) {
	if isInFlightPhase(PhaseIdle) {
		t.Fatal("idle should not be in-flight")
	}
}

func TestHB8_IsInFlightPhase_Downloading(t *testing.T) {
	if !isInFlightPhase(PhaseDownloading) {
		t.Fatal("downloading should be in-flight")
	}
}

func TestHB8_IsInFlightPhase_Replacing(t *testing.T) {
	if !isInFlightPhase(PhaseReplacing) {
		t.Fatal("replacing should be in-flight")
	}
}

func TestHB8_IsInFlightPhase_Restarting(t *testing.T) {
	if !isInFlightPhase(PhaseRestarting) {
		t.Fatal("restarting should be in-flight")
	}
}

func TestHB8_IsInFlightPhase_Success(t *testing.T) {
	if isInFlightPhase(PhaseSuccess) {
		t.Fatal("success should not be in-flight")
	}
}

func TestHB8_IsInFlightPhase_Failed(t *testing.T) {
	if isInFlightPhase(PhaseFailed) {
		t.Fatal("failed should not be in-flight")
	}
}

func TestHB8_CompareVersion_Equal(t *testing.T) {
	if compareVersion("1.0.0", "1.0.0") != 0 {
		t.Fatal("1.0.0 should equal 1.0.0")
	}
}

func TestHB8_CompareVersion_Greater(t *testing.T) {
	if compareVersion("2.0.0", "1.0.0") != 1 {
		t.Fatal("2.0.0 should be greater than 1.0.0")
	}
}

func TestHB8_CompareVersion_Lesser(t *testing.T) {
	if compareVersion("1.0.0", "2.0.0") != -1 {
		t.Fatal("1.0.0 should be less than 2.0.0")
	}
}

func TestHB8_CompareVersion_WithVPrefix(t *testing.T) {
	if compareVersion("v1.0.0", "1.0.0") != 0 {
		t.Fatal("v prefix should be ignored")
	}
}

func TestHB8_CompareVersion_ShortPadded(t *testing.T) {
	if compareVersion("4.1", "4.1.0") != 0 {
		t.Fatal("4.1 should equal 4.1.0")
	}
}

func TestHB8_UpdateManager_ListStatuses(t *testing.T) {
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", Phase: PhaseIdle},
		peers: map[string]UpdateStatus{
			"peer-1": {Env: "peer-1", Phase: PhaseDownloading},
		},
	}
	statuses := um.ListStatuses()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
}

func TestHB8_UpdateManager_setLocalFailed(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local:  UpdateStatus{Env: "local", Phase: PhaseDownloading},
		peers:  make(map[string]UpdateStatus),
		dataDir: dir,
	}
	um.setLocalFailed("test error")
	if um.local.Phase != PhaseFailed {
		t.Fatalf("expected failed phase, got %s", um.local.Phase)
	}
	if um.local.Error != "test error" {
		t.Fatalf("expected 'test error', got %s", um.local.Error)
	}
}

func TestHB8_UpdateManager_setLocalTarget(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local:  UpdateStatus{Env: "local"},
		peers:  make(map[string]UpdateStatus),
		dataDir: dir,
	}
	um.setLocalTarget("v5.0.0")
	if um.local.TargetVersion != "v5.0.0" {
		t.Fatalf("expected v5.0.0, got %s", um.local.TargetVersion)
	}
}

func TestHB8_UpdateManager_upsertPeer(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local:  UpdateStatus{Env: "local"},
		peers:  make(map[string]UpdateStatus),
		dataDir: dir,
	}
	um.upsertPeer("peer-1", func(s *UpdateStatus) {
		s.Phase = PhaseSuccess
		s.Progress = 100
	})
	if um.peers["peer-1"].Phase != PhaseSuccess {
		t.Fatalf("expected success, got %s", um.peers["peer-1"].Phase)
	}
}

// ============================================================
// Network disclaimer tests
// ============================================================

func TestHB8_GetDisclaimer(t *testing.T) {
	d := GetDisclaimer()
	if d.Title == "" {
		t.Fatal("expected non-empty disclaimer title")
	}
	if d.ConfirmationText == "" {
		t.Fatal("expected non-empty confirmation text")
	}
}

// ============================================================
// PublicKeyQuota tests
// ============================================================

func TestHB8_PublicKeyQuota_CheckQuota_AllowWithinLimit(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{"gpt-4o": 500},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        make(map[string]int64),
		lastDailyReset:    time.Now(),
		lastHourlyReset:   time.Now(),
	}
	ok, reason, _ := q.CheckQuota("1.2.3.4", "gpt-4o", 100)
	if !ok {
		t.Fatalf("expected ok, got reason: %s", reason)
	}
}

func TestHB8_PublicKeyQuota_CheckQuota_GlobalExceeded(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        make(map[string]int64),
		lastDailyReset:    time.Now(),
		lastHourlyReset:   time.Now(),
		globalUsedToday:   99,
	}
	ok, _, _ := q.CheckQuota("1.2.3.4", "", 10)
	if ok {
		t.Fatal("should reject when global quota would be exceeded")
	}
}

func TestHB8_PublicKeyQuota_CheckQuota_IPExceeded(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      50,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{},
		ipUsage: map[string]*IPUsageTracker{
			"1.2.3.4": {DailyUsed: 45, LastReset: time.Now()},
		},
		hourlyUsage:     make(map[string]int64),
		modelUsage:      make(map[string]int64),
		lastDailyReset:  time.Now(),
		lastHourlyReset: time.Now(),
	}
	ok, reason, _ := q.CheckQuota("1.2.3.4", "", 10)
	if ok {
		t.Fatalf("should reject when IP quota exceeded, reason: %s", reason)
	}
}

func TestHB8_PublicKeyQuota_CheckQuota_HourlyExceeded(t *testing.T) {
	hourKey := time.Now().Format("2006-01-02-15")
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 50,
		ModelLimits:       map[string]int64{},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       map[string]int64{hourKey: 45},
		modelUsage:        make(map[string]int64),
		lastDailyReset:    time.Now(),
		lastHourlyReset:   time.Now(),
	}
	ok, _, _ := q.CheckQuota("1.2.3.4", "", 10)
	if ok {
		t.Fatal("should reject when hourly quota exceeded")
	}
}

func TestHB8_PublicKeyQuota_CheckQuota_ModelExceeded(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{"gpt-4o": 50},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        map[string]int64{"gpt-4o": 45},
		lastDailyReset:    time.Now(),
		lastHourlyReset:   time.Now(),
	}
	ok, _, _ := q.CheckQuota("1.2.3.4", "gpt-4o", 10)
	if ok {
		t.Fatal("should reject when model quota exceeded")
	}
}

func TestHB8_PublicKeyQuota_ReserveQuota_Success(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        make(map[string]int64),
		lastDailyReset:    time.Now(),
		lastHourlyReset:   time.Now(),
	}
	ok, _, _ := q.ReserveQuota("1.2.3.4", "", 100)
	if !ok {
		t.Fatal("should allow within limit")
	}
	if q.globalUsedToday != 100 {
		t.Fatalf("expected globalUsedToday=100, got %d", q.globalUsedToday)
	}
}

func TestHB8_PublicKeyQuota_AdjustQuota(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{},
		ipUsage: map[string]*IPUsageTracker{
			"1.2.3.4": {DailyUsed: 100, HourlyUsed: 100, LastReset: time.Now()},
		},
		hourlyUsage:     map[string]int64{time.Now().Format("2006-01-02-15"): 100},
		modelUsage:      make(map[string]int64),
		lastDailyReset:  time.Now(),
		lastHourlyReset: time.Now(),
		globalUsedToday: 100,
	}
	q.AdjustQuota("1.2.3.4", "", 100, 80)
	if q.globalUsedToday != 80 {
		t.Fatalf("expected globalUsedToday=80 after adjust, got %d", q.globalUsedToday)
	}
}

func TestHB8_PublicKeyQuota_RecordUsage(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        make(map[string]int64),
		lastDailyReset:    time.Now(),
		lastHourlyReset:   time.Now(),
	}
	q.RecordUsage("1.2.3.4", "gpt-4o", 100)
	if q.globalUsedToday != 100 {
		t.Fatalf("expected 100, got %d", q.globalUsedToday)
	}
}

func TestHB8_PublicKeyQuota_GetQuotaStatus(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      10000,
		HourlyWindowLimit: 1000,
		ModelLimits:       map[string]int64{},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        make(map[string]int64),
		lastDailyReset:    time.Now(),
		lastHourlyReset:   time.Now(),
	}
	status := q.GetQuotaStatus()
	if status == nil {
		t.Fatal("expected non-nil status")
	}
}

// ============================================================
// Additional admin handler tests
// ============================================================

func TestHB8_HandleForgotPassword_NotInitialized(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/forgot-password", strings.NewReader(`{"email":"test@test.com"}`))
	handleForgotPassword(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleResetPassword_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/reset-password", strings.NewReader("bad"))
	handleResetPassword(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleVerifyResetToken_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/verify-reset-token", strings.NewReader("bad"))
	handleVerifyResetToken(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB8_HandleVerifyAuth_NoToken(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/verify-auth", nil)
	handleVerifyAuth(w, r)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHB8_HandleStatus(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	handleStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB8_MapKeys(t *testing.T) {
	m := map[string]string{"a": "1", "b": "2"}
	keys := mapKeys(m)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

func TestHB8_HandleGetProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/providers/nonexistent", nil)
	r.SetPathValue("id", "nonexistent")
	handleGetProvider(w, r)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB8_HandleDeleteProvider_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/providers/nonexistent", nil)
	r.SetPathValue("id", "nonexistent")
	handleDeleteProvider(w, r)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB8_HandleGetProviderAccessControl_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/providers/nonexistent/access-control", nil)
	r.SetPathValue("id", "nonexistent")
	handleGetProviderAccessControl(w, r)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB8_HandleUpdateProviderAccessControl_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/providers/nonexistent/access-control", strings.NewReader(`{"share_to_pool":true}`))
	r.SetPathValue("id", "nonexistent")
	handleUpdateProviderAccessControl(w, r)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB8_HandleSyncModels_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/providers/nonexistent/sync-models", nil)
	r.SetPathValue("id", "nonexistent")
	handleSyncModels(w, r)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHB8_HandleGetProviderModels_NotFound(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/providers/nonexistent/models", nil)
	r.SetPathValue("id", "nonexistent")
	handleGetProviderModels(w, r)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ============================================================
// resolvePublicEndpoint tests
// ============================================================

func TestHB8_ResolvePublicEndpoint_FederationEndpoint(t *testing.T) {
	e := setupTestEnv(t)
	e.cfgInst.Set("federation_endpoint", "https://my-endpoint.com")
	result := resolvePublicEndpoint("")
	if result != "https://my-endpoint.com" {
		t.Fatalf("expected federation endpoint, got %s", result)
	}
}

func TestHB8_ResolvePublicEndpoint_PublicDomain(t *testing.T) {
	e := setupTestEnv(t)
	e.cfgInst.Set("public_domain", "https://my-domain.com")
	result := resolvePublicEndpoint("")
	if result != "https://my-domain.com" {
		t.Fatalf("expected public domain, got %s", result)
	}
}

func TestHB8_ResolvePublicEndpoint_HostHeader(t *testing.T) {
	setupTestEnv(t)
	result := resolvePublicEndpoint("example.com:8080")
	if result != "https://example.com:8080" {
		t.Fatalf("expected https://example.com:8080, got %s", result)
	}
}

// ============================================================
// firstAddress tests
// ============================================================

func TestHB8_FirstAddress_Empty(t *testing.T) {
	if firstAddress(nil) != "" {
		t.Fatal("expected empty string for nil")
	}
	if firstAddress([]string{}) != "" {
		t.Fatal("expected empty string for empty slice")
	}
}

func TestHB8_FirstAddress_NonEmpty(t *testing.T) {
	if firstAddress([]string{"a", "b"}) != "a" {
		t.Fatal("expected first element")
	}
}

// ============================================================
// NetworkManager RecordConsent
// ============================================================

func TestHB8_NetworkManager_RecordConsent(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{},
		dataPath: dir + "/network.json",
	}
	err := nm.RecordConsent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nm.config.ConsentAccepted {
		t.Fatal("expected consent accepted")
	}
}
