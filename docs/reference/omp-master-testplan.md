# OpenModelPool 完整测试方案（Master Test & Regression Plan）

> 生成日期：2026-08-12 ｜ 范围：v4.1.6 → v4.5.2 全部批次 + 近期变更 + 全量功能回归
> 基线实测：2026-08-12 `go test -count=1 ./...` → `ok 45.2s`，**0 FAIL**（104 测试文件 / ≈2602 用例）
> 关联：本文件为聚合主计划；各批次细节见 `docs/reference/*-testplan.md`（§4.2 索引）

---

## 0. 总览与判读

| 维度 | 现状（2026-08-12 实测） | 判读 |
|------|------------------------|------|
| 全量功能回归 | `go test -count=1 ./...` → 45.2s，0 FAIL | ✅ 绿 |
| 多节点组网核心单测 | 44 例（Gossip/NAT-STUN/NAT打洞/DHT/账本多副本/AddrMan）全绿 | ✅ 绿 |
| 近期 v4.5.2 测试补齐 | 6 文件 / 27 用例已提交（2ff4121） | ✅ 已落地 |
| 主动探测交叉验证（P0, v4.3.17） | `capability_probe_test.go` ×3（`TestProbeSchedule_IntervalsByReputation` / `TestCapabilityVerifier_CrossVerifyQuorum` / `TestCapabilityVerifier_VerifyClaim`） | ✅ 已补（2026-08-12 编写，2026-08-15 提交） |
| provider 免密修复（ee9938f/3fb07e4） | `provider_keyless_test.go` ×2（`TestTestKeylessConnectivity_AnonymousNoToken` / `TestHandleTestAllKeys_KeylessFreePool`） | ✅ 已补（2026-08-12 编写，2026-08-15 提交） |
| §2.8 其余建议用例 | AddrMan 富字段 / NAT 端点 / gossip 循环 / 账本 60s 定时器：部分被调用、缺专属用例 | ⚠️ 部分 |

**一句话结论**：整个功能回归当前全绿；多节点组网能力有单测+集成手册两层覆盖；最大的"未自动化"风险集中在 P0 安全能力（主动探测交叉验证）与近期 provider 免密修复。

---

## 1. 多节点组网能力（核心，引用三份 testplan）

| 能力域 | testplan 文档 | 覆盖点 | 实时状态 |
|--------|---------------|--------|----------|
| 网络/NAT/去中心化（v4.3.14–v4.3.29） | `network-nat-decentralization-v4.3.14-v4.3.29-testplan.md` | Gossip 五消息 / AddrMan / NAT-STUN / NAT 打洞直连 / DHT I/O / 账本多副本 reconciliation / 三节点 mesh 集成 QA 手册（§3） | 单测 44 例全绿；§2.8 缺口见 §2.4 |
| 私有节点联邦互联（v4.1.6） | `federation-v4.1.6-testplan.md` | 手动加 Peer / 邀请码 / 种子发现 / 两节点联调 | 已随全量回归覆盖 |
| 联邦自动发现 mesh（v4.1.7） | `discovery-v4.1.7-testplan.md` | 手动 peer 桥接信任池 / PEX gossip / 三节点 mesh 发现 | 已随全量回归覆盖 |

> 多节点组网"端到端验证"的手动集成步骤见 `network-nat-decentralization` §3（拓扑：full-cone 直连 + 对称 NAT 强制中继 + 三节点联邦；6 条验收标准 + 失败判据）。该部分目前**仅手动、未自动化**。

---

## 2. 近期变更与回归覆盖

### 2.1 v4.5.2 提交的测试补齐（6 文件 / 27 用例，提交 `2ff4121`）

| 测试文件 | 覆盖能力 | 用例数 |
|----------|----------|--------|
| `governance_exec_test.go` | 治理执行 hook（P2-1(iv)）：`executeRatified`/`rebuildEffect`、加成式策展名册、批准生效/审计-only | 5 |
| `ticket_test.go` | **补齐 §2.8 缺口**：Ticket 防双花（双签名 / 指纹 / countersign+double-spend / notarize / AntiCollusion 两层） | 7 |
| `region_sync_test.go` | 区域同步 reconciliation：IP 探测/池上报填缺口、保留已知节点、剪枝过期、HostFromEndpoint | 7 |
| `discovery_notify_poison_test.go` | 发现 notify 投毒防护：pubkey 替换被拒 | 1 |
| `ledger_panel_wire_test.go` | 管理面板账本页脚本注入真实端点、嵌于 admin.html | 2 |
| `update_failclosed_test.go` | 更新完整性 fail-closed：checksum/signature 校验（非法格式/尺寸/缺失拒绝） | 5 |

