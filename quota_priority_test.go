package main

import (
	"testing"
)

// buildTestPools constructs the three quota pools from explicit balances
// (-1 = unlimited) for use in multi-pool scenarios.
func buildTestPools(priv, shared, remote int64) map[PoolKind]*QuotaPool {
	return map[PoolKind]*QuotaPool{
		PoolPrivate:      {Kind: PoolPrivate, NodeID: "self", Balance: priv},
		PoolShared:       {Kind: PoolShared, NodeID: "self", Balance: shared},
		PoolRemoteShared: {Kind: PoolRemoteShared, NodeID: "peer", Balance: remote},
	}
}

// Scenario 1: private pool sufficient -> only the private pool is charged.
func TestQuotaPriority_PrivateSufficient(t *testing.T) {
	pools := buildTestPools(100, 100, 100)
	res := ConsumeWithPriority(KeyTypeGuest, 40, pools)

	if !res.OK {
		t.Fatalf("expected success, got failure: %s", res.Reason)
	}
	if res.Kind != PoolPrivate {
		t.Fatalf("expected private pool charged, got %s", res.Kind)
	}
	if res.NodeID != "self" {
		t.Fatalf("expected private pool on self node, got %q", res.NodeID)
	}
	if pools[PoolPrivate].Remaining() != 60 {
		t.Fatalf("private should be 60 after deduct, got %d", pools[PoolPrivate].Remaining())
	}
	if pools[PoolShared].Remaining() != 100 || pools[PoolRemoteShared].Remaining() != 100 {
		t.Fatalf("shared/remote pools must be untouched, got shared=%d remote=%d",
			pools[PoolShared].Remaining(), pools[PoolRemoteShared].Remaining())
	}
}

// Scenario 2: private insufficient -> downgrade to the shared pool.
func TestQuotaPriority_PrivateInsufficientFallsToShared(t *testing.T) {
	pools := buildTestPools(30, 100, 100)
	res := ConsumeWithPriority(KeyTypeGuest, 40, pools)

	if !res.OK {
		t.Fatalf("expected success via shared pool, got failure: %s", res.Reason)
	}
	if res.Kind != PoolShared {
		t.Fatalf("expected shared pool charged, got %s", res.Kind)
	}
	if pools[PoolPrivate].Remaining() != 30 {
		t.Fatalf("private must stay at 30 (untouched), got %d", pools[PoolPrivate].Remaining())
	}
	if pools[PoolShared].Remaining() != 60 {
		t.Fatalf("shared should be 60 after deduct, got %d", pools[PoolShared].Remaining())
	}
	if pools[PoolRemoteShared].Remaining() != 100 {
		t.Fatalf("remote must stay untouched, got %d", pools[PoolRemoteShared].Remaining())
	}
}

// Scenario 3: private and shared insufficient -> downgrade to the remote pool.
func TestQuotaPriority_SharedInsufficientFallsToRemote(t *testing.T) {
	pools := buildTestPools(10, 10, 100)
	res := ConsumeWithPriority(KeyTypeGuest, 40, pools)

	if !res.OK {
		t.Fatalf("expected success via remote pool, got failure: %s", res.Reason)
	}
	if res.Kind != PoolRemoteShared {
		t.Fatalf("expected remote_shared pool charged, got %s", res.Kind)
	}
	if res.NodeID != "peer" {
		t.Fatalf("expected remote pool on peer node, got %q", res.NodeID)
	}
	if pools[PoolPrivate].Remaining() != 10 || pools[PoolShared].Remaining() != 10 {
		t.Fatalf("private/shared must stay untouched, got priv=%d shared=%d",
			pools[PoolPrivate].Remaining(), pools[PoolShared].Remaining())
	}
	if pools[PoolRemoteShared].Remaining() != 60 {
		t.Fatalf("remote should be 60 after deduct, got %d", pools[PoolRemoteShared].Remaining())
	}
}

// Scenario 4: all pools insufficient -> rejected with an exhaustion reason.
func TestQuotaPriority_AllExhaustedRejected(t *testing.T) {
	pools := buildTestPools(10, 10, 10)
	res := ConsumeWithPriority(KeyTypeGuest, 40, pools)

	if res.OK {
		t.Fatalf("expected rejection, got success charged from %s", res.Kind)
	}
	if res.Reason == "" {
		t.Fatalf("rejection must carry a reason")
	}
	// No pool should have been mutated on a failed consumption.
	if pools[PoolPrivate].Remaining() != 10 || pools[PoolShared].Remaining() != 10 || pools[PoolRemoteShared].Remaining() != 10 {
		t.Fatalf("no pool should change when all are exhausted")
	}
}

