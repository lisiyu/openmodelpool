# OpenModelPool 适配器 / Provider / 免费池 测试计划（v4.2.9 – v4.3.29）

> 版本范围：v4.2.9 – v4.3.29（适配器/Provider/免费池聚合批次）
> 聚合理由：这 6 个版本虽跨越 4 个小版本段，但改动集中落在同一组文件上——
> `providers.go`（预设定义）、`types.go` + `provider.go`（Provider 结构与选 Key 逻辑）、
> `platform_adapter.go` + `client.go`（上游平台适配）、`gemini_api.go` + `azure_api.go`（下游协议兼容）、
> `tunnel.go`（域名绑定副作用）。它们共同回答一个问题：**一个 provider 从预设定义到被选中、
> 到请求被翻译成上游协议、再到对外暴露给公共 Key，这条链路是否完整**。
> 拆成 6 份计划会让同一条链路的验证点散落，故合并为一份批次计划。
> 关联文档：`docs/CHANGELOG.md`（v4.2.9 / v4.3.7 / v4.3.10 / v4.3.11 / v4.3.13 / v4.3.29）

---

## 1. 范围与背景

### 1.1 版本 → 能力 → 关键变更 → 优先级

| 版本 | 能力 | 关键变更 | 优先级 |
|------|------|----------|--------|
| **v4.2.9** | Kilo Gateway 免费模型预设 | `providers.go:628` 新增 `kilo-gateway` 预设：`BaseURL=https://api.kilo.ai/api/gateway`、`APIKey="free-anonymous"`、`Enabled:true`、`Priority:3`、**12 个免费模型**（已核对为 12 条 `ModelDef`）。开箱即用，无需用户配置 Key | P0 |
| **v4.3.7** | Provider `KeyOptional` 字段 | `types.go:231` 新增 `KeyOptional bool`；`kilo-gateway`（`providers.go:633`）与 `ollama`（`providers.go:389`）标记 `KeyOptional:true`；`admin_providers.go:61` `handleGetPresets` 响应回传 `key_optional`；前端据此跳过 Key 校验并隐藏注册链接 | P1 |
| **v4.3.10** | Provider Key 级精细配额 | `provider.go:1102` `SelectAPIKey` 在总配额之外增查 `QuotaDaily`（:1136-1143）与 `QuotaMonthly`（:1146-1153）；**日/月计数器日期不匹配时视为已用 0**（而非判超限）；任一活跃配额触顶即跳过该 Key | P0 |
| **v4.3.11** | 域名绑定后自动注册为 Gateway | `tunnel.go:599` `handleBindDomain`（Cloudflare API 路径）与 `tunnel.go:915` `handleManualDomainBind`（手动路径）成功后各自 `cfg.Set("is_gateway","true")`（:679 / :947）并持久化；网关状态经既有 Gossip 心跳传播，无需额外广播 | P1 |
| **v4.3.13** | Gemini 原生适配器（**上游**方向） | `platform_adapter.go` 新增 `GeminiAdapter`（`TranslateRequest` / `TranslateResponse` / `TranslateStreamChunk` / `ExtractUsage`），`adapterRegistry`（:75）注册 `"gemini"`；`client.go` 新增 `geminiNonStream` / `geminiStream`，`doNonStream`（:157）/ `doStream`（:174）switch 增 `"gemini"` case；`providers.go:237` Gemini 预设 `Type` 由 `openai_compatible` 改为 `"gemini"` | P0 |
| **v4.3.29** | Gemini / Azure 下游兼容 + 免费池解耦 + 一键部署 | `gemini_api.go` / `azure_api.go` 提供**下游**入口（客户端以 Gemini / Azure 协议调 OMP）；`provider.go:877` `isFreePoolProvider` + `:882` `filterFreePoolOnly` + `:840-850` `providerAllowsKeyType` 让社区免费池与私有共享 provider 解耦（公共 Key 在任何模式下可达免费池，私有共享仍需 shared 模式）；`docker-compose.yml` + `.dockerignore` 一键部署 | P0 |

