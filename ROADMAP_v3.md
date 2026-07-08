# OpenModelPool Agent v3.0 路线图：去中心化联邦网络

## 核心愿景

多个独立部署的 openmodelpool 实例组成去中心化的 AI 模型联邦。API Key 永远不出本机，共享的是"代理中继能力"。GitHub 仓库作为权威 Registry，Gossip 协议提供实时同步，社区联合治理准入与信誉。

---

## 架构总览

```
                ┌──────────────────┐
                │   GitHub 仓库    │  ← 宪法层（权威）
                │ trust_pool.json  │
                └────────┬─────────┘
                         │
              ┌──────────┼──────────┐
              │ 管理员审核 │ 社区联合审核│
              │ (一票制)   │ (投票制)  │
              └──────────┼──────────┘
                         │
                    审核通过 → 种子节点
                         │
              ┌──────────┼──────────┐
              ▼                     ▼
     ┌──────────────┐     ┌──────────────┐
     │ Gossip 传播   │     │ 信誉评分系统  │  ← 运行层
     │ 实时同步状态   │     │ 互相打分评价  │
     └──────┬───────┘     └──────┬───────┘
            └──────────┬──────────┘
                       ▼
              ┌──────────────────────┐
              │   Provider 自发现    │  ← 数据层
              │ 新模型自动广播全网    │
              └──────────────────────┘
```

**核心原则：**
- GitHub 是最终权威，日常运行靠 Gossip
- 早期用户有更大话语权，但权重随时间衰减
- 信誉是核心货币，贡献越多权力越大
- 审核去中心化，管理员保留最终否决权

---

## Phase 1：联邦基础架构

### 1.1 节点身份系统

**NodeID 生成：**
- 每个 openmodelpool 实例生成 Ed25519 密钥对
- NodeID = `"mm-" + base58(pubkey[:16])`
- 私钥存储：`data/node.key`（AES-256-GCM 加密）
- 公钥用于签名验证，节点间通信身份认证

**节点信息结构：**
```json
{
  "node_id": "mm-abc123def456",
  "github_user": "lisiyu",
  "github_id": 12345678,
  "endpoint": "https://mm.lisiyu.dev",
  "pub_key": "ed25519:Ak3x...",
  "shared_models": ["gpt-4o", "claude-3.5-sonnet"],
  "shared_providers": [
    {"platform": "coze", "models": ["gpt-4o"], "capacity": 85}
  ],
  "joined_at": "2026-07-01T00:00:00Z",
  "last_seen": "2026-07-07T13:45:00Z",
  "status": "active",
  "seed_node": true,
  "reputation": 186,
  "version": "3.0.0",
  "invite_by": null
}
```

**新增文件：**
- `node.go` — 节点身份、密钥管理、签名验签
- `federation.go` — 联邦网络核心逻辑

### 1.2 GitHub-Native Registry

**核心设计：零额外部署成本**
```
openmodelpool/ (GitHub 仓库)
├── federation/
│   ├── trust_pool.json      ← 活跃节点列表（客户端读取）
│   ├── pending/             ← 待审核申请
│   │   └── mm-abc123.json
│   └── archive/             ← 已退出/撤销的节点归档
```

**客户端三层获取策略（自动降级）：**
1. GitHub Raw：`raw.githubusercontent.com/lisiyu/openmodelpool/main/federation/trust_pool.json`
2. GitHub Pages：`lisiyu.github.io/openmodelpool/federation/trust_pool.json`
3. 已知活跃节点 P2P 互备
4. 本地缓存兜底

**更新频率：** 5 分钟，带 ETag 增量更新，无变化时 304

**管理员 CLI：**
```bash
openmodelpool admin network pending          # 查看待审核
openmodelpool admin network verify mm-xxx    # 验证节点连通性
openmodelpool admin network approve mm-xxx   # 批准 → 自动 commit + push
openmodelpool admin network reject mm-xxx    # 拒绝
openmodelpool admin network revoke mm-xxx    # 撤销已加入节点
```

### 1.3 Gossip 传播协议

**传播内容：**
- 信任池版本更新（新节点加入/移除）
- 信誉评分交换
- Provider 公告（新增/移除/容量变化）
- 心跳状态

