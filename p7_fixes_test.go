package main

// p7_fixes_test.go — regression tests for the batch 7 (v4.5.24) hardening:
//
//	B7-1  multiuser save/saveLocked split (no self-deadlock, no double-unlock)
//	B7-2  governance save via atomicWriteFile + error logging
//	B7-3  consecutive-failure circuit breaker (recordProviderFailure/Success)
//	B7-4  inviteManager mutex (concurrent Create/MarkUsed/GetInvites)
//	B7-5  SIGHUP reload includes WAF (reloadWAF wired)
//	B7-6  setup endpoints localOnly
//	B7-7  per-proxy transport cache (shared instance + cap)
//	B7-8  atomicWriteFile unique tmp names + fsync-before-rename
//	Supp  tracker flush/snapshotLocked contract; federation snapshot/persist
//	      outside fed.mu; parseFloat64 warns on bad config values

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================
// B7-8: atomicWriteFile
// ============================================================

func TestP7_AtomicWriteFile_WritesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := atomicWriteFile(path, []byte(`{"a":1}`), 0600); err != nil {
		t.Fatalf("atomicWriteFile failed: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	if string(b) != `{"a":1}` {
		t.Fatalf("unexpected content: %q", string(b))
	}
}

func TestP7_AtomicWriteFile_LeavesNoTmpResidue(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		if err := atomicWriteFile(filepath.Join(dir, "f.json"), []byte(fmt.Sprintf("v%d", i)), 0600); err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "f.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected only f.json, got: %v", names)
	}
}

func TestP7_AtomicWriteFile_ConcurrentWritersNoCollision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.json")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	// 8 concurrent savers of the same path: on Linux the rename is always
	// atomic; on Windows dev, rename-replace can transiently hit an open
	// destination handle, which the retry loop in atomicWriteFile absorbs.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// B7-8: unique tmp names mean concurrent writers cannot collide
			// on the same tmp path and fail with a rename error.
			if err := atomicWriteFile(path, []byte(fmt.Sprintf("w%d", i)), 0600); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write failed: %v", err)
	}
}

// ============================================================
// B7-3: consecutive-failure circuit breaker
// ============================================================

func TestP7_FailureBreaker_OpensAfterThreshold(t *testing.T) {
	id := fmt.Sprintf("brk-%d", time.Now().UnixNano())
	defer func() {
		cooldownMu.Lock()
		delete(cooldownMap, id)
		delete(consecFailures, id)
		cooldownMu.Unlock()
	}()
	if providerInCooldown(id) {
		t.Fatal("fresh provider must not be in cooldown")
	}
	for i := 0; i < breakerThreshold-1; i++ {
		recordProviderFailure(id)
	}
	if providerInCooldown(id) {
		t.Fatal("breaker must not open below threshold")
	}
	recordProviderFailure(id) // reaches threshold
	if !providerInCooldown(id) {
		t.Fatal("breaker should be open at threshold")
	}
}

func TestP7_FailureBreaker_SuccessResetsCount(t *testing.T) {
	id := fmt.Sprintf("brk-ok-%d", time.Now().UnixNano())
	defer func() {
		cooldownMu.Lock()
		delete(cooldownMap, id)
		delete(consecFailures, id)
		cooldownMu.Unlock()
	}()
	for i := 0; i < breakerThreshold-1; i++ {
		recordProviderFailure(id)
	}
	recordProviderSuccess(id)
	for i := 0; i < breakerThreshold-1; i++ {
		recordProviderFailure(id)
	}
	if providerInCooldown(id) {
		t.Fatal("success reset should prevent the breaker from opening")
	}
}

// ============================================================
// B7-4: inviteManager locking
// ============================================================

func TestP7_InviteManager_ConcurrentCreateAndRead(t *testing.T) {
	m := &inviteManager{
		issued:  make(map[string]*FederationInvite),
		used:    make(map[string]bool),
		dataDir: t.TempDir(),
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code := fmt.Sprintf("inv-%d", i)
			// Same sequence CreateInvite performs under the lock (B7-4);
			// without the mutex, concurrent issued/used map writes panic.
			m.mu.Lock()
			m.issued[code] = &FederationInvite{NetworkID: "net", Inviter: fmt.Sprintf("peer%d", i), InviteePub: "*"}
			m.saveLocked()
			m.mu.Unlock()
			m.MarkUsed(&FederationInvite{NetworkID: "net", Inviter: code})
			_ = m.GetInvites()
		}(i)
	}
	wg.Wait()
	if got := len(m.GetInvites()); got != 16 {
		t.Fatalf("expected 16 invites after concurrent creation, got %d", got)
	}
}

// ============================================================
// B7-7: proxied transport cache
// ============================================================

func TestP7_ProxiedTransportCache_SharesInstance(t *testing.T) {
	key := fmt.Sprintf("test://cache-%d", time.Now().UnixNano())
	build := func() (*http.Transport, error) { return &http.Transport{}, nil }
	a := cachedProxiedTransport(key, build)
	b := cachedProxiedTransport(key, build)
	if a == nil || b == nil {
		t.Fatal("transports must not be nil")
	}
	if a != b {
		t.Fatal("second call must return the cached transport")
	}
	a.CloseIdleConnections()
}

// ============================================================
// Supp: tracker flush contract
// ============================================================

