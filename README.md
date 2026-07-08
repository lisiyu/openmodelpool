# OpenModelPool Agent

**去中心化 AI 能力共享网络** — 让大模型的能力像信息一样自由流动。

> 网络无边界，模型能力也不应该有边界。

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## 🤖 它是什么？

**OpenModelPool Agent 是一个 AI 智能代理。** 和你用过的任何 AI Agent 没有本质区别——持有 API Key，向上游模型服务商发送请求，获取响应。

唯一的区别是：它多了一个**可选的共享功能**。你可以把闲置的模型调用能力分享给网络中的其他人，也可以使用他人分享的能力。

对上游服务商而言，这和任何人直接调用 API 完全一样——同一个 Key，同一个配额，同一个服务商。**不存在"转售"，不存在"中间商"，只是一个 Agent 在帮你转发请求。**

---

## 🌍 我们的信念

互联网最伟大的创造，是打破了信息的边界。

当年，BitTorrent 让知识不再被服务器垄断；IPFS 让存储不再依赖单一节点；Tor 让通信不再受地域束缚。

**OpenModelPool Agent 要做的是同一件事——但共享的不是文件，而是 AI 的能力。**

我们相信，一个身处纽约的开发者手中的 Claude API，和北京程序员手中的一样有价值。当全球的 AI 能力通过一个去中心化网络汇聚在一起，任何人都可以平等地获取最强大的智能——无论他在哪里。

这不是商业产品，这是互联网精神的延续：**共享、开放、无边界。**

---

## 🔄 模型能力交换网络

**你的闲置，是别人的稀缺。**

> 你有 Gemini 的 Token 余量用不完，但你想用 GLM——却没有渠道。
> 地球另一端的人正好有 GLM 额度富余，却想用 Gemini。
>
> **你们互相交换，各取所需。**

这就是 OpenModelPool Agent 的核心经济模型——**不是买卖，是交换**：

- 你贡献自己富余的模型能力（Gemini、Claude、GPT-4……）
- 获得贡献积分，可以用来调用别人分享的模型（GLM、通义、Kimi……）
- 你贡献得越多，签发的访问密钥权重越高，享受的服务质量越好
- 你也可以把自己的密钥分享给朋友，让他们一起用

```
  你的节点                    共享网络                    别人的节点
┌──────────┐            ┌──────────────┐            ┌──────────┐
│ Gemini   │────分享────→│   贡献积分    │←──分享────│ GLM      │
│ 余量丰富  │            │   = 交换货币  │            │ 余量丰富  │
│ 缺 GLM   │←──调用────│   信誉驱动    │────调用───→│ 缺 Gemini│
└──────────┘            └──────────────┘            └──────────┘
```

没有中间商，没有定价，没有法币。**纯粹的能力互换，让每个 Token 都不浪费。**

### ⏳ 时间维度的削峰填谷

你不可能每个月都用一样多。有的月份项目忙，消耗远超预算；有的月份闲着，额度白白过期。

**网络帮你抹平波动：**

- 用不完的月份 → 剩余额度自动存入网络，变成你的贡献积分
- 消耗爆表的月份 → 从积分池支取，或借用网络中其他节点的富余
- 长期来看 → 你的年均用量 ≈ 年均额度，不再浪费任何一个月

就像电网的削峰填谷——你的闲置算力在给别人用，你的高峰期有整个网络兜底。**所有人的用量波动叠加后趋于稳定，整个网络的资源利用率接近 100%。**

---

## 🧭 项目愿景

OpenModelPool Agent 从一个轻量级 AI API 代理起步，正在演化为一个 **P2P AI 能力共享网络**：

```
  今天                          未来
┌──────────┐            ┌─────────────────────────┐
│ 单节点代理 │    →→→     │  全球节点互联的共享网络    │
│ 手动配置   │            │  自动发现 · 信誉驱动      │
│ 本地路由   │            │  多跳中继 · 端到端加密    │
│ 个人使用   │            │  贡献激励 · 社区共建      │
└──────────┘            └─────────────────────────┘
```

- **Phase 1** ✅ 单节点智能代理（当前） — 34+ 平台统一接入，4 维路由，多用户管理
- **Phase 2** 🔜 联邦共享网络 — 节点自动发现，邀请链信任传递，跨节点路由，贡献积分签发
- **Phase 3** 🌐 全球能力交换 — 去中心化 relay 网络，签名密钥跨节点验证，模型能力自由互换

---

## ✨ 核心特性

