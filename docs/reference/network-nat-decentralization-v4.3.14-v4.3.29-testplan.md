# OpenModelPool 网络 / NAT / 去中心化批次测试计划（v4.3.14 – v4.3.29）

> 版本范围：v4.3.14 – v4.3.29（网络/NAT/去中心化聚合批次）
> 聚合理由：v4.3.14→v4.3.19 是连续提交的「网络基础能力」增量（Gossip 五消息、AddrMan、NAT traversal、主动探测交叉验证、Ticket 防双花及修复），v4.3.22→v4.3.24 是 git 上紧邻的 DHT 网络 I/O 与 NAT/STUN 真实打通（含对称 NAT 强制中继），v4.3.29 是上述能力的功能性闭环（NAT 打洞直连 + 账本多副本 60s reconciliation）。二者在源码与测试上同属一个主题域，故合并为一份 testplan。CHANGELOG 中 v4.3.20/v4.3.21/v4.3.25–v4.3.28 与本批次无直接关联，不纳入范围。
> 关联文档：`docs/CHANGELOG.md`、`ARCH-discovery-v4.1.7.md`、`federation-v4.1.6-testplan.md`、`discovery-v4.1.7-testplan.md`

---

## 1. 范围与背景

本批次覆盖「让私有节点无需中继即可直连 + 贡献可去中心化交叉验证与存证」的完整能力栈。关键变更按版本列示如下。

| 版本 | 能力 | 关键变更 | 优先级 |
|------|------|----------|--------|
| v4.3.14 | Gossip 五消息协议 | `gossip.go` 拆为 `pingLoop`/`getPeersLoop`/`announceLoop`；新增 PING/PONG/GET_PEERS/PEERS/ANNOUNCE/SYNC 消息类型；`handleFederationGossip` 分发全 5 类；新增 `processPeerHints()` | P1 |
| v4.3.15 | AddrMan 地址管理器 | `network.go` `RouteTable` 新增 `RecordSuccess`/`RecordFail`/`GetGateways`/`GetSeeds`/`PurgeStale`/`MarkGateway`/`MarkSeed`；`RouteEntry` 扩展 `FailCount`/`UptimeScore`/`IsGateway`/`IsSeed`；`node_registry.go` 持久化这些字段；`routeTableHealthLoop()` 每 30min 调 `PurgeStale()` | P1 |
| v4.3.16 | NAT traversal 架构 | `nat_traversal.go`：`NATManager`/`stunQuery`/`ProbeDirect`/`ShouldUseDirect`/`stunLoop`；`network_relay.go` 触发直连探测；`/api/nat/status`、`/api/nat/probe`；`stubs.go` `registerWithBootstraps` 落地 | P1 |
| v4.3.17 | 主动探测与交叉验证 | `contribution_ledger.go`：`ProbeSchedulerLoop`/`probeSchedule`/`CrossVerifyWithQuorum`（3 节点独立验证，>20% 延迟偏差触发调查）；`realProbeFn` 替代 no-op；`minVerifiers` 2→3 | P1 |
| v4.3.18 | Ticket 防双花系统 | `ticket.go`：`UsageTicket` 双签名、`TicketFingerprint`、`TicketStore` issue/countersign/double-spend/notarize；`AntiCollusionCheck` 三层；`relay.go` `recordContributionToLedger` 发 ticket；`/api/ticket/submit\|notarize\|anti-collusion`；`contribution_ledger_init.go` `initTicketStore`+`notarizeLoop` | P0（安全） |
| v4.3.19 | Ticket 修复 | `TicketStore.Cleanup()` 修复内存泄漏；`handleNotarize` 不再洗白 double-spend；`AntiCollusionCheck` 改用 `flaggedSet` 去重 | P0（安全修复） |
| v4.3.22 | 阶段0 诚信地基 + P1-1 DHT 网络 I/O | `dht_networking.go`/`dht_kademlia.go`；`NewDHTNode` 接入 `InMemoryDHTNetwork` 测试台（git d5f8af4） | P1 |
| v4.3.23 | NAT/STUN 真实打通 | 多 server 探测 + NAT 类型分类 + 纯函数单测（git d597d42） | P1 |
| v4.3.24 | 对称 NAT 强制中继 | 对称 NAT 经 `PreferRelay()` 一律走中继，不再浪费直连探测（git 6758829） | P1 |
| v4.3.29 | NAT 打洞直连 + 账本多副本 | `nat_punch.go`/`nat_punch_loop.go` 让节点直连；`ledger_redundancy.go`/`ledger_replication.go` 多副本存储 + 60s 自动 reconciliation | P1（功能闭环） |

