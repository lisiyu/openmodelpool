package main

import (
	"log/slog"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// ============================================================
// Helpers
// ============================================================

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	if status >= 500 {
		slog.Error("upstream error", "status", status, "message", msg)
	}
	writeJSON(w, status, ErrorResponse{Error: ErrorDetail{
		Message: msg, Type: "api_error", Code: fmt.Sprintf("%d", status),
	}})
}

// readJSON decodes JSON from request body with a 1MB size limit (SA-11).
func readJSON(r *http.Request, v any) error {
	const maxBodySize = 1 << 20 // 1 MB — strict limit for all API endpoints
	limited := http.MaxBytesReader(nil, r.Body, maxBodySize)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	return decoder.Decode(v)
}

// ============================================================
// Handlers - Health & Models
// ============================================================

func handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"status":           "ok",
		"version":          AppVersion,
		"providers_enabled": len(pm.Enabled()),
		"models_available": len(pm.AllModels()),
	}
	if fed != nil && fed.IsEnabled() {
		pool := fed.GetTrustPool()
		seedCount := 0
		for _, n := range pool.Nodes {
			if n.SeedNode {
				seedCount++
			}
		}
		status["federation"] = map[string]any{
			"enabled":    true,
			"relay":      fed.IsRelayEnabled(),
			"node_id":    node.NodeID(),
			"nodes":      len(pool.Nodes),
			"seed_nodes": seedCount,
		}
	} else {
		status["federation"] = map[string]any{"enabled": false}
	}
	// P2P shared network status (Phase 1)
	if netMgr != nil {
		s := netMgr.GetStatus()
		status["network"] = map[string]any{
			"mode":    s["mode"],
			"node_id": s["node_id"],
		}
	} else {
		status["network"] = map[string]any{"mode": "personal"}
	}
	writeJSON(w, 200, status)
}

// handleVersion returns the running binary version and Go runtime version.
// It is intentionally PUBLIC (no withAuth wrapper) so monitoring/auto-update
// scripts can probe it the same way as /health.
func handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"version":    AppVersion,
		"go_version": runtime.Version(),
	})
}

func handleListModels(w http.ResponseWriter, r *http.Request) {
	keyType := RequestKeyType(r)
	models := pm.AllModelsFiltered(keyType)
	writeJSON(w, 200, ModelListResponse{Object: "list", Data: models})
}
func handleFederationStatus(w http.ResponseWriter, r *http.Request) {
	if fed == nil {
		writeJSON(w, 200, map[string]any{"enabled": false})
		return
	}

	pool := fed.GetTrustPool()
	seedCount := 0
	for _, n := range pool.Nodes {
		if n.SeedNode {
			seedCount++
		}
	}
	status := map[string]any{
		"enabled":      fed.IsEnabled(),
		"relay":        fed.IsRelayEnabled(),
		"pool_version": pool.Version,
		"total_nodes":  len(pool.Nodes),
		"seed_nodes":   seedCount,
		"active_nodes": len(fed.GetActiveNodes()),
	}

	if node != nil && node.IsInitialized() {
		info := node.GetInfo()
		status["node"] = map[string]any{
			"id":          info.NodeID,
			"pub_key":     node.PubKeyB64(),
			"github_user": info.GitHubUser,
			"joined_at":   info.JoinedAt,
		}
	}

	if repMgr != nil {
		allReps := repMgr.GetAllReputations()
		status["reputation"] = map[string]any{
			"tracked_nodes": len(allReps),
		}
	}

	if allocMgr != nil {
		status["quota_allocation"] = allocMgr.GetUsageStats()
	}

	if msgMgr != nil {
		status["messages"] = map[string]any{
			"inbox":  len(msgMgr.GetInbox(0)),
			"outbox": len(msgMgr.GetOutbox(0)),
			"unread": msgMgr.GetUnreadCount(),
		}
	}

	// Genesis hash info
	status["genesis"] = GenesisInfo()

	// DHT routing table info (Phase 3 hybrid discovery)
	status["dht"] = GetDHTStats()

	writeJSON(w, 200, status)
}

// getLocalIP returns the first non-loopback IPv4 address.
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

