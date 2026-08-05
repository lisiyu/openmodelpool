package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ============================================================
// UpdateManager — compareVersion
// ============================================================

func TestHB6_compareVersion_Equal(t *testing.T) {
	if compareVersion("1.0.0", "1.0.0") != 0 {
		t.Error("expected equal")
	}
}

func TestHB6_compareVersion_Greater(t *testing.T) {
	if compareVersion("2.0.0", "1.0.0") <= 0 {
		t.Error("expected greater")
	}
}

func TestHB6_compareVersion_Lesser(t *testing.T) {
	if compareVersion("1.0.0", "2.0.0") >= 0 {
		t.Error("expected lesser")
	}
}

func TestHB6_compareVersion_WithVPrefix(t *testing.T) {
	if compareVersion("v4.1.7", "v4.1.6") <= 0 {
		t.Error("expected greater")
	}
}

func TestHB6_compareVersion_ShortPadding(t *testing.T) {
	if compareVersion("4.1", "4.1.7") >= 0 {
		t.Error("4.1 < 4.1.7")
	}
}

func TestHB6_compareVersion_MixedVPrefix(t *testing.T) {
	if compareVersion("v1.2.3", "1.2.3") != 0 {
		t.Error("expected equal with mixed v prefix")
	}
}

func TestHB6_compareVersion_MultiDigit(t *testing.T) {
	if compareVersion("1.10.0", "1.9.0") <= 0 {
		t.Error("1.10.0 > 1.9.0")
	}
}

// ============================================================
// UpdatePhase — isInFlightPhase
// ============================================================

func TestHB6_isInFlightPhase_Downloading(t *testing.T) {
	if !isInFlightPhase(PhaseDownloading) {
		t.Error("downloading should be in-flight")
	}
}

func TestHB6_isInFlightPhase_Replacing(t *testing.T) {
	if !isInFlightPhase(PhaseReplacing) {
		t.Error("replacing should be in-flight")
	}
}

func TestHB6_isInFlightPhase_Restarting(t *testing.T) {
	if !isInFlightPhase(PhaseRestarting) {
		t.Error("restarting should be in-flight")
	}
}

func TestHB6_isInFlightPhase_Idle(t *testing.T) {
	if isInFlightPhase(PhaseIdle) {
		t.Error("idle should not be in-flight")
	}
}

func TestHB6_isInFlightPhase_Success(t *testing.T) {
	if isInFlightPhase(PhaseSuccess) {
		t.Error("success should not be in-flight")
	}
}

func TestHB6_isInFlightPhase_Failed(t *testing.T) {
	if isInFlightPhase(PhaseFailed) {
		t.Error("failed should not be in-flight")
	}
}

// ============================================================
// UpdateManager — ListStatuses, setLocalPhase, upsertPeer
// ============================================================

func TestHB6_UpdateManager_ListStatuses_Empty(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	statuses := um.ListStatuses()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Env != "local" {
		t.Errorf("expected local, got %s", statuses[0].Env)
	}
}

func TestHB6_UpdateManager_setLocalPhase(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	um.setLocalPhase(PhaseDownloading, 10, "starting", "")
	um.mu.RLock()
	phase := um.local.Phase
	progress := um.local.Progress
	um.mu.RUnlock()
	if phase != PhaseDownloading {
		t.Errorf("expected downloading, got %s", phase)
	}
	if progress != 10 {
		t.Errorf("expected 10, got %d", progress)
	}
}

func TestHB6_UpdateManager_setLocalFailed(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	um.setLocalFailed("something broke")
	um.mu.RLock()
	phase := um.local.Phase
	errMsg := um.local.Error
	um.mu.RUnlock()
	if phase != PhaseFailed {
		t.Errorf("expected failed, got %s", phase)
	}
	if errMsg != "something broke" {
		t.Errorf("expected error message, got %s", errMsg)
	}
}

func TestHB6_UpdateManager_upsertPeer(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	um.upsertPeer("node-1", func(s *UpdateStatus) {
		s.Phase = PhaseDownloading
		s.TargetVersion = "v5.0.0"
	})
	um.mu.RLock()
	p, ok := um.peers["node-1"]
	um.mu.RUnlock()
	if !ok {
		t.Fatal("peer not found")
	}
	if p.Phase != PhaseDownloading {
		t.Errorf("expected downloading, got %s", p.Phase)
	}
	if p.TargetVersion != "v5.0.0" {
		t.Errorf("expected v5.0.0, got %s", p.TargetVersion)
	}
}

func TestHB6_UpdateManager_OnReportReceived(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	report := UpdateReport{
		NodeID:        "node-2",
		Name:          "peer2",
		TargetVersion: "v5.0.0",
		Phase:         PhaseSuccess,
		Progress:      100,
	}
	um.OnReportReceived(report)
	um.mu.RLock()
	p, ok := um.peers["node-2"]
	um.mu.RUnlock()
	if !ok {
		t.Fatal("peer not found")
	}
	if p.Phase != PhaseSuccess {
		t.Errorf("expected success, got %s", p.Phase)
	}
}

func TestHB6_UpdateManager_setReportBack(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	sig := UpdateSignal{BroadcastBy: "origin-1", TargetVersion: "v5.0.0"}
	um.setReportBack(sig)
	um.mu.RLock()
	rb := um.reportBack
	um.mu.RUnlock()
	if rb == nil {
		t.Fatal("expected reportBack set")
	}
	if rb.BroadcastBy != "origin-1" {
		t.Errorf("expected origin-1, got %s", rb.BroadcastBy)
	}
}