### 1.2 两个「Gemini」不是同一件事（易混淆点）

本批次出现两处 Gemini 改动，**方向相反、测试状况差异很大**，计划中始终分列：

| | v4.3.13 上游适配器 | v4.3.29 下游兼容入口 |
|---|---|---|
| 方向 | OMP → Gemini（把 Gemini 当 provider 调） | 客户端 → OMP（客户端用 Gemini 协议调 OMP） |
| 文件 | `platform_adapter.go`、`client.go`、`providers.go` | `gemini_api.go` |
| 现有单测 | **无**（见 §2.5 缺口 G-2） | 有，6 个用例（见 §2.4） |

---

## 2. 自动化单元测试

### 2.1 运行命令

```bash
# 本批次全部已存在的相关用例（一次跑完）
go test -run 'TestHB5_SelectAPIKey_|TestHB5_keyAllowedForAccess|TestHB8_Provider_SelectAPIKey_|TestHB8_ProviderManager_ResetKeyQuota_|TestHB2_ResetKeyQuota_NotFound|TestHB10_HandleResetKeyQuota_NotFound|TestIsFreePoolProvider|TestProviderAllowsKeyType_PublicFreePool|TestHB8_FilterByAccessControl_PublicFreePoolPersonalMode|TestSplitGeminiModelSuffix|TestExtractGeminiText|TestConvertGeminiFinish|TestGeminiAuthAdapter|TestGeminiResponseWriter_|TestInjectModelIntoBody|TestAzureAuthAdapter|TestHB6_ProviderManager_EnabledRaw_Empty|TestProviderManager_Enabled|HandleGetPresets|TestHandler_GetPresets' -v ./...

# 分特性运行
go test -run 'TestHB5_SelectAPIKey_' -v ./...                        # v4.3.10 Key 级配额（11 例）
go test -run 'TestGemini|TestAzure|TestInjectModelIntoBody' -v ./...  # v4.3.29 下游兼容（9 例）
go test -run 'TestIsFreePoolProvider|TestProviderAllowsKeyType_PublicFreePool' -v ./...   # v4.3.29 免费池解耦
go test -run 'TestHB9_HandleFreePool' -v ./...                        # 免费池管理端点（5 例）
```

> ⚠️ 正则须匹配**实际**测试名。`TestHB5_SelectAPIKey_` 末尾下划线是必要的：
> 去掉后会连带匹配 `TestHB5_keyAllowedForAccess` 之外的无关项吗？不会——但保留下划线可与
> `TestHB8_Provider_SelectAPIKey_*` 明确区分开两组同主题、不同前缀的用例，避免误判覆盖来源。

### 2.2 v4.2.9 Kilo Gateway 预设 / v4.3.7 KeyOptional

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `handler_batch6_test.go` | `TestHB6_ProviderManager_EnabledRaw_Empty` | **v4.2.9 关键回归**：`EnabledRaw()` 结果中允许出现 `Enabled:true` 的预设（如 `kilo-gateway`），但不得出现任何非预设 provider。这是「预设默认启用」不污染用户列表的守护 |
| `provider_test.go` | `TestProviderManager_Enabled` | 用户新增的 enabled provider 计数不受默认启用预设干扰（按 ID 白名单过滤后计数） |
| | `TestProviderManager_GetVisible` | 按 `Owner` 可见性隔离 |
| | `TestProviderManager_FindCandidates` / `TestProviderManager_PrioritySort` | 候选集合与优先级排序（`kilo-gateway` `Priority:3` 参与排序的基础） |
| | `TestProviderManager_Safe_MasksAPIKey` | `Safe()` 掩码 Key，`free-anonymous` 亦不外泄原文 |
| `handler_batch10_test.go` | `TestHB10_HandleGetPresets` | `GET /api/presets` 返回 200（v4.3.7 `key_optional` 字段所在响应的最外层契约） |
| `handler_batch7_test.go` | `TestHB7_HandleGetPresets` | 同上，另一批次的重复守护 |
| `handler_batch8_test.go` | `TestHB8_HandleGetPresets` | 同上 |
| `handler_test.go` | `TestHandler_GetPresets` | 同上 |