// handleGetFederationConfig returns the current federation configuration.
func handleGetFederationConfig(w http.ResponseWriter, r *http.Request) {
	approvalMode := cfg.Get("node_approval_mode", "auto")
	if nwm != nil {
		approvalMode = nwm.GetApprovalMode()
	}
	var tokenBudget int64
	if nwm != nil {
		tokenBudget = nwm.GetTokenBudget()
	}

	// Detect LAN IP
	lanIP := getLocalIP()
	servicePort := cfg.Get("port", "8000")

	writeJSON(w, 200, map[string]any{
		"federation_enabled":       cfg.Get("federation_enabled", "false"),
		"federation_relay_enabled": cfg.Get("federation_relay_enabled", "false"),
		"federation_registry_url":  cfg.Get("federation_registry_url", ""),
		"federation_registry_repo": cfg.Get("federation_registry_repo", "lisiyu/openmodelpool"),
		"gossip_interval_s":        cfg.Get("gossip_interval_s", "30"),
		"heartbeat_interval_s":     cfg.Get("heartbeat_interval_s", "60"),
		"tunnel_enabled":           cfg.Get("tunnel_enabled", "false"),
		"tunnel_mode":              cfg.Get("tunnel_mode", "quick"), // quick | named
		"tunnel_domain":            filterPlaceholder(cfg.Get("tunnel_domain", "")),
		"tunnel_url":               filterPlaceholder(cfg.Get("tunnel_url", "")),
		"lan_ip":                   lanIP,
		"service_port":             servicePort,
		"public_ip":                getPublicIP(),
		"bound_ip":                 cfg.Get("bound_ip", ""),
		"bound_port":              cfg.Get("bound_port", "8000"),
		"federation_doc_version":   AppVersion,                       // current doc version
		"federation_doc_read_version": cfg.Get("federation_doc_read_version", ""), // last read version
		"node_approval_mode":       cfg.Get("node_approval_mode", "auto"),
		"approval_mode":            approvalMode,
		"token_budget":             tokenBudget,
	})
}

// handleSaveFederationConfig saves federation configuration.
func handleSaveFederationConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	for _, key := range []string{
		"federation_enabled", "federation_relay_enabled",
		"federation_registry_url", "federation_registry_repo",
		"gossip_interval_s", "heartbeat_interval_s",
		"tunnel_enabled", "tunnel_mode", "tunnel_domain", "tunnel_url",
		"federation_doc_read_version", "node_approval_mode",
	} {
		if v, ok := body[key]; ok {
			cfg.Set(key, v)
		}
	}
	cfg.save()

	// Apply federation config changes to running instance
	if fed != nil {
		fed.mu.Lock()
		fed.enabled = cfg.Get("federation_enabled", "false") == "true"
		fed.relayEnabled = cfg.Get("federation_relay_enabled", "false") == "true"
		fed.mu.Unlock()
	}

	// Apply tunnel config changes
	applyTunnelConfig()

	// Broadcast config update via SSE
	BroadcastConfigUpdate("federation")

	writeJSON(w, 200, map[string]string{"status": "saved"})
}

