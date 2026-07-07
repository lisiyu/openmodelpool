package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// ============================================================
// Decentralized Relay Handler
// ============================================================
//
// Route: ANY /network/{node_id}/{rest...}
//
// When a shared-network node receives a request at /network/{node_id}/...,
// it acts as a relay:
//   1. Look up node_id in the route table
//   2. If found → reverse-proxy the request to the target node
//   3. If not found → try querying bootstrap nodes (Phase 1: return 404)
//   4. Hop-count header prevents infinite loops (max 3)
//
// The target node receives the request with /network/{node_id} stripped,
// so /network/mmx-abc123/v1/chat/completions → target sees /v1/chat/completions
// This ensures OpenAI SDK compatibility at the target.

const (
	headerRelayHop  = "X-ModelMux-Agent-Hop"
	headerRelayFrom = "X-ModelMux-Agent-Relay-From"
)

// handleNetworkRelay handles relay requests: /network/{node_id}/{rest...}
func handleNetworkRelay(w http.ResponseWriter, r *http.Request) {
	// Only serve in shared mode
	if netMgr == nil || !netMgr.IsSharedMode() {
		writeError(w, 404, "shared network not active")
		return
	}

	// Extract node_id from path: /network/{node_id}/...
	path := strings.TrimPrefix(r.URL.Path, "/network/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, 400, "missing node_id in path")
		return
	}
	targetNodeID := parts[0]

	// Validate NodeID format
	if !strings.HasPrefix(targetNodeID, p2pNodeIDPrefix) {
		writeError(w, 400, "invalid node_id format")
		return
	}

	// Check hop count to prevent loops
	hopCount := 0
	if hopStr := r.Header.Get(headerRelayHop); hopStr != "" {
		hopCount, _ = strconv.Atoi(hopStr)
	}
	if hopCount >= maxRelayHops {
		writeError(w, 508, "max relay hops exceeded")
		slog.Warn("relay loop detected", "node_id", targetNodeID, "hops", hopCount)
		return
	}

	// Phase 2: Check key-based routing restrictions
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer mk_trial_") {
		// Trial keys can ONLY route back to the issuer node
		issuerNodeID := extractTrialKeyIssuer(authHeader)
		if issuerNodeID != "" && targetNodeID != issuerNodeID {
			writeError(w, 403, "trial keys can only access the issuing node")
			return
		}
	} else if strings.HasPrefix(authHeader, "Bearer mk_open_") {
		// Open key routing logic
		keyType := ClassifyKey(strings.TrimPrefix(authHeader, "Bearer "))
		if keyType == KeyTypeOpenUnbound {
			// Unbound open keys: only route to the node that issued them
			// (they're trial-like, limited scope)
			issuerNodeID := extractOpenKeyIssuer(authHeader)
			if issuerNodeID != "" && targetNodeID != issuerNodeID {
				writeError(w, 403, "unbound open keys can only access the issuing node")
				return
			}
		} else if keyType == KeyTypeOpenBound {
			// Bound open keys: can access all nodes, but require the bound node to be unlocked
			issuerNodeID := extractOpenBoundKeyNodeID(authHeader)
			if issuerNodeID != "" && targetNodeID != issuerNodeID {
				if !netMgr.IsNodeUnlocked(issuerNodeID) {
					writeError(w, 403, "node not yet unlocked — must contribute first to access network resources")
					return
				}
			}
		}
	} else if strings.HasPrefix(authHeader, "Bearer mk_") {
		// Standard signed keys: check if issuer node is unlocked
		mkKey := strings.TrimPrefix(authHeader, "Bearer ")
		payload, err := ValidateKey(mkKey)
		if err == nil && payload != nil {
			issuerNodeID := payload.Iss
			if issuerNodeID != "" && targetNodeID != issuerNodeID {
				if !netMgr.IsNodeUnlocked(issuerNodeID) {
					writeError(w, 403, "issuer node not yet unlocked — must contribute first to access network resources")
					return
				}
			}
		}
	}

	// If the target is ourselves, handle locally
	selfID := netMgr.GetNodeID()
	if targetNodeID == selfID {
		handleRelayToLocal(w, r, parts, hopCount)
		return
	}

	// Phase 2: Check if target node is unlocked (for non-trial keys)
	if !strings.HasPrefix(authHeader, "Bearer mk_trial_") {
		if !netMgr.IsNodeUnlocked(targetNodeID) {
			// Target node exists but is not unlocked - still allow routing but note it
			slog.Debug("routing to locked node", "target", targetNodeID)
		}
	}

	// Resolve target node in route table
	entry := routeTable.Get(targetNodeID)
	if entry == nil {
		// Phase 1: query bootstrap nodes (simplified)
		// Phase 2: full DHT lookup via libp2p
		entry = queryBootstrapForNode(targetNodeID)
	}

	if entry == nil || len(entry.Addresses) == 0 {
		writeJSON(w, 404, map[string]any{
			"error":   "node not found",
			"node_id": targetNodeID,
			"message": "target node not found in route table. It may be offline or not yet registered.",
		})
		return
	}

	// Forward request via reverse proxy to the target node
	relayToRemote(w, r, entry, parts, hopCount)
}

