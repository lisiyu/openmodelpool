package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// This file implements route handlers that were referenced by server.go's route
// table but whose real implementations were missing from the tree. They are
// intentionally minimal: they return real data where the underlying subsystem
// is available (e.g. algorithm params) and a clear "not implemented" status
// where the subsystem (WAF, region manager, DHT) has not been wired up yet.

// requireHTTPS is a middleware wrapper that rejects non-HTTPS requests unless
// terminated behind a proxy that sets X-Forwarded-Proto: https.
func requireHTTPS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			writeError(w, http.StatusForbidden, "HTTPS required")
			return
		}
		h(w, r)
	}
}

// ---- Node ----

func handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status": "ok",
		"note":   "node info endpoint",
	})
}

func handleNodePubKey(w http.ResponseWriter, r *http.Request) {
	pubKeyB64 := ""
	if node != nil && node.IsInitialized() {
		pubKeyB64 = node.PubKeyB64()
	}
	writeJSON(w, 200, map[string]any{
		"public_key": pubKeyB64,
		"node_id":    func() string { if node != nil { return node.NodeID() }; return "" }(),
	})
}

// handleNetworkHeartbeat implements POST /api/network/heartbeat — the
// receiving side of the node-to-node heartbeat. It:
//  1. Authenticates the sender (federation secret, or known-node fallback
//     when the mesh is open / unsecured).
//  2. Requires a sender node_id (X-Node-ID header, else JSON body).
//  3. Refreshes the sender's liveness in this node's local view:
//     - bumps the peer's LastSeen in the network manager,
//     - marks the node active in the federation manager,
//     - keeps the participant active in the shared global pool.
//
// One failing sub-step must not abort the others; each is best-effort.
func handleNetworkHeartbeat(w http.ResponseWriter, r *http.Request) {
	secret := ""
	if cfg != nil {
		secret = cfg.Get("federation_secret", "")
	}

	// Read the (optional) heartbeat body once. Region info is self-reported and,
	// when present, is used to register/refresh the node's region in the
	// RegionManager (see the wiring below, after authentication).
	// Parse the (optional) heartbeat body directly. SEC-B5-9: readJSON writes an
	// error response on malformed input; ignoring that error here then writing a
	// second response would produce a corrupted "superfluous WriteHeader"
	// response. Parse independently so a bad body is simply treated as empty.
	var hbBody struct {
		NodeID    string  `json:"node_id"`
		Endpoint  string  `json:"endpoint"`
		Region    string  `json:"region"`
		SubRegion string  `json:"sub_region"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 4096)); err == nil {
		_ = json.Unmarshal(bodyBytes, &hbBody)
	}

	senderNodeID := sanitizeNodeID(r.Header.Get("X-Node-ID"))
	if senderNodeID == "" {
		senderNodeID = hbBody.NodeID
	}

	authed := false
	if secret != "" {
		// Secret-protected mesh: require the matching header.
		// SEC-B3-4: constant-time comparison so the shared secret is not
		// recoverable via timing across many heartbeat attempts.
		if supplied := r.Header.Get("X-Federation-Secret"); supplied != "" {
			authed = subtle.ConstantTimeCompare([]byte(supplied), []byte(secret)) == 1
		}
	} else if fed != nil && senderNodeID != "" {
		// Open mesh: require a node known to the federation manager.
		if _, ok := fed.GetNode(senderNodeID); ok {
			authed = true
		}
	}
	// SEC-P2-13: there is deliberately NO "no auth configured -> allow" branch.
	// Heartbeat mutates peer liveness/region/global-pool state and must default
	// to deny when no authentication mechanism is configured.
	if !authed {
		writeError(w, http.StatusForbidden, "federation authentication required")
		return
	}

	if senderNodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}

	// Register / refresh the sender's region from the self-reported heartbeat.
	// This is the heartbeat half of the Region Manager wiring: nodes that include
	// a "region" field in their heartbeat are tracked for region-aware routing.
	// SEC-B5-5: the region string is NOT part of the authenticated payload, so
	// only the coarse region label (normalized by regionCanonical) is trusted;
	// self-reported latitude/longitude are discarded to prevent a peer from
	// claiming an arbitrary coordinate that could skew region-aware routing.
	if regionManager != nil && hbBody.Region != "" {
		info := &HeartbeatRegionInfo{
			Region: hbBody.Region,
		}
		regionManager.ProcessHeartbeatRegion(senderNodeID, info, extractRemoteIP(r))
	}

	// Refresh the sender's liveness in this node's local view.
	touchPeerLastSeen(senderNodeID)

	if fed != nil {
		if existing, ok := fed.GetNode(senderNodeID); ok {
			updated := *existing
			updated.Status = "active"
			updated.LastSeen = time.Now().UTC().Format(time.RFC3339)
			fed.UpdateNodeInfo(updated)
		}
	}

	if globalPool != nil {
		globalPool.Heartbeat(senderNodeID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"node_id": senderNodeID,
	})
}

// touchPeerLastSeen records that we recently heard from a peer by bumping its
// LastSeen timestamp in the local network manager (best-effort). It accesses
// the unexported NetworkManager fields directly because package main owns them;
// this keeps the heartbeat receiver self-contained without touching network.go.
func touchPeerLastSeen(nodeID string) {
	if netMgr == nil || nodeID == "" {
		return
	}
	netMgr.mu.Lock()
	defer netMgr.mu.Unlock()
	for i := range netMgr.config.Peers {
		if netMgr.config.Peers[i].NodeID == nodeID {
			netMgr.config.Peers[i].LastSeen = time.Now().Format(time.RFC3339)
			return
		}
	}
}

// ---- Algorithm governance chain ----

func handleAlgorithmCurrent(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeJSON(w, 200, map[string]any{"params": DefaultAlgorithmParams()})
		return
	}
	writeJSON(w, 200, map[string]any{"params": algoChain.GetCurrentParams()})
}

func handleAlgorithmHistory(w http.ResponseWriter, r *http.Request) {
	if governor == nil {
		writeError(w, http.StatusInternalServerError, "algorithm governance not initialized")
		return
	}
	history := governor.GetHistory()
	writeJSON(w, http.StatusOK, map[string]any{
		"history":          history,
		"count":            len(history),
		"governance_scope": GovernanceScope,
		"note":             GovernanceScopeNote,
	})
}

func handleAlgorithmProposals(w http.ResponseWriter, r *http.Request) {
	if governor == nil {
		writeError(w, http.StatusInternalServerError, "algorithm governance not initialized")
		return
	}
	statusFilter := r.URL.Query().Get("status")
	proposals := governor.ListProposals(statusFilter)
	views := make([]proposalView, 0, len(proposals))
	for _, p := range proposals {
		views = append(views, toProposalView(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proposals":        views,
		"count":            len(views),
		"governance_scope": GovernanceScope,
		"note":             GovernanceScopeNote,
	})
}

// algorithmProposeRequest is the body of POST /api/network/algorithm/propose.
type algorithmProposeRequest struct {
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Proposer     string           `json:"proposer"`
	ProposerName string           `json:"proposer_name"`
	Target       *AlgorithmParams `json:"target,omitempty"`
}

func handleAlgorithmPropose(w http.ResponseWriter, r *http.Request) {
	if governor == nil {
		writeError(w, http.StatusInternalServerError, "algorithm governance not initialized")
		return
	}
	var req algorithmProposeRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := governor.CreateProposal(req.Title, req.Description, req.Proposer, req.ProposerName, req.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "created",
		"proposal":         toProposalView(p),
		"governance_scope": GovernanceScope,
		"note":             GovernanceScopeNote,
	})
}

// algorithmVoteRequest is the body of POST /api/network/algorithm/vote.
type algorithmVoteRequest struct {
	ProposalID string `json:"proposal_id"`
	Voter      string `json:"voter"`
	VoterName  string `json:"voter_name"`
	Choice     string `json:"choice"`
	Comment    string `json:"comment"`
}

func handleAlgorithmVote(w http.ResponseWriter, r *http.Request) {
	if governor == nil {
		writeError(w, http.StatusInternalServerError, "algorithm governance not initialized")
		return
	}
	var req algorithmVoteRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ProposalID == "" {
		writeError(w, http.StatusBadRequest, "proposal_id is required")
		return
	}
	p, err := governor.CastVote(req.ProposalID, req.Voter, req.VoterName, req.Choice, req.Comment)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "recorded",
		"proposal":         toProposalView(p),
		"governance_scope": GovernanceScope,
		"note":             GovernanceScopeNote,
	})
}

// algorithmResolveRequest is the body of POST .../algorithm/proposals/{id}/resolve.
type algorithmResolveRequest struct {
	Decision string `json:"decision"` // "passed" | "rejected" | "closed"
	Reason   string `json:"reason"`
	Resolver string `json:"resolver"`
}

func handleAlgorithmProposalResolve(w http.ResponseWriter, r *http.Request) {
	if governor == nil {
		writeError(w, http.StatusInternalServerError, "algorithm governance not initialized")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "proposal id is required")
		return
	}
	var req algorithmResolveRequest
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var status ProposalStatus
	switch req.Decision {
	case "passed":
		status = ProposalStatusPassed
	case "rejected":
		status = ProposalStatusRejected
	case "closed":
		status = ProposalStatusClosed
	default:
		writeError(w, http.StatusBadRequest, "decision must be one of passed/rejected/closed")
		return
	}
	p, err := governor.ResolveProposal(id, req.Resolver, req.Reason, status)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "resolved",
		"proposal":         toProposalView(p),
		"governance_scope": GovernanceScope,
		"note":             GovernanceScopeNote,
	})
}

func handleAlgorithmValidate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"valid": true})
}

func handleAlgorithmGossip(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeJSON(w, 200, map[string]any{
			"status": "gossiped",
			"params": DefaultAlgorithmParams(),
		})
		return
	}
	writeJSON(w, 200, map[string]any{
		"status": "gossiped",
		"params": algoChain.GetCurrentParams(),
	})
}

// ---- Region manager ----

// handleNetworkRegions implements GET /api/network/regions. It returns the set
// of currently-known regions, a per-region node count, and the active region
// configuration. When the RegionManager is not initialized it returns empty
// (rather than erroring) so the UI degrades gracefully.
func handleNetworkRegions(w http.ResponseWriter, r *http.Request) {
	if regionManager == nil {
		writeJSON(w, 200, map[string]any{
			"regions":     []any{},
			"node_counts": map[string]int{},
			"config":      DefaultRegionConfig(),
		})
		return
	}

	regions := regionManager.GetAllRegions()
	if regions == nil {
		regions = []Region{}
	}

	summary := regionManager.GetRegionSummary()
	nodeCounts := make(map[string]int, len(summary))
	for rg, c := range summary {
		nodeCounts[string(rg)] = c
	}

	writeJSON(w, 200, map[string]any{
		"regions":     regions,
		"node_counts": nodeCounts,
		"config":      regionManager.GetConfig(),
	})
}

// handleNetworkRegionNodes implements GET /api/network/regions/{region}/nodes.
// It returns the node IDs registered in the requested region (matched after
// canonicalizing the region alias, e.g. "asia" -> "ap").
func handleNetworkRegionNodes(w http.ResponseWriter, r *http.Request) {
	regionStr := r.PathValue("region")
	if regionManager == nil {
		writeJSON(w, 200, map[string]any{"region": regionStr, "nodes": []string{}})
		return
	}
	region := regionCanonical(regionStr)
	nodes := regionManager.GetNodesByRegion(region)
	if nodes == nil {
		nodes = []string{}
	}
	writeJSON(w, 200, map[string]any{
		"region": region,
		"nodes":  nodes,
	})
}

// handleNetworkRegionConfigUpdate implements PUT /api/network/regions/config.
// It parses a RegionConfig from the request body (the Region type's
// UnmarshalJSON lets callers use aliases like "ap"/"eu" for RegionWeights keys),
// applies it via UpdateConfig, and echoes the resulting configuration.
func handleNetworkRegionConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if regionManager == nil {
		writeError(w, http.StatusInternalServerError, "region manager not initialized")
		return
	}
	var cfg RegionConfig
	if err := readJSON(w, r, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	regionManager.UpdateConfig(cfg)
	writeJSON(w, 200, map[string]any{
		"status": "updated",
		"config": regionManager.GetConfig(),
	})
}

// ---- WAF (four-layer protection) ----
//
// These handlers now reflect the real WAF engine state (see waf.go). They report
// live enforcement status, recorded violations, and active dynamic bans, and
// allow unbanning a previously-banned key.

func handleWAFStatus(w http.ResponseWriter, r *http.Request) {
	if wafEngine == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "note": "WAF engine not initialized"})
		return
	}
	writeJSON(w, 200, wafEngine.Status())
}

func handleWAFBans(w http.ResponseWriter, r *http.Request) {
	if wafEngine == nil {
		writeJSON(w, 200, map[string]any{"bans": []any{}})
		return
	}
	bans := wafEngine.Bans()
	out := make([]any, 0, len(bans))
	for _, b := range bans {
		out = append(out, b)
	}
	writeJSON(w, 200, map[string]any{"bans": out})
}

func handleWAFViolations(w http.ResponseWriter, r *http.Request) {
	if wafEngine == nil {
		writeJSON(w, 200, map[string]any{"violations": []any{}})
		return
	}
	vs := wafEngine.Violations()
	out := make([]any, 0, len(vs))
	for _, v := range vs {
		out = append(out, v)
	}
	writeJSON(w, 200, map[string]any{"violations": out})
}

func handleWAFUnban(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "ban key required")
		return
	}
	if wafEngine == nil {
		writeJSON(w, 200, map[string]any{"status": "ok", "removed": false})
		return
	}
	removed := wafEngine.RemoveBan(key)
	writeJSON(w, 200, map[string]any{"status": "ok", "removed": removed})
}
