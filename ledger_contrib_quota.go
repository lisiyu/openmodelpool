package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ============================================================
// Contribution → Free-Quota linkage (P2-3(i), public-welfare)
// ============================================================
//
// This closes the public-welfare loop: a node that DONATES compute to the
// pool (recorded in the contribution ledger) earns an EQUAL free-quota
// entitlement in the Public Key Pool. It is intentionally NOT a token
// economy:
//   - 1:1 accounting, no fee, no inflation, no interest.
//   - The "earned" quota is a transparent entitlement, NOT a tradeable coin.
//   - There is no marketplace, no exchange, no 抽成 (skim). It only answers
//     "who contributed how much, and what free quota they are entitled to".
//   - Consumption-side wiring lives in ledger_quota_consume.go (P2-3(ii)). It
//     is deliberately NON-EXCLUSIONARY: drawing on an earned entitlement is an
//     extra lane for contributors, never a gate in front of the community free
//     pool. Anonymous callers keep the exact same open access they had before.
//
// The ContributionQuotaTracker is additive and nil-safe everywhere else.

// ContributionQuota is a single contributor's transparent accounting row.
type ContributionQuota struct {
	PeerID            string `json:"peer_id"`            // contributor node id
	ContributedTokens int64  `json:"contributed_tokens"` // total tokens donated to the pool
	EarnedFreeQuota   int64  `json:"earned_free_quota"`  // = ContributedTokens (1:1, public-welfare)
	ConsumedQuota     int64  `json:"consumed_quota"`     // tokens already drawn against the entitlement
	RemainingQuota    int64  `json:"remaining_quota"`    // = EarnedFreeQuota - ConsumedQuota (never negative)
	LastUpdated       int64  `json:"last_updated"`       // unix seconds
}

// remainingLocked computes the still-drawable entitlement. Caller holds the lock.
func (q *ContributionQuota) remainingLocked() int64 {
	r := q.EarnedFreeQuota - q.ConsumedQuota
	if r < 0 {
		return 0
	}
	return r
}

// ContributionQuotaTracker keeps the per-contributor accrual in memory and
// persists it to data/contribution_quota.json.
type ContributionQuotaTracker struct {
	mu      sync.RWMutex
	dataDir string
	entries map[string]*ContributionQuota
	bbolt   *bboltHandle // G4: 句柄归实例所有，避免包级全局在测试间泄漏
}

// contribQuotaTracker is the process-wide instance (nil until init).
var contribQuotaTracker *ContributionQuotaTracker

// initContributionQuotaTracker loads (or creates) the accrual store.
func initContributionQuotaTracker(dataDir string) *ContributionQuotaTracker {
	t := &ContributionQuotaTracker{
		dataDir: dataDir,
		entries: make(map[string]*ContributionQuota),
	}
	// G4: bbolt 后端。open/import 失败则降级走 JSON。
	if storageUseBbolt() {
		if h, err := openBbolt(dataDir); err == nil {
			if err := t.importToBbolt(h); err != nil {
				slog.Warn("contribution quota bbolt import failed, falling back to JSON", "error", err)
				t.load()
			} else {
				t.loadBbolt(h)
			}
		} else {
			slog.Warn("bbolt open failed, contribution quota using JSON backend", "error", err)
			t.load()
		}
	} else {
		t.load()
	}
	slog.Info("contribution-quota tracker initialized", "entries", len(t.entries))
	return t
}

// Accrue adds donated tokens for a peer. Earned free quota tracks the
// contributed total 1:1 (public-welfare, no fee/no inflation).
func (t *ContributionQuotaTracker) Accrue(peerID string, tokens int64) {
	if peerID == "" || tokens <= 0 {
		return
	}
	t.mu.Lock()
	e, ok := t.entries[peerID]
	if !ok {
		e = &ContributionQuota{PeerID: peerID}
		t.entries[peerID] = e
	}
	e.ContributedTokens += tokens
	e.EarnedFreeQuota = e.ContributedTokens // 1:1 entitlement, public-welfare
	e.RemainingQuota = e.remainingLocked()
	e.LastUpdated = time.Now().Unix()
	t.mu.Unlock()
	t.saveEntry(peerID)
}