### 2.2 治理执行 hook（P2-1(iv)）

- 源码：`governance.go`（`executeRatified`/`rebuildEffect` + `AdmittedNodes`/`AllowedModels`/`IsCommunityAdmitted`/`IsCommunityCuratedModel`）
- 测试：`governance_exec_test.go` ×5（已列 §2.1）
- 验证点：批准后 `admit_node`/`allow_model` 写入加成式策展名册并持久化、`load` 重建；`param_change` 仅审计

### 2.3 贡献配额去自报（决策 3B）

- 源码：`network_quota.go`（`contrib_quota` 改用账本真实已捐 token）、`network.go`（移除 `config.ContribPoints` 自报灌配额）
- 测试：`ledger_contrib_quota_test.go` ×4（`TestContributionQuotaAccrue`/`Persistence`/`TestRecordContributionAccruesQuota`/`TestRecordContributionQuotaHookNilSafe`）+ `handler_batch10_test.go` 引用
- 验证点：配额按真实捐赠累积、持久化、hook 为 nil 时安全降级

### 2.4 已知覆盖缺口（待补，按优先级）

| 优先级 | 缺口 | 所在源文件 | 建议新增用例 |
|--------|------|------------|--------------|
| ~~**P0**~~ ✅ 已补 | 主动探测交叉验证：`probeSchedule`/`CrossVerify`/`VerifyClaim`（`CapabilityVerifier`） | `contribution_ledger.go`、`capability_probe_test.go` | `TestProbeSchedule_IntervalsByReputation`、`TestCapabilityVerifier_CrossVerifyQuorum`、`TestCapabilityVerifier_VerifyClaim`（随本批次 2026-08-15 提交） |
| ~~**P1**~~ ✅ 已补 | provider 免密修复专属单测（ee9938f 允许 keyless free-pool test-all-keys；3fb07e4 匿名 free-pool 免 token） | `admin_providers.go`、`client.go`、`provider_keyless_test.go` | `TestTestKeylessConnectivity_AnonymousNoToken`、`TestHandleTestAllKeys_KeylessFreePool`（随本批次 2026-08-15 提交） |
| P2 | AddrMan 富字段方法：`RecordSuccess`/`RecordFail`/`GetGateways`/`GetSeeds`/`PurgeStale`/`MarkGateway`/`MarkSeed`/`routeTableHealthLoop`（现有仅部分调用，缺专属用例） | `network.go` | §2.8 所列 8 个 `TestRouteTable_*` |
| P2 | NAT 端点：`/api/nat/status`、`/api/nat/probe`、`ProbeDirect`/`ShouldUseDirect`/`stunLoop` 行为 | `nat_traversal.go`、`routes.go` | `TestNATStatusHandler`、`TestNATProbeHandler`、`TestProbeDirect_CachesSuccess`、`TestShouldUseDirect_StaleExpiry` |
| P2 | Gossip 循环内部：`pingLoop`/`getPeersLoop`/`announceLoop`/`sendToRandomPeers`/`doAnnounceRound`/`processPeerHints` | `gossip.go` | §2.8 所列 4 个 `Test*` |
| P3 | 账本 60s 自动 reconciliation 定时器（非手动触发，现有仅手动触发用例） | `contribution_ledger.go` | `TestLedgerAutoReconcile_Ticked60s` |

> 注：`ticket_test.go` 已补齐 §2.8 中 "Ticket 防双花" 一项；其余 §2.8 缺口状态如上。

---

## 3. 全量功能回归（整个功能回归）

### 3.1 命令

```bash
# 标准回归（本地 / 本方案基线）
go test -count=1 ./...

# CI 门禁（与 Linux CI 一致，见 v4.3.32 CHANGELOG）
go test -race -count=1 -timeout 25m ./...
```

### 3.2 基线结果（2026-08-12 实跑）

| 套件 | 测试文件 | 用例数（约） | 状态 |
|------|----------|--------------|------|
| 整个模块（单 package） | 104 个 `*_test.go` | ≈2602 | ✅ `ok 45.2s`，0 FAIL |
| `-race` 全量 | 同上 | 同上 | ⏳ 未在本机实跑（按 CHANGELOG 与 network-nat §4 口径：Linux CI 全绿；Windows 沙箱历史有 3 个文件/锁预存失败，与本批次无关） |

> 本次 `go test -count=1 ./...` 本机实测**未触发**网络-nat §2 提到的 3 个 Windows 预存失败，全绿。

---

## 4. CI 质量门禁