// handleInitNode initializes the node identity with GitHub info.
func handleInitNode(w http.ResponseWriter, r *http.Request) {
	if node == nil {
		writeError(w, 500, "node not initialized")
		return
	}

	var body struct {
		GitHubUser string `json:"github_user"`
		GitHubID   int64  `json:"github_id"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if body.GitHubUser == "" {
		writeError(w, 400, "github_user is required")
		return
	}

	node.SetGitHub(body.GitHubUser, body.GitHubID)
	node.save()

	writeJSON(w, 200, map[string]any{
		"node_id":     node.NodeID(),
		"pub_key":     node.PubKeyB64(),
		"github_user": body.GitHubUser,
	})
}

// handleGetNodeWeights returns all per-node weight overrides.
func handleGetNodeWeights(w http.ResponseWriter, r *http.Request) {
	if nwm == nil {
		writeJSON(w, 200, map[string]any{"overrides": []any{}, "approval_mode": "auto"})
		return
	}
	overrides := nwm.GetOverrides()
	if overrides == nil {
		overrides = []*NodeWeightOverride{}
	}
	writeJSON(w, 200, map[string]any{
		"overrides":     overrides,
		"approval_mode": nwm.GetApprovalMode(),
		"token_budget":  nwm.GetTokenBudget(),
	})
}

// handleSetNodeWeight sets a per-node weight multiplier.
func handleSetNodeWeight(w http.ResponseWriter, r *http.Request) {
	if nwm == nil {
		writeError(w, 500, "node weight manager not initialized")
		return
	}
	var body struct {
		NodeID string  `json:"node_id"`
		Weight float64 `json:"weight"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.NodeID == "" {
		writeError(w, 400, "node_id is required")
		return
	}
	if body.Weight < 0 {
		writeError(w, 400, "weight must be >= 0")
		return
	}

	req := nwm.SetOverride(body.NodeID, body.Weight)
	resp := map[string]any{
		"node_id":  body.NodeID,
		"weight":   body.Weight,
		"approved": nwm.GetApprovalMode() == "auto" || (node != nil && body.NodeID == node.NodeID()),
	}
	if req != nil {
		resp["approval_request"] = req
		resp["approved"] = false
	}
	writeJSON(w, 200, resp)
}

// handleGetApprovals returns pending or all approval requests.
func handleGetApprovals(w http.ResponseWriter, r *http.Request) {
	if nwm == nil {
		writeJSON(w, 200, map[string]any{"pending": []any{}, "all": []any{}})
		return
	}
	pendingOnly := r.URL.Query().Get("pending") == "true"
	if pendingOnly {
		reqs := nwm.GetPendingRequests()
		if reqs == nil {
			reqs = []*ApprovalRequest{}
		}
		writeJSON(w, 200, map[string]any{"pending": reqs})
	} else {
		reqs := nwm.GetAllRequests()
		if reqs == nil {
			reqs = []*ApprovalRequest{}
		}
		writeJSON(w, 200, map[string]any{"all": reqs})
	}
}

// handleResolveApproval approves or rejects a pending approval request.
func handleResolveApproval(w http.ResponseWriter, r *http.Request) {
	if nwm == nil {
		writeError(w, 500, "node weight manager not initialized")
		return
	}
	var body struct {
		RequestID string `json:"request_id"`
		Approve   bool   `json:"approve"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.RequestID == "" {
		writeError(w, 400, "request_id is required")
		return
	}
	if err := nwm.ResolveApproval(body.RequestID, body.Approve); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "resolved", "request_id": body.RequestID, "approved": body.Approve})
}

// handleSetTokenBudget sets this node's declared token budget.
func handleSetTokenBudget(w http.ResponseWriter, r *http.Request) {
	if nwm == nil {
		writeError(w, 500, "node weight manager not initialized")
		return
	}
	var body struct {
		Budget int64 `json:"budget"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Budget < 0 {
		writeError(w, 400, "budget must be >= 0")
		return
	}
	nwm.SetTokenBudget(body.Budget)
	writeJSON(w, 200, map[string]any{"token_budget": body.Budget})
}

// handleJoinNetwork processes a node join request (Genesis Hash verification).
func handleJoinNetwork(w http.ResponseWriter, r *http.Request) {
	var req NodeJoinRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	resp := HandleJoinRequest(req)
	status := 200
	if !resp.Accepted {
		status = 403
	}
	writeJSON(w, status, resp)
}

// handleGetGenesis returns the genesis configuration (public endpoint).
func handleGetGenesis(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, GenesisInfo())
}

// handleCreateInvite creates a new signed invite code.
func handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	if invMgr == nil {
		writeError(w, 500, "invite manager not initialized")
		return
	}
	var body struct {
		InviteePub  string `json:"invitee_pub"`   // public key or "*" for public
		InviteeName string `json:"invitee_name"`  // optional display name
		Type        string `json:"type"`          // directed, public, chain
		ExpiresIn   int    `json:"expires_hours"` // hours until expiration, default 168 (7 days)
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.InviteePub == "" {
		body.InviteePub = "*" // default to public invite
	}
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 168 // 7 days
	}
	inviteType := FederationInviteType(body.Type)
	switch inviteType {
	case FederationInviteDirected, FederationInvitePublic, FederationInviteChain:
	default:
		inviteType = FederationInvitePublic
	}

	invite, err := invMgr.CreateInvite(body.InviteePub, body.InviteeName, inviteType, body.ExpiresIn)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	encoded, _ := EncodeInvite(invite)
	writeJSON(w, 200, map[string]any{
		"invite":  invite,
		"encoded": encoded,
	})
}

// handleListInvites returns all issued invites.
func handleListInvites(w http.ResponseWriter, r *http.Request) {
	if invMgr == nil {
		writeJSON(w, 200, map[string]any{"invites": []any{}})
		return
	}
	invites := invMgr.GetInvites()
	if invites == nil {
		invites = []*FederationInvite{}
	}
	writeJSON(w, 200, map[string]any{"invites": invites})
}

