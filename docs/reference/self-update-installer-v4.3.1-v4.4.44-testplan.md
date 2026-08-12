# OpenModelPool 自动更新 / 安装器 测试计划（v4.3.1 – v4.4.44）

> 版本范围：v4.3.1 – v4.4.44（自动更新/安装器聚合批次）
>
> **聚合理由**：这批版本跨度大但改动始终落在同一条代码路径上——`update.go` 的
> `TriggerSelfUpdate` 下载—校验—替换—重启链路，以及三份安装/管理脚本
> （`scripts/install.sh`、`scripts/omp-manager.sh`、`scripts/omp-manager.ps1`）里与之
> 镜像对应的下载—校验链路。v4.3.1 的出站超时、v4.3.21 的进度上报、v4.3.24 的多镜像
> 回退、v4.3.28 的区域感知、v4.4.44 的 fail-closed 校验，都是同一函数体上的连续增量：
> 单独为每个版本写计划会把同一条链路的验收标准切成六份，端到端 QA 也无法只跑一段。
> 因此按链路聚合，一次串起「从哪里下载 → 下载得到什么 → 凭什么信它 → 换完怎么起来」。
>
> 关联源码：`update.go`、`admin_config_io.go`、`admin_providers.go`、`stubs.go`、
> `platform_discovery.go`、`network_balance.go`、`scripts/install.sh`、
> `scripts/omp-manager.sh`、`scripts/omp-manager.ps1`

---

## 1. 范围与背景

本批次解决四类问题：出站请求无超时导致更新流程挂死；GitHub 直连在部分地区不可达；
下载期间进度条冻结；以及最关键的一项——**校验材料可被镜像投喂**。v4.4.44 之前，
校验缺失走的是 warn-and-continue，等于在连通性最差的网络里把完整性检查也一并放弃了。

| 版本 | 能力 | 关键变更 | 优先级 |
|------|------|----------|--------|
| v4.3.1 | 出站请求全部带超时 | `update.go` 5 处 `context.Background()` 加 30s 超时（`update.go:357` 版本查询、`:898` fetchChecksum、`:931` fetchSignature、`:1130` / `:1251` 联邦信号与回报）；`stubs.go:169`/`:255` 15s、`platform_discovery.go:318` 15s、`network_balance.go:632` 30s | P0（安全/可用性） |
| v4.3.20 | 自更新后服务确实重启 | `TriggerSelfUpdate` 不再依赖 `os.Exit(0)` + systemd `Restart=` 策略：`detectSystemdService()`（`update.go:989`）解析 `/proc/self/cgroup` 中的 `.service` 单元名，命中则 `restartViaSystemd()`（`update.go:1016`，10s ctx，`systemctl restart <unit>`）；失败回退 `os.Exit(1)`（`update.go:760`）以触发 `Restart=on-failure`/`always` | P0（可靠性） |
| v4.3.21 | 下载进度实时显示 | 新增 `progressReader`（`update.go:766`）包装下载流，`minInterval = 500ms`（`update.go:781`）上报百分比与已下载字节，下载阶段进度封顶 65%（`update.go:795`） | P1（可用性） |
| v4.3.24 | 安装器多镜像回退 | `scripts/install.sh` 构造候选源 GitHub direct → `ghfast.top` → `gh-proxy.com` → `ghproxy.net` → `mirror.ghproxy.com`；`download_with_retry`（`install.sh:106`）每源 2 次尝试 + `attempt*2` 秒 backoff，并校验文件大小 ≥ 100000 B（`install.sh:120`）拒收错误页 | P1（可用性） |
| v4.3.28 | 区域感知下载 | `install.sh:39` `detect_region()` / `omp-manager.sh:46` / `omp-manager.ps1:35` `Get-Region` / `update.go:84` `detectRegion()`：经 `ifconfig.me` → `api.ipify.org` → `icanhazip.com` 取公网 IP，再由 `ip-api.com/line/<ip>?fields=countryCode` 定位。**CN → 镜像优先、直连兜底；非 CN → 直连优先、镜像兜底**；检测失败默认 `global` | P1（可用性） |
| v4.4.44 | **更新管道 fail-closed** | 校验材料只从 canonical GitHub 取，**绝不走镜像**：`checksumURL := directURL + ".sha256"`、`sigURL := directURL + ".sig"`（`update.go:676`/`:692`，`directURL` 是未加镜像前缀的原始 release 地址）。checksum 获取失败 / 不匹配、签名获取失败 / 验签失败，四种情况**一律删除临时文件并中止更新**（`update.go:677-710`），不再 warn-and-continue。`install.sh:154-176` 同步收紧：`SHA_URLS` 只含官方 `${URL}.sha256`，取不到即 `fail` | P0（安全 high） |
| v4.4.44 | provider config import 收紧 | `handleImportConfig` 对每个 provider 的 `BaseURL` 调用共享的 `validateProviderBaseURL`（`admin_config_io.go:173` → `admin_providers.go:20`，scheme + 私网 IP SSRF 校验）；改为**按 ID upsert 合并**而非整表截断（`admin_config_io.go:194`）；`pm.save()` 移到 `pm.mu.Unlock()` 之后（`admin_config_io.go:196-202`），修掉 v4.3.32 引入的 `save()` 重入 RWMutex 自死锁 | P0（安全 high + 可靠性） |