> 注：v4.3.16 的 `NATManager`/`stunQuery`/`ProbeDirect` 等已存在，`v4.3.23` 将其从「结构就绪」推进到「真实可跑」（多 STUN server、NAT 类型分类）。`Pure functions` 的单测集中在 `nat_traversal_test.go`（见 §2.3）。

---

## 2. 自动化单元测试（离线 / httptest）

运行全集：`go test ./...`（Linux CI 全绿；Windows 沙箱下存在 3 个与文件权限/日志锁相关的预存失败，与本批次无关，见 `federation-v4.1.6-testplan.md` 第 5 节说明）。

针对性运行命令按能力分组列于各小节末尾。

### 2.1 Gossip 五消息协议（v4.3.14）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `discovery_gossip_test.go` | `TestBuildKnownPeers_MergesFederationAndManual` | `KnownPeers` = 信任池活跃节点 + 手动 peer 去重合并、不含自身 |
| | `TestDoGossipRound_AttachesKnownPeers` | `doGossipRound` 出站 sync 消息 JSON 含 `known_peers` |
| | `TestProcessGossipResponse_MergesKnownPeersIntoHints` | `processGossipResponse` 把对端 `KnownPeers` 并入 `fed.discoveryHints` |
| | `TestExchange_SendsXNodeIDHeader` | 出站 gossip 路径 `/api/federation/gossip`，带 `X-Node-ID` |
| | `TestFetchFullPoolFromPeer_SendsXNodeIDHeader` | 全量拉取路径 `/api/federation/pool`，带 `X-Node-ID`，版本递增 |
| | `TestGossipURLHasAPIPrefix` | 回归守护：所有出站联邦 URL 含 `/api` 前缀 |

```bash
# Gossip（6 个用例）
go test ./... -run 'TestBuildKnownPeers_MergesFederationAndManual|TestDoGossipRound_AttachesKnownPeers|TestProcessGossipResponse_MergesKnownPeersIntoHints|TestExchange_SendsXNodeIDHeader|TestFetchFullPoolFromPeer_SendsXNodeIDHeader|TestGossipURLHasAPIPrefix' -v
```

### 2.2 AddrMan 路由表（v4.3.15）

现有测试覆盖 `RouteTable` 的通用 CRUD（`Put`/`Get`/`Remove`/`GetAll`/`Count`/`GetByModel`/`PurgeExpired`/`SelectBestNode`/`UpsertEntry`），分布在 `handler_batch5_test.go`、`handler_batch8_test.go`、`handler_batch10_test.go`、`utils_test.go`。与 v4.3.15 新增 rich 字段持久化直接相关的用例：

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `node_registry_qa_test.go` | `TestRouteTable_UpsertEntry_PreservesRichFields` | `UpsertEntry` 保留 `FailCount`/`UptimeScore`/`IsGateway`/`IsSeed` 等 rich 字段（冷启动还原不丢字段） |
| `node_registry_qa_test.go` | `TestInitNodeRegistry_ColdStartRefillsRouteTable` | 重启后从 `.nodes/` 持久化目录回填 RouteTable，rich 字段恢复 |
| `node_registry_test.go` | `TestRouteEntryFromNodeInfo` | `RouteEntry` 从 NodeInfo 构造，字段映射正确 |
| `node_registry_test.go` | `TestNodeRegistry_SavePeerRoundTrip` / `TestNodeRegistry_SaveAndLoadNode` | NodeRegistry 持久化往返（含 `FailCount`/`UptimeScore`） |
| `node_registry_qa_test.go` | `TestFederationUpdateNodeInfo_PersistsToRegistry` | `updateNodeInfo` 写盘到 registry |

