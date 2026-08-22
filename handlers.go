package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ============================================================
// Helpers
// ============================================================

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")   // B4: prevent MIME sniffing
	w.Header().Set("X-Frame-Options", "DENY")             // B4: prevent clickjacking
	w.Header().Set("Cache-Control", "no-store")           // B4: prevent caching of API responses
	w.Header().Set("Content-Security-Policy", "default-src 'none'") // F12: CSP for API responses
	w.Header().Set("Referrer-Policy", "no-referrer")              // F14: prevent referrer leak
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()") // F15
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
// m6-fix: Pass w to MaxBytesReader so it can auto-send 413 on overflow.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	// B83: Validate Content-Type for POST/PUT/PATCH requests
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		ct := r.Header.Get("Content-Type")
		if ct != "" && !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "multipart/form-data") {
			writeError(w, 415, "Content-Type must be application/json")
			return fmt.Errorf("unsupported Content-Type: %s", ct)
		}
	}
	const maxBodySize = 1 << 20 // 1 MB — strict limit for all API endpoints
	limited := http.MaxBytesReader(w, r.Body, maxBodySize)
	defer limited.Close()
	decoder := json.NewDecoder(limited)
	return decoder.Decode(v)
}

// ============================================================
// Handlers - Health & Models
// ============================================================

func handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"status":            "ok",
		"version":           AppVersion,
		"providers_enabled": len(pm.Enabled()),
		"models_available":  len(pm.AllModels()),
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
	// F3: Encryption and config health
	if enc != nil {
		status["encryption"] = map[string]any{
			"ready":      !enc.IsEphemeral(),
			"ephemeral":  enc.IsEphemeral(),
		}
	}
	// Uptime
	if metrics != nil {
		status["uptime_seconds"] = time.Since(metrics.startTime).Seconds()
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

// isPrivateIPv4 reports whether the IPv4 address belongs to an RFC1918 private
// LAN range: 10.0.0.0/8, 172.16.0.0/12, or 192.168.0.0/16. These are the
// addresses typically used by home/office routers for LAN access.
func isPrivateIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	if v4[0] == 10 {
		return true
	}
	if v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31 {
		return true
	}
	if v4[0] == 192 && v4[1] == 168 {
		return true
	}
	return false
}

// isUsableLANIP reports whether the address is a usable, displayable LAN
// address. It rejects loopback (127.0.0.0/8), link-local/APIPA
// (169.254.0.0/16 for IPv4 and fe80::/10 for IPv6), multicast, unspecified
// (0.0.0.0) and any non-IPv4 address.
func isUsableLANIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsLoopback() {
		return false
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsMulticast() {
		return false
	}
	if ip.Equal(net.IPv4bcast) {
		return false // 255.255.255.255 limited broadcast
	}
	// The LAN access URL only supports IPv4.
	return ip.To4() != nil
}

// pickLANIP selects the best LAN IPv4 address from a list of candidate IPs. It
// prefers RFC1918 private ranges and falls back to any other usable
// global-unicast IPv4 address. This is the deterministic, dependency-free core
// of LAN IP detection and is covered by unit tests with injected candidates.
func pickLANIP(ips []net.IP) string {
	var fallback net.IP
	for _, ip := range ips {
		if !isUsableLANIP(ip) {
			continue
		}
		if isPrivateIPv4(ip) {
			return ip.String()
		}
		if fallback == nil {
			fallback = ip
		}
	}
	if fallback != nil {
		return fallback.String()
	}
	return ""
}

// getLocalIP returns the best local LAN IPv4 address for display. It enumerates
// all up, non-loopback network interfaces, collects their IPv4 addresses and
// delegates selection to pickLANIP (which prefers RFC1918 private ranges and
// filters out loopback, APIPA/link-local 169.254.0.0/16, multicast and
// unspecified addresses). The behaviour is identical across Windows, Linux and
// macOS.
func getLocalIP() string {
	ifaces, err := net.Interfaces()
	if err != nil || len(ifaces) == 0 {
		return legacyLocalIP()
	}
	ips := make([]net.IP, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			ips = append(ips, ip)
		}
	}
	return pickLANIP(ips)
}

