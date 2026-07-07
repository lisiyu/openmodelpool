package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const AppVersion = "3.2.0"

func main() {
	// Initialize all components
	os.MkdirAll("data", 0755)
	initEncryptor("data/.key")
	initConfig("data/config.json")
	initLogger("data")
	initProviderManager("data/providers.json")
	initTracker("data/usage.json")
	initSiderMonitor("data/sider_token_status.json")
	initAuth("data/admin.json")
	initVMessManager("data")
	initMultiUser("data")

	// Initialize v3.0 federation components
	initNode("data")
	LoadGenesisConfig("data") // Load custom genesis or use compiled-in default
	initFederation("data")
	initGossip()
	initReputation("data")
	initCredits("data")
	initMessages("data")
	initNodeWeightManager("data")
	initInviteManager("data")

	// Initialize DHT routing table (Phase 3 hybrid discovery)
	initDHT()

	// Initialize event bus for real-time push
	initEventBus()

	// Initialize metrics collector
	initMetrics()

	// Initialize rate limiter
	initRateLimiter()

	// Migrate: re-save to encrypt any plaintext sensitive data
	cfg.save()
	pm.save()
	auth.save()

	// Re-start VMess proxies on startup
	for _, p := range pm.GetAll() {
		raw, ok := pm.GetRaw(p.ID)
		if ok && strings.HasPrefix(raw.Proxy, "vmess://") {
			if _, err := ResolveProxy(raw.ID, raw.Proxy); err != nil {
				slog.Warn("failed to re-start VMess proxy on startup", "provider", raw.ID, "error", err)
			} else {
				slog.Info("re-started VMess proxy on startup", "provider", raw.ID)
			}
		}
	}

	// Start health checker (every 5 minutes)
	initHealthChecker(5 * time.Minute)

	// Setup HTTP mux
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", handleHealth)

	// OpenAI-compatible endpoints (supports admin proxy key + consumer keys)
	mux.HandleFunc("GET /v1/models", withProxyAuth(rateLimitMiddleware(handleListModels)))
	mux.HandleFunc("POST /v1/chat/completions", withProxyAuth(rateLimitMiddleware(handleChatCompletions)))

	// Auth (public)
	mux.HandleFunc("GET /api/setup/status", handleSetupStatus)
	mux.HandleFunc("POST /api/setup", handleSetup)
	mux.HandleFunc("POST /api/login", handleLogin)
	mux.HandleFunc("POST /api/forgot-password", handleForgotPassword)
	mux.HandleFunc("POST /api/reset-password", handleResetPassword)
	mux.HandleFunc("POST /api/reset-password/verify", handleVerifyResetToken)
	mux.HandleFunc("POST /api/auth/reset-with-code", handleResetWithCode)

	// Auth (protected)
	mux.HandleFunc("GET /api/auth/verify", withAuth(handleVerifyAuth))
	mux.HandleFunc("GET /api/config", withAuth(handleGetConfig))
	mux.HandleFunc("GET /api/config/export", withAuth(handleExportConfig))
	mux.HandleFunc("POST /api/config/import", withAuth(handleImportConfig))
	mux.HandleFunc("POST /api/config", withAuth(handleSaveConfig))
	mux.HandleFunc("GET /api/status", withAuth(handleStatus))
	mux.HandleFunc("GET /api/admin/info", withAuth(handleAdminInfo))
	mux.HandleFunc("POST /api/admin/change-password", withAuth(handleChangePassword))
	mux.HandleFunc("POST /api/admin/update-email", withAuth(handleUpdateEmail))
	mux.HandleFunc("GET /api/share/info", withAuth(handleShareInfo))

	// Provider management (admin + consumer)
	mux.HandleFunc("GET /api/providers", withConsumerOrAdminAuth(handleListProviders))
	mux.HandleFunc("GET /api/providers/presets", handleGetPresets)
	mux.HandleFunc("POST /api/providers", withConsumerOrAdminAuth(handleCreateProvider))
	mux.HandleFunc("GET /api/providers/{id}", withConsumerOrAdminAuth(handleGetProvider))
	mux.HandleFunc("PUT /api/providers/{id}", withConsumerOrAdminAuth(handleUpdateProvider))
	mux.HandleFunc("DELETE /api/providers/{id}", withConsumerOrAdminAuth(handleDeleteProvider))
	mux.HandleFunc("POST /api/providers/{id}/test", withConsumerOrAdminAuth(handleTestProvider))
	mux.HandleFunc("GET /api/providers/{id}/models", withConsumerOrAdminAuth(handleGetProviderModels))
	mux.HandleFunc("POST /api/providers/{id}/sync-url", withConsumerOrAdminAuth(handleSyncProviderURL))
	mux.HandleFunc("POST /api/providers/{id}/sync-models", withConsumerOrAdminAuth(handleSyncModels))
	mux.HandleFunc("POST /api/providers/sync-all-urls", withConsumerOrAdminAuth(handleSyncAllURLs))

	// Sider status
	mux.HandleFunc("GET /api/providers/sider/status", withConsumerOrAdminAuth(handleSiderStatus))
	mux.HandleFunc("POST /api/providers/sider/test", withConsumerOrAdminAuth(handleSiderTest))

	// Usage & routing (admin + consumer)
	mux.HandleFunc("GET /api/usage/summary", withConsumerOrAdminAuth(handleUsageSummary))
	mux.HandleFunc("GET /api/usage/providers", withConsumerOrAdminAuth(handleUsageProviders))
	mux.HandleFunc("GET /api/usage/records", withConsumerOrAdminAuth(handleUsageRecords))
	mux.HandleFunc("DELETE /api/usage/reset", withAuth(handleUsageReset)) // admin only
	mux.HandleFunc("GET /api/routing/mode", withConsumerOrAdminAuth(handleGetRoutingMode))
	mux.HandleFunc("POST /api/routing/mode", withAuth(handleSetRoutingMode)) // admin only
	mux.HandleFunc("GET /api/routing/weights", withConsumerOrAdminAuth(handleGetRoutingWeights))
	mux.HandleFunc("POST /api/routing/weights", withAuth(handleSetRoutingWeights)) // admin only
	mux.HandleFunc("GET /api/routing/advice/{model}", withConsumerOrAdminAuth(handleRoutingAdvice))

	// SMTP (protected)
	mux.HandleFunc("GET /api/smtp/status", handleSMTPStatus)
	mux.HandleFunc("GET /api/smtp/config", withAuth(handleGetSMTPConfig))
	mux.HandleFunc("POST /api/smtp/config", withAuth(handleSaveSMTPConfig))
	mux.HandleFunc("POST /api/smtp/test", withAuth(handleSMTPTest))

	// Request logs & health (protected)
	mux.HandleFunc("GET /api/logs", withAuth(handleRequestLogs))
	mux.HandleFunc("GET /api/health", withAuth(handleHealthStatus))

	// Domain binding APIs
	mux.HandleFunc("POST /api/domain/verify", withAuth(handleVerifyDomainToken))
	mux.HandleFunc("POST /api/domain/bind", withAuth(handleBindDomain))
	mux.HandleFunc("GET /api/domain/status", withAuth(handleGetDomainStatus))
	mux.HandleFunc("POST /api/domain/unbind", withAuth(handleUnbindDomain))

	// Real-time events (SSE)
	mux.HandleFunc("GET /events", handleSSE)

	// Prometheus metrics
	mux.HandleFunc("GET /metrics", handleMetrics)

	// Multi-user / invite codes (protected)
	mux.HandleFunc("GET /api/invite-codes", withAuth(handleListInviteCodes))
	mux.HandleFunc("POST /api/invite-codes", withAuth(handleCreateInviteCode))
	mux.HandleFunc("DELETE /api/invite-codes/{code}", withAuth(handleDeleteInviteCode))
	mux.HandleFunc("GET /api/consumers", withAuth(handleListConsumers))
	mux.HandleFunc("POST /api/consumers", withAuth(handleCreateConsumer))
	mux.HandleFunc("DELETE /api/consumers/{id}", withAuth(handleDeleteConsumer))
	mux.HandleFunc("POST /api/consumers/{id}/toggle", withAuth(handleToggleConsumer))
	mux.HandleFunc("PUT /api/consumers/{id}", withAuth(handleUpdateConsumer))
	mux.HandleFunc("POST /api/consumer/register", handleConsumerRegister)

	// Static pages
	mux.HandleFunc("GET /", handleAdminPage)
	mux.HandleFunc("GET /admin", handleAdminPage)
	mux.HandleFunc("GET /setup", handleSetupPage)
	mux.HandleFunc("GET /login", handleLoginPage)

	// Federation API (v3.0)
	mux.HandleFunc("GET /api/federation/status", withAuth(handleFederationStatus))
	mux.HandleFunc("GET /api/federation/pool", handleFederationPool)
	mux.HandleFunc("POST /api/federation/gossip", handleFederationGossip)
	mux.HandleFunc("POST /api/federation/announce", handleFederationAnnounce)
	mux.HandleFunc("POST /api/federation/relay", handleRelayRequest)
	mux.HandleFunc("GET /api/federation/reputations", handleGetReputations)
	mux.HandleFunc("POST /api/federation/score", withAuth(handlePostScore))
	mux.HandleFunc("GET /api/federation/credits", withAuth(handleGetCredits))
	mux.HandleFunc("GET /api/federation/credits/history", withAuth(handleGetCreditHistory))
	mux.HandleFunc("POST /api/federation/messages/send", withAuth(handleSendMessage))
	mux.HandleFunc("GET /api/federation/messages/inbox", withAuth(handleGetInbox))
	mux.HandleFunc("GET /api/federation/messages/outbox", withAuth(handleGetOutbox))
	mux.HandleFunc("POST /api/federation/messages/read", withAuth(handleMarkAsRead))
	mux.HandleFunc("GET /api/federation/config", withAuth(handleGetFederationConfig))
	mux.HandleFunc("POST /api/federation/config", withAuth(handleSaveFederationConfig))
	mux.HandleFunc("POST /api/federation/init-node", withAuth(handleInitNode))
	mux.HandleFunc("GET /api/federation/weights", withAuth(handleGetNodeWeights))
	mux.HandleFunc("POST /api/federation/weights", withAuth(handleSetNodeWeight))
	mux.HandleFunc("GET /api/federation/approvals", withAuth(handleGetApprovals))
	mux.HandleFunc("POST /api/federation/approvals/resolve", withAuth(handleResolveApproval))
	mux.HandleFunc("POST /api/federation/token-budget", withAuth(handleSetTokenBudget))
	mux.HandleFunc("POST /api/federation/join", handleJoinNetwork)
	mux.HandleFunc("GET /api/federation/genesis", handleGetGenesis)
	mux.HandleFunc("POST /api/federation/invites", withAuth(handleCreateInvite))
	mux.HandleFunc("GET /api/federation/invites", withAuth(handleListInvites))
	mux.HandleFunc("POST /api/federation/invites/verify", handleVerifyInvite)

	// CORS + request logging middleware
	handler := corsMiddleware(requestLogMiddleware(mux))

	port := cfg.Get("service_port", "8000")
	addr := ":" + port

	// Initialize Cloudflare Tunnel if enabled
	portNum := 8000
	if p, err := strconv.Atoi(port); err == nil {
		portNum = p
	}
	initTunnel(portNum)

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // long for streaming
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		for {
			sig := <-sigCh
			switch sig {
			case syscall.SIGHUP:
				// Hot reload configuration
				slog.Info("SIGHUP received, reloading configuration...")
				cfg.load()
				// Reinitialize rate limiter with new config
				initRateLimiter()
				// Reload federation config if changed
				if fed != nil {
					fed.mu.Lock()
					fed.enabled = cfg.Get("federation_enabled", "false") == "true"
					fed.relayEnabled = cfg.Get("federation_relay_enabled", "false") == "true"
					fed.mu.Unlock()
				}
				// Broadcast config update via SSE
				BroadcastConfigUpdate("all")
				slog.Info("configuration reloaded successfully")
			case syscall.SIGINT, syscall.SIGTERM:
				slog.Info("shutting down...")
				tracker.Stop()
				healthChecker.stop()
				CloseAccessLog()
				if tunnel != nil {
					tunnel.stop()
				}
				if fed != nil {
					fed.stop()
				}
				if gossip != nil {
					gossip.stop()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				server.Shutdown(ctx)
				return
			}
		}
	}()

	slog.Info("ModelMux started", "port", port, "providers", len(pm.Enabled()))
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// ============================================================
// Middleware
// ============================================================

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigins := cfg.Get("cors_allowed_origins", "*")

		if allowedOrigins == "*" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" && isOriginAllowed(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isOriginAllowed checks if an origin matches the whitelist.
// Supports exact match and wildcard subdomain (*.example.com).
func isOriginAllowed(origin, whitelist string) bool {
	origins := strings.Split(whitelist, ",")
	for _, allowed := range origins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if allowed == origin {
			return true
		}
		// Wildcard subdomain: *.example.com matches sub.example.com
		if strings.HasPrefix(allowed, "*.") {
			suffix := allowed[1:] // ".example.com"
			if strings.HasSuffix(origin, suffix) {
				return true
			}
		}
	}
	return false
}