### 🔌 统一 API 网关

- **OpenAI 兼容接口** — 统一 `/v1/chat/completions`，支持流式 (SSE) 与非流式，零拷贝转发
- **34 个预置平台** — Coze、Sider.ai、OpenAI、Anthropic Claude、DeepSeek、Gemini、通义千问、智谱、月之暗面、零一万物、MiniMax、硅基流动、Groq、xAI、Together、Mistral、豆包、讯飞星火、腾讯 TokenHub（Coding Plan / Token Plan / 企业版）、百度千帆、阶跃星辰、百川智能、NVIDIA NIM、Novita AI、Fireworks AI、Cohere、Cerebras、OpenRouter、Poe、Ollama 等
- **`provider/model` 语法** — 可通过 `deepseek/deepseek-chat` 格式指定平台，也支持 OpenRouter 风格路由

### 🧠 4 维智能路由

| 模式 | 策略 |
|------|------|
| 🎯 优先级优先 | 按预设优先级排序 |
| 💰 成本最低 | 按「平台×模型」定价选择最便宜的平台 |
| ⚡ 速度最快 | 基于 EWMA 历史延迟选择最快平台 |
| 🧠 综合权重 | 加权融合 4 个维度：**优先级 40%** + **成本 25%** + **延迟 20%** + **剩余 Token 15%** |

> 综合权重的 4 个维度均可在管理面板中自定义权重比例。

### 🔗 失败自动降级

请求失败时自动切换到下一个可用 Provider，形成 fallback 链，直到成功或所有候选耗尽。

### 👥 多用户支持

- **邀请码注册** — 管理员生成邀请码，消费者凭码自助注册
- **Provider 共享** — 消费者可添加自己的 Provider，共享到统一代理池
- **严格可见性隔离** — 管理员看到全部，消费者仅看到自己的 + 系统预置的
- **独立 API Key** — 每个消费者拥有独立的 Proxy API Key，用于调用代理接口
- **用量追踪** — 按消费者维度统计 Token 消耗和请求次数

### 💰 Token 预算管理

- **双维度定价** — 按「平台 × 模型」精确设定每百万 Token 的输入/输出价格（USD）
- **月度预算** — 为每个 Provider 设定月度 Token 上限
- **阈值告警** — 达到 80% / 90% / 100% 阈值时自动发送邮件告警

### 🩺 Provider 自动健康检测

- 每 **5 分钟**并发探测所有已启用 Provider 的健康状态
- 记录状态：`healthy` / `degraded` / `down` / `unknown`
- 追踪连续失败次数、最近成功/失败时间、故障原因

### 📝 请求日志

- **内存环形缓冲区** — 最多保留 1000 条请求记录，实时查看
- 记录字段：时间、模型、Provider、延迟、Token 数、成本、成功/失败、重试次数、是否流式

### 📊 用量归档

- 按天 / 月自动归档历史用量数据
- 支持 7 天 / 30 天统计视图
- EWMA（指数加权移动平均）延迟追踪

### 🔐 安全与加密

- **AES-256-GCM** — 所有敏感数据（API Key、SMTP 密码、Proxy API Key）加密存储
- **bcrypt** — 管理员密码哈希
- **JWT** — Token 认证，支持过期机制

### 📧 SMTP 邮件服务

- **忘记密码** — 通过邮箱发送重置码找回管理员密码
- **重置密码** — 通过 Proxy API Key 重置
- **预算告警** — Token 预算阈值邮件通知
- **SMTP 测试** — 管理面板一键测试邮件发送

### 🌐 VMess 代理支持

- 解析 `vmess://` 链接，自动启动本地 Xray 代理
- 为 Provider 配置 VMess 出站代理，透明转发请求
- 启动时自动恢复所有 VMess 代理

### 🖥️ Web 管理面板

- **暗色主题**，响应式设计，移动端友好
- 初始配置向导（Setup Wizard）
- Provider 管理（增删改查、测试连通性、同步模型列表）
- 路由模式 / 权重配置
- 用量统计与请求日志
- 邀请码与消费者管理
- 配置导出 / 导入（AES-256-GCM 加密）
- SMTP 配置管理

---

## 🚀 快速开始

### 一键安装（推荐）

**Linux / macOS：**

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/install.sh | bash
```

自定义参数：

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/install.sh | bash -s -- --port 9090 --dir /opt/openmodelpool
```

**Windows (PowerShell 管理员)：**

