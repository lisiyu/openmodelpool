package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
)

// ============================================================
// Community co-governance (P2-1) — lightweight, trust-through-audit
//
// Governance philosophy (2026-08-09, decided by Lei Gong):
//   - Assume good intent by default. The system ONLY guards against
//`    malicious abuse (e.g. proposal spam); it does NOT punish, slash, or
//     score-trust participants for merely not contributing.
//   - "Contributors govern": eligible voters are nodes that have contributed
//     compute to the commons. This keeps governance in the hands of those
//     who actually run capacity.
//   - Proposals (e.g. admit a node, allowlist a model, change a parameter)
//     are recorded on an append-only, hash-chained ledger; ratifications are
//     likewise chained so the whole history is tamper-evident. A proposal
//     passes by supermajority of eligible voters. No penalties are ever
//     applied for dissent.
// ============================================================

// Co-governance proposal types.
const (
	GovTypeAdmitNode  = "admit_node"
	GovTypeAllowModel = "allow_model"
	GovTypeParam      = "param_change"
)

// Max open proposals per proposer — the only abuse guard (bounds spam).
const govMaxOpenPerProposer = 5

// GovernanceProposal is a single co-governance proposal on the ledger.
// Status: "open" → "ratified" | "rejected".
type GovernanceProposal struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Payload   json.RawMessage `json:"payload"`
	Proposer  string          `json:"proposer"`
	CreatedAt time.Time       `json:"created_at"`
	Status    string          `json:"status"`
	Seq       int64           `json:"seq"`
	PrevHash  string          `json:"prev_hash"`
	Hash      string          `json:"hash"`
}

// GovernanceRatification is one node's approval/rejection of a proposal.
// Chained so the ratification trail is tamper-evident.
type GovernanceRatification struct {
	ProposalID string    `json:"proposal_id"`
	NodeID     string    `json:"node_id"`
	Approve    bool      `json:"approve"`
	Timestamp  time.Time `json:"timestamp"`
	Seq        int64     `json:"seq"`
	PrevHash   string    `json:"prev_hash"`
	Hash       string    `json:"hash"`
}

// VoterSource returns the eligible voter node IDs. In production this is the
// set of nodes that have contributed compute to the commons.
type VoterSource func() []string

// GovernanceLedger is an append-only, hash-chained co-governance log.
type GovernanceLedger struct {
	mu            sync.RWMutex
	selfID        string
	voters        VoterSource
	proposalList  []*GovernanceProposal
	proposalIndex map[string]*GovernanceProposal
	ratifications []GovernanceRatification
	ratIndex      map[string]map[string]bool // proposalID -> nodeID -> voted
	openByProp    map[string]int             // proposer -> # of open proposals
	proposalSeq   int64
	lastPHash     string
	lastRHash     string
	dataPath      string
}

// NewGovernanceLedger creates a ledger. voters may be nil (falls back to the
// local node only, so a single-node commons can still self-ratify).
func NewGovernanceLedger(selfID string, voters VoterSource, dataPath string) *GovernanceLedger {
	g := &GovernanceLedger{
		selfID:        selfID,
		voters:        voters,
		proposalIndex: make(map[string]*GovernanceProposal),
		ratIndex:      make(map[string]map[string]bool),
		openByProp:    make(map[string]int),
		dataPath:      dataPath,
	}
	g.load()
	return g
}

