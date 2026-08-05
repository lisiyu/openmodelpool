# V4.3.0 实施计划 — Phase 1 补齐 + 安全/正确性修复

> **目标**: 完成设计文档中 Phase 1 未实现的功能，修复安全/正确性缺陷，发布 V4.3.0
> **基于**: PRD-Phase1、openmodelpool-v4-design.md、代码审查结果

---

## 实施状态总览

| 类别 | 项数 | 状态 |
|------|------|------|
| 安全/正确性缺陷 (S1-S5) | 5 | 待实施 |
| Phase 1 未完成功能 (F1-F8) | 8 | 待实施 |
| Phase 2 功能 (P2.1-P2.9) | 9 | 待实施 |

---

## 一、安全/正确性缺陷修复

- [ ] 1. 修复 IncrProviderConn 竞态条件 — sync.Map.Load+Store 非原子
  - [ ] 1.1 将 `conn_tracker.go` 中 `IncrProviderConn` 的 Load+Store 改为 `sync.Map.CompareAndSwap` 循环或 `atomic.Int64` 方案
    - 当前代码 `conn_tracker.go:12-18` 先 Load 再 Store，并发时计数丢失
    - 改用 `CompareAndSwap` 循环确保原子性
  - [ ] 1.2 同步修复 `IncrGuestConn` 的相同竞态问题
    - `conn_tracker.go:88` 同样的 Load+Store 模式

- [ ] 2. 修复 context.Background() 残留 — 出站请求无超时
  - [ ] 2.1 `update.go` — 为 4 处 `context.Background()` 添加合理超时（30s）
    - 行 259/563/637/792 的 HTTP 请求均无超时
  - [ ] 2.2 `stubs.go:174` — 为 `registerWithBootstraps` 的 HTTP POST 添加 15s 超时
  - [ ] 2.3 `platform_discovery.go:324` — 为平台发现 HTTP GET 添加 15s 超时
  - [ ] 2.4 `network_balance.go:632` — 为 `context.Background()` 添加 30s 超时

- [ ] 3. 修复 handleConsumerRegister 返回 API Key 明文
  - [ ] 3.1 在 `multiuser.go:603` 中将 `writeJSON` 改为只返回安全字段（id, name, message），不返回完整 Consumer 对象
    - API Key 只在创建时返回一次，后续查询不返回
  - [ ] 3.2 为 Consumer 结构体的 APIKey 字段添加 `json:"-"` 标签，防止序列化泄露

- [ ] 4. 修复 vmess.go 临时文件 TOCTOU 竞态
  - [ ] 4.1 重构 `vmess.go:136-143`，去掉 `os.CreateTemp` + `atomicWriteFile` 双写同一文件
    - 改为直接用 `atomicWriteFile` 写入目标文件，或先写临时文件再 rename

- [ ] 5. 修复 hopCount strconv.Atoi 错误忽略
  - [ ] 5.1 在 `network_relay.go:90` 中，当 `strconv.Atoi` 失败时返回 400 Bad Request
    - 当前非数字 hop 头被当 0 处理，应拒绝非法请求

- [ ] 6. 检查点 — 确保所有测试通过，安全缺陷修复完成
  - 确保所有测试通过，如有疑问请询问用户

---

## 二、Phase 1 未完成功能补齐

- [ ] 7. 实现 4 步入网向导（REQ-5/6/12，PRD §6.1-6.4）
  - [ ] 7.1 将 `admin.html` 中 `networkDisclaimerModal` 单步弹窗升级为 4 步向导
    - 步骤 1: 须知说明（公益共享、绝不发币、Key 不上传）
    - 步骤 2: 助记词生成与强制备份（复用现有 BIP39 逻辑）
    - 步骤 3: 共享边界配置（DailyContribCap/ShareIdleOnly/ModelWhitelist）
    - 步骤 4: 完成确认
  - [ ] 7.2 在 `admin-network.js` 中实现 `renderOnboardingWizard` 函数，替代 `confirmNetworkJoin`
    - 每步有"下一步"按钮，未完成当前步骤时禁用
    - 步骤 2 必须完成备份确认才能继续
  - [ ] 7.3 在向导步骤 3 中集成 `loadShareBoundary()` / `saveShareBoundary()` 函数
    - 复用 `quotaAllocation` 区块，扩展边界配置表单

