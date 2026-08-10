package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ledgerReplicator drives cross-node replication of the contribution ledger
// across the federation (P1-3: "联邦内 N 个节点各存一份 + 哈希校验"). It is
// additive — when nil (default / unit tests) recording behaviour is unchanged.
var ledgerReplicator *LedgerReplicator

// LedgerReplicator pushes contribution records to federation peer nodes and
// reconciles manifests so a record missing on one node can be healed from a
// peer. Replication failures are logged, never fatal — the source node stays
// authoritative.
type LedgerReplicator struct {
	mu      sync.RWMutex
	primary *GossipLedger
	peers   []string // base URLs of federation peers (excluding self)
	client  *http.Client
	selfID  string

	// PERF-P0-3: bounded fan-out worker pool. RecordContribution enqueues via
	// EnqueueContribution instead of spawning one goroutine per record; at most
	// ledgerReplicateWorkers goroutines perform the actual replication and the
	// queue is bounded, so a burst can never create unbounded goroutines.
	jobCh     chan *ContributionRecord
	stopCh    chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
	startOnce sync.Once
}

const (
	ledgerReplicateWorkers   = 4
	ledgerReplicateQueueSize = 256
	ledgerReplicateTimeout   = 10 * time.Second
)

func NewLedgerReplicator(primary *GossipLedger, selfID string, peers ...string) *LedgerReplicator {
	r := &LedgerReplicator{
		primary: primary,
		peers:   append([]string{}, peers...),
		selfID:  selfID,
		// PERF-P1-8: reuse the shared connection-pooled client.
		client: GetSharedHTTPClientWithTimeout(ledgerReplicateTimeout),
		jobCh:  make(chan *ContributionRecord, ledgerReplicateQueueSize),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	r.startWorkers()
	return r
}

// startWorkers launches the bounded replication workers (idempotent).
func (r *LedgerReplicator) startWorkers() {
	r.startOnce.Do(func() {
		var wg sync.WaitGroup
		for i := 0; i < ledgerReplicateWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-r.stopCh:
						return
					case rec := <-r.jobCh:
						if rec == nil {
							return
						}
						if _, err := r.replicateOne(rec); err != nil {
							slog.Warn("ledger replicate failed", "id", rec.ID, "error", err)
						}
					}
				}
			}()
		}
		go func() {
			wg.Wait()
			close(r.done)
		}()
	})
}

// Stop shuts down the worker pool and waits for it to drain (idempotent).
func (r *LedgerReplicator) Stop() {
	r.stopOnce.Do(func() { close(r.stopCh) })
	<-r.done
}

// EnqueueContribution submits a contribution to the bounded worker pool
// (PERF-P0-3). Never blocks the caller for long: if the queue is full the job
// is dropped (the source node stays authoritative). Nil-safe and additive.
func (r *LedgerReplicator) EnqueueContribution(rec *ContributionRecord) {
	if rec == nil {
		return
	}
	select {
	case r.jobCh <- rec:
	case <-r.stopCh:
	default:
		slog.Warn("ledger replicate queue full, dropping contribution", "id", rec.ID)
	}
}

func (r *LedgerReplicator) SetPeers(peers []string) {
	r.mu.Lock()
	r.peers = append([]string{}, peers...)
	r.mu.Unlock()
}

func (r *LedgerReplicator) Peers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string{}, r.peers...)
}

func (r *LedgerReplicator) currentSelfID() string {
	if node != nil {
		return node.NodeID()
	}
	return r.selfID
}