> **v4.3.7 覆盖现状**：`KeyOptional` / `key_optional` 在**全部 `*_test.go` 中 0 次出现**。
> 上表 4 个 `GetPresets` 用例只断言 HTTP 200，未断言 `key_optional` 字段值。
> 该版本实质处于未测状态 → 见 §2.5 缺口 G-1。

### 2.3 v4.3.10 Provider Key 级精细配额

`handler_batch5_test.go:247-384`，`TestHB5_SelectAPIKey_*` 共 11 例。其中 **后 5 例为 v4.3.10 新增**
（与 CHANGELOG「5 new `TestHB5_SelectAPIKey_*` cases」一致）：

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `handler_batch5_test.go` | `TestHB5_SelectAPIKey_DailyQuotaExceeded` | `QuotaDaily=100, UsedDaily=100, LastDailyReset=今天` → 日配额触顶，跳过该 Key，返回 error |
| | `TestHB5_SelectAPIKey_MonthlyQuotaExceeded` | `QuotaMonthly=500, UsedMonthly=500, LastMonthlyReset=本月` → 月配额触顶，返回 error |
| | `TestHB5_SelectAPIKey_DailyQuotaNotReset` | `LastDailyReset="2000-01-01"`（过期）+ `UsedDaily=100` → **计数器视为 0**，Key 仍可用，返回 `k1`。这是 v4.3.10 语义的核心断言 |
| | `TestHB5_SelectAPIKey_MonthlyQuotaNotReset` | `LastMonthlyReset="2000-01"`（过期）+ `UsedMonthly=500` → 视为 0，Key 仍可用 |
| | `TestHB5_SelectAPIKey_DailyOkMonthlyExceeded` | 日配额有余（10/100）但月配额触顶（500/500）→ 仍拒绝。验证两个维度是 AND 关系而非 OR |
| | `TestHB5_SelectAPIKey_NoKeys` | 无 `APIKey` 且 `APIKeys` 为空 → error |
| | `TestHB5_SelectAPIKey_LegacyKey` | 仅 `APIKey` 时合成 legacy Key 配置（`ID="legacy"`, `AccessControl="private"`） |
| | `TestHB5_SelectAPIKey_QuotaExceeded` | 总配额 `Quota=100, Used=100` → 跳过（v4.3.10 之前的既有路径，回归） |
| | `TestHB5_SelectAPIKey_Expired` | `ExpiresAt` 为过去时间 → 跳过 |
| | `TestHB5_SelectAPIKey_DisabledKey` | `Enabled:false` → 跳过 |
| | `TestHB5_SelectAPIKey_WrongAccess` | `private` Key 请求 `shared` 访问级 → 拒绝 |
| | `TestHB5_keyAllowedForAccess` | 表驱动：`public/shared/private` × `private/shared/guest/""` 访问矩阵 |
| `handler_batch8_test.go` | `TestHB8_Provider_SelectAPIKey_NoKeys` / `_LegacyKey` / `_DisabledKey` / `_QuotaExceeded` / `_ExpiredKey` / `_ValidKey` / `_PriorityOrder`（7 例） | 同一函数的第二组守护，额外覆盖 `_ValidKey` 正常路径与 `_PriorityOrder` 优先级降序 + 同优先级轮转 |
| | `TestHB8_ProviderManager_ResetKeyQuota_NotFound` / `_KeyNotFound` / `_Success`（3 例） | 配额重置（与日/月计数器复位配套的管理动作） |
| `handler_batch2_test.go` | `TestHB2_ResetKeyQuota_NotFound` | 重置不存在的 provider → 404 |
| `handler_batch10_test.go` | `TestHB10_HandleResetKeyQuota_NotFound` | 同上，handler 层 |

