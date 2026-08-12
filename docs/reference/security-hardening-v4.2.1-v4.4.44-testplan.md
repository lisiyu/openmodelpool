# OpenModelPool 安全加固批次 测试计划（v4.2.1 – v4.4.44）

> 版本范围：v4.2.1 – v4.4.44（安全加固聚合批次）
> 聚合理由：本计划覆盖一条贯穿多个版本的**安全相关增量线**——从 v4.2.1 的 TOCTOU/匿名 admin 修复，到 v4.2.3/4.2.4 的更新校验与传输加密，再到 v4.4.44 的「最大安全 pass」（72 项外部审查结论的 triage 与修复）。纯版本号 bump、且未携带安全变更的版本（如 v4.1.8–v4.1.11、v4.2.6–v4.2.12 等仅含重构/测试修复/功能）已跳过，不计入本批次能力表，但其既有回归测试仍随全量门禁运行。
> 关联文档：`docs/CHANGELOG.md`、独立审查 triage 记录 `docs/reference/REVIEW-TRIAGE-2026-08-10.md`。

---

## 1. 范围与背景

本批次按安全增量而非功能主题组织。下表逐版本列出能力、关键变更与优先级（P0/P1/P2）。所有条目均提炼自 `docs/CHANGELOG.md` 对应版本段。

| 版本 | 能力 | 关键变更 | 优先级 |
|------|------|----------|--------|
| v4.2.1 | C1 auth 深拷贝 | `auth.go` `save()`/`saveLocked()` 改用 `deepCopyDataLocked()`，消除 `encryptField()` 原地改写明文 SMTP 密码的 TOCTOU | P0 |
| v4.2.1 | C2 provider 原子写 | `provider.go` 写文件由 `os.WriteFile()` 改为 `atomicWriteFile()`（写临时文件 + rename） | P0 |
| v4.2.1 | C3 匿名 admin 收窄 | 未配 `proxy_api_key` 且无 consumer 时，匿名 admin 仅限 localhost/私网来源，公网不可达 | P0 |
| v4.2.3 | P0-1 自更新校验 | 自更新下载后校验 SHA-256 checksum；缺失仅告警（向后兼容），不匹配即删除并中止 | P0 |
| v4.2.3 | P1-1 访问控制头伪造（部分） | `X-MK-KeyType`→`X-OMP-KeyType` 重命名；但 strip 实际未实现，留待 v4.4.44 解决；`FilterByAccessControl()` 默认分支改为 fail-closed | P1 |
| v4.2.3 | P2-2 CORS 收窄 | `eventbus.go`/`network_seed.go` 硬编码 `*` 改为 `isOriginAllowed()`（受 `cors_allowed_origins` 约束） | P2 |
| v4.2.3 | P2-3 SSRF 私网拦截 | `handleCreateProvider`/`handleUpdateProvider` 对 BaseURL 增加私网/环回地址拦截（`isLocalOrPrivateIP()`） | P2 |
| v4.2.4 | P2-Transport 传输加密 | 自研 SHA-256 KDF 升级为 X25519 ECDH + HKDF-SHA256（Ed25519→X25519 双有理映射，RFC 7748） | P2 |
| v4.2.4 | P2-Update 签名校验 | 自更新下载 `.sig` 并用内嵌 Ed25519 公钥验签；验签失败 fail-closed，缺 `.sig` 回退 SHA-256 | P2 |
| v4.2.4 | P2-WAF 攻击模式 | WAF 增加 40+ 内置攻击模式（SQLi/XSS/路径穿越/命令注入/SSRF） | P2 |
| v4.3.0 | conn_tracker 竞态 | `IncrProviderConn`/`IncrGuestConn` 由 Load+Store 改为 `LoadOrStore` + `atomic.AddInt64` | P2 |
| v4.3.1 | 出站请求超时 | `update.go`/`stubs.go`/`platform_discovery.go`/`network_balance.go` 的 `context.Background()` 补超时（15s/30s） | P2 |
| v4.3.2 | 消费者注册泄露 | `multiuser.go` `handleConsumerRegister` 不再返回含 `APIKey` 的完整 `Consumer`；`Consumer.APIKey` 加 `json:",omitempty"` | P2 |
| v4.3.3 | vmess TOCTOU | `vmess.go` 去除 `os.CreateTemp` + `atomicWriteFile` 双写，直接 `atomicWriteFile` 写 `data/xray-{id}.json` | P2 |
| v4.3.4 | hopCount 解析 | `network_relay.go` 2 处 `strconv.Atoi(hopCount)` 失败返回 400（非数字不再按 0 处理） | P2 |
| v4.3.6 | 中继防环遮蔽（P0） | `network_relay.go` `handleGatewayRequest` 中 `hopCount` 误用 `:=` 遮蔽外层变量，中继防环完全失效 | P0 |
| v4.3.8 | 共享网络开关全死（P0） | `admin-network.js` 末尾多余 `}}` 致整文件 SyntaxError，三开关（加入共享/共享额度/开启中继）定义丢失 | P0 |
| v4.3.9 | 初始化回归修复 | `renderNetworkUI()` 从权威服务端状态派生 `_networkMode`；恢复 `loadFederationConfig()` 调用 | P0 |
| v4.4.44 | 关闭 relay-to-self 绕过 | `/network/{id}/...` 加 `relayAuthMiddleware`；`handleRelayToLocal` 保留原始 `RemoteAddr` 并标记 untrusted；可中继路径白名单 `/v1/*`、`/api/network/heartbeat/ping` | P0 |
| v4.4.44 | 停止信任 X-OMP-KeyType | 最早中间件 + 两个 relay Director 均 strip；`RequestKeyType` 改从已验证 token/context 派生 | P0 |
| v4.4.44 | 联邦 trust-pool 投毒修复 | `peers/notify` 取 key 失败时不再回退攻击者 payload 的 `PubKey`，验证 fail-closed | P0 |
| v4.4.44 | seed 注册 fail-closed | 无 `seed_secret` 时拒绝注册；密钥比较用常量时间 | P0 |
| v4.4.44 | 更新管道 fail-closed | checksum/签名仅从 canonical GitHub 取；缺失/不匹配即中止（不再 warn-and-continue） | P0 |
| v4.4.44 | provider config import 加固 | 用共享 `validateProviderBaseURL`（scheme + 私网 IP）校验；合并而非截断；不再死锁 | P0/P2 |
| v4.4.44 | CSV 公式注入中和 | 导出贡献 CSV 时中和 `= + - @` 等公式触发字符 | P2 |
| v4.4.44 | XFF 受信任代理限制 | `X-Forwarded-For` 仅当来源在 `OMP_TRUSTED_PROXY` 内才采纳 | P2 |
| v4.4.44 | heartbeat 默认 deny | `/api/network/heartbeat/*` 默认拒绝，需显式放行 | P2 |
| v4.4.44 | ReadHeaderTimeout | HTTP server 设 `ReadHeaderTimeout: 10s` | P2 |
| v4.4.44 | 常量时间 reset-token | 重置令牌比较用常量时间比较 | P2 |
| v4.4.44 | 条件 CORS credentials | CORS `credentials` 按来源条件设置，不再无条件 | P2 |
| v4.4.44 | 扩展 SSRF 私网 CIDR | `isLocalOrPrivateIP()` 私网 CIDR 范围扩展 | P2 |
| v4.4.44 | goroutine dump 开关 | goroutine dump 受 debug 开关限制 | P2 |
| v4.4.44 | restart.sh 权限检查 | `restart.sh` 增加属主/权限检查 | P2 |
| v4.4.44 | install.sh fail-closed | `install.sh` checksum 校验失败即中止 | P2 |
| v4.4.44 | handleDirectProbe 校验 | `handleDirectProbe` 增加目标 URL 合法性校验（SSRF） | P2 |
| v4.4.44 | 通用错误消息 | 统一错误响应，避免泄露内部细节 | P2 |
| v4.4.44 | 修复 escapeJS | 修正 `escapeJS` 转义逻辑 | P2 |

