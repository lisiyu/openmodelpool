package main

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ===========================================================================
// G3: Algorithm governance / voting (node-local, real persistence)
// ===========================================================================
//
// This module turns the previously-stubbed /api/network/algorithm/* governance
// endpoints into a working, persisted node-local governance store. A proposal
// has a real lifecycle (open → passed / rejected / closed), votes are recorded
// and de-duplicated per voter, and the proposals/history endpoints return real
// data that survives restarts.
//
// SCOPE (intentional): this is NODE-LOCAL governance. Cross-federation voting
// (broadcasting proposals/votes to peers and aggregating them into a consensus)
// is explicitly deferred — it would require a distributed consensus protocol
// that is out of scope for this iteration. Every response carries
// governance_scope:"local" and a note so callers/clients understand the
// boundary. See the handoff report for rationale.

// ProposalStatus enumerates the lifecycle states of a governance proposal.
type ProposalStatus string

const (
	// ProposalStatusOpen means the proposal is accepting votes.
	ProposalStatusOpen ProposalStatus = "open"
	// ProposalStatusPassed means the proposal was approved.
	ProposalStatusPassed ProposalStatus = "passed"
	// ProposalStatusRejected means the proposal was denied.
	ProposalStatusRejected ProposalStatus = "rejected"
	// ProposalStatusClosed means the proposal was closed without resolution.
	ProposalStatusClosed ProposalStatus = "closed"
)

// isTerminal reports whether the status is a final, non-votable state.
func (s ProposalStatus) isTerminal() bool {
	switch s {
	case ProposalStatusPassed, ProposalStatusRejected, ProposalStatusClosed:
		return true
	}
	return false
}

// VoteChoice is a voter's decision on a proposal.
type VoteChoice string

const (
	VoteYes     VoteChoice = "yes"
	VoteNo      VoteChoice = "no"
	VoteAbstain VoteChoice = "abstain"
)

// isValid reports whether the choice is one of the supported values.
func (c VoteChoice) isValid() bool {
	switch c {
	case VoteYes, VoteNo, VoteAbstain:
		return true
	}
	return false
}

// AlgorithmVote is a single recorded vote on a proposal.
type AlgorithmVote struct {
	Voter     string `json:"voter"` // voter identity (node id or user id)
	VoterName string `json:"voter_name,omitempty"`
	Choice    string `json:"choice"` // "yes" | "no" | "abstain"
	Comment   string `json:"comment,omitempty"`
	VotedAt   string `json:"voted_at"` // RFC3339
}

// ProposalTally is the computed vote counts for a proposal.
type ProposalTally struct {
	Yes     int `json:"yes"`
	No      int `json:"no"`
	Abstain int `json:"abstain"`
	Total   int `json:"total"`
}

// AlgorithmProposal is a governance proposal to change algorithm parameters.
type AlgorithmProposal struct {
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Description   string         `json:"description"`
	Proposer      string         `json:"proposer"` // node id or user id
	ProposerName  string         `json:"proposer_name,omitempty"`
	Status        ProposalStatus `json:"status"`
	CreatedAt     string         `json:"created_at"`
	ClosedAt      string         `json:"closed_at,omitempty"`
	ResolvedBy    string         `json:"resolved_by,omitempty"`
	ResolveReason string         `json:"resolve_reason,omitempty"`
	// Target optionally carries the proposed algorithm parameter changes. It is
	// stored for record-keeping; applying it to the live AlgorithmChain is out
	// of scope for G3 (kept stable — quota/balance engines are not perturbed).
	Target *AlgorithmParams `json:"target,omitempty"`
	Votes  []AlgorithmVote  `json:"votes"`
}

// GovernanceEvent is one entry in the governance timeline (history).
type GovernanceEvent struct {
	Type       string `json:"type"` // "proposal_created" | "vote_cast" | "proposal_resolved"
	ProposalID string `json:"proposal_id"`
	Actor      string `json:"actor"`
	ActorName  string `json:"actor_name,omitempty"`
	Detail     string `json:"detail,omitempty"`
	At         string `json:"at"` // RFC3339
}

// GovernanceScope is reported on every governance response so clients know
// whether votes are global (federated) or local to this node.
const GovernanceScope = "local"

// GovernanceScopeNote documents the deferred cross-federation work.
const GovernanceScopeNote = "本地治理，跨联邦待扩展"

// governanceSnapshot is the on-disk persistence shape.
type governanceSnapshot struct {
	Proposals []AlgorithmProposal `json:"proposals"`
	History   []GovernanceEvent   `json:"history"`
}

