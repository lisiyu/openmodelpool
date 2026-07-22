package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// runServer sets up HTTP routes, starts the server, and handles graceful shutdown.
func runServer() {
	mux := setupRoutes()

	port := cfg.Get("service_port", "8000")
	addr := ":" + port

	// Initialize Cloudflare Tunnel if enabled
	portNum := 8000
	if p, err := strconv.Atoi(port); err == nil {
		portNum = p
	}
	initTunnel(portNum)

	handler := corsMiddleware(requestLogMiddleware(concurrencyMiddleware(mux)))

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // long for streaming
		IdleTimeout:  120 * time.Second,
	}

	// Start HTTPS server if public_url is https://
	setupHTTPS(server, handler)

	// Start Seed discovery service on port 8001
	startSeedServer()

	slog.Info("OpenModelPool Agent started", "port", port, "providers", len(pm.Enabled()))

	// Graceful shutdown
	go gracefulShutdown(server)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// setupRoutes registers all HTTP routes on a new ServeMux.
func setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", handleHealth)
	// Version (public, no auth) — used by monitoring & auto-update scripts
	mux.HandleFunc("GET /api/version", handleVersion)

	// OpenAI-compatible endpoints — Gateway mode
	mux.HandleFunc("GET /v1/models", withProxyAuth(rateLimitMiddleware(handleGatewayModels)))
	mux.HandleFunc("POST /v1/chat/completions", withProxyAuth(rateLimitMiddleware(handleGatewayRequest)))
	mux.HandleFunc("POST /v1/completions", withProxyAuth(rateLimitMiddleware(handleGatewayRequest)))
	mux.HandleFunc("POST /v1/embeddings", withProxyAuth(rateLimitMiddleware(handleGatewayRequest)))
	// Anthropic Messages API compatibility — for Claude Code and other Anthropic clients
	mux.HandleFunc("POST /v1/messages", anthropicAuthAdapter(withProxyAuth(rateLimitMiddleware(handleAnthropicMessages))))

	// Seed discovery endpoints (public, no auth required)
	mux.HandleFunc("GET /api/peers", handleSeedPeers)
	mux.HandleFunc("POST /api/register", handleSeedRegister)
	mux.HandleFunc("GET /api/seed/health", handleSeedHealth)

	// Auth (public)
	mux.HandleFunc("GET /api/setup/status", handleSetupStatus)
	mux.HandleFunc("GET /api/addresses", handleGetAddresses)
	mux.HandleFunc("POST /api/setup", rateLimitByIP(3, "setup")(handleSetup))
	mux.HandleFunc("POST /api/login", rateLimitByIP(5, "login")(handleLogin))
	mux.HandleFunc("POST /api/refresh", rateLimitByIP(10, "refresh")(handleRefreshToken))
	mux.HandleFunc("POST /api/forgot-password", localOnly(rateLimitByIP(3, "forgot_password")(handleForgotPassword)))
	mux.HandleFunc("POST /api/reset-password", localOnly(rateLimitByIP(5, "reset_password")(handleResetPassword)))
	mux.HandleFunc("POST /api/reset-password/verify", localOnly(rateLimitByIP(10, "reset_verify")(handleVerifyResetToken)))
	mux.HandleFunc("POST /api/auth/reset-with-code", localOnly(rateLimitByIP(5, "reset_code")(handleResetWithCode)))

	// Auth (protected)
	mux.HandleFunc("GET /api/auth/verify", withAuth(handleVerifyAuth))
	mux.HandleFunc("GET /api/config", withAuth(handleGetConfig))
	mux.HandleFunc("GET /api/config/export", withAuth(handleExportConfig))
	mux.HandleFunc("POST /api/config/import", rateLimitByIP(5, "config_import")(withAuth(handleImportConfig)))
	mux.HandleFunc("POST /api/config", rateLimitByIP(20, "config_save")(withAuth(handleSaveConfig)))
	mux.HandleFunc("GET /api/status", withAuth(handleStatus))
	mux.HandleFunc("GET /api/admin/info", withAuth(handleAdminInfo))
	mux.HandleFunc("POST /api/admin/change-password", rateLimitByIP(3, "change_password")(withAuth(handleChangePassword)))
	mux.HandleFunc("POST /api/admin/update-email", withAuth(handleUpdateEmail))
	mux.HandleFunc("POST /api/admin/restart", rateLimitByIP(3, "restart")(withAuth(handleRestart)))
	mux.HandleFunc("GET /api/share/info", withAuth(handleShareInfo))

	// Provider management (admin + consumer)
	mux.HandleFunc("GET /api/providers", withConsumerOrAdminAuth(handleListProviders))
	mux.HandleFunc("GET /api/providers/presets", handleGetPresets)
	mux.HandleFunc("POST /api/providers", withConsumerOrAdminAuth(handleCreateProvider))
	mux.HandleFunc("GET /api/providers/{id}", withConsumerOrAdminAuth(handleGetProvider))
	mux.HandleFunc("PUT /api/providers/{id}", withConsumerOrAdminAuth(handleUpdateProvider))
	mux.HandleFunc("DELETE /api/providers/{id}", withConsumerOrAdminAuth(handleDeleteProvider))
	mux.HandleFunc("POST /api/providers/{id}/test", withConsumerOrAdminAuth(handleTestProvider))
	mux.HandleFunc("POST /api/providers/{id}/test-all-keys", withConsumerOrAdminAuth(handleTestAllKeys))
	mux.HandleFunc("GET /api/providers/{id}/models", withConsumerOrAdminAuth(handleGetProviderModels))
	mux.HandleFunc("POST /api/providers/{id}/sync-url", withConsumerOrAdminAuth(handleSyncProviderURL))
	mux.HandleFunc("POST /api/providers/{id}/sync-models", withConsumerOrAdminAuth(handleSyncModels))
	mux.HandleFunc("POST /api/providers/{id}/browser-login/start", withAuth(handleBrowserLoginStart))
	mux.HandleFunc("GET /api/providers/{id}/browser-login/status", withAuth(handleBrowserLoginStatus))
	mux.HandleFunc("POST /api/providers/{id}/browser-login/login", withAuth(handleBrowserLoginLogin))
	mux.HandleFunc("POST /api/providers/{id}/browser-login/action", withAuth(handleBrowserLoginAction))
	mux.HandleFunc("POST /api/providers/{id}/browser-login/finish", withAuth(handleBrowserLoginFinish))
	mux.HandleFunc("DELETE /api/providers/{id}/browser-login", withAuth(handleBrowserLoginCancel))
	// Provider access control (admin only)
	mux.HandleFunc("GET /api/providers/{id}/access-control", withAuth(handleGetProviderAccessControl))
	mux.HandleFunc("PUT /api/providers/{id}/access-control", withAuth(handleUpdateProviderAccessControl))
	mux.HandleFunc("POST /api/providers/sync-all-urls", withConsumerOrAdminAuth(handleSyncAllURLs))

	// Provider multi API key management (admin + consumer)
	mux.HandleFunc("GET /api/providers/{id}/keys", withConsumerOrAdminAuth(handleListAPIKeys))
	mux.HandleFunc("POST /api/providers/{id}/keys", withConsumerOrAdminAuth(handleAddAPIKey))
	mux.HandleFunc("PUT /api/providers/{id}/keys/{key_id}", withConsumerOrAdminAuth(handleUpdateAPIKey))
	mux.HandleFunc("DELETE /api/providers/{id}/keys/{key_id}", withConsumerOrAdminAuth(handleDeleteAPIKey))
	mux.HandleFunc("POST /api/providers/{id}/keys/{key_id}/reset-quota", withConsumerOrAdminAuth(handleResetKeyQuota))

	// Sider status
	mux.HandleFunc("GET /api/providers/sider/status", withConsumerOrAdminAuth(handleSiderStatus))
	mux.HandleFunc("POST /api/providers/sider/test", withConsumerOrAdminAuth(handleSiderTest))

	// Platform discovery (admin + consumer)
	mux.HandleFunc("GET /api/discovery/platforms", withConsumerOrAdminAuth(handleGetDiscoveredPlatforms))
	mux.HandleFunc("PUT /api/discovery/platforms/{id}", withConsumerOrAdminAuth(handleUpdateDiscoveredPlatform))
	mux.HandleFunc("POST /api/discovery/scan", withConsumerOrAdminAuth(handleTriggerDiscovery))
	mux.HandleFunc("POST /api/discovery/platforms/{id}/check", withConsumerOrAdminAuth(handleCheckDiscoveredPlatform))

	// Usage & routing (admin + consumer)
	mux.HandleFunc("GET /api/usage/summary", withConsumerOrAdminAuth(handleUsageSummary))
	mux.HandleFunc("GET /api/usage/providers", withConsumerOrAdminAuth(handleUsageProviders))
	mux.HandleFunc("GET /api/usage/records", withConsumerOrAdminAuth(handleUsageRecords))
	mux.HandleFunc("DELETE /api/usage/reset", withAuth(handleUsageReset))
	mux.HandleFunc("GET /api/routing/mode", withConsumerOrAdminAuth(handleGetRoutingMode))
	mux.HandleFunc("POST /api/routing/mode", withAuth(handleSetRoutingMode))
	mux.HandleFunc("GET /api/routing/weights", withConsumerOrAdminAuth(handleGetRoutingWeights))
	mux.HandleFunc("POST /api/routing/weights", withAuth(handleSetRoutingWeights))
	mux.HandleFunc("GET /api/routing/advice/{model}", withConsumerOrAdminAuth(handleRoutingAdvice))

	// SMTP (protected)
	mux.HandleFunc("GET /api/smtp/status", handleSMTPStatus)
	mux.HandleFunc("GET /api/smtp/config", withAuth(handleGetSMTPConfig))
	mux.HandleFunc("POST /api/smtp/config", rateLimitByIP(5, "smtp_config")(withAuth(handleSaveSMTPConfig)))
	mux.HandleFunc("POST /api/smtp/test", rateLimitByIP(5, "smtp_test")(withAuth(handleSMTPTest)))

	// Request logs & health (protected)
	mux.HandleFunc("GET /api/logs", withAuth(handleRequestLogs))
	mux.HandleFunc("GET /api/health", withAuth(handleHealthStatus))

	// Domain binding APIs
	mux.HandleFunc("POST /api/domain/verify", rateLimitByIP(5, "domain_verify")(withAuth(handleVerifyDomainToken)))
	mux.HandleFunc("POST /api/domain/bind", rateLimitByIP(3, "domain_bind")(withAuth(handleBindDomain)))
	mux.HandleFunc("GET /api/domain/status", withAuth(handleGetDomainStatus))
	mux.HandleFunc("POST /api/domain/unbind", rateLimitByIP(3, "domain_unbind")(withAuth(handleUnbindDomain)))
	mux.HandleFunc("POST /api/domain/manual-bind", withAuth(handleManualDomainBind))

	// IP binding
	mux.HandleFunc("POST /api/ip/bind", withAuth(handleBindIP))
	mux.HandleFunc("POST /api/ip/unbind", withAuth(handleUnbindIP))

	// Real-time events (SSE)
	mux.HandleFunc("GET /events", withAuth(handleSSE))

	// Prometheus metrics
	mux.HandleFunc("GET /metrics", withAuth(handleMetrics))

	// Performance metrics (lightweight JSON endpoint, no auth required for monitoring)
	mux.HandleFunc("GET /api/metrics", handleAPIMetrics)

	// Multi-user / invite codes (protected)
	mux.HandleFunc("GET /api/invite-codes", withAuth(handleListInviteCodes))
	mux.HandleFunc("POST /api/invite-codes", withAuth(handleCreateInviteCode))
	mux.HandleFunc("DELETE /api/invite-codes/{code}", withAuth(handleDeleteInviteCode))
	mux.HandleFunc("GET /api/consumers", withAuth(handleListConsumers))
	mux.HandleFunc("POST /api/consumers", withAuth(handleCreateConsumer))
	mux.HandleFunc("DELETE /api/consumers/{id}", withAuth(handleDeleteConsumer))
	mux.HandleFunc("POST /api/consumers/{id}/toggle", withAuth(handleToggleConsumer))
	mux.HandleFunc("PUT /api/consumers/{id}", withAuth(handleUpdateConsumer))
	mux.HandleFunc("POST /api/consumer/register", rateLimitByIP(10, "consumer_register")(handleConsumerRegister))

	// Static pages
	mux.HandleFunc("GET /", handleAdminPage)
	mux.HandleFunc("GET /admin", handleAdminPage)
	mux.HandleFunc("GET /setup", handleSetupPage)
	mux.HandleFunc("GET /login", handleLoginPage)
	mux.HandleFunc("GET /admin/provider", handleProviderPage)
	mux.HandleFunc("GET /admin/models", handleModelsPage)
	mux.HandleFunc("GET /admin/browser-login", handleBrowserLoginPage)
	mux.HandleFunc("GET /admin-common.js", handleAdminCommonJS)
	mux.HandleFunc("GET /admin-settings.js", handleAdminSettingsJS)
	mux.HandleFunc("GET /admin-network.js", handleAdminNetworkJS)
	mux.HandleFunc("GET /admin-share.js", handleAdminShareJS)
	mux.HandleFunc("GET /admin-logs.js", handleAdminLogsJS)

	// Federation API (v3.0)
	mux.HandleFunc("GET /api/federation/status", withAuth(handleFederationStatus))
	mux.HandleFunc("GET /api/federation/pool", withFederationAuth(handleFederationPool))
	mux.HandleFunc("POST /api/federation/gossip", withFederationAuth(handleFederationGossip))
	mux.HandleFunc("POST /api/federation/announce", withFederationAuth(handleFederationAnnounce))
	mux.HandleFunc("POST /api/federation/relay", rateLimitByIP(60, "federation_relay")(handleRelayRequest))
	mux.HandleFunc("GET /api/federation/reputations", handleGetReputations)
	mux.HandleFunc("POST /api/federation/score", withAuth(handlePostScore))

	// v2.0: Quota allocation (replaces old credits system)
	mux.HandleFunc("GET /api/network/quota-allocation", handleGetQuotaAllocation)
	mux.HandleFunc("PUT /api/network/quota-allocation", withAuth(handleUpdateQuotaAllocation))
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
	mux.HandleFunc("POST /api/federation/join", rateLimitByIP(5, "federation_join")(handleJoinNetwork))
	mux.HandleFunc("GET /api/federation/genesis", handleGetGenesis)
	mux.HandleFunc("POST /api/federation/invites", withAuth(handleCreateInvite))
	mux.HandleFunc("GET /api/federation/invites", withAuth(handleListInvites))
	mux.HandleFunc("POST /api/federation/invites/verify", rateLimitByIP(10, "invite_verify")(handleVerifyInvite))

	// P2P Shared Network API (Phase 1) — decentralized relay
	mux.HandleFunc("GET /api/network/status", handleNetworkStatus)
	mux.HandleFunc("GET /api/network/stats", handleNetworkStats)
	mux.HandleFunc("POST /api/network/consent", rateLimitByIP(5, "network_consent")(handleNetworkConsent))
	mux.HandleFunc("GET /api/network/disclaimer", handleNetworkDisclaimer)
	mux.HandleFunc("POST /api/network/enable", withAuth(handleNetworkEnable))
	mux.HandleFunc("POST /api/network/disable", withAuth(handleNetworkDisable))
	// Phase 2 切片② — explicit identity lifecycle endpoints (generate → confirm-backup → restore).
	mux.HandleFunc("POST /api/network/identity/generate", withAuth(handleNetworkIdentityGenerate))
	mux.HandleFunc("POST /api/network/identity/confirm-backup", withAuth(handleNetworkIdentityConfirmBackup))
	mux.HandleFunc("POST /api/network/identity/restore", withAuth(handleNetworkIdentityRestore))
	mux.HandleFunc("POST /api/network/toggle", withAuth(handleNetworkToggle))
	mux.HandleFunc("PUT /api/network/config", withAuth(handleNetworkConfigUpdate))
	mux.HandleFunc("GET /api/network/peers", withAuth(handleNetworkPeers))
	mux.HandleFunc("POST /api/network/peers", withAuth(handleNetworkAddPeer))
	mux.HandleFunc("DELETE /api/network/peers/{id}", withAuth(handleNetworkRemovePeer))
	mux.HandleFunc("GET /api/network/resolve/{id}", handleNetworkResolve)
	mux.HandleFunc("GET /api/network/routes", withAuth(handleNetworkRoutes))
	mux.HandleFunc("GET /api/network/join-conditions", withAuth(handleNetworkJoinConditions))

	// v2.0 Guest Keys
	mux.HandleFunc("POST /api/network/keys/issue", withAuth(handleGuestKeyIssue))
	mux.HandleFunc("POST /api/network/guest-keys", withAuth(handleGuestKeyIssue))
	mux.HandleFunc("GET /api/network/guest-keys", withAuth(handleGuestKeyList))
	mux.HandleFunc("DELETE /api/network/guest-keys/{key}", withAuth(handleGuestKeyRevoke))
	mux.HandleFunc("POST /api/network/keys/validate", rateLimitByIP(30, "key_validate")(handleNetworkKeyValidate))
	mux.HandleFunc("PUT /api/network/guest-keys/{key}/quota", withAuth(handleGuestKeyUpdateQuota))

	// Node Heartbeat & Discovery (Phase 2)
	mux.HandleFunc("POST /api/network/heartbeat", rateLimitByIP(30, "heartbeat")(handleNetworkHeartbeat))
	mux.HandleFunc("GET /api/node/pubkey", requireHTTPS(handleNodePubKey))
	mux.HandleFunc("GET /api/node/info", handleNodeInfo)

	// Algorithm Chain & Quota (Phase 3)
	mux.HandleFunc("GET /api/network/algorithm/current", handleAlgorithmCurrent)
	mux.HandleFunc("GET /api/network/algorithm/history", handleAlgorithmHistory)
	mux.HandleFunc("POST /api/network/algorithm/propose", withAuth(handleAlgorithmPropose))
	mux.HandleFunc("POST /api/network/algorithm/vote", withAuth(handleAlgorithmVote))
	mux.HandleFunc("POST /api/network/algorithm/gossip", rateLimitByIP(30, "algo_gossip")(handleAlgorithmGossip))
	mux.HandleFunc("GET /api/network/algorithm/proposals", handleAlgorithmProposals)
	mux.HandleFunc("GET /api/network/algorithm/validate", handleAlgorithmValidate)
	mux.HandleFunc("GET /api/network/open-key-quota", handleOpenKeyQuota)
	mux.HandleFunc("GET /api/network/open-key-quota/all", handleOpenKeyQuotaAll)

	// Global Pool & Global Keys (Phase 4)
	mux.HandleFunc("GET /api/network/global-pool", handleGlobalPoolStatus)
	mux.HandleFunc("POST /api/network/global-pool/join", withAuth(handleGlobalPoolJoin))
	mux.HandleFunc("POST /api/network/global-pool/contribute", withAuth(handleGlobalPoolContribute))
	mux.HandleFunc("GET /api/network/global-pool/nodes", handleGlobalPoolNodes)
	mux.HandleFunc("GET /api/network/global-pool/stats", handleGlobalPoolStats)

	// §10A: WAF status & management
	mux.HandleFunc("GET /api/waf/status", withAuth(handleWAFStatus))
	mux.HandleFunc("GET /api/waf/violations", withAuth(handleWAFViolations))
	mux.HandleFunc("GET /api/waf/bans", withAuth(handleWAFBans))
	mux.HandleFunc("POST /api/waf/unban/{key}", withAuth(handleWAFUnban))

	// §3.2.3: Public key quota status
	mux.HandleFunc("GET /api/network/public-key-quota", withAuth(handlePublicKeyQuotaStatus))

	// Dynamic Load Balancer (Phase 4)
	mux.HandleFunc("GET /api/network/loadbalancer/status", handleLBStatus)
	mux.HandleFunc("GET /api/network/loadbalancer/nodes", withAuth(handleLBNodes))
	mux.HandleFunc("GET /api/network/loadbalancer/metrics/{node_id}", withAuth(handleLBNodeMetrics))
	mux.HandleFunc("PUT /api/network/loadbalancer/config", withAuth(handleLBConfigUpdate))
	mux.HandleFunc("GET /api/network/heartbeat/ping", handleHeartbeatPing)

	// Cross-Region Routing (Phase 4)
	mux.HandleFunc("GET /api/network/regions", handleNetworkRegions)
	mux.HandleFunc("GET /api/network/regions/{region}/nodes", handleNetworkRegionNodes)
	mux.HandleFunc("PUT /api/network/regions/config", withAuth(handleNetworkRegionConfigUpdate))

	// Dynamic Balance Engine (Phase 4)
	mux.HandleFunc("GET /api/network/balance/status", handleBalanceStatus)
	mux.HandleFunc("GET /api/network/balance/nodes", handleBalanceNodes)
	mux.HandleFunc("GET /api/network/balance/adjustments", handleBalanceAdjustments)
	mux.HandleFunc("POST /api/network/balance/recalculate", withAuth(handleBalanceRecalculate))

	// P2P Relay: /network/{node_id}/{rest...} — any shared node can relay
	mux.HandleFunc("GET /network/{id}/", handleNetworkRelay)
	mux.HandleFunc("POST /network/{id}/", handleNetworkRelay)
	mux.HandleFunc("PUT /network/{id}/", handleNetworkRelay)
	mux.HandleFunc("DELETE /network/{id}/", handleNetworkRelay)
	mux.HandleFunc("GET /network/{id}", handleNetworkRelay)
	mux.HandleFunc("POST /network/{id}", handleNetworkRelay)

	return mux
}