**Gossip 循环：**
```go
// 每 30 秒执行一次
func gossipLoop() {
    peers := selectPeers(3)  // 种子节点优先
    for _, peer := range peers {
        resp := peer.Exchange(GossipMsg{
            TrustPoolVersion: local.Version,
            ScoreDigest:      computeDigest(),
            Timestamp:        now(),
            Signature:        sign(msg),
        })
        if resp.Version > local.Version {
            fetchDelta(peer, resp.Version)
        }
    }
}
```

**防循环：** 每条消息带签名哈希，已见过的不重复转发

**冲突解决：** GitHub trust_pool.json 为最终权威，Gossip 为加速层

### 1.4 Provider 中继模式

**请求链路：**
```
用户请求 → 本地 openmodelpool → 选择路由：
  ├─ 本地 Provider → 直接调用 → 返回结果
  └─ 远程共享 Provider → 请求发给远端 Node（带签名）
       → 远端 Node 验证签名
       → 用自己的 Key 调用实际 API
       → 结果原路返回
```

**中继端点：**
```
POST /federation/relay
Headers:
  X-Node-ID: mm-xxx
  X-Signature: ed25519:xxx
  X-Timestamp: 2026-07-07T14:00:00Z

Body: { 标准 OpenAI 请求格式 }

远程节点：
1. 验证签名 + 时间戳（防重放，5分钟窗口）
2. 检查请求方在信任池中
3. 选择本地 Provider 调用
4. 返回结果（流式/非流式透传）
```

**中继节点配置：**
- 是否启用中继模式（默认关闭）
- 最大并发中继请求数
- 速率限制（req/min per node）
- 白名单（仅允许特定节点使用中继）

### 1.5 Provider 自发现

**Provider 公告消息：**
```json
{
  "type": "provider_announce",
  "node_id": "mm-xxx",
  "provider_id": "p-coze-1",
  "platform": "coze",
  "models": ["gpt-4o", "claude-3.5-sonnet"],
  "capacity": 85,
  "timestamp": "2026-07-07T14:00:00Z",
  "signature": "ed25519:xxx"
}
```

**流程：**
1. 节点添加新 Provider → 自动生成公告
2. 立即广播给所有已知节点
3. 后续 Gossip 交换中继续传播
4. 接收方验签 → 更新本地共享模型路由表

---

## Phase 2：信誉与治理

### 2.1 信誉评分系统

**评分维度（自动 + 手动）：**

| 维度 | 权重 | 来源 |
|------|------|------|
| 可用性 | 40% | 调用成功率自动统计 |
| 延迟 | 30% | 响应时间自动统计 |
| 准确性 | 20% | 返回格式/模型匹配度 |
| 社区评价 | 10% | 节点管理员手动评分 |

**信誉等级：**
| 等级 | 分数 | 效果 |
|------|------|------|
| S | ≥200 | 种子节点候选，最高路由优先级 |
| A | ≥100 | 活跃节点，正常路由 |
| B | ≥50 | 普通节点，降低路由权重 |
| C | ≥20 | 观察期，仅作为 fallback |
| D | <20 | 自动踢出信任池 |

**降级保护：**
- 新节点前 30 天保护期
- 单日信誉下降 >50% 触发告警
- 节点可发起 GitHub Issue 申诉

### 2.2 多节点联合审核

**审核权重模型：**
```
最终权重 = 基础权重(1.0) × 早期加成 × 信誉加成

早期加成：
  第一个月加入: ×3.0
  第二个月: ×2.0
  第三个月: ×1.5
  之后: ×1.0

信誉加成：min(reputation/100, 2.0)
```

**审核通过条件（满足任一）：**
1. 管理员 approve（一票制）
2. ≥3 个种子节点 approve，且加权投票总分 ≥ 阈值
3. 72 小时内无反对票 + ≥2 票赞成

**审核拒绝条件：**
1. 管理员 reject
2. ≥2 个种子节点 reject
3. 自动验证不通过

### 2.3 GitHub 账户验证

