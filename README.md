# OpenModelPool Agent

**Personal Model Proxy + Geek Sharing Network** — Your local AI gateway first, then optionally join the global AI capability sharing network.

> Network has no borders; AI capabilities shouldn't either.

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v4.0.1-blue)](#)

---

## 🤖 What Is It?

**OpenModelPool Agent is a temporary Token bank + a geek sharing network.**

By default, it is a **pure-local personal model proxy** — managing your API tokens, providing a unified OpenAI-compatible interface, and tracking usage. No network, no sharing, no network identity generated.

Only when you configure Provider Tokens, enable quota management, and the system detects idle quota this month, will it gently prompt you: **Would you like to share some idle quota to the network?**

> Your GPT-4o quota is only 60% used this month? The remaining 40% expires and goes to waste.
> Share it to the OpenModelPool network, and you earn **Contribution Credits** when others use it.
> When you need Claude-3 in the future, use Contribution Credits to reclaim equivalent resources.
> If you never reclaim — these contributions naturally become a geek gift to the community.

**Three principles**:
- Configuring Token ≠ Joining the sharing network
- Having idle quota ≠ Auto-sharing
- Joining the sharing network ≠ Sharing all quota

To upstream providers, this is exactly the same as anyone calling the API directly — same Key, same quota, same provider. **No "reselling", no "middleman", just an Agent forwarding your requests.**

---

## 🌍 Our Belief

The internet's greatest creation was breaking the boundaries of information.

BitTorrent let knowledge escape server monopolies; IPFS let storage escape single-node dependency; Tor let communication escape geographic constraints.

**OpenModelPool Agent does the same thing — but what's shared is not files, but AI capabilities.**

We believe a developer in New York with a Claude API and a programmer in Beijing have equally valuable access. When global AI capabilities converge through a decentralized network, anyone can equally access the most powerful intelligence — regardless of where they are.

This is not a commercial product. This is the continuation of internet spirit: **sharing, openness, no borders.**

> **v4.0.1 Note**: In the BT network, seeders could be anonymous and still participate. In OpenModelPool, joining the sharing network requires an identity (mnemonic → Ed25519 key pair → Node ID), and Contribution Credits are bound to identity. This is not for censorship, but for Sybil defense — ensuring contributions are traceable and preventing one person from impersonating a thousand nodes to farm credits.
>
> We also provide a **Public Global Key** — anyone can use it to experience model capabilities in the network with zero barrier. It's a low-quota experience entry with quadruple rate limiting, not guaranteed always-available, but lets you feel the network's value at zero cost.

---

## 📋 Implementation Status（诚实状态）

> This section honestly tracks what is actually wired up in the current `workbody` branch versus what remains planned. It is kept in sync with the **code**, not the vision. Items marked ⚠️ are partially designed or stubbed and are **not yet functional end-to-end**.

### ✅ Implemented & Usable

| Area | State |
|------|-------|
| OpenAI-compatible unified gateway (`/v1/chat/completions`, `/v1/models`, `/v1/embeddings`, `/v1/completions`) | ✅ Real, working |
| Personal-mode **4-dimension** routing weights (priority / cost / latency / tokens), editable via admin sliders | ✅ Real |
| Automatic failover, multi-user, token budget, provider health check | ✅ Real |
| **Real AES-256-GCM encryption** (`encryptor.go`; prefix `omp:e:`) for API keys / config fields | ✅ Real |
| Request logging, usage archiving, SMTP email, Web admin panel | ✅ Real |
| Network mode — reputation system (EWMA, S/A/B/C/D), contribution credits, key system (guest/public key issue/list/revoke), quota allocation, health-aware load balancing, Global Pool (join/contribute/nodes/stats), algorithm governance chain params (current/history/propose/vote/validate), region (minimal stub, compiles) | ✅ Implemented (some as minimal/partial stubs — see ⚠️ below) |
| Federated trust pool (registry-based `trust_pool.json`) | ✅ Real |

### ⚠️ Planned / Not Yet Wired

| Area | Current State |
|------|---------------|
| BIP39 mnemonic node identity | ⚠️ `handleNodePubKey` returns empty pubkey; UI not yet exposed. The "Identity System (BIP39 Mnemonic)" below is currently a **vision**; the node pubkey interface is not yet generated/displayed |
| DHT | ⚠️ Former empty shell removed; `GetDHTStats` returns `{"enabled":false}`. The DHT layer in triple-layer discovery is **currently disabled**; P2P discovery relies on registry/gossip, not DHT |
| IPFS ledger | ⚠️ Ledger is currently an **in-memory `GossipLedger` stub**, no IPFS persistence; contribution credits stored locally only |
| WAF 4-layer protection | ⚠️ `handleWAFStatus` returns `enabled:false`; the WAF engine is **not yet wired into the proxy request path** |
| Algorithm governance DAO voting | ⚠️ `propose`/`vote` endpoints accept locally and return status; on-chain / decentralized voting is **not implemented** |
| 5-dimension network routing | ⚠️ README describes network mode as 5-dimension (trust/reputation/latency/availability/contribution), but the admin weight editor currently exposes **only 4 sliders** |
| Regional routing | ⚠️ Compiles (minimal stub), but real geo-based routing is not wired (`handleNetworkRegions` returns empty) |

---

## ✨ Core Features

### 🏠 Personal Mode (Default)

Personal Mode is a pure-local proxy — no network participation, no identity generation, no sharing.

#### 🔌 Unified API Gateway

- **OpenAI-compatible interface** — Unified `/v1/chat/completions`, supporting streaming (SSE) and non-streaming, zero-copy forwarding
- **36 preset platforms** — Coze, Sider.ai, OpenAI, Anthropic Claude, DeepSeek, Gemini, Qwen, Zhipu, Moonshot, MiniMax, SiliconFlow, Groq, xAI, Together, Mistral, Doubao, iFlytek, NVIDIA NIM, TokenHub (Coding/Plan/Enterprise), Baidu Qianfan, Stepfun, Baichuan, Novita AI, Fireworks AI, Cohere, Cerebras, OpenRouter, Poe, SID.ai, Agnes AI, AIHubMix, Ollama, LM Studio, and more
- **`provider/model` syntax** — Route to specific platforms via `deepseek/deepseek-chat` format, also supports OpenRouter-style routing
- **Auto platform discovery** — Automatically scans and discovers free AI platforms on the internet

#### 🧠 4-Dimension Intelligent Routing

| Mode | Strategy |
|------|----------|
| 🎯 Priority | Sorted by preset priority |
| 💰 Cheapest | Selects the cheapest platform by `platform × model` pricing |
| ⚡ Fastest | Selects the fastest platform based on EWMA historical latency |
| 🧠 Composite | Weighted fusion of **4 dimensions**: **priority / cost / latency / tokens** (all customizable via the admin panel sliders) |

> **Personal Mode is 4-dimension** (priority / cost / latency / tokens). Network Mode's **design target** is 5-dimension (trust / reputation / latency / availability / contribution), but the admin weight editor currently exposes **only 4 sliders** — the 5th (network-specific) dimension is not yet surfaced in the UI.

#### 🔗 Automatic Failover

Failed requests automatically switch to the next available Provider, forming a fallback chain until success or all candidates exhausted.

#### 👥 Multi-User Support

- **Invite code registration** — Admin generates invite codes, consumers self-register
- **Provider sharing** — Consumers can add their own Providers to the unified proxy pool
- **Strict visibility isolation** — Admin sees all; consumers see only their own + system presets
- **Independent API Key** — Each consumer has an independent Proxy API Key
- **Usage tracking** — Per-consumer Token consumption and request count statistics
- **Multi-key management** — Multiple API Keys per Provider with individual quota control

#### 💰 Token Budget Management

- **Dual-dimension pricing** — Per `platform × model` input/output price per million Tokens (USD)
- **Monthly budget** — Set monthly Token limits per Provider
- **Threshold alerts** — Automatic email alerts at 80% / 90% / 100% thresholds

#### 🩺 Provider Auto Health Check

- Concurrent probing every **5 minutes** for all enabled Providers
- Status tracking: `healthy` / `degraded` / `down` / `unknown`
- Consecutive failure count, last success/failure time, failure reasons

#### 🛡️ WAF 4-Layer Protection（架构已设计，尚未激活）

| Layer | Function (designed) |
|-------|----------|
| Layer 1: Rate Limit | Global QPS + per-NodeID QPS + per-IP QPS (token bucket) |
| Layer 2: Token Limit | Pre-request token estimation guardrails |
| Layer 3: Content Safety | L1 hard block / L2 soft block / L3 log-only |
| Layer 4: Behavioral | High-frequency repetition / anomaly detection |

> **⚠️ Status: not yet active.** The WAF engine is **not yet wired into the proxy request path**. `handleWAFStatus` currently returns `enabled:false`. The four-layer rules and escalation model (warn → record → temp ban (2h) → long ban (7d) → permanent ban) are **designed and documented**, but enforcement is pending integration.

#### 🔐 Security & Encryption（真实实现）

- **AES-256-GCM — real implementation** — `encryptor.go` provides genuine AES-256-GCM symmetric encryption. All sensitive data (API Keys, SMTP passwords, Proxy API Keys) is encrypted at rest, with ciphertext tagged by the `omp:e:` prefix. This is **not** a placeholder: keys are derived and the GCM auth tag is verified on decrypt.
- **bcrypt** — Admin password hashing
- **JWT** — Token authentication with expiration
- **Data integrity** — HMAC-SHA256 signatures on critical data files to detect tampering
- **Rate limiting** — Token bucket algorithm with per-IP and per-Consumer independent limits

#### 📝 Request Logging

- **In-memory ring buffer** — Up to 1000 request records, real-time view
- Fields: time, model, Provider, latency, Token count, cost, success/failure, retry count, streaming

#### 📊 Usage Archiving

- Daily / monthly automatic usage data archiving
- 7-day / 30-day statistical views
- EWMA (Exponentially Weighted Moving Average) latency tracking

#### 📧 SMTP Email Service

- **Forgot password** — Email reset code for admin password recovery
- **Password reset** — Via Proxy API Key
- **Budget alerts** — Token budget threshold email notifications
- **SMTP test** — One-click email test in admin panel

#### 🌐 VMess Proxy Support

- Parse `vmess://` links, auto-start local Xray proxy
- Configure VMess outbound proxy per Provider for transparent request forwarding
- Auto-restore all VMess proxies on startup

#### 🖥️ Web Admin Panel

- **Dark theme**, responsive design, mobile-friendly
- Initial setup wizard
- Provider management (CRUD, connectivity test, model list sync)
- Routing mode / weight configuration
- Usage statistics and request logs
- Invite code and consumer management
- Config export / import (AES-256-GCM encrypted)
- SMTP configuration management

---

### 🌐 Network Mode (Opt-In)

> **⚠️ Network Mode is disabled by default.** Personal Mode does all local proxying without any network activity.

When you opt in, your node joins the **AI Capability Sharing Network** — a decentralized P2P network where nodes share AI model access and exchange Contribution Credits.

#### 🔑 Identity System (BIP39 Mnemonic) ⚠️

> **⚠️ Planned / not yet exposed.** The node mnemonic identity described below is currently a **vision**. `handleNodePubKey` returns an **empty pubkey** and the UI does **not** yet surface mnemonic generation or the derived Ed25519 key pair. No node identity is generated or broadcast in the current build.

| Component | Description |
|-----------|-------------|
| **BIP39 Mnemonic** | Generated when joining the network (12/24 words), manually backed up, never uploaded |
| **Ed25519 Key Pair** | Derived from mnemonic; private key never leaves this node; public key broadcast network-wide |
| **Node ID** | Unique identifier: `mmx-` + Base58(Ed25519 public key first 16 bytes) |
| **Signing** | All broadcast data (Providers, scores, credit transactions) signed by node private key |

#### 🌍 P2P Node Discovery (Triple-Layer) ⚠️

> **⚠️ DHT layer currently disabled.** The former Kademlia DHT shell was removed; `GetDHTStats` returns `{"enabled":false}`. In the current build, P2P discovery actually relies on the **registry + gossip** path, **not** DHT. The DHT row below represents the design target.

| Mechanism | Purpose | Protocol |
|-----------|---------|----------|
| **Peer Seed** (:8001) | Initial bootstrapping; every online node can serve as seed | HTTPS + dynamic seed list |
| **Kademlia DHT** | Global node routing, capability registration (256-bit hash space, k=20 buckets) | SHA-256 XOR distance metric |
| **Gossip Protocol** | Real-time state propagation (node online/offline, capability changes) | Plumtree / Scuttlebutt variant |
| **LAN Discovery** | Local network node auto-discovery | mDNS |

#### 🏆 Reputation System (EWMA-Tracked, S/A/B/C/D Grades)

| Grade | Score | Description |
|-------|-------|-------------|
| **S** | ≥ 200 | Excellent node, priority routing |
| **A** | ≥ 100 | Quality node |
| **B** | ≥ 50 | Normal node |
| **C** | ≥ 20 | Needs improvement |
| **D** | < 20 | Probation, may be removed after 7 days |

**Scoring formula**: `Score = Success Rate × 40% + Avg Latency × 25% + Uptime × 20% + Peer Rating × 15%`
**EWMA smoothing**: `New Score = 0.3 × Current + 0.7 × Previous` (α=0.3)

#### 💎 Contribution Credit System ⚠️

> **⚠️ Ledger is currently in-memory.** The ledger is an in-memory **`GossipLedger` stub** — there is **no IPFS persistence** yet, and contribution credits are stored **locally only**. The anti-double-spend chain described below is designed but not yet backed by durable/IPFS storage.

- **Earn**: Provide Provider resources that other nodes consume → earn Contribution Credits (requests without request-id are not counted)
- **Spend**: Call other nodes' Providers, send P2P messages, etc.
- **Non-withdrawable**: Cannot be exchanged for fiat or financial assets
- **Non-transferable**: Cannot be transferred between nodes
- **Bound to Node ID**: Credits follow identity, not device
- **Anti-double-spend**: Each transaction includes predecessor hash, chain verification

#### 🔑 Key System

| Key Type | Prefix | Purpose |
|----------|--------|---------|
| Proxy API Key | `sk-` | Admin-configured proxy access key |
| Guest Proxy Key | `gk-` | Temporary access keys issued to guests, with quota limits |
| Public Global Key | `pk-` | Public experience key for zero-barrier network access, quadruple rate-limited |
| Provider Key | — | Upstream provider API keys (encrypted at rest) |

#### 🔄 Quota Allocation (Guest Key / Public Key Pool)

Nodes configure how their shared resources are allocated:
- **Guest Key Pool**: Portion contributed to guest access (default 50%)
- **Public Key Pool**: Portion contributed to public global access (default 50%)
- Adjustable per node via admin panel

#### ⚖️ Health-Aware Load Balancer

The network load balancer uses a 5-dimension scoring model for optimal node selection:

| Dimension | Weight | Description |
|-----------|--------|-------------|
| Trust | 25% | Trust from peer interactions |
| Reputation | 25% | Reputation Manager score |
| Latency | 20% | Network latency |
| Availability | 15% | Node uptime / reliability |
| Contribution | 15% | Contribution to the network |

Real-time metrics tracked per node: latency, CPU, memory, error rate, active connections, sliding-window history.

#### 🌐 Public Access (Cloudflare Tunnel)

- **Quick Tunnel** (default): Free, no domain needed, auto-generated temporary address
- **Named Tunnel**: Custom domain with Cloudflare API Token for full automation
- **Manual Binding**: Bind your own domain without Cloudflare API

#### 📡 Network API Endpoints

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

---

## 🧭 Project Vision

OpenModelPool Agent evolves from a lightweight personal AI proxy into a **decentralized AI capability sharing network**:

```
  Phase 0                    Phase 1                    Phase 2                    Phase 3
┌──────────────┐      ┌──────────────────┐      ┌──────────────────┐      ┌──────────────────┐
│ Personal MVP │  →→  │ Min Viable Share │  →→  │ P2P Enhancement  │  →→  │ Autonomous Network│
│ Local proxy  │      │ Dual-mode arch   │      │ Multi-hop relay  │      │ Reputation system│
│ Quota mgmt   │      │ Mnemonic identity│      │ Path encryption  │      │ Decentralized    │
│ 36 platforms │      │ Single-hop relay │      │ P2P discovery    │      │ governance       │
│ 5-dim router │      │ Contribution     │      │ Capability verify│      │ Full self-govern │
└──────────────┘      └──────────────────┘      └──────────────────┘      └──────────────────┘
```

- **Phase 0** ✅ Personal MVP (current) — 36 platforms, 5-dimension routing, multi-user, local quota management, WAF, Token estimation
- **Phase 1** 🔜 Min Viable Sharing — Dual-mode architecture, mnemonic identity, single-hop relay, Contribution Credits, two-level switch
- **Phase 2** 🌐 P2P Enhancement — Multi-hop relay, transport path encryption, P2P capability discovery, capability verification protocol
- **Phase 3** 🧠 Autonomous Network — Reputation system, notary decentralization evolution, fully self-governing

> See [ROADMAP_v3.md](ROADMAP_v3.md) for details

---

## 🚀 Quick Start

### One-Click Install (Recommended)

**Linux / macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/install.sh | bash
```

Custom parameters:

```bash
curl -fsSL https://raw.githubusercontent.com/lisiyu/openmodelpool/main/install.sh | bash -s -- --port 9090 --dir /opt/openmodelpool
```

**Windows (PowerShell as Admin):**

```powershell
irm "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1?t=$(Get-Date -Format 'yyyyMMddHHmmss')" | iex
```

The manager script provides an interactive menu: install / upgrade / uninstall / tunnel setup (Cloudflare / FRP / ngrok) / port change / status check / restart — all in one.

### Build from Source

```bash
# Clone
git clone https://github.com/lisiyu/openmodelpool.git
cd openmodelpool

# Build for current platform
make build

# Run (default port 8000)
./openmodelpool
```

### Docker Deployment

```bash
# Docker Compose (recommended)
docker compose up -d

# Or manually
docker build -t openmodelpool .
docker run -d --name openmodelpool -p 8000:8000 -v $(pwd)/data:/app/data openmodelpool
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Service port | `8000` |
| `COZE_API_TOKEN` | Coze PAT (optional, configurable in panel) | — |
| `COZE_BOT_ID` | Default Coze Bot ID | — |

### First Use

1. Visit `http://localhost:8000` to enter the setup wizard
2. Set admin account (username, password, email)
3. Add Providers and fill in API Keys in the admin panel
4. Done! Call via `/v1/chat/completions`

---

## 📡 API Documentation

### Proxy Interface (OpenAI Compatible)

#### `GET /v1/models`

List all available models.

```bash
curl http://localhost:8000/v1/models \
  -H "Authorization: Bearer YOUR_PROXY_KEY"
```

#### `POST /v1/chat/completions`

Chat completions, fully compatible with OpenAI API format.

**Non-streaming:**

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_PROXY_KEY" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

**Streaming (SSE):**

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_PROXY_KEY" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Write a poem"}],
    "stream": true
  }'