// extractTrialKeyIssuer extracts the issuer node_id from a trial key
func extractTrialKeyIssuer(authHeader string) string {
	// Format: Bearer mk_trial_{node_id}_{timestamp}.{payload}.{sig}
	key := strings.TrimPrefix(authHeader, "Bearer ")
	rest := strings.TrimPrefix(key, "mk_trial_")
	// node_id is before the first underscore after the prefix... actually:
	// Format: mk_trial_{node_id}_{timestamp}...
	// node_id starts with mmx- and contains 32 hex chars
	// So: mk_trial_mmx-XXXXX_YYYYY.payload.sig
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) == 0 {
		return ""
	}
	prefix := parts[0]
	// Find the last underscore which separates node_id from timestamp
	lastUnderscore := strings.LastIndex(prefix, "_")
	if lastUnderscore <= 0 {
		return ""
	}
	nodeID := prefix[:lastUnderscore]
	// Verify it looks like a node ID
	if strings.HasPrefix(nodeID, p2pNodeIDPrefix) {
		return nodeID
	}
	return ""
}

// extractOpenKeyIssuer extracts the issuer node_id from an open key (for unbound)
func extractOpenKeyIssuer(authHeader string) string {
	// Unbound: mk_open_{random}.{payload}.{sig}
	// No node_id in the key — returns "" meaning no issuer restriction
	// The validation happens at the target node
	return ""
}

// extractOpenBoundKeyNodeID extracts the bound node_id from a bound open key
func extractOpenBoundKeyNodeID(authHeader string) string {
	// Bound: mk_open_{node_id}_{random}.{payload}.{sig}
	key := strings.TrimPrefix(authHeader, "Bearer ")
	rest := strings.TrimPrefix(key, "mk_open_")
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) == 0 {
		return ""
	}
	prefix := parts[0]
	// Try to find node_id pattern (mmx-...)
	if idx := strings.Index(prefix, "mmx-"); idx >= 0 {
		// node_id is from mmx- to the next underscore
		remaining := prefix[idx:]
		endIdx := strings.Index(remaining, "_")
		if endIdx > 0 {
			nodeID := remaining[:endIdx]
			if strings.HasPrefix(nodeID, p2pNodeIDPrefix) {
				return nodeID
			}
		}
	}
	return ""
}