---

## 2. 自动化单元测试（离线 / httptest）

下表所列**测试文件与用例名均经 Grep 校验，真实存在**。对每个能力：若已有对应测试则列出验证点；若关键安全路径缺少自动化测试，在 §2.10「测试缺口」明确标注建议新增用例。

### 2.1 认证、重置令牌与匿名 admin（v4.2.1 C1 / v4.2.1 C3 / v4.4.44 常量时间）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `security_p0_test.go` | `TestP0_1_HandleVerifyAuth_ValidToken` / `_InvalidToken` / `_NoToken` / `_ExpiredToken` | token 校验四态：合法放行、非法/无/过期均拒 |
| `security_p0_test.go` | `TestP0_2_GenerateAndValidateResetCode` | 重置码可生成并校验通过 |
| `security_p0_test.go` | `TestP0_2_ResetCodeNotProxyAPIKey` / `TestP0_2_HandleResetWithCode_NoLongerUsesProxyKey` / `TestP0_2_HandleResetWithCode_ProxyKeyRejected` | 重置码不再是 proxy_api_key；用 proxy key 重置被拒（防代理密钥即重置密钥的越权） |
| `security_p0_test.go` | `TestP0_3_AuthConcurrentAccess` / `TestP0_3_AuthConcurrentReadWrite` / `TestP0_3_AuthConcurrentVerifyCredentials` / `TestP0_3_AuthConcurrentTokenOps` | auth 并发读写/校验无竞态（覆盖 C1 深拷贝的并发面） |
| `security_p0_test.go` | `TestP0_4_ProviderManagerSaveMutex` / `TestP0_4_ProviderManagerSaveFilePermissions` | `save()` 持锁安全；写出文件权限正确（Windows 下 mode 合成 0666） |
| `security_medium_test.go` | `TestSA09_ValidateKeyGenericErrors` | 密钥校验错误走通用消息（对应 v4.4.44 通用错误消息） |
| `security_medium_test.go` | `TestSA14_PasswordStrength` | 管理员密码强度要求 |

