package ledger

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestLedger(t *testing.T) *GossipLedger {
	t.Helper()
	g, err := NewGossipLedger("test-peer-1")
	if err != nil {
		t.Fatalf("NewGossipLedger: %v", err)
	}
	return g
}

func sampleContribution() *ContributionRecord {
	return &ContributionRecord{
		PeerID:   "peer-alpha",
		ModelID:  "gpt-4",
		Provider: "openai",
		Tokens:   1500,
		ValueUSD: 0.045,
	}
}

// ---------------------------------------------------------------------------
// Test 1: Signature and verification
// ---------------------------------------------------------------------------

func TestSignatureAndVerify(t *testing.T) {
	g := newTestLedger(t)
	data := []byte("hello world")
	sig := g.Sign(data)

	if !VerifySignature(g.PublicKey(), data, sig) {
		t.Fatal("valid signature should verify")
	}

	// Tampered data should not verify.
	if VerifySignature(g.PublicKey(), []byte("tampered"), sig) {
		t.Fatal("tampered data should not verify")
	}
}

// ---------------------------------------------------------------------------
// Test 2: Ed25519 key pair generation
// ---------------------------------------------------------------------------

func TestKeyGeneration(t *testing.T) {
	g := newTestLedger(t)
	if len(g.PublicKey()) != ed25519.PublicKeySize {
		t.Fatalf("expected public key size %d, got %d", ed25519.PublicKeySize, len(g.PublicKey()))
	}
}

// ---------------------------------------------------------------------------
// Test 3: Record and retrieve contribution
// ---------------------------------------------------------------------------

