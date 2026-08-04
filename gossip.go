package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

// GossipManager handles peer-to-peer state synchronization via the gossip protocol.
// It maintains a dedup cache of seen message hashes and drives periodic sync rounds.
type GossipManager struct {
	mu     sync.RWMutex
	seen   map[string]time.Time // message hash -> first seen time (for dedup)
	stopCh chan struct{}
}

// initGossip creates the GossipManager and starts the gossip and cleanup loops.
// Should be called after initFederation.
func initGossip() {
	if fed == nil || !fed.IsEnabled() {
		slog.Info("gossip not started (federation disabled)")
		return
	}

	g := &GossipManager{
		seen:   make(map[string]time.Time),
		stopCh: make(chan struct{}),
	}
	gossip = g

	go g.gossipLoop()
	go g.cleanupLoop()

	slog.Info("gossip manager initialized and running")
}

// gossipLoop runs the periodic gossip round. Every gossip_interval_s (default 30s),
// it picks 3 random active peers and exchanges sync messages.
func (g *GossipManager) gossipLoop() {
	intervalSecs := cfg.Get("gossip_interval_s", "30")
	interval := parseDurationSecs(intervalSecs, 30)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("gossip loop started", "interval_s", interval.Seconds())

	for {
		select {
		case <-ticker.C:
			g.doGossipRound()
		case <-g.stopCh:
			slog.Info("gossip loop exiting")
			return
		}
	}
}

// doGossipRound performs a single round of gossip: build a sync message,
// send it to selected peers, and process their responses.
func (g *GossipManager) doGossipRound() {
	peers := g.selectPeers(3)
	if len(peers) == 0 {
		slog.Debug("no peers available for gossip round")
		return
	}

	// Build our sync message
	pool := fed.GetTrustPool()
	msg := GossipMessage{
		Type:             "sync",
		FromNode:         node.NodeID(),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		TrustPoolVersion: pool.Version,
		ScoreDigest:      g.computeScoreDigest(),
	}
	// P1-1: attach PEX endpoint hints so receivers learn peer addresses even
	// when trust-pool endpoints are missing. Must be set BEFORE signing.
	msg.KnownPeers = buildKnownPeers()

	if contributionLedger != nil {
		ledgerPayload := struct {
			Contributions []*ContributionRecord `json:"contributions,omitempty"`
			Claims        []*CapabilityClaim     `json:"claims,omitempty"`
		}{
			Contributions: contributionLedger.GetAllContributions(),
			Claims:        contributionLedger.GetAllClaims(),
		}
		if payloadBytes, err := json.Marshal(ledgerPayload); err == nil {
			msg.Payload = payloadBytes
		}
	}

	msg.Signature = node.SignJSON(msg)

	for _, peer := range peers {
		resp, err := g.exchange(peer, msg)
		if err != nil {
			slog.Debug("gossip exchange failed",
				"peer_id", peer.NodeID, "error", err)
			continue
		}
		if resp != nil {
			g.processGossipResponse(resp, peer)
		}
	}
}

// selectPeers picks up to count random active peers, preferring seed nodes.
// Excludes this node itself.
func (g *GossipManager) selectPeers(count int) []NodeInfo {
	allActive := fed.GetActiveNodes()
	if len(allActive) == 0 {
		return nil
	}

	myID := node.NodeID()
	var seeds, regular []NodeInfo

	for _, n := range allActive {
		if n.NodeID == myID || (n.Endpoint == "" && len(n.Addresses) == 0) {
			continue
		}
		if n.SeedNode {
			seeds = append(seeds, n)
		} else {
			regular = append(regular, n)
		}
	}

	// Shuffle both groups using crypto/rand (Fisher-Yates with secure randomness)
	cryptoShuffle(seeds)
	cryptoShuffle(regular)

	// Prefer seeds, then fill with regular nodes
	result := make([]NodeInfo, 0, count)
	for _, n := range seeds {
		if len(result) >= count {
			break
		}
		result = append(result, n)
	}
	for _, n := range regular {
		if len(result) >= count {
			break
		}
		result = append(result, n)
	}

	return result
}

// peerEndpoints returns the list of endpoints to try for a peer,
// preferring Addresses (multi-address) over the legacy single Endpoint.
func peerEndpoints(peer NodeInfo) []string {
	if len(peer.Addresses) > 0 {
		return peer.Addresses
	}
	if peer.Endpoint != "" {
		return []string{peer.Endpoint}
	}
	return nil
}