> C1 的 `deepCopyDataLocked()` 无**独立**用例断言「加密不改写原内存明文」；C3 匿名 admin 仅限 localhost/私网的收窄**无专门用例**（现状依赖 `withProxyAuth`/匿名路径集成行为）。二者列入 §2.10 缺口。

### 2.2 原子写与文件持久化（v4.2.1 C2 / v4.4.44 数据完整性）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `security_p0_test.go` | `TestP0_4_ProviderManagerSaveFilePermissions` | provider 配置写盘权限正确（原子写副作用） |
| `data_integrity_test.go` | `TestDataIntegrity_SaveAndLoad` / `TestDataIntegrity_TamperedData` / `TestDataIntegrity_LoadPlainJSON` / `TestDataIntegrity_VerifyHMAC` | HMAC 前缀文件被篡改即拒（SA-15 fail-closed）；无前缀纯 JSON 兼容加载 |
| `security_medium_test.go` | `TestSA15_SaveAndLoadWithIntegrity` / `TestSA15_TamperedFileDetected` / `TestSA15_BackwardCompat_PlainJSON` | 完整性加载：篡改检测 + 旧格式兼容 |

### 2.3 自更新校验（v4.2.3 P0-1 / v4.2.4 P2-Update / v4.4.44 更新 fail-closed）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `update_test.go` | `TestAtomicReplace` | 自更新二进制原子替换（写临时 + rename） |
| `update_test.go` | `TestDownloadFile404` / `TestDownloadFileOK` | 下载失败/成功路径 |
| `update_test.go` | `TestReconcilePendingSuccess` / `TestReconcilePendingInFlightReset` | pending 状态对账 |
| `update_qa_test.go` | `TestQASignatureRoundTrip` / `TestQARouteSignalValidSignature` / `TestQARouteSignalWrongSignature` / `TestQARouteSignalUnknownNode` | 更新信令路由的 ed25519 验签（合法/伪造/未知节点） |
| `update_qa_test.go` | `TestQARouteUpdateStartNoUpdate` / `TestQARouteUpdateStatus` / `TestQARouteVersionLatest` | 更新状态/启动路由；版本比较 |

> **关键缺口**：`update_test.go` 无任何用例校验「SHA-256 checksum 不匹配即中止」（P0-1）与「Ed25519 签名验签失败 fail-closed」（P2-Update 与 v4.4.44 更新管道）。见 §2.10。

### 2.4 访问控制头与 X-OMP-KeyType（v4.2.3 P1-1 / v4.4.44 停止信任）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `relay_security_test.go` | `TestRequestKeyType_IgnoresWireHeader` | `RequestKeyType` 忽略伪造 `X-OMP-KeyType`（Priority 1 不再采信），伪造 admin→`unknown`（fail-closed） |
| `relay_security_test.go` | `TestStripInternalHeadersMiddleware` | 最早中间件在 handler 前删除 `X-OMP-KeyType` |
| `relay_security_test.go` | `TestRelayToRemote_StripsKeyTypeHeader` | `relayToRemote` 出站转发前 strip `X-OMP-KeyType` |
| `relay_auth_test.go` | `TestRelayForwardAuth_ValidSignature_Passes` / `_MissingSignature_Rejected` / `_ReplayTimestamp_Rejected` / `_PathTamper_Rejected` | 中继转发 auth：合法签名放行；缺签名/重放/路径篡改均拒 |
| `relay_auth_test.go` | `TestGatewayForwardToRemoteSignsRequest` / `TestRelayToRemoteSignsRequest` | 出站中继请求由本地签名 |
| `security_medium_test.go` | `TestSA12_FederationAuth_LocaleRequiresSecret` | 联邦受保护路径需密钥（fail-closed 兜底） |

> 该组用例即 v4.4.44 对 P1-1「strip 从未实现」的闭环修复；P1-1 在 v4.2.3 仅做重命名，真实 strip 由本批次 v4.4.44 补齐。

### 2.5 CORS / SSRF / 私网（v4.2.3 P2-2 / P2-3 / v4.4.44 扩展 CIDR、XFF、direct probe）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `middleware_test.go` | `TestIsOriginAllowed` | 精确 origin 匹配（通配 `*.example.com` 已移除，防伪造） |
| `middleware_test.go` | `TestIsLocalOrPrivateIP` | 环回/私网/RFC1918 判定 |
| `middleware_test.go` | `TestCorsMiddleware_CORSHeadersSet` | CORS 头按配置设置 |
| `handler_batch4_test.go` / `handler_batch10_test.go` | `TestHB4_IsPrivateIPv4_*` / `TestHB10_IsLocalOrPrivateIP_*` | 私网/环回 IP 判定矩阵（覆盖 v4.4.44 扩展 CIDR 的基底） |
| `handlers_test.go` | `TestIsPrivateIPv4` | RFC1918 私有段检测 |
| `batch2_security_test.go` | `TestClientIPs_XFFGatedByTrustedProxy` | `X-Forwarded-For` 仅 `OMP_TRUSTED_PROXY` 内采纳 |
| `batch2_security_test.go` | `TestHandleDirectProbe_RejectsPrivateTarget` | `handleDirectProbe` 拒绝私网目标（SSRF 守卫） |
| `network_keys_security_test.go` | `TestClassifyKey_GuestKey` / `TestClassifyKey_ProxyKey` | 密钥类型分类（与访问控制联动） |

