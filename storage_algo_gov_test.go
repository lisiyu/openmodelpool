package main

// G4: algorithm_proposals 域 bbolt 后端测试（决策 4：无 HMAC 信封）。

import (
	"os"
	"path/filepath"
	"testing"
)

func testAlgoGovBbolt(t *testing.T, dir string) (*bboltHandle, func()) {
	t.Helper()
	h, err := openBbolt(dir)
	if err != nil {
		t.Fatalf("openBbolt: %v", err)
	}
	return h, func() {
		closeBbolt(dir)
	}
}

func newTestAlgoGovernor(dir string) *AlgorithmGovernor {
	return &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
}

func TestAlgoGov_BboltMigrateFromJSON(t *testing.T) {
	dir := t.TempDir()
	// 用现有 saveWithIntegrity 写一份 JSON（单测下 enc 未就绪，降级为普通写）
	src := newTestAlgoGovernor(dir)
	p, err := src.CreateProposal("migrate me", "desc", "tester", "", nil)
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	jsonPath := filepath.Join(dir, algorithmGovernanceFile)
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("json not written: %v", err)
	}

	h, cleanup := testAlgoGovBbolt(t, dir)
	defer cleanup()

	g := newTestAlgoGovernor(dir)
	if err := g.importToBbolt(h); err != nil {
		t.Fatalf("importToBbolt: %v", err)
	}
	if len(g.proposals) != 1 || g.proposals[p.ID] == nil {
		t.Fatalf("proposals not migrated: %d", len(g.proposals))
	}
	if g.proposals[p.ID].Title != "migrate me" {
		t.Fatalf("title = %q", g.proposals[p.ID].Title)
	}
	if !h.isMigrated("algo_gov") {
		t.Fatal("migrated marker not set")
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Fatalf(".bak missing: %v", err)
	}
	// 幂等
	if err := g.importToBbolt(h); err != nil {
		t.Fatalf("second importToBbolt: %v", err)
	}
}

func TestAlgoGov_BboltRoundTrip(t *testing.T) {
	dir := t.TempDir()
	h, cleanup := testAlgoGovBbolt(t, dir)
	defer cleanup()

	g := newTestAlgoGovernor(dir)
	g.bbolt = h // 走 bbolt 后端
	p, err := g.CreateProposal("bbolt prop", "d", "tester", "", nil)
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	g2 := newTestAlgoGovernor(dir)
	g2.loadBbolt(h)
	if len(g2.proposals) != 1 || g2.proposals[p.ID] == nil {
		t.Fatalf("loadBbolt lost proposals: %d", len(g2.proposals))
	}
	assertFileMode0600(t, filepath.Join(dir, bboltFileName))
}

func TestAlgoGov_JSONFallbackStillWorks(t *testing.T) {
	dir := t.TempDir()
	g := newTestAlgoGovernor(dir)
	if _, err := g.CreateProposal("json prop", "d", "tester", "", nil); err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, algorithmGovernanceFile)); err != nil {
		t.Fatalf("json fallback did not write file: %v", err)
	}
	g2 := newTestAlgoGovernor(dir)
	g2.Load()
	if len(g2.proposals) != 1 {
		t.Fatalf("json fallback round-trip lost proposals: %d", len(g2.proposals))
	}
}