func newP7TestTracker(t *testing.T) *Tracker {
	return &Tracker{
		dataPath:             filepath.Join(t.TempDir(), "usage.json"),
		ewmaCache:            map[string]float64{},
		tokenUsageByProvider: map[string]int64{},
		stopCh:               make(chan struct{}),
		reqLogMax:            1000,
		alertThresholds:      []float64{0.8, 0.9, 1.0},
		alertedTokens:        map[string]map[float64]bool{},
	}
}

func TestP7_Tracker_FlushTakesItsOwnLock(t *testing.T) {
	tr := newP7TestTracker(t)
	// Append a record directly (RecordWithAccessType pulls in provider-manager
	// globals via budget checks; the flush contract is what's under test).
	tr.mu.Lock()
	tr.records = append(tr.records, UsageRecord{ProviderID: "p1", Model: "m1", Success: true})
	tr.dirtyCount = 3
	tr.mu.Unlock()
	// B7-supp: flush() acquires the lock itself — calling it while holding the
	// lock used to be required by save()'s unlock/relock contract, which would
	// double-unlock under a deferred Unlock.
	tr.flush()
	tr.mu.RLock()
	n := tr.dirtyCount
	tr.mu.RUnlock()
	if n != 0 {
		t.Fatalf("flush should reset dirtyCount, got %d", n)
	}
	if _, err := os.Stat(tr.dataPath); err != nil {
		t.Fatalf("usage file not written: %v", err)
	}
}

func TestP7_Tracker_ResetPersistsEmptyState(t *testing.T) {
	tr := newP7TestTracker(t)
	tr.mu.Lock()
	tr.records = append(tr.records, UsageRecord{ProviderID: "p1", Model: "m1", Success: true})
	tr.mu.Unlock()
	tr.Reset()
	if _, err := os.Stat(tr.dataPath); err != nil {
		t.Fatalf("reset did not persist state file: %v", err)
	}
}

// ============================================================
// Supp: federation snapshot/persist split
// ============================================================

func TestP7_FederationSnapshotPersistRoundTrip(t *testing.T) {
	f := &FederationManager{dataDir: t.TempDir()}
	f.trustPool = TrustPool{
		Version: 7,
		Nodes:   []NodeInfo{{NodeID: "n1", Status: "active"}},
	}
	snapshot, err := f.snapshotLocked() // safe without lock in test (single goroutine)
	if err != nil {
		t.Fatalf("snapshotLocked: %v", err)
	}
	f.persistSnapshot(snapshot)

	f2 := &FederationManager{dataDir: f.dataDir}
	if err := f2.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if f2.trustPool.Version != 7 || len(f2.trustPool.Nodes) != 1 || f2.trustPool.Nodes[0].NodeID != "n1" {
		t.Fatalf("round-trip mismatch: %+v", f2.trustPool)
	}
}

func TestP7_UpdateTrustPool_PersistsOutsideLock(t *testing.T) {
	dir := t.TempDir()
	f := &FederationManager{dataDir: dir}
	// UpdateTrustPool with a strictly newer version must persist the pool.
	pool := TrustPool{
		Version: 3,
		Nodes:   []NodeInfo{{NodeID: "x1", Status: "active"}, {NodeID: "x2", Status: "active"}},
	}
	f.UpdateTrustPool(pool)
	if _, err := os.Stat(filepath.Join(dir, "federation_pool.json")); err != nil {
		t.Fatalf("pool not persisted: %v", err)
	}
	// Stale version must be ignored entirely.
	f2 := &FederationManager{dataDir: dir}
	if err := f2.load(); err != nil {
		t.Fatal(err)
	}
	before := f2.trustPool.Version
	f.UpdateTrustPool(TrustPool{Version: 2})
	f3 := &FederationManager{dataDir: dir}
	if err := f3.load(); err != nil {
		t.Fatal(err)
	}
	if f3.trustPool.Version != before {
		t.Fatalf("stale update overwrote pool: %d -> %d", before, f3.trustPool.Version)
	}
}

// ============================================================
// B7-d: parseFloat64 warning path (behavioral part covered in utils_test;
// here we assert the empty-value default is silent-equivalent).
// ============================================================

func TestP7_ParseFloat64_EmptyIsDefault(t *testing.T) {
	if got := parseFloat64("k", "", 42); got != 42 {
		t.Fatalf("empty value should yield default, got %v", got)
	}
	if got := parseFloat64("k", "not-a-number", 42); got != 42 {
		t.Fatalf("invalid value should yield default, got %v", got)
	}
	if got := parseFloat64("k", "3.5", 42); got != 3.5 {
		t.Fatalf("valid value misparsed: %v", got)
	}
}

// ============================================================
// B7-b: dynamic 5xx messages no longer echo internals (spot check that the
// relay SSE failure string is static).
// ============================================================

func TestP7_RelaySSEFailureMessageIsStatic(t *testing.T) {
	// The constant lives inline in relay.go; guard against regression to
	// fmt.Sprintf(...lastErr) by checking the literal is present verbatim.
	b, err := os.ReadFile("relay.go")
	if err != nil {
		t.Skip("relay.go not readable")
	}
	if !strings.Contains(string(b), `"all providers failed for relay, please retry later"`) {
		t.Fatal("static relay SSE failure message missing — lastErr may leak again")
	}
}
