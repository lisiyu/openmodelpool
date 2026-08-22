package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================
// network_keys.go — ClassifyKey, ParseGuestKeyFormat, handlers
// ============================================================

func TestHB3_ClassifyKey_Proxy(t *testing.T) {
	kt := ClassifyKey("sk-abc123")
	if kt != KeyTypeProxy {
		t.Errorf("expected proxy, got %s", kt)
	}
}

func TestHB3_ClassifyKey_Guest(t *testing.T) {
	kt := ClassifyKey("sk-guest-node1-abc")
	if kt != KeyTypeGuest {
		t.Errorf("expected guest, got %s", kt)
	}
}

func TestHB3_ClassifyKey_Public(t *testing.T) {
	kt := ClassifyKey(PublicKeyValue)
	if kt != KeyTypePublic {
		t.Errorf("expected public, got %s", kt)
	}
}

func TestHB3_ClassifyKey_Unknown(t *testing.T) {
	kt := ClassifyKey("invalid-key")
	if kt != KeyTypeUnknown {
		t.Errorf("expected unknown, got %s", kt)
	}
}

func TestHB3_ClassifyKey_Empty(t *testing.T) {
	kt := ClassifyKey("")
	if kt != KeyTypeUnknown {
		t.Errorf("expected unknown for empty, got %s", kt)
	}
}

func TestHB3_ParseGuestKeyFormat_Valid(t *testing.T) {
	nodeID, valid := ParseGuestKeyFormat("sk-guest-mmx-abc123-deadbeef")
	if !valid {
		t.Error("expected valid")
	}
	if nodeID != "mmx-abc123" {
		t.Errorf("expected nodeID=mmx-abc123, got %s", nodeID)
	}
}

func TestHB3_ParseGuestKeyFormat_NoPrefix(t *testing.T) {
	_, valid := ParseGuestKeyFormat("sk-proxy-key")
	if valid {
		t.Error("expected invalid for non-guest prefix")
	}
}

func TestHB3_ParseGuestKeyFormat_NoDash(t *testing.T) {
	_, valid := ParseGuestKeyFormat("sk-guest-nodash")
	if valid {
		t.Error("expected invalid for no dash separator")
	}
}

func TestHB3_ParseGuestKeyFormat_TrailingDash(t *testing.T) {
	_, valid := ParseGuestKeyFormat("sk-guest-nodeid-")
	if valid {
		t.Error("expected invalid for trailing dash")
	}
}

func TestHB3_GuestKeyStore_RevokeAndDelete(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	key, err := GenerateGuestKey("test-node")
	if err != nil {
		t.Fatal(err)
	}
	rec := guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil {
		t.Fatal("expected record")
	}
	if rec.Revoked {
		t.Error("should not be revoked yet")
	}
	if err := guestKeyStore.RevokeGuestKey(key); err != nil {
		t.Fatal(err)
	}
	rec = guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil || !rec.Revoked {
		t.Error("expected revoked")
	}
	if err := guestKeyStore.DeleteGuestKey(key); err != nil {
		t.Fatal(err)
	}
	rec = guestKeyStore.GetGuestKeyRecord(key)
	if rec != nil {
		t.Error("expected nil after delete")
	}
}

func TestHB3_GuestKeyStore_DeleteNotRevoked(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	key, _ := GenerateGuestKey("test-node")
	err := guestKeyStore.DeleteGuestKey(key)
	if err == nil {
		t.Error("expected error deleting non-revoked key")
	}
}

