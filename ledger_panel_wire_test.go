package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// P2-2(ii): the transparency *endpoints* shipped in P2-2(i), but no page
// consumed them — the deliverable ("面板") was missing. These tests pin the
// three links in the chain so the panel cannot silently rot again:
//
//	admin.html  -> loads /admin-ledger.js and owns the card container
//	admin-ledger.js -> calls the three admin ledger endpoints
//	the server  -> actually serves /admin-ledger.js from the embedded FS
//
// They are static/wiring checks on purpose: the data paths themselves are
// already covered by ledger_transparency_test.go / ledger_export_test.go /
// ledger_quota_consume_test.go.

func TestLedgerPanelWiredInAdminHTML(t *testing.T) {
	html, err := os.ReadFile("admin.html")
	if err != nil {
		t.Fatalf("read admin.html: %v", err)
	}
	h := string(html)
	for _, need := range []string{
		`id="ledgerTransparencyCard"`,
		`id="ledgerPanelBody"`,
		`onclick="loadLedgerTransparency()"`,
		`onclick="exportLedger('csv')"`,
		`onclick="exportLedger('json')"`,
	} {
		if !strings.Contains(h, need) {
			t.Errorf("admin.html missing ledger panel fragment: %q", need)
		}
	}
	// Same convention as TestQAFrontendWiring: assert *some* cache-busting
	// version rather than a literal that goes stale on every asset bump.
	if !regexp.MustCompile(`/admin-ledger\.js\?v=\d+`).MatchString(h) {
		t.Error("admin.html must load /admin-ledger.js with a ?v=<number> cache-busting query")
	}
}

func TestLedgerPanelScriptCallsRealEndpoints(t *testing.T) {
	js, err := os.ReadFile("admin-ledger.js")
	if err != nil {
		t.Fatalf("read admin-ledger.js: %v", err)
	}
	j := string(js)
	for _, need := range []string{
		"/api/admin/ledger/transparency",
		"/api/admin/ledger/contribution-quota",
		"/api/admin/ledger/export?format=",
		"function loadLedgerTransparency",
		"function exportLedger",
	} {
		if !strings.Contains(j, need) {
			t.Errorf("admin-ledger.js missing %q", need)
		}
	}
	// Public-welfare wording guard: the panel must not reintroduce the
	// token-economy vocabulary the project forbids. Explicit *disclaimers*
	// ("不是代币") are exactly what we want, so they are removed before the
	// check — only an affirmative use of the banned terms fails.
	stripped := j
	for _, disclaimer := range []string{"不是代币", "不是货币", "无手续费", "不可交易", "不可提现", "不通胀"} {
		stripped = strings.ReplaceAll(stripped, disclaimer, "")
	}
	for _, banned := range []string{"代币", "积分", "抽成", "分润", "挖矿"} {
		if strings.Contains(stripped, banned) {
			t.Errorf("admin-ledger.js must not use forbidden economic wording %q (disclaimers excepted)", banned)
		}
	}
	// Ledger values are user/peer controlled (peer_id, model_id) — they must be
	// escaped before hitting innerHTML.
	if !strings.Contains(j, "escapeHtml(") {
		t.Error("admin-ledger.js must escape ledger-provided strings via escapeHtml()")
	}
}

func TestAdminLedgerJSServedFromEmbeddedFS(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAdminLedgerJS(rec, httptest.NewRequest(http.MethodGet, "/admin-ledger.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin-ledger.js = %d, want 200 (is the file in the //go:embed list?)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want a javascript type", ct)
	}
	if !strings.Contains(rec.Body.String(), "loadLedgerTransparency") {
		t.Error("served /admin-ledger.js does not contain loadLedgerTransparency")
	}
}
