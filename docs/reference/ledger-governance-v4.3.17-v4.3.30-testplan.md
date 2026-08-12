# OpenModelPool 账本 / 治理 / 公益批次 测试计划（v4.3.17 – v4.3.30）

> 版本范围：v4.3.17 – v4.3.30（账本/治理/公益聚合批次）
> 聚合理由：区间内增量集中于**账本记录侧**（贡献记账 + Ticket 双签名防双花，v4.3.17/18）、**贡献透明与共享治理**（透明度/导出端点、公益配额核算、propose/ratify 双链治理流、共享网络软提醒、消费侧自有额度跳过匿名闸门，v4.3.29），并以 **v4.3.30 修复"重启丢失 consumer 用量"** 收尾。四版本同属"账本可信 + 治理可达 + 公益可核"一条主线，合并回归可避免跨文档遗漏。

---

## 1. 范围与背景

| 版本 | 能力 | 关键变更（代码侧） | 优先级 |
|------|------|--------------------|--------|
| v4.3.17 | 主动探测与交叉验证（账本记录侧） | `ProbeSchedulerLoop()` 周期探测声明能力（按信誉/最近可见自适应间隔）；`CrossVerifyWithQuorum()` 三节点独立验证，>20% 延迟偏差触发调查；`probeSchedule()` 决定间隔；`minVerifiers` 由 2 升至 3 | P1 |
| v4.3.18 | Ticket 双签名防双花（账本记录侧） | `ticket.go` 新增 `UsageTicket`（请求方 + 提供方双签名）与 `TicketFingerprint` 去重；`TicketStore` 签发/会签/双花检测/公证跟踪；`notarizeLoop()` 小时级批量公证；`relay.go:recordContributionToLedger` 每笔贡献签发并会签 ticket | P1 |
| v4.3.29 | P2 贡献透明 & 共享治理 | `ledger_transparency.go` / `ledger_export.go`：贡献来源透明端点 + JSON/CSV 导出；`ledger_contrib_quota.go`：公益配额核算；`governance.go`：propose/ratify 双链治理流；`network.go`+`init.go`：共享网络参与软提醒 `governance_reminder` | P2 |
| v4.3.29 | P2-3(ii) 消费侧自有额度（非排他） | 复用联邦 ed25519 节点身份；已验证贡献者余额充足时用自有额度**跳过匿名 per-IP 闸门**；额度耗尽/匿名/无法验证时**回退社区免费池**（与免费池代码路径完全一致，不增设 toll） | P2 |
| v4.3.30 | 重启不丢 consumer 用量 | `gracefulShutdown` 现调用 `multiUser.StopBatchSave()`；`RecordConsumerUsage` 仅低于批阈值(10)时标记 dirty；`StopBatchSave` 由死代码改为幂等关闭 `saveStopCh` 并等待 `saveDone`（5s 超时）；修复 `TestHB10_MultiUser_RecordConsumerUsage` 偶发清理竞态 | P1 |

---

## 2. 自动化单元测试（离线 / httptest）

> 全部用例名均经 `Grep "func Test"` 在仓库中确认真实存在，未编造。运行命令见各分区末尾，注意正则须精确匹配实际测试名。

### 2.1 账本记录侧 + 共享边界（v4.3.17/18 的账本侧）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `capability_ledger_test.go` | `TestRecordContributionToLedger` | `recordContributionToLedger` 写入贡献记录与交易，且链 `VerifyChain()` 有效 |
| | `TestRecordConsumptionToLedger` | 消费侧 `recordConsumptionToLedger` 写入 `type=consumption` 交易 |
| | `TestRecordContribution_NilLedger` / `TestRecordConsumption_NilLedger` | `contributionLedger == nil` 时调用安全返回不 panic |
| | `TestLedgerBalance` / `TestLedgerTransactions` / `TestLedger_NilLedger_Returns503` | 余额/交易聚合与 nil 守卫 |
| | `TestCheckShareBoundary_AllowWhenNoRestrictions` | 无限制时放行 |
| | `TestCheckShareBoundary_DailyCapExceeded` / `..._DailyCapNotExceeded` | 日贡献上限触发/未触发拦截 |
| | `TestCheckShareBoundary_ModelWhitelist` / `TestUpdateShareBoundary` | 模型白名单与边界配置更新 |

