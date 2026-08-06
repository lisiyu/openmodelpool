# PRD：OpenModelPool 私有节点联邦「自动发现补齐」（v4.1.7 增量）

> 角色：产品经理 许清楚（Xu）｜类型：**增量 PRD**（基于 v4.1.6）｜目标版本：**v4.1.7**
> 适用对象：架构师（系统设计）、工程师（实现）、QA（测试）
> 关联文档：`PRD-federation-v4.1.6.md`、`ARCH-federation-v4.1.6.md`

---

## 0. 增量元信息

| 字段 | 内容 |
|---|---|
| 类型 | **增量开发**（非从零；在 v4.1.6 已落地的「手动加 Peer / 邀请码 / 双向可达」之上补齐发现闭环） |
| 基线版本 | v4.1.6（`main.go` `AppVersion="4.1.6"`；`scripts/omp-manager.ps1` fallback `v4.1.6`） |
| 目标版本 | **v4.1.7** |
| 语言 | 简体中文 |
| 技术栈 | 纯 Go 后端 + 既有原生 JS 管理面板（`admin.html` / `admin-network.js`）；**不引入新依赖 / 新框架** |
| Project Name | `omp_federation_discovery_fill_v417` |
| 原始需求复述 | v4.1.6 让两个私有节点能"手动互连"，但联邦存在**两套不连通的成员存储**：手动 Peer 只写 `NetworkManager.config.Peers`，从不被 gossip 信任池看见；gossip 的发现传播只读 `federation.trustPool`+`localPeers`。后果：手动加节点是**单向、本地**的，且 gossip 对手动私有节点"无活跃节点可聊"，不会传递发现。用户实测 3 节点（.com 加 .cc；.io 加 .com+.cc）验证：.com 见不到 .io、.cc 见不到另外两个。本增量需：① 加节点时双向通知；② 手动 peer bridge 进信任池；③ gossip 传递发现（PEX），形成网状可见。 |
| 版本号升级 | **P0 发布门禁**：`main.go` `AppVersion` `4.1.6→4.1.7`；`scripts/omp-manager.ps1` fallback `v4.1.6→v4.1.7`。 |

---

## 1. 基线现状与根因（已用 Grep/Read 核实，非假设）

| # | 事实 | 位置（行号已核） | 现状 |
|---|---|---|---|
| S1 | `AddPeer(peer PeerInfo)` 只写 `nm.config.Peers` + `routeTable`，**从不写 `fed`** | `network.go:844` | 手动 peer 仅存在于 `config.Peers`，gossip 看不见 |
| S2 | `handleNetworkAddPeer` 手动加节点的 HTTP handler，调用 `netMgr.AddPeer(peer)`，**无 fed upsert、无反向通知** | `network.go:1415` | 加 B 只登记在 A 本地 |
| S3 | `FederationManager` 含 `trustPool TrustPool` + `localPeers map`；`GetActiveNodes()` 只读 `trustPool.Nodes` + `localPeers`，**不读 `config.Peers`** | `federation.go:92` / `:168` | gossip 候选集天然排除手动 peer |
| S4 | `UpdateTrustPool` 仅当 `pool.Version > 本地` 才应用；`UpdateNodeInfo` upsert 到 trustPool 或 localPeers | `federation.go:206` / `:223` | 本地手动节点无法进入候选 |
| S5 | `doGossipRound` 的 sync 消息仅带 `TrustPoolVersion` + `ScoreDigest`，**不带对端端点**；`selectPeers` 从 `fed.GetActiveNodes()` 挑目标 | `gossip.go:72` / `:105` | 手动 peer 永不被选为 gossip 目标 |
| S6 | `processGossipResponse` 仅当 `msg.TrustPoolVersion > 本地` 才 `fetchFullPoolFromPeer`；后者 `GET {addr}/federation/pool` 拉全量池 | `gossip.go:277` / `:305` | 版本不涨则不拉新节点 |
| S7 | `POST /api/network/peers` 注册为 `withAuth(handleNetworkAddPeer)`（`server.go:264`） | `server.go:264` | **A 的后端无法用管理员令牌调用 B 的同名接口** → 双向通知需独立鉴权端点 |
| S8 | `withFederationAuth` 放行规则：① 可信种子 GET `/federation/pool`；② `X-Node-ID` 命中信任池；③ 管理员 JWT；④ `X-Federation-Secret` | `federation.go:19` | gossip 的 `exchange`/`fetchFullPoolFromPeer` **发送零凭证** → 两私有非种子节点间默认 **403**（见风险 R5） |

