# Platforms

> Preset platform list and configuration guide for non-OpenAI-compatible providers. Homepage summary: [README](../README.md).

---

## 📦 Preset Platforms (37)

| # | Platform | Priority | Type | Highlights |
|---|----------|----------|------|------------|
| 1 | Coze | 1 | Proprietary API | AI agent platform, `coze-{bot_id}` model format |
| 2 | LM Studio (Local) | 1 | OpenAI Compatible | Local model hosting, zero latency |
| 3 | Sider.ai (Web) | 2 | Web Token | Web multi-model aggregation, login Token required |
| 4 | Anthropic Claude | 2 | Proprietary API | Claude 3.5/4 series, Messages API adapter |
| 5 | Tencent TokenHub Coding Plan | 3 | OpenAI Compatible | Programming plan, request-count limits, `sk-sp-xxxx` keys |
| 6 | Tencent TokenHub Token Plan | 3 | OpenAI Compatible | Personal token subscription, `sk-tp-xxxx` keys |
| 7 | Tencent TokenHub Enterprise | 3 | OpenAI Compatible | Enterprise credits, multi-key quota, team sharing |
| 8 | Google Gemini | 4 | OpenAI Compatible | Multimodal, ultra-long context, 2.5 Pro/Flash series |
| 9 | NVIDIA NIM | 4 | OpenAI Compatible | 100+ models free inference, 40 RPM free tier |
| 10 | Cerebras | 4 | OpenAI Compatible | Extreme inference speed, WSE chip |
| 11 | OpenAI | 10 | OpenAI Compatible | GPT-4o, o1, o3, o4-mini |
| 12 | Poe | 15 | OpenAI Compatible | Quora multi-model aggregation |
| 13 | SID.ai | 15 | OpenAI Compatible | Developer platform |
| 14 | OpenRouter | 20 | OpenAI Compatible | Global model aggregation |
| 15 | Ollama (Local) | 50 | OpenAI Compatible | Local model deployment, zero latency |
| 16 | DeepSeek | 5 | OpenAI Compatible | High-performance domestic LLM, V3/R1 |
| 17 | Qwen | 5 | OpenAI Compatible | Alibaba Cloud Qwen Turbo/Plus/Max/Long |
| 18 | Zhipu AI | 5 | OpenAI Compatible | GLM-4 series, including vision models |
| 19 | Moonshot (Kimi) | 5 | OpenAI Compatible | Long context 8K/32K/128K |
| 20 | Lingyi Wanwu | 5 | OpenAI Compatible | Yi series |
| 21 | MiniMax | 5 | OpenAI Compatible | MiniMax large models |
| 22 | SiliconFlow | 5 | OpenAI Compatible | Open-source model aggregation |
| 23 | Groq | 5 | OpenAI Compatible | Ultra-fast inference speed |
| 24 | xAI (Grok) | 5 | OpenAI Compatible | Grok 2/3 series |
| 25 | Together AI | 5 | OpenAI Compatible | Open-source model inference platform |
| 26 | Mistral AI | 5 | OpenAI Compatible | Leading European LLM, including Codestral |
| 27 | Doubao (Volcano Engine) | 5 | OpenAI Compatible | ByteDance Doubao |
| 28 | iFlytek Spark | 5 | OpenAI Compatible | iFlytek Spark |
| 29 | Baidu Qianfan | 5 | OpenAI Compatible | ERNIE series |
| 30 | Stepfun | 5 | OpenAI Compatible | Step series models |
| 31 | Baichuan | 5 | OpenAI Compatible | Baichuan series |
| 32 | Novita AI | 5 | OpenAI Compatible | Aggregation platform |
| 33 | Fireworks AI | 5 | OpenAI Compatible | High-speed inference platform |
| 34 | Cohere | 5 | OpenAI Compatible | Enterprise NLP, Command R+ |
| 35 | Agnes AI | 5 | OpenAI Compatible | Text/Image/Video multi-modal |
| 36 | AIHubMix | 5 | OpenAI Compatible | Multi-provider aggregation |
| 37 | iFlytek MaaS | 5 | OpenAI Compatible | iFlytek model-as-a-service, Spark X1 models |

---

## 🔧 Non-OpenAI-Compatible Platform Configuration Guide

The following 3 platforms use proprietary APIs and require special configuration. All non-standard API Keys/Tokens are configured in the **Provider edit interface**.

### 🎯 Coze

**API Type:** Proprietary Chat API (`/v3/chat` + polling)
**API Key Format:** Personal Access Token (PAT), format `pat_xxxxxxxxxxxx`

**How to get:**
1. Login to [Coze Open Platform](https://www.coze.cn)
2. Top-right avatar → **API Token** → **Create Token**
3. Name and copy the token (shown only once at creation)

**Configuration:** Fill in the PAT token in the Provider edit interface **API Key** field
**Calling:** Model name format `coze-{bot_id}`
```bash
curl -d '{"model": "coze-7xxxxxxxxxx0", "messages": [...]}'
```

### 🌐 Sider.ai (Web)

**API Type:** Web private API (`/api/v3/completion/text`)
**API Key Format:** Browser extension Session Token

**How to get:**
1. Install [Sider.ai Chrome Extension](https://sider.ai/) and login
2. F12 → **Application** → **Cookies** → `sider.ai` → copy `token` field value

**Note:** Token expires periodically, needs regular updates; built-in health check auto-detects expiration

**Web Session Template:** Sider.ai uses the `web_session` provider type — a generic template for browser-login platforms. Other browser-based AI platforms can be added using the same template without writing custom code.

### 🟠 Anthropic Claude

**API Type:** Messages API (`/v1/messages`)
**API Key Format:** `sk-ant-xxxxx` (x-api-key header auth)

**How to get:**
1. Login to [Anthropic Console](https://console.anthropic.com/)
2. **API Keys** → **Create Key** → Copy

**Auto-adaptation:** System messages extracted independently, proprietary auth headers, SSE event auto-conversion