```powershell
irm https://raw.githubusercontent.com/lisiyu/openmodelpool/main/install.ps1 | iex
```

安装脚本自动完成：检测平台 → 下载二进制 → SHA256 校验 → 安装 → 注册系统服务 → 自动启动。

### 编译运行

```bash
# 克隆
git clone https://github.com/lisiyu/openmodelpool.git
cd openmodelpool

# 编译当前平台
make build

# 运行（默认端口 8000）
./openmodelpool
```

### Docker 部署

```bash
# Docker Compose（推荐）
docker compose up -d

# 或手动
docker build -t openmodelpool .
docker run -d --name openmodelpool -p 8000:8000 -v $(pwd)/data:/app/data openmodelpool
```

### 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `PORT` | 服务端口 | `8000` |
| `COZE_API_TOKEN` | 扣子 PAT（可选，可在面板配置） | — |
| `COZE_BOT_ID` | 默认扣子 Bot ID | — |

### 首次使用

1. 访问 `http://localhost:8000` 进入初始配置向导
2. 设置管理员账号（用户名、密码、邮箱）
3. 在管理面板中添加 Provider 并填入 API Key
4. 完成！通过 `/v1/chat/completions` 调用

---

## 📡 API 文档

### 代理接口（OpenAI 兼容）

#### `GET /v1/models`

列出所有可用的模型。

```bash
curl http://localhost:8000/v1/models \
  -H "Authorization: Bearer YOUR_PROXY_KEY"
```

#### `POST /v1/chat/completions`

聊天补全接口，完全兼容 OpenAI API 格式。

**非流式请求：**

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_PROXY_KEY" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "你好！"}]
  }'
```

**流式请求（SSE）：**

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_PROXY_KEY" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "写一首诗"}],
    "stream": true
  }'
```

**指定平台：**

```bash
# provider/model 格式强制路由到指定平台
curl ... -d '{"model": "deepseek/deepseek-chat", ...}'
```

### 认证方式

代理接口支持两种认证方式：

| 方式 | Header | 说明 |
|------|--------|------|
| Proxy API Key | `Authorization: Bearer sk-xxx` | 管理员设置的代理密钥 |
| Consumer API Key | `Authorization: Bearer ck-xxx` | 消费者独立密钥 |

> 如果未设置 Proxy API Key，代理接口默认允许匿名访问（管理员权限）。

### 管理接口

#### 认证相关（公开）

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/setup/status` | 查询是否已完成初始化 |
| `POST` | `/api/setup` | 初始化管理员账号 |
| `POST` | `/api/login` | 管理员登录 |
| `POST` | `/api/forgot-password` | 发送密码重置邮件 |
| `POST` | `/api/reset-password` | 通过邮箱重置码重置密码 |
| `POST` | `/api/reset-password/verify` | 验证重置 Token |
| `POST` | `/api/auth/reset-with-code` | 通过 Proxy API Key 重置密码 |

#### 管理员接口（需 JWT）

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/config` | 获取配置 |
| `POST` | `/api/config` | 保存配置 |
| `GET` | `/api/config/export` | 导出配置（AES-256-GCM 加密） |
| `POST` | `/api/config/import` | 导入配置 |
| `GET` | `/api/status` | 系统状态概览 |
| `GET` | `/api/admin/info` | 管理员信息 |
| `POST` | `/api/admin/change-password` | 修改密码 |
| `POST` | `/api/admin/update-email` | 修改邮箱 |

#### Provider 管理（管理员 + 消费者）

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/providers` | 列出所有 Provider |
| `GET` | `/api/providers/presets` | 获取预置平台列表 |
| `POST` | `/api/providers` | 创建 Provider |
| `GET` | `/api/providers/{id}` | 获取单个 Provider |
| `PUT` | `/api/providers/{id}` | 更新 Provider |
| `DELETE` | `/api/providers/{id}` | 删除 Provider |
| `POST` | `/api/providers/{id}/test` | 测试 Provider 连通性 |
| `GET` | `/api/providers/{id}/models` | 获取 Provider 可用模型 |
| `POST` | `/api/providers/{id}/sync-url` | 同步 Provider 模型列表 |
| `POST` | `/api/providers/sync-all-urls` | 批量同步所有 Provider |

#### 路由与用量（管理员 + 消费者）

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/routing/mode` | 获取当前路由模式 |
| `POST` | `/api/routing/mode` | 设置路由模式（仅管理员） |
| `GET` | `/api/routing/weights` | 获取综合权重配置 |
| `POST` | `/api/routing/weights` | 设置综合权重（仅管理员） |
| `GET` | `/api/routing/advice/{model}` | 获取模型路由建议 |
| `GET` | `/api/usage/summary` | 用量汇总 |
| `GET` | `/api/usage/providers` | 按 Provider 统计用量 |
| `GET` | `/api/usage/records` | 用量明细记录 |
| `DELETE` | `/api/usage/reset` | 重置用量数据（仅管理员） |

