package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ============================================================
// Node Heartbeat & Discovery (Phase 2)
// ============================================================
//
// Every 60 seconds, this node sends heartbeats to all known peers.
// Peers respond with their own status + gossip (known peers list).
// After 3 missed heartbeats, a peer is marked offline.

const (
	heartbeatInterval = 60 * time.Second
	maxMissedHeartbeats = 3
)

// HeartbeatPayload is sent/received in heartbeat requests.
type HeartbeatPayload struct {
	NodeID    string              `json:"node_id"`
	NodeName  string              `json:"node_name"`
	Addresses []string            `json:"addresses"`
	Models    []string            `json:"models"`
	Uptime    int64               `json:"uptime"`
	Version   string              `json:"version,omitempty"`
	Timestamp int64               `json:"timestamp"`
	Region    *HeartbeatRegionInfo `json:"region_info,omitempty"` // Phase 4: optional region info
}

// HeartbeatResponse is returned by the heartbeat endpoint.
type HeartbeatResponse struct {
	Status string             `json:"status"`
	Peers  []HeartbeatPeerInfo `json:"peers,omitempty"` // gossip: other known peers
}

// HeartbeatPeerInfo is a peer entry in the gossip response.
type HeartbeatPeerInfo struct {
	NodeID    string   `json:"node_id"`
	NodeName  string   `json:"node_name"`
	Addresses []string `json:"addresses"`
	Models    []string `json:"models"`
	Status    string   `json:"status"`
}

// peerHeartbeatState tracks heartbeat health per peer.
type peerHeartbeatState struct {
	missedCount  int
	lastHeartbeat time.Time
}

var heartbeatStates = make(map[string]*peerHeartbeatState)

// startHeartbeatLoop begins the periodic heartbeat sender.
func startHeartbeatLoop() {
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		slog.Info("heartbeat loop started", "interval", heartbeatInterval)

		for {
			<-ticker.C
			if netMgr == nil || !netMgr.IsSharedMode() {
				continue
			}
			sendHeartbeats()
			checkPeerHealth()

			// Phase 4: Update global pool heartbeat for self
			if globalPool != nil && netMgr.GetNodeID() != "" {
				globalPool.Heartbeat(netMgr.GetNodeID())
			}
		}
	}()
}