> P2-3「provider BaseURL 私网拦截」**无专门用例**断言 `handleCreateProvider`/`handleUpdateProvider` 拒绝私网 BaseURL；provider config import 的 `validateProviderBaseURL` 校验亦无用例。见 §2.10。

### 2.6 传输加密（v4.2.4 P2-Transport）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `encryptor_test.go` | `TestEncryptor_EncryptDecrypt_Roundtrip` / `TestEncryptField_DecryptField` / `TestDecryptAPIKey` / `TestNewEncryptor_GeneratesKey` | 字段级加解密往返、密钥生成（AES-GCM 层） |

> **关键缺口**：X25519 ECDH + HKDF-SHA256 的密钥协商**无专门用例**（如 `TestEncryptor_X25519ECDH_HKDF`）。encryptor_test 仅覆盖 AES-GCM 字段层。见 §2.10。

### 2.7 WAF（v4.2.4 P2-WAF：40+ 攻击模式）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `waf_qa_test.go` | `TestWAFDefaultOffPassthroughQA` / `TestWAFDefaultOffFullMuxQA` / `TestWAFBansEndpointQA` / `TestWAFConcurrentStress` | 默认关闭透传、封禁端点、并发压力 |
| `waf_wire_test.go` | `TestWAFBlocksBlacklistedIP` / `TestWAFBlocksUserAgent` / `TestWAFRateLimitBlocksExcess` / `TestWAFBlocksBlockedPath` / `TestWAFDisabledAllowsAll` / `TestWAFEnabledNoRulesAllows` / `TestWAFStatusReflectsEnabled` / `TestWAFViolationsRecorded` / `TestWAFUnbanRemovesBan` | IP/UA/限流/路径封禁、禁用放行、状态、违规记录、解封 |

> **缺口**：上述用例覆盖 WAF 的封禁/限流/状态机，但**未覆盖 40+ 内置攻击模式本身**（SQLi/XSS/路径穿越/命令注入/SSRF 模式匹配）。见 §2.10。

### 2.8 竞态 / 超时 / 泄露 / TOCTOU（v4.3.0 – v4.3.6、4.3.8/4.3.9）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `conn_tracker_verify_test.go` | `TestConnTracker_RequestLifecycleReturnsToZero` / `TestConnTracker_GuestScopeIsolation` | 连接计数生命周期归零、guest 作用域隔离 |
| `network_relay_test.go` | `TestRelayHopCountValidation` | hopCount 与 maxHops 比较逻辑（对应 v4.3.4 的 400 返回与 v4.3.6 的防环语义，helper 级） |
| `network_relay_test.go` | `TestRelayKeyRouting` / `TestRelayHeaderConstants` | 中继密钥路由、头常量 |
| `consumer_security_test.go` | `TestConsumerSecurity_APIKeyHashed` / `TestConsumerSecurity_HashLookup` / `TestConsumerSecurity_WrongKeyRejected` / `TestConsumerSecurity_MultipleConsumersHashed` / `TestConsumerSecurity_DeleteRemovesHash` / `TestConsumerSecurity_PersistAndReload` / `TestHashAPIKey_Consistency` / `TestConsumerSecurity_BatchSave` | consumer APIKey 以 SHA-256 存储、查表、错误拒绝、删除清理、持久化 |

> **缺口**：
> - v4.3.0 `IncrProviderConn`/`IncrGuestConn` 竞态修复无**确定性**用例，依赖 `go test -race` 门禁（§4）。
> - v4.3.1 出站请求 `context.Background()` 加超时**无单测**（依赖集成/代码审查）。
> - v4.3.2 `handleConsumerRegister` 不再泄露完整 `Consumer`（含 `APIKey`）**无用例**断言响应体已裁剪。
> - v4.3.3 vmess TOCTOU 双写去除**无用例**。
> - v4.3.6 `handleGatewayRequest` 的 `:=` 变量遮蔽（P0，防环失效）仅 helper 级覆盖，**无端到端防环用例**断言中继环被打破。
> - v4.3.8/4.3.9 为前端 JS（整文件 SyntaxError、初始化回归），Go 单测无法覆盖，归 §3 手动 QA。
> 见 §2.10。