// Public keys may never touch the private pool: shared -> remote_shared only.
func TestQuotaPriority_PublicUsesSharedThenRemote(t *testing.T) {
	pools := buildTestPools(1000, 50, 1000)
	res := ConsumeWithPriority(KeyTypePublic, 40, pools)
	if !res.OK || res.Kind != PoolShared {
		t.Fatalf("public should charge shared first, got ok=%v kind=%s", res.OK, res.Kind)
	}

	// Exhaust shared, then public must fall through to remote.
	pools2 := buildTestPools(1000, 10, 1000)
	res2 := ConsumeWithPriority(KeyTypePublic, 40, pools2)
	if !res2.OK || res2.Kind != PoolRemoteShared {
		t.Fatalf("public should fall to remote when shared insufficient, got ok=%v kind=%s", res2.OK, res2.Kind)
	}
}

// PriorityOrder encodes the §4.2 ordering for each key type.
func TestQuotaPriority_Order(t *testing.T) {
	guest := PriorityOrder(KeyTypeGuest)
	if len(guest) != 3 || guest[0] != PoolPrivate || guest[1] != PoolShared || guest[2] != PoolRemoteShared {
		t.Fatalf("guest order wrong: %v", guest)
	}
	proxy := PriorityOrder(KeyTypeProxy)
	if len(proxy) != 3 || proxy[0] != PoolPrivate {
		t.Fatalf("proxy(admin) order wrong: %v", proxy)
	}
	pub := PriorityOrder(KeyTypePublic)
	if len(pub) != 2 || pub[0] != PoolShared || pub[1] != PoolRemoteShared {
		t.Fatalf("public order wrong: %v", pub)
	}
}

// With enforcement disabled (the default), the manager is a passthrough that
// always succeeds and reports the private pool — preserving single-pool behavior.
func TestQuotaPriorityManager_DisabledPassthrough(t *testing.T) {
	m := &quotaPriorityManager{enabled: false, privateLimit: -1, sharedLimit: -1, remoteLimit: -1}
	res := m.Resolve(KeyTypeGuest, 99999)
	if !res.OK {
		t.Fatalf("disabled manager must always pass")
	}
	if res.Kind != PoolPrivate {
		t.Fatalf("disabled manager must report private pool, got %s", res.Kind)
	}
}

// With enforcement enabled and finite limits, the manager routes a request that
// exceeds private+shared to the remote pool (uses the config limits directly
// when the global pool ledger is absent, i.e. single-node deployment).
func TestQuotaPriorityManager_EnabledRouting(t *testing.T) {
	// private=10, shared=100, remote=100; amount=40 -> shared charged.
	m := &quotaPriorityManager{enabled: true, privateLimit: 10, sharedLimit: 100, remoteLimit: 100}
	res := m.Resolve(KeyTypeProxy, 40)
	if !res.OK || res.Kind != PoolShared {
		t.Fatalf("expected shared pool, got ok=%v kind=%s reason=%s", res.OK, res.Kind, res.Reason)
	}

	// All pools below the requested amount -> rejected.
	m2 := &quotaPriorityManager{enabled: true, privateLimit: 10, sharedLimit: 10, remoteLimit: 10}
	res2 := m2.Resolve(KeyTypeProxy, 40)
	if res2.OK {
		t.Fatalf("expected rejection when all pools are below amount, got %s", res2.Kind)
	}
}

// keyTypeFromString maps handler key-type strings onto the engine enum.
func TestKeyTypeFromString(t *testing.T) {
	if keyTypeFromString("guest") != KeyTypeGuest {
		t.Fatalf("guest mapping wrong")
	}
	if keyTypeFromString("proxy") != KeyTypeProxy || keyTypeFromString("admin") != KeyTypeProxy {
		t.Fatalf("proxy/admin must map to KeyTypeProxy")
	}
	if keyTypeFromString("public") != KeyTypePublic {
		t.Fatalf("public mapping wrong")
	}
}
