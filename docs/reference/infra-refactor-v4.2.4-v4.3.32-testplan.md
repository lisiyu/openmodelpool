# OpenModelPool 基础设施 / CI / 重构聚合批次 测试计划

> 版本范围：v4.2.4 – v4.3.32（基础设施/CI/重构聚合批次）
> 聚合理由：上述版本均不涉及新用户功能，而是围绕三条工程主线收口——（1）安全地基（WAF 模式、传输加密、自更新验签，其中传输加密 X25519 与自更新 Ed25519 验签已在安全批次 A 详述，本批次聚焦 WAF 模式覆盖）；（2）主包与 admin 包的拆分重构（admin.go → 14 文件、setupRoutes/routes.go、全局状态归集）；（3）CI 测试门禁诚实化与一批既有测试失败的修复。合并成一份计划可减少跨版本的重复核对成本，并以「重构不应改变行为」为统一验收基线。

---

## 1. 范围与背景

| 版本 | 能力 | 关键变更 | 优先级 |
|------|------|----------|--------|
| v4.2.4 | WAF 内容扫描 | 内置 40+ 攻击模式（SQLi / XSS / 路径穿越 / 命令注入 / SSRF），叠加用户配置 `waf_content_keywords` | P2 |
| v4.2.4 | 传输加密 X25519 | ECDH + HKDF-SHA256 密钥协商（**已在安全批次 A 详述，本批次不重复覆盖**） | P2 |
| v4.2.4 | 自更新验签 | 下载 `.sig` 并用内嵌 Ed25519 公钥验证（**已在安全批次 A 详述**） | P2 |
| v4.2.4 | CI 拆分（初版） | 单元测试硬门禁 `go test -race -short` + 集成软门禁（**该 `-short` tier 在 v4.3.32 被移除**，见下） | P2 |
| v4.2.5 | admin.go 拆分 | `admin.go`（2559 行）→ 14 个领域文件（providers/health/usage/smtp/status/config_io/apikeys/restart/pages/models/url_sync/collab/auth） | P3 |
| v4.2.5 | audit 远程 webhook | `audit.go` 增加 `audit_webhook_url`，审计记录异步（非阻塞）转发，10s 超时 | P3 |
| v4.2.6 | 主包重构 | `setupRoutes()` 抽出到 `routes.go`；全局状态归集 `globals_core.go`（16 个基础设施单例）/ `globals_network.go`（25 个网络单例）；41 文件更新 | P3 |
| v4.2.10 | 修复既有测试失败 | `/health`、`/diagnostics` 的 `metrics` nil 守卫；移除 `isOriginAllowed` 通配；修复 `TestHB6_ProviderManager_EnabledRaw_Empty` | P2 |
| v4.2.11 | 修复既有测试失败（17 个） | SiderMonitor 数据竞态、reportToOrigin 竞态、relay SSRF 测试 bypass、IPv6 `extractClientIP`、GetByModel 顺序、G6 头重命名、Update QA 版本、preset provider 干扰等 | P2 |
| v4.2.12 | 修复既有测试失败（4 个） | preset provider 清空策略改为禁用 `Enabled`（保留 `GetRaw`/`GetPresets`）；`TestQARouteSignalValidSignature` 竞态（用 `reportToOriginWG.Wait()`） | P2 |
| v4.3.32 | CI 门禁诚实化 | 移除不存在的 `-short` tier，硬门禁改为 `go test -race -count=1 -timeout 25m ./...`；第二 job 改为诚实 flaky watch（重跑同套件、软门禁） | P2 |

> **跨批次说明**：v4.2.4 引入的 `-short` tier 在 v4.3.32 被判定为「从未真实存在」而移除——仓库无 `testing.Short()` 分支、无 build tag、无读取 `OMP_TEST_INTEGRATION` 的代码。因此本批次对 v4.2.4 的 CI 项仅做历史记录，实际门禁以 v4.3.32 为准。

---

## 2. 自动化单元测试（离线 / httptest）

