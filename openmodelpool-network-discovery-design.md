# OpenModelPool P2P 网络发现与 Gateway 架构设计

> **版本：v4.0.1** | 2026-07-12
>
> **v4.0.1 变更**：从 v2.1 升级，与 `ModelMux_P2P_Architecture.md` V4 架构全面对齐。整合 DHT 256-bit 哈希空间、独立 GossipManager、ReputationManager、LAN Discovery 等已实现模块。明确去中心化三层节点发现架构（Peer Seed + DHT + Gossip）。移除 Cloudflare Tunnel 引用，统一为 FRP/ngrok 双隧道部署方案。

---

## 目录

1. [核心思路](#1-核心思路)
2. [节点模型：统一 Peer](#2-节点模型统一-peer)
3. [BT 互惠共享模型](#3-bt-互惠共享模型)
4. [全路由节点设计](#4-全路由节点设计)
5. [三层节点发现架构](#5-三层节点发现架构)
6. [DHT 节点发现（Kademlia）](#6-dht-节点发现kademlia)
7. [Gossip 协议](#7-gossip-协议)
8. [LAN 局域网发现](#8-lan-局域网发现)
9. [声誉系统](#9-声誉系统)
10. [路由权重：五维模型](#10-路由权重五维模型)
11. [Seed 端点与冷启动](#11-seed-端点与冷启动)
12. [DNS 与统一入口寻址](#12-dns-与统一入口寻址)
13. [安全与恶意节点防护](#13-安全与恶意节点防护)
14. [渐进去中心化路线图](#14-渐进去中心化路线图)
15. [代码实现状态](#15-代码实现状态)

---

## 1. 核心思路

借鉴 BT（BitTorrent）网络的互惠共享理念 + 比特币 P2P 网络的节点发现机制，设计一套**互惠共享、渐进去中心化**的算力共享网络。

### 核心理念：像 BT 一样共享，不收过路费

```
Node A 有 gpt-4o，Node B 有 claude-3
     │                          │
     └──── 加入共享网络 ────────┘
                │
     双方共享资源池，互相免费使用对方的算力
     转发请求不收过路费，跟 BT 传数据一样
```

### 整体演进路径

```
Phase 0（冷启动）       Phase 1（网络形成）       Phase 2（自治网络）
━━━━━━━━━━━━━━━       ━━━━━━━━━━━━━━━━━━       ━━━━━━━━━━━━━━━━━
GitHub 注册表引导       Peer Seed + DHT 发现      完全自治
:8001 Seed 端点         Gossip 状态同步           DHT 自组织
FRP/ngrok 隧道          全路由 Gateway             无中心依赖

用户 → 项目域名          用户 → 任一 Seed/Gateway   用户 → 任一 Gateway
```

### 关键架构决策

| 决策 | 说明 |
|------|------|
| **P2P 节点发现** | FRP/ngrok 是部署层隧道方案，与产品层 P2P 节点发现无关。节点发现通过 DHT + Gossip + Seed 三层实现 |
| **去中心化** | 严禁中心化中继广播。所有节点平等，通过 Gossip 协议对等同步 |
| **声誉驱动路由** | 基于声誉/延迟/距离的节点权重计算，用于 DHT 路由选择 |
| **渐进式去中心化** | 初期通过 GitHub 注册表引导，最终实现完全去中心化 |

---

## 2. 节点模型：统一 Peer

所有节点都是对等的 peer，没有预设的类型区分。每个节点可以同时承担多种角色，角色由节点的能力声明和实际配置动态决定。

### 节点能力声明

```yaml
node_capabilities:
  providers:
    - model: claude-3-opus
      region: us-west
      quota: 1000
    - model: gpt-4
      region: us-east
      quota: 500
  network:
    bandwidth: high
    can_relay: true
    can_seed: true
  reputation_score: 0.85
  uptime_hours: 720
```

### 实际角色映射

| 实际角色 | 触发条件 | 说明 |
|---------|---------|------|
| **Consumer** | 任何节点发起请求时 | 所有节点都可以发起请求 |
| **Provider/Exit** | 节点拥有对应 API 且被选中时 | 拥有 API 能力的节点作为出口 |
| **Relay** | 节点声明 can_relay=true 且被选中时 | 带宽充足的节点中继流量 |
| **Seed** | 节点声明 can_seed=true 且在线时 | 所有节点默认都可作为 seed |

> **核心原则**：所有节点都是 seed。任何节点都可以为新加入的 peer 提供节点发现服务，无需硬编码的 Bootstrap 节点。

---

## 3. BT 互惠共享模型

### 3.1 核心理念

```
传统思路：我贡献算力 → 别人付费使用 → 我赚钱
BT 模式：我贡献算力 → 别人免费使用 → 我也免费使用别人的算力
```

**没有过路费，没有算力交易市场。** 纯粹互惠互助。

### 3.2 激励来自互惠

| 贡献 | 权益 |
|------|------|
| 不加入网络 | 只能用自己本地的 Provider Key |
| 加入，贡献 30% 额度到共享池 | 访问全网共享资源池 |
| 贡献 50% 额度 | 同上，吸引更多节点加入 |

### 3.3 额度模型

```
节点配置：
  - X% 额度 → 消费者（自己的 API Key 用户）
  - Y% 额度 → 共享网络（全网用户可用）
  X + Y = 100%
```

### 3.4 转发成本

谁提供算力谁承担——跟 BT 一样：

```
用户请求 claude-3，Node A 没有 → 转发到 Node B

Node B（Provider）：
  - 用自己的 Provider Key 调 Claude API
  - 消耗的是自己贡献给共享池的额度
  - 不向 Node A 收费，不向用户收费

Node A（Gateway）：
  - 只是帮忙转发，不额外消耗
  - 享受的是 Node B 也可能转发请求到 Node A 的模型
```

---

## 4. 全路由节点设计

### 4.1 核心概念

每个加入网络的节点，都可以成为全网入口：

```
用户 → https://my-node.chal.cc/v1
         │
         ▼
    my-node 收到请求
         │
         ├── 本地有该模型？ → 直接处理
         │
         └── 本地没有？ → 查 DHT 路由表 → 转发到最优节点
                          → 对用户完全透明
```

### 4.2 路由决策

```
1. 本地有该模型 → 直接处理（成本最低）
2. 本地没有 → 查 DHT 路由表，选最优远程节点：
   a. 五维权重计算（Trust 25% + Reputation 25% + Latency 20% + Availability 15% + Contribution 15%）
   b. 同级时随机选择（负载均衡）
3. 转发请求，返回结果给用户
```

### 4.3 认证与请求转发

```
用户 (sk-xxxx) → Node A (Gateway) → Node B (Provider) → Provider API
                        │                    │
                        └─ 内部转发认证 ──────┘
                           - 节点间通过 P2P 握手建立的信任
                           - 转发时附加: 来源节点、请求追踪 ID
                           - 不暴露用户的原始 Key
```

---

## 5. 三层节点发现架构

节点发现采用 **Peer Seed + Kademlia DHT + Gossip** 三层机制：

```
新节点加入
  │
  ├── Phase 1: Peer Seed 引导
  │   ├── 读取本地 peers.dat（上次缓存）
  │   ├── 缓存不足？查 GitHub 注册表
  │   ├── 连接 Seed 节点（:8001/api/peers）
  │   └── 获取已知节点列表
  │
  ├── Phase 2: DHT 路由表填充
  │   ├── FIND_NODE(自身ID)
  │   ├── 迭代查询，填充 K-Buckets
  │   └── 256-bit 哈希空间，k=20
  │
  └── Phase 3: Gossip 状态同步
      ├── PING 邻居节点
      ├── 交换状态向量（健康度/能力声明）
      └── 每 5 分钟增量同步
```

| 机制 | 用途 | 阶段 | 实现文件 |
|------|------|------|---------|
| Peer Seed | 初始引导，任意在线节点都可响应 | Phase 1–3 | `network_seed.go` |
| Kademlia DHT | 全局节点路由、能力注册 | Phase 2–3 | `dht.go` |
| Gossip 协议 | 实时状态传播（在线/离线/能力变化） | Phase 2–3 | `gossip.go` |
| LAN Discovery | 本地网络 UDP 广播发现 | Phase 1 | `discovery.go` |

### 初始 Seed 列表

新节点首次加入时配置初始 seed 列表：

```yaml
initial_seeds:
  - https://seed1.openmodelpool.com
  - https://peer.example.com:8000
```

一旦加入网络，通过 DHT 发现更多节点，不再依赖初始 seed。

---

## 6. DHT 节点发现（Kademlia）

### 6.1 设计参数

| 参数 | 值 | 说明 |
|------|------|------|
| 哈希空间 | 256-bit SHA-256 | 节点 ID 空间 |
| K-Bucket 大小 | k=20 | 每个桶最多 20 个节点 |
| 并发查找 | α=3 | 每次迭代查询 3 个节点 |
| 复制因子 | — | 关键数据存储在 k 个最近节点 |

### 6.2 核心操作

```go
// DHTTable 管理 Kademlia 路由表
type DHTTable struct {
    localID  string           // 本地节点 ID
    buckets  []*KBucket       // K-Bucket 数组（按距离排序）
    nodes    map[string]*KBucketEntry  // 快速查找
}

// 核心操作
AddNode(nodeID, endpoint)     // 添加节点到路由表
RemoveNode(nodeID)            // 移除节点
FindNode(nodeID)              // 查找最近节点
FindClosest(targetID, count)  // 查找 targetID 最近的 count 个节点
IterativeLookup(targetID)     // 迭代查找（α=3 并发）
GetSuccessors(n)              // 获取 n 个后继节点
GetPredecessors(n)            // 获取 n 个前驱节点
```

### 6.3 迭代查找流程

```
FIND_NODE(targetID):
  1. 从本地 K-Bucket 中找 α=3 个最接近 targetID 的节点
  2. 并发向这 3 个节点发送 FIND_NODE 请求
  3. 收集响应，更新 K-Bucket
  4. 如果发现了更近的节点 → 继续迭代
  5. 直到没有更近的节点 → 返回结果
```

---

## 7. Gossip 协议

### 7.1 核心设计

独立 GossipManager 实现去中心化状态同步：

| 组件 | 功能 |
|------|------|
| **去重缓存** | 已处理消息的 SHA-256 哈希缓存，防止重复处理 |
| **周期性 sync 轮次** | 定时选择随机 peer 进行状态交换 |
| **Provider 状态广播** | 广播 Provider 可用性变化 |
| **消息签名** | 所有 Gossip 消息 Ed25519 签名验证 |

### 7.2 Gossip 消息类型

| 消息 | 方向 | 内容 | 频率 |
|------|------|------|------|
| `PING` | 双向 | 节点 ID + 版本 + 时间戳 | 每 30s |
| `PONG` | 回复 | 确认 + 时间戳 | 收到 PING 即回 |
| `GOSSIP` | 双向 | 状态向量增量 | 每 5min |
| `ANNOUNCE` | 广播 | Provider 变更通知 | 事件触发 |

### 7.3 同步流程

```
Gossip Round:
  1. 从 DHT 路由表中随机选择 peer
  2. 构建 GossipMessage（本地 TrustPool 摘要 + 签名）
  3. 发送给选中 peer
  4. 收到响应 → 比对版本 → 合并增量
  5. 如果对方版本更新 → 拉取完整 TrustPool
  6. 消息去重 → 更新本地状态
```

### 7.4 API 接口

| 端点 | 方法 | 说明 |
|------|------|------|
| `POST /api/federation/broadcast` | 手动触发 Gossip 广播 |
| `POST /api/federation/peers/add` | 手动添加 DHT 种子节点 |
| `GET /api/federation/peers` | 获取 DHT 路由表中的节点列表 |
| `GET /api/federation/providers` | 从 P2P 网络获取远程 Provider |

---

## 8. LAN 局域网发现

### 8.1 UDP 广播发现

局域网内节点通过 UDP 广播自动发现彼此：

```
节点 A 启动
  │
  ├── 发送 UDP 广播（ANNOUNCE）
  │   → 255.255.255.255:端口
  │   → 包含: NodeID, 地址, 能力声明
  │
  └── 监听 UDP 广播
      → 收到其他节点的 ANNOUNCE
      → 添加到本地路由表
      → 回复 ACK
```

### 8.2 实现

`discovery.go` 中的 LAN 发现模块支持：
- UDP 广播发送和接收
- 从 GitHub 注册表拉取初始 TrustPool
- 从 Seed 节点获取已知节点列表
- ETag 条件请求（304 Not Modified）

---

## 9. 声誉系统

### 9.1 EWMA 多指标追踪

ReputationManager 通过 EWMA（指数加权移动平均）追踪每个节点的多维度指标：

| 指标 | EWMA 因子 | 说明 |
|------|----------|------|
| **Availability** | α=0.3 | 节点在线率 |
| **Latency** | α=0.3 | 响应延迟 |
| **Accuracy** | α=0.3 | 请求成功率 |

### 9.2 五级评级

| 等级 | 分数阈值 | 说明 |
|------|----------|------|
| **S** | ≥ 200 | 卓越节点，优先路由 |
| **A** | ≥ 100 | 优质节点 |
| **B** | ≥ 50 | 正常节点 |
| **C** | ≥ 20 | 需要改进 |
| **D** | < 20 | 观察期，7 天后可能移除 |

### 9.3 综合评分公式

```
综合分数 = 调用成功率×40% + 平均延迟×25% + 正常运行时间×20% + peer 评价×15%

EWMA 平滑: 新分数 = α × 本次评分 + (1-α) × 旧分数（α=0.3）
```

### 9.4 V4 信誉等级（架构文档补充）

| 评分区间 | 等级 | 出口权限 | 中继权限 | 信任权重 |
|---------|------|---------|---------|---------|
| 0.8–1.0 | Diamond | ✅ 全部模型 | ✅ 优先路由 | 1.0 |
| 0.6–0.8 | Gold | ✅ 非敏感模型 | ✅ 正常路由 | 0.8 |
| 0.4–0.6 | Silver | ⚠️ 受限模型 | ✅ 正常路由 | 0.5 |
| 0.2–0.4 | Bronze | ❌ 不可出口 | ⚠️ 低优先 | 0.2 |
| 0–0.2 | Untrusted | ❌ | ❌ | 0.0 |

---

## 10. 路由权重：五维模型

### 10.1 五维权重

LoadBalancer 使用五维权重模型选择最优节点：

| 维度 | 默认权重 | 说明 |
|------|---------|------|
| **Trust** | 25% | 来自 peer 交互的信任分 |
| **Reputation** | 25% | ReputationManager 计算的声誉分 |
| **Latency** | 20% | 网络延迟（EWMA 追踪） |
| **Availability** | 15% | 节点在线率/可靠性 |
| **Contribution** | 15% | 对网络的贡献度 |

### 10.2 计算公式

```go
score = trustScore * 0.25 +
        reputationScore * 0.25 +
        latencyScore * 0.20 +
        availabilityScore * 0.15 +
        contributionScore * 0.15
```

### 10.3 与旧版对比

| 维度 | 旧版（v3.x） | V4 |
|------|------------|------|
| 优先级 | 40% | 融入 Trust 维度 |
| 成本 | 25% | 融入 Contribution 维度 |
| 延迟 | 20% | 20%（保留） |
| Token 余额 | 15% | 融入 Availability 维度 |
| Trust | — | 25%（新增） |
| Reputation | — | 25%（新增） |
| Availability | — | 15%（新增） |
| Contribution | — | 15%（新增） |

---

## 11. Seed 端点与冷启动

### 11.1 Seed 复用模型

**每个 OpenModelPool 节点本身就是 Seed**，无需额外服务器：

| 端口 | 用途 | 说明 |
|------|------|------|
| :8000 | API 服务 | 处理请求 |
| :8001 | 节点发现 | Seed 端点，返回已知节点列表 |

```go
// :8001/api/peers — 每个节点都跑的 Seed 端点
func handleSeedPeers(w http.ResponseWriter, r *http.Request) {
    nodes := networkManager.GetKnownNodes()
    json.NewEncoder(w).Encode(nodes)
}
```

### 11.2 Seed 运维规则

- **心跳**：节点每 60 秒向已知 Seed 发送 PING
- **超时**：30 分钟无响应从路由表移除（`fail_count >= 3`）
- **扩容**：网络节点 ≥ 5 时，鼓励更多用户绑定域名成为 Seed
- **成本为零**：Seed 跟 API 服务跑在同一台机器上

### 11.3 GitHub 注册表引导

初期通过 GitHub 仓库维护节点注册表：

```json
// .nodes/{node_id}.json
{
  "node_id": "mmx-xxxx",
  "name": "Chal's Node",
  "url": "https://ai.chal.cc",
  "models": ["gpt-4o", "claude-3-opus"],
  "region": "us-east",
  "is_gateway": true,
  "is_seed": true,
  "last_heartbeat": "2026-07-12T16:05:00Z"
}
```

新节点启动时从 GitHub 拉取初始列表，之后通过 DHT + Gossip 发现更多节点，最终脱离对 GitHub 的依赖。

---

## 12. DNS 与统一入口寻址

### 12.1 两类用户

#### 纯消费者（使用项目域名）

```python
client = OpenAI(
    base_url="https://openmodelpool.com/v1",
    api_key="mk_public_v1"
)
```

#### 节点运营者（用自己的域名）

```python
client = OpenAI(
    base_url="https://my-node.chal.cc/v1",
    api_key="sk-my-key"
)
# 调 gpt-4o → 本地处理
# 调 claude-3 → 自动转发到全网
```

> **两种入口等价**：无论用项目域名还是自己的域名，用户都能访问全网模型。

### 12.2 部署层隧道

部署层面使用 FRP + ngrok 双隧道方案提供公网访问能力：

| 隧道 | 用途 | 说明 |
|------|------|------|
| FRP | 固定 IP 隧道 | 稳定公网 IP，适合 Seed 节点 |
| ngrok | 固定 HTTPS 域名 | 快速部署，适合测试和普通节点 |

> **注意**：FRP/ngrok 是部署层基础设施，与产品层 P2P 节点发现（DHT/Gossip/Seed）是完全独立的两层。

---

## 13. 安全与恶意节点防护

### 13.1 恶意节点防护

| 风险 | 防御 |
|------|------|
| 虚假节点注册 | PING-PONG 握手验证可达性 |
| 伪造模型列表 | 请求失败后降低 reputation，多次失败后摘除 |
| Gateway 拒绝服务 | 请求超时后自动切换下一个节点 |
| 路由投毒 | 每个节点独立验证，不盲目信任 peer 信息 |
| DDoS 攻击 | WAF 四层防护 + 限流 |
| 白嫖 | 额度分配模型限制：共享池额度用尽即止 |
| Sybil 攻击 | 邀请链信任传递 + IP /24 子网限制 + 声誉门槛 |

### 13.2 Sybil 防御（纵深防御）

| 防御层 | 机制 | 原理 |
|--------|------|------|
| 经济成本 | 邀请码绑定 | 增加身份创建成本 |
| 社交信任图 | Web of Trust | 信任沿邀请链衰减 |
| 行为分析 | 请求模式指纹 | 检测异常行为分布 |
| IP 限制 | /24 子网最多 3 节点 | 防止单机批量注册 |
| 声誉门槛 | 低声誉节点流量受限 | 新身份无法立即获得高权限 |

### 13.3 信任模型

```
用户信任 Gateway → Gateway 不篡改请求内容（纯透传）
                  → Gateway 不窃取 API Key

节点信任 peer 的模型声明 → 实际请求时验证
                         → 失败后自动降级
```

---

## 14. 渐进去中心化路线图

### Phase 0：冷启动（当前 ✅）

```
目标：建立初始网络，验证核心流程

- [x] :8001 Seed 端点（/api/peers）
- [x] DHT 256-bit 哈希空间 + K-Buckets
- [x] Gossip 协议（去重缓存 + 周期性 sync）
- [x] LAN 局域网 UDP 发现
- [x] ReputationManager（EWMA + S/A/B/C/D 评级）
- [x] 五维路由权重模型
- [x] FRP + ngrok 双隧道部署
- [ ] GitHub 注册表引导（trust_pool.json）
- [ ] peers.dat 持久化
```

### Phase 1：网络形成（3-6 个月）

```
目标：实现完整 Gossip 节点发现，Gateway 自动加入

- [ ] 独立 GossipManager 整合到 network.go 体系
- [ ] DHT 与 RouteTable 合并（升级为完整 AddrMan）
- [ ] 节点绑定域名后自动注册为 Gateway
- [ ] 全路由：Gateway 转发请求到全网节点
- [ ] DNS Manager 自动更新 DNS A 记录
- [ ] Provider 广播（Gossip 传播 Provider 状态变化）
```

### Phase 2：自治网络（6 个月+）

```
目标：移除中心化依赖，网络完全自治

- [ ] Seed 节点降级为普通 Gateway
- [ ] 节点发现完全依赖 DHT + Gossip
- [ ] Gateway 选举：五维权重综合评分
- [ ] 探索纯 P2P 寻址（DHT 替代 DNS）
- [ ] 联邦治理协议
- [ ] 完全无中心依赖运行
```

---

## 15. 代码实现状态

### 核心网络模块

| 文件 | 行数 | 功能 | 状态 |
|------|------|------|------|
| `dht.go` | 775 | Kademlia DHT：256-bit 哈希空间、K-Buckets(k=20)、迭代查找(α=3) | ✅ 已实现 |
| `gossip.go` | 604 | Gossip 协议：去重缓存、周期性 sync、Provider 状态广播 | ✅ 已实现 |
| `discovery.go` | 262 | LAN 局域网 UDP 广播发现 + GitHub 注册表 | ✅ 已实现 |
| `reputation.go` | 456 | 节点声誉：EWMA 追踪、S/A/B/C/D 评级 | ✅ 已实现 |
| `network.go` | 1217 | RouteTable、NetworkManager 主框架 | ✅ 已实现 |
| `network_discovery.go` | 589 | 心跳、简易 Gossip（peers 附带）、LAN 发现 | ✅ 已实现 |
| `network_relay.go` | 718 | WebSocket 信令、Gateway 路由 | ✅ 已实现 |
| `network_seed.go` | 350 | :8001 Seed 端点、/api/peers | ✅ 已实现 |
| `network_loadbalancer.go` | 789 | 五维权重框架 | ✅ 已实现 |
| `network_algorithm.go` | 881 | 算法链 | ✅ 已实现 |
| `network_global_pool.go` | 1177 | 全局池管理 | ✅ 已实现 |
| `network_balance.go` | 662 | 余额引擎 | ✅ 已实现 |
| `network_region.go` | 858 | 区域管理 | ✅ 已实现 |
| `network_quota.go` | 326 | 配额管理 | ✅ 已实现 |
| `network_keys.go` | 891 | 网络密钥 | ✅ 已实现 |

### 安全模块

| 文件 | 功能 | 状态 |
|------|------|------|
| `waf.go` | WAF 四层防护 | ✅ 已实现 |
| `token_estimator.go` | Token 精准估算 | ✅ 已实现 |
| `ratelimit.go` | 令牌桶限流 | ✅ 已实现 |
| `encryptor.go` | AES-256-GCM 加密 | ✅ 已实现 |

### 待整合项

| 项目 | 说明 | 优先级 |
|------|------|--------|
| GossipManager 整合 | 将 `gossip.go` 的独立 GossipManager 整合到 `network.go` 体系 | P1 |
| DHT ↔ RouteTable 合并 | 将 `network.go` 的内存 map RouteTable 升级为 DHT-backed AddrMan | P1 |
| getReputationScore 接入 | `network_loadbalancer.go` 的 `getReputationScore()` 接入 ReputationManager 真实数据 | P1 |
| peers.dat 持久化 | DHT 路由表持久化到磁盘 | P2 |
| GitHub 注册表引导 | 启动时从 GitHub 拉取初始节点列表 | P2 |

---

## 附录 A：与 BT 网络类比

| 维度 | BT 网络 | OpenModelPool |
|------|--------|---------------|
| 共享什么 | 数据片段 | 算力（模型调用额度） |
| 激励 | 互惠：分享越多，下载越快 | 互惠：贡献越多，可用模型越多 |
| 收费 | 不收费 | 不收过路费 |
| 节点发现 | DHT + Tracker | Peer Seed + DHT + Gossip |
| 地址解析 | info hash → peer 列表 | 模型名 → Provider 节点列表 |
| 冷启动 | Tracker（中心化） | GitHub 注册表 + Seed 端点 |
| 成熟后 | 纯 DHT（去中心化） | 纯 DHT + Gossip（去中心化） |
| 地址持久化 | .torrent + DHT 缓存 | peers.dat |

## 附录 B：设计文档交叉引用

| 本文档章节 | 对应架构文档章节 |
|-----------|----------------|
| §2 节点模型 | `ModelMux_P2P_Architecture.md` §2.1 |
| §5 三层发现 | `ModelMux_P2P_Architecture.md` §2.2 |
| §9 声誉系统 | `ModelMux_P2P_Architecture.md` §3.1 |
| §10 路由权重 | `ModelMux_P2P_Architecture.md` §9.2 |
| §13 Sybil 防御 | `ModelMux_P2P_Architecture.md` §3.2 |
| §14 路线图 | `ModelMux_P2P_Architecture.md` §13 |

---

> **版本历史**:
> - v1.0 (2026-07-08): 初始 P2P 网络发现设计
> - v2.1 (2026-07-08): Seed 复用模型，放弃 Cloudflare Worker
> - v4.0.1 (2026-07-12): 全面对齐 V4 架构，整合 DHT/Gossip/Reputation/LAN Discovery，五维路由权重，渐进去中心化路线图