> **根因结论**：OMP 联邦有**两套完全不连通的成员存储**——`config.Peers`（手动/UI，本地、单向）与 `fed.trustPool`+`localPeers`（GitHub registry + gossip，发现传播只读这份）。三块需求即对应打通这两套存储 + 让发现可传递。

---

## 2. 产品目标（一句话）

> **让手动互连的私有节点在联邦内"彼此可见且经 gossip 传递发现"，形成网状（mesh）而非单向名单，且加一次即双向互见、无需对端再手动加。**

---

## 3. 用户故事（3 条，对应 P0-1 / P0-2 / P1-1）

| ID | 角色 | 诉求 | 收益 |
|---|---|---|---|
| US1 | .io 的节点管理员 | 我在网络页粘贴 `.com` 地址并提交后，希望 **`.com` 也自动把我登记进它的节点列表**，不用 `.com` 管理员再手动加一次 | A↔B 立即互见，互连体验从"单向"变"双向" |
| US2 | .io 的节点管理员 | 我手动加 `.com` 后，希望这个节点**立即出现在 gossip 的活跃节点里**，并能把 `.com` 共享的 provider 传播给下游 | 手动 peer 不再是"孤岛"，纳入联邦发现平面 |
| US3 | .io 的节点管理员 | 我已加 `.com`、`.com` 已加 `.cc`，希望**下一轮 gossip 后我能自动学到 `.cc`**，无需我亲自加 `.cc` | 发现可传递，3+ 节点自然成网（mesh），运维零额外操作 |

---

## 4. 需求池（Requirements Pool）

> 优先级：P0=必须（Must）/ P1=应该（Should）/ P2=可选（Nice-to-have）

### P0（必须）

#### P0-1 双向通知（加节点时反向登记对端）

**目标**：A 通过 UI/API 加 B 时，A 在本地 `AddPeer(B)` 成功后，主动反向通知 B 把 A 登记进 B 的 `config.Peers`，使 A↔B **立即互见**。

| ID | 需求 | 验收标准 |
|---|---|---|
| P0-1a | **反向通知触发** | A 本地 `AddPeer(B)` 成功（`propagated==false`，即本端人类主动发起）后，A 后端向 B 的公网端点 `resolvePublicEndpoint(B.addresses)` 发送反向通知，携带 `A.node_id` + `A.name` + `A.addresses` + `A.pub_key` + `timestamp` + `signature` |
| P0-1b | **对端本地登记** | B 收到通知后执行 `AddPeer(A)`（含 P0-****bridge），成功后 A 出现在 B 的节点列表；B **不再向 A 回发通知**（见防环 R1） |
| P0-1c | **静默失败不阻塞** | 若 B 公网不可达 / 未开启 `shared`+`network_enabled` / 通知被拒，A 本地添加仍成功，仅记录 WARN 日志；**不回滚本地添加、不阻塞 UI 返回** |
| P0-1d | **通知端点鉴权** | 反向通知走**独立端点** `POST /api/network/peers/notify`（限流公开），由**源节点 ed25519 自签名**（`signature` 覆盖 `node_id+addresses+timestamp`）；接收方用请求体内 `pub_key` 验签后登记。**不再复用 `POST /api/network/peers`**（该接口 `withAuth` 管理员令牌，跨实例不可达，见 S7） |

> **防环规则（P0-1 硬要求，必须在实现中落地）**
>
> | 规则 | 内容 |
> |---|---|
> | **R1 终断性（根本）** | 经 `/peers/notify` 收到的添加请求，接收方**只做本地 `AddPeer`（含 bridge），绝不向原发起方再发反向通知**。从根本上阻断 A↔B 无限 ping-pong。 |
> | **R2 标记传导** | 反向通知体带 `propagated: true`（区别于人类 UI 主动发起的 `propagated: false`）。接收方据 `propagated` 执行 R1；即便未来扩展多级传播，也以该标记界定、不回发原发起方。 |
> | **R3 幂等去重** | `AddPeer` 以 `node_id` 为唯一键（v4.1.6 已有 NodeID 去重逻辑），重复添加幂等，天然防止重复登记与额外外发。 |
> | **R4 落库后外发** | 仅当本地 `AddPeer` 成功 **且** `propagated==false` 时，才向对端发反向通知；任一步失败则静默忽略，不阻塞本地添加。 |

