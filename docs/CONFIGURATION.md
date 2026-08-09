# Configuration

> Runtime configuration, data storage and encryption. For build/deploy, see [DEPLOYMENT_GUIDE.md](DEPLOYMENT_GUIDE.md). Homepage summary: [README](../README.md).

All data is stored in the `data/` directory as JSON. `data/` is git-ignored and never enters version control.

---

## Data Storage

| File | Content |
|------|---------|
| `data/config.json` | Global config (routing mode, weights, Proxy API Key, etc.) |
| `data/providers.json` | Provider config (API Keys encrypted) |
| `data/admin.json` | Admin account, JWT Secret, SMTP config, invite codes, consumers |
| `data/usage.json` | Usage records |
| `data/network.json` | Network mode config (peers, federation, trust pool) |
| `data/global_pool.json` | Global resource pool data |
| `data/node.key` | Node identity key (Ed25519, generated on network join) |
| `data/.enc_key` | AES-256-GCM encryption key (auto-generated, 32 bytes) |
| `data/sider_token_status.json` | Sider Token status |
| `data/guest_keys.json` | Guest Key store |
| `data/discovered_platforms.json` | Auto-discovered platforms |
| `data/access.log` | Request access log |

---

## Sensitive Data Encryption

All sensitive fields encrypted with **AES-256-GCM**:

- Provider API Keys
- Proxy API Keys
- Guest Proxy Keys
- SMTP passwords
- VMess proxy links

Key file `data/.enc_key` is auto-generated on first startup (32-byte random key). All encrypted fields use `omp:e:` prefix.

> ⚠️ **Keep `data/.enc_key` safe** — lost means unable to decrypt stored sensitive data.

---

## Config Export / Import

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

---

## Routing Mode Configuration

```bash
# Set routing mode
curl -X POST http://localhost:8000/api/routing/mode \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "auto"}'

# Custom 4-dimension weights (Personal Mode: priority / cost / latency / tokens)
curl -X POST http://localhost:8000/api/routing/weights \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"priority": 0.30, "cost": 0.25, "latency": 0.25, "tokens": 0.20}'
```

> The 4 weights above cover **every** adjustable routing dimension. Network mode's 5th "dimension" is the routing algorithm itself and is not user-tunable — see the "Not a gap — by design" note in [README Implementation Status](../README.md#-implementation-status诚实状态).