// Propose records a new co-governance proposal. It bounds spam by limiting
// how many OPEN proposals a single proposer may have at once.
func (g *GovernanceLedger) Propose(proposer, ptype, title string, payload json.RawMessage) (*GovernanceProposal, error) {
	if proposer == "" {
		proposer = g.selfID
	}
	if ptype == "" || title == "" {
		return nil, fmt.Errorf("type and title are required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.openByProp[proposer] >= govMaxOpenPerProposer {
		return nil, fmt.Errorf("proposer has too many open proposals (spam guard)")
	}

	g.proposalSeq++
	p := &GovernanceProposal{
		ID:        fmt.Sprintf("gov-%s-%d", g.selfID, g.proposalSeq),
		Type:      ptype,
		Title:     title,
		Payload:   payload,
		Proposer:  proposer,
		CreatedAt: time.Now(),
		Status:    "open",
		Seq:       g.proposalSeq,
		PrevHash:  g.lastPHash,
	}
	p.Hash = g.hashProposal(p)
	g.lastPHash = p.Hash

	g.proposalList = append(g.proposalList, p)
	g.proposalIndex[p.ID] = p
	g.openByProp[proposer]++
	g.save()
	return p, nil
}

// Ratify records a node's approval/rejection of a proposal. A node may
// ratify a given proposal only once. No penalty for dissent.
func (g *GovernanceLedger) Ratify(proposalID, nodeID string, approve bool) (*GovernanceRatification, error) {
	if nodeID == "" {
		nodeID = g.selfID
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.proposalIndex[proposalID]
	if !ok {
		return nil, fmt.Errorf("proposal not found")
	}
	if p.Status != "open" {
		return nil, fmt.Errorf("proposal is not open (status=%s)", p.Status)
	}
	if g.ratIndex[proposalID] == nil {
		g.ratIndex[proposalID] = make(map[string]bool)
	}
	if g.ratIndex[proposalID][nodeID] {
		return nil, fmt.Errorf("node already ratified this proposal")
	}

	r := &GovernanceRatification{
		ProposalID: proposalID,
		NodeID:     nodeID,
		Approve:    approve,
		Timestamp:  time.Now(),
		Seq:        int64(len(g.ratifications) + 1),
		PrevHash:   g.lastRHash,
	}
	r.Hash = g.hashRatification(r)
	g.lastRHash = r.Hash

	g.ratifications = append(g.ratifications, *r)
	g.ratIndex[proposalID][nodeID] = true

	// Auto-close once a supermajority is reached either way.
	g.recompute(p)
	g.save()
	return r, nil
}

// recompute counts approvals/rejections for p and closes it when a
// supermajority of eligible voters has decided.
func (g *GovernanceLedger) recompute(p *GovernanceProposal) {
	eligible := g.eligibleCount()
	if eligible == 0 {
		eligible = 1 // lone node can self-ratify its commons
	}
	need := supermajority(eligible)
	approve, reject := 0, 0
	for _, r := range g.ratifications {
		if r.ProposalID != p.ID {
			continue
		}
		if r.Approve {
			approve++
		} else {
			reject++
		}
	}
	if approve >= need {
		p.Status = "ratified"
		g.openByProp[p.Proposer]--
		if g.openByProp[p.Proposer] < 0 {
			g.openByProp[p.Proposer] = 0
		}
	} else if reject >= need {
		p.Status = "rejected"
		g.openByProp[p.Proposer]--
		if g.openByProp[p.Proposer] < 0 {
			g.openByProp[p.Proposer] = 0
		}
	}
}

// Tally returns the current standing of a proposal.
func (g *GovernanceLedger) Tally(proposalID string) (found, open bool, approved, eligible, need int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.proposalIndex[proposalID]
	if !ok {
		return false, false, 0, 0, 0
	}
	eligible = g.eligibleCount()
	if eligible == 0 {
		eligible = 1
	}
	need = supermajority(eligible)
	approve, reject := 0, 0
	for _, r := range g.ratifications {
		if r.ProposalID != proposalID {
			continue
		}
		if r.Approve {
			approve++
		} else {
			reject++
		}
	}
	_ = reject
	return true, p.Status == "open", approve, eligible, need
}

// List returns proposals, optionally filtered by status ("open"/"ratified"/...).
// Empty filter returns all.
func (g *GovernanceLedger) List(statusFilter string) []*GovernanceProposal {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]*GovernanceProposal, 0, len(g.proposalList))
	for _, p := range g.proposalList {
		if statusFilter == "" || p.Status == statusFilter {
			out = append(out, p)
		}
	}
	return out
}

// Get returns a single proposal by ID.
func (g *GovernanceLedger) Get(proposalID string) (*GovernanceProposal, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.proposalIndex[proposalID]
	return p, ok
}

// VerifyChain re-validates the hash chains of proposals and ratifications.
func (g *GovernanceLedger) VerifyChain() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	prev := ""
	for _, p := range g.proposalList {
		if p.PrevHash != prev {
			return false
		}
		if p.Hash != g.hashProposal(p) {
			return false
		}
		prev = p.Hash
	}
	prev = ""
	for i := range g.ratifications {
		r := &g.ratifications[i]
		if r.PrevHash != prev {
			return false
		}
		if r.Hash != g.hashRatification(r) {
			return false
		}
		prev = r.Hash
	}
	return true
}

func (g *GovernanceLedger) eligibleCount() int {
	if g.voters == nil {
		return 0
	}
	return len(g.voters())
}

func (g *GovernanceLedger) hashProposal(p *GovernanceProposal) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%d", p.ID, p.Type, p.Title, string(p.Payload), p.Proposer, p.CreatedAt.Unix())))
	return fmt.Sprintf("%x", sum[:])
}

func (g *GovernanceLedger) hashRatification(r *GovernanceRatification) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%t|%d", r.ProposalID, r.NodeID, r.Approve, r.Timestamp.Unix())))
	return fmt.Sprintf("%x", sum[:])
}