// handleRelayToLocal handles requests targeting this node itself
// Strips /network/{node_id} prefix and serves the remaining path locally
func handleRelayToLocal(w http.ResponseWriter, r *http.Request, parts []string, hopCount int) {
	netMgr.RecordReceived()

	// Phase 2: Validate mk_ signed keys if present
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer mk_") {
		mkKey := strings.TrimPrefix(authHeader, "Bearer ")
		keyType := ClassifyKey(mkKey)

		// Determine the model from the path for access check
		model := extractModelFromPath(r.URL.Path)

		switch keyType {
		case KeyTypeTrial:
			// Trial key: validate signature and apply 2x contribution
			payload, err := ValidateKey(mkKey)
			if err != nil {
				slog.Warn("trial key validation failed", "error", err, "path", r.URL.Path)
				writeError(w, 401, fmt.Sprintf("trial key invalid: %v", err))
				return
			}
			// Record usage with 2x contribution (trial incentive)
			if keyStore != nil {
				keyStore.RecordUsage(payload.Sub)
			}
			RecordContribution(payload.Iss, 2) // 2x for trial
			slog.Info("trial key validated", "consumer_id", payload.Sub, "issuer", payload.Iss, "model", model)

			r.Header.Del("Authorization")
			r.Header.Set("X-MK-Consumer", payload.Sub)
			r.Header.Set("X-MK-Issuer", payload.Iss)
			r.Header.Set("X-MK-KeyType", "trial")

		case KeyTypeOpenUnbound:
			// Open unbound: validate and record usage
			payload, err := ValidateKey(mkKey)
			if err != nil {
				slog.Warn("open unbound key validation failed", "error", err)
				writeError(w, 401, fmt.Sprintf("open key invalid: %v", err))
				return
			}
			// Record usage
			if openKeys != nil {
				openKeys.RecordOpenKeyUsage(payload.Sub)
			}
			if keyStore != nil {
				keyStore.RecordUsage(payload.Sub)
			}
			slog.Info("open unbound key validated", "consumer_id", payload.Sub, "model", model)

			r.Header.Del("Authorization")
			r.Header.Set("X-MK-Consumer", payload.Sub)
			r.Header.Set("X-MK-KeyType", "open_unbound")

		case KeyTypeOpenBound:
			// Open bound: validate and check unlock status
			payload, err := ValidateKey(mkKey)
			if err != nil {
				slog.Warn("open bound key validation failed", "error", err)
				writeError(w, 401, fmt.Sprintf("open bound key invalid: %v", err))
				return
			}
			// Check if bound node is unlocked (for cross-node access)
			if payload.Iss != "" && !netMgr.IsNodeUnlocked(payload.Iss) {
				slog.Warn("open bound key node not unlocked", "node_id", payload.Iss)
				writeError(w, 403, "bound node not yet unlocked")
				return
			}
			// Check reputation degradation (rep < 50 → degrade to unbound)
			if repMgr != nil {
				rep := repMgr.GetReputation(payload.Iss)
				if rep != nil && rep.OverallScore < 50 {
					slog.Warn("open bound key degraded due to low reputation", "node_id", payload.Iss, "score", rep.OverallScore)
					// Allow but mark as degraded
					r.Header.Set("X-MK-Degrad", "true")
				}
			}
			if openKeys != nil {
				openKeys.RecordOpenKeyUsage(payload.Sub)
			}
			if keyStore != nil {
				keyStore.RecordUsage(payload.Sub)
			}
			slog.Info("open bound key validated", "consumer_id", payload.Sub, "issuer", payload.Iss, "model", model)

			r.Header.Del("Authorization")
			r.Header.Set("X-MK-Consumer", payload.Sub)
			r.Header.Set("X-MK-Issuer", payload.Iss)
			r.Header.Set("X-MK-KeyType", "open_bound")

		default:
			// Standard signed key
			payload, err := ValidateKey(mkKey)
			if err != nil {
				slog.Warn("signed key validation failed", "error", err, "path", r.URL.Path)
				writeError(w, 401, fmt.Sprintf("signed key invalid: %v", err))
				return
			}

			// Check model access
			if model != "" && !CheckModelAccess(payload, model) {
				slog.Warn("signed key model access denied", "model", model, "allowed", payload.Models)
				writeError(w, 403, fmt.Sprintf("model '%s' not allowed by this key", model))
				return
			}

			// Record usage
			if keyStore != nil {
				keyStore.RecordUsage(payload.Sub)
			}

			// Record contribution
			RecordContribution(payload.Iss, 0)

			slog.Info("signed key validated", "consumer_id", payload.Sub, "issuer", payload.Iss, "model", model)

			// Clear the mk_ auth header so it doesn't interfere with local serving
			r.Header.Del("Authorization")
			r.Header.Set("X-MK-Consumer", payload.Sub)
			r.Header.Set("X-MK-Issuer", payload.Iss)
		}
	} else if strings.HasPrefix(authHeader, "Bearer sk_") {
		// sk_ keys: keep original behavior (pass through as consumer key)
		// No additional validation needed here
	}

	// Reconstruct path without the /network/{node_id} prefix
	restPath := ""
	if len(parts) > 1 {
		restPath = "/" + parts[1]
	} else {
		restPath = "/"
	}

	// Rewrite the request path
	r.URL.Path = restPath
	r.RequestURI = restPath
	if r.URL.RawQuery != "" {
		r.RequestURI += "?" + r.URL.RawQuery
	}

	slog.Info("relay to local", "target", "self", "path", restPath, "hops", hopCount)

	// Serve the rewritten request using the main handler
	// We re-dispatch to the main mux by calling the server's handler
	// The simplest way: construct a new request and serve it
	localPort := cfg.Get("service_port", "8000")
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%s", localPort))

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = restPath
			req.URL.RawQuery = r.URL.RawQuery
			req.Host = target.Host
			// Remove relay headers for local delivery
			req.Header.Del(headerRelayHop)
			req.Header.Del(headerRelayFrom)
		},
		ErrorHandler: func(w2 http.ResponseWriter, r2 *http.Request, err error) {
			slog.Error("local relay proxy error", "error", err)
			writeError(w2, 502, "local relay failed")
		},
	}

	proxy.ServeHTTP(w, r)
}