```

**Specify platform:**

```bash
# provider/model format forces routing to a specific platform
curl ... -d '{"model": "deepseek/deepseek-chat", ...}'
```

#### `POST /v1/completions`

Legacy completions endpoint (same handler as chat/completions).

#### `POST /v1/embeddings`

Embeddings endpoint (same handler, supports embedding models).

### Authentication

| Method | Header | Description |
|--------|--------|-------------|
| Proxy API Key | `Authorization: Bearer sk-xxx` | Admin-configured proxy key |
| Consumer API Key | `Authorization: Bearer ck-xxx` | Consumer independent key |
| Guest Proxy Key | `Authorization: Bearer gk-xxx` | Temporary guest access key |
| Public Global Key | `Authorization: Bearer pk-xxx` | Public experience key (rate-limited) |

> If no Proxy API Key is set, proxy endpoints allow anonymous access (admin privilege).

### Management API

#### Auth (Public)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/setup/status` | Check if initial setup is done |
| `POST` | `/api/setup` | Initialize admin account |
| `POST` | `/api/login` | Admin login |
| `POST` | `/api/forgot-password` | Send password reset email |
| `POST` | `/api/reset-password` | Reset password via email code |
| `POST` | `/api/reset-password/verify` | Verify reset token |
| `POST` | `/api/auth/reset-with-code` | Reset password via Proxy API Key |
| `GET` | `/api/addresses` | Get bound addresses |
| `POST` | `/api/refresh` | Refresh JWT token |
| `GET` | `/api/collaborator/check-key` | Check collaborator key |
| `POST` | `/api/collaborator/register` | Register as collaborator |

