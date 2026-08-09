package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newLedgerTestServer wires the ledger HTTP endpoints to an in-memory ledger.
func newLedgerTestServer(t *testing.T, g *GossipLedger) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ledger/__manifest", func(w http.ResponseWriter, r *http.Request) { handleLedgerManifestFor(w, r, g) })
	mux.HandleFunc("POST /ledger/__sync", func(w http.ResponseWriter, r *http.Request) { handleLedgerSyncFor(w, r, g) })
	mux.HandleFunc("GET /ledger/__record", func(w http.ResponseWriter, r *http.Request) { handleLedgerRecordFor(w, r, g) })
	return httptest.NewServer(mux)
}

func TestLedgerReplicationPush(t *testing.T) {
	primary, _ := NewGossipLedger("A")
	peer, _ := NewGossipLedger("B")
	ps := newLedgerTestServer(t, primary)
	bs := newLedgerTestServer(t, peer)
	defer ps.Close()
	defer bs.Close()

	rep := NewLedgerReplicator(primary, "A", bs.URL)
	n, err := rep.ReplicateContribution(&ContributionRecord{ID: "c1", PeerID: "A", ModelID: "gpt", Tokens: 50, Timestamp: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 replica accepted, got %d", n)
	}
	if _, err := peer.GetContribution("c1"); err != nil {
		t.Fatalf("peer did not receive replicated record: %v", err)
	}
}

func TestLedgerReconcileHeal(t *testing.T) {
	primary, _ := NewGossipLedger("A")
	peer, _ := NewGossipLedger("B")
	ps := newLedgerTestServer(t, primary)
	bs := newLedgerTestServer(t, peer)
	defer ps.Close()
	defer bs.Close()

	primary.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "A", Tokens: 1, Timestamp: time.Now()})
	peer.RecordContribution(&ContributionRecord{ID: "c2", PeerID: "B", Tokens: 1, Timestamp: time.Now()})

	rep := NewLedgerReplicator(primary, "A", bs.URL)
	diff, healed, err := rep.ReconcileWith(bs.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Missing) != 1 || diff.Missing[0].ID != "c2" {
		t.Fatalf("want missing c2, got %+v", diff.Missing)
	}
	if healed != 1 {
		t.Fatalf("want 1 healed record, got %d", healed)
	}
	if _, err := primary.GetContribution("c2"); err != nil {
		t.Fatalf("primary not healed from peer: %v", err)
	}
}

func TestLedgerReconcileDivergent(t *testing.T) {
	primary, _ := NewGossipLedger("A")
	peer, _ := NewGossipLedger("B")
	ps := newLedgerTestServer(t, primary)
	bs := newLedgerTestServer(t, peer)
	defer ps.Close()
	defer bs.Close()

	ts := time.Now()
	primary.RecordContribution(&ContributionRecord{ID: "x1", PeerID: "A", Tokens: 1, Timestamp: ts})
	peer.RecordContribution(&ContributionRecord{ID: "x1", PeerID: "B", Tokens: 2, Timestamp: ts})

	rep := NewLedgerReplicator(primary, "A", bs.URL)
	diff, healed, err := rep.ReconcileWith(bs.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Divergent) != 1 || diff.Divergent[0] != "x1" {
		t.Fatalf("want 1 divergent (x1), got %+v", diff.Divergent)
	}
	if healed != 0 {
		t.Fatalf("divergent records must NOT be auto-overwritten, healed=%d", healed)
	}
}

func TestLedgerManifestHandler(t *testing.T) {
	g, _ := NewGossipLedger("A")
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "A", Tokens: 1, Timestamp: time.Now()})
	ps := newLedgerTestServer(t, g)
	defer ps.Close()

	resp, err := http.Get(ps.URL + "/ledger/__manifest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var m LedgerManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 {
		t.Fatalf("want 1 manifest entry, got %d", len(m.Entries))
	}
}

func TestLedgerRecordHandler(t *testing.T) {
	g, _ := NewGossipLedger("A")
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "A", Tokens: 1, Timestamp: time.Now()})
	ps := newLedgerTestServer(t, g)
	defer ps.Close()

	resp, err := http.Get(ps.URL + "/ledger/__record?id=c1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var rr ledgerRecordResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		t.Fatal(err)
	}
	if rr.RecordType != "contrib" {
		t.Fatalf("wrong record type: %s", rr.RecordType)
	}

	resp2, err := http.Get(ps.URL + "/ledger/__record?id=nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 for missing record, got %d", resp2.StatusCode)
	}
}

func TestRecordContributionTriggerNoopWhenReplicatorNil(t *testing.T) {
	// In unit tests ledgerReplicator is nil, so recording must behave exactly
	// as before (no goroutine leak / panic). This guards the additive hook.
	saved := ledgerReplicator
	ledgerReplicator = nil
	defer func() { ledgerReplicator = saved }()

	g, _ := NewGossipLedger("A")
	if _, err := g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "A", Tokens: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.GetContribution("c1"); err != nil {
		t.Fatalf("record should be stored locally: %v", err)
	}
}

func TestLedgerReconcileAll(t *testing.T) {
	primary, _ := NewGossipLedger("A")
	peer, _ := NewGossipLedger("B")
	ps := newLedgerTestServer(t, primary)
	bs := newLedgerTestServer(t, peer)
	defer ps.Close()
	defer bs.Close()

	primary.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "A", Tokens: 1, Timestamp: time.Now()})
	peer.RecordContribution(&ContributionRecord{ID: "c2", PeerID: "B", Tokens: 1, Timestamp: time.Now()})

	rep := NewLedgerReplicator(primary, "A", bs.URL)
	results := rep.ReconcileAll()
	if len(results) != 1 {
		t.Fatalf("want 1 peer result, got %d", len(results))
	}
	if results[0].Missing != 1 || results[0].Healed != 1 {
		t.Fatalf("want missing=1 healed=1, got %+v", results[0])
	}
	if _, err := primary.GetContribution("c2"); err != nil {
		t.Fatalf("ReconcileAll did not heal c2: %v", err)
	}
}