#### P0-2 手动 Peer bridge 进信任池（打通两套存储）

**目标**：`AddPeer` 写入 `config.Peers` 的**同时**，把该 peer `upsert` 进 `fed.trustPool`（作为 `NodeInfo`，`status=active`），并 `TrustPoolVersion + 1`，使 gossip 的 `GetActiveNodes()` 能"看见"手动 peer，且版本号变化触发对端 `fetchFullPoolFromPeer` 拉到它。

| ID | 需求 | 验收标准 |
|---|---|---|
| P0-2a | **bridge upsert** | 新增 `fed.AddManualNode(peer PeerInfo)`：以 `peer.NodeID` 为键 upsert 进 `trustPool.Nodes`，映射 `NodeInfo{NodeID, Name, Addresses:peer.Addresses, Endpoint:peer.Addresses[0], Status:"active", LastSeen, PubKey:peer.PubKey}`；`trustPool.Version++` 并 `saveLocked()` 持久化 |
| P0-2b | **触发点** | `AddPeer`（`network.go:844`）在写 `config.Peers` 成功后调用 `fed.AddManualNode(peer)`；`RemovePeer` 同步从 trustPool 移除该 `node_id`（复用 `fed.RemoveNode`） |
| P0-2c | **进入 gossip 候选** | bridge 后 `fed.GetActiveNodes()` 返回包含该手动 peer（`status=active`），`selectPeers`（`gossip.go:105`）可将其选为 gossip 目标 |
| P0-2d | **版本驱动拉取** | `TrustPoolVersion + 1` 使对端在 `processGossipResponse`（`gossip.go:277`）检测到 `msg.TrustPoolVersion > 本地` 而 `fetchFullPoolFromPeer` 拉全量池，从而学到该手动 peer |
| P0-2e | **可达性字段必填** | bridge 的 `NodeInfo` 必须填充 `Addresses`（或 `Endpoint`），否则 `selectPeers` 会因 `Endpoint=="" && len(Addresses)==0` 将其排除（`gossip.go:115`） |

### P1（应该）

#### P1-1 gossip PEX（传递发现）

**目标**：gossip 的 `sync` 消息携带"已知 peer 端点列表"，并对 `config.Peers`/`trustPool` 任一变化导致的版本增长触发全量拉取，使 A 加 B、B 已知 C → A 下一轮 gossip 拉到 B 的池即学到 C（传递发现，成网）。

| ID | 需求 | 验收标准 |
|---|---|---|
| P1-1a | **GossipMessage 增加 PEX 字段** | `GossipMessage`（`types.go:541`）新增 `KnownPeers []PeerHint`（`node_id` + `addresses []string`）；`doGossipRound`（`gossip.go:72`）从**合并后的已知节点集**（`fed.GetActiveNodes()` + `config.Peers` 去重）填充；响应端（`handleFederationGossip` `gossip.go:441`）同样填充 |
| P1-1b | **端点提示落地** | 接收方在 `processGossipResponse` 中将 `KnownPeers` 合并进本地"发现提示表"（`node_id → []address`），用于增强 `fetchFullPoolFromPeer` / `exchange` 的地址可达性（当 trustPool 内 `endpoint` 字段缺失/失效时回退） |
| P1-1c | **版本驱动拉取（主线）** | 保持现有规则：`msg.TrustPoolVersion > 本地` → `fetchFullPoolFromPeer` 拉全量池（P0-2 已使手动 peer 进池并涨版本）；PEX 作为地址可达性加固，不依赖 trustPool.endpoint 正确 |

> **技术备注（供架构师）**：P0-2 的版本涨 + 既有 `fetchFullPoolFromPeer`（拉**全量**池，非单节点）已能在 A↔B gossip 时让 A 拉到含 C 的 B 全池，从而**天然实现传递发现**。P1-1 的价值在于：① 显式 PEX 端点列表让 `fetch`/`exchange` 在 `endpoint` 字段缺失时仍有可达地址；② 让 sync 自描述已知节点，降低对信任池字段完整性的依赖。两者互补，P1-1 不改变 P0-2 的版本驱动主链路。

### P2（可选 / 非必须）