运行基线：`go test ./...`（CI 外加 `-race -count=1 -timeout 25m`，见第 4 节）。以下仅列本批次相关用例，**均确认真实存在，无编造**。

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `waf_wire_test.go` | `TestWAFBlocksBlacklistedIP` | 启用 + IP 黑名单 → 引擎与 `wafMiddleware` 均 403；干净 IP 放行 |
| `waf_wire_test.go` | `TestWAFBlocksUserAgent` | UA 黑名单命中 → `ua_filter` 违规；干净 UA 放行 |
| `waf_wire_test.go` | `TestWAFRateLimitBlocksExcess` | 单 IP 超过 rps/burst → `rate_limit` 违规 |
| `waf_wire_test.go` | `TestWAFBlocksBlockedPath` | 路径前缀命中 `waf_blocked_paths` → `path_block` 违规 |
| `waf_wire_test.go` | `TestWAFDisabledAllowsAll` / `TestWAFEnabledNoRulesAllows` | 禁用有规则、或启用无规则均不误杀（默认关 false-positive 基线） |
| `waf_wire_test.go` | `TestWAFStatusReflectsEnabled` | `/api/waf/status` 反映实时 `enabled` 及规则计数 |
| `waf_wire_test.go` | `TestWAFViolationsRecorded` | 违规被记录并经 `/api/waf/violations` 暴露 |
| `waf_wire_test.go` | `TestWAFUnbanRemovesBan` | `/api/waf/unban/{key}` 移除动态封禁 |
| `waf_qa_test.go` | `TestWAFDefaultOffPassthroughQA` | 默认关闭时 `/v1/*` 与 `/network/{id}` 全路径透传（核心 no-false-positive 守护） |
| `waf_qa_test.go` | `TestWAFDefaultOffFullMuxQA` | 默认关闭下真实 `setupRoutes()` 全链路不对 `/v1/models` 等返回 403 |
| `waf_qa_test.go` | `TestWAFBansEndpointQA` | `AddBan` 后 `/api/waf/bans` 反映真实封禁 |
| `waf_qa_test.go` | `TestWAFConcurrentStress` | 双锁（mu + violMu）并发无死锁，计数精确（黑/白名单 + 违规环缓冲 + 限流惰性创建） |
| `handler_batch10_test.go` | `TestHB10_IsOriginAllowed_Wildcard` | **v4.2.10/4.2.11 修复回归**：`*.example.com` 通配**不再**匹配，仅精确 origin 匹配 |
| `middleware_test.go` | `TestIsOriginAllowed` / `TestExtractClientIP` | 精确 origin 匹配；IPv6 地址 `net.SplitHostPort` 去括号 |
| `security_medium_test.go` | `TestSA10_ExtractClientIP` | **v4.2.11 IPv6 修复回归**：剥离 IPv6 方括号后的解析 |
| `handler_batch6_test.go` | `TestHB6_ProviderManager_EnabledRaw_Empty` | **v4.2.10 修复回归**：仅返回 preset provider，不含用户新增 |
| `handler_batch7_test.go` | `TestHB7_HandleHealthStatus_NilChecker` | **v4.2.11 修复回归**：`metrics` 为 nil 时 `/health` 不 SIGSEGV |
| `sider_test.go` | `TestSiderMonitor_IsExpired` / `TestSiderMonitor_Concurrent` | **v4.2.11 竞态修复回归**：`IsExpired()` 取 RLock；并发读写无 data race |
| `update_qa_test.go` | `TestQARouteSignalValidSignature` | **v4.2.11/4.2.12 修复回归**：用 `reportToOriginWG.Wait()` 同步后台 `reportToOrigin`，消竞态 |
| `update_qa_test.go` | `TestQARouteVersionLatest` | **v4.2.11 修复回归**：mock GitHub 返回比 `AppVersion` 更新的版本 |
| `relay_security_test.go` | `TestRelayToRemote_StripsKeyTypeHeader` 等（含 `allowLocalRelayForTest=true`） | **v4.2.11 修复回归**：relay SSRF 测试 bypass——测试环境允许 127.0.0.1 作为 relay 目标 |
| `relay_auth_test.go` | `TestWithProxyAuth_RelayDispatched_NoAnonymousAdmin` | relay 派发不触发本地匿名 admin fallback |
| `utils_test.go` / `handler_batch5_test.go` | `TestRouteTable_GetByModel_SpecificModel` 等 | **v4.2.11 修复回归**：map 遍历无序，改验成员而非顺序 |
| `quota_priority_handler_test.go` | `TestG6_HandlerEnabled_AdminKeyPrivate` 等 | **v4.2.11 修复回归**：`X-MK-KeyType` → `X-OMP-KeyType` 头重命名一致 |
| `admin_stats_test.go` | `TestAdminStats_SuccessRateFromTracker` 等 4 例 | **admin.go 拆分行为回归**：统计计算在拆分后保持一致 |
| `handler_batch7/8/10_test.go` | `TestHB7_Auth_*`、`TestHB8_*`、`TestHB10_HandleAdminInfo` 等 | **admin.go 拆分行为回归**：登录/消费者/邀请码等 handler 行为不变 |
| `test_helpers_test.go` | `setupTestEnv`（禁用 preset `Enabled`） | **v4.2.11/4.2.12 修复回归**：preset provider 干扰清零，且保留 `GetRaw`/`GetPresets` |

