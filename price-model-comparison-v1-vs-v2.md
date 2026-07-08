# ModelMux 经济模型对比分析：v1 设计 vs v2.0 实现 vs Gateway 架构

> 生成时间：2026-07-08  
> 对比对象：  
> - **v1（昨晚设计版）**：`docs/ModelMux_P2P_Architecture.md` 第十章经济模型  
> - **v2.0（当前实现版）**：已落地的代码（`network_keys.go`、`credits.go`、`network.go` 等）  
> - **Gateway v2.0（今天讨论）**：BT 互惠共享模式，全路由节点 + Gossip 发现  

---

## 4.1 核心差异对比表

### 4.1.1 密钥与认证体系

| 维度 | v1（设计版） | v2.0（当前实现） | Gateway v2.0（讨论） |
|------|-------------|-----------------|-------------------|
| **密钥格式** | `mk_{consumer_id}.{payload}.{signature}` — Ed25519 签名 | `sk-{random}` / `sk-guest-{nid}.{random}` / `mk_public_v1` | 沿用 v2.0 格式，增加 Gateway 路由层 |
| **签名机制** | Ed25519 私钥签名 payload，公钥验证 | 无密码学签名，仅本地存储验证 | 无密码学签名（BT 模式隐式信任） |
| **密钥类型数** | 6 种：mk_签名、mk_trial、mk_open、ck-xxx、Consumer Key、Provider Key | 4 种：Proxy(sk-)、Guest(sk-guest-)、Public(mk_public_v1)、Provider Key | 4 种（保持 v2.0 不变） |
| **消费者使用方式** | `base_url="https://relay节点/network/NodeID/v1"` + 签名密钥 | `base_url="https://节点地址/v1"` + `mk_public_v1` 或 Proxy Key | `base_url="https://api.openmodelpool.com/v1"` 统一入口 |
| **密钥携带信息** | 丰富：sub/iss/quota/used/models/weight/exp | 最少：仅标识类型和签发节点 | 最少（保持 v2.0） |
| **跨节点验证** | DHT 获取公钥 → 验签 → 提取 payload | 无（依赖 P2P 握手的隐式信任） | 无（Gateway 统一代理，后端处理） |

### 4.1.2 经济模型与激励

| 维度 | v1（设计版） | v2.0（当前实现） | Gateway v2.0（讨论） |
|------|-------------|-----------------|-------------------|
| **核心逻辑** | 贡献积分 = 交换货币，精确记账 | 百分比分配（50/50），粗粒度控制 | BT 互惠：贡献算力 → 换取全网访问权 |
| **额度管理** | 冻结→扣减→透支→月度结转 | 无冻结，按百分比实时分配 | 无冻结，按贡献比例分配 |
| **贡献追踪** | 精确到 token 级别，全局账本同步 | `ContribRecord` 结构存在但未接入主流程 | 粗粒度：在线率 + 提供模型数 + 响应质量 |
| **信誉系统** | 4 维加权：在线率40% + 响应速度25% + 持续贡献20% + 投诉15% | `PeerInfo.TrustScore` 字段存在，但无自动计算 | 节点质量评估（延迟、可用性、模型覆盖） |
| **防搭便车** | 动态阈值解锁：必须贡献达到阈值才能消费全网 | 无（mk_public_v1 谁都能用） | 软约束：贡献越多 → 路由优先级越高 |
| **时间维度** | 月度削峰填谷，年均平滑 | 无 | 无 |
| **社交维度** | 直赠/限额赠/时效赠 | Guest Key 签发 + 撤销（仅签发/撤销，无额度/时效控制） | 保持 v2.0 Guest Key |
| **全局状态** | 需要所有节点维护一致的全局贡献积分池 | 无需全局状态 | 无需全局状态（Gossip 最终一致） |

### 4.1.3 网络架构

| 维度 | v1（设计版） | v2.0（当前实现） | Gateway v2.0（讨论） |
|------|-------------|-----------------|-------------------|
| **节点发现** | DHT (Kademlia) | 手动添加 Peer + Bootstrap 节点查询 | GitHub 注册表(种子源) + Gossip 协议扩散 |
| **路由方式** | 显式路由：消费者指定 `/network/{NodeID}/v1` | 显式路由：`/network/{NodeID}/v1` + RouteTable | 隐式路由：统一入口 → Gateway 自动选路 |
| **消费者入口** | 需知道目标 NodeID | 需知道目标 NodeID | 无需知道 NodeID（统一入口 or 自定义域名） |
| **中继机制** | 多跳中继（洋葱路由） | 单跳中继（`handleNetworkRelay`） | 单跳中继（Gateway 直连目标节点） |
| **路由表** | Kademlia DHT（O(log N) 查找） | 简化 `RouteTable`（内存 map + TTL） | Gossip 传播（最终一致） |
| **去中心化程度** | 高（密码学信任 + DHT） | 中（本地信任 + 手动发现） | 中-高（BT 模式 + Gossip + 无中心服务器） |
| **实现复杂度** | ⭐⭐⭐⭐⭐ 极高 | ⭐⭐ 低 | ⭐⭐⭐ 中等 |

