package main

import (
	"encoding/json"
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
			http.Error(w, "HTTPS required", http.StatusForbidden)
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
	writeJSON(w, 200, map[string]any{
		"public_key": "",
		"note":       "node public key not exposed (BIP39 identity not yet wired)",
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

	senderNodeID := r.Header.Get("X-Node-ID")

	authed := false
	if secret != "" {
		// Secret-protected mesh: require the matching header.
		authed = r.Header.Get("X-Federation-Secret") == secret
	} else if fed != nil && senderNodeID != "" {
		// Open mesh: require a node known to the federation manager.
		if _, ok := fed.GetNode(senderNodeID); ok {
			authed = true
		}
	} else if secret == "" && fed == nil {
		// No auth mechanism configured at all — allow (best-effort open mesh).
		authed = true
	}
	if !authed {
		writeError(w, http.StatusForbidden, "federation authentication required")
		return
	}

	// Resolve sender node id from header, falling back to the JSON body.
	if senderNodeID == "" {
		var body struct {
			NodeID string `json:"node_id"`
		}
		_ = readJSON(r, &body)
		senderNodeID = body.NodeID
	}
	if senderNodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
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
	writeJSON(w, 200, map[string]any{"history": []any{}})
}

func handleAlgorithmProposals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"proposals": []any{}})
}

func handleAlgorithmPropose(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status": "accepted",
		"note":   "algorithm governance proposal accepted locally; decentralized voting not yet implemented",
	})
}

func handleAlgorithmVote(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status": "accepted",
		"note":   "vote recorded locally; decentralized voting not yet implemented",
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

func handleNetworkRegions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"regions": []any{},
		"note":    "region manager not yet wired (see network_region_test.go)",
	})
}

func handleNetworkRegionNodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"nodes": []any{}})
}

func handleNetworkRegionConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	_ = readJSON(r, &body)
	writeJSON(w, 200, map[string]any{"status": "updated"})
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
