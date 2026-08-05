# V4.4.0 实施计划 — 性能优化

> **目标**: 全面优化性能，包括热路径优化、代码质量改进、中低优先级性能问题
> **基于**: 豆包代码审查 + 性能分析结果
> **前提**: V4.3.0 已发布

---

## 实施状态总览

| 类别 | 项数 | 状态 |
|------|------|------|
| P1 性能热路径优化 | 5 | 待实施 |
| P2 代码质量/健壮性 | 8 | 待实施 |
| P3 性能中低优先级 | 9 | 待实施 |

---

## 一、P1 性能热路径优化

- [ ] 1. 缓存 isLocalOrPrivateIP 的 CIDR 解析结果
  - [ ] 1.1 将 `middleware.go:215-238` 中三个 `net.ParseCIDR` 调用提取为包级变量
    - `privateIPNets` 在 `init()` 中一次性解析，后续直接用 `Contains()` 判断
    - 消除每个未认证请求的 3 次 CIDR 字符串解析

- [ ] 2. 为 ProviderManager.Enabled() 添加缓存
  - [ ] 2.1 在 `provider.go` 中添加 `cachedEnabled []Provider` + `enabledCacheValid bool`
    - 与 `cachedAll` 共享同一失效机制
    - `Enabled()` 优先返回缓存，缓存失效时重新遍历
  - [ ] 2.2 在 `SetProvider`/`UpdateProvider`/`DeleteProvider` 等写操作中标记 `enabledCacheValid = false`

- [ ] 3. 缓存 corsMiddleware 的 allowedOrigins 解析结果
  - [ ] 3.1 在 `middleware.go` 中添加 `cachedAllowedOrigins []string` + `originsCacheValid bool`
    - 仅在配置变更时（`SIGHUP`/`Set()`）失效重解析
  - [ ] 3.2 替换当前每请求的 `cfg.Get("cors_allowed_origins")` + `strings.Split` 逻辑

- [ ] 4. 移除 performance.go 中的 runtime.GC() 调用
  - [ ] 4.1 删除 `performance.go:324-329` 中 `runtime.GC()` 调用
    - 改用 `runtime/debug.SetMemoryLimit` + `GOGC` 调优，让 Go 运行时自行管理 GC
  - [ ] 4.2 添加 `debug.SetMemoryLimit(300 << 20)` 在 `initPerformance()` 中设置内存上限

- [ ] 5. 优化 Config.Get 的字符串分配
  - [ ] 5.1 为高频读取的配置项（`routing_mode`, `cors_allowed_origins` 等）添加热缓存
    - `cfg.hotCache map[string]cacheEntry`，仅在 `load()`/`Set()` 时失效
  - [ ] 5.2 扩展 `envMap` 为完整 key→env 映射，避免 fallback 到 `strings.ToUpper` + `os.Getenv`

- [ ] 6. 检查点 — 确保 P1 性能优化完成，所有测试通过
  - 确保所有测试通过，如有疑问请询问用户

---

## 二、P2 代码质量/健壮性

- [ ] 7. 修复 json.Marshal 错误忽略
  - [ ] 7.1 在 `client.go` 中约 15 处 `json.Marshal` 调用后添加错误检查
    - 至少 `slog.Warn` 记录，不吞错误
  - [ ] 7.2 在 `eventbus.go` 2 处、`genesis.go` 1 处同样添加错误检查

- [ ] 8. 修复 io.ReadAll 错误忽略
  - [ ] 8.1 在 `client.go` 约 15 处、`gossip.go` 1 处添加错误检查
    - `io.ReadAll` 失败时返回 502/500，不吞错误

- [ ] 9. 修复 readJSON 返回值被丢弃
  - [ ] 9.1 在 `handlers.go:969` 将 `_ = readJSON(w, r, &body)` 改为检查错误
    - 错误时返回 400 Bad Request

- [ ] 10. 替换 config.go/node_registry.go 中的 os.WriteFile
  - [ ] 10.1 确认 `config.go:235` 和 `node_registry.go:212` 是否已使用 atomicWriteFile 模式
    - 如果是先写临时文件再 rename 则已安全
    - 如果直接 `os.WriteFile` 目标文件则替换为 `atomicWriteFile`

- [ ] 11. 统一 connTrackerStopCh 到 globalStopCh
  - [ ] 11.1 将 `conn_tracker.go:48` 的 `connTrackerStopCh` 替换为 `globalStopCh`
    - 删除 `stopConnTracker()` 独立关闭函数
    - 在 `gracefulShutdown` 中统一关闭

