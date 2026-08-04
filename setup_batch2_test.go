package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ============================================================
// tracker.go — Record, RecordWithRetry, Flush, Reset, Stop
// ============================================================

func TestBatch2_Tracker_Record(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "TestProvider", "gpt-4o", 100, 50, 200.0, true, "")

	if len(tracker.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(tracker.records))
	}
	r := tracker.records[0]
	if r.ProviderID != "p1" {
		t.Errorf("ProviderID = %q, want %q", r.ProviderID, "p1")
	}
	if r.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", r.PromptTokens)
	}
	if r.CompletionTokens != 50 {
		t.Errorf("CompletionTokens = %d, want 50", r.CompletionTokens)
	}
	if !r.Success {
		t.Error("Success should be true")
	}
}

func TestBatch2_Tracker_RecordWithRetry(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.RecordWithRetry("p2", "Provider2", "deepseek-chat", 200, 100, 350.5, true, "", true, 2, "guest")

	if len(tracker.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(tracker.records))
	}
	r := tracker.records[0]
	if r.AccessType != "guest" {
		t.Errorf("AccessType = %q, want %q", r.AccessType, "guest")
	}
	if r.LatencyMS != 350.5 {
		t.Errorf("LatencyMS = %f, want 350.5", r.LatencyMS)
	}
}

func TestBatch2_Tracker_Record_FailedRequest(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p3", "BadProvider", "model-x", 0, 0, 0, false, "timeout")

	if len(tracker.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(tracker.records))
	}
	r := tracker.records[0]
	if r.Success {
		t.Error("Success should be false for failed request")
	}
	if r.Error != "timeout" {
		t.Errorf("Error = %q, want %q", r.Error, "timeout")
	}
	if r.CostUSD != 0 {
		t.Errorf("CostUSD should be 0 for failed request, got %f", r.CostUSD)
	}
}

func TestBatch2_Tracker_Flush(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "TestProvider", "gpt-4o", 100, 50, 200.0, true, "")
	tracker.Flush()

	data, err := os.ReadFile(env.tkInst.dataPath)
	if err != nil {
		t.Fatalf("failed to read flushed data: %v", err)
	}
	var records []UsageRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("failed to unmarshal flushed data: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 flushed record, got %d", len(records))
	}
}

func TestBatch2_Tracker_Reset(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "TestProvider", "gpt-4o", 100, 50, 200.0, true, "")
	tracker.Reset()

	if len(tracker.records) != 0 {
		t.Errorf("expected 0 records after reset, got %d", len(tracker.records))
	}
	if len(tracker.ewmaCache) != 0 {
		t.Errorf("expected empty ewmaCache after reset, got %d entries", len(tracker.ewmaCache))
	}
}

func TestBatch2_Tracker_Stop(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "TestProvider", "gpt-4o", 100, 50, 200.0, true, "")
	tracker.Stop()

	select {
	case <-tracker.stopCh:
	default:
		t.Error("stopCh should be closed after Stop()")
	}
}

func TestBatch2_Tracker_EWMAUpdate(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	tracker.Record("p1", "TestProvider", "gpt-4o", 100, 50, 200.0, true, "")
	ewma := tracker.GetEWMA("p1")
	if ewma != 200.0 {
		t.Errorf("first EWMA = %f, want 200.0", ewma)
	}

	tracker.Record("p1", "TestProvider", "gpt-4o", 100, 50, 100.0, true, "")
	ewma2 := tracker.GetEWMA("p1")
	if ewma2 == 0 {
		t.Error("EWMA should be updated after second record")
	}
}

// ============================================================
// conn_tracker.go — IncrProviderConn, DecrProviderConn, GetProviderConns,
// IncrGuestConn, DecrGuestConn, GetGuestConns, IncrConn, DecrConn
// ============================================================

func TestBatch2_IncrProviderConn_FromZero(t *testing.T) {
	providerConns.Range(func(k, v any) bool { providerConns.Delete(k); return true })

	IncrProviderConn("test-p1")
	if got := GetProviderConns("test-p1"); got != 1 {
		t.Errorf("GetProviderConns = %d, want 1", got)
	}
}

