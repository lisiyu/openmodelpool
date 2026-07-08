package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ============================================================
// Phase 3: Decentralized Algorithm Storage & Consensus
// ============================================================
//
// Algorithm parameters are stored in a blockchain-like chain.
// Each block contains the full parameter set, a hash of the
// previous block, an Ed25519 signature, and the proposer node ID.
//
// Changes to parameters require a 2/3 supermajority vote from
// all active nodes. Each proposal has a 24-hour voting window.

// AlgorithmParams holds the tunable parameters for the network.
type AlgorithmParams struct {
	OpenKeyRatio              float64 `json:"open_key_ratio"`                // fraction of total network resources reserved for open keys (default 0.3)
	TrustWeight               float64 `json:"trust_weight"`                  // weight of reputation in user quota calculation
	ContribWeight             float64 `json:"contrib_weight"`                // weight of contribution in user quota calculation
	TrialQuotaMultiplier      float64 `json:"trial_quota_multiplier"`        // multiplier applied to trial quota
	UnlockThresholdCoef       float64 `json:"unlock_threshold_coef"`         // coefficient for unlock threshold calculation

	// Phase 4: Global Pool Parameters
	GlobalPoolAvailabilityRatio float64 `json:"global_pool_availability_ratio"` // fraction of pool available for use (default 0.8)
	GlobalPoolRoutingLoadWeight float64 `json:"global_pool_routing_load_weight"` // weight of load in routing score (default 0.3)
	GlobalPoolRoutingRepWeight  float64 `json:"global_pool_routing_rep_weight"`  // weight of reputation in routing score (default 0.3)
	GlobalKeyMinContribution    int64   `json:"global_key_min_contribution"`    // minimum contribution to sign global keys
}

// DefaultAlgorithmParams returns the genesis parameter set.
func DefaultAlgorithmParams() AlgorithmParams {
	return AlgorithmParams{
		OpenKeyRatio:                  0.3,
		TrustWeight:                   0.4,
		ContribWeight:                 0.6,
		TrialQuotaMultiplier:          2.0,
		UnlockThresholdCoef:           0.3,
		GlobalPoolAvailabilityRatio:   0.8,
		GlobalPoolRoutingLoadWeight:   0.3,
		GlobalPoolRoutingRepWeight:    0.3,
		GlobalKeyMinContribution:      5000,
	}
}

// AlgorithmBlock is one link in the algorithm parameter chain.
type AlgorithmBlock struct {
	Version      int64           `json:"version"`
	Timestamp    time.Time       `json:"timestamp"`
	Parameters   AlgorithmParams `json:"parameters"`
	PreviousHash string          `json:"previous_hash"`
	CurrentHash  string          `json:"current_hash"`
	Signature    string          `json:"signature"`  // Ed25519 base64
	UpdatedBy    string          `json:"updated_by"` // NodeID of proposer
}

// AlgorithmProposal represents a pending parameter change vote.
type AlgorithmProposal struct {
	ID         string          `json:"id"`
	Proposer   string          `json:"proposer"` // NodeID
	Parameters AlgorithmParams `json:"parameters"`
	CreatedAt  time.Time       `json:"created_at"`
	Votes      map[string]bool `json:"votes"` // NodeID -> agree
	Status     string          `json:"status"` // pending | approved | rejected
	ExpiresAt  time.Time       `json:"expires_at"`
}

// AlgorithmChain is the in-memory chain of algorithm parameter blocks.
type AlgorithmChain struct {
	mu        sync.RWMutex
	blocks    []AlgorithmBlock
	proposals map[string]*AlgorithmProposal
	dataPath  string
}

var algoChain *AlgorithmChain

// ============================================================
// Initialization
// ============================================================

func initAlgorithmChain(dataDir string) {
	algoChain = &AlgorithmChain{
		proposals: make(map[string]*AlgorithmProposal),
		dataPath:  filepath.Join(dataDir, "algorithm_chain.json"),
	}
	algoChain.load()

	if len(algoChain.blocks) == 0 {
		// Create genesis block
		genesis := AlgorithmBlock{
			Version:      0,
			Timestamp:    time.Now().UTC(),
			Parameters:   DefaultAlgorithmParams(),
			PreviousHash: "0000000000000000000000000000000000000000000000000000000000000000",
			UpdatedBy:    "genesis",
		}
		genesis.CurrentHash = calculateBlockHash(genesis)
		algoChain.blocks = append(algoChain.blocks, genesis)
		algoChain.save()
		slog.Info("algorithm chain genesis block created")
	} else {
		slog.Info("algorithm chain loaded", "blocks", len(algoChain.blocks))
	}

	// Start proposal expiry checker
	go algoChain.proposalExpiryLoop()
}

