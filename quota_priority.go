package main

import (
	"log/slog"
	"strconv"
	"sync"
)

// ============================================================
// G6: Cross-Pool Quota Consumption Priority (ACCESS_CONTROL.md §4.2)
// ============================================================
//
// Per §4.2 the three quota pools and their consumption priority are:
//
//   - Private pool (私有额度池): this node's own `ShareToPool=false` upstream keys.
//   - Shared  pool (共享额度池): this node's `ShareToPool=true` upstream keys,
//     contributed to the network and aggregated in the global pool ledger.
//   - Remote-shared pool (他节点共享池): another node's shared pool, reached via
//     relay / gateway forwarding.
//
// Consumption priority (Guest / Admin key):  private -> shared -> remote_shared.
// A pool is only used once the previous one cannot satisfy the request.
// Public keys never touch the private pool (shared -> remote_shared only).
//
// This file implements the priority *deduction* logic without rewriting the
// existing quota engine. Enforcement is opt-in via the `quota_priority_enabled`
// config flag. When disabled (the default) every pool is treated as unlimited,
// so existing single-pool deployments keep their exact external behavior.

// PoolKind enumerates the three quota pools described in §4.2.
type PoolKind int

const (
	// PoolPrivate is the node's own private upstream-key quota (ShareToPool=false).
	PoolPrivate PoolKind = iota
	// PoolShared is the node's own shared upstream-key quota (ShareToPool=true).
	PoolShared
	// PoolRemoteShared is another node's shared pool, reached via relay/gateway.
	PoolRemoteShared
)

// String returns a stable, lower-case label for a pool kind.
func (k PoolKind) String() string {
	switch k {
	case PoolPrivate:
		return "private"
	case PoolShared:
		return "shared"
	case PoolRemoteShared:
		return "remote_shared"
	default:
		return "unknown"
	}
}

// QuotaPool is a single deductable quota bucket.
// A Balance of -1 means "unlimited" (the common default), so any deduction
// succeeds without mutating the balance. Finite balances are decremented
// atomically on success.
type QuotaPool struct {
	Kind    PoolKind
	NodeID  string // owning/issuing node; "" for self or an anonymous remote pool
	Balance int64  // remaining tokens; -1 = unlimited
	mu      sync.Mutex
}

// CanDeduct reports whether amount can be deducted without going negative.
// Unlimited pools (Balance < 0) always report true.
func (p *QuotaPool) CanDeduct(amount int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Balance < 0 {
		return true
	}
	return p.Balance >= amount
}

// Deduct atomically deducts amount if available, returning whether it succeeded.
// Unlimited pools always succeed and leave Balance unchanged.
func (p *QuotaPool) Deduct(amount int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Balance < 0 {
		return true
	}
	if p.Balance < amount {
		return false
	}
	p.Balance -= amount
	return true
}

// TryDeduct atomically checks and deducts in a single operation, preventing TOCTOU races.
// Prefer this over CanDeduct+Deduct sequences.
func (p *QuotaPool) TryDeduct(amount int64) bool {
	return p.Deduct(amount)
}

// Remaining returns the remaining balance, or -1 for an unlimited pool.
func (p *QuotaPool) Remaining() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Balance
}

// ConsumeResult records which pool (if any) satisfied a consumption request.
type ConsumeResult struct {
	OK     bool     // true if some pool satisfied the request
	Kind   PoolKind // pool kind that was charged (valid when OK == true)
	NodeID string   // owning node of the charged pool
	Amount int64    // amount deducted
	Reason string   // rejection reason when OK == false
}

// PriorityOrder returns the ordered pool kinds for a key type, per §4.2:
//   - Guest / Proxy (Admin): private -> shared -> remote_shared
//   - Public: shared -> remote_shared (never touches the private pool)
//   - Unknown: same as Guest/Proxy (fail-safe, private-first)
func PriorityOrder(keyType KeyType) []PoolKind {
	switch keyType {
	case KeyTypeGuest, KeyTypeProxy, KeyTypeUnknown:
		return []PoolKind{PoolPrivate, PoolShared, PoolRemoteShared}
	case KeyTypePublic:
		return []PoolKind{PoolShared, PoolRemoteShared}
	default:
		return []PoolKind{PoolPrivate, PoolShared, PoolRemoteShared}
	}
}