// handleVerifyInvite verifies an invite code (public endpoint for new nodes).
func handleVerifyInvite(w http.ResponseWriter, r *http.Request) {
	if invMgr == nil {
		writeError(w, 500, "invite manager not initialized")
		return
	}
	var body struct {
		Encoded string `json:"encoded"` // base64-encoded invite
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	invite, err := DecodeInvite(body.Encoded)
	if err != nil {
		writeError(w, 400, fmt.Sprintf("invalid invite: %v", err))
		return
	}

	err = invMgr.VerifyInvite(invite)
	if err != nil {
		writeJSON(w, 200, map[string]any{
			"valid":  false,
			"reason": err.Error(),
		})
		return
	}

	writeJSON(w, 200, map[string]any{
		"valid":     true,
		"inviter":   invite.Inviter,
		"endpoint":  invite.Endpoint,
		"network":   invite.NetworkID,
		"type":      invite.Type,
		"expires":   invite.ExpiresAt,
	})
}

// ============================================================
// Handlers - Chat Completions (core)
// ============================================================

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, 400, "messages cannot be empty")
		return
	}

	consumerID := getRequestOwner(r) // "" = admin
	model := req.Model
	stream := req.Stream

	// Build extra params
	extra := make(map[string]any)
	if req.Temperature != nil {
		extra["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		extra["top_p"] = *req.TopP
	}
	if req.MaxTokens != nil {
		extra["max_tokens"] = *req.MaxTokens
	}
	for k, v := range req.Extra {
		extra[k] = v
	}

	// Coze-specific routing
	if strings.HasPrefix(model, "coze-") {
		handleCozeRequest(w, r, model, req.Messages, stream, extra)
		return
	}

	// Determine key type
	keyType := RequestKeyType(r)
	// Map keyType to access type for stats
	accessType := "private"
	switch keyType {
	case "public":
		accessType = "public"
	case "guest":
		accessType = "guest"
	}

	// D-4: Per-Key local quota check for Guest Keys
	if keyType == "guest" && guestKeyUsage != nil && guestKeyStore != nil {
		auth := r.Header.Get("Authorization")
		guestKey := strings.TrimPrefix(auth, "Bearer ")
		record := guestKeyStore.GetGuestKeyRecord(guestKey)
		if record != nil && record.Quota > 0 {
			estimated := int64(4096)
			if req.MaxTokens != nil && *req.MaxTokens > 0 {
				estimated = int64(*req.MaxTokens)
			}
			allowed, _ := guestKeyUsage.CheckAndReserve(guestKey, record.Quota, estimated)
			if !allowed {
				writeError(w, 429, "该 Guest Key 的本地额度已用尽")
				return
			}
			// Adjust after request — deferred
			defer func() {
				// For streaming, actual usage is unknown; estimate as 0 adjustment (reserve stands)
				guestKeyUsage.Adjust(guestKey, estimated, 0)
			}()
		}
	}

	// Smart routing with fallback — uses the unified pool (all providers from all users)
	routingMode := cfg.Get("routing_mode", "priority")
	allCandidates := pm.OrderedCandidates(model, routingMode)

	// Provider access control: filter candidates based on key type
	candidates := FilterByAccessControl(allCandidates, keyType)

	// D-2/D-3: "先本地后池" routing for Guest Key and Proxy API Key
	// Try local providers first (providers on this node), then fall back to full pool
	if (keyType == "guest" || keyType == "proxy") && pm != nil {
		localCandidates := filterLocalOnly(candidates)
		if len(localCandidates) > 0 {
			candidates = localCandidates
		}
		// If no local candidates, keep the full filtered list (fallback to pool)
	}

	if len(candidates) == 0 {
		models := pm.AllModels()
		var names []string
		for i, m := range models {
			if i >= 20 {
				break
			}
			names = append(names, m.ID)
		}
		hint := ""
		if len(names) > 0 {
			hint = ", available models: " + strings.Join(names, ", ")
		}
		writeError(w, 404, fmt.Sprintf("no provider found for model '%s'%s", model, hint))
		return
	}

	var lastErr error
	for idx, c := range candidates {
		p := c.Provider
		actualModel := c.Model

		if idx > 0 {
			slog.Warn("fallback", "model", model, "to", p.Name, "idx", idx, "mode", routingMode)
		}

		// Resolve multi-key: populate legacy APIKey field from APIKeys array
		if p.APIKey == "" && len(p.APIKeys) > 0 {
			p.APIKey = p.GetEffectiveAPIKey()
		}
		if p.APIKey == "" {
			lastErr = fmt.Errorf("provider '%s' has no API key", p.Name)
			continue
		}

		startTime := time.Now()

		if stream {
			IncrProviderConn(p.ID)
			dataSent, err := handleStreamProxy(w, p, actualModel, req.Messages, extra, model, startTime, accessType)
			DecrProviderConn(p.ID)
			if err == nil {
				if consumerID != "" {
					multiUser.RecordConsumerUsage(consumerID, 0)
				}
				return
			}
			// If data was already sent to client, cannot retry with another provider
			if dataSent {
				slog.Error("stream failed after data sent", "provider", p.Name, "error", err)
				return
			}
			// No data sent yet — safe to try next provider
			slog.Warn("stream failed before data sent, trying next provider", "provider", p.Name, "error", err)
			lastErr = err
		} else {
			IncrProviderConn(p.ID)
			resp, err := doNonStream(p, actualModel, req.Messages, extra)
			DecrProviderConn(p.ID)
			if err != nil {
				slog.Warn("non-stream provider failed", "provider", p.Name, "model", actualModel, "error", err)
				lastErr = err
				tracker.RecordWithAccessType(p.ID, p.Name, model, 0, 0, float64(time.Since(startTime).Milliseconds()), false, err.Error(), false, 0, accessType)
				continue
			}
			resp.Model = model
			latencyMS := float64(time.Since(startTime).Milliseconds())
			var promptTok, compTok int
			if resp.Usage != nil {
				promptTok = resp.Usage.PromptTokens
				compTok = resp.Usage.CompletionTokens
			}
			tracker.RecordWithAccessType(p.ID, p.Name, model, promptTok, compTok, latencyMS, true, "", false, 0, accessType)
			if consumerID != "" {
				multiUser.RecordConsumerUsage(consumerID, promptTok+compTok)
			}
			writeJSON(w, 200, resp)
			return
		}
	}

	writeError(w, 502, fmt.Sprintf("all providers failed: %v", lastErr))
}