// ============================================================
// Persistence
// ============================================================

type algorithmChainStore struct {
	Blocks    []AlgorithmBlock              `json:"blocks"`
	Proposals map[string]*AlgorithmProposal `json:"proposals"`
}

func (c *AlgorithmChain) load() {
	b, err := os.ReadFile(c.dataPath)
	if err != nil {
		return
	}
	var store algorithmChainStore
	if err := json.Unmarshal(b, &store); err != nil {
		slog.Error("failed to parse algorithm chain", "error", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if store.Blocks != nil {
		c.blocks = store.Blocks
	}
	if store.Proposals != nil {
		c.proposals = store.Proposals
	}
}

func (c *AlgorithmChain) save() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.doSave()
}

func (c *AlgorithmChain) doSave() {
	store := algorithmChainStore{
		Blocks:    c.blocks,
		Proposals: c.proposals,
	}
	b, _ := json.MarshalIndent(store, "", "  ")
	os.MkdirAll(filepath.Dir(c.dataPath), 0755)
	os.WriteFile(c.dataPath, b, 0600)
}

// ============================================================
// Hash & Signature
// ============================================================

// calculateBlockHash produces a SHA-256 hash of the block content
// (excluding CurrentHash and Signature fields to avoid circularity).
func calculateBlockHash(block AlgorithmBlock) string {
	data := fmt.Sprintf("%d|%s|%v|%s|%s",
		block.Version,
		block.Timestamp.UTC().Format(time.RFC3339Nano),
		block.Parameters,
		block.PreviousHash,
		block.UpdatedBy,
	)
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

// signBlock signs the block with this node's Ed25519 private key.
func signBlock(block AlgorithmBlock) (string, error) {
	if node == nil || !node.IsInitialized() {
		return "", fmt.Errorf("node identity not initialized")
	}
	payload := []byte(block.CurrentHash + "|" + block.PreviousHash + "|" + fmt.Sprintf("%d", block.Version))
	return node.Sign(payload), nil
}

// verifyBlockSignature checks the Ed25519 signature on a block.
func verifyBlockSignature(block AlgorithmBlock) bool {
	if block.UpdatedBy == "genesis" {
		return true
	}
	proposerPubKey := getPublicKeyForNode(block.UpdatedBy)
	if proposerPubKey == nil {
		return false
	}
	payload := []byte(block.CurrentHash + "|" + block.PreviousHash + "|" + fmt.Sprintf("%d", block.Version))
	sigBytes, err := base64.StdEncoding.DecodeString(block.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(proposerPubKey, payload, sigBytes)
}

// ============================================================
// Chain Operations
// ============================================================

// AddBlock validates and appends a new block to the chain.
func (c *AlgorithmChain) AddBlock(params AlgorithmParams, updatedBy string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.blocks) == 0 {
		return fmt.Errorf("chain is empty, no genesis block")
	}

	previousBlock := c.blocks[len(c.blocks)-1]

	newBlock := AlgorithmBlock{
		Version:      previousBlock.Version + 1,
		Timestamp:    time.Now().UTC(),
		Parameters:   params,
		PreviousHash: previousBlock.CurrentHash,
		UpdatedBy:    updatedBy,
	}

	// Compute hash
	newBlock.CurrentHash = calculateBlockHash(newBlock)

	// Sign if this node is the proposer
	selfNodeID := ""
	if node != nil && node.IsInitialized() {
		selfNodeID = node.NodeID()
	}
	if updatedBy == selfNodeID {
		sig, err := signBlock(newBlock)
		if err != nil {
			return fmt.Errorf("sign block: %w", err)
		}
		newBlock.Signature = sig
	}

	c.blocks = append(c.blocks, newBlock)
	c.doSaveLocked()

	slog.Info("algorithm chain: new block added",
		"version", newBlock.Version,
		"updated_by", updatedBy,
		"hash", newBlock.CurrentHash[:min(len(newBlock.CurrentHash), 16)]+"...",
	)

	return nil
}

// GetCurrentParams returns the latest algorithm parameters.
func (c *AlgorithmChain) GetCurrentParams() AlgorithmParams {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.blocks) == 0 {
		return DefaultAlgorithmParams()
	}
	return c.blocks[len(c.blocks)-1].Parameters
}

// GetCurrentBlock returns the latest block info.
func (c *AlgorithmChain) GetCurrentBlock() AlgorithmBlock {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.blocks) == 0 {
		return AlgorithmBlock{}
	}
	return c.blocks[len(c.blocks)-1]
}