- [ ] 8. 实现闲置额度提示 UI（REQ-13，PRD §P2）
  - [ ] 8.1 在 `admin-network.js` 的 `renderNetworkUI` 中添加闲置额度检测逻辑
    - 调用 `GET /api/network/idle-quota` 获取状态
    - 满足条件且未入网时显示温和 toast/横幅
  - [ ] 8.2 在 `admin.html` 的 `section-note` 区添加横幅 HTML 模板
    - 文案: "你本月还有 X 额度闲置，是否加入共享网络？"
    - 包含"了解/加入"和"暂不"按钮
  - [ ] 8.3 实现 dismiss 逻辑 — 用户点击"暂不"后 localStorage 记录，不再重复提示
    - 同一月份内不重复骚扰

- [ ] 9. 实现贡献积分消费扣减逻辑（REQ-11 完整闭环）
  - [ ] 9.1 在 `relay.go` 的 `RelayToRemote`/`RelayStreamToRemote` 成功后调用 `AppendTransaction("consumption", ...)`
    - 消费时扣减积分：调用他人 Provider 时记录负值交易
  - [ ] 9.2 在 `network.go` 的 `CheckShareBoundary` 中增加消费扣减后的余额检查
    - 积分不足时拒绝消费请求（返回 429）
  - [ ] 9.3 在 `admin-network.js` 的 `loadContributionLedger` 面板中展示"我的贡献"与积分余额
    - 复用 `netCredits` 槽位，展示 contribution - consumption = balance

- [ ] 10. 实现 Provider Key 级精细化额度管控（§8.3）
  - [ ] 10.1 扩展 `APIKey` 结构体，添加 `SharedQuota`/`QuotaType`/`QuotaPeriod`/`ExpiresAt` 字段
    - `QuotaType`: "token"（默认）或 "credit"
    - `QuotaPeriod`: "daily"/"monthly"/"total"
  - [ ] 10.2 在 `admin-network.js` 中为 Provider Key 编辑表单添加精细化额度配置 UI
    - 每个共享 Key 可独立配置额度上限和周期
  - [ ] 10.3 在 `relay.go` 的 `handleRelayRequest` 中添加 Key 级额度检查
    - 共享 Key 的已用额度超过 SharedQuota 时拒绝请求

- [ ] 11. 实现域名绑定后自动注册为 Gateway（§7.10）
  - [ ] 11.1 在 `tunnel.go` 的域名绑定成功回调中，自动设置 `is_gateway = true`
    - 绑定域名后自动成为全路由 Gateway
  - [ ] 11.2 在 `network.go` 中添加 `AutoRegisterGateway` 方法
    - 域名绑定成功 → 调用 `SetGateway(true)` → 刷新路由表

- [ ] 12. 实现申诉机制（§12.8）
  - [ ] 12.1 添加 `POST /api/network/appeal` API 端点
    - 被封节点可提交申诉，附上申诉理由
  - [ ] 12.2 在 `contribution_ledger.go` 中添加 `AppealRecord` 类型和管理方法
    - 申诉记录持久化，通过 Gossip 传播
  - [ ] 12.3 在 `admin-network.js` 中添加申诉提交 UI
    - WAF 封禁详情页中增加"申诉"按钮

- [ ] 13. 检查点 — 确保 Phase 1 功能补齐完成，所有测试通过
  - 确保所有测试通过，如有疑问请询问用户

---

## 三、Phase 2 核心功能实现

- [ ] 14. 实现 Gemini 适配器（§4.1-4.4）
  - [ ] 14.1 在 `platform_adapter.go` 中添加 `GeminiAdapter` 实现 `PlatformAdapter` 接口
    - `contents` + `parts` 嵌套结构转换
    - `user`/`model` 角色映射
    - `candidates[0].content.parts[0].text` 增量拼接
  - [ ] 14.2 在 `provider.go` 的适配器注册中添加 Gemini 类型
  - [ ] 14.3 在 `admin-network.js` 中为 Gemini Provider 添加配置选项

- [ ] 15. 实现 Gossip 五消息协议（§7.8.3）
  - [ ] 15.1 在 `gossip.go` 中定义 `PING`/`PONG`/`GET_PEERS`/`PEERS`/`ANNOUNCE` 五种消息类型
    - 替换当前简化版心跳同步
  - [ ] 15.2 实现 PING 每 30s、GET_PEERS 每 5min、ANNOUNCE 每 10min 的定时调度
  - [ ] 15.3 实现加入网络时的 ANNOUNCE 广播