**相邻配额回归**（非本批次引入，但共享 quota 语义，建议同跑）：

| 测试文件 | 用例数 | 验证点 |
|----------|--------|--------|
| `quota_priority_test.go` | 9 | 私有→共享→远端配额优先级降级链（`TestQuotaPriority_PrivateSufficient`、`_PrivateInsufficientFallsToShared`、`_SharedInsufficientFallsToRemote`、`_AllExhaustedRejected`、`_PublicUsesSharedThenRemote`、`_Order`、`TestQuotaPriorityManager_DisabledPassthrough`、`_EnabledRouting`、`TestKeyTypeFromString`） |
| `public_key_quota_test.go` | 4 | `TestGuestKeyQuota_DefaultUnlimited`、`_WithQuota`、`TestQuotaAllocation_Defaults`、`_GuestKeyEqualShare` |
| `quota_priority_handler_test.go` | — | 配额优先级 HTTP 路由层守护 |

### 2.4 v4.3.29 Gemini / Azure 下游兼容 + 免费池解耦

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `gemini_api_test.go` | `TestSplitGeminiModelSuffix` | `models/{model}:{suffix}` 解析出 model 与 `generateContent`/`streamGenerateContent` 后缀 |
| | `TestExtractGeminiText` | 从 `parts[]` 拼接文本 |
| | `TestConvertGeminiFinish` | Gemini `finishReason` → OpenAI `finish_reason` 映射 |
| | `TestGeminiAuthAdapter` | 子测试 2 个：`x-goog-api-key` 头认证；`key` query 参数认证且**从 query 中剥离**（避免 Key 泄漏进日志/上游 URL） |
| | `TestGeminiResponseWriter_NonStream` | OpenAI 响应 → Gemini `candidates/content/parts` 非流式封装 |
| | `TestGeminiResponseWriter_Stream` | OpenAI SSE → Gemini SSE 流式封装 + `finalize()` |
| `azure_api_test.go` | `TestInjectModelIntoBody` | Azure 路径参数 `deployment` 注入请求体 `model` 字段 |
| | `TestAzureAuthAdapter` | `api-key` 头 → 内部 Authorization 转换 |
| | `TestAzureAuthAdapter_Passthrough` | 已带标准 Authorization 时透传不覆盖 |
| `utils_test.go` | `TestIsFreePoolProvider` | `APIKey=="free-anonymous"` 命中；`ID` 以 `free-` 前缀命中；付费 provider 不命中（`provider.go:877` 判定器） |
| | `TestProviderAllowsKeyType_PublicFreePool` | **P3-2 解耦核心**：public Key 在 personal 模式下亦可达免费池 provider |
| `handler_batch8_test.go` | `TestHB8_FilterByAccessControl_PublicFreePoolPersonalMode` | 同一语义在候选过滤层的守护（personal 模式下 public Key 过滤后仍保留免费池候选） |
| `handler_batch9_test.go` | `TestHB9_HandleFreePoolStatus_Nil` / `_Sync_Nil` / `_Config_Nil` / `_SetKey_Nil` / `_RemoveKey_Nil`（5 例） | 免费池 5 个管理端点在 `freePool==nil` 时的 fail-safe（不 panic） |
| `handler_test.go` | `TestHandler_FreePoolStatus_Nil` | 同上，重复守护 |

### 2.5 测试缺口（建议新增，尚未实现）

以下缺口均经 `grep` 在全部 `*_test.go` 中确认为 **0 命中**或仅有间接覆盖。建议用例名按现有命名风格给出。