// GetHistory returns all blocks in the chain.
func (c *AlgorithmChain) GetHistory() []AlgorithmBlock {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]AlgorithmBlock, len(c.blocks))
	copy(result, c.blocks)
	return result
}

// ValidateChain checks the integrity of the entire chain.
func (c *AlgorithmChain) ValidateChain() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var errors []string
	for i := 1; i < len(c.blocks); i++ {
		block := c.blocks[i]
		prev := c.blocks[i-1]

		// Check previous hash link
		if block.PreviousHash != prev.CurrentHash {
			errors = append(errors, fmt.Sprintf("block %d: broken hash link", block.Version))
		}

		// Check hash integrity
		expectedHash := calculateBlockHash(block)
		if block.CurrentHash != expectedHash {
			errors = append(errors, fmt.Sprintf("block %d: hash mismatch", block.Version))
		}
	}
	return errors
}

// ============================================================
// Consensus: Proposals & Voting
// ============================================================

const proposalVotingDuration = 24 * time.Hour

// generateProposalID creates a unique proposal ID.
func generateProposalID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "prop_" + hex.EncodeToString(b)
}

// ProposeChange creates a new algorithm parameter change proposal.
func (c *AlgorithmChain) ProposeChange(proposer string, params AlgorithmParams) (*AlgorithmProposal, error) {
	// Validate parameters
	if params.OpenKeyRatio < 0 || params.OpenKeyRatio > 1 {
		return nil, fmt.Errorf("open_key_ratio must be between 0 and 1")
	}
	if params.TrustWeight < 0 || params.TrustWeight > 1 {
		return nil, fmt.Errorf("trust_weight must be between 0 and 1")
	}
	if params.ContribWeight < 0 || params.ContribWeight > 1 {
		return nil, fmt.Errorf("contrib_weight must be between 0 and 1")
	}
	if params.TrialQuotaMultiplier < 0 {
		return nil, fmt.Errorf("trial_quota_multiplier must be >= 0")
	}
	if params.UnlockThresholdCoef < 0 || params.UnlockThresholdCoef > 1 {
		return nil, fmt.Errorf("unlock_threshold_coef must be between 0 and 1")
	}
	// Phase 4: Global pool parameter validation
	if params.GlobalPoolAvailabilityRatio < 0 || params.GlobalPoolAvailabilityRatio > 1 {
		return nil, fmt.Errorf("global_pool_availability_ratio must be between 0 and 1")
	}
	if params.GlobalPoolRoutingLoadWeight < 0 || params.GlobalPoolRoutingLoadWeight > 1 {
		return nil, fmt.Errorf("global_pool_routing_load_weight must be between 0 and 1")
	}
	if params.GlobalPoolRoutingRepWeight < 0 || params.GlobalPoolRoutingRepWeight > 1 {
		return nil, fmt.Errorf("global_pool_routing_rep_weight must be between 0 and 1")
	}
	if params.GlobalKeyMinContribution < 0 {
		return nil, fmt.Errorf("global_key_min_contribution must be >= 0")
	}

	proposal := &AlgorithmProposal{
		ID:         generateProposalID(),
		Proposer:   proposer,
		Parameters: params,
		CreatedAt:  time.Now().UTC(),
		Votes:      make(map[string]bool),
		Status:     "pending",
		ExpiresAt:  time.Now().UTC().Add(proposalVotingDuration),
	}

	c.mu.Lock()
	c.proposals[proposal.ID] = proposal
	c.doSaveLocked()
	c.mu.Unlock()

	slog.Info("algorithm proposal created",
		"id", proposal.ID,
		"proposer", proposer,
	)

	// Broadcast to peers (fire and forget)
	go broadcastProposalToPeers(proposal)

	return proposal, nil
}

