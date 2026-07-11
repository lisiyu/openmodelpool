package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
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
//
// === Public Key (sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1) Design Principles ===
//
// 1. Public keys ALWAYS access the global shared pool — never bound to any node.
// 2. Nodes with public internet access automatically participate in the network.
// 3. Public key routing does NOT depend on local node network state.
// 4. All providers with ShareToPool=true (default) are accessible via public keys.

const (
	headerRelayHop  = "X-OpenModelPool-Agent-Hop"
	headerRelayFrom = "X-OpenModelPool-Agent-Relay-From"
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

	// v2.0: Check key-based routing restrictions
	authHeader := r.Header.Get("Authorization")
	bearerKey := strings.TrimPrefix(authHeader, "Bearer ")

	switch ClassifyKey(bearerKey) {
	case KeyTypePublic:
		// sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1 → public trial key.
		// Design principle: Public keys ALWAYS route to the global shared pool.
		// They are not bound to any specific node and work regardless of whether
		// this node has joined the network. No routing restrictions at relay level.

	case KeyTypeGuest:
		// sk-guest-{node_id}-{random} → route to the issuing node
		guestNodeID, accessPublicPool, valid := GetGuestKeyAccessPublicPool(bearerKey)
		if !valid {
			writeError(w, 401, "invalid guest key")
			return
		}
		if guestNodeID != "" && targetNodeID != guestNodeID {
			// Key is not for this node
			if accessPublicPool {
				// Guest key with public pool access — allow relay to proceed (treat like public key)
				r.Header.Set("X-MK-KeyType", "public")
				r.Header.Set("X-MK-GuestPublicPool", "true")
			} else {
				// Guest key without public pool access — only valid at issuing node
				writeError(w, 403, "guest keys can only access the issuing node")
				return
			}
		}

	case KeyTypeProxy:
		// sk-{random} → Proxy API Key, can route to any node if the owner joined the network
		// No specific restriction at relay level

	default:
		// Unknown key type — allow relay (will be validated at destination)
	}

	// If the target is ourselves, handle locally
	selfID := netMgr.GetNodeID()
	if targetNodeID == selfID {
		handleRelayToLocal(w, r, parts, hopCount)
		return
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

// handleRelayToLocal handles requests targeting this node itself
// Strips /network/{node_id} prefix and serves the remaining path locally
func handleRelayToLocal(w http.ResponseWriter, r *http.Request, parts []string, hopCount int) {
	netMgr.RecordReceived()

	// v2.0: Simplified key handling for local relay
	authHeader := r.Header.Get("Authorization")
	bearerKey := strings.TrimPrefix(authHeader, "Bearer ")
	keyType := ClassifyKey(bearerKey)

	switch keyType {
	case KeyTypePublic:
		// sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1 — public key validated; always routes to shared pool.
		// No additional validation needed at relay level.
		r.Header.Set("X-MK-KeyType", "public")

	case KeyTypeGuest:
		// sk-guest-{node_id}-{random}
		nodeID, accessPublicPool, valid := GetGuestKeyAccessPublicPool(bearerKey)
		if !valid {
			writeError(w, 401, "invalid guest key")
			return
		}
		r.Header.Del("Authorization")
		if accessPublicPool {
			// Guest key with public pool access — treat like public key
			r.Header.Set("X-MK-KeyType", "public")
			r.Header.Set("X-MK-GuestPublicPool", "true")
			slog.Info("guest key with public pool access, routing as public", "node_id", nodeID)
		} else {
			// Regular guest key — local resources only
			r.Header.Set("X-MK-KeyType", "guest")
			r.Header.Set("X-MK-Guest-Node", nodeID)
			slog.Info("guest key validated for local relay", "node_id", nodeID)
		}

	case KeyTypeProxy:
		// sk-{random} — proxy API key, pass through
		r.Header.Set("X-MK-KeyType", "proxy")

	default:
		// Unknown key — pass through, let the local handler validate
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

	// SA-04: Enforce HTTPS for relay to prevent data interception
	if !strings.HasPrefix(targetAddr, "https://") {
		slog.Warn("relay target uses insecure protocol, rejecting", "node_id", entry.NodeID, "addr", targetAddr)
		writeError(w, 502, "relay target must use HTTPS for security")
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

	relayStart := time.Now()

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

			// S-4/V-3: Remove original Authorization to prevent Consumer Key leakage
			req.Header.Del("Authorization")

			// Add node-to-node authentication header
			if relayFrom != "" {
				req.Header.Set("X-Node-Auth", relayFrom)
			}
		},
		Transport: GetSharedHTTPClient().Transport,
		ErrorHandler: func(w2 http.ResponseWriter, r2 *http.Request, err error) {
			slog.Error("relay to remote failed", "target", entry.NodeID, "addr", targetAddr, "error", err)
			netMgr.RecordRelayResult(false)
			// Phase 4: Record failed request in load balancer
			if lbInstance != nil {
				lbInstance.RecordRequest(entry.NodeID, time.Since(relayStart), false)
			}
			writeError(w2, 502, fmt.Sprintf("relay to %s failed: %v", entry.NodeID, err))
		},
		ModifyResponse: func(resp *http.Response) error {
			success := resp.StatusCode < 400
			netMgr.RecordRelayResult(success)
			// Phase 4: Record relay outcome in load balancer metrics
			if lbInstance != nil {
				lbInstance.RecordRequest(entry.NodeID, time.Since(relayStart), success)
			}
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
// ============================================================
// Gateway Mode — Unified Entry Point
// ============================================================
//
// Gateway mode allows consumers to access the network without knowing
// the target NodeID. Requests to /v1/* are automatically routed to
// the best available node based on model, latency, and load.
//
// Flow:
//   1. Consumer sends request to /v1/chat/completions (standard OpenAI SDK)
//   2. Gateway parses the model field from request body
//   3. RouteTable.SelectBestNode picks the optimal node
//   4. Request is forwarded to the selected node
//   5. Response (streaming or non-streaming) is transparently relayed
//
// If no suitable node is found, the request falls back to local processing.

// handleGatewayRequest handles /v1/chat/completions, /v1/completions, /v1/embeddings
// in gateway mode. It selects the best node and forwards the request.
func handleGatewayRequest(w http.ResponseWriter, r *http.Request) {
	// Check hop count to prevent loops
	hopCount := 0
	if hopStr := r.Header.Get(headerRelayHop); hopStr != "" {
		hopCount, _ = strconv.Atoi(hopStr)
	}
	if hopCount >= maxRelayHops {
		writeError(w, 508, "max relay hops exceeded")
		slog.Warn("gateway loop detected", "hops", hopCount)
		return
	}

	// Read and buffer the body so we can parse model and re-send
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "failed to read request body")
		return
	}
	r.Body.Close()

	// Parse model from body
	var bodyMap map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}

	model := ""
	if rawModel, ok := bodyMap["model"]; ok {
		json.Unmarshal(rawModel, &model)
	}

	stream := false
	if rawStream, ok := bodyMap["stream"]; ok {
		json.Unmarshal(rawStream, &stream)
	}

	// D-5/S-5: Public key four-layer quota check
	authHeader := r.Header.Get("Authorization")
	bearerKey := strings.TrimPrefix(authHeader, "Bearer ")
	keyType := ClassifyKey(bearerKey)

	var reservedQuota int64
	if keyType == KeyTypePublic && publicQuota != nil {
		clientIP := ""
		if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			clientIP = ip
		}
		estimatedTokens := int64(4096) // default estimate
		if mt, ok := bodyMap["max_tokens"]; ok {
			var mtVal int64
			if json.Unmarshal(mt, &mtVal) == nil && mtVal > 0 {
				estimatedTokens = mtVal
			}
		}

		ok, reason, _ := publicQuota.ReserveQuota(clientIP, model, estimatedTokens)
		if !ok {
			writeError(w, 429, fmt.Sprintf("public key quota exceeded: %s", reason))
			return
		}
		reservedQuota = estimatedTokens
	}

	// Try to find the best node for this model
	var bestNode *RouteEntry
	if routeTable != nil && model != "" {
		bestNode = routeTable.SelectBestNode(model)
	}

	// If no node found or route table is empty, fallback to local handling
	if bestNode == nil {
		slog.Debug("gateway: no suitable node found, falling back to local", "model", model)
		if keyType == KeyTypePublic && reservedQuota > 0 && publicQuota != nil {
			clientIP := ""
			if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				clientIP = ip
			}
			defer publicQuota.AdjustQuota(clientIP, model, reservedQuota, 0)
		}
		handleGatewayFallback(w, r, bodyBytes, model, stream)
		return
	}

	// Check if the best node is ourselves — handle locally
	selfID := ""
	if netMgr != nil {
		selfID = netMgr.GetNodeID()
	}
	if bestNode.NodeID == selfID {
		slog.Debug("gateway: best node is self, handling locally", "model", model, "node_id", selfID)
		if keyType == KeyTypePublic && reservedQuota > 0 && publicQuota != nil {
			clientIP := ""
			if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				clientIP = ip
			}
			defer publicQuota.AdjustQuota(clientIP, model, reservedQuota, 0)
		}
		handleGatewayFallback(w, r, bodyBytes, model, stream)
		return
	}

	// Forward to the selected remote node
	slog.Info("gateway: routing request", "model", model, "target_node", bestNode.NodeID, "stream", stream, "hop", hopCount+1)

	// Adjust quota after remote request (estimated=reservedQuota, actual=0 for remote — will be corrected)
	if keyType == KeyTypePublic && reservedQuota > 0 && publicQuota != nil {
		clientIP := ""
		if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			clientIP = ip
		}
		defer publicQuota.AdjustQuota(clientIP, model, reservedQuota, reservedQuota/2)
	}

	gatewayForwardToRemote(w, r, bestNode, bodyBytes, hopCount, stream)
}