**验证流程：**
1. `openmodelpool network verify-github` → 生成 challenge token
2. 打开浏览器 → GitHub OAuth 授权
3. 验证：账户年龄 >30 天、至少 1 个公开仓库
4. 绑定 GitHub 身份到 NodeID（签名写入节点配置）

**约束：**
- 同一 GitHub 账户只能注册 1 个节点
- 审核节点必须验证 GitHub 身份

### 2.4 防恶意注册

| 层级 | 措施 |
|------|------|
| GitHub 门槛 | 账户年龄>30天、有公开仓库、1账户=1节点 |
| 能力验证 | 连通性+API兼容性+版本检测+至少1个Provider |
| 邀请链约束 | 初始3邀请码、邀请人连带责任（-30信誉） |
| 速率限制 | 每IP每小时5次申请、每GitHub账户每天1次、全网每天上限20个新节点 |

---

## Phase 3：积分与经济系统

### 3.1 积分获取

| 行为 | 积分 |
|------|------|
| 共享 Provider 被调用 | +1 / 千 token |
| 节点在线时长 | +0.1 / 小时 |
| 邀请新节点（审核通过） | +50 |
| GitHub 提交并被 approve | +20 |

### 3.2 积分消耗

| 行为 | 积分 |
|------|------|
| 调用他人共享 Provider | -1 / 千 token |
| 发送点对点消息 | -5 / 条 |
| 优先路由（加速） | -10 / 次 |

### 3.3 清算机制

**前期：中心化清算**
- 每次中继调用，双方各自签名记录用量
- Registry（GitHub Actions）每日汇总
- 写入各节点信誉/积分

**后期：签名对账**
- 双方签名记录，定期对账
- 争议时社区投票裁决

---

## Phase 4：前端与交互

### 4.1 Web 管理面板新增页面

**联邦网络页：**
- 在线节点数/可用模型数/总 Provider 数
- 网络拓扑可视化（节点关系图）
- 最新 Provider 动态流
- 节点详情（信誉、延迟、共享模型）

**审核面板：**
- 待审核节点列表
- 自动验证结果展示
- 投票界面（批准/拒绝 + 理由）
- 当前投票进度条

**点对点消息页：**
- 消息列表（收件箱/发件箱）
- 发送消息（消耗 5 积分）
- 消息类型：请求协助、节点交流、合作提议

### 4.2 CLI 命令扩展

```bash
# 节点管理
openmodelpool network init              # 初始化节点身份
openmodelpool network status            # 查看节点状态
openmodelpool network request-join      # 申请加入联邦
openmodelpool network leave             # 退出联邦

# 联邦信息
openmodelpool network nodes             # 列出所有节点
openmodelpool network providers         # 列出联邦所有可用 Provider
openmodelpool network reputation        # 查看信誉详情

# 投票与治理
openmodelpool network vote <node_id>    # 对新节点投票
openmodelpool network verify-github     # 验证 GitHub 身份

# 积分与消息
openmodelpool network balance           # 查看积分余额
openmodelpool network message <target>  # 发送点对点消息
```

---

## Phase 5：裂变增长

### 5.1 GitHub 裂变

- README 加 "Join Federation" 按钮 → Issue 模板
- GitHub Actions 自动验证节点 → 减少审核成本
- CONTRIBUTORS.md 按贡献积分排名
- Release Notes @活跃贡献者
- "Fork & Deploy" 一键按钮

### 5.2 社交媒体裂变

**分享卡片（自动生成）：**
```
┌────────────────────────────────┐
│  🌐 OpenModelPool Agent Federation       │
│                                │
│  @ChalLee 的节点               │
│  ├─ 已贡献 3 个 Provider       │
│  ├─ 已服务 42 次请求           │
│  ├─ 累计积分 186               │
│  └─ 节点在线 7 天              │
│                                │
│  扫码加入联邦                   │
└────────────────────────────────┘
```

**传播渠道：** Twitter/X、Telegram、V2EX、HackerNews、即刻、少数派

### 5.3 游戏化激励

| 成就 | 条件 |
|------|------|
| 🌱 Seed Node | 首批 50 个加入的节点 |
| ⭐ Power Node | 累计服务 1000 次请求 |
| 🔗 Connector | 邀请 10 个新节点 |
| 🛡️ Guardian | 连续在线 30 天 |
| 💬 Communicator | 发送/接收 100 条消息 |
| 🏆 Federation Master | 积分排名前 10 |

