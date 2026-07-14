# OpenModelPool 访问控制与权限矩阵

> 版本：v3.2.0 | 更新日期：2026-07-14

## 1. Key 类型总览

OpenModelPool 共有 **3 种 Key 类型**，其中 Guest Key 有 2 种分享模式：

| Key 类型 | 格式 | 生成方式 | 说明 |
|---------|------|---------|------|
| **Public Key** | `sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1` | 管理面板生成 | 共享网络专用，仅在节点加入共享网络时有效 |
| **Guest Key** | `sk-guest-{node_id}-{random}` | 网络 API 发放 | 分发给外部用户，有两种分享模式 |
| **Admin Key（Proxy Key）** | 配置文件 `proxy_api_key` 的值 | 配置文件设定 | 节点管理员密钥，又称 Proxy Key，拥有全部权限 |

### Guest Key 的两种分享模式

| 模式 | 管理面板账号 | 说明 |
|------|------------|------|
| **Consumer** | ❌ 无 | 纯资源消费者，只能使用 API 访问模型 |
| **Collaborator** | ✅ 有（只读+编辑 Provider） | 可登录管理面板查看和编辑 Provider，但不能发放 Key 或管理网络 |

> Consumer 和 Collaborator 在**资源访问权限上完全一致**，唯一区别是 Collaborator 拥有管理面板的有限登录权限。

---

## 2. 网络模式

| 模式 | 说明 |
|------|------|
| **个人模式**（默认） | 节点独立运行，只能访问本机资源 |
| **共享模式** | 节点加入共享网络，可跨节点访问其他成员共享的资源 |

---

## 3. 权限矩阵

### 3.1 个人模式（未加入共享网络）

| 能力 | Public Key | Guest Key | Admin Key |
|------|:---------:|:---------:|:---------:|
| 访问本机所有 Provider | ❌ | ✅ | ✅ |
| 访问其他节点资源 | ❌ | ❌ | ❌ |
| 查看管理面板 | ❌ | ❌ | ✅ |
| 编辑 Provider | ❌ | ❌ | ✅ |
| 发放 Guest Key | ❌ | ❌ | ✅ |
| 管理共享网络 | ❌ | ❌ | ✅ |

> ⚠️ **Public Key 在个人模式下完全无效**：不返回任何可用 Provider 和模型。Public Key 是共享网络的产物，只有在节点加入共享网络后才能使用。

### 3.2 共享模式（已加入共享网络）

| 能力 | Public Key | Guest Key (Consumer) | Guest Key (Collaborator) | Admin Key |
|------|:---------:|:---------:|:---------:|:---------:|
| 访问本机所有 Provider | ✅ | ✅ | ✅ | ✅ |
| 访问其他节点已共享的 Provider | ✅ | ✅ | ✅ | ✅ |
| 访问其他节点私有 Provider | ❌ | ❌ | ❌ | ✅ |
| 查看管理面板 | ❌ | ❌ | ✅ | ✅ |
| 编辑 Provider | ❌ | ❌ | ✅ | ✅ |
| 发放 Guest Key | ❌ | ❌ | ❌ | ✅ |
| 管理共享网络 | ❌ | ❌ | ❌ | ✅ |

> 共享模式下，节点需开启「共享到网络池」后，其 Provider 才对网络可见。Guest Key（无论 Consumer 还是 Collaborator）自动获得跨节点访问权。

---

## 4. 共享机制

### 4.1 两层共享控制

共享控制分为两层：

**第一层：Key 级别标记**
管理员添加上游 API Key 时，可为每个 Key 设定访问权限：
- **私有（private）**：该 Key 的额度仅本机使用，不贡献到共享池
- **共享（shared）**：该 Key 的额度可贡献到共享网络池，供其他节点使用

**第二层：节点级别开关**
节点管理员决定是否加入共享网络：
- **未加入共享网络**：所有 Key 的额度仅本机使用，无论标记为私有还是共享
- **已加入共享网络**：标记为「共享」的 Key 的额度贡献到网络池，其他节点可通过 Public Key 或 Guest Key 访问

