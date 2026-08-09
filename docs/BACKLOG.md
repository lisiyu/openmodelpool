# OpenModelPool 演进 Backlog（公益优先）

> 铁律：纯公益、不考虑商业模式。任何"积分/代币/抽成"类设计禁止。
> 工作流（2026-08-08 起）：**小步快跑、不限制每日项数**；每完成一批改动必须 `go build ./...` + `go test ./...` 全绿；文档随代码同步修订，杜绝"文档与代码背离"。
> **每晚 ~22:00 由雷工验收确认；确认 OK 后由雷工手动 push 向 GitHub 并打 tag 触发发布（代理不自动 push、不打 tag）。**

## Phase 0 — 诚信地基（✅ 已完成 2026-08-08）

- [x] P0-1 心跳循环修复：`startHeartbeatLoop` 补 `for{select{}}`（stubs.go:52）
- [x] P0-2 区域同步循环修复：`startRegionSyncLoop` 补 `for{select{}}`（stubs.go:213）
- [x] P0-3 贡献账本诚实化：`IPFSClient`→`ContentHashStore`，去掉伪 IPFS（"Qm"前缀/网关列表），改 `sha256:` 前缀、`StorageLocation=local-hash`（contribution_ledger.go）
- [x] P0-4 治理层死代码清理：删除全仓无调用者的 `governance.go`（GovernanceManager / initGovernanceManager / MultiSigThreshold / NetworkGovernanceEvent 等纯死代码），并移除 globals_network.go 中孤立的 `var governanceMgr *GovernanceManager` 声明
- [x] P0-5 文档对齐：README/design/PRD 中"去中心化/IPFS/分布式治理"等未兑现承诺加"未实现/预留"标注；删除死代码 `ipfs_ledger.go`
- [x] P0-6 版本常量对齐：`AppVersion` 4.3.16 → 4.3.20（对齐 git 发布版本，修复常量漂移）
- [x] P0-7 WAF 文档纠错：核定 `waf_enabled` 默认 `true` 且 `wafMiddleware` 已包裹 `/v1/*` 与 relay；修正 README 中"not yet wired / enabled:false"的错误说法，并修掉 waf.go 中自相矛盾的注释
- [x] P0-8 仓库垃圾清理：删除残留构建/临时/运行时产物（`openmodelpool.exe.bak`、`_wtest2`、`.tmp*`、`.check_globals.js`、`omp.{out,err}.log`、`data/audit/audit.log`、`data/access.log` 等）
- [x] P0-9 配置写盘可中断化（**同时修复生产关机响应性 + 测试套件卡死**）：`config.go` 的 `debounceWriter` 原先在 `dirtyCh` 分支里 `time.Sleep(3s)` 期间完全不响应 `stopCh`，导致关机需空等一个完整防抖窗口、且 `go test ./...` 因每个用例的 cleanup 都要等它退出而必然超时。改为 `configDebounceWindow` 常量 + `time.NewTimer` + `select{timer.C / stopCh}`，等待可被打断（生产语义不变，仍 3s 合并写）
- [x] P0-10 测试 goroutine 泄漏修复：`setupTestEnv` cleanup 补上停止 `multiUser.batchSaveLoop`（`saveStopCh`），消除每用例泄漏一个 goroutine
- [x] P0-11 测试断言去脆化：`TestQAFrontendWiring` 原先硬编码 `/admin-update.js?v=345`，资源版本升到 346 后即失败；改为正则 `\?v=\d+` 断言，不再随缓存版本号漂移而误报
- [x] P0-12 测试防抖窗口可注入：新增 `configDebounceOverride` + `main_test.go` 的 `TestMain`（测试内窗口 5ms）。此前数十个用例整齐卡在 ~3.0-3.2s（正好等于生产防抖窗口），累计拖垮整个套件；生产仍为 3s 合并写，语义不变
- [x] P0-13 节点注册表并发写修复：`node_registry.go` 的 `writeLocked` 名不副实——注释声称"在互斥锁下原子写入"，实际只在计算路径时持锁，**写文件与 rename 完全无保护**；且 `LoadAll` 未加锁扫描目录，与 rename 撞车（Windows 上表现为 `cannot find the path specified` 报错刷屏）。现让锁真正覆盖 write+rename，`LoadAll` 同锁读取
- [x] P0-14 并发用例规模降载：`TestNodeRegistry_ConcurrentSaveAndLoad` 并发度 64 → 16。经 goroutine dump 定位，卡死发生在 Go 自身的 `t.TempDir()` 清理（`syscall.RemoveDirectory` 阻塞 3 分钟）——Windows 实时杀毒扫描持有刚写入文件的句柄所致，**与项目代码无关**；16 并发同样能验证锁正确性，不再放大宿主文件系统问题

