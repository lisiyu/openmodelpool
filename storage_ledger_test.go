package main

// G4: 账本域 bbolt 后端测试。

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testLedgerBbolt(t *testing.T, dir string) (*bboltHandle, func()) {
	t.Helper()
	h, err := openBbolt(dir)
	if err != nil {
		t.Fatalf("openBbolt: %v", err)
	}
	return h, func() {
		closeBbolt(dir)
	}
}

// 构造一个带各类记录的账本并落 JSON。
func seedLedgerJSON(t *testing.T, dir string) (*GossipLedger, string) {
	t.Helper()
	g, err := NewGossipLedger("ledger-test-node")
	if err != nil {
		t.Fatalf("NewGossipLedger: %v", err)
	}
	cid, err := g.RecordContribution(&ContributionRecord{PeerID: "peer-a", Tokens: 100})
	if err != nil || cid == "" {
		t.Fatalf("RecordContribution: %v", err)
	}
	if _, err := g.RecordTrust(&TrustRecord{SubjectPeerID: "peer-a", ModelID: "m1", Success: true}); err != nil {
		t.Fatalf("RecordTrust: %v", err)
	}
	if g.RecordClaim(&CapabilityClaim{PeerID: "peer-a", Models: []string{"m1"}}) == "" {
		t.Fatal("RecordClaim returned empty id")
	}
	if _, err := g.RecordPenalty(&PenaltyRecord{PeerID: "peer-b", Reason: "spam"}); err != nil {
		t.Fatalf("RecordPenalty: %v", err)
	}
	tx, err := g.AppendTransaction("contribution", "peer-a", 100, "m1", "req-1")
	if err != nil || tx == nil {
		t.Fatalf("AppendTransaction: %v", err)
	}
	p := filepath.Join(dir, "ledger.json")
	if err := g.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return g, p
}

