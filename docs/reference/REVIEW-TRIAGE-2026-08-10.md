# OpenModelPool Review 报告甄别清单（Triage）

> 甄别对象：`D:\openmodelpool-review\REVIEW-FULL-v4.3.31.md`（399 行，性能 25 / 安全 25 / 易用性 16 / PRD·文档 12）
> 代码基线：`D:\openmodelpool` @ **8d45330**（v4.3.32，feature/mvp-iteration，194 个 .go，工作树干净）
> 甄别日期：2026-08-10
> 方法：只读分析，逐条对照当前磁盘代码给出判定；报告声明的"文件:行号"按当前代码重新定位（基线 v4.3.31 + 本地领先 3 提交，行号已漂移）。

## 0. 统计摘要

| 判定 | 数量 | 占比 | 说明 |
|---|---|---|---|
| **VERIFIED 真实存在** | 72 | 92.3% | 其中安全 23 / 性能 25 / 易用性 13 / PRD·文档 11 |
| **FALSE-POSITIVE 误报** | 2 | 2.6% | 安全 P3-20（ephemeral 回退被夸大）、易用性 P1-16（SSE 实际可用） |
| **ALREADY-FIXED 已修复** | 1 | 1.3% | PRD·文档 #2（P5-3 CI 门禁，v4.3.32 交付） |
| **BY-DESIGN 设计权衡** | 3 | 3.8% | 安全 P3-19（熵失败回退）、易用性 P0-2（无影响预览）、P1-5（通用错误消息） |
| **合计** | **78** | 100% | 另有若干 VERIFIED 项附带"部分细节修正/需工程师核实"注记 |

**甄别要点**：
- 报告基线已过时：第四节 #2（P5-3 CI 门禁）在 v4.3.32（commit 8d45330）已交付，报告仍标"未实现"——**ALREADY-FIXED**，是报告基于旧代码的硬证据。
- 报告自评高危项（性能 P0-1、安全 P0-1/P0-2/P1-4、更新 fail-open、文档背离、PRD 完成度）**全部属实**，且均已按当前代码重新定位行号。
- 例外：CHANGELOG:397/399 声称"不再信任客户端 X-MK-KeyType / relayToRemote 已剥离 X-OMP-KeyType"与当前代码**矛盾**（仅改了头名、strip 从未落地）——安全 P0-2 因此成立且是当前最危险的访问控制绕过之一。

---

## 1. 性能（Performance）25 条

### P0-1. RWMutex 死锁 — `defer RLock` 应为 `RUnlock` — **VERIFIED（P0，一字修复）**
- 当前证据：`network.go:1246-1247`
  ```go
  nm.mu.RLock()
  defer nm.mu.RLock()   // ← 应为 RUnlock；defer 在函数返回时再次 RLock，两次读锁全部泄漏
  ```
- `nm.GetNodeID()`（`network.go:1053-1057`）在已持 `nm.mu.RLock` 时再次 RLock（`network.go:1253`）：Go RWMutex 在 writer 排队时拒绝新 RLock → 递归读锁可死锁。
- 热路径确认：`relay.go:164`、`network_relay.go:765` 在 shared mode 每个 relay 请求调用。
- 修复：`defer nm.mu.RUnlock()`；锁内直接读 `selfID := nm.config.NodeID`（锁已持有，无需再调 GetNodeID）。风险：零。工作量：S。

### P0-2. 每请求 O(n) ledger 全表扫描 — **VERIFIED（P0）**，锁序子项需工程师核实
- 当前证据：`network.go:1250-1263` — `contributionLedger.GetAllTransactions()`（`contribution_ledger.go:555-561` 持 `g.mu.RLock` 复制全量切片）在持 `nm.mu.RLock` 期间遍历；无日期过滤（"日限额"实为累计额）；`txs` 只 append 不裁剪。
- 锁序子项：报告称与 `network.go:1930` 构成 `netMgr.mu→ledger.mu` / 反向嵌套风险。当前代码中 `network.go:1930-1941`（notify 记 claims）在 `AddPeer` 返回后执行，未持 netMgr 锁；未发现确定的反向顺序路径 → **需工程师核实**。
- 修复：按 (selfID,date) 增量计数器在 AppendTransaction 时维护；或按日期过滤查询。风险：中（需迁移存量）。工作量：M。

### P0-3. 每条贡献记录无界起 goroutine — **VERIFIED（P0）**
- 当前证据：`contribution_ledger.go:168-171` `go ledgerReplicator.ReplicateContribution(&recCopy)` 每记录一个 goroutine；`ledger_replication.go:39` replicator 的 `&http.Client{Timeout:10s}` 无 context deadline。另 `network_relay.go:986-999` 每次成功网关中继额外 `go func(){RecordContribution+AppendTransaction+saveContributionLedger}()`。
- 修复：有界 worker pool / 批量 tick 扇出。风险：低-中。工作量：M。

### P1-4. `saveContributionLedger` 同步全量落盘无去抖 — **VERIFIED**（部分细节修正）
- 当前证据：`contribution_ledger.go:579-599`（`Save`）全量 `MarshalIndent` + `atomicWriteFile`；无防抖（对比 `config.go:62`、`multiuser.go:65` 有批量写）。热路径调用：`network_relay.go:998`、`network.go:1939`、`saveContributionLedger()`（`contribution_ledger_init.go:69-76`）。
- **修正**：报告称"RLock 持有期间 marshal 阻塞所有 ledger 读者"——当前代码 `RUnlock` 在 `:592`、marshal 在 `:594`，锁已先释放，该描述不成立。但因此 marshal 读取的是**无锁存活的 map 引用**（`:581-591` 只拷贝引用），并发写存在数据竞争窗口（新引入的隐患）。
- 修复：防抖批量写 + 快照后 marshal。风险：中。工作量：M。

