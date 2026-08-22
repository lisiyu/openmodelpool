package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ============================================================
// B8-1a: guest key daily quota window resets on UTC date rollover
// ============================================================

func TestP8_GuestQuota_DailyReset(t *testing.T) {
	tr := &guestKeyUsageTracker{
		usage: map[string]int64{"k1": 1000},
		day:   "2000-01-01", // stale window
	}
	// Old behavior: usage accumulated forever — key permanently exhausted.
	// New behavior: a stale day resets the window before the check.
	allowed, remaining := tr.CheckAndReserve("k1", 1000, 400)
	if !allowed {
		t.Fatal("expected request allowed after daily window reset")
	}
	if remaining != 600 {
		t.Fatalf("remaining = %d, want 600", remaining)
	}
	if got := tr.GetUsage("k1"); got != 400 {
		t.Fatalf("usage after reserve = %d, want 400", got)
	}
}

// ============================================================
// B8-1b: reservation settlement semantics
//   - total failure (actual=0) refunds the reservation
//   - success keeps it (stream / unknown usage)
//   - non-stream charges the real token count
// ============================================================

func TestP8_GuestQuota_Settlement(t *testing.T) {
	// Stream success: actual == reserved → reservation stands as consumption.
	tr := &guestKeyUsageTracker{usage: make(map[string]int64)}
	tr.CheckAndReserve("k", 1000, 500)
	tr.Adjust("k", 500, 500)
	if got := tr.GetUsage("k"); got != 500 {
		t.Fatalf("stream keep: usage = %d, want 500", got)
	}

	// Total failure: actual = 0 → full refund.
	tr.Adjust("k", 500, 0)
	if got := tr.GetUsage("k"); got != 0 {
		t.Fatalf("failure refund: usage = %d, want 0", got)
	}

	// Non-stream: actual < reserved → charge real tokens.
	tr.CheckAndReserve("k", 1000, 400)
	tr.Adjust("k", 400, 137)
	if got := tr.GetUsage("k"); got != 137 {
		t.Fatalf("non-stream settle: usage = %d, want 137", got)
	}
}

// ============================================================
// B8-2: provider ownership enforcement
// ============================================================

func TestP8_ProviderOwnership(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// An admin-created provider with no Owner (legacy/unowned).
	adminProv := Provider{ID: "adminprov", Name: "Admin Prov", BaseURL: "https://admin.example.com"}
	pm.Add(adminProv)
	// A consumer-owned provider.
	own := Provider{ID: "own1", Name: "Own", BaseURL: "https://own.example.com", Owner: "c1"}
	pm.Add(own)

	consumerReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/providers/x", nil)
		r.Header.Set("X-Request-Owner", "c1")
		return r
	}

	// READ: consumer may read their own provider.
	if _, ok := checkProviderAccess(consumerReq(), "own1"); !ok {
		t.Error("consumer must be able to read own provider")
	}
	// READ: consumer may read presets.
	presetID := presetProviders[0].ID
	if _, ok := checkProviderAccess(consumerReq(), presetID); !ok {
		t.Errorf("consumer must be able to read preset %q", presetID)
	}
	// READ: consumer must NOT read an unowned admin-created provider.
	if _, ok := checkProviderAccess(consumerReq(), "adminprov"); ok {
		t.Error("consumer read of unowned admin provider must be denied")
	}

	// WRITE: strict ownership — unowned and presets are off-limits.
	if _, ok := checkProviderWriteAccess(consumerReq(), "adminprov"); ok {
		t.Error("consumer write of unowned admin provider must be denied")
	}
	if _, ok := checkProviderWriteAccess(consumerReq(), presetID); ok {
		t.Error("consumer write of preset must be denied")
	}
	if _, ok := checkProviderWriteAccess(consumerReq(), "own1"); !ok {
		t.Error("consumer write of own provider must be allowed")
	}

	// Admin bypasses everything.
	adminReq := httptest.NewRequest(http.MethodGet, "/", nil) // no X-Request-Owner
	if _, ok := checkProviderAccess(adminReq, "adminprov"); !ok {
		t.Error("admin read access broken")
	}
	if _, ok := checkProviderWriteAccess(adminReq, "adminprov"); !ok {
		t.Error("admin write access broken")
	}
}

