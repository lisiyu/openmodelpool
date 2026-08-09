package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// contributor entitlement covers the request -> drawn 1:1, remainder shrinks.
func TestContributionQuotaConsume(t *testing.T) {
	tr := initContributionQuotaTracker(t.TempDir())
	tr.Accrue("donor", 1000)

	ok, remaining := tr.Consume("donor", 400)
	if !ok || remaining != 600 {
		t.Fatalf("first draw: ok=%v remaining=%d, want true/600", ok, remaining)
	}
	q := tr.GetQuota("donor")
	if q.ConsumedQuota != 400 || q.RemainingQuota != 600 || q.EarnedFreeQuota != 1000 {
		t.Fatalf("row after draw: %+v", q)
	}
	if tr.TotalConsumed() != 400 {
		t.Fatalf("total consumed = %d, want 400", tr.TotalConsumed())
	}
}

// Not enough entitlement (or unknown peer) must be a no-op, never a rejection.
// The caller falls back to the open community pool.
func TestContributionQuotaConsumeInsufficientIsNoOp(t *testing.T) {
	tr := initContributionQuotaTracker(t.TempDir())
	tr.Accrue("donor", 100)

	ok, remaining := tr.Consume("donor", 500)
	if ok {
		t.Fatal("draw beyond entitlement must not succeed")
	}
	if remaining != 100 {
		t.Fatalf("remaining = %d, want the untouched 100", remaining)
	}
	if q := tr.GetQuota("donor"); q.ConsumedQuota != 0 {
		t.Fatalf("failed draw must not consume anything: %+v", q)
	}

	if ok, _ := tr.Consume("stranger", 1); ok {
		t.Fatal("unknown peer must not be able to draw")
	}
}

// Refund returns the unused part of a reservation, and never over-credits.
func TestContributionQuotaRefund(t *testing.T) {
	tr := initContributionQuotaTracker(t.TempDir())
	tr.Accrue("donor", 1000)
	tr.Consume("donor", 400)

	tr.Refund("donor", 150)
	if q := tr.GetQuota("donor"); q.ConsumedQuota != 250 || q.RemainingQuota != 750 {
		t.Fatalf("after refund: %+v", q)
	}
	// Over-refunding clamps at zero — no free tokens minted.
	tr.Refund("donor", 9999)
	if q := tr.GetQuota("donor"); q.ConsumedQuota != 0 || q.RemainingQuota != 1000 {
		t.Fatalf("over-refund must clamp: %+v", q)
	}
}

// Consumption state must survive a restart.
func TestContributionQuotaConsumePersistence(t *testing.T) {
	dir := t.TempDir()
	tr := initContributionQuotaTracker(dir)
	tr.Accrue("donor", 800)
	tr.Consume("donor", 300)

	reloaded := initContributionQuotaTracker(dir)
	q := reloaded.GetQuota("donor")
	if q == nil || q.ConsumedQuota != 300 || q.RemainingQuota != 500 {
		t.Fatalf("consumption did not persist: %+v", q)
	}
}

// A verified contributor identity draws on its entitlement.
func TestTryContributorDraw(t *testing.T) {
	saved := contribQuotaTracker
	contribQuotaTracker = initContributionQuotaTracker(t.TempDir())
	defer func() { contribQuotaTracker = saved }()
	contribQuotaTracker.Accrue("node-donor", 5000)

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("X-Node-ID", "node-donor")

	draw := tryContributorDraw(r, 2000)
	if !draw.OK || draw.PeerID != "node-donor" || draw.Remaining != 3000 {
		t.Fatalf("draw = %+v, want OK with 3000 remaining", draw)
	}

	// settle() refunds the unused part once real usage is known.
	draw.settle(500)
	if got := contribQuotaTracker.Remaining("node-donor"); got != 4500 {
		t.Fatalf("remaining after settle = %d, want 4500", got)
	}
	// settle(0) means "usage unknown" — the reservation stands.
	d2 := tryContributorDraw(r, 1000)
	d2.settle(0)
	if got := contribQuotaTracker.Remaining("node-donor"); got != 3500 {
		t.Fatalf("remaining after settle(0) = %d, want 3500", got)
	}
}

// No identity, or an identity with nothing left, must NOT be treated as a
// rejection — the caller keeps the open community path. This is the
// 善意默认 guarantee expressed as a test.
func TestTryContributorDrawFallsThrough(t *testing.T) {
	saved := contribQuotaTracker
	contribQuotaTracker = initContributionQuotaTracker(t.TempDir())
	defer func() { contribQuotaTracker = saved }()

	anon := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if draw := tryContributorDraw(anon, 100); draw.OK || draw.PeerID != "" {
		t.Fatalf("anonymous request must not draw: %+v", draw)
	}

	contribQuotaTracker.Accrue("small-donor", 10)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("X-Node-ID", "small-donor")
	if draw := tryContributorDraw(r, 4096); draw.OK {
		t.Fatal("exhausted entitlement must fall through, not draw")
	}
	if got := contribQuotaTracker.Remaining("small-donor"); got != 10 {
		t.Fatalf("fall-through must leave the entitlement intact, got %d", got)
	}

	// A nil tracker keeps the whole path inert.
	contribQuotaTracker = nil
	if draw := tryContributorDraw(r, 100); draw.OK {
		t.Fatal("nil tracker must be inert")
	}
}

// The internal accounting marker must never be honoured from the wire.
func TestStripInternalQuotaHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set(headerQuotaCharged, "contributor")
	stripInternalQuotaHeaders(r)
	if r.Header.Get(headerQuotaCharged) != "" {
		t.Fatal("client-supplied quota marker must be stripped")
	}
}

// A malformed / oversized node id must not resolve to an identity.
func TestVerifiedContributorIDRejectsGarbage(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("X-Node-ID", "bad id/../with slashes")
	if id := verifiedContributorID(r); id != "" {
		t.Fatalf("garbage node id resolved to %q", id)
	}
}

// The admin transparency endpoint reports consumption alongside accrual.
func TestAdminContributionQuotaReportsConsumption(t *testing.T) {
	saved := contribQuotaTracker
	contribQuotaTracker = initContributionQuotaTracker(t.TempDir())
	defer func() { contribQuotaTracker = saved }()
	contribQuotaTracker.Accrue("donor", 900)
	contribQuotaTracker.Consume("donor", 200)

	w := httptest.NewRecorder()
	handleAdminLedgerContributionQuota(w, httptest.NewRequest(http.MethodGet, "/api/admin/ledger/contribution-quota", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`"total_contributed_tokens":900`, `"total_consumed_tokens":200`, `"total_remaining_tokens":700`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s\nbody: %s", want, body)
		}
	}
}