// handleStreamProxy handles streaming requests. Returns (dataSent bool, err error).
// If dataSent is true, the response headers have been written and retry is not possible.
func handleStreamProxy(w http.ResponseWriter, p Provider, model string, messages []ChatMessage, extra map[string]any, origModel string, startTime time.Time, accessType string) (bool, error) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return false, fmt.Errorf("streaming not supported")
	}

	sw := &streamWriter{w: w, flusher: flusher}
	err := doStream(p, model, messages, extra, sw)

	latencyMS := float64(time.Since(startTime).Milliseconds())
	if err != nil {
		tracker.RecordWithAccessType(p.ID, p.Name, origModel, 0, 0, latencyMS, false, err.Error(), true, 0, accessType)
		return sw.bytesWritten > 0, err
	}
	tracker.RecordWithAccessType(p.ID, p.Name, origModel, 0, 0, latencyMS, true, "", true, 0, accessType)
	return sw.bytesWritten > 0, nil
}

type streamWriter struct {
	w            http.ResponseWriter
	flusher      http.Flusher
	bytesWritten int64
}

func (s *streamWriter) Write(p []byte) (n int, err error) {
	n, err = s.w.Write(p)
	s.bytesWritten += int64(n)
	s.flusher.Flush()
	return
}

func handleCozeRequest(w http.ResponseWriter, r *http.Request, model string, messages []ChatMessage, stream bool, extra map[string]any) {
	// Get coze provider or use a synthetic one
	p, _ := pm.GetRaw("coze")

	// Resolve multi-key
	if p.APIKey == "" && len(p.APIKeys) > 0 {
		p.APIKey = p.GetEffectiveAPIKey()
	}
	// Fall back to global config for backward compatibility
	if p.APIKey == "" {
		p.APIKey = cfg.Get("coze_api_token", "")
	}
	if p.APIKey == "" {
		writeError(w, 500, "Coze API token not configured (set API Key in provider config)")
		return
	}

	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, _ := w.(http.Flusher)
		sw := &streamWriter{w: w, flusher: flusher}
		cozeStream(p, model, messages, sw)
		return
	}

	resp, err := cozeNonStream(p, model, messages)
	if err != nil {
		writeError(w, 502, fmt.Sprintf("Coze error: %v", err))
		return
	}
	writeJSON(w, 200, resp)
}

// filterLocalOnly filters candidates to only include providers from this node.
// Currently all providers in pm are local, so this is a passthrough.
// D-2/D-3: Ensures Guest Key and Proxy API Key requests prioritize local providers
// before falling back to pool resources.
func filterLocalOnly(cands []candidate) []candidate {
	// All providers managed by pm are local to this node.
	// This function serves as an explicit filter for the "local-first" routing policy.
	// If future changes introduce remote/virtual providers into the candidate list,
	// this function should filter them out by checking provider origin.
	local := make([]candidate, 0, len(cands))
	for _, c := range cands {
		// All pm providers are local (they have a local ID and local API keys)
		if c.Provider.ID != "" {
			local = append(local, c)
		}
	}
	return local
}

// getPublicIP returns the server public IP.
func getPublicIP() string {
	return detectPublicIP()
}

// filterPlaceholder returns empty string for known placeholder values.
func filterPlaceholder(s string) string {
	if s == "" || s == "api.example.com" || s == "https://api.example.com" {
		return ""
	}
	return s
}
