# OpenModelPool 访问控制与权限矩阵

> 版本：v3.2.0 | 更新日期：2026-07-14

## 1. Key 类型总览

OpenModelPool 共有 **3 种 Key 类型**，其中 Guest Key 有 2 种分享模式：

| Key 类型 | 格式 | 生成方式 | 说明 |
|---------|------|---------|------|
| **Public Key** | `sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1` | 管理面板生成 | 共享网络专用，仅在节点加入共享网络时有效 |
| **Guest Key** | `sk-guest-{node_id}-{random}` | 网络 API 发放 | 分发给外部用户，有两种分享模式 |
| **Admin Key** | 配置文件 `proxy_api_key` 的值 | 配置文件设定 | 节点管理员密钥，拥有全部权限 |

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
| 访问其他节点 Provider | ✅ | ✅ | ✅ | ✅ |
| 访问其他节点私有 Provider | ❌ | ❌ | ❌ | ✅ |
| 查看管理面板 | ❌ | ❌ | ✅ | ✅ |
| 编辑 Provider | ❌ | ❌ | ✅ | ✅ |
| 发放 Guest Key | ❌ | ❌ | ❌ | ✅ |
| 管理共享网络 | ❌ | ❌ | ❌ | ✅ |

> 共享模式下，所有 Provider 默认对网络可见。Guest Key（无论 Consumer 还是 Collaborator）自动获得跨节点访问权。

---

## 4. 关键设计决策

1. **Public Key 仅限共享模式**：Public Key 是共享网络的产物，节点未加入共享网络时，Public Key 无法访问任何资源（包括本机）。
2. **Guest Key 共享模式下自动提升**：当发放节点处于共享模式时，所有 Guest Key（不区分 Consumer/Collaborator）自动获得跨节点资源访问权。
3. **Guest Key 发放为管理员专属**：仅 Admin Key 可以发放、撤销和管理 Guest Key，Collaborator 无此权限。
4. **网络管理为管理员专属**：共享网络配置、节点管理等操作仅 Admin Key 可执行。
5. **Consumer 与 Collaborator 唯一区别**：Collaborator 拥有管理面板的 Provider 查看/编辑权限，Consumer 没有。资源访问权限完全一致。

---

## 5. 代码参考

| 函数 | 文件 | 职责 |
|------|------|------|
| `RequestKeyType` | provider.go | 分类请求 Key 类型（admin/guest/public/proxy） |
| `FilterByAccessControl` | provider.go | 请求路由时按 Key 类型过滤候选 Provider |
| `providerAllowsKeyType` | provider.go | 模型列表展示时按 Key 类型过滤 Provider |
| `GetGuestKeyAccessPublicPool` | network_keys.go | 判断 Guest Key 是否可跨节点访问（共享模式自动提升） |
| `withProxyAuth` | middleware.go | API 认证中间件（Public Key / Admin Key / Consumer API Key） |
| `withAuth` | middleware.go | 管理面板认证中间件（JWT，仅 Admin） |
