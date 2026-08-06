# OpenModelPool v4.1.7 — 私有节点联邦自动发现补齐 测试计划

> 版本目标：**v4.1.7**（基于 v4.1.6 的增量功能）
> 主题：手动链接的私有节点相互可见，并通过 gossip 传播发现，形成网状（mesh）而非单向列表。
> 关联文档：`ARCH-discovery-v4.1.7.md`、`PRD-discovery-v4.1.7.md`

---

## 1. 范围与背景

v4.1.6 中，手动 peer（`config.Peers`）与联邦信任池（`fed.trustPool` + `localPeers`）是两套**互不连通**的存储：
手动 peer 永不被选为 gossip 目标，联邦节点也看不到手动 peer → 手动链接的两个私有节点彼此“看不见”。

v4.1.7 通过三块能力补齐闭环：

| 编号 | 能力 | 关键变更 | 优先级 |
|------|------|----------|--------|
| R5 / T-1 | 修复 gossip 客户端 URL 与身份头 | `/federation/*` → `/api/federation/*`；所有出站请求带 `X-Node-ID: node.NodeID()` | P0 |
| P0-2 | 手动 peer 桥接进信任池 | 新增 `fed.AddKnownNode`；`AddPeer`/`RemovePeer` 同步桥接 | P0 |
| P0-1 | 双向 notify + 防环 | 新增公开限流端点 `POST /api/network/peers/notify`（ed25519 验签）；仅人类新增触发回发 | P0 |
| P1-1 | gossip PEX | `GossipMessage.KnownPeers`；`processGossipResponse` 并入 `fed.discoveryHints` | P1 |

> **主发现闭环仍由 P0-2 信任池版本号驱动**：`processGossipResponse` 在 `msg.TrustPoolVersion > 本地` 时 `fetchFullPoolFromPeer` 拉全量池。PEX 作为地址可达性加固，不依赖 trustPool.endpoint 正确。

---

## 2. 自动化单元测试（离线 / httptest）

运行：`go test ./...` 或针对本特性：
```
go test -run 'TestPeersNotify|TestAddPeer|TestBuildKnownPeers|TestDoGossipRound|TestProcessGossipResponse|TestExchange|TestFetchFullPoolFromPeer|TestGossipURLHasAPIPrefix|TestAddKnownNode|TestRemovePeer|TestBridge' -v
```

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `discovery_notify_test.go` | `TestPeersNotify_ValidSignature_RegistersPeer` | 合法 ed25519 签名（基于 `node_id\|addresses\|timestamp`）+ 内嵌 `pub_key` → 200，注册本地 peer 并桥接入信任池 |
| | `TestPeersNotify_ForgedSignature_Rejected` | 伪造签名 → 401，不写盘、不桥接 |
| | `TestPeersNotify_ExpiredTimestamp_Rejected` | 时间戳 >5min → 400（先于验签） |
| | `TestAddPeer_TriggersNotifyNoLoop` | 人类新增 B → 仅向 B 发 1 次 notify；B 收到后注册 A 但**绝不回发**；重复新增不重发（R1/R3） |
| | `TestAddPeer_ConcurrentMutualAddNoInfiniteLoop` | 10 并发新增同一 peer → notify 总数有界（1..10），A 永不受回发（无环） |
| `discovery_bridge_test.go` | `TestAddPeer_BridgesToTrustPool` | `AddPeer` upsert 进信任池（`status=active`，带 pub_key），版本号递增，进入 `GetActiveNodes` |
| | `TestAddKnownNode_UpsertBumpsVersion` | 重复 `AddKnownNode` 是同 id 更新（不重复），版本严格递增 |
| | `TestRemovePeer_BridgesOut` | `RemovePeer` 同时从信任池移除 |
| | `TestBridgeDisabledInPersonalMode` | 联邦未启用时桥接为 no-op，`AddPeer` 仅本地生效 |
| `discovery_gossip_test.go` | `TestBuildKnownPeers_MergesFederationAndManual` | `KnownPeers` = 信任池活跃节点 + 手动 peer 去重合并，不含自身 |
| | `TestDoGossipRound_AttachesKnownPeers` | `doGossipRound` 出站 sync 消息 JSON 含 `known_peers` 且含手动 peer 提示 |
| | `TestProcessGossipResponse_MergesKnownPeersIntoHints` | `processGossipResponse` 把对端 `KnownPeers` 并入 `fed.discoveryHints`（首见优先） |
| | `TestExchange_SendsXNodeIDHeader` | `exchange` 请求路径 `/api/federation/gossip`，带 `X-Node-ID` |
| | `TestFetchFullPoolFromPeer_SendsXNodeIDHeader` | 全量拉取路径 `/api/federation/pool`，带 `X-Node-ID`，版本递增 |
| | `TestGossipURLHasAPIPrefix` | 回归守护：所有出站联邦 URL 必须含 `/api` 前缀 |

---

## 3. 集成测试：三节点 mesh 手动 + 自动发现

### 3.1 拓扑

