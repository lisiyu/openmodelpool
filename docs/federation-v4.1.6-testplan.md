# OpenModelPool 私有节点联邦组网联调测试计划（v4.1.6）

> 目标版本：**v4.1.6**｜修复范围：私有节点互连（手动加 Peer / 邀请码 / 种子发现）
> 适用场景：两个自托管 OMP 实例 `https://openmodelpool.io` 与 `https://openmodelpool.com`
> 都已 `network_enabled=true, mode=shared`，需互相发现并共享 provider。

---

## 0. 前置条件（两节点都要做）

1. 两节点均升级到 **v4.1.6**（`/api/version` 返回 `4.1.6`）。
2. 两节点均已在管理面板「共享网络」完成身份生成 + 助记词备份 + 启用共享网络。
3. 两节点的**公网地址可达**（防火墙/隧道/HTTPS 已配置）。可用以下命令自检：
   ```bash
   curl -s https://openmodelpool.io/api/network/heartbeat/ping
   # 期望: {"status":"ok","node_id":"mmx-xxxx",...}
   curl -s https://openmodelpool.com/api/network/heartbeat/ping
   ```
4. （可选但推荐）在 `config.json` 配置 `public_domain`（或 `federation_endpoint`），避免 endpoint 回落到内网地址：
   ```json
   { "public_domain": "https://openmodelpool.io" }
   ```
   配置后生成的邀请码 / peer 注册信息中的 `endpoint` 应为公网域名。

> ⚠️ **待确认（阻塞项 Q1）**：两节点的 `NetworkID (genesis)` 必须一致，否则邀请码 `redeem` 会拒签（`network_id mismatch`）。联调前请确认两端 `.io` / `.com` 使用同一 `GenesisConfig`（同一 `NetworkName`）。手动加 Peer 路径不受 genesis 约束。

---

## 1. 手动添加 Peer（P0-2，主互连路径，推荐）

| 步骤 | 操作（在 openmodelpool.io 节点） | 期望结果 |
|---|---|---|
| 1.1 | 管理面板 → 「网络」页 → 「➕ 添加节点（手动互连）」 | 出现 `peerAddr` / `peerNodeId` 输入框与「添加」按钮 |
| 1.2 | `peerAddr` 填 `https://openmodelpool.com`，`peerNodeId` 留空 | — |
| 1.3 | 点击「添加」 | Toast「✅ 节点已添加」；下方「👥 已连接节点」出现 `openmodelpool.com` 条目 |
| 1.4 | 观察该条目状态点 | 应为 🟢 online（心跳持续刷新）；若不可达则 🔴 offline |
| 1.5 | 反向：在 `.com` 节点重复 1.2–1.4，填 `.io` 地址 | 两端互现对方 |
| 1.6 | 点击条目「移除」按钮 | 条目消失，对方从本节点 peer 列表移除 |

**后端校验点**：`handleNetworkAddPeer` 在 `node_id` 为空时，向 `{addr}/api/network/heartbeat/ping` 解析 `node_id`；地址与 node_id 均空时返回 400；`mode != shared` 时报错。

**自测（自动化）**：见 `network_peer_test.go`（`TestNetworkAddPeer_*`）。

---

## 2. 邀请码互连（P1-2）

| 步骤 | 操作（.io 生成，.com redeem） | 期望结果 |
|---|---|---|
| 2.1 | 在 `.io` 节点「🔑 邀请码」区点击「生成邀请码」 | Toast「✅ 邀请码已生成」；`fedInviteCode` 显示 `omp-fed-xxxx` 且已复制到剪贴板 |
| 2.2 | 复制 `fedInviteCode` 中的邀请码 | — |
| 2.3 | 在 `.com` 节点「粘贴对方邀请码」输入框粘贴该码，点「加入网络」 | 经 `verify` → `join`；Toast「✅ 已加入对方网络」；`.io` 出现在 `.com` 的 peer 列表 |
| 2.4 | 观察 `.com` 的「已连接节点」 | 出现 `.io`，状态点随时间变化 |
| 2.5 | 「已生成记录」区（`inviteList`） | 渲染 2.1 生成的邀请码记录（类型/受邀方/过期时间） |