**镜像信任模型（本批次的核心不变量）**：镜像只被允许提供**不透明字节**。
二进制可以来自任意镜像，但断言这些字节可信的 checksum 与签名必须来自 canonical GitHub。
任何让校验材料与被校验对象走同一条（可被同一方控制的）通道的实现，都退化为无校验。

---

## 2. 自动化单元测试

运行本批次相关的既有用例：

```bash
# update.go 工程侧用例（7 个）
go test . -run 'TestCompareVersion|TestAtomicReplace|TestDownloadFile404|TestDownloadFileOK|TestOldPeerUnsupported|TestReconcilePending' -count=1 -v

# update QA 套件（20 个）
go test . -run 'TestQA' -count=1 -v

# config import 合并 + 死锁回归（1 个，务必带 -timeout：旧代码下会挂死而非失败）
go test . -run 'TestImportConfig_ShareToPoolMerge' -count=1 -timeout 60s -v

# 原子写盘 / 持久化回归
go test . -run 'TestAtomicWriteFile|TestConfig_SaveSync' -count=1 -v
```

### 2.1 既有测试映射

| 测试文件 | 用例 | 验证点 |
|----------|------|--------|
| `update_test.go` | `TestCompareVersion` | 语义版本比较：v 前缀、段数不等补零、多位数段、与 `MinSupportedUpdateVersion` 的边界、空串 |
| | `TestAtomicReplace` | tmp → rename 原子替换；临时文件被 rename 消费；Windows 下原二进制备份为 `<exe>.bak` |
| | `TestDownloadFile404` | 下载 404 时报错含 `404`，且**不留下部分/空目标文件**（否则后续校验会拿到垃圾字节） |
| | `TestDownloadFileOK` | 200 路径写出非空文件 |
| | `TestOldPeerUnsupported` | 旧 peer 无 update-signal 端点（404）→ 标记 `PhaseUnsupported`，不阻塞广播 |
| | `TestReconcilePendingSuccess` | pending 标记的 target 等于当前运行版本 → 提升为 `PhaseSuccess`/progress=100 并删除标记（自更新在重启后自证完成） |
| | `TestReconcilePendingInFlightReset` | 无 pending 标记但残留 in-flight 阶段 → 重置为 `PhaseIdle`/0，UI 不会永久卡住 |
| `update_qa_test.go` | `TestQACompareVersionBoundaries` | 比较函数更严边界：双 v 前缀、pre-release < release、首尾空格、单段版本、零补齐等价 |
| | `TestQAPersistRoundTrip` | 状态快照 HMAC 持久化往返一致 |
| | `TestQAPersistTamperDetected` | 被篡改的状态文件 fail-closed 拒绝加载 |
| | `TestQAPersistPlainJSONFallback` | 无 HMAC 前缀的历史纯 JSON 文件仍可加载（向后兼容） |
| | `TestQARecPendingTargetMismatch` | pending target 与运行版本不符 → 不误报成功 |
| | `TestQARecPendingCorruptMarker` | 损坏的 pending 标记不导致 panic，状态收敛 |
| | `TestQACapabilityNegotiationUnsupported` | 能力协商：低于 `MinSupportedVersion` 的节点被判 unsupported |
| | `TestQAReplayProtectionTimestamp` | 更新信号时间戳 ±5min 之外拒收 |
| | `TestQASignalReplayRejected` | 重放同一信号被拒 |
| | `TestQASignatureRoundTrip` | ed25519 签名/验签往返（联邦更新信号的安全主路径） |
| | `TestQARouteVersionLatest` | `GET /api/admin/version/latest` 打本地 mock GitHub（`um.githubURL`）→ 200，含 `current_version`/`latest_version`/`has_update`/`checked_at`，`has_update=true`。**同时是 v4.3.1 版本查询超时路径（`update.go:357`）的执行覆盖** |
| | `TestQARouteUpdateStatus` | `GET /api/admin/update/status` 返回 `statuses[]`，本地条目含 `env`/`phase`/`target_version`/`progress` 四字段——即 v4.3.21 进度上报的落地载体 |
| | `TestQARouteUpdateStartNoUpdate` | 无可用更新时 `POST /api/admin/update/start` → 400（不触发下载） |
| | `TestQARouteSignalValidSignature` | 合法签名 + `X-Node-ID` 命中信任池 → 通过验签；用高 `MinSupportedVersion` 走 unsupported 分支以规避 `os.Exit` 副作用 |
| | `TestQARouteSignalWrongSignature` | 错误签名 → 403 |
| | `TestQARouteSignalUnknownNode` | 未知节点 → 拒绝 |
| | `TestQARouteReportValidSignature` | 回报端点合法签名接受 |
| | `TestQARouteReportWrongSignature` | 回报端点错误签名拒绝 |
| | `TestQARouteAdminUpdateJS` | 管理页 JS 资源可达 |
| | `TestQAFrontendWiring` | 前端与更新接口字段接线一致 |
| `config_import_qa_test.go` | `TestImportConfig_ShareToPoolMerge` | v4.4.44 config import 主回归，5 个 case：省略 `share_to_pool`/`api_key` 时保留现存值（导出-导入往返安全）；嵌套 `access_control.share_to_pool` 显式值被采纳；顶层未绑定的 `share_to_pool` 不算显式值。**死锁回归是隐式的**：旧代码持写锁调 `pm.save()` 自死锁，本用例会挂死而非断言失败——所以必须带 `-timeout` 跑 |
| `config_test.go` | `TestAtomicWriteFile` | 原子写盘语义（`pm.save()` / 状态持久化的底座） |
| | `TestConfig_SaveSync` | 同步保存路径不丢写 |

