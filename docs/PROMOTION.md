# OpenModelPool Agent 推广文案合集

---

## 一、主推广文案（适用于 V2EX / 掘金 / 知乎 / GitHub Discussion）

### 标题建议（三选一）

1. **OpenModelPool Agent — 把 34+ AI 平台塞进一个 API 地址，我造了个去中心化联邦管理网关**
2. **受够了每接一个 AI 平台就改一套代码？OpenModelPool Agent 一个地址搞定所有**
3. **一个人管 34 个 AI 平台，还组了个去中心化联邦网络 — OpenModelPool Agent v3.1**

---

### 正文

各位好，

先说痛点。

如果你跟我一样，日常折腾十几个 AI 平台 — OpenAI、Claude、Gemini、千帆、通义、Coze、NVIDIA NIM…… 你一定体会过以下场景：

- 每接一个新平台，写一套 SDK 适配代码，改一套环境变量
- 想在 A 平台挂了之后自动切到 B 平台？手写 fallback 逻辑写到怀疑人生
- 团队里几个人共用几把 key，计费全靠人肉统计，月底对账对到头皮发麻
- 想搞个成本最低的模型路由，结果发现没有一个统一的地方管理所有定价

更离谱的是，当你有 5 台服务器各跑一个代理，它们之间完全是孤岛 — 互相不知道对方存在，没有协同，没有信誉，没有联邦。

**所以我写了 OpenModelPool Agent。**

---

### 🧩 它是什么

一句话：**去中心化 AI 模型联邦管理平台**。

技术上说，它是一个用 Go 写的单二进制程序，把 **34+ 个 AI 平台** 统一封装成 **OpenAI 兼容的 `/v1/chat/completions` 接口**。你只需要一个地址、一把 Key，就能调用所有平台的所有模型。

```bash
curl https://your-mux.com/v1/chat/completions \
  -H "Authorization: Bearer YOUR_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model": "deepseek/deepseek-chat", "messages": [{"role":"user","content":"Hello"}]}'
```

注意 `model` 字段 — `deepseek/deepseek-chat` 这种 `provider/model` 语法，可以直接指定走哪个平台。也支持 OpenRouter 风格路由。

---

### ⚡ 核心特性

**统一 API 网关**
- 34 个预置平台开箱即用（OpenAI / Claude / Gemini / DeepSeek / Coze / 百度千帆 / NVIDIA NIM / 通义千问 / 智谱 / 月之暗面 / xAI / Groq / Ollama 等等）
- 完整的 SSE 流式转发，零拷贝中继
- 所有平台一个入口，客户端零改动

**4 种智能路由**
- 🎯 **优先级优先**：按预设优先级排序，依次尝试
- 💰 **成本最低**：按「平台×模型」双维度定价，自动选最便宜的
- ⚡ **速度最快**：基于 EWMA 延迟追踪，实时选最快的节点
- 🎛️ **综合权重**：自定义权重因子，精细化控制流量分配

**多用户 + 邀请码体系**
- 多用户系统，每个用户独立 Key
- 邀请码机制控制注册
- 积分经济：用户通过邀请获得积分，通过调用消耗积分

**去中心化联邦网络（v3.0+）**
- Ed25519 密钥对标识节点身份
- Gossip 协议实现节点发现与状态同步
- 信誉系统：节点间基于历史表现计算信任度
- Genesis Hash 网络身份锚定 — fork 即可互通，零配置加入联邦

**运维友好**
- 实时健康检测 + 自动降级，平台挂了秒切
- 可视化 Admin 面板，所有配置 GUI 管理
- Cloudflare Tunnel 一键公网暴露
- Docker / 二进制双部署，一行命令上线
- SMTP 邮件通知
- Prometheus 指标暴露（v3.2.0 新增）
- Rate Limiting 限流保护（v3.2.0 新增）

---

### 🔧 技术亮点

| 设计点 | 实现方式 |
|---|---|
| 性能 | Go 单二进制，零 CGO 依赖，内存占用极低 |
| 加密 | AES-256-GCM 加密所有 API Key 等敏感数据 |
| 身份 | Ed25519 签名体系，节点身份不可伪造 |
| 路由 | EWMA（指数加权移动平均）延迟追踪，比简单均值更贴近真实 |
| 同步 | Gossip 协议去中心化同步，无中心节点瓶颈 |
| 代理 | 支持 VMess / SOCKS5 / HTTP 代理，Provider 级别独立配置 |
| 安全 | Provider 级别代理隔离，合规条款声明 |