```bash
# AddrMan / 路由表 rich 字段（5 个用例，加上通用 RouteTable 用例另算）
go test ./... -run 'TestRouteTable_UpsertEntry_PreservesRichFields|TestInitNodeRegistry_ColdStartRefillsRouteTable|TestRouteEntryFromNodeInfo|TestNodeRegistry_SavePeerRoundTrip|TestNodeRegistry_SaveAndLoadNode|TestFederationUpdateNodeInfo_PersistsToRegistry' -v
```

### 2.3 NAT traversal / STUN 纯函数（v4.3.16 / v4.3.23 / v4.3.24）

v4.3.23 提到的「纯函数单测」即 `nat_traversal_test.go` 中的 STUN 解析与 NAT 分类用例，覆盖 v4.3.16 的 `stunQuery` 解析路径、v4.3.23 的 NAT 类型分类与 v4.3.24 的对称 NAT 强制中继判定。

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `nat_traversal_test.go` | `TestParseSTUNResponse_XORMappedIPv4` | `stunQuery` 解析 XOR-MAPPED-ADDRESS（IPv4）正确提取公网地址 |
| | `TestParseSTUNResponse_Truncated` | 截断报文 → 解析失败（不 panic） |
| | `TestParseSTUNResponse_WrongType` | 非 Binding 响应 → 解析失败 |
| | `TestParseSTUNResponse_NoXorAddr` | 缺 XOR-MAPPED-ADDRESS 属性 → 解析失败 |
| | `TestClassifyNAT` | 多 server 探测结果 → NAT 类型分类（full/port/symmetric 等） |
| | `TestPreferRelay` | **对称 NAT 强制中继**：`symmetric→true`，`full_cone`/`open`/`unknown→false`（v4.3.24 门禁） |

```bash
# NAT traversal / STUN 纯函数（6 个用例，覆盖 v4.3.16/23/24）
go test ./... -run 'TestParseSTUNResponse_XORMappedIPv4|TestParseSTUNResponse_Truncated|TestParseSTUNResponse_WrongType|TestParseSTUNResponse_NoXorAddr|TestClassifyNAT|TestPreferRelay' -v
```

### 2.4 NAT 打洞直连（v4.3.29）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `nat_punch_test.go` | `TestNewPunchOffer` | 生成打洞 offer（含 nonce/candidate） |
| | `TestEncodeDecodeOfferRoundtrip` | offer 编码→解码往返一致 |
| | `TestDecodeRejectsBadMagic` | 错误 magic → 拒绝 |
| | `TestDecodeRejectsShortFrame` | 过短帧 → 拒绝 |
| | `TestDecodeRejectsMissingFields` | 缺字段 → 拒绝 |
| | `TestDecodeRejectsBadNonceLen` | 非法 nonce 长度 → 拒绝 |
| | `TestNonceEqual` | nonce 常量时间比较 |
| | `TestParseUDPAddr` | UDP 地址解析 |
| | `TestCandidate4Tuple` | 4 元组候选构造 |
| | `TestPunchTarget` | 计算打洞目标地址 |
| | `TestPackUnpackUint64` | 长度编码/解码往返 |
| `nat_punch_loop_test.go` | `TestPunchLoopback` | 回环打洞可建立直连通道 |
| `nat_punch_relay_test.go` | `TestPunchOfferExchange` | 经中继交换 offer 并完成打洞握手 |
| | `TestHandlePunchExchange` | `handlePunchExchange` 处理对端 exchange 请求 |

```bash
# NAT 打洞直连（14 个用例）
go test ./... -run 'TestNewPunchOffer|TestEncodeDecodeOfferRoundtrip|TestDecodeRejectsBadMagic|TestDecodeRejectsShortFrame|TestDecodeRejectsMissingFields|TestDecodeRejectsBadNonceLen|TestNonceEqual|TestParseUDPAddr|TestCandidate4Tuple|TestPunchTarget|TestPackUnpackUint64|TestPunchLoopback|TestPunchOfferExchange|TestHandlePunchExchange' -v
```