| ID | 需求 | 说明 |
|---|---|---|
| P2-1 | **admin 网络页渲染发现来源** | 把 gossip 学到的 `localPeers`/trustPool 节点也渲染进网络页，标记「通过发现」，与手动 peer（标记「手动添加」）区分。仅增强可视化，不影响功能。 |

---

## 5. UI 设计要点

- **复用现有「添加节点」表单**，无需新表单。P0-1/P0-2 均为后端行为增强，**前端零改动**或仅保证 `addNetworkPeer()`（`admin-network.js`）在本地 `POST /api/network/peers` 成功后不阻断（反向通知由后端异步发起）。
- **P2 才涉及渲染改动**：网络页节点列表项增加来源标签（「手动添加」/「通过发现」）。
- 不新增配置项；沿用 v4.1.6 的 `federation_endpoint` / `public_domain` / `shared` + `network_enabled` 约定。

```
网络页（沿用 v4.1.6「添加节点」表单，本增量无表单改动）
┌─ 网络 ──────────────────────────────┐
│ 已连接节点                          │
│  openmodelpool.com   [🟢] [移除]    │  ← 手动添加 (P0-2 bridge 后亦进信任池)
│  openmodelpool.io    [🟢] [移除]    │  ← P0-1 反向通知后自动出现，无需手动加
│  openmodelpool.cc    [🟢] [移除]    │  ← P1-1 经 gossip 传递发现后自动出现
└─────────────────────────────────────┘
(P2) 来源标签：手动添加 / 通过发现
```

---

## 6. 效果推演（3 节点 mesh 如何形成，对齐用户实测场景）

**场景**：A=.io 加 B=.com、C=.cc；B=.com 加 A=.io、C=.cc；C=.cc 未加任何人（原实测 C 见不到另外两个）。

| 步骤 | 机制 | 结果 |
|---|---|---|
| 1 | A 加 B → P0-1 反向通知 B 登记 A；P0-2 bridge 使 A、B 互入对方 trustPool（`status=active`，版本各 +1） | A↔B 双向互见 |
| 2 | A 加 C、B 加 C → 同理，A、B 的 trustPool 含 C；A、B 的 `GetActiveNodes()` 现含 B 与 C | A、B 可选 B、C 为 gossip 目标 |
| 3 | A 下一轮 `doGossipRound` 选到 B → 交换 sync：A 版本（`含 B、C`）> B 部分？B 版本（`含 A、C`）> A → 双向 `fetchFullPoolFromPeer` 拉全量池 | A、B 相互补齐，均含 A/B/C |
| 4 | C 被 A、B 选为 gossip 目标（C 在其 ActiveNodes 中）→ C 收到 A/B 的 sync（`version > 0=C 的空池`）→ C `fetchFullPoolFromPeer(A或B)` 拉到含 A、B 的全量池 | **C 自动学到 A、B**，原「见不到另外两个」问题解决 |
| 5 | 后续 gossip 持续维持 mesh；任一对端掉线由心跳/状态更新反映 | 网状稳定可见 |

> 推演结论：P0-2（版本涨+进池）是发现闭环的**关键使能**；P0-1 解决"加一次即双向"；P1-1 加固地址可达性。**前置依赖见 R5**。

---

## 7. 待确认 / 已知风险