### 4.1.4 综合评分

| 维度 | v1（设计版） | v2.0（当前实现） | Gateway v2.0（讨论） |
|------|-------------|-----------------|-------------------|
| **公平性** | ⭐⭐⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐ |
| **易用性** | ⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| **安全性** | ⭐⭐⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐ |
| **可落地性** | ⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| **可扩展性** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ |

---

## 4.2 优势与劣势分析

### v1（昨晚设计版）

#### ✅ 优势

| 优势 | 说明 |
|------|------|
| **密码学保证的跨节点信任** | Ed25519 签名确保密钥不可伪造，任何节点都能独立验证，无需信任第三方 |
| **精细的额度控制** | 冻结→扣减→透支→结转，完整的生命周期管理，杜绝超额消费 |
| **信誉系统防止搭便车** | 4 维信誉评分 + 动态阈值解锁，确保"先贡献后消费"的公平性 |
| **削峰填谷的时间维度** | 月度额度自动转积分，忙月可透支，年均平滑，提高全网利用率 |
| **社交分享维度** | 直赠/限额赠/时效赠，灵活的资源分配方式 |
| **精确的贡献追踪** | 全局账本 + 精确到 token 级别的记录，完全可审计 |

#### ❌ 劣势

| 劣势 | 说明 |
|------|------|
| **实现复杂度极高** | Ed25519 签名/验证 + DHT 公钥分发 + 全局账本同步 + 信誉计算，至少 3-6 个月开发 |
| **全局状态同步难题** | 所有节点需维护一致的全局贡献积分池，分布式一致性本身就是难题（CAP 定理） |
| **信誉分博弈空间** | 节点可能优化在线率/响应速度而非真实贡献，存在"刷分"风险 |
| **密钥格式复杂** | `mk_{consumer_id}.{payload}.{signature}` 对用户不友好，调试困难 |
| **过度设计** | 早期网络规模小（<100 节点），密码学信任没有实际必要 |
| **消费者体验差** | 需要知道目标 NodeID + 复杂的签名密钥，违反 OpenAI SDK 兼容原则 |
| **冷启动成本高** | 新节点必须先贡献才能获得消费权，阻碍网络增长 |

### v2.0（当前实现版）

#### ✅ 优势

| 优势 | 说明 |
|------|------|
| **极度简化** | 4 种 key + 百分比分配，核心逻辑 < 500 行代码 |
| **无需全局状态同步** | 每个节点独立管理自己的 Guest Key 和配额，无分布式一致性问题 |
| **用户友好** | `mk_public_v1` 直接使用，零门槛体验；Guest Key 格式简单 |
| **快速上线验证** | 核心 P2P 功能（relay + 路由表 + Guest Key）已可运行 |
| **OpenAI SDK 完全兼容** | `base_url + api_key` 标准格式，无需任何特殊处理 |
| **与现有系统兼容** | Provider 管理、多用户、路由策略等不受影响 |

#### ❌ 劣势

| 劣势 | 说明 |
|------|------|
| **缺少跨节点信任验证** | 依赖 P2P 握手的隐式信任，恶意节点可伪造 Guest Key |
| **无法精确追踪贡献与消费平衡** | 百分比分配是粗粒度的，无法知道 A 节点到底为 B 节点提供了多少服务 |
| **没有防搭便车机制** | 不贡献也能用 `mk_public_v1`，网络可能被"吸血" |
| **Gateway 路由缺少节点质量评估** | RouteTable 仅记录地址，无延迟/负载/信誉信息，选路随机 |
| **缺少社交分享维度** | Guest Key 无额度控制、无有效期、无子额度 |
| **NodeUnlock 机制半成品** | `NodeUnlockState` 结构存在但未与路由决策关联 |

### Gateway v2.0（今天讨论）

#### ✅ 优势