> ⚠️ **此处仅覆盖"记账落盘"，未覆盖 v4.3.18 的 Ticket 双签名防双花**（见 §2.6 缺口）。`TestRecordContributionToLedger` 虽带 `request_id` 调用，但**不断言 ticket 是否签发/双签名/双花被拒**。

```bash
# 账本记录 + 共享边界（10 个用例）
go test ./... -run 'TestRecordContributionToLedger|TestRecordConsumptionToLedger|TestRecordContribution_NilLedger|TestRecordConsumption_NilLedger|TestLedgerBalance|TestLedgerTransactions|TestLedger_NilLedger_Returns503|TestCheckShareBoundary' -v
```

### 2.2 治理双链流 + 死锁修复（v4.3.29）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `governance_test.go` | `TestGovernance_ProposeRatifyPass` | 提案→批准双链：propose 生成 `gov-<self>-<seq>`（链 `PrevHash` 衔接），ratify 达超级多数后提案关闭 |
| | `TestGovernance_DoubleRatifyRejected` | 同一节点对同提案重复 ratify → 拒签（`node already ratified`） |
| | `TestGovernance_SpamGuard` | 超出 `govMaxOpenPerProposer` 开放上限 → 拒绝（防 spam） |
| | `TestGovernance_RejectByMajority` | 多数否决 → 提案关闭为未通过 |
| | `TestGovernance_SingleNodeSelfRatify` | 单节点可自 ratify 自身公益提案（`eligible==0` 退化为 1） |
| `governance_deadlock_test.go` | `TestGovernanceProposeRatify_PersistsWithRealDataPath` | 用真实 `dataPath`：Propose/Ratify 持写锁调 `saveLocked`（非 `save()`），不触发 v4.3.32 的 RWMutex 自死锁；落盘后重载仍可见 |

```bash
# 治理双链 + 死锁（6 个用例）
go test ./... -run 'TestGovernance_|TestGovernanceProposeRatify_PersistsWithRealDataPath' -v
```

### 2.3 算法治理（v4.3.29 治理延伸，同模块）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `algorithm_gov_test.go` | `TestAlgorithmProposeAppearsInProposals` | 算法参数提案进入提案列表 |
| | `TestAlgorithmVoteRecordedAndTallied` | 投票被记录并计入 tally |
| | `TestAlgorithmHistoryReflectsProposeAndVote` | 历史记录反映提案与投票 |
| | `TestAlgorithmStatusReflectsResolve` | 决议后状态正确流转 |
| | `TestAlgorithmGovernancePersistsAcrossReload` | 真实 `dataPath` 重载后治理状态保留 |
| `algorithm_chain_test.go` | `TestDefaultAlgorithmParams` / `TestNewAlgorithmChain` / `TestAlgorithmChain_GetCurrentParams` / `TestAlgorithmChain_UpdateParams` / `TestInitAlgorithmChain` | 算法链默认参数、构造、读写当前参数、初始化 |

```bash
# 算法治理（10 个用例）
go test ./... -run 'TestAlgorithm|TestDefaultAlgorithmParams|TestNewAlgorithmChain|TestInitAlgorithmChain' -v
```

### 2.4 共享网络软提醒（v4.3.29 `network.go`/`init.go`）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `governance_reminder_test.go` | `TestSharedNetworkSoftReminder_PersonalWithIdle` | personal 模式且有闲置容量 → 触发 `governance_reminder` 软提醒（不强制） |
| | `TestSharedNetworkSoftReminder_NoOwnCapacity` | 自身无容量 → 不提醒 |

```bash
# 共享网络软提醒（2 个用例）
go test ./... -run 'TestSharedNetworkSoftReminder' -v
```