func TestBatch2_IncrDecrProviderConn(t *testing.T) {
	providerConns.Range(func(k, v any) bool { providerConns.Delete(k); return true })

	IncrProviderConn("test-p2")
	IncrProviderConn("test-p2")
	if got := GetProviderConns("test-p2"); got != 2 {
		t.Errorf("GetProviderConns after 2 incr = %d, want 2", got)
	}

	DecrProviderConn("test-p2")
	if got := GetProviderConns("test-p2"); got != 1 {
		t.Errorf("GetProviderConns after 1 decr = %d, want 1", got)
	}
}

func TestBatch2_GetProviderConns_UnknownProvider(t *testing.T) {
	providerConns.Range(func(k, v any) bool { providerConns.Delete(k); return true })

	if got := GetProviderConns("nonexistent"); got != 0 {
		t.Errorf("GetProviderConns for unknown = %d, want 0", got)
	}
}

func TestBatch2_IncrGuestConn_FromZero(t *testing.T) {
	guestConns.Range(func(k, v any) bool { guestConns.Delete(k); return true })

	IncrGuestConn()
	if got := GetGuestConns(); got != 1 {
		t.Errorf("GetGuestConns = %d, want 1", got)
	}
}

func TestBatch2_IncrDecrGuestConn(t *testing.T) {
	guestConns.Range(func(k, v any) bool { guestConns.Delete(k); return true })

	IncrGuestConn()
	IncrGuestConn()
	if got := GetGuestConns(); got != 2 {
		t.Errorf("GetGuestConns after 2 incr = %d, want 2", got)
	}

	DecrGuestConn()
	if got := GetGuestConns(); got != 1 {
		t.Errorf("GetGuestConns after 1 decr = %d, want 1", got)
	}
}

func TestBatch2_IncrConn_GuestType(t *testing.T) {
	providerConns.Range(func(k, v any) bool { providerConns.Delete(k); return true })
	guestConns.Range(func(k, v any) bool { guestConns.Delete(k); return true })

	IncrConn("p1", "guest")
	if got := GetProviderConns("p1"); got != 1 {
		t.Errorf("provider conns = %d, want 1", got)
	}
	if got := GetGuestConns(); got != 1 {
		t.Errorf("guest conns = %d, want 1", got)
	}
}

func TestBatch2_IncrConn_PrivateType(t *testing.T) {
	providerConns.Range(func(k, v any) bool { providerConns.Delete(k); return true })
	guestConns.Range(func(k, v any) bool { guestConns.Delete(k); return true })

	IncrConn("p1", "private")
	if got := GetProviderConns("p1"); got != 1 {
		t.Errorf("provider conns = %d, want 1", got)
	}
	if got := GetGuestConns(); got != 0 {
		t.Errorf("guest conns should stay 0 for private, got %d", got)
	}
}

func TestBatch2_DecrConn_GuestType(t *testing.T) {
	providerConns.Range(func(k, v any) bool { providerConns.Delete(k); return true })
	guestConns.Range(func(k, v any) bool { guestConns.Delete(k); return true })

	IncrConn("p1", "guest")
	DecrConn("p1", "guest")
	if got := GetProviderConns("p1"); got != 0 {
		t.Errorf("provider conns after decr = %d, want 0", got)
	}
	if got := GetGuestConns(); got != 0 {
		t.Errorf("guest conns after decr = %d, want 0", got)
	}
}

// ============================================================
// logger.go — CloseAccessLog
// ============================================================

func TestBatch2_CloseAccessLog_NilLogger(t *testing.T) {
	orig := appLogger
	appLogger = nil
	t.Cleanup(func() { appLogger = orig })

	CloseAccessLog()
}

func TestBatch2_CloseAccessLog_WithFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("failed to create test log file: %v", err)
	}

	orig := appLogger
	appLogger = &Logger{accessFile: f}
	t.Cleanup(func() { appLogger = orig })

	CloseAccessLog()
}

