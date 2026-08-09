package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

// LedgerManifestEntry is a lightweight digest of one ledger record, used for
// cross-node reconciliation without shipping full records over the wire.
type LedgerManifestEntry struct {
	ID          string `json:"id"`
	RecordType  string `json:"record_type"` // contrib|trust|claim|penalty|tx
	ContentHash string `json:"content_hash"`
}

// LedgerManifest summarizes a node's ledger as a set of record digests indexed
// by record id. Two nodes reconcile by comparing manifests.
type LedgerManifest struct {
	PeerID  string                        `json:"peer_id"`
	Entries map[string]LedgerManifestEntry `json:"entries"`
}

// contentHashContribution returns a stable digest of a contribution's business
// content. It EXCLUDES the node-specific Signature and the derived Proof so
// that identical contributions hashed on different replicas (which re-sign
// with their own ed25519 key) still produce the same digest. Tampering with
// any business field (tokens, model, peer, etc.) changes the digest.
func contentHashContribution(r *ContributionRecord) string {
	cp := *r
	cp.Signature = nil
	cp.Proof = StorageProof{}
	data, err := json.Marshal(cp)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

// BuildManifest constructs a digest manifest of every record in the ledger.
func BuildManifest(g *GossipLedger) *LedgerManifest {
	m := &LedgerManifest{PeerID: g.PeerID(), Entries: map[string]LedgerManifestEntry{}}
	for _, r := range g.GetAllContributions() {
		m.Entries[r.ID] = LedgerManifestEntry{ID: r.ID, RecordType: "contrib", ContentHash: contentHashContribution(r)}
	}
	for _, t := range g.GetAllTrusts() {
		m.Entries[t.ID] = LedgerManifestEntry{ID: t.ID, RecordType: "trust", ContentHash: contentHashContributionOf(t)}
	}
	for _, c := range g.GetAllClaims() {
		m.Entries[c.ID] = LedgerManifestEntry{ID: c.ID, RecordType: "claim", ContentHash: contentHashClaimOf(c)}
	}
	for _, p := range g.GetAllPenalties() {
		m.Entries[p.ID] = LedgerManifestEntry{ID: p.ID, RecordType: "penalty", ContentHash: contentHashPenaltyOf(p)}
	}
	for _, tx := range g.GetAllTransactions() {
		m.Entries[tx.ID] = LedgerManifestEntry{ID: tx.ID, RecordType: "tx", ContentHash: contentHashTxOf(tx)}
	}
	return m
}

// contentHashContributionOf / ... strip the node-specific Signature before
// hashing, mirroring contentHashContribution, so cross-replica digests match.
func contentHashContributionOf(t *TrustRecord) string     { return stripHash(t) }
func contentHashClaimOf(c *CapabilityClaim) string         { return stripHash(c) }
func contentHashPenaltyOf(p *PenaltyRecord) string         { return stripHash(p) }
func contentHashTxOf(tx *SignedTransaction) string         { return stripHash(tx) }

func stripHash(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

// ManifestDiff describes the difference between a local manifest and a remote
// one, from the local node's perspective.
type ManifestDiff struct {
	Missing   []LedgerManifestEntry // present remotely, absent locally
	Divergent []string              // same id, different content hash (tamper/fork)
	Extra     []LedgerManifestEntry // present locally, absent remotely
}

// DiffManifests compares a local manifest against a remote one.
func DiffManifests(local, remote *LedgerManifest) ManifestDiff {
	var d ManifestDiff
	for id, re := range remote.Entries {
		if _, ok := local.Entries[id]; !ok {
			d.Missing = append(d.Missing, re)
		} else if local.Entries[id].ContentHash != re.ContentHash {
			d.Divergent = append(d.Divergent, id)
		}
	}
	for id, le := range local.Entries {
		if _, ok := remote.Entries[id]; !ok {
			d.Extra = append(d.Extra, le)
		}
	}
	return d
}

// ReplicaManager keeps N copies of the contribution ledger across federation
// nodes so a single node's loss or tampering does not lose the record of who
// contributed what. replicas[0] is the primary; the rest are redundant copies.
type ReplicaManager struct {
	mu       sync.RWMutex
	replicas []*GossipLedger
	N        int
}

// NewReplicaManager creates a manager with a primary and zero or more redundant
// replica ledgers. N is the total desired copies (primary + replicas).
func NewReplicaManager(primary *GossipLedger, replicas ...*GossipLedger) *ReplicaManager {
	all := append([]*GossipLedger{primary}, replicas...)
	return &ReplicaManager{replicas: all, N: len(all)}
}

// ReplicateContribution writes a contribution record to every replica. It
// returns the number of replicas that accepted it (a quorum if >= majority).
func (rm *ReplicaManager) ReplicateContribution(rec *ContributionRecord) (int, error) {
	if rec == nil {
		return 0, fmt.Errorf("nil contribution record")
	}
	rm.mu.RLock()
	replicas := make([]*GossipLedger, len(rm.replicas))
	copy(replicas, rm.replicas)
	rm.mu.RUnlock()
	ok := 0
	var lastErr error
	for _, r := range replicas {
		if _, err := r.RecordContribution(rec); err != nil {
			lastErr = err
			continue
		}
		ok++
	}
	if ok == 0 && lastErr != nil {
		return 0, lastErr
	}
	return ok, nil
}

// VerifyConsistency compares the manifest of every replica. It returns true
// only if all replicas hold byte-identical record digests. The report maps each
// replica's peer id to its record count for diagnostics.
func (rm *ReplicaManager) VerifyConsistency() (bool, map[string]int) {
	rm.mu.RLock()
	replicas := make([]*GossipLedger, len(rm.replicas))
	copy(replicas, rm.replicas)
	rm.mu.RUnlock()
	report := map[string]int{}
	var ref *LedgerManifest
	for i, r := range replicas {
		m := BuildManifest(r)
		report[r.PeerID()] = len(m.Entries)
		if i == 0 {
			ref = m
			continue
		}
		if !manifestsEqual(ref, m) {
			return false, report
		}
	}
	return true, report
}

func manifestsEqual(a, b *LedgerManifest) bool {
	if len(a.Entries) != len(b.Entries) {
		return false
	}
	for id, ae := range a.Entries {
		be, ok := b.Entries[id]
		if !ok || ae.ContentHash != be.ContentHash {
			return false
		}
	}
	return true
}
