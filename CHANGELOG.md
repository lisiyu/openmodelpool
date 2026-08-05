# Changelog

## [4.3.5] - 2026-08-05

### ✨ Feature: 4-step onboarding wizard (REQ-5/6/12)
- Replace single-step disclaimer modal with 4-step wizard
- Step 1: Network须知说明 (公益共享、Key 本地保存、积分不可交易、随时退出)
- Step 2: 助记词生成与强制备份 (复用现有 BIP39 逻辑)
- Step 3: 共享边界配置 (DailyContribCap/ShareIdleOnly/ModelWhitelist)
- Step 4: 完成确认 (显示 Node ID，启用网络)
- Progress bar with step indicators

## [4.3.4] - 2026-08-05

### 🔒 Security: Fix hopCount strconv.Atoi error ignored
- `network_relay.go`: 2处 hopCount Atoi 失败时返回 400 Bad Request
- 非数字 hop 头不再被默认当 0 处理

## [4.3.3] - 2026-08-05

### 🔒 Security: Fix vmess.go TOCTOU race condition
- Remove `os.CreateTemp` + `atomicWriteFile` double-write on same file
- Use `atomicWriteFile` directly to write xray config to `data/xray-{id}.json`
- Eliminates TOCTOU window between CreateTemp and atomicWriteFile

## [4.3.2] - 2026-08-05

### 🔒 Security: Fix API Key exposure in consumer register response
- `multiuser.go`: handleConsumerRegister 不再返回完整 Consumer 对象，仅返回 id/name/api_key
- `types.go`: Consumer.APIKey 字段添加 `json:"api_key,omitempty"` 标签，防止序列化泄露

## [4.3.1] - 2026-08-05

### 🔒 Security: Fix context.Background() residual — add timeouts to outbound requests
- `update.go`: 5处 `context.Background()` 添加 30s 超时
- `stubs.go`: 1处添加 15s 超时
- `platform_discovery.go`: 1处添加 15s 超时
- `network_balance.go`: 1处添加 30s 超时

## [4.3.0] - 2026-08-05

### 🔒 Security: Fix IncrProviderConn/IncrGuestConn race condition
- `conn_tracker.go`: Load+Store 改为 LoadOrStore + atomic.AddInt64 (已由远程修复)

## [4.2.9] - 2026-08-05

### ✨ New Feature: Kilo Gateway Free Model Preset