- [ ] 12. 修复 browser_login.go 中 chromedp.Run 错误忽略
  - [ ] 12.1 在约 12 处 `chromedp.Run` 调用后添加错误检查
    - 至少 `slog.Warn` 记录，方便排查浏览器自动化问题

- [ ] 13. 清理 encryptor.go 无用代码
  - [ ] 13.1 删除 `encryptor.go:188` 的 `var _ = filepath.Join`
    - 无用代码残留，直接删除

- [ ] 14. 确认 InsecureSkipVerify 用途
  - [ ] 14.1 确认 `performance.go:75` 的 `InsecureSkipVerify: true` 是否仅用于内部自签证书
    - 如果是，添加注释说明用途
    - 如果不是，考虑配置自定义 CA 替代

- [ ] 15. 检查点 — 确保 P2 代码质量修复完成，所有测试通过
  - 确保所有测试通过，如有疑问请询问用户

---

## 三、P3 性能中低优先级

- [ ] 16. 优化 jsonBody 的 buffer 复用
  - [ ] 16.1 在 `client.go:1293-1296` 中使用 `jsonEncodePool` 或 `bufPool` 替代每次 `json.Marshal` + `bytes.NewReader`
    - 在 openaiNonStream/openaiStream/anthropicNonStream/anthropicStream 等高频路径使用池化 buffer

- [ ] 17. 优化 Tracker.ProviderStats() 内存峰值
  - [ ] 17.1 改为只读取时间窗口内的记录，反向遍历到 cutoff 即停止
    - 避免全量 `copy(t.records)` 快照
  - [ ] 17.2 考虑使用分桶/滚动窗口替代全量遍历

- [ ] 18. 优化 HealthChecker.checkAll 的 HTTP 开销
  - [ ] 18.1 采样检查：每次只检查部分 key（如随机 2-3 个）
  - [ ] 18.2 缓存健康状态：连续健康的 key 降低检查频率
  - [ ] 18.3 使用轻量级端点（如 `GET /models`）替代 `POST /chat/completions` 做连通性检测

- [ ] 19. 优化 writeJSON 安全 Header 设置
  - [ ] 19.1 将 6 个安全 Header 移到中间件中一次性设置
    - `X-Content-Type-Options`/`X-Frame-Options`/`Cache-Control`/`Content-Security-Policy`/`Referrer-Policy`/`Permissions-Policy`
  - [ ] 19.2 从 `writeJSON` 中移除重复的 Header 设置

- [ ] 20. 优化 reqLog 环形缓冲
  - [ ] 20.1 在 `tracker.go:212-219` 中将切片模拟替换为真正的环形缓冲区
    - 写入时只移动指针，消除 O(n) 拷贝
  - [ ] 20.2 读取时从指针位置开始遍历

- [ ] 21. 优化 Coze 轮询策略
  - [ ] 21.1 在 `client.go:899-918` 中将固定 1s 间隔改为指数退避
    - 500ms → 1s → 2s → 4s，上限 10s

- [ ] 22. 优化 Gossip 增量同步
  - [ ] 22.1 在 `gossip.go:91-102` 中实现增量同步
    - 只发送上次 gossip 后的新记录，非全量
    - 或发送摘要/哈希，对方需要时才传输完整数据

- [ ] 23. 优化 cryptoShuffle 内存分配
  - [ ] 23.1 在 `gossip.go:729-741` 中将 `make([]byte, 8)` 移到循环外
    - 循环内复用同一 buffer

- [ ] 24. 合并重复 Transport 定义
  - [ ] 24.1 统一 `client.go:27-33` 和 `performance.go:67-76` 的 Transport 配置
    - 通过 `TLSClientConfig` 动态配置，避免两套独立连接池

- [ ] 25. 检查点 — 确保所有性能优化完成，所有测试通过
  - 确保所有测试通过，如有疑问请询问用户

---

## 四、收尾与发布

- [ ] 26. 更新版本号和 CHANGELOG
  - [ ] 26.1 将 `AppVersion` 更新为 `4.4.0`
  - [ ] 26.2 更新 `CHANGELOG.md` 记录所有变更

- [ ] 27. 合并到 main 分支并打 tag
  - [ ] 27.1 创建 `v4.4.0` git tag
  - [ ] 27.2 推送到 origin
