# Changelog

## v4.5.3 (2026-08-15)

Bug fix release (self-update card correctness + China-region update reachability):

- **Update card double-v label and false "不一致" warning fixed.** `admin-update.js` compared versions by raw string (GitHub tag `v4.5.2` vs `AppVersion` `4.5.2`), which raised a spurious "vv4.5.2 与当前运行 v4.5.2 不一致" warning and a `vv` prefix. Now uses the same semantic `compareVersionStr` as the version card plus `vLabel()` for display.
- **Stale "failed" update status auto-cleared on restart.** `update.go::reconcilePending` now resets a `failed` local status whose recorded target already equals the running `AppVersion` (e.g. an aborted in-app self-update later achieved via external deploy), preventing a permanent misleading "失败" badge.
- **Verification files now fall back to mirrors.** `.sig` / `.sha256` are fetched from canonical GitHub FIRST and fall back to the region-aware mirrors (`githubDownloadMirrors`) only when the official source is unreachable, fixing the China-region self-update failure (`无法获取 GitHub 官方 Ed25519 签名 ... context deadline exceeded`). Security is unchanged: the checksum is still verified by SHA-256 against the downloaded binary and the signature by the hardcoded Ed25519 public key, so a poisoned mirror cannot forge a valid signature or a matching checksum.
- AppVersion bumped to 4.5.3.

## v4.5.2 (2026-08-12)

Maintenance release (fold in-flight work into mainline):

- **Admin ledger transparency panel (P2-2(ii))** and **region sync local reconciliation (P5-4)** landed on mainline.
- **Governance / network-quota modules** added.
- **Security regression tests**: ticket double-sign, update fail-closed, discovery poisoning.
- **omp-manager checksum trust fix** (canonical-GitHub-only artifact fetch).
- AppVersion bumped to 4.5.2; folds in the v4.5.0 / v4.5.1 keyless free-pool provider fixes.

## v4.5.1 (2026-08-12)

Bug fix (anonymous free-pool providers, follow-up to v4.5.0):

- **Free-pool providers no longer report a false "key failed" on connectivity test.** v4.5.0 fixed the empty-`APIKeys` misreport but still probed these providers with `Authorization: Bearer free-anonymous`, which the upstream rejects (OVHcloud `/v1/models` returns HTTP 403 for a bogus token but 200 with no token at all — verified). Added `testKeylessConnectivity`, which probes `/models` **without any token** and treats HTTP 2xx as reachable; both `handleTestAllKeys` (keyless branch) and `testConnection` (single-test path) now route anonymous providers (`APIKey=="free-anonymous"`) through it. Failure messages are no longer collapsed to a generic "upstream error", so a genuinely unreachable endpoint still surfaces the real reason. Regression coverage: test-plan case **T-P1-6b**.

## v4.5.0 (2026-08-12)

Bug fix (keyless free-pool providers):

- **Admin panel no longer misreports keyless free-pool providers as "未配置任何 Key".** `handleTestAllKeys` (`admin_providers.go:408`) returned `success:false` + an empty `results` array whenever a provider had no API keys in its `APIKeys` slice — which is exactly the case for the out-of-the-box public free-pool providers whose `APIKey` is the literal `free-anonymous`. The admin UI (`admin.html:2281`, `admin-provider.html:1391`) then toasted "未配置任何 Key" on every "测试全部 Key" click. The handler now detects keyless providers via `isFreePoolProvider` and verifies upstream connectivity through `testConnectionWithKey(p, p.APIKey)`, returning a single `keyless:true` result so the panel reports the true connectivity state instead of a false "no keys" error. Regression coverage tracked as test-plan case **T-P1-6b**.

## v4.4.44 (2026-08-11)

Security, reliability and honesty pass: 72 findings from an independent external code review triaged and fixed (a full-team triage record is in `docs/reference/REVIEW-TRIAGE-2026-08-10.md`).

### Security (P0)
- **Closed the relay-to-self authentication bypass.** `/network/{id}/...` routes are now gated by `relayAuthMiddleware`; `handleRelayToLocal` dispatches in-process while preserving the original `RemoteAddr` and marking the request as untrusted, so the "localhost anonymous admin" fallback in `withProxyAuth` and the `localOnly` guard can no longer be reached via a loopback re-dispatch. Relayable paths are whitelisted to `/v1/*` and `/api/network/heartbeat/ping`.
- **Stopped trusting client-supplied `X-OMP-KeyType`.** It is stripped at the earliest middleware and in both relay Directors; `RequestKeyType` derives the key type from the verified token/context instead. The CHANGELOG's earlier claim that this header was already stripped was itself a false record — the strip had never been implemented (the header was only renamed). Corrected.
- **Fixed the federation trust-pool poisoning.** `peers/notify` no longer falls back to the attacker-supplied `PubKey` from the payload when key fetch fails; verification now fails closed.
- **Seed registration is now fail-closed** when no `seed_secret` is configured; secret comparison uses constant-time compare.