// AlgorithmGovernor owns the node-local algorithm governance state.
type AlgorithmGovernor struct {
	mu        sync.RWMutex
	proposals map[string]*AlgorithmProposal
	history   []GovernanceEvent
	dataDir   string
}

const algorithmGovernanceFile = "algorithm_proposals.json"

// initAlgorithmGovernance creates the global governor, restores any persisted
// state from dataDir, and is safe to call once during startup. It does not
// depend on node/federation identity, so it runs in initCore().
func initAlgorithmGovernance(dataDir string) {
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		history:   nil,
		dataDir:   dataDir,
	}
	governor = g
	g.Load()
	slog.Info("algorithm governance initialized (node-local)", "data_dir", dataDir, "scope", GovernanceScope)
}

// Load restores proposals + history from the integrity-protected snapshot.
func (g *AlgorithmGovernor) Load() {
	path := filepath.Join(g.dataDir, algorithmGovernanceFile)
	var snap governanceSnapshot
	if err := loadWithIntegrity(path, &snap); err != nil {
		// Not yet created (or tampered) — start fresh.
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.proposals = make(map[string]*AlgorithmProposal, len(snap.Proposals))
	for i := range snap.Proposals {
		p := snap.Proposals[i]
		g.proposals[p.ID] = &p
	}
	g.history = snap.History
}

// persistLocked writes the snapshot. Caller must hold g.mu.
func (g *AlgorithmGovernor) persistLocked() {
	snap := governanceSnapshot{
		Proposals: make([]AlgorithmProposal, 0, len(g.proposals)),
		History:   g.history,
	}
	for _, p := range g.proposals {
		snap.Proposals = append(snap.Proposals, *p)
	}
	path := filepath.Join(g.dataDir, algorithmGovernanceFile)
	if err := saveWithIntegrity(path, snap); err != nil {
		slog.Error("failed to persist algorithm governance", "error", err)
	}
}

// newProposalID returns a collision-resistant local proposal id.
func newProposalID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		slog.Error("crypto/rand.Read failed in newProposalID", "err", err) // B10: log instead of ignore
	}
	return fmt.Sprintf("prop-%d-%x", time.Now().UnixNano(), b)
}

// nowRFC3339 returns the current UTC time in RFC3339.
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// resolveActor returns a display identity for the acting principal, defaulting
// to the local node identity when available, else "admin".
func resolveActor(id, name string) (string, string) {
	if id == "" {
		if node != nil && node.IsInitialized() {
			id = node.NodeID()
		} else {
			id = "admin"
		}
	}
	if name == "" {
		if node != nil && node.IsInitialized() {
			if info := node.GetInfo(); info.GitHubUser != "" {
				name = info.GitHubUser
			}
		}
		if name == "" {
			name = localEnvName()
		}
	}
	return id, name
}

// CreateProposal records a new open proposal and returns it.
func (g *AlgorithmGovernor) CreateProposal(title, description, proposer, proposerName string, target *AlgorithmParams) (*AlgorithmProposal, error) {
	title = trimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	proposer, proposerName = resolveActor(proposer, proposerName)

	g.mu.Lock()
	defer g.mu.Unlock()

	p := &AlgorithmProposal{
		ID:           newProposalID(),
		Title:        title,
		Description:  trimSpace(description),
		Proposer:     proposer,
		ProposerName: proposerName,
		Status:       ProposalStatusOpen,
		CreatedAt:    nowRFC3339(),
		Target:       target,
		Votes:        nil,
	}
	g.proposals[p.ID] = p
	g.appendHistoryLocked(GovernanceEvent{
		Type:       "proposal_created",
		ProposalID: p.ID,
		Actor:      proposer,
		ActorName:  proposerName,
		Detail:     title,
		At:         p.CreatedAt,
	})
	g.persistLocked()
	return p, nil
}

