package main

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// withNilGlobalPool swaps the package-global pool ledger to nil for the test
// and restores it afterwards, so the config-limit fallback paths are exercised
// deterministically regardless of test execution order.
func withNilGlobalPool(t *testing.T) {
	t.Helper()
	orig := globalPool
	globalPool = nil
	t.Cleanup(func() { globalPool = orig })
}

// P1: the private pool must actually deplete across Resolve calls. Before the
// fix, Resolve rebuilt every pool from the config limits on each call, so the
// private pool always looked full and the quota never ran out.
func TestQuotaPriorityManager_PrivatePoolDepletes(t *testing.T) {
	withNilGlobalPool(t)
	m := &quotaPriorityManager{enabled: true, privateLimit: 100, sharedLimit: 0, remoteLimit: 0}

	// 100 / 30 = 3 successful deductions (90 spent, 10 left); the 4th request
	// must be rejected because every pool is exhausted.
	for i := 0; i < 3; i++ {
		res := m.Resolve(KeyTypeGuest, 30)
		if !res.OK || res.Kind != PoolPrivate {
			t.Fatalf("resolve %d: expected private pool charged, got ok=%v kind=%s reason=%s",
				i, res.OK, res.Kind, res.Reason)
		}
	}
	if got := m.lazyPool(PoolPrivate).Remaining(); got != 10 {
		t.Fatalf("private pool should hold 10 after 3x30, got %d", got)
	}
	res := m.Resolve(KeyTypeGuest, 30)
	if res.OK {
		t.Fatalf("expected rejection once all pools are exhausted, got charged from %s", res.Kind)
	}
	if res.Reason == "" {
		t.Fatal("rejection must carry a reason")
	}
}

// The shared-pool fallback (no global pool ledger, single-node deployment)
// must also persist deductions across Resolve calls.
func TestQuotaPriorityManager_SharedFallbackPoolDepletes(t *testing.T) {
	withNilGlobalPool(t)
	m := &quotaPriorityManager{enabled: true, privateLimit: 10, sharedLimit: 60, remoteLimit: 0}

	// private(10) < 40, so the shared pool is charged: 60 -> 20.
	res := m.Resolve(KeyTypeGuest, 40)
	if !res.OK || res.Kind != PoolShared {
		t.Fatalf("expected shared pool charged, got ok=%v kind=%s reason=%s", res.OK, res.Kind, res.Reason)
	}
	if got := m.lazyPool(PoolShared).Remaining(); got != 20 {
		t.Fatalf("shared fallback pool should hold 20 after deduct, got %d", got)
	}
	// shared has 20 left < 40, remote is 0 -> rejected.
	res = m.Resolve(KeyTypeGuest, 40)
	if res.OK {
		t.Fatalf("expected rejection once shared fallback is exhausted, got charged from %s", res.Kind)
	}
}