### 2.5 DHT 网络 I/O（v4.3.22）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `dht_networking_test.go` | `TestDHT_PingPong` | `NewDHTNode` + `InMemoryDHTNetwork` 测试台：Ping/Pong 往返 |
| | `TestDHT_IterativeFindNodeDiscoversViaRelay` | 迭代 FindNode 经中继发现节点 |
| | `TestDHT_StoreAndFindValueViaRelay` | Store/FindValue 经中继完成 |
| | `TestDHT_FindValueMissingReturnsNotFound` | 缺失 Value → NotFound（不 panic） |

```bash
# DHT 网络 I/O（4 个用例）
go test ./... -run 'TestDHT_PingPong|TestDHT_IterativeFindNodeDiscoversViaRelay|TestDHT_StoreAndFindValueViaRelay|TestDHT_FindValueMissingReturnsNotFound' -v
```

### 2.6 贡献账本多副本与 reconciliation（v4.3.29）

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `ledger_redundancy_test.go` | `TestBuildManifest` | 构建副本清单（record→replica 映射） |
| | `TestDiffManifests` | 两份清单 diff，定位缺失/不一致副本 |
| | `TestReplicaRedundancy` | 冗余度达标校验 |
| `ledger_replication_test.go` | `TestLedgerReplicationPush` | 向对端推送副本 |
| | `TestLedgerReconcileHeal` | 60s reconciliation：修复缺失副本 |
| | `TestLedgerReconcileDivergent` | 分歧副本按权威版本收敛 |
| | `TestLedgerReconcileAll` | 全量 reconciliation 覆盖所有 record |
| | `TestLedgerManifestHandler` | `/api/ledger/manifest` 处理 |
| | `TestLedgerRecordHandler` | `/api/ledger/record` 处理 |
| | `TestRecordContributionTriggerNoopWhenReplicatorNil` | replicator 为 nil 时写账本不触发推送（安全降级） |

```bash
# 账本多副本 + reconciliation（10 个用例）
go test ./... -run 'TestBuildManifest|TestDiffManifests|TestReplicaRedundancy|TestLedgerReplicationPush|TestLedgerReconcileHeal|TestLedgerReconcileDivergent|TestLedgerReconcileAll|TestLedgerManifestHandler|TestLedgerRecordHandler|TestRecordContributionTriggerNoopWhenReplicatorNil' -v
```

### 2.7 相关回归（本批次能力依赖的既有网络/身份测试）

| 测试文件 | 用例（节选） | 验证点 |
|----------|------|--------|
| `network_relay_test.go` | `TestPickBestAddress` / `TestRelayHopCountValidation` / `TestSelectBestNodeScoring` / `TestRouteEntryFields` 等 | 中继选路、跳数防环、RouteEntry 字段（与 AddrMan/直连探测协同） |
| `network_region_test.go` | `TestDetectRegion_IPv4` / `TestSelectNodeForRegion` / `TestGetOptimalRoute` 等 | 区域路由（直连候选排序依赖） |
| `network_slice1_test.go` | `TestSlice1_CheckJoinConditions_*` / `TestSlice1_SetNetworkEnabled_*` | 网络启用前置条件 |
| `network_slice2_test.go` / `network_slice2_qa_test.go` | `TestSlice2_DeriveP2PNodeID_*` / `TestSlice2_QA_*` | Node ID（gossip/ticket 身份基础） |
| `region_manager_wire_test.go` | `TestRegionsHandlerReflectsJoinPool` 等 | 区域聚合端点 |

```bash
# 相关回归（网络/中继/区域/身份）
go test ./... -run 'TestPickBestAddress|TestRelayHopCountValidation|TestSelectBestNodeScoring|TestDetectRegion_IPv4|TestSelectNodeForRegion|TestGetOptimalRoute|TestSlice1_CheckJoinConditions|TestSlice2_DeriveP2PNodeID|TestSlice2_QA_NodeIDGetInfoMatchesIdentity|TestRegionsHandlerReflectsJoinPool' -v
```

### 2.8 测试缺口（建议新增）

下列关键路径在本批次源码中存在，但**无对应自动化单测**，需补齐：

