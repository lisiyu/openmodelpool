package main

import (
	"testing"
)

func TestBuildManifest(t *testing.T) {
	g, err := NewGossipLedger("node1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "node1", ModelID: "gpt", Tokens: 10}); err != nil {
		t.Fatal(err)
	}
	m := BuildManifest(g)
	if len(m.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(m.Entries))
	}
	if m.Entries["c1"].ContentHash == "" {
		t.Fatal("empty content hash")
	}
	if m.Entries["c1"].RecordType != "contrib" {
		t.Fatalf("wrong record type: %s", m.Entries["c1"].RecordType)
	}
	// Tampering with a business field must change the digest.
	g.recs["c1"].Tokens = 999
	m2 := BuildManifest(g)
	if m2.Entries["c1"].ContentHash == m.Entries["c1"].ContentHash {
		t.Fatal("tampering with tokens should change the manifest hash")
	}
}

func TestDiffManifests(t *testing.T) {
	la, _ := NewGossipLedger("A")
	lb, _ := NewGossipLedger("B")
	la.RecordContribution(&ContributionRecord{ID: "a1", PeerID: "A", Tokens: 1})
	lb.RecordContribution(&ContributionRecord{ID: "b1", PeerID: "B", Tokens: 1})

	diff := DiffManifests(BuildManifest(la), BuildManifest(lb))
	if len(diff.Missing) != 1 || diff.Missing[0].ID != "b1" {
		t.Fatalf("missing wrong: %+v", diff.Missing)
	}
	if len(diff.Extra) != 1 || diff.Extra[0].ID != "a1" {
		t.Fatalf("extra wrong: %+v", diff.Extra)
	}
	if len(diff.Divergent) != 0 {
		t.Fatalf("divergent should be empty: %+v", diff.Divergent)
	}

	// Same id, different content → divergent (fork / tamper).
	lc, _ := NewGossipLedger("C")
	ld, _ := NewGossipLedger("D")
	lc.RecordContribution(&ContributionRecord{ID: "x1", PeerID: "C", Tokens: 1})
	ld.RecordContribution(&ContributionRecord{ID: "x1", PeerID: "D", Tokens: 2})
	diff2 := DiffManifests(BuildManifest(lc), BuildManifest(ld))
	if len(diff2.Divergent) != 1 || diff2.Divergent[0] != "x1" {
		t.Fatalf("divergent wrong: %+v", diff2.Divergent)
	}
}

func TestReplicaRedundancy(t *testing.T) {
	primary, _ := NewGossipLedger("p")
	r1, _ := NewGossipLedger("r1")
	r2, _ := NewGossipLedger("r2")
	rm := NewReplicaManager(primary, r1, r2)

	rec := &ContributionRecord{ID: "c1", PeerID: "p", ModelID: "gpt", Tokens: 100}
	n, err := rm.ReplicateContribution(rec)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("have %d copies, want 3", n)
	}

	ok, report := rm.VerifyConsistency()
	if !ok {
		t.Fatalf("replicas should be consistent: %v", report)
	}
	if report["p"] != 1 || report["r1"] != 1 || report["r2"] != 1 {
		t.Fatalf("replica counts wrong: %v", report)
	}

	// Tamper with one replica (not the signature — a data mutation).
	r1.recs["c1"].Tokens = 999
	ok2, _ := rm.VerifyConsistency()
	if ok2 {
		t.Fatal("tampered replica must be detected as inconsistent")
	}
}