| 优势 | 说明 |
|------|------|
| **BT 式互惠理念清晰** | "贡献算力换访问权"而非赚钱，社区驱动而非商业驱动 |
| **消费者体验最佳** | 统一入口 `api.openmodelpool.com/v1`，完全无感知 |
| **无需密码学信任** | 利用现有 P2P 握手的隐式信任，降低实现复杂度 |
| **Gossip 发现足够实用** | 比 DHT 简单，比手动添加自动化，适合中等规模网络 |
| **渐进式演进** | 可以在 v2.0 基础上逐步增加 Gateway 功能，无需推倒重来 |

#### ❌ 劣势

| 劣势 | 说明 |
|------|------|
| **统一入口是单点** | `api.openmodelpool.com` 成为中心依赖，违反去中心化理念 |
| **贡献度量粗糙** | "贡献算力"难以精确定量，可能出现不公平 |
| **Gossip 最终一致的延迟** | 节点发现可能有数秒延迟，影响首次请求体验 |
| **GitHub 注册表的局限** | 依赖 GitHub 可用性，更新频率受限 |

---

## 4.3 代码保留/修改分析

### 📄 `network_keys.go` — 密钥管理系统

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | `KeyType` 枚举（Proxy/Guest/Public/Unknown） | v2.0 核心，继续使用 |
| ✅ 保留 | `ClassifyKey()` 函数 | 密钥分类逻辑完好 |
| ✅ 保留 | `GuestKeyStore` 结构及 CRUD 方法 | Guest Key 管理核心 |
| ✅ 保留 | `GenerateGuestKey()` / `ValidateGuestKey()` / `RevokeGuestKey()` | 核心功能完整 |
| ✅ 保留 | API Handlers: `handleGuestKeyIssue/List/Revoke` | 完整可用 |
| ✅ 保留 | `handleNetworkKeyValidate` | 密钥验证接口 |
| ✅ 保留 | Quota Allocation API handlers | 配额管理 API |
| 🔧 修改 | `GuestKeyRecord` 结构体 | ➕ 增加 `Quota int64`、`ExpiresAt string`、`Models []string` 字段 |
| 🔧 修改 | `ValidateGuestKey()` | 🔧 增加额度检查和过期验证 |
| 🔧 修改 | `GenerateGuestKey()` | 🔧 支持设置额度、有效期、模型白名单 |
| ❌ 删除 | `fetchPeerPublicKey()` 遗留 stub | 已无用的 Ed25519 遗留代码 |

### 📄 `credits.go` — 额度分配管理

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | `QuotaAllocation` 结构体 | 百分比分配模型核心 |
| ✅ 保留 | `AllocationManager` 及核心方法 | 运行时配额追踪 |
| ✅ 保留 | `RecordUsage()` / `GetUsageStats()` | 使用量统计 |
| ✅ 保留 | 持久化逻辑（save/load JSON） | 配置持久化完好 |
| 🔧 修改 | `RecordUsage()` | 🔧 增加按节点追踪（谁贡献了多少/消费了多少） |
| 🔧 修改 | `GetUsageStats()` | 🔧 返回更详细的贡献/消费比例数据 |
| ➕ 新增 | `ContributionTracker` 结构 | ➕ 追踪每个节点的历史贡献量，为 Gateway 路由选路提供数据 |

### 📄 `network.go` — 网络管理器

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | `NetworkManager` 核心结构 | 网络状态管理核心 |
| ✅ 保留 | `NetworkConfig` 结构体（大部分字段） | 网络配置核心 |
| ✅ 保留 | `PeerInfo` 结构体 | 节点信息基础 |
| ✅ 保留 | `RouteTable` 结构及方法 | 简化版路由表，Gateway 模式可复用 |
| ✅ 保留 | 共识/启用/禁用/配置更新 API | 网络管理 API 完整 |
| ✅ 保留 | `handleNetworkResolve` / `handleNetworkRoutes` | 节点解析/路由查看 |
| ✅ 保留 | `GetDisclaimer()` / consent 逻辑 | 风险告知流程 |
| 🔧 修改 | `RouteTable` | 🔧 增加 `LatencyMS`、`LoadScore`、`ModelsOffered` 字段用于 Gateway 选路 |
| 🔧 修改 | `RouteEntry` 结构体 | 🔧 增加节点质量指标 |
| 🔧 修改 | `RouteTable.Get()` | 🔧 增加按模型/延迟/负载的过滤逻辑 |
| 🔧 修改 | `GetStatus()` | 🔧 增加 Gateway 模式状态信息 |
| 🔧 修改 | `AddPeer()` | 🔧 增加 Gossip 消息触发的 peer 添加 |
| ❌ 删除 | `NodeUnlockState` 及相关方法 | 整个动态阈值解锁机制（~150 行死代码） |
| ❌ 删除 | `NetworkConfig.NodeUnlockStates` 字段 | 不再需要 |
| ❌ 删除 | `NetworkConfig.PublicKeys` 字段 | v2.0 已用固定常量替代 |