// withProxyAuth authenticates v1 proxy endpoints.
// Accepts: admin proxy API key (owner="") or consumer API key (owner=consumer_id).
// If no proxy API key is set and no consumer key matches, allows anonymous access as admin.
func withProxyAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			// No auth header - check if proxy key is required
			proxyKey := cfg.Get("proxy_api_key", "")
			if proxyKey == "" {
				r.Header.Set("X-Request-Owner", "")
				r.Header.Set("X-Request-Role", "admin")
				handler(w, r)
				return
			}
			writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
				Message: "API key required",
				Type:    "authentication_error",
				Code:    "missing_api_key",
			}})
			return
		}

		key := authHeader[7:]
		// Check admin proxy API key first
		proxyKey := cfg.Get("proxy_api_key", "")
		if proxyKey != "" && key == proxyKey {
			r.Header.Set("X-Request-Owner", "")
			r.Header.Set("X-Request-Role", "admin")
			handler(w, r)
			return
		}

		// Check consumer API key
		if consumer, ok := multiUser.ValidateAPIKey(key); ok {
			r.Header.Set("X-Request-Owner", consumer.ID)
			r.Header.Set("X-Request-Role", "consumer")
			r.Header.Set("X-Consumer-Name", consumer.Name)
			handler(w, r)
			return
		}

		// If no proxy key is set at all, allow as admin (backward compat)
		if proxyKey == "" {
			r.Header.Set("X-Request-Owner", "")
			r.Header.Set("X-Request-Role", "admin")
			handler(w, r)
			return
		}

		writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
			Message: "Invalid API key",
			Type:    "authentication_error",
			Code:    "invalid_api_key",
		}})
	}
}

func withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			writeJSON(w, 401, map[string]string{"error": "not authenticated"})
			return
		}
		_, err := auth.VerifyToken(token)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "token expired"})
			return
		}
		r.Header.Set("X-Request-Owner", "")
		r.Header.Set("X-Request-Role", "admin")
		handler(w, r)
	}
}

func extractToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return authHeader[7:]
	}
	cookie, _ := r.Cookie("admin_token")
	if cookie != nil {
		return cookie.Value
	}
	return ""
}

// ============================================================
// Helpers
// ============================================================

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: ErrorDetail{
		Message: msg, Type: "api_error", Code: fmt.Sprintf("%d", status),
	}})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
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
	writeJSON(w, 200, status)
}

func handleListModels(w http.ResponseWriter, r *http.Request) {
	models := pm.AllModels()
	writeJSON(w, 200, ModelListResponse{Object: "list", Data: models})
}

// handleFederationStatus returns a comprehensive federation status overview.
// GET /api/federation/status
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

	if credits != nil {
		status["credits"] = map[string]any{
			"balance":     credits.GetBalance(),
			"transactions": len(credits.transactions),
		}
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

	writeJSON(w, 200, map[string]any{
		"federation_enabled":       cfg.Get("federation_enabled", "false"),
		"federation_relay_enabled": cfg.Get("federation_relay_enabled", "false"),
		"federation_registry_url":  cfg.Get("federation_registry_url", ""),
		"federation_registry_repo": cfg.Get("federation_registry_repo", "lisiyu/modelmux"),
		"gossip_interval_s":        cfg.Get("gossip_interval_s", "30"),
		"heartbeat_interval_s":     cfg.Get("heartbeat_interval_s", "60"),
		"tunnel_enabled":           cfg.Get("tunnel_enabled", "false"),
		"tunnel_mode":              cfg.Get("tunnel_mode", "quick"), // quick | named
		"tunnel_domain":            cfg.Get("tunnel_domain", ""),     // custom domain e.g. mux.example.com
		"tunnel_url":               cfg.Get("tunnel_url", ""),        // current quick tunnel URL
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

	// Smart routing with fallback — uses the unified pool (all providers from all users)
	routingMode := cfg.Get("routing_mode", "priority")
	candidates := pm.OrderedCandidates(model, routingMode)

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

		if p.APIKey == "" {
			lastErr = fmt.Errorf("provider '%s' has no API key", p.Name)
			continue
		}

		startTime := time.Now()

		if stream {
			dataSent, err := handleStreamProxy(w, p, actualModel, req.Messages, extra, model, startTime)
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
			resp, err := doNonStream(p, actualModel, req.Messages, extra)
			if err != nil {
				lastErr = err
				tracker.Record(p.ID, p.Name, model, 0, 0, float64(time.Since(startTime).Milliseconds()), false, err.Error())
				continue
			}
			resp.Model = model
			latencyMS := float64(time.Since(startTime).Milliseconds())
			var promptTok, compTok int
			if resp.Usage != nil {
				promptTok = resp.Usage.PromptTokens
				compTok = resp.Usage.CompletionTokens
			}
			tracker.Record(p.ID, p.Name, model, promptTok, compTok, latencyMS, true, "")
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
func handleStreamProxy(w http.ResponseWriter, p Provider, model string, messages []ChatMessage, extra map[string]any, origModel string, startTime time.Time) (bool, error) {
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
		tracker.Record(p.ID, p.Name, origModel, 0, 0, latencyMS, false, err.Error())
		return sw.bytesWritten > 0, err
	}
	tracker.Record(p.ID, p.Name, origModel, 0, 0, latencyMS, true, "")
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

	// Use provider's API Key, fall back to global config for backward compatibility
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