// knownNodeAddresses returns the best address list for a federation node,
// preferring the multi-address field then the legacy single endpoint.
func knownNodeAddresses(n NodeInfo) []string {
	if len(n.Addresses) > 0 {
		return n.Addresses
	}
	if n.Endpoint != "" {
		return []string{n.Endpoint}
	}
	return nil
}

// buildKnownPeers returns the combined set of known peer endpoints (P1-1 PEX),
// merging the federation trust pool / gossip-learned nodes with the locally
// configured manual peers. It is used to populate GossipMessage.KnownPeers so
// receivers learn addresses even when a node's trust-pool endpoint is missing.
// De-duplicated by NodeID; the local node is excluded.
func buildKnownPeers() []PeerHint {
	seen := make(map[string]bool)
	var hints []PeerHint
	add := func(nodeID string, addrs []string) {
		if nodeID == "" || seen[nodeID] || len(addrs) == 0 {
			return
		}
		if node != nil && nodeID == node.NodeID() {
			return // never advertise ourselves
		}
		seen[nodeID] = true
		hints = append(hints, PeerHint{NodeID: nodeID, Addresses: addrs})
	}
	if fed != nil {
		for _, n := range fed.GetActiveNodes() {
			add(n.NodeID, knownNodeAddresses(n))
		}
	}
	if netMgr != nil {
		for _, p := range netMgr.GetPeers() {
			add(p.NodeID, p.Addresses)
		}
	}
	return hints
}

// exchange sends a signed GossipMessage to a peer, trying all available addresses.
// Returns the peer's response message on first successful attempt.
func (g *GossipManager) exchange(peer NodeInfo, msg GossipMessage) (*GossipMessage, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal gossip message: %w", err)
	}

	client := GetSharedHTTPClient()
	endpoints := peerEndpoints(peer)
	var lastErr error

	for _, addr := range endpoints {
		// P1 fix: wrap loop body in anonymous function so defer runs per-iteration
		respMsg, done, err := func() (*GossipMessage, bool, error) {
			gossipURL := fmt.Sprintf("%s/api/federation/gossip", addr)
			gCtx, gCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer gCancel()
			req, err := http.NewRequestWithContext(gCtx, http.MethodPost, gossipURL, bytes.NewReader(body))
			if err != nil {
				return nil, false, fmt.Errorf("build request for %s: %w", addr, err)
			}
			req.Header.Set("Content-Type", "application/json")
			// R5: identify ourselves via X-Node-ID so the receiver's
			// withFederationAuth path-1 (X-Node-ID in trust pool) can admit us
			// (we are bridged into the peer's trust pool by P0-2 on first contact).
			if node != nil {
				req.Header.Set("X-Node-ID", node.NodeID())
			}
			resp, err := client.Do(req)
			if err != nil {
				return nil, false, fmt.Errorf("POST to %s: %w", addr, err)
			}

			if resp.StatusCode != http.StatusOK {
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return nil, false, fmt.Errorf("peer returned HTTP %d from %s: %s", resp.StatusCode, addr, string(respBody))
			}

			respBody, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, false, fmt.Errorf("read response from %s: %w", addr, err)
			}

			var respMsg GossipMessage
			if err := json.Unmarshal(respBody, &respMsg); err != nil {
				return nil, false, fmt.Errorf("parse response from %s: %w", addr, err)
			}

			return &respMsg, true, nil
		}()
		if done {
			return respMsg, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("all addresses failed for peer %s: %v", peer.NodeID, lastErr)
}

// isSeen checks if a message hash has been seen before. If not, it marks it
// as seen with the current timestamp and returns false. Returns true if duplicate.
func (g *GossipManager) isSeen(hash string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.seen[hash]; exists {
		return true
	}
	g.seen[hash] = time.Now()
	return false
}

// cleanup removes entries older than 1 hour from the seen map.
func (g *GossipManager) cleanup() {
	g.mu.Lock()
	defer g.mu.Unlock()

	cutoff := time.Now().Add(-1 * time.Hour)
	removed := 0
	for hash, seenAt := range g.seen {
		if seenAt.Before(cutoff) {
			delete(g.seen, hash)
			removed++
		}
	}

	if removed > 0 {
		slog.Debug("gossip dedup cleanup", "removed", removed, "remaining", len(g.seen))
	}
}