| 缺口路径 | 所在源文件 | 建议新增用例 |
|----------|------------|--------------|
| Gossip 五消息循环内部：`pingLoop`/`getPeersLoop`/`announceLoop`/`sendToRandomPeers`/`doAnnounceRound`/`processPeerHints` | `gossip.go` | `TestPingLoop_RespondsPong`、`TestGetPeersLoop_MergesHints`、`TestAnnounceLoop_BroadcastsProviders`、`TestProcessPeerHints_Merge` |
| AddrMan 方法：`RecordSuccess`/`RecordFail`/`GetGateways`/`GetSeeds`/`PurgeStale`/`MarkGateway`/`MarkSeed`/`routeTableHealthLoop` | `network.go` | `TestRouteTable_RecordSuccess_UpdatesUptimeScore`、`TestRouteTable_RecordFail_MarksUnreachableAt3`、`TestRouteTable_GetGateways`、`TestRouteTable_GetSeeds`、`TestRouteTable_PurgeStale_Removes7DayOld`、`TestRouteTable_MarkGateway`、`TestRouteTable_MarkSeed`、`TestRouteTableHealthLoop_CallsPurgeStale` |
| NAT 端点：`/api/nat/status`、`/api/nat/probe`，`ProbeDirect`/`ShouldUseDirect`/`stunLoop` 行为 | `nat_traversal.go`、`routes.go` | `TestNATStatusHandler`、`TestNATProbeHandler`、`TestProbeDirect_CachesSuccess`、`TestShouldUseDirect_StaleExpiry` |
| ~~主动探测交叉验证~~ ✅ 已补（2026-08-15）：`probeSchedule`/`CrossVerify`/`VerifyClaim`（`CapabilityVerifier`） | `contribution_ledger.go`、`capability_probe_test.go` | `TestProbeSchedule_IntervalsByReputation`、`TestCapabilityVerifier_CrossVerifyQuorum`、`TestCapabilityVerifier_VerifyClaim` |
| Ticket 防双花：`UsageTicket` 双签名、`TicketFingerprint`、`TicketStore.issue/countersign/double-spend/notarize`、`AntiCollusionCheck` 三层、`Cleanup` 内存泄漏修复、`handleNotarize` 不洗白、`flaggedSet` 去重 | `ticket.go`、`relay.go`、`contribution_ledger_init.go`、`routes.go` | `TestTicketStore_IssueAndCountersign`、`TestTicketFingerprint_Deterministic`、`TestTicketStore_DetectsDoubleSpend`、`TestTicketStore_Notarize`、`TestAntiCollusionCheck_ThreeLayers`、`TestTicketStore_Cleanup_NoLeak`、`TestHandleNotarize_RejectsDoubleSpend`、`TestAntiCollusionCheck_FlaggedSetDedup`、`TestRecordContributionToLedger_IssuesTicket` |
| 账本 reconciliation 定时器：60s 自动 reconciliation 调度（非手动触发） | `contribution_ledger.go` | `TestLedgerAutoReconcile_Ticked60s` |

> 最大测试缺口（更新于 2026-08-15）：**Ticket 防双花系统（v4.3.18/19）** 已随 v4.5.2 测试补齐获 `ticket_test.go` ×7 覆盖；**主动探测交叉验证（v4.3.17）** 已获 `capability_probe_test.go` ×3 覆盖（见上表 ✅ 已补）。二者 P0 安全能力现已均有自动化单测。

---

## 3. 集成测试 / QA 手册（三/多节点 mesh 直连 + gossip + ticket 交叉验证）

### 3.1 拓扑

```
   node-A (公网 A)              node-B (公网 B, 对称 NAT)        node-C (公网 C)
   ┌──────────────┐           ┌──────────────┐               ┌──────────────┐
   │ NAT: full_cone │          │ NAT: symmetric  │            │ NAT: port_restricted│
   │ gossip ↔ B,C   │           │ gossip ↔ A,C   │            │ gossip ↔ A,B       │
   │ 直连 A↔C       │◄─打洞─►   │ 中继 B↔A/C     │◄─中继─┐      │ 直连 A↔C          │
   └──────────────┘           └──────────────┘     │       └──────────────┘
                                                    │       
       贡献账本：A/B/C 各持多副本，每 60s reconciliation 收敛
       ticket：B 经 A 贡献 → relay 发 UsageTicket（双签名）→ A countersign → notarize
```

