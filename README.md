# OpenModelPool Agent

**A non-profit, OpenAI-compatible gateway for pooled AI access** — a local model proxy first, and optionally a node in a community-run capability sharing network.

> Network has no borders; AI capabilities shouldn't either.

**No business model. No token. No points economy. No skim.** There is nothing to pay for: a single Go binary you run yourself, on your own machine, against your own provider keys. Read the pledge — [中文](docs/PUBLIC-WELFARE.md) · [English](docs/PUBLIC-WELFARE.en.md).

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-v4.3.30-blue)](#)
[![Non-profit](https://img.shields.io/badge/non--profit-no%20token%20%C2%B7%20no%20skim-2f855a)](docs/PUBLIC-WELFARE.en.md)

---

## 🤖 What Is It?

**OpenModelPool Agent is a temporary Token bank + a geek sharing network.**

By default, it is a **pure-local personal model proxy** — managing your API tokens, providing a unified OpenAI-compatible interface, and tracking usage. No network, no sharing, no network identity generated.

Only when you configure Provider Tokens, enable quota management, and the system detects idle quota this month, will it gently prompt you: **Would you like to share some idle quota to the network?**

> Your GPT-4o quota is only 60% used this month? The remaining 40% expires and goes to waste.
> Share the idle part with the network, and the ledger records what you gave.
> That record entitles you to the same amount back from the pool later — 1:1, no fee, no interest, no expiry games.
> If you never claim it — the contribution simply stays a gift to the community.

**This is bookkeeping, not currency.** The entitlement cannot be bought, sold, transferred, or withdrawn, and the project takes no cut of anything. Non-contributors are never locked out — the community free pool stays open to everyone by default.

**Three principles**: Configuring Token ≠ Joining the sharing network · Having idle quota ≠ Auto-sharing · Joining the sharing network ≠ Sharing all quota.

To upstream providers, this is exactly the same as anyone calling the API directly — same Key, same quota, same provider. **No "reselling", no "middleman", just an Agent forwarding your requests.**

---

## 🌍 Our Belief

The internet's greatest creation was breaking the boundaries of information. BitTorrent let knowledge escape server monopolies; IPFS let storage escape single-node dependency; Tor let communication escape geographic constraints. **OpenModelPool Agent does the same thing — but what's shared is not files, but AI capabilities.**

We believe a developer in New York with a Claude API and a programmer in Beijing have equally valuable access. When global AI capabilities converge through a decentralized network, anyone can equally access the most powerful intelligence — regardless of where they are.

This is not a commercial product. This is the continuation of internet spirit: **sharing, openness, no borders.**

**The pledge, in four lines:**
1. **No money changes hands.** No subscription, no credits for sale, no token, no ad, no telemetry sold.
2. **Good faith is the default.** The system defends against malicious abuse, never against "this person did not contribute". No freeloader penalty, no trust score.
3. **Nothing is claimed that isn't shipped.** Every unimplemented item here is marked ⚠️ on purpose — see Implementation Status.
4. **Governance belongs to contributors.** Node admission and model allow-lists go through an append-only, hash-chained proposal ledger with a 2/3 supermajority of contributing nodes.

> 📖 Full philosophy, the public-global-key design, and the Sybil-defense rationale: [docs/PUBLIC-WELFARE.md](docs/PUBLIC-WELFARE.md).

---

## 📋 Implementation Status（诚实状态）

> This section honestly tracks what is actually wired up versus what remains planned. It is kept in sync with the **code**, not the vision. Items marked ⚠️ are partially designed or stubbed and are **not yet functional end-to-end**.

### ✅ Implemented & Usable

| Area | State |
|------|-------|
| OpenAI-compatible unified gateway (`/v1/chat/completions`, `/v1/models`, `/v1/embeddings`, `/v1/completions`, `/v1/messages`) | ✅ Real, working |
| Personal-mode **4-dimension** routing weights (priority / cost / latency / tokens), editable via admin sliders | ✅ Real |
| Automatic failover, multi-user, token budget, provider health check | ✅ Real |
| **Real AES-256-GCM encryption** (`encryptor.go`; prefix `omp:e:`) for API keys / config fields | ✅ Real |
| Request logging, usage archiving, SMTP email, Web admin panel | ✅ Real |
| Network mode — reputation (EWMA, S/A/B/C/D), contribution credits, key system, quota allocation, health-aware load balancing, Global Pool, algorithm governance chain params, region (minimal stub) | ✅ Implemented (some as minimal/partial stubs — see ⚠️ below) |
| Federated trust pool (registry-based `trust_pool.json`) | ✅ Real |

### ⚠️ Planned / Not Yet Wired

| Area | Current State |
|------|---------------|
| BIP39 mnemonic node identity | ⚠️ `handleNodePubKey` returns empty pubkey; UI not yet exposed |
| DHT | ⚠️ Former empty shell removed; `GetDHTStats` returns `{"enabled":false}`. P2P discovery relies on registry/gossip, not DHT |
| Contribution ledger | ⚠️ Local content-hash store (`sha256:` prefix) — verifiable but **no IPFS / distributed persistence**; credits stored locally only |
| Algorithm governance DAO voting | ⚠️ `propose`/`vote` accept locally and return status; on-chain / decentralized voting **not implemented** |
| Regional routing | ⚠️ Compiles (minimal stub), but real geo-based routing is not wired (`handleNetworkRegions` returns empty) |

> **Not a gap — by design: "5-dimension routing" exposes only 4 sliders.**
> Network-mode scoring genuinely is a 5-dimension weighted model (trust / reputation / latency / availability / contribution — see `ScoreNode` and `LBConfig` in `network_loadbalancer.go`). The 5th "dimension" is the **routing algorithm itself** — weighted composition, regional adjustment and `SelectNode` selection logic — which is fixed backend behaviour and deliberately not user-tunable. The 4 admin sliders (priority / cost / latency / quota) already cover **every** adjustable weight. This is intentional, not unfinished work; a 5th slider will not be added.

---

## ✨ Core Features

Full detail, examples and endpoint tables: **[docs/FEATURES.md](docs/FEATURES.md)**.

**Personal Mode (default — pure-local proxy):**
- 🔌 **Unified API Gateway** — OpenAI-compatible `/v1/chat/completions` + `/v1/messages` (Anthropic), streaming SSE, 37 preset platforms, `provider/model` routing, auto platform discovery, 🎁 Free Model Pool (16+ free providers auto-synced; **Kilo Code + OVHcloud AI Endpoints are seeded at startup**, so a fresh deploy works out of the box even before remote sync), `web_session` type for browser-login platforms
- 🧠 **4-Dimension Intelligent Routing** — priority / cheapest / fastest / composite (weighted fusion, all slider-tunable)
- 🔗 **Automatic Failover** · 👥 **Multi-User** (invite codes, visibility isolation, per-consumer keys) · 💰 **Token Budget** (dual-dimension pricing, monthly limits, threshold alerts)
- 🩺 **Provider Auto Health Check** (5-min probe) · 🛡️ **WAF 4-Layer Protection** (real, default-on) · 🔐 **AES-256-GCM + bcrypt + JWT** · 📝 Request Logging · 📊 Usage Archiving · 📧 SMTP · 🌐 VMess Proxy · 🖥️ Web Admin Panel

**Network Mode (opt-in — P2P capability sharing):**
- 🔑 Identity (BIP39 ⚠️) · 🌍 P2P Discovery (triple-layer ⚠️) · 🔗 Federation config · 🏆 Reputation (S/A/B/C/D) · 💎 Contribution Credits (⚠️ local-only) · 🔑 Key System · 🔄 Quota Allocation · ⚖️ Health-Aware Load Balancer · 🌐 Public Access (Cloudflare Tunnel) · 📡 Network API

> **⚠️ Network Mode is disabled by default.** Personal Mode does all local proxying without any network activity.

---

## 🚀 Quick Start

**One-Click Install (Linux / macOS):**

```bash
curl -fsSL "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.sh?t=$(date +%s)" | sudo bash
```

**Windows (PowerShell as Admin):**

```powershell
irm "https://raw.githubusercontent.com/lisiyu/openmodelpool/main/scripts/omp-manager.ps1?t=$(Get-Date -Format 'yyyyMMddHHmmss')" | iex
```

Both manager scripts provide an interactive menu: install / upgrade / uninstall / tunnel (Cloudflare / FRP / ngrok) / port change / status / restart.

> 📖 Build, cross-compilation, deployment, configuration, and the auto-update mode: **[docs/DEPLOYMENT_GUIDE.md](docs/DEPLOYMENT_GUIDE.md)** · **[docs/CONFIGURATION.md](docs/CONFIGURATION.md)**.

---

## 📚 Documentation

| Topic | Doc |
|-------|-----|
| Features (full catalog) | [docs/FEATURES.md](docs/FEATURES.md) |
| API reference (proxy + management) | [docs/API.md](docs/API.md) |
| Preset platforms & non-OpenAI config | [docs/PLATFORMS.md](docs/PLATFORMS.md) |
| Deployment & build | [docs/DEPLOYMENT_GUIDE.md](docs/DEPLOYMENT_GUIDE.md) |
| Configuration & encryption | [docs/CONFIGURATION.md](docs/CONFIGURATION.md) |
| Federation / network mode | [docs/FEDERATION.md](docs/FEDERATION.md) |
| Access control & security | [docs/ACCESS_CONTROL.md](docs/ACCESS_CONTROL.md) |
| Performance optimization | [docs/PERFORMANCE_OPTIMIZATION.md](docs/PERFORMANCE_OPTIMIZATION.md) |
| Philosophy & public welfare | [docs/PUBLIC-WELFARE.md](docs/PUBLIC-WELFARE.md) |
| Public roadmap (BACKLOG) | [docs/BACKLOG.md](docs/BACKLOG.md) |
| Changelog | [docs/CHANGELOG.md](docs/CHANGELOG.md) |
| **Internal design specs & historical working docs** | [docs/reference/](docs/reference/) |

---

## 🤝 Contributing

**This project belongs to everyone who uses it.** Features, design input and criticism are all genuinely welcome — you do not need permission to start. [CONTRIBUTING.md](CONTRIBUTING.md) has the full guide.

| I want to… | Do this |
|------------|---------|
| Propose a feature or challenge a design decision | Open a [feature / design proposal](../../issues/new?template=feature_request.yml) |
| Pick up existing work | The roadmap is public in [docs/BACKLOG.md](docs/BACKLOG.md). Comment to claim, then send a PR |
| Report a bug | Open a [bug report](../../issues/new?template=bug_report.yml) |
| Report a security issue | **Not via issues** — see [SECURITY.md](SECURITY.md) |
| Understand the codebase first | Start with [docs/reference/openmodelpool-v4-design.md](docs/reference/openmodelpool-v4-design.md), then [docs/reference/REVIEW-2026-08-08.md](docs/reference/REVIEW-2026-08-08.md) |

**Before a PR:** `go build ./...` · `go test ./...` · `go vet ./...` must pass. The suite runs offline in about two minutes — no API keys or network needed.

**Keep the dependency footprint small.** No web framework, no ORM, no DI container — Go stdlib for everything structural. The five direct dependencies (`golang-jwt`, `x/crypto`, `x/net`, `go-bip39`, `chromedp`) each exist because stdlib has no equivalent.

**One thing we will not merge:** anything that introduces a token, points economy, paid tier, or revenue share. Non-profit by construction, not by circumstance.

---

## 📜 License

MIT — see [LICENSE](LICENSE).

---

## 🙏 Acknowledgments

Built upon: **Go** · **golang-jwt/jwt** · **golang.org/x/crypto** · **golang.org/x/net** · **go-bip39** · Free LLM directory [**awesome-free-llm-apis**](https://github.com/mnfst/awesome-free-llm-apis) · inspired by [**one-api**](https://github.com/songquanpeng/one-api) / [**new-api**](https://github.com/Calcium-Ion/new-api) · spiritual predecessors [**BitTorrent**](https://www.bittorrent.com/) / [**IPFS**](https://ipfs.tech/) / [**Tor**](https://www.torproject.org/).