// legacyLocalIP is a minimal fallback used when interface enumeration fails. It
// mirrors the previous first-non-loopback behaviour but still routes candidates
// through pickLANIP so APIPA/link-local addresses are filtered out.
func legacyLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			ips = append(ips, ipnet.IP)
		}
	}
	return pickLANIP(ips)
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
		"federation_enabled":          cfg.Get("federation_enabled", "false"),
		"federation_relay_enabled":    cfg.Get("federation_relay_enabled", "false"),
		"federation_registry_url":     cfg.Get("federation_registry_url", ""),
		"federation_registry_repo":    cfg.Get("federation_registry_repo", "lisiyu/openmodelpool"),
		"gossip_interval_s":           cfg.Get("gossip_interval_s", "30"),
		"heartbeat_interval_s":        cfg.Get("heartbeat_interval_s", "60"),
		"tunnel_enabled":              cfg.Get("tunnel_enabled", "false"),
		"tunnel_mode":                 cfg.Get("tunnel_mode", "quick"), // quick | named
		"tunnel_domain":               filterPlaceholder(cfg.Get("tunnel_domain", "")),
		"tunnel_url":                  filterPlaceholder(cfg.Get("tunnel_url", "")),
		"lan_ip":                      lanIP,
		"service_port":                servicePort,
		"public_ip":                   getPublicIP(),
		"bound_ip":                    cfg.Get("bound_ip", ""),
		"bound_port":                  cfg.Get("bound_port", "8000"),
		"federation_doc_version":      AppVersion,                                 // current doc version
		"federation_doc_read_version": cfg.Get("federation_doc_read_version", ""), // last read version
		"node_approval_mode":          cfg.Get("node_approval_mode", "auto"),
		"approval_mode":               approvalMode,
		"token_budget":                tokenBudget,
	})
}