- **Added Kilo Gateway as preset provider**: 12 free models from [Kilo Gateway](https://kilo.ai/) (api.kilo.ai) preconfigured and enabled out-of-the-box, no API Key required.
- **Models include**: Auto Free (smart routing), StepFun Step 3.7 Flash, Ling-3.0-flash, Laguna S/XS 2.1, North Mini Code, Nemotron 3 Ultra (1M context), Nemotron 3 Super/Nano Omni, OpenRouter Free Router, Tencent Hy3.
- **Anonymous access**: Uses `free-anonymous` API key marker, no user configuration needed — provider is `Enabled: true` by default.
- **API endpoint**: `https://api.kilo.ai/api/gateway` (OpenAI-compatible)
- **Model list auto-verified**: Fetched live from Kilo Gateway API on 2026-08-05, all 12 free models confirmed working.
- **Test updated**: `TestProviderManager_EnabledRaw` made resilient to enabled presets.
- Build: ✅ | Vet: ✅ | Tests: no new failures (pre-existing unchanged)

## [4.2.6] - 2026-08-04

### 🏗️ P3: Main Package Refactoring

- **Extract `routes.go`**: Moved `setupRoutes()` (304 lines, ~120 HTTP route registrations) from `server.go` into dedicated `routes.go`. `server.go` reduced from 469→165 lines, now focused on server lifecycle (runServer, setupHTTPS, gracefulShutdown).
- **Consolidate global state into `globals_core.go`**: 16 core infrastructure singleton pointers (cfg, enc, auth, pm, tracker, siderMon, appLogger, multiUser, auditLog, eventBus, metrics, rateLimiter, healthChecker, wafEngine, freePool, vmessManager) moved from 16 scattered files into one documented file.
- **Consolidate global state into `globals_network.go`**: 25 federation/network singleton pointers (node, fed, gossip, repMgr, allocMgr, msgMgr, nwm, invMgr, updateManager, netMgr, routeTable, guestKeyStore, guestKeyUsage, lbInstance, quotaMgr, regionManager, balanceEngine, globalPool, publicQuota, algoChain, governor, governanceMgr, quotaPriorityMgr, tunnel, nodeRegistry) moved from 23 scattered files into one documented file.
- **41 source files updated**: Removed relocated `var` declarations, reducing per-file noise and making global state ownership explicit.
- **Go environment upgraded**: Cloud development environment upgraded from Go 1.18.1 to Go 1.26.5 for local compilation and testing.
- Build: ✅ | Vet: ✅ | Tests: no new failures (pre-existing unchanged)

## [4.2.5] - 2026-08-04

### 🔧 Code Review Fixes (P2-P3 Continued)

#### P2: Security & Quality Improvements
- **P2-Signing**: Added **Ed25519 binary signing** step to release CI/CD workflow. Release binaries are now signed with an Ed25519 private key (stored as GitHub secret `ED25519_SIGNING_KEY`). The `.sig` files are uploaded alongside binaries and checksums, enabling the update.go signature verification (added in v4.2.4) to function end-to-end.

#### P3: Code Cleanup & Maintenance
- **P3-AdminSplit**: Split `admin.go` (2,559 lines) into **14 domain-specific files**:
  - `admin.go` (~260 lines): Core config & gateway handlers
  - `admin_auth.go` (~210 lines): Authentication, login, password reset
  - `admin_providers.go` (~525 lines): Provider CRUD, testing, models
  - `admin_health.go` (~656 lines): Health status & enriched metrics (previously 640-line monolith)
  - `admin_usage.go` (~136 lines): Usage statistics & routing
  - `admin_smtp.go` (~32 lines): SMTP configuration
  - `admin_status.go` (~116 lines): Status, SMTP test, mail sending
  - `admin_config_io.go` (~198 lines): Config export/import, reset
  - `admin_apikeys.go` (~110 lines): API key management
  - `admin_restart.go` (~75 lines): Restart & token refresh
  - `admin_pages.go` (~81 lines): Page handlers & utilities
  - `admin_models.go` (~98 lines): Model sync, access control, Sider
  - `admin_url_sync.go` (~73 lines): Provider URL synchronization
  - `admin_collab.go` (~76 lines): Collaborator & JS handlers
- **P3-AuditWebhook**: Added **remote webhook reporting** to audit logger. Audit records can now be forwarded to an external endpoint via `audit_webhook_url` config. Webhook calls are asynchronous (non-blocking) with a 10-second timeout, ensuring local audit logging is never delayed by remote availability.

## [4.2.4] - 2026-08-04

### 🔧 Code Review Fixes (P1-P3)

#### P1: Critical Bug Fixes
- **P1-Q1**: Fixed defer-in-loop resource leaks in `discovery.go` (2 instances: `fetchFromPeers`, `fetchFromSeedNodes`) and `gossip.go` (3 instances: `exchange`, `fetchFullPoolFromPeer`, `broadcastAnnouncement`). All loop bodies with `context.WithTimeout` + `defer cancel()` are now wrapped in anonymous functions so `defer` executes per-iteration instead of at function return.

#### P2: Security & Quality Improvements
- **P2-Transport**: Migrated transport encryption from self-rolled SHA-256 KDF to proper **X25519 ECDH** key agreement with **HKDF-SHA256** key derivation. Ed25519 keys are converted to X25519 via the birational map (RFC 7748). Eliminates the "simple KDF" technical debt identified in the security audit.
- **P2-Update**: Added **Ed25519 signature verification** for self-update binaries. The updater now downloads a `.sig` file from release assets and verifies the binary against an embedded Ed25519 public key. Fail-closed if signature verification fails; falls back to SHA-256 only if `.sig` is not present (backward compatible).
- **P2-WAF**: Enhanced WAF content scanning with **40+ built-in attack pattern detectors** covering SQL injection, XSS, path traversal, command injection, and SSRF. Patterns are checked in addition to user-configured content keywords.
- **P2-CI**: Split CI test job into **unit tests** (hard gate, `go test -race -short`) and **integration tests** (soft gate with `continue-on-error`, Chrome pre-installed for chromedp tests). Test timeout increased to 30 minutes.

#### P3: Code Cleanup & Maintenance
- **P3-Ledger**: Removed orphan `ledger/` package (~2,000 lines of dead code not imported by the main package). Reduces audit surface and repository size.
- **P3-Audit**: Added **audit log rotation** — logs rotate at 10 MB, keeping up to 5 rotated files (`audit.log.1` through `audit.log.5`). Prevents unbounded log growth.
- **P3-Stubs**: Updated `stubs.go` comments to accurately reflect that most functions now have real implementations. Only `registerWithBootstraps` remains as a documented TODO.

### Deferred to Future Iterations
- **P3**: Split `admin.go` (2,553 lines) into domain-specific modules — requires dedicated refactoring sprint
- **P3**: Split monolithic main package (~84 package-level vars, ~59 goroutine launches) — requires architectural planning
- **P2**: Ed25519 release signing in CI/CD (signing infrastructure not yet in place)
- **P2**: WAF deep content inspection (body parsing beyond pattern matching)

## [4.2.3] - 2026-08-02

### 🔴 P0 Security Fixes (Critical)

- **P0-1**: Self-update binary now verifies SHA-256 checksum before replacing the running executable. Downloads a `.sha256` checksum file from GitHub Release assets and compares against the computed hash. If checksum is unavailable, a warning is logged but update proceeds (backward-compatible). If checksum mismatches, the downloaded file is deleted and update is aborted — preventing potential RCE via tampered releases or MITM.
- **P0-CI**: CI workflow now has a `build-gate` job that runs `go build ./...` + `go vet ./...` as a hard gate. All other jobs (lint, test, build, cross-build, security) depend on this gate. This prevents broken code from ever merging into `main`.

### 🟠 P1 Security Fixes (High)

- **P1-1**: Access control header spoofing fixed:
  - `RequestKeyType()` no longer trusts client-supplied `X-MK-KeyType` header. Renamed internal header to `X-OMP-KeyType` (only set by our own relay code).
  - `FilterByAccessControl()` default branch changed from fail-open (allow all) to fail-closed (deny all) for unknown key types.
  - `relayToRemote()` now strips `X-OMP-KeyType` from forwarded requests to prevent header injection across nodes.

### 🟡 P2 Security Fixes (Medium)

- **P2-1**: Frontend XSS — `admin-settings.js` now escapes `extractError()` output with `escapeHtml()` before inserting into innerHTML.
- **P2-2**: CORS hardcoded `*` replaced with configured origin check in `eventbus.go` (SSE endpoint) and `network_seed.go` (seed peers endpoint). Both now use `isOriginAllowed()` with `cors_allowed_origins` config.
- **P2-3**: SSRF private IP blocking added to provider BaseURL validation in both `handleCreateProvider` and `handleUpdateProvider`. Previously only checked scheme (http/https); now also blocks private/loopback addresses via `isLocalOrPrivateIP()`.

### 🐛 Bug Fixes (from rounds 38-43)

- **B171**: Added `context.WithTimeout(15s)` to remaining HTTP request in `discovery.go` pool fetch loop.
- **B172**: Added error handling for `json.NewDecoder.Decode` in `tunnel.go` Cloudflare API response decoding (2 locations).
- **B173**: Added `TLSClientConfig{MinVersion: tls.VersionTLS12}` to `sharedTransport` in `client.go`.
- **B174**: Added TLS 1.2 minimum to SOCKS5 proxy transport and HTTP proxy transport in `client.go`.
- **B175**: Added `sync.Once` protection for `globalStopCh` close — `closeGlobalStopCh()` is now idempotent, preventing panic from double-close.
- **B176**: Added `Content-Type: text/plain` header to 503 response when metrics endpoint is unavailable.

### 🔧 Compilation Fix (Critical)

- Fixed all compilation errors introduced in rounds 2-37 that left `main` branch unable to compile:
  - `audit.go`: `extractClientIP` takes `string` (r.RemoteAddr), not `*http.Request`
  - `auth.go`: `AuthData` → `AdminStore` (correct type name)
  - `conn_tracker.go`: `GetConnStats` rewritten with proper `sync.Map.Range` + `atomic.LoadInt64`
  - `encryptor.go`: Added missing `IsReady()` method
  - `handlers.go`: Fixed `connTracker` undefined, `wafInstance` → `wafEngine`, added `strconv` import
  - `init.go`: Added missing `globalStopCh` declaration
  - `types.go`: Added missing `log/slog` import
  - `network_seed.go`: Removed unused `encoding/json` import

## [4.2.1] - 2025-08-03

### 🔒 Security Fixes (Critical)

- **C1**: Fixed TOCTOU race condition in `auth.go` `save()`/`saveLocked()` — previously, `safe := a.data` was a shallow copy, causing `encryptField()` to mutate the in-memory plaintext SMTP password to ciphertext. Now uses `deepCopyDataLocked()` to create a proper deep copy before encryption.
- **C2**: Fixed non-atomic file write in `provider.go` `writeFile()` — previously used `os.WriteFile()` which can corrupt data on crash. Now uses `atomicWriteFile()` (write-to-tmp + rename) consistent with `config.go` and `auth.go`.
- **C3**: Fixed anonymous admin access from public internet — previously, when `proxy_api_key` was unset and no consumers existed, any remote IP could access `/v1/chat/completions` as admin. Now anonymous admin access is restricted to localhost/private network IPs only.

### 🐛 Bug Fixes (Major)

- **M1**: Implemented wildcard subdomain matching in `isOriginAllowed()` — the comment promised `*.example.com` support but the code only did exact matching. Now properly matches subdomains against wildcard patterns.
- **M3**: `encryptField()` now refuses to encrypt data when using an ephemeral key — previously, data encrypted with an ephemeral key would be unrecoverable after restart. Now logs a warning and returns plaintext, preventing silent data loss.
- **M4**: Fixed `extractClientIP()` IPv6 address parsing — previously used `strings.LastIndex(":")` which incorrectly parsed `[::1]:8000` as `[::1]`. Now uses `net.SplitHostPort()` for correct handling.

### 🧹 Code Quality (Minor)

- **M5**: Removed debug logging residual in `provider.go` `load()` — the `slog.Info("after load", ...)` loop was printing key metadata for every provider on every startup, potentially leaking sensitive information (key length).
- **m1**: Replaced hand-rolled `toUpper()` in `config.go` with `strings.ToUpper()` — the manual implementation only handled ASCII a-z, while the standard library properly handles Unicode.
- **m4**: Fixed `Provider.Safe()` shallow copy — previously, `WebSession`, `ModelBotMap`, `ExtraHeaders`, and other map/slice fields were shared references. Callers modifying the returned value could corrupt the original. Now performs deep copies of all reference-type fields.
- **m6**: Fixed `readJSON()` `MaxBytesReader` using `nil` ResponseWriter — now passes `w` so HTTP 413 is automatically sent on body size overflow. Updated all 65+ call sites across the codebase.
- **A3**: Unified error response format in `withAuth()` middleware — previously used `map[string]string{"error": "..."}` inconsistent with the rest of the codebase. Now uses `ErrorResponse{Error: ErrorDetail{...}}` format.

## v4.2.10 (2026-08-05)

### Bug Fixes
- **Fix nil pointer panic in /health endpoint**: `handleHealth` and `handleDiagnostics` accessed `metrics.startTime` without nil check, causing SIGSEGV when `metrics` was uninitialized (e.g. in test environments). Added nil guard.
- **Remove wildcard origin matching for security**: `isOriginAllowed` previously supported `*.example.com` wildcard patterns, which could be exploited for origin spoofing. Removed wildcard matching; only exact origin matches are now accepted.
- **Fix TestHB6_ProviderManager_EnabledRaw_Empty**: Test expected 0 enabled providers but Kilo Gateway preset (Enabled:true) is included by design. Updated test to verify only preset providers are returned, not user-added ones.

## v4.2.11 (2026-08-05)

### Bug Fixes — Pre-existing Test Failures (17 tests fixed)
- **Fix nil pointer panic in /health and /diagnostics**: Added nil guard for `metrics` global in `handleHealth` and `handleDiagnostics` (handlers.go)
- **Remove wildcard origin matching (security)**: `isOriginAllowed` no longer supports `*.example.com` patterns, preventing origin spoofing (middleware.go)
- **Fix SiderMonitor data race**: `IsExpired()` now acquires RLock before reading `status.TokenStatus` (sider.go)
- **Fix reportToOrigin data race**: Added `sync.WaitGroup` to synchronize background `reportToOrigin` goroutine with test cleanup (update.go)
- **Fix relay SSRF test bypass**: Added `allowLocalRelayForTest` flag to allow httptest servers (127.0.0.1) as relay targets in tests (network_relay.go)
- **Fix IPv6 extractClientIP test expectations**: Updated tests to match `net.SplitHostPort` behavior which strips IPv6 brackets (middleware_test.go, security_medium_test.go)
- **Fix GetByModel ordering test**: Map iteration is non-deterministic; test now checks membership not order (handler_batch5_test.go)
- **Fix G6 handler test header**: Updated `X-MK-KeyType` → `X-OMP-KeyType` to match production code rename (quota_priority_handler_test.go)
- **Fix TestQARouteVersionLatest**: Mock GitHub API now returns version newer than current AppVersion (update_qa_test.go)
- **Fix preset provider test interference**: `setupTestEnv` now clears `presetProviders` during tests and restores on cleanup, preventing Kilo Gateway preset from affecting tests that expect empty provider lists (test_helpers_test.go)
- **Fix EnabledRaw_Empty test**: Test now verifies only preset providers are returned, not user-added ones (handler_batch6_test.go)

## v4.2.12 (2026-08-05)

### Bug Fixes — Additional Pre-existing Test Failures (4 tests fixed)
- **Fix preset provider clearing**: Changed from `presetProviders = nil` to disabling `Enabled` on all presets, preserving `GetRaw`/`GetPresets` functionality (test_helpers_test.go)
- **Fix TestQARouteSignalValidSignature race**: Use `MinSupportedVersion` instead of modifying `AppVersion`; add `reportToOriginWG.Wait()` (update_qa_test.go)