// Vote records a vote on a proposal and checks if it has reached quorum.
func (c *AlgorithmChain) Vote(proposalID, voterNodeID string, agree bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	proposal, ok := c.proposals[proposalID]
	if !ok {
		return fmt.Errorf("proposal not found: %s", proposalID)
	}
	if proposal.Status != "pending" {
		return fmt.Errorf("proposal is not pending (status: %s)", proposal.Status)
	}
	if time.Now().UTC().After(proposal.ExpiresAt) {
		return fmt.Errorf("proposal has expired")
	}

	// Don't allow voting twice
	if _, already := proposal.Votes[voterNodeID]; already {
		return fmt.Errorf("already voted")
	}

	proposal.Votes[voterNodeID] = agree

	// Count votes
	agreeCount := 0
	totalNodes := c.countActiveNodesLocked()

	for _, v := range proposal.Votes {
		if v {
			agreeCount++
		}
	}

	requiredVotes := (totalNodes*2/3) + 1
	if totalNodes <= 1 {
		requiredVotes = 1 // single-node network: auto-approve
	}

	slog.Info("algorithm vote recorded",
		"proposal", proposalID,
		"voter", voterNodeID,
		"agree", agree,
		"agree_count", agreeCount,
		"required", requiredVotes,
	)

	// Check if quorum reached
	if agreeCount >= requiredVotes {
		proposal.Status = "approved"
		params := proposal.Parameters
		proposer := proposal.Proposer

		// Add to chain (we already hold c.mu, so call internal method)
		if len(c.blocks) > 0 {
			previousBlock := c.blocks[len(c.blocks)-1]
			newBlock := AlgorithmBlock{
				Version:      previousBlock.Version + 1,
				Timestamp:    time.Now().UTC(),
				Parameters:   params,
				PreviousHash: previousBlock.CurrentHash,
				UpdatedBy:    proposer,
			}
			newBlock.CurrentHash = calculateBlockHash(newBlock)

			// Sign if this node is the proposer
			selfNodeID := ""
			if node != nil && node.IsInitialized() {
				selfNodeID = node.NodeID()
			}
			if proposer == selfNodeID {
				payload := []byte(newBlock.CurrentHash + "|" + newBlock.PreviousHash + "|" + fmt.Sprintf("%d", newBlock.Version))
				if node != nil && node.IsInitialized() {
					newBlock.Signature = node.Sign(payload)
				}
			}

			c.blocks = append(c.blocks, newBlock)
			slog.Info("algorithm proposal approved and applied",
				"id", proposalID,
				"agree_count", agreeCount,
				"version", newBlock.Version,
			)
		}
	}

	// Check if rejection is inevitable
	disagreeCount := 0
	for _, v := range proposal.Votes {
		if !v {
			disagreeCount++
		}
	}
	maxPossibleAgree := agreeCount + (totalNodes - len(proposal.Votes))
	if maxPossibleAgree < requiredVotes && proposal.Status == "pending" {
		proposal.Status = "rejected"
		slog.Info("algorithm proposal rejected (impossible to pass)", "id", proposalID)
	}

	c.doSaveLocked()
	return nil
}

// GetProposal returns a specific proposal.
func (c *AlgorithmChain) GetProposal(id string) *AlgorithmProposal {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.proposals[id]
	if !ok {
		return nil
	}
	cp := *p
	return &cp
}

// GetAllProposals returns all proposals.
func (c *AlgorithmChain) GetAllProposals() []*AlgorithmProposal {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]*AlgorithmProposal, 0, len(c.proposals))
	for _, p := range c.proposals {
		cp := *p
		result = append(result, &cp)
	}
	return result
}

// GetPendingProposals returns only pending proposals.
func (c *AlgorithmChain) GetPendingProposals() []*AlgorithmProposal {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var result []*AlgorithmProposal
	for _, p := range c.proposals {
		if p.Status == "pending" {
			cp := *p
			result = append(result, &cp)
		}
	}
	return result
}

// countActiveNodesLocked counts active nodes (caller must hold c.mu).
func (c *AlgorithmChain) countActiveNodesLocked() int {
	if netMgr == nil {
		return 1
	}
	netMgr.mu.RLock()
	defer netMgr.mu.RUnlock()
	count := 1 // self
	for _, peer := range netMgr.config.Peers {
		if peer.Status == "online" {
			count++
		}
	}
	return count
}