#### Admin (JWT Required)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/auth/verify` | Verify auth token |
| `GET` | `/api/config` | Get configuration |
| `POST` | `/api/config` | Save configuration |
| `GET` | `/api/config/export` | Export encrypted config |
| `POST` | `/api/config/import` | Import encrypted config |
| `GET` | `/api/status` | System status |
| `GET` | `/api/admin/info` | Admin info |
| `POST` | `/api/admin/change-password` | Change admin password |
| `POST` | `/api/admin/update-email` | Update admin email |
| `POST` | `/api/admin/restart` | Restart service |
| `GET` | `/api/share/info` | Share info (sanitized) |

#### Provider Management (Admin + Consumer)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/providers` | List all providers |
| `GET` | `/api/providers/presets` | Get preset platforms |
| `POST` | `/api/providers` | Create provider |
| `GET` | `/api/providers/{id}` | Get provider details |
| `PUT` | `/api/providers/{id}` | Update provider |
| `DELETE` | `/api/providers/{id}` | Delete provider |
| `POST` | `/api/providers/{id}/test` | Test provider connectivity |
| `POST` | `/api/providers/{id}/test-all-keys` | Test all keys for provider |
| `GET` | `/api/providers/{id}/models` | Get provider model list |
| `POST` | `/api/providers/{id}/sync-url` | Sync provider base URL |
| `POST` | `/api/providers/{id}/sync-models` | Sync provider models |
| `GET` | `/api/providers/{id}/keys` | List provider API keys |
| `POST` | `/api/providers/{id}/keys` | Add API key to provider |
| `PUT` | `/api/providers/{id}/keys/{key_id}` | Update API key |
| `DELETE` | `/api/providers/{id}/keys/{key_id}` | Delete API key |
| `POST` | `/api/providers/{id}/keys/{key_id}/reset-quota` | Reset key quota |
| `GET` | `/api/providers/{id}/access-control` | Get access control |
| `PUT` | `/api/providers/{id}/access-control` | Update access control |
| `POST` | `/api/providers/sync-all-urls` | Sync all provider URLs |
| `GET` | `/api/providers/sider/status` | Sider token status |
| `POST` | `/api/providers/sider/test` | Test Sider token |

