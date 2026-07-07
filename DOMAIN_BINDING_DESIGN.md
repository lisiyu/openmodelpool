# ModelMux 一键域名绑定功能设计

## 功能目标
在 Web 管理面板实现一键绑定固定域名，替代手动配置 Cloudflare Tunnel。

## 技术方案

### 方案选择：Cloudflare API Token 方式

**为什么不用 OAuth？**
- OAuth 需要浏览器交互，远程服务器无法直接完成
- Device Code Flow 用户体验差
- API Token 方式更安全、可控

**API Token 优势：**
- 用户可以在 Cloudflare Dashboard 精确控制权限
- Token 可以撤销，安全性高
- 支持完整的隧道管理 API
- 无需浏览器交互

### 实现步骤

#### 1. 后端 API 设计

**存储 Cloudflare API Token**
```
POST /api/tunnel/token
Body: { "token": "xxxxx" }
```
- 加密存储到 config.json（使用现有的 encryptor）
- Token 权限要求：Cloudflare Tunnel DNS Edit

**创建命名隧道**
```
POST /api/tunnel/create
Body: { "name": "modelmux", "domain": "zuiniu.com" }
```
- 调用 Cloudflare API 创建隧道
- 返回 tunnel_id
- 自动配置 DNS 路由

**查询隧道状态**
```
GET /api/tunnel/status
```
- 返回当前隧道信息：name, domain, tunnel_id, url, status

**启动/停止隧道**
```
POST /api/tunnel/start
POST /api/tunnel/stop
```

#### 2. 前端 UI 设计

**位置**：admin.html → 配置管理卡片 → 公网访问区域

**UI 流程**：
1. **未绑定状态**：
   - 显示"绑定域名"按钮
   - 点击弹出对话框：
     - 输入 Cloudflare API Token（带说明链接）
     - 输入想要的域名（如 zuiniu.com）
     - "一键绑定"按钮
   
2. **绑定中状态**：
   - 显示进度：创建隧道 → 配置 DNS → 验证
   - 禁用按钮

3. **已绑定状态**：
   - 显示当前域名：zuiniu.com ✅
   - 显示隧道状态：运行中 / 已停止
   - 显示公网地址：https://zuiniu.com
   - "解绑"按钮

#### 3. 后端实现

**tunnel.go 扩展**：
```go
type TunnelManager struct {
    // 现有字段
    mu       sync.Mutex
    cmd      *exec.Cmd
    url      string
    running  bool
    
    // 新增字段
    apiToken    string  // Cloudflare API Token
    tunnelID    string  // 命名隧道 ID
    customDomain string // 自定义域名
    mode        string  // "quick" | "named"
}

// 新方法
func (t *TunnelManager) CreateNamedTunnel(name, domain string) error
func (t *TunnelManager) ConfigureDNS(domain string) error
func (t *TunnelManager) StartNamedTunnel() error
func (t *TunnelManager) StopNamedTunnel() error
```

**Cloudflare API 调用**：
- 创建隧道：POST /accounts/{account_id}/cfd_tunnel
- 配置 DNS：PUT /zones/{zone_id}/dns_records
- 获取账户信息：GET /accounts
- 获取域名列表：GET /zones

#### 4. 安全考虑

**Token 存储**：
- 使用现有 encryptor 加密
- 存储在 config.json 的 `cloudflare_api_token_encrypted` 字段
- 不在日志中输出 Token

**权限最小化**：
- 引导用户创建只包含必要权限的 Token：
  - Account: Cloudflare Tunnel: Edit
  - Zone: DNS: Edit

#### 5. 错误处理

**常见错误**：
- Token 无效：提示用户检查权限
- 域名已被其他账户占用：提示用户选择其他域名
- DNS 配置失败：提示用户检查域名是否在 Cloudflare 管理
- 隧道名称冲突：自动添加随机后缀

#### 6. 用户体验优化

**引导说明**：
- 提供图文教程：如何生成 Cloudflare API Token
- 视频演示（可选）
- 常见问题 FAQ

**实时反馈**：
- 使用 WebSocket 推送配置进度
- 显示详细的错误信息和解决建议

**回滚机制**：
- 绑定失败时自动清理已创建的资源
- 提供"重试"按钮

## 实现优先级

### Phase 1: 基础功能（MVP）
- [ ] 存储 API Token
- [ ] 创建隧道 + 配置 DNS
- [ ] 启动命名隧道
- [ ] 前端 UI：绑定对话框

### Phase 2: 增强体验
- [ ] 隧道状态监控
- [ ] 解绑功能
- [ ] 域名验证
- [ ] 错误提示优化

### Phase 3: 高级功能
- [ ] 多域名支持
- [ ] 隧道健康检查
- [ ] 自动重连
- [ ] 隧道统计信息

## 开发时间估算

- Phase 1: 2-3 小时
- Phase 2: 1-2 小时
- Phase 3: 2-3 小时

## 替代方案

如果用户不想使用 Cloudflare API Token，可以：
1. 手动在 Cloudflare Dashboard 创建隧道
2. 复制 Tunnel Token（不是 API Token）
3. 在管理面板输入 Tunnel Token
4. 后端使用 `cloudflared tunnel run --token <TOKEN>` 启动

这个方案更简单，但需要用户手动创建隧道。

## 结论

推荐使用 **API Token 方案**，用户体验最好，真正的一键绑定。
备选 **Tunnel Token 方案**，实现更简单，但需要用户手动创建隧道。

可以两个方案都支持，让用户选择。