// refreshPeersFromRouteTable derives peer base URLs from the live federation
// route table so replication tracks current membership without manual config.
// Self and address-less entries are skipped.
func (r *LedgerReplicator) refreshPeersFromRouteTable() {
	if routeTable == nil {
		return
	}
	self := r.currentSelfID()
	var peers []string
	for _, e := range routeTable.GetAll() {
		if e.NodeID == self {
			continue
		}
		if len(e.Addresses) == 0 {
			continue
		}
		peers = append(peers, e.Addresses[0])
	}
	if len(peers) > 0 {
		r.SetPeers(peers)
	}
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// ReplicateContribution pushes a freshly-recorded contribution to every known
// federation peer (synchronous; used by callers that need the result).
// Returns how many peers accepted it.
func (r *LedgerReplicator) ReplicateContribution(rec *ContributionRecord) (int, error) {
	return r.replicateOne(rec)
}

// replicateOne performs the actual fan-out to every known peer.
func (r *LedgerReplicator) replicateOne(rec *ContributionRecord) (int, error) {
	if rec == nil {
		return 0, fmt.Errorf("nil contribution record")
	}
	peers := r.Peers()
	if len(peers) == 0 {
		r.refreshPeersFromRouteTable()
		peers = r.Peers()
	}
	if len(peers) == 0 {
		return 0, nil // nobody to replicate to yet
	}
	payload, err := json.Marshal(ledgerSyncPayload{RecordType: "contrib", Record: mustJSON(rec)})
	if err != nil {
		return 0, err
	}
	// PERF-P0-3: bound each peer request with a context deadline (in addition
	// to the client timeout) so a stalled peer cannot pin a worker forever.
	ctx, cancel := context.WithTimeout(context.Background(), ledgerReplicateTimeout)
	defer cancel()
	ok := 0
	for _, base := range peers {
		u := strings.TrimRight(base, "/") + "/ledger/__sync"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := r.client.Do(req)
		if err != nil {
			slog.Warn("ledger replicate failed", "peer", base, "error", err)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			ok++
		}
	}
	return ok, nil
}

// FetchManifest retrieves a peer's ledger manifest.
func (r *LedgerReplicator) FetchManifest(peerBase string) (*LedgerManifest, error) {
	u := strings.TrimRight(peerBase, "/") + "/ledger/__manifest"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest status %d", resp.StatusCode)
	}
	var m LedgerManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// fetchRecord retrieves one record by id from a peer.
func (r *LedgerReplicator) fetchRecord(peerBase, id string) (string, json.RawMessage, error) {
	u := strings.TrimRight(peerBase, "/") + "/ledger/__record?id=" + url.QueryEscape(id)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil, fmt.Errorf("record %s not found on peer", id)
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("record status %d", resp.StatusCode)
	}
	var rr ledgerRecordResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return "", nil, err
	}
	raw, _ := json.Marshal(rr.Record)
	return rr.RecordType, raw, nil
}

// ReconcileWith fetches a peer's manifest, heals records missing locally
// (present on the peer, absent here), and reports divergence (same id,
// different content) for alerting. Divergent records are NOT auto-overwritten.
func (r *LedgerReplicator) ReconcileWith(peerBase string) (ManifestDiff, int, error) {
	return r.reconcileWithManifest(peerBase, BuildManifest(r.primary))
}

// reconcileWithManifest is ReconcileWith against a pre-computed local manifest
// (PERF-P2-16: ReconcileAll builds it once instead of per peer).
func (r *LedgerReplicator) reconcileWithManifest(peerBase string, local *LedgerManifest) (ManifestDiff, int, error) {
	remote, err := r.FetchManifest(peerBase)
	if err != nil {
		return ManifestDiff{}, 0, err
	}
	diff := DiffManifests(local, remote) // Missing = on peer, absent locally
	healed := 0
	for _, me := range diff.Missing {
		rt, raw, ferr := r.fetchRecord(peerBase, me.ID)
		if ferr != nil {
			slog.Warn("ledger reconcile fetch failed", "id", me.ID, "error", ferr)
			continue
		}
		if storeFetchedRecord(r.primary, rt, raw) {
			healed++
		}
	}
	return diff, healed, nil
}

// ReconcileResult summarizes one peer's reconciliation.
type ReconcileResult struct {
	Peer      string `json:"peer"`
	Missing   int    `json:"missing"`
	Healed    int    `json:"healed"`
	Divergent int    `json:"divergent"`
	Error     string `json:"error,omitempty"`
}

// reconcileParallelism bounds concurrent per-peer reconciliations (PERF-P2-16).
const reconcileParallelism = 4

// ReconcileAll reconciles against every known peer with bounded parallelism
// and a single locally-built manifest (PERF-P2-16).
func (r *LedgerReplicator) ReconcileAll() []ReconcileResult {
	peers := r.Peers()
	if len(peers) == 0 {
		r.refreshPeersFromRouteTable()
		peers = r.Peers()
	}
	if len(peers) == 0 {
		return nil
	}
	local := BuildManifest(r.primary)
	sem := make(chan struct{}, reconcileParallelism)
	var wg sync.WaitGroup
	out := make([]ReconcileResult, len(peers))
	for i, p := range peers {
		wg.Add(1)
		go func(i int, peer string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := ReconcileResult{Peer: peer}
			diff, healed, err := r.reconcileWithManifest(peer, local)
			if err != nil {
				res.Error = err.Error()
			} else {
				res.Missing = len(diff.Missing)
				res.Divergent = len(diff.Divergent)
				res.Healed = healed
			}
			out[i] = res
		}(i, p)
	}
	wg.Wait()
	return out
}