#### Platform Discovery (Admin)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/discovered-platforms` | List discovered platforms |
| `POST` | `/api/discovered-platforms/trigger` | Trigger platform discovery scan |
| `POST` | `/api/discovered-platforms/` | Update discovered platform |
| `POST` | `/api/discovered-platforms/{id}/check` | Check discovered platform |

#### Usage & Routing (Admin + Consumer)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/usage/summary` | Usage summary |
| `GET` | `/api/usage/providers` | Usage by provider |
| `GET` | `/api/usage/records` | Usage records |
| `DELETE` | `/api/usage/reset` | Reset usage data |
| `GET` | `/api/routing/mode` | Get routing mode |
| `POST` | `/api/routing/mode` | Set routing mode |
| `GET` | `/api/routing/weights` | Get routing weights |
| `POST` | `/api/routing/weights` | Set routing weights |
| `GET` | `/api/routing/advice/{model}` | Get routing advice for model |

#### SMTP (Admin)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/smtp/status` | SMTP status |
| `GET` | `/api/smtp/config` | Get SMTP config |
| `POST` | `/api/smtp/config` | Save SMTP config |
| `POST` | `/api/smtp/test` | Test SMTP |

#### Multi-User (Admin)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/invite-codes` | List invite codes |
| `POST` | `/api/invite-codes` | Create invite code |
| `DELETE` | `/api/invite-codes/{code}` | Delete invite code |
| `GET` | `/api/consumers` | List consumers |
| `POST` | `/api/consumers` | Create consumer |
| `DELETE` | `/api/consumers/{id}` | Delete consumer |
| `POST` | `/api/consumers/{id}/toggle` | Toggle consumer status |
| `PUT` | `/api/consumers/{id}` | Update consumer |
| `POST` | `/api/consumer/register` | Consumer self-registration |

#### Domain & Tunnel (Admin)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/domain/verify` | Verify domain token |
| `POST` | `/api/domain/bind` | Bind domain (Cloudflare) |
| `POST` | `/api/domain/manual-bind` | Manual domain binding |
| `GET` | `/api/domain/status` | Domain/tunnel status |
| `POST` | `/api/domain/unbind` | Unbind domain |
| `POST` | `/api/ip/bind` | Bind IP address |
| `POST` | `/api/ip/unbind` | Unbind IP address |