### Security (high/medium)
- Update pipeline is fail-closed: checksum and signature are fetched only from canonical GitHub (never mirrors), and a missing or mismatched artifact aborts the update instead of warn-and-continue.
- Provider config import validates each provider with the shared `validateProviderBaseURL` (scheme + private-IP check), merges instead of truncating, and no longer deadlocks (see below).
- Plus: CSV formula-injection neutralisation, X-Forwarded-For gated behind `OMP_TRUSTED_PROXY`, heartbeat defaults to deny, `ReadHeaderTimeout: 10s`, constant-time reset-token compare, conditional CORS credentials, extended private-range CIDRs for the SSRF guard, goroutine-dump gated behind a debug flag, `restart.sh` ownership/permission check, `install.sh` fail-closed checksum, `handleDirectProbe` URL validation, generic error messages, fixed `escapeJS` escaping.

### Reliability
- **Fixed two deadlocks that shipped in v4.3.32** (config import and governance `Propose`/`Ratify` both called `save()` while holding the write lock; `save()` then re-acquired an RLock on a non-reentrant RWMutex). Both are fixed with lock-safe save paths (`saveLocked`), and both now have tests that run with a real `dataPath` — the tests hang on the old code and pass in milliseconds on the new.
- Removed a leaking read-lock in `CheckShareBoundary` (`defer RUnlock`), converted the per-request O(n) ledger scan to an O(1) daily counter, bounded replication fan-out with a worker pool, added `globalStopCh` shutdown paths to seven background loops, bounded unbounded maps, shared the HTTP client pool, and made the concurrency semaphore time out with a 503 instead of blocking forever.

### Usability
- `authFetch` throws on non-401 errors and success toasts are gated on the actual response; restart failure is reported instead of fake success; the log panel gained pagination, filtering and an error column; update-check failures are visible; `prompt()` chains were replaced with a modal; the setup wizard no longer destroys the access URL.

### Documentation honesty
- `docs/FEATURES.md`: node-identity section corrected (it is implemented, not stubbed); removed false Plumtree/Scuttlebutt and mDNS claims; replication section updated to the delivered P1-3 state; undocumented live endpoints (Gemini/Azure entry, governance, transparency/export, ledger replication, `/network/__punch`, `X-OMP-Quota-Source`) recorded in FEATURES and API docs.
- `docs/BACKLOG.md`: split DHT transport bridging, UDP data-carrying protocol, governance execution hooks, transparency UI panel and the no-op region-sync loop into honest open items.

## v4.3.32 (2026-08-10)

CI: align the test gate with what contributors actually run locally.

### Changed
- **Removed a test tier that never existed.** The hard gate ran `go test -race -short ./...`, and the second job set `OMP_TEST_INTEGRATION=1` with a comment claiming it covered "integration tests tagged with `//go:build integration`". None of that was real: the repository contains no `testing.Short()` branches, no build tags, and no code reading that environment variable. The `-short` flag excluded exactly zero tests while implying a tier that was never written — so the gate was rewritten rather than extended. It now runs `go test -race -count=1 -timeout 25m ./...`, the same suite a contributor runs locally with `-race` added. `-count=1` disables the test result cache, because a cached PASS vouches for an earlier commit rather than the current one, which is where flaky tests hide.
- **The second CI job is now an honest flaky watch.** Instead of posing as a different test tier, it re-runs the identical suite as a soft gate. A test that fails one run in three survives a single run two times out of three; an independent second run materially improves the odds of catching it before a contributor does — the v4.3.30 flaky test took three consecutive local runs to reproduce. Chrome remains preinstalled so that any test which grows a real chromedp dependency fails visibly instead of skipping silently.

### Documentation
- `CONTRIBUTING.md` and `README.md` now state the gate accurately: CI runs the same suite with `-race` on top, so it can only be stricter than a local run. Neither claims a green local run guarantees a green CI run — race-only failures are precisely the gap `-race` exists to close.

## v4.3.31 (2026-08-09)

Scripts: region-aware smart mirror selection for one-click install/update scripts.

### Features
- **Region detection + smart mirror for all one-click scripts.** `scripts/omp-manager.ps1` and `.devcontainer/run-omp.sh` gained `detect_region`/`Get-Region` plus a four-source GitHub mirror list (`ghfast.top` → `gh-proxy.com` → `ghproxy.net` → `mirror.ghproxy.com`) with region-sorted candidate selection — `cn` mirrors first, `global` direct first. `scripts/install.sh` and `scripts/omp-manager.sh` already had this; all four now follow the same mandatory SOP, so GitHub Release downloads no longer stall on mainland China networks.

