# API Reference

> Complete API reference for OpenModelPool Agent. The homepage ([README](../README.md)) keeps only a summary; this file is the full endpoint catalog.

Two auth surfaces, strictly separated:
- **Admin** — username + password (`admin_auth.go` → bcrypt + JWT). `/api/setup`, `/api/login` and all `/api/*` management routes go through `withAuth`.
- **Caller (end user / app)** — base URL + API Key only, in OpenAI-compatible format. Proxy routes (`/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`, `/v1/messages`) go through `withProxyAuth`, authenticated by `Authorization: Bearer <key>`.

The community free pool key is the hardcoded public key `sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1` — any node address + this key is a zero-config closed loop.

---

## Proxy Interface (OpenAI Compatible)

### `GET /v1/models`

List all available models.

```bash
curl http://localhost:8000/v1/models \
  -H "Authorization: Bearer YOUR_PROXY_KEY"
```

### `POST /v1/chat/completions`

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

### `POST /v1/messages`

Anthropic Messages API compatibility layer — accepts Anthropic-format requests (`x-api-key` header, `anthropic-version` header), auto-converts to OpenAI format internally, routes through the same provider pool.

```bash
curl http://localhost:8000/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: YOUR_PROXY_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-sonnet-4-20250514",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### `POST /v1/completions`

Legacy completions endpoint (same handler as chat/completions).

### `POST /v1/embeddings`

Embeddings endpoint (same handler, supports embedding models).

---

## Authentication

| Method | Header | Description |
|--------|--------|-------------|
| Proxy API Key | `Authorization: Bearer sk-xxx` | Admin-configured proxy key |
| Consumer API Key | `Authorization: Bearer ck-xxx` | Consumer independent key |
| Guest Proxy Key | `Authorization: Bearer gk-xxx` | Temporary guest access key |
| Public Global Key | `Authorization: Bearer pk-xxx` | Public experience key (rate-limited) |

> If no Proxy API Key is set, proxy endpoints allow anonymous access (admin privilege).

---

## Management API

### Auth (Public)

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

### Admin (JWT Required)

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

### Provider Management (Admin + Consumer)

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

### Platform Discovery (Admin)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/discovered-platforms` | List discovered platforms |
| `POST` | `/api/discovered-platforms/trigger` | Trigger platform discovery scan |
| `POST` | `/api/discovered-platforms/` | Update discovered platform |
| `POST` | `/api/discovered-platforms/{id}/check` | Check discovered platform |

### Usage & Routing (Admin + Consumer)

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

### SMTP (Admin)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/smtp/status` | SMTP status |
| `GET` | `/api/smtp/config` | Get SMTP config |
| `POST` | `/api/smtp/config` | Save SMTP config |
| `POST` | `/api/smtp/test` | Test SMTP |

### Multi-User (Admin)

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

### Domain & Tunnel (Admin)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/domain/verify` | Verify domain token |
| `POST` | `/api/domain/bind` | Bind domain (Cloudflare) |
| `POST` | `/api/domain/manual-bind` | Manual domain binding |
| `GET` | `/api/domain/status` | Domain/tunnel status |
| `POST` | `/api/domain/unbind` | Unbind domain |
| `POST` | `/api/ip/bind` | Bind IP address |
| `POST` | `/api/ip/unbind` | Unbind IP address |

### Federation & Network

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

### Guest Keys & Public Keys

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

### Global Pool

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/network/global-pool` | Global pool status |
| `POST` | `/api/network/global-pool/join` | Join global pool |
| `POST` | `/api/network/global-pool/contribute` | Contribute to pool |
| `GET` | `/api/network/global-pool/nodes` | Pool nodes |
| `GET` | `/api/network/global-pool/stats` | Pool statistics |

### Algorithm Governance ⚠️

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

### Load Balancer & Regions

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

### Node Identity

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/node/pubkey` | Get node public key (HTTPS required) |
| `GET` | `/api/node/info` | Get node info |

### WAF

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/waf/status` | WAF status |
| `GET` | `/api/waf/violations` | WAF violations |
| `GET` | `/api/waf/bans` | Active bans |
| `POST` | `/api/waf/unban/{key}` | Unban entry |

### Network Relay

| Method | Path | Description |
|--------|------|-------------|
| `GET/POST/PUT/DELETE` | `/network/{id}/` | Relay requests to target node |

### Real-time & Monitoring

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/events` | SSE real-time event stream |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/api/metrics` | Runtime metrics (memory, goroutines, etc.) |
| `GET` | `/api/logs` | Request logs |
| `GET` | `/api/health` | Health check status |