#### Federation & Network

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/federation/config` | Get federation config |
| `POST` | `/api/federation/config` | Save federation config |
| `GET` | `/api/network/status` | Network status |
| `GET` | `/api/network/stats` | Network statistics |
| `POST` | `/api/network/consent` | Network consent |
| `GET` | `/api/network/disclaimer` | Network disclaimer |
| `POST` | `/api/network/enable` | Enable network |
| `POST` | `/api/network/disable` | Disable network |
| `POST` | `/api/network/toggle` | Toggle network |
| `PUT` | `/api/network/config` | Update network config |
| `GET` | `/api/network/peers` | List network peers |
| `POST` | `/api/network/peers` | Add peer |
| `DELETE` | `/api/network/peers/{id}` | Remove peer |
| `GET` | `/api/network/resolve/{id}` | Resolve node address |
| `GET` | `/api/network/routes` | View routing table |
| `GET` | `/api/network/join-conditions` | Join conditions |
| `GET` | `/api/network/quota-allocation` | Quota allocation config |
| `PUT` | `/api/network/quota-allocation` | Update quota allocation |
| `GET` | `/api/network/shared-pool-breakdown` | Shared pool breakdown |

#### Guest Keys & Public Keys

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/network/guest-keys` | Issue guest key |
| `GET` | `/api/network/guest-keys` | List guest keys |
| `DELETE` | `/api/network/guest-keys/{key}` | Revoke guest key |
| `DELETE` | `/api/network/guest-keys/{key}/permanent` | Permanently delete guest key |
| `POST` | `/api/network/guest-keys/{key}/mark-collaborator` | Mark as collaborator |
| `POST` | `/api/network/guest-keys/{key}/share-type` | Set share type |
| `POST` | `/api/network/keys/validate` | Validate key |
| `PUT` | `/api/network/guest-keys/{key}/quota` | Update key quota |
| `PUT` | `/api/network/guest-keys/{key}` | Update guest key |
| `GET` | `/api/network/public-key-quota` | Public key quota status |
| `GET` | `/api/network/open-key-quota` | Open key quota |
| `GET` | `/api/network/open-key-quota/all` | All open key quotas |

#### Global Pool

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/network/global-pool` | Global pool status |
| `POST` | `/api/network/global-pool/join` | Join global pool |
| `POST` | `/api/network/global-pool/contribute` | Contribute to pool |
| `GET` | `/api/network/global-pool/nodes` | Pool nodes |
| `GET` | `/api/network/global-pool/stats` | Pool statistics |

#### Algorithm Governance ⚠️

> **⚠️ Local-only.** The `propose` / `vote` endpoints **accept requests locally and return a status**, but **on-chain / decentralized DAO voting is not implemented**. Governance is currently a local parameter store (current / history / propose / vote / validate), not a distributed consensus.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/network/algorithm/current` | Current algorithm |
| `GET` | `/api/network/algorithm/history` | Algorithm history |
| `POST` | `/api/network/algorithm/propose` | Propose algorithm change |
| `POST` | `/api/network/algorithm/vote` | Vote on proposal |
| `POST` | `/api/network/algorithm/gossip` | Algorithm gossip |
| `GET` | `/api/network/algorithm/proposals` | List proposals |
| `GET` | `/api/network/algorithm/validate` | Validate algorithm |

#### Load Balancer & Regions

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/network/loadbalancer/status` | LB status |
| `GET` | `/api/network/loadbalancer/nodes` | LB node list |
| `GET` | `/api/network/loadbalancer/metrics/{node_id}` | Node metrics |
| `PUT` | `/api/network/loadbalancer/config` | Update LB config |
| `GET` | `/api/network/heartbeat/ping` | Heartbeat ping |
| `GET` | `/api/network/regions` | Network regions |
| `GET` | `/api/network/regions/{region}/nodes` | Nodes in region |
| `PUT` | `/api/network/regions/config` | Update region config |
| `GET` | `/api/network/balance/status` | Balance status |
| `GET` | `/api/network/balance/nodes` | Balance nodes |
| `GET` | `/api/network/balance/adjustments` | Balance adjustments |
| `POST` | `/api/network/balance/recalculate` | Recalculate balance |

#### Node Identity

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/node/pubkey` | Get node public key (HTTPS required) |
| `GET` | `/api/node/info` | Get node info |

#### WAF

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/waf/status` | WAF status |
| `GET` | `/api/waf/violations` | WAF violations |
| `GET` | `/api/waf/bans` | Active bans |
| `POST` | `/api/waf/unban/{key}` | Unban entry |

#### Network Relay

| Method | Path | Description |
|--------|------|-------------|
| `GET/POST/PUT/DELETE` | `/network/{id}/` | Relay requests to target node |

#### Real-time & Monitoring

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/events` | SSE real-time event stream |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/api/metrics` | Runtime metrics (memory, goroutines, etc.) |
| `GET` | `/api/logs` | Request logs |
| `GET` | `/api/health` | Health check status |

---

## 🏗️ Architecture

### Personal Mode Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Client Request                       │
│            (OpenAI SDK / curl / any HTTP)                │
└───────────────────────┬─────────────────────────────────┘
                        │ POST /v1/chat/completions
                        ▼
┌─────────────────────────────────────────────────────────┐
│              OpenModelPool Agent Gateway                 │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐  │
│  │   Auth   │→│  5-Dim   │→│  Failover Fallback    │  │
│  │ Proxy/   │  │ Routing  │  │  Auto-try next       │  │
│  │ Consumer │  │ Trust    │  │  available Provider   │  │
│  │ Guest/PK │  │ Reputa-  │  │                       │  │
│  │          │  │ tion..   │  └──────────────────────┘  │
│  └──────────┘  └──────────┘                             │
│  ┌──────────────────────────────────────────────────┐   │
│  │              Provider Unified Pool                │   │
│  │  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐  │   │
│  │  │OpenAI│ │DeepS.│ │Gemini│ │Qwen  │ │Groq  │  │   │
│  │  └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘  │   │
│  │     │        │        │        │        │       │   │
│  │  ┌──┴──┐ ┌──┴──┐ ┌──┴──┐ ┌──┴──┐ ┌──┴──┐   │   │
│  │  │VMess│ │VMess│ │SOCKS│ │Direct│ │Direct│   │   │
│  │  └─────┘ └─────┘ └─────┘ └─────┘ └─────┘   │   │
│  └──────────────────────────────────────────────────┘   │
│                                                         │
│  ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐          │
│  │ WAF    │ │Tracker │ │Health  │ │Request │          │
│  │ 4-Layer│ │Usage + │ │Checker │ │Log     │          │
│  │Protect │ │EWMA +  │ │5min    │ │Ring    │          │
│  │        │ │Archive │ │Probe   │ │Buffer  │          │
│  └────────┘ └────────┘ └────────┘ └────────┘          │
└─────────────────────────────────────────────────────────┘
```

