package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetContributorHonors_SortedByTokens(t *testing.T) {
	g, _ := NewGossipLedger("A")
	now := time.Now()
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "peerX", Tokens: 10, Timestamp: now.Add(-5 * time.Minute)})
	g.RecordContribution(&ContributionRecord{ID: "c2", PeerID: "peerX", Tokens: 5, Timestamp: now})
	g.RecordContribution(&ContributionRecord{ID: "c3", PeerID: "peerY", Tokens: 20, Timestamp: now})

	honors := g.GetContributorHonors()
	if len(honors) != 2 {
		t.Fatalf("honors len = %d, want 2", len(honors))
	}
	if honors[0].PeerID != "peerY" || honors[0].Tokens != 20 || honors[0].Records != 1 {
		t.Errorf("first honor = %+v, want peerY/tokens=20/records=1", honors[0])
	}
	if honors[1].PeerID != "peerX" || honors[1].Tokens != 15 || honors[1].Records != 2 {
		t.Errorf("second honor = %+v, want peerX/tokens=15/records=2", honors[1])
	}
	if !honors[1].LastSeen.Equal(now) {
		t.Errorf("peerX last_seen = %v, want latest record timestamp", honors[1].LastSeen)
	}
}

func TestGetContributorHonors_Empty(t *testing.T) {
	g, _ := NewGossipLedger("A")
	if honors := g.GetContributorHonors(); len(honors) != 0 {
		t.Fatalf("honors len = %d, want 0", len(honors))
	}
}

func TestHandleAdminLedgerContributors(t *testing.T) {
	g, _ := NewGossipLedger("A")
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "peerX", Tokens: 10, Timestamp: time.Now()})
	g.RecordContribution(&ContributionRecord{ID: "c2", PeerID: "peerX", Tokens: 5, Timestamp: time.Now()})
	g.RecordContribution(&ContributionRecord{ID: "c3", PeerID: "peerY", Tokens: 20, Timestamp: time.Now()})

	savedLedger, savedFed := contributionLedger, fed
	contributionLedger = g
	fed = nil // ensure short-id fallback path is exercised
	defer func() {
		contributionLedger = savedLedger
		fed = savedFed
	}()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/ledger/contributors?limit=1", nil)
	w := httptest.NewRecorder()
	handleAdminLedgerContributors(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Contributors      []ContributorHonorView `json:"contributors"`
		TotalContributors int                    `json:"total_contributors"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalContributors != 2 {
		t.Errorf("total_contributors = %d, want 2", resp.TotalContributors)
	}
	if len(resp.Contributors) != 1 {
		t.Fatalf("contributors len = %d, want 1 (limit)", len(resp.Contributors))
	}
	if resp.Contributors[0].PeerID != "peerY" || resp.Contributors[0].Tokens != 20 {
		t.Errorf("top contributor = %+v, want peerY/20", resp.Contributors[0])
	}
	if resp.Contributors[0].Name == "" {
		t.Error("name must not be empty (short-id fallback)")
	}
}

func TestHandleAdminLedgerContributors_LedgerNotReady(t *testing.T) {
	saved := contributionLedger
	contributionLedger = nil
	defer func() { contributionLedger = saved }()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/ledger/contributors", nil)
	w := httptest.NewRecorder()
	handleAdminLedgerContributors(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