// supermajority returns the number of votes needed to pass/reject among n
// eligible voters: ceil(2/3 n), with a floor of 1.
func supermajority(n int) int {
	if n <= 0 {
		return 1
	}
	return int(math.Ceil(float64(n) * 2.0 / 3.0))
}

// ---- persistence (best-effort; never fatal) ----

func (g *GovernanceLedger) save() {
	if g.dataPath == "" {
		return
	}
	g.mu.RLock()
	snap := struct {
		Proposals     []*GovernanceProposal  `json:"proposals"`
		Ratifications []byte                 `json:"-"`
		Rats          []GovernanceRatification `json:"ratifications"`
		LastPHash     string                 `json:"last_proposal_hash"`
		LastRHash     string                 `json:"last_ratification_hash"`
		Seq           int64                  `json:"seq"`
	}{
		Proposals:     g.proposalList,
		Rats:          g.ratifications,
		LastPHash:     g.lastPHash,
		LastRHash:     g.lastRHash,
		Seq:           g.proposalSeq,
	}
	g.mu.RUnlock()
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(g.dataPath, b, 0o600)
}

func (g *GovernanceLedger) load() {
	if g.dataPath == "" {
		return
	}
	b, err := os.ReadFile(g.dataPath)
	if err != nil {
		return
	}
	var snap struct {
		Proposals     []*GovernanceProposal    `json:"proposals"`
		Ratifications []GovernanceRatification `json:"ratifications"`
		LastPHash     string                   `json:"last_proposal_hash"`
		LastRHash     string                   `json:"last_ratification_hash"`
		Seq           int64                    `json:"seq"`
	}
	if err := json.Unmarshal(b, &snap); err != nil {
		return
	}
	g.proposalList = snap.Proposals
	g.ratifications = snap.Ratifications
	g.lastPHash = snap.LastPHash
	g.lastRHash = snap.LastRHash
	g.proposalSeq = snap.Seq
	g.proposalIndex = make(map[string]*GovernanceProposal)
	g.ratIndex = make(map[string]map[string]bool)
	g.openByProp = make(map[string]int)
	for _, p := range g.proposalList {
		g.proposalIndex[p.ID] = p
		if p.Status == "open" {
			g.openByProp[p.Proposer]++
		}
	}
	for _, r := range g.ratifications {
		if g.ratIndex[r.ProposalID] == nil {
			g.ratIndex[r.ProposalID] = make(map[string]bool)
		}
		g.ratIndex[r.ProposalID][r.NodeID] = true
	}
}

// governanceLedger is the process-wide co-governance ledger (P2-1).
var governanceLedger *GovernanceLedger

// contributorsVoterSource returns the distinct node IDs that have contributed
// compute — these are the eligible voters ("contributors govern").
func contributorsVoterSource() []string {
	if contributionLedger == nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, c := range contributionLedger.GetAllContributions() {
		if c.PeerID != "" && !seen[c.PeerID] {
			seen[c.PeerID] = true
			out = append(out, c.PeerID)
		}
	}
	return out
}

// ---- HTTP handlers (P2-1) ----

func handleGovernancePropose(w http.ResponseWriter, r *http.Request) {
	if governanceLedger == nil {
		writeError(w, 503, "governance not initialized")
		return
	}
	var body struct {
		Type    string          `json:"type"`
		Title   string          `json:"title"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	proposer := r.Header.Get("X-Node-ID")
	if proposer == "" {
		proposer = selfNodeID()
	}
	p, err := governanceLedger.Propose(proposer, body.Type, body.Title, body.Payload)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func handleGovernanceRatify(w http.ResponseWriter, r *http.Request) {
	if governanceLedger == nil {
		writeError(w, 503, "governance not initialized")
		return
	}
	var body struct {
		ProposalID string `json:"proposal_id"`
		Approve    bool   `json:"approve"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid body")
		return
	}
	nodeID := r.Header.Get("X-Node-ID")
	if nodeID == "" {
		nodeID = selfNodeID()
	}
	rat, err := governanceLedger.Ratify(body.ProposalID, nodeID, body.Approve)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, rat)
}

func handleGovernanceProposals(w http.ResponseWriter, r *http.Request) {
	if governanceLedger == nil {
		writeError(w, 503, "governance not initialized")
		return
	}
	status := r.URL.Query().Get("status")
	list := governanceLedger.List(status)
	writeJSON(w, 200, map[string]any{
		"proposals":   list,
		"chain_valid": governanceLedger.VerifyChain(),
	})
}