### P1-5. 7 个后台循环无 shutdown 路径 — **VERIFIED**
- 逐条确认（均 `for range ticker.C`、无 `globalStopCh` select）：
  | 循环 | 当前位置 |
  |---|---|
  | `NATManager.stunLoop` | `nat_traversal.go:68-75` |
  | `ProbeSchedulerLoop` | `contribution_ledger.go:739-773` |
  | `GlobalPool.refreshLoop` | `network_global_pool.go:511-528` |
  | `PublicKeyQuota.resetLoop` | `network_global_pool.go:1148-1156` |
  | `Logger.rotationLoop` | `logger.go:76-82` |
  | `startTicketCleanup` / `notarizeLoop` | `ticket.go:87-95` / `ticket.go:235-265` |
  | `startAutoRefresh` | `network_quota.go:220-226` |
- `ledgerReconcileStop`：`ledger_replication.go:248` 创建、`:252` select，**全仓无 `close` 调用**（grep 零命中）。
- 修复：各循环加 `select { case <-globalStopCh: return; ... }`；`gracefulShutdown`（`server.go:134-170`）补 `close(ledgerReconcileStop)`。风险：低。工作量：S-M。

### P1-6. `ProbeSchedulerLoop` 每 30s O(claims×models×history) — **VERIFIED**
- 当前证据：`contribution_ledger.go:739-773` — 对每个 claim×model 遍历 `cv.crossResults[modelID]` 全部历史（`:754-761`）；`crossResults` 仅 append（`:666`）不驱逐。
- 修复：per-(peer,model) 最近探测时间索引。风险：低。工作量：M。

### P1-7. 5 个 map 无界增长 — **VERIFIED**
- 当前证据：
  - `CapabilityVerifier.crossResults`：`contribution_ledger.go:632`/`:666`，无驱逐
  - `NATManager.probeCache`：`nat_traversal.go:24`/`:217`，无驱逐
  - `ContentHashStore.localCache`：`contribution_ledger.go:820`/`:845`，存全量 JSON，无驱逐
  - `GossipLedger.recs/trusts/claims/penalties/txs`：`contribution_ledger.go:95-99`，无驱逐
  - `DHT.records`：`dht_kademlia.go:172`；`ExpireRecords`（`:216`）定义但全仓无调用
- `runCleanup`（`performance.go:286-329`）只清 routeTable/gossip.seen/metrics/贡献记录/IP 限速器，不清理上述结构。
- 修复：为各结构加 TTL/上限驱逐，`runCleanup` 调 `ExpireRecords`。风险：低。工作量：M。

### P1-8. 多个后台 tick 自建 `http.Client` 不复用连接池 — **VERIFIED**
- 当前证据（裸 `&http.Client{}`）：`stubs.go:98`、`ledger_replication.go:39`、`network.go:1748/1951/1981`、`network_relay.go:444`、`free_pool.go:226`、`audit.go:128`、`tunnel.go:1064`、`platform_discovery.go:432`（与报告清单一致）。
- 修复：统一 `GetSharedHTTPClientWithTimeout(d)`（`performance.go:101`）。风险：低。工作量：S。

### P1-9. `fetchNodePubKey` 同步阻塞在 peer-notify 热路径 — **VERIFIED**
- 当前证据：`network.go:1895` 同步 `fetchNodePubKey`（`:1976-1998`，3s 超时）+ `:1907` 同步 `pingPeerOnce`（`:1949-1959`，3s 超时），均新建裸 client；路由限速 10/min（`routes.go:251`），可被分布式 IP 放大为出站风暴。
- 修复：异步校验 / pubkey 缓存 / 批量 tick。风险：低。工作量：M。

### P2-10. `MultiUserManager` 锁内落盘 — **VERIFIED**
- 当前证据：`multiuser.go:146-273` — `CreateInviteCode`（`:168`）、`UseInviteCode`（`:219`）、`CreateConsumer`（`:266`）等在 `defer m.mu.Unlock()` 内调 `m.save()`（`:130-143`，MkdirAll+MarshalIndent+atomicWrite）；`ValidateAPIKey` 每代理请求取 RLock（`middleware.go:123`）被阻塞。
- 修复：标脏 → `batchSaveLoop`（已有）。风险：低。工作量：S-M。

### P2-11. `RecordConsumerUsage` 解锁-重锁舞蹈 + 内联 sync I/O — **VERIFIED**
- 当前证据：`multiuser.go:293-310` — `count>=10` 时 `Unlock(); save(); Lock()`（`:305-307`）；save 读 map 时无锁（数据竞争）。
- 修复：删除内联 save，全交 `batchSaveLoop`（5s）+ 关机 flush（`multiuser.go:65-89` 已实现）。风险：低。工作量：S。

### P2-12. `handleChatCompletions` 重复枚举 provider — **VERIFIED**
- 当前证据：`handlers.go:773` `pm.OrderedCandidates(...)` → `GetAllRaw` 每次分配 seen map + 全量副本；404 路径再 `pm.AllModels()`（`:789`）。
- 修复：复用候选列表/缓存。风险：低。工作量：S-M。