// handleSaveFederationConfig saves federation configuration.
func handleSaveFederationConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := readJSON(w, r, &body); err != nil {
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
	if err := readJSON(w, r, &body); err != nil {
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
	if err := readJSON(w, r, &body); err != nil {
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
	if err := readJSON(w, r, &body); err != nil {
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
	if err := readJSON(w, r, &body); err != nil {
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
	if err := readJSON(w, r, &req); err != nil {
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
	if err := readJSON(w, r, &body); err != nil {
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

	invite, err := invMgr.CreateInvite(body.InviteePub, body.InviteeName, inviteType, body.ExpiresIn, r.Host)
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
	if err := readJSON(w, r, &body); err != nil {
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
		"valid":    true,
		"inviter":  invite.Inviter,
		"endpoint": invite.Endpoint,
		"network":  invite.NetworkID,
		"type":     invite.Type,
		"expires":  invite.ExpiresAt,
	})
}

// ============================================================
// Handlers - Chat Completions (core)
// ============================================================

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := readJSON(w, r, &req); err != nil {
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

	// P3-2: abuse guard for the global public key on the DIRECT request path.
	// Reuses the existing per-IP four-layer PublicKeyQuota so a single abuser
	// cannot monopolize the community free pool. (The relay/cross-node path in
	// network_relay.go already applies the same guard; this closes the gap for
	// requests served directly by this node.)
	//
	// P2-3(ii): skip when the gateway already accounted for this request
	// (marker set by handleGatewayRequest, which strips any client-supplied
	// value first). Without this check a request arriving through the gateway
	// and falling back to local handling would be charged twice against the
	// same per-IP cap, and a contributor drawing on its own entitlement would
	// still be throttled at the anonymous rate.
	if keyType == "public" && publicQuota != nil && r.Header.Get(headerQuotaCharged) == "" {
		clientIP := extractClientIP(r.RemoteAddr)
		estTokens := int64(4096)
		if req.MaxTokens != nil && *req.MaxTokens > 0 {
			estTokens = int64(*req.MaxTokens)
		}
		if ok, reason, _ := publicQuota.ReserveQuota(clientIP, model, estTokens); !ok {
			writeError(w, 429, fmt.Sprintf("public free pool quota exceeded: %s", reason))
			return
		}
		defer publicQuota.AdjustQuota(clientIP, model, estTokens, 0)
	}

	// D-4: Per-Key local quota check for Guest Keys
	if keyType == "guest" && guestKeyUsage != nil && guestKeyStore != nil {
		auth := r.Header.Get("Authorization")
		guestKey := strings.TrimPrefix(auth, "Bearer ")
		// P1-5: on a relay-dispatched request handleRelayToLocal stripped the
		// Authorization header and carries the verified key via context —
		// otherwise the per-key quota would be silently bypassed over the relay
		// path. Prefer the context key, fall back to the header for direct calls.
		if ctxKey := relayGuestKey(r); ctxKey != "" {
			guestKey = ctxKey
		}
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

	// G6: Cross-pool consumption priority (private -> shared -> remote_shared).
	// For Guest / Admin(Proxy) keys, deduct from the highest-priority pool that
	// still has capacity. Enforcement is opt-in (quota_priority_enabled): the
	// whole block below is gated on quotaPriorityMgr.enabled. When the flag is
	// false (the default) the block is skipped ENTIRELY — no X-Quota-Pool header
	// is written and control flows straight to provider selection, which is
	// exactly the pre-G6 external behavior (zero wire impact). Only when enabled
	// do we deduct from a pool, write X-Quota-Pool, and return 429 once all
	// three pools (private / shared / remote_shared) are exhausted.
	if (keyType == "guest" || keyType == "proxy" || keyType == "admin") && quotaPriorityMgr != nil && quotaPriorityMgr.enabled {
		estimate := int64(4096)
		if req.MaxTokens != nil && *req.MaxTokens > 0 {
			estimate = int64(*req.MaxTokens)
		}
		qres := quotaPriorityMgr.Resolve(keyTypeFromString(keyType), estimate)
		// Surface which pool was charged for observability ("返回实际扣自哪个池").
		w.Header().Set("X-Quota-Pool", qres.Kind.String())
		if !qres.OK {
			writeError(w, 429, "额度耗尽：私有/共享/他节点共享池均不足")
			return
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
		// UX-P2-14: generic error — do not leak the full local model list to a
		// remote/unknown caller.
		writeError(w, 404, fmt.Sprintf("no provider available for model '%s'", model))
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
			IncrConn(p.ID, accessType)
			dataSent, err := handleStreamProxy(w, r, p, actualModel, req.Messages, extra, model, startTime, accessType, consumerID)
			DecrConn(p.ID, accessType)
			if err == nil {
				recordProviderSuccess(p.ID) // B7-3
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
			if isRateLimitError(err) {
				recordProviderCooldown(p.ID)
			} else {
				recordProviderFailure(p.ID) // B7-3
			}
		} else {
			IncrConn(p.ID, accessType)
			resp, err := doNonStream(r.Context(), p, actualModel, req.Messages, extra)
			DecrConn(p.ID, accessType)
			if err != nil {
				slog.Warn("non-stream provider failed", "provider", p.Name, "model", actualModel, "error", err)
				lastErr = err
				if isRateLimitError(err) {
					recordProviderCooldown(p.ID)
				} else {
					recordProviderFailure(p.ID) // B7-3
				}
				tracker.RecordWithOwner(p.ID, p.Name, model, 0, 0, float64(time.Since(startTime).Milliseconds()), false, err.Error(), false, 0, accessType, consumerID)
				continue
			}
			resp.Model = model
			latencyMS := float64(time.Since(startTime).Milliseconds())
			var promptTok, compTok int
			if resp.Usage != nil {
				promptTok = resp.Usage.PromptTokens
				compTok = resp.Usage.CompletionTokens
			}
			recordProviderSuccess(p.ID) // B7-3
			tracker.RecordWithOwner(p.ID, p.Name, model, promptTok, compTok, latencyMS, true, "", false, 0, accessType, consumerID)
			if consumerID != "" {
				multiUser.RecordConsumerUsage(consumerID, promptTok+compTok)
			}
			writeJSON(w, 200, resp)
			return
		}
	}

	if lastErr != nil && isRateLimitError(lastErr) {
		// Rate-limited upstream: surface 429 so the client knows to back off
		// and retry later (instead of a generic 502).
		writeError(w, 429, "上游限流，请稍后重试 (rate limited)")
		return
	}

	// B7-b: lastErr can embed upstream internals (URLs, account ids, provider
	// responses). Full detail is already in the logs per-provider above; give
	// clients a generic reason so public/guest consumers learn nothing.
	writeError(w, 502, "all providers failed, please retry later")
}

// handleStreamProxy handles streaming requests. Returns (dataSent bool, err error).
// If dataSent is true, the response headers have been written and retry is not possible.
func handleStreamProxy(w http.ResponseWriter, r *http.Request, p Provider, model string, messages []ChatMessage, extra map[string]any, origModel string, startTime time.Time, accessType string, owner string) (bool, error) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return false, fmt.Errorf("streaming not supported")
	}

	sw := &streamWriter{w: w, flusher: flusher}
	err := doStream(r.Context(), p, model, messages, extra, sw)

	latencyMS := float64(time.Since(startTime).Milliseconds())
	if err != nil {
		tracker.RecordWithOwner(p.ID, p.Name, origModel, 0, 0, latencyMS, false, err.Error(), true, 0, accessType, owner)
		return sw.bytesWritten > 0, err
	}
	tracker.RecordWithOwner(p.ID, p.Name, origModel, 0, 0, latencyMS, true, "", true, 0, accessType, owner)
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
		cozeStream(r.Context(), p, model, messages, sw)
		return
	}

	resp, err := cozeNonStream(r.Context(), p, model, messages)
	if err != nil {
		// B7-b: don't echo upstream Coze error text to the client.
		slog.Warn("coze request failed", "model", model, "error", err)
		writeError(w, 502, "Coze upstream error, please retry later")
		return
	}
	writeJSON(w, 200, resp)
}

// filterLocalOnly filters candidates to only include providers from this node.
// B8-fix: Properly filter by Owner — empty Owner means local admin/system provider.
// D-2/D-3: Ensures Guest Key and Proxy API Key requests prioritize local providers
// before falling back to pool resources.
func filterLocalOnly(cands []candidate) []candidate {
	local := make([]candidate, 0, len(cands))
	for _, c := range cands {
		// Local providers have empty Owner (admin/system) or match this node's ID
		if c.Provider.Owner == "" || c.Provider.Owner == node.NodeID() {
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

// ============================================================
// Handlers - Network Identity (Phase 2 切片②)
// 显式编排的身份生命周期：generate → confirm-backup → enable / restore
// ============================================================

// handleNetworkIdentityGenerate generates a new mnemonic-based identity.
// POST /api/network/identity/generate  body: {"word_count": 12|24}
// Returns the plaintext mnemonic (held in memory only on the client side).
// REQ-S2-2: the frontend shows the mnemonic exactly once; the server never
// persists or re-sends it after backup is confirmed.
func handleNetworkIdentityGenerate(w http.ResponseWriter, r *http.Request) {
	if node == nil {
		writeError(w, 500, "节点身份模块未初始化")
		return
	}
	// REQ-S2-6: once backup is confirmed the identity is locked — refuse to
	// regenerate (would overwrite the user's only recovery seed).
	if node.IsBackupConfirmed() {
		writeError(w, 409, "已完成助记词备份确认，无法重新生成身份；如需更换身份请退出共享网络后重试")
		return
	}
	var body struct {
		WordCount int `json:"word_count"`
	}
	_ = readJSON(w, r, &body)
	if body.WordCount != 24 {
		body.WordCount = 12 // default 12
	}
	mnemonic, err := node.GenerateWithMnemonic(body.WordCount)
	if err != nil {
		// SEC-P3-24: do not leak the underlying error (path, key material
		// details) to the client.
		slog.Error("network identity generate failed", "error", err)
		writeError(w, 400, "生成助记词失败，请重试")
		return
	}
	writeJSON(w, 200, map[string]any{
		"mnemonic":             mnemonic,
		"word_count":           body.WordCount,
		"backup_confirmed":     false,
		"node_id":              node.NodeID(),
		"identity_initialized": true,
	})
}

// handleNetworkIdentityConfirmBackup marks the mnemonic backup as confirmed.
// POST /api/network/identity/confirm-backup  body: {}
// REQ-S2-2/6: clears the in-memory mnemonic and persists backup_confirmed=true.
func handleNetworkIdentityConfirmBackup(w http.ResponseWriter, r *http.Request) {
	if node == nil {
		writeError(w, 500, "节点身份模块未初始化")
		return
	}
	if !node.IsInitialized() {
		writeError(w, 400, "请先生成或恢复助记词以创建节点身份")
		return
	}
	// ConfirmBackup is idempotent and safe to call repeatedly.
	node.ConfirmBackup()
	writeJSON(w, 200, map[string]any{
		"backup_confirmed": true,
		"node_id":          node.NodeID(),
	})
}

// handleNetworkIdentityRestore restores an identity from an existing mnemonic.
// POST /api/network/identity/restore  body: {"mnemonic": "..."}
// REQ-S2-4: same mnemonic always derives the same Node ID (deterministic).
func handleNetworkIdentityRestore(w http.ResponseWriter, r *http.Request) {
	if node == nil {
		writeError(w, 500, "节点身份模块未初始化")
		return
	}
	var body struct {
		Mnemonic string `json:"mnemonic"`
	}
	if err := readJSON(w, r, &body); err != nil || strings.TrimSpace(body.Mnemonic) == "" {
		writeError(w, 400, "请提供有效的助记词")
		return
	}
	if err := node.RestoreFromMnemonic(body.Mnemonic); err != nil {
		// REQ-S2-7: clear, actionable message for the recovery failure path.
		writeError(w, 400, "助记词无效，请检查每个单词拼写后重试")
		return
	}
	writeJSON(w, 200, map[string]any{
		"node_id":          node.NodeID(),
		"backup_confirmed": true,
		"restored":         true,
	})
}

// handleDiagnostics returns comprehensive system diagnostics for troubleshooting.
// F16: Consolidated diagnostic endpoint for admin/support use.
func handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	diag := map[string]any{}

	// Runtime info
	diag["version"] = AppVersion
	diag["go_version"] = runtime.Version()
	diag["goroutines"] = runtime.NumGoroutine()
	if metrics != nil {
		diag["uptime_seconds"] = time.Since(metrics.startTime).Seconds()
	}

	// Memory stats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	diag["memory"] = map[string]any{
		"alloc_mb":       m.Alloc / 1024 / 1024,
		"total_alloc_mb": m.TotalAlloc / 1024 / 1024,
		"sys_mb":         m.Sys / 1024 / 1024,
		"gc_count":       m.NumGC,
	}

	// Provider health
	if healthChecker != nil {
		healthy := 0
		unhealthy := 0
		for _, h := range healthChecker.GetHealth() {
			if h.Status == "healthy" {
				healthy++
			} else {
				unhealthy++
			}
		}
		diag["providers"] = map[string]any{
			"healthy":   healthy,
			"unhealthy": unhealthy,
		}
	}

	// Network status
	if netMgr != nil {
		s := netMgr.GetStatus()
		diag["network"] = s
	}

	// Encryption status
	if enc != nil {
		diag["encryption"] = map[string]any{
			"ready":     enc.IsReady(),
			"ephemeral": enc.IsEphemeral(),
		}
	}

	// Connection tracker
	diag["connections"] = GetConnStats()

	writeJSON(w, 200, diag)
}

// handleSecurityCheck returns a security assessment of the current configuration.
// F25: Security audit endpoint for admin dashboard.
func handleSecurityCheck(w http.ResponseWriter, r *http.Request) {
	findings := []map[string]any{}

	// Check 1: Default admin password
	if auth != nil {
		info := auth.AdminInfo()
		if info["username"] == "admin" {
			findings = append(findings, map[string]any{
				"severity": "medium",
				"category": "authentication",
				"message":  "Default admin username detected",
				"detail":   "Change the default 'admin' username to reduce brute-force attack surface",
			})
		}
	}

	// Check 2: HTTPS enforcement
	if cfg != nil {
		publicURL := cfg.Get("public_url", "")
		if publicURL != "" && !strings.HasPrefix(publicURL, "https://") {
			findings = append(findings, map[string]any{
				"severity": "high",
				"category": "transport",
				"message":  "Public URL uses HTTP instead of HTTPS",
				"detail":   "Set public_url to an HTTPS URL to protect data in transit",
			})
		}
	}

	// Check 3: Encryption status
	if enc != nil {
		if enc.IsEphemeral() {
			findings = append(findings, map[string]any{
				"severity": "high",
				"category": "encryption",
				"message":  "Using ephemeral encryption key",
				"detail":   "Encryption key could not be persisted; encrypted data will be lost on restart",
			})
		}
	}

	// Check 4: Rate limiting
	if rateLimiter == nil {
		findings = append(findings, map[string]any{
			"severity": "medium",
			"category": "rate_limiting",
			"message":  "Rate limiting is not enabled",
			"detail":   "Configure rate_limit_global and rate_limit_per_consumer to prevent abuse",
		})
	}

	// Check 5: WAF
	if wafEngine == nil {
		findings = append(findings, map[string]any{
			"severity": "low",
			"category": "waf",
			"message":  "WAF is not enabled",
			"detail":   "Enable WAF for additional request filtering and IP blacklisting",
		})
	}

	// Check 6: Provider API key exposure
	if pm != nil {
		emptyKeys := 0
		for _, p := range pm.GetAll() {
			if p.APIKey == "" {
				emptyKeys++
			}
		}
		if emptyKeys > 0 {
			findings = append(findings, map[string]any{
				"severity": "low",
				"category": "providers",
				"message":  fmt.Sprintf("%d provider(s) have empty API keys", emptyKeys),
				"detail":   "Providers without API keys will fail on requests",
			})
		}
	}

	// Check 7: Data directory permissions
	if info, err := os.Stat("data"); err == nil {
		if info.Mode().Perm()&0077 != 0 {
			findings = append(findings, map[string]any{
				"severity": "medium",
				"category": "file_permissions",
				"message":  "Data directory has overly permissive access",
				"detail":   fmt.Sprintf("Data directory mode: %o (should be 0700)", info.Mode().Perm()),
			})
		}
	}

	severity := "ok"
	if len(findings) > 0 {
		for _, f := range findings {
			if f["severity"] == "high" {
				severity = "high"
				break
			}
		}
		if severity != "high" {
			for _, f := range findings {
				if f["severity"] == "medium" {
					severity = "medium"
					break
				}
			}
		}
		if severity != "high" && severity != "medium" {
			severity = "low"
		}
	}

	writeJSON(w, 200, map[string]any{
		"severity":     severity,
		"findings":     findings,
		"total_checks": 7,
		"timestamp":    time.Now().Format(time.RFC3339),
	})
}

// handleGoroutineDump returns goroutine stack traces for debugging.
// F28: Debug endpoint for diagnosing goroutine leaks.
// SEC-P2-10: gated behind debug_goroutine_dump=true (default off) — an
// unauthenticated-by-design stack dump can leak secrets held in goroutine
// locals. Also uses runtime.Stack(buf, false) to avoid the STW pause of a
// full-process dump.
func handleGoroutineDump(w http.ResponseWriter, r *http.Request) {
	if cfg.Get("debug_goroutine_dump", "false") != "true" {
		writeError(w, 403, "goroutine dump disabled")
		return
	}
	buf := make([]byte, 1<<20) // 1MB buffer
	n := runtime.Stack(buf, false)
	writeJSON(w, 200, map[string]any{
		"count":      runtime.NumGoroutine(),
		"stack_dump": string(buf[:n]),
	})
}

func handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if auditLog == nil || !auditLog.enabled {
		writeJSON(w, 200, map[string]any{"entries": []string{}, "enabled": false})
		return
	}
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 1000 {
		limit = v
	}

	auditLog.mu.Lock()
	defer auditLog.mu.Unlock()
	if auditLog.file == nil {
		writeJSON(w, 200, map[string]any{"entries": []string{}, "enabled": false})
		return
	}

	info, err := auditLog.file.Stat()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "failed to stat audit log"})
		return
	}

	readSize := int64(64 * 1024)
	if info.Size() < readSize {
		readSize = info.Size()
	}
	buf := make([]byte, readSize)
	if _, err := auditLog.file.ReadAt(buf, info.Size()-readSize); err != nil {
		writeJSON(w, 500, map[string]any{"error": "failed to read audit log"})
		return
	}

	lines := strings.Split(string(buf), "\n")
	var entries []string
	for i := len(lines) - 1; i >= 0 && len(entries) < limit; i-- {
		if lines[i] != "" {
			entries = append(entries, lines[i])
		}
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	writeJSON(w, 200, map[string]any{
		"entries": entries,
		"enabled": true,
		"total":   len(entries),
	})
}

func handleCapabilityClaim(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		writeError(w, 503, "contribution ledger not initialized")
		return
	}
	var claim CapabilityClaim
	if err := readJSON(w, r, &claim); err != nil {
		return
	}
	if claim.PeerID == "" {
		writeError(w, 400, "peer_id is required")
		return
	}
	if len(claim.Models) == 0 {
		writeError(w, 400, "at least one model is required")
		return
	}
	id := contributionLedger.RecordClaim(&claim)
	saveContributionLedger()
	writeJSON(w, 201, map[string]any{"id": id, "status": "recorded"})
}

func handleCapabilityClaims(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		writeError(w, 503, "contribution ledger not initialized")
		return
	}
	claims := contributionLedger.GetAllClaims()
	if claims == nil {
		claims = []*CapabilityClaim{}
	}
	writeJSON(w, 200, map[string]any{"claims": claims, "total": len(claims)})
}