// ConsumeWithPriority tries each pool in priority order and deducts from the
// first pool that can satisfy amount. It returns the pool that was charged, or
// a failure result when no pool has sufficient capacity.
//
// The pools map is expected to be keyed by PoolKind; missing entries are
// skipped. Deduction is atomic per pool.
func ConsumeWithPriority(keyType KeyType, amount int64, pools map[PoolKind]*QuotaPool) ConsumeResult {
	for _, kind := range PriorityOrder(keyType) {
		pool, ok := pools[kind]
		if !ok || pool == nil {
			continue
		}
		if pool.TryDeduct(amount) {
			slog.Debug("quota priority: deducted from pool",
				"key_type", keyType, "pool", pool.Kind.String(),
				"node", pool.NodeID, "amount", amount)
			return ConsumeResult{
				OK:     true,
				Kind:   pool.Kind,
				NodeID: pool.NodeID,
				Amount: amount,
			}
		}
	}
	return ConsumeResult{
		OK:     false,
		Reason: "quota exhausted across private/shared/remote pools",
	}
}

// ============================================================
// Node-backed priority manager (opt-in)
// ============================================================

// quotaPriorityManager builds the live three-pool view for the current node and
// performs the priority deduction at the consumption entry. It is intentionally a
// no-op (all pools unlimited) unless an operator explicitly enables finite pool
// limits, so existing single-pool deployments keep their exact behavior.
type quotaPriorityManager struct {
	mu sync.RWMutex

	// enabled gates enforcement. When false, Resolve is a passthrough.
	enabled bool
	// Explicit finite limits; -1 means "unlimited" (default).
	privateLimit int64
	sharedLimit  int64
	remoteLimit  int64

	// Long-lived, mutex-guarded pool instances. Resolve deducts from these
	// same instances on every call instead of rebuilding them from the
	// config limits per request, so consumption persists across requests
	// and a finite quota actually depletes. QuotaPool serializes concurrent
	// deductions with its own mutex.
	//
	// sharedPool / remotePool are only the fallback for single-node
	// deployments where the global pool ledger is unavailable. When the
	// ledger is available it is the source of truth: shared deductions go
	// through GlobalPool.TryConsume (atomic check-and-record) and the
	// remote pool is read as a fresh snapshot per request (remote
	// enforcement is the target node's job).
	privatePool *QuotaPool
	sharedPool  *QuotaPool
	remotePool  *QuotaPool
}

// initQuotaPriority initializes the cross-pool priority manager from config.
// Safe to call at any time: it only reads cfg when present.
func initQuotaPriority() {
	m := &quotaPriorityManager{
		privateLimit: -1,
		sharedLimit:  -1,
		remoteLimit:  -1,
		enabled:      false,
	}
	if cfg != nil {
		if cfg.Get("quota_priority_enabled", "") == "true" {
			m.enabled = true
		}
		m.privateLimit = parseInt64Config(cfg.Get("quota_private_pool_limit", ""))
		m.sharedLimit = parseInt64Config(cfg.Get("quota_shared_pool_limit", ""))
		m.remoteLimit = parseInt64Config(cfg.Get("quota_remote_pool_limit", ""))
	}
	// Create the long-lived pool instances once, seeded from the configured
	// limits. Every Resolve deducts from these same instances so consumption
	// persists across requests (previously each Resolve rebuilt the pools
	// from the limits, so the private pool always looked full).
	m.privatePool = &QuotaPool{Kind: PoolPrivate, Balance: m.privateLimit}
	m.sharedPool = &QuotaPool{Kind: PoolShared, Balance: m.sharedLimit}
	m.remotePool = &QuotaPool{Kind: PoolRemoteShared, Balance: m.remoteLimit}
	quotaPriorityMgr = m
	slog.Info("quota priority manager initialized",
		"enabled", m.enabled,
		"private_limit", m.privateLimit,
		"shared_limit", m.sharedLimit,
		"remote_limit", m.remoteLimit)
}

// parseInt64Config parses a non-negative int64 from a config string.
// Empty or invalid input, or a negative value, yields -1 (unlimited).
func parseInt64Config(s string) int64 {
	if s == "" {
		return -1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return -1
	}
	return v
}

// selfNodeID returns the current node ID, or "" when the network manager is
// not yet initialized.
func selfNodeID() string {
	if netMgr != nil {
		return netMgr.GetNodeID()
	}
	return ""
}

// lazyPool returns the manager's long-lived pool instance for kind, creating
// it from the configured limit on first use. initQuotaPriority creates the
// instances eagerly; managers built directly (struct literal, e.g. in tests)
// get them here, so every Resolve still shares one instance per pool and
// deductions persist across calls.
func (m *quotaPriorityManager) lazyPool(kind PoolKind) *QuotaPool {
	m.mu.Lock()
	defer m.mu.Unlock()
	var slot **QuotaPool
	var limit int64
	switch kind {
	case PoolPrivate:
		slot, limit = &m.privatePool, m.privateLimit
	case PoolShared:
		slot, limit = &m.sharedPool, m.sharedLimit
	case PoolRemoteShared:
		slot, limit = &m.remotePool, m.remoteLimit
	default:
		return nil
	}
	if *slot == nil {
		*slot = &QuotaPool{Kind: kind, Balance: limit}
	}
	return *slot
}

