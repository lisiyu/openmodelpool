package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLedgerExportCSV(t *testing.T) {
	g, _ := NewGossipLedger("A")
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "peerX", ModelID: "gpt-4", Provider: "openai", Tokens: 10, ValueUSD: 0.01, Timestamp: time.Now()})
	csvStr, err := g.ExportContributionsCSV()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csvStr, "id,peer_id,model_id,provider,tokens,value_usd,timestamp") {
		t.Fatalf("missing CSV header: %s", csvStr)
	}
	if !strings.Contains(csvStr, "c1,peerX,gpt-4,openai,10") {
		t.Fatalf("missing CSV row: %s", csvStr)
	}
}

func TestLedgerExportJSON(t *testing.T) {
	g, _ := NewGossipLedger("A")
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "peerX", ModelID: "gpt-4", Tokens: 10, Timestamp: time.Now()})
	data, err := g.ExportLedgerJSON()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["contributions"]; !ok {
		t.Fatal("export missing contributions key")
	}
	if _, ok := m["transactions"]; !ok {
		t.Fatal("export missing transactions key")
	}
}

func TestLedgerExportHandler(t *testing.T) {
	g, _ := NewGossipLedger("A")
	g.RecordContribution(&ContributionRecord{ID: "c1", PeerID: "peerX", ModelID: "gpt-4", Tokens: 10, Timestamp: time.Now()})

	saved := contributionLedger
	contributionLedger = g
	defer func() { contributionLedger = saved }()

	// CSV via handler
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ledger/export?format=csv", nil)
	w := httptest.NewRecorder()
	handleLedgerExport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("csv status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "c1,peerX") {
		t.Fatalf("csv body unexpected: %s", w.Body.String())
	}

	// JSON via handler (default)
	req2 := httptest.NewRequest(http.MethodGet, "/api/admin/ledger/export", nil)
	w2 := httptest.NewRecorder()
	handleLedgerExport(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("json status %d", w2.Code)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["contributions"]; !ok {
		t.Fatal("json export missing contributions")
	}
}