| # | 项 | 影响 | 建议 / 规则 |
|---|---|---|---|
| **Q1** | **genesis 约束仍在**：邀请码 redeem 要求两端 `NetworkID(genesis)` 一致；本次三块针对**手动加节点 + gossip** 路径，**不受 genesis 约束**（手动 peer 不校验 genesis） | 阻塞的是 P1-2 邀请码，不阻塞本增量 | PRD 明确：**本次不改 redeem 路径，Q1 仍适用邀请码，不适用于手动互连** |
| **Q2** | **对端可达性**：双向通知依赖 B 公网可达 `:8000`（经 `resolvePublicEndpoint` 解析的公网地址），且 B 须开启 `shared` + `network_enabled=true` | 否则 P0-1 反向通知失败 | 失败静默忽略（P0-1c）；UI 仍显示本地添加成功 |
| **Q3** | **防环规则** | P0-1 正确性硬要求 | 见 §4 P0-1 防环 R1–R4（终断性 / 标记传导 / 幂等 / 落库后外发） |
| **R4** | **通知端点鉴权**（S7） | `POST /api/network/peers` 为 `withAuth` 管理员令牌，A 跨实例不可达 | 采用独立签名端点 `POST /api/network/peers/notify`（限流公开 + 源节点 ed25519 自签名验签）；**不复用** `withAuth` 接口 |
| **R5** | **gossip 传输鉴权缺口（关键前置）**：`/federation/gossip` 与 `/federation/pool` 受 `withFederationAuth` 保护；而 gossip `exchange` / `fetchFullPoolFromPeer` **发送零凭证**（S8），两个私有非种子节点间默认 **403** | 若不解决，P0-2/P1-1 的发现链路**端到端不通**（A 无法向 B 发 gossip、无法拉 B 的池） | 推荐修复：① gossip 客户端在请求头附加 `X-Node-ID: <self.NodeID>`；② 因 P0-2 已把对端 bridge 进 `trustPool`，目标侧 `withFederationAuth` 的「X-Node-ID 命中信任池」路径（federation.go:34）即可放行。需架构师确认是否如此实现或增设「已知 Peer（`config.Peers`）免密只读 `/federation/pool`」放行。**此项为 P0-2/P1-1 生效的硬前置，建议列为同版本 P0 任务**。 |
| **R6** | **手动节点 pubkey 缺失** | 手动加节点时 `resolvePeerNodeID` 仅解析 `node_id`，未取 `pub_key`；bridge 的 `NodeInfo.PubKey` 可能为空，影响后续对该节点的 gossip 签名校验 | 可在 `resolvePeerNodeID` 或 bridge 时一并取 pubkey；或扩展公开 ping 端点返回 pubkey。列为实现细节，不阻塞 v4.1.7 连接/发现目标 |
| **R7** | **版本冲突/震荡** | 多节点同时 bridge 致 trustPool 版本频繁 +1，gossip 频繁全量拉取 | `fetchFullPoolFromPeer` 已有 `UpdateTrustPool` 的 `version>` 守卫去重；未见严重风险，T 阶段观测即可 |

---

## 8. 交付范围小结（给主理人转发用）

- **类型 / 版本**：增量 PRD；目标 **v4.1.7**（基线 v4.1.6）；纯 Go + 原生 JS，无新依赖。
- **根因（已代码核实）**：联邦两套不连通存储——`config.Peers`（手动/本地/单向）vs `fed.trustPool`+`localPeers`（gossip 只读）；`AddPeer` 不写 fed、`GetActiveNodes` 不读 config.Peers、`doGossipRound` sync 不带端点、`fetch` 零凭证（详见 §1 S1–S8）。
- **P0（必须）**
  - **P0-1 双向通知**：加 B 后反向通知 B 登记 A，A↔B 立即互见；走独立签名端点 `/api/network/peers/notify`（因 `POST /api/network/peers` 为 `withAuth` 不可跨实例）；**防环 R1–R4 为硬要求**（终断性 / 标记传导 / 幂等 / 落库后外发）。
  - **P0-2 bridge**：`AddPeer` 同步 upsert 进 `fed.trustPool`（`status=active`，`TrustPoolVersion+1`），打通两套存储、进入 gossip 候选、驱动对端拉全量池。**是发现闭环的关键使能**。
  - **发布门禁**：`main.go` + `scripts/omp-manager.ps1` 版本号升 `4.1.7`。
- **P1（应该）**
  - **P1-1 gossip PEX**：`GossipMessage` 增 `KnownPeers` 端点列表，加固 `fetch`/`exchange` 地址可达性；版本驱动拉取主链路由 P0-2 提供。
- **P2（可选）**：admin 网络页把「通过发现」的节点渲染出来（标记来源），前端可视化增强。
- **关键风险**：
  - **R5（硬前置）**：gossip 传输层 `/federation/gossip`、`/federation/pool` 当前拒绝零凭证请求，两私有非种子节点间默认 403，**会阻断 P0-2/P1-1 端到端生效**；建议同版本 P0 修复（gossip 客户端附加 `X-Node-ID`，借 P0-2 bridge 命中信任池放行）。
  - Q1 genesis 仅约束邀请码 redeem，不适用于本增量手动互连路径。
  - 双向通知依赖对端公网可达 + `shared`+`network_enabled`，失败静默忽略。
- **明确不改**：邀请码 redeem / genesis 校验路径（Q1 仍适用邀请码）。
