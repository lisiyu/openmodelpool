package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setupLedgerTestEnv(t *testing.T) {
	t.Helper()
	env := setupTestEnv(t)
	node = newTestNodeIdentity(t, env.dir)
	t.Cleanup(func() { node = nil })

	gl, err := NewGossipLedger(node.NodeID())
	if err != nil {
		t.Fatalf("NewGossipLedger: %v", err)
	}
	contributionLedger = gl
	t.Cleanup(func() { contributionLedger = nil })

	capabilityVerifier = NewCapabilityVerifier(nil, 2)
	t.Cleanup(func() { capabilityVerifier = nil })
}

func TestCapabilityClaim_API_PostAndGet(t *testing.T) {
	setupLedgerTestEnv(t)

	body := `{"peer_id":"mmx-testpeer","models":["gpt-4o","claude-3-5-sonnet"],"providers":["openai","anthropic"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/network/capability/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCapabilityClaim(w, req)
	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "recorded" {
		t.Errorf("expected status=recorded, got %v", resp["status"])
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/network/capability/claims", nil)
	w2 := httptest.NewRecorder()
	handleCapabilityClaims(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("expected 200, got %d", w2.Code)
	}
	var resp2 map[string]any
	json.Unmarshal(w2.Body.Bytes(), &resp2)
	claims, ok := resp2["claims"].([]any)
	if !ok || len(claims) != 1 {
		t.Errorf("expected 1 claim, got %v", resp2["claims"])
	}
}

func TestCapabilityClaim_API_MissingPeerID(t *testing.T) {
	setupLedgerTestEnv(t)

	body := `{"models":["gpt-4o"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/network/capability/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCapabilityClaim(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCapabilityClaim_API_MissingModels(t *testing.T) {
	setupLedgerTestEnv(t)

	body := `{"peer_id":"mmx-testpeer"}`
	req := httptest.NewRequest(http.MethodPost, "/api/network/capability/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleCapabilityClaim(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCapabilityClaims_Empty(t *testing.T) {
	setupLedgerTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/network/capability/claims", nil)
	w := httptest.NewRecorder()
	handleCapabilityClaims(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["total"] != float64(0) {
		t.Errorf("expected 0 claims, got %v", resp["total"])
	}
}

func TestCapabilityVerify_NotFound(t *testing.T) {
	setupLedgerTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/network/capability/verify/mmx-nonexistent", nil)
	req.SetPathValue("peer_id", "mmx-nonexistent")
	w := httptest.NewRecorder()
	handleCapabilityVerify(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCapabilityVerify_Success(t *testing.T) {
	setupLedgerTestEnv(t)

	claim := &CapabilityClaim{
		PeerID:   "mmx-testpeer",
		Models:   []string{"gpt-4o"},
	}
	contributionLedger.RecordClaim(claim)

	req := httptest.NewRequest(http.MethodGet, "/api/network/capability/verify/mmx-testpeer", nil)
	req.SetPathValue("peer_id", "mmx-testpeer")
	w := httptest.NewRecorder()
	handleCapabilityVerify(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "verified" {
		t.Errorf("expected status=verified, got %v", resp["status"])
	}
}

func TestLedgerContributions_Empty(t *testing.T) {
	setupLedgerTestEnv(t)

	req := httptest.NewRequest(http.MethodGet, "/api/network/ledger/contributions", nil)
	w := httptest.NewRecorder()
	handleLedgerContributions(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestLedgerBalance(t *testing.T) {
	setupLedgerTestEnv(t)

	nodeID := node.NodeID()
	contributionLedger.AppendTransaction("contribution", nodeID, 5000, "gpt-4o", "req-1")
	contributionLedger.AppendTransaction("consumption", nodeID, 2000, "claude-3", "req-2")

	req := httptest.NewRequest(http.MethodGet, "/api/network/ledger/balance/"+nodeID, nil)
	req.SetPathValue("node_id", nodeID)
	w := httptest.NewRecorder()
	handleLedgerBalance(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["balance"] != float64(3000) {
		t.Errorf("expected balance=3000, got %v", resp["balance"])
	}
}

func TestLedgerTransactions(t *testing.T) {
	setupLedgerTestEnv(t)

	contributionLedger.AppendTransaction("contribution", node.NodeID(), 1000, "gpt-4o", "req-1")

	req := httptest.NewRequest(http.MethodGet, "/api/network/ledger/transactions", nil)
	w := httptest.NewRecorder()
	handleLedgerTransactions(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["total"] != float64(1) {
		t.Errorf("expected 1 transaction, got %v", resp["total"])
	}
	if resp["chain_valid"] != true {
		t.Errorf("expected chain_valid=true, got %v", resp["chain_valid"])
	}
}

func TestLedger_NilLedger_Returns503(t *testing.T) {
	contributionLedger = nil
	capabilityVerifier = nil

	req := httptest.NewRequest(http.MethodGet, "/api/network/capability/claims", nil)
	w := httptest.NewRecorder()
	handleCapabilityClaims(w, req)
	if w.Code != 503 {
		t.Errorf("expected 503, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/network/ledger/contributions", nil)
	w2 := httptest.NewRecorder()
	handleLedgerContributions(w2, req2)
	if w2.Code != 503 {
		t.Errorf("expected 503, got %d", w2.Code)
	}
}

func TestGossipLedger_SaveLoad(t *testing.T) {
	gl, err := NewGossipLedger("test-peer")
	if err != nil {
		t.Fatalf("NewGossipLedger: %v", err)
	}
	gl.RecordClaim(&CapabilityClaim{
		PeerID:   "test-peer",
		Models:   []string{"gpt-4o"},
	})
	gl.AppendTransaction("contribution", "test-peer", 1000, "gpt-4o", "req-1")

	dir := t.TempDir()
	path := dir + "/json"
	if err := gl.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	gl2, err := LoadGossipLedger(path)
	if err != nil {
		t.Fatalf("LoadGossipLedger: %v", err)
	}
	if gl2.PeerID() != "test-peer" {
		t.Errorf("expected peer_id=test-peer, got %s", gl2.PeerID())
	}
	claims := gl2.GetAllClaims()
	if len(claims) != 1 {
		t.Errorf("expected 1 claim, got %d", len(claims))
	}
	balance := gl2.DeriveBalance("test-peer")
	if balance != 1000 {
		t.Errorf("expected balance=1000, got %d", balance)
	}
}

func TestRecordContributionToLedger(t *testing.T) {
	setupLedgerTestEnv(t)
	recordContributionToLedger("mmx-peer1", "gpt-4o", "openai", 5000, map[string]any{"request_id": "req-1"})
	recordContributionToLedger("mmx-peer2", "claude-3", "anthropic", 3000, nil)

	contribs := contributionLedger.GetAllContributions()
	if len(contribs) != 2 {
		t.Errorf("expected 2 contributions, got %d", len(contribs))
	}

	txs := contributionLedger.GetAllTransactions()
	if len(txs) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(txs))
	}
	if !contributionLedger.VerifyChain() {
		t.Error("chain should be valid")
	}
}

func TestRecordConsumptionToLedger(t *testing.T) {
	setupLedgerTestEnv(t)
	recordConsumptionToLedger("gpt-4o", 2000, "req-2")

	txs := contributionLedger.GetAllTransactions()
	if len(txs) != 1 {
		t.Errorf("expected 1 transaction, got %d", len(txs))
	}
	if txs[0].Type != "consumption" {
		t.Errorf("expected type=consumption, got %s", txs[0].Type)
	}
}

func TestRecordContribution_NilLedger(t *testing.T) {
	contributionLedger = nil
	recordContributionToLedger("mmx-peer1", "gpt-4o", "openai", 5000, nil)
}

func TestRecordConsumption_NilLedger(t *testing.T) {
	contributionLedger = nil
	node = nil
	recordConsumptionToLedger("gpt-4o", 2000, "")
}

func TestLedgerBalance_ContributionMinusConsumption(t *testing.T) {
	setupLedgerTestEnv(t)

	nodeID := node.NodeID()
	contributionLedger.AppendTransaction("contribution", nodeID, 10000, "gpt-4o", "req-1")
	contributionLedger.AppendTransaction("contribution", nodeID, 5000, "claude-3", "req-2")
	contributionLedger.AppendTransaction("consumption", nodeID, 3000, "deepseek", "req-3")

	balance := contributionLedger.DeriveBalance(nodeID)
	if balance != 12000 {
		t.Errorf("expected balance=12000, got %d", balance)
	}
	if !contributionLedger.VerifyChain() {
		t.Error("chain should be valid after 3 transactions")
	}
}

func TestGossipSync_Merge(t *testing.T) {
	gl1, _ := NewGossipLedger("peer-1")
	gl2, _ := NewGossipLedger("peer-2")

	gl1.RecordContribution(&ContributionRecord{PeerID: "peer-1", ModelID: "gpt-4o", Tokens: 1000})
	gl1.RecordClaim(&CapabilityClaim{PeerID: "peer-1", Models: []string{"gpt-4o"}})

	contribs := gl1.GetAllContributions()
	claims := gl1.GetAllClaims()

	added := gl2.GossipSync(contribs, nil, claims, nil)
	if added != 2 {
		t.Errorf("expected 2 new records, got %d", added)
	}
	if len(gl2.GetAllContributions()) != 1 {
		t.Errorf("expected 1 contribution after sync, got %d", len(gl2.GetAllContributions()))
	}
	if len(gl2.GetAllClaims()) != 1 {
		t.Errorf("expected 1 claim after sync, got %d", len(gl2.GetAllClaims()))
	}

	added2 := gl2.GossipSync(contribs, nil, claims, nil)
	if added2 != 0 {
		t.Errorf("expected 0 new records on re-sync, got %d", added2)
	}
}

func TestCheckShareBoundary_AllowWhenNoRestrictions(t *testing.T) {
	nm := &NetworkManager{
		config: NetworkConfig{
			Mode:          NetworkModeShared,
			ShareBoundary: ShareBoundaryConfig{DailyContribCap: 0, ModelWhitelist: nil},
		},
	}
	ok, reason := nm.CheckShareBoundary("gpt-4o", 5000)
	if !ok {
		t.Errorf("expected allowed, got rejected: %s", reason)
	}
}

func TestCheckShareBoundary_DailyCapExceeded(t *testing.T) {
	nm := &NetworkManager{
		config: NetworkConfig{
			Mode:          NetworkModeShared,
			ShareBoundary: ShareBoundaryConfig{DailyContribCap: 1000},
		},
	}
	contributionLedger, _ = NewGossipLedger("self")
	defer func() { contributionLedger = nil }()
	contributionLedger.AppendTransaction("contribution", nm.GetNodeID(), 800, "gpt-4o", "req-1")

	ok, _ := nm.CheckShareBoundary("gpt-4o", 500)
	if ok {
		t.Errorf("expected rejected (800+500=1300 > cap 1000)")
	}
}

func TestCheckShareBoundary_DailyCapNotExceeded(t *testing.T) {
	nm := &NetworkManager{
		config: NetworkConfig{
			Mode:          NetworkModeShared,
			ShareBoundary: ShareBoundaryConfig{DailyContribCap: 10000},
		},
	}
	contributionLedger, _ = NewGossipLedger("self")
	defer func() { contributionLedger = nil }()
	contributionLedger.AppendTransaction("contribution", nm.GetNodeID(), 1000, "gpt-4o", "req-1")

	ok, reason := nm.CheckShareBoundary("gpt-4o", 5000)
	if !ok {
		t.Errorf("expected allowed, got rejected: %s", reason)
	}
}

func TestCheckShareBoundary_ModelWhitelist(t *testing.T) {
	nm := &NetworkManager{
		config: NetworkConfig{
			Mode:          NetworkModeShared,
			ShareBoundary: ShareBoundaryConfig{ModelWhitelist: []string{"gpt-4o", "claude-3"}},
		},
	}
	ok, reason := nm.CheckShareBoundary("gpt-4o", 100)
	if !ok {
		t.Errorf("expected allowed for whitelisted model, got: %s", reason)
	}
	ok2, reason2 := nm.CheckShareBoundary("deepseek-chat", 100)
	if ok2 {
		t.Errorf("expected rejected for non-whitelisted model")
	}
	if reason2 != "model not in whitelist" {
		t.Errorf("expected 'model not in whitelist', got: %s", reason2)
	}
}

func TestUpdateShareBoundary(t *testing.T) {
	nm := &NetworkManager{
		config:   NetworkConfig{Mode: NetworkModeShared, ShareBoundary: ShareBoundaryConfig{}},
		dataPath: t.TempDir() + "/network.json",
	}
	err := nm.UpdateShareBoundary(&ShareBoundaryConfig{
		DailyContribCap: 20000,
		ShareIdleOnly:   true,
		ModelWhitelist:  []string{"gpt-4o"},
	})
	if err != nil {
		t.Fatalf("UpdateShareBoundary: %v", err)
	}
	if nm.config.ShareBoundary.DailyContribCap != 20000 {
		t.Errorf("expected cap=20000, got %d", nm.config.ShareBoundary.DailyContribCap)
	}
	if !nm.config.ShareBoundary.ShareIdleOnly {
		t.Error("expected ShareIdleOnly=true")
	}
	if len(nm.config.ShareBoundary.ModelWhitelist) != 1 || nm.config.ShareBoundary.ModelWhitelist[0] != "gpt-4o" {
		t.Errorf("expected whitelist=[gpt-4o], got %v", nm.config.ShareBoundary.ModelWhitelist)
	}
}