| 编号 | 缺口 | 现状证据 | 建议新增用例 | 优先级 |
|------|------|----------|--------------|--------|
| **G-1** | **v4.3.7 `KeyOptional` 全无覆盖** | `KeyOptional` / `key_optional` 在 `*_test.go` 中 0 命中；4 个 `GetPresets` 用例仅断言 200 | `TestKeyOptional_PresetsMarkedOptional`（断言 `kilo-gateway`/`ollama` 预设 `KeyOptional==true`，且付费预设为 `false`）<br>`TestKeyOptional_HandleGetPresets_ReturnsKeyOptional`（解析 `/api/presets` JSON，断言 `key_optional` 字段随预设正确回传） | P1 |
| **G-2** | **v4.3.13 上游 `GeminiAdapter` 全无覆盖**（含流解析） | `GeminiAdapter` / `geminiStream` / `geminiNonStream` / `adapterRegistry` 在 `*_test.go` 中 0 命中 | `TestGeminiAdapter_TranslateRequest_ContentsParts`（IR → `contents/parts` 嵌套，`user`/`model` 角色映射）<br>`TestGeminiAdapter_TranslateRequest_SystemInstruction`（system prompt → `systemInstruction`）<br>`TestGeminiAdapter_TranslateResponse_CandidatesToChatResponse`<br>`TestGeminiAdapter_TranslateStreamChunk_SSEToChunk`（**流解析**：Gemini SSE chunk → OpenAI `ChatChunk`）<br>`TestGeminiAdapter_ExtractUsage_UsageMetadata`<br>`TestGeminiAdapter_RegisteredInAdapterRegistry`（`adapterRegistry["gemini"]` 非 nil 且 `PlatformName()=="gemini"`）<br>`TestClient_DoNonStream_GeminiCaseRoutes` / `TestClient_DoStream_GeminiCaseRoutes`（httptest 断言路径 `/v1beta/models/{model}:generateContent` 与 `:streamGenerateContent?alt=sse`）<br>`TestGeminiPreset_TypeIsGemini`（回归守护：`providers.go` gemini 预设 `Type=="gemini"` 而非 `openai_compatible`） | **P0** |
| **G-3** | **v4.3.11 `is_gateway` 自动注册无覆盖** | `tunnel_test.go` 9 个用例全为 `TestQADomainBinding*` / `TestQAResolveBoundDomain*` 等域名探测方向，无一断言 `is_gateway` | `TestTunnel_HandleBindDomain_SetsIsGateway`（Cloudflare 路径成功后 `cfg.Get("is_gateway")=="true"`）<br>`TestTunnel_HandleManualDomainBind_SetsIsGateway`（手动路径同上）<br>`TestTunnel_BindDomain_IsGatewayPersisted`（重载配置后仍为 true，验证持久化而非仅内存）<br>`TestTunnel_BindDomainFailure_DoesNotSetIsGateway`（绑定失败时**不**置位，避免误标网关） | P1 |
| **G-4** | **v4.2.9 Kilo 预设形状无断言** | 12 模型 / `Enabled:true` / 无 Key 三个特征仅由 `EnabledRaw` 用例间接兜底 | `TestKiloGatewayPreset_TwelveModelsNoKeyEnabled`（断言 `ID=="kilo-gateway"`、`len(Models)==12`、`Enabled==true`、`APIKey=="free-anonymous"`、`BaseURL=="https://api.kilo.ai/api/gateway"`） | P2 |
| **G-5** | **下游 Gemini/Azure 主 handler 无端到端覆盖** | 现有 9 例只覆盖 helper 与 auth adapter；`handleGeminiGenerateContent`（`gemini_api.go:83`）与 `handleAzureChatCompletions`（`azure_api.go:36`）本体未被直接调用 | `TestHandleGeminiGenerateContent_NonStreamEndToEnd`<br>`TestHandleGeminiGenerateContent_StreamEndToEnd`<br>`TestHandleAzureChatCompletions_EndToEnd`（均以 httptest 上游 + 真实 handler 串联） | P1 |
| **G-6** | **免费池解耦端到端无覆盖** | 现有 3 例均为谓词/过滤层单点；`routes.go` 路由串联与「私有共享不被误暴露」的反向断言缺失 | `TestFreePool_PublicKeyReachesFreePoolInPersonalMode_E2E`（personal 模式下 public Key 走完整路由拿到免费池候选）<br>`TestFreePool_SharedProviderNotExposedInPersonalMode`（**反向断言**：非免费池的 operator-shared provider 在 personal 模式下对 public Key 不可见） | P1 |