引用既有真实用例合计 **30** 个。

### 2.2 测试缺口（建议新增，当前**无**自动化覆盖）

以下是本批次改动中**没有**任何单测触及的部分。按风险排序：

| 优先级 | 建议用例 | 应验证的行为 | 目标代码 |
|---|---|---|---|
| **P0** | `TestSelfUpdate_ChecksumFetchFailureAborts` | `fetchChecksum` 报错 → 状态 `PhaseFailed`、临时文件被删除、**不调用 `atomicReplace`** | `update.go:677-682` |
| **P0** | `TestSelfUpdate_ChecksumMismatchAborts` | `downloadHash != expectedHash` → 中止并删除 tmp，失败信息含期望/实际哈希 | `update.go:683-687` |
| **P0** | `TestSelfUpdate_MissingSignatureAborts` | `.sig` 404 → 中止（v4.4.44 前此处是 warn-and-continue，回归风险最高） | `update.go:693-698` |
| **P0** | `TestSelfUpdate_ForgedSignatureAborts` | 签名有效长度但非官方私钥所签 → `ed25519.Verify` 失败 → 中止 | `update.go:706-710` |
| **P0** | `TestSelfUpdate_ChecksumURLIsCanonicalNotMirror` | **结构性守护**：即使二进制经镜像下载成功，`fetchChecksum`/`fetchSignature` 收到的 URL 必须以 canonical GitHub 前缀开头，不含任何 `githubDownloadMirrors` 条目 | `update.go:676`/`:692` |
| **P1** | `TestBuildDownloadSources_RegionOrder` | `region=="cn"` → 4 个镜像在前、`GitHub 直连` 最后；否则直连在前。当前该顺序逻辑内联在 `TriggerSelfUpdate` 中不可单测，建议先抽出 `buildDownloadSources(directURL, region)` 再测 | `update.go:616-640` |
| **P1** | `TestDetectRegion_CNAndFallback` | mock IP/geo 服务：`countryCode=CN` → `"cn"`；geo 失败或取不到公网 IP → 默认 `"global"`；结果被缓存只探测一次。需将 `ipServices`/`geoURL` 参数化后方可测 | `update.go:84-155` |
| **P1** | `TestDetectSystemdService_ParsesCgroupUnit` | cgroup v2 `0::/system.slice/openmodelpool.service` 与 v1 `1:name=systemd:/system.slice/omp.service` 均解析出单元名；无 `.service` 行返回 `""`。需将 `/proc/self/cgroup` 路径参数化（当前硬编码，非 Linux 直接返回 `""`） | `update.go:989-1012` |
| **P1** | `TestTriggerSelfUpdate_FallsThroughMirrors` | 前 N 个源返回 5xx、第 N+1 个成功 → 最终成功且 tmp 文件在每次失败后被清理；全部失败时错误信息含已尝试源数量 | `update.go:645-665` |
| **P2** | `TestProgressReader_ReportsAtInterval` | 每 ≥500ms 或百分比变化时回调一次；`total<=0` 时 percent 为 -1；下载阶段百分比封顶 65 | `update.go:787-806` |
| **P2** | `TestImportConfig_RejectsPrivateIPBaseURL` | import 含 `http://127.0.0.1` / `http://10.0.0.1` / 非 http(s) scheme 的 provider → 400，且**已导入的前序 provider 不被留下半套状态** | `admin_config_io.go:173` |
| **P2** | `TestImportConfig_MergesNotTruncates` | 已有 p1、p2，import 只含 p2 → p1 仍存在（合并语义，非整表替换） | `admin_config_io.go:194` |