func TestBatch2_CloseAccessLog_NoFile(t *testing.T) {
	orig := appLogger
	appLogger = &Logger{accessFile: nil}
	t.Cleanup(func() { appLogger = orig })

	CloseAccessLog()
}

// ============================================================
// node.go — ConfirmBackup, PubKeyB64, canonicalJSON
// ============================================================

func TestBatch2_ConfirmBackup_ClearsMnemonic(t *testing.T) {
	dir := t.TempDir()
	n := &NodeIdentity{
		keyPath:         filepath.Join(dir, "node.key"),
		hasMnemonic:     true,
		backupConfirmed: false,
		mnemonic:        "test secret mnemonic phrase",
	}

	if n.IsBackupConfirmed() {
		t.Error("should not be confirmed initially")
	}

	n.ConfirmBackup()

	if !n.IsBackupConfirmed() {
		t.Error("should be confirmed after ConfirmBackup")
	}
	if n.mnemonic != "" {
		t.Error("mnemonic should be cleared from memory after backup confirmation")
	}
}

func TestBatch2_PubKeyB64_WithKey(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	origNode := node
	node = &NodeIdentity{pubKey: pub, keyPath: filepath.Join(t.TempDir(), "node.key")}
	t.Cleanup(func() { node = origNode })

	got := node.PubKeyB64()
	expected := base64.StdEncoding.EncodeToString(pub)
	if got != expected {
		t.Errorf("PubKeyB64 = %q, want %q", got, expected)
	}
}

func TestBatch2_PubKeyB64_NilKey(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	origNode := node
	node = &NodeIdentity{pubKey: nil, keyPath: filepath.Join(t.TempDir(), "node.key")}
	t.Cleanup(func() { node = origNode })

	got := node.PubKeyB64()
	if got != "" {
		t.Errorf("PubKeyB64 with nil key = %q, want empty string", got)
	}
}

func TestBatch2_CanonicalJSON_StructWithSignature(t *testing.T) {
	type SignedMsg struct {
		Data      string `json:"data"`
		Signature string `json:"signature"`
	}

	msg := SignedMsg{Data: "hello", Signature: "sig123"}
	b, err := canonicalJSON(msg)
	if err != nil {
		t.Fatalf("canonicalJSON error: %v", err)
	}

	var parsed map[string]string
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if parsed["signature"] != "" {
		t.Errorf("signature should be zeroed in canonical JSON, got %q", parsed["signature"])
	}
	if parsed["data"] != "hello" {
		t.Errorf("data = %q, want %q", parsed["data"], "hello")
	}
}

func TestBatch2_CanonicalJSON_PlainStruct(t *testing.T) {
	type Plain struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	p := Plain{Name: "test", Value: 42}
	b, err := canonicalJSON(p)
	if err != nil {
		t.Fatalf("canonicalJSON error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if parsed["name"] != "test" {
		t.Errorf("name = %v, want %q", parsed["name"], "test")
	}
}

func TestBatch2_CanonicalJSON_NilPointer(t *testing.T) {
	b, err := canonicalJSON((*struct{ X int })(nil))
	if err != nil {
		t.Fatalf("canonicalJSON on nil pointer error: %v", err)
	}
	if string(b) != "null" {
		t.Errorf("nil pointer should marshal to null, got %q", string(b))
	}
}

// ============================================================
// performance.go — ActiveWorkers
// ============================================================

func TestBatch2_ActiveWorkers_Idle(t *testing.T) {
	wp := &WorkerPool{
		taskCh:  make(chan func(), 10),
		workers: 2,
	}
	if got := wp.ActiveWorkers(); got != 0 {
		t.Errorf("ActiveWorkers on idle pool = %d, want 0", got)
	}
}

func TestBatch2_ActiveWorkers_WithActiveTask(t *testing.T) {
	wp := &WorkerPool{
		taskCh:  make(chan func(), 10),
		workers: 1,
	}

	started := make(chan struct{})
	done := make(chan struct{})
	wp.taskCh <- func() {
		close(started)
		<-done
	}

	go wp.worker()

	<-started
	if got := wp.ActiveWorkers(); got != 1 {
		t.Errorf("ActiveWorkers with active task = %d, want 1", got)
	}
	close(done)
}

func TestBatch2_ActiveWorkers_AfterCompletion(t *testing.T) {
	wp := &WorkerPool{
		taskCh:  make(chan func(), 10),
		workers: 1,
	}

	done := make(chan struct{})
	wp.taskCh <- func() {
		close(done)
	}

	go wp.worker()
	<-done
	time.Sleep(50 * time.Millisecond)

	if got := wp.ActiveWorkers(); got != 0 {
		t.Errorf("ActiveWorkers after task done = %d, want 0", got)
	}
}

// ============================================================
// health_check_now.go — CheckProviderNow
// ============================================================

func TestBatch2_CheckProviderNow_UnknownProvider(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	hc := &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
		interval: 5 * time.Minute,
		stopCh:   make(chan struct{}),
	}
	defer close(hc.stopCh)

	hc.CheckProviderNow("nonexistent")

	if got := len(hc.statuses); got != 0 {
		t.Errorf("statuses should remain empty for unknown provider, got %d entries", got)
	}
}