> 缺口合计：6 项，建议新增 19 个用例。**最大缺口为 G-2**——v4.3.13 整个上游 Gemini 适配器
> （请求/响应/流式翻译 + 注册表 + client 分发 + 预设类型变更）零单测，且它是 P0 功能路径。

---

## 3. 集成 / QA 手册

### 3.1 Provider 预设与 `KeyOptional`（v4.2.9 + v4.3.7）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.1.1 | 全新数据目录首次启动，进入管理面板「Provider」页 | `Kilo Gateway (免费)` 已存在且开关为**开**（`Enabled:true`），无需任何配置 |
| 3.1.2 | 展开 Kilo Gateway 模型列表 | 共 **12** 个模型，含 `Auto Free (智能路由)`、Nemotron 3 Ultra（1M 上下文）、StepFun、Ling 等 |
| 3.1.3 | `curl -s localhost:PORT/api/presets \| jq '.[] \| select(.id=="kilo-gateway") \| .key_optional'` | 返回 `true` |
| 3.1.4 | 同上查询 `ollama` | 返回 `true` |
| 3.1.5 | 同上查询任一付费预设（如 `openai`） | 返回 `false` 或字段缺省（`omitempty`） |
| 3.1.6 | 新增 provider 界面选择 `Kilo Gateway` 预设，**Key 留空**直接保存 | 保存成功；不出现「请填写 API Key」校验拦截；Key 输入框 placeholder 为「API Key（可选，此平台无需 Key）」；注册链接被隐藏 |
| 3.1.7 | 改选 `openai` 预设，Key 留空保存 | **应被拦截**（对照组：`KeyOptional=false` 时校验仍生效） |
| 3.1.8 | 用免费池模型发一次 `/v1/chat/completions` | 返回 200，无需配置任何 Key |

**失败判据**：3.1.1 开关为关 → v4.2.9 回归；3.1.2 模型数 ≠ 12 → 预设被篡改；3.1.3/3.1.4 返回 false → `handleGetPresets` 未回传 `key_optional`（v4.3.7 回归）；3.1.6 被拦截 → 前端未读 `key_optional`；**3.1.7 未被拦截 → 校验被整体绕过，属安全回归**。

### 3.2 Provider Key 级精细配额（v4.3.10）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.2.1 | 任一 provider → Key 列表 → 「编辑额度」 | 弹出日/月/总额度输入；`quotaDaily`/`quotaMonthly` 有实际取值（v4.3.10 修复前为 undefined） |
| 3.2.2 | 设日额度 = 10，发 10 次请求 | Key 列表显示「日: 10/10」 |
| 3.2.3 | 发第 11 次请求 | 该 Key 被跳过；若无其他可用 Key，返回「no available API key」类错误 |
| 3.2.4 | 手动把该 Key 的 `LastDailyReset` 改成过去日期（如 `2000-01-01`），保留 `UsedDaily=10` | 请求**恢复可用**——过期计数器按 0 处理，而非判定超限 |
| 3.2.5 | 设日额度 = 100（余量充足）、月额度 = 已用满 | 请求仍被拒绝（日月为 AND 关系） |
| 3.2.6 | Key 列表检查显示 | 日/月/总三档各自显示 `used/limit`，且总额度不重复显示 |
| 3.2.7 | 点「重置额度」 | 计数归零，请求恢复 |

**失败判据**：3.2.4 仍被拒 → 过期计数器被误判为超限（v4.3.10 核心语义反向失败，会导致跨天后 Key 永久不可用）；3.2.5 放行 → 日月被当 OR；3.2.1 输入框为空/undefined → `showAddApiKey` 回归。