> 脚本层（`install.sh` / `omp-manager.sh` / `omp-manager.ps1`）目前**完全没有**自动化测试，
> 且 Go 测试框架也覆盖不到。第 3 节的手动 QA 是这部分唯一的验收手段；若要补自动化，
> 需要引入 shell 测试框架（如 bats）并独立立项，本计划不作承诺。

---

## 3. 集成 / QA 手册：自更新端到端

### 3.1 环境

- 一台 Linux VPS，systemd 管理，单元名如 `openmodelpool.service`，`Restart=on-failure`。
- 已安装一个**低于** `v4.4.44` 的版本（用于触发真实更新）。
- 一台海外节点与一台中国大陆节点（验证 v4.3.28 区域分支）；无条件时用 3.4 的强制手段替代。
- 本地 HTTP 服务器 + hosts 改写能力（用于 3.5 的篡改场景）。

### 3.2 A：正常自更新全链路（v4.3.20 / v4.3.21 / v4.3.28）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| A.1 | 启动服务，`GET /api/version` | 返回旧版本号 |
| A.2 | 管理面板 → 版本更新 → 「检查更新」 | 显示 `has_update=true` 与目标版本；日志无超时报错（v4.3.1） |
| A.3 | 观察启动日志中的区域检测行 | 出现 `region detection: China mainland detected, mirror-first download` 或 `non-CN region detected, direct-first download`（v4.3.28） |
| A.4 | 点击「开始更新」，全程注视进度条 | 进度条**持续跳动**，每约 500ms 刷新一次，并显示已下载 MB 数；**不出现长时间冻结**（v4.3.21）。下载阶段百分比不超过 65% |
| A.5 | 观察下载源日志 | CN 环境先试 `ghfast.top` 等镜像；海外环境先试 GitHub 直连（v4.3.28） |
| A.6 | 校验阶段日志 | 依次出现 `SHA-256 checksum verified` 与 `Ed25519 signature verified`；两条 URL 均指向 `github.com/...`，**不带任何镜像前缀**（v4.4.44） |
| A.7 | 替换与重启 | 日志出现 `self-update binary replaced; restarting service` 与 `attempting explicit systemctl restart`，`service` 字段为真实单元名（v4.3.20） |
| A.8 | `systemctl status openmodelpool` | `active (running)`，PID 已变化，启动时间为刚刚 |
| A.9 | `GET /api/version` | 返回新版本号 |
| A.10 | 管理面板更新状态 | `phase=success`、`progress=100`，pending 标记已被清理（`reconcilePending` 自证） |