**精确运行命令（注意正则需匹配实际测试名）**：

```bash
# WAF 四层 + QA（13 个用例，覆盖 waf_wire_test.go 与 waf_qa_test.go 全部 TestWAF*）
go test ./... -run 'TestWAF' -v

# origin 通配移除 + IPv6 extractClientIP（v4.2.10/4.2.11）
go test ./... -run 'TestHB10_IsOriginAllowed|TestIsOriginAllowed|TestExtractClientIP|TestSA10_ExtractClientIP' -v

# /health 与 /diagnostics 的 metrics nil 守卫（v4.2.10/4.2.11）
go test ./... -run 'TestHB7_HandleHealthStatus_NilChecker|TestHandler_Health|TestHB10_HandleHealthStatus' -v

# SiderMonitor / reportToOrigin 竞态修复（v4.2.11，需 -race 才有效）
go test ./... -race -run 'TestSiderMonitor|TestQARouteSignalValidSignature' -v

# preset provider 干扰 / 清空（v4.2.10/4.2.11/4.2.12）
go test ./... -run 'TestHB6_ProviderManager_EnabledRaw_Empty|TestHB10_HandleGetPresets|TestHB8_HandleGetPresets' -v

# ⚠️ 不要用 'TestWAFContent' / 'TestAuditWebhook' 等子串：对应用例尚不存在（见下方测试缺口），
#    会漏跑且给出空匹配（非失败，但无覆盖）。
```

> 注：本仓库在 Windows 沙箱下 `go test ./...` 有 3 个与文件权限/日志锁相关的预存失败，属环境限制，与本次改动无关；以 Linux CI（`-race`）全绿为准。

### 测试缺口（建议新增，不编造已有用例）

下列为第 1 节能力**确无对应真实测试**之处，建议补 `TestXxx_*`：

1. **WAF 攻击模式分类断言（最大缺口）**——`waf.go` 的 `wafAttackPatterns` 含 40+ 条内置模式（SQLi 16、XSS 14、路径穿越 4、命令注入 9、SSRF 4），但现有 WAF 测试仅覆盖四层（IP/UA/rate/path/ban），**未对 `CheckContent` 的任一攻击模式做断言**。且 `CheckContent` 未被 `wafMiddleware` 自动调用（设计上 opt-in）。建议：
   - `TestWAFCheckContent_AttackPatterns`：表驱动遍历 `wafAttackPatterns`，断言每类 payload（`union select`、`<script`、`../etc/passwd`、`; ls`、`169.254.169.254` 等）均被 `CheckContent` 判否并回报 `attack_pattern:<name>`。
   - `TestWAFCheckContent_UserKeywords`：用户 `waf_content_keywords` 叠加生效。
   - `TestWAFCheckContent_NotCalledByMiddleware`：回归守护 `wafMiddleware.Check` 不触发内容扫描（避免对 AI 对话体的误杀）。
2. **audit webhook 异步与超时**——`audit.go` 的 `forwardAuditWebhook`（异步 `go`、10s `http.Client` 超时）无任何测试。建议：
   - `TestAuditWebhook_AsyncNonBlocking`：本地 httptest server 挂起时，`LogAction` 不阻塞返回。
   - `TestAuditWebhook_Timeout10s`：server 休眠 >10s 时客户端按时中断并记 error 而非永久挂起。
   - `TestAuditWebhook_NotConfiguredNoOp`：`audit_webhook_url` 为空时不启动 goroutine。