### 3.3 域名绑定后自动注册为 Gateway（v4.3.11）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.3.1 | 绑定前：`curl -s localhost:PORT/api/config \| grep is_gateway`（或查 config 持久化文件） | `is_gateway` 为 `false` / 缺省 |
| 3.3.2 | 走 **Cloudflare API** 路径绑定域名（`handleBindDomain`） | 绑定成功；`is_gateway` 变为 `"true"` |
| 3.3.3 | **重启进程**后再查 | 仍为 `"true"`（验证 `cfg.Set` 后确实落盘，非仅内存） |
| 3.3.4 | 换新数据目录，走**手动绑定**路径（`handleManualDomainBind`，无 CF Token） | 同样置 `is_gateway="true"` 并持久化 |
| 3.3.5 | 让绑定**失败**（填错误域名/无效 Token） | `is_gateway` **不**被置位 |
| 3.3.6 | 在已互连的对端节点等待一轮 Gossip 心跳（默认 30s），查对端路由表 | 本节点条目 `IsGateway=true`（经既有心跳传播，无需额外广播） |

**失败判据**：3.3.2/3.3.4 未置位 → v4.3.11 失败；3.3.3 重启后丢失 → 只改内存未持久化；**3.3.5 失败时置位 → 误标网关，会把流量引到不可达节点**；3.3.6 对端不可见 → 心跳未携带该字段。

### 3.4 Gemini 原生适配（上游，v4.3.13）与下游兼容（v4.3.29）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.4.1 | 查 Gemini 预设类型 | `Type == "gemini"`（非 `openai_compatible`）；`BaseURL == https://generativelanguage.googleapis.com` |
| 3.4.2 | 配置真实 Gemini Key，发**非流式** `/v1/chat/completions` | 出站 URL 为 `/v1beta/models/{model}:generateContent?key=`；返回标准 OpenAI 格式 `choices[0].message.content`；`usage` 由 `usageMetadata` 填充 |
| 3.4.3 | 同上发**流式**（`stream:true`） | 出站 `:streamGenerateContent?alt=sse&key=`；下游收到 OpenAI 兼容 SSE 增量，末尾 `[DONE]` |
| 3.4.4 | 带 system prompt 再发一次 | 上游请求体含 `systemInstruction`，而非把 system 混入 `contents` |
| 3.4.5 | **下游** Gemini 入口：`POST /v1beta/models/{m}:generateContent`，Key 放 `x-goog-api-key` 头 | 200，响应为 Gemini 原生 `candidates/content/parts` 形状 |
| 3.4.6 | 同上但 Key 放 `?key=` query | 200，且 Key **不出现**在转发给上游的 URL / 访问日志中 |
| 3.4.7 | **下游** Azure 入口：`POST /openai/deployments/{dep}/chat/completions`，Key 放 `api-key` 头 | 200；`{dep}` 被注入请求体 `model` 字段 |
| 3.4.8 | 3.4.7 请求改带标准 `Authorization: Bearer` | 透传不被覆盖 |

**失败判据**：3.4.1 类型仍为 `openai_compatible` → v4.3.13 预设变更回归（会走错适配分支）；3.4.2/3.4.3 出站路径缺 `/v1beta` 或流式缺 `alt=sse` → `client.go` `"gemini"` case 未生效；3.4.4 无 `systemInstruction` → `TranslateRequest` 缺陷；**3.4.6 Key 出现在日志/上游 URL → 凭据泄漏，P0 安全问题**。

### 3.5 免费池与私有共享解耦（v4.3.29）+ 一键部署

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.5.1 | 节点置 **personal 模式**（非 shared），配置 1 个免费池 provider + 1 个私有共享 provider | — |
| 3.5.2 | 用**公共 Key** 请求免费池模型 | 200 —— 免费池在任何模式下对 public Key 开放（无需 shared 模式） |
| 3.5.3 | 用公共 Key 请求那个**私有共享** provider 的模型 | **被拒** —— 私有共享仍要求 shared 模式，不因免费池放开而一并暴露 |
| 3.5.4 | 切到 shared 模式且 `ShareToPool=true`，重试 3.5.3 | 200 |
| 3.5.5 | `docker compose up -d` 后访问服务端口 | 单命令起服；管理面板可访问；数据卷持久化 |
| 3.5.6 | 观察 `X-OMP-Quota-Source` 等响应头（若启用） | 免费池请求标记来源为免费池，不计入私有配额 |