### 2.5 透明度 / 导出 / 面板接线 / 公益配额 / 冗余复制（v4.3.29）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `ledger_transparency_test.go` | `TestLedgerTransparency` / `TestLedgerTransparencyHandler` | 贡献来源透明数据正确；`GET /api/admin/ledger/transparency` 端点鉴权+返回 |
| `ledger_export_test.go` | `TestLedgerExportCSV` / `TestLedgerExportJSON` / `TestLedgerExportHandler` | CSV（贡献）/ JSON（全量）导出格式与 `GET /api/admin/ledger/export` 端点 |
| `ledger_panel_wire_test.go` | `TestLedgerPanelWiredInAdminHTML` / `TestLedgerPanelScriptCallsRealEndpoints` / `TestAdminLedgerJSServedFromEmbeddedFS` | 管理面板接线真实端点；JS 来自内嵌 FS |
| `ledger_contrib_quota_test.go` | `TestContributionQuotaAccrue` / `TestContributionQuotaPersistence` | 公益配额累积与落盘 |
| | `TestRecordContributionAccruesQuota` | 记录贡献同步累加公益配额 |
| | `TestRecordContributionQuotaHookNilSafe` | 配额 hook 在 nil 状态下不 panic |
| `ledger_quota_consume_test.go` | `TestContributionQuotaConsume` / `TestContributionQuotaConsumeInsufficientIsNoOp` / `TestContributionQuotaRefund` / `TestContributionQuotaConsumePersistence` | 消费侧额度扣减/不足为 no-op/退款/落盘 |
| | `TestTryContributorDraw` / `TestTryContributorDrawFallsThrough` | 已验证贡献者走自有额度；**匿名或验证失败回退社区免费池（与免费池同路径）** |
| | `TestStripInternalQuotaHeaders` / `TestVerifiedContributorIDRejectsGarbage` / `TestAdminContributionQuotaReportsConsumption` | 内部额度头剥离；非法贡献者 ID 拒绝；管理端点上报消费 |
| `ledger_redundancy_test.go` | `TestBuildManifest` / `TestDiffManifests` / `TestReplicaRedundancy` | 多副本清单构建/差异/冗余 |
| `ledger_replication_test.go` | `TestLedgerReplicationPush` / `TestLedgerReconcileHeal` / `TestLedgerReconcileDivergent` / `TestLedgerManifestHandler` / `TestLedgerRecordHandler` / `TestRecordContributionTriggerNoopWhenReplicatorNil` / `TestLedgerReconcileAll` | 推送/自愈/分歧调和/清单与记录端点/replicator 为 nil 时 no-op/全量调和 |

```bash
# 透明度/导出/面板（8 个用例）
go test ./... -run 'TestLedgerTransparency|TestLedgerExport|TestLedgerPanel' -v

# 公益配额核算 + 消费侧额度（13 个用例）
go test ./... -run 'TestContributionQuota|TestRecordContributionAccruesQuota|TestRecordContributionQuotaHookNilSafe|TestTryContributorDraw|TestStripInternalQuotaHeaders|TestVerifiedContributorIDRejectsGarbage|TestAdminContributionQuotaReportsConsumption' -v

# 冗余/复制（10 个用例）
go test ./... -run 'TestBuildManifest|TestDiffManifests|TestReplicaRedundancy|TestLedgerReplicationPush|TestLedgerReconcile|TestLedgerManifestHandler|TestLedgerRecordHandler|TestRecordContributionTriggerNoopWhenReplicatorNil|TestLedgerReconcileAll' -v
```

### 2.6 multiUser 重启不丢 consumer 用量（v4.3.30）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `multiuser_shutdown_test.go` | `TestMultiUserStopBatchSaveFlushesPendingUsage` | `StopBatchSave` 关闭 `saveStopCh` 后把未落盘用量 flush 到盘 |
| | `TestMultiUserStopBatchSaveWaitsForExit` | 等待 `saveDone`，goroutine 真正退出 |
| | `TestMultiUserStopBatchSaveIdempotent` | `saveOnce.Do` 保证重复/并发调用幂等（不会 double-close panic） |
| | `TestMultiUserStopBatchSaveWithoutLoop` | 直接构造（无后台循环）时安全返回不阻塞 |
| `handler_batch10_test.go` | `TestHB10_MultiUser_RecordConsumerUsage` | `RecordConsumerUsage` 累计 token/请求计数（修复了 `TempDir` 清理竞态） |