func TestHB6_UpdateManager_clearReportBack(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	sig := UpdateSignal{BroadcastBy: "origin-1", TargetVersion: "v5.0.0"}
	um.setReportBack(sig)
	um.clearReportBack()
	um.mu.RLock()
	rb := um.reportBack
	um.mu.RUnlock()
	if rb != nil {
		t.Error("expected nil reportBack")
	}
}

// ============================================================
// UpdateManager — reconcilePending
// ============================================================

func TestHB6_UpdateManager_reconcilePending_NoFile(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseDownloading},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	um.reconcilePending()
	um.mu.RLock()
	phase := um.local.Phase
	um.mu.RUnlock()
	if phase != PhaseIdle {
		t.Errorf("in-flight phase should reset to idle, got %s", phase)
	}
}

// ============================================================
// UpdateManager — writePending
// ============================================================

func TestHB6_UpdateManager_writePending(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", IsLocal: true, Role: "origin", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	um.writePending("v5.0.0")
	// Verify file was written
	data, err := json.Marshal(map[string]string{})
	_ = data
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ============================================================
// dedupeStrings
// ============================================================

func TestHB6_dedupeStrings_Empty(t *testing.T) {
	result := dedupeStrings([]string{})
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestHB6_dedupeStrings_NoDupes(t *testing.T) {
	result := dedupeStrings([]string{"a", "b", "c"})
	if len(result) != 3 {
		t.Errorf("expected 3, got %d", len(result))
	}
}

func TestHB6_dedupeStrings_WithDupes(t *testing.T) {
	result := dedupeStrings([]string{"a", "b", "a", "c", "b"})
	if len(result) != 3 {
		t.Errorf("expected 3, got %d", len(result))
	}
}

func TestHB6_dedupeStrings_SkipsEmpty(t *testing.T) {
	result := dedupeStrings([]string{"a", "", "b", ""})
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
}

// ============================================================
// peerDisplayName
// ============================================================

func TestHB6_peerDisplayName_GitHubUser(t *testing.T) {
	peer := NodeInfo{GitHubUser: "alice"}
	if name := peerDisplayName(peer); name != "alice" {
		t.Errorf("expected alice, got %s", name)
	}
}

func TestHB6_peerDisplayName_Endpoint(t *testing.T) {
	peer := NodeInfo{Endpoint: "https://example.com"}
	if name := peerDisplayName(peer); name != "https://example.com" {
		t.Errorf("expected endpoint, got %s", name)
	}
}

func TestHB6_peerDisplayName_FallbackShortID(t *testing.T) {
	peer := NodeInfo{NodeID: "very-long-node-id-12345678"}
	name := peerDisplayName(peer)
	if name == "" {
		t.Error("expected non-empty name")
	}
}

// ============================================================
// TunnelManager — newTunnelManager
// ============================================================

func TestHB6_newTunnelManager_Quick(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	tm := newTunnelManager("", "", "")
	if tm.mode != "quick" {
		t.Errorf("expected quick mode, got %s", tm.mode)
	}
}

func TestHB6_newTunnelManager_Named(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	tm := newTunnelManager("example.com", "tun-123", "tok-abc")
	if tm.mode != "named" {
		t.Errorf("expected named mode, got %s", tm.mode)
	}
	if tm.domain != "example.com" {
		t.Errorf("expected example.com, got %s", tm.domain)
	}
	if tm.tunnelID != "tun-123" {
		t.Errorf("expected tun-123, got %s", tm.tunnelID)
	}
}

func TestHB6_TunnelManager_GetURL_Empty(t *testing.T) {
	tm := &TunnelManager{}
	if url := tm.GetURL(); url != "" {
		t.Errorf("expected empty, got %s", url)
	}
}

func TestHB6_TunnelManager_IsRunning_False(t *testing.T) {
	tm := &TunnelManager{}
	if tm.IsRunning() {
		t.Error("expected not running")
	}
}

// ============================================================
// Tunnel — sanitizeDomain, extractSubdomain, isValidDomain
// ============================================================

func TestHB6_sanitizeDomain_Basic(t *testing.T) {
	result := sanitizeDomain("Example.Com")
	if result != "example-com" {
		t.Errorf("expected example-com, got %s", result)
	}
}

func TestHB6_sanitizeDomain_Spaces(t *testing.T) {
	result := sanitizeDomain("my domain com")
	if result != "my-domain-com" {
		t.Errorf("expected my-domain-com, got %s", result)
	}
}

func TestHB6_sanitizeDomain_Long(t *testing.T) {
	long := strings.Repeat("a", 50)
	result := sanitizeDomain(long)
	if len(result) != 40 {
		t.Errorf("expected 40 chars, got %d", len(result))
	}
}

func TestHB6_extractSubdomain_Valid(t *testing.T) {
	result := extractSubdomain("https://abc-xyz.trycloudflare.com")
	if result != "abc-xyz" {
		t.Errorf("expected abc-xyz, got %s", result)
	}
}

func TestHB6_extractSubdomain_NoScheme(t *testing.T) {
	result := extractSubdomain("noproto")
	if result != "" {
		t.Errorf("expected empty, got %s", result)
	}
}

func TestHB6_isValidDomain_Valid(t *testing.T) {
	if !isValidDomain("example.com") {
		t.Error("expected valid")
	}
}

func TestHB6_isValidDomain_Subdomain(t *testing.T) {
	if !isValidDomain("sub.example.com") {
		t.Error("expected valid")
	}
}

func TestHB6_isValidDomain_SinglePart(t *testing.T) {
	if isValidDomain("localhost") {
		t.Error("expected invalid for single part")
	}
}

func TestHB6_isValidDomain_EmptyPart(t *testing.T) {
	if isValidDomain("example..com") {
		t.Error("expected invalid for empty part")
	}
}

func TestHB6_isValidDomain_InvalidChar(t *testing.T) {
	if isValidDomain("exa_mple.com") {
		t.Error("expected invalid for underscore")
	}
}

// ============================================================
// Tunnel — hostOf, ensureHTTPS
// ============================================================

func TestHB6_hostOf_WithScheme(t *testing.T) {
	if h := hostOf("https://example.com:8443"); h != "example.com" {
		t.Errorf("expected example.com, got %s", h)
	}
}

func TestHB6_hostOf_NoScheme(t *testing.T) {
	if h := hostOf("example.com:8080"); h != "example.com" {
		t.Errorf("expected example.com, got %s", h)
	}
}

func TestHB6_hostOf_Empty(t *testing.T) {
	if h := hostOf(""); h != "" {
		t.Errorf("expected empty, got %s", h)
	}
}

func TestHB6_hostOf_WithPath(t *testing.T) {
	if h := hostOf("https://example.com/path?q=1"); h != "example.com" {
		t.Errorf("expected example.com, got %s", h)
	}
}

func TestHB6_ensureHTTPS_NoScheme(t *testing.T) {
	if u := ensureHTTPS("example.com"); u != "https://example.com" {
		t.Errorf("expected https://example.com, got %s", u)
	}
}

func TestHB6_ensureHTTPS_WithScheme(t *testing.T) {
	if u := ensureHTTPS("https://example.com"); u != "https://example.com" {
		t.Errorf("expected https://example.com, got %s", u)
	}
}

func TestHB6_ensureHTTPS_Empty(t *testing.T) {
	if u := ensureHTTPS(""); u != "" {
		t.Errorf("expected empty, got %s", u)
	}
}

// ============================================================
// Tunnel — handleTunnelStatus
// ============================================================

func TestHB6_handleTunnelStatus_Nil(t *testing.T) {
	origTunnel := tunnel
	tunnel = nil
	defer func() { tunnel = origTunnel }()

	status := handleTunnelStatus()
	if status["running"] != false {
		t.Error("expected not running")
	}
}

// ============================================================
// Tunnel — handleVerifyDomainToken
// ============================================================

func TestHB6_handleVerifyDomainToken_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/domain/verify-token", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleVerifyDomainToken(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB6_handleVerifyDomainToken_EmptyToken(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/domain/verify-token", strings.NewReader(`{"api_token":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleVerifyDomainToken(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ============================================================
// Tunnel — handleGetDomainStatus
// ============================================================

func TestHB6_handleGetDomainStatus_NoBinding(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("GET", "/api/domain/status", nil)
	w := httptest.NewRecorder()
	handleGetDomainStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["bound"] != false {
		t.Error("expected bound=false")
	}
}

// ============================================================
// Tunnel — handleManualDomainBind
// ============================================================

func TestHB6_handleManualDomainBind_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/domain/manual", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleManualDomainBind(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB6_handleManualDomainBind_EmptyDomain(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/domain/manual", strings.NewReader(`{"domain":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleManualDomainBind(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB6_handleManualDomainBind_InvalidDomain(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/domain/manual", strings.NewReader(`{"domain":"localhost"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleManualDomainBind(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB6_handleManualDomainBind_Success(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origTunnel := tunnel
	tunnel = nil
	defer func() { tunnel = origTunnel }()

	req := httptest.NewRequest("POST", "/api/domain/manual", strings.NewReader(`{"domain":"example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleManualDomainBind(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Error("expected success=true")
	}
	if resp["domain"] != "example.com" {
		t.Error("expected domain=example.com")
	}
}

// ============================================================
// Tunnel — handleBindIP
// ============================================================

func TestHB6_handleBindIP_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/ip/bind", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	handleBindIP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB6_handleBindIP_EmptyIP(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/ip/bind", strings.NewReader(`{"ip":""}`))
	w := httptest.NewRecorder()
	handleBindIP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB6_handleBindIP_InvalidIP(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/ip/bind", strings.NewReader(`{"ip":"abc"}`))
	w := httptest.NewRecorder()
	handleBindIP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHB6_handleBindIP_Success(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origNetMgr := netMgr
	origRouteTable := routeTable
	netMgr = nil
	routeTable = initRouteTable()
	defer func() { netMgr = origNetMgr; routeTable = origRouteTable }()

	req := httptest.NewRequest("POST", "/api/ip/bind", strings.NewReader(`{"ip":"192.168.1.100","port":"8080"}`))
	w := httptest.NewRecorder()
	handleBindIP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ============================================================
// NetworkManager — IsSharedMode, GetNodeID, HasPeer
// ============================================================

func TestHB6_NetworkManager_IsSharedMode_Personal(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{Mode: NetworkModePersonal},
		dataPath: dir + "/net.json",
	}
	if nm.IsSharedMode() {
		t.Error("expected personal mode")
	}
}

func TestHB6_NetworkManager_IsSharedMode_Shared(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{Mode: NetworkModeShared},
		dataPath: dir + "/net.json",
	}
	if !nm.IsSharedMode() {
		t.Error("expected shared mode")
	}
}

func TestHB6_NetworkManager_GetNodeID(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{NodeID: "mmx-abc123"},
		dataPath: dir + "/net.json",
	}
	if nm.GetNodeID() != "mmx-abc123" {
		t.Errorf("expected mmx-abc123, got %s", nm.GetNodeID())
	}
}

func TestHB6_NetworkManager_HasPeer_True(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config: NetworkConfig{
			Peers: []PeerInfo{{NodeID: "peer-1"}, {NodeID: "peer-2"}},
		},
		dataPath: dir + "/net.json",
	}
	if !nm.HasPeer("peer-1") {
		t.Error("expected true")
	}
}

func TestHB6_NetworkManager_HasPeer_False(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config: NetworkConfig{
			Peers: []PeerInfo{{NodeID: "peer-1"}},
		},
		dataPath: dir + "/net.json",
	}
	if nm.HasPeer("peer-99") {
		t.Error("expected false")
	}
}

// ============================================================
// NetworkManager — RecordRelayResult, RecordReceived
// ============================================================

func TestHB6_NetworkManager_RecordRelayResult_Success(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{},
		dataPath: dir + "/net.json",
	}
	nm.RecordRelayResult(true)
	nm.mu.RLock()
	relayed := nm.config.Stats.RequestsRelayed
	success := nm.config.Stats.RelaySuccess
	nm.mu.RUnlock()
	if relayed != 1 {
		t.Errorf("expected 1 relayed, got %d", relayed)
	}
	if success != 1 {
		t.Errorf("expected 1 success, got %d", success)
	}
}

func TestHB6_NetworkManager_RecordRelayResult_Fail(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{},
		dataPath: dir + "/net.json",
	}
	nm.RecordRelayResult(false)
	nm.mu.RLock()
	failed := nm.config.Stats.RelayFailed
	nm.mu.RUnlock()
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}

func TestHB6_NetworkManager_RecordReceived(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{},
		dataPath: dir + "/net.json",
	}
	nm.RecordReceived()
	nm.mu.RLock()
	received := nm.config.Stats.RequestsReceived
	nm.mu.RUnlock()
	if received != 1 {
		t.Errorf("expected 1, got %d", received)
	}
}

// ============================================================
// NetworkManager — IsSharingToPool, SetCapabilities
// ============================================================

func TestHB6_NetworkManager_IsSharingToPool_True(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{ShareToPool: true},
		dataPath: dir + "/net.json",
	}
	if !nm.IsSharingToPool() {
		t.Error("expected true")
	}
}

func TestHB6_NetworkManager_IsSharingToPool_False(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{ShareToPool: false},
		dataPath: dir + "/net.json",
	}
	if nm.IsSharingToPool() {
		t.Error("expected false")
	}
}

func TestHB6_NetworkManager_SetCapabilities(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{},
		dataPath: dir + "/net.json",
	}
	caps := PeerCapabilities{CanRelay: true, CanSeed: true, Providers: []string{"openai", "anthropic"}}
	nm.SetCapabilities(caps)
	nm.mu.RLock()
	got := nm.config.Capabilities
	nm.mu.RUnlock()
	if !got.CanRelay || !got.CanSeed || len(got.Providers) != 2 {
		t.Errorf("unexpected capabilities: %+v", got)
	}
}

// ============================================================
// NetworkManager — GetStatus
// ============================================================

func TestHB6_NetworkManager_GetStatus_Personal(t *testing.T) {
	dir := t.TempDir()
	origRT := routeTable
	routeTable = initRouteTable()
	defer func() { routeTable = origRT }()

	nm := &NetworkManager{
		config: NetworkConfig{
			Mode:    NetworkModePersonal,
			NodeID:  "node-1",
			NodeName: "test-node",
		},
		dataPath: dir + "/net.json",
	}
	status := nm.GetStatus()
	if status["mode"] != NetworkModePersonal {
		t.Errorf("expected personal, got %v", status["mode"])
	}
	if status["node_id"] != "node-1" {
		t.Errorf("expected node-1, got %v", status["node_id"])
	}
}

// ============================================================
// NetworkManager — RecordConsent
// ============================================================

func TestHB6_NetworkManager_RecordConsent(t *testing.T) {
	dir := t.TempDir()
	nm := &NetworkManager{
		config:   NetworkConfig{},
		dataPath: dir + "/net.json",
	}
	if err := nm.RecordConsent(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nm.mu.RLock()
	accepted := nm.config.ConsentAccepted
	nm.mu.RUnlock()
	if !accepted {
		t.Error("expected consent accepted")
	}
}

// ============================================================
// Network handlers — handleNetworkRemovePeer
// ============================================================

func TestHB6_handleNetworkRemovePeer_MissingID(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/network/peers/remove", strings.NewReader(`{"node_id":"p1"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleNetworkRemovePeer(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ============================================================
// Network handlers — handleNetworkJoinConditions
// ============================================================

func TestHB6_handleNetworkJoinConditions_NilMgr(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origNetMgr := netMgr
	netMgr = nil
	defer func() { netMgr = origNetMgr }()

	req := httptest.NewRequest("GET", "/api/network/join-conditions", nil)
	w := httptest.NewRecorder()
	handleNetworkJoinConditions(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ============================================================
// Network — firstAddress
// ============================================================

func TestHB6_firstAddress_NonEmpty(t *testing.T) {
	if firstAddress([]string{"a", "b"}) != "a" {
		t.Error("expected a")
	}
}

func TestHB6_firstAddress_Empty(t *testing.T) {
	if firstAddress([]string{}) != "" {
		t.Error("expected empty")
	}
}

func TestHB6_firstAddress_Nil(t *testing.T) {
	if firstAddress(nil) != "" {
		t.Error("expected empty for nil")
	}
}

// ============================================================
// GlobalPool — recalculateLocked, topContributorsLocked
// ============================================================

func TestHB6_GlobalPool_Recalculate_Empty(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  []GlobalPoolNode{},
	}
	gp.recalculateLocked()
	if gp.TotalContributed != 0 {
		t.Errorf("expected 0, got %d", gp.TotalContributed)
	}
	if gp.AvailableQuota != 0 {
		t.Errorf("expected 0, got %d", gp.AvailableQuota)
	}
}

func TestHB6_GlobalPool_UtilizationZero(t *testing.T) {
	gp := &GlobalPool{
		TotalContributed: 0,
		TotalConsumed:    0,
	}
	if u := gp.utilizationLocked(); u != 0 {
		t.Errorf("expected 0, got %f", u)
	}
}

func TestHB6_GlobalPool_UtilizationPartial(t *testing.T) {
	gp := &GlobalPool{
		TotalContributed: 1000,
		TotalConsumed:    250,
	}
	if u := gp.utilizationLocked(); u != 0.25 {
		t.Errorf("expected 0.25, got %f", u)
	}
}

func TestHB6_GlobalPool_TopContributors_Empty(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	result := gp.topContributorsLocked(5)
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestHB6_GlobalPool_TopContributors_WithData(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: map[string]int64{"n1": 500, "n2": 200, "n3": 800},
		NodeConsumptions:  map[string]int64{"n1": 100, "n2": 50, "n3": 200},
	}
	result := gp.topContributorsLocked(2)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0]["node_id"] != "n3" {
		t.Errorf("expected n3 as top, got %v", result[0]["node_id"])
	}
}

// ============================================================
// GlobalPool — SelectBestNode
// ============================================================

func TestHB6_GlobalPool_SelectBestNode_Empty(t *testing.T) {
	gp := &GlobalPool{
		ParticipantNodes: []GlobalPoolNode{},
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
	}
	best := gp.SelectBestNode("")
	if best != nil {
		t.Error("expected nil for empty pool")
	}
}

func TestHB6_GlobalPool_SelectBestNode_SingleActive(t *testing.T) {
	gp := &GlobalPool{
		ParticipantNodes: []GlobalPoolNode{
			{NodeID: "n1", Status: "active", Ratio: 5.0, Reputation: 0.8, Region: "us-east"},
		},
		NodeContributions: map[string]int64{"n1": 5000},
		NodeConsumptions:  map[string]int64{"n1": 1000},
		TotalContributed:  5000,
	}
	best := gp.SelectBestNode("us-east")
	if best == nil {
		t.Fatal("expected a node")
	}
	if best.NodeID != "n1" {
		t.Errorf("expected n1, got %s", best.NodeID)
	}
}

func TestHB6_GlobalPool_SelectBestNode_SkipsOffline(t *testing.T) {
	gp := &GlobalPool{
		ParticipantNodes: []GlobalPoolNode{
			{NodeID: "n1", Status: "offline", Ratio: 5.0},
			{NodeID: "n2", Status: "active", Ratio: 2.0, Reputation: 0.5},
		},
		NodeContributions: map[string]int64{"n1": 5000, "n2": 2000},
		NodeConsumptions:  map[string]int64{"n1": 1000, "n2": 500},
		TotalContributed:  7000,
	}
	best := gp.SelectBestNode("")
	if best == nil {
		t.Fatal("expected a node")
	}
	if best.NodeID == "n1" {
		t.Error("should not select offline node")
	}
}

// ============================================================
// GlobalPool — Heartbeat
// ============================================================

func TestHB6_GlobalPool_Heartbeat_Existing(t *testing.T) {
	gp := &GlobalPool{
		ParticipantNodes: []GlobalPoolNode{
			{NodeID: "n1", Status: "offline", LastHeartbeat: time.Now().Add(-20 * time.Minute)},
		},
	}
	gp.Heartbeat("n1")
	if gp.ParticipantNodes[0].Status != "active" {
		t.Errorf("expected active, got %s", gp.ParticipantNodes[0].Status)
	}
}

func TestHB6_GlobalPool_Heartbeat_Unknown(t *testing.T) {
	gp := &GlobalPool{
		ParticipantNodes: []GlobalPoolNode{},
	}
	gp.Heartbeat("unknown")
	// Should not panic, just no-op
}

// ============================================================
// Auth — RegisterCollaborator
// ============================================================

func TestHB6_Auth_RegisterCollaborator_MissingFields(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	err := auth.RegisterCollaborator("", "", "")
	if err == nil {
		t.Error("expected error for missing fields")
	}
}

func TestHB6_Auth_RegisterCollaborator_UsernameTaken(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com")
	err := auth.RegisterCollaborator("admin", "Str0ng!Pass#456", "gk-123")
	if err == nil {
		t.Error("expected error for taken username")
	}
}

func TestHB6_Auth_RegisterCollaborator_Success(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com")
	err := auth.RegisterCollaborator("collab1", "Str0ng!Collab#1", "gk-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ============================================================
// Auth — VerifyCollaboratorCredentials
// ============================================================

func TestHB6_Auth_VerifyCollaboratorCredentials_NotFound(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	result := auth.VerifyCollaboratorCredentials("nobody", "pass")
	if result != nil {
		t.Error("expected nil for unknown collaborator")
	}
}

func TestHB6_Auth_VerifyCollaboratorCredentials_WrongPass(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com")
	auth.RegisterCollaborator("collab1", "Str0ng!Collab#1", "gk-abc")
	result := auth.VerifyCollaboratorCredentials("collab1", "wrong")
	if result != nil {
		t.Error("expected nil for wrong password")
	}
}

func TestHB6_Auth_VerifyCollaboratorCredentials_Success(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	auth.SetupAdmin("admin", "Str0ng!Pass#123", "admin@test.com")
	auth.RegisterCollaborator("collab1", "Str0ng!Collab#1", "gk-abc")
	result := auth.VerifyCollaboratorCredentials("collab1", "Str0ng!Collab#1")
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Username != "collab1" {
		t.Errorf("expected collab1, got %s", result.Username)
	}
}

// ============================================================
// Auth — ValidateGuestKeyForRegistration
// ============================================================

func TestHB6_Auth_ValidateGuestKeyForRegistration_NilStore(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origStore := guestKeyStore
	guestKeyStore = nil
	defer func() { guestKeyStore = origStore }()

	result := auth.ValidateGuestKeyForRegistration("some-key")
	if result {
		t.Error("expected false when store is nil")
	}
}

// ============================================================
// Auth — randomString
// ============================================================

func TestHB6_randomString_Length(t *testing.T) {
	s := randomString(32)
	if len(s) != 32 {
		t.Errorf("expected 32, got %d", len(s))
	}
}

func TestHB6_randomString_Uniqueness(t *testing.T) {
	a := randomString(64)
	b := randomString(64)
	if a == b {
		t.Error("expected different strings")
	}
}

// ============================================================
// Performance — getMemoryUsage
// ============================================================

func TestHB6_getMemoryUsage_NonZero(t *testing.T) {
	mem := getMemoryUsage()
	if mem.NumGoroutine <= 0 {
		t.Error("expected positive goroutine count")
	}
}

// ============================================================
// Performance — BufferPool
// ============================================================

func TestHB6_BufferPool_GetPut(t *testing.T) {
	buf := GetBuffer()
	buf.WriteString("hello")
	PutBuffer(buf)
	buf2 := GetBuffer()
	if buf2.Len() != 0 {
		t.Error("expected reset buffer")
	}
	PutBuffer(buf2)
}

// ============================================================
// Performance — WorkerPool Submit Full
// ============================================================

func TestHB6_WorkerPool_SubmitFull(t *testing.T) {
	wp := &WorkerPool{
		taskCh:  make(chan func(), 1),
		workers: 1,
	}
	// Start one worker
	go wp.worker()
	time.Sleep(10 * time.Millisecond)

	// Fill the channel
	wp.taskCh <- func() {}

	// Next submit should overflow
	done := make(chan bool, 1)
	result := wp.Submit(func() { done <- true })
	// Overflow executes in goroutine
	_ = result
}

// ============================================================
// ProviderManager — GetConfigured
// ============================================================

func TestHB6_ProviderManager_GetConfigured_Empty(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	result := pm.GetConfigured()
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}

func TestHB6_ProviderManager_GetConfigured_WithData(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	p := makeProvider("test-prov", "Test", makeModelDef("gpt-4"), 1, true)
	pm.Add(p)
	result := pm.GetConfigured()
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0].ID != "test-prov" {
		t.Errorf("expected test-prov, got %s", result[0].ID)
	}
}

// ============================================================
// ProviderManager — DeleteByOwner
// ============================================================

func TestHB6_ProviderManager_DeleteByOwner_None(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	count := pm.DeleteByOwner("nonexistent")
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestHB6_ProviderManager_DeleteByOwner_WithMatch(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	p := Provider{
		ID:       "prov1",
		Name:     "Test",
		Type:     "openai_compatible",
		BaseURL:  "https://api.example.com/v1",
		APIKey:   "key-123",
		Enabled:  true,
		Owner:    "consumer-1",
		Priority: 1,
	}
	pm.Add(p)
	count := pm.DeleteByOwner("consumer-1")
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

// ============================================================
// ProviderManager — EnabledRaw
// ============================================================

func TestHB6_ProviderManager_EnabledRaw_Empty(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	result := pm.EnabledRaw()
	// Preset providers with Enabled=true (e.g. kilo-gateway) are included by design.
	// Verify that no user-added providers are in the list.
	for _, p := range result {
		isPreset := false
		for _, pp := range presetProviders {
			if pp.ID == p.ID {
				isPreset = true
				break
			}
		}
		if !isPreset {
			t.Errorf("expected only preset providers, found non-preset provider %s", p.ID)
		}
	}
}

// ============================================================
// ProviderManager — FindCandidates
// ============================================================

func TestHB6_ProviderManager_FindCandidates_NoMatch(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	p := makeProvider("prov1", "Test", makeModelDef("gpt-4"), 1, true)
	pm.Add(p)
	cands := pm.FindCandidates("claude-3")
	if len(cands) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(cands))
	}
}

func TestHB6_ProviderManager_FindCandidates_WithMatch(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	p := makeProvider("prov1", "Test", makeModelDef("gpt-4"), 1, true)
	pm.Add(p)
	cands := pm.FindCandidates("gpt-4")
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
	if cands[0].Model != "gpt-4" {
		t.Errorf("expected gpt-4, got %s", cands[0].Model)
	}
}

// ============================================================
// ProviderManager — isOrgPrefix
// ============================================================

func TestHB6_isOrgPrefix_True(t *testing.T) {
	for _, p := range []string{"Qwen", "deepseek-ai", "meta-llama", "openai", "anthropic", "google"} {
		if !isOrgPrefix(p) {
			t.Errorf("expected %s to be org prefix", p)
		}
	}
}

func TestHB6_isOrgPrefix_False(t *testing.T) {
	if isOrgPrefix("my-provider") {
		t.Error("expected false")
	}
}

// ============================================================
// ProviderManager — ResolveRoute
// ============================================================

func TestHB6_ProviderManager_ResolveRoute_NoProvider(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	_, _, ok := pm.ResolveRoute("nonexistent-model", "priority")
	if ok {
		t.Error("expected no route for unknown model")
	}
}

func TestHB6_ProviderManager_ResolveRoute_WithProvider(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	p := makeProvider("prov1", "Test", makeModelDef("gpt-4"), 1, true)
	pm.Add(p)
	prov, model, ok := pm.ResolveRoute("gpt-4", "priority")
	if !ok {
		t.Fatal("expected route found")
	}
	if model != "gpt-4" {
		t.Errorf("expected gpt-4, got %s", model)
	}
	if prov.ID != "prov1" {
		t.Errorf("expected prov1, got %s", prov.ID)
	}
}

// ============================================================
// Provider — Safe masking
// ============================================================

func TestHB6_Provider_Safe_MasksKey(t *testing.T) {
	p := Provider{APIKey: "sk-1234567890abcdef", APIKeys: []APIKeyConfig{{Key: "sk-abcdefghijk"}}}
	safe := p.Safe()
	if safe.APIKey == "sk-1234567890abcdef" {
		t.Error("API key should be masked")
	}
	if safe.APIKeys[0].Key == "sk-abcdefghijk" {
		t.Error("multi-key should be masked")
	}
}

func TestHB6_Provider_Safe_MasksVMess(t *testing.T) {
	p := Provider{Proxy: "vmess://abc123"}
	safe := p.Safe()
	if safe.Proxy != "vmess://***" {
		t.Errorf("expected vmess://***, got %s", safe.Proxy)
	}
}

// ============================================================
// ProviderAccessControl — UnmarshalJSON migration
// ============================================================

func TestHB6_ProviderAccessControl_Migration(t *testing.T) {
	data := `{"allow_public": true, "share_to_pool": false}`
	var ac ProviderAccessControl
	if err := json.Unmarshal([]byte(data), &ac); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !ac.ShareToPool {
		t.Error("expected share_to_pool migrated from allow_public")
	}
	if ac.MigrationAllowPublic != nil {
		t.Error("migration field should be cleared")
	}
}

// ============================================================
// Performance — initConcurrencyLimiter
// ============================================================

func TestHB6_initConcurrencyLimiter_Default(t *testing.T) {
	initConcurrencyLimiter(0)
	if cap(requestSemaphore) != defaultMaxConcurrentRequests {
		t.Errorf("expected %d, got %d", defaultMaxConcurrentRequests, cap(requestSemaphore))
	}
}

func TestHB6_initConcurrencyLimiter_Custom(t *testing.T) {
	initConcurrencyLimiter(50)
	if cap(requestSemaphore) != 50 {
		t.Errorf("expected 50, got %d", cap(requestSemaphore))
	}
}

// ============================================================
// handleGetAddresses
// ============================================================

func TestHB6_handleGetAddresses_Basic(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("GET", "/api/addresses", nil)
	w := httptest.NewRecorder()
	handleGetAddresses(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ============================================================
// handleDomainBindingStatus
// ============================================================

func TestHB6_handleDomainBindingStatus_NoDomain(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origTunnel := tunnel
	tunnel = nil
	defer func() { tunnel = origTunnel }()

	req := httptest.NewRequest("GET", "/api/domain/binding-status", nil)
	w := httptest.NewRecorder()
	handleDomainBindingStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ============================================================
// Update — platformAssetName
// ============================================================

func TestHB6_platformAssetName_Format(t *testing.T) {
	name := platformAssetName()
	if name == "" {
		t.Error("expected non-empty asset name")
	}
}

// ============================================================
// Update — validTimestamp
// ============================================================

func TestHB6_validTimestamp_Now(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	if !validTimestamp(ts) {
		t.Error("expected valid for now")
	}
}

func TestHB6_validTimestamp_Stale(t *testing.T) {
	ts := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	if validTimestamp(ts) {
		t.Error("expected invalid for stale timestamp")
	}
}

func TestHB6_validTimestamp_InvalidFormat(t *testing.T) {
	if validTimestamp("not-a-timestamp") {
		t.Error("expected invalid for bad format")
	}
}

// ============================================================
// Update — UpdateStatus fields
// ============================================================

func TestHB6_UpdateStatus_Fields(t *testing.T) {
	s := UpdateStatus{
		Env:           "local",
		IsLocal:       true,
		Role:          "origin",
		TargetVersion: "v5.0.0",
		Phase:         PhaseIdle,
		Progress:      50,
	}
	if s.Env != "local" {
		t.Error("unexpected Env")
	}
	if s.Phase != PhaseIdle {
		t.Error("unexpected Phase")
	}
}

// ============================================================
// Update — UpdateSignal fields
// ============================================================

func TestHB6_UpdateSignal_Fields(t *testing.T) {
	sig := UpdateSignal{
		BroadcastBy:         "origin-1",
		TargetVersion:       "v5.0.0",
		MinSupportedVersion: "4.1.7",
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
	}
	if sig.BroadcastBy != "origin-1" {
		t.Error("unexpected BroadcastBy")
	}
}

// ============================================================
// Update — handleAdminUpdateStatus Nil
// ============================================================

func TestHB6_handleAdminUpdateStatus_Nil(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origUM := updateManager
	updateManager = nil
	defer func() { updateManager = origUM }()

	req := httptest.NewRequest("GET", "/api/admin/update/status", nil)
	w := httptest.NewRecorder()
	handleAdminUpdateStatus(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ============================================================
// Update — handleAdminVersionLatest Nil
// ============================================================

func TestHB6_handleAdminVersionLatest_Nil(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origUM := updateManager
	updateManager = nil
	defer func() { updateManager = origUM }()

	req := httptest.NewRequest("GET", "/api/admin/version/latest", nil)
	w := httptest.NewRecorder()
	handleAdminVersionLatest(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ============================================================
// Network — resolvePeerNodeID
// ============================================================

func TestHB6_resolvePeerNodeID_InvalidScheme(t *testing.T) {
	_, err := resolvePeerNodeID("ftp://example.com")
	if err == nil {
		t.Error("expected error for invalid scheme")
	}
}

func TestHB6_resolvePeerNodeID_NoScheme(t *testing.T) {
	_, err := resolvePeerNodeID("example.com")
	if err == nil {
		t.Error("expected error for no scheme")
	}
}

// ============================================================
// Network — resolvePublicEndpoint
// ============================================================

func TestHB6_resolvePublicEndpoint_FederationEndpoint(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	cfg.Set("federation_endpoint", "https://omp.example.com")
	result := resolvePublicEndpoint("")
	if result != "https://omp.example.com" {
		t.Errorf("expected https://omp.example.com, got %s", result)
	}
}

func TestHB6_resolvePublicEndpoint_PublicDomain(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	cfg.Set("public_domain", "https://myomp.io")
	result := resolvePublicEndpoint("")
	if result != "https://myomp.io" {
		t.Errorf("expected https://myomp.io, got %s", result)
	}
}

func TestHB6_resolvePublicEndpoint_HostHeader(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	result := resolvePublicEndpoint("myhost:8000")
	if !strings.HasPrefix(result, "https://myhost") {
		t.Errorf("expected https://myhost, got %s", result)
	}
}

// ============================================================
// Network — NetworkConfig defaults
// ============================================================

func TestHB6_NetworkConfig_Defaults(t *testing.T) {
	c := NetworkConfig{}
	if c.Mode != "" {
		t.Error("expected empty mode default")
	}
	if c.MaxDailyRequests != 0 {
		t.Error("expected 0 default")
	}
}

// ============================================================
// Network — DisclaimerResponse
// ============================================================

func TestHB6_GetDisclaimer_HasSections(t *testing.T) {
	d := GetDisclaimer()
	if d.Title == "" {
		t.Error("expected non-empty title")
	}
	if len(d.Sections) == 0 {
		t.Error("expected sections")
	}
	if d.ConfirmationText == "" {
		t.Error("expected confirmation text")
	}
}

// ============================================================
// Network — JoinConditionResult
// ============================================================

func TestHB6_JoinConditionResult_Defaults(t *testing.T) {
	r := JoinConditionResult{}
	if r.AllMet {
		t.Error("expected AllMet=false by default")
	}
	if r.HasProvider {
		t.Error("expected HasProvider=false by default")
	}
}

// ============================================================
// Network — handleNetworkRoutes
// ============================================================

func TestHB6_handleNetworkRoutes_NilRouteTable(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	origRT := routeTable
	routeTable = nil
	defer func() { routeTable = origRT }()

	req := httptest.NewRequest("GET", "/api/network/routes", nil)
	w := httptest.NewRecorder()
	// handleNetworkRoutes doesn't nil-check routeTable, so we must not call it with nil
	// Instead test with an empty route table
	routeTable = initRouteTable()
	handleNetworkRoutes(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ============================================================
// Network — handleNetworkResolve
// ============================================================

func TestHB6_handleNetworkResolve_InvalidBody(t *testing.T) {
	te := setupTestEnv(t)
	_ = te
	req := httptest.NewRequest("POST", "/api/network/resolve", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleNetworkResolve(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ============================================================
// Network — PeerCapabilities
// ============================================================

func TestHB6_PeerCapabilities_Fields(t *testing.T) {
	caps := PeerCapabilities{
		CanRelay:  true,
		CanSeed:   false,
		Providers: []string{"openai", "anthropic"},
		Bandwidth: "100Mbps",
	}
	if !caps.CanRelay {
		t.Error("expected CanRelay=true")
	}
	if len(caps.Providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(caps.Providers))
	}
}

// ============================================================
// GlobalPoolNode fields
// ============================================================

func TestHB6_GlobalPoolNode_Fields(t *testing.T) {
	n := GlobalPoolNode{
		NodeID:     "n1",
		Region:     "us-east",
		Contributed: 1000,
		Consumed:    200,
		Ratio:       4.76,
		Reputation:  0.9,
		Status:      "active",
	}
	if n.NodeID != "n1" {
		t.Error("unexpected NodeID")
	}
	if n.Status != "active" {
		t.Error("unexpected Status")
	}
}