// Consume draws `tokens` against a contributor's earned entitlement.
//
// It returns (true, remaining) only when the peer is a known contributor with
// enough remaining entitlement to cover the request. Otherwise it returns
// (false, remaining) and changes NOTHING — the caller then falls back to the
// ordinary community free-pool path, which stays wide open to everyone.
// This is the "善意默认" rule in code: a contributor entitlement is an extra
// lane, never a toll gate.
func (t *ContributionQuotaTracker) Consume(peerID string, tokens int64) (bool, int64) {
	if t == nil || peerID == "" || tokens <= 0 {
		return false, 0
	}
	t.mu.Lock()
	e, ok := t.entries[peerID]
	if !ok {
		t.mu.Unlock()
		return false, 0
	}
	remaining := e.remainingLocked()
	if remaining < tokens {
		t.mu.Unlock()
		return false, remaining
	}
	e.ConsumedQuota += tokens
	e.RemainingQuota = e.remainingLocked()
	e.LastUpdated = time.Now().Unix()
	remaining = e.RemainingQuota
	t.mu.Unlock()
	t.saveEntry(peerID)
	return true, remaining
}

// Refund returns unused tokens to a contributor's entitlement after the real
// usage turned out lower than the reservation. Refunds can never push the
// consumed counter below zero (1:1 accounting, no interest, no inflation).
func (t *ContributionQuotaTracker) Refund(peerID string, tokens int64) {
	if t == nil || peerID == "" || tokens <= 0 {
		return
	}
	t.mu.Lock()
	e, ok := t.entries[peerID]
	if !ok {
		t.mu.Unlock()
		return
	}
	e.ConsumedQuota -= tokens
	if e.ConsumedQuota < 0 {
		e.ConsumedQuota = 0
	}
	e.RemainingQuota = e.remainingLocked()
	e.LastUpdated = time.Now().Unix()
	t.mu.Unlock()
	t.saveEntry(peerID)
}

// Remaining reports the still-drawable entitlement for a peer (0 if unknown).
func (t *ContributionQuotaTracker) Remaining(peerID string) int64 {
	if t == nil || peerID == "" {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if e, ok := t.entries[peerID]; ok {
		return e.remainingLocked()
	}
	return 0
}

// GetQuota returns the accrual row for a peer (nil if unknown).
func (t *ContributionQuotaTracker) GetQuota(peerID string) *ContributionQuota {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if e, ok := t.entries[peerID]; ok {
		cp := *e
		cp.RemainingQuota = e.remainingLocked()
		return &cp
	}
	return nil
}

// Snapshot returns all accrual rows sorted by contributed tokens desc.
func (t *ContributionQuotaTracker) Snapshot() []ContributionQuota {
	t.mu.RLock()
	out := make([]ContributionQuota, 0, len(t.entries))
	for _, e := range t.entries {
		row := *e
		row.RemainingQuota = e.remainingLocked()
		out = append(out, row)
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].ContributedTokens == out[j].ContributedTokens {
			return out[i].PeerID < out[j].PeerID
		}
		return out[i].ContributedTokens > out[j].ContributedTokens
	})
	return out
}

// TotalContributed returns the federation-wide donated token sum.
func (t *ContributionQuotaTracker) TotalContributed() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var sum int64
	for _, e := range t.entries {
		sum += e.ContributedTokens
	}
	return sum
}

// TotalConsumed returns the federation-wide sum of entitlement already drawn.
func (t *ContributionQuotaTracker) TotalConsumed() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var sum int64
	for _, e := range t.entries {
		sum += e.ConsumedQuota
	}
	return sum
}

func (t *ContributionQuotaTracker) save() {
	if t.dataDir == "" {
		return
	}
	path := filepath.Join(t.dataDir, "contribution_quota.json")
	t.mu.RLock()
	data, err := json.MarshalIndent(t.entries, "", "  ")
	t.mu.RUnlock()
	if err != nil {
		slog.Error("failed to marshal contribution quota", "error", err)
		return
	}
	if err := atomicWriteFile(path, data, 0600); err != nil {
		slog.Error("failed to write contribution quota", "error", err)
	}
}

func (t *ContributionQuotaTracker) load() {
	if t.dataDir == "" {
		return
	}
	path := filepath.Join(t.dataDir, "contribution_quota.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to read contribution quota", "error", err)
		}
		return
	}
	loaded := make(map[string]*ContributionQuota)
	if err := json.Unmarshal(raw, &loaded); err != nil {
		slog.Error("failed to unmarshal contribution quota", "error", err)
		return
	}
	// Re-normalize earned free quota to contributed (1:1) in case the
	// persisted file predates the rule or was hand-edited, and repair any
	// out-of-range consumed counter (files written before P2-3(ii) simply
	// carry consumed=0, which is the correct default).
	for _, e := range loaded {
		if e.EarnedFreeQuota != e.ContributedTokens {
			e.EarnedFreeQuota = e.ContributedTokens
		}
		if e.ConsumedQuota < 0 {
			e.ConsumedQuota = 0
		}
		if e.ConsumedQuota > e.EarnedFreeQuota {
			e.ConsumedQuota = e.EarnedFreeQuota
		}
		e.RemainingQuota = e.remainingLocked()
	}
	t.entries = loaded
}