- **A、C**：非对称 NAT，应经 NAT 打洞（`nat_punch.go`）建立直连。
- **B**：对称 NAT，经 `PreferRelay()`（v4.3.24）强制走中继，不浪费直连探测。
- 三节点均加入同一 federation，gossip 传播 peer 提示与 provider 广播。

### 3.2 预期行为（验收标准）

1. **B 对称 NAT 强制中继**：`GET /api/nat/status` 显示 B 的 NAT 类型为 `symmetric`，且 `prefer_relay=true`；A→B 的流量全部经中继，日志无对 B 的 `ProbeDirect` 成功记录。
2. **A↔C 直连打洞**：A、C 完成 `nat_punch` 握手后，彼此通信不再经中继（延迟显著低于中继路径）。
3. **gossip 五消息互通**：A、B、C 之间出现 PING/PONG（liveness）、GET_PEERS/PEERS（PEERS 经 `processPeerHints` 合并）、ANNOUNCE（provider 广播）报文；`/api/federation/pool` 三方一致。
4. **Ticket 交叉验证**：B 作为贡献方经 A 处理后，`relay.go` `recordContributionToLedger` 发出 `UsageTicket`（请求方+提供方双签名）；A `countersign` 后 `notarizeLoop` 周期性公证；`GET /api/ticket/anti-collusion` 三层检查无异常。
5. **主动探测**：`ProbeSchedulerLoop` 周期性对声称能力做 `realProbeFn` 探测；`CrossVerifyWithQuorum` 对偏差 >20% 的节点触发调查（日志有 `investigation` 标记）。
6. **账本多副本 + 60s reconciliation**：任一节点 `POST /api/ledger/record` 写入后，60s 内其余节点经 `LedgerReconcileAll` 收敛到一致状态；`TestLedgerManifestHandler` 对应端点可查清单。

### 3.3 手动操作步骤

1. 启动三节点（目标版本），各自 `network_enabled=true`、完成身份生成、加入同一 federation（genesis 一致）。
2. 在 B 执行 `curl -s <B>/api/nat/status`，确认 `nat_type=symmetric`、`prefer_relay=true`。
3. 在 A、C 执行 `curl -s <A>/api/nat/status`，确认 NAT 类型非 symmetric；观察 A↔C 直连建立（日志出现 punch 成功）。
4. 经 B 向 A 发一次 chat completion 请求（B 为贡献方，A 为提供方中继）。
5. `curl -s -X POST <A>/api/ticket/submit -d '{...UsageTicket...}'`；查 A 日志 `countersign` 与 `notarize` 记录；`curl -s <A>/api/ticket/anti-collusion` 应无 anomalies。
6. 等待约 60s，在 A、B、C 各执行 `curl -s <node>/api/ledger/manifest`，比对 record 集合一致（reconciliation 收敛）。
7. 观察 gossip：`curl -s <A>/api/federation/pool` 含 B、C；抓包或日志确认 PING/PONG/PEERS/ANNOUNCE 报文齐全。

### 3.4 失败判据

- B 的 `prefer_relay` 为 `false`（对称 NAT 仍尝试直连）→ v4.3.24 失败。
- A↔C 始终走中继、无 punch 成功日志 → v4.3.29 打洞失败。
- `processPeerHints`/PEERS 未合并对端 peer，某节点 pool 缺员 → v4.3.14 失败。
- B 贡献后无 `UsageTicket` 发出，或 `anti-collusion` 报 anomaly，或 double-spend ticket 被 `notarize` 洗白 → v4.3.18/19 失败。
- 60s 后多节点 ledger manifest 不一致，reconciliation 未收敛 → v4.3.29 失败。
- `ProbeSchedulerLoop` 未启动 / `CrossVerifyWithQuorum` 对已知偏差节点无调查日志 → v4.3.17 失败。

---

