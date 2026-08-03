# 开发者指南

## 项目目的

OpenModelPool 是一个去中心化 AI 模型共享池，使节点能够在对等网络中汇聚、路由和共享 LLM 计算资源。它在更大系统中担任 API 网关和联邦协调器的角色。

**核心职责**:
- 兼容 OpenAI/Anthropic API 格式的代理网关
- 多 Provider 路由（优先级/成本/延迟/自动）
- 联邦信任池和去中心化中继
- 四层 WAF 防护和速率限制
- Ed25519 身份认证和传输加密

## 环境搭建

### 前置条件

- Go >= 1.26
- Git

### 安装

```bash
# 克隆仓库
git clone https://github.com/lisiyu/openmodelpool
cd openmodelpool

# 安装依赖
go mod tidy

# 编译
go build -o openmodelpool .
```

### 运行

```bash
# 开发模式
go run .

# 编译后运行
./openmodelpool
```

首次运行会进入 Setup 向导（`http://localhost:8000/setup`），设置管理员密码和基础配置。

### 配置

运行时配置存储在 `data/config.json`，通过 Admin UI 或 API 修改。环境变量可作为配置回退（如 `COZE_API_TOKEN`、`PORT`）。

敏感字段（API Key 等）在写入磁盘前自动 AES-256-GCM 加密。

## 开发工作流

### 代码质量工具

| 工具 | 命令 | 目的 |
|------|------|------|
| Go Build | `go build ./...` | 编译检查 |
| Go Vet | `go vet ./...` | 静态分析 |
| Go Test | `go test -count=1 -timeout 360s -p 1 ./...` | 全量测试 |
| Race Detect | `go test -race -count=1 -timeout 120s -run "TestQuota|TestWorkerPool"` | 竞态检测 |

### 提交前检查

1. `go build ./...` — 编译通过
2. `go vet ./...` — 无静态分析问题
3. `go test -count=1 -timeout 360s -p 1 ./...` — 全量测试通过

### 分支策略

- `main` — 生产就绪代码
- 功能分支从 `main` 创建

## 常见任务

### 添加新 Provider 类型

**需修改的文件**:
1. `client.go` — 添加 `xxxNonStream()` 和 `xxxStream()` 函数
2. `client.go` — 在 `doNonStream()`/`doStream()` 的 switch 中添加 case
3. `types.go` — 添加 Provider 类型常量
4. `providers.go` — 添加预设 Provider 定义

**步骤**:
1. 在 `types.go` 中定义新类型字符串
2. 在 `client.go` 中实现 `xxxNonStream(ctx, p, model, messages, extra)` 和 `xxxStream(ctx, p, model, messages, extra, w)` 函数
3. 使用 `http.NewRequestWithContext(ctx, ...)` 创建请求
4. 在 `doNonStream()`/`doStream()` 的 switch 中添加 case
5. 在 `providers.go` 中添加预设定义
6. 编写测试

### 添加新 HTTP 端点

**需修改的文件**:
1. `server.go` — 注册路由
2. 对应功能文件 — 实现 handler 函数

**步骤**:
1. 实现 `handleXxx(w http.ResponseWriter, r *http.Request)` 函数
2. 在 `server.go` 的 `setupRoutes()` 中注册路由
3. 选择适当的中间件包装：
   - 公开端点：`rateLimitByIP(n, "name")`
   - 管理端点：`withAuth(handleXxx)`
   - 消费者端点：`withConsumerOrAdminAuth(handleXxx)`
   - 联邦端点：`withFederationAuth(handleXxx)`
4. 如果使用全局单例，添加 nil-check
5. 编写测试

### 添加新全局单例

**需修改的文件**:
1. 功能文件 — 定义类型和包级变量
2. `init.go` — 在适当初始化阶段添加 `initXxx()`
3. `stubs.go` — 如果需要占位函数

**步骤**:
1. 定义类型和包级 `var xxx *XxxType`
2. 实现 `initXxx(dataDir string)` 函数
3. 在 `init.go` 的对应阶段（`initCore`/`initAllFederation`/`initAllNetwork`）中调用
4. 确保所有使用该单例的 handler 有 nil-check
5. 在 `startBackgroundTasks()` 中启动后台 goroutine（如需）

### 修复 Bug

**流程**:
1. 编写复现 bug 的失败测试
2. 在代码中定位根因
3. 用最小改动修复
4. 验证测试通过
5. 运行全量测试确认无回归

## 编码规范

### 文件组织
- 单二进制架构，所有代码在 `package main`
- 功能相关的代码放在同一文件（如 `federation.go` 包含 `FederationManager` 的所有方法）
- 子包仅用于逻辑独立的模块（如 `ledger/`）

### 命名

| 类型 | 约定 | 示例 |
|------|------|------|
| 文件 | snake_case | `network_loadbalancer.go` |
| 结构体 | PascalCase | `FederationManager` |
| 函数 | camelCase | `handleChatCompletions` |
| 包级变量 | camelCase | `wafEngine` |
| 常量 | camelCase 或 SCREAMING_SNAKE | `wafDefaultMaxViolations` |
| Handler | `handle` + PascalCase | `handleNetworkStatus` |

### 错误处理
- 在 handler 中使用 `writeError(w, statusCode, message)` 返回错误
- 不向客户端暴露内部错误详情（`err.Error()` 不直接返回给客户端）
- 在日志中记录完整错误信息（`slog.Error("...", "error", err)`）

### 并发安全
- 所有共享状态使用 `sync.RWMutex` 或 `sync.Mutex` 保护
- 全局单例在 handler 中使用前检查 nil
- `http.NewRequestWithContext(ctx, ...)` 传播 context 以支持取消

### 日志
- 使用 `log/slog` 结构化日志
- 包含上下文：`slog.Info("message", "key", value)`
- 日志级别：Debug（开发详情）、Info（正常操作）、Warn（可恢复问题）、Error（需要关注的故障）

### 测试
- 测试文件: `*_test.go` 与源码同目录
- 全量测试: `go test -count=1 -timeout 360s -p 1 ./...`
- 测试超时: 360 秒（全量），单测默认 60 秒