func handleCapabilityVerify(w http.ResponseWriter, r *http.Request) {
	if capabilityVerifier == nil || contributionLedger == nil {
		writeError(w, 503, "capability verifier not initialized")
		return
	}
	peerID := r.PathValue("peer_id")
	if peerID == "" {
		writeError(w, 400, "peer_id is required")
		return
	}
	claims := contributionLedger.GetAllClaims()
	var target *CapabilityClaim
	for _, c := range claims {
		if c.PeerID == peerID {
			target = c
			break
		}
	}
	if target == nil {
		writeError(w, 404, "no capability claim found for peer")
		return
	}
	results, allOK := capabilityVerifier.VerifyClaim(target)
	var probeResults []map[string]any
	for _, r := range results {
		probeResults = append(probeResults, map[string]any{
			"model_id":   r.ModelID,
			"success":    r.Success,
			"latency_ms": r.LatencyMS,
			"error":      r.Error,
		})
	}
	status := "verified"
	if !allOK {
		status = "partial"
	}
	writeJSON(w, 200, map[string]any{
		"peer_id": peerID,
		"status":  status,
		"results": probeResults,
	})
}

func handleLedgerContributions(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		writeError(w, 503, "contribution ledger not initialized")
		return
	}
	contribs := contributionLedger.GetAllContributions()
	if contribs == nil {
		contribs = []*ContributionRecord{}
	}
	writeJSON(w, 200, map[string]any{"contributions": contribs, "total": len(contribs)})
}

func handleLedgerBalance(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		writeError(w, 503, "contribution ledger not initialized")
		return
	}
	nodeID := r.PathValue("node_id")
	if nodeID == "" {
		if node != nil {
			nodeID = node.NodeID()
		} else {
			writeError(w, 400, "node_id is required")
			return
		}
	}
	balance := contributionLedger.DeriveBalance(nodeID)
	chain := contributionLedger.GetTransactionChain(nodeID)
	writeJSON(w, 200, map[string]any{
		"node_id":      nodeID,
		"balance":      balance,
		"transactions": len(chain),
		"chain_valid":  contributionLedger.VerifyChain(),
	})
}

func handleLedgerTransactions(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		writeError(w, 503, "contribution ledger not initialized")
		return
	}
	txs := contributionLedger.GetAllTransactions()
	if txs == nil {
		txs = []*SignedTransaction{}
	}
	writeJSON(w, 200, map[string]any{
		"transactions": txs,
		"total":        len(txs),
		"chain_valid":  contributionLedger.VerifyChain(),
	})
}