### P2-13. `adminTimeoutMiddleware` 给所有流量加 60s 超时 — **VERIFIED**
- 当前证据：`server.go:33` `adminTimeoutMiddleware(mux)` 包住整个 mux（含流式 `/v1/*`），60s context cancel；`WriteTimeout` 300s（`server.go:39`）对长流无效。
- 修复：仅对 admin 路由加超时，或流式路径豁免。风险：低-中。工作量：M。

### P2-14. 全局并发信号量无队列上限 — **VERIFIED**
- 当前证据：`performance.go:240-242` `acquireSemaphore()` 无限阻塞（`requestSemaphore <- struct{}{}`）；注释"Returns false if context expires"是**过期注释**（函数无 context 无返回值）。
- 修复：context-aware 超时 → 503 负载脱落。风险：低-中。工作量：M。

### P2-15. 每请求日志写 8 字段 + 同步 slog — **VERIFIED**
- 当前证据：`logger.go:251-260`（method/path/status/latency/consumer/remote/request_id/user_agent）；无缓冲 `io.MultiWriter(os.Stdout, f)`（`logger.go:52`）；slog handler mutex 串行化。
- 修复：异步/环形缓冲，或降级 user_agent。风险：低。工作量：S-M。

### P2-16. `ReconcileAll` 全串行、无 deadline — **VERIFIED**
- 当前证据：`ledger_replication.go:212-232` 串行逐 peer `ReconcileWith`；`BuildManifest` 每 peer 重算。
- 修复：有界并行 + manifest 缓存。风险：中。工作量：M。

### P2-17. `BeginPunch` 每打洞 1 goroutine + session map 无驱逐 — **VERIFIED**（细节修正）
- 当前证据：`nat_punch_loop.go:151-181` 每打洞起 goroutine+ticker；`d.links` 写入（`:155`/`:195`）无驱逐。
- **修正**：`d.sessions` 在打洞完成时自删（`:157`），报告称"session 不驱逐"不成立；`d.links` 确实不驱逐死 peer。
- 修复：打洞并发上限 + links TTL 驱逐。风险：低-中。工作量：M。

### P2-18. `handleLedgerRecord` 线性扫描 tx 切片 — **VERIFIED**
- 当前证据：`ledger_replication.go:377-382` 持 `g.mu.RLock` 线性扫 `g.txs`，忽略已存在的 `txIndex`（`contribution_ledger.go:100`）。
- 修复：改用 txIndex。风险：零。工作量：S。

### P3-19. 导出 marshal 持 RLock — **VERIFIED**
- 当前证据：`ExportContributionsCSV`（`contribution_ledger.go:452-479`）、`ExportLedgerJSON`（`:483+`）、`GetTransparency` 均在 RLock 内 marshal。
- 修复：快照后 marshal。工作量：S。

### P3-20. goroutine-dump 1MB + string 复制 — **VERIFIED**
- 当前证据：`handlers.go:1246-1251` `make([]byte, 1<<20)` + `runtime.Stack(buf,true)`（STW）+ `string(buf[:n])` 复制。
- 修复：`runtime.Stack(buf,false)` 或计数摘要。工作量：S。

### P3-21. `nextID` 无锁自增 — **VERIFIED**
- 当前证据：`contribution_ledger.go:133-136` `g.seq++` 依赖调用者持锁（隐式契约，`RecordContribution` `:142` 先 Lock）。
- 修复：函数内加锁或文档化。工作量：S。

### P3-22. `runCleanup` 强制 `runtime.GC()` — **VERIFIED**
- 当前证据：`performance.go:326` `AllocMB > 150` 时 `runtime.GC()`，每 5min 一次。
- 修复：交给 GOGC，或仅在明显泄漏时触发。工作量：S-M。

### P3-23. `NodeRegistry` 每 peer 一文件、单 mutex 串行 — **VERIFIED**
- 当前证据：`node_registry.go:121-145`/`:217-235` 每 peer `MarshalIndent`+WriteFile+Rename，`r.mu` 串行。
- 修复：批量/异步写。工作量：S。

### P3-24. `parseSTUNResponse` 循环内分配 + stunCh 死通道 + 双 reader — **VERIFIED**
- 当前证据：`nat_traversal.go:335` 每轮 `make([]byte,576)`；`stunCh` 仅 `udpRecvLoop` 写入（`:372`），全仓**无任何读取**（grep 零命中）→ 死通道；`stunQueryOnConn`（`:336` `ReadFromUDP`）与 `udpRecvLoop`（`:357` `ReadFromUDP`）**并发读同一 UDP socket**——注释自称"单 reader"（`:349-352`）与代码矛盾，SetReadDeadline 互相干扰、数据报被两个 reader 抢分。
- 修复：STUN 响应改经 stunCh 由 `stunQueryOnConn` 消费，真正单 reader。风险：中。工作量：M。

### P3-25. `registerWithBootstraps` 每入口起 goroutine 无上限 — **VERIFIED**
- 当前证据：`stubs.go:259-285` `for _, bs := range bootstrapNodes { go func(...){...}(bs) }`。
- 修复：有界并发。工作量：S。

---

## 2. 安全（Security）25 条

