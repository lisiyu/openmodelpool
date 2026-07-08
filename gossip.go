package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"crypto/rand"
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

var gossip *GossipManager

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
		FromNode:        node.NodeID(),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		TrustPoolVersion: pool.Version,
		ScoreDigest:      g.computeScoreDigest(),
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
		if n.NodeID == myID || n.Endpoint == "" {
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

// exchange sends a signed GossipMessage to a peer's /federation/gossip endpoint
// and returns the peer's response message.
func (g *GossipManager) exchange(peer NodeInfo, msg GossipMessage) (*GossipMessage, error) {
	url := fmt.Sprintf("%s/federation/gossip", peer.Endpoint)

	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal gossip message: %w", err)
	}

	client := GetSharedHTTPClient()
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("POST to peer gossip: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("peer returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read gossip response: %w", err)
	}

	var respMsg GossipMessage
	if err := json.Unmarshal(respBody, &respMsg); err != nil {
		return nil, fmt.Errorf("parse gossip response: %w", err)
	}

	return &respMsg, nil
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

// fetchFullPoolFromPeer retrieves the complete trust pool from a peer.
func (g *GossipManager) fetchFullPoolFromPeer(peer NodeInfo) {
	url := fmt.Sprintf("%s/federation/pool", peer.Endpoint)

	client := GetSharedHTTPClient()
	resp, err := client.Get(url)
	if err != nil {
		slog.Debug("failed to fetch full pool from peer",
			"peer_id", peer.NodeID, "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Debug("peer returned non-200 for pool fetch",
			"peer_id", peer.NodeID, "status", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var pool TrustPool
	if err := json.Unmarshal(body, &pool); err != nil {
		slog.Debug("failed to parse pool from peer",
			"peer_id", peer.NodeID, "error", err)
		return
	}

	fed.UpdateTrustPool(pool)
	slog.Info("fetched full trust pool from peer",
		"peer_id", peer.NodeID, "version", pool.Version)
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
	if err := readJSON(r, &msg); err != nil {
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
		FromNode:        node.NodeID(),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		TrustPoolVersion: fed.GetTrustPool().Version,
		ScoreDigest:      digest,
	}
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
	if err := readJSON(r, &ann); err != nil {
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
// asynchronously. The announcement is signed before broadcasting.
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
		if peer.NodeID == node.NodeID() || peer.Endpoint == "" {
			continue
		}

		wg.Add(1)
		go func(p NodeInfo) {
			defer wg.Done()

			url := fmt.Sprintf("%s/federation/announce", p.Endpoint)
			resp, err := client.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				slog.Debug("failed to broadcast announcement to peer",
					"peer_id", p.NodeID, "error", err)
				return
			}
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				slog.Debug("peer rejected announcement",
					"peer_id", p.NodeID, "status", resp.StatusCode)
				return
			}

			slog.Debug("announcement delivered to peer", "peer_id", p.NodeID)
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
		j := int(uint64(buf[0])<<56|uint64(buf[1])<<48|uint64(buf[2])<<40|uint64(buf[3])<<32|
			uint64(buf[4])<<24|uint64(buf[5])<<16|uint64(buf[6])<<8|uint64(buf[7])) % (i + 1)
		nodes[i], nodes[j] = nodes[j], nodes[i]
	}
}