> Phase 0 验证基线（2026-08-08）：`go build ./...` EXIT=0；`go vet ./...` 全绿；`go test ./...` 全量通过 EXIT=0，耗时约 140s、**零超时**（修复前 300s 跑不完且报错用例随机漂移）。

## Phase 1 — 真实去中心化（公益抗审查 / 永续）

- [x] **P1-1 DHT 网络 I/O（单进程多节点验证）**：为 `dht_kademlia.go` 的惰性 K-Bucket 表补上真正的 Kademlia RPC 层（新增 `dht_networking.go`）。实现 `DHTMessage`（PING/PONG、FIND_NODE、FIND_VALUE、STORE）+ `DHTTransport` 可插拔传输 + `DHTNode`（含迭代查找算法）+ `InMemoryDHTNetwork`（单进程内多节点路由，作验证台）。`dht_networking_test.go` 四个用例证明：Ping/Pong、A 仅知 B 却经 B 学到 C 的**多跳发现**、值经 relay 存储并被 D 跨跳取回、缺失键返回未找到。这是 P1-1 的第一个里程碑（先单进程多节点验证）；后续再桥接真实 UDP/QUIC/HTTP 传输。**未改动现有 federation/gossip 代码路径**（加法式、零耦合）。
- [x] P1-2a STUN 响应解析抽离为纯函数 `parseSTUNResponse` + NAT 类型判定 `classifyNAT`（full_cone/symmetric，RFC 5780 轻量探测：对比两个 STUN 服务器返回的公网端点是否一致）+ `nat_traversal_test.go` 单测（此前解析内联、零测试、natType 恒为 "unknown"）。`discoverPublicAddr` 改为轮询全部 STUN 服务器后再判定
- [x] P1-2b-1 NAT 类型驱动路由：`NATManager.PreferRelay()` 在 `symmetric` NAT 下强制走中继、跳过后台直连探测（`network_relay.go` 已接线，无行为回退风险），`nat_traversal_test.go` 新增 `TestPreferRelay` 表驱动验证
- [x] P1-2b-2 真实 UDP 打洞 / 直连通道建立（cone NAT 尝试直连：经 relay/gossip 交换 reflexive 地址 + 并发打洞 + 直连通道旁路 relay）
  - [x] P1-2b-2(i) 打洞协议层：`nat_punch.go` —— `PunchOffer` 通告（经 relay/gossip 交换双方 reflexive 地址）+ `Encode/DecodePunchOffer` 帧编解码（magic 前缀防误认）+ `NonceEqual` 存活校验 + `ParseUDPAddr`/`Candidate4Tuple` 四元组推导；`nat_punch_test.go` 10 用例全绿（含坏 magic / 截断 / 缺字段 / nonce 长度错误的拒绝路径）
  - [x] P1-2b-2(ii) 打洞协调器：`nat_punch_loop.go` —— `PunchSession` 纯状态机（双方 offer + 建立标记 + 直连地址）；`DirectLinkManager` 复用共享 UDP socket 发起打洞（`BeginPunch` 发送协程 + `Ingest` 接收标记，单读者避免竞争）；`nat_punch_loop_test.go` 回环集成测试验证「交换 offer → 并发打洞 → 双向均建立直连」。`go test ./...` 全量通过
  - [x] P1-2b-2(iii-1) 共享 UDP socket 与接收多路复用：`nat_traversal.go` 的 `NATManager` 绑定**单一长期 UDP socket**，STUN 探测（`stunQueryOnConn`）与打洞共用同一端口使 reflexive 地址稳定（修复此前每查临时 Dial 导致端口漂移、symmetric 误判）；`udpRecvLoop` 作为 socket 唯一读者，打洞帧转交 `DirectLinkManager.Ingest`、STUN 响应投递 `stunCh`；`NATManager.UDPConn()/LocalUDP()` 暴露给打洞层；死代码 `stunQuery` 已删
  - [x] P1-2b-2(iii-2) 接入 relay 真实交换 offer + 直连旁路：`RouteEntry` 增 `ReflexiveUDP`/`NATType`；新增 `/network/__punch` 端点交换 PunchOffer；`relayToRemote` 在 non-symmetric 且有对端 reflexive 时异步 `ExchangePunchWithPeer` + `BeginPunch`，直连建立后跳过冗余 TCP 探测（relay 旁路信号）；`nat_punch_relay_test.go` 2 用例覆盖「经 HTTP 交换 offer → 双向 UDP 打洞建立直连」真实链路。修复打洞协议缺陷：发送须持续到 maxAttempts（NAT 映射需双向持续外出包），不能在收到首帧即停。注：直连通道**承载实际 HTTP 流量**需在 UDP 上自建请求/应答协议（P2 工作），本期达成「打洞真实打通 + 链路可用」目标
