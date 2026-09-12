package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 3 certified education/public-welfare quota grants: the store behaves
// like a small auditable ledger (append + revoke-flag, daily budget draw).

func newTestGrantManager(t *testing.T) *GrantQuotaManager {
	t.Helper()
	return NewGrantQuotaManager(filepath.Join(t.TempDir(), "grants.json"))
}

func TestGrant_AddAndList(t *testing.T) {
	g := newTestGrantManager(t)
	e, err := g.Grant("示例公益大学", "edu-org-key-1", 100000)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if e.ID == "" || e.Holder != "示例公益大学" || e.DailyTokens != 100000 || e.Revoked {
		t.Fatalf("unexpected entry: %+v", e)
	}
	got, ok := g.ActiveGrantByKey("edu-org-key-1")
	if !ok || got.ID != e.ID {
		t.Fatalf("ActiveGrantByKey = %+v,%v", got, ok)
	}
	if _, ok := g.ActiveGrantByKey("other-key"); ok {
		t.Fatal("unknown key must not match an active grant")
	}
}

func TestGrant_ValidationAndDuplicate(t *testing.T) {
	g := newTestGrantManager(t)
	if _, err := g.Grant("", "k", 100); err == nil {
		t.Error("empty holder must fail")
	}
	if _, err := g.Grant("h", "", 100); err == nil {
		t.Error("empty key_id must fail")
	}
	if _, err := g.Grant("h", "k", 0); err == nil {
		t.Error("non-positive daily_tokens must fail")
	}
	if _, err := g.Grant("h", "dup", 100); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	_, err := g.Grant("h2", "dup", 200)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate key must be rejected, got %v", err)
	}
}

func TestGrant_RevokeKeepsAuditTrail(t *testing.T) {
	g := newTestGrantManager(t)
	e, _ := g.Grant("公益组织A", "org-a", 5000)
	saved := g.GetGrants()
	if len(saved) != 1 || saved[0].ID != e.ID {
		t.Fatalf("saved = %+v", saved)
	}
	if err := g.Revoke(e.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := g.Revoke(e.ID); err == nil {
		t.Error("double revoke must fail")
	}
	if err := g.Revoke("g-unknown"); err == nil {
		t.Error("revoking unknown id must fail")
	}
	if _, ok := g.ActiveGrantByKey("org-a"); ok {
		t.Error("revoked grant must not be active")
	}
	list := g.GetGrants()
	if len(list) != 1 || !list[0].Revoked || list[0].RevokedAt == nil {
		t.Fatalf("revoked entry must stay in the audit list, got %+v", list)
	}
}

func TestGrant_DailyBudgetDraw(t *testing.T) {
	g := newTestGrantManager(t)
	_, err := g.Grant("h", "edu-key", 100)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !g.TryGrantDraw("edu-key", 40) {
		t.Fatal("draw 40/100 should succeed")
	}
	if !g.TryGrantDraw("edu-key", 60) {
		t.Fatal("draw 40+60=100 should succeed (at budget)")
	}
	if g.TryGrantDraw("edu-key", 1) {
		t.Fatal("draw beyond daily budget must fail")
	}
	// Unknown / revoked keys never draw.
	if g.TryGrantDraw("nobody", 1) {
		t.Error("unknown key must not draw")
	}
	// Non-positive draws are no-ops.
	if g.TryGrantDraw("edu-key", 0) {
		t.Error("zero draw must fail")
	}
	// Reload from disk: used-budget is runtime-only, but the grant itself
	// (100/day) survives so a fresh manager still honors its entries.
	g2 := NewGrantQuotaManager(g.path)
	if !g2.TryGrantDraw("edu-key", 100) {
		t.Error("reloaded manager should honor the persisted grant budget")
	}
}

func TestGrant_HandlerEndpoints(t *testing.T) {
	setupTestEnv(t)
	old := grantQuota
	defer func() { grantQuota = old }()
	grantQuota = newTestGrantManager(t)

	// List initially empty.
	rec := httptest.NewRecorder()
	handleAdminLedgerGrantsList(rec, httptest.NewRequest(http.MethodGet, "/api/admin/ledger/grants", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}

	// Create.
	body, _ := json.Marshal(map[string]any{"holder": "教育实验室", "key_id": "lab-key", "daily_tokens": 8888})
	rec = httptest.NewRecorder()
	handleAdminLedgerGrantsGrant(rec, httptest.NewRequest(http.MethodPost, "/api/admin/ledger/grants", bytes.NewReader(body)))
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), "lab-key") {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}

	// Duplicate -> 409.
	rec = httptest.NewRecorder()
	handleAdminLedgerGrantsGrant(rec, httptest.NewRequest(http.MethodPost, "/api/admin/ledger/grants", bytes.NewReader(body)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", rec.Code)
	}

	// Revoke.
	rec = httptest.NewRecorder()
	handleAdminLedgerGrantsRevoke(rec, httptest.NewRequest(http.MethodDelete, "/api/admin/ledger/grants?id=g1", nil))
	if rec.Code != 200 {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body.String())
	}
	if !grantQuota.GetGrants()[0].Revoked {
		t.Error("revoked flag not set")
	}
	// Missing id -> 400.
	rec = httptest.NewRecorder()
	handleAdminLedgerGrantsRevoke(rec, httptest.NewRequest(http.MethodDelete, "/api/admin/ledger/grants", nil))
	if rec.Code != 400 {
		t.Fatalf("revoke without id = %d", rec.Code)
	}
	// Unknown id -> 404.
	rec = httptest.NewRecorder()
	handleAdminLedgerGrantsRevoke(rec, httptest.NewRequest(http.MethodDelete, "/api/admin/ledger/grants?id=g-nope", nil))
	if rec.Code != 404 {
		t.Fatalf("revoke unknown = %d", rec.Code)
	}
}

func TestGrant_ManagerNilReturns503(t *testing.T) {
	setupTestEnv(t)
	old := grantQuota
	defer func() { grantQuota = old }()
	grantQuota = nil

	rec := httptest.NewRecorder()
	handleAdminLedgerGrantsList(rec, httptest.NewRequest(http.MethodGet, "/api/admin/ledger/grants", nil))
	if rec.Code != 503 {
		t.Fatalf("nil manager list = %d, want 503", rec.Code)
	}
}