### P0-1. Relay-to-self 认证绕过 — **VERIFIED（P0，全链路成立）**
- 当前证据（完整链路）：
  1. 路由：`routes.go:333-338` `/network/{id}/...` 仅 `wafMiddleware`，无 `withAuth`/`withProxyAuth`。
  2. `handleRelayToLocal`（`network_relay.go:187-270`）：`restPath` 完全由攻击者控制（`:229-242` 无 `/v1/*` 白名单）；回环反向代理到 `http://127.0.0.1:{service_port}`（`:249-250`）；Director 仅删 relay hop/from 头（`:260-261`），**不删 X-OMP-KeyType**。
  3. `withProxyAuth` C3 回退：`middleware.go:84-103` — 无 Authorization + `proxyKey==""` + 无消费者 + `isLocalOrPrivateIP(clientIP)` → 匿名 admin。回环重派发使 `RemoteAddr=127.0.0.1` → 通过。
  4. `localOnly`（`middleware.go:196-206`）同样基于 `RemoteAddr` → `/api/forgot-password`、`/api/reset-password`（`routes.go:41-44`）可经 `POST /network/{selfID}/api/forgot-password` 远程触达 → 远程密码重置成立。
  5. guest-key 分支删 Authorization（`network_relay.go:208`）但 default 分支原样透传（`:225-227`）——与报告一致。
- 修复：进程内 dispatch 保留原始 `RemoteAddr` + 内部标记使 `withProxyAuth`/`localOnly` 视为不可信远程；白名单可中继路径前缀（`/v1/`、`/api/network/heartbeat/ping`）；relay 路由加认证。风险：中（改动面大）。工作量：M-L。**最高优先级**。

### P0-2. `X-OMP-KeyType` 从无认证入站被信任 — **VERIFIED（P0，CHANGELOG 与代码矛盾）**
- 当前证据：
  - `provider.go:405-410`：`RequestKeyType` Priority 1 返回入站 `X-OMP-KeyType`（注释仍写旧名 X-MK-KeyType，具误导性）。
  - **全仓无 `Header.Del("X-OMP-KeyType")`**（grep 仅 Set 于 `network_relay.go:140/199/211/216/223`、Get 于 `provider.go:408`）。
  - `relayToRemote` Director（`network_relay.go:364-381`）只删 Authorization，不删 X-OMP-KeyType。
  - `stripInternalQuotaHeaders`（`ledger_quota_consume.go:56-58`）只删 `headerQuotaCharged`。
  - `docs/CHANGELOG.md:397/399` 声称"不再信任客户端 X-MK-KeyType / relayToRemote 已剥离 X-OMP-KeyType"——**与当前代码矛盾**（仅改头名，strip 从未落地）。
- 利用链可达：客户端发 `Authorization: Bearer sk-openmodelpool-...-public-key-v1` + `X-OMP-KeyType: admin` → `withProxyAuth` 接受公开试钥（`middleware.go:77-82`）→ `RequestKeyType` 返回 admin（`provider.go:408`）→ `FilterByAccessControl` 返回全量候选（`provider.go:446-447`），绕过 `ShareToPool` 与 personal 模式 `filterFreePoolOnly`。`RequestKeyType` 在请求路径使用点：`handlers.go:123`、`:690`。
- 修复：最早 middleware 无条件 `r.Header.Del("X-OMP-KeyType")` + 两个 relay Director 重复 strip；key type 从已验证 token 派生，内部值走 context。工作量：S-M。**最高优先级**。

### P1-3. 更新管线 fail-open — **VERIFIED**
- 当前证据：`update.go:672` 校验和**优先从 mirror（usedSource）获取**；`:679-680` 失败仅告警继续；`:692-695` 签名失败仅告警继续。Mirror 列表 `update.go:70-75`（ghfast.top/gh-proxy.com/ghproxy.net/mirror.ghproxy.com）。
- 补充：下载/校验/签名均经 `GetSharedHTTPClient`（`update.go:817/904/932`）→ `internalTransport`（`performance.go:67-76` `InsecureSkipVerify:true`）→ 更新管线**同时跳过 TLS 证书校验**（报告的 `#nosec G402 不成立` 成立）。
- 修复：fail-closed；校验和/签名仅从 canonical GitHub 获取；任一缺失即中止；mirror 只信字节。风险：中。工作量：M。**最高优先级**。

### P1-4. 联邦信任池被无认证 peer notify 投毒 — **VERIFIED**
- 当前证据：`network.go:1895-1898`：
  ```go
  pubKey := fetchNodePubKey(p.Addresses[0])
  if pubKey == "" { pubKey = p.PubKey }  // ← 回退到攻击者 payload 自带 pubkey，签名自指
  ```
- 注释（`:1890-1893`）声称"defeats payload pubkey substitution"，回退逻辑使其失效：攻击者自持密钥对、令 fetch 失败（广告不可达/内网地址）即通过校验。成功后 `netMgr.AddPeer`（`:1925`）→ `bridgePeerToFederation`（`federation.go:300-348`）接入信任池 → 获得 `withFederationAuth`（`federation.go:52-71`）与 `verifyRelayForwardAuth`（`network_relay.go:637-640`）门控的联邦面访问。
- 修复：永不回退 payload pubkey；池外准入要求带外授权（invite/operator 批准）或隔离到不被信任的 localPeers 层。风险：中。工作量：M。**最高优先级**。

### P1-5. 无认证 seed 注册投毒路由表 — **VERIFIED**
- 当前证据：`network_seed.go:181` `if expectedSecret != "" && req.Secret != expectedSecret` — seed_secret 默认空 = 开放注册；`:188` `routeTable.Put(req.NodeID, req.NodeName, req.Addresses)` 接受任意地址。
- 修复：未配置 secret 时 fail-closed；或对 (node_id, addresses, timestamp) 做 ed25519 签名（密钥不从 payload 提供）。风险：中。工作量：M。

### P1-6. `/api/network/consent` 无认证状态突变 — **VERIFIED**
- 当前证据：`routes.go:235` 仅 `rateLimitByIP(5)`；`network.go:1571-1584` `RecordConsent()` 无 withAuth。
- 修复：包 `withAuth`。工作量：S。

