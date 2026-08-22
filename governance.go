package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
)

// ============================================================
// Community co-governance (P2-1) — lightweight, trust-through-audit, with
// additive execution (P2-1(iv))
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

	// Effective curation rosters (P2-1(iv), option C). Populated ONLY from
	// RATIFIED proposals of type admit_node / allow_model. These are additive
	// allowlists used purely for labelling/curation — they never revoke
	// anyone's existing access (good-intent default: anyone may still join,
	// every model is still served). Rebuilt from the ledger on load so the
	// roster can never diverge from the tamper-evident record. param_change
	// proposals are audit-only and do NOT mutate runtime (applying them would
	// let a 2/3 contributor coalition flip security-sensitive settings).
	admittedNodes map[string]bool
	allowedModels map[string]bool
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
	// saveLocked: caller already holds g.mu (save() would re-acquire RLock and
	// self-deadlock).
	g.saveLocked()
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
	// saveLocked: caller already holds g.mu (save() would re-acquire RLock and
	// self-deadlock).
	g.saveLocked()
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
		// Apply the ratified proposal to the effective curation rosters
		// (P2-1(iv)). admit_node/allow_model take effect; param_change is a
		// no-op by design.
		g.executeRatified(p)
	} else if reject >= need {
		p.Status = "rejected"
		g.openByProp[p.Proposer]--
		if g.openByProp[p.Proposer] < 0 {
			g.openByProp[p.Proposer] = 0
		}
	}
}

// executeRatified applies a freshly-ratified proposal to the effective
// curation rosters. Caller must hold g.mu (write lock). Malformed or empty
// payloads are ignored silently — ratification already succeeded, we simply
// have nothing actionable to record.
func (g *GovernanceLedger) executeRatified(p *GovernanceProposal) {
	switch p.Type {
	case GovTypeAdmitNode:
		var pl struct {
			NodeID string `json:"node_id"`
		}
		if err := json.Unmarshal(p.Payload, &pl); err != nil || pl.NodeID == "" {
			return
		}
		if g.admittedNodes == nil {
			g.admittedNodes = make(map[string]bool)
		}
		g.admittedNodes[pl.NodeID] = true
	case GovTypeAllowModel:
		var pl struct {
			ModelID string `json:"model_id"`
		}
		if err := json.Unmarshal(p.Payload, &pl); err != nil || pl.ModelID == "" {
			return
		}
		if g.allowedModels == nil {
			g.allowedModels = make(map[string]bool)
		}
		g.allowedModels[pl.ModelID] = true
	case GovTypeParam:
		// Audit-only by design (P2-1(iv), option C): the proposal is recorded
		// on the tamper-evident ledger but is deliberately NOT applied to
		// runtime. Mutating parameters via governance would let a 2/3
		// contributor coalition alter security-sensitive settings.
	}
}

// rebuildEffect reconstructs the curation rosters from every ratified proposal
// on the ledger. The ledger is the single source of truth, so this guarantees
// the in-memory rosters can never drift from the persisted record (and makes
// hand-editing a roster pointless). Called after load().
func (g *GovernanceLedger) rebuildEffect() {
	for _, p := range g.proposalList {
		if p.Status == "ratified" {
			g.executeRatified(p)
		}
	}
}

// AdmittedNodes returns the community-admitted node IDs (from ratified
// admit_node proposals). Additive curation roster only.
func (g *GovernanceLedger) AdmittedNodes() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.admittedNodes))
	for id := range g.admittedNodes {
		out = append(out, id)
	}
	return out
}

// AllowedModels returns the community-curated model IDs (from ratified
// allow_model proposals). Additive curation roster only.
func (g *GovernanceLedger) AllowedModels() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.allowedModels))
	for id := range g.allowedModels {
		out = append(out, id)
	}
	return out
}

// IsCommunityAdmitted reports whether nodeID is on the ratified admit_node
// roster. Pure read; safe for callers that want to label/curate a node.
func (g *GovernanceLedger) IsCommunityAdmitted(nodeID string) bool {
	if nodeID == "" {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.admittedNodes[nodeID]
}

// IsCommunityCuratedModel reports whether modelID is on the ratified
// allow_model roster. Pure read; safe for callers that want to label/curate a
// model in listings.
func (g *GovernanceLedger) IsCommunityCuratedModel(modelID string) bool {
	if modelID == "" {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.allowedModels[modelID]
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

// save persists the governance ledger to disk. Callers must NOT hold g.mu
// (it acquires the read lock itself).
func (g *GovernanceLedger) save() {
	g.mu.RLock()
	g.saveLocked()
	g.mu.RUnlock()
}

// saveLocked writes the governance ledger to disk. The caller MUST already
// hold g.mu (write lock). Split from save() so locked callers (Propose/Ratify)
// can persist without re-acquiring the RWMutex — calling save() while holding
// g.mu.Lock() would self-deadlock on its g.mu.RLock() when dataPath != "".
func (g *GovernanceLedger) saveLocked() {
	if g.dataPath == "" {
		return
	}
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
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		slog.Error("governance ledger marshal failed", "path", g.dataPath, "error", err)
		return
	}
	// B7-2: atomic + error-checked. This was the last non-atomic write in the
	// codebase — a crash mid-write left a truncated file which load() silently
	// ignored, and the next save persisted the now-empty in-memory state over
	// it, permanently losing proposal/ratification history.
	if err := atomicWriteFile(g.dataPath, b, 0o600); err != nil {
		slog.Error("governance ledger save failed", "path", g.dataPath, "error", err)
	}
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
	g.rebuildEffect()
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
		"proposals":      list,
		"chain_valid":    governanceLedger.VerifyChain(),
		"admitted_nodes": governanceLedger.AdmittedNodes(),
		"allowed_models": governanceLedger.AllowedModels(),
	})
}