---

### 🚀 快速上手

**Docker 部署（推荐）**
```bash
docker run -d \
  --name openmodelpool \
  -p 3000:3000 \
  -v $(pwd)/data:/app/data \
  ghcr.io/lisiyu/openmodelpool:latest
```

**二进制部署**
```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/install.sh | bash
```

启动后访问 `http://localhost:3000` 进入初始化向导，3 分钟完成全部配置。

---

### 🌐 在线体验

> 🔗 **GitHub**: https://github.com/lisiyu/openmodelpool
>
> 欢迎 Star ⭐、Fork 🔱、提 Issue 🐛、贡献 PR 🎉

---

### 🗺️ Roadmap

- ✅ v3.1 — 34 平台预置 + Genesis Hash Phase 1
- 🔜 v3.2 — Prometheus 指标 + Rate Limiting（即将发布）
- 🔮 Phase 2 — 签名邀请链，联邦节点间可验证的信任传递
- 🔮 Phase 3 — 跨联邦模型发现与负载均衡

---

如果你也有多平台 AI 管理的痛点，或者对去中心化联邦网络感兴趣，欢迎来聊聊。

这个项目是开源的，MIT 协议，随便折腾。

---

## 二、短文案版本

### Twitter/X 版（英文，200 字符内）

> 🧩 OpenModelPool Agent — 1 API endpoint for 34+ AI platforms. Smart routing, auto-fallback, decentralized federation. Go binary, zero deps.
>
> ⭐ https://github.com/lisiyu/openmodelpool
>
> #AI #LLM #OpenSource #Golang

### 微信朋友圈 / 中文短版

> 造了个工具：**OpenModelPool Agent** — 一个 API 地址代理 34+ AI 平台（OpenAI/Claude/Gemini/千帆/Coze/NIM…），智能路由自动降级，还搞了个去中心化联邦网络。Go 单二进制，Docker 一键部署。
>
> 🔗 github.com/lisiyu/openmodelpool

### Telegram 群发版

> 🤖 **OpenModelPool Agent v3.1** — 去中心化 AI 模型联邦管理平台
>
> ✅ 34+ 平台统一 OpenAI 兼容 API
> ✅ 4 种智能路由（优先级/成本/速度/权重）
> ✅ Ed25519 身份 + Gossip 同步 + 信誉系统
> ✅ Docker 一键部署，可视化 Admin 面板
> ✅ v3.2 即将发布：Prometheus + Rate Limiting
>
> 🔗 https://github.com/lisiyu/openmodelpool

---

## 三、GitHub Social Preview Card 文案建议

**尺寸：1280×640**

### 方案 A（极简科技风）

- 主标题：**OpenModelPool Agent**
- 副标题：**One API. 34+ Platforms. Decentralized.**
- 底部 tag：`Go` · `OpenAI Compatible` · `Smart Routing` · `Federation`
- 视觉：深色背景 + 节点网络连线图 + 渐变蓝紫色调

### 方案 B（功能展示风）

- 左侧大标题：**OpenModelPool Agent**
- 右侧列出核心卖点（用图标）：
  - 🔌 34+ Platforms
  - 🧠 Smart Routing
  - 🔐 Ed25519 Identity
  - 🌐 Decentralized Federation
- 底部 CTA：`⭐ Star on GitHub`
- 视觉：左侧文字，右侧抽象网络拓扑图

### 方案 C（极简口号风）

- 居中大字：**Your AI, Unified.**
- 下方小字：**OpenModelPool Agent — Decentralized AI Model Federation Platform**
- 背景：深色系 + 微光粒子效果

---

## 四、README 社交分享页方案说明

详见同目录下的 `share.html` 文件。

该页面设计为一个独立可部署的 HTML 单页，功能包括：

1. **项目简介区**：OpenModelPool Agent 核心卖点展示
2. **邀请链接区**：一键复制当前页面链接
3. **二维码生成**：使用 qrcode.js CDN 自动生成页面二维码
4. **社交分享按钮**：
   - 微信（复制链接 + 提示手动分享）
   - Twitter/X（预填文案 + 链接）
   - Telegram（预填文案 + 链接）
   - Reddit（预填标题 + 链接）
5. **移动端适配**：响应式设计，手机浏览体验良好

部署方式：将此文件放到任意静态服务器或 GitHub Pages 即可使用。

---

*文档最后更新：2025-07*