func TestHB3_GuestKeyStore_RevokeNotFound(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	err := guestKeyStore.RevokeGuestKey("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestHB3_GuestKeyStore_MarkAsCollaborator(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	key, _ := GenerateGuestKey("test-node")
	if err := guestKeyStore.MarkAsCollaborator(key); err != nil {
		t.Fatal(err)
	}
	rec := guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil || rec.Note != "[\u534f\u4f5c]" {
		t.Errorf("expected note, got %q", rec.Note)
	}
}

func TestHB3_GuestKeyStore_MarkAsCollaborator_WithExistingNote(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	key, _ := GenerateGuestKey("test-node", GuestKeyOptions{Note: "my note"})
	if err := guestKeyStore.MarkAsCollaborator(key); err != nil {
		t.Fatal(err)
	}
	rec := guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil || !strings.HasPrefix(rec.Note, "[\u534f\u4f5c]") {
		t.Errorf("expected note to start with prefix, got %q", rec.Note)
	}
}

func TestHB3_GuestKeyStore_MarkAsCollaborator_NotFound(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	err := guestKeyStore.MarkAsCollaborator("nonexistent")
	if err == nil {
		t.Error("expected error")
	}
}

func TestHB3_GuestKeyStore_SetShareType(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	key, _ := GenerateGuestKey("test-node")
	if err := guestKeyStore.SetShareType(key, "consumer"); err != nil {
		t.Fatal(err)
	}
	st := guestKeyStore.GetShareType(key)
	if st != "consumer" {
		t.Errorf("expected consumer, got %s", st)
	}
}

func TestHB3_GuestKeyStore_SetShareType_Locked(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	key, _ := GenerateGuestKey("test-node")
	_ = guestKeyStore.SetShareType(key, "consumer")
	err := guestKeyStore.SetShareType(key, "collaborator")
	if err == nil {
		t.Error("expected error when share type already locked")
	}
}

func TestHB3_GuestKeyStore_SetShareType_NotFound(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	err := guestKeyStore.SetShareType("nonexistent", "consumer")
	if err == nil {
		t.Error("expected error")
	}
}

func TestHB3_GuestKeyStore_GetShareType_NotFound(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	st := guestKeyStore.GetShareType("nonexistent")
	if st != "" {
		t.Errorf("expected empty, got %s", st)
	}
}

func TestHB3_GuestKeyStore_UpdateGuestKeyQuota(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	key, _ := GenerateGuestKey("test-node")
	if err := guestKeyStore.UpdateGuestKeyQuota(key, 5000); err != nil {
		t.Fatal(err)
	}
	rec := guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil || rec.Quota != 5000 {
		t.Errorf("expected quota 5000, got %d", rec.Quota)
	}
}

func TestHB3_GuestKeyStore_UpdateGuestKeyQuotaMulti(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	key, _ := GenerateGuestKey("test-node")
	q := int64(1000)
	qh := int64(500)
	rpm := 30
	if err := guestKeyStore.UpdateGuestKeyQuotaMulti(key, &q, &qh, nil, &rpm); err != nil {
		t.Fatal(err)
	}
	rec := guestKeyStore.GetGuestKeyRecord(key)
	if rec == nil || rec.Quota != 1000 || rec.QuotaHourly != 500 || rec.RPM != 30 {
		t.Errorf("unexpected quota values: %+v", rec)
	}
}

func TestHB3_GuestKeyStore_UpdateGuestKeyQuota_NotFound(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyStore(dir)
	err := guestKeyStore.UpdateGuestKeyQuota("nonexistent", 100)
	if err == nil {
		t.Error("expected error")
	}
}

func TestHB3_GuestKeyUsageTracker_CheckAndReserve(t *testing.T) {
	tracker := &guestKeyUsageTracker{usage: make(map[string]int64)}
	allowed, remaining := tracker.CheckAndReserve("key1", 1000, 500)
	if !allowed || remaining != 500 {
		t.Errorf("expected allowed=true remaining=500, got allowed=%v remaining=%d", allowed, remaining)
	}
}

func TestHB3_GuestKeyUsageTracker_CheckAndReserve_NoQuota(t *testing.T) {
	tracker := &guestKeyUsageTracker{usage: make(map[string]int64)}
	allowed, _ := tracker.CheckAndReserve("key1", 0, 500)
	if !allowed {
		t.Error("expected allowed when no quota limit")
	}
}

func TestHB3_GuestKeyUsageTracker_CheckAndReserve_Exceeded(t *testing.T) {
	tracker := &guestKeyUsageTracker{usage: map[string]int64{"key1": 999}, day: todayUTC()} // B8-1a: current window
	allowed, _ := tracker.CheckAndReserve("key1", 1000, 500)
	if allowed {
		t.Error("expected not allowed when quota exceeded")
	}
}

func TestHB3_GuestKeyUsageTracker_Adjust(t *testing.T) {
	tracker := &guestKeyUsageTracker{usage: map[string]int64{"key1": 500}}
	tracker.Adjust("key1", 500, 300)
	if tracker.GetUsage("key1") != 300 {
		t.Errorf("expected 300, got %d", tracker.GetUsage("key1"))
	}
}

func TestHB3_GuestKeyUsageTracker_Adjust_NegativeClamp(t *testing.T) {
	tracker := &guestKeyUsageTracker{usage: map[string]int64{"key1": 10}}
	tracker.Adjust("key1", 10, 0)
	if tracker.GetUsage("key1") != 0 {
		t.Errorf("expected 0, got %d", tracker.GetUsage("key1"))
	}
}

func TestHB3_GuestKeyUsageTracker_GetUsage_Empty(t *testing.T) {
	tracker := &guestKeyUsageTracker{usage: make(map[string]int64)}
	if tracker.GetUsage("nonexistent") != 0 {
		t.Error("expected 0 for nonexistent key")
	}
}

// ============================================================
// network_keys.go — Handlers
// ============================================================

func TestHB3_HandleGuestKeyList_NilStore(t *testing.T) {
	setupTestEnv(t)
	guestKeyStore = nil
	req := httptest.NewRequest("GET", "/api/network/guest-keys", nil)
	w := httptest.NewRecorder()
	handleGuestKeyList(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyRevoke_NilStore(t *testing.T) {
	setupTestEnv(t)
	guestKeyStore = nil
	req := httptest.NewRequest("DELETE", "/api/network/guest-keys/somekey", nil)
	w := httptest.NewRecorder()
	handleGuestKeyRevoke(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyDelete_NilStore(t *testing.T) {
	setupTestEnv(t)
	guestKeyStore = nil
	req := httptest.NewRequest("DELETE", "/api/network/guest-keys/somekey/permanent", nil)
	w := httptest.NewRecorder()
	handleGuestKeyDelete(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyMarkCollaborator_NilStore(t *testing.T) {
	setupTestEnv(t)
	guestKeyStore = nil
	req := httptest.NewRequest("POST", "/api/network/guest-keys/somekey/mark-collaborator", nil)
	w := httptest.NewRecorder()
	handleGuestKeyMarkCollaborator(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyShareType_NilStore(t *testing.T) {
	setupTestEnv(t)
	guestKeyStore = nil
	body := `{"share_type":"consumer"}`
	req := httptest.NewRequest("POST", "/api/network/guest-keys/somekey/share-type", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleGuestKeyShareType(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyShareType_InvalidType(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initGuestKeyStore(dir)
	body := `{"share_type":"invalid"}`
	req := httptest.NewRequest("POST", "/api/network/guest-keys/somekey/share-type", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleGuestKeyShareType(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyShareType_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initGuestKeyStore(dir)
	req := httptest.NewRequest("POST", "/api/network/guest-keys/somekey/share-type", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	handleGuestKeyShareType(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyUpdateQuota_NilStore(t *testing.T) {
	setupTestEnv(t)
	guestKeyStore = nil
	body := `{"quota":1000}`
	req := httptest.NewRequest("PUT", "/api/network/guest-keys/somekey/quota", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleGuestKeyUpdateQuota(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyUpdateQuota_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initGuestKeyStore(dir)
	req := httptest.NewRequest("PUT", "/api/network/guest-keys/somekey/quota", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	handleGuestKeyUpdateQuota(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleGuestKeyUpdateQuota_NegativeQuota(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initGuestKeyStore(dir)
	body := `{"quota":-1}`
	req := httptest.NewRequest("PUT", "/api/network/guest-keys/somekey/quota", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleGuestKeyUpdateQuota(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleNetworkKeyValidate_PublicKey(t *testing.T) {
	setupTestEnv(t)
	body := `{"key":"sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1"}`
	req := httptest.NewRequest("POST", "/api/network/keys/validate", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleNetworkKeyValidate(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["key_type"] != "public" {
		t.Errorf("expected key_type=public, got %v", resp["key_type"])
	}
}

func TestHB3_HandleNetworkKeyValidate_UnknownKey(t *testing.T) {
	setupTestEnv(t)
	body := `{"key":"unknown-key-format"}`
	req := httptest.NewRequest("POST", "/api/network/keys/validate", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleNetworkKeyValidate(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["valid"] != false {
		t.Error("expected valid=false for unknown key")
	}
}

func TestHB3_HandleNetworkKeyValidate_MissingKey(t *testing.T) {
	setupTestEnv(t)
	body := `{"key":""}`
	req := httptest.NewRequest("POST", "/api/network/keys/validate", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleNetworkKeyValidate(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleNetworkKeyValidate_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest("POST", "/api/network/keys/validate", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	handleNetworkKeyValidate(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleGetQuotaAllocation_NilNetMgr(t *testing.T) {
	setupTestEnv(t)
	netMgr = nil
	req := httptest.NewRequest("GET", "/api/network/quota-allocation", nil)
	w := httptest.NewRecorder()
	handleGetQuotaAllocation(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB3_HandleUpdateQuotaAllocation_NilNetMgr(t *testing.T) {
	setupTestEnv(t)
	netMgr = nil
	body := `{"guest_key_percent":30}`
	req := httptest.NewRequest("PUT", "/api/network/quota-allocation", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleUpdateQuotaAllocation(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleUpdateQuotaAllocation_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initNetworkManager(dir)
	body := "bad json"
	req := httptest.NewRequest("PUT", "/api/network/quota-allocation", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleUpdateQuotaAllocation(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleUpdateQuotaAllocation_OutOfRange(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initNetworkManager(dir)
	body := `{"guest_key_percent":150}`
	req := httptest.NewRequest("PUT", "/api/network/quota-allocation", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleUpdateQuotaAllocation(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ============================================================
// network_global_pool.go — GlobalPool methods
// ============================================================

func TestHB3_GlobalPool_JoinPool_EmptyNodeID(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
	}
	err := gp.JoinPool("", "us", 10000)
	if err == nil {
		t.Error("expected error for empty node_id")
	}
}

func TestHB3_GlobalPool_JoinPool_BelowMinContribution(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
	}
	err := gp.JoinPool("node1", "us", 100)
	if err == nil {
		t.Error("expected error for below minimum contribution")
	}
}

func TestHB3_GlobalPool_JoinPool_Success(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          dir + "/pool.json",
	}
	err := gp.JoinPool("node1", "us", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(gp.ParticipantNodes) != 1 {
		t.Error("expected 1 participant")
	}
}

func TestHB3_GlobalPool_JoinPool_UpdateExisting(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          dir + "/pool.json",
	}
	_ = gp.JoinPool("node1", "us", 10000)
	err := gp.JoinPool("node1", "us", 10000)
	if err != nil {
		t.Fatal(err)
	}
	if gp.NodeContributions["node1"] != 20000 {
		t.Errorf("expected 20000, got %d", gp.NodeContributions["node1"])
	}
}

func TestHB3_GlobalPool_Contribute_NotParticipant(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
	}
	err := gp.Contribute("node1", 1000)
	if err == nil {
		t.Error("expected error for non-participant")
	}
}

func TestHB3_GlobalPool_Contribute_ZeroAmount(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
	}
	err := gp.Contribute("node1", 0)
	if err == nil {
		t.Error("expected error for zero amount")
	}
}

func TestHB3_GlobalPool_RecordConsumption(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          dir + "/pool.json",
	}
	_ = gp.JoinPool("node1", "", 10000)
	gp.RecordConsumption("node1", 1000)
	if gp.NodeConsumptions["node1"] != 1000 {
		t.Errorf("expected 1000, got %d", gp.NodeConsumptions["node1"])
	}
	time.Sleep(50 * time.Millisecond)
}

func TestHB3_GlobalPool_GetStatus(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          dir + "/pool.json",
	}
	_ = gp.JoinPool("node1", "us", 10000)
	status := gp.GetStatus()
	if status["participant_count"] != 1 {
		t.Errorf("expected 1 participant, got %v", status["participant_count"])
	}
}

func TestHB3_GlobalPool_GetNodes(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          dir + "/pool.json",
	}
	_ = gp.JoinPool("node1", "us", 10000)
	nodes := gp.GetNodes()
	if len(nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(nodes))
	}
}

func TestHB3_GlobalPool_GetStats(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          dir + "/pool.json",
	}
	_ = gp.JoinPool("node1", "us", 10000)
	stats := gp.GetStats()
	if stats["total_participants"] != 1 {
		t.Errorf("expected 1, got %v", stats["total_participants"])
	}
}

func TestHB3_GlobalPool_SelectBestNode_Empty(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
	}
	best := gp.SelectBestNode("")
	if best != nil {
		t.Error("expected nil for empty pool")
	}
}

func TestHB3_GlobalPool_Heartbeat(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          dir + "/pool.json",
	}
	_ = gp.JoinPool("node1", "us", 10000)
	gp.Heartbeat("node1")
	nodes := gp.GetNodes()
	if len(nodes) != 1 || nodes[0].Status != "active" {
		t.Error("expected active after heartbeat")
	}
}

func TestHB3_GlobalPool_UtilizationZero(t *testing.T) {
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
	}
	if gp.utilizationLocked() != 0 {
		t.Error("expected 0 utilization for empty pool")
	}
}

// ============================================================
// network_global_pool.go — PublicKeyQuota
// ============================================================

func TestHB3_PublicKeyQuota_CheckQuota_Allowed(t *testing.T) {
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
	allowed, reason, _ := q.CheckQuota("1.2.3.4", "gpt-4o", 100)
	if !allowed {
		t.Errorf("expected allowed, got reason: %s", reason)
	}
}

func TestHB3_PublicKeyQuota_CheckQuota_GlobalExhausted(t *testing.T) {
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
		globalUsedToday:   100,
	}
	allowed, reason, _ := q.CheckQuota("1.2.3.4", "", 50)
	if allowed {
		t.Error("expected not allowed when global exhausted")
	}
	if reason == "" {
		t.Error("expected reason")
	}
}

func TestHB3_PublicKeyQuota_ReserveQuota(t *testing.T) {
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
	reserved, _, _ := q.ReserveQuota("1.2.3.4", "", 100)
	if !reserved {
		t.Error("expected reserved")
	}
	if q.globalUsedToday != 100 {
		t.Errorf("expected 100 used, got %d", q.globalUsedToday)
	}
}

func TestHB3_PublicKeyQuota_AdjustQuota(t *testing.T) {
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
		globalUsedToday:   100,
	}
	q.AdjustQuota("1.2.3.4", "", 100, 50)
	if q.globalUsedToday != 50 {
		t.Errorf("expected 50 after adjust, got %d", q.globalUsedToday)
	}
}

func TestHB3_PublicKeyQuota_AdjustQuota_Nil(t *testing.T) {
	var q *PublicKeyQuota
	q.AdjustQuota("1.2.3.4", "", 100, 50)
}

func TestHB3_PublicKeyQuota_RecordUsage(t *testing.T) {
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
	q.RecordUsage("1.2.3.4", "gpt-4o", 200)
	if q.globalUsedToday != 200 {
		t.Errorf("expected 200, got %d", q.globalUsedToday)
	}
	if q.modelUsage["gpt-4o"] != 200 {
		t.Errorf("expected model usage 200, got %d", q.modelUsage["gpt-4o"])
	}
}

func TestHB3_PublicKeyQuota_RecordUsage_ZeroTokens(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit: 100000,
		ipUsage:          make(map[string]*IPUsageTracker),
		hourlyUsage:      make(map[string]int64),
		modelUsage:       make(map[string]int64),
		lastDailyReset:   time.Now(),
		lastHourlyReset:  time.Now(),
	}
	q.RecordUsage("1.2.3.4", "", 0)
	if q.globalUsedToday != 0 {
		t.Error("expected no change for zero tokens")
	}
}

func TestHB3_PublicKeyQuota_GetQuotaStatus_Nil(t *testing.T) {
	var q *PublicKeyQuota
	status := q.GetQuotaStatus()
	if status["enabled"] != false {
		t.Error("expected enabled=false for nil quota")
	}
}

func TestHB3_PublicKeyQuota_GetQuotaStatus(t *testing.T) {
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
	if status["enabled"] != true {
		t.Error("expected enabled=true")
	}
}

// ============================================================
// network_global_pool.go — Handlers
// ============================================================

func TestHB3_HandleGlobalPoolStatus_Nil(t *testing.T) {
	setupTestEnv(t)
	globalPool = nil
	req := httptest.NewRequest("GET", "/api/network/global-pool", nil)
	w := httptest.NewRecorder()
	handleGlobalPoolStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB3_HandleGlobalPoolJoin_Nil(t *testing.T) {
	setupTestEnv(t)
	globalPool = nil
	body := `{"node_id":"n1","region":"us","amount":10000}`
	req := httptest.NewRequest("POST", "/api/network/global-pool/join", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleGlobalPoolJoin(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleGlobalPoolJoin_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initGlobalPool(dir)
	req := httptest.NewRequest("POST", "/api/network/global-pool/join", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	handleGlobalPoolJoin(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleGlobalPoolContribute_Nil(t *testing.T) {
	setupTestEnv(t)
	globalPool = nil
	body := `{"node_id":"n1","amount":1000}`
	req := httptest.NewRequest("POST", "/api/network/global-pool/contribute", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleGlobalPoolContribute(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleGlobalPoolContribute_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initGlobalPool(dir)
	req := httptest.NewRequest("POST", "/api/network/global-pool/contribute", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	handleGlobalPoolContribute(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleGlobalPoolNodes_Nil(t *testing.T) {
	setupTestEnv(t)
	globalPool = nil
	req := httptest.NewRequest("GET", "/api/network/global-pool/nodes", nil)
	w := httptest.NewRecorder()
	handleGlobalPoolNodes(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB3_HandleGlobalPoolStats_Nil(t *testing.T) {
	setupTestEnv(t)
	globalPool = nil
	req := httptest.NewRequest("GET", "/api/network/global-pool/stats", nil)
	w := httptest.NewRecorder()
	handleGlobalPoolStats(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB3_HandlePublicKeyQuotaStatus_Nil(t *testing.T) {
	setupTestEnv(t)
	publicQuota = nil
	req := httptest.NewRequest("GET", "/api/network/public-key-quota", nil)
	w := httptest.NewRecorder()
	handlePublicKeyQuotaStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["enabled"] != false {
		t.Error("expected enabled=false")
	}
}

// ============================================================
// update.go — Pure functions
// ============================================================

func TestHB3_IsInFlightPhase(t *testing.T) {
	if !isInFlightPhase(PhaseDownloading) {
		t.Error("downloading should be in-flight")
	}
	if !isInFlightPhase(PhaseReplacing) {
		t.Error("replacing should be in-flight")
	}
	if !isInFlightPhase(PhaseRestarting) {
		t.Error("restarting should be in-flight")
	}
	if isInFlightPhase(PhaseIdle) {
		t.Error("idle should not be in-flight")
	}
	if isInFlightPhase(PhaseSuccess) {
		t.Error("success should not be in-flight")
	}
	if isInFlightPhase(PhaseFailed) {
		t.Error("failed should not be in-flight")
	}
}

func TestHB3_ValidTimestamp(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	if !validTimestamp(now) {
		t.Error("current time should be valid")
	}
}

func TestHB3_ValidTimestamp_Stale(t *testing.T) {
	stale := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	if validTimestamp(stale) {
		t.Error("10-minute-old timestamp should be invalid")
	}
}

func TestHB3_ValidTimestamp_Invalid(t *testing.T) {
	if validTimestamp("not-a-time") {
		t.Error("invalid string should not be valid timestamp")
	}
}

func TestHB3_DedupeStrings(t *testing.T) {
	in := []string{"a", "b", "a", "", "c", "b"}
	out := dedupeStrings(in)
	if len(out) != 3 {
		t.Errorf("expected 3, got %d: %v", len(out), out)
	}
}

func TestHB3_DedupeStrings_Empty(t *testing.T) {
	out := dedupeStrings(nil)
	if len(out) != 0 {
		t.Errorf("expected 0, got %d", len(out))
	}
}

func TestHB3_ShortNodeID(t *testing.T) {
	if shortNodeID("short") != "short" {
		t.Error("short IDs should be unchanged")
	}
	longID := "abcdefghijk_lmno_pqrst"
	result := shortNodeID(longID)
	if !strings.Contains(result, "\u2026") {
		t.Errorf("expected ellipsis in short form, got %s", result)
	}
}

func TestHB3_PeerDisplayName(t *testing.T) {
	peer := NodeInfo{GitHubUser: "alice", Endpoint: "http://x", NodeID: "n1"}
	if peerDisplayName(peer) != "alice" {
		t.Errorf("expected alice, got %s", peerDisplayName(peer))
	}
	peer2 := NodeInfo{Endpoint: "http://x", NodeID: "n1"}
	if peerDisplayName(peer2) != "http://x" {
		t.Errorf("expected endpoint, got %s", peerDisplayName(peer2))
	}
	peer3 := NodeInfo{NodeID: "verylongnodeidthatislong"}
	result := peerDisplayName(peer3)
	if result == "" {
		t.Error("expected non-empty display name")
	}
}

func TestHB3_UpdateManager_WritePending(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{
			Env:     "local",
			IsLocal: true,
			Role:    "origin",
			Phase:   PhaseIdle,
		},
		peers:   make(map[string]UpdateStatus),
		cache:   &versionCache{},
		dataDir: dir,
	}
	um.writePending("v9.9.9")
	pendingPath := dir + "/" + updatePendingFile
	data, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatalf("expected pending file to exist: %v", err)
	}
	if !strings.Contains(string(data), "v9.9.9") {
		t.Errorf("expected file to contain v9.9.9, got %s", string(data))
	}
}

func TestHB3_UpdateManager_ListStatuses(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{
			Env:     "local",
			IsLocal: true,
			Role:    "origin",
			Phase:   PhaseIdle,
		},
		peers:   make(map[string]UpdateStatus),
		cache:   &versionCache{},
		dataDir: dir,
	}
	statuses := um.ListStatuses()
	if len(statuses) != 1 {
		t.Errorf("expected 1 status, got %d", len(statuses))
	}
}

func TestHB3_UpdateManager_SetReportBack(t *testing.T) {
	dir := t.TempDir()
	um := &UpdateManager{
		local: UpdateStatus{Env: "local", Phase: PhaseIdle},
		peers: make(map[string]UpdateStatus),
		cache: &versionCache{},
		dataDir: dir,
	}
	sig := UpdateSignal{BroadcastBy: "node1", TargetVersion: "v1.0"}
	um.setReportBack(sig)
	if um.reportBack == nil {
		t.Error("expected report back to be set")
	}
	um.clearReportBack()
	if um.reportBack != nil {
		t.Error("expected report back to be cleared")
	}
}

// ============================================================
// update.go — Handlers
// ============================================================

func TestHB3_HandleAdminVersionLatest_Nil(t *testing.T) {
	setupTestEnv(t)
	updateManager = nil
	req := httptest.NewRequest("GET", "/api/admin/version/latest", nil)
	w := httptest.NewRecorder()
	handleAdminVersionLatest(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleAdminUpdateStart_Nil(t *testing.T) {
	setupTestEnv(t)
	updateManager = nil
	req := httptest.NewRequest("POST", "/api/admin/update/start", nil)
	w := httptest.NewRecorder()
	handleAdminUpdateStart(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB3_HandleAdminUpdateStatus_Nil(t *testing.T) {
	setupTestEnv(t)
	updateManager = nil
	req := httptest.NewRequest("GET", "/api/admin/update/status", nil)
	w := httptest.NewRecorder()
	handleAdminUpdateStatus(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// ============================================================
// network_region_impl.go — RegionManager methods
// ============================================================

func TestHB3_RegionCanonical(t *testing.T) {
	if regionCanonical("ap") != RegionAsiaPacific {
		t.Error("expected ap -> asia-pacific")
	}
	if regionCanonical("asia") != RegionAsiaPacific {
		t.Error("expected asia -> asia-pacific")
	}
	if regionCanonical("eu") != RegionEurope {
		t.Error("expected eu -> europe")
	}
	if regionCanonical("europe") != RegionEurope {
		t.Error("expected europe -> europe")
	}
	if regionCanonical("us") != RegionAmericas {
		t.Error("expected us -> americas")
	}
	if regionCanonical("americas") != RegionAmericas {
		t.Error("expected americas -> americas")
	}
	if regionCanonical("mars") != RegionUnknown {
		t.Error("expected mars -> unknown")
	}
}

func TestHB3_RegionManager_NewRegionManager(t *testing.T) {
	rm := NewRegionManager()
	if rm == nil {
		t.Fatal("expected non-nil manager")
	}
	if len(rm.nodes) != 0 {
		t.Error("expected empty nodes map")
	}
}

func TestHB3_RegionManager_GetNodesByRegion(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "eu", "", 0, 0)
	rm.RegisterNodeSelfReport("n3", "ap", "", 0, 0)
	apNodes := rm.GetNodesByRegion(RegionAsiaPacific)
	if len(apNodes) != 2 {
		t.Errorf("expected 2 AP nodes, got %d", len(apNodes))
	}
	euNodes := rm.GetNodesByRegion(RegionEurope)
	if len(euNodes) != 1 {
		t.Errorf("expected 1 EU node, got %d", len(euNodes))
	}
}

func TestHB3_RegionManager_GetNodesByRegion_Alias(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	nodes := rm.GetNodesByRegion("asia")
	if len(nodes) != 1 {
		t.Errorf("expected 1 node via alias, got %d", len(nodes))
	}
}

func TestHB3_RegionManager_UpdateAndGetConfig(t *testing.T) {
	rm := NewRegionManager()
	cfg := DefaultRegionConfig()
	cfg.PreferLocal = false
	rm.UpdateConfig(cfg)
	got := rm.GetConfig()
	if got.PreferLocal != false {
		t.Error("expected PreferLocal=false after update")
	}
}

func TestHB3_RegionManager_ProcessHeartbeatRegion_NilInfo(t *testing.T) {
	rm := NewRegionManager()
	rm.ProcessHeartbeatRegion("n1", nil, "8.8.8.8")
	nr := rm.GetNodeRegion("n1")
	if nr == nil {
		t.Fatal("expected node region")
	}
	if nr.Source != "ip_detect" {
		t.Errorf("expected ip_detect, got %s", nr.Source)
	}
}

func TestHB3_RegionManager_ProcessHeartbeatRegion_EmptyBoth(t *testing.T) {
	rm := NewRegionManager()
	rm.ProcessHeartbeatRegion("n1", nil, "")
	nr := rm.GetNodeRegion("n1")
	if nr != nil {
		t.Error("expected nil when both info and ip are empty")
	}
}

func TestHB3_RegionDistance_Same(t *testing.T) {
	if regionDistance(RegionAsiaPacific, RegionAsiaPacific) != 0 {
		t.Error("expected 0 for same region")
	}
}

func TestHB3_RegionCenter_Values(t *testing.T) {
	lat, lon := regionCenter(RegionAsiaPacific)
	if lat == 0 && lon == 0 {
		t.Error("expected non-zero center for AP")
	}
	lat, lon = regionCenter(RegionAmericas)
	if lat == 0 && lon == 0 {
		t.Error("expected non-zero center for Americas")
	}
}

func TestHB3_AllRegions_Four(t *testing.T) {
	regions := AllRegions()
	if len(regions) != 4 {
		t.Errorf("expected 4 regions, got %d", len(regions))
	}
}

func TestHB3_ExtractRemoteIP_XFF(t *testing.T) {
	old := trustedReverseProxy
	trustedReverseProxy = true
	t.Cleanup(func() { trustedReverseProxy = old })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	ip := extractRemoteIP(req)
	if ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}
}

func TestHB3_ExtractRemoteIP_XRealIP(t *testing.T) {
	old := trustedReverseProxy
	trustedReverseProxy = true
	t.Cleanup(func() { trustedReverseProxy = old })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "9.8.7.6")
	ip := extractRemoteIP(req)
	if ip != "9.8.7.6" {
		t.Errorf("expected 9.8.7.6, got %s", ip)
	}
}

func TestHB3_ExtractRemoteIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	ip := extractRemoteIP(req)
	if ip != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", ip)
	}
}

// ============================================================
// message.go — MessageManager methods
// ============================================================

func TestHB3_ValidMsgType(t *testing.T) {
	if !validMsgType("request") {
		t.Error("request should be valid")
	}
	if !validMsgType("collaboration") {
		t.Error("collaboration should be valid")
	}
	if !validMsgType("system") {
		t.Error("system should be valid")
	}
	if !validMsgType("general") {
		t.Error("general should be valid")
	}
	if validMsgType("invalid") {
		t.Error("invalid should not be valid")
	}
	if validMsgType("") {
		t.Error("empty should not be valid")
	}
}

func TestHB3_GenerateMsgID(t *testing.T) {
	id, err := generateMsgID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Errorf("expected 32-char hex ID, got %d chars", len(id))
	}
	id2, _ := generateMsgID()
	if id == id2 {
		t.Error("IDs should be unique")
	}
}

func TestHB3_MessageManager_GetInbox_Empty(t *testing.T) {
	m := &MessageManager{inbox: make([]FederationMessage, 0), outbox: make([]FederationMessage, 0)}
	msgs := m.GetInbox(10)
	if len(msgs) != 0 {
		t.Errorf("expected 0, got %d", len(msgs))
	}
}

func TestHB3_MessageManager_GetOutbox_Empty(t *testing.T) {
	m := &MessageManager{inbox: make([]FederationMessage, 0), outbox: make([]FederationMessage, 0)}
	msgs := m.GetOutbox(10)
	if len(msgs) != 0 {
		t.Errorf("expected 0, got %d", len(msgs))
	}
}

func TestHB3_MessageManager_GetInbox_Limit(t *testing.T) {
	m := &MessageManager{
		inbox: []FederationMessage{
			{ID: "1", Subject: "a"},
			{ID: "2", Subject: "b"},
			{ID: "3", Subject: "c"},
		},
	}
	msgs := m.GetInbox(2)
	if len(msgs) != 2 {
		t.Errorf("expected 2, got %d", len(msgs))
	}
	if msgs[0].ID != "3" {
		t.Errorf("expected most recent first, got %s", msgs[0].ID)
	}
}

func TestHB3_MessageManager_GetOutbox_Limit(t *testing.T) {
	m := &MessageManager{
		outbox: []FederationMessage{
			{ID: "1", Subject: "a"},
			{ID: "2", Subject: "b"},
		},
	}
	msgs := m.GetOutbox(1)
	if len(msgs) != 1 {
		t.Errorf("expected 1, got %d", len(msgs))
	}
}

func TestHB3_MessageManager_MarkAsRead(t *testing.T) {
	m := &MessageManager{
		inbox: []FederationMessage{
			{ID: "1", Read: false},
			{ID: "2", Read: false},
		},
		dataDir: t.TempDir(),
	}
	if !m.MarkAsRead("1") {
		t.Error("expected true for existing message")
	}
	if m.MarkAsRead("nonexistent") {
		t.Error("expected false for nonexistent message")
	}
}

func TestHB3_MessageManager_GetUnreadCount(t *testing.T) {
	m := &MessageManager{
		inbox: []FederationMessage{
			{ID: "1", Read: true},
			{ID: "2", Read: false},
			{ID: "3", Read: false},
		},
	}
	count := m.GetUnreadCount()
	if count != 2 {
		t.Errorf("expected 2 unread, got %d", count)
	}
}

func TestHB3_MessageManager_GetUnreadCount_AllRead(t *testing.T) {
	m := &MessageManager{
		inbox: []FederationMessage{
			{ID: "1", Read: true},
		},
	}
	count := m.GetUnreadCount()
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

// ============================================================
// message.go — Handlers
// ============================================================

func TestHB3_HandleSendMessage_MethodNotAllowed(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	req := httptest.NewRequest("GET", "/federation/message/send", nil)
	w := httptest.NewRecorder()
	handleSendMessage(w, req)
	if w.Code != 405 {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHB3_HandleSendMessage_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	req := httptest.NewRequest("POST", "/federation/message/send", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	handleSendMessage(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleSendMessage_MissingToNode(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	body := `{"subject":"hi","body":"hello"}`
	req := httptest.NewRequest("POST", "/federation/message/send", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleSendMessage(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleReceiveMessage_MethodNotAllowed(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	req := httptest.NewRequest("GET", "/federation/message", nil)
	w := httptest.NewRecorder()
	handleReceiveMessage(w, req)
	if w.Code != 405 {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHB3_HandleReceiveMessage_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	req := httptest.NewRequest("POST", "/federation/message", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	handleReceiveMessage(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleGetInbox(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	req := httptest.NewRequest("GET", "/federation/messages/inbox", nil)
	w := httptest.NewRecorder()
	handleGetInbox(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB3_HandleGetOutbox(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	req := httptest.NewRequest("GET", "/federation/messages/outbox", nil)
	w := httptest.NewRecorder()
	handleGetOutbox(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB3_HandleMarkAsRead_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	req := httptest.NewRequest("POST", "/federation/messages/read", strings.NewReader("bad"))
	w := httptest.NewRecorder()
	handleMarkAsRead(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleMarkAsRead_MissingID(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	body := `{"message_id":""}`
	req := httptest.NewRequest("POST", "/federation/messages/read", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleMarkAsRead(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB3_HandleMarkAsRead_NotFound(t *testing.T) {
	setupTestEnv(t)
	dir := t.TempDir()
	initMessages(dir)
	body := `{"message_id":"nonexistent"}`
	req := httptest.NewRequest("POST", "/federation/messages/read", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleMarkAsRead(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ============================================================
// invite.go — EncodeInvite, DecodeInvite, inviteManager
// ============================================================

func TestHB3_EncodeDecodeInvite_Roundtrip_Batch3(t *testing.T) {
	inv := &FederationInvite{
		NetworkID:  "net1",
		Inviter:    "node1",
		InviteePub: "*",
		Endpoint:   "http://example.com",
		ExpiresAt:  time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
		Type:       FederationInvitePublic,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Signature:  "dGVzdA==",
	}
	encoded, err := EncodeInvite(inv)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeInvite(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.NetworkID != inv.NetworkID {
		t.Errorf("expected %s, got %s", inv.NetworkID, decoded.NetworkID)
	}
	if decoded.Inviter != inv.Inviter {
		t.Errorf("expected %s, got %s", inv.Inviter, decoded.Inviter)
	}
}

func TestHB3_DecodeInvite_InvalidBase64_Batch3(t *testing.T) {
	_, err := DecodeInvite("!!!not-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestHB3_DecodeInvite_InvalidJSON(t *testing.T) {
	encoded := "e30=" // base64 of "{}"
	_, err := DecodeInvite(encoded)
	if err != nil {
		t.Error("valid JSON object should decode, even with empty fields")
	}
}

func TestHB3_InviteManager_MarkUsed(t *testing.T) {
	dir := t.TempDir()
	m := &inviteManager{
		issued:  make(map[string]*FederationInvite),
		used:    make(map[string]bool),
		dataDir: dir,
	}
	inv := &FederationInvite{
		NetworkID: "net1",
		Inviter:   "node1",
		Type:      FederationInviteDirected,
	}
	payload := FederationInvitePayload{
		NetworkID: inv.NetworkID,
		Inviter:   inv.Inviter,
		Type:      inv.Type,
	}
	inviteID := m.inviteID(payload)
	m.issued[inviteID] = inv
	m.MarkUsed(inv)
	if !m.used[inviteID] {
		t.Error("expected invite to be marked as used")
	}
}

func TestHB3_InviteManager_GetInvites(t *testing.T) {
	dir := t.TempDir()
	m := &inviteManager{
		issued:  make(map[string]*FederationInvite),
		used:    make(map[string]bool),
		dataDir: dir,
	}
	inv := &FederationInvite{NetworkID: "net1", Inviter: "node1"}
	m.issued["inv-1"] = inv
	result := m.GetInvites()
	if len(result) != 1 {
		t.Errorf("expected 1 invite, got %d", len(result))
	}
}

// ============================================================
// config.go — Config methods
// ============================================================

func TestHB3_Config_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	initConfig(filepath.Join(dir, "config.json"))
	cfg.Set("test_key", "test_value")
	got := cfg.Get("test_key", "")
	if got != "test_value" {
		t.Errorf("expected test_value, got %s", got)
	}
}

func TestHB3_Config_GetNonexistent(t *testing.T) {
	dir := t.TempDir()
	initConfig(filepath.Join(dir, "config.json"))
	got := cfg.Get("nonexistent_key", "default_val")
	if got != "default_val" {
		t.Errorf("expected default_val, got %s", got)
	}
}

func TestHB3_Config_SetMany(t *testing.T) {
	dir := t.TempDir()
	initConfig(filepath.Join(dir, "config.json"))
	cfg.SetMany(map[string]any{"k1": "v1", "k2": "v2"})
	if cfg.Get("k1", "") != "v1" {
		t.Error("expected k1=v1")
	}
	if cfg.Get("k2", "") != "v2" {
		t.Error("expected k2=v2")
	}
}

func TestHB3_Config_Masked(t *testing.T) {
	dir := t.TempDir()
	initConfig(filepath.Join(dir, "config.json"))
	cfg.Set("proxy_api_key", "sk-1234567890abcdef")
	masked := cfg.Masked()
	if _, ok := masked["proxy_api_key"]; ok {
		t.Error("expected proxy_api_key to be removed from masked output")
	}
	if _, ok := masked["proxy_api_key_masked"]; !ok {
		t.Error("expected proxy_api_key_masked to exist")
	}
}

// ============================================================
// algorithm_governance.go — AlgorithmGovernor methods
// ============================================================

func TestHB3_ProposalStatus_IsTerminal(t *testing.T) {
	if ProposalStatusOpen.isTerminal() {
		t.Error("open should not be terminal")
	}
	if !ProposalStatusPassed.isTerminal() {
		t.Error("passed should be terminal")
	}
	if !ProposalStatusRejected.isTerminal() {
		t.Error("rejected should be terminal")
	}
	if !ProposalStatusClosed.isTerminal() {
		t.Error("closed should be terminal")
	}
}

func TestHB3_VoteChoice_IsValid(t *testing.T) {
	if !VoteYes.isValid() {
		t.Error("yes should be valid")
	}
	if !VoteNo.isValid() {
		t.Error("no should be valid")
	}
	if !VoteAbstain.isValid() {
		t.Error("abstain should be valid")
	}
	if VoteChoice("maybe").isValid() {
		t.Error("maybe should not be valid")
	}
}

func TestHB3_AlgorithmGovernor_CreateProposal_EmptyTitle(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	_, err := governor.CreateProposal("", "desc", "admin", "", nil)
	if err == nil {
		t.Error("expected error for empty title")
	}
}

func TestHB3_AlgorithmGovernor_CreateProposal_Success(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, err := governor.CreateProposal("Test Proposal", "description", "admin", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Error("expected non-empty ID")
	}
	if p.Status != ProposalStatusOpen {
		t.Errorf("expected open, got %s", p.Status)
	}
}

func TestHB3_AlgorithmGovernor_CastVote_InvalidChoice(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("Test", "desc", "admin", "", nil)
	_, err := governor.CastVote(p.ID, "voter1", "", "maybe", "")
	if err == nil {
		t.Error("expected error for invalid choice")
	}
}

func TestHB3_AlgorithmGovernor_CastVote_NotFound(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	_, err := governor.CastVote("nonexistent", "voter1", "", "yes", "")
	if err == nil {
		t.Error("expected error for nonexistent proposal")
	}
}

func TestHB3_AlgorithmGovernor_CastVote_Success(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("Test", "desc", "admin", "", nil)
	updated, err := governor.CastVote(p.ID, "voter1", "Voter One", "yes", "looks good")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Votes) != 1 {
		t.Errorf("expected 1 vote, got %d", len(updated.Votes))
	}
}

func TestHB3_AlgorithmGovernor_CastVote_Deduplicate(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("Test", "desc", "admin", "", nil)
	governor.CastVote(p.ID, "voter1", "", "yes", "")
	updated, _ := governor.CastVote(p.ID, "voter1", "", "no", "changed mind")
	if len(updated.Votes) != 1 {
		t.Errorf("expected 1 vote after dedup, got %d", len(updated.Votes))
	}
	if updated.Votes[0].Choice != "no" {
		t.Errorf("expected updated choice 'no', got %s", updated.Votes[0].Choice)
	}
}

func TestHB3_AlgorithmGovernor_ResolveProposal_NotFound(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	_, err := governor.ResolveProposal("nonexistent", "admin", "", ProposalStatusPassed)
	if err == nil {
		t.Error("expected error for nonexistent proposal")
	}
}

func TestHB3_AlgorithmGovernor_ResolveProposal_InvalidStatus(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("Test", "desc", "admin", "", nil)
	_, err := governor.ResolveProposal(p.ID, "admin", "", ProposalStatusOpen)
	if err == nil {
		t.Error("expected error for non-terminal status")
	}
}

func TestHB3_AlgorithmGovernor_ResolveProposal_Success(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("Test", "desc", "admin", "", nil)
	resolved, err := governor.ResolveProposal(p.ID, "admin", "looks good", ProposalStatusPassed)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProposalStatusPassed {
		t.Errorf("expected passed, got %s", resolved.Status)
	}
}

func TestHB3_AlgorithmGovernor_ResolveProposal_Idempotent(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("Test", "desc", "admin", "", nil)
	governor.ResolveProposal(p.ID, "admin", "", ProposalStatusPassed)
	resolved, err := governor.ResolveProposal(p.ID, "admin", "", ProposalStatusRejected)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProposalStatusPassed {
		t.Error("already-resolved proposal should remain unchanged")
	}
}

func TestHB3_AlgorithmGovernor_CastVote_OnClosedProposal(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("Test", "desc", "admin", "", nil)
	governor.ResolveProposal(p.ID, "admin", "", ProposalStatusClosed)
	_, err := governor.CastVote(p.ID, "voter1", "", "yes", "")
	if err == nil {
		t.Error("expected error voting on closed proposal")
	}
}

func TestHB3_AlgorithmGovernor_GetProposal(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("Test", "desc", "admin", "", nil)
	found, ok := governor.GetProposal(p.ID)
	if !ok || found == nil {
		t.Error("expected to find proposal")
	}
	_, ok = governor.GetProposal("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent proposal")
	}
}

func TestHB3_AlgorithmGovernor_ListProposals(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	governor.CreateProposal("P1", "desc", "admin", "", nil)
	governor.CreateProposal("P2", "desc", "admin", "", nil)
	all := governor.ListProposals("")
	if len(all) != 2 {
		t.Errorf("expected 2, got %d", len(all))
	}
	open := governor.ListProposals("open")
	if len(open) != 2 {
		t.Errorf("expected 2 open, got %d", len(open))
	}
}

func TestHB3_AlgorithmGovernor_ListProposals_Filter(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	p, _ := governor.CreateProposal("P1", "desc", "admin", "", nil)
	governor.ResolveProposal(p.ID, "admin", "", ProposalStatusPassed)
	passed := governor.ListProposals("passed")
	if len(passed) != 1 {
		t.Errorf("expected 1 passed, got %d", len(passed))
	}
	open := governor.ListProposals("open")
	if len(open) != 0 {
		t.Errorf("expected 0 open, got %d", len(open))
	}
}

func TestHB3_AlgorithmGovernor_GetHistory(t *testing.T) {
	dir := t.TempDir()
	initAlgorithmGovernance(dir)
	governor.CreateProposal("P1", "desc", "admin", "", nil)
	history := governor.GetHistory()
	if len(history) == 0 {
		t.Error("expected history entries")
	}
}

func TestHB3_AlgorithmProposal_Tally(t *testing.T) {
	p := &AlgorithmProposal{
		Votes: []AlgorithmVote{
			{Choice: "yes"},
			{Choice: "yes"},
			{Choice: "no"},
			{Choice: "abstain"},
		},
	}
	tally := p.Tally()
	if tally.Yes != 2 || tally.No != 1 || tally.Abstain != 1 || tally.Total != 4 {
		t.Errorf("unexpected tally: %+v", tally)
	}
}

func TestHB3_AlgorithmProposal_Tally_Empty(t *testing.T) {
	p := &AlgorithmProposal{}
	tally := p.Tally()
	if tally.Total != 0 {
		t.Errorf("expected 0 total, got %d", tally.Total)
	}
}

func TestHB3_ToProposalView(t *testing.T) {
	p := &AlgorithmProposal{
		ID:     "p1",
		Title:  "Test",
		Status: ProposalStatusOpen,
		Votes:  []AlgorithmVote{{Choice: "yes"}},
	}
	view := toProposalView(p)
	if view.Tally.Yes != 1 {
		t.Errorf("expected tally yes=1, got %d", view.Tally.Yes)
	}
}

func TestHB3_TrimSpace(t *testing.T) {
	if trimSpace("  hello  ") != "hello" {
		t.Error("expected trimmed")
	}
	if trimSpace("") != "" {
		t.Error("expected empty")
	}
	if trimSpace("   ") != "" {
		t.Error("expected empty for whitespace")
	}
}

func TestHB3_NowRFC3339(t *testing.T) {
	s := nowRFC3339()
	if s == "" {
		t.Error("expected non-empty time string")
	}
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		t.Errorf("expected valid RFC3339, got parse error: %v", err)
	}
}

func TestHB3_NewProposalID(t *testing.T) {
	id := newProposalID()
	if id == "" {
		t.Error("expected non-empty proposal ID")
	}
	if !strings.HasPrefix(id, "prop-") {
		t.Errorf("expected prop- prefix, got %s", id)
	}
}