### 📄 `network_relay.go` — 中继处理器

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | `handleNetworkRelay()` 核心逻辑 | 中继路由核心 |
| ✅ 保留 | `handleRelayToLocal()` | 本地中继处理 |
| ✅ 保留 | `relayToRemote()` | 远程中继处理 |
| ✅ 保留 | `pickBestAddress()` | 地址选择逻辑 |
| ✅ 保留 | Hop count 防环机制 | `X-OpenModelPool-Agent-Hop` |
| ✅ 保留 | Key-based routing 逻辑 | Guest Key 限制访问签发节点 |
| 🔧 修改 | `handleNetworkRelay()` | 🔧 增加 Gateway 模式：当请求不指定 NodeID 时，自动选择最优节点 |
| 🔧 修改 | `relayToRemote()` | 🔧 增加基于延迟/负载/贡献比例的节点选择 |
| 🔧 修改 | `pickBestAddress()` | 🔧 结合节点健康状态选择地址 |
| ➕ 新增 | Gateway 路由入口 | ➕ 处理 `/v1/*` 请求的全网路由模式（消费者无需指定 NodeID） |
| ❌ 删除 | `queryBootstrapForNode()` | 将被 Gossip 协议替代 |

### 📄 `network_global_pool.go` — 全局计算池

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | `GlobalPoolNode` 结构体 | 节点参与信息 |
| ✅ 保留 | `GlobalPool` 核心结构 | 全局池状态 |
| ✅ 保留 | `JoinPool()` / `Contribute()` / `RecordConsumption()` | 贡献/消费记录 |
| ✅ 保留 | `SelectBestNode()` | 节点选择算法 |
| ✅ 保留 | `Heartbeat()` / `refreshLoop()` | 心跳机制 |
| 🔧 修改 | `SelectBestNode()` | 🔧 简化评分算法，移除信誉分依赖 |
| 🔧 修改 | API Handlers | 🔧 适配 Gateway 模式的入口 |
| ❌ 删除 | `globalKeyStore` 及相关方法 | v2.0 已用 `mk_public_v1` 固定常量替代 |
| ❌ 删除 | `CanSignGlobalKey()` | 不再需要签发全局密钥 |
| ❌ 删除 | `globalKeyDefaultQuota` / `globalKeyExpDays` 等常量 | 不再需要 |

### 📄 `types.go` — 类型定义

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | `Provider` / `APIKeyConfig` / `ProviderAccessControl` | Provider 核心类型 |
| ✅ 保留 | `ChatRequest` / `ChatResponse` / `ChatChunk` 等 | OpenAI 兼容类型 |
| ✅ 保留 | `NodeInfo` / `SharedProvider` / `TrustPool` | Federation 类型 |
| ✅ 保留 | `GossipMessage` / `PeerScore` | Gossip 协议类型 |
| ✅ 保留 | `ProviderAnnouncement` / `RelayRequest` / `RelayResponse` | 中继类型 |
| 🔧 修改 | `ProviderAccessControl` | 🔧 考虑增加 `AllowGateway bool` 字段 |
| ❌ 删除 | `CreditTransaction` / `NodeCredits` | 旧积分系统类型，v2.0 未使用 |
| ❌ 删除 | `FederationVote` / `PendingJoinRequest` | 旧的联邦投票机制，已被简化 |

### 📄 `main.go` — 主入口

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | 所有初始化调用 | 启动流程 |
| ✅ 保留 | 所有路由注册 | HTTP 路由 |
| ✅ 保留 | `withProxyAuth` 中间件 | 认证中间件，已正确处理 `mk_public_v1` |
| 🔧 修改 | 路由注册 | 🔧 增加 Gateway 模式的路由入口 |
| 🔧 修改 | `handleHealth` | 🔧 增加 Gateway 模式状态输出 |

