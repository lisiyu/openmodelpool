# 接口文档

OpenModelPool 对外暴露两组接口：**OpenAI/Anthropic 兼容的 API 代理接口**和**管理接口**。所有 API 使用 JSON 格式，认证方式根据端点类型不同。

## 认证方式

### 1. Bearer Token（API 代理路径）

用于 `/v1/*` 端点。请求头 `Authorization: Bearer <api_key>`。

| Key 类型 | 前缀 | 描述 |
|----------|------|------|
| Admin Proxy Key | 自定义 | 管理员代理密钥，通过 `proxy_api_key` 配置 |
| Consumer Key | `sk-` | 多用户 Consumer 密钥，由 `multiUser` 管理 |
| Guest Key | `sk-guest-` | 访客密钥，受限配额 |
| Public Key | 自定义 | 公开试用密钥 |

### 2. JWT Token（管理 API）

用于 `/api/*` 管理端点。请求头 `Authorization: Bearer <jwt_token>`。

- Access Token: 24h 有效期（remember 模式 7 天）
- Refresh Token: 7 天有效期
- 通过 `POST /api/login` 获取

### 3. Federation Auth（联邦端点）

用于 `/federation/*` 端点。三种认证路径：

| 路径 | 请求头 | 描述 |
|------|--------|------|
| 节点身份 | `X-Node-ID` + `X-Node-Signature` + `X-Node-Timestamp` | Ed25519 签名，5 分钟时间窗口 |
| JWT | `Authorization: Bearer <jwt>` | 标准 JWT 认证 |
| 联邦密钥 | `X-Federation-Secret` | 共享密钥，用于 P2P 通信 |

## OpenAI 兼容接口

### POST /v1/chat/completions

核心聊天补全接口，兼容 OpenAI API 格式。

**请求**:
```json
{
  "model": "gpt-4o",
  "messages": [{"role": "user", "content": "Hello"}],
  "stream": false,
  "temperature": 0.7
}
```

**非流式响应**:
```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "created": 1234567890,
  "model": "gpt-4o",
  "choices": [{"message": {"role": "assistant", "content": "Hi!"}, "finish_reason": "stop"}],
  "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
}
```

**流式响应**: `Content-Type: text/event-stream`，SSE 格式，`data: [DONE]` 结束。

**路由模式**: 通过 `routing_mode` 配置选择 Provider 排序策略：
- `priority` — 按 Provider 优先级排序
- `cheapest` — 按定价排序
- `fastest` — 按 EWMA 延迟排序
- `auto` — 加权综合评分

**Fallback 链**: 请求失败时自动尝试下一个候选 Provider。

### GET /v1/models

返回可用模型列表。

### POST /v1/completions

文本补全接口（非聊天格式）。

### POST /v1/embeddings

文本嵌入接口。

### POST /v1/messages

Anthropic Messages API 兼容接口。使用 `x-api-key` 头认证，`anthropic-version: 2023-06-01`。

## 管理接口

### 认证

| 方法 | 路径 | 认证 | 描述 |
|------|------|------|------|
| GET | `/api/setup/status` | 公开 | 检查是否已初始化 |
| POST | `/api/setup` | rateLimitByIP(3) | 初始设置 |
| POST | `/api/login` | rateLimitByIP(5) | 登录获取 JWT |
| POST | `/api/refresh` | rateLimitByIP(10) | 刷新 Token |
| POST | `/api/forgot-password` | localOnly + rateLimitByIP(3) | 忘记密码 |
| POST | `/api/reset-password` | localOnly + rateLimitByIP(5) | 重置密码 |

### Provider 管理

| 方法 | 路径 | 认证 | 描述 |
|------|------|------|------|
| GET | `/api/providers` | ConsumerOrAdmin | 列出所有 Provider |
| POST | `/api/providers` | ConsumerOrAdmin | 创建 Provider |
| GET | `/api/providers/{id}` | ConsumerOrAdmin | 获取 Provider 详情 |
| PUT | `/api/providers/{id}` | ConsumerOrAdmin | 更新 Provider |
| DELETE | `/api/providers/{id}` | ConsumerOrAdmin | 删除 Provider |
| POST | `/api/providers/{id}/test` | ConsumerOrAdmin | 测试连接 |
| GET | `/api/providers/{id}/models` | ConsumerOrAdmin | 获取模型列表 |
| POST | `/api/providers/{id}/sync-models` | ConsumerOrAdmin | 同步模型列表 |
| GET | `/api/providers/presets` | rateLimitByIP(30) | 预设 Provider 模板 |

### 配置管理

| 方法 | 路径 | 认证 | 描述 |
|------|------|------|------|
| GET | `/api/config` | withAuth | 获取配置 |
| POST | `/api/config` | rateLimitByIP(20) + withAuth | 保存配置 |
| GET | `/api/config/export` | withAuth | 导出配置 |
| POST | `/api/config/import` | rateLimitByIP(5) + withAuth | 导入配置 |

### 联邦网络

| 方法 | 路径 | 认证 | 描述 |
|------|------|------|------|
| GET | `/api/federation/status` | withAuth | 联邦状态 |
| GET | `/api/federation/pool` | withFederationAuth | 信任池 |
| POST | `/api/federation/gossip` | withFederationAuth | Gossip 同步 |
| POST | `/api/federation/relay` | rateLimitByIP(60) + withProxyAuth | 中继请求 |
| GET | `/api/federation/reputations` | withAuth | 声誉列表 |

### P2P 网络

| 方法 | 路径 | 认证 | 描述 |
|------|------|------|------|
| GET | `/api/network/status` | withAuth | 网络状态 |
| POST | `/api/network/consent` | withAuth | 同意/拒绝加入网络 |
| GET | `/api/network/peers` | withAuth | 对等节点列表 |
| POST | `/api/network/heartbeat` | rateLimitByIP(30) | 心跳 |
| POST | `/api/network/keys/validate` | rateLimitByIP(30) | 密钥验证 |
| GET | `/api/network/idle-quota` | withAuth | 空闲配额检测 |

### WAF 管理

| 方法 | 路径 | 认证 | 描述 |
|------|------|------|------|
| GET | `/api/waf/status` | withAuth | WAF 状态 |
| GET | `/api/waf/violations` | withAuth | 违规记录 |
| GET | `/api/waf/bans` | withAuth | 封禁列表 |
| POST | `/api/waf/unban/{key}` | withAuth | 解封 |

### Seed 节点（公开）

| 方法 | 路径 | 认证 | 描述 |
|------|------|------|------|
| GET | `/api/peers` | rateLimitByIP(30) | 对等节点列表 |
| POST | `/api/register` | rateLimitByIP(5) | 注册节点 |
| GET | `/api/seed/health` | rateLimitByIP(30) | Seed 健康检查 |

## P2P 中继接口

| 方法 | 路径 | 认证 | 描述 |
|------|------|------|------|
| ANY | `/network/{node_id}/` | WAF | 反向代理到指定节点 |
| ANY | `/network/{node_id}` | WAF | 同上 |

## 错误响应格式

```json
{
  "error": {
    "message": "error description",
    "type": "error_type",
    "code": "error_code"
  }
}
```

常见错误码：
- `rate_limit_error` — 速率限制
- `waf_blocked` — WAF 拦截
- `auth_error` — 认证失败
- `upstream_error` — 上游 Provider 错误