// ledgerReconcileInterval controls how often the background reconcile loop
// heals missing ledger records across the federation. Exposed for testing.
var ledgerReconcileInterval = 60 * time.Second

var ledgerReconcileStop chan struct{}

// startLedgerReconcileLoop periodically reconciles the local ledger against all
// known federation peers so a record missing on this node is automatically
// healed from a peer (fulfils the "N 个节点各存一份" redundancy at runtime).
func startLedgerReconcileLoop() {
	if ledgerReplicator == nil {
		return
	}
	ticker := time.NewTicker(ledgerReconcileInterval)
	ledgerReconcileStop = make(chan struct{})
	go func() {
		for {
			select {
			case <-ledgerReconcileStop:
				ticker.Stop()
				return
			case <-ticker.C:
				for _, res := range ledgerReplicator.ReconcileAll() {
					if res.Error != "" {
						slog.Warn("ledger reconcile failed", "peer", res.Peer, "error", res.Error)
					} else if res.Healed > 0 || res.Divergent > 0 {
						slog.Info("ledger reconcile", "peer", res.Peer, "missing", res.Missing, "healed", res.Healed, "divergent", res.Divergent)
					}
				}
			}
		}
	}()
}

// storeFetchedRecord writes a record fetched from a peer into the local ledger
// as-is (preserving its originating signature). Returns false for unsupported
// or rejected types. Chained transactions are intentionally not auto-healed to
// avoid corrupting the hash chain.
func storeFetchedRecord(g *GossipLedger, recordType string, raw json.RawMessage) bool {
	switch recordType {
	case "contrib":
		var rec ContributionRecord
		if json.Unmarshal(raw, &rec) != nil {
			return false
		}
		g.GossipSync([]*ContributionRecord{&rec}, nil, nil, nil)
	case "trust":
		var rec TrustRecord
		if json.Unmarshal(raw, &rec) != nil {
			return false
		}
		g.GossipSync(nil, []*TrustRecord{&rec}, nil, nil)
	case "claim":
		var rec CapabilityClaim
		if json.Unmarshal(raw, &rec) != nil {
			return false
		}
		g.GossipSync(nil, nil, []*CapabilityClaim{&rec}, nil)
	case "penalty":
		var rec PenaltyRecord
		if json.Unmarshal(raw, &rec) != nil {
			return false
		}
		g.GossipSync(nil, nil, nil, []*PenaltyRecord{&rec})
	default:
		return false
	}
	return true
}

// --- HTTP endpoints (global variants use the package-level ledger) ---

type ledgerSyncPayload struct {
	RecordType string          `json:"record_type"`
	Record     json.RawMessage `json:"record"`
}

type ledgerRecordResponse struct {
	RecordType string      `json:"record_type"`
	Record     interface{} `json:"record"`
}

func handleLedgerManifest(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		http.Error(w, "ledger not ready", http.StatusServiceUnavailable)
		return
	}
	handleLedgerManifestFor(w, r, contributionLedger)
}

func handleLedgerManifestFor(w http.ResponseWriter, r *http.Request, g *GossipLedger) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(BuildManifest(g))
}

func handleLedgerSync(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		http.Error(w, "ledger not ready", http.StatusServiceUnavailable)
		return
	}
	handleLedgerSyncFor(w, r, contributionLedger)
}

func handleLedgerSyncFor(w http.ResponseWriter, r *http.Request, g *GossipLedger) {
	var p ledgerSyncPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !storeFetchedRecord(g, p.RecordType, p.Record) {
		http.Error(w, "unsupported or rejected record type", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"accepted": true})
}

func handleLedgerRecord(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		http.Error(w, "ledger not ready", http.StatusServiceUnavailable)
		return
	}
	handleLedgerRecordFor(w, r, contributionLedger)
}

func handleLedgerRecordFor(w http.ResponseWriter, r *http.Request, g *GossipLedger) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	g.mu.RLock()
	var rt string
	var rec interface{}
	if v, ok := g.recs[id]; ok {
		rt, rec = "contrib", v
	} else if v, ok := g.trusts[id]; ok {
		rt, rec = "trust", v
	} else if v, ok := g.claims[id]; ok {
		rt, rec = "claim", v
	} else if v, ok := g.penalties[id]; ok {
		rt, rec = "penalty", v
	} else if tx, ok := g.txIndex[id]; ok {
		// PERF-P2-18: use the tx index instead of a linear scan.
		rt, rec = "tx", tx
	}
	g.mu.RUnlock()
	if rt == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ledgerRecordResponse{RecordType: rt, Record: rec})
}