#### 多用户管理（需 JWT）

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/invite-codes` | 列出邀请码 |
| `POST` | `/api/invite-codes` | 创建邀请码 |
| `DELETE` | `/api/invite-codes/{code}` | 删除邀请码 |
| `GET` | `/api/consumers` | 列出消费者 |
| `POST` | `/api/consumers` | 创建消费者 |
| `DELETE` | `/api/consumers/{id}` | 删除消费者 |
| `POST` | `/api/consumers/{id}/toggle` | 启用/禁用消费者 |
| `POST` | `/api/consumer/register` | 消费者自助注册（需邀请码） |

#### 其他

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/smtp/status` | SMTP 配置状态（公开） |
| `GET` | `/api/smtp/config` | 获取 SMTP 配置 |
| `POST` | `/api/smtp/config` | 保存 SMTP 配置 |
| `POST` | `/api/smtp/test` | 测试 SMTP 发送 |
| `GET` | `/api/logs` | 请求日志（环形缓冲区） |
| `GET` | `/api/health` | Provider 健康状态 |
| `GET` | `/api/providers/sider/status` | Sider Token 状态 |
| `POST` | `/api/providers/sider/test` | 测试 Sider 连通性 |
| `GET` | `/health` | 服务健康检查（公开） |

---

## 🔨 构建与部署

### 多平台交叉编译

```bash
# 编译所有平台（6 个目标）
make build-all
# 或
./build-all.sh

# 编译单个平台
./build-all.sh linux-amd64
./build-all.sh linux-arm64
./build-all.sh linux-armv7    # 树莓派 3B / OpenWRT
./build-all.sh darwin-arm64   # Apple Silicon
./build-all.sh windows-amd64

# 查看支持的平台
./build-all.sh list
```

编译产物输出到 `dist/` 目录，包含 SHA256 校验文件。

### 支持的平台

| 平台 | 架构 | 输出文件 | 适用设备 |
|------|------|---------|---------|
| Linux | amd64 | `openmodelpool-linux-amd64` | x86_64 服务器 |
| Linux | arm64 | `openmodelpool-linux-arm64` | ARM 服务器 |
| Linux | armv7 | `openmodelpool-linux-armv7` | 树莓派 3B、OpenWRT |
| macOS | amd64 | `openmodelpool-darwin-amd64` | Intel Mac |
| macOS | arm64 | `openmodelpool-darwin-arm64` | Apple Silicon Mac |
| Windows | amd64 | `openmodelpool-windows-amd64.exe` | x64 Windows |

### Makefile 命令速查

| 命令 | 说明 |
|------|------|
| `make build` | 编译当前平台 |
| `make build-all` | 编译所有 6 个平台 |
| `make build-linux` | 仅编译 Linux (3 个架构) |
| `make build-darwin` | 仅编译 macOS (2 个架构) |
| `make build-windows` | 仅编译 Windows |
| `make clean` | 清理编译产物 |
| `make test` | 运行测试 + 覆盖率 |
| `make docker` | 构建 Docker 镜像 |
| `make docker-compose` | Docker Compose 启动 |
| `make release` | 完整发布流程 |

### 编译优化

所有编译均使用以下优化参数：

```bash
go build -ldflags="-s -w" -trimpath
```

- `-s -w`：去除调试信息和符号表，减小二进制体积
- `-trimpath`：去除本地路径信息，提高可移植性和安全性

### 安装脚本参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--port` | 服务端口 | `8000` |
| `--dir` | 安装目录 | `/usr/local/bin` |
| `--data` | 数据目录 | `/var/lib/openmodelpool` |
| `--version` | 指定版本 | `latest` |

## ⚙️ 配置说明

### 数据存储

所有数据存储在 `data/` 目录下，JSON 格式：

