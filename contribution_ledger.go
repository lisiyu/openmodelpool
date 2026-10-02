package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

type ContributionRecord struct {
	ID            string       `json:"id"`
	PeerID        string       `json:"peer_id"`
	PeerPublicKey []byte       `json:"peer_public_key"`
	ModelID       string       `json:"model_id"`
	Provider      string       `json:"provider"`
	Tokens        int64        `json:"tokens"`
	ValueUSD      float64      `json:"value_usd"`
	Timestamp     time.Time    `json:"timestamp"`
	Signature     []byte       `json:"signature"`
	Proof         StorageProof `json:"proof"`
}

type StorageProof struct {
	ContentHash     string `json:"content_hash"`
	StorageLocation string `json:"storage_location"`
	Verified        bool   `json:"verified"`
}

type TrustRecord struct {
	ID             string    `json:"id"`
	SubjectPeerID  string    `json:"subject_peer_id"`
	VerifierPeerID string    `json:"verifier_peer_id"`
	ModelID        string    `json:"model_id"`
	Success        bool      `json:"success"`
	LatencyMS      int64     `json:"latency_ms"`
	Timestamp      time.Time `json:"timestamp"`
	Signature      []byte    `json:"signature"`
}

type CapabilityClaim struct {
	ID         string    `json:"id"`
	PeerID     string    `json:"peer_id"`
	Models     []string  `json:"models"`
	Providers  []string  `json:"providers"`
	MaxQuota   int64     `json:"max_quota"`
	Timestamp  time.Time `json:"timestamp"`
	Signature  []byte    `json:"signature"`
	ValidUntil time.Time `json:"valid_until"`
}

type PenaltyRecord struct {
	ID        string    `json:"id"`
	PeerID    string    `json:"peer_id"`
	Reason    string    `json:"reason"`
	Evidence  []string  `json:"evidence"`
	Action    string    `json:"action"`
	Timestamp time.Time `json:"timestamp"`
	Verifiers []string  `json:"verifiers"`
	Signature []byte    `json:"signature"`
}