### P1-7. WAF IP 控件可经 XFF 伪造绕过 — **VERIFIED**
- 当前证据：`waf.go:153-174` `clientIPs()` 无条件信任 XFF/XRI，无 trusted-proxy 门控（`OMP_TRUSTED_PROXY` 存在于 `admin_auth.go:16`）；`:193` `firstIP(ips)` 取首个 XFF 值。
- 修复：XFF/XRI 解析门控到 trustedReverseProxy，从右到左解析。工作量：S-M。

### P1-8. Guest-key 公网池提升转发 key type — **VERIFIED**（并入 P0-2）
- 当前证据：`network_relay.go:140` 设置 `X-OMP-KeyType: public` 并转发；Director 不 strip（P0-2）。
- 修复 = P0-2；目的地从 token 重派生 key type。工作量：S（随 P0-2）。

### P2-9. CSV 公式注入 — **VERIFIED**
- 当前证据：`contribution_ledger.go:452-479` 直接写 `r.ID/PeerID/ModelID/Provider`，`encoding/csv` 不中和 `=+-@` 前缀；数据源含远程 peer 经无认证 notify 直录的 claims（`network.go:1930-1938`）。
- 修复：`=+-@\t\r` 开头字段前缀单引号。工作量：S。

### P2-10. Goroutine stack dump 泄漏 secrets — **VERIFIED**
- 当前证据：`handlers.go:1245-1252` 返回完整 1MB `runtime.Stack(buf,true)`；路由 `routes.go:59` 仅 withAuth。
- 修复：debug flag 门控（默认 off）+ `runtime.Stack(buf,false)` 或摘要。工作量：S。

### P2-11. `handleRestart` 从可执行目录执行 restart.sh — **VERIFIED**
- 当前证据：`admin_restart.go:28-41` `scriptPath = filepath.Join(filepath.Dir(exePath), "restart.sh")` 无所有权/权限校验即 `exec.Command`。
- 修复：校验 owner + `mode & 0022 == 0`；或用绝对配置路径。工作量：S。

### P2-12. `install.sh` 校验不可用时跳过 — **VERIFIED**
- 当前证据：`scripts/install.sh:156-175` `SHA_URLS` 含第三方 mirror；全部失败 → `warn "校验文件不可用，跳过"` 并继续（root 执行）。
- 修复：fail-closed，仅从 github.com 获取校验。工作量：S。

### P2-13. `/api/network/heartbeat` 认证 fail-open — **VERIFIED**
- 当前证据：`handlers_missing.go:90-92` `else if secret == "" && fed == nil { authed = true }`（注释"best-effort open mesh"）；handler 变更 peer 存活/区域/全局池状态。
- 修复：默认 deny。工作量：S。

### P2-14. 10MB relay 缓冲 + 信号量停滞 — **VERIFIED**
- 当前证据：`network_relay.go:346` `io.ReadAll(io.LimitReader(r.Body, maxGatewayBodySize+1))`（10MB）；`performance.go:240-242` 无限阻塞。
- 修复：context-aware 503；流式 relay；in-flight byte 预算。工作量：M。

### P2-15. `ReadTimeout` 30s 无 `ReadHeaderTimeout` — **VERIFIED**
- 当前证据：`server.go:35-41` 无 ReadHeaderTimeout；WriteTimeout 300s。
- 修复：`ReadHeaderTimeout: 10s`；per-IP 连接上限。工作量：S。

### P2-16. Reset token 比较非 constant-time — **VERIFIED**
- 当前证据：`auth.go:493` `r.Token != tok`；对照 `federation.go:86` 用 `subtle.ConstantTimeCompare`（不一致成立）。
- 修复：`subtle.ConstantTimeCompare`；`network_seed.go:181` 同。工作量：S。

### P2-17. `Access-Control-Allow-Credentials: true` 无条件 — **VERIFIED**
- 当前证据：`middleware.go:39` 无条件设置（含未命中白名单的 origin）。
- 修复：仅 origin 命中时发。工作量：S。

### P2-18. 配置导入批量覆写 provider 无校验 — **VERIFIED**
- 当前证据：`admin_config_io.go:152-163` `pm.providers = make(...)` 从上传文件重建，无 BaseURL/scheme/SSRF 校验（对比 create 路径 `admin_providers.go:132-144` 有 `isLocalOrPrivateIP` 校验）；`proxy_api_key` 直写（`:170-172`）。
- 修复：逐 provider 复用 create 校验；merge 替代 truncate；要求重认证。工作量：M。

### P3-19. `randomString` 熵失败退化为时间戳 — **BY-DESIGN**
- 当前证据：`auth.go:595-605` — 失败时 `slog.Error` + 时间戳回退；注释明确"This is not cryptographically strong but prevents service outage"（m2-fix 有意为之）。
- 判定：行为属实但为**显式记录的可用性取舍**；风险是熵失败时 secret 可预测。建议：secret 生成场景 fail-closed（fatal），非 secret 场景可保留回退。工作量：S。

### P3-20. 加密 key 文件可读经可预测路径；ephemeral 回退 — **FALSE-POSITIVE（夸大/误读）**
- 当前证据：`encryptor.go:125-139` — ephemeral 回退**不是静默**：打 `CRITICAL` 日志并置 `ephemeral:true`；`encryptor.go:70-73` 写 key 失败仅 `Warn` 并继续（key 文件未持久化 → 下次启动旧密文不可恢复，属**可用性**损失）。`Decrypt`（`:98-101`）对无前缀输入原样返回——不存在"静默明文存储 secret"路径。
- 判定：报告将可用性回退夸大为"静默明文存储"，不成立。残余风险（进程重启后数据不可恢复）真实但已被大声记录。建议（可选）：敏感字段持久化失败时 fail-closed。