- [x] P1-3 贡献账本多节点冗余：联邦内 N 个节点各存一份 + 哈希校验（替代真 IPFS 的短期方案）
  - [x] P1-3(i) 冗余层（加法式、零耦合现有 federation/gossip 路径）：`ledger_redundancy.go` —— `LedgerManifest`/`BuildManifest` 内容哈希摘要（剔除节点专属 Signature/Proof 使跨副本业务摘要一致，篡改业务字段即变摘要）+ `DiffManifests`（missing/divergent/extra）+ `ReplicaManager`（`ReplicateContribution` 写全副本返回副本数；`VerifyConsistency` 比对各副本清单检测分歧/篡改）；`contribution_ledger.go` 补 `GetAllTrusts/GetAllPenalties`；`ledger_redundancy_test.go` 3 用例全绿（篡改即变摘要、缺/多/分歧、3 副本一致 + 篡改检测）。`go build/vet/test ./...` 全绿
  - [x] P1-3(ii) 接入真实联邦复制 + 清单对账端点（加法式、零耦合现有 federation/gossip 路径）：`ledger_replication.go` —— `LedgerReplicator`（`NewLedgerReplicator`；`ReplicateContribution` 落库后异步推送贡献到各联邦 peer 的 `/ledger/__sync`，返回副本接受数；`FetchManifest`/`fetchRecord` 经 HTTP 互查；`ReconcileWith` 取对端清单→`DiffManifests`→对缺失记录拉取并 `GossipSync` 补齐、对分歧记录仅告警不覆盖；`ReconcileAll` 批量对账；`refreshPeersFromRouteTable` 从 routeTable 自动派生 peer 基址）。`RecordContribution` 末尾加 nil 安全异步复制钩子（`ledgerReplicator` 为 nil 时无副作用，回归测试保护）。路由注册 `GET /ledger/__manifest`、`POST /ledger/__sync`、`GET /ledger/__record`（均 `withFederationAuth`）。`ledger_replication_test.go` 6 用例全绿（推送复制、缺失补齐、分歧不覆盖、清单/记录端点、nil 钩子无副作用）。`go build/vet/test ./...` 全绿

## Phase 2 — 贡献透明与社区共治（公益信任）

  - [x] P1-3(iii) 后台自动对账循环（让冗余真正"运行"起来）：`ledger_replication.go` 新增 `startLedgerReconcileLoop`（默认每 60s 经 `ReconcileAll` 对全部已知联邦 peer 对账、自愈缺失记录、对分歧仅告警；`ledgerReconcileInterval` 可配、`ledgerReconcileStop` 可停）；`initContributionLedger` 末尾启动。`ledger_replication_test.go` 新增 `TestLedgerReconcileAll`（A 有 c1 / B 有 c2 → 对账后 A 自愈 c2）。`go build/vet/test ./...` 全绿