| 文件 | 内容 |
|------|------|
| `data/config.json` | 全局配置（路由模式、权重、Proxy API Key 等） |
| `data/providers.json` | Provider 配置（API Key 加密存储） |
| `data/admin.json` | 管理员账号、JWT Secret、SMTP 配置 |
| `data/usage.json` | 用量记录 |
| `data/consumers.json` | 多用户数据（邀请码、消费者） |
| `data/.key` | AES-256 加密密钥（自动生成） |
| `data/sider_token_status.json` | Sider Token 状态 |

### 敏感数据加密

所有敏感字段使用 **AES-256-GCM** 加密后存储：

- Provider API Key
- Proxy API Key
- SMTP 密码
- VMess 代理链接

密钥文件 `data/.key` 在首次启动时自动生成（32 字节随机密钥，Base64 编码）。

> ⚠️ **请妥善保管 `data/.key` 文件**，丢失后无法解密已存储的敏感数据。

### 配置导出 / 导入

支持将完整配置导出为加密文件，方便迁移和备份：

```bash
# 导出（通过管理面板 API）
curl http://localhost:8000/api/config/export \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -o backup.json

# 导入
curl http://localhost:8000/api/config/import \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -F "file=@backup.json"
```

### 路由模式配置

通过 API 或管理面板设置：

```bash
# 设置路由模式
curl -X POST http://localhost:8000/api/routing/mode \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "auto"}'

# 自定义综合权重
curl -X POST http://localhost:8000/api/routing/weights \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"priority": 0.4, "cost": 0.25, "latency": 0.2, "tokens": 0.15}'
```

---

## 🏗️ 架构简述

```
┌─────────────────────────────────────────────────────────┐
│                     客户端请求                           │
│            (OpenAI SDK / curl / 任意 HTTP)               │
└───────────────────────┬─────────────────────────────────┘
                        │ POST /v1/chat/completions
                        ▼
┌─────────────────────────────────────────────────────────┐
│                  OpenModelPool Agent 代理网关                       │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐  │
│  │ 认证中间件 │→│ 智能路由  │→│  失败降级 (fallback)  │  │
│  │ Proxy/   │  │ priority │  │  自动尝试下一个       │  │
│  │ Consumer │  │ cheapest │  │  可用 Provider        │  │
│  │ API Key  │  │ fastest  │  │                       │  │
│  └──────────┘  │ auto     │  └──────────────────────┘  │
│                └──────────┘                             │
│  ┌──────────────────────────────────────────────────┐   │
│  │              Provider 统一池                      │   │
│  │  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐  │   │
│  │  │OpenAI│ │DeepS.│ │Gemini│ │通义  │ │Groq  │  │   │
│  │  └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘  │   │
│  │     │        │        │        │        │       │   │
│  │  ┌──┴──┐ ┌──┴──┐ ┌──┴──┐ ┌──┴──┐ ┌──┴──┐   │   │
│  │  │VMess│ │VMess│ │SOCKS│ │直连 │ │直连 │   │   │
│  │  └─────┘ └─────┘ └─────┘ └─────┘ └─────┘   │   │
│  └──────────────────────────────────────────────────┘   │
│                                                         │
│  ┌────────┐ ┌────────┐ ┌──────────┐ ┌──────────────┐   │
│  │Tracker │ │Health  │ │ Request  │ │ Multi-User   │   │
│  │用量追踪│ │Checker │ │  Log     │ │ 邀请码/消费者 │   │
│  │EWMA延迟│ │5min探活│ │ 环形缓冲 │ │ 可见性隔离   │   │
│  │月度归档│ │状态监控│ │ 1000条   │ │ Token预算    │   │
│  └────────┘ └────────┘ └──────────┘ └──────────────┘   │
└─────────────────────────────────────────────────────────┘
```

### 技术栈

| 组件 | 技术 | 说明 |
|------|------|------|
| HTTP 服务 | Go 标准库 `net/http` | 无第三方 Web 框架，Go 1.22+ 路由模式 |
| 认证 | `golang-jwt/jwt/v5` | JWT Token 签发与验证 |
| 密码 | `golang.org/x/crypto/bcrypt` | 密码哈希 |
| 加密 | Go 标准库 `crypto/aes` + `crypto/cipher` | AES-256-GCM 加密 |
| 代理 | `golang.org/x/net/proxy` | SOCKS5 出站代理 |
| VMess | Xray（外部二进制） | VMess 本地代理 |
| 并发 | Go goroutine | 并发健康检测、请求转发 |
| 流式 | SSE + `io.Writer` | 零拷贝流式转发 |
| 部署 | 单二进制 / Docker | 零依赖，跨平台 |

### 项目结构