// handleGatewayFallback handles the request locally when no remote node is suitable.
// It re-constructs the request body and dispatches to local handlers.
func handleGatewayFallback(w http.ResponseWriter, r *http.Request, bodyBytes []byte, model string, stream bool) {
	// Reconstruct the request body
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	r.ContentLength = int64(len(bodyBytes))

	// Dispatch based on path
	switch r.URL.Path {
	case "/v1/chat/completions":
		handleChatCompletions(w, r)
	case "/v1/completions":
		// For completions, use the same chat handler path (will use local providers)
		handleChatCompletions(w, r)
	case "/v1/embeddings":
		// Embeddings: pass through to local handler if available, else error
		writeError(w, 501, "embeddings not supported in gateway fallback mode")
	default:
		writeError(w, 404, "unknown gateway endpoint")
	}
}

// gatewayForwardToRemote forwards the gateway request to a remote node.
// Supports both streaming (SSE) and non-streaming responses.
func gatewayForwardToRemote(w http.ResponseWriter, r *http.Request, entry *RouteEntry, bodyBytes []byte, hopCount int, stream bool) {
	// Pick the best address
	targetAddr := pickBestAddress(entry.Addresses)
	if targetAddr == "" {
		writeError(w, 502, "no reachable address for node")
		return
	}

	// Enforce HTTPS for relay
	if !strings.HasPrefix(targetAddr, "https://") {
		slog.Warn("gateway: relay target uses insecure protocol, rejecting", "node_id", entry.NodeID, "addr", targetAddr)
		writeError(w, 502, "relay target must use HTTPS for security")
		return
	}

	target, err := url.Parse(targetAddr)
	if err != nil {
		writeError(w, 502, "invalid target address")
		return
	}

	relayFrom := ""
	if netMgr != nil {
		relayFrom = netMgr.GetNodeID()
	}

	relayStart := time.Now()

	// Build the outbound request
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, target.String()+r.URL.Path, bytes.NewReader(bodyBytes))
	if err != nil {
		writeError(w, 500, "failed to create relay request")
		return
	}

	// Copy query parameters
	outReq.URL.RawQuery = r.URL.RawQuery

	// Copy headers but strip original Authorization (S-4/V-3)
	for key, vals := range r.Header {
		if key == "Authorization" {
			continue // do not forward consumer key to remote node
		}
		for _, val := range vals {
			outReq.Header.Add(key, val)
		}
	}

	// Set relay headers
	outReq.Header.Set(headerRelayHop, strconv.Itoa(hopCount+1))
	if relayFrom != "" {
		outReq.Header.Set(headerRelayFrom, relayFrom)
	}

	outReq.ContentLength = int64(len(bodyBytes))
	outReq.Host = target.Host

	// Execute the request
	client := GetSharedHTTPClient()
	resp, err := client.Do(outReq)
	if err != nil {
		slog.Error("gateway: relay to remote failed", "target", entry.NodeID, "addr", targetAddr, "error", err)
		if netMgr != nil {
			netMgr.RecordRelayResult(false)
		}
		if lbInstance != nil {
			lbInstance.RecordRequest(entry.NodeID, time.Since(relayStart), false)
		}
		writeError(w, 502, fmt.Sprintf("relay to %s failed: %v", entry.NodeID, err))
		return
	}
	defer resp.Body.Close()

	// Record relay result
	success := resp.StatusCode < 400
	if netMgr != nil {
		netMgr.RecordRelayResult(success)
	}
	if lbInstance != nil {
		lbInstance.RecordRequest(entry.NodeID, time.Since(relayStart), success)
	}

	// Copy response headers
	for key, vals := range resp.Header {
		for _, val := range vals {
			w.Header().Add(key, val)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream the response body back to the client
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, writeErr := w.Write(buf[:n])
			if writeErr != nil {
				slog.Debug("gateway: client disconnected during relay", "error", writeErr)
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				slog.Debug("gateway: relay body read error", "error", readErr)
			}
			return
		}
	}
}

// handleGatewayModels returns an aggregated list of all models available across the network.
// Models from all route table entries are deduplicated and merged with local models.
func handleGatewayModels(w http.ResponseWriter, r *http.Request) {
	// Collect models from all route table entries
	modelSet := make(map[string]bool)

	if routeTable != nil {
		entries := routeTable.GetAll()
		for _, entry := range entries {
			for _, m := range entry.Models {
				modelSet[m] = true
			}
		}
	}

	// Also include local models
	if pm != nil {
		localModels := pm.AllModels()
		for _, m := range localModels {
			modelSet[m.ID] = true
		}
	}

	// Build deduplicated list
	models := make([]ModelInfo, 0, len(modelSet))
	for id := range modelSet {
		models = append(models, ModelInfo{
			ID:       id,
			Object:   "model",
			Created:  time.Now().Unix(),
			OwnedBy:  "network",
		})
	}

	writeJSON(w, 200, ModelListResponse{Object: "list", Data: models})
}