### P3-21. `isLocalOrPrivateIP` 遗漏网段 — **VERIFIED**
- 当前证据：`middleware.go:209-233` 仅 loopback + 10/8、172.16/12、192.168/16；缺 `169.254.0.0/16`、`100.64.0.0/10`、`fc00::/7`（SSRF 守卫）。
- 修复：补 CIDR。工作量：S。

### P3-22. Provider update 路径丢弃 SSRF 检查 — **VERIFIED**
- 当前证据：`admin_providers.go:298-305` update 仅校验 scheme；create（`:132-144`）校验 `isLocalOrPrivateIP`。
- 修复：提取共享 `validateProviderBaseURL()`。工作量：S。

### P3-23. `handleDirectProbe` 认证任意 URL SSRF — **VERIFIED**
- 当前证据：`nat_traversal.go:277-298` 任意 `TargetURL`（无 scheme/IP 校验）→ `ProbeDirect`（`:183` GET `targetURL+"/api/network/status"`）；路由 `nat_traversal.go:399` `withAuth`（admin-only）。
- 修复：scheme + isLocalOrPrivateIP 校验。工作量：S。

### P3-24. 错误和日志泄漏内部细节 — **VERIFIED**
- 当前证据：`network_relay.go:390`（`relay to %s failed: %v`）、`admin_config_io.go:147`、`handlers.go:1063` 原始 Go error 返回客户端。
- 修复：通用化错误文案。工作量：S。

