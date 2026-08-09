package main

import (
	"testing"
)

func TestContributionQuotaAccrue(t *testing.T) {
	dir := t.TempDir()
	tr := initContributionQuotaTracker(dir)

	tr.Accrue("peerA", 100)
	tr.Accrue("peerA", 50) // cumulative
	tr.Accrue("peerB", 200)

	// 1:1 public-welfare entitlement, no fee/no inflation.
	if q := tr.GetQuota("peerA"); q == nil || q.ContributedTokens != 150 || q.EarnedFreeQuota != 150 {
		t.Fatalf("peerA: want 150/150, got %+v", q)
	}
	if q := tr.GetQuota("peerB"); q == nil || q.ContributedTokens != 200 || q.EarnedFreeQuota != 200 {
		t.Fatalf("peerB: want 200/200, got %+v", q)
	}
	// Snapshot sorted by contributed desc: peerB(200) then peerA(150).
	snap := tr.Snapshot()
	if len(snap) != 2 || snap[0].PeerID != "peerB" || snap[1].PeerID != "peerA" {
		t.Fatalf("snapshot order wrong: %+v", snap)
	}
	if tr.TotalContributed() != 350 {
		t.Fatalf("total want 350, got %d", tr.TotalContributed())
	}
}

func TestContributionQuotaPersistence(t *testing.T) {
	dir := t.TempDir()
	tr := initContributionQuotaTracker(dir)
	tr.Accrue("peerA", 120)

	// Reload from the same data dir; accrual must survive.
	tr2 := initContributionQuotaTracker(dir)
	q := tr2.GetQuota("peerA")
	if q == nil || q.ContributedTokens != 120 || q.EarnedFreeQuota != 120 {
		t.Fatalf("persisted quota wrong: %+v", q)
	}
}

func TestRecordContributionAccruesQuota(t *testing.T) {
	dir := t.TempDir()
	saved := contribQuotaTracker
	contribQuotaTracker = initContributionQuotaTracker(dir)
	defer func() { contribQuotaTracker = saved }()

	g, err := NewGossipLedger("nodeX")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "donor1", Tokens: 77}); err != nil {
		t.Fatal(err)
	}
	q := contribQuotaTracker.GetQuota("donor1")
	if q == nil || q.ContributedTokens != 77 || q.EarnedFreeQuota != 77 {
		t.Fatalf("RecordContribution did not accrue quota: %+v", q)
	}
}

func TestRecordContributionQuotaHookNilSafe(t *testing.T) {
	// When the tracker is nil, recording must behave exactly as before.
	saved := contribQuotaTracker
	contribQuotaTracker = nil
	defer func() { contribQuotaTracker = saved }()

	g, err := NewGossipLedger("nodeY")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.RecordContribution(&ContributionRecord{ID: "c2", PeerID: "donor2", Tokens: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.GetContribution("c2"); err != nil {
		t.Fatalf("record should be stored locally: %v", err)
	}
}
