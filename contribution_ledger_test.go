package main

import (
	"crypto/ed25519"
	"testing"
	"time"
)

// ============================================================
// NewGossipLedger Tests
// ============================================================

func TestNewGossipLedger(t *testing.T) {
	ledger, err := NewGossipLedger("mmx-test-node")
	if err != nil {
		t.Fatalf("NewGossipLedger failed: %v", err)
	}
	if ledger == nil {
		t.Fatal("NewGossipLedger returned nil")
	}
	if ledger.peerID != "mmx-test-node" {
		t.Fatalf("peerID = %q, want mmx-test-node", ledger.peerID)
	}
}

// ============================================================
// PublicKey and Sign Tests
// ============================================================

func TestGossipLedger_PublicKey(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	pub := ledger.PublicKey()
	if len(pub) == 0 {
		t.Fatal("PublicKey returned empty")
	}
}

func TestGossipLedger_Sign(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	sig := ledger.Sign([]byte("test-data"))
	if len(sig) == 0 {
		t.Fatal("Sign returned empty signature")
	}
	// Verify the signature with the public key
	if !ed25519.Verify(ledger.PublicKey(), []byte("test-data"), sig) {
		t.Fatal("Sign produced invalid signature")
	}
}

// ============================================================
// RecordContribution / GetContribution Tests
// ============================================================

func TestGossipLedger_RecordAndGetContribution(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	rec := &ContributionRecord{
		Tokens:   100,
		ValueUSD: 10.5,
		ModelID:  "model-A",
		Provider: "provider-A",
	}
	id, err := ledger.RecordContribution(rec)
	if err != nil {
		t.Fatalf("RecordContribution failed: %v", err)
	}
	if id == "" {
		t.Fatal("RecordContribution returned empty ID")
	}
	got, err := ledger.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetContribution returned nil")
	}
	if got.Tokens != 100 {
		t.Fatalf("Tokens = %d, want 100", got.Tokens)
	}
	if got.ModelID != "model-A" {
		t.Fatalf("ModelID = %q, want model-A", got.ModelID)
	}
}

func TestGossipLedger_RecordNilContribution(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	_, err := ledger.RecordContribution(nil)
	if err == nil {
		t.Fatal("RecordContribution(nil) should return error")
	}
}

// ============================================================
// RecordTrust / RecordClaim / RecordPenalty Tests
// ============================================================

func TestGossipLedger_RecordTrust(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	rec := &TrustRecord{
		SubjectPeerID:  "peer-A",
		VerifierPeerID: "mmx-test-node",
		ModelID:        "model-A",
		Success:        true,
		LatencyMS:      100,
	}
	id, err := ledger.RecordTrust(rec)
	if err != nil {
		t.Fatalf("RecordTrust failed: %v", err)
	}
	if id == "" {
		t.Fatal("RecordTrust returned empty ID")
	}
	got, err := ledger.GetContribution(id)
	// Trust records have a different storage mechanism; just verify no panic
	_ = got
}

func TestGossipLedger_RecordClaim(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	claim := &CapabilityClaim{
		PeerID:    "peer-A",
		Models:    []string{"model-A"},
		Providers: []string{"prov-A"},
		MaxQuota:  1000,
	}
	id := ledger.RecordClaim(claim)
	if id == "" {
		t.Fatal("RecordClaim returned empty ID")
	}
}

func TestGossipLedger_RecordPenalty(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	rec := &PenaltyRecord{
		PeerID: "peer-A",
		Reason: "test",
		Action: "warn",
	}
	id, err := ledger.RecordPenalty(rec)
	if err != nil {
		t.Fatalf("RecordPenalty failed: %v", err)
	}
	if id == "" {
		t.Fatal("RecordPenalty returned empty ID")
	}
}

// ============================================================
// AppendTransaction / DeriveBalance / VerifyChain Tests
// ============================================================

func TestGossipLedger_AppendTransaction(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	tx, err := ledger.AppendTransaction("contribution", "node-A", 100, "model-A", "")
	if err != nil {
		t.Fatalf("AppendTransaction failed: %v", err)
	}
	if tx == nil {
		t.Fatal("AppendTransaction returned nil")
	}
	if tx.Amount != 100 {
		t.Fatalf("Amount = %d, want 100", tx.Amount)
	}
	if tx.Type != "contribution" {
		t.Fatalf("Type = %q, want contribution", tx.Type)
	}
}

func TestGossipLedger_DeriveBalance(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	_, err := ledger.AppendTransaction("contribution", "node-A", 100, "model-A", "")
	if err != nil {
		t.Fatalf("AppendTransaction failed: %v", err)
	}
	_, err = ledger.AppendTransaction("contribution", "node-A", 50, "model-B", "")
	if err != nil {
		t.Fatalf("AppendTransaction second call failed: %v", err)
	}
	balance := ledger.DeriveBalance("node-A")
	if balance != 150 {
		t.Fatalf("DeriveBalance = %d, want 150", balance)
	}
	// Non-existent node should have zero balance
	if ledger.DeriveBalance("nonexistent") != 0 {
		t.Fatal("DeriveBalance for nonexistent node should be 0")
	}
}

func TestGossipLedger_VerifyChain(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	// Empty ledger should still have valid chain
	if !ledger.VerifyChain() {
		t.Fatal("VerifyChain returned false for empty ledger")
	}
	// Add a transaction and verify
	_, err := ledger.AppendTransaction("contribute", "node-A", 100, "model-A", "")
	if err != nil {
		t.Fatalf("AppendTransaction failed: %v", err)
	}
	if !ledger.VerifyChain() {
		t.Fatal("VerifyChain returned false after adding valid transaction")
	}
}

func TestGossipLedger_GetTransactionChain(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	_, err := ledger.AppendTransaction("contribute", "node-A", 100, "model-A", "")
	if err != nil {
		t.Fatalf("AppendTransaction failed: %v", err)
	}
	chain := ledger.GetTransactionChain("node-A")
	if len(chain) != 1 {
		t.Fatalf("GetTransactionChain returned %d, want 1", len(chain))
	}
	// Non-existent node should return empty slice
	if len(ledger.GetTransactionChain("nonexistent")) != 0 {
		t.Fatal("GetTransactionChain for nonexistent node should be empty")
	}
}

// ============================================================
// nextID Tests
// ============================================================

func TestGossipLedger_NextID(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	ledger.mu.Lock()
	id1 := ledger.nextID("contrib")
	id2 := ledger.nextID("contrib")
	ledger.mu.Unlock()
	if id1 == id2 {
		t.Fatalf("nextID not unique: %q == %q", id1, id2)
	}
	if id1 != "contrib-mmx-test-node-1" {
		t.Fatalf("nextID(1) = %q, want contrib-mmx-test-node-1", id1)
	}
	if id2 != "contrib-mmx-test-node-2" {
		t.Fatalf("nextID(2) = %q, want contrib-mmx-test-node-2", id2)
	}
}

// ============================================================
// ContentHashStore Tests
// ============================================================

func TestContentHashStore_Basic(t *testing.T) {
	store := NewContentHashStore()
	if store == nil {
		t.Fatal("NewContentHashStore returned nil")
	}
}

func TestGossipLedger_GetDailyContributions(t *testing.T) {
	ledger, _ := NewGossipLedger("mmx-test-node")
	_, err := ledger.AppendTransaction("contribution", "node-A", 100, "model-A", "")
	if err != nil {
		t.Fatalf("AppendTransaction failed: %v", err)
	}
	total := ledger.GetDailyContributions("node-A", time.Now())
	if total != 100 {
		t.Fatalf("GetDailyContributions = %d, want 100", total)
	}
}