### 3.3 B：非 systemd 环境回退（v4.3.20）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| B.1 | 在容器/裸进程（无 systemd cgroup）中运行同版本并触发更新 | `detectSystemdService()` 返回 `""`，跳过 `systemctl` 分支 |
| B.2 | 观察退出日志 | 出现 `exiting with code 1 for supervisor restart`，进程以**退出码 1** 结束（不是 0） |
| B.3 | 由外部 supervisor（Docker `restart: always` / `Restart=always`）重拉 | 新进程以新版本启动；若 supervisor 策略为 `Restart=no`，服务停止属预期，需人工拉起 |

### 3.4 C：区域感知与镜像回退（v4.3.28 / v4.3.24）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| C.1 | CN 节点执行 `curl -fsSL .../install.sh \| bash` | 输出「检测到中国大陆网络环境，优先使用镜像下载」，首个尝试源为 `ghfast.top` |
| C.2 | 海外节点执行同一命令 | 输出「检测到海外网络环境，优先直连 GitHub」，首个尝试源为 `github.com` |
| C.3 | 用 hosts 将 `github.com` 指向黑洞地址后重跑安装 | 直连失败后**自动切换**到下一镜像，逐源打印「尝试源: xxx」，最终安装成功（v4.3.24） |
| C.4 | 令某镜像返回 HTML 错误页（体积 < 100000 B） | 该源被判「文件异常 (NB)」并跳过，不会把错误页当二进制装上（`install.sh:120`） |
| C.5 | 断开所有出口后重跑 | 明确失败：「所有下载源均失败」，**不留下**半装状态 |
| C.6 | 令全部 IP 定位服务不可达后重跑 | 区域检测降级为 `global`（直连优先），流程继续而非中断 |

### 3.5 D：fail-closed 校验（v4.4.44，本批次最关键场景）

前置：将 `github.com` 通过 hosts + 本地 HTTPS mock 指向可控服务器，使 release 资产可被逐项替换。

| 步骤 | 操作 | 期望结果（**失败即为阻塞缺陷**） |
|---|---|---|
| D.1 | 移除 `<asset>.sha256`（返回 404），触发自更新 | 更新**中止**，状态 `failed`，提示「无法获取 GitHub 官方 SHA-256 校验和，已中止更新」；临时文件被删除；**旧二进制未被替换**，服务仍以旧版本正常运行 |
| D.2 | 篡改 `<asset>.sha256` 为不匹配的哈希 | 中止，提示「SHA-256 校验失败: 期望 … 实际 …」；同上不替换、不重启 |
| D.3 | 移除 `<asset>.sig`（404） | 中止，提示「无法获取 GitHub 官方 Ed25519 签名，已中止更新」 |
| D.4 | 用**非官方私钥**重新签名二进制并替换 `.sig` | 中止，提示「Ed25519 签名验证失败: 二进制文件可能被篡改」 |
| D.5 | 篡改二进制本体但保留原始正确的 `.sha256` | 在 D.2 同一处被 SHA-256 拦下并中止 |
| D.6 | **镜像投毒**：镜像返回被篡改的二进制，canonical GitHub 的 checksum/`.sig` 保持真实 | 二进制虽下载成功，但校验阶段失败并中止。抓包确认 `.sha256`/`.sig` 两个请求**只发往 github.com**，绝无镜像前缀 |
| D.7 | 对 `install.sh` 重复 D.1（`.sha256` 404） | 脚本以「无法从 GitHub 官方获取 SHA-256 校验文件，已中止安装（fail-closed）」退出，非零退出码，不安装任何文件 |
| D.8 | 对 `install.sh` 重复 D.2（哈希不匹配） | 「SHA-256 校验失败！expected=… actual=…」退出 |