```bash
# multiUser 重启不丢用量（5 个用例）
go test ./... -run 'TestMultiUserStopBatchSave|TestHB10_MultiUser_RecordConsumerUsage' -v
```

### 2.7 合并运行命令（覆盖本节全部真实用例）

```bash
go test ./... -run 'TestRecordContributionToLedger|TestRecordConsumptionToLedger|TestRecordContribution_NilLedger|TestRecordConsumption_NilLedger|TestLedgerBalance|TestLedgerTransactions|TestLedger_NilLedger_Returns503|TestCheckShareBoundary|TestGovernance_|TestGovernanceProposeRatify_PersistsWithRealDataPath|TestAlgorithm|TestSharedNetworkSoftReminder|TestLedgerTransparency|TestLedgerExport|TestLedgerPanel|TestContributionQuota|TestRecordContributionAccruesQuota|TestRecordContributionQuotaHookNilSafe|TestTryContributorDraw|TestStripInternalQuotaHeaders|TestVerifiedContributorIDRejectsGarbage|TestAdminContributionQuotaReportsConsumption|TestBuildManifest|TestDiffManifests|TestReplicaRedundancy|TestLedgerReplicationPush|TestLedgerReconcile|TestLedgerManifestHandler|TestLedgerRecordHandler|TestRecordContributionTriggerNoopWhenReplicatorNil|TestLedgerReconcileAll|TestMultiUserStopBatchSave|TestHB10_MultiUser_RecordConsumerUsage' -v
```

> ⚠️ **不要**用 `Ticket`/`Probe`/`Notarize` 作为子串：当前仓库无任何匹配这些关键词的 `*_test.go`（见 §2.8 缺口），会漏跑零用例而非报错，造成"已覆盖"的假象。

### 2.8 测试缺口（建议新增 `TestXxx_...`）

下列关键路径目前**无真实测试覆盖**，须标注并补测：

- **✅ Ticket 双签名防双花（v4.3.18，最大缺口，已于 2026-08-11 补齐）**：`ticket_test.go` 新增 `TestTicketFingerprint_Deterministic` / `TestTicketStore_CountersignAndDoubleSpend` / `TestTicketStore_IsDoubleSpend` / `TestTicketStore_Notarized` / `TestTicketStore_Cleanup`（v4.3.19 泄漏修复）/ `TestAntiCollusionCheck_FlagsDeviation` / `TestAntiCollusionCheck_Empty`。覆盖 `IssueTicket`/`Countersign` 双花检测/`IsDoubleSpend`/`MarkNotarized`/三层 `AntiCollusionCheck` 偏差标记。
  - `recordContributionToLedger` 的签发分支（`relay.go`）仍依赖全局 `node`/`contributionLedger` 与网络环境，未做纯单测；其下游 `TicketStore` 行为现已被上述用例覆盖。
  - 可选追加 `TestRecordContributionToLedger_EmitsTicketWhenRequestIDPresent`（带 `request_id` 时 `ticketStore` 存在则签发）
- **主动探测验证（v4.3.17）**：`ProbeSchedulerLoop`/`CrossVerifyWithQuorum`/`probeSchedule` 无测试。
  - 建议新增 `TestProbeSchedule_IntervalsByReputation`（新节点 5m / 常规 30m / 高信誉 2h / 可疑 1m）
  - `TestCrossVerifyWithQuorum_DetectsLatencyDeviation`（>20% 偏差置 `suspect`）
  - `TestProbeSchedulerLoop_SchedulesPeriodicProbe`
- **公证循环与 TicketStore 初始化（v4.3.18）**：`notarizeLoop`/`initTicketStore`/`Cleanup` 无测试。
  - 建议新增 `TestNotarizeLoop_BatchesHourly` / `TestInitTicketStore_CleanupRemovesExpired`