### 2.9 v4.4.44 中继绕过 + trust-pool + seed + config import + 其它

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `relay_security_test.go` | `TestRelayToSelf_AttackClosed_ThroughMux` | 经真实 mux 的 relay-to-self 绕过攻击被关闭（P0） |
| `relay_security_test.go` | `TestRelayAuthMiddleware` | `/network/{id}/...` 受 `relayAuthMiddleware` 守护 |
| `relay_security_test.go` | `TestRelayToLocal_PathWhitelist` | `handleRelayToLocal` 仅白名单路径（`/v1/*`、`/api/network/heartbeat/ping`）可本地派发 |
| `relay_security_test.go` | `TestWithProxyAuth_RelayDispatched_NoAnonymousAdmin` | 经中继派发时，`withProxyAuth` 的「localhost 匿名 admin」兜底**不可达** |
| `relay_security_test.go` | `TestLocalOnly_RelayDispatched_Rejected` | 中继派发的请求不满足 `localOnly` 守卫（保留原始 RemoteAddr 标记 untrusted） |
| `config_import_qa_test.go` | `TestImportConfig_ShareToPoolMerge` | provider config import 合并而非截断；旧代码在锁内 `save()` 自死锁，本用例用真实 `dataPath` 运行——旧代码会挂起、新代码毫秒级通过 |
| `batch2_security_test.go` | `TestExportContributionsCSV_FormulaInjectionNeutralized` | 贡献 CSV 导出中和公式注入字符 |
| `batch2_security_test.go` | `TestNetworkHeartbeat_DefaultDeny` | heartbeat 默认 deny |
| `batch2_security_test.go` | `TestCheckShareBoundary_LockPairingAndCorrectness` | `CheckShareBoundary` 锁配对正确（修复 v4.3.32 泄漏 RLock） |
| `federation_auth_test.go` | `TestFederationAuth_TrustedSeedGETPoolAllowed` / `TestFederationAuth_NonSeedGETPoolForbidden` / `TestFederationAuth_NonGETPoolForbidden` / `TestFederationAuth_OtherProtectedPathForbidden` | 联邦受保护路径鉴权（只读池仅可信种子 GET 放行，SA-12 不破坏） |
| `network_keys_security_test.go` | `TestValidateGuestKey_RejectsUnknownKey` / `_RejectsNonGuestPrefix` / `_RejectsRevokedKey` / `_AcceptsValidKey` / `_RejectsExpiredKey` / `_MalformedKeys` / `TestGenerateGuestKey_Format` | guest key 校验全矩阵 |

> **缺口**：v4.4.44 的 trust-pool 投毒（`peers/notify` 取 key 失败 fail-closed）、seed 注册无 `seed_secret` fail-closed + 常量时间比较、provider config import 的 `validateProviderBaseURL`（scheme + 私网）校验——均**无专门用例**。见 §2.10。

### 2.10 测试缺口（建议新增用例）

下列关键安全路径当前**缺少自动化测试**，建议在本批次或后续补测。括号内为建议用例名（命名遵循现有 `TestXxx_*` 约定，使用前请以 Grep 确认未与现有用例冲突）：

| 缺口归属 | 建议新增用例 | 说明 |
|----------|--------------|------|
| v4.2.1 C1 | `TestAuth_SaveDoesNotMutatePlaintext` | 断言 `save()`/`saveLocked()` 加密后原内存明文 SMTP 密码未被改写（深拷贝验证） |
| v4.2.1 C3 | `TestAnonymousAdmin_RejectedFromPublicIP` | 公网来源访问匿名 admin 路径被拒；localhost/私网放行 |
| ✅ v4.2.3 P0-1 / v4.2.4 P2-Update / v4.4.44 | `TestFetchChecksum_InvalidFormat` / `TestFetchChecksum_OK` / `TestFetchSignature_NotFound` / `TestFetchSignature_WrongSize` / `TestFetchSignature_OK`（`update_failclosed_test.go`） | 校验和格式非法 / 签名 404 / 签名长度非 64 字节均 fail-closed 报错；合法值解析通过。**说明**：覆盖了 `fetchChecksum`/`fetchSignature` 的守卫（v4.4.44 更新管道 fail-closed 的核心校验），整条 `applyUpdate` 管道因依赖真实下载未做端到端 mock，由安装器侧 `omp-manager.sh/.ps1`（见 E testplan §3.5，已修）兜底 |
| v4.2.4 P2-Transport | `TestEncryptor_X25519ECDH_HKDF_Roundtrip` | X25519 ECDH 协商 + HKDF-SHA256 派生 key 的往返一致 |
| v4.2.3 P2-3 / v4.4.44 | `TestHandleCreateProvider_RejectsPrivateBaseURL` / `TestImportConfig_RejectsPrivateBaseURL` | 创建/导入 provider 拒私网 BaseURL（`validateProviderBaseURL`） |
| v4.2.4 P2-WAF | `TestWAF_BlocksSQLiPattern` / `TestWAF_BlocksXSSPattern` / `TestWAF_BlocksPathTraversal` | 40+ 攻击模式中具代表性的模式匹配拦截 |
| v4.3.0 | `TestConnTracker_IncrProviderConnNoRace`（配合 `-race`） | 并发 `IncrProviderConn`/`IncrGuestConn` 无竞态（确定性不足，靠 `-race` 兜底） |
| v4.3.1 | `TestOutboundRequests_HaveDeadline` | 出站请求带超时（代码层断言 `ctx` 含 deadline） |
| v4.3.2 | `TestHandleConsumerRegister_NoAPIKeyLeak` | 注册响应体不含完整 `Consumer` 与 `APIKey` |
| v4.3.3 | `TestVMess_AtomicWriteNoTOCTOU` | `vmess.go` 直接 `atomicWriteFile`，无 CreateTemp 双写窗口 |
| v4.3.6（P0） | `TestHandleGatewayRequest_HopCountNoShadowing` | 中继环场景：gateway 请求 hopCount 正确累加并打破环路（防 `:=` 遮蔽回归） |
| ✅ v4.4.44 | `TestPeersNotify_PubKeySubstitution_Rejected`（`discovery_notify_poison_test.go`）+ 既有 `TestPeersNotify_FetchFail_Rejected` / `TestPeersNotify_ForgedSignature_Rejected` | 取 key 失败与「权威 key 可达但与签名 key 不同（payload 公钥替换攻击）」均 401 fail-closed，攻击者不注册/不进入信任池 |
| ✅ v4.4.44 | `TestHandleSeedRegisterFailClosedNoSecret` / `TestHandleSeedRegisterWrongSecret`（`network_seed_test.go`，常量时间比较 `subtle.ConstantTimeCompare`） | 无 `seed_secret` 拒绝注册（403）；错误密钥（含近邻密钥）拒绝（401） |
| v4.4.44 | `TestResetToken_ConstantTimeCompare` | 重置令牌常量时间比较（在 P0 重置码用例上补断言） |
| v4.4.44 | `TestCors_ConditionalCredentials` | CORS `credentials` 按来源条件设置 |
| v4.4.44 | `TestEscapeJS_Correctness` | `escapeJS` 转义覆盖引号/斜杠/换行 |

