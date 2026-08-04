# Changelog

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