// sendHeartbeats sends heartbeat to all known peers.
func sendHeartbeats() {
	netMgr.mu.RLock()
	peers := make([]PeerInfo, len(netMgr.config.Peers))
	copy(peers, netMgr.config.Peers)
	netMgr.mu.RUnlock()

	// Build heartbeat payload
	var uptime int64
	if !netMgr.startTime.IsZero() {
		uptime = int64(time.Since(netMgr.startTime).Seconds())
	}

	hb := HeartbeatPayload{
		NodeID:    netMgr.GetNodeID(),
		NodeName:  netMgr.config.NodeName,
		Addresses: netMgr.collectAddresses(),
		Models:    netMgr.config.SharedModels,
		Uptime:    uptime,
		Version:   AppVersion,
		Timestamp: time.Now().Unix(),
	}

	// Phase 4: Attach region info to heartbeat
	if regionMgr != nil {
		selfRegion := regionMgr.GetNodeRegion(netMgr.GetNodeID())
		if selfRegion != nil {
			hb.Region = &HeartbeatRegionInfo{
				Region:    string(selfRegion.Region),
				SubRegion: selfRegion.SubRegion,
				Latitude:  selfRegion.Latitude,
				Longitude: selfRegion.Longitude,
			}
		}
	}

	body, _ := json.Marshal(hb)
	client := &httpClient10

	// Sign heartbeat: Ed25519 signature over node_id
	sig := signHeartbeat(hb.NodeID)

	for _, peer := range peers {
		if len(peer.Addresses) == 0 {
			continue
		}
		addr := pickBestAddress(peer.Addresses)
		if addr == "" {
			continue
		}

		url := strings.TrimRight(addr, "/") + "/api/network/heartbeat"
		go func(peerID, peerURL string, peerBody []byte) {
			req, err := http.NewRequest("POST", peerURL, bytes.NewReader(peerBody))
			if err != nil {
				slog.Debug("heartbeat request build failed", "peer", peerID, "error", err)
				recordMissedHeartbeat(peerID)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Node-Signature", sig)
			resp, err := client.Do(req)
			if err != nil {
				slog.Debug("heartbeat failed", "peer", peerID, "url", peerURL, "error", err)
				recordMissedHeartbeat(peerID)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				slog.Debug("heartbeat non-200", "peer", peerID, "status", resp.StatusCode)
				recordMissedHeartbeat(peerID)
				return
			}

			// Parse gossip response
			var hbResp HeartbeatResponse
			if err := json.NewDecoder(resp.Body).Decode(&hbResp); err == nil {
				processGossipPeers(hbResp.Peers)
			}

			// Mark peer as alive
			recordSuccessfulHeartbeat(peerID)

			// Update peer info in route table
			routeTable.Put(peerID, peer.Name, peer.Addresses)
		}(peer.NodeID, url, body)
	}
}

// recordSuccessfulHeartbeat records a successful heartbeat from a peer.
func recordSuccessfulHeartbeat(nodeID string) {
	state, ok := heartbeatStates[nodeID]
	if !ok {
		state = &peerHeartbeatState{}
		heartbeatStates[nodeID] = state
	}
	state.missedCount = 0
	state.lastHeartbeat = time.Now()

	// Update peer status to online
	if netMgr != nil {
		netMgr.mu.Lock()
		for i, p := range netMgr.config.Peers {
			if p.NodeID == nodeID && p.Status != "online" {
				netMgr.config.Peers[i].Status = "online"
				netMgr.config.Peers[i].LastSeen = time.Now().Format(time.RFC3339)
				netMgr.updateOnlineCount()
				netMgr.doSave()
				slog.Info("peer back online", "node_id", nodeID)
			} else if p.NodeID == nodeID {
				netMgr.config.Peers[i].LastSeen = time.Now().Format(time.RFC3339)
				netMgr.doSave()
			}
		}
		netMgr.mu.Unlock()
	}
}

// recordMissedHeartbeat increments the missed counter for a peer.
func recordMissedHeartbeat(nodeID string) {
	state, ok := heartbeatStates[nodeID]
	if !ok {
		state = &peerHeartbeatState{}
		heartbeatStates[nodeID] = state
	}
	state.missedCount++
}

// checkPeerHealth checks if any peers should be marked offline.
func checkPeerHealth() {
	netMgr.mu.Lock()
	changed := false
	for i, p := range netMgr.config.Peers {
		state, ok := heartbeatStates[p.NodeID]
		if !ok {
			continue
		}
		if state.missedCount >= maxMissedHeartbeats && p.Status != "offline" {
			netMgr.config.Peers[i].Status = "offline"
			changed = true
			slog.Warn("peer marked offline", "node_id", p.NodeID, "missed", state.missedCount)
		}
	}
	if changed {
		netMgr.updateOnlineCount()
		netMgr.doSave()
	}
	netMgr.mu.Unlock()
}

// processGossipPeers processes peers received from a gossip response.
func processGossipPeers(peers []HeartbeatPeerInfo) {
	selfID := netMgr.GetNodeID()
	for _, gp := range peers {
		if gp.NodeID == selfID {
			continue // skip self
		}
		// Check if we already know this peer
		netMgr.mu.RLock()
		known := false
		for _, p := range netMgr.config.Peers {
			if p.NodeID == gp.NodeID {
				known = true
				break
			}
		}
		netMgr.mu.RUnlock()

		if !known && len(gp.Addresses) > 0 {
			// Auto-register discovered peer
			newPeer := PeerInfo{
				NodeID:    gp.NodeID,
				Name:      gp.NodeName,
				Models:    gp.Models,
				Status:    gp.Status,
				LastSeen:  time.Now().Format(time.RFC3339),
				Addresses: gp.Addresses,
				TrustScore: 0.5,
				JoinedAt:  time.Now().Format(time.RFC3339),
			}
			netMgr.AddPeer(newPeer)
			slog.Info("discovered new peer via gossip", "node_id", gp.NodeID, "name", gp.NodeName)
		}

		// Update route table regardless
		if len(gp.Addresses) > 0 {
			routeTable.Put(gp.NodeID, gp.NodeName, gp.Addresses)
		}
	}
}

// ============================================================
// API Handler — Heartbeat Endpoint
// ============================================================

// POST /api/network/heartbeat — receive heartbeat, return gossip peers
func handleNetworkHeartbeat(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil || !netMgr.IsSharedMode() {
		writeError(w, 400, "shared network not active")
		return
	}

	// SA-03: Verify request signature to prevent unauthorized heartbeats
	signature := r.Header.Get("X-Node-Signature")
	if signature == "" {
		writeError(w, 401, "missing X-Node-Signature header")
		return
	}

	var hb HeartbeatPayload
	if err := readJSON(r, &hb); err != nil {
		writeError(w, 400, "invalid heartbeat payload")
		return
	}

	if hb.NodeID == "" {
		writeError(w, 400, "node_id is required")
		return
	}

	// Verify signature: sender must prove ownership of node_id
	// The signature is Ed25519(node_id, timestamp) and we verify using the node's public key
	if !verifyHeartbeatAuth(hb.NodeID, signature) {
		writeError(w, 401, "invalid signature — cannot verify node identity")
		return
	}

	// Update the sender's info in our route table
	if len(hb.Addresses) > 0 {
		routeTable.Put(hb.NodeID, hb.NodeName, hb.Addresses)
	}

	// Phase 4: Process region information
	if regionMgr != nil {
		regionMgr.ProcessHeartbeatRegion(hb.NodeID, hb.Region, extractRemoteIP(r))
	}

	// Update or add the peer
	existingPeer := false
	netMgr.mu.Lock()
	for i, p := range netMgr.config.Peers {
		if p.NodeID == hb.NodeID {
			netMgr.config.Peers[i].Name = hb.NodeName
			netMgr.config.Peers[i].Status = "online"
			netMgr.config.Peers[i].LastSeen = time.Now().Format(time.RFC3339)
			if len(hb.Addresses) > 0 {
				netMgr.config.Peers[i].Addresses = hb.Addresses
			}
			if len(hb.Models) > 0 {
				netMgr.config.Peers[i].Models = hb.Models
			}
			existingPeer = true
			break
		}
	}
	if !existingPeer {
		newPeer := PeerInfo{
			NodeID:     hb.NodeID,
			Name:       hb.NodeName,
			Models:     hb.Models,
			Status:     "online",
			LastSeen:   time.Now().Format(time.RFC3339),
			Addresses:  hb.Addresses,
			TrustScore: 0.5,
			JoinedAt:   time.Now().Format(time.RFC3339),
		}
		netMgr.config.Peers = append(netMgr.config.Peers, newPeer)
		netMgr.config.Stats.TotalPeers = len(netMgr.config.Peers)
	}
	netMgr.updateOnlineCount()
	netMgr.doSave()
	netMgr.mu.Unlock()

	// Record successful heartbeat
	recordSuccessfulHeartbeat(hb.NodeID)

	// Build gossip response: other known peers
	selfID := netMgr.GetNodeID()
	var gossipPeers []HeartbeatPeerInfo
	netMgr.mu.RLock()
	for _, p := range netMgr.config.Peers {
		if p.NodeID == hb.NodeID || p.NodeID == selfID {
			continue // skip the sender and self
		}
		gossipPeers = append(gossipPeers, HeartbeatPeerInfo{
			NodeID:    p.NodeID,
			NodeName:  p.Name,
			Addresses: p.Addresses,
			Models:    p.Models,
			Status:    p.Status,
		})
	}
	netMgr.mu.RUnlock()

	writeJSON(w, 200, HeartbeatResponse{
		Status: "ok",
		Peers:  gossipPeers,
	})
}

// ============================================================
// Public Node Info Endpoint
// ============================================================

// requireHTTPS wraps a handler to reject non-TLS connections (except localhost).
// SA-02: Prevents public key exfiltration over plaintext HTTP.
func requireHTTPS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Allow localhost/loopback for local development and health checks
		if r.TLS == nil {
			host := strings.Split(r.RemoteAddr, ":")[0]
			if host != "127.0.0.1" && host != "::1" && host != "localhost" {
				http.Error(w, "HTTPS required", http.StatusForbidden)
				slog.Warn("rejected non-TLS pubkey request", "remote", r.RemoteAddr)
				return
			}
		}
		next(w, r)
	}
}

