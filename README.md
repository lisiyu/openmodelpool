# ModelMux

**轻量级多平台 AI 模型统一代理管理系统** — 将 34+ AI 平台封装为 OpenAI 兼容 API，智能路由，一键部署。

> Go 重写版，单二进制，零依赖，极致性能。

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

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

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/modelmux/main/install.sh | bash
```

自定义端口和目录：

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/modelmux/main/install.sh | bash -s -- --port 9090 --dir /opt/modelmux
```

脚本自动完成：检测并安装 Docker → 克隆仓库 → 构建镜像 → 启动容器。后续更新同一行命令。

### 编译运行

```bash
# 克隆
git clone https://github.com/lisiyu/modelmux.git
cd modelmux

# 编译
make build

# 运行（默认端口 8000）
./modelmux
```

或一步完成：

```bash
make run
```

### Docker 手动部署

```bash
# 构建镜像
docker build -t modelmux .

# 启动容器
docker run -d \
  --name modelmux \
  --restart unless-stopped \
  -p 8000:8000 \
  -v $(pwd)/data:/app/data \
  -e TZ=Asia/Shanghai \
  modelmux
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
│                  ModelMux 代理网关                       │
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
modelmux/
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

## 📊 对比 Python 版

| | Python (coze-openai-proxy) | Go (ModelMux) |
|---|---|---|
| 每连接内存 | ~50-100KB | ~2-5KB |
| 冷启动 | ~2s | ~0.01s |
| 部署 | Python + pip + venv | 单二进制 |
| SSE 转发 | 逐 chunk 读写 | 零拷贝流式 |
| 并发模型 | asyncio + threads | goroutine |
| 安全 | 明文存储 | AES-256-GCM 加密 |
| 多用户 | ❌ | ✅ 邀请码 + 消费者 |
| 路由模式 | 单一路径 | 4 种智能路由 |
| 健康检测 | ❌ | ✅ 每 5 分钟探活 |

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

ModelMux 的诞生离不开以下优秀的开源项目和技术：

- [**Go**](https://go.dev/) — 简洁高效的编程语言，ModelMux 的基石
- [**golang-jwt/jwt**](https://github.com/golang-jwt/jwt) — 可靠的 JWT 认证实现
- [**golang.org/x/crypto**](https://pkg.go.dev/golang.org/x/crypto) — 安全的 bcrypt 密码哈希
- [**golang.org/x/net**](https://pkg.go.dev/golang.org/x/net) — SOCKS5 代理支持

灵感来源于以下优秀的开源 API 管理项目：

- [**one-api**](https://github.com/songquanpeng/one-api) — OpenAI 管理工具，为 AI 模型聚合提供了优秀的参考范式
- [**new-api**](https://github.com/Calcium-Ion/new-api) — one-api 的增强版，拓展了多用户和渠道管理的思路

感谢开源社区的持续贡献，让 AI 工具生态更加繁荣。