// relayToRemote forwards a request to a remote node via reverse proxy
func relayToRemote(w http.ResponseWriter, r *http.Request, entry *RouteEntry, parts []string, hopCount int) {
	// Pick the best address (prefer HTTPS)
	targetAddr := pickBestAddress(entry.Addresses)
	if targetAddr == "" {
		writeError(w, 502, "no reachable address for node")
		return
	}

	target, err := url.Parse(targetAddr)
	if err != nil {
		writeError(w, 502, "invalid target address")
		return
	}

	// Reconstruct the path: /network/{node_id}/{rest} → /network/{node_id}/{rest}
	// We keep the full path so the target can also strip it if it's also a relay
	// Actually, we strip it so the target sees the original path: /{rest}
	restPath := ""
	if len(parts) > 1 {
		restPath = "/" + parts[1]
	} else {
		restPath = "/"
	}

	relayFrom := netMgr.GetNodeID()

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = restPath
			req.URL.RawQuery = r.URL.RawQuery
			req.Host = target.Host

			// Set relay headers
			req.Header.Set(headerRelayHop, strconv.Itoa(hopCount+1))
			req.Header.Set(headerRelayFrom, relayFrom)

			// Preserve original auth headers (consumer key transparent to relay)
			// Do NOT strip Authorization — target node validates it
		},
		Transport: GetSharedHTTPClient().Transport,
		ErrorHandler: func(w2 http.ResponseWriter, r2 *http.Request, err error) {
			slog.Error("relay to remote failed", "target", entry.NodeID, "addr", targetAddr, "error", err)
			netMgr.RecordRelayResult(false)
			writeError(w2, 502, fmt.Sprintf("relay to %s failed: %v", entry.NodeID, err))
		},
		ModifyResponse: func(resp *http.Response) error {
			netMgr.RecordRelayResult(resp.StatusCode < 400)
			return nil
		},
	}

	slog.Info("relaying to remote", "target_node", entry.NodeID, "addr", targetAddr, "path", restPath, "hop", hopCount+1)
	proxy.ServeHTTP(w, r)
}

// pickBestAddress selects the best address from a list (prefer HTTPS public URLs)
func pickBestAddress(addresses []string) string {
	if len(addresses) == 0 {
		return ""
	}
	// Prefer custom domain > tunnel URL > localhost
	var tunnelURL, localAddr string
	for _, a := range addresses {
		if strings.HasPrefix(a, "https://") && !strings.Contains(a, "trycloudflare.com") {
			return a // custom domain — best
		}
		if strings.Contains(a, "trycloudflare.com") {
			tunnelURL = a
		}
		if strings.HasPrefix(a, "http://localhost") {
			localAddr = a
		}
	}
	if tunnelURL != "" {
		return tunnelURL
	}
	if localAddr != "" {
		return localAddr
	}
	return addresses[0]
}

// queryBootstrapForNode queries bootstrap nodes for a NodeID (Phase 1 simplified)
// In Phase 2 this will be replaced by full DHT lookup via libp2p
func queryBootstrapForNode(nodeID string) *RouteEntry {
	if netMgr == nil {
		return nil
	}
	netMgr.mu.RLock()
	bootstrapNodes := make([]string, len(netMgr.config.BootstrapNodes))
	copy(bootstrapNodes, netMgr.config.BootstrapNodes)
	netMgr.mu.RUnlock()

	client := GetSharedHTTPClient()

	for _, bootstrapURL := range bootstrapNodes {
		resolveURL := fmt.Sprintf("%s/api/network/resolve/%s", strings.TrimRight(bootstrapURL, "/"), nodeID)
		resp, err := client.Get(resolveURL)
		if err != nil {
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			continue
		}

		var result struct {
			NodeID    string   `json:"node_id"`
			NodeName  string   `json:"node_name"`
			Addresses []string `json:"addresses"`
			Status    string   `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		if len(result.Addresses) > 0 {
			// Cache in local route table
			routeTable.Put(result.NodeID, result.NodeName, result.Addresses)
			return &RouteEntry{
				NodeID:    result.NodeID,
				NodeName:  result.NodeName,
				Addresses: result.Addresses,
				Status:    result.Status,
			}
		}
	}
	return nil
}
// extractModelFromPath tries to extract a model name from the request path.
// e.g. /v1/chat/completions doesn't contain model in path, so returns "".
// For POST requests the model is in the body, but we can't read it here.
// This is a best-effort helper — returns "" if no model in path.
func extractModelFromPath(path string) string {
	// OpenAI paths: /v1/chat/completions, /v1/completions, /v1/models
	// Model is typically in the request body, not the path
	// Return empty — the actual model check happens at the proxy auth layer
	return ""
}