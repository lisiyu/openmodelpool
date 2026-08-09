# Features

> Full feature catalog for OpenModelPool Agent. The homepage ([README](../README.md)) keeps only a summary; this file is the complete reference.

- Personal Mode is a pure-local proxy — no network participation, no identity generation, no sharing.
- Network Mode is opt-in — see [docs/FEDERATION.md](FEDERATION.md) for the sharing-network detail.

---

## 🏠 Personal Mode (Default)

### 🔌 Unified API Gateway

- **OpenAI-compatible interface** — Unified `/v1/chat/completions` + `/v1/messages` (Anthropic compatibility), supporting streaming (SSE) and non-streaming, zero-copy forwarding
- **37 preset platforms** — Coze, Sider.ai, OpenAI, Anthropic Claude, DeepSeek, Gemini, Qwen, Zhipu, Moonshot, MiniMax, SiliconFlow, Groq, xAI, Together, Mistral, Doubao, iFlytek, NVIDIA NIM, TokenHub (Coding/Plan/Enterprise), Baidu Qianfan, Stepfun, Baichuan, Novita AI, Fireworks AI, Cohere, Cerebras, OpenRouter, Poe, SID.ai, Agnes AI, AIHubMix, Ollama, LM Studio, iFlytek MaaS, and more
- **`provider/model` syntax** — Route to specific platforms via `deepseek/deepseek-chat` format, also supports OpenRouter-style routing
- **Auto platform discovery** — Automatically scans and discovers free AI platforms on the internet
- **🎁 Free Model Pool** — Auto-syncs 16+ permanently free LLM API providers from [awesome-free-llm-apis](https://github.com/mnfst/awesome-free-llm-apis), configured as low-priority public pool resources. Anonymous providers (OVHcloud) work zero-config; key-based providers can be enabled with a single paste in the admin UI
  - **Seeded at startup** — `Kilo Code` (api.kilo.ai, 12 models incl. gpt-4o / claude-3.5-sonnet / gemini-2.0-flash) and `OVHcloud AI Endpoints` (7 models) are hardcoded defaults created on first boot, so a fresh deployment has working free models **immediately, even with no network access to remote sync**. Remote sync then augments the list.
- **Web session template** — Generic `web_session` provider type for browser-login platforms (no API needed), Sider.ai as first template

### 🧠 4-Dimension Intelligent Routing

| Mode | Strategy |
|------|----------|
| 🎯 Priority | Sorted by preset priority |
| 💰 Cheapest | Selects the cheapest platform by `platform × model` pricing |
| ⚡ Fastest | Selects the fastest platform based on EWMA historical latency |
| 🧠 Composite | Weighted fusion of **4 dimensions**: **priority / cost / latency / tokens** (all customizable via the admin panel sliders) |

> **Personal Mode is 4-dimension** (priority / cost / latency / tokens). Network Mode scores nodes on a **5-dimension** weighted model (trust / reputation / latency / availability / contribution).
>
> The admin weight editor exposes **4 sliders in both modes, by design** — the 5th network dimension is the routing algorithm itself (weighted composition + regional adjustment + `SelectNode`), which is fixed backend behaviour and intentionally not user-tunable. The 4 sliders cover every adjustable weight. See the "Not a gap — by design" note in [README Implementation Status](../README.md#-implementation-status诚实状态).

### 🔗 Automatic Failover

Failed requests automatically switch to the next available Provider, forming a fallback chain until success or all candidates exhausted.

### 👥 Multi-User Support

- **Invite code registration** — Admin generates invite codes, consumers self-register
- **Provider sharing** — Consumers can add their own Providers to the unified proxy pool
- **Strict visibility isolation** — Admin sees all; consumers see only their own + system presets
- **Independent API Key** — Each consumer has an independent Proxy API Key
- **Usage tracking** — Per-consumer Token consumption and request count statistics
- **Multi-key management** — Multiple API Keys per Provider with individual quota control

### 💰 Token Budget Management

- **Dual-dimension pricing** — Per `platform × model` input/output price per million Tokens (USD)
- **Monthly budget** — Set monthly Token limits per Provider
- **Threshold alerts** — Automatic email alerts at 80% / 90% / 100% thresholds

### 🩺 Provider Auto Health Check

- Concurrent probing every **5 minutes** for all enabled Providers
- Status tracking: `healthy` / `degraded` / `down` / `unknown`
- Consecutive failure count, last success/failure time, failure reasons

### 🛡️ WAF 4-Layer Protection（已实现，默认启用）

| Layer | Function (designed) |
|-------|----------|
| Layer 1: Rate Limit | Global QPS + per-NodeID QPS + per-IP QPS (token bucket) |
| Layer 2: Token Limit | Pre-request token estimation guardrails |
| Layer 3: Content Safety | L1 hard block / L2 soft block / L3 log-only |
| Layer 4: Behavioral | High-frequency repetition / anomaly detection |

> **状态：已实现并默认启用。** `wafMiddleware` 已包裹 `/v1/*` 代理路由与 network relay 路由（`routes.go:17-22`、`routes.go:310-315`），由 `waf_enabled` 配置控制（默认 `true`）。四层规则与升级模型（warn → record → temp ban (2h) → long ban (7d) → permanent ban）均已实现并经测试（`waf_wire_test.go`、`waf_qa_test.go`）。`handleWAFStatus` 返回引擎实时状态。

### 🔐 Security & Encryption（真实实现）

- **AES-256-GCM — real implementation** — `encryptor.go` provides genuine AES-256-GCM symmetric encryption. All sensitive data (API Keys, SMTP passwords, Proxy API Keys) is encrypted at rest, with ciphertext tagged by the `omp:e:` prefix. This is **not** a placeholder: keys are derived and the GCM auth tag is verified on decrypt.
- **bcrypt** — Admin password hashing
- **JWT** — Token authentication with expiration
- **Data integrity** — HMAC-SHA256 signatures on critical data files to detect tampering
- **Rate limiting** — Token bucket algorithm with per-IP and per-Consumer independent limits

### 📝 Request Logging

- **In-memory ring buffer** — Up to 1000 request records, real-time view
- Fields: time, model, Provider, latency, Token count, cost, success/failure, retry count, streaming

### 📊 Usage Archiving

- Daily / monthly automatic usage data archiving
- 7-day / 30-day statistical views
- EWMA (Exponentially Weighted Moving Average) latency tracking

### 📧 SMTP Email Service

- **Forgot password** — Email reset code for admin password recovery
- **Password reset** — Via Proxy API Key
- **Budget alerts** — Token budget threshold email notifications
- **SMTP test** — One-click email test in admin panel

### 🌐 VMess Proxy Support

- Parse `vmess://` links, auto-start local Xray proxy
- Configure VMess outbound proxy per Provider for transparent request forwarding
- Auto-restore all VMess proxies on startup

### 🖥️ Web Admin Panel

- **Dark theme**, responsive design, mobile-friendly
- Initial setup wizard
- Provider management (CRUD, connectivity test, model list sync)
- Routing mode / weight configuration
- Usage statistics and request logs
- Invite code and consumer management
- Config export / import (AES-256-GCM encrypted)
- SMTP configuration management

---

## 🌐 Network Mode (Opt-In)

> **⚠️ Network Mode is disabled by default.** Personal Mode does all local proxying without any network activity.

When you opt in, your node joins the **AI Capability Sharing Network** — a decentralized P2P network where nodes share AI model access and exchange Contribution Credits.

### 🔑 Identity System (BIP39 Mnemonic) ⚠️

> **⚠️ Planned / not yet exposed.** The node mnemonic identity described below is currently a **vision**. `handleNodePubKey` returns an **empty pubkey** and the UI does **not** yet surface mnemonic generation or the derived Ed25519 key pair. No node identity is generated or broadcast in the current build.

| Component | Description |
|-----------|-------------|
| **BIP39 Mnemonic** | Generated when joining the network (12/24 words), manually backed up, never uploaded |
| **Ed25519 Key Pair** | Derived from mnemonic; private key never leaves this node; public key broadcast network-wide |
| **Node ID** | Unique identifier: `mmx-` + Base58(Ed25519 public key first 16 bytes) |
| **Signing** | All broadcast data (Providers, scores, credit transactions) signed by node private key |

### 🌍 P2P Node Discovery (Triple-Layer) ⚠️

> **⚠️ DHT layer currently disabled.** The former Kademlia DHT shell was removed; `GetDHTStats` returns `{"enabled":false}`. In the current build, P2P discovery actually relies on the **registry + gossip** path, **not** DHT. The DHT row below represents the design target.

| Mechanism | Purpose | Protocol |
|-----------|---------|----------|
| **Peer Seed** (:8001) | Initial bootstrapping; every online node can serve as seed | HTTPS + dynamic seed list |
| **Kademlia DHT** | Global node routing, capability registration (256-bit hash space, k=20 buckets) | SHA-256 XOR distance metric |
| **Gossip Protocol** | Real-time state propagation (node online/offline, capability changes) | Plumtree / Scuttlebutt variant |
| **LAN Discovery** | Local network node auto-discovery | mDNS |

### 🔗 联邦组网配置（v4.1.6 新增）

当两个自托管节点（如 `https://openmodelpool.io` 与 `https://openmodelpool.com`）需要互相发现、共享 provider 时，节点之间传播的公网可达地址由以下配置键决定。**优先级从高到低**：

| 优先级 | 配置键 | 说明 |
|---|---|---|
| 1（最高） | `federation_endpoint` | 显式配置的公网基址，例如 `https://openmodelpool.io`。完全确定，不受请求上下文影响。 |
| 2 | `public_domain` | **v4.1.6 新增**。建议设为节点的公网域名，例如 `https://openmodelpool.io`。当未显式配置 `federation_endpoint` 时使用，避免回落到内网主机名。 |
| 3 | 请求 `Host` 头 | HTTP 请求到达时，若上述两项均未配置，则使用 `https://<Host>`（仅在有请求上下文时生效，如生成邀请码）。 |
| 4（兜底，仅 LAN） | `http://<hostname>:<port>` | 以上均未命中时的最后兜底，使用本机主机名 + 端口（默认 8000）。**此地址通常不可达，会在日志打印 `[WARN] resolvePublicEndpoint fell back to LAN address`**，生产环境应配置前两项之一。 |

> 配置方式：在管理面板「共享网络」中设置，或直接写入 `config.json`（键名同上）。两个私有节点互连推荐至少配置 `public_domain`（或 `federation_endpoint`）。

**种子节点（`bootstrap_nodes`）**：用于「自动发现」的可信对端公网地址列表，元素形如 `https://openmodelpool.com`。互设后，节点会向这些地址的 `GET /federation/pool` 拉取对端节点信息（v4.1.6 已对该只读路径、仅对可信种子 Host 放行，修复原先的 403）。例如：

```json
{
  "bootstrap_nodes": ["https://openmodelpool.com"],
  "public_domain": "https://openmodelpool.io"
}
```

> 注意：私有 mesh 互连**不依赖**零配置自动发现。最可靠的方式是在「网络」页用「添加节点」表单手动粘贴对端公网地址，或使用「邀请码」互连。

### 🏆 Reputation System (EWMA-Tracked, S/A/B/C/D Grades)

| Grade | Score | Description |
|-------|-------|-------------|
| **S** | ≥ 200 | Excellent node, priority routing |
| **A** | ≥ 100 | Quality node |
| **B** | ≥ 50 | Normal node |
| **C** | ≥ 20 | Needs improvement |
| **D** | < 20 | Probation, may be removed after 7 days |

**Scoring formula**: `Score = Success Rate × 40% + Avg Latency × 25% + Uptime × 20% + Peer Rating × 15%`
**EWMA smoothing**: `New Score = 0.3 × Current + 0.7 × Previous` (α=0.3)

### 💎 Contribution Credit System ⚠️

> **⚠️ Ledger is currently local-only.** Contribution records are persisted locally with a **verifiable content hash** (`sha256:` prefix via `ContentHashStore`) — there is **no IPFS / distributed persistence** yet, and contribution credits are stored **locally only**. The anti-double-spend chain is backed by local signed records; durable multi-node replication is a future phase.

- **Earn**: Provide Provider resources that other nodes consume → the ledger credits you 1:1 with what you gave (requests without request-id are not counted). No fee, no interest, no inflation
- **Spend**: A verified contributor draws on its own entitlement instead of the anonymous per-IP abuse guard. Running out is **not** a rejection — the request falls through to the community free pool like anyone else's
- **Non-withdrawable**: Cannot be exchanged for fiat or financial assets
- **Non-transferable**: Cannot be transferred between nodes
- **Bound to Node ID**: Credits follow identity, not device
- **Anti-double-spend**: Each transaction includes predecessor hash, chain verification

### Contribution Credit eligibility (consumer-side draw)

`tryContributorDraw` lets a verified contributor spend their own entitlement in place of the anonymous per-IP abuse guard. `settle` refunds any unused portion and clamps the balance to zero (no negative quota). The draw is **non-exclusive**: if the contributor still has remaining personal balance, that is consumed first and the per-IP gate is skipped; otherwise the request falls back to the community free pool.

### 🔑 Key System

| Key Type | Prefix | Purpose |
|----------|--------|---------|
| Proxy API Key | `sk-` | Admin-configured proxy access key |
| Guest Proxy Key | `gk-` | Temporary access keys issued to guests, with quota limits |
| Public Global Key | `pk-` | Public experience key for zero-barrier network access, quadruple rate-limited |
| Provider Key | — | Upstream provider API keys (encrypted at rest) |

### 🔄 Quota Allocation (Guest Key / Public Key Pool)

Nodes configure how their shared resources are allocated:
- **Guest Key Pool**: Portion contributed to guest access (default 50%)
- **Public Key Pool**: Portion contributed to public global access (default 50%)
- Adjustable per node via admin panel

### ⚖️ Health-Aware Load Balancer

The network load balancer uses a 5-dimension scoring model for optimal node selection:

| Dimension | Weight | Description |
|-----------|--------|-------------|
| Trust | 25% | Trust from peer interactions |
| Reputation | 25% | Reputation Manager score |
| Latency | 20% | Network latency |
| Availability | 15% | Node uptime / reliability |
| Contribution | 15% | Contribution to the network |

These weights are backend defaults (`LBConfig`, §9.2). They are **not** exposed as a 5th admin slider — see the "Not a gap — by design" note in [README Implementation Status](../README.md#-implementation-status诚实状态).

Real-time metrics tracked per node: latency, CPU, memory, error rate, active connections, sliding-window history.

### 🌐 Public Access (Cloudflare Tunnel)

- **Quick Tunnel** (default): Free, no domain needed, auto-generated temporary address
- **Named Tunnel**: Custom domain with Cloudflare API Token for full automation
- **Manual Binding**: Bind your own domain without Cloudflare API

### 📡 Network API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /api/peers` | List all known peer nodes |
| `POST /api/register` | Node self-registration (heartbeat) |
| `GET /api/seed/health` | Seed service health check |
| `GET /api/network/status` | Current network status |
| `GET /api/network/peers` | Manage peer connections |
| `GET /api/network/routes` | View routing table |
| `GET /api/network/guest-keys` | Manage Guest Keys |
| `POST /api/network/guest-keys` | Issue new Guest Key |
| `GET /api/network/loadbalancer/status` | Load balancer status |
| `GET /api/waf/status` | WAF protection status |
| `GET /api/waf/violations` | WAF violation log |
| `GET /api/network/algorithm/current` | Current routing algorithm |
| `GET /api/network/regions` | Network region information |
| `GET /api/network/balance/status` | Load balance status |