```
openmodelpool/
├── main.go           # 入口，HTTP 路由注册，中间件
├── types.go          # 数据模型（OpenAI 兼容格式）
├── config.go         # 配置管理（JSON + 环境变量 + 加密）
├── provider.go       # Provider CRUD + 智能路由引擎
├── providers.go      # 34 个预置平台定义
├── client.go         # 上游请求转发（OpenAI 兼容 / Sider / Coze）
├── sider.go          # Sider 网页版适配 + Token 状态监控
├── tracker.go        # 用量追踪 + EWMA + 批量写入 + 月度归档 + 预算告警
├── pricing.go        # 「平台×模型」双维度定价表
├── health.go         # Provider 健康检测（并发探测）
├── auth.go           # JWT 认证 + bcrypt + SMTP + 密码找回
├── admin.go          # 管理面板 API
├── multiuser.go      # 多用户：邀请码 + 消费者 + API Key 管理
├── encryptor.go      # AES-256-GCM 加密器
├── vmess.go          # VMess 代理管理（解析 + 启停 Xray）
├── admin.html        # Web 管理面板（暗色主题 SPA）
├── login.html        # 登录页
├── setup.html        # 初始配置向导页
├── forgot_password.html  # 忘记密码页
├── go.mod / go.sum   # Go 模块依赖
├── Makefile           # 构建快捷命令
├── Dockerfile         # 多阶段 Docker 构建
├── install.sh         # 一键安装脚本
└── deploy.sh          # 部署脚本
```

---

## 📦 预置平台一览

| # | 平台 | 优先级 | 类型 | 特色 |
|---|------|--------|------|------|
| 1 | 扣子 (Coze) | 1 | 专有 API | 智能体平台，`coze-{bot_id}` 模型格式 |
| 2 | Sider.ai (网页版) | 2 | 网页 Token | 网页版多模型聚合，需登录获取 Token |
| 3 | 腾讯 TokenHub Coding Plan | 3 | OpenAI 兼容 | 编程专属套餐，按请求次数限额，API Key 格式 `sk-sp-xxxx` |
| 4 | 腾讯 TokenHub Token Plan | 3 | OpenAI 兼容 | 个人版按 Token 计费订阅制，API Key 格式 `sk-tp-xxxx`，含 GLM-5/5.1、MiniMax M2.5/M2.7、Kimi K2.5、DeepSeek V4、Hy3 |
| 5 | 腾讯 TokenHub 企业版 | 3 | OpenAI 兼容 | 企业积分制，多 Key 配额管理，团队共享，含 GLM-5/5.1/5.2/Turbo、MiniMax M3、Kimi K2.6 等最新模型 |
| 6 | Google Gemini | 4 | OpenAI 兼容 | 多模态、超长上下文，2.5 Pro/Flash 系列 |
| 7 | DeepSeek | 5 | OpenAI 兼容 | 高性价比国产大模型，V3/R1 |
| 8 | 通义千问 | 5 | OpenAI 兼容 | 阿里云 Qwen Turbo/Plus/Max/Long |
| 9 | 智谱 AI | 5 | OpenAI 兼容 | GLM-4 系列，含视觉模型 |
| 10 | Moonshot (Kimi) | 5 | OpenAI 兼容 | 长上下文 8K/32K/128K |
| 11 | 零一万物 | 5 | OpenAI 兼容 | Yi 系列 |
| 12 | MiniMax | 5 | OpenAI 兼容 | MiniMax 大模型 |
| 13 | 硅基流动 | 5 | OpenAI 兼容 | 开源模型聚合平台 |
| 14 | Groq | 5 | OpenAI 兼容 | 超快推理速度 |
| 15 | xAI (Grok) | 5 | OpenAI 兼容 | Grok 2/3 系列 |
| 16 | Together AI | 5 | OpenAI 兼容 | 开源模型推理平台 |
| 17 | Mistral AI | 5 | OpenAI 兼容 | 欧洲领先大模型，含 Codestral |
| 18 | 火山引擎 (豆包) | 5 | OpenAI 兼容 | 字节跳动豆包 |
| 19 | 讯飞星火 | 5 | OpenAI 兼容 | 科大讯飞 |
| 20 | NVIDIA NIM | 5 | OpenAI 兼容 | 100+ 模型免费推理，40 RPM 免费层 |
| 21 | 百度千帆 | 5 | OpenAI 兼容 | ERNIE 系列大模型 |
| 22 | 阶跃星辰 (Stepfun) | 5 | OpenAI 兼容 | Step 系列模型 |
| 23 | 百川智能 (Baichuan) | 5 | OpenAI 兼容 | Baichuan 系列模型 |
| 24 | Novita AI | 5 | OpenAI 兼容 | 聚合平台，多模型支持 |
| 25 | Fireworks AI | 5 | OpenAI 兼容 | 高速推理平台 |
| 26 | Cohere | 5 | OpenAI 兼容 | 企业级 NLP，Command R+ |
| 27 | Cerebras | 5 | OpenAI 兼容 | 极致推理速度，WSE 芯片 |
| 28 | Anthropic Claude | 5 | 专有 API | Claude 3.5/4 系列，Messages API 适配 |
| 29 | OpenAI | 10 | OpenAI 兼容 | GPT-4o、o1、o3、o4-mini |
| 30 | Poe | 15 | OpenAI 兼容 | Quora 多模型聚合 |
| 31 | SID.ai | 15 | OpenAI 兼容 | 开发者平台 |
| 32 | OpenRouter | 20 | OpenAI 兼容 | 全球模型聚合平台 |
| 33 | Ollama (本地) | 50 | OpenAI 兼容 | 本地部署模型，零延迟 |

