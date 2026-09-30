package main

// G4: governance 域 bbolt 后端测试。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testGovBbolt(t *testing.T, dir string) (*bboltHandle, func()) {
	t.Helper()
	h, err := openBbolt(dir)
	if err != nil {
		t.Fatalf("openBbolt: %v", err)
	}
	return h, func() {
		closeBbolt(dir)
	}
}

// withNilCfg 把全局 cfg 强制为 nil（JSON 后端）并在测试后恢复。
// 全量测试时其他测试会泄漏非 nil cfg，依赖 JSON 分支的测试必须隔离。
func withNilCfg(t *testing.T) {
	t.Helper()
	orig := cfg
	cfg = nil
	t.Cleanup(func() { cfg = orig })
}

func TestGovernance_BboltMigrateFromJSON(t *testing.T) {
	withNilCfg(t)
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "governance.json")

	// 先走 JSON 路径产生一份真实数据（单测下 cfg 为 nil → JSON）
	g := NewGovernanceLedger("node-1", func() []string { return nil }, jsonPath)
	if _, err := g.Propose("", "param", "gov migrate", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("json not written: %v", err)
	}

	h, cleanup := testGovBbolt(t, dir)
	defer cleanup()

	g2 := NewGovernanceLedger("node-1", func() []string { return nil }, jsonPath)
	// 注意：NewGovernanceLedger 在 cfg==nil 时走 JSON 路径；这里显式走迁移
	if err := g2.importToBbolt(h); err != nil {
		t.Fatalf("importToBbolt: %v", err)
	}
	if len(g2.proposalList) != 1 {
		t.Fatalf("proposals = %d, want 1", len(g2.proposalList))
	}
	if g2.proposalList[0].Title != "gov migrate" {
		t.Fatalf("title = %q", g2.proposalList[0].Title)
	}
	if !h.isMigrated("governance") {
		t.Fatal("migrated marker not set")
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Fatalf(".bak missing: %v", err)
	}

	// bbolt 读回
	g3 := NewGovernanceLedger("node-1", func() []string { return nil }, jsonPath)
	g3.bbolt = h // 手动挂上 bbolt 句柄
	g3.loadBbolt(h)
	if len(g3.proposalList) != 1 || g3.proposalList[0].Title != "gov migrate" {
		t.Fatalf("loadBbolt lost data: %+v", g3.proposalList)
	}
}

func TestGovernance_BboltSaveLoadRoundTrip(t *testing.T) {
	withNilCfg(t)
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "governance.json")
	h, cleanup := testGovBbolt(t, dir)
	defer cleanup()

	g := NewGovernanceLedger("node-1", func() []string { return nil }, jsonPath)
	g.bbolt = h // 走 bbolt 后端
	if _, err := g.Propose("", "param", "bbolt gov", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	// JSON 文件不应产生（bbolt 模式）
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatal("json file should not be written in bbolt mode")
	}
	g2 := NewGovernanceLedger("node-1", func() []string { return nil }, jsonPath)
	g2.bbolt = h
	g2.loadBbolt(h)
	if len(g2.proposalList) != 1 || g2.proposalList[0].Title != "bbolt gov" {
		t.Fatalf("round-trip failed: %+v", g2.proposalList)
	}
	assertFileMode0600(t, filepath.Join(dir, bboltFileName))
}

func TestGovernance_JSONFallbackStillWorks(t *testing.T) {
	withNilCfg(t)
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "governance.json")
	g := NewGovernanceLedger("node-1", func() []string { return nil }, jsonPath)
	if _, err := g.Propose("", "param", "json gov", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	assertFileMode0600(t, jsonPath)
	g2 := NewGovernanceLedger("node-1", func() []string { return nil }, jsonPath)
	if len(g2.proposalList) != 1 {
		t.Fatalf("json fallback round-trip lost data: %d", len(g2.proposalList))
	}
}
