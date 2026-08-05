# V4.5.0 实施计划 — 安全风险全面修复

> **目标**: 全面检查并修复安全风险，发布 V4.5.0
> **基于**: 豆包代码审查安全项 + 全代码安全扫描
> **前提**: V4.4.0 已发布

---

## 实施状态总览

| 类别 | 项数 | 状态 |
|------|------|------|
| 认证与授权安全 | 5 | 待实施 |
| 输入验证与注入防护 | 5 | 待实施 |
| 加密与密钥管理 | 4 | 待实施 |
| 网络与传输安全 | 4 | 待实施 |
| 数据保护与隐私 | 3 | 待实施 |

---

## 一、认证与授权安全

- [ ] 1. 全面审计所有 API 端点的认证覆盖
  - [ ] 1.1 检查 `routes.go` 中所有端点是否已用 `withAuth`/`withProxyAuth`/`withFederationAuth` 包裹
    - 任何未保护的端点都需添加认证中间件
  - [ ] 1.2 验证 `withAuth` 中间件的 JWT 校验逻辑无绕过
    - 检查 `none` 算法攻击是否已防护
    - 确认 Token 过期检查严格

- [ ] 2. 审计权限提升风险
  - [ ] 2.1 检查 Consumer 角色是否能访问 Admin-only 端点
    - `withConsumerOrAdminAuth` 的权限边界是否正确
  - [ ] 2.2 检查 Guest Key 持有者是否能越权访问其他 Key 的资源

- [ ] 3. 审计 Federation 认证机制
  - [ ] 3.1 验证 `withFederationAuth` 的签名验证逻辑
    - 签名时间戳是否检查（防重放）
    - 公钥是否硬编码或可动态更新
  - [ ] 3.2 检查 peer notify 端点的签名验证是否完整

- [ ] 4. 检查密码重置流程安全性
  - [ ] 4.1 验证 `handleResetPassword`/`handleVerifyResetToken`/`handleResetWithCode` 的 Token 安全性
    - Token 是否有时效性
    - 是否防暴力破解

- [ ] 5. 审计 Setup 端点竞态条件
  - [ ] 5.1 确认 `handleSetup` 已使用 `sync.Mutex` 替代 `sync.Once`（已知修复）
  - [ ] 5.2 检查是否仍有竞态窗口允许未认证访问

---

## 二、输入验证与注入防护

- [ ] 6. 全面审计 SQL/命令注入风险
  - [ ] 6.1 检查所有 `exec.Command` 调用是否使用参数化方式（非字符串拼接）
    - 特别是 `restart.sh` 调用路径
  - [ ] 6.2 检查所有 JSON 解析后的字符串是否在使用前做了长度/字符校验

- [ ] 7. 审计 VMess 配置注入
  - [ ] 7.1 确认 `vmess.go` 中用户输入是否在写入 xray 配置前做了转义/校验
  - [ ] 7.2 确认临时文件权限为 0600（已知修复，需确认）

- [ ] 8. 审计 SSRF 风险
  - [ ] 8.1 检查 `proxy_url`/`vmess_url` 配置项是否允许内网地址
    - `isPrivateHost` 的 DNS fail-open 行为是否可接受
  - [ ] 8.2 检查平台发现 `platform_discovery.go` 的 URL 白名单机制

- [ ] 9. 审计路径穿越风险
  - [ ] 9.1 检查所有文件操作（`os.Open`/`os.ReadFile`/`http.ServeFile`）的路径参数
    - 是否对 `..` 做了检查
  - [ ] 9.2 检查 `/proc/pid` 端点的 pid 参数是否限制为数字

- [ ] 10. 审计 Content-Type 与请求体验证
  - [ ] 10.1 确认 `readJSON` 的 `MaxBytesReader` 限制是否足够严格（当前 1MB）
  - [ ] 10.2 检查是否有端点绕过 `readJSON` 直接读取请求体

---

