package ledger

import (
	"encoding/json"
	"crypto/ed25519"
	"fmt"
	"sync"
	"time"
)

// GossipLedger is a local, append-only contribution ledger. Records are kept
// in memory and mirrored to IPFS (best-effort) via the embedded IPFSClient.
//
// This is a minimal but functional implementation that closes the previously
// undefined `GossipLedger` symbol. The "gossip" sync merges remote records
// into the local store.
type GossipLedger struct {
	mu        sync.RWMutex
	peerID    string
	ipfs      *IPFSClient
	recs      map[string]*ContributionRecord
	trusts    map[string]*TrustRecord
	claims    map[string]*CapabilityClaim
	penalties map[string]*PenaltyRecord
	seq       uint64
	pub       ed25519.PublicKey
	priv      ed25519.PrivateKey
}

// NewGossipLedger creates an empty ledger for the given local peer.
func NewGossipLedger(peerID string) (*GossipLedger, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	return &GossipLedger{
		peerID:    peerID,
		ipfs:      NewIPFSClient(),
		recs:      make(map[string]*ContributionRecord),
		trusts:    make(map[string]*TrustRecord),
		claims:    make(map[string]*CapabilityClaim),
		penalties: make(map[string]*PenaltyRecord),
		pub:       pub,
		priv:      priv,
	}, nil
}

// PublicKey returns the ledger's ed25519 public key.
func (g *GossipLedger) PublicKey() ed25519.PublicKey {
	return g.pub
}

// Sign returns an ed25519 signature over data using the ledger's private key.
func (g *GossipLedger) Sign(data []byte) []byte {
	return ed25519.Sign(g.priv, data)
}

// getAllClaims returns the ledger's capability claims as a slice.
func (g *GossipLedger) getAllClaims() []*CapabilityClaim {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*CapabilityClaim, 0, len(g.claims))
	for _, c := range g.claims {
		out = append(out, c)
	}
	return out
}

// VerifySignature reports whether sig is a valid ed25519 signature of data
// for the given public key.
func VerifySignature(pub ed25519.PublicKey, data, sig []byte) bool {
	return ed25519.Verify(pub, data, sig)
}

// GetPenalties returns all penalties recorded for a peer.
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

// Count returns the total number of records stored in the ledger.
func (g *GossipLedger) Count() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.recs) + len(g.trusts) + len(g.claims) + len(g.penalties)
}

func (g *GossipLedger) nextID(prefix string) string {
	g.seq++
	return fmt.Sprintf("%s-%s-%d", prefix, g.peerID, g.seq)
}

// RecordContribution stores a contribution locally and mirrors it to IPFS.
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
	// Sign the record content so consumers can verify its authenticity.
	if data, err := json.Marshal(record); err == nil {
		record.Signature = g.Sign(data)
	}
	cp := *record
	g.recs[record.ID] = &cp
	g.mu.Unlock()

	if cid, err := g.ipfs.StoreJSON(record); err == nil {
		cp.Proof.IPFSHash = cid
		cp.Proof.StorageLocation = "ipfs"
		g.mu.Lock()
		g.recs[record.ID] = &cp
		g.mu.Unlock()
	}
	return record.ID, nil
}

// GetContribution returns a stored contribution by ID.
func (g *GossipLedger) GetContribution(id string) (*ContributionRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	r, ok := g.recs[id]
	if !ok {
		return nil, fmt.Errorf("contribution %s not found", id)
	}
	return r, nil
}

// VerifyContribution reports whether a contribution exists locally.
func (g *GossipLedger) VerifyContribution(id string) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.recs[id]
	return ok, nil
}

// GetPeerContributions returns all contributions recorded for a peer.
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

// RecordTrust stores a trust probe result.
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
	g.mu.Unlock()
	return rec.ID, nil
}

// RecordClaim stores a capability claim.
func (g *GossipLedger) RecordClaim(claim *CapabilityClaim) (string, error) {
	if claim == nil {
		return "", fmt.Errorf("nil claim")
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
	g.mu.Unlock()
	return claim.ID, nil
}

// RecordPenalty stores a penalty.
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
	// Sign the penalty so consumers can verify its authenticity.
	if data, err := json.Marshal(rec); err == nil {
		rec.Signature = g.Sign(data)
	}
	cp := *rec
	g.penalties[rec.ID] = &cp
	g.mu.Unlock()
	return rec.ID, nil
}

// GossipSync merges remote records into the local store and returns the
// number of new records added.
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
			added++
		}
	}
	return added
}