**契约要点（前端已修复）**：
- 生成请求体：`{invitee_name:"", type:"public", expires_hours:72}`（旧版错发 `invite_type`）。
- 生成响应读取 `d.encoded`（旧版错读 `d.code`）。
- redeem：`POST /api/federation/invites/verify {encoded}` → `POST /api/federation/join {network_id, node_id, pub_key, endpoint, invite_sig}`。

> ⚠️ 若 2.3 返回「network_id mismatch」，说明两端 genesis 不一致（Q1），需先统一 genesis 或改用「手动添加 Peer」路径。

---

## 3. 种子发现（P1-1，可选自动路径）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.1 | 在 `.io` 的 `config.json` 配置 `bootstrap_nodes: ["https://openmodelpool.com"]` 并重启 | 日志显示 bootstrap 列表已加载 |
| 3.2 | `.com` 同样配置 `bootstrap_nodes: ["https://openmodelpool.io"]` 并重启 | 同上 |
| 3.3 | 等待刷新周期（默认 5 分钟）或重启触发 `fetchFromSeedNodes` | `.io` 向 `https://openmodelpool.com/federation/pool` 发**无认证 GET**；v4.1.6 对该只读路径、仅对可信种子 Host 放行，返回 200（旧版 403） |
| 3.4 | 检查「已连接节点」/ 联邦池 | 两端通过种子互相发现节点信息 |

**安全说明**：放行仅限 `GET /federation/pool` 且请求 Host 命中 `BootstrapNodes`，其余路径（含写操作）仍严格鉴权（SA-12 不破坏）。

**自测（自动化）**：见 `federation_auth_test.go`（可信种子 GET 放行 / 非种子 403 / 非 GET 403 / 其他受保护路径 403）。

---

## 4. 端点可达性（P0-1，回归）

| 步骤 | 操作 | 期望 |
|---|---|---|
| 4.1 | 未配置 `federation_endpoint` / `public_domain`，向 `.io` 生成邀请码 | 邀请码内 `endpoint` 应为请求 `Host` 对应的 `https://openmodelpool.io` |
| 4.2 | 配置 `public_domain=https://openmodelpool.io` 后生成邀请码 | `endpoint` 应为 `https://openmodelpool.io` |
| 4.3 | 完全未配置、且非请求上下文（如注册/gossip） | 回落 `http://<hostname>:<port>`，日志打印 `[WARN] resolvePublicEndpoint fell back to LAN address` |

---

## 5. 自动化测试清单（已提交）

| 测试文件 | 覆盖 |
|---|---|
| `federation_auth_test.go` | P1-1 种子只读 GET 放行且不破坏 SA-12 |
| `network_peer_test.go` | P0-2 空 node_id 经 ping 解析 / 地址缺失 400 / Mode 非 shared 报错 |
| 既有回归 | `go build ./...` 通过；`getEndpoint` 不再回落内网 |

> 注：本仓库在 Windows 沙箱中 `go test ./...` 会有 3 个与文件权限/日志锁相关的预存失败（`TestP0_4_*`、`TestP0_6_LoggerConcurrentRotation`），属环境限制，与本次改动无关。

---

## 6. 已知风险 / Open Questions

- **Q1（阻塞 P1-2）**：两端 `NetworkID(genesis)` 不一致 → 邀请码 redeem 拒签。手动加 Peer 不受影响。
- **Q2**：`.com` 是否公网可达正确端口/路径/HTTPS。先用 0.3 的 curl 自检。
- **Q5（安全）**：P1-1 放行仅暴露只读 trust pool（节点公开信息），风险可控。
- **Q6（已修复）**：`generateFedInvite` 字段/响应错配（`invite_type`→`type`、`d.code`→`d.encoded`）。