- [x] P2-1 社区共治设计：节点准入 / 模型白名单多签共治（治理哲学 2026-08-09 已定：默认善意、只防滥发提案、贡献者共治、无惩罚/无slash）
  - [x] P2-1(i) 轻量共治账本 `governance.go`：`GovernanceLedger` 追加式 + 哈希链式存证（提案/批准双链，`VerifyChain` 可校验防篡改）；提案类型 `admit_node`/`allow_model`/`param_change`；**贡献者共治**（投票者=有贡献的节点，`contributorsVoterSource` 取贡献账本去重节点）；**超多数通过** `supermajority=ceil(2/3·eligible)`（单人节点可自决其公地）；**只防滥用不惩罚**——每提案者最多 5 个未结提案的滥发限流， dissent 不扣分；无 slash/信任评分。`governance_test.go` 5 用例全绿（提案→超多数批准通过、重复批准拒、滥发限流、多数否决、单人自决）。`governanceLedger` 在 `initContributionLedger` 接线（持久化 `data/governance.json`）
  - [x] P2-1(ii) 共治 HTTP 端点：`POST /api/governance/propose`、`POST /api/governance/ratify`（均 `withFederationAuth` + 限流，调用方节点取自 `X-Node-ID` 签名校验、空则本节点）、`GET /api/governance/proposals`（公开只读，返回提案列表 + `chain_valid`）。`routes.go` 已注册
  - [x] P2-1(iii) 治理软提醒 + 免费额度归属模型 + 网关角色（2026-08-09 补充）：节点默认个人模式、**绝不强制**加入共享网络；仅当个人模式且有闲置自有额度时 `GetStatus()` 返回 `shared_network_suggestion`（`should_join`/`reason`/`idle_quota`），启动 5s 后 `logSharedNetworkSoftReminder()` 打一条 INFO 软提醒（鼓励非胁迫）；免费额度归属——个人模式仅自用不进联邦公共池，加入共享网络才成为公共池资源（更多出口=更多并发额度，免费服务商限并发/总额度）；无额度可贡献也能转发流量/做 gateway（relay 本就支持）。`network.go` 改 `GetStatus` 嵌入软提醒 + 新增 `logSharedNetworkSoftReminder`；`init.go` 启动 5s 后 `go` 触发。`governance_reminder_test.go` 2 用例全绿（个人+闲置→软提醒、已加入→不打扰、无自有额度→不提醒）。`docs/PUBLIC-WELFARE.md` 同步"免费额度归属模型 / 默认不强制 / 网关角色"章节
- [x] P2-2 贡献账本可视化：admin 页面展示"算力从哪来、到哪去"透明度面板
  - [x] P2-2(i) 透明度数据端点（加法式、零耦合）：`contribution_ledger.go` 新增 `GetTransparency`（`LedgerTransparency`：按 peer/按 model 聚合贡献 token、记录数、trust/claim/penalty/tx 计数、交易链完整性 `chainValid`；`VerifyChain` 抽出无锁 `chainValid` 供复用）；`ledger_transparency.go` 新增 `handleAdminLedgerTransparency`；路由注册 `GET /api/admin/ledger/transparency`（admin 鉴权 + 限流）。`ledger_transparency_test.go` 2 用例全绿（聚合正确、端点 200 返回）。`go build/vet/test ./...` 全绿