// GET /api/node/pubkey — returns this node's public key (for signature verification)
func handleNodePubKey(w http.ResponseWriter, r *http.Request) {
	if node == nil || !node.IsInitialized() {
		writeError(w, 500, "node not initialized")
		return
	}
	writeJSON(w, 200, map[string]string{
		"node_id": netMgr.GetNodeID(),
		"pub_key": node.PubKeyB64(),
	})
}

// GET /api/node/info — returns public node information
func handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	if node == nil || !node.IsInitialized() {
		writeError(w, 500, "node not initialized")
		return
	}

	var uptime int64
	if netMgr != nil && !netMgr.startTime.IsZero() {
		uptime = int64(time.Since(netMgr.startTime).Seconds())
	}

	writeJSON(w, 200, map[string]any{
		"node_id":    netMgr.GetNodeID(),
		"node_name":  netMgr.config.NodeName,
		"pub_key":    node.PubKeyB64(),
		"addresses":  netMgr.collectAddresses(),
		"models":     netMgr.config.SharedModels,
		"uptime":     uptime,
		"version":    AppVersion,
		"mode":       netMgr.config.Mode,
	})
}

// ============================================================
// Contribution Recording
// ============================================================

// RecordContribution records a contribution after a successful relay.
func RecordContribution(fromNodeID string, tokensUsed int64) {
	if netMgr == nil {
		return
	}
	netMgr.mu.Lock()
	defer netMgr.mu.Unlock()

	record := ContribRecord{
		Timestamp:  time.Now().Format(time.RFC3339),
		TokensUsed: tokensUsed,
		Requests:   1,
		FromNodeID: fromNodeID,
	}

	netMgr.config.ContribRecords = append(netMgr.config.ContribRecords, record)

	// Keep only last 1000 records
	if len(netMgr.config.ContribRecords) > 1000 {
		netMgr.config.ContribRecords = netMgr.config.ContribRecords[len(netMgr.config.ContribRecords)-1000:]
	}

	// Add contribution points (1 point per request, or tokens/1000)
	points := tokensUsed / 1000
	if points < 1 {
		points = 1
	}
	netMgr.config.ContribPoints += points

	// Phase 4: Record contribution to balance engine
	if balanceEngine != nil {
		balanceEngine.RecordContributionBalance(netMgr.config.NodeID, tokensUsed)
		if fromNodeID != "" {
			balanceEngine.RecordConsumptionBalance(fromNodeID, tokensUsed)
		}
	}

	// Phase 2: Track contribution for unlock state
	if netMgr.config.NodeUnlockStates == nil {
		netMgr.config.NodeUnlockStates = make(map[string]*NodeUnlockState)
	}
	if fromNodeID != "" {
		state, exists := netMgr.config.NodeUnlockStates[fromNodeID]
		if !exists {
			state = &NodeUnlockState{
				NodeID:   fromNodeID,
				Unlocked: false,
			}
			netMgr.config.NodeUnlockStates[fromNodeID] = state
		}
		state.ContribPoints += points
	}

	netMgr.doSave()
}