func TestRecordAndGetContribution(t *testing.T) {
	g := newTestLedger(t)
	rec := sampleContribution()

	id, err := g.RecordContribution(rec)
	if err != nil {
		t.Fatalf("RecordContribution: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	got, err := g.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got.PeerID != "peer-alpha" {
		t.Fatalf("expected peer-alpha, got %s", got.PeerID)
	}
	if got.Tokens != 1500 {
		t.Fatalf("expected 1500 tokens, got %d", got.Tokens)
	}
	if len(got.Signature) == 0 {
		t.Fatal("expected signature to be set")
	}
}

// ---------------------------------------------------------------------------
// Test 4: Verify contribution signature
// ---------------------------------------------------------------------------

func TestVerifyContribution(t *testing.T) {
	g := newTestLedger(t)
	rec := sampleContribution()
	rec.PeerPublicKey = g.PublicKey()

	id, err := g.RecordContribution(rec)
	if err != nil {
		t.Fatalf("RecordContribution: %v", err)
	}

	ok, err := g.VerifyContribution(id)
	if err != nil {
		t.Fatalf("VerifyContribution: %v", err)
	}
	if !ok {
		t.Fatal("contribution signature should be valid")
	}
}

// ---------------------------------------------------------------------------
// Test 5: Get non-existent contribution
// ---------------------------------------------------------------------------

func TestGetNonExistent(t *testing.T) {
	g := newTestLedger(t)
	_, err := g.GetContribution("non-existent-id")
	if err == nil {
		t.Fatal("expected error for non-existent contribution")
	}
}

// ---------------------------------------------------------------------------
// Test 6: Get peer contributions
// ---------------------------------------------------------------------------

func TestGetPeerContributions(t *testing.T) {
	g := newTestLedger(t)

	for i := 0; i < 5; i++ {
		rec := sampleContribution()
		rec.ModelID = "model-" + string(rune('a'+i))
		_, err := g.RecordContribution(rec)
		if err != nil {
			t.Fatalf("RecordContribution %d: %v", i, err)
		}
	}

	contribs, err := g.GetPeerContributions("peer-alpha")
	if err != nil {
		t.Fatalf("GetPeerContributions: %v", err)
	}
	if len(contribs) != 5 {
		t.Fatalf("expected 5 contributions, got %d", len(contribs))
	}
}

// ---------------------------------------------------------------------------
// Test 7: IPFS store and retrieve (local fallback)
// ---------------------------------------------------------------------------

func TestIPFSStoreAndRetrieve(t *testing.T) {
	client := NewIPFSClient()
	data := []byte("test data for IPFS")

	cid, err := client.Store(data)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if !strings.HasPrefix(cid, "Qm") {
		t.Fatalf("expected CID prefix Qm, got %s", cid[:2])
	}

	retrieved, err := client.Retrieve(cid)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if string(retrieved) != string(data) {
		t.Fatalf("retrieved data mismatch: got %q, want %q", retrieved, data)
	}
}

// ---------------------------------------------------------------------------
// Test 8: IPFS JSON store and retrieve
// ---------------------------------------------------------------------------

func TestIPFSJSONStoreAndRetrieve(t *testing.T) {
	client := NewIPFSClient()

	type testPayload struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	original := testPayload{Name: "test", Value: 42}
	cid, err := client.StoreJSON(original)
	if err != nil {
		t.Fatalf("StoreJSON: %v", err)
	}

	var decoded testPayload
	err = client.RetrieveJSON(cid, &decoded)
	if err != nil {
		t.Fatalf("RetrieveJSON: %v", err)
	}
	if decoded.Name != "test" || decoded.Value != 42 {
		t.Fatalf("JSON mismatch: %+v", decoded)
	}
}

// ---------------------------------------------------------------------------
// Test 9: IOTA submit and verify (local fallback)
// ---------------------------------------------------------------------------

func TestIOTASubmitAndVerify(t *testing.T) {
	client := NewIOTAClient()
	data := []byte("iota test data")

	txHash, err := client.SubmitData(data, "TEST")
	if err != nil {
		t.Fatalf("SubmitData: %v", err)
	}
	if txHash == "" {
		t.Fatal("expected non-empty tx hash")
	}

	retrieved, found, err := client.VerifyData(txHash)
	if err != nil {
		t.Fatalf("VerifyData: %v", err)
	}
	if !found {
		t.Fatal("data should be found")
	}
	if string(retrieved) != string(data) {
		t.Fatalf("data mismatch: got %q, want %q", retrieved, data)
	}
}

// ---------------------------------------------------------------------------
// Test 10: IOTA tx count
// ---------------------------------------------------------------------------

func TestIOTATxCount(t *testing.T) {
	client := NewIOTAClient()

	for i := 0; i < 3; i++ {
		_, _ = client.SubmitData([]byte("data"), "TAG")
	}

	if client.TxCount() != 3 {
		t.Fatalf("expected 3 txs, got %d", client.TxCount())
	}
}

// ---------------------------------------------------------------------------
// Test 11: Trust manager - reputation score calculation
// ---------------------------------------------------------------------------

func TestReputationScore(t *testing.T) {
	tm := NewTrustManager(3)

	// Record 100 successful probes with 50ms latency.
	for i := 0; i < 100; i++ {
		tm.RecordProbe("peer-good", true, 50)
	}

	rel := tm.GetReliability("peer-good")
	if rel.SuccessRate != 1.0 {
		t.Fatalf("expected success rate 1.0, got %f", rel.SuccessRate)
	}
	if rel.ReputationScore < 80.0 {
		t.Fatalf("expected reputation >= 80, got %f", rel.ReputationScore)
	}
	if rel.AvgLatencyMS != 50 {
		t.Fatalf("expected avg latency 50, got %d", rel.AvgLatencyMS)
	}
}

// ---------------------------------------------------------------------------
// Test 12: Trust manager - trust level progression
// ---------------------------------------------------------------------------

func TestTrustLevelProgression(t *testing.T) {
	tm := NewTrustManager(3)

	// New peer: below minProbes.
	tm.RecordProbe("peer-new", true, 50)
	if tm.GetTrustLevel("peer-new") != TrustLevelNew {
		t.Fatalf("expected TrustLevelNew, got %s", tm.GetTrustLevel("peer-new"))
	}

	// Low trust: 5 probes, 60% success.
	for i := 0; i < 4; i++ {
		tm.RecordProbe("peer-low", i < 3, 100)
	}
	if tm.GetTrustLevel("peer-low") != TrustLevelLow {
		t.Fatalf("expected TrustLevelLow, got %s", tm.GetTrustLevel("peer-low"))
	}

	// Medium trust: 15 probes, 80% success.
	for i := 0; i < 15; i++ {
		tm.RecordProbe("peer-med", i < 12, 100)
	}
	if tm.GetTrustLevel("peer-med") != TrustLevelMedium {
		t.Fatalf("expected TrustLevelMedium, got %s", tm.GetTrustLevel("peer-med"))
	}

	// High trust: 25 probes, 95% success.
	for i := 0; i < 25; i++ {
		tm.RecordProbe("peer-high", i < 24, 80)
	}
	if tm.GetTrustLevel("peer-high") != TrustLevelHigh {
		t.Fatalf("expected TrustLevelHigh, got %s", tm.GetTrustLevel("peer-high"))
	}
}

// ---------------------------------------------------------------------------
// Test 13: Penalty assessment
// ---------------------------------------------------------------------------

func TestPenaltyAssessment(t *testing.T) {
	tm := NewTrustManager(3)

	// Peer with 20% success rate should be isolated.
	for i := 0; i < 20; i++ {
		tm.RecordProbe("peer-bad", i < 4, 200)
	}
	action := tm.EvaluatePenalty("peer-bad")
	if action != "isolate" {
		t.Fatalf("expected isolate, got %s", action)
	}

	// Peer with 5% success rate should be banned.
	for i := 0; i < 20; i++ {
		tm.RecordProbe("peer-terrible", i < 1, 500)
	}
	action = tm.EvaluatePenalty("peer-terrible")
	if action != "ban" {
		t.Fatalf("expected ban, got %s", action)
	}

	// Good peer should not be penalized.
	for i := 0; i < 20; i++ {
		tm.RecordProbe("peer-good", true, 50)
	}
	action = tm.EvaluatePenalty("peer-good")
	if action != "" {
		t.Fatalf("expected no penalty, got %s", action)
	}
}

// ---------------------------------------------------------------------------
// Test 14: Cross-verification
// ---------------------------------------------------------------------------

func TestCrossVerification(t *testing.T) {
	probeFn := func(peerID, modelID string) (bool, int64, error) {
		return true, 42, nil
	}
	cv := NewCapabilityVerifier(probeFn, 2)

	// Two different peers probe the same model.
	cv.Probe("peer-a", "gpt-4")
	cv.Probe("peer-b", "gpt-4")

	count, confirmed := cv.CrossVerify("gpt-4")
	if !confirmed {
		t.Fatal("expected cross-verification to be confirmed")
	}
	if count != 2 {
		t.Fatalf("expected 2 verifiers, got %d", count)
	}

	// Unconfirmed model.
	_, confirmed = cv.CrossVerify("nonexistent-model")
	if confirmed {
		t.Fatal("expected unconfirmed for unknown model")
	}
}

// ---------------------------------------------------------------------------
// Test 15: Async upload in FreeLedger
// ---------------------------------------------------------------------------

func TestFreeLedgerAsyncUpload(t *testing.T) {
	cfg := DefaultFreeLedgerConfig()
	cfg.AsyncUpload = true

	fl, err := NewFreeLedger("node-1", cfg)
	if err != nil {
		t.Fatalf("NewFreeLedger: %v", err)
	}
	defer fl.Close()

	rec := &ContributionRecord{
		PeerID:        "contributor-1",
		PeerPublicKey: fl.Gossip.PublicKey(),
		ModelID:       "gpt-4",
		Provider:      "openai",
		Tokens:        5000,
		ValueUSD:      0.15,
	}

	id, err := fl.RecordContribution(rec)
	if err != nil {
		t.Fatalf("RecordContribution: %v", err)
	}

	// Wait for async upload.
	fl.WaitForUploads()

	// Verify the record is retrievable.
	got, err := fl.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if got.ID != id {
		t.Fatalf("ID mismatch: got %s, want %s", got.ID, id)
	}

	// IPFS cache should have data.
	if fl.IPFS.CacheSize() < 1 {
		t.Fatal("expected IPFS cache to contain data")
	}
}

// ---------------------------------------------------------------------------
// Test 16: FreeLedger high-value IOTA anchoring
// ---------------------------------------------------------------------------

func TestFreeLedgerHighValueIOTA(t *testing.T) {
	cfg := DefaultFreeLedgerConfig()
	cfg.AsyncUpload = false // synchronous for deterministic test
	cfg.MajorEventThreshold = 10.0

	fl, err := NewFreeLedger("node-2", cfg)
	if err != nil {
		t.Fatalf("NewFreeLedger: %v", err)
	}

	rec := &ContributionRecord{
		PeerID:        "contributor-2",
		PeerPublicKey: fl.Gossip.PublicKey(),
		ModelID:       "claude-3",
		Provider:      "anthropic",
		Tokens:        50000,
		ValueUSD:      150.0, // above threshold
	}

	id, err := fl.RecordContribution(rec)
	if err != nil {
		t.Fatalf("RecordContribution: %v", err)
	}

	got, err := fl.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}

	// Should have IOTA anchoring since value > threshold.
	if got.Proof.StorageLocation != "ipfs+iota" {
		t.Fatalf("expected ipfs+iota storage, got %s", got.Proof.StorageLocation)
	}
	if got.Proof.IOTATxHash == "" {
		t.Fatal("expected IOTA tx hash for high-value contribution")
	}
}