- **gracefulShutdown 端到端"重启不丢用量"（v4.3.30）**：现有单测覆盖 `StopBatchSave` 四个行为，但 `server.go:gracefulShutdown` 调用 `StopBatchSave` 后"进程退出前用量已落盘、重启后可见"未被端到端断言。
  - 建议新增 `TestGracefulShutdown_FlushesConsumerUsageBeforeExit`（或 `TestMultiUser_RestartPreservesUsage`：启动→`RecordConsumerUsage`→触发 shutdown 路径→重载消费者状态含累计值）

---

## 3. 集成 / QA 手册（手动步骤 + 失败判据）

> 下列步骤为单测之外的端到端验证。环境：两节点 `mode=shared`、`network_enabled=true`、派生 NodeID 且信任池启用。

### 3.1 治理双链审批（propose → ratify）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.1.1 | 节点 A `POST /api/governance/propose {type,title,payload}` | 返回 `gov-<A>-<seq>`，`PrevHash` 衔接上一条提案；`GET /api/governance/proposals` 可见 `status=open` |
| 3.1.2 | 节点 B、C 分别 `POST /api/governance/ratify {proposal_id,approve:true}` | 各自仅能 ratify 一次；重复 ratify → 4xx（"node already ratified"） |
| 3.1.3 | 达超级多数（≥ `supermajority(eligible)`） | 提案 `status` 转为 `approved`/`rejected`；`GET /api/governance/proposals` 反映结果 |
| 3.1.4 | 检查 `VerifyChain()`（管理/调试端点或日志） | 提案链与 ratify 链的 hash 连续、无篡改 |

**失败判据**：A 提案后 B 不可见（P2 失败）；同节点重复 ratify 未拒绝（双签防重放失效）；达多数后状态不流转（recompute 失效）；`VerifyChain` 失败（双链完整性破坏）。

### 3.2 贡献透明度 + 导出端点

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.2.1 | 产生若干贡献后，`GET /api/admin/ledger/transparency` | 200，返回贡献来源（peer/model/provider/tokens）聚合；非 admin → 401/403 |
| 3.2.2 | `GET /api/admin/ledger/export?format=json` | 200，返回全量账本 JSON |
| 3.2.3 | `GET /api/admin/ledger/export?format=csv` | 200，返回贡献 CSV（表头含 peer/model/tokens 等） |
| 3.2.4 | 对照 `ledger_transparency_test.go` 的 `TestLedgerTransparency` 数据形状 | 端点返回值与单测断言结构一致 |

**失败判据**：透明端点未鉴权即返回（SA 回归）；CSV/JSON 字段缺失或与单测不符；导出超时或无响应（限流 `rateLimitByIP(10)` 误伤正常请求需另判）。

### 3.3 公益配额消费侧（P2-3(ii)）：自有额度跳过匿名闸门

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.3.1 | 用**已验证贡献者**身份（联邦 ed25519 节点签名）发请求，且其公益配额余额充足 | 请求走贡献者自有额度，**跳过匿名 per-IP 闸门**；`TestTryContributorDraw` 对应路径命中 |
| 3.3.2 | 同一贡献者余额耗尽后再发请求 | **回退社区免费池**，走与匿名完全相同的免费池代码路径（无新增 toll） |
| 3.3.3 | 用**匿名/无法验证**身份发请求 | 直接进入社区免费池，不经贡献者额度分支 |
| 3.3.4 | 检查 `X-OMP-Quota-Source` 等内部头 | 出站前被 `TestStripInternalQuotaHeaders` 对应的 `StripInternalQuotaHeaders` 剥离，不泄露 |

**失败判据**：余额充足却仍走匿名闸门（跳过逻辑失效）；额度耗尽时**未**回退免费池或走了不同代码路径（违反"与免费池完全一致"）；内部额度头外泄；非法 `X-OMP-Quota-Source` 被 `TestVerifiedContributorIDRejectsGarbage` 对应的校验放过。

### 3.4 共享网络软提醒（governance_reminder）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.4.1 | personal 模式节点且有闲置共享容量，触发 `logSharedNetworkSoftReminder()` | 仅打**软提醒**日志/提示，**不**强制改 `network_enabled`/`share_to_pool` |
| 3.4.2 | 自身无容量时 | 不提醒（对照 `TestSharedNetworkSoftReminder_NoOwnCapacity`） |