3. **重构后路由注册完整性**——`routes.go` 的 `setupRoutes()` 仅被 `TestWAFDefaultOffFullMuxQA` 顺带调用（且只断言 2 条路径不 403），**无专门用例断言全部 ~120 条路由已注册**。建议：
   - `TestSetupRoutes_RegistersAllRoutes`：遍历 `setupRoutes()` 返回的 mux（或对照 `http.ServeMux` 探测关键路径如 `/v1/chat/completions`、`/api/waf/*`、`/api/network/*`、`/api/admin/*`），断言任一核心路径不 404。
4. **`handleDiagnostics` nil 守卫**——`/health` 已有 `TestHB7_HandleHealthStatus_NilChecker`，但 **`handleDiagnostics` 缺少专用 nil-guard 测试**（v4.2.10/4.2.11 同样修复了 `metrics` 守卫）。建议补 `TestHandleDiagnostics_NilMetricsGuard`。
5. **全局状态归集冒烟**（低优先级）——`globals_core.go` / `globals_network.go` 为纯 `var` 归集（16+25 个单例），无行为变化，但建议 `TestGlobalsInit_CoreAndNetworkPopulated` 在 `initCore()` 后断言关键单例非 nil，防止某文件漏初始化。

---

## 3. 集成测试 / QA 手册 + 失败判据

### 3.1 CI 门禁诚实化人工核对点（v4.3.32）

| 步骤 | 操作 | 期望 |
|---|---|---|
| 3.1.1 | 打开 `.github/workflows/ci.yml` | `test-unit` job 命令为 `go test -race -count=1 -timeout 25m -coverprofile=coverage.out -covermode=atomic ./...` |
| 3.1.2 | 同文件搜索 `-short` | **不应出现**任何 `-short` / `testing.Short` 引用（tier 已移除） |
| 3.1.3 | `test-integration` job | `continue-on-error: true`（软门禁）；命令与 3.1.1 同套件（`go test -race -count=1 -timeout 30m ./...`） |
| 3.1.4 | 本地执行 3.1.1 命令 | 全绿；若存在 race-only 失败，说明 CI 比本地更严格（符合 v4.3.32 设计） |
| 3.1.5 | `go build ./...` 与 `go vet ./...`（`build-gate` job） | 0 error / 0 新增 warning |

**失败判据**：3.1.1 命令缺失 `-race` 或 `-count=1`、或重新出现 `-short` → 门禁回退，违反 v4.3.32 诚实化原则。

### 3.2 WAF 模式手动验证（注入各类 payload）

> 前置：WAF 默认开启（`waf_enabled=true`）。内容扫描 `CheckContent` 为 opt-in，需在被测非对话端点（如配置导入、provider URL 同步等可安全扫描的入口）显式调用；四层（IP/UA/rate/path）经 `wafMiddleware` 自动生效，可直接打代理流量。

| 步骤 | 操作 | 期望 |
|---|---|---|
| 3.2.1 | 配置 `waf_ip_blacklist=203.0.113.9`，向 `/v1/models` 发请求 | 该 IP 返回 403 `waf_blocked`（`ip_blacklist`） |
| 3.2.2 | 配置 `waf_ua_blacklist=BadBot`，UA 带 `BadBot` | 403 `ua_filter` |
| 3.2.3 | 配置 `waf_blocked_paths=/debug`，请求 `/debug/pprof/` | 403 `path_block` |
| 3.2.4 | 向可扫描端点注入 SQLi：`' UNION SELECT password FROM users--` | `CheckContent` 判否，`attack_pattern:sqli_union_select` |
| 3.2.5 | 注入 XSS：`<script>document.cookie</script>` | 判否，`xss_script_tag` |
| 3.2.6 | 注入路径穿越：`../../../../etc/passwd` | 判否，`path_traversal_etc_passwd` |
| 3.2.7 | 注入命令注入：`; ls -la /` | 判否，`cmd_inject_semicolon` |
| 3.2.8 | 注入 SSRF：`http://169.254.169.254/latest/meta-data/` | 判否，`ssrf_169_254` |
| 3.2.9 | 配置 `audit_webhook_url` 指向可观察的接收端，触发任意审计动作 | 接收端在 ~10s 内收到 JSON，且本地 `LogAction` 不阻塞 |

