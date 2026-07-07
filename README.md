# ModelMux

**多平台 AI 模型统一代理网关** — 将 22+ AI 平台封装为 OpenAI 兼容 API，智能路由，一键切换。

> Go 重写版，极致性能，单二进制部署。

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## 特性

- 🔄 **OpenAI 兼容 API** — 统一 `/v1/chat/completions` 接口，支持流式/非流式
- 🏢 **22 个预置平台** — Coze、Sider、OpenAI、DeepSeek、Gemini、通义千问、腾讯混元等
- 🧠 **4 种路由模式** — 优先级 / 成本最低 / 速度最快 / 综合权重（4 维度融合）
- 🔗 **失败降级链** — 自动 fallback 到下一个可用平台
- ⚡ **极致性能** — Go 标准库 `net/http`，goroutine 并发，零拷贝流式转发
- 📊 **使用量追踪** — Token 消耗、成本估算、EWMA 延迟监控
- 🔐 **管理面板** — Web UI，JWT 认证，密码找回
- 🐳 **单二进制部署** — 无依赖，Docker 支持

## 快速开始

### 编译运行

```bash
# 编译
make build

# 运行（默认端口 8000）
make run

# 或直接
go run .
```

### Docker

```bash
docker build -t modelmux .
docker run -d -p 8000:8000 -v $(pwd)/data:/app/data modelmux
```

### 环境变量

```bash
export PORT=8000                    # 服务端口
export COZE_API_TOKEN=your_token    # 扣子 PAT（可选，可在管理面板配置）
export COZE_BOT_ID=your_bot_id      # 默认 Bot ID
```

## 使用方式

### 配置

1. 访问 `http://localhost:8000` 初始化管理员
2. 在管理面板中添加/启用 Provider，填入 API Key
3. 完成！

### API 调用

```bash
# 查看可用模型
curl http://localhost:8000/v1/models

# 聊天补全（非流式）
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

# 流式
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

### 指定平台

```bash
# 使用 provider/model 格式指定平台
curl ... -d '{"model": "deepseek/deepseek-chat", ...}'
```

## 路由模式

| 模式 | 说明 |
|------|------|
| 🎯 **优先级优先** | 按预设优先级排序（Coze > Sider > 混元 > Gemini > 其他） |
| 💰 **成本最低** | 按平台×模型定价选择最便宜的平台 |
| ⚡ **速度最快** | 根据 EWMA 历史响应时间选择最快的平台 |
| 🧠 **综合权重** | 加权融合 4 个维度：优先级(40%) + 成本(25%) + 延迟(20%) + 剩余Token(15%) |

综合权重的 4 个维度均可在管理面板中自定义。

## 预置平台

| 平台 | 优先级 | 类型 |
|------|--------|------|
| 扣子 (Coze) | 1 | 专有 API |
| Sider.ai | 2 | 网页 Token |
| 腾讯混元 TokenHub | 3 | OpenAI 兼容 |
| Google Gemini | 4 | OpenAI 兼容 |
| DeepSeek | 5 | OpenAI 兼容 |
| 通义千问 | 5 | OpenAI 兼容 |
| 智谱 AI | 5 | OpenAI 兼容 |
| Moonshot | 5 | OpenAI 兼容 |
| 零一万物 | 5 | OpenAI 兼容 |
| MiniMax | 5 | OpenAI 兼容 |
| 硅基流动 | 5 | OpenAI 兼容 |
| Groq | 5 | OpenAI 兼容 |
| xAI | 5 | OpenAI 兼容 |
| Together AI | 5 | OpenAI 兼容 |
| Mistral | 5 | OpenAI 兼容 |
| 豆包 | 5 | OpenAI 兼容 |
| 讯飞星火 | 5 | OpenAI 兼容 |
| Poe | 15 | OpenAI 兼容 |
| SID.ai | 15 | OpenAI 兼容 |
| OpenRouter | 20 | OpenAI 兼容 |
| OpenAI | 10 | OpenAI 兼容 |
| Ollama | 50 | OpenAI 兼容 |

## 项目结构

```
modelmux/
├── main.go          # 入口，HTTP 路由，中间件
├── types.go         # 数据模型（OpenAI 格式）
├── config.go        # 配置管理（JSON + 环境变量）
├── provider.go      # Provider 管理 + 智能路由
├── providers.go     # 22 个预置平台定义
├── client.go        # 上游请求转发（OpenAI/Sider/Coze）
├── tracker.go       # 使用量追踪 + EWMA + 批量写入
├── pricing.go       # 模型定价表
├── sider.go         # Sider Token 状态监控
├── auth.go          # JWT 认证 + 密码管理
├── admin.go         # 管理面板 API
├── go.mod
├── Makefile
└── Dockerfile
```

## 对比 Python 版

| | Python (coze-openai-proxy) | Go (modelmux) |
|---|---|---|
| 每连接内存 | ~50-100KB | ~2-5KB |
| 冷启动 | ~2s | ~0.01s |
| 部署 | Python + pip + venv | 单二进制 |
| SSE 转发 | 逐 chunk 读写 | io.Copy 零拷贝 |
| 并发模型 | asyncio + threads | goroutine |

## License

MIT