// Concurrent Resolve calls must never over-issue a finite pool: exactly
// limit/amount requests succeed and the pool ends at exactly zero.
func TestQuotaPriorityManager_ConcurrentNoOverissue(t *testing.T) {
	withNilGlobalPool(t)
	m := &quotaPriorityManager{enabled: true, privateLimit: 1000, sharedLimit: 0, remoteLimit: 0}

	const workers = 50
	const perWorker = 20
	const amount = int64(10)
	var okCount atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if m.Resolve(KeyTypeGuest, amount).OK {
					okCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	// 1000 tokens / 10 per request = exactly 100 successful deductions.
	// 1000 attempts were made, so any over-issue or lost update shows up here.
	if got := okCount.Load(); got != 100 {
		t.Fatalf("expected exactly 100 successful deductions, got %d (over-issue or lost update)", got)
	}
	if got := m.lazyPool(PoolPrivate).Remaining(); got != 0 {
		t.Fatalf("private pool should be exactly 0 after exhaustion, got %d", got)
	}
}

// GlobalPool.TryConsume must be an atomic check-and-deduct: insufficient
// balance fails without mutating anything, and concurrent callers cannot
// over-issue the node's share.
func TestGlobalPool_TryConsume_Atomic(t *testing.T) {
	newLedger := func() *GlobalPool {
		return &GlobalPool{
			NodeContributions: map[string]int64{"n1": 1000},
			NodeConsumptions:  map[string]int64{},
			ParticipantNodes:  []GlobalPoolNode{{NodeID: "n1", Status: "active"}},
			dataPath:          filepath.Join(t.TempDir(), "global_pool.json"),
		}
	}

	// Sequential: partial success then a failing overdraft leaves state intact.
	gp := newLedger()
	if !gp.TryConsume("n1", 300) {
		t.Fatal("TryConsume(300) against 1000 should succeed")
	}
	if gp.TryConsume("n1", 800) {
		t.Fatal("TryConsume(800) against 700 remaining should fail")
	}
	if got := gp.NodeConsumptions["n1"]; got != 300 {
		t.Fatalf("failed TryConsume must not mutate the ledger, consumed=%d", got)
	}

	// Concurrent: 100 x 20 against 1000 -> exactly 50 succeed.
	gp2 := newLedger()
	var okCount atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 100; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if gp2.TryConsume("n1", 20) {
				okCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := okCount.Load(); got != 50 {
		t.Fatalf("expected exactly 50 successful TryConsume calls, got %d", got)
	}
	if got := gp2.NodeConsumptions["n1"]; got != 1000 {
		t.Fatalf("ledger must record exactly 1000 consumed, got %d", got)
	}
	if gp2.TryConsume("n1", 1) {
		t.Fatal("exhausted ledger share must reject further consumption")
	}
}

// End-to-end: with the global pool ledger available, shared-pool deductions
// made by Resolve are recorded atomically in the ledger (the project's
// quota-state persistence), and exhaustion is enforced against it.
func TestQuotaPriorityManager_LedgerSharedPoolIntegration(t *testing.T) {
	origPool := globalPool
	origNet := netMgr
	t.Cleanup(func() { globalPool = origPool; netMgr = origNet })

	globalPool = &GlobalPool{
		NodeContributions: map[string]int64{"test-node": 100},
		NodeConsumptions:  map[string]int64{},
		ParticipantNodes:  []GlobalPoolNode{{NodeID: "test-node", Status: "active"}},
		dataPath:          filepath.Join(t.TempDir(), "global_pool.json"),
	}
	netMgr = &NetworkManager{config: NetworkConfig{NodeID: "test-node"}}

	m := &quotaPriorityManager{enabled: true, privateLimit: 0, sharedLimit: -1, remoteLimit: 0}

	// private(0) insufficient -> shared via the ledger: 100 -> 60 -> 20.
	for i := 0; i < 2; i++ {
		res := m.Resolve(KeyTypeGuest, 40)
		if !res.OK || res.Kind != PoolShared {
			t.Fatalf("resolve %d: expected shared pool charged, got ok=%v kind=%s reason=%s",
				i, res.OK, res.Kind, res.Reason)
		}
		if res.NodeID != "test-node" {
			t.Fatalf("resolve %d: expected node test-node charged, got %q", i, res.NodeID)
		}
	}
	if got := globalPool.NodeConsumptions["test-node"]; got != 80 {
		t.Fatalf("ledger must reflect the 2x40 shared deductions, consumed=%d", got)
	}
	// Ledger share has 20 left < 40; remote snapshot (AvailableQuota=20) also
	// insufficient -> rejected.
	res := m.Resolve(KeyTypeGuest, 40)
	if res.OK {
		t.Fatalf("expected rejection once the ledger share is exhausted, got charged from %s", res.Kind)
	}
	if got := globalPool.NodeConsumptions["test-node"]; got != 80 {
		t.Fatalf("rejected request must not touch the ledger, consumed=%d", got)
	}
}

// A manager built by initQuotaPriority (the production path) must persist
// deductions across Resolve calls as well.
func TestQuotaPriorityManager_InitFromConfigPersists(t *testing.T) {
	_ = setupTestEnv(t)
	withNilGlobalPool(t)
	cfg.Set("quota_priority_enabled", "true")
	cfg.Set("quota_private_pool_limit", "50")
	cfg.Set("quota_shared_pool_limit", "0")
	cfg.Set("quota_remote_pool_limit", "0")
	initQuotaPriority()

	// 50 - 30 = 20 left; the second 30-token request must be rejected.
	if res := quotaPriorityMgr.Resolve(KeyTypeGuest, 30); !res.OK || res.Kind != PoolPrivate {
		t.Fatalf("expected private pool charged, got %+v", res)
	}
	if res := quotaPriorityMgr.Resolve(KeyTypeGuest, 30); res.OK {
		t.Fatalf("second request must be rejected (private pool had 20 left), got charged from %s", res.Kind)
	}
}
