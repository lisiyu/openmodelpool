# OpenModelPool 演进 Backlog（公益优先）

> 铁律：纯公益、不考虑商业模式。任何"积分/代币/抽成"类设计禁止。
> 工作流（2026-08-08 起）：**小步快跑、不限制每日项数**；每完成一批改动必须 `go build ./...` + `go test ./...` 全绿；文档随代码同步修订，杜绝"文档与代码背离"。
> **每晚 ~22:00 由雷工验收确认；确认 OK 后由雷工手动 push 向 GitHub 并打 tag 触发发布（代理不自动 push、不打 tag）。**

## Phase 0 — 诚信地基（✅ 已完成 2026-08-08）

- [x] P0-1 心跳循环修复：`startHeartbeatLoop` 补 `for{select{}}`（stubs.go:52）
- [x] P0-2 区域同步循环修复：`startRegionSyncLoop` 补 `for{select{}}`（stubs.go:213）
- [x] P0-3 贡献账本诚实化：`IPFSClient`→`ContentHashStore`，去掉伪 IPFS（"Qm"前缀/网关列表），改 `sha256:` 前缀、`StorageLocation=local-hash`（contribution_ledger.go）
- [x] P0-4 治理层死代码清理：删除全仓无调用者的 `governance.go`（GovernanceManager / initGovernanceManager / MultiSigThreshold / NetworkGovernanceEvent 等纯死代码），并移除 globals_network.go 中孤立的 `var governanceMgr *GovernanceManager` 声明
- [x] P0-5 文档对齐：README/design/PRD 中"去中心化/IPFS/分布式治理"等未兑现承诺加"未实现/预留"标注；删除死代码 `ipfs_ledger.go`
- [x] P0-6 版本常量对齐：`AppVersion` 4.3.16 → 4.3.20（对齐 git 发布版本，修复常量漂移）
- [x] P0-7 WAF 文档纠错：核定 `waf_enabled` 默认 `true` 且 `wafMiddleware` 已包裹 `/v1/*` 与 relay；修正 README 中"not yet wired / enabled:false"的错误说法，并修掉 waf.go 中自相矛盾的注释
- [x] P0-8 仓库垃圾清理：删除残留构建/临时/运行时产物（`openmodelpool.exe.bak`、`_wtest2`、`.tmp*`、`.check_globals.js`、`omp.{out,err}.log`、`data/audit/audit.log`、`data/access.log` 等）
- [x] P0-9 配置写盘可中断化（**同时修复生产关机响应性 + 测试套件卡死**）：`config.go` 的 `debounceWriter` 原先在 `dirtyCh` 分支里 `time.Sleep(3s)` 期间完全不响应 `stopCh`，导致关机需空等一个完整防抖窗口、且 `go test ./...` 因每个用例的 cleanup 都要等它退出而必然超时。改为 `configDebounceWindow` 常量 + `time.NewTimer` + `select{timer.C / stopCh}`，等待可被打断（生产语义不变，仍 3s 合并写）
- [x] P0-10 测试 goroutine 泄漏修复：`setupTestEnv` cleanup 补上停止 `multiUser.batchSaveLoop`（`saveStopCh`），消除每用例泄漏一个 goroutine
- [x] P0-11 测试断言去脆化：`TestQAFrontendWiring` 原先硬编码 `/admin-update.js?v=345`，资源版本升到 346 后即失败；改为正则 `\?v=\d+` 断言，不再随缓存版本号漂移而误报
- [x] P0-12 测试防抖窗口可注入：新增 `configDebounceOverride` + `main_test.go` 的 `TestMain`（测试内窗口 5ms）。此前数十个用例整齐卡在 ~3.0-3.2s（正好等于生产防抖窗口），累计拖垮整个套件；生产仍为 3s 合并写，语义不变
- [x] P0-13 节点注册表并发写修复：`node_registry.go` 的 `writeLocked` 名不副实——注释声称"在互斥锁下原子写入"，实际只在计算路径时持锁，**写文件与 rename 完全无保护**；且 `LoadAll` 未加锁扫描目录，与 rename 撞车（Windows 上表现为 `cannot find the path specified` 报错刷屏）。现让锁真正覆盖 write+rename，`LoadAll` 同锁读取
- [x] P0-14 并发用例规模降载：`TestNodeRegistry_ConcurrentSaveAndLoad` 并发度 64 → 16。经 goroutine dump 定位，卡死发生在 Go 自身的 `t.TempDir()` 清理（`syscall.RemoveDirectory` 阻塞 3 分钟）——Windows 实时杀毒扫描持有刚写入文件的句柄所致，**与项目代码无关**；16 并发同样能验证锁正确性，不再放大宿主文件系统问题