> **已知不一致（需在本轮 QA 中确认口径，勿当作 D.1–D.8 的失败）**：
> v4.4.44 的 fail-closed 只落到了 `update.go` 与 `scripts/install.sh`。
> 另两份管理脚本仍是旧的宽松语义：
> - `scripts/omp-manager.sh:454` —— `.sha256` 下载带 `|| true`，取不到即打印「跳过 SHA256 校验（无校验文件）」并继续安装；
> - `scripts/omp-manager.ps1:367` —— `.sha256` 经 `Invoke-DownloadWithRetry` 获取，该函数对 GitHub URL 会走 `Get-GitHubCandidates` 展开**镜像候选**，因此校验文件可能来自镜像；且 `if (Test-Path $tmpSha)` 意味着取不到校验文件时静默跳过校验。
>
> 这两处与第 1 节的镜像信任模型直接冲突（校验材料与被校验字节可由同一方提供）。
> 请在 QA 中如实记录现状，并作为独立缺陷提出，不要在本测试计划中假设其已修复。

### 3.6 E：provider config import（v4.4.44 / v4.3.32 死锁）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| E.1 | 已有 provider p1、p2，导入只含 p2 的配置文件 | p1 **仍存在**（合并而非截断）；p2 被更新 |
| E.2 | 导入含 `base_url: http://127.0.0.1:8080` 的 provider | 400，错误信息含该 provider ID；SSRF 校验与创建路径一致 |
| E.3 | 导入含 `base_url: ftp://x` 的 provider | 400（scheme 校验） |
| E.4 | 导入文件省略 `api_key` / `access_control.share_to_pool` | 保留现存密钥与现存 `share_to_pool`（导出-导入往返不丢配置） |
| E.5 | 导入后立即在面板做任意 provider 增删改 | **立刻响应**。若界面卡死、请求永不返回，即 `save()` 持锁死锁复发（v4.3.32 → v4.4.44 回归） |
| E.6 | 导入后重启服务 | 变更已落盘（`pm.save()` 在解锁后真实执行，而非被死锁吞掉） |

### 3.7 失败判据

任一条成立即判本批次不通过：

- D.1–D.6 中**任何一项**未中止更新，或替换了二进制 —— fail-closed 失效（P0 安全）。
- 抓包发现 `.sha256` 或 `.sig` 请求带镜像前缀 —— 镜像信任模型被破坏（P0 安全）。
- A.7/A.8 更新后服务未起来（`systemctl status` 非 active 且退出码为 0）—— v4.3.20 回归。
- B.2 非 systemd 环境下以退出码 0 结束 —— 退化为 v4.3.20 修复前的挂死行为。
- A.4 进度条冻结超过 5s 无刷新 —— v4.3.21 回归。
- C.3 直连失败后未切换镜像，或 C.4 把错误页当二进制安装 —— v4.3.24 回归。
- C.1/C.2 区域分支判定与实际网络位置相反 —— v4.3.28 回归。
- C.6 IP 定位不可达时流程中断（而非降级为 global）—— 区域检测未做 fail-open 降级。
- E.1 导入后既有 provider 消失 —— 截断语义复发。
- E.2/E.3 私网地址或非法 scheme 被接受 —— SSRF 校验缺失。
- E.5 导入后 provider 操作挂死，或 `TestImportConfig_ShareToPoolMerge` 超时 —— 死锁复发。
- 任一出站请求无超时导致更新流程无限挂起 —— v4.3.1 回归。

---