// cleanupLoop periodically runs cleanup every 10 minutes.
func (g *GossipManager) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			g.cleanup()
		case <-g.stopCh:
			return
		}
	}
}

// computeScoreDigest produces a SHA-256 digest of all known node reputations,
// sorted by NodeID for deterministic comparison.
func (g *GossipManager) computeScoreDigest() string {
	pool := fed.GetTrustPool()

	h := sha256.New()
	ids := make([]string, 0, len(pool.Nodes))
	for _, n := range pool.Nodes {
		ids = append(ids, n.NodeID)
	}
	sort.Strings(ids)

	for _, id := range ids {
		score := 0
		if repMgr != nil {
			if rep := repMgr.GetReputation(id); rep != nil {
				score = int(rep.OverallScore)
			}
		}
		fmt.Fprintf(h, "%s:%d;", id, score)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// processGossipResponse handles a sync response received from a peer.
func (g *GossipManager) processGossipResponse(msg *GossipMessage, peer NodeInfo) {
	if msg == nil {
		return
	}

	// Dedup check
	hash := messageHash(msg)
	if g.isSeen(hash) {
		return
	}

	// Update the peer's last-seen timestamp in our local state
	peer.LastSeen = time.Now().UTC().Format(time.RFC3339)
	fed.UpdateNodeInfo(peer)

	// P1-1: merge PEX address hints so receivers can later reach nodes whose
	// trust-pool endpoint is missing (address-reachability fallback).
	if len(msg.KnownPeers) > 0 && fed != nil {
		fed.MergePeerHints(msg.KnownPeers)
	}

	if len(msg.Payload) > 0 && contributionLedger != nil {
		var ledgerPayload struct {
			Contributions []*ContributionRecord `json:"contributions,omitempty"`
			Claims        []*CapabilityClaim     `json:"claims,omitempty"`
		}
		if err := json.Unmarshal(msg.Payload, &ledgerPayload); err == nil {
			added := contributionLedger.GossipSync(ledgerPayload.Contributions, nil, ledgerPayload.Claims, nil)
			if added > 0 {
				slog.Info("gossip: merged ledger records from peer", "peer_id", peer.NodeID, "added", added)
				saveContributionLedger()
			}
		}
	}

	// If peer reports a newer trust pool version, fetch the full pool
	ourPool := fed.GetTrustPool()
	if msg.TrustPoolVersion > ourPool.Version {
		slog.Info("peer has newer trust pool, fetching",
			"peer_id", peer.NodeID,
			"peer_version", msg.TrustPoolVersion,
			"our_version", ourPool.Version)
		g.fetchFullPoolFromPeer(peer)
	}
}

// fetchFullPoolFromPeer retrieves the complete trust pool from a peer,
// trying all available addresses until one succeeds.
func (g *GossipManager) fetchFullPoolFromPeer(peer NodeInfo) {
	client := GetSharedHTTPClient()
	endpoints := peerEndpoints(peer)

	for _, addr := range endpoints {
		// P1 fix: wrap loop body in anonymous function so defer runs per-iteration
		done := func() bool {
			poolURL := fmt.Sprintf("%s/api/federation/pool", addr)
			g2Ctx, g2Cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer g2Cancel()
			req, err := http.NewRequestWithContext(g2Ctx, http.MethodGet, poolURL, nil)
			if err != nil {
				slog.Debug("failed to build pool request",
					"peer_id", peer.NodeID, "addr", addr, "error", err)
				return false
			}
			// R5: identify ourselves via X-Node-ID (see exchange for rationale).
			if node != nil {
				req.Header.Set("X-Node-ID", node.NodeID())
			}
			resp, err := client.Do(req)
			if err != nil {
				slog.Debug("failed to fetch pool from peer address",
					"peer_id", peer.NodeID, "addr", addr, "error", err)
				return false
			}

			if resp.StatusCode != http.StatusOK {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				return false
			}

			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return false
			}

			var pool TrustPool
			if err := json.Unmarshal(body, &pool); err != nil {
				slog.Debug("failed to parse pool from peer",
					"peer_id", peer.NodeID, "addr", addr, "error", err)
				return false
			}

			fed.UpdateTrustPool(pool)
			slog.Info("fetched full trust pool from peer",
				"peer_id", peer.NodeID, "version", pool.Version, "addr", addr)
			return true
		}()
		if done {
			return
		}
	}

	slog.Debug("failed to fetch pool from all peer addresses",
		"peer_id", peer.NodeID)
}

// messageHash computes a SHA-256 hash of a GossipMessage for dedup purposes.
func messageHash(msg *GossipMessage) string {
	data, err := json.Marshal(msg)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// HTTP Handlers
// ---------------------------------------------------------------------------

// handleFederationGossip is the HTTP handler for POST /federation/gossip.
// It verifies the sender's signature, processes the sync message, and responds
// with our own sync state.
func handleFederationGossip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if fed == nil || !fed.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "federation is not enabled")
		return
	}

	// Parse incoming message
	var msg GossipMessage
	if err := readJSON(w, r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid gossip message")
		return
	}

	// Look up sender's public key from our trust pool or local peers
	sender, ok := fed.GetNode(msg.FromNode)
	if !ok {
		slog.Warn("gossip from unknown node", "from", msg.FromNode)
		writeError(w, http.StatusForbidden, "unknown sender node")
		return
	}

	// Verify the message signature
	if !VerifyJSONSig(sender.PubKey, msg, msg.Signature) {
		slog.Warn("gossip signature verification failed",
			"from", msg.FromNode, "type", msg.Type)
		writeError(w, http.StatusForbidden, "invalid signature")
		return
	}

	// Dedup check
	hash := messageHash(&msg)
	if gossip != nil && gossip.isSeen(hash) {
		slog.Debug("duplicate gossip message received",
			"from", msg.FromNode, "hash", hash[:12])
		// Still respond with our state — peer may need our info
	}

	// Process the message based on type
	switch msg.Type {
	case "sync":
		// Update the sender's last-seen time
		sender.LastSeen = time.Now().UTC().Format(time.RFC3339)
		fed.UpdateNodeInfo(*sender)

		// If sender has a newer pool version, note it for the response
		ourPool := fed.GetTrustPool()
		if msg.TrustPoolVersion > ourPool.Version {
			slog.Info("gossip peer has newer trust pool",
				"peer_id", msg.FromNode,
				"peer_version", msg.TrustPoolVersion,
				"our_version", ourPool.Version)
		}

	case "announce":
		if len(msg.Payload) > 0 {
			var ann ProviderAnnouncement
			if err := json.Unmarshal(msg.Payload, &ann); err == nil {
				slog.Info("gossip contains provider announcement",
					"from", msg.FromNode,
					"provider", ann.ProviderID)
			}
		}

	default:
		slog.Debug("unknown gossip type", "type", msg.Type, "from", msg.FromNode)
	}

	// Build our response sync message
	digest := ""
	if gossip != nil {
		digest = gossip.computeScoreDigest()
	}

	resp := GossipMessage{
		Type:             "sync",
		FromNode:         node.NodeID(),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		TrustPoolVersion: fed.GetTrustPool().Version,
		ScoreDigest:      digest,
	}
	// P1-1: echo our PEX endpoint hints (set before signing).
	resp.KnownPeers = buildKnownPeers()
	resp.Signature = node.SignJSON(resp)

	writeJSON(w, http.StatusOK, resp)
}

