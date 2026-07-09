package ledger

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// GossipLedger implements a local gossip-style ledger with in-memory storage
// and Ed25519 signature verification.
type GossipLedger struct {
	mu sync.RWMutex

	contributions map[string]*ContributionRecord
	trustRecords  map[string]*TrustRecord
	claims        map[string]*CapabilityClaim
	penalties     map[string]*PenaltyRecord

	// peerID -> records
	peerContributions map[string][]string
	peerTrustRecords  map[string][]string
	peerPenalties    map[string][]string

	// For signing
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	peerID     string
}

// NewGossipLedger creates a new GossipLedger, generating a fresh Ed25519 keypair.
func NewGossipLedger(peerID string) (*GossipLedger, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	return &GossipLedger{
		contributions:     make(map[string]*ContributionRecord),
		trustRecords:      make(map[string]*TrustRecord),
		claims:            make(map[string]*CapabilityClaim),
		penalties:         make(map[string]*PenaltyRecord),
		peerContributions: make(map[string][]string),
		peerTrustRecords:  make(map[string][]string),
		peerPenalties:     make(map[string][]string),
		privateKey:        priv,
		publicKey:         pub,
		peerID:            peerID,
	}, nil
}

// GenerateID creates a unique ID from the current timestamp and random bytes.
func GenerateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	ts := time.Now().UnixNano()
	return fmt.Sprintf("%d-%s", ts, hex.EncodeToString(b))
}

// Sign signs arbitrary data with this node's private key.
func (g *GossipLedger) Sign(data []byte) []byte {
	return ed25519.Sign(g.privateKey, data)
}

// VerifySignature verifies a signature against the given public key and data.
func VerifySignature(pubKey ed25519.PublicKey, data, sig []byte) bool {
	return ed25519.Verify(pubKey, data, sig)
}

// PublicKey returns this ledger's public key.
func (g *GossipLedger) PublicKey() ed25519.PublicKey {
	return g.publicKey
}

// PeerID returns the node's peer ID.
func (g *GossipLedger) PeerID() string {
	return g.peerID
}

// ---------------------------------------------------------------------------
// ContributionLedger implementation
// ---------------------------------------------------------------------------

// RecordContribution signs and stores a contribution record.
// Returns the record ID.
func (g *GossipLedger) RecordContribution(record *ContributionRecord) (string, error) {
	if record.ID == "" {
		record.ID = GenerateID()
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}

	// Sign the canonical payload.
	payload, err := contributionPayload(record)
	if err != nil {
		return "", fmt.Errorf("failed to build payload: %w", err)
	}
	record.Signature = g.Sign(payload)

	g.mu.Lock()
	g.contributions[record.ID] = record
	g.peerContributions[record.PeerID] = append(g.peerContributions[record.PeerID], record.ID)
	g.mu.Unlock()

	return record.ID, nil
}

// GetContribution retrieves a contribution by ID.
func (g *GossipLedger) GetContribution(id string) (*ContributionRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	rec, ok := g.contributions[id]
	if !ok {
		return nil, errors.New("contribution not found: " + id)
	}
	return rec, nil
}

// VerifyContribution verifies the signature of a stored contribution.
func (g *GossipLedger) VerifyContribution(id string) (bool, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	rec, ok := g.contributions[id]
	if !ok {
		return false, errors.New("contribution not found: " + id)
	}
	if len(rec.PeerPublicKey) == 0 {
		return false, errors.New("missing public key in record")
	}

	payload, err := contributionPayload(rec)
	if err != nil {
		return false, err
	}

	return VerifySignature(ed25519.PublicKey(rec.PeerPublicKey), payload, rec.Signature), nil
}