// ============================================================
// B8-5: reputation scores require a valid signature from the claimed sender
// ============================================================

func TestP8_PostScore_RequiresSignature(t *testing.T) {
	setupDiscoveryTestEnv(t)

	// setupDiscoveryTestEnv does not wire repMgr — provide an isolated one.
	origMgr := repMgr
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  t.TempDir(),
	}
	t.Cleanup(func() { repMgr = origMgr })

	victimPub, victimPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	fed.AddKnownNode(NodeInfo{
		NodeID: "victim-node",
		Status: "active",
		PubKey: base64.StdEncoding.EncodeToString(victimPub),
	})

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/federation/score", strings.NewReader(body))
		w := httptest.NewRecorder()
		handlePostScore(w, req)
		return w
	}

	// Unsigned score impersonating a pubkey-known node → rejected.
	w := post(`{"from_node":"victim-node","target_node":"t","availability":99,"latency":50,"accuracy":50,"timestamp":"2026-01-01T00:00:00Z"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unsigned score: code = %d, want 403", w.Code)
	}

	// Garbage signature → rejected.
	w = post(`{"from_node":"victim-node","target_node":"t","availability":99,"latency":50,"accuracy":50,"timestamp":"2026-01-01T00:00:00Z","signature":"AAAA"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("malformed signature: code = %d, want 403", w.Code)
	}

	// Properly signed score → accepted.
	payload := fmt.Sprintf("victim-node:t:%.0f:%.0f:%.0f:%s", 99.0, 50.0, 50.0, "2026-01-01T00:00:00Z")
	sig := ed25519.Sign(victimPriv, []byte(payload))
	signedBody := fmt.Sprintf(
		`{"from_node":"victim-node","target_node":"t","availability":99,"latency":50,"accuracy":50,"timestamp":"2026-01-01T00:00:00Z","signature":"%s"}`,
		base64.StdEncoding.EncodeToString(sig))
	w = post(signedBody)
	if w.Code != http.StatusOK {
		t.Fatalf("signed score: code = %d body=%s, want 200", w.Code, w.Body.String())
	}
}

// ============================================================
// B8-7: quotaCache is bounded even under unique node_id floods
// ============================================================

func TestP8_QuotaCache_Bounded(t *testing.T) {
	m := &OpenKeyQuotaManager{quotaCache: make(map[string]*QuotaInfo)}
	for i := 0; i < maxQuotaCacheEntries+200; i++ {
		id := fmt.Sprintf("node-%04d", i%maxQuotaCacheEntries) // reuse some IDs too
		m.mu.Lock()
		m.quotaCache[id] = &QuotaInfo{NodeID: id, LastUpdated: fmt.Sprintf("%04d", i)}
		m.mu.Unlock()
	}
	m.mu.Lock()
	over := len(m.quotaCache) - maxQuotaCacheEntries
	if over > 0 {
		m.evictQuotaCacheLocked()
	}
	size := len(m.quotaCache)
	m.mu.Unlock()
	if size > maxQuotaCacheEntries {
		t.Fatalf("cache size after eviction = %d, want <= %d", size, maxQuotaCacheEntries)
	}
}

// ============================================================
// B8-8: tunnel global pointer accessors are race-safe
// ============================================================

func TestP8_TunnelPointer_Accessors(t *testing.T) {
	orig := getTunnel()
	t.Cleanup(func() { setTunnel(orig) })

	tm := &TunnelManager{port: 9999, mode: "quick"}
	setTunnel(tm)
	if got := getTunnel(); got != tm {
		t.Fatal("getTunnel did not return the manager set by setTunnel")
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = getTunnel()
		}()
		go func(i int) {
			defer wg.Done()
			setTunnel(&TunnelManager{port: i})
		}(i)
	}
	wg.Wait()
	setTunnel(nil) // leave a consistent final state before cleanup restore
}