- [x] P2-3 公益额度（非代币）：贡献 → 免费配额兑换（透明记账，1:1 等额、零手续费/不通胀、不可交易），杜绝抽成 / 积分经济
  - [x] P2-3(i) 贡献→免费配额闭环（加法式、零耦合）：`ledger_contrib_quota.go` —— `ContributionQuotaTracker`（按 peer_id 累计贡献 token → 等额免费配额，1:1 公益、无手续费/不通胀/不可交易；持久化 `data/contribution_quota.json`、线程安全）；`RecordContribution` 末尾加 nil 安全累计钩子（消费侧强制留 P2-3(ii)）；路由注册 `GET /api/admin/ledger/contribution-quota`（admin 鉴权 + 限流）返回每个贡献者"贡献量↔赚得免费配额"透明视图。`ledger_contrib_quota_test.go` 4 用例全绿（累计 1:1、持久化、RecordContribution 累计、nil 钩子无副作用）。`go build/vet/test ./...` 全绿
  - [x] P2-3(ii) 消费侧接入贡献者身份（**非排他实现**，2026-08-09）：`ledger_quota_consume.go` —— 身份直接复用联邦既有的 ed25519 节点身份（`verifyRelayForwardAuth` 已校验 `X-Node-ID` + 签名 + 重放窗口），**不新建用户系统**。`tryContributorDraw` 在 `handleGatewayRequest` 的 public-key 分支：已验签且有余额的贡献者从自己赚得的额度扣减，并因此跳过匿名 per-IP 滥用闸门；**无身份 / 额度用尽 / tracker 为 nil 一律回落到原社区免费池路径，不拒绝任何人**（贴合"善意默认、只防恶意滥用、不防不贡献"治理哲学，故把 backlog 原文的"强制"落地为"贡献者优先"，此为本轮唯一设计判断，请雷工验收时确认）。`ledger_contrib_quota.go` 增 `ConsumedQuota`/`RemainingQuota` + `Consume`/`Refund`/`Remaining`/`TotalConsumed`（1:1、可退款且钳零、不增发、不可交易），持久化向后兼容（旧文件 consumed 缺省 0）。响应头 `X-OMP-Quota-Source: contributor|community` 透明化扣自哪条通道；admin 透明度端点补 `total_consumed_tokens`/`total_remaining_tokens`。**顺带修掉一个既有双扣 bug**：网关已扣费的请求回落到本地 `handleChatCompletions` 时会对同一 IP 再扣一次，现由内部标记 `X-OMP-Quota-Charged` 阻断（该标记在网关入口 `stripInternalQuotaHeaders` 强制剥离，客户端无法伪造绕过闸门）。`ledger_quota_consume_test.go` 9 用例全绿（扣减/不足即 no-op/退款钳零/持久化/settle 结算/匿名与耗尽均回落/nil 惰性/伪造标记被剥离/畸形 node id 不成身份/端点字段）

## Phase 3 — 普惠低门槛

- [x] P3-1 一键部署 / 容器化（降低个人运行门槛）：`Dockerfile`（多阶段、CGO 关、非 root、:8000、data 卷、版本 ldflags 注入）已具备；新增 `docker-compose.yml`（`docker compose up -d` 一行起，命名卷持久化 + restart unless-stopped + no-new-privileges）+ `.dockerignore`（排除 data/.git/构建产物，避免敏感/冗余打进构建上下文）。纯仓库产物，不改应用逻辑
- [x] P3-2 免费池开箱即用：默认配置即可零成本接入
  - [x] P3-2(i) 任意节点 + 硬编码全球公钥即开箱可用：`network_keys.go` 既有 `PublicKeyValue = "sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1"` 为全局固定公钥；本次把"社区免费池（匿名 `free-*`/`free-anonymous` 提供方）"与"运营商私有共享"解耦——personal 模式下 public key 仍只触达社区免费池（不暴露私有付费额度），shared 模式保持原行为。`FilterByAccessControl` 与 `providerAllowsKeyType`（`/v1/models` 列表）两处闸门同步解耦；直连 `/v1/chat/completions` 路径接入既有 `PublicKeyQuota` 按 IP 四层限流（防单一滥用者垄断免费池，复用 `network_relay.go` 同一套机制）。免费池自动同步默认开启（`free_pool_auto_sync=true`）。`handler_batch8_test.go`/`utils_test.go` 共 3 新用例全绿