- [ ] 16. 实现 AddrMan 地址管理器（§7.8.4）
  - [ ] 16.1 创建 `addrman.go`，实现 `AddrMan` 结构体
    - `Known`/`Gateways`/`Seeds` 子集
    - `FailCount`/`UptimeScore`/`LatencyMs` 追踪
  - [ ] 16.2 实现 30 分钟无响应 `fail_count++`、`fail_count>=3` 标记不可达、7 天未见移除
  - [ ] 16.3 实现 `peers.dat` 持久化（加载/保存）
  - [ ] 16.4 替换 `RouteTable` 中的节点管理逻辑，使用 `AddrMan`

- [ ] 17. 实现 NAT 穿透架构（§7.5）
  - [ ] 17.1 创建 `nat_traversal.go`，实现直连探测逻辑
    - 5 秒超时直连探测 → 成功则直连 → 否则 Relay 降级
  - [ ] 17.2 实现 STUN 协议客户端获取公网地址
  - [ ] 17.3 在 `relay.go` 中集成直连优先 → Relay 降级路由逻辑
  - [ ] 17.4 实现 `registerWithBootstraps()` 的实际注册逻辑（替换 `stubs.go:220` 空函数）

- [ ] 18. 实现主动探测与交叉验证（§10.2-10.3）
  - [ ] 18.1 在 `contribution_ledger.go` 的 `CapabilityVerifier.Probe` 中实现真实远程探测
    - 向目标节点发送 1-token 测试请求，验证模型可用性
  - [ ] 18.2 实现探测调度策略：新节点每 5min、常规 30min、高声誉 2h、可疑 1min
  - [ ] 18.3 实现交叉验证：3 节点独立验证，偏差 >20% 触发调查

- [ ] 19. 实现 Ticket 防双花系统（§9.3-9.4）
  - [ ] 19.1 创建 `ticket.go`，定义 `UsageTicket` 和 `TicketFingerprint` 类型
    - 双方签名、请求 ID、时间戳、金额
  - [ ] 19.2 实现双向签名流程：请求方与资源方各自签名
  - [ ] 19.3 实现批量公证：每小时上报 seed1
  - [ ] 19.4 实现防共谋三层机制：上游响应指纹、随机抽样验证、统计异常检测

- [ ] 20. 实现 IPFS 真实持久化（§9.8）
  - [ ] 20.1 在 `contribution_ledger.go` 中将 `IPFSClient.StoreJSON` 从 stub 改为真实 IPFS 上传
    - 使用 HTTP API `/api/v0/add` 上传到公共网关
  - [ ] 20.2 实现贡献记录的 IPFS 存储和 CID 引用
  - [ ] 20.3 实现争议解决存证的 IPFS 上传

- [ ] 21. 实现 DNS Manager 自动化（§7.12）
  - [ ] 21.1 创建 `dns_manager.go`，实现从 Gateway 列表自动更新 DNS 记录
  - [ ] 21.2 支持 Cloudflare DNS API 更新 A/TXT 记录
  - [ ] 21.3 添加 `GET /api/dns/status` 和 `POST /api/dns/sync` API 端点

- [ ] 22. 实现 Cloudflare API Token 一键域名绑定（§13.4）
  - [ ] 22.1 在 `tunnel.go` 中添加 API Token 方式创建命名隧道
  - [ ] 22.2 添加 `POST /api/tunnel/token`、`POST /api/tunnel/create`、`POST /api/tunnel/start`、`POST /api/tunnel/stop` 端点
  - [ ] 22.3 在 `admin-network.js` 中添加 Cloudflare Token 配置 UI

- [ ] 23. 检查点 — 确保 Phase 2 功能完成，所有测试通过
  - 确保所有测试通过，如有疑问请询问用户

---

## 四、收尾与发布

- [ ] 24. 更新 CHANGELOG.md 和版本号
  - [ ] 24.1 将 `AppVersion` 更新为 `4.3.0`
  - [ ] 24.2 更新 `CHANGELOG.md` 记录所有变更

- [ ] 25. 合并到 main 分支并打 tag
  - [ ] 25.1 创建 `v4.3.0` git tag
  - [ ] 25.2 推送到 origin