**失败判据**：
- 3.2.1–3.2.3 任一层未拦截 → WAF 四层回归失败。
- 3.2.4–3.2.8 任一类别 payload 未被 `CheckContent` 判否 → WAF 攻击模式覆盖缺口（对应第 2 节缺口 1，应补 `TestWAFCheckContent_*`）。
- 3.2.9 webhook 未收到或本地因远端挂起而卡住 → audit webhook 异步/超时回归（对应缺口 2）。

### 3.3 重构行为回归核对（v4.2.5 / v4.2.6）

- admin 14 文件拆分后，逐项手测管理面板：登录、provider CRUD、健康检查、用量统计、配置导入导出、API key、重启、模型同步、URL 同步、协作——行为与拆分前一致。
- `routes.go` 抽离后，前端所有已在用的 API 路径仍可访问（结合 3.2 代理流量与 `/api/*` 探测，无 404 新增）。
- 全局状态归集后，进程启动完整（`initCore` → `initWAF` → 各 manager），`/api/health` 与 `/api/waf/status` 反映真实状态。

**失败判据**：任一 admin 子功能在拆分后行为改变，或 `setupRoutes` 遗漏既有路径 → 违反「重构不应改变行为」基线。

---

## 4. 质量门禁（CI）

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -l .`（仅 Go 文件） | 无未格式化文件 |
| 构建 | `go build ./...` | 0 error |
| 静态 | `go vet ./...` | 0 新增 warning |
| 硬门禁（单测） | `go test -race -count=1 -timeout 25m -coverprofile=coverage.out -covermode=atomic ./...` | 全部 PASS（Linux） |
| 软门禁（flaky watch） | `go test -race -count=1 -timeout 30m ./...`（独立重跑，`continue-on-error`） | 绿为通过信号；失败仅告警，不阻断 PR |
| 交叉编译 | `go build` 于 linux/darwin/windows × amd64/arm64/arm | 0 error |
| 安全扫描 | `gosec` / `govulncheck ./...` | 无新增高危（软门禁） |

> 硬门禁命令与贡献者本地运行一致且额外加 `-race`；`-count=1` 关闭结果缓存，避免缓存 PASS 掩盖 flaky（v4.3.32 设计）。

---

## 5. 一致性复核（IS_PASS）

最终人工/自动复核清单：

- [ ] `wafAttackPatterns` 的 5 类攻击模式经 `CheckContent` 全部命中（若未补 `TestWAFCheckContent_*` 则至少人工完成 3.2.4–3.2.8）
- [ ] WAF 四层（IP/UA/rate/path/ban）单测全绿（`go test -run TestWAF`）
- [ ] `isOriginAllowed` 通配已移除且有回归守护（`TestHB10_IsOriginAllowed_Wildcard` / `TestIsOriginAllowed`）
- [ ] `/health`、`/diagnostics` 的 `metrics` nil 守卫就位（建议补 `TestHandleDiagnostics_NilMetricsGuard`）
- [ ] SiderMonitor、reportToOrigin 竞态在 `-race` 下不复现（`TestSiderMonitor_*` / `TestQARouteSignalValidSignature`）
- [ ] preset provider 干扰/清空修复后 `EnabledRaw` 仅含 preset、`GetRaw`/`GetPresets` 仍可用
- [ ] IPv6 `extractClientIP` 解析正确（`TestSA10_ExtractClientIP` / `TestExtractClientIP`）
- [ ] admin.go 拆分后 14 文件行为不变（admin handler 既有单测全绿）
- [ ] `setupRoutes()` 注册完整（建议补 `TestSetupRoutes_RegistersAllRoutes`）
- [ ] audit webhook 异步非阻塞、10s 超时（建议补 `TestAuditWebhook_*`）
- [ ] `ci.yml` 硬门禁为 `go test -race -count=1 -timeout 25m ./...` 且无 `-short`；软门禁为同套件重跑
- [ ] 无 `TODO` / 占位 / `pass` / 未实现标记残留于本批次涉及文件