// tryConsumeShared atomically deducts amount from this node's shared-pool
// share. When the global pool ledger is available (and the node ID is known)
// it is the source of truth and the deduction is a single atomic
// check-and-record via GlobalPool.TryConsume, which also persists the ledger
// to disk — the project's existing quota-state persistence pattern (mutate
// under lock, then save). Otherwise (single-node deployment, or unknown node
// ID which must not pollute the ledger) the manager's persistent fallback
// pool carries the configured shared limit.
func (m *quotaPriorityManager) tryConsumeShared(nodeID string, amount int64) bool {
	if globalPool != nil && nodeID != "" {
		return globalPool.TryConsume(nodeID, amount)
	}
	return m.lazyPool(PoolShared).TryDeduct(amount)
}

// remotePoolView returns the current view of the remote-shared pool. When the
// global pool ledger is available the view is a throwaway per-request
// snapshot of its available quota: remote deductions are enforced by the
// target node, so this node keeps no persistent local balance for them.
// Otherwise the manager's persistent pool carries the configured remote
// limit, so a finite limit still depletes across requests.
func (m *quotaPriorityManager) remotePoolView() *QuotaPool {
	if globalPool == nil {
		return m.lazyPool(PoolRemoteShared)
	}
	globalPool.mu.RLock()
	balance := globalPool.AvailableQuota
	globalPool.mu.RUnlock()
	if balance < 0 {
		balance = 0
	}
	return &QuotaPool{Kind: PoolRemoteShared, Balance: balance}
}

// charged builds the success result for a deduction, keeping the debug log in
// one place.
func (m *quotaPriorityManager) charged(kind PoolKind, nodeID string, amount int64) ConsumeResult {
	slog.Debug("quota priority: deducted from pool",
		"pool", kind.String(), "node", nodeID, "amount", amount)
	return ConsumeResult{OK: true, Kind: kind, NodeID: nodeID, Amount: amount}
}

// Resolve performs the cross-pool priority consumption for a request of the
// given key type, attempting to deduct amount tokens.
//
// When enforcement is disabled (default) it is a no-op passthrough that reports
// the private pool as charged, preserving existing single-pool behavior exactly.
// When enabled, it deducts from the first pool (private -> shared -> remote)
// that can satisfy the request, per PriorityOrder(keyType). A pool is only
// used once the previous one cannot satisfy the request, and exactly one pool
// is charged per call.
//
// Deduction targets are persistent, so quotas actually deplete: the private
// pool is a long-lived mutex-guarded instance on the manager (previously it
// was rebuilt from the config limit on every call, so it always looked full).
// Shared-pool deductions go through GlobalPool.TryConsume — a single atomic
// check-and-record on the ledger — so concurrent requests cannot over-issue
// the shared pool. The remote pool is a per-request snapshot of the ledger's
// available quota; remote deductions are enforced by the target node and are
// intentionally not recorded here.
func (m *quotaPriorityManager) Resolve(keyType KeyType, amount int64) ConsumeResult {
	m.mu.RLock()
	enabled := m.enabled
	m.mu.RUnlock()

	if !enabled {
		return ConsumeResult{OK: true, Kind: PoolPrivate, NodeID: selfNodeID(), Amount: amount}
	}

	nodeID := selfNodeID()
	for _, kind := range PriorityOrder(keyType) {
		switch kind {
		case PoolPrivate:
			if m.lazyPool(PoolPrivate).TryDeduct(amount) {
				return m.charged(PoolPrivate, nodeID, amount)
			}
		case PoolShared:
			if m.tryConsumeShared(nodeID, amount) {
				return m.charged(PoolShared, nodeID, amount)
			}
		case PoolRemoteShared:
			if pool := m.remotePoolView(); pool.TryDeduct(amount) {
				return m.charged(PoolRemoteShared, pool.NodeID, amount)
			}
		}
	}
	return ConsumeResult{
		OK:     false,
		Reason: "quota exhausted across private/shared/remote pools",
	}
}

// keyTypeFromString maps the handler-side key-type string (returned by
// RequestKeyType) to the KeyType enum used by the priority engine.
// Both "proxy" and "admin" are treated as the Admin/Proxy key tier.
func keyTypeFromString(s string) KeyType {
	switch s {
	case "guest":
		return KeyTypeGuest
	case "proxy", "admin":
		return KeyTypeProxy
	case "public":
		return KeyTypePublic
	default:
		return KeyTypeUnknown
	}
}
