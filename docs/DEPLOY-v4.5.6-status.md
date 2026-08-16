# OpenModelPool v4.5.6 — 推送与多节点测试状态

> 生成时间：2026-08-16 | 执行：WorkBuddy 自动化批次（雷工指令"推送V4.5.6并更新各主机启动多节点测试"）

## 1. 推送结果（已完成）
- commit `b80138b`：P1-1(ii) DHT 真实 UDP transport + P1-2b-2(iv) UDP 直连数据承载协议（实现+文档）。
- commit `7969fb2`：版本号 `4.5.5 → 4.5.6`。
- `feature/mvp-iteration` fast-forward merge 进 `main`，push `main` + tag `v4.5.6`（无冲突、无 `--force`，push 后 `ls-remote` 复核一致）。
- CI 自动发布 release `v4.5.6`，含全平台二进制（linux-amd64 sha `0684fea1...`）。

## 2. 各主机更新（已完成）
| 主机 | 结果 | 进程 | :8000 |
|------|------|------|-------|
| omp-cc | ✅ 升 v4.5.6（此前进程不在，已拉起） | RUNNING | OK |
| omp-org | ✅ 升 v4.5.6 | RUNNING | OK |
| omp-com | ✅ 升 v4.5.6 | RUNNING | OK |
| omp-io | ✅ 升 v4.5.6 | RUNNING | OK |

部署方式：主机侧 `curl` 从 GitHub release 下载 CI 出包（cloudflared 隧道传 ~13MB 二进制会断，故走 release 下载 + sha256 校验 + `systemctl restart`）。

## 3. 新传输层验证（已完成）
4 主机日志均确认 P1 代码已激活：
- `DHT Kademlia 256-bit module loaded k=20 alpha=10 buckets=256`
- `NAT traversal manager initialized local_udp=[::]:<port>`
- **`dht: node listening for discovery ... addr=[::]:19001 seeds=0`** ← P1-1(ii) UDP DHT 运输层生效
- `OpenModelPool Agent started port=8000` + quick tunnel 起

代码级多节点测试（已随门禁全绿）：`dht_udp_test.go`、`udp_bearer_test.go` 覆盖 DHT UDP 收发 + UDP 直连承载 round-trip。

## 4. 多节点联邦联调（待雷工人工完成 —— 唯一下一步）
联邦入网需先在**每台**主机 admin UI 完成助记词身份（代码 `network.go:784` 已移除自动生成，出于安全必须人工备份），再开"启用联邦网络"：

1. 浏览器打开各主机管理页（如 `https://openmodelpool.org` / `.cc` / `.com` / `.io`）。
2. 生成并**离线备份助记词**（12/24 词，永不上传）。
3. 将"启用联邦网络"置开（`network_enabled=true`，默认不共享额度）。
4. 待 4 节点 peer 互发现（经 :19001 UDP DHT）+ UDP 直连承载互传，即完成多节点测试。
- 4 主机同 genesis `0xae90fb58...`，入网后即组成同一测试网络。
- Agent 无 admin JWT，无法代调 `/api/network/toggle`；若雷工提供令牌或要求在 admin UI 外驱动，可继续。

## 5. 回滚预案
每台主机保留升级前备份 `/opt/openmodelpool/openmodelpool.bak.<时间戳>`；异常时 `mv` 回退 + `systemctl restart openmodelpool` 即可。