// CastVote records (or replaces) a voter's choice on a proposal.
// Votes are de-duplicated by voter: a repeat vote from the same voter updates
// their existing choice rather than adding a second ballot.
func (g *AlgorithmGovernor) CastVote(proposalID, voter, voterName, choice, comment string) (*AlgorithmProposal, error) {
	choice = trimSpace(choice)
	if !VoteChoice(choice).isValid() {
		return nil, fmt.Errorf("choice must be one of yes/no/abstain")
	}
	voter, voterName = resolveActor(voter, voterName)

	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.proposals[proposalID]
	if !ok {
		return nil, fmt.Errorf("proposal not found: %s", proposalID)
	}
	if p.Status.isTerminal() {
		return nil, fmt.Errorf("proposal %s is %s and no longer accepts votes", proposalID, p.Status)
	}

	// De-duplicate by voter: replace any prior vote from this voter.
	for i := range p.Votes {
		if p.Votes[i].Voter == voter {
			p.Votes[i] = AlgorithmVote{
				Voter:     voter,
				VoterName: voterName,
				Choice:    choice,
				Comment:   trimSpace(comment),
				VotedAt:   nowRFC3339(),
			}
			g.appendHistoryLocked(GovernanceEvent{
				Type:       "vote_cast",
				ProposalID: p.ID,
				Actor:      voter,
				ActorName:  voterName,
				Detail:     string(choice),
				At:         p.Votes[i].VotedAt,
			})
			g.persistLocked()
			return p, nil
		}
	}

	// New voter.
	v := AlgorithmVote{
		Voter:     voter,
		VoterName: voterName,
		Choice:    choice,
		Comment:   trimSpace(comment),
		VotedAt:   nowRFC3339(),
	}
	p.Votes = append(p.Votes, v)
	g.appendHistoryLocked(GovernanceEvent{
		Type:       "vote_cast",
		ProposalID: p.ID,
		Actor:      voter,
		ActorName:  voterName,
		Detail:     string(choice),
		At:         v.VotedAt,
	})
	g.persistLocked()
	return p, nil
}

// ResolveProposal finalizes an open proposal with one of the terminal states
// (passed / rejected / closed). It is idempotent against already-resolved
// proposals (returns the current proposal without error).
func (g *AlgorithmGovernor) ResolveProposal(proposalID, resolver, reason string, status ProposalStatus) (*AlgorithmProposal, error) {
	if !status.isTerminal() {
		return nil, fmt.Errorf("decision must be one of passed/rejected/closed")
	}
	resolver, _ = resolveActor(resolver, "")

	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.proposals[proposalID]
	if !ok {
		return nil, fmt.Errorf("proposal not found: %s", proposalID)
	}
	if p.Status.isTerminal() {
		return p, nil
	}
	now := nowRFC3339()
	p.Status = status
	p.ClosedAt = now
	p.ResolvedBy = resolver
	p.ResolveReason = trimSpace(reason)
	g.appendHistoryLocked(GovernanceEvent{
		Type:       "proposal_resolved",
		ProposalID: p.ID,
		Actor:      resolver,
		Detail:     string(status),
		At:         now,
	})
	g.persistLocked()
	return p, nil
}

// appendHistoryLocked appends an event to the in-memory history. Caller holds g.mu.
// History is kept most-recent-last; the handlers return it reversed (newest first).
func (g *AlgorithmGovernor) appendHistoryLocked(e GovernanceEvent) {
	g.history = append(g.history, e)
}

// GetProposal returns the proposal with the given id.
func (g *AlgorithmGovernor) GetProposal(id string) (*AlgorithmProposal, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p, ok := g.proposals[id]
	return p, ok
}

// ListProposals returns proposals ordered newest-first. When statusFilter is
// non-empty it is matched exactly (case-sensitive against ProposalStatus values).
func (g *AlgorithmGovernor) ListProposals(statusFilter string) []*AlgorithmProposal {
	g.mu.RLock()
	defer g.mu.RUnlock()

	out := make([]*AlgorithmProposal, 0, len(g.proposals))
	for _, p := range g.proposals {
		if statusFilter != "" && string(p.Status) != statusFilter {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

// GetHistory returns the governance timeline ordered newest-first.
func (g *AlgorithmGovernor) GetHistory() []GovernanceEvent {
	g.mu.RLock()
	defer g.mu.RUnlock()

	out := make([]GovernanceEvent, len(g.history))
	copy(out, g.history)
	// Newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Tally computes the vote counts for a proposal.
func (p *AlgorithmProposal) Tally() ProposalTally {
	t := ProposalTally{}
	for _, v := range p.Votes {
		switch VoteChoice(v.Choice) {
		case VoteYes:
			t.Yes++
		case VoteNo:
			t.No++
		case VoteAbstain:
			t.Abstain++
		}
		t.Total++
	}
	return t
}

// proposalView is the API view of a proposal: the proposal plus its computed tally.
type proposalView struct {
	*AlgorithmProposal
	Tally ProposalTally `json:"tally"`
}

// toProposalView wraps a proposal with its computed tally for serialization.
func toProposalView(p *AlgorithmProposal) proposalView {
	return proposalView{AlgorithmProposal: p, Tally: p.Tally()}
}

// trimSpace is a tiny local helper to avoid importing strings everywhere.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