// handleFederationAnnounce is the HTTP handler for POST /federation/announce.
// It processes provider announcements from other nodes, verifying the signature
// and updating the announcing node's shared provider list.
func handleFederationAnnounce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if fed == nil || !fed.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "federation is not enabled")
		return
	}

	// Parse the announcement
	var ann ProviderAnnouncement
	if err := readJSON(w, r, &ann); err != nil {
		writeError(w, http.StatusBadRequest, "invalid announcement")
		return
	}

	// Look up the announcing node
	sender, ok := fed.GetNode(ann.NodeID)
	if !ok {
		slog.Warn("announcement from unknown node", "node_id", ann.NodeID)
		writeError(w, http.StatusForbidden, "unknown announcing node")
		return
	}

	// Verify signature
	if !VerifyJSONSig(sender.PubKey, ann, ann.Signature) {
		slog.Warn("announcement signature verification failed",
			"node_id", ann.NodeID)
		writeError(w, http.StatusForbidden, "invalid signature")
		return
	}

	// Update the sender's shared providers in our local state
	updated := *sender
	// Add/update the announced provider in the node's shared providers list
	found := false
	for i, sp := range updated.SharedProviders {
		if sp.ProviderID == ann.ProviderID {
			updated.SharedProviders[i] = SharedProvider{
				ProviderID: ann.ProviderID,
				Platform:   ann.Platform,
				Models:     ann.Models,
				Capacity:   ann.Capacity,
			}
			found = true
			break
		}
	}
	if !found {
		updated.SharedProviders = append(updated.SharedProviders, SharedProvider{
			ProviderID: ann.ProviderID,
			Platform:   ann.Platform,
			Models:     ann.Models,
			Capacity:   ann.Capacity,
		})
	}
	updated.LastSeen = time.Now().UTC().Format(time.RFC3339)
	fed.UpdateNodeInfo(updated)

	slog.Info("processed provider announcement",
		"from", ann.NodeID,
		"provider", ann.ProviderID,
		"models", len(ann.Models))

	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// broadcastAnnouncement sends a ProviderAnnouncement to all known active peers