## 三、加密与密钥管理

- [ ] 11. 审计传输加密实现
  - [ ] 11.1 检查 `transport_encryption.go` 的 X25519 ECDH + HKDF-SHA256 实现
    - 密钥派生是否使用 salt
    - 是否有前向保密
  - [ ] 11.2 检查 AES-256-GCM 的 nonce 使用是否防重放

- [ ] 12. 审计密钥存储安全
  - [ ] 12.1 检查 Provider API Key 在磁盘上的存储方式
    - 是否加密存储（`Encryptor` 的 `IsReady` 检查）
    - 文件权限是否为 0600
  - [ ] 12.2 检查 JWT 签名密钥的轮换机制

- [ ] 13. 审计助记词处理安全
  - [ ] 13.1 确认助记词在内存中的生命周期是否最小化
    - 使用后是否及时清零（`memzero`）
  - [ ] 13.2 确认助记词绝不通过 HTTP 传输或写入日志

- [ ] 14. 审计随机数生成安全
  - [ ] 14.1 检查所有 `crypto/rand` vs `math/rand` 的使用是否正确
    - 安全场景必须使用 `crypto/rand`
  - [ ] 14.2 确认 `randomString()` 在 `rand.Read` 失败时 panic（已知修复）

---

## 四、网络与传输安全

- [ ] 15. 审计 TLS 配置
  - [ ] 15.1 确认所有 HTTPS 端点最低 TLS 1.2（已知修复，需确认）
  - [ ] 15.2 检查 `InsecureSkipVerify` 的使用范围
    - 是否仅在内部自签证书场景使用
    - 是否有替代方案（自定义 CA）

- [ ] 16. 审计 CORS 配置
  - [ ] 16.1 确认 `cors_allowed_origins` 不包含 `*` 通配符
  - [ ] 16.2 检查 `corsMiddleware` 的 `Access-Control-Allow-Credentials` 设置

- [ ] 17. 审计 WAF 规则
  - [ ] 17.1 确认 WAF 默认启用（已知修复，需确认）
  - [ ] 17.2 检查 WAF 攻击模式检测的完整性
    - SQL 注入/XSS/路径穿越/命令注入模式是否覆盖

- [ ] 18. 审计速率限制
  - [ ] 18.1 检查所有未认证端点是否有速率限制
  - [ ] 18.2 检查速率限制器的内存是否有限制（防 OOM）
    - `ipLimiters` 的无界 map 是否已修复

---

## 五、数据保护与隐私

- [ ] 19. 审计日志泄露
  - [ ] 19.1 检查所有 `slog` 调用是否泄露敏感信息（API Key、Token、助记词）
  - [ ] 19.2 检查错误响应是否泄露内部信息（堆栈、文件路径、数据库结构）

- [ ] 20. 审计响应数据泄露
  - [ ] 20.1 检查所有 API 响应是否返回了不必要的内部字段
    - 特别是 `handleConsumerRegister` 的 API Key 泄露（V4.3.0 已修复，需确认）
  - [ ] 20.2 检查 `/api/status`/`/api/config` 等端点是否暴露了敏感配置

- [ ] 21. 审计审计日志完整性
  - [ ] 21.1 确认 `audit.go` 的日志不可被篡改
  - [ ] 21.2 检查审计日志的访问控制

---

## 六、收尾与发布

- [ ] 22. 安全测试与验证
  - [ ] 22.1 运行 `go vet` / `staticcheck` / `gosec` 静态分析
  - [ ] 22.2 确认所有安全修复通过测试

- [ ] 23. 更新版本号和 CHANGELOG
  - [ ] 23.1 将 `AppVersion` 更新为 `4.5.0`
  - [ ] 23.2 更新 `CHANGELOG.md` 记录所有安全修复

- [ ] 24. 合并到 main 分支并打 tag
  - [ ] 24.1 创建 `v4.5.0` git tag
  - [ ] 24.2 推送到 origin