### 4.1 门禁表

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -l .`（仅改动 Go 文件） | 无未格式化文件 |
| 构建 | `go build ./...` | 0 error（本方案实跑 EXIT=0） |
| 静态 | `go vet ./...` | 0 新增 warning |
| 特性单测（聚合批次） | 各 `*-testplan.md` §4 的 `-run` 聚合 | 全部 PASS |
| 全量回归 | `go test -count=1 ./...` | 通过（本方案实跑 45.2s 0 FAIL） |
| 全量（竞态） | `go test -race -count=1 -timeout 25m ./...` | 通过（CI） |

### 4.2 现有 9 份按批次 testplan 索引（聚合）

| 文档 | 覆盖版本 |
|------|----------|
| `network-nat-decentralization-v4.3.14-v4.3.29-testplan.md` | v4.3.14 – v4.3.29（网络/NAT/去中心化） |
| `federation-v4.1.6-testplan.md` | v4.1.6（私有节点联邦互联） |
| `discovery-v4.1.7-testplan.md` | v4.1.7（联邦自动发现 mesh） |
| `ledger-governance-v4.3.17-v4.3.30-testplan.md` | v4.3.17 – v4.3.30（账本/治理/公益） |
| `infra-refactor-v4.2.4-v4.3.32-testplan.md` | v4.2.4 – v4.3.32（基础设施/CI/重构） |
| `security-hardening-v4.2.1-v4.4.44-testplan.md` | v4.2.1 – v4.4.44（安全加固） |
| `self-update-installer-v4.3.1-v4.4.44-testplan.md` | v4.3.1 – v4.4.44（自动更新/安装器） |
| `adapters-providers-v4.2.9-v4.3.29-testplan.md` | v4.2.9 – v4.3.29（适配器/Provider/免费池） |
| `ui-onboarding-v4.3.5-v4.3.10-testplan.md` | v4.3.5 – v4.3.10（UI/Onboarding） |

> v4.4.44 → v4.5.2 的近期变更（provider 免密、治理执行、贡献配额、v4.5.2 测试补齐）由本文档 §2 统一收纳；其中 v4.5.2 测试补齐已随 §3 全量回归自动覆盖。

---

## 5. 一致性复核（IS_PASS）

- [x] `go build ./...` 通过
- [x] `go test -count=1 ./...` 全绿（45.2s，0 FAIL）
- [x] 多节点组网核心单测（44 例）全绿
- [x] v4.5.2 测试补齐（6 文件 / 27 用例）已提交并随全量回归通过
- [x] 现有 9 份批次 testplan 均索引于 §4.2
- [x] **已补**：主动探测交叉验证（P0）单测 — §2.4 P0（2026-08-15 提交）
- [x] **已补**：provider 免密修复专属单测 — §2.4 P1（2026-08-15 提交）
- [ ] 部分 §2.8 建议用例（AddrMan/NAT 端点/gossip 循环/账本 60s 定时器）待补 — §2.4 P2/P3
- [ ] 多节点组网端到端（§1 `network-nat` §3）目前仅手动，未自动化

---

## 附录：近期测试真实用例清单（v4.5.2，提交 `2ff4121`）

**governance_exec_test.go（5）**
`TestGovernance_AdmitNodeBadPayloadNoop` · `TestGovernance_AdmitNodeTakesEffect` · `TestGovernance_AllowModelTakesEffect` · `TestGovernance_EffectRebuiltOnLoad` · `TestGovernance_ParamChangeAuditOnly`

**ticket_test.go（7）**
`TestAntiCollusionCheck_Empty` · `TestAntiCollusionCheck_FlagsDeviation` · `TestTicketFingerprint_Deterministic` · `TestTicketStore_Cleanup` · `TestTicketStore_CountersignAndDoubleSpend` · `TestTicketStore_IsDoubleSpend` · `TestTicketStore_Notarized`

**region_sync_test.go（7）**
`TestReconcileEmptyKnownMapDisablesPruning` · `TestReconcileFillsGapFromIPDetect` · `TestReconcileFillsGapFromPoolReport` · `TestReconcileKeepsKnownNodes` · `TestReconcileKeepsSelfEvenWhenStale` · `TestReconcilePrunesStaleUnknownNode` · `TestHostFromEndpoint`

**discovery_notify_poison_test.go（1）**
`TestPeersNotify_PubKeySubstitution_Rejected`

**ledger_panel_wire_test.go（2）**
`TestLedgerPanelScriptCallsRealEndpoints` · `TestLedgerPanelWiredInAdminHTML`

**update_failclosed_test.go（5）**
`TestFetchChecksum_InvalidFormat` · `TestFetchChecksum_OK` · `TestFetchSignature_NotFound` · `TestFetchSignature_OK` · `TestFetchSignature_WrongSize`