### Network Mode Architecture (Opt-In)

```
┌──────────────┐         ┌───────────────────────────┐         ┌──────────────┐
│  Your Node   │         │    P2P Sharing Network     │        │  Other Node  │
│              │         │                            │         │              │
│ ┌──────────┐ │  Share  │  ┌──────────────────────┐  │  Share  │ ┌──────────┐ │
│ │ Gemini   │─┼────────→│  │  Contribution Credit │  │←────────┼─│ GLM      │ │
│ │ Surplus  │ │         │  │  (Non-transferable,  │  │         │ │ Surplus  │ │
│ │ Need GLM │←┼────────│  │   Bound to Node ID)   │  │────────→┼─│ Need Gm  │ │
│ └──────────┘ │  Call   │  └──────────────────────┘  │  Call   │ └──────────┘ │
│              │         │                            │         │              │
│ ┌──────────┐ │         │  ┌────────┐ ┌──────────┐  │         │ ┌──────────┐ │
│ │ Identity │ │         │  │Kademlia│ │  Gossip  │  │         │ │Reputation│ │
│ │ BIP39 →  │ │←───────→│  │  DHT   │ │ Protocol │  │←───────→│ │ S/A/B/C/D│ │
│ │ Ed25519  │ │         │  └────────┘ └──────────┘  │         │ └──────────┘ │
│ └──────────┘ │         │                            │         │              │
│              │         │  ┌────────┐ ┌──────────┐  │         │              │
│ ┌──────────┐ │         │  │  WAF   │ │   Load   │  │         │              │
│ │  Seed    │ │         │  │ 4-Layer│ │ Balancer │  │         │              │
│ │  :8001   │ │         │  │  Guard │ │ 5-Dim    │  │         │              │
│ └──────────┘ │         │  └────────┘ └──────────┘  │         │              │
└──────────────┘         └───────────────────────────┘         └──────────────┘
```

### Tech Stack

| Component | Technology | Description |
|-----------|-----------|-------------|
| HTTP Server | Go stdlib `net/http` | No third-party web framework, Go 1.22+ route patterns |
| Auth | `golang-jwt/jwt/v5` | JWT token issuance and verification |
| Password | `golang.org/x/crypto/bcrypt` | Password hashing |
| Encryption | Go stdlib `crypto/aes` + `crypto/cipher` | AES-256-GCM encryption |
| Identity | `go-bip39` + `crypto/ed25519` | BIP39 mnemonic → Ed25519 key derivation |
| DHT | Custom 256-bit Kademlia | SHA-256 hash space, k=20 buckets |
| Proxy | `golang.org/x/net/proxy` | SOCKS5 outbound proxy |
| VMess | Xray (external binary) | VMess local proxy |
| Tunnel | cloudflared | Cloudflare Tunnel (quick/named/manual) |
| Concurrency | Go goroutine | Concurrent health checks, request forwarding |
| Streaming | SSE + `io.Writer` | Zero-copy streaming forwarding |
| Deployment | Single binary / Docker | Zero dependency, cross-platform |

### Project Structure