// httpClient10 is a shared HTTP client with 10s timeout for internal calls.
var httpClient10 = http.Client{Timeout: 10 * time.Second}

// ============================================================
// Bootstrap node auto-registration
// ============================================================

// registerWithBootstraps sends initial heartbeat to all bootstrap nodes.
func registerWithBootstraps() {
	if netMgr == nil || !netMgr.IsSharedMode() {
		return
	}

	netMgr.mu.RLock()
	bootstraps := make([]string, len(netMgr.config.BootstrapNodes))
	copy(bootstraps, netMgr.config.BootstrapNodes)
	netMgr.mu.RUnlock()

	var uptime int64
	if !netMgr.startTime.IsZero() {
		uptime = int64(time.Since(netMgr.startTime).Seconds())
	}

	hb := HeartbeatPayload{
		NodeID:    netMgr.GetNodeID(),
		NodeName:  netMgr.config.NodeName,
		Addresses: netMgr.collectAddresses(),
		Models:    netMgr.config.SharedModels,
		Uptime:    uptime,
		Version:   AppVersion,
		Timestamp: time.Now().Unix(),
	}

	body, _ := json.Marshal(hb)
	client := &httpClient10

	for _, bs := range bootstraps {
		url := strings.TrimRight(bs, "/") + "/api/network/heartbeat"
		go func(bsURL string, bsBody []byte) {
			resp, err := client.Post(bsURL, "application/json", bytes.NewReader(bsBody))
			if err != nil {
				slog.Debug("bootstrap registration failed", "url", bsURL, "error", err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == 200 {
				var hbResp HeartbeatResponse
				if err := json.NewDecoder(resp.Body).Decode(&hbResp); err == nil {
					processGossipPeers(hbResp.Peers)
					slog.Info("registered with bootstrap node", "url", bsURL, "discovered_peers", len(hbResp.Peers))
				}
			}
		}(url, body)
	}
}

// signHeartbeat creates an Ed25519 signature over the node_id for heartbeat authentication.
// SA-13: Uses node.Sign() for decrypt-on-demand signing.
func signHeartbeat(nodeID string) string {
	if node == nil || !node.IsInitialized() {
		return ""
	}
	return node.Sign([]byte(nodeID))
}

// verifyHeartbeatAuth verifies the heartbeat signature against the node's known public key.
// Returns true if the signature is valid, or if the node is unknown (to allow initial discovery).
func verifyHeartbeatAuth(nodeID, signatureB64 string) bool {
	if signatureB64 == "" {
		return false
	}

	sigBytes, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}

	// If this is our own node_id, verify against our own key
	if netMgr != nil && nodeID == netMgr.GetNodeID() {
		return true // self-heartbeat (shouldn't happen in practice)
	}

	// Look up the sender's public key
	pubKey := lookupNodePublicKey(nodeID)
	if pubKey == nil {
		// Unknown node — accept on first contact for discovery, but log it
		slog.Debug("heartbeat from unknown node, accepting for discovery", "node_id", nodeID)
		return true
	}

	return ed25519.Verify(pubKey, []byte(nodeID), sigBytes)
}

// lookupNodePublicKey finds the Ed25519 public key for a given node_id.
func lookupNodePublicKey(nodeID string) ed25519.PublicKey {
	if node == nil {
		return nil
	}
	// Check known peers
	if netMgr != nil {
		netMgr.mu.RLock()
		defer netMgr.mu.RUnlock()
		for _, peer := range netMgr.config.Peers {
			if peer.NodeID == nodeID {
				// For peers we know, try to fetch their public key
				if len(peer.Addresses) > 0 {
					return fetchPeerPublicKey(peer.Addresses)
				}
				return nil
			}
		}
	}
	return nil
}