// setupHTTPS configures HTTPS with Let's Encrypt auto-cert if public_url is https://.
func setupHTTPS(server *http.Server, handler http.Handler) {
	publicURL := cfg.Get("public_url", "")
	if !strings.HasPrefix(publicURL, "https://") {
		return
	}

	u, err := url.Parse(publicURL)
	if err != nil {
		return
	}
	domain := u.Hostname()
	certDir := "data/certs"
	os.MkdirAll(certDir, 0700)

	certManager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(certDir),
		HostPolicy: autocert.HostWhitelist(domain),
	}

	// Wrap HTTP handler with ACME HTTP-01 challenge support
	server.Handler = certManager.HTTPHandler(handler)

	// HTTPS server on port 8443 (iptables forwards 443→8443)
	httpsServer := &http.Server{
		Addr:    ":8443",
		Handler: handler,
		TLSConfig: &tls.Config{
			GetCertificate: certManager.GetCertificate,
		},
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("HTTPS server starting", "addr", ":8443", "domain", domain)
		if err := httpsServer.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			slog.Error("HTTPS server error", "error", err)
		}
	}()

	slog.Info("HTTPS enabled with Let's Encrypt auto-cert", "domain", domain)
}

// gracefulShutdown handles OS signals for hot reload and clean shutdown.
func gracefulShutdown(server *http.Server) {
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
			// Reload federation: reconcile with the network_enabled single source
			// of truth instead of the legacy federation_enabled key (REQ-2).
			if netMgr != nil {
				netMgr.syncFederationToNetwork()
			}
			if fed != nil {
				fed.mu.Lock()
				fed.relayEnabled = cfg.Get("federation_relay_enabled", "false") == "true"
				fed.mu.Unlock()
			}
			// Broadcast config update via SSE
			BroadcastConfigUpdate("all")
			slog.Info("configuration reloaded successfully")

		case syscall.SIGINT, syscall.SIGTERM:
			slog.Info("shutting down...")
			cfg.stop()
			cfg.saveSync()
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
}
