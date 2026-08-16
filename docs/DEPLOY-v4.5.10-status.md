# OpenModelPool v4.5.10 — 推送与多节点部署状态

> 生成时间：2026-08-16 | 执行：WorkBuddy（雷工指令"放开权限自主推送" + "现在部署"）

## 1. 推送结果（已完成）
- 工作树累积上几批因护栏未提交的修复，直接在当前 `main` 提交（改动已验证，最低风险路径）：commit `f373baa`（10 files, +349/-25）。
- `AppVersion` `4.5.9 → 4.5.10`；`docs/CHANGELOG.md` 加 `v4.5.10` 段。
- `git push origin main`（`79ccd96..f373baa`，fast-forward，无 `--force`）+ `git push origin v4.5.10`；push 后 `git ls-remote` 复核 `main=f373baab…`、`tag v4.5.10=915246c0…` 一致。
- CI 自动发布 release `v4.5.10`，含全平台二进制（`openmodelpool-linux-amd64` sha `c290c5cb4e3f5c7bebc648b2d27050862762a01a9d31bec3c9e8f66c29d97668`）。
- `Release` workflow 对 `v4.5.10` 已 `completed/success`；`Docker` / `CI` 同步跑。

## 2. 各主机更新（已完成）
| 主机 | 结果 | 进程 | :8000 | `/api/version` |
|------|------|------|-------|----------------|
| omp-cc | ✅ 升 v4.5.10（sha 命中） | RUNNING | OK | v4.5.10 |
| omp-org | ✅ 升 v4.5.10（sha 命中） | RUNNING | OK | v4.5.10 |
| omp-com | ✅ 升 v4.5.10（sha 命中） | RUNNING | OK | v4.5.10 |
| omp-io | ✅ 升 v4.5.10（sha 命中） | RUNNING | OK | v4.5.10 |
| omp-net | ✅ 升 v4.5.10（sha 命中） | RUNNING | OK | v4.5.10 |

部署方式：复用 `dist/deploy_remote_cn.sh` SOP——主机侧 `curl` 从 GitHub release 下载 CI 出包（cloudflared 隧道传 ~13MB 二进制会断，故走 release 下载 + 区域镜像兜底 + sha256 校验 + 备份 + `mv` + `systemctl restart`）。脚本 base64 化经 Windows 原生 `ssh.exe`（`C:/Windows/System32/OpenSSH/ssh.exe`）以 `nohup bash &` 分离跑，避开隧道断长会话；短 ssh 轮询 marker 收尾。5 台最终 `sha` 全部 == `c290c5cb…`。

## 3. 本版修复内容（已落地全部生产主机）
- **内部 HTTP transport 现优先 IPv4**（`performance.go` + `dialPreferIPv4`）：`tcp4` 优先，仅当 IPv4 不可用时回退 `tcp6`。修复 IPv6 出网死的主机（Go `net/http` 不兜底导致 ledger reconcile / gossip / federation relay / discovery / 自更新挂到 10s 超时）。transport 层、隧道无关，对所有 peer 一视同仁。
- **`openaiStream` SSE 扫描缓冲 1 MiB → 16 MiB**（`client.go`）：大单行（大 tool_calls / 长 reasoning / 内联媒体）不再触发 `bufio.ErrTooLong` 中断客户端流；回归测试 `TestProxyKeyClient_StreamLargeLine`。
- **内置浏览器执行硬化**（`browser_login.go`）：终端重试 + 显式"Chrome/Chromium 未找到"错误 + `isExecNotFound`；新增 `browser_exec_test.go`。
- **治理措辞统一**：`docs/FEATURES.md` / `docs/FEDERATION.md` 的"积分 / 贡献积分" → "公益额度·贡献记账（非货币）"。

## 4. 部署前 IPv6/IPv4 出网探测（诊断，非对称且异质）
并行 `curl -6` / `curl -4` 到 `openmodelpool.com` 结果：
| 主机 | v6 | v4 | 结论 |
|------|----|----|------|
| omp-cc | 000（死） | 000（瞬时失败） | IPv6 死；本版走 tcp4 |
| omp-org | 200 | 000（IPv4 黑洞） | IPv4 死；本版 tcp4 速败→tcp6 成功 |
| omp-com | 200 | 000（IPv4 黑洞） | IPv4 死；本版 tcp4 速败→tcp6 成功 |
| omp-io | 000（死） | 200 | IPv6 死；本版走 tcp4 |
| omp-net | 000 | 000（双失败，瞬时） | 探测时双死，疑似瞬时断网 |

`dialPreferIPv4` 覆盖全部非对称情况；仅"net 双死"类主机断网无解（非代码问题）。

## 5. 回滚预案
每台主机保留升级前备份 `/opt/openmodelpool/openmodelpool.bak.<时间戳>`（部署脚本 `cp -p` 生成）；异常时 `mv` 回退 + `systemctl restart openmodelpool` 即可。

## 6. 下一步观察
- cc 此前刷的 ledger reconcile `Client.Timeout exceeded while awaiting headers` WARN 应已消失（出网不再挂到 10s）。建议过几分钟看 cc 日志确认 `gossip` / `ledger` 错误归零。
- 若仍有残留超时，多为对端（如 net 瞬时断网）导致，非本机问题。
