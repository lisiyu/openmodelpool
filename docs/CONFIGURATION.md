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

## Audit & Privacy (审计与隐私)

管理操作审计日志默认开启（写入 `data/audit/audit.log`，自动轮转，可经 `audit_webhook_url` 远程转发）。私有部署若要求本地零留存，可关闭整条审计链路：

| Key | Default | Description |
|-----|---------|-------------|
| `audit_enabled` | `true` | 审计总开关。设 `false` 进入**零日志模式**：不创建 `data/audit/`、不写任何审计文件、不转发 webhook |
| `audit_webhook_url` | empty | 可选远程审计转发端点；`audit_enabled=false` 时一并静默 |

> 零日志模式下 `auditRecord` 恒为 no-op（无文件、无网络、无进程副作用）；`GET /api/admin/audit-log` 返回 `{"entries":[],"enabled":false}` 而非错误。默认值保持既有行为不变。

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

## Region Routing (区域路由)

共享网络模式下的区域感知路由：节点按地理区域分组，负载均衡优先选择同区域节点。区域检测分两档：

1. **GeoIP 精准检测**（默认开启）：经 HTTPS 查询 IP 归属国家并映射到 `ap`（亚太）/`eu`（欧洲）/`americas`（美洲），结果按 IP 缓存 24 小时。本节点在启动时检测一次；探测结果为 unknown 的对端节点在后台异步补齐（单 IP 单 flight，不阻塞心跳热路径）。
2. **离线启发式**（兜底）：首字节 IP 段近似分类；关闭 GeoIP 或查询失败时自动回退。

节点主动上报的区域（`self_report`/`heartbeat`）永远优先于启发式结果，不会被覆盖。

| Key | Default | Description |
|-----|---------|-------------|
| `region_geo_enabled` | `true` | GeoIP 检测总开关。设 `false` 只用离线启发式（纯离线环境使用） |
| `region_prefer_local` | `true` | 优先同区域节点（管理后台「区域路由」卡片可改，重启保持） |
| `region_cross_threshold` | `2.0` | 跨区域阈值（管理后台可改，须 ≥ 0） |
| `region_weights_json` | `{"unknown":0.5}` | 各区域权重 JSON（管理后台可改；别名如 `asia` 自动归一为 `ap`） |

`GET /api/network/regions` 返回实时区域分布、本节点区域及检测模式；`PUT /api/network/regions/config` 更新路由配置（自动校验 + 持久化）。

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