## v4.3.30 (2026-08-09)

Post-release sustainability pass: make the project trustworthy for the first outside contributor.

### Bug Fixes
- **Consumer usage stats were lost on every restart.** `gracefulShutdown` stopped a dozen subsystems but never stopped `multiUser`, and `RecordConsumerUsage` only marks the manager dirty below its batch threshold of 10 — so up to five seconds of every consumer's token and request counts were silently dropped on each shutdown. `StopBatchSave()` was meanwhile dead code that nothing called, and its non-blocking send on an unbuffered channel meant the stop signal was discarded outright whenever the loop happened to be inside `save()`.
- **Fixed the intermittent test failure that was hiding it.** `TestHB10_MultiUser_RecordConsumerUsage` failed roughly one run in three with `TempDir RemoveAll cleanup: The directory is not empty` — not an assertion failure, but the batch loop's final flush racing `t.TempDir()` cleanup. `MultiUserManager` now exposes a `saveDone` channel (mirroring the existing `stopCh`/`done` pair in `config.go`); `StopBatchSave` closes the stop channel idempotently and waits for the goroutine to exit, bounded by a 5s timeout so a wedged goroutine cannot hang shutdown. Verified: three consecutive full runs failed once before the fix and pass cleanly after.

### Documentation
- Added `CONTRIBUTING.md` — quick start, the four ways to contribute, the build/vet/test gate, code conventions (stdlib first, additive changes, don't oversell in docs), and what will not be merged.
- Added `SECURITY.md` — private reporting via GitHub Security Advisories, scope, and an explicit statement of what the software does *not* promise (provider keys sit in plaintext config; federation trust is reputational, not proof of honesty; don't expose the admin plane publicly).
- Added GitHub issue forms (bug report, feature/design proposal), an issue-template config routing security reports away from public issues, and a pull request template.
- README contribution table now links the templates directly and lists security reporting; `docs/INDEX.md` gained a contributor section for these files.

## v4.3.29 (2026-08-09)

Functional closure release: decentralization (P1), contribution transparency & governance (P2), low-barrier access (P3), and public-welfare documentation (P4) land together, plus a security regression fix.

### Features
- **P1 — Real decentralization**: NAT hole punching (`nat_punch.go`, `nat_punch_loop.go`, `nat_traversal.go`) lets nodes connect directly without a relay wherever possible. The contribution ledger gains multi-replica storage with 60s automatic reconciliation (`ledger_redundancy.go`, `ledger_replication.go`, `contribution_ledger.go`).
- **P2 — Contribution transparency & shared governance**: ledger transparency and export endpoints (`ledger_transparency.go`, `ledger_export.go`), public-welfare quota accounting (`ledger_contrib_quota.go`), a proposal/approval dual-chain governance flow (`governance.go`), and a soft reminder for shared-network participation (`network.go`, `init.go`).
- **P2-3(ii) — Contributor quota on the consumption side (non-exclusive)**: reuses the existing federation ed25519 node identity rather than introducing a user system. A verified contributor with remaining balance spends their own earned quota and skips the anonymous per-IP gate; once the quota is exhausted — or when the caller is anonymous or cannot be verified — the request falls back to the community free pool with **exactly** the anonymous code path. Quota is an extra lane for contributors, never a toll gate in front of the free pool.
- **P3 — Low-barrier access**: downstream Gemini and Azure OpenAI compatibility (`gemini_api.go`, `azure_api.go`), decoupling of the community free pool from private shared providers (`provider.go`, `routes.go`), and one-line deployment via `docker-compose.yml` / `.dockerignore`.

### Security
- **SA-15 — Restored fail-closed tamper detection** (`data_integrity.go`): `loadWithIntegrity` no longer "recovers" a file whose HMAC check fails just because the bytes after the 32-byte header still parse as valid JSON — that path silently defeated tamper detection. HMAC-prefixed files that fail verification are now rejected outright. Legacy plain-JSON files without an HMAC prefix still load (whole-file JSON parse fallback).

### Bug Fixes
- Fixed an existing double-charge bug where a gateway request falling back to local handling deducted quota twice for the same IP.

### Documentation
- **README compressed from ~1300 to 169 lines**: detail split into `docs/` (API, FEATURES, CONFIGURATION, PLATFORMS, INDEX), and historical design docs consolidated under `docs/reference/`.
- Added a standard **MIT LICENSE** file and linked it from the README.
- Added a **Contributing** section (four contribution paths, build/test/vet PR gate, and the no-token/no-credit/no-revenue-share red line).
- Clarified that the **5-dimension routing vs. 4 UI sliders** gap is by design, not unfinished work — the fifth dimension is the algorithm itself and is not user-tunable.
- Corrected a false "zero third-party dependencies" claim to the accurate "single binary, no web framework / ORM / database, five direct dependencies".
- Added public-welfare positioning docs (`PUBLIC-WELFARE.md` / `.en.md`) and the promotion material kit (`LAUNCH-KIT.md`).

## v4.3.28 (2026-08-09)

### Features
- **Region-aware download optimization**: Both the installer (`scripts/install.sh`, `scripts/omp-manager.sh`) and the built-in self-updater (`update.go`) now detect the VPS network region from its public IP (via `ifconfig.me` / `api.ipify.org` + `ip-api.com` geolocation). **Mainland China → mirrors first, direct GitHub last**; **overseas → direct GitHub first, mirrors as fallback**. Eliminates the slow direct-GitHub-first penalty for CN users.

## v4.3.24 (2026-08-09)

### Features
- **Free pool works out of the box**: `Kilo Code` (api.kilo.ai, 12 models) and `OVHcloud AI Endpoints` (7 models) are now **seeded at first boot** as hardcoded default free providers — available immediately even before the remote free-provider sync completes (or with no network access to sync at all). Remote sync still augments the list.
- **Installer multi-mirror download fallback**: `scripts/install.sh` now tries GitHub direct first, then falls back through `ghfast.top` / `gh-proxy.com` / `ghproxy.net` / `mirror.ghproxy.com` mirrors, with per-source retry/backoff, file-size validation, and multi-source SHA256 verification. Regions with poor GitHub connectivity install reliably.

### Bug Fixes
- Installer: `systemctl` detection hardened for restart-after-self-update edge cases.
- Installer: download timeout now triggers the mirror fallback instead of failing outright.

## v4.3.21 (2026-08-08)

### Bug Fixes
- 自更新下载进度实时显示：新增 progressReader 包装下载流，每 500ms 上报进度百分比和已下载量，解决 12MB+ 二进制下载期间进度条冻结问题
- 下载状态显示已下载 MB 数，用户在慢速网络下也能感知进度

## [v4.3.20] - 2026-08-08

### Bug Fixes
- **update: 修复自更新后服务无法重启的问题** — `TriggerSelfUpdate` 不再依赖 `os.Exit(0)` + systemd Restart 策略来拉起新进程。改为：
  1. 自动检测是否运行在 systemd 下（通过 /proc/self/cgroup 解析 .service 单元名）
  2. 若检测到 systemd，显式调用 `systemctl restart <service>` 重启服务
  3. 若 systemctl 失败（非 systemd 环境、权限不足等），回退到 `os.Exit(1)`，触发 `Restart=on-failure` / `Restart=always` 策略
  修复前：`os.Exit(0)` 在 `Restart=on-failure` 策略下不会触发重启，导致服务挂死
  修复后：无论 systemd 策略配置如何，新进程都能被正确拉起

## [v4.3.19] - 2026-08-08

### Bug Fixes
- **ticket: 修复 TicketStore 内存泄漏** — 添加 `Cleanup()` 方法，每 2 小时清理超过 24h 的 fingerprint 和 ticket，防止长时间运行节点内存无限增长
- **ticket: 修复 handleNotarize 逻辑** — 不再将 double-spend ticket 标记为已公证，攻击者无法通过重复提交来"洗白"非法 ticket
- **ticket: 修复 AntiCollusionCheck 重复计数** — 使用 `flaggedSet` 去重，同一 provider 不会因 success deviation 和 amount deviation 被重复计入 anomalies

### Performance
- **nat_traversal: 复用共享 HTTP Client** — `ProbeDirect` 不再每次创建新 `http.Client{}`，改用 `GetSharedHTTPClient()` 复用连接池

### Security
- 延续 v4.3.18 安全加固（data dir 0700、X-Forwarded-Proto trust gate）
## [4.3.18] - 2026-08-07

### ✨ Feature: Ticket anti-double-spend system (§9.3-9.4)
- New `ticket.go`: `UsageTicket` with dual signatures (requestor + provider), `TicketFingerprint` for deterministic dedup
- `TicketStore`: issue, countersign, double-spend detection, notarization tracking
- `notarizeLoop()`: hourly batch notarization with seed nodes
- `AntiCollusionCheck()`: three-layer anti-collusion verification:
  - Layer 1: Upstream response fingerprint
  - Layer 2: Random sampling (5% re-probe)
  - Layer 3: Statistical anomaly detection (>50% deviation from average flags provider)
- `relay.go`: `recordContributionToLedger` now issues and countersigns a ticket for each contribution
- API endpoints: `POST /api/ticket/submit`, `POST /api/ticket/notarize`, `GET /api/ticket/anti-collusion`
- `contribution_ledger_init.go`: `initTicketStore()` + `notarizeLoop()` started after ledger init

## [4.3.17] - 2026-08-06

### ✨ Feature: Active probing and cross-verification (§10.2-10.3)
- `contribution_ledger_init.go`: Replace no-op `probeFn` with `realProbeFn` — sends 1-token chat completion request to remote node, 15s timeout
- `contribution_ledger_init.go`: `minVerifiers` raised from 2 to 3 for cross-verification quorum
- `contribution_ledger.go`: Add `ProbeSchedulerLoop()` — periodic probing of claimed capabilities at adaptive intervals:
  - New node (last seen <10min): 5min
  - Regular node: 30min
  - High-reputation (>80): 2h
  - Suspicious (<30): 1min
- `contribution_ledger.go`: Add `probeSchedule()` — determines interval based on reputation and last-seen
- `contribution_ledger.go`: Add `CrossVerifyWithQuorum()` — 3-node independent verification, >20% latency deviation triggers investigation
- Probe scheduler starts automatically after `initContributionLedger()`

## [4.3.16] - 2026-08-06

### ✨ Feature: NAT traversal architecture (§7.5)
- New `nat_traversal.go`: `NATManager` with STUN-based public address discovery and direct connectivity probing
  - `stunQuery()`: RFC 5389 STUN binding request, extracts XOR-MAPPED-ADDRESS
  - `ProbeDirect(nodeID, targetURL)`: 5s timeout direct HTTP probe
  - `ShouldUseDirect(nodeID)`: returns true if cached probe succeeded and <5min old
  - `stunLoop()`: periodic STUN discovery every 5min
- `network_relay.go`: `relayToRemote` now triggers async direct probe when cached result is stale
- `routes.go`: Register NAT routes (`GET /api/nat/status`, `POST /api/nat/probe`)
- `init.go`: `initNATManager()` called after contribution ledger init
- `stubs.go`: `registerWithBootstraps()` implemented — registers node with seed/bootstrap nodes, advertises public address and gateway status

## [4.3.15] - 2026-08-06

### ✨ Feature: AddrMan address manager (§7.8.4)
- `network.go`: `RouteEntry` extended with `FailCount`, `UptimeScore`, `IsGateway`, `IsSeed` fields
- `network.go`: `RouteTable` new methods:
  - `RecordSuccess(nodeID, latencyMs)`: reset FailCount, update UptimeScore (EMA 0.9/0.1), update LatencyMS (EMA 0.8/0.2)
  - `RecordFail(nodeID)`: increment FailCount, degrade UptimeScore (×0.8), mark unreachable at FailCount>=3
  - `GetGateways()`: return all non-unreachable gateway entries
  - `GetSeeds()`: return all seed entries
  - `PurgeStale()`: remove 7-day-unseen entries, increment FailCount for 30-min-unseen entries
  - `MarkGateway(nodeID, bool)`, `MarkSeed(nodeID, bool)`: set flags
- `network.go`: `routeTableHealthLoop()` runs every 30min, calls `PurgeStale()`
- `node_registry.go`: `persistedNode` extended with `FailCount`, `UptimeScore`; `SaveNode` and `LoadAll` updated
- Existing `NodeRegistry` persistence (`.nodes/` directory) now covers all AddrMan fields

## [4.3.14] - 2026-08-06

### ✨ Feature: Gossip five-message protocol (§7.8.3)
- `gossip.go`: Replace single `gossipLoop` with three independent loops:
  - `pingLoop`: PING every 30s to 3 random peers (liveness probe, peer responds PONG)
  - `getPeersLoop`: GET_PEERS every 5min to 2 random peers (peer responds PEERS with KnownPeers)
  - `announceLoop`: ANNOUNCE every 10min to all active peers (broadcasts enabled providers)
- `gossip.go`: Add `sendToRandomPeers()` helper and `doAnnounceRound()` for provider broadcasts
- `gossip.go`: `handleFederationGossip` now handles all 5 message types:
  - PING → respond PONG
  - GET_PEERS → respond PEERS with KnownPeers
  - PEERS → merge peer hints via `processPeerHints()`
  - SYNC/ANNOUNCE → existing logic preserved
- `gossip.go`: Add `processPeerHints()` method for PEERS message handling
- `types.go`: `GossipMessage.Type` comment updated with all 8 type values
- `gossip.go`: Add message type constants (`GossipPing`, `GossipPong`, `GossipGetPeers`, `GossipPeers`, `GossipAnnounce`, `GossipSync`, `GossipScore`, `GossipHeartbeat`)
- Legacy `doGossipRound` (sync) kept for backward compatibility

## [4.3.13] - 2026-08-06

### ✨ Feature: Gemini native adapter (§4.1-4.4)
- `platform_adapter.go`: Add `GeminiAdapter` implementing `PlatformAdapter` interface
  - `TranslateRequest`: IR → Gemini `contents/parts` nesting, `user`/`model` role mapping, `systemInstruction` for system prompt
  - `TranslateResponse`: `candidates[0].content.parts[0].text` → OpenAI `ChatResponse`, `usageMetadata` → `TokenUsage`
  - `TranslateStreamChunk`: Gemini SSE chunk → OpenAI `ChatChunk`
  - `ExtractUsage`: `usageMetadata` extraction
- `client.go`: Add `geminiNonStream` and `geminiStream` functions
  - Non-stream: `POST /v1beta/models/{model}:generateContent?key=`
  - Stream: `POST /v1beta/models/{model}:streamGenerateContent?alt=sse&key=`
  - Both convert Gemini native format to OpenAI-compatible response
- `client.go`: `doNonStream`/`doStream` switch adds `"gemini"` case
- `providers.go`: Gemini preset Type changed from `"openai_compatible"` to `"gemini"`, BaseURL to `https://generativelanguage.googleapis.com`
- `platform_adapter.go`: `adapterRegistry` registers `"gemini"` → `&GeminiAdapter{}`

## [4.3.11] - 2026-08-06

### ✨ Feature: Auto-register as Gateway after domain binding (§7.10)
- `tunnel.go`: `handleBindDomain` (Cloudflare API bind) now auto-sets `is_gateway=true` and persists on success
- `tunnel.go`: `handleManualDomainBind` (manual bind) now auto-sets `is_gateway=true` and persists on success
- Gateway status propagates to peers via existing Gossip heartbeat — no extra broadcast needed

## [4.3.10] - 2026-08-06

### ✨ Feature: Idle quota prompt UI (REQ-13)
- Replace session-only toast with persistent banner using `/api/network/idle-quota` endpoint
- Banner shows usage percentage and remaining tokens from backend `ShouldNotify` logic
- "暂不" button dismisses with localStorage per-month persistence (no re-prompt within same month)
- "了解/加入" button opens the 4-step onboarding wizard directly
- Reuses existing `slideIn` animation from toast system

### ✨ Feature: Provider Key-level fine-grained quota control (§8.3)
- `provider.go`: `SelectAPIKey` now checks `QuotaDaily`/`QuotaMonthly` in addition to total `Quota`
  - Stale daily/monthly counters (date mismatch) are treated as 0 used, not as exceeded
  - Key is skipped when any active quota limit is reached
- `admin-provider.html`: Fix `showAddApiKey` — `quotaDaily`/`quotaMonthly` were undefined (bug), now prompted
- `admin-provider.html`: Key list shows used/limit for daily, monthly, and total quotas (e.g. "日: 50/100")
- `admin-provider.html`: Add "编辑额度" button per key with `editKeyQuota()` function
- `admin-provider.html`: Remove duplicate total quota display in key list
- Tests: 5 new `TestHB5_SelectAPIKey_*` cases (daily exceeded, monthly exceeded, stale daily, stale monthly, daily-ok-monthly-exceeded)

## [4.3.9] - 2026-08-06

### 🐛 Fix: shared-network card initialization regressions introduced in 4.3.8
- `admin-network.js` `renderNetworkUI()`: derive `window._networkMode` from the
  authoritative server status. 4.3.8 replaced the manual `/api/network/status`
  fetch in `admin.html` with `loadNetworkStatus()`, which never assigned
  `_networkMode` — it stayed `undefined` on page load, so the `=== 'shared'`
  guard never passed and `loadShareInfo()` / `loadGuestKeys()` never ran
- `admin.html`: restore the `loadFederationConfig()` call in the page-init block.
  It is the only code that reads `/api/federation/config` and sets the relay
  toggle state, so 「开启中继」 always rendered OFF on load even when relay was
  enabled server-side
- Bump `admin-network.js` cache-buster to `v=347`
- `main.go`: bump `AppVersion` to 4.3.9 so source-built deploys (which build
  without the release ldflags) report the real version instead of a stale 4.3.7

## [4.3.8] - 2026-08-06

### 🐛 Fix: all three shared-network toggles were completely dead (P0)
- `admin-network.js` ended with a stray `}}` (introduced in 8945306), turning the
  entire file into a SyntaxError. Browsers discarded the whole script, so
  `toggleNetworkEnabled` / `toggleShareToPool` / `saveRelayToggle` were never
  defined and 「加入共享网络」/「共享剩余额度」/「开启中继」 all silently did nothing
- `admin.html`: page init now calls `loadNetworkStatus()` so `renderNetworkUI()`
  initializes the toggle states on load

## [4.3.7] - 2026-08-05

### 🐛 Fix: Kilo Gateway / Ollama preset forces API Key in admin UI
- Add `KeyOptional` field to Provider struct (types.go)
- Mark `kilo-gateway` and `ollama` presets as `KeyOptional: true`
- `handleGetPresets` now returns `key_optional` in API response
- admin-provider.html: `selectPreset` sets `selectedPresetKeyOptional` based on
  `key_optional`, skips Key validation and hides registration link
- Key placeholder shows "API Key（可选，此平台无需 Key）" for optional providers

## [4.3.6] - 2026-08-05

### ✨ Feature: Idle quota prompt UI (REQ-13)
- Replace session-only toast with persistent banner using `/api/network/idle-quota` endpoint
- Banner shows usage percentage and remaining tokens from backend `ShouldNotify` logic
- "暂不" button dismisses with localStorage per-month persistence (no re-prompt within same month)
- "了解/加入" button opens the 4-step onboarding wizard directly
- Reuses existing `slideIn` animation from toast system

### 🔒 Fix: gateway hop count variable shadowing bug (P0)
- `network_relay.go`: `handleGatewayRequest` used `:=` instead of `=`, creating a local
  `hopCount` that shadowed the outer variable — relay loop prevention was completely
  broken for gateway requests

### 🧹 Cleanup
- Remove accidentally committed `openmodelpool` binary (18MB) from repo
- Remove `.monkeycode/` planning directory from repo
- Update `.gitignore` to include `openmodelpool` and `.monkeycode/`

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

- **P1-1**: Access control header spoofing **partially** fixed (record corrected in v4.3.32 — the original entry overstated the fix):
  - `RequestKeyType()` no longer trusts the old client-supplied `X-MK-KeyType` header — the internal header was **renamed** to `X-OMP-KeyType`. **However, the strip was never actually implemented**: as of v4.3.32, `RequestKeyType()` still trusts any inbound `X-OMP-KeyType` at Priority 1, and neither `relayToRemote()` nor `handleRelayToLocal()` deletes it before forwarding. **Resolved in v4.4.44** (strip at the earliest middleware + both relay Directors; key type now derived from the verified token).
  - `FilterByAccessControl()` default branch changed from fail-open (allow all) to fail-closed (deny all) for unknown key types. — this part is accurate.

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

---

## Historical Release Notes (v4.1.6 and earlier)

> Moved here from the README so the homepage stays short. Feature-level release notes; the entries above are the maintained engineering changelog.

### v4.1.6 (2026-07)

**🌐 Federation / Private Mesh**
- **Private-node mesh** — `public_domain` + `federation_endpoint` now resolve the correct public address for private mesh interconnection; `resolvePublicEndpoint` no longer falls back to LAN hostnames in production
- **Manual peer UI** — Add-node form for pasting a peer's public URL, plus invite-code based interconnection
- **Seed auth** — `/federation/pool` read-only path allowed for trusted seed `Host`s only (fixes the prior 403)
- **QA regression** — endpoint priority, empty `node_id` peer handling, `GetInfo` public endpoint

### v4.1.5 (2026-07)

**🖥️ Built-in Browser Login Fix**
- **Cross-platform Chrome discovery** — `findBrowserExecutable` (env override `OMP_CHROME_PATH`/`CHROME_PATH`/`CHROMIUM_PATH` + OS dirs + PATH); replaces the Windows-only lookup
- **Launch flags** — `--headless=new`, `--disable-gpu`, `--enable-unsafe-swiftshader` (fixes Chromium 139+ "chrome failed to start" on Windows); Linux adds `--no-sandbox` + `--disable-dev-shm-usage`
- **Per-session profile** — isolated temp `userDataDir`, cleaned up after use
- **Panic-safe JSON** — all browser handlers return valid JSON on panic
- **Docs** — built-in browser prerequisite FAQ (requires Chrome/Chromium)

### v4.1.4 (2026-07)

**🧩 Web Session Providers on Main UI**
- **`web_session` on main UI** — included in `/api/health` even without an API key

### v4.1.3 (2026-07)

**🛠️ Web Session Two-Step Save**
- **Cookie field fix** — removed the readonly cookie field that blocked saving; `extra_cookies` now restored on edit load

### v4.1.2 (2026-07)

**🌐 LAN IP & Browser Login Fixes**
- **LAN IP detection** — `getLocalIP` filters APIPA/link-local (169.254/16), prefers RFC1918 private IPs
- **Linux browser login** — added `chromedp.Headless` for headless servers
- **Docs** — corrected the stale "v4.0.3 fixed 169.254" claim

### v4.1.1 (2026-07)

**🔗 Network Join Conditions & Hardening**
- **Join requires shared key only** — remaining quota downgraded to an idle-capacity reminder
- **Codespace self-heal** — `run-omp.sh` repo-writable binary path; cron in postStart + `:8000` watchdog
- **Concurrency** — eliminated remaining data race; resolved CI smoke-test failures
- **Free pool** — key-management UI + real model sync + LLM7.io auth fix

### v4.1.0 (2026-07)

**🎁 Free Model Pool**
- **Auto-sync free LLM providers** — 16+ free providers from [awesome-free-llm-apis](https://github.com/mnfst/awesome-free-llm-apis), low-priority public pool
- **Anonymous provider support** — OVHcloud works zero-config
- **Real model list sync** — queries actual `/v1/models` after sync
- **In-page API key management** — enable key-based providers from the Free Pool page
- **24-hour auto-sync** + smart provider filtering + dark-theme admin page

### v4.0.7 (2026-07)

**🪟 Windows Browser Login & Domain Bind**
- **Windows built-in browser login** — `chromedp.ExecPath` for Chrome path
- **Domain manual-bind route** — `POST /api/domain/manual-bind` (fixes 405)
- **Codespace** — `postStartCommand` auto-starts cron + OMP
- **Windows script** — fixed `$var` parsing in double-quoted strings

### v4.0.6 (2026-07)

**🆔 Identity Module & Performance**
- **Identity module (slice2)** — BIP39 mnemonic → Node ID (`mmx-` prefix)
- **Performance** — preset caching + lite modes (68KB → 461B / 2.4KB)
- **Cloudflare tunnel** — auto-detect GUI + install verification
- **Docs** — consolidated 9 design docs into `openmodelpool-v4-design.md`

### v4.0.5 (2026-07)

**🔵 Script Consolidation**
- **All-in-one manager scripts** — `omp-manager.sh` + `omp-manager.ps1`, `--auto-update` mode
- **Dynamic Release asset detection** · **CPU arch auto-detection** · **SHA256 verification**

### v4.0.4 (2026-07)

**🟠 API & Performance**
- **Anthropic Messages API compatibility** — `/v1/messages`
- **ChatMessage array content fix** · **SOCKS5 connection pool** (5-7s → 0.3s) · **FindCandidates fix**

### v4.0.3 (2026-07)

**🟢 Multi-Key & Quota System**
- **Multi-key health check** · **Quota aggregation** (Guest/Public pool) · **Multi-period quota** · **Platform cap**

### v4.0.2 (2026-07)

**🔵 Tunnel & Deployment**
- **ngrok tunnel support** · **Cloudflare domain reuse** · **FRP tunnel reuse**

### v4.0.1 (2026-07)

**🔴 Architecture Upgrade**
- **Dual-Mode Architecture** — Personal + Network
- **BIP39 Mnemonic Identity** — → Ed25519 → Node ID
- **5-Dimension Routing** — Trust/Reputation/Latency/Availability/Contribution
- **Two-Level Switch** — `network_enabled` + `share_to_pool`

**🟠 Network System (New)**
- **P2P Node Discovery** (Peer Seed + DHT + Gossip) · **Reputation** (S/A/B/C/D) · **non-currency contribution ledger** · **Guest/Public Key** · **WAF 4-Layer** · **Token Estimation** · **Auto Discovery** · **Load Balancer** · **Data Integrity** · **Global Pool** · **Algorithm Governance**

**🟢 Platform Updates** — 34 → **36** platforms (Agnes AI, AIHubMix)

### v3.4.1 (2026-07)

**🔴 Admin UI Modularization**
- **admin.html** 5063 → 2457 lines · JS modular split · sub-page architecture · 30 unused functions removed · 4-platform cross-compilation

### v3.3.0 (2026-07)

**🔴 Web Session Template System**
- **`web_session` provider type** · **`WebSessionConfig`** (7 generic functions) · **Sider.ai migration**

**🔴 Security Fixes**
- API Key masking · Consumer Key AES-256-GCM · CORS tightening · file perms 0644→0600 · HttpOnly Cookie JWT · `/metrics` + `/events` auth required

### v3.2.0 (2026-07)

**🔴 Security & Performance**
- **Rate Limiting** (token bucket) · **CORS whitelist** · **Sensitive field encryption unified** · **JSON parse error handling**

**🟡 Feature Enhancements**
- **Provider model list auto-sync** · **Federation Gossip** · **Structured logging** · **SSE `/events`** · **Prometheus `/metrics`** · **Frontend modularization** · **Config hot reload (`SIGHUP`)**
