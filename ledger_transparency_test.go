package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLedgerTransparency(t *testing.T) {
	g, _ := NewGossipLedger("A")
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "peerX", ModelID: "gpt-4", Tokens: 10, Timestamp: time.Now()})
	g.RecordContribution(&ContributionRecord{ID: "c2", PeerID: "peerX", ModelID: "gpt-4", Tokens: 5, Timestamp: time.Now()})
	g.RecordContribution(&ContributionRecord{ID: "c3", PeerID: "peerY", ModelID: "claude", Tokens: 20, Timestamp: time.Now()})

	tp := g.GetTransparency()
	if tp.TotalTokens != 35 {
		t.Fatalf("total tokens = %d, want 35", tp.TotalTokens)
	}
	if tp.ContributionCount != 3 {
		t.Fatalf("contrib count = %d, want 3", tp.ContributionCount)
	}
	if tp.ByModel["gpt-4"] != 15 {
		t.Fatalf("gpt-4 tokens = %d, want 15", tp.ByModel["gpt-4"])
	}
	if tp.ByModel["claude"] != 20 {
		t.Fatalf("claude tokens = %d, want 20", tp.ByModel["claude"])
	}
	if tp.ByPeer["peerX"] != 15 {
		t.Fatalf("peerX tokens = %d, want 15", tp.ByPeer["peerX"])
	}
	if tp.ByPeer["peerY"] != 20 {
		t.Fatalf("peerY tokens = %d, want 20", tp.ByPeer["peerY"])
	}
	if !tp.ChainValid {
		t.Fatal("chain should be valid for a fresh ledger")
	}
}

func TestLedgerTransparencyHandler(t *testing.T) {
	g, _ := NewGossipLedger("A")
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "peerX", ModelID: "gpt-4", Tokens: 10, Timestamp: time.Now()})

	saved := contributionLedger
	contributionLedger = g
	defer func() { contributionLedger = saved }()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/ledger/transparency", nil)
	w := httptest.NewRecorder()
	handleAdminLedgerTransparency(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var tp LedgerTransparency
	if err := json.NewDecoder(w.Body).Decode(&tp); err != nil {
		t.Fatal(err)
	}
	if tp.TotalTokens != 10 {
		t.Fatalf("total tokens = %d, want 10", tp.TotalTokens)
	}
}