// proposalExpiryLoop periodically checks for expired proposals.
func (c *AlgorithmChain) proposalExpiryLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		changed := false
		for id, p := range c.proposals {
			if p.Status == "pending" && time.Now().UTC().After(p.ExpiresAt) {
				agreeCount := 0
				for _, v := range p.Votes {
					if v {
						agreeCount++
					}
				}
				totalNodes := c.countActiveNodesLocked()
				requiredVotes := (totalNodes*2/3) + 1
				if totalNodes <= 1 {
					requiredVotes = 1
				}

				if agreeCount >= requiredVotes && len(c.blocks) > 0 {
					p.Status = "approved"
					params := p.Parameters
					proposer := p.Proposer

					previousBlock := c.blocks[len(c.blocks)-1]
					newBlock := AlgorithmBlock{
						Version:      previousBlock.Version + 1,
						Timestamp:    time.Now().UTC(),
						Parameters:   params,
						PreviousHash: previousBlock.CurrentHash,
						UpdatedBy:    proposer,
					}
					newBlock.CurrentHash = calculateBlockHash(newBlock)

					selfNodeID := ""
					if node != nil && node.IsInitialized() {
						selfNodeID = node.NodeID()
					}
					if proposer == selfNodeID && node != nil {
						payload := []byte(newBlock.CurrentHash + "|" + newBlock.PreviousHash + "|" + fmt.Sprintf("%d", newBlock.Version))
						newBlock.Signature = node.Sign(payload)
					}

					c.blocks = append(c.blocks, newBlock)
				} else {
					p.Status = "rejected"
				}
				slog.Info("proposal expired and resolved", "id", id, "status", p.Status)
				changed = true
			}
		}
		if changed {
			c.doSaveLocked()
		}
		c.mu.Unlock()
	}
}

// doSaveLocked saves chain data (caller must hold c.mu for reading).
func (c *AlgorithmChain) doSaveLocked() {
	store := algorithmChainStore{
		Blocks:    c.blocks,
		Proposals: c.proposals,
	}
	b, _ := json.MarshalIndent(store, "", "  ")
	os.MkdirAll(filepath.Dir(c.dataPath), 0755)
	os.WriteFile(c.dataPath, b, 0600)
}

// ============================================================
// Proposal Broadcasting
// ============================================================

// broadcastProposalToPeers sends a proposal to all known peers.
func broadcastProposalToPeers(proposal *AlgorithmProposal) {
	if netMgr == nil || !netMgr.IsSharedMode() {
		return
	}

	netMgr.mu.RLock()
	peers := make([]PeerInfo, len(netMgr.config.Peers))
	copy(peers, netMgr.config.Peers)
	netMgr.mu.RUnlock()

	payload, err := json.Marshal(proposal)
	if err != nil {
		slog.Error("failed to marshal proposal for broadcast", "error", err)
		return
	}

	client := &httpClient10
	for _, peer := range peers {
		if len(peer.Addresses) == 0 {
			continue
		}
		go func(peer PeerInfo) {
			for _, addr := range peer.Addresses {
				addr = strings.TrimRight(addr, "/")
				resp, err := client.Post(addr+"/api/network/algorithm/gossip", "application/json", bytes.NewReader(payload))
				if err != nil {
					continue
				}
				resp.Body.Close()
				if resp.StatusCode == 200 {
					break
				}
			}
		}(peer)
	}
}

// getPublicKeyForNode retrieves the Ed25519 public key for a node by its NodeID.
func getPublicKeyForNode(nodeID string) ed25519.PublicKey {
	// Check self
	if node != nil && node.IsInitialized() {
		selfP2P := DeriveP2PNodeID()
		if nodeID == selfP2P {
			node.mu.RLock()
			pub := node.pubKey
			node.mu.RUnlock()
			return pub
		}
	}

	// Check federation trust pool
	if fed != nil {
		pool := fed.GetTrustPool()
		for _, n := range pool.Nodes {
			if n.NodeID == nodeID && n.PubKey != "" {
				pubBytes, err := base64.StdEncoding.DecodeString(n.PubKey)
				if err == nil && len(pubBytes) == ed25519.PublicKeySize {
					return ed25519.PublicKey(pubBytes)
				}
			}
		}
	}

	return nil
}

// ============================================================
// API Handlers
// ============================================================

// GET /api/network/algorithm/current
func handleAlgorithmCurrent(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeError(w, 500, "algorithm chain not initialized")
		return
	}

	block := algoChain.GetCurrentBlock()
	writeJSON(w, 200, map[string]any{
		"version":    block.Version,
		"timestamp":  block.Timestamp.Format(time.RFC3339),
		"parameters": block.Parameters,
		"hash":       block.CurrentHash,
		"updated_by": block.UpdatedBy,
	})
}