> 简单说：管理员先预设哪些 Key 可以共享，但能不能真正共享出来，取决于管理员有没有加入共享网络。

### 4.2 额度池分配与消耗优先级

共享模式下，额度分为不同的池，不同 Key 类型消耗额度的优先级不同：

**额度池划分：**

| 额度池 | 来源 | 说明 |
|--------|------|------|
| 私有额度池 | 节点标记为 private 的上游 Key | 仅本节点自有请求使用 |
| 共享额度池 | 节点标记为 shared 的上游 Key | 贡献到网络，按 Guest/Public 比例分配 |

**消耗优先级：**

| Key 类型 | 第一优先 | 第二优先 | 第三优先 |
|---------|---------|---------|---------|
| **Guest Key** | 发放节点的私有额度池 | 发放节点的共享额度池 | 其他节点的共享额度池 |
| **Admin Key（Proxy Key）** | 本节点的私有额度池 | 本节点的共享额度池 | 其他节点的共享额度池 |
| **Public Key** | — | 所有节点的共享额度池 | — |

- **Guest Key**：优先消耗发放节点自身的私有额度，私有额度耗尽后再消耗发放节点的共享额度，最后才路由到其他节点的共享池。
- **Admin Key（即 Proxy Key）**：与 Guest Key 同理，优先消耗本节点私有额度，再共享额度，最后其他节点共享池。
- **Public Key**：只能消耗所有节点贡献到共享网络的额度，无法触达任何节点的私有额度池。

### 4.3 跨节点路由

当 Guest Key 或 Admin Key（Proxy Key）请求的模型在当前节点不存在时，系统自动路由到拥有该模型的最近可用节点，消耗目标节点的共享额度池。

> Public Key 不涉及此场景——Public Key 始终只从共享池中调度，所有路由目标都是其他节点贡献的共享资源。

---

## 5. 关键设计决策

1. **Public Key 仅限共享模式**：Public Key 是共享网络的产物，节点未加入共享网络时，Public Key 无法访问任何资源（包括本机）。
2. **Guest Key 共享模式下自动提升**：当发放节点处于共享模式时，所有 Guest Key（不区分 Consumer/Collaborator）自动获得跨节点资源访问权。
3. **Guest Key 发放为管理员专属**：仅 Admin Key 可以发放、撤销和管理 Guest Key，Collaborator 无此权限。
4. **网络管理为管理员专属**：共享网络配置、节点管理等操作仅 Admin Key 可执行。
5. **Consumer 与 Collaborator 唯一区别**：Collaborator 拥有管理面板的 Provider 查看/编辑权限，Consumer 没有。资源访问权限完全一致。
6. **额度消耗有优先级**：Guest Key / Admin Key（Proxy Key）优先消耗本节点私有额度，再消耗本节点共享额度，最后才使用其他节点共享池；Public Key 只能消耗共享池额度。
7. **跨节点自动路由**：当请求的模型在本节点不存在时，系统自动路由到拥有该模型的其他节点，消耗目标节点的共享额度池。

---

## 6. 代码参考

| 函数 | 文件 | 职责 |
|------|------|------|
| `RequestKeyType` | provider.go | 分类请求 Key 类型（admin/guest/public/proxy） |
| `FilterByAccessControl` | provider.go | 请求路由时按 Key 类型过滤候选 Provider |
| `providerAllowsKeyType` | provider.go | 模型列表展示时按 Key 类型过滤 Provider |
| `GetGuestKeyAccessPublicPool` | network_keys.go | 判断 Guest Key 是否可跨节点访问（共享模式自动提升） |
| `withProxyAuth` | middleware.go | API 认证中间件（Public Key / Admin Key / Consumer API Key） |
| `withAuth` | middleware.go | 管理面板认证中间件（JWT，仅 Admin） |