### P3-25. `escapeJS` 产生无效转义 — **VERIFIED**
- 当前证据：`admin-common.js:20-23` 把 `\n` 映射为 `\` + 换行（JS 行继续）而非 `\n`；受 `sanitizeNodeID` 白名单（`^[a-zA-Z0-9_-]+$`）缓解。
- 修复：`\n`/`\r`/`\t` 用标准转义。工作量：S。

---

## 3. 功能易用性（Usability）16 条

- **P0-1. 重启按钮 Windows 静默无效 — VERIFIED**：`admin_restart.go:19-49` 先返回 `{"success":true}`（`:21`）再异步查 restart.sh；缺失仅 `slog.Error`（`:38`）。
- **P0-2. 退出网络/删除 Provider 无二次确认无影响预览 — BY-DESIGN**：单 operator 管理面板刻意简化；影响预览属 UX 增强非缺陷。
- **P0-3. `authFetch` 吞非 401 — VERIFIED（最危险 UX bug）**：`admin-common.js:87-97` 仅 401 throw；`admin-settings.js:12-16`（saveProxyApiKey）、`:26-29`、`:78`（updateEmail）无条件 toast 成功。
- **P1-4. 登录过期提示未翻译 — VERIFIED**：`admin-common.js:92` `toast('login expired', 'error')`，1.5s 硬跳转。
- **P1-5. 认证失败消息误导 — BY-DESIGN**：`middleware.go:144-149` S-9 注释明示"不暴露内部细节"（防用户枚举）；"无限重试"摩擦真实但与安全意图冲突。建议：保持通用但文案可更可操作（如"API key 无效，请检查后重试"）。
- **P1-6. `toast()` 忽略 duration — VERIFIED**：`admin-common.js:101-108` 恒 3000ms。
- **P1-7. 日志面板失败静默无分页 — VERIFIED**：`admin-logs.js:7-26` `catch(e){console.error}`。
- **P1-8. 日志不显示错误原因 — VERIFIED**：`types.go:452` 有 `Error` 字段，`admin-logs.js:17-21` 表格不渲染。
- **P1-9. 配置导入无备份无 dry-run — VERIFIED**：`admin_config_io.go` 导入流程无自动快照。
- **P1-10. 更新检查失败不可见 — VERIFIED**：`admin-update.js:305-316` catch 保留旧值；`:185` 对 NaN 显示"✅ 已是最新版本"（注释 `:172-173` 为有意保留旧文案，但具误导性）。
- **P1-11. 配置验证缺失 — VERIFIED**：`config.go:236-255` `Set`/`SetMany` 接受任意值无校验；`SetMany` 跳过 `nil/""`（清空静默无效）。
- **P1-12. 部署向导 3 步进度条 + 自动跳转 — VERIFIED**：`setup.html:215-219` 3 步指示对 1 步表单；`:420` 1.5s 自动跳转销毁访问 URL。
- **P2-13. API Key 管理用连续 `prompt()` — VERIFIED**：`admin-provider.html` 11 处、`admin.html` 6 处、`admin-network.js` 1 处；无返回/校验/plaintext 展示。
- **P2-14. 联邦错误泄内部+无下一步 — VERIFIED**：`handlers.go:788-800` 20 个模型 ID + 原始 Go error。
- **P2-15. 半个 admin 界面 `catch{}` 空吞 — VERIFIED**：`admin-settings.js:43`、`:76` 加载失败留白表单。
- **P1-16. SSE 日志通道 cookie-auth 失效 + 断连不重连 — FALSE-POSITIVE**：登录会种 `admin_token` cookie（`admin_auth.go:85-94`，HttpOnly+SameSite=Lax）；`EventSource('/events')` 同源自动携带 cookie → `withAuth` 经 `middleware.go:187` cookie 分支通过；`admin-logs.js:38` 有 5s 重连（`es.onerror → close + setTimeout(connectSSE, 5000)`）。"cookie-auth 失效"与"不重连"均不成立。

---

## 4. PRD / Backlog 完成度 12 条

1. **P3-3(iii) OpenAI `/v1/responses`/`/v1/images`/`/v1/audio` — VERIFIED（未实现，属刻意的低优先级延期）**：全仓零引用；BACKLOG.md:61 保持 `[ ]`，诚实。建议维持现状。
2. **P5-3 CI 门禁 — ALREADY-FIXED**：BACKLOG.md:87-91 已 `[x]`（2026-08-10）；`.github/workflows/ci.yml` `test-unit` 已改为 `go test -race -count=1 -timeout 25m ... ./...`（无 `-short`），注释明确"short 分层不存在"；commit 8d45330（v4.3.32）。报告基于旧代码，误判。
3. **P1-1 DHT — VERIFIED（BACKLOG 诚实性问题，修文档非代码）**：`init.go:106-108` 明示 "DHT (Kademlia) is not yet implemented"；`NewDHTNode` 仅测试调用（`dht_networking_test.go`）；`InMemoryDHTNetwork` 是唯一 transport；存在两套实现（`dht_networking.go` + `dht_kademlia.go`）。BACKLOG.md:28 把 P1-1 标 `[x]` 但正文仅"第一个里程碑"，运输层桥接无独立条目。
4. **P1-2b-2 UDP 打洞不承载数据 — VERIFIED（丢失 backlog 项）**：`network_relay.go:292-293` 直连链路只用于跳过 TCP 探测，数据仍走 HTTP；BACKLOG.md:35 正文注"P2 工作"但**无独立 backlog 项跟踪**。
5. **P2-1 治理批准无效果 — VERIFIED（BACKLOG 诚实性问题）**：`governance.go:186-216` `recompute` 仅置 `Status="ratified"/"rejected"`；`GovTypeAdmitNode/AllowModel/Param`（`:33-35`）声明后无任何执行 hook；payload 只哈希不解析。BACKLOG.md:43 标 `[x]` 的"节点准入/模型白名单多签共治"承诺了执行语义而实现仅为审计账本（"trust-through-audit"哲学可辩护，但与 BACKLOG 措辞不符）。
6. **P2-2 透明度面板 — VERIFIED（BACKLOG 诚实性问题）**：后端端点齐全（`routes.go:61-64`），admin UI **零引用**（grep `.html/.js` 无命中）；BACKLOG.md:47-48 仅完成 (i) 端点、(ii) 面板缺失却标父项 `[x]`。
7. **P5-1 后续子系统未接入 gracefulShutdown — VERIFIED（需改代码）**：`ledgerReconcileStop` 无 close（`ledger_replication.go:238-266`）；`NATManager` 无 `Stop()`；`DirectLinkManager.Stop()`（`nat_punch_loop.go:248`）仅测试调用（`nat_punch_loop_test.go:41`、`nat_punch_relay_test.go:33/92`）。
8. **P0-2 `startRegionSyncLoop` 空转 — VERIFIED（诚实性问题）**：`stubs.go:209-222` sleep-only + TODO；无对应 backlog 项。
9. **FEATURES.md:124 节点身份反向背离 — VERIFIED（修文档）**：`handlers_missing.go:35-44` 返回真实 `PubKeyB64`；`node.go:131-201` 助记词生成、`:206-243` 恢复、`:517/524` NodeID/PubKeyB64；`admin.html:1126-1150` 助记词 UI（词数选择/生成/导出/恢复）。文档低估实现度，最误导。
10. **FEATURES.md P2P 发现表假声明 — VERIFIED（修文档）**：`:141` "Plumtree/Scuttlebutt"、`:142` "mDNS" 全仓零代码命中。
11. **FEATURES.md:183 复制"未来阶段"已过时 — VERIFIED（修文档）**：P1-3 推送复制 + 60s 对账循环已交付（`ledger_replication.go`、`/ledger/__manifest|__sync|__record`）。
12. **未记录特性（正向背离）— VERIFIED（修文档）**：Gemini/Azure 入口（`routes.go:25-28`）、治理端点（`:80-82`）、透明/导出（`:61-64`）、ledger 端点（`:76-78`）、`/network/__punch`、`X-OMP-Quota-Source` 均已上线但 FEATURES.md/API.md 未提及。

---

## 5. VERIFIED 修复清单（按 安全 → 性能 → 易用性 → 文档 排序，供工程师直接执行）

> 编号沿用报告原编号；"依赖"指修复顺序或关联项。

### 安全（6 项，P0 优先）

| ID | 文件 | 修复内容 | 风险 | 工作量 | 依赖 |
|---|---|---|---|---|---|
| SEC-P0-1 | routes.go / network_relay.go / middleware.go | relay-to-self 认证绕过：relay 路由加认证；`handleRelayToLocal` 改进程内 dispatch 保留原始 RemoteAddr + 内部不可信标记；中继路径白名单（/v1/*、heartbeat/ping）；`localOnly` 不信任回环重派发 | 中 | M-L | 无 |
| SEC-P0-2 | middleware.go / provider.go / network_relay.go | X-OMP-KeyType：最早 middleware + 两个 relay Director 无条件 `Header.Del("X-OMP-KeyType")`；key type 从已验证 token 派生走 context；修正 CHANGELOG 假声明 | 低 | S-M | 无（P1-8 随之修复） |
| SEC-P1-3 | update.go | 更新校验 fail-closed：校验和/签名仅从 canonical GitHub 获取；任一缺失/失配中止；mirror 只信字节；更新下载单独走证书校验的 transport | 中 | M | 无 |
| SEC-P1-4 | network.go | peer notify 签名校验永不回退 payload pubkey；池外准入要求带外授权（invite/operator 批准）或隔离 localPeers 层 | 中 | M | 无 |
| SEC-P1-5 | network_seed.go | seed 注册未配 secret 时 fail-closed；或 (node_id,addresses,timestamp) ed25519 签名且密钥不从 payload 提供；`req.Secret` 比较改 constant-time | 中 | M | 无 |
| SEC-P2-18 | admin_config_io.go | 配置导入逐 provider 复用 create 的 scheme+isLocalOrPrivateIP 校验；merge 替代 truncate；要求重新认证 | 中 | M | 无 |

*低危安全项（S 级，可随批次）：SEC-P1-6（consent 加 withAuth）、SEC-P1-7（XFF trusted-proxy 门控）、SEC-P2-9（CSV 公式中和）、SEC-P2-10（goroutine dump 门控）、SEC-P2-11（restart.sh 权限校验）、SEC-P2-12（install.sh fail-closed）、SEC-P2-13（heartbeat 默认 deny）、SEC-P2-15（ReadHeaderTimeout）、SEC-P2-16（constant-time 比较）、SEC-P2-17（CORS 条件化）、SEC-P3-21（补 CIDR）、SEC-P3-22（共享 validateProviderBaseURL）、SEC-P3-23（directProbe URL 校验）、SEC-P3-24（错误文案）、SEC-P3-25（escapeJS 转义）。*

### 性能（4 项，P0/P1 优先）

| ID | 文件 | 修复内容 | 风险 | 工作量 | 依赖 |
|---|---|---|---|---|---|
| PERF-P0-1 | network.go:1247 | `defer nm.mu.RUnlock()`；锁内直接读 `nm.config.NodeID`（去掉 GetNodeID 重入） | 零 | S | 无（生产死锁，最高优先） |
| PERF-P0-2 | network.go / contribution_ledger.go | CheckShareBoundary 改按 (selfID,date) 增量计数器；GetAllTransactions 全量遍历改日期过滤 | 中 | M | PERF-P0-1（同函数） |
| PERF-P0-3 | contribution_ledger.go / network_relay.go | 复制扇出改有界 worker pool / 批量 tick；ReplicateContribution 加 context deadline | 低-中 | M | 无 |
| PERF-P1-5 | 7 个循环 + ledger_replication.go | 各循环加 `globalStopCh` select；gracefulShutdown 补 `close(ledgerReconcileStop)` | 低 | S-M | 无 |

*中危性能项（可随批次）：PERF-P1-4（save 防抖）、PERF-P1-6（probe 索引）、PERF-P1-7（map 驱逐 + ExpireRecords 接线）、PERF-P1-8（统一 GetSharedHTTPClientWithTimeout）、PERF-P1-9（fetchNodePubKey 异步化）、PERF-P2-13（adminTimeout 分流式豁免）、PERF-P2-14（acquireSemaphore 超时 503）、PERF-P3-24（stunCh 消费 + 单 reader）。*

### 易用性（3 项）

| ID | 文件 | 修复内容 | 风险 | 工作量 | 依赖 |
|---|---|---|---|---|---|
| UX-P0-3 | admin-common.js / admin-settings.js | authFetch 对 `!r.ok` throw；所有 success toast 门控 `r.ok` + 响应体 success | 低 | S | 无 |
| UX-P1-4 | admin-common.js:92 | 'login expired' 改中文；跳转前保留表单 | 低 | S | 无 |
| UX-P1-10 | admin-update.js | 更新检查失败显示"检查失败/重试"而非"已是最新版本" | 低 | S | 无 |

### 文档 / Backlog 诚实性（6 项，均为文档改动，工作量 S，风险零）

| ID | 文件 | 修复内容 |
|---|---|---|
| DOC-1 | docs/FEATURES.md:122-131 | 重写节点身份段（当前构建已生成/展示/广播节点身份） |
| DOC-2 | docs/FEATURES.md:141-142 | 删除 Plumtree/Scuttlebutt、mDNS 假声明（改"gossip 变体（内部实现）/ 无 LAN 发现"） |
| DOC-3 | docs/FEATURES.md:183 | 更新为"已交付联邦推送复制 + 60s 对账循环" |
| DOC-4 | docs/BACKLOG.md | P1-1 拆"里程碑已完成 + 运输层桥接待办"；P2-1 措辞改"审计账本（执行 hook 未实现）"或补执行项；P2-2 补面板待办；补"UDP 数据承载协议 P2 项"；补 region-sync 空转项 |
| DOC-5 | docs/FEATURES.md / docs/API.md | 补记 Gemini/Azure 入口、治理端点、透明/导出端点、ledger 端点、/network/__punch、X-OMP-Quota-Source |
| DOC-6 | docs/CHANGELOG.md:397/399 | 修正"已剥离 X-OMP-KeyType"的虚假记录（与 SEC-P0-2 一并处理） |

### 待工程师核实项

1. PERF-P0-2 锁序子项：`ledger.mu→netMgr.mu` 反向顺序的确定路径未找到，建议 grep 锁调用后确认。
2. SEC-P0-1 攻击面广度：`/network/{selfID}/` 对 `/v1/*` 与 `/api/*` 的实际可达面建议用集成测试枚举一次。
3. SEC-P2-14 的 10MB 缓冲对内存峰值的实际影响需压测量化。