> Phase 0 验证基线（2026-08-08）：`go build ./...` EXIT=0；`go vet ./...` 全绿；`go test ./...` 全量通过 EXIT=0，耗时约 140s、**零超时**（修复前 300s 跑不完且报错用例随机漂移）。

## Phase 1 — 真实去中心化（公益抗审查 / 永续）

- [x] **P1-1 DHT 网络 I/O（单进程多节点验证）**：为 `dht_kademlia.go` 的惰性 K-Bucket 表补上真正的 Kademlia RPC 层（新增 `dht_networking.go`）。实现 `DHTMessage`（PING/PONG、FIND_NODE、FIND_VALUE、STORE）+ `DHTTransport` 可插拔传输 + `DHTNode`（含迭代查找算法）+ `InMemoryDHTNetwork`（单进程内多节点路由，作验证台）。`dht_networking_test.go` 四个用例证明：Ping/Pong、A 仅知 B 却经 B 学到 C 的**多跳发现**、值经 relay 存储并被 D 跨跳取回、缺失键返回未找到。这是 P1-1 的第一个里程碑（先单进程多节点验证）；后续再桥接真实 UDP/QUIC/HTTP 传输。**未改动现有 federation/gossip 代码路径**（加法式、零耦合）。
- [x] P1-2a STUN 响应解析抽离为纯函数 `parseSTUNResponse` + NAT 类型判定 `classifyNAT`（full_cone/symmetric，RFC 5780 轻量探测：对比两个 STUN 服务器返回的公网端点是否一致）+ `nat_traversal_test.go` 单测（此前解析内联、零测试、natType 恒为 "unknown"）。`discoverPublicAddr` 改为轮询全部 STUN 服务器后再判定
- [x] P1-2b-1 NAT 类型驱动路由：`NATManager.PreferRelay()` 在 `symmetric` NAT 下强制走中继、跳过后台直连探测（`network_relay.go` 已接线，无行为回退风险），`nat_traversal_test.go` 新增 `TestPreferRelay` 表驱动验证
- [ ] P1-2b-2 真实 UDP 打洞 / 直连通道建立（cone NAT 尝试直连：经 relay/gossip 交换 reflexive 地址 + 并发打洞 + 直连通道旁路 relay）
- [ ] P1-3 贡献账本多节点冗余：联邦内 N 个节点各存一份 + 哈希校验（替代真 IPFS 的短期方案）

## Phase 2 — 贡献透明与社区共治（公益信任）

- [ ] P2-1 社区共治设计：如需节点准入 / 模型白名单多签共治，从零设计治理层（原 GovernanceManager 死代码已于 2026-08-08 清理删除，不保留半成品骨架）
- [ ] P2-2 贡献账本可视化：admin 页面展示"算力从哪来、到哪去"透明度面板
- [ ] P2-3 公益额度（非代币）：贡献 → 免费配额兑换，杜绝抽成 / 积分经济

## Phase 3 — 普惠低门槛

- [ ] P3-1 一键部署脚本 / 容器化（降低个人运行门槛）
- [ ] P3-2 免费池开箱即用：默认配置即可零成本接入

## Phase 4 — 教育科研

- [ ] P4-1 贡献数据开放下载（CSV/JSON），供研究者使用
- [ ] P4-2 架构与公益理念文档（中英文）

## Promotion（稳定后）

- [ ] 准备推广物料包：README 润色、公益宣言、发布稿草稿、GitHub topics（写入 docs/LAUNCH-KIT.md 并通知用户审核）
