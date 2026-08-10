package main

// governance_deadlock_test.go — regression for the save-in-lock self-deadlock
// in GovernanceLedger.Propose/Ratify.
//
// The bug: Propose/Ratify hold g.mu.Lock() and called g.save(), which
// internally acquires g.mu.RLock() — Go's RWMutex is not reentrant, so with a
// real dataPath (dataPath != "") the call self-deadlocks. Existing unit tests
// escaped because they construct the ledger with dataPath="" and save() returns
// early before locking. This test uses a real temp-dir dataPath, runs the full
// Propose → Ratify flow under a timeout guard, and verifies the state was
// actually persisted and is reloadable.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGovernanceProposeRatify_PersistsWithRealDataPath hangs (deadlocks) on the
// pre-fix code and passes after the saveLocked split.
func TestGovernanceProposeRatify_PersistsWithRealDataPath(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "governance.json")
	voters := func() []string { return []string{"n1", "n2"} }

	g := NewGovernanceLedger("self", voters, dataPath)

	// Capture the Propose-returned ID so the reloaded proposal can be matched
	// against the exact persisted identity (fixes a never-firing
	// self-comparison `props[0].ID != props[0].ID`).
	var proposedID string
	done := make(chan struct{})
	go func() {
		defer close(done)
		p, err := g.Propose("n1", GovTypeAllowModel, "allow gpt-x", json.RawMessage(`{"model":"gpt-x"}`))
		if err != nil {
			t.Errorf("Propose: %v", err)
			return
		}
		proposedID = p.ID
		if _, err := g.Ratify(p.ID, "n1", true); err != nil {
			t.Errorf("Ratify: %v", err)
			return
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("governance Propose/Ratify deadlocked (save-in-lock); g.mu held forever")
	}

	// The ledger must be persisted to the real dataPath…
	if _, err := os.Stat(dataPath); err != nil {
		t.Fatalf("governance file not persisted at %s: %v", dataPath, err)
	}

	// …and a freshly-constructed ledger on the same path must reload it.
	g2 := NewGovernanceLedger("self", voters, dataPath)
	props := g2.List("")
	if len(props) != 1 {
		t.Fatalf("expected 1 proposal after reload, got %d", len(props))
	}
	if proposedID == "" {
		t.Fatal("Propose returned an empty proposal ID")
	}
	if props[0].ID != proposedID {
		t.Fatalf("reloaded proposal ID = %q, want %q (persistence lost identity)", props[0].ID, proposedID)
	}
	if props[0].Status == "" {
		t.Fatalf("reloaded proposal has empty status: %+v", props[0])
	}
	// The single approval by n1 should have auto-ratified (supermajority of 2
	// voters needs 2; a lone self-ratify may stay open — assert only that the
	// reloaded chain is intact and the proposal is queryable).
	if got := len(props); got != 1 {
		t.Fatalf("unexpected proposal count: %d", got)
	}

	// The proposal ID seen by the reloaded ledger must match the in-memory one
	// (hash-chain intact, sequence persisted).
	if props[0].Seq != 1 {
		t.Fatalf("reloaded proposal Seq = %d, want 1 (persistence dropped state)", props[0].Seq)
	}
}
