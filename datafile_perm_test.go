package main

// G4 前置加固：data/*.json 权限回归测试。
// ledger.json 直接存着账本 ed25519 私钥（priv_key），其余五个文件同样经由
// atomicWriteFile(path, data, 0600) 落盘。本文件断言六个文件的真实写入路径
// 产出的文件权限恒为 0600，并在该权限下读写往返正常。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func assertFileMode0600(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if mode := fi.Mode().Perm(); mode != 0600 {
		t.Fatalf("%s mode = %o, want 0600", path, mode)
	}
}

// 漏斗测试：所有 JSON 落盘最终都走 atomicWriteFile。
func TestAtomicWriteFile_Perm0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")

	if err := atomicWriteFile(p, []byte(`{}`), 0600); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	assertFileMode0600(t, p)

	// 覆盖已存在的宽松权限文件：rename 替换后仍应为 0600。
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := atomicWriteFile(p, []byte(`{"a":1}`), 0600); err != nil {
		t.Fatalf("atomicWriteFile overwrite: %v", err)
	}
	assertFileMode0600(t, p)
}

// 关键文件：ledger.json（含 priv_key），0600 + 读写往返。
func TestLedgerJSON_Perm0600_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ledger.json")

	ledger, err := NewGossipLedger("perm-test-node")
	if err != nil {
		t.Fatalf("NewGossipLedger: %v", err)
	}
	if len(ledger.priv) == 0 {
		t.Fatal("ledger private key not generated")
	}
	if err := ledger.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertFileMode0600(t, p)

	loaded, err := LoadGossipLedger(p)
	if err != nil {
		t.Fatalf("LoadGossipLedger under 0600: %v", err)
	}
	if loaded.peerID != "perm-test-node" {
		t.Fatalf("peerID = %q, want perm-test-node", loaded.peerID)
	}
	if len(loaded.priv) == 0 {
		t.Fatal("private key lost after 0600 round-trip")
	}
}

func TestGovernanceJSON_Perm0600(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "governance.json")

	g := NewGovernanceLedger("perm-test-node", func() []string { return nil }, p)
	if _, err := g.Propose("", "param", "perm test", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	assertFileMode0600(t, p)
}

func TestContributionQuotaJSON_Perm0600(t *testing.T) {
	dir := t.TempDir()

	tr := initContributionQuotaTracker(dir)
	tr.Accrue("peer-x", 100)
	assertFileMode0600(t, filepath.Join(dir, "contribution_quota.json"))
}

func TestQuotaAllocationJSON_Perm0600(t *testing.T) {
	dir := t.TempDir()

	am := &AllocationManager{config: DefaultQuotaAllocation(), dataDir: dir}
	if err := am.SetAllocation(30); err != nil {
		t.Fatalf("SetAllocation: %v", err)
	}
	assertFileMode0600(t, filepath.Join(dir, "quota_allocation.json"))
}

func TestGlobalPoolJSON_Perm0600(t *testing.T) {
	dir := t.TempDir()

	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		ParticipantNodes:  make([]GlobalPoolNode, 0),
		dataPath:          filepath.Join(dir, "global_pool.json"),
	}
	gp.doSave()
	assertFileMode0600(t, filepath.Join(dir, "global_pool.json"))
}

func TestAlgorithmProposalsJSON_Perm0600(t *testing.T) {
	dir := t.TempDir()

	// 直接构造实例，避免碰全局 governor（enc 未就绪时降级为普通原子写）。
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	if _, err := g.CreateProposal("t", "d", "perm-tester", "", nil); err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	assertFileMode0600(t, filepath.Join(dir, algorithmGovernanceFile))
}