// ---------------------------------------------------------------------------
// Test 17: Gossip sync merge
// ---------------------------------------------------------------------------

func TestGossipSyncMerge(t *testing.T) {
	g1 := newTestLedger(t)
	g2, _ := NewGossipLedger("test-peer-2")

	// Add records to g1.
	rec := sampleContribution()
	id1, _ := g1.RecordContribution(rec)

	claim := &CapabilityClaim{
		PeerID:     "peer-alpha",
		Models:     []string{"gpt-4"},
		Providers:  []string{"openai"},
		MaxQuota:   10000,
		ValidUntil: time.Now().Add(24 * time.Hour),
	}
	g1.RecordClaim(claim)

	// Retrieve from g1 to sync.
	c, _ := g1.GetContribution(id1)

	// Sync to g2.
	merged := g2.GossipSync([]*ContributionRecord{c}, nil, g1.getAllClaims(), nil)
	if merged != 2 {
		t.Fatalf("expected 2 merged records, got %d", merged)
	}

	// g2 should now have the contribution.
	_, err := g2.GetContribution(id1)
	if err != nil {
		t.Fatalf("contribution should exist after sync: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 18: Capability verifier - verify claim
// ---------------------------------------------------------------------------

func TestVerifyClaim(t *testing.T) {
	probeFn := func(peerID, modelID string) (bool, int64, error) {
		if modelID == "broken-model" {
			return false, 0, nil
		}
		return true, 30, nil
	}
	cv := NewCapabilityVerifier(probeFn, 1)

	claim := &CapabilityClaim{
		PeerID: "peer-x",
		Models: []string{"gpt-4", "claude-3"},
	}
	results, allOK := cv.VerifyClaim(claim)
	if !allOK {
		t.Fatal("all models should pass")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Claim with a broken model.
	claim2 := &CapabilityClaim{
		PeerID: "peer-y",
		Models: []string{"gpt-4", "broken-model"},
	}
	_, allOK = cv.VerifyClaim(claim2)
	if allOK {
		t.Fatal("should fail because broken-model is unreachable")
	}
}

// ---------------------------------------------------------------------------
// Test 19: IPFS retrieve non-existent CID
// ---------------------------------------------------------------------------

func TestIPFSRetrieveNonExistent(t *testing.T) {
	client := NewIPFSClient()
	_, err := client.Retrieve("Qm00000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected error for non-existent CID")
	}
}

// ---------------------------------------------------------------------------
// Test 20: Trust level string representation
// ---------------------------------------------------------------------------

func TestTrustLevelString(t *testing.T) {
	tests := []struct {
		level TrustLevel
		want  string
	}{
		{TrustLevelNew, "new"},
		{TrustLevelLow, "low"},
		{TrustLevelMedium, "medium"},
		{TrustLevelHigh, "high"},
		{TrustLevelVerified, "verified"},
		{TrustLevel(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("TrustLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 21: Penalty record storage
// ---------------------------------------------------------------------------

func TestPenaltyRecordStorage(t *testing.T) {
	g := newTestLedger(t)

	rec := &PenaltyRecord{
		PeerID:    "bad-peer",
		Reason:    "repeated timeout",
		Evidence:  []string{"probe-1", "probe-2"},
		Action:    "isolate",
		Verifiers: []string{"verifier-1", "verifier-2"},
	}

	id, err := g.RecordPenalty(rec)
	if err != nil {
		t.Fatalf("RecordPenalty: %v", err)
	}

	penalties, err := g.GetPenalties("bad-peer")
	if err != nil {
		t.Fatalf("GetPenalties: %v", err)
	}
	if len(penalties) != 1 {
		t.Fatalf("expected 1 penalty, got %d", len(penalties))
	}
	if penalties[0].ID != id {
		t.Fatalf("penalty ID mismatch: got %s, want %s", penalties[0].ID, id)
	}
	if len(penalties[0].Signature) == 0 {
		t.Fatal("expected penalty to be signed")
	}
}

// ---------------------------------------------------------------------------
// Test 22: Ledger count
// ---------------------------------------------------------------------------

func TestLedgerCount(t *testing.T) {
	g := newTestLedger(t)

	g.RecordContribution(sampleContribution())
	g.RecordContribution(sampleContribution())

	trust := &TrustRecord{
		SubjectPeerID:  "peer-a",
		VerifierPeerID: "peer-b",
		ModelID:        "gpt-4",
		Success:        true,
		LatencyMS:      50,
	}
	g.RecordTrust(trust)

	if g.Count() != 3 {
		t.Fatalf("expected count 3, got %d", g.Count())
	}
}
