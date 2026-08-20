package main

import (
	"net/http"
)

func setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", handleHealth)
	// Version (public, no auth) — used by monitoring & auto-update scripts
	mux.HandleFunc("GET /api/version", handleVersion)

	// OpenAI-compatible endpoints — Gateway mode
	// §10A: WAF is enforced on the inbound proxy path (no-op until enabled).
	mux.HandleFunc("GET /v1/models", withProxyAuth(wafMiddleware(rateLimitMiddleware(handleGatewayModels))))
	mux.HandleFunc("POST /v1/chat/completions", withProxyAuth(wafMiddleware(rateLimitMiddleware(handleGatewayRequest))))
	mux.HandleFunc("POST /v1/completions", withProxyAuth(wafMiddleware(rateLimitMiddleware(handleGatewayRequest))))
	mux.HandleFunc("POST /v1/embeddings", withProxyAuth(wafMiddleware(rateLimitMiddleware(handleGatewayRequest))))
	// Anthropic Messages API compatibility — for Claude Code and other Anthropic clients
	mux.HandleFunc("POST /v1/messages", anthropicAuthAdapter(withProxyAuth(wafMiddleware(rateLimitMiddleware(handleAnthropicMessages)))))
	// Azure OpenAI URL compatibility — accepts /openai/deployments/{deployment}/chat/completions
	// (the deployment name is used as the model). Response is OpenAI-format, no translation needed.
	mux.HandleFunc("POST /openai/deployments/{deployment}/chat/completions", azureAuthAdapter(withProxyAuth(wafMiddleware(rateLimitMiddleware(handleAzureChatCompletions)))))
	// Google Gemini native API compatibility — accepts /v1beta/models/{model}:generateContent
	// and /v1beta/models/{model}:streamGenerateContent, translating to/from OpenAI format.
	mux.HandleFunc("POST /v1beta/models/{model}", geminiAuthAdapter(withProxyAuth(wafMiddleware(rateLimitMiddleware(handleGeminiGenerateContent)))))

	// Seed discovery endpoints (public, no auth required)
	mux.HandleFunc("GET /api/peers", rateLimitByIP(30, "peers")(handleSeedPeers))
	mux.HandleFunc("POST /api/register", rateLimitByIP(5, "register")(handleSeedRegister))
	mux.HandleFunc("GET /api/seed/health", rateLimitByIP(30, "seed_health")(handleSeedHealth))

	// Auth (public)
	mux.HandleFunc("GET /api/setup/status", handleSetupStatus)
	mux.HandleFunc("GET /api/addresses", rateLimitByIP(10, "addresses")(handleGetAddresses))
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
	// Gateway mark (本节点是否为网络入口节点)
	mux.HandleFunc("GET /api/gateway", withAuth(handleGetGateway))
	mux.HandleFunc("POST /api/gateway", rateLimitByIP(20, "gateway_set")(withAuth(handleSetGateway)))
	mux.HandleFunc("GET /api/status", withAuth(handleStatus))
	mux.HandleFunc("GET /api/admin/info", withAuth(handleAdminInfo))
	mux.HandleFunc("GET /api/admin/diagnostics", rateLimitByIP(10, "diagnostics")(withAuth(handleDiagnostics)))
	mux.HandleFunc("GET /api/admin/security/check", rateLimitByIP(10, "security_check")(withAuth(handleSecurityCheck)))
	mux.HandleFunc("GET /api/admin/goroutines", rateLimitByIP(5, "goroutines")(withAuth(handleGoroutineDump)))
	// Ledger transparency (P2-2): where contributed compute came from + integrity
	mux.HandleFunc("GET /api/admin/ledger/transparency", rateLimitByIP(10, "ledger_transparency")(withAuth(handleAdminLedgerTransparency)))
	mux.HandleFunc("GET /api/admin/ledger/contribution-quota", rateLimitByIP(10, "ledger_quota")(withAuth(handleAdminLedgerContributionQuota)))
	// Ledger export for research / openness (P4-1): JSON (full) or CSV (contributions)
	mux.HandleFunc("GET /api/admin/ledger/export", rateLimitByIP(10, "ledger_export")(withAuth(handleLedgerExport)))
	mux.HandleFunc("GET /api/admin/audit", rateLimitByIP(10, "audit")(withAuth(handleAuditLog)))
	mux.HandleFunc("POST /api/admin/change-password", rateLimitByIP(3, "change_password")(withAuth(handleChangePassword)))
	mux.HandleFunc("POST /api/admin/update-email", rateLimitByIP(5, "update_email")(withAuth(handleUpdateEmail)))
	// One-click version update (incremental)
	mux.HandleFunc("GET /api/admin/version/latest", withAuth(handleAdminVersionLatest))
	mux.HandleFunc("POST /api/admin/update/start", rateLimitByIP(3, "update_start")(withAuth(handleAdminUpdateStart)))
	mux.HandleFunc("GET /api/admin/update/status", withAuth(handleAdminUpdateStatus))
	// Federation cross-node update signal + report-back
	mux.HandleFunc("POST /api/federation/update-signal", rateLimitByIP(30, "update_signal")(withFederationAuth(handleFederationUpdateSignal)))
	mux.HandleFunc("POST /api/federation/update-report", rateLimitByIP(30, "update_report")(withFederationAuth(handleFederationUpdateReport)))
	// Ledger redundancy / federation reconciliation (P1-3): manifest + sync + record pull
	mux.HandleFunc("GET /ledger/__manifest", withFederationAuth(handleLedgerManifest))
	mux.HandleFunc("POST /ledger/__sync", withFederationAuth(handleLedgerSync))
	mux.HandleFunc("GET /ledger/__record", withFederationAuth(handleLedgerRecord))
	// P2-1 community co-governance (contributors govern; lightweight, no penalties)
	mux.HandleFunc("POST /api/governance/propose", rateLimitByIP(10, "gov_propose")(withFederationAuth(handleGovernancePropose)))
	mux.HandleFunc("POST /api/governance/ratify", rateLimitByIP(30, "gov_ratify")(withFederationAuth(handleGovernanceRatify)))
	mux.HandleFunc("GET /api/governance/proposals", rateLimitByIP(30, "gov_list")(handleGovernanceProposals))
	mux.HandleFunc("POST /api/admin/restart", rateLimitByIP(3, "restart")(withAuth(handleRestart)))
	mux.HandleFunc("GET /api/share/info", withAuth(handleShareInfo))

	// Provider management (admin + consumer)
	mux.HandleFunc("GET /api/providers", withConsumerOrAdminAuth(handleListProviders))
	mux.HandleFunc("GET /api/providers/presets", rateLimitByIP(30, "presets")(handleGetPresets))
	mux.HandleFunc("POST /api/providers", withConsumerOrAdminAuth(handleCreateProvider))
	mux.HandleFunc("GET /api/providers/{id}", withConsumerOrAdminAuth(handleGetProvider))
	mux.HandleFunc("PUT /api/providers/{id}", withConsumerOrAdminAuth(handleUpdateProvider))
	mux.HandleFunc("DELETE /api/providers/{id}", withConsumerOrAdminAuth(handleDeleteProvider))
	mux.HandleFunc("POST /api/providers/{id}/test", withConsumerOrAdminAuth(handleTestProvider))
	mux.HandleFunc("POST /api/providers/{id}/test-all-keys", withConsumerOrAdminAuth(handleTestAllKeys))
	mux.HandleFunc("GET /api/providers/{id}/models", withConsumerOrAdminAuth(handleGetProviderModels))
	mux.HandleFunc("POST /api/providers/{id}/sync-url", withConsumerOrAdminAuth(handleSyncProviderURL))
	mux.HandleFunc("POST /api/providers/{id}/sync-models", withConsumerOrAdminAuth(handleSyncModels))
	mux.HandleFunc("POST /api/providers/{id}/browser-login/start", rateLimitByIP(5, "browser_login")(withAuth(handleBrowserLoginStart)))
	mux.HandleFunc("GET /api/providers/{id}/browser-login/status", withAuth(handleBrowserLoginStatus))
	mux.HandleFunc("POST /api/providers/{id}/browser-login/login", rateLimitByIP(5, "browser_login")(withAuth(handleBrowserLoginLogin)))
	mux.HandleFunc("POST /api/providers/{id}/browser-login/action", rateLimitByIP(10, "browser_login")(withAuth(handleBrowserLoginAction)))
	mux.HandleFunc("POST /api/providers/{id}/browser-login/finish", withAuth(handleBrowserLoginFinish))
	mux.HandleFunc("DELETE /api/providers/{id}/browser-login", withAuth(handleBrowserLoginCancel))
	// Provider access control (admin only)
	mux.HandleFunc("GET /api/providers/{id}/access-control", withAuth(handleGetProviderAccessControl))
	mux.HandleFunc("PUT /api/providers/{id}/access-control", rateLimitByIP(20, "access_control")(withAuth(handleUpdateProviderAccessControl)))
	mux.HandleFunc("POST /api/providers/sync-all-urls", rateLimitByIP(5, "sync_all_urls")(withAuth(handleSyncAllURLs)))

	// Provider multi API key management (admin + consumer)
	mux.HandleFunc("GET /api/providers/{id}/keys", withConsumerOrAdminAuth(handleListAPIKeys))
	mux.HandleFunc("POST /api/providers/{id}/keys", withConsumerOrAdminAuth(handleAddAPIKey))
	mux.HandleFunc("PUT /api/providers/{id}/keys/{key_id}", withConsumerOrAdminAuth(handleUpdateAPIKey))
	mux.HandleFunc("DELETE /api/providers/{id}/keys/{key_id}", withConsumerOrAdminAuth(handleDeleteAPIKey))
	mux.HandleFunc("POST /api/providers/{id}/keys/{key_id}/reset-quota", withConsumerOrAdminAuth(handleResetKeyQuota))

	// Sider status
	mux.HandleFunc("GET /api/providers/sider/status", withConsumerOrAdminAuth(handleSiderStatus))
	mux.HandleFunc("POST /api/providers/sider/test", withConsumerOrAdminAuth(handleSiderTest))

	// Free pool management (awesome-free-llm-apis sync)
	mux.HandleFunc("GET /api/admin/free-pool/status", withAuth(handleFreePoolStatus))
	mux.HandleFunc("POST /api/admin/free-pool/sync", withAuth(handleFreePoolSync))
	mux.HandleFunc("PUT /api/admin/free-pool/config", withAuth(handleFreePoolConfig))
	mux.HandleFunc("PUT /api/admin/free-pool/{providerId}/key", withAuth(handleFreePoolSetKey))
	mux.HandleFunc("DELETE /api/admin/free-pool/{providerId}/key", withAuth(handleFreePoolRemoveKey))

	// Platform discovery (admin + consumer)
	mux.HandleFunc("GET /api/discovery/platforms", withConsumerOrAdminAuth(handleGetDiscoveredPlatforms))
	mux.HandleFunc("PUT /api/discovery/platforms/{id}", withAuth(handleUpdateDiscoveredPlatform))
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
	mux.HandleFunc("GET /api/smtp/status", withAuth(handleSMTPStatus))
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
	mux.HandleFunc("GET /api/domain/binding-status", withAuth(handleDomainBindingStatus))
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
	mux.HandleFunc("GET /api/metrics", withAuth(handleAPIMetrics))

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
	mux.HandleFunc("GET /admin/free-pool", handleFreePoolPage)
	mux.HandleFunc("GET /admin/federation-health", handleFederationHealthPage)
	mux.HandleFunc("GET /admin-common.js", handleAdminCommonJS)
	mux.HandleFunc("GET /admin-settings.js", handleAdminSettingsJS)
	mux.HandleFunc("GET /admin-network.js", handleAdminNetworkJS)
	mux.HandleFunc("GET /admin-share.js", handleAdminShareJS)
	mux.HandleFunc("GET /admin-update.js", handleAdminUpdateJS)
	mux.HandleFunc("GET /admin-logs.js", handleAdminLogsJS)
	mux.HandleFunc("GET /admin-ledger.js", handleAdminLedgerJS)

	// Federation API (v3.0)
	mux.HandleFunc("GET /api/federation/status", withAuth(handleFederationStatus))
	mux.HandleFunc("GET /api/federation/pool", withFederationAuth(handleFederationPool))
	mux.HandleFunc("POST /api/federation/gossip", withFederationAuth(handleFederationGossip))
	mux.HandleFunc("POST /api/federation/announce", withFederationAuth(handleFederationAnnounce))
	mux.HandleFunc("POST /api/federation/relay", rateLimitByIP(60, "federation_relay")(withProxyAuth(handleRelayRequest)))
	mux.HandleFunc("GET /api/federation/reputations", withAuth(handleGetReputations))
	mux.HandleFunc("POST /api/federation/score", withAuth(handlePostScore))
	mux.HandleFunc("GET /api/federation/health", withAuth(handleFederationHealth))

	// v2.0: Quota allocation (replaces old credits system)
	mux.HandleFunc("GET /api/network/quota-allocation", withAuth(handleGetQuotaAllocation))
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
	mux.HandleFunc("GET /api/federation/genesis", withAuth(handleGetGenesis))
	mux.HandleFunc("POST /api/federation/invites", withAuth(handleCreateInvite))
	mux.HandleFunc("GET /api/federation/invites", withAuth(handleListInvites))
	mux.HandleFunc("POST /api/federation/invites/verify", rateLimitByIP(10, "invite_verify")(handleVerifyInvite))

	// P2P Shared Network API (Phase 1) — decentralized relay
	mux.HandleFunc("GET /api/network/status", withAuth(handleNetworkStatus))
	mux.HandleFunc("GET /api/network/stats", withAuth(handleNetworkStats))
	mux.HandleFunc("POST /api/network/consent", rateLimitByIP(5, "network_consent")(withAuth(handleNetworkConsent)))
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
	// P0-1: reverse-registration notify endpoint. Rate-limited and protected by an
	// ed25519 signature (verified against the embedded public key) — NOT wrapped
	// in withFederationAuth/withAuth, because on first contact the sender is not
	// yet in our trust pool (would 403) and a cross-instance admin JWT is absent.
	mux.HandleFunc("POST /api/network/peers/notify", rateLimitByIP(10, "network_notify")(handleNetworkPeersNotify))
	mux.HandleFunc("DELETE /api/network/peers/{id}", withAuth(handleNetworkRemovePeer))
	mux.HandleFunc("GET /api/network/resolve/{id}", withAuth(handleNetworkResolve))
	mux.HandleFunc("GET /api/network/routes", withAuth(handleNetworkRoutes))
	mux.HandleFunc("GET /api/network/join-conditions", withAuth(handleNetworkJoinConditions))
	mux.HandleFunc("GET /api/network/idle-quota", withAuth(handleIdleQuotaCheck))

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
	mux.HandleFunc("GET /api/node/info", withAuth(handleNodeInfo))

	// Algorithm Chain & Quota (Phase 3)
	mux.HandleFunc("GET /api/network/algorithm/current", withAuth(handleAlgorithmCurrent))
	mux.HandleFunc("GET /api/network/algorithm/history", withAuth(handleAlgorithmHistory))
	mux.HandleFunc("POST /api/network/algorithm/propose", withAuth(handleAlgorithmPropose))
	mux.HandleFunc("POST /api/network/algorithm/vote", withAuth(handleAlgorithmVote))
	mux.HandleFunc("POST /api/network/algorithm/gossip", rateLimitByIP(30, "algo_gossip")(handleAlgorithmGossip))
	mux.HandleFunc("GET /api/network/algorithm/proposals", withAuth(handleAlgorithmProposals))
	mux.HandleFunc("POST /api/network/algorithm/proposals/{id}/resolve", withAuth(handleAlgorithmProposalResolve))
	mux.HandleFunc("GET /api/network/algorithm/validate", withAuth(handleAlgorithmValidate))
	mux.HandleFunc("GET /api/network/open-key-quota", withAuth(handleOpenKeyQuota))
	mux.HandleFunc("GET /api/network/open-key-quota/all", withAuth(handleOpenKeyQuotaAll))

	// Global Pool & Global Keys (Phase 4)
	mux.HandleFunc("GET /api/network/global-pool", withAuth(handleGlobalPoolStatus))
	mux.HandleFunc("POST /api/network/global-pool/join", withAuth(handleGlobalPoolJoin))
	mux.HandleFunc("POST /api/network/global-pool/contribute", withAuth(handleGlobalPoolContribute))
	mux.HandleFunc("GET /api/network/global-pool/nodes", withAuth(handleGlobalPoolNodes))
	mux.HandleFunc("GET /api/network/global-pool/stats", withAuth(handleGlobalPoolStats))

	// §10A: WAF status & management
	mux.HandleFunc("GET /api/waf/status", withAuth(handleWAFStatus))
	mux.HandleFunc("GET /api/waf/violations", withAuth(handleWAFViolations))
	mux.HandleFunc("GET /api/waf/bans", withAuth(handleWAFBans))
	mux.HandleFunc("POST /api/waf/unban/{key}", withAuth(handleWAFUnban))

	// §3.2.3: Public key quota status
	mux.HandleFunc("GET /api/network/public-key-quota", withAuth(handlePublicKeyQuotaStatus))

	mux.HandleFunc("POST /api/network/capability/claim", withAuth(handleCapabilityClaim))
	mux.HandleFunc("GET /api/network/capability/claims", withAuth(handleCapabilityClaims))
	mux.HandleFunc("GET /api/network/capability/verify/{peer_id}", withAuth(handleCapabilityVerify))

	mux.HandleFunc("GET /api/network/ledger/contributions", withAuth(handleLedgerContributions))
	mux.HandleFunc("GET /api/network/ledger/balance/{node_id}", withAuth(handleLedgerBalance))
	mux.HandleFunc("GET /api/network/ledger/transactions", withAuth(handleLedgerTransactions))

	// Dynamic Load Balancer (Phase 4)
	mux.HandleFunc("GET /api/network/loadbalancer/status", withAuth(handleLBStatus))
	mux.HandleFunc("GET /api/network/loadbalancer/nodes", withAuth(handleLBNodes))
	mux.HandleFunc("GET /api/network/loadbalancer/metrics/{node_id}", withAuth(handleLBNodeMetrics))
	mux.HandleFunc("PUT /api/network/loadbalancer/config", withAuth(handleLBConfigUpdate))
	mux.HandleFunc("GET /api/network/heartbeat/ping", rateLimitByIP(60, "heartbeat_ping")(handleHeartbeatPing))

	// Cross-Region Routing (Phase 4)
	mux.HandleFunc("GET /api/network/regions", withAuth(handleNetworkRegions))
	mux.HandleFunc("GET /api/network/regions/{region}/nodes", withAuth(handleNetworkRegionNodes))
	mux.HandleFunc("PUT /api/network/regions/config", withAuth(handleNetworkRegionConfigUpdate))

	// Dynamic Balance Engine (Phase 4)
	mux.HandleFunc("GET /api/network/balance/status", withAuth(handleBalanceStatus))
	mux.HandleFunc("GET /api/network/balance/nodes", withAuth(handleBalanceNodes))
	mux.HandleFunc("GET /api/network/balance/adjustments", withAuth(handleBalanceAdjustments))
	mux.HandleFunc("POST /api/network/balance/recalculate", withAuth(handleBalanceRecalculate))

	RegisterNATRoutes(mux)

	mux.HandleFunc("POST /api/ticket/submit", withAuth(handleTicketSubmit))
	mux.HandleFunc("POST /api/ticket/notarize", withFederationAuth(handleNotarize))
	mux.HandleFunc("GET /api/ticket/anti-collusion", withAuth(handleAntiCollusionCheck))

	// P2P Relay: /network/{node_id}/{rest...} — any shared node can relay
	// §10A: WAF is enforced on the relay proxy path (no-op until enabled).
	// SEC-P0-1: relay routes require a valid API key or signed relay forward;
	// anonymous clients can no longer reach local-only endpoints via the relay.
	mux.HandleFunc("GET /network/{id}/", relayAuthMiddleware(wafMiddleware(handleNetworkRelay)))
	mux.HandleFunc("POST /network/{id}/", relayAuthMiddleware(wafMiddleware(handleNetworkRelay)))
	mux.HandleFunc("PUT /network/{id}/", relayAuthMiddleware(wafMiddleware(handleNetworkRelay)))
	mux.HandleFunc("DELETE /network/{id}/", relayAuthMiddleware(wafMiddleware(handleNetworkRelay)))
	mux.HandleFunc("GET /network/{id}", relayAuthMiddleware(wafMiddleware(handleNetworkRelay)))
	mux.HandleFunc("POST /network/{id}", relayAuthMiddleware(wafMiddleware(handleNetworkRelay)))

	return mux
}

// setupHTTPS configures HTTPS with Let's Encrypt auto-cert if public_url is https://.