### 2.11 精确运行命令（正则匹配真实测试名）

```bash
# v4.2.1 认证/重置/并发 + 文件权限（security_p0）
go test -run 'TestP0_1_|TestP0_2_|TestP0_3_|TestP0_4_|TestP0_5_|TestP0_6_|TestP0_Integration_' -v

# v4.4.44 安全中危项 + 数据完整性（security_medium）
go test -run 'TestSA09_|TestSA10_|TestSA11_|TestSA14_|TestSA15_|TestSA17_|TestSA12_' -v

# 连接计数 / guest 隔离（v4.3.0）
go test -run 'TestConnTracker_' -v

# 消费者密钥哈希（v4.3.2 面）
go test -run 'TestConsumerSecurity_|TestHashAPIKey_' -v

# 中继绕过 + X-OMP-KeyType strip + relayAuthMiddleware（v4.3.6/v4.4.44）
go test -run 'TestRelayToSelf_AttackClosed_ThroughMux|TestRelayAuthMiddleware|TestRelayToLocal_PathWhitelist|TestWithProxyAuth_RelayDispatched_NoAnonymousAdmin|TestLocalOnly_RelayDispatched_Rejected|TestRequestKeyType_IgnoresWireHeader|TestStripInternalHeadersMiddleware|TestRelayToRemote_StripsKeyTypeHeader' -v

# 中继转发签名（v4.4.44 关联）
go test -run 'TestRelayForwardAuth_|TestGatewayForwardToRemoteSignsRequest|TestRelayToRemoteSignsRequest' -v

# 自更新（v4.2.3 P0-1 / v4.2.4 P2-Update / v4.4.44）
go test -run 'TestAtomicReplace|TestDownloadFile|TestReconcilePending|TestQASignatureRoundTrip|TestQARouteSignal|TestQARouteUpdate' -v

# CORS / 私网 IP / XFF / direct probe（v4.2.3 P2-2/P2-3 / v4.4.44）
go test -run 'TestIsOriginAllowed|TestIsLocalOrPrivateIP|TestCorsMiddleware_|TestHB4_IsPrivateIPv4_|TestHB10_IsLocalOrPrivateIP_|TestIsPrivateIPv4|TestClientIPs_XFFGatedByTrustedProxy|TestHandleDirectProbe_RejectsPrivateTarget' -v

# WAF（v4.2.4 P2-WAF）
go test -run 'TestWAF' -v

# provider config import 合并 + 死锁守卫（v4.4.44）
go test -run 'TestImportConfig_ShareToPoolMerge' -v

# 联邦受保护路径 / trust-pool（v4.2.3/v4.4.44 关联）
go test -run 'TestFederationAuth_|TestValidateGuestKey_|TestClassifyKey_|TestGenerateGuestKey_Format' -v

# 中继 hop 逻辑（v4.3.4/v4.3.6）
go test -run 'TestRelayHopCountValidation|TestRelayKeyRouting|TestRelayHeaderConstants' -v

# 数据完整性（v4.4.44 关联）
go test -run 'TestDataIntegrity_' -v

# 传输加密（v4.2.4 P2-Transport 字段层；ECDH 用例待补）
go test -run 'TestEncryptor_|TestEncryptField_|TestDecryptAPIKey|TestNewEncryptor_' -v

# 全量（含本批次未覆盖文件，已有回归一并跑）：
go test -race -count=1 -timeout 25m ./...
```

> 注：本仓库在 Windows 沙箱中 `go test ./...` 会有少量与文件权限/日志锁相关的预存失败（如 `TestP0_4_*`、`TestP0_6_LoggerConcurrentRotation`），属环境限制，与本次改动无关；Linux CI 全绿。

