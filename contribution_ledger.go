package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
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
	IPFSHash        string `json:"ipfs_hash"`
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
	ipfs      *IPFSClient
	recs      map[string]*ContributionRecord
	trusts    map[string]*TrustRecord
	claims    map[string]*CapabilityClaim
	penalties map[string]*PenaltyRecord
	txs       []*SignedTransaction
	txIndex   map[string]*SignedTransaction
	seq       uint64
	pub       ed25519.PublicKey
	priv      ed25519.PrivateKey
}

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
		txs:       make([]*SignedTransaction, 0),
		txIndex:   make(map[string]*SignedTransaction),
		pub:       pub,
		priv:      priv,
	}, nil
}

func (g *GossipLedger) PublicKey() ed25519.PublicKey {
	return g.pub
}

func (g *GossipLedger) Sign(data []byte) []byte {
	return ed25519.Sign(g.priv, data)
}

func (g *GossipLedger) nextID(prefix string) string {
	g.seq++
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
	return tx, nil
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
	PeerID    string                        `json:"peer_id"`
	Recs      map[string]*ContributionRecord `json:"recs"`
	Trusts    map[string]*TrustRecord        `json:"trusts"`
	Claims    map[string]*CapabilityClaim     `json:"claims"`
	Penalties map[string]*PenaltyRecord       `json:"penalties"`
	Txs       []*SignedTransaction            `json:"txs"`
	Seq       uint64                          `json:"seq"`
	PubKey    []byte                          `json:"pub_key"`
	PrivKey   []byte                          `json:"priv_key"`
}

func (g *GossipLedger) Save(path string) error {
	g.mu.RLock()
	data := gossipLedgerData{
		PeerID:    g.peerID,
		Recs:      g.recs,
		Trusts:    g.trusts,
		Claims:    g.claims,
		Penalties: g.penalties,
		Txs:       g.txs,
		Seq:       g.seq,
		PubKey:    g.pub,
		PrivKey:   g.priv,
	}
	g.mu.RUnlock()

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
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
	for _, tx := range data.Txs {
		txIndex[tx.ID] = tx
	}
	return &GossipLedger{
		peerID:    data.PeerID,
		ipfs:      NewIPFSClient(),
		recs:      data.Recs,
		trusts:    data.Trusts,
		claims:    data.Claims,
		penalties: data.Penalties,
		txs:       data.Txs,
		txIndex:   txIndex,
		seq:       data.Seq,
		pub:       ed25519.PublicKey(data.PubKey),
		priv:      ed25519.PrivateKey(data.PrivKey),
	}, nil
}

type CapabilityVerifier struct {
	mu           sync.RWMutex
	probeFn      func(peerID, modelID string) (bool, int64, error)
	crossResults map[string][]ProbeResult
	minVerifiers int
}

func NewCapabilityVerifier(probeFn func(peerID, modelID string) (bool, int64, error), minVerifiers int) *CapabilityVerifier {
	if minVerifiers <= 0 {
		minVerifiers = 2
	}
	return &CapabilityVerifier{
		probeFn:      probeFn,
		crossResults: make(map[string][]ProbeResult),
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

type IPFSClient struct {
	mu         sync.RWMutex
	gateways   []string
	localCache map[string][]byte
}

func NewIPFSClient() *IPFSClient {
	return &IPFSClient{
		gateways:   []string{"https://ipfs.io", "https://dweb.link"},
		localCache: make(map[string][]byte),
	}
}

func (c *IPFSClient) StoreJSON(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	cid := "Qm" + fmt.Sprintf("%x", h[:])
	c.mu.Lock()
	c.localCache[cid] = data
	c.mu.Unlock()
	return cid, nil
}