```
openmodelpool/
├── main.go                          # Entry point, HTTP route registration, middleware
├── init.go                          # Core component initialization
├── types.go                         # Data models (OpenAI compatible format)
├── config.go                        # Configuration management (JSON + env + encryption)
│
├──── Provider Layer ──────────────────────────────────────────────
├── provider.go                      # Provider CRUD + smart routing engine
├── providers.go                     # 36 preset platform definitions
├── client.go                        # Upstream request forwarding (OpenAI / Sider / Coze)
├── sider.go                         # Sider web version adapter + Token status monitoring
├── pricing.go                       # Platform × model dual-dimension pricing table
├── health.go                        # Provider health check (concurrent probing)
├── platform_discovery.go            # Auto-discover free AI platforms
├── model_sync_scheduler.go          # Scheduled model list synchronization
│
├──── Auth & User Layer ───────────────────────────────────────────
├── auth.go                          # JWT auth + bcrypt + SMTP + password recovery
├── admin.go                         # Admin panel API
├── multiuser.go                     # Multi-user: invite codes + consumers + API key mgmt
├── middleware.go                     # HTTP middleware (CORS, auth, rate limiting)
├── handlers.go                      # Shared HTTP handlers
├── encryptor.go                     # AES-256-GCM encryptor
├── cmd_resetpwd.go                  # CLI password reset command
│
├──── Tracking & Monitoring ───────────────────────────────────────
├── tracker.go                       # Usage tracking + EWMA + batch write + archiving + budget alerts
├── events.go                        # SSE real-time event push
├── metrics.go                       # Prometheus metrics endpoint
├── logger.go                        # Structured logging system
├── performance.go                   # Performance optimization (memory monitor, sync.Pool, worker pool)
│
├──── Security Layer ──────────────────────────────────────────────
├── waf.go                           # WAF 4-layer protection framework
├── ratelimit.go                     # Token bucket rate limiter (global + per-consumer)
├── token_estimator.go               # Token precise estimation (upstream + local fallback)
├── data_integrity.go                # HMAC-SHA256 data file integrity verification
│
├──── Network Layer (P2P / Federation) ────────────────────────────
├── network.go                       # Network mode & data models (Personal/Shared)
├── network_loadbalancer.go          # 5-dimension health-aware load balancer
├── network_relay.go                 # Multi-hop relay routing
├── network_seed.go                  # Seed node discovery service (:8001)
├── network_discovery.go             # Network peer discovery
├── network_keys.go                  # Guest Key / Public Global Key management
├── network_quota.go                 # Quota allocation manager (Guest/Public pool)
├── network_region.go                # Network region management
├── network_balance.go               # Load balance tracking & adjustments
├── network_global_pool.go           # Global resource pool
├── network_algorithm.go             # Algorithm governance (propose/vote/gossip)
├── node.go                          # Node identity (BIP39 mnemonic → Ed25519 → Node ID)
├── dht.go                           # Kademlia DHT (256-bit hash space, k-buckets)
├── gossip.go                        # Gossip protocol state synchronization
├── discovery.go                     # Trust pool registry fetching
├── reputation.go                    # Reputation manager (EWMA, S/A/B/C/D grades)
├── credits.go                       # Contribution Credit allocation manager
│
├──── Infrastructure ──────────────────────────────────────────────
├── vmess.go                         # VMess proxy management (parse + Xray start/stop)
├── tunnel.go                        # Cloudflare Tunnel management (quick/named/manual)
├── server.go                        # HTTP server setup, route registration, graceful shutdown
│
├──── Frontend ────────────────────────────────────────────────────
├── admin.html                       # Web admin panel (dark theme SPA)
├── login.html                       # Login page
├── setup.html                       # Initial setup wizard
├── forgot_password.html             # Forgot password page
│
├──── Build & Deploy ──────────────────────────────────────────────
├── go.mod / go.sum                  # Go module dependencies
├── Makefile                         # Build shortcuts
├── Dockerfile                       # Multi-stage Docker build
├── install.sh                       # One-click install script
├── deploy.sh                        # Deployment script
├── build-all.sh                     # Cross-platform build script
├── restart.sh                       # Restart script
├── setup-named-tunnel.sh            # Named tunnel setup helper
│
├──── Tests ───────────────────────────────────────────────────────
├── client_test.go                   # Client tests
├── consumer_security_test.go        # Consumer security tests
├── dht_test.go                      # DHT tests
├── encryptor_test.go                # Encryptor tests
├── federation_test.go               # Federation tests
├── health_test.go                   # Health check tests
├── http_pool_bench_test.go          # HTTP pool benchmarks
├── multiuser_test.go                # Multi-user tests
├── network_keys_security_test.go    # Network keys security tests
├── network_keys_test.go             # Network keys tests
├── network_region_test.go           # Network region tests
├── network_relay_test.go            # Network relay tests
├── network_seed_test.go             # Network seed tests
├── network_test.go                  # Network tests
├── node_test.go                     # Node identity tests
├── pricing_test.go                  # Pricing tests
├── provider_test.go                 # Provider tests
├── public_key_quota_test.go         # Public key quota tests
├── quota_enforcement_test.go        # Quota enforcement tests
├── ratelimit_test.go                # Rate limit tests
├── security_medium_test.go          # Medium security tests
├── security_p0_test.go              # P0 security tests
├── test_helpers_test.go             # Test helpers
├── token_estimator_test.go          # Token estimator tests
├── tracker_test.go                  # Tracker tests
└── waf_test.go                      # WAF tests
```

---

## 📦 Preset Platforms (36)

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

### 🟠 Anthropic Claude

**API Type:** Messages API (`/v1/messages`)
**API Key Format:** `sk-ant-xxxxx` (x-api-key header auth)