type SignedTransaction struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	NodeID    string    `json:"node_id"`
	Amount    int64     `json:"amount"`
	ModelID   string    `json:"model_id,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
	Timestamp time.Time `json:"timestamp"`
	Signature []byte    `json:"signature"`
}

type ProbeResult struct {
	PeerID    string
	ModelID   string
	Success   bool
	LatencyMS int64
	Error     string
	Timestamp time.Time
}

type GossipLedger struct {
	mu        sync.RWMutex
	peerID    string
	hashStore *ContentHashStore
	recs      map[string]*ContributionRecord
	trusts    map[string]*TrustRecord
	claims    map[string]*CapabilityClaim
	penalties map[string]*PenaltyRecord
	txs       []*SignedTransaction
	txIndex   map[string]*SignedTransaction
	// dailyContrib is an incremental per-(node,date) contribution counter
	// (PERF-P0-2): key = nodeID + "|" + YYYY-MM-DD (local time), maintained in
	// AppendTransaction so CheckShareBoundary never scans the whole ledger.
	dailyContrib map[string]int64
	seq          uint64
	pub          ed25519.PublicKey
	priv         ed25519.PrivateKey
	// G4: bbolt 脏 key 集合（guarded by mu）。mutation 方法标记，
	// saveBbolt 批量 Put 后换出清空。
	dirty map[string]struct{}
	// G4: bbolt 后端句柄，nil 则走 JSON。归实例所有，避免包级全局在测试间泄漏。
	bbolt *bboltHandle
}

// G4: ledger bucket 的 key 前缀。
const (
	bboltLedgerContribPrefix = "contrib/"
	bboltLedgerTrustPrefix   = "trust/"
	bboltLedgerClaimPrefix   = "claim/"
	bboltLedgerPenaltyPrefix = "penalty/"
	bboltLedgerTxPrefix      = "tx/"
	// meta/identity 存节点身份（peer_id/seq/pub/priv，含 ed25519 私钥）。
	bboltLedgerIdentityKey = "meta/identity"
)

// G4: 句柄归实例所有（g.bbolt），避免包级全局在测试间泄漏。

// markDirtyLocked 标记 key 待持久化。调用方必须持有 g.mu。
func (g *GossipLedger) markDirtyLocked(keys ...string) {
	if g.dirty == nil {
		g.dirty = make(map[string]struct{})
	}
	for _, k := range keys {
		g.dirty[k] = struct{}{}
	}
}

func NewGossipLedger(peerID string) (*GossipLedger, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	return &GossipLedger{
		peerID:       peerID,
		hashStore:    NewContentHashStore(),
		recs:         make(map[string]*ContributionRecord),
		trusts:       make(map[string]*TrustRecord),
		claims:       make(map[string]*CapabilityClaim),
		penalties:    make(map[string]*PenaltyRecord),
		txs:          make([]*SignedTransaction, 0),
		txIndex:      make(map[string]*SignedTransaction),
		dailyContrib: make(map[string]int64),
		pub:          pub,
		priv:         priv,
		dirty:        make(map[string]struct{}),
	}, nil
}

func (g *GossipLedger) PublicKey() ed25519.PublicKey {
	return g.pub
}

func (g *GossipLedger) Sign(data []byte) []byte {
	return ed25519.Sign(g.priv, data)
}

// nextID returns a unique record id. PERF-P3-21: the seq counter is not
// independently synchronized — the caller MUST already hold g.mu (write lock),
// which every caller in this file does (RecordContribution/RecordTrust/
// RecordClaim/RecordPenalty/AppendTransaction).
func (g *GossipLedger) nextID(prefix string) string {
	g.seq++
	g.markDirtyLocked(bboltLedgerIdentityKey) // G4: seq 持久化
	return fmt.Sprintf("%s-%s-%d", prefix, g.peerID, g.seq)
}

func (g *GossipLedger) RecordContribution(record *ContributionRecord) (string, error) {
	if record == nil {
		return "", fmt.Errorf("nil contribution record")
	}
	g.mu.Lock()
	if record.ID == "" {
		record.ID = g.nextID("contrib")
	}
	if record.PeerID == "" {
		record.PeerID = g.peerID
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if data, err := json.Marshal(record); err == nil {
		record.Signature = g.Sign(data)
	}
	cp := *record
	g.recs[record.ID] = &cp
	g.markDirtyLocked(bboltLedgerContribPrefix + record.ID) // G4
	g.mu.Unlock()

	if cid, err := g.hashStore.StoreJSON(record); err == nil {
		cp.Proof.ContentHash = cid
		cp.Proof.StorageLocation = "local-hash"
		g.mu.Lock()
		g.recs[record.ID] = &cp
		g.markDirtyLocked(bboltLedgerContribPrefix + record.ID) // G4
		g.mu.Unlock()
	}
	// P1-3(ii): asynchronously mirror the contribution to federation peers so
	// the record survives any single node's loss (nil replicator = no-op).
	// PERF-P0-3: the replicator's bounded worker pool replaces the previous
	// unbounded per-record goroutine.
	if ledgerReplicator != nil {
		recCopy := *record
		ledgerReplicator.EnqueueContribution(&recCopy)
	}
	// P2-3(i): accrue the donor's public-welfare free-quota entitlement
	// (1:1 with contributed tokens). Nil tracker = no-op (keeps unit tests
	// and the additive hook side-effect free).
	if contribQuotaTracker != nil && record.Tokens > 0 {
		contribQuotaTracker.Accrue(record.PeerID, record.Tokens)
	}
	return record.ID, nil
}

func (g *GossipLedger) GetContribution(id string) (*ContributionRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	r, ok := g.recs[id]
	if !ok {
		return nil, fmt.Errorf("contribution %s not found", id)
	}
	return r, nil
}

func (g *GossipLedger) VerifyContribution(id string) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.recs[id]
	return ok, nil
}

func (g *GossipLedger) GetPeerContributions(peerID string) ([]*ContributionRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []*ContributionRecord
	for _, r := range g.recs {
		if r.PeerID == peerID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (g *GossipLedger) RecordTrust(rec *TrustRecord) (string, error) {
	if rec == nil {
		return "", fmt.Errorf("nil trust record")
	}
	g.mu.Lock()
	if rec.ID == "" {
		rec.ID = g.nextID("trust")
	}
	if rec.VerifierPeerID == "" {
		rec.VerifierPeerID = g.peerID
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	cp := *rec
	g.trusts[rec.ID] = &cp
	g.markDirtyLocked(bboltLedgerTrustPrefix + rec.ID) // G4
	g.mu.Unlock()
	return rec.ID, nil
}

func (g *GossipLedger) RecordClaim(claim *CapabilityClaim) string {
	if claim == nil {
		return ""
	}
	g.mu.Lock()
	if claim.ID == "" {
		claim.ID = g.nextID("claim")
	}
	if claim.PeerID == "" {
		claim.PeerID = g.peerID
	}
	if claim.Timestamp.IsZero() {
		claim.Timestamp = time.Now()
	}
	cp := *claim
	g.claims[claim.ID] = &cp
	g.markDirtyLocked(bboltLedgerClaimPrefix + claim.ID) // G4
	g.mu.Unlock()
	return claim.ID
}

func (g *GossipLedger) RecordPenalty(rec *PenaltyRecord) (string, error) {
	if rec == nil {
		return "", fmt.Errorf("nil penalty record")
	}
	g.mu.Lock()
	if rec.ID == "" {
		rec.ID = g.nextID("penalty")
	}
	if rec.PeerID == "" {
		rec.PeerID = g.peerID
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	if data, err := json.Marshal(rec); err == nil {
		rec.Signature = g.Sign(data)
	}
	cp := *rec
	g.penalties[rec.ID] = &cp
	g.markDirtyLocked(bboltLedgerPenaltyPrefix + rec.ID) // G4
	g.mu.Unlock()
	return rec.ID, nil
}

func (g *GossipLedger) GossipSync(contributions []*ContributionRecord, trusts []*TrustRecord, claims []*CapabilityClaim, penalties []*PenaltyRecord) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	added := 0
	for _, r := range contributions {
		if r == nil || r.ID == "" {
			continue
		}
		if _, ok := g.recs[r.ID]; !ok {
			cp := *r
			g.recs[r.ID] = &cp
			g.markDirtyLocked(bboltLedgerContribPrefix + r.ID) // G4
			added++
		}
	}
	for _, t := range trusts {
		if t == nil || t.ID == "" {
			continue
		}
		if _, ok := g.trusts[t.ID]; !ok {
			cp := *t
			g.trusts[t.ID] = &cp
			g.markDirtyLocked(bboltLedgerTrustPrefix + t.ID) // G4
			added++
		}
	}
	for _, c := range claims {
		if c == nil || c.ID == "" {
			continue
		}
		if _, ok := g.claims[c.ID]; !ok {
			cp := *c
			g.claims[c.ID] = &cp
			g.markDirtyLocked(bboltLedgerClaimPrefix + c.ID) // G4
			added++
		}
	}
	for _, p := range penalties {
		if p == nil || p.ID == "" {
			continue
		}
		if _, ok := g.penalties[p.ID]; !ok {
			cp := *p
			g.penalties[p.ID] = &cp
			g.markDirtyLocked(bboltLedgerPenaltyPrefix + p.ID) // G4
			added++
		}
	}
	return added
}

func (g *GossipLedger) AppendTransaction(txType, nodeID string, amount int64, modelID, requestID string) (*SignedTransaction, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.seq++
	txID := fmt.Sprintf("tx-%s-%d", g.peerID, g.seq)

	var prevHash string
	if len(g.txs) > 0 {
		prevHash = g.txs[len(g.txs)-1].Hash
	}

	tx := &SignedTransaction{
		ID:        txID,
		Type:      txType,
		NodeID:    nodeID,
		Amount:    amount,
		ModelID:   modelID,
		RequestID: requestID,
		PrevHash:  prevHash,
		Timestamp: time.Now(),
	}

	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s", tx.ID, tx.Type, tx.NodeID, tx.Amount, tx.ModelID, tx.RequestID, tx.PrevHash)))
	tx.Hash = fmt.Sprintf("%x", hash[:])

	data, _ := json.Marshal(tx)
	tx.Signature = ed25519.Sign(g.priv, data)

	g.txs = append(g.txs, tx)
	g.txIndex[tx.ID] = tx
	// G4: tx 单 key + seq（identity）脏标记
	g.markDirtyLocked(bboltLedgerTxPrefix+tx.ID, bboltLedgerIdentityKey)
	// PERF-P0-2: maintain the incremental daily contribution counter so
	// CheckShareBoundary does not rescan the full transaction slice per
	// request. Only "contribution" transactions accrue the cap.
	if txType == "contribution" && amount != 0 {
		g.dailyContrib[dailyKey(nodeID, tx.Timestamp)] += amount
	}
	return tx, nil
}

// GetDailyContributions returns the total contributed by nodeID on the given
// day (local timezone), maintained incrementally (PERF-P0-2). Lock order note:
// this takes g.mu only — callers that already hold nm.mu (CheckShareBoundary)
// keep the single nm.mu → g.mu order; no ledger code ever takes nm.mu.
func (g *GossipLedger) GetDailyContributions(nodeID string, day time.Time) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.dailyContrib[dailyKey(nodeID, day)]
}

// dailyKey returns the per-day counter key for a node (local date).
func dailyKey(nodeID string, t time.Time) string {
	return nodeID + "|" + t.Format("2006-01-02")
}

func (g *GossipLedger) GetTransactionChain(nodeID string) []*SignedTransaction {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var chain []*SignedTransaction
	for _, tx := range g.txs {
		if tx.NodeID == nodeID {
			chain = append(chain, tx)
		}
	}
	return chain
}

func (g *GossipLedger) DeriveBalance(nodeID string) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var balance int64
	for _, tx := range g.txs {
		if tx.NodeID == nodeID {
			switch tx.Type {
			case "contribution":
				balance += tx.Amount
			case "consumption":
				balance -= tx.Amount
			}
		}
	}
	return balance
}

func (g *GossipLedger) VerifyChain() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.chainValid()
}

// chainValid checks the transaction hash chain without locking. Callers must
// hold at least a read lock.
func (g *GossipLedger) chainValid() bool {
	for i, tx := range g.txs {
		if i > 0 {
			if tx.PrevHash != g.txs[i-1].Hash {
				return false
			}
		}
		expected := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s", tx.ID, tx.Type, tx.NodeID, tx.Amount, tx.ModelID, tx.RequestID, tx.PrevHash)))
		if tx.Hash != fmt.Sprintf("%x", expected[:]) {
			return false
		}
	}
	return true
}

// LedgerTransparency is the public-welfare transparency view: where compute
// came from (contributions by peer/model) and the integrity of the record. It
// powers the admin "算力从哪来、到哪去" panel (P2-2).
type LedgerTransparency struct {
	PeerID            string           `json:"peer_id"`
	TotalTokens       int64            `json:"total_tokens"`
	ContributionCount int              `json:"contribution_count"`
	ByModel           map[string]int64 `json:"by_model"`
	ByPeer            map[string]int64 `json:"by_peer"`
	TrustCount        int              `json:"trust_count"`
	ClaimCount        int              `json:"claim_count"`
	PenaltyCount      int              `json:"penalty_count"`
	TransactionCount  int              `json:"transaction_count"`
	ChainValid        bool             `json:"chain_valid"`
}

// GetTransparency aggregates the ledger into a transparency report. Callers may
// hold any lock state; the method locks internally.
func (g *GossipLedger) GetTransparency() LedgerTransparency {
	g.mu.RLock()
	defer g.mu.RUnlock()
	t := LedgerTransparency{
		PeerID:  g.peerID,
		ByModel: map[string]int64{},
		ByPeer:  map[string]int64{},
	}
	for _, r := range g.recs {
		t.TotalTokens += r.Tokens
		t.ContributionCount++
		if r.ModelID != "" {
			t.ByModel[r.ModelID] += r.Tokens
		}
		if r.PeerID != "" {
			t.ByPeer[r.PeerID] += r.Tokens
		}
	}
	t.TrustCount = len(g.trusts)
	t.ClaimCount = len(g.claims)
	t.PenaltyCount = len(g.penalties)
	t.TransactionCount = len(g.txs)
	t.ChainValid = g.chainValid()
	return t
}

// ContributorHonor is a purely-visibility entry on the public-welfare honor
// roll (P2 "让贡献者被看见"): how much compute a peer donated. It carries NO
// economic meaning — no points, no exchange rate, no trading, no entitled
// access. It exists so donors and volunteers see their real impact.
type ContributorHonor struct {
	PeerID   string    `json:"peer_id"`
	Tokens   int64     `json:"tokens"`
	Records  int       `json:"records"`
	LastSeen time.Time `json:"last_seen"`
}

// GetContributorHonors aggregates contribution records per peer and returns
// them sorted by donated tokens (desc), then record count (desc), then peer id
// (asc) for deterministic ordering. Informational only — never consulted by any
// quota, priority, or governance code path.
func (g *GossipLedger) GetContributorHonors() []ContributorHonor {
	g.mu.RLock()
	defer g.mu.RUnlock()
	type agg struct {
		tokens   int64
		records  int
		lastSeen time.Time
	}
	m := make(map[string]*agg)
	for _, r := range g.recs {
		a := m[r.PeerID]
		if a == nil {
			a = &agg{}
			m[r.PeerID] = a
		}
		a.tokens += r.Tokens
		a.records++
		if r.Timestamp.After(a.lastSeen) {
			a.lastSeen = r.Timestamp
		}
	}
	out := make([]ContributorHonor, 0, len(m))
	for id, a := range m {
		out = append(out, ContributorHonor{
			PeerID:   id,
			Tokens:   a.tokens,
			Records:  a.records,
			LastSeen: a.lastSeen,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		if out[i].Records != out[j].Records {
			return out[i].Records > out[j].Records
		}
		return out[i].PeerID < out[j].PeerID
	})
	return out
}

// csvSafeCell neutralizes spreadsheet formula injection (SEC-P2-9): a cell
// that begins with =, +, -, @, tab or CR is prefixed with a single quote so
// spreadsheet software treats it as text instead of executing it as a formula.
func csvSafeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// ExportContributionsCSV renders all contribution records as CSV (header +
// one row per record) for researchers / public-welfare transparency (P4-1).
func (g *GossipLedger) ExportContributionsCSV() (string, error) {
	g.mu.RLock()
	rows := make([]*ContributionRecord, 0, len(g.recs))
	for _, r := range g.recs {
		rows = append(rows, r)
	}
	g.mu.RUnlock()
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"id", "peer_id", "model_id", "provider", "tokens", "value_usd", "timestamp"}); err != nil {
		return "", err
	}
	for _, r := range rows {
		row := []string{
			csvSafeCell(r.ID),
			csvSafeCell(r.PeerID),
			csvSafeCell(r.ModelID),
			csvSafeCell(r.Provider),
			strconv.FormatInt(r.Tokens, 10),
			strconv.FormatFloat(r.ValueUSD, 'f', 2, 64),
			r.Timestamp.Format(time.RFC3339),
		}
		if err := w.Write(row); err != nil {
			return "", err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ExportLedgerJSON renders the full ledger (contributions, trusts, claims,
// penalties, transactions) as indented JSON for download / archival (P4-1).
func (g *GossipLedger) ExportLedgerJSON() ([]byte, error) {
	// PERF-P3-19: snapshot under the read lock, marshal outside it.
	g.mu.RLock()
	payload := map[string]interface{}{
		"peer_id":       g.peerID,
		"contributions": cloneRecs(g.recs),
		"trusts":        cloneTrusts(g.trusts),
		"claims":        cloneClaims(g.claims),
		"penalties":     clonePenalties(g.penalties),
		"transactions":  append([]*SignedTransaction(nil), g.txs...),
	}
	g.mu.RUnlock()
	return json.MarshalIndent(payload, "", "  ")
}

func (g *GossipLedger) GetPenalties(peerID string) ([]*PenaltyRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []*PenaltyRecord
	for _, p := range g.penalties {
		if p.PeerID == peerID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (g *GossipLedger) Count() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.recs) + len(g.trusts) + len(g.claims) + len(g.penalties)
}

func (g *GossipLedger) GetAllClaims() []*CapabilityClaim {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*CapabilityClaim, 0, len(g.claims))
	for _, c := range g.claims {
		out = append(out, c)
	}
	return out
}

func (g *GossipLedger) GetAllContributions() []*ContributionRecord {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*ContributionRecord, 0, len(g.recs))
	for _, r := range g.recs {
		out = append(out, r)
	}
	return out
}

func (g *GossipLedger) GetAllTrusts() []*TrustRecord {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*TrustRecord, 0, len(g.trusts))
	for _, t := range g.trusts {
		out = append(out, t)
	}
	return out
}

func (g *GossipLedger) GetAllPenalties() []*PenaltyRecord {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*PenaltyRecord, 0, len(g.penalties))
	for _, p := range g.penalties {
		out = append(out, p)
	}
	return out
}

func (g *GossipLedger) GetAllTransactions() []*SignedTransaction {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*SignedTransaction, len(g.txs))
	copy(out, g.txs)
	return out
}

func (g *GossipLedger) PeerID() string {
	return g.peerID
}

type gossipLedgerData struct {
	PeerID    string                         `json:"peer_id"`
	Recs      map[string]*ContributionRecord `json:"recs"`
	Trusts    map[string]*TrustRecord        `json:"trusts"`
	Claims    map[string]*CapabilityClaim    `json:"claims"`
	Penalties map[string]*PenaltyRecord      `json:"penalties"`
	Txs       []*SignedTransaction           `json:"txs"`
	Seq       uint64                         `json:"seq"`
	PubKey    []byte                         `json:"pub_key"`
	PrivKey   []byte                         `json:"priv_key"`
}

func (g *GossipLedger) Save(path string) error {
	// PERF-P1-4: snapshot the maps under the read lock, then marshal the
	// copies OUTSIDE the lock — the previous code marshalled the live map
	// references after unlocking, racing concurrent writers.
	g.mu.RLock()
	data := gossipLedgerData{
		PeerID:    g.peerID,
		Recs:      cloneRecs(g.recs),
		Trusts:    cloneTrusts(g.trusts),
		Claims:    cloneClaims(g.claims),
		Penalties: clonePenalties(g.penalties),
		Txs:       append([]*SignedTransaction(nil), g.txs...),
		Seq:       g.seq,
		PubKey:    g.pub,
		PrivKey:   g.priv,
	}
	g.mu.RUnlock()

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, raw, 0600)
}

func cloneRecs(m map[string]*ContributionRecord) map[string]*ContributionRecord {
	out := make(map[string]*ContributionRecord, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneTrusts(m map[string]*TrustRecord) map[string]*TrustRecord {
	out := make(map[string]*TrustRecord, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneClaims(m map[string]*CapabilityClaim) map[string]*CapabilityClaim {
	out := make(map[string]*CapabilityClaim, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func clonePenalties(m map[string]*PenaltyRecord) map[string]*PenaltyRecord {
	out := make(map[string]*PenaltyRecord, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func LoadGossipLedger(path string) (*GossipLedger, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data gossipLedgerData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	txIndex := make(map[string]*SignedTransaction, len(data.Txs))
	daily := make(map[string]int64, len(data.Txs))
	for _, tx := range data.Txs {
		txIndex[tx.ID] = tx
		if tx.Type == "contribution" {
			daily[dailyKey(tx.NodeID, tx.Timestamp)] += tx.Amount
		}
	}
	return &GossipLedger{
		peerID:       data.PeerID,
		hashStore:    NewContentHashStore(),
		recs:         data.Recs,
		trusts:       data.Trusts,
		claims:       data.Claims,
		penalties:    data.Penalties,
		txs:          data.Txs,
		txIndex:      txIndex,
		dailyContrib: daily,
		seq:          data.Seq,
		pub:          ed25519.PublicKey(data.PubKey),
		priv:         ed25519.PrivateKey(data.PrivKey),
		dirty:        make(map[string]struct{}),
	}, nil
}

type CapabilityVerifier struct {
	mu           sync.RWMutex
	probeFn      func(peerID, modelID string) (bool, int64, error)
	crossResults map[string][]ProbeResult
	// lastProbe is a per-(peer,model) index of the most recent probe timestamp
	// (PERF-P1-6) so the scheduler does not rescan all history every tick.
	lastProbe    map[string]time.Time
	minVerifiers int
}

func NewCapabilityVerifier(probeFn func(peerID, modelID string) (bool, int64, error), minVerifiers int) *CapabilityVerifier {
	if minVerifiers <= 0 {
		minVerifiers = 2
	}
	return &CapabilityVerifier{
		probeFn:      probeFn,
		crossResults: make(map[string][]ProbeResult),
		lastProbe:    make(map[string]time.Time),
		minVerifiers: minVerifiers,
	}
}

func (cv *CapabilityVerifier) Probe(peerID, modelID string) *ProbeResult {
	var result ProbeResult
	result.PeerID = peerID
	result.ModelID = modelID
	result.Timestamp = time.Now().UTC()

	if cv.probeFn != nil {
		ok, latency, err := cv.probeFn(peerID, modelID)
		result.Success = ok
		result.LatencyMS = latency
		if err != nil {
			result.Error = err.Error()
		}
	} else {
		result.Success = true
		result.LatencyMS = 50
	}

	cv.mu.Lock()
	cv.crossResults[modelID] = append(cv.crossResults[modelID], result)
	// PERF-P1-6: maintain the per-(peer,model) last-probe index.
	cv.lastProbe[peerID+"|"+modelID] = result.Timestamp
	cv.mu.Unlock()

	return &result
}

func (cv *CapabilityVerifier) VerifyClaim(claim *CapabilityClaim) ([]*ProbeResult, bool) {
	var results []*ProbeResult
	allOK := true

	for _, modelID := range claim.Models {
		r := cv.Probe(claim.PeerID, modelID)
		results = append(results, r)
		if !r.Success {
			allOK = false
		}
	}

	return results, allOK
}

func (cv *CapabilityVerifier) CrossVerify(modelID string) (int, bool) {
	cv.mu.RLock()
	defer cv.mu.RUnlock()

	results := cv.crossResults[modelID]
	successCount := 0
	seen := make(map[string]bool)

	for _, r := range results {
		if r.Success && !seen[r.PeerID] {
			successCount++
			seen[r.PeerID] = true
		}
	}

	return successCount, successCount >= cv.minVerifiers
}

func (cv *CapabilityVerifier) GetProbeHistory(modelID string) []ProbeResult {
	cv.mu.RLock()
	defer cv.mu.RUnlock()
	results := cv.crossResults[modelID]
	out := make([]ProbeResult, len(results))
	copy(out, results)
	return out
}

// probeSchedule determines the next probe interval based on node category.
// New node: 5min, regular: 30min, high-rep: 2h, suspicious: 1min (§10.3).
func probeSchedule(nodeID string) time.Duration {
	if repMgr != nil {
		if rep := repMgr.GetReputation(nodeID); rep != nil {
			if rep.OverallScore < 30 {
				return 1 * time.Minute
			}
			if rep.OverallScore > 80 {
				return 2 * time.Hour
			}
		}
	}
	if routeTable != nil {
		e := routeTable.Get(nodeID)
		if e != nil && time.Since(e.LastSeen) < 10*time.Minute {
			return 5 * time.Minute
		}
	}
	return 30 * time.Minute
}

// ProbeSchedulerLoop runs the periodic active probing of known peers.
// It collects capability claims from the ledger and probes each claimed
// model at the interval determined by probeSchedule.
func (cv *CapabilityVerifier) ProbeSchedulerLoop() {
	slog.Info("capability probe scheduler started")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-globalStopCh:
			return
		case <-ticker.C:
			cv.probeDueClaims()
		}
	}
}

// probeDueClaims probes every claim whose per-(peer,model) interval has
// elapsed. PERF-P1-6: last-probe timestamps come from a per-(peer,model)
// index, so this is O(claims×models) instead of O(claims×models×history).
func (cv *CapabilityVerifier) probeDueClaims() {
	if contributionLedger == nil {
		return
	}
	claims := contributionLedger.GetAllClaims()
	for _, claim := range claims {
		if claim.PeerID == "" || len(claim.Models) == 0 {
			continue
		}
		for _, modelID := range claim.Models {
			lastProbe := cv.lastProbeTime(claim.PeerID, modelID)
			interval := probeSchedule(claim.PeerID)
			if time.Since(lastProbe) < interval {
				continue
			}
			r := cv.Probe(claim.PeerID, modelID)
			slog.Debug("scheduled probe completed",
				"peer", claim.PeerID, "model", modelID,
				"success", r.Success, "latency_ms", r.LatencyMS)
		}
	}
}

// lastProbeTime returns the most recent probe timestamp for (peerID, modelID)
// from the per-(peer,model) index (PERF-P1-6), or zero time if never probed.
func (cv *CapabilityVerifier) lastProbeTime(peerID, modelID string) time.Time {
	cv.mu.RLock()
	defer cv.mu.RUnlock()
	key := peerID + "|" + modelID
	if r, ok := cv.lastProbe[key]; ok {
		return r
	}
	return time.Time{}
}

// cleanup bounds the transient probe history (PERF-P1-7): drops last-probe
// index entries older than maxAge and caps each model's crossResults slice.
func (cv *CapabilityVerifier) cleanup(maxAge time.Duration, maxPerModel int) {
	cv.mu.Lock()
	defer cv.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for key, ts := range cv.lastProbe {
		if ts.Before(cutoff) {
			delete(cv.lastProbe, key)
		}
	}
	for modelID, results := range cv.crossResults {
		if len(results) > maxPerModel {
			cv.crossResults[modelID] = results[len(results)-maxPerModel:]
		}
	}
}

// CrossVerifyWithQuorum performs cross-verification: 3 independent verifiers
// probe the same model, and deviation >20% triggers investigation (§10.3).
func (cv *CapabilityVerifier) CrossVerifyWithQuorum(modelID string) (verified int, suspect bool) {
	cv.mu.RLock()
	results := cv.crossResults[modelID]
	cv.mu.RUnlock()

	successCount := 0
	totalLatency := int64(0)
	seen := make(map[string]bool)
	var peerLatencies []int64

	for _, r := range results {
		if r.PeerID == "" || seen[r.PeerID] {
			continue
		}
		seen[r.PeerID] = true
		if r.Success {
			successCount++
			totalLatency += r.LatencyMS
			peerLatencies = append(peerLatencies, r.LatencyMS)
		}
	}

	if len(peerLatencies) < 3 {
		return successCount, false
	}

	avgLatency := float64(totalLatency) / float64(len(peerLatencies))
	for _, lat := range peerLatencies {
		if avgLatency > 0 {
			deviation := float64(lat) / avgLatency
			if deviation < 0.8 || deviation > 1.2 {
				slog.Warn("cross-verify: latency deviation >20%, suspect",
					"model", modelID, "latency_ms", lat, "avg", avgLatency)
				return successCount, true
			}
		}
	}

	return successCount, false
}

type ContentHashStore struct {
	mu         sync.RWMutex
	localCache map[string][]byte
}

// NewContentHashStore returns a local content-addressing store. It computes a
// SHA-256 content hash that serves as an integrity proof and keeps a local
// cache for redundancy.
//
// IMPORTANT: This is NOT a real IPFS node. It provides a verifiable content
// hash and local-only redundancy. Distributed persistence (real IPFS / multi-
// node replication) is a planned future phase and must not be implied by the
// naming or behavior here.
func NewContentHashStore() *ContentHashStore {
	return &ContentHashStore{
		localCache: make(map[string][]byte),
	}
}

func (c *ContentHashStore) StoreJSON(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	cid := "sha256:" + fmt.Sprintf("%x", h[:])
	c.mu.Lock()
	// PERF-P1-7: bound the local content cache (content hashes are derivable
	// from the records, so eviction is safe).
	if len(c.localCache) >= contentHashCacheMax {
		for k := range c.localCache {
			delete(c.localCache, k)
			break
		}
	}
	c.localCache[cid] = data
	c.mu.Unlock()
	return cid, nil
}

// contentHashCacheMax caps the number of cached JSON blobs (PERF-P1-7).
const contentHashCacheMax = 1024

// Cleanup evicts cache entries above maxEntries (PERF-P1-7).
func (c *ContentHashStore) Cleanup(maxEntries int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.localCache) > maxEntries {
		for k := range c.localCache {
			delete(c.localCache, k)
			break
		}
	}
}

// ============================================================
// G4: bbolt backend for the contribution ledger
// ============================================================

// ledgerIdentity 是 meta/identity 的值：节点身份 + seq（含 ed25519 私钥）。
// 字段名与 gossipLedgerData 的 peer_id/seq/pub_key/priv_key 对齐。
type ledgerIdentity struct {
	PeerID  string             `json:"peer_id"`
	Seq     uint64             `json:"seq"`
	PubKey  ed25519.PublicKey  `json:"pub_key"`
	PrivKey ed25519.PrivateKey `json:"priv_key"`
}

type bboltKV struct {
	k string
	v []byte
}

// marshalLedgerKeyLocked 从内存序列化单个 key。调用方必须持有 g.mu（读即可）。
func (g *GossipLedger) marshalLedgerKeyLocked(key string) ([]byte, error) {
	switch {
	case key == bboltLedgerIdentityKey:
		return json.Marshal(ledgerIdentity{
			PeerID:  g.peerID,
			Seq:     g.seq,
			PubKey:  g.pub,
			PrivKey: g.priv,
		})
	case strings.HasPrefix(key, bboltLedgerContribPrefix):
		if r := g.recs[strings.TrimPrefix(key, bboltLedgerContribPrefix)]; r != nil {
			return json.Marshal(r)
		}
	case strings.HasPrefix(key, bboltLedgerTrustPrefix):
		if r := g.trusts[strings.TrimPrefix(key, bboltLedgerTrustPrefix)]; r != nil {
			return json.Marshal(r)
		}
	case strings.HasPrefix(key, bboltLedgerClaimPrefix):
		if r := g.claims[strings.TrimPrefix(key, bboltLedgerClaimPrefix)]; r != nil {
			return json.Marshal(r)
		}
	case strings.HasPrefix(key, bboltLedgerPenaltyPrefix):
		if r := g.penalties[strings.TrimPrefix(key, bboltLedgerPenaltyPrefix)]; r != nil {
			return json.Marshal(r)
		}
	case strings.HasPrefix(key, bboltLedgerTxPrefix):
		if t := g.txIndex[strings.TrimPrefix(key, bboltLedgerTxPrefix)]; t != nil {
			return json.Marshal(t)
		}
	}
	return nil, fmt.Errorf("ledger bbolt: dirty key not found in memory: %s", key)
}

// serializeKeysLocked 批量序列化。调用方必须持有 g.mu（读即可）。
func (g *GossipLedger) serializeKeysLocked(keys []string) ([]bboltKV, error) {
	puts := make([]bboltKV, 0, len(keys))
	for _, k := range keys {
		v, err := g.marshalLedgerKeyLocked(k)
		if err != nil {
			return nil, err
		}
		puts = append(puts, bboltKV{k: k, v: v})
	}
	return puts, nil
}

// putKVsBbolt 在单个写事务内批量 Put。
func putKVsBbolt(h *bboltHandle, puts []bboltKV) error {
	return h.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bboltBucketLedger))
		for _, p := range puts {
			if err := b.Put([]byte(p.k), p.v); err != nil {
				return err
			}
		}
		return nil
	})
}

// saveBbolt 将脏 key 以单事务批量写入。换出（swap）语义：序列化前把 dirty
// 集合换成空 map，期间新标记的 key 留在新集合里由下次 flush 写入；
// 事务失败则把换出的 key 合并回去重试。
func (g *GossipLedger) saveBbolt(h *bboltHandle) error {
	g.mu.Lock()
	if len(g.dirty) == 0 {
		g.mu.Unlock()
		return nil
	}
	keys := make([]string, 0, len(g.dirty))
	for k := range g.dirty {
		keys = append(keys, k)
	}
	pending := g.dirty
	g.dirty = make(map[string]struct{})
	puts, err := g.serializeKeysLocked(keys)
	g.mu.Unlock()
	if err != nil {
		g.mu.Lock()
		for k := range pending {
			g.dirty[k] = struct{}{}
		}
		g.mu.Unlock()
		return err
	}
	if err := putKVsBbolt(h, puts); err != nil {
		g.mu.Lock()
		for k := range pending {
			g.dirty[k] = struct{}{}
		}
		g.mu.Unlock()
		return err
	}
	return nil
}

// writeAllBbolt 全量写入（迁移用）：单事务。
func (g *GossipLedger) writeAllBbolt(h *bboltHandle) error {
	g.mu.RLock()
	keys := make([]string, 0, len(g.recs)+len(g.trusts)+len(g.claims)+len(g.penalties)+len(g.txs)+1)
	keys = append(keys, bboltLedgerIdentityKey)
	for id := range g.recs {
		keys = append(keys, bboltLedgerContribPrefix+id)
	}
	for id := range g.trusts {
		keys = append(keys, bboltLedgerTrustPrefix+id)
	}
	for id := range g.claims {
		keys = append(keys, bboltLedgerClaimPrefix+id)
	}
	for id := range g.penalties {
		keys = append(keys, bboltLedgerPenaltyPrefix+id)
	}
	for _, tx := range g.txs {
		keys = append(keys, bboltLedgerTxPrefix+tx.ID)
	}
	puts, err := g.serializeKeysLocked(keys)
	g.mu.RUnlock()
	if err != nil {
		return err
	}
	return putKVsBbolt(h, puts)
}

// loadLedgerBbolt 从 bbolt 重建账本。dailyContrib 与 JSON 路径一致，由 txs 重建。
func loadLedgerBbolt(h *bboltHandle) (*GossipLedger, error) {
	g := &GossipLedger{
		hashStore:    NewContentHashStore(),
		recs:         make(map[string]*ContributionRecord),
		trusts:       make(map[string]*TrustRecord),
		claims:       make(map[string]*CapabilityClaim),
		penalties:    make(map[string]*PenaltyRecord),
		txIndex:      make(map[string]*SignedTransaction),
		dailyContrib: make(map[string]int64),
		dirty:        make(map[string]struct{}),
	}
	var ident *ledgerIdentity
	err := h.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bboltBucketLedger)).Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			key := string(k)
			switch {
			case key == bboltLedgerIdentityKey:
				var id ledgerIdentity
				if err := json.Unmarshal(v, &id); err != nil {
					return fmt.Errorf("unmarshal ledger identity: %w", err)
				}
				ident = &id
			case strings.HasPrefix(key, bboltLedgerContribPrefix):
				var r ContributionRecord
				if err := json.Unmarshal(v, &r); err != nil {
					slog.Warn("skip corrupt contrib record", "key", key)
					continue
				}
				g.recs[r.ID] = &r
			case strings.HasPrefix(key, bboltLedgerTrustPrefix):
				var r TrustRecord
				if err := json.Unmarshal(v, &r); err != nil {
					slog.Warn("skip corrupt trust record", "key", key)
					continue
				}
				g.trusts[r.ID] = &r
			case strings.HasPrefix(key, bboltLedgerClaimPrefix):
				var r CapabilityClaim
				if err := json.Unmarshal(v, &r); err != nil {
					slog.Warn("skip corrupt claim record", "key", key)
					continue
				}
				g.claims[r.ID] = &r
			case strings.HasPrefix(key, bboltLedgerPenaltyPrefix):
				var r PenaltyRecord
				if err := json.Unmarshal(v, &r); err != nil {
					slog.Warn("skip corrupt penalty record", "key", key)
					continue
				}
				g.penalties[r.ID] = &r
			case strings.HasPrefix(key, bboltLedgerTxPrefix):
				var t SignedTransaction
				if err := json.Unmarshal(v, &t); err != nil {
					slog.Warn("skip corrupt tx record", "key", key)
					continue
				}
				tc := t
				g.txs = append(g.txs, &tc)
				g.txIndex[tc.ID] = &tc
				if tc.Type == "contribution" {
					g.dailyContrib[dailyKey(tc.NodeID, tc.Timestamp)] += tc.Amount
				}
			default:
				slog.Warn("unknown ledger bbolt key", "key", key)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if ident == nil {
		return nil, fmt.Errorf("ledger bbolt: identity key missing")
	}
	g.peerID = ident.PeerID
	g.seq = ident.Seq
	g.pub = ident.PubKey
	g.priv = ident.PrivKey
	return g, nil
}

// importLedgerToBbolt 执行 JSON→bbolt 一次性迁移：复用 LoadGossipLedger，
// 单事务全量写入，打标记，原文件 rename 为 .bak。幂等。
func importLedgerToBbolt(h *bboltHandle, selfID, dataDir string) (*GossipLedger, error) {
	const domain = "ledger"
	jsonPath := filepath.Join(dataDir, "ledger.json")
	if h.isMigrated(domain) {
		if err := bakJSON(jsonPath); err != nil {
			return nil, err
		}
		g, err := loadLedgerBbolt(h)
		if err != nil {
			return nil, err
		}
		g.bbolt = h
		return g, nil
	}
	var g *GossipLedger
	if _, err := os.Stat(jsonPath); err == nil {
		gl, loadErr := LoadGossipLedger(jsonPath)
		if loadErr != nil {
			// 损坏的 JSON：建新账本（与旧逻辑"损坏则重建"一致），坏文件 bak 留证。
			// P3-5: 这是一次不可逆的网络身份丢失——新账本生成全新的 ed25519
			// 身份，旧身份签名的全部历史数据从此无法验证，对等节点会把本节点
			// 当成一个新节点。ERROR 级别 + 明确恢复指引（不要只记一条 warn）。
			slog.Error("ledger json corrupt: creating a NEW ledger identity in bbolt; previously signed history becomes unverifiable",
				"path", jsonPath, "backup", jsonPath+".bak", "error", loadErr,
				"hint", "if this was unexpected, stop the node and restore the backup over ledger.json (delete openmodelpool.bbolt first so it re-imports)")
			gl, err = NewGossipLedger(selfID)
			if err != nil {
				return nil, err
			}
		}
		g = gl
	} else {
		gl, err := NewGossipLedger(selfID)
		if err != nil {
			return nil, err
		}
		g = gl
	}
	if err := g.writeAllBbolt(h); err != nil {
		return nil, err
	}
	if err := h.markMigrated(domain); err != nil {
		return nil, err
	}
	if err := bakJSON(jsonPath); err != nil {
		return nil, err
	}
	g.bbolt = h
	return g, nil
}