```
   node-io  (openmodelpool.io)            node-com (openmodelpool.com)         node-cc (openmodelpool.cc)
   ┌──────────────┐                        ┌──────────────┐                    ┌──────────────┐
   │ manual: node-com                         │ manual: node-io   ◄─┐            │ (仅被传播)    │
   │ gossip ↔ node-com, node-cc               │ gossip ↔ node-io, node-cc        │ gossip ↔ 两者 │
   └──────┬───────┘                        └──────┬───────┘            └──────┬───────┘
          │ 人类手动链接                          │ 人类手动链接                   │
          └──────────────────────────────────────┘  (A↔B 手动)                │
                              gossip 自动把 node-cc 传播给 A、B ───────────────┘
```

- **A（.io）**：人类在 UI 手动添加 B（.com）的地址。
- **B（.com）**：人类在 UI 手动添加 A（.io）的地址。
- **C（.cc）**：**未被任何人类手动添加**，仅作为联邦活跃节点存在（例如已在 GitHub 信任池注册，或经种子节点发现）。

### 3.2 预期行为（验收标准）

1. **A↔B 双向可见（P0-1）**
   - A 添加 B → A 向 B 发 `POST /api/network/peers/notify`（带 ed25519 签名 + `X-Node-ID`）。
   - B 收到后：验签通过 → 本地注册 A → 桥接入信任池（`status=active`，版本+1）→ **不回发**给 A（R1 防环）。
   - 结果：A 的 `config.Peers` 含 B；B 的 `config.Peers` 含 A。双方互见。

2. **手动 peer 进入 gossip 候选（P0-2）**
   - A 添加 B 后，`fed.trustPool` 含 B（`active`），`GetActiveNodes()` 返回 B。
   - 下一轮 `doGossipRound`：A 的 sync 消息 `KnownPeers` 含 B 的地址提示，且 `TrustPoolVersion` 因桥接 +1。

3. **C 被自动传播（P1-1 + 版本驱动主闭环）**
   - A、B 各自 `doGossipRound` 交换 sync：因 `msg.TrustPoolVersion > 本地`（C 已在某方池中且版本更高），触发 `fetchFullPoolFromPeer({addr}/api/federation/pool)` 拉全量池，学到 C。
   - 同时 `processGossipResponse` 把对端 `KnownPeers` 并入 `fed.discoveryHints`，作为 C 地址不可达时的回退。
   - 结果：A、B 均能“看到”并保持 C 的活跃状态，**无需人类手动添加 C**。

4. **R5 URL 修复验证**
   - A/B/C 之间所有 gossip 出站请求路径均为 `/api/federation/{gossip,pool,announce}`，且均带 `X-Node-ID` 头；`withFederationAuth` 路径 1（X-Node-ID 命中信任池）可放行。

### 3.3 手动操作步骤（QA 手册）

1. 分别启动三节点（v4.1.7），各自 `network_enabled=true`、派生 NodeID、信任池启用。
2. 在 A 的 `/api/network/peers`（POST）添加 B 的地址；观察 A 日志出现向 B `/api/network/peers/notify` 的回发。
3. 在 B 用 `GET /api/network/peers` 确认含 A；确认 B **没有**向 A 回发 notify（查 B 日志无 `-> A` 出站 notify）。
4. 让 C 注册进 GitHub 信任池（或经种子发现），等待 A/B 下一轮 gossip（默认 30s）。
5. 在 A、B 分别 `GET /api/federation/pool` 或管理面板确认含 C 节点；`GET /api/network/peers` 间接可见性通过联邦路由表确认。
6. 检查任一节点日志：gossip 请求 URL 全部 `/api/federation/*`，且无 403（X-Node-ID 命中信任池）。

### 3.4 失败判据

- A 添加 B 后 B 未出现在本地的 `config.Peers` → P0-1 失败。
- A、B 互相回发 notify 形成 ping-pong（日志出现循环出站）→ R1 失败。
- C 在 A/B 经多轮 gossip 后仍不可见 → P0-2 / P1-1 主闭环失败。
- 任一节点日志出现 `POST to https://.../federation/gossip` 缺 `/api` → R5 回归。

---

## 4. 质量门禁（CI）

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -w` 仅改动 Go 文件 | 无 diff 残留 |
| 构建 | `go build ./...` | 0 error |
| 静态 | `go vet ./...` | 0 新增 warning |
| 特性单测 | `go test discovery_notify_test.go discovery_bridge_test.go discovery_gossip_test.go` | 全部 PASS |
| 全量 | `go test ./...` | 通过（Windows 下 3 个已知文件权限/锁失败为预期，Linux CI 全绿） |

---

## 5. 一致性复核（IS_PASS）

最终人工/自动复核清单：

- [ ] notify 接收端**不**回发（R1 防环结构成立）
- [ ] 手动 peer 桥接入信任池且 `TrustPoolVersion` 递增（P0-2）
- [ ] 所有 gossip 出站 URL 为 `/api/federation/*` 且带 `X-Node-ID`（R5）
- [ ] `GossipMessage.KnownPeers` 在 `doGossipRound` / `handleFederationGossip` 填充（P1-1）
- [ ] `processGossipResponse` 把对端 `KnownPeers` 并入 `fed.discoveryHints`（P1-1）
- [ ] `main.go` `AppVersion = "4.1.7"`；`scripts/omp-manager.ps1` 兜底 `v4.1.7`
- [ ] 无 `TODO` / 占位 / `pass` / `...`