---

## 3. 集成 / QA 手册（手动步骤 + 失败判据）

本节为 v4.4.44 重点回归的手工验证。前端（v4.3.8/4.3.9）与脚本（restart.sh/install.sh）项亦在此节。

### 3.1 relay-to-self 认证绕过（P0）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.1.1 | 以普通/匿名公网请求向本节点 `POST /network/{id}/v1/chat/completions`（经 mux，伪造本地回环派发） | 401/403；`relayAuthMiddleware` 介入，匿名 admin 兜底不可达 |
| 3.1.2 | 经合法 relay 路径派发，请求 `handleRelayToLocal` 转发到白名单外路径（如 `/admin/...`） | 拒绝；仅 `/v1/*`、`/api/network/heartbeat/ping` 可本地派发 |
| 3.1.3 | 中继派发请求检查服务端日志 | 该请求 `RemoteAddr` 保留原始客户端地址，标记为 untrusted，`localOnly` 守卫不误放行 |

**失败判据**：3.1.1 返回 200 即 P0 绕过未关闭；3.1.2 非白名单路径被派发即 `handleRelayToLocal` 白名单失效。

### 3.2 X-OMP-KeyType 伪造（P0）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.2.1 | 请求头带 `X-OMP-KeyType: admin`，访问受 key-type 路由的端点 | 服务端 `RequestKeyType` 取值为 `unknown`（fail-closed），不提升为 admin |
| 3.2.2 | 经本节点中继转发到下游，抓下游收到的请求头 | 下游**不再**收到 `X-OMP-KeyType`（两处 Director 已 strip）；key type 由下游已验证 token 派生 |

**失败判据**：3.2.1 命中 admin 路径或 3.2.2 下游仍见 `X-OMP-KeyType` → 伪造未修复（P1-1 历史漏洞复发）。

### 3.3 联邦 trust-pool 投毒（P0）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.3.1 | 向 `POST /api/network/peers/notify` 发送 payload，其中 `pub_key` 为攻击者控制，并构造使服务端 key 获取失败的场景 | 验签/取 key 失败时**拒绝**注册，不回退到攻击者 `PubKey` |
| 3.3.2 | 检查信任池 | 攻击者节点未写入信任池；日志无「fallback to payload pubkey」类记录 |

**失败判据**：攻击者节点进入信任池 → 投毒未 fail-closed。

### 3.4 seed 注册（P0）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.4.1 | `config.json` 不配 `seed_secret`，尝试 seed 注册接口 | 拒绝（fail-closed），不静默放行 |
| 3.4.2 | 配错误 `seed_secret` 重试 | 常量时间比较拒绝，响应时长无显著可观测差异（防时序侧信道） |

**失败判据**：3.4.1 返回 200 → seed 注册未 fail-closed。

### 3.5 更新管道 fail-closed（P0）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.5.1 | 指向非 canonical（mirror/伪造）GitHub 源触发自检更新 | 拒绝采用；checksum/签名仅从 canonical GitHub 取 |
| 3.5.2 | 人为制造 checksum 不匹配或签名无效 | 下载文件被删除、更新中止（不再 warn-and-continue） |

**失败判据**：3.5.2 仍完成更新 → fail-closed 未生效。

### 3.6 共享网络开关（v4.3.8 P0 / v4.3.9 回归，前端）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.6.1 | 浏览器打开 `/admin.html`，F12 控制台查 `admin-network.js` 是否报 SyntaxError | 无 SyntaxError；`toggleNetworkEnabled`/`toggleShareToPool`/`saveRelayToggle` 均定义 |
| 3.6.2 | 查看「加入共享网络」「共享剩余额度」「开启中继」三开关 | 三者均可见、可点击、状态与服务端一致（v4.3.9 从 `/api/network/status` 派生 `_networkMode`） |
| 3.6.3 | 服务端已启用 relay，刷新页面 | 「开启中继」初始即显示 ON（v4.3.9 `loadFederationConfig` 恢复） |

**失败判据**：3.6.1 控制台报 SyntaxError，或 3.6.2/3.6.3 任一开关无响应/状态错误 → 前端回归。

### 3.7 安装/重启脚本（v4.4.44）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.7.1 | 篡改 `install.sh` 下载产物 checksum，运行安装 | 校验失败即中止（fail-closed） |
| 3.7.2 | 以非属主/越权权限放置 `restart.sh` 并触发重启 | `restart.sh` 权限/属主检查拒绝执行 |

**失败判据**：3.7.1 仍完成安装，或 3.7.2 越权重启成功 → 脚本加固失效。

### 3.8 其它 v4.4.44 回归（手动/代码审查）

- **ReadHeaderTimeout 10s**：压测下发送极慢 header，服务端 10s 内断开（代码审查 `http.Server.ReadHeaderTimeout`）。
- **goroutine dump 开关**：未开启 debug 开关时 goroutine dump 不可达。
- **escapeJS**：渲染含 `'"<\>` 的提供商名/备注，确认无 JS 注入（前端审查）。
- **CSV 公式注入**：导出贡献 CSV，确认单元格以 `'`/空格/tab 前缀中和 `= + - @`。
- **XFF**：从非 `OMP_TRUSTED_PROXY` 来源带 `X-Forwarded-For` 的请求，服务端取到的客户端 IP 为直连 IP 而非伪造值。