### 5.4 邀请机制

- 每个已审核节点获得 3 个邀请码
- 被邀请人提交 Issue 时附上邀请码
- 邀请人 +50 积分
- 邀请树深度 ≥3 → "Builder" 徽章

---

## 技术选型

| 组件 | 方案 | 理由 |
|------|------|------|
| 节点通信 | 标准 HTTP/JSON | 与现有架构一致，零额外依赖 |
| 节点发现 | GitHub trust_pool.json | 零成本、全球 CDN、git 审计 |
| 实时同步 | Gossip over HTTP | 轻量、去中心化、抗单点故障 |
| 身份认证 | Ed25519 签名 | 无中心 CA、去中心化信任 |
| GitHub 集成 | GitHub API + OAuth | 账户验证 + Issue 模板 |
| 前端可视化 | 内嵌 HTML（现有模式） | 保持单二进制部署 |

---

## 新增文件规划

| 文件 | 职责 |
|------|------|
| `node.go` | 节点身份、Ed25519 密钥对、签名验签 |
| `federation.go` | 联邦网络核心：信任池管理、Gossip 循环 |
| `gossip.go` | Gossip 协议实现：消息交换、传播、防循环 |
| `relay.go` | Provider 中继：请求转发、签名验证、流式透传 |
| `reputation.go` | 信誉评分：自动统计、手动评分、等级计算 |
| `credits.go` | 积分系统：获取、消耗、清算 |
| `message.go` | 点对点消息：发送、接收、加密 |
| `discovery.go` | 服务发现：GitHub 拉取、ETag、P2P 互备 |
| `network_cmd.go` | CLI 命令：network 子命令集 |

---

## 实施顺序

| 步骤 | 内容 | 依赖 |
|------|------|------|
| **Step 1** | 节点身份（node.go）+ Ed25519 密钥管理 | 无 |
| **Step 2** | GitHub Registry（trust_pool.json 读写 + CLI） | Step 1 |
| **Step 3** | Gossip 协议（gossip.go + discovery.go） | Step 1, 2 |
| **Step 4** | Provider 中继（relay.go） | Step 1, 3 |
| **Step 5** | 信誉系统（reputation.go） | Step 3, 4 |
| **Step 6** | 联合审核 + 投票机制 | Step 2, 5 |
| **Step 7** | 积分系统（credits.go） | Step 4, 5 |
| **Step 8** | 点对点消息（message.go） | Step 3, 7 |
| **Step 9** | Web 前端联邦页面 | Step 1-8 |
| **Step 10** | Provider 自发现 + 广播 | Step 3, 4 |
| **Step 11** | GitHub 裂变集成（Issue 模板 + Actions） | Step 2 |
| **Step 12** | 测试 + 文档 + 发布 | 全部 |

---

## 里程碑

| 版本 | 内容 | 核心能力 |
|------|------|---------|
| v3.0-alpha.1 | Step 1-3 | 节点身份 + GitHub Registry + Gossip 发现 |
| v3.0-alpha.2 | Step 4-5 | Provider 中继 + 信誉系统 |
| v3.0-beta.1 | Step 6-8 | 联合审核 + 积分 + 点对点消息 |
| v3.0-beta.2 | Step 9-10 | Web 前端 + Provider 自发现 |
| v3.0-rc | Step 11-12 | 裂变集成 + 测试 + 文档 |
| v3.0 正式发布 | — | 完整去中心化联邦网络 |

---

## 与 v2.0 的衔接

| v2.0 基础 | v3.0 扩展 |
|-----------|----------|
| Provider 管理 | → Provider 中继 + 自发现广播 |
| 4 维路由引擎 | → 5 维联邦路由（+节点信誉） |
| 多用户/Consumer | → 节点身份 + 联邦成员 |
| 健康检测 | → 联邦节点存活探测 + 信誉评分 |
| Token 预算 | → 联邦积分系统 |
| AES-256-GCM 加密 | → 节点密钥加密 + 消息加密 |
| 单二进制部署 | → 保持不变（联邦能力内嵌） |