**How to get:**
1. Login to [Anthropic Console](https://console.anthropic.com/)
2. **API Keys** → **Create Key** → Copy

**Auto-adaptation:** System messages extracted independently, proprietary auth headers, SSE event auto-conversion

---

## 🔨 Build & Deployment

### Cross-Compilation

| Platform | Arch | Binary | Target Devices |
|----------|------|--------|----------------|
| Linux | amd64 | `openmodelpool-linux-amd64` | x86_64 servers, VPS |
| Linux | arm64 | `openmodelpool-linux-arm64` | Raspberry Pi 4, ARM servers |
| Linux | armv7 | `openmodelpool-linux-armv7` | Raspberry Pi 3B, OpenWRT |
| macOS | amd64 | `openmodelpool-darwin-amd64` | Intel Mac |
| macOS | arm64 | `openmodelpool-darwin-arm64` | Apple Silicon Mac |
| Windows | amd64 | `openmodelpool-windows-amd64.exe` | x64 Windows |

### Makefile Commands

| Command | Description |
|---------|-------------|
| `make build` | Build for current platform |
| `make build-all` | Build for all 6 platforms |
| `make build-linux` | Build Linux only (3 architectures) |
| `make build-darwin` | Build macOS only (2 architectures) |
| `make build-windows` | Build Windows only |
| `make clean` | Clean build artifacts |
| `make test` | Run tests + coverage |
| `make docker` | Build Docker image |
| `make docker-compose` | Docker Compose start |
| `make release` | Full release workflow |

### Build Optimization

All builds use:

```bash
go build -ldflags="-s -w" -trimpath
```

- `-s -w`: Strip debug info and symbol tables, reduce binary size
- `-trimpath`: Strip local path info, improve portability and security

### Install Script Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `--port` | Service port | `8000` |
| `--dir` | Install directory | `/usr/local/bin` |
| `--data` | Data directory | `/var/lib/openmodelpool` |
| `--version` | Specify version | `latest` |

---

## ⚙️ Configuration

### Data Storage

All data stored in `data/` directory, JSON format:

| File | Content |
|------|---------|
| `data/config.json` | Global config (routing mode, weights, Proxy API Key, etc.) |
| `data/providers.json` | Provider config (API Keys encrypted) |
| `data/admin.json` | Admin account, JWT Secret, SMTP config |
| `data/usage.json` | Usage records |
| `data/consumers.json` | Multi-user data (invite codes, consumers) |
| `data/.key` | AES-256 encryption key (auto-generated) |
| `data/sider_token_status.json` | Sider Token status |
| `data/guest_keys.json` | Guest Key store |
| `data/discovered_platforms.json` | Auto-discovered platforms |

### Sensitive Data Encryption

All sensitive fields encrypted with **AES-256-GCM**:

- Provider API Keys
- Proxy API Keys
- Guest Proxy Keys
- SMTP passwords
- VMess proxy links

Key file `data/.key` is auto-generated on first startup (32-byte random key, Base64 encoded).

> ⚠️ **Keep `data/.key` safe** — lost means unable to decrypt stored sensitive data.

### Config Export / Import

```bash
# Export (via admin panel API)
curl http://localhost:8000/api/config/export \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -o backup.json

# Import
curl http://localhost:8000/api/config/import \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -F "file=@backup.json"
```

### Routing Mode Configuration

```bash
# Set routing mode
curl -X POST http://localhost:8000/api/routing/mode \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "auto"}'

# Custom 5-dimension weights
curl -X POST http://localhost:8000/api/routing/weights \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"trust": 0.25, "reputation": 0.25, "latency": 0.20, "availability": 0.15, "contribution": 0.15}'
```

---

## 📜 License

MIT

---

## 🙏 Acknowledgments

OpenModelPool Agent is built upon these excellent open-source projects:

- [**Go**](https://go.dev/) — Clean and efficient programming language
- [**golang-jwt/jwt**](https://github.com/golang-jwt/jwt) — Reliable JWT implementation
- [**golang.org/x/crypto**](https://pkg.go.dev/golang.org/x/crypto) — Secure bcrypt password hashing
- [**golang.org/x/net**](https://pkg.go.dev/golang.org/x/net) — SOCKS5 proxy support
- [**go-bip39**](https://github.com/tyler-smith/go-bip39) — BIP39 mnemonic implementation

Inspired by these open-source API management projects:

- [**one-api**](https://github.com/songquanpeng/one-api) — OpenAI management tool
- [**new-api**](https://github.com/Calcium-Ion/new-api) — Enhanced one-api with multi-user support

**Spiritual predecessors** — these projects proved the power of decentralized sharing:

- [**BitTorrent**](https://www.bittorrent.com/) — P2P file sharing pioneer
- [**IPFS**](https://ipfs.tech/) — Content-addressed, decentralized storage
- [**Tor**](https://www.torproject.org/) — Onion routing, communication freedom

---

## 📋 Changelog

### v4.0.1 (2026-07)

**🔴 Architecture Upgrade**
- **Dual-Mode Architecture** — Personal Mode (default, pure local) + Network Mode (opt-in P2P sharing)
- **BIP39 Mnemonic Identity** — Mnemonic → Ed25519 key pair → Node ID (`mmx-` prefix), replacing legacy key system
- **5-Dimension Routing** — Upgraded from 4-dimension to 5-dimension: Trust 25% + Reputation 25% + Latency 20% + Availability 15% + Contribution 15%
- **Two-Level Switch** — `network_enabled` (join network) + `share_to_pool` (share quota) fully independent

**🟠 Network System (New)**
- **P2P Node Discovery** — Triple-layer: Peer Seed (:8001) + Kademlia DHT (256-bit) + Gossip protocol
- **Reputation System** — EWMA-tracked scoring, S/A/B/C/D five-grade system
- **Contribution Credit** — Non-withdrawable, non-transferable credits bound to Node ID
- **Guest Key / Public Global Key** — `gk-` and `pk-` key types for guest and public access
- **WAF 4-Layer Protection** — Rate limit → Token limit → Content safety → Behavioral analysis
- **Token Precise Estimation** — Dual-strategy: upstream extraction (preferred) + local estimation (fallback)
- **Auto Platform Discovery** — Automatic scanning and discovery of free AI platforms
- **Health-Aware Load Balancer** — Real-time per-node metrics with sliding-window history
- **Data Integrity Verification** — HMAC-SHA256 signatures on critical data files
- **Global Resource Pool** — Cross-node resource aggregation and contribution
- **Algorithm Governance** — Decentralized algorithm proposal, voting, and gossip

**🟢 Platform Updates**
- Platform count increased from 34 → **36** (added Agnes AI, AIHubMix)
- TokenHub Enterprise expanded with GLM-5.2, MiniMax M3, Kimi K2.6 models
- Multi-key management per Provider with individual quota control

**🔵 Seed Endpoints**
- `GET /api/peers` — Peer discovery (no auth)
- `POST /api/register` — Node self-registration (no auth)
- `GET /api/seed/health` — Seed health check (no auth)

### v3.3.0 (2026-07)

**🔴 Critical Security Fixes**
- **API Key masking** — `/api/share/info` and `/api/config/export` no longer expose Proxy API Key in plaintext
- **Consumer Key encryption** — Consumer API Keys encrypted with AES-256-GCM at rest

**🟠 Security Hardening**
- **CORS tightening** — Removed wildcard `*`, default allows only localhost + tunnel URL
- **File permissions** — All data files from 0644 to 0600
- **Error masking** — Proxy error messages no longer leak internal IP addresses
- **JWT security** — admin.html removed localStorage token, switched to HttpOnly Cookie
- **Cookie enhancement** — Secure + SameSite=Lax flags
- **Endpoint auth** — `/metrics` and `/events` endpoints now require authentication (401)
- **Federation auth** — Federation endpoints restricted to known nodes/admin

**🟢 Other Improvements**
- **Password strength** — Minimum length from 6 to 8
- **Reset Token** — Reuse unexpired tokens, prevent concurrent race
- **Anonymous fallback** — Disabled when consumers are registered
- **Consumer permissions** — handleTestProvider adds Consumer permission check

**⚡ Performance**
- **Config write debounce** — 3-second aggregate writes, reduce disk I/O
- **HTTP connection pool** — Global MaxIdleConns=100, TCP connection reuse
- **Async writes** — Config.save() made async, non-blocking
- **Tracker optimization** — Record() releases lock before flushing to disk

### v3.2.0 (2026-07)

**🔴 Security & Performance**
- **Rate Limiting** — Token bucket algorithm, global QPS + per-Consumer independent limits, 429 on excess
- **CORS whitelist** — Exact match + `*.example.com` wildcard subdomain support
- **Sensitive field encryption unified** — `coze_api_token` added to AES-256-GCM scope
- **JSON parse error handling** — All API endpoints return 400 + clear error messages on parse failure

**🟡 Feature Enhancements**
- **Provider model list auto-sync** — `SyncModels()` + `/api/providers/{id}/sync-models` endpoint + panel sync button
- **Federation Phase 3 Gossip-DHT hybrid discovery** — DHT hash ring routing table
- **Structured logging** — `log_level` config, request log middleware, output to `data/access.log` + stdout
- **SSE real-time push** — `/events` endpoint, Provider status changes, health updates, config changes
- **Prometheus metrics** — `/metrics` endpoint, request counts, latency, error rates, Token usage
- **Frontend modularization** — admin.html JS split into 10+ module comment areas
- **Config hot reload** — `SIGHUP` signal triggers config reload without process restart