// GetPeerContributions returns all contributions for a given peer.
func (g *GossipLedger) GetPeerContributions(peerID string) ([]*ContributionRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ids := g.peerContributions[peerID]
	var out []*ContributionRecord
	for _, id := range ids {
		if rec, ok := g.contributions[id]; ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Trust records
// ---------------------------------------------------------------------------

// RecordTrust stores a trust probe result.
func (g *GossipLedger) RecordTrust(rec *TrustRecord) (string, error) {
	if rec.ID == "" {
		rec.ID = GenerateID()
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}

	payload, err := trustPayload(rec)
	if err != nil {
		return "", err
	}
	rec.Signature = g.Sign(payload)

	g.mu.Lock()
	g.trustRecords[rec.ID] = rec
	g.peerTrustRecords[rec.SubjectPeerID] = append(g.peerTrustRecords[rec.SubjectPeerID], rec.ID)
	g.mu.Unlock()

	return rec.ID, nil
}

// GetTrustRecords returns all trust records for a subject peer.
func (g *GossipLedger) GetTrustRecords(subjectPeerID string) ([]*TrustRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ids := g.peerTrustRecords[subjectPeerID]
	var out []*TrustRecord
	for _, id := range ids {
		if rec, ok := g.trustRecords[id]; ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Capability claims
// ---------------------------------------------------------------------------

// RecordClaim stores a capability claim.
func (g *GossipLedger) RecordClaim(claim *CapabilityClaim) (string, error) {
	if claim.ID == "" {
		claim.ID = GenerateID()
	}
	if claim.Timestamp.IsZero() {
		claim.Timestamp = time.Now().UTC()
	}

	payload, err := claimPayload(claim)
	if err != nil {
		return "", err
	}
	claim.Signature = g.Sign(payload)

	g.mu.Lock()
	g.claims[claim.ID] = claim
	g.mu.Unlock()

	return claim.ID, nil
}

// GetClaim retrieves a capability claim by ID.
func (g *GossipLedger) GetClaim(id string) (*CapabilityClaim, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	claim, ok := g.claims[id]
	if !ok {
		return nil, errors.New("claim not found: " + id)
	}
	return claim, nil
}

// ---------------------------------------------------------------------------
// Penalty records
// ---------------------------------------------------------------------------

// RecordPenalty stores a penalty record.
func (g *GossipLedger) RecordPenalty(rec *PenaltyRecord) (string, error) {
	if rec.ID == "" {
		rec.ID = GenerateID()
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}

	payload, err := penaltyPayload(rec)
	if err != nil {
		return "", err
	}
	rec.Signature = g.Sign(payload)

	g.mu.Lock()
	g.penalties[rec.ID] = rec
	g.peerPenalties[rec.PeerID] = append(g.peerPenalties[rec.PeerID], rec.ID)
	g.mu.Unlock()

	return rec.ID, nil
}

// GetPenalties returns all penalties for a peer.
func (g *GossipLedger) GetPenalties(peerID string) ([]*PenaltyRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ids := g.peerPenalties[peerID]
	var out []*PenaltyRecord
	for _, id := range ids {
		if rec, ok := g.penalties[id]; ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Gossip sync (simulated)
// ---------------------------------------------------------------------------

// GossipSync merges records from a remote peer into the local ledger.
// In production this would be a CRDT merge over libp2p.
func (g *GossipLedger) GossipSync(contributions []*ContributionRecord, trusts []*TrustRecord, claims []*CapabilityClaim, penalties []*PenaltyRecord) int {
	merged := 0

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, c := range contributions {
		if _, exists := g.contributions[c.ID]; !exists {
			g.contributions[c.ID] = c
			g.peerContributions[c.PeerID] = append(g.peerContributions[c.PeerID], c.ID)
			merged++
		}
	}
	for _, t := range trusts {
		if _, exists := g.trustRecords[t.ID]; !exists {
			g.trustRecords[t.ID] = t
			g.peerTrustRecords[t.SubjectPeerID] = append(g.peerTrustRecords[t.SubjectPeerID], t.ID)
			merged++
		}
	}
	for _, cl := range claims {
		if _, exists := g.claims[cl.ID]; !exists {
			g.claims[cl.ID] = cl
			merged++
		}
	}
	for _, p := range penalties {
		if _, exists := g.penalties[p.ID]; !exists {
			g.penalties[p.ID] = p
			g.peerPenalties[p.PeerID] = append(g.peerPenalties[p.PeerID], p.ID)
			merged++
		}
	}

	return merged
}

// Count returns the total number of records stored in the ledger.
func (g *GossipLedger) Count() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.contributions) + len(g.trustRecords) + len(g.claims) + len(g.penalties)
}


// getAllClaims returns all capability claims (helper for testing/sync).
func (g *GossipLedger) getAllClaims() []*CapabilityClaim {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var out []*CapabilityClaim
	for _, c := range g.claims {
		out = append(out, c)
	}
	return out
}

// ---------------------------------------------------------------------------
// Canonical payload helpers (for signing)
// ---------------------------------------------------------------------------

func contributionPayload(r *ContributionRecord) ([]byte, error) {
	return json.Marshal(struct {
		ID        string    `json:"id"`
		PeerID    string    `json:"peer_id"`
		ModelID   string    `json:"model_id"`
		Provider  string    `json:"provider"`
		Tokens    int64     `json:"tokens"`
		ValueUSD  float64   `json:"value_usd"`
		Timestamp time.Time `json:"timestamp"`
	}{r.ID, r.PeerID, r.ModelID, r.Provider, r.Tokens, r.ValueUSD, r.Timestamp})
}

func trustPayload(r *TrustRecord) ([]byte, error) {
	return json.Marshal(struct {
		ID             string    `json:"id"`
		SubjectPeerID  string    `json:"subject_peer_id"`
		VerifierPeerID string    `json:"verifier_peer_id"`
		ModelID        string    `json:"model_id"`
		Success        bool      `json:"success"`
		LatencyMS      int64     `json:"latency_ms"`
		Timestamp      time.Time `json:"timestamp"`
	}{r.ID, r.SubjectPeerID, r.VerifierPeerID, r.ModelID, r.Success, r.LatencyMS, r.Timestamp})
}

func claimPayload(c *CapabilityClaim) ([]byte, error) {
	return json.Marshal(struct {
		ID         string    `json:"id"`
		PeerID     string    `json:"peer_id"`
		Models     []string  `json:"models"`
		Providers  []string  `json:"providers"`
		MaxQuota   int64     `json:"max_quota"`
		Timestamp  time.Time `json:"timestamp"`
		ValidUntil time.Time `json:"valid_until"`
	}{c.ID, c.PeerID, c.Models, c.Providers, c.MaxQuota, c.Timestamp, c.ValidUntil})
}

func penaltyPayload(r *PenaltyRecord) ([]byte, error) {
	return json.Marshal(struct {
		ID        string    `json:"id"`
		PeerID    string    `json:"peer_id"`
		Reason    string    `json:"reason"`
		Action    string    `json:"action"`
		Timestamp time.Time `json:"timestamp"`
	}{r.ID, r.PeerID, r.Reason, r.Action, r.Timestamp})
}