**失败判据**：3.5.2 被拒 → 解耦未生效（免费池仍被 shared 模式门槛挡住）；**3.5.3 放行 → 私有付费 Key 被泄露给公共 Key，P0 隐私回归**；3.5.5 起不来 → `docker-compose.yml` 失效。

---

## 4. 质量门禁（CI）

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -l .` | 无输出 |
| 构建 | `go build ./...` | 0 error |
| 静态 | `go vet ./...` | 0 新增 warning |
| 本批次单测 | §2.1 首条合并命令 | 全部 PASS（已验证：provider 配额组 + 免费池组 `ok`；Gemini/Azure 下游组 9 例全 PASS） |
| 全量（对齐 CI） | `go test -race -count=1 -timeout 25m ./...` | 通过。`-count=1` 禁用缓存（缓存 PASS 只为旧 commit 背书）；Windows 沙箱下存在与文件权限/日志锁相关的预存失败，属环境限制 |
| 缺口守护 | §2.5 的 19 个建议用例落地后并入上表 | 落地前须在 PR 描述中显式声明缺口仍存在，不得以「已覆盖」表述 |

---

## 5. 一致性复核（IS_PASS）

- [ ] `providers.go` `kilo-gateway` 预设：`Enabled:true`、`APIKey=="free-anonymous"`、`KeyOptional:true`、模型数 **12**、`BaseURL=="https://api.kilo.ai/api/gateway"`（v4.2.9）
- [ ] `types.go` `KeyOptional` 字段存在；`kilo-gateway` 与 `ollama` 均标记 `true`；`handleGetPresets` 响应含 `key_optional`（v4.3.7）
- [ ] `provider.go` `SelectAPIKey` 三档配额（总/日/月）全部检查，且**日期不匹配的计数器按 0 处理**（v4.3.10）
- [ ] 5 个 v4.3.10 新增用例 `TestHB5_SelectAPIKey_{DailyQuotaExceeded,MonthlyQuotaExceeded,DailyQuotaNotReset,MonthlyQuotaNotReset,DailyOkMonthlyExceeded}` 存在且 PASS
- [ ] `tunnel.go` 两条绑定路径（`handleBindDomain` / `handleManualDomainBind`）成功后均 `cfg.Set("is_gateway","true")` 并持久化；失败路径不置位（v4.3.11）
- [ ] `platform_adapter.go` `adapterRegistry["gemini"] == &GeminiAdapter{}`；四个 Translate/Extract 方法齐备（v4.3.13）
- [ ] `client.go` `doNonStream` / `doStream` 均含 `"gemini"` case；`geminiNonStream` / `geminiStream` 存在（v4.3.13）
- [ ] `providers.go` gemini 预设 `Type == "gemini"`（v4.3.13）
- [ ] `gemini_api.go` / `azure_api.go` 下游入口存在；两种 Key 传递方式（头 / query）均支持，且 query Key 被剥离（v4.3.29）
- [ ] `provider.go` `isFreePoolProvider` + `filterFreePoolOnly` + `providerAllowsKeyType` 三者协同：public Key 任何模式可达免费池，operator-shared 仍需 shared 模式（v4.3.29）
- [ ] `docker-compose.yml` 与 `.dockerignore` 存在且可 `docker compose up` 起服（v4.3.29）
- [ ] §2.5 六项缺口在文档中如实标注为「建议新增」，未被表述成已覆盖
- [ ] 无 `TODO` / 占位 / `pass` / `...`
