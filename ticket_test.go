package main

import (
	"testing"
	"time"
)

// newTestTicketStore builds a standalone TicketStore for unit testing without
// touching the global singletons (no disk, no identity).
func newTestTicketStore() *TicketStore {
	return &TicketStore{
		seen:      make(map[string]time.Time),
		tickets:   make(map[string]*UsageTicket),
		notarized: make(map[string]bool),
	}
}

// TestTicketFingerprint_Deterministic verifies the dedup fingerprint is stable
// for identical fields and distinct for differing fields (anti-double-spend
// core).
func TestTicketFingerprint_Deterministic(t *testing.T) {
	a := &UsageTicket{RequestID: "r1", RequestorID: "req1", ProviderID: "prov1", ModelID: "m1", Amount: 10, Timestamp: "2026-01-01T00:00:00Z"}
	b := &UsageTicket{RequestID: "r1", RequestorID: "req1", ProviderID: "prov1", ModelID: "m1", Amount: 10, Timestamp: "2026-01-01T00:00:00Z"}
	if TicketFingerprint(a) != TicketFingerprint(b) {
		t.Fatal("fingerprint not deterministic for identical fields")
	}
	c := &UsageTicket{RequestID: "r2", RequestorID: "req1", ProviderID: "prov1", ModelID: "m1", Amount: 10, Timestamp: "2026-01-01T00:00:00Z"}
	if TicketFingerprint(a) == TicketFingerprint(c) {
		t.Fatal("different request_id produced identical fingerprint")
	}
}

// TestTicketStore_CountersignAndDoubleSpend verifies that two tickets with the
// same fingerprint cannot both be countersigned — the second is rejected as a
// double-spend and the fingerprint is recorded as seen.
func TestTicketStore_CountersignAndDoubleSpend(t *testing.T) {
	ts := newTestTicketStore()
	tk := ts.IssueTicket("r1", "req1", "prov1", "m1", 10)
	if err := ts.Countersign(tk); err != nil {
		t.Fatalf("first countersign failed: %v", err)
	}

	tk2 := ts.IssueTicket("r1", "req1", "prov1", "m1", 10) // identical fields -> same fingerprint
	if err := ts.Countersign(tk2); err == nil {
		t.Fatal("expected double-spend error on identical fingerprint")
	}
	if !ts.IsDoubleSpend(tk.Fingerprint) {
		t.Fatal("expected fingerprint marked as double-spend")
	}
}

// TestTicketStore_IsDoubleSpend checks the seen-set transitions correctly.
func TestTicketStore_IsDoubleSpend(t *testing.T) {
	ts := newTestTicketStore()
	tk := ts.IssueTicket("r", "req", "prov", "m", 1)
	if ts.IsDoubleSpend(tk.Fingerprint) {
		t.Fatal("fresh fingerprint should not be a double-spend")
	}
	if err := ts.Countersign(tk); err != nil {
		t.Fatalf("countersign: %v", err)
	}
	if !ts.IsDoubleSpend(tk.Fingerprint) {
		t.Fatal("countersigned fingerprint should be a double-spend")
	}
}

// TestTicketStore_Notarized verifies the notarization flag and that notarized
// tickets drop out of the pending-notarization set.
func TestTicketStore_Notarized(t *testing.T) {
	ts := newTestTicketStore()
	tk := ts.IssueTicket("r", "req", "prov", "m", 1)
	if err := ts.Countersign(tk); err != nil {
		t.Fatalf("countersign: %v", err)
	}
	if ts.IsNotarized(tk.Fingerprint) {
		t.Fatal("ticket should not be notarized yet")
	}
	ts.MarkNotarized(tk.Fingerprint)
	if !ts.IsNotarized(tk.Fingerprint) {
		t.Fatal("expected ticket to be notarized")
	}
	for _, p := range ts.GetPendingNotarization() {
		if p.Fingerprint == tk.Fingerprint {
			t.Fatal("notarized ticket must not remain pending")
		}
	}
}

// TestTicketStore_Cleanup verifies the memory-bounded cleanup removes stale
// fingerprints and tickets (v4.3.19 leak fix), without dropping fresh ones.
func TestTicketStore_Cleanup(t *testing.T) {
	ts := newTestTicketStore()
	old := ts.IssueTicket("old", "req", "prov", "m", 1)
	if err := ts.Countersign(old); err != nil {
		t.Fatalf("countersign: %v", err)
	}
	fresh := ts.IssueTicket("fresh", "req", "prov", "m", 1)
	if err := ts.Countersign(fresh); err != nil {
		t.Fatalf("countersign: %v", err)
	}

	oldTs := time.Now().Add(-48 * time.Hour)
	ts.mu.Lock()
	ts.seen[old.Fingerprint] = oldTs
	old.Timestamp = oldTs.UTC().Format(time.RFC3339)
	ts.mu.Unlock()

	ts.Cleanup(24 * time.Hour)

	if ts.IsDoubleSpend(old.Fingerprint) {
		t.Fatal("cleanup should have evicted the old fingerprint")
	}
	if ts.GetTicket(old.ID) != nil {
		t.Fatal("cleanup should have evicted the old ticket")
	}
	if !ts.IsDoubleSpend(fresh.Fingerprint) {
		t.Fatal("cleanup must keep fresh fingerprints")
	}
}

// TestAntiCollusionCheck_FlagsDeviation verifies the three-layer check flags a
// provider whose success rate deviates >50% from the cross-provider average.
func TestAntiCollusionCheck_FlagsDeviation(t *testing.T) {
	tickets := []*UsageTicket{
		{ProviderID: "good", ReqSig: "x", ProvSig: "y", Amount: 10},
		{ProviderID: "good", ReqSig: "x", ProvSig: "y", Amount: 10},
		{ProviderID: "bad", ReqSig: "x", ProvSig: "", Amount: 10}, // 0% success
		{ProviderID: "bad", ReqSig: "x", ProvSig: "", Amount: 10},
	}
	anomalies, flagged := AntiCollusionCheck(tickets)
	if anomalies == 0 {
		t.Fatal("expected at least one anomaly")
	}
	found := false
	for _, pid := range flagged {
		if pid == "bad" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'bad' flagged, got %v", flagged)
	}
}

// TestAntiCollusionCheck_Empty confirms the no-input guard.
func TestAntiCollusionCheck_Empty(t *testing.T) {
	a, f := AntiCollusionCheck(nil)
	if a != 0 || f != nil {
		t.Fatalf("empty input should yield 0 anomalies, got %d %v", a, f)
	}
}