**失败判据**：软提醒变成强制行为（改写了共享配置）→ 与"软提醒"语义不符。

### 3.5 v4.3.30 专项：重启不丢 consumer 用量

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.5.1 | 启动节点，以某 consumer 连续发请求产生 token/请求计数（低于批阈值 10，使仅标记 dirty 不立即落盘） | `RecordConsumerUsage` 累计 `TotalTokens`/`TotalRequests` |
| 3.5.2 | 在计数尚未触发即时落盘（<10 次变更）时，发送 `SIGTERM`/`SIGINT` 触发 `gracefulShutdown` | 日志出现 `multiUser.StopBatchSave()` 调用；`StopBatchSave` 关闭 `saveStopCh` 并等待 `saveDone`（≤5s） |
| 3.5.3 | 进程退出后重启，读取该 consumer 用量 | 重启前最后一次 dirty 计数**已落盘**，未被静默丢弃 |
| 3.5.4 | 反复 SIGTERM 重启 3 次（对照 v4.3.30 修复说明） | 每次重启后用量均连续累加，无"每次重启丢 5 秒用量"现象；`StopBatchSave` 重复调用不 panic（幂等） |

**失败判据**：重启后 consumer 用量缺失最后若干次计数（dirty 未 flush）→ v4.3.30 修复回归；`StopBatchSave` 并发/重复调用 panic（非幂等）；shutdown 卡死 >5s（无超时保护）。

---

## 4. 质量门禁（CI）

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -l .`（仅 Go 文件） | 无未格式化文件 |
| 构建 | `go build ./...` | 0 error |
| 静态 | `go vet ./...` | 0 新增 warning |
| 特性单测 | 见 §2.7 合并命令 | 全部 PASS |
| 全量 | `go test -race -count=1 -timeout 25m ./...` | 通过（Windows 沙箱下存在与文件权限/日志锁相关的预存失败，属环境限制，与本次改动无关；Linux CI 全绿） |

---

## 5. 一致性复核（IS_PASS）

最终人工/自动复核清单：

- [ ] `recordContributionToLedger` 在 `request_id` 非空且 `ticketStore`/`node` 存在时签发并会签 ticket（v4.3.18 记账侧）
- [ ] Ticket 双签名 + `IsDoubleSpend` 防重放（v4.3.18，**当前无单测覆盖**，见 §2.8 缺口）
- [ ] `probeSchedule` 按信誉/最近可见返回正确间隔（v4.3.17，**当前无单测**）
- [ ] `CrossVerifyWithQuorum` 在 >20% 延迟偏差时置 `suspect`（v4.3.17，**当前无单测**）
- [ ] 治理 propose→ratify 双链 `PrevHash` 连续且 `VerifyChain()` 有效（v4.3.29，`governance_test.go` 已覆盖）
- [ ] 重复 ratify 被拒、spam 上限生效（v4.3.29，`governance_test.go` 已覆盖）
- [ ] Propose/Ratify 持锁调 `saveLocked` 不触发 RWMutex 自死锁（v4.3.32 回归，`governance_deadlock_test.go` 已覆盖）
- [ ] 透明度/导出端点鉴权生效且格式与单测一致（v4.3.29）
- [ ] 公益配额核算 + 消费侧回退免费池与免费池同路径（v4.3.29，`ledger_quota_consume_test.go` 已覆盖）
- [ ] 贡献者身份校验拒绝非法来源（`TestVerifiedContributorIDRejectsGarbage` 对应逻辑）
- [ ] `logSharedNetworkSoftReminder` 仅为软提醒，不强制改共享配置（v4.3.29）
- [ ] `gracefulShutdown` 调用 `multiUser.StopBatchSave()`；`StopBatchSave` 幂等关闭 `saveStopCh` 并等待 `saveDone`（≤5s）（v4.3.30，`multiuser_shutdown_test.go` 已覆盖）
- [ ] 重启后 consumer 用量连续不丢（v4.3.30，端到端见 §3.5 / §2.8 缺口）
- [ ] `main.go` `AppVersion` 与发布版本一致；无 `TODO` / 占位 / `pass` / `...`