func TestLedger_BboltMigrateFromJSON(t *testing.T) {
	dir := t.TempDir()
	src, jsonPath := seedLedgerJSON(t, dir)
	srcPriv := append([]byte(nil), src.priv...)
	srcSeq := src.seq

	h, cleanup := testLedgerBbolt(t, dir)
	defer cleanup()

	g, err := importLedgerToBbolt(h, "ledger-test-node", dir)
	if err != nil {
		t.Fatalf("importLedgerToBbolt: %v", err)
	}
	if g.Count() != src.Count() {
		t.Fatalf("Count = %d, want %d", g.Count(), src.Count())
	}
	if string(g.priv) != string(srcPriv) {
		t.Fatal("private key NOT preserved through migration")
	}
	if g.seq != srcSeq {
		t.Fatalf("seq = %d, want %d", g.seq, srcSeq)
	}
	if g.peerID != "ledger-test-node" {
		t.Fatalf("peerID = %q", g.peerID)
	}
	// 各类记录都在
	if len(g.recs) != 1 || len(g.trusts) != 1 || len(g.claims) != 1 || len(g.penalties) != 1 {
		t.Fatalf("records lost: recs=%d trusts=%d claims=%d penalties=%d",
			len(g.recs), len(g.trusts), len(g.claims), len(g.penalties))
	}
	if len(g.txs) != 1 {
		t.Fatalf("txs = %d, want 1", len(g.txs))
	}
	// dailyContrib 由 txs 重建（与 JSON 路径一致）
	if got := g.GetDailyContributions("peer-a", time.Now()); got != 100 {
		t.Fatalf("dailyContrib = %d, want 100", got)
	}
	if !h.isMigrated("ledger") {
		t.Fatal("migrated marker not set")
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Fatalf(".bak missing: %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatal("original json should be renamed to .bak")
	}
	assertFileMode0600(t, filepath.Join(dir, bboltFileName))
}

func TestLedger_BboltLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src, _ := seedLedgerJSON(t, dir)

	h, cleanup := testLedgerBbolt(t, dir)
	defer cleanup()

	if _, err := importLedgerToBbolt(h, "ledger-test-node", dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	g2, err := loadLedgerBbolt(h)
	if err != nil {
		t.Fatalf("loadLedgerBbolt: %v", err)
	}
	if g2.Count() != src.Count() {
		t.Fatalf("Count = %d, want %d", g2.Count(), src.Count())
	}
	if string(g2.priv) != string(src.priv) {
		t.Fatal("private key lost in load round-trip")
	}
	// 链完整性：tx 的 hash 链可验证
	if !g2.VerifyChain() {
		t.Fatal("tx chain broken after bbolt round-trip")
	}
}

func TestLedger_BboltIncrementalDirtyWrite(t *testing.T) {
	dir := t.TempDir()
	h, cleanup := testLedgerBbolt(t, dir)
	defer cleanup()

	// 全新节点：无 JSON，直接进 bbolt
	g, err := importLedgerToBbolt(h, "fresh-node", dir)
	if err != nil {
		t.Fatalf("importLedgerToBbolt fresh: %v", err)
	}
	if g.peerID != "fresh-node" {
		t.Fatalf("peerID = %q", g.peerID)
	}

	// 增量变更 → saveBbolt 只写脏 key
	cid, err := g.RecordContribution(&ContributionRecord{PeerID: "peer-a", Tokens: 50})
	if err != nil {
		t.Fatalf("RecordContribution: %v", err)
	}
	if _, err := g.AppendTransaction("contribution", "peer-a", 50, "m1", "req-9"); err != nil {
		t.Fatalf("AppendTransaction: %v", err)
	}
	if err := g.saveBbolt(h); err != nil {
		t.Fatalf("saveBbolt: %v", err)
	}
	if len(g.dirty) != 0 {
		t.Fatalf("dirty not drained: %d keys", len(g.dirty))
	}
	// 无变更时 saveBbolt 应直接返回
	if err := g.saveBbolt(h); err != nil {
		t.Fatalf("empty saveBbolt: %v", err)
	}

	g2, err := loadLedgerBbolt(h)
	if err != nil {
		t.Fatalf("loadLedgerBbolt: %v", err)
	}
	if _, err := g2.GetContribution(cid); err != nil {
		t.Fatalf("contribution lost: %v", err)
	}
	if len(g2.txs) != 1 {
		t.Fatalf("txs = %d, want 1", len(g2.txs))
	}
	// seq 连续性：新记录 ID 不与已迁移的冲突
	cid2, err := g2.RecordContribution(&ContributionRecord{PeerID: "peer-b", Tokens: 10})
	if err != nil {
		t.Fatalf("RecordContribution on loaded: %v", err)
	}
	if cid2 == cid {
		t.Fatal("ID collision after reload: seq not persisted")
	}
}

func TestLedger_BboltGossipSyncDirty(t *testing.T) {
	dir := t.TempDir()
	h, cleanup := testLedgerBbolt(t, dir)
	defer cleanup()

	g, err := importLedgerToBbolt(h, "sync-node", dir)
	if err != nil {
		t.Fatal(err)
	}
	// 先落盘一次，清空 dirty
	if err := g.saveBbolt(h); err != nil {
		t.Fatal(err)
	}
	added := g.GossipSync(
		[]*ContributionRecord{{ID: "contrib-remote-1", PeerID: "peer-r", Tokens: 7}},
		[]*TrustRecord{{ID: "trust-remote-1", SubjectPeerID: "peer-r", Success: true}},
		nil, nil,
	)
	if added != 2 {
		t.Fatalf("GossipSync added = %d, want 2", added)
	}
	if err := g.saveBbolt(h); err != nil {
		t.Fatal(err)
	}
	g2, err := loadLedgerBbolt(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g2.GetContribution("contrib-remote-1"); err != nil {
		t.Fatalf("gossip record lost: %v", err)
	}
	if len(g2.trusts) != 1 {
		t.Fatalf("trusts = %d, want 1", len(g2.trusts))
	}
}

// P3-5: 损坏的 ledger.json 在 JSON 后端初始化时不得被静默覆盖——坏文件必须
// bak 留证（唯一的恢复证据），节点以全新身份启动。
func TestCorruptLedgerJSON_PreservedAsBakAndFreshIdentity(t *testing.T) {
	dir := t.TempDir()
	corrupt := []byte("{corrupt ledger json")
	if err := os.WriteFile(filepath.Join(dir, "ledger.json"), corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	old := contributionLedger
	t.Cleanup(func() { contributionLedger = old })

	initContributionLedgerJSON(dir, "mmx-fresh-id")
	if contributionLedger == nil {
		t.Fatal("fresh ledger must initialize after corrupt json")
	}
	bak, err := os.ReadFile(filepath.Join(dir, "ledger.json.bak"))
	if err != nil {
		t.Fatalf("corrupt ledger must be preserved as .bak: %v", err)
	}
	if string(bak) != string(corrupt) {
		t.Fatal("bak content must equal the original corrupt file")
	}
}