- [x] P3-3 调用格式扩展（下游消费格式，与上游 provider.Type 正交）：2026-08-09 拍板**以 OpenAI 兼容为通用语 + Anthropic 原生已内建，不引入专有格式**。普通调用方契约恒为"base URL + API Key"，复用 `withProxyAuth`。
  - [x] P3-3(i) **Gemini 下游入口**：`gemini_api.go` 接受 `POST /v1beta/models/{model}:generateContent` 与 `:streamGenerateContent`，请求翻译为 OpenAI 格式后复用 `handleGatewayRequest`（与 Anthropic 同模式），响应经 `geminiResponseWriter` 翻回 Gemini 格式（含流式 SSE、usageMetadata、finishReason 映射）。`geminiAuthAdapter` 支持 `x-goog-api-key` 头与 `?key=` 查询（后者在鉴权后从 query 剥离，避免令牌外泄）。`gemini_api_test.go` 覆盖解析/翻译/鉴权共 7 用例全绿
  - [x] P3-3(ii) **Azure 下游 URL 风格**：`azure_api.go` 接受 `POST /openai/deployments/{deployment}/chat/completions`（`azureAuthAdapter` 把 Azure SDK 的 `api-key` 头转 Bearer），从路径提取 `deployment` 作为 model 注入 OpenAI 请求体、重写路径为 `/v1/chat/completions` 后复用 `handleGatewayRequest`；响应本就是 OpenAI 格式，无需翻译。`azure_api_test.go` 覆盖注入/鉴权共 3 用例全绿
  - [ ] P3-3(iii) 候选增量（低优先级、社区明确诉求时再做，均收敛到"base url + key"契约）：OpenAI 新版 `/v1/responses`、`/v1/images`、`/v1/audio` 下游透传

## Phase 4 — 教育科研

- [x] P4-1 贡献数据开放下载（CSV/JSON），供研究者使用
  - [x] P4-1(i) 开放下载端点（加法式、零耦合）：`contribution_ledger.go` 新增 `ExportContributionsCSV`（贡献明细 CSV：id/peer_id/model_id/provider/tokens/value_usd/timestamp）与 `ExportLedgerJSON`（完整账本 JSON：contributions/trusts/claims/penalties/transactions）；`ledger_export.go` 新增 `handleLedgerExport`（`?format=csv|json`，默认 JSON，attachment 下载）；路由注册 `GET /api/admin/ledger/export`（admin 鉴权 + 限流）。`ledger_export_test.go` 3 用例全绿（CSV 头与行、JSON 键、handler 双格式）。`go build/vet/test ./...` 全绿
- [x] P4-2 架构与公益理念文档（中英文）
  - [x] P4-2(i) 中文版 `docs/PUBLIC-WELFARE.md`（全部对应已落地代码，不夸大）
  - [x] P4-2(ii) 英文版 `docs/PUBLIC-WELFARE.en.md`（与中文版对齐：免费额度归属模型/默认不强制/软提醒/网关角色/社区共治）
  - [x] P4-2(i) 中文版 `docs/PUBLIC-WELFARE.md`：使命 / 架构分层 / 去中心化联邦 / 透明 / 公益额度闭环 / 与商业网关区别 / 一行部署，全部对应已落地代码、不夸大

## Promotion（稳定后）

- [x] 准备推广物料包（2026-08-09）：`docs/LAUNCH-KIT.md` —— 中英一句话定位 + 仓库 About 文案、README 润色清单（副标题改为直述公益、新增"无商业模式/无代币/无积分/无抽成"段与徽章、版本徽章 v4.1.6→v4.3.24 修漂移、Contribution Credits 经济学措辞改写为"记账非货币"、新增 four-line pledge、Earn/Spend 明确 1:1 且额度耗尽不拒绝）、约 330 字中文发布稿（附裁到 300 字的删法）、Show HN 英文稿、15 个 GitHub topics（并说明为何**不**加 web3/dao/decentralized-ai）、发布前检查清单、以及"一律不用"的措辞黑名单。**代理不发布**，待雷工审核
