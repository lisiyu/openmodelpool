# OpenModelPool 文档

OpenModelPool 是一个去中心化 AI 模型共享池，使节点能够在对等网络中汇聚、路由和共享 LLM 计算资源。它兼容 OpenAI/Anthropic API 格式，提供四层 WAF 防护、Ed25519 身份认证、多签治理和跨区域负载均衡。

**快速链接**: [架构](./ARCHITECTURE.md) | [接口](./INTERFACES.md) | [开发者指南](./DEVELOPER_GUIDE.md)

---

## 核心文档

### [架构](./ARCHITECTURE.md)
系统设计、技术栈、组件结构和数据流程。从这里开始了解系统如何运作。

### [接口](./INTERFACES.md)
HTTP API 端点、认证方式、请求/响应格式。集成或使用此系统的参考。

### [开发者指南](./DEVELOPER_GUIDE.md)
环境搭建、开发工作流、编码规范和常见任务。贡献者必读。

---

## 模块

| 模块 | 描述 | 文档 |
|------|------|------|
| 核心代理 | OpenAI/Anthropic API 兼容网关，多 Provider 路由 | [模块/核心代理.md](./模块/核心代理.md) |
| 联邦网络 | P2P 信任池、Gossip 协议、中继转发 | [模块/联邦网络.md](./模块/联邦网络.md) |
| 安全防护 | WAF、JWT 认证、传输加密、速率限制 | [模块/安全防护.md](./模块/安全防护.md) |
| 数据持久化 | JSON 文件存储、加密、HMAC 完整性 | [模块/数据持久化.md](./模块/数据持久化.md) |
| 账本子系统 | 贡献记录、信任管理、IPFS 镜像 | [模块/账本子系统.md](./模块/账本子系统.md) |

---

## 核心概念

| 概念 | 描述 |
|------|------|
| [Provider](./专有概念/Provider.md) | AI 模型提供者，包含 API Key、模型列表、路由策略 |
| [TrustPool](./专有概念/TrustPool.md) | 联邦信任池，节点间的信任关系和共享资源 |
| [NodeIdentity](./专有概念/NodeIdentity.md) | 节点身份，Ed25519 密钥 + BIP39 助记词 |
| [Relay](./专有概念/Relay.md) | 去中心化中继，跨节点请求转发 |
| [KeySystem](./专有概念/KeySystem.md) | 四密钥架构：Proxy/Guest/Public/Provider |

---

## 入门指南

### 项目新人？

按此路径学习：
1. **[架构](./ARCHITECTURE.md)** - 了解全局
2. **[核心概念](#核心概念)** - 学习领域术语
3. **[开发者指南](./DEVELOPER_GUIDE.md)** - 搭建环境
4. **[接口](./INTERFACES.md)** - 探索 API

### 需要集成？

1. **[接口](./INTERFACES.md)** - API 契约和认证
2. **[架构](./ARCHITECTURE.md)** - 系统边界和数据流

### 首次贡献？

1. **[开发者指南](./DEVELOPER_GUIDE.md)** - 搭建和工作流
2. **[常见任务](./DEVELOPER_GUIDE.md#常见任务)** - 分步指南

---

## 快速参考

### 命令

```bash
go build -o openmodelpool .    # 编译
go test -count=1 -timeout 360s -p 1 ./...  # 运行测试
go vet ./...                   # 静态检查
```

### 重要文件

| 文件 | 目的 |
|------|------|
| `main.go` | 应用入口 |
| `server.go` | HTTP 路由和服务器配置 |
| `init.go` | 初始化编排 |
| `types.go` | 全局类型定义 |
| `client.go` | 上游 AI Provider 客户端 |