## 4. 质量门禁（CI）

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -l .`（仅对改动 Go 文件） | 无未格式化文件 |
| 构建 | `go build ./...` | 0 error |
| 静态 | `go vet ./...` | 0 新增 warning |
| 特性单测（聚合批次） | `go test ./... -run 'TestBuildKnownPeers|TestDoGossipRound|TestProcessGossipResponse|TestExchange|TestFetchFullPoolFromPeer|TestGossipURLHasAPIPrefix|TestRouteTable_UpsertEntry_PreservesRichFields|TestInitNodeRegistry_ColdStartRefillsRouteTable|TestParseSTUNResponse|TestClassifyNAT|TestPreferRelay|TestNewPunchOffer|TestEncodeDecodeOfferRoundtrip|TestDecodeRejects|TestNonceEqual|TestParseUDPAddr|TestCandidate4Tuple|TestPunchTarget|TestPackUnpackUint64|TestPunchLoopback|TestPunchOfferExchange|TestHandlePunchExchange|TestDHT_|TestBuildManifest|TestDiffManifests|TestReplicaRedundancy|TestLedgerReplicationPush|TestLedgerReconcile|TestLedgerManifestHandler|TestLedgerRecordHandler|TestRecordContributionTriggerNoopWhenReplicatorNil' -v` | 全部 PASS |
| 全量 | `go test -race -count=1 -timeout 25m ./...`（CI 与本地一致，见 v4.3.32 CHANGELOG） | 通过（Windows 沙箱 3 个预存文件/锁失败为环境限制，Linux CI 全绿） |

---

## 5. 一致性复核（IS_PASS）

最终人工/自动复核清单（逐条可勾选）：

- [ ] Gossip 五消息协议落地：`pingLoop`/`getPeersLoop`/`announceLoop` 三循环运行，`handleFederationGossip` 分发 PING/PONG/GET_PEERS/PEERS/ANNOUNCE/SYNC，`processPeerHints` 合并 PEERS（v4.3.14）
- [ ] AddrMan 方法实现并持久化：`RecordSuccess`/`RecordFail`/`GetGateways`/`GetSeeds`/`PurgeStale`/`MarkGateway`/`MarkSeed` 存在，`node_registry.go` 持久化 rich 字段，`routeTableHealthLoop` 启动（v4.3.15）
- [ ] NAT traversal 纯函数通过：`TestParseSTUNResponse_*`/`TestClassifyNAT`/`TestPreferRelay` 全绿；STUN 解析与 NAT 分类真实可跑（v4.3.16/23）
- [ ] 对称 NAT 强制中继：`PreferRelay()` 对 `symmetric` 返回 `true`，B 类节点不浪费直连探测（v4.3.24）
- [ ] NAT 打洞直连：`nat_punch.go`/`nat_punch_loop.go` 测试全绿，A↔C 非对称 NAT 可直连（v4.3.29）
- [ ] DHT 网络 I/O：`NewDHTNode` + `InMemoryDHTNetwork` 测试台 4 用例全绿（v4.3.22）
- [ ] 账本多副本 + 60s reconciliation：`ledger_redundancy_test.go`/`ledger_replication_test.go` 全绿，reconciliation 端点可用（v4.3.29）
- [ ] Ticket 防双花系统已实现对齐 CHANGELOG：双签名 `UsageTicket`、`TicketStore` 四态、`AntiCollusionCheck` 三层、`Cleanup` 修复、`handleNotarize` 不洗白、`flaggedSet` 去重（v4.3.18/19）
- [ ] 主动探测与交叉验证落地：`ProbeSchedulerLoop`/`probeSchedule`/`CrossVerifyWithQuorum` 存在且 `realProbeFn` 替换 no-op（v4.3.17）
- [ ] 端点齐备：`/api/nat/status`、`/api/nat/probe`、`/api/ticket/submit`、`/api/ticket/notarize`、`/api/ticket/anti-collusion` 已注册
- [ ] 无 `TODO`/占位/`pass`/`...`；无遗留 no-op 探测函数（v4.3.17 前的 `probeFn` 已替换）
- [ ] 版本号：当前 `main.go` `AppVersion = "4.4.44"`（本批次开发期最高为 v4.3.29，发布时按实际 ldflags 对齐）