---


## 🔑 非 OpenAI 兼容平台配置指南

以下 3 个平台使用专有 API，不走标准 `Authorization: Bearer` + OpenAI 格式，需特殊配置。所有非标平台的 API Key/Token 均统一在 **Provider 编辑界面** 填写。

---

### 🎯 扣子 (Coze)

**API 类型：** 专有 Chat API（`/v3/chat` + 轮询）  
**API Key 格式：** Personal Access Token (PAT)，格式 `pat_xxxxxxxxxxxx`

**获取方式：**
1. 登录 [扣子开放平台](https://www.coze.cn)
2. 右上角头像 → **API 令牌** → **创建令牌**
3. 命名后复制令牌（仅创建时显示一次）

**配置方式：** 在 Provider 编辑界面的 **API Key** 字段填入 PAT 令牌  
**调用方式：** 模型名格式 `coze-{bot_id}`
```bash
curl -d '{"model": "coze-7xxxxxxxxxx0", "messages": [...]}'
```

**工作原理：** 发起聊天 → 轮询状态 → 获取回复，完整 OpenAI 格式转换。

---

### 🌐 Sider.ai（网页版）

**API 类型：** 网页版私有 API（`/api/v3/completion/text`）  
**API Key 格式：** 浏览器扩展 Session Token

**获取方式：**
1. 安装 [Sider.ai Chrome 扩展](https://sider.ai/) 并登录
2. F12 → **Application** → **Cookies** → `sider.ai` → 复制 `token` 字段值（`Bearer ` 后面部分）

**配置方式：** 在 Provider 编辑界面的 **API Key** 字段填入 Token  
**注意事项：** Token 会过期，需定期更新；内置健康检测自动标记过期状态

---

### 🟠 Anthropic Claude

**API 类型：** Messages API（`/v1/messages`）  
**API Key 格式：** `sk-ant-xxxxx`（x-api-key 头认证）

**获取方式：**
1. 登录 [Anthropic Console](https://console.anthropic.com/)
2. **API Keys** → **Create Key** → 复制

**配置方式：** 在 Provider 编辑界面的 **API Key** 字段填入 API Key  
**自动适配：** system 消息独立提取、专有认证头、SSE 事件自动转换

---

## 📜 License

MIT

---

## 🙏 开源致谢

OpenModelPool Agent 的诞生离不开以下优秀的开源项目和技术：

- [**Go**](https://go.dev/) — 简洁高效的编程语言，OpenModelPool Agent 的基石
- [**golang-jwt/jwt**](https://github.com/golang-jwt/jwt) — 可靠的 JWT 认证实现
- [**golang.org/x/crypto**](https://pkg.go.dev/golang.org/x/crypto) — 安全的 bcrypt 密码哈希
- [**golang.org/x/net**](https://pkg.go.dev/golang.org/x/net) — SOCKS5 代理支持

灵感来源于以下优秀的开源 API 管理项目：

- [**one-api**](https://github.com/songquanpeng/one-api) — OpenAI 管理工具，为 AI 模型聚合提供了优秀的参考范式
- [**new-api**](https://github.com/Calcium-Ion/new-api) — one-api 的增强版，拓展了多用户和渠道管理的思路

感谢开源社区的持续贡献，让 AI 工具生态更加繁荣。

**精神先驱** — 以下项目证明了去中心化共享的力量，OpenModelPool Agent 沿袭同样的信念：

- [**BitTorrent**](https://www.bittorrent.com/) — 让知识不再被服务器垄断，P2P 文件共享的先驱
- [**IPFS**](https://ipfs.tech/) — 内容寻址、去中心化存储，让数据属于所有人
- [**Tor**](https://www.torproject.org/) — 洋葱路由，让通信自由不受地域束缚

---

## 📋 更新日志

### v3.3.0 (2026-07)

**🔴 Critical 安全修复**
- **API Key 脱敏** — `/api/share/info` 和 `/api/config/export` 接口不再明文暴露 Proxy API Key
- **Consumer Key 加密** — Consumer API Key 使用 AES-256-GCM 加密存储，不再明文落盘

**🟠 安全加固**
- **CORS 收紧** — 移除通配符 `*`，默认仅允许 localhost + 隧道 URL
- **文件权限** — 所有数据文件权限从 0644 收紧至 0600
- **错误脱敏** — 代理错误消息不再泄露内部 IP 地址
- **JWT 安全** — admin.html 移除 localStorage 存 token，改用 HttpOnly Cookie
- **Cookie 增强** — 添加 Secure + SameSite=Lax 标志
- **端点认证** — `/metrics` 和 `/events` 端点新增认证保护（401）
- **联邦鉴权** — 联邦端点限制为已知节点/管理员访问

**🟢 其他改进**
- **密码强度** — 最低密码长度从 6 位提升至 8 位
- **Reset Token** — 复用未过期令牌，防止并发竞争
- **匿名回退** — 在有 Consumer 注册时禁用匿名回退
- **Consumer 权限** — handleTestProvider 添加 Consumer 权限检查，防止越权

**⚡ 性能优化**
- **Config 写入 debounce** — 3 秒聚合写入，减少磁盘 I/O
- **HTTP 连接池** — 全局 MaxIdleConns=100，复用 TCP 连接
- **异步写入** — Config.save() 改为异步，不阻塞请求
- **Tracker 优化** — Record() 释放锁后再刷盘，减少锁竞争

**🐛 Bug 修复**
- 空 records 返回 `[]` 而非 `null`（前端兼容性）
- Login cookie 不再重复 Set-Cookie
- MarkAsRead 不存在时返回 404（此前静默成功）
- round1/round4 负数归零处理
- 分享功能脱敏显示 API Key

**🆕 新功能**
- **TokenHub 企业版兼容** — 健康检查 `/models` 返回 404 时自动降级为 `/chat/completions` 探测
- **Provider 自定义健康端点** — 新增 `health_check_endpoint` 字段
- **一键域名绑定** — Cloudflare API Token 方案，全自动创建隧道 + DNS 配置

### v3.2.0 (2026-07)

**🔴 安全 & 性能**
- **Rate Limiting 限流** — 令牌桶算法，全局 QPS + 按 Consumer 独立限流，超限返回 429
- **CORS 白名单** — 支持精确匹配 + `*.example.com` 通配符子域，`cors_allowed_origins` 配置项
- **敏感字段加密统一** — `coze_api_token` 纳入 AES-256-GCM 加密范围，Provider APIKey 加密存储
- **JSON 解析错误处理** — 所有 API 端点解析失败统一返回 400 + 明确错误消息

**🟡 功能增强**
- **Provider 模型列表自动同步** — `SyncModels()` 方法 + `/api/providers/{id}/sync-models` 端点 + 管理面板一键同步按钮
- **联邦 Phase 3 Gossip-DHT 混合发现** — DHT 哈希环路由表，支持 successor/predecessor 查找
- **结构化日志系统** — `log_level` 配置，请求日志中间件，输出到 `data/access.log` + stdout
- **SSE 实时推送** — `/events` 端点，推送 Provider 状态变更、健康变化、配置更新
- **Prometheus 指标** — `/metrics` 端点，请求总数、延迟、错误率、Token 用量等
- **前端模块化** — admin.html JS 按功能分为 10+ 模块注释区域，结构清晰
- **配置热更新** — `SIGHUP` 信号触发配置重载，无需重启进程

**🐛 Bug 修复**
- 联邦配置开关保存后即时生效（修复 `federation_enabled` / `federation_relay_enabled` 不生效）
- 联邦配置 API 返回 `approval_mode` 和 `token_budget` 字段
- 管理面板版本号更新为 v3.2.0