// GET /api/network/algorithm/history
func handleAlgorithmHistory(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeError(w, 500, "algorithm chain not initialized")
		return
	}

	blocks := algoChain.GetHistory()
	validationErrors := algoChain.ValidateChain()

	writeJSON(w, 200, map[string]any{
		"blocks":       blocks,
		"total":        len(blocks),
		"valid":        len(validationErrors) == 0,
		"chain_errors": validationErrors,
	})
}

// POST /api/network/algorithm/propose
func handleAlgorithmPropose(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeError(w, 500, "algorithm chain not initialized")
		return
	}

	var body struct {
		Parameters AlgorithmParams `json:"parameters"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	proposer := ""
	if node != nil && node.IsInitialized() {
		proposer = node.NodeID()
	}
	if netMgr != nil && proposer == "" {
		proposer = netMgr.GetNodeID()
	}
	if proposer == "" {
		writeError(w, 400, "node identity not available")
		return
	}

	proposal, err := algoChain.ProposeChange(proposer, body.Parameters)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"proposal_id": proposal.ID,
		"status":      proposal.Status,
		"expires_at":  proposal.ExpiresAt.Format(time.RFC3339),
	})
}

// POST /api/network/algorithm/vote
func handleAlgorithmVote(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeError(w, 500, "algorithm chain not initialized")
		return
	}

	var body struct {
		ProposalID string `json:"proposal_id"`
		Agree      bool   `json:"agree"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.ProposalID == "" {
		writeError(w, 400, "proposal_id is required")
		return
	}

	voter := ""
	if node != nil && node.IsInitialized() {
		voter = node.NodeID()
	}
	if netMgr != nil && voter == "" {
		voter = netMgr.GetNodeID()
	}
	if voter == "" {
		writeError(w, 400, "node identity not available")
		return
	}

	if err := algoChain.Vote(body.ProposalID, voter, body.Agree); err != nil {
		writeError(w, 400, err.Error())
		return
	}

	// Return updated vote status
	proposal := algoChain.GetProposal(body.ProposalID)
	if proposal == nil {
		writeJSON(w, 200, map[string]any{"status": "voted"})
		return
	}

	agreeCount := 0
	for _, v := range proposal.Votes {
		if v {
			agreeCount++
		}
	}
	totalNodes := algoChain.countActiveNodesLocked()
	requiredVotes := (totalNodes*2/3) + 1
	if totalNodes <= 1 {
		requiredVotes = 1
	}

	writeJSON(w, 200, map[string]any{
		"status":          "voted",
		"current_votes":   agreeCount,
		"required_votes":  requiredVotes,
		"proposal_status": proposal.Status,
	})
}

// POST /api/network/algorithm/gossip — receive proposal from peer (no auth)
func handleAlgorithmGossip(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeError(w, 500, "algorithm chain not initialized")
		return
	}

	var proposal AlgorithmProposal
	if err := readJSON(r, &proposal); err != nil {
		writeError(w, 400, "invalid proposal")
		return
	}

	algoChain.mu.Lock()
	if _, exists := algoChain.proposals[proposal.ID]; !exists {
		algoChain.proposals[proposal.ID] = &proposal
		algoChain.doSaveLocked()
		slog.Info("received algorithm proposal via gossip", "id", proposal.ID, "proposer", proposal.Proposer)
	}
	algoChain.mu.Unlock()

	writeJSON(w, 200, map[string]string{"status": "received"})
}

// GET /api/network/algorithm/proposals — list all proposals
func handleAlgorithmProposals(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeError(w, 500, "algorithm chain not initialized")
		return
	}

	pendingOnly := r.URL.Query().Get("pending") == "true"
	var proposals []*AlgorithmProposal
	if pendingOnly {
		proposals = algoChain.GetPendingProposals()
	} else {
		proposals = algoChain.GetAllProposals()
	}
	if proposals == nil {
		proposals = []*AlgorithmProposal{}
	}

	writeJSON(w, 200, map[string]any{
		"proposals": proposals,
		"total":     len(proposals),
	})
}

// GET /api/network/algorithm/validate — validate chain integrity
func handleAlgorithmValidate(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeError(w, 500, "algorithm chain not initialized")
		return
	}

	errors := algoChain.ValidateChain()
	blocks := algoChain.GetHistory()
	writeJSON(w, 200, map[string]any{
		"valid":  len(errors) == 0,
		"errors": errors,
		"blocks": len(blocks),
	})
}
