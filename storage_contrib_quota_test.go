package main

// G4: contribution_quota 域 bbolt 后端测试。

import (
	"os"
	"path/filepath"
	"testing"
)

func testCQuotaBbolt(t *testing.T, dir string) (*bboltHandle, func()) {
	t.Helper()
	h, err := openBbolt(dir)
	if err != nil {
		t.Fatalf("openBbolt: %v", err)
	}
	return h, func() {
		closeBbolt(dir)
	}
}

func TestCQuota_BboltMigrateFromJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "contribution_quota.json")
	payload := `{"peer-a":{"peer_id":"peer-a","contributed_tokens":1000,"earned_free_quota":1000,"consumed_quota":200,"remaining_quota":800,"last_updated":1700000000},"peer-b":{"peer_id":"peer-b","contributed_tokens":500,"earned_free_quota":0,"consumed_quota":0,"remaining_quota":0,"last_updated":1700000000}}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	h, cleanup := testCQuotaBbolt(t, dir)
	defer cleanup()

	tr := &ContributionQuotaTracker{dataDir: dir, entries: make(map[string]*ContributionQuota)}
	if err := tr.importToBbolt(h); err != nil {
		t.Fatalf("importToBbolt: %v", err)
	}
	if len(tr.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(tr.entries))
	}
	// earned_free_quota 被 re-normalize 为 1:1
	if tr.entries["peer-b"].EarnedFreeQuota != 500 {
		t.Fatalf("peer-b earned = %d, want 500 (re-normalized)", tr.entries["peer-b"].EarnedFreeQuota)
	}
	if tr.entries["peer-a"].RemainingQuota != 800 {
		t.Fatalf("peer-a remaining = %d, want 800", tr.entries["peer-a"].RemainingQuota)
	}
	if !h.isMigrated("contrib_quota") {
		t.Fatal("migrated marker not set")
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Fatalf(".bak missing: %v", err)
	}
}

func TestCQuota_BboltHotPathSingleKey(t *testing.T) {
	dir := t.TempDir()
	h, cleanup := testCQuotaBbolt(t, dir)
	defer cleanup()

	tr := &ContributionQuotaTracker{dataDir: dir, entries: make(map[string]*ContributionQuota)}
	tr.bbolt = h // 走 bbolt 后端
	tr.Accrue("peer-x", 100)
	tr.Accrue("peer-y", 50)
	ok, remaining := tr.Consume("peer-x", 30)
	if !ok || remaining != 70 {
		t.Fatalf("Consume = %v,%d want true,70", ok, remaining)
	}

	// 新实例从 bbolt 读回
	tr2 := &ContributionQuotaTracker{dataDir: dir, entries: make(map[string]*ContributionQuota)}
	tr2.loadBbolt(h)
	if len(tr2.entries) != 2 {
		t.Fatalf("loadBbolt entries = %d, want 2", len(tr2.entries))
	}
	if tr2.entries["peer-x"].RemainingQuota != 70 {
		t.Fatalf("peer-x remaining = %d, want 70", tr2.entries["peer-x"].RemainingQuota)
	}
	if tr2.entries["peer-y"].ContributedTokens != 50 {
		t.Fatalf("peer-y contributed = %d, want 50", tr2.entries["peer-y"].ContributedTokens)
	}
	assertFileMode0600(t, filepath.Join(dir, bboltFileName))
}

func TestCQuota_JSONFallbackStillWorks(t *testing.T) {
	dir := t.TempDir()
	tr := &ContributionQuotaTracker{dataDir: dir, entries: make(map[string]*ContributionQuota)}
	tr.Accrue("peer-z", 77)
	jsonPath := filepath.Join(dir, "contribution_quota.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("json fallback did not write file: %v", err)
	}
	tr2 := &ContributionQuotaTracker{dataDir: dir, entries: make(map[string]*ContributionQuota)}
	tr2.load()
	if tr2.entries["peer-z"].ContributedTokens != 77 {
		t.Fatalf("json fallback round-trip failed: %+v", tr2.entries)
	}
}