func TestBatch2_CheckProviderNow_DisabledProvider(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{
		ID: "disabled-p", Name: "Disabled", Type: "openai_compatible",
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test",
		Enabled: false, Models: makeModelDef("model-a"),
		Priority: 1,
	})

	hc := &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
		interval: 5 * time.Minute,
		stopCh:   make(chan struct{}),
	}
	defer close(hc.stopCh)

	hc.CheckProviderNow("disabled-p")

	if _, ok := hc.statuses["disabled-p"]; ok {
		t.Error("disabled provider should not get a health status entry from CheckProviderNow")
	}
}

func TestBatch2_CheckProviderNow_EnabledProvider(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{
		ID: "enabled-p", Name: "Enabled", Type: "openai_compatible",
		BaseURL: "https://api.example.com/v1", APIKey: "sk-test",
		Enabled: true, Models: makeModelDef("model-a"),
		Priority: 1,
	})

	hc := &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
		interval: 5 * time.Minute,
		stopCh:   make(chan struct{}),
	}
	defer close(hc.stopCh)

	hc.CheckProviderNow("enabled-p")

	hs, ok := hc.statuses["enabled-p"]
	if !ok {
		t.Fatal("enabled provider should get a health status entry")
	}
	if hs.LastCheck == "" {
		t.Error("LastCheck should be set after CheckProviderNow")
	}
}

// ============================================================
// network.go — updateOnlineCount
// ============================================================

func TestBatch2_UpdateOnlineCount_AllOnline(t *testing.T) {
	nm := &NetworkManager{
		config: NetworkConfig{
			Peers: []PeerInfo{
				{NodeID: "n1", Status: "online"},
				{NodeID: "n2", Status: "online"},
				{NodeID: "n3", Status: "online"},
			},
		},
	}

	nm.updateOnlineCount()
	if nm.config.Stats.OnlinePeers != 3 {
		t.Errorf("OnlinePeers = %d, want 3", nm.config.Stats.OnlinePeers)
	}
}

func TestBatch2_UpdateOnlineCount_MixedStatus(t *testing.T) {
	nm := &NetworkManager{
		config: NetworkConfig{
			Peers: []PeerInfo{
				{NodeID: "n1", Status: "online"},
				{NodeID: "n2", Status: "offline"},
				{NodeID: "n3", Status: "degraded"},
			},
		},
	}

	nm.updateOnlineCount()
	if nm.config.Stats.OnlinePeers != 1 {
		t.Errorf("OnlinePeers = %d, want 1 (only 'online' status counts)", nm.config.Stats.OnlinePeers)
	}
}

func TestBatch2_UpdateOnlineCount_EmptyPeers(t *testing.T) {
	nm := &NetworkManager{
		config: NetworkConfig{
			Peers: []PeerInfo{},
		},
	}

	nm.updateOnlineCount()
	if nm.config.Stats.OnlinePeers != 0 {
		t.Errorf("OnlinePeers with no peers = %d, want 0", nm.config.Stats.OnlinePeers)
	}
}