### 📄 `admin.html` — 管理面板

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | 网络状态展示区域 | 基础网络信息 |
| ✅ 保留 | Guest Key 管理 UI | 签发/列表/撤销 |
| ✅ 保留 | 配额分配 UI | 百分比滑块 |
| ✅ 保留 | Provider 管理、使用量、日志等 | 核心管理功能 |
| 🔧 修改 | 网络状态面板 | 🔧 增加 Gateway 模式指示器、Gossip 节点发现状态 |
| 🔧 修改 | Guest Key 管理 | 🔧 增加额度设置、有效期设置 UI |
| ➕ 新增 | Gateway 模式控制面板 | ➕ 启用/禁用 Gateway、统一入口地址展示、Gossip 节点列表 |

### 📄 `admin.go` — 管理后端

| 类别 | 内容 | 说明 |
|------|------|------|
| ✅ 保留 | 所有 auth handlers | 登录/注册/密码重置 |
| ✅ 保留 | Provider CRUD handlers | 平台管理 |
| ✅ 保留 | Usage/Routing handlers | 使用量与路由 |
| ✅ 保留 | SMTP handlers | 邮件服务 |
| ✅ 保留 | Config import/export | 配置管理 |
| 🔧 修改 | 无重大修改 | admin.go 与网络经济模型无直接关联 |

---

## 4.4 渐进式迁移路径

```
Phase 0 (当前)          Phase 1                   Phase 2                  Phase 3
v2.0 已实现     →      Gateway 路由       →      Gossip 发现      →      信誉 + 贡献追踪
先跑起来               全路由节点功能             节点发现协议              可选：精细控制
```

### Phase 0（当前 — v2.0 已实现）✅

**已完成：**
- 4 种 Key 类型体系（Proxy / Guest / Public / Provider）
- 百分比配额分配（50/50）
- Guest Key 管理（签发/撤销/验证）
- P2P Relay 中继（单跳）
- RouteTable 简化路由表
- `mk_public_v1` 公共试用 Key
- Provider Access Control

**待清理：**
- 删除 `NodeUnlockState` 相关代码（~150 行死代码）
- 删除 `globalKeyStore` 遗留 stub
- 删除 `fetchPeerPublicKey()` 遗留 stub
- 清理 `CreditTransaction` / `NodeCredits` 未使用类型

### Phase 1（Gateway 路由 — 预估 2-3 周）

**目标：** 实现全路由节点功能，消费者无需指定 NodeID

**核心任务：**

1. **Gateway 路由入口**
   - 新增 `/gateway/v1/*` 路由（或扩展现有 `/v1/*`）
   - 消费者使用 `base_url="https://api.openmodelpool.com/v1"` 统一入口
   - Gateway 节点接收请求后，根据模型/延迟/负载自动选择最优节点

2. **增强 RouteTable**
   ```go
   type RouteEntry struct {
       NodeID       string
       Addresses    []string
       Models       []string    // 新增：该节点提供的模型
       LatencyMS    float64     // 新增：平均延迟
       LoadScore    float64     // 新增：当前负载 (0-1)
       ContribRatio float64     // 新增：贡献/消费比
       LastSeen     time.Time
   }
   ```

3. **智能选路**
   - 基于模型匹配 → 延迟 → 负载 → 贡献比例的加权选路
   - 复用 `GlobalPool.SelectBestNode()` 的评分逻辑

4. **贡献/消费追踪**
   - 在 `AllocationManager` 中增加按节点追踪
   - 每次 relay 记录：哪个节点为哪个请求贡献了多少 token

**改动文件：**
- `network_relay.go`：新增 Gateway 路由入口
- `network.go`：增强 RouteTable
- `credits.go`：增加按节点追踪
- `main.go`：新增路由注册
- `admin.html`：Gateway 控制面板

### Phase 2（Gossip 发现 — 预估 2-3 周）

**目标：** 自动化节点发现，替代手动添加 Peer

**核心任务：**

1. **GitHub 注册表（种子源）**
   - 类似 .torrent 文件，存储初始节点列表
   - 定期从 GitHub 拉取最新注册表
   - 新节点注册到 GitHub 注册表

2. **Gossip 协议实现**
   - 节点间定期交换 Peer 列表
   - 新增节点通过 Gossip 扩散到全网
   - 节点下线通过 TTL 自然淘汰

3. **Heartbeat 增强**
   - 心跳消息携带：NodeID、地址列表、提供的模型、延迟指标
   - 接收心跳更新本地 RouteTable

4. **替代 `queryBootstrapForNode()`**
   - 删除对 Bootstrap 节点的 HTTP 查询
   - 使用 Gossip 协议进行节点发现

**改动文件：**
- 新增 `gossip_discovery.go`：Gossip 协议实现
- `network.go`：集成 Gossip 触发 peer 添加
- `network_relay.go`：删除 `queryBootstrapForNode()`

