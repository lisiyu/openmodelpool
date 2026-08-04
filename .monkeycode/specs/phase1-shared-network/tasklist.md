# Phase 1 实施任务列表

> **基于**: PRD-Phase1 v1.0 + openmodelpool-v4-design.md
> **生成日期**: 2026-08-04
> **状态**: TASK-1/2/3/4 全部完成

---

## 实施状态总览

| REQ | 里程碑 | 优先级 | 后端 | 前端 | 端到端 | 状态 |
|-----|--------|--------|------|------|--------|------|
| REQ-1 | M1 | P0 | DONE | DONE | DONE | 已完成 |
| REQ-2 | M1 | P0 | DONE | DONE | DONE | 已完成 |
| REQ-3 | M4 | P0 | DONE | DONE | DONE | 已完成 |
| REQ-4 | M4 | P0 | DONE | DONE | DONE | **已完成** |
| REQ-5 | M2 | P0 | DONE | DONE | DONE | 已完成 |
| REQ-6 | M3 | P0 | DONE | DONE | DONE | 已完成 |
| REQ-7 | M5 | P0 | DONE | DONE | DONE | 已完成 |
| REQ-8 | M7 | P0 | DONE | N/A | DONE | 已完成 |
| REQ-9 | M8 | P0 | DONE | DONE | DONE | **已完成** |
| REQ-10 | M9 | P0 | DONE | N/A | DONE | 已完成 |
| REQ-11 | M6 | P1 | DONE | DONE | DONE | **已完成** |
| REQ-12 | M11 | P1 | DONE | DONE | DONE | **已完成** |
| REQ-13 | M10 | P2 | DONE | DONE | DONE | 已完成 |

---

## 待实施任务

### TASK-1: REQ-9 能力声明协议端到端打通 (P0)

**现状**: `ledger/` 包中 `CapabilityClaim` 结构体、`GossipLedger.RecordClaim()`、`CapabilityVerifier` 均已实现，但未初始化、未接入主应用、无 API 端点、无前端展示。

**子任务**:

- [x] T1.1: 初始化 GossipLedger — 在 `initAllFederation()` 中创建 `contributionLedger` 全局变量，调用 `ledger.NewGossipLedger(node.NodeID())`，持久化到 `data/ledger.json`
- [x] T1.2: 添加 API 端点 — `POST /api/network/capability/claim`（提交签名声明）、`GET /api/network/capability/claims`（列出全网声明）、`GET /api/network/capability/verify/{peer_id}`（触发验证）
- [x] T1.3: 接入 peer 握手 — 扩展 `PeerNotifyPayload` 增加 `Claims []CapabilityClaimEntry` 字段；`handleNetworkPeersNotify` 收到声明后调用 `contributionLedger.RecordClaim()`；`sendNotifyToPeer` 附带本地声明
- [x] T1.4: 接入 CapabilityVerifier — 初始化时注入 probe 函数（Phase 1 使用 no-op probe，实际验证留 Phase 2）；验证端点已接入
- [x] T1.5: 前端展示 — `admin-network.js` 增加"能力声明"面板，展示各 peer 的声明；提交新声明表单
- [x] T1.6: 测试 — 单元测试覆盖 API 端点、账本持久化、空/错误场景（`capability_ledger_test.go`）；ledger 包全部 20 测试通过

**涉及文件**: `init.go`, `server.go`, `network.go`, `handlers.go`, `admin-network.js`, `admin.html`, `ledger/gossip_ledger.go`, `ledger/capability_verifier.go`

---

### TASK-2: REQ-11 贡献积分账本端到端打通 (P1)

**现状**: `GossipLedger` 有 `RecordContribution()`、`AppendTransaction()`、`DeriveBalance()`、`GossipSync()`，但未在 relay/gateway 请求路径中调用。

**子任务**:

- [x] T2.1: 复用 TASK-1 的 GossipLedger 初始化
- [x] T2.2: relay 路径记账 — `relay.go:handleRelayRequest` 成功转发后调用 `RecordContribution()` + `AppendTransaction("contribution", ...)`
- [x] T2.3: gateway 路径记账 — `network_relay.go:gatewayForwardToRemote` 成功后异步记账
- [x] T2.4: 消费记账 — `RelayToRemote`/`RelayStreamToRemote` 成功后 `AppendTransaction("consumption", ...)`
- [x] T2.5: 添加 API 端点 — `GET /api/network/ledger/contributions`、`GET /api/network/ledger/balance/{node_id}`、`GET /api/network/ledger/transactions`
- [x] T2.6: Gossip 同步 — gossip 同步消息 `Payload` 字段携带账本记录，接收端调用 `GossipSync()`
- [x] T2.7: 前端展示 — `admin-network.js` 增加"贡献账本"面板，展示余额、交易记录、链完整性
- [x] T2.8: 测试 — 覆盖记账、余额计算、交易链完整性、GossipSync 去重合并

**涉及文件**: `relay.go`, `network_relay.go`, `server.go`, `gossip.go`, `admin-network.js`, `admin.html`

---

### TASK-3: REQ-12 共享额度边界配置 UI + 执行 (P1)

**现状**: `ShareBoundaryConfig` 结构体已存在（`network.go:47`），含 `DailyContribCap`/`ShareIdleOnly`/`ModelWhitelist`，已持久化、已返回在 `/api/network/status`，但无编辑 UI、无保存 API、无执行逻辑。

**子任务**:

- [x] T3.1: 扩展 `handleNetworkConfigUpdate` — 请求体增加 `ShareBoundary *ShareBoundaryConfig`，传入 `netMgr.UpdateShareBoundary()`
- [x] T3.2: 执行逻辑 — relay/gateway 路径处理前检查：DailyContribCap 超限→429、ModelWhitelist 不含→429
- [x] T3.3: 前端编辑 UI — `admin-network.js` 增加"共享边界"配置区：每日贡献上限(数字输入)、仅共享空闲额度(toggle)、模型白名单(标签输入)
- [x] T3.4: 向导集成 — 入网向导第 3 步嵌入边界配置（复用 `quotaAllocation` 区块）；`loadShareBoundary()` 已接入 dashboard 加载流
- [x] T3.5: 测试 — `capability_ledger_test.go` 覆盖 `CheckShareBoundary`（无限制/超限/未超限/白名单）+ `UpdateShareBoundary`（持久化+字段更新）

**涉及文件**: `network.go`, `relay.go`, `network_relay.go`, `server.go`, `admin-network.js`, `admin.html`

---

### TASK-4: REQ-4 入网前置校验决策对齐 (P0, 需决策)

**现状**: `CheckJoinConditions()` (`network.go:1269`) 的 `AllMet = HasProvider && HasQuotaManager && HasSharedKey`，PRD 要求 `remaining_quota > 0` 为硬条件，但代码将其降级为软提示。

**决策选项**:
- **A (对齐代码)**: 更新 PRD，`HasSharedKey` 为硬条件，`remaining_quota > 0` 为软提示
- **B (对齐 PRD)**: 在 `AllMet` 中增加 `HasRemaining` 条件

**子任务** (决策后执行):
- [x] T4.1: 按选项A对齐 — `remaining_quota > 0` 保持软提示，修改 PRD-phase1.md REQ-4 描述（硬条件改为 Provider+QuotaManager+SharedKey，remaining_quota 为软提示）；同步修改 v4 设计文档和 REQ-13 描述
- [x] T4.2: 代码 `CheckJoinConditions()` 已与选项A一致（`AllMet = HasProvider && HasQuotaManager && HasSharedKey`，`HasRemaining` 不影响 `AllMet`），无需修改

**涉及文件**: `network.go`, `network_slice1_test.go`, `docs/PRD-phase1.md`

---

## 执行顺序

```
TASK-4 (决策) → TASK-1 + TASK-2 (可并行，共享 GossipLedger 初始化) → TASK-3
```

1. **先决策 TASK-4** — 影响后续校验逻辑
2. **TASK-1 + TASK-2 并行** — 共享 GossipLedger 初始化，是最大缺失块
3. **TASK-3** — 独立的配置+执行逻辑，依赖 TASK-2 的记账基础设施

---

## 已完成项详情 (审计确认)

### REQ-1 双模式运行时底座 — DONE
- `network_enabled` 配置默认 false (`network.go:73`)
- 所有网络能力在 `network_enabled=true` 时才初始化
- 个人版接口零回归

### REQ-2 收敛 federation_enabled — DONE
- 升级迁移: `network.go:453-460`，旧 `federation_enabled=true` 自动映射为 `network_enabled=true`
- `federation.go:147` 明确不再由旧键驱动
- 测试: `network_slice1_test.go:57-91`

### REQ-3 两级开关 UI — DONE
- 后端: `network.go:80` ShareToPool 字段，`SetShareToPool()` 方法
- 前端: `admin-network.js:175-186` 两级开关渲染 + 单向依赖
- 关闭 network_enabled 自动关闭 share_to_pool (`network.go:1160`)

### REQ-4 入网前置校验 — DONE (选项A)
- `CheckJoinConditions()` 在 `network.go:1326`
- 硬条件: `HasProvider && HasQuotaManager && HasSharedKey`（`AllMet`）
- `HasRemaining` 为软提示，不影响 `AllMet`
- 前端: `admin-network.js:699` 开启前校验
- PRD 已同步: `remaining_quota > 0` 明确为软提示条件

### REQ-5 助记词生成与备份 — DONE
- 后端: `node.go:136` `GenerateWithMnemonic()`，BIP39 + SLIP-0010
- API: `POST /api/network/identity/generate`、`POST /api/network/identity/confirm-backup`
- 前端: `admin-network.js:336-622` 完整向导（生成→展示→备份确认→5分钟超时→窗口失焦清空）
- 强制备份: 未勾选"已抄写"则"下一步"禁用

### REQ-6 Node ID 生成 — DONE
- `node.go:168` `deriveKeyFromMnemonic()` — BIP39→Ed25519→mmx-+Hex(pubkey)
- 确定性: 同一助记词派生同一 Node ID
- 恢复: `POST /api/network/identity/restore`

### REQ-7 Seed 端点中继 — DONE
- `relay.go` 完整单跳中继实现
- Seed 列表可配置
- Seed 故障不影响个人版

### REQ-8 传输路径加密 — DONE
- `transport_encryption.go` AES-256-GCM 实现
- 发送方签名加密字节，接收方验签后解密
- 已接入 relay 路径

### REQ-10 Public Key 四重限额 — DONE
- `network_global_pool.go:702-725` 四层限额: GlobalDailyLimit/IPDailyLimit/HourlyWindowLimit/ModelLimits
- 配置可持久化 (`public_key_global_daily_limit` 等)
- API: `GET /api/network/public-key-quota`
- 执行: `network_relay.go:665` 超限拒绝

### REQ-13 闲置额度检测提示 — DONE
- `admin-network.js:796-801` 检测条件 + 温和提示
- dismiss 后不再骚扰

---

## 非目标 (Phase 1 不做)

- 多跳路由 (Phase 2)
- 声誉系统自动评分 (Phase 2)
- 能力验证 probe (Phase 2, TASK-1 仅做声明+签名)
- 多 Seed 冗余 (Phase 2/3)
- 跨节点账本 BFT 共识 (Phase 3)
- DHT 网络协议实际查询 (Phase 2, 当前为内存版)
- IPFS 真实集成 (Phase 3, 当前为接口存根)