// asynchronously. Tries all available addresses per peer. The announcement is signed before broadcasting.
func (g *GossipManager) broadcastAnnouncement(ann ProviderAnnouncement) {
	peers := fed.GetActiveNodes()
	if len(peers) == 0 {
		slog.Debug("no peers to broadcast announcement to")
		return
	}

	// Sign the announcement with our node identity
	ann.NodeID = node.NodeID()
	ann.Timestamp = time.Now().UTC().Format(time.RFC3339)
	ann.Signature = node.SignJSON(ann)

	body, err := json.Marshal(ann)
	if err != nil {
		slog.Error("failed to marshal announcement for broadcast", "error", err)
		return
	}

	var wg sync.WaitGroup
	client := GetSharedHTTPClient()

	for _, peer := range peers {
		if peer.NodeID == node.NodeID() || len(peerEndpoints(peer)) == 0 {
			continue
		}

		wg.Add(1)
		go func(p NodeInfo) {
			defer wg.Done()

			endpoints := peerEndpoints(p)
			for _, addr := range endpoints {
				// P1 fix: wrap loop body so defer runs per-iteration
				delivered := func() bool {
					announceURL := fmt.Sprintf("%s/api/federation/announce", addr)
					aCtx, aCancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer aCancel()
					req, err := http.NewRequestWithContext(aCtx, http.MethodPost, announceURL, bytes.NewReader(body))
					if err != nil {
						return false
					}
					req.Header.Set("Content-Type", "application/json")
					// R5: identify ourselves via X-Node-ID so the receiver's
					// withFederationAuth admits us (we are in its trust pool via P0-2).
					if node != nil {
						req.Header.Set("X-Node-ID", node.NodeID())
					}
					resp, err := client.Do(req)
					if err != nil {
						return false
					}
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()

					if resp.StatusCode != http.StatusOK {
						return false
					}

					slog.Debug("announcement delivered to peer", "peer_id", p.NodeID, "addr", addr)
					return true
				}()
				if delivered {
					return
				}
			}
			slog.Debug("failed to deliver announcement to peer on all addresses",
				"peer_id", p.NodeID)
		}(peer)
	}

	wg.Wait()
	slog.Info("announcement broadcast complete", "peers_targeted", len(peers)-1)
}

// stop halts the gossip manager's background loops.
func (g *GossipManager) stop() {
	select {
	case <-g.stopCh:
		// already closed
	default:
		close(g.stopCh)
		slog.Info("gossip manager stopped")
	}
}

// cryptoShuffle performs a Fisher-Yates shuffle using crypto/rand for secure randomness.
func cryptoShuffle(nodes []NodeInfo) {
	n := len(nodes)
	for i := n - 1; i > 0; i-- {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			break // fallback: leave remaining elements in place
		}
		val := uint64(buf[0])<<56 | uint64(buf[1])<<48 | uint64(buf[2])<<40 | uint64(buf[3])<<32 |
			uint64(buf[4])<<24 | uint64(buf[5])<<16 | uint64(buf[6])<<8 | uint64(buf[7])
		j := int(val % uint64(i+1))
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}
}