## 4. 质量门禁（CI）

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -l .` | 无输出 |
| 构建 | `go build ./...` | 0 error |
| 静态检查 | `go vet ./...` | 0 新增 warning |
| 更新链路单测 | `go test . -run 'TestCompareVersion\|TestAtomicReplace\|TestDownloadFile\|TestOldPeerUnsupported\|TestReconcilePending' -count=1` | 全部 PASS |
| 更新 QA 套件 | `go test . -run 'TestQA' -count=1` | 全部 PASS |
| import 死锁回归 | `go test . -run 'TestImportConfig_ShareToPoolMerge' -count=1 -timeout 60s` | PASS 且**秒级**返回（旧代码会挂到超时） |
| 全量（含竞态） | `go test -race -count=1 -timeout 25m ./...` | 通过。与 v4.3.32 起的 CI 门禁一致；`-count=1` 禁用结果缓存 |
| 脚本语法 | `bash -n scripts/install.sh scripts/omp-manager.sh` | 0 error |
| 脚本静态 | `shellcheck scripts/install.sh scripts/omp-manager.sh` | 无新增 error 级告警（当前无此门禁，建议纳入） |
| 手动 QA | 第 3 节 A–E 全部场景 | D 段必须 100% 通过，无例外 |

> Windows 沙箱下 `go test ./...` 存在与文件权限/日志锁相关的预存失败，与本批次无关；
> 判定以 Linux CI 为准。

---

## 5. 一致性复核（IS_PASS）

- [ ] `update.go` 5 处 30s 超时在位（`:357`、`:898`、`:931`、`:1130`、`:1251`），无裸 `context.Background()` 用于出站请求
- [ ] `stubs.go`（15s）、`platform_discovery.go`（15s）、`network_balance.go`（30s）出站超时在位
- [ ] `detectSystemdService()` 解析 `/proc/self/cgroup` 的 `.service`，cgroup v1/v2 两种格式均覆盖
- [ ] `restartViaSystemd()` 显式 `systemctl restart`，带 10s ctx；失败回退 `os.Exit(1)` 而非 `os.Exit(0)`
- [ ] `progressReader` 的 `minInterval == 500ms`，回调上报百分比与已下载字节，下载阶段封顶 65%
- [ ] `githubDownloadMirrors` 四源齐备（`ghfast.top` / `gh-proxy.com` / `ghproxy.net` / `mirror.ghproxy.com`），且 `update.go` 与三份脚本的镜像列表一致
- [ ] 区域检测四处实现（`update.go`、`install.sh`、`omp-manager.sh`、`omp-manager.ps1`）判定口径一致：CN → 镜像优先、非 CN → 直连优先、检测失败 → `global`
- [ ] `install.sh` 每源 retry + backoff + 文件大小 ≥ 100000 B 校验在位
- [ ] **checksum 与签名 URL 由未加镜像前缀的 `directURL` 拼接**（`update.go:676`/`:692`），代码中不存在任何把镜像前缀拼进校验 URL 的路径
- [ ] checksum 缺失 / 不匹配、签名缺失 / 验签失败，四条分支**均**删除 tmp 并 `setLocalFailed` 后 `return`，无一条继续走到 `atomicReplace`
- [ ] `install.sh` 的 `SHA_URLS` 仅含官方 `${URL}.sha256`，取不到即 `fail`
- [ ] `handleImportConfig` 对每个 provider 调用 `validateProviderBaseURL`，与创建路径同一函数（无重复实现漂移）
- [ ] config import 为 upsert 合并，不清空既有 provider 集合
- [ ] `pm.save()` 在 `pm.mu.Unlock()` 之后调用，`admin_config_io.go` 内无「持写锁调 save」路径
- [ ] `TestImportConfig_ShareToPoolMerge` 在 60s 内 PASS（死锁哨兵）
- [ ] 第 2.2 节 12 项测试缺口已登记为待办；4 项 P0（fail-closed 相关）已排期
- [ ] 3.5 节记录的 `omp-manager.sh` / `omp-manager.ps1` 校验不一致已作为独立缺陷提单
- [ ] `main.go` `AppVersion = "4.4.44"`；`docs/CHANGELOG.md` 各版本条目与本文表格一致
- [ ] 全文无 `TODO` / 占位 / 未验证的测试名