// handleAdminLedgerContributionQuota returns the transparent "contribution →
// free-quota" view for every contributor (P2-3). Admin-authenticated.
func handleAdminLedgerContributionQuota(w http.ResponseWriter, r *http.Request) {
	if contribQuotaTracker == nil {
		http.Error(w, "contribution-quota tracker not ready", http.StatusServiceUnavailable)
		return
	}
	total := contribQuotaTracker.TotalContributed()
	consumed := contribQuotaTracker.TotalConsumed()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"total_contributed_tokens": total,
		"total_consumed_tokens":    consumed,
		"total_remaining_tokens":   total - consumed,
		"contributors":             contribQuotaTracker.Snapshot(),
	})
}

// ============================================================
// G4: bbolt backend for contribution_quota
// ============================================================

// bboltCQuotaPrefix 是 quota bucket 内单条配额记录的 key 前缀。
const bboltCQuotaPrefix = "cquota/"

// saveEntry 持久化单条记录：bbolt 模式走单 key Put（高频热路径），
// JSON 模式走旧的全量文件写（回滚路径）。
func (t *ContributionQuotaTracker) saveEntry(peerID string) {
	if h := t.bbolt; h != nil {
		t.mu.RLock()
		e := t.entries[peerID]
		var data []byte
		if e != nil {
			data, _ = json.Marshal(e)
		}
		t.mu.RUnlock()
		if data == nil {
			return
		}
		if err := h.db.Update(func(tx *bolt.Tx) error {
			return tx.Bucket([]byte(bboltBucketQuota)).Put([]byte(bboltCQuotaPrefix+peerID), data)
		}); err != nil {
			slog.Error("failed to write contribution quota to bbolt", "peer", peerID, "error", err)
		}
		return
	}
	t.save()
}

// loadBbolt 从 bbolt 恢复全部条目；损坏条目跳过（与 load 的容错一致）。
func (t *ContributionQuotaTracker) loadBbolt(h *bboltHandle) {
	loaded := make(map[string]*ContributionQuota)
	_ = h.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bboltBucketQuota)).Cursor()
		prefix := []byte(bboltCQuotaPrefix)
		for k, v := c.Seek(prefix); k != nil && len(k) >= len(prefix) && string(k[:len(prefix)]) == bboltCQuotaPrefix; k, v = c.Next() {
			var e ContributionQuota
			if err := json.Unmarshal(v, &e); err != nil {
				slog.Error("failed to unmarshal contribution quota from bbolt", "key", string(k), "error", err)
				continue
			}
			loaded[string(k[len(prefix):])] = &e
		}
		return nil
	})
	// 与 load() 相同的 re-normalize，保证 1:1 规则。
	for _, e := range loaded {
		if e.EarnedFreeQuota != e.ContributedTokens {
			e.EarnedFreeQuota = e.ContributedTokens
		}
		if e.ConsumedQuota < 0 {
			e.ConsumedQuota = 0
		}
		if e.ConsumedQuota > e.EarnedFreeQuota {
			e.ConsumedQuota = e.EarnedFreeQuota
		}
		e.RemainingQuota = e.remainingLocked()
	}
	t.entries = loaded
}

// importToBbolt 执行 JSON→bbolt 一次性迁移：复用 load()（含 re-normalize），
// 逐条写入，打标记，原文件 rename 为 .bak。幂等。
func (t *ContributionQuotaTracker) importToBbolt(h *bboltHandle) error {
	const domain = "contrib_quota"
	jsonPath := filepath.Join(t.dataDir, "contribution_quota.json")
	if h.isMigrated(domain) {
		if err := bakJSON(jsonPath); err != nil {
			return err
		}
		t.bbolt = h
		return nil
	}
	if _, err := os.Stat(jsonPath); err == nil {
		t.load() // 复用现有解析 + 修复逻辑
	}
	t.mu.RLock()
	snapshot := make(map[string]*ContributionQuota, len(t.entries))
	for k, v := range t.entries {
		snapshot[k] = v
	}
	t.mu.RUnlock()
	err := h.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bboltBucketQuota))
		for peerID, e := range snapshot {
			data, err := json.Marshal(e)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(bboltCQuotaPrefix+peerID), data); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := h.markMigrated(domain); err != nil {
		return err
	}
	t.bbolt = h
	return bakJSON(jsonPath)
}