---

## 4. 质量门禁（CI）

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -l .`（仅改动 Go 文件） | 无 diff 残留 |
| 构建 | `go build ./...` | 0 error |
| 静态 | `go vet ./...` | 0 新增 warning |
| 特性单测（本批次） | 见 §2.11 各 `go test -run ...` 命令 | 全部 PASS |
| 全量（含 race） | `go test -race -count=1 -timeout 25m ./...` | 通过（Windows 沙箱 3 个预存权限/锁失败为预期；Linux CI 全绿） |

> CI 门禁形态参考 v4.2.3 `P0-CI`（`build-gate` 运行 `go build ./...` + `go vet ./...` 为硬门禁）与 v4.3.32（统一为 `go test -race -count=1 -timeout 25m`，禁用缓存、加 `-race`）。

---

## 5. 一致性复核（IS_PASS）

最终人工/自动复核清单（逐条可勾选）：

**认证与匿名 admin（v4.2.1）**
- [ ] `auth.save()/saveLocked()` 用 `deepCopyDataLocked()`，加密不改写原内存明文（C1）
- [ ] provider 写文件走 `atomicWriteFile`（C2）
- [ ] 匿名 admin 仅 localhost/私网可达，公网不可达（C3）

**自更新与传输（v4.2.3 / v4.2.4）**
- [ ] 自更新校验 SHA-256 checksum，不匹配即中止（P0-1）
- [ ] `FilterByAccessControl()` 未知 key type 走 fail-closed（P1-1）
- [ ] CORS 用 `isOriginAllowed()`，非 `*` 通配（P2-2）
- [ ] provider BaseURL 私网/环回被拦截（P2-3）
- [ ] 传输加密为 X25519 ECDH + HKDF-SHA256（P2-Transport）
- [ ] 自更新 Ed25519 签名校验，失败 fail-closed（P2-Update）
- [ ] WAF 含 40+ 攻击模式并生效（P2-WAF）

**竞态 / 超时 / 泄露 / TOCTOU（v4.3.0 – v4.3.6）**
- [ ] `conn_tracker` `IncrProviderConn`/`IncrGuestConn` 用 `LoadOrStore`+`atomic`（无竞态，靠 `-race` 守护）
- [ ] 出站请求均带超时（v4.3.1）
- [ ] `handleConsumerRegister` 响应不含完整 `Consumer`/`APIKey`（v4.3.2）
- [ ] `vmess.go` 直接 `atomicWriteFile`，无双写窗口（v4.3.3）
- [ ] hopCount 非数字返回 400（v4.3.4）
- [ ] `handleGatewayRequest` 无 `:=` 遮蔽，中继防环有效（v4.3.6 P0）

**前端（v4.3.8 / v4.3.9）**
- [ ] `admin-network.js` 无 SyntaxError，三开关可点击
- [ ] `_networkMode` 由 `/api/network/status` 派生，中继开关初始状态正确

**v4.4.44 最大安全 pass**
- [ ] `/network/{id}/...` 受 `relayAuthMiddleware` 守护；relay-to-self 绕过关闭
- [ ] `handleRelayToLocal` 仅白名单路径派发，保留原始 RemoteAddr 标记 untrusted
- [ ] 最早中间件 + 两 relay Director 均 strip `X-OMP-KeyType`；`RequestKeyType` 从已验证 token 派生
- [ ] `peers/notify` 取 key 失败 fail-closed，不回退攻击者 PubKey
- [ ] seed 注册无 `seed_secret` fail-closed；密钥常量时间比较
- [ ] 更新管道 checksum/签名仅从 canonical GitHub 取，缺失/不匹配即中止
- [ ] provider config import 用 `validateProviderBaseURL` 校验、合并而非截断、无死锁
- [ ] 贡献 CSV 公式注入中和
- [ ] `X-Forwarded-For` 受 `OMP_TRUSTED_PROXY` 限制
- [ ] heartbeat 默认 deny
- [ ] `ReadHeaderTimeout: 10s`
- [ ] 重置令牌常量时间比较
- [ ] CORS `credentials` 按来源条件设置
- [ ] SSRF 私网 CIDR 已扩展
- [ ] goroutine dump 受 debug 开关限制
- [ ] `restart.sh` 权限/属主检查；`install.sh` checksum fail-closed
- [ ] `handleDirectProbe` URL 校验（SSRF）
- [ ] 通用错误消息；`escapeJS` 转义正确

**门禁与版本**
- [ ] `go build ./...` / `go vet ./...` / `go test -race -count=1 -timeout 25m ./...` 通过
- [ ] `main.go` `AppVersion` 反映 v4.4.44；脚本兜底版本一致
- [ ] 无 `TODO` / 占位 / `pass` / `...` 遗留；CHANGELOG 与实现一致（含 P1-1 strip 历史记录已更正）