### Phase 3（信誉 + 贡献追踪 — 可选，预估 3-4 周）

**目标：** 如果社区需要更精细的公平性控制

**核心任务：**

1. **轻量信誉系统**
   ```go
   type NodeReputation struct {
       NodeID       string
       UptimePct    float64   // 在线率
       AvgLatencyMS float64   // 平均延迟
       ContribRatio float64   // 贡献/消费比
       Age          int       // 加入天数
       Score        float64   // 综合分 (0-1)
   }
   ```
   - 不使用 v1 的 4 维复杂计算
   - 简化为：在线率 + 贡献比 + 延迟，3 个维度

2. **贡献驱动的选路**
   - 贡献比高的节点 → 路由优先级更高
   - 纯消费节点 → 路由优先级降低（但不完全拒绝）

3. **Guest Key 增强**
   - 增加额度控制（`Quota` 字段）
   - 增加有效期（`ExpiresAt` 字段）
   - 增加模型白名单（`Models` 字段）

---

## 4.5 总结与建议

### 优先级矩阵

| 功能 | 优先级 | 建议 | 理由 |
|------|--------|------|------|
| **清理死代码** | 🔴 P0 | **立即执行** | 删除 NodeUnlock、globalKeyStore stub 等，减少维护负担 |
| **Gateway 路由入口** | 🔴 P0 | **尽快实现** | 这是消费者体验的核心——统一入口，无需知道 NodeID |
| **增强 RouteTable** | 🟡 P1 | **Phase 1 核心** | 增加模型/延迟/负载字段，为智能选路奠基 |
| **贡献/消费追踪** | 🟡 P1 | **Phase 1 核心** | 知道谁贡献了多少，是公平调度的基础 |
| **Gossip 节点发现** | 🟢 P2 | **Phase 2** | 自动化发现，但手动添加在早期足够 |
| **Guest Key 增强** | 🟢 P2 | **Phase 2-3** | 额度/有效期/模型白名单，社交分享维度 |
| **轻量信誉系统** | 🔵 P3 | **Phase 3 可选** | 等社区规模 >50 节点后再考虑 |
| **Ed25519 签名密钥** | ⚪ 放弃 | **不实现** | 过度设计，BT 模式的隐式信任足够 |
| **全局贡献积分池** | ⚪ 放弃 | **不实现** | 分布式一致性成本太高，收益不明显 |
| **动态阈值解锁** | ⚪ 放弃 | **不实现** | 阻碍网络增长，用贡献驱动选路替代 |
| **密钥冻结机制** | ⚪ 放弃 | **不实现** | 实现复杂，百分比分配已足够 |
| **时间维度削峰填谷** | ⚪ 放弃 | **不实现** | 需要全局账本，成本过高 |
| **多跳中继/洋葱路由** | ⚪ 放弃 | **不实现** | 单跳足够，多跳延迟不可接受 |

### 核心决策建议

1. **v1 设计中应该放弃的部分：**
   - Ed25519 签名密钥体系 → BT 模式的隐式信任已足够
   - 全局贡献积分池 → 分布式一致性成本过高
   - 动态阈值解锁 → 阻碍增长，用软约束（路由优先级）替代硬约束
   - 密钥冻结 → 百分比分配已足够简单有效
   - 时间维度削峰填谷 → 需要全局状态，ROI 不高

2. **v1 设计中值得保留的思想：**
   - 贡献追踪（但简化为粗粒度）→ Phase 1 实现
   - 信誉系统（但简化为 3 维）→ Phase 3 可选实现
   - 密钥分享（但简化为 Guest Key 增强）→ Phase 2-3

3. **v2.0 应该保留的核心：**
   - 4 种 Key 类型体系 ✅
   - 百分比分配模型 ✅
   - Guest Key 管理 ✅
   - P2P Relay 中继 ✅
   - RouteTable 路由表 ✅

4. **Gateway v2.0 的核心价值：**
   - 统一入口（消费者无感知）→ **最高优先级**
   - BT 互惠理念（贡献换取访问权）→ 通过路由优先级实现
   - Gossip 发现（自动化节点发现）→ Phase 2

### 一句话总结

> **放弃 v1 的密码学精确性，保留 v2.0 的极简实用，用 Gateway 统一入口提升消费者体验，用贡献驱动选路实现 BT 式互惠——这是一个"足够好"的方案，能在 2-3 个月内落地，而不是 6-12 个月的过度设计。**
