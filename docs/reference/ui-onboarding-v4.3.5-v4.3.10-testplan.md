# OpenModelPool v4.3.5 – v4.3.10 UI / Onboarding 批次 测试计划

> 版本范围：v4.3.5 – v4.3.10（UI/Onboarding 聚合批次）
> 聚合理由：本批次均为**前端 / 引导交互**相关的连续改动，且存在一条清晰的回归修复链（4.3.8 → 4.3.9）与一项在 4.3.6 与 4.3.10 两度发行的同名功能（空闲配额提示 UI），故合并为一份聚合测试计划，避免跨文档割裂。
> 关联文档：`admin-network.js`、`admin.html`、`network.go`（`checkIdleQuota`/`handleIdleQuotaCheck`）、`routes.go`、`CHANGELOG.md`。

---

## 1. 范围与背景

| 版本 | 能力 | 关键变更 | 优先级 |
|------|------|----------|--------|
| v4.3.5 | 4 步引导向导（REQ-5/6/12） | 替换单步免责弹窗为向导：①网络须知 → ②助记词生成与**强制备份** → ③共享边界配置（`DailyContribCap` / `ShareIdleOnly` / `ModelWhitelist`）→ ④完成确认显示 Node ID 并启用网络；含进度条 | P1 |
| v4.3.6 | 空闲配额提示 UI（REQ-13） | 以 `/api/network/idle-quota` 端点 + 后端 `ShouldNotify` 逻辑替换会话级 toast；横幅含使用率与剩余额度；"暂不"经 localStorage 按月持久化；"了解/加入"打开引导向导 | P1 |
| v4.3.8 | 修复三开关全部静默失效（P0） | `admin-network.js` 末尾多余 `}}` 致全文件 `SyntaxError`，`toggleNetworkEnabled` / `toggleShareToPool` / `saveRelayToggle` 未被定义，「加入共享网络/共享剩余额度/开启中继」全部失效；`admin.html` 页面初始化补 `loadNetworkStatus()` | P0 |
| v4.3.9 | 修复 4.3.8 引入的初始化回归（P0） | `renderNetworkUI()` 从权威服务端状态派生 `window._networkMode`（修复 4.3.8 中 `_networkMode` 恒为 `undefined` 导致 `=== 'shared'` 守卫永不过，`loadShareInfo()`/`loadGuestKeys()` 不执行）；恢复 `loadFederationConfig()` 调用使中继开关正确渲染；cache-buster 升 `v=347`，`AppVersion=4.3.9` | P0 |
| v4.3.10 | 空闲配额提示 UI（REQ-13，与 v4.3.6 同名重复） | 内容同 v4.3.6；复用 `slideIn` 动画；在 4.3.9 修复后的稳定 UI 之上重新落地 | P1 |

> 注：v4.3.6 与 v4.3.10 的空闲配额提示 UI 为**同一功能两次发行**（4.3.6 首发，4.3.10 在 UI 稳定性修复后再次提交）。验收与测试均以该功能的**最终落地形态**为准，单次测试即可覆盖两端。

---

## 2. 自动化单元测试（现状与缺口）

### 2.1 现状核实

对仓库内全部 `*_test.go` 执行 Grep，检索关键词 `idle-quota` / `IdleQuota` / `onboarding` / `NetworkEnabled` / `ShareToPool` / `RenderNetworkUI` / `ShouldNotify` / `checkIdleQuota` / `handleIdleQuotaCheck` / `toggleShareToPool` / `renderNetworkUI`：

- **无任何 `*_test.go` 引用本批次功能**。
- 后端 `idle-quota` 相关代码确已存在且未被测试：
  - `network.go:2186` `func checkIdleQuota() IdleQuotaStatus`
  - `network.go:2224` `func handleIdleQuotaCheck(w, r)`
  - `routes.go:257` `GET /api/network/idle-quota`（受 `withAuth` 保护）
- 前端逻辑（`admin-network.js` 的 `toggleNetworkEnabled`/`toggleShareToPool`/`saveRelayToggle`/`renderNetworkUI`/`loadNetworkStatus`/`loadFederationConfig`，`admin.html` 的 4 步向导与横幅）均为 JS/HTML，无对应 Go 单测，亦无 chromedp e2e 测试覆盖。

### 2.2 测试缺口与建议新增

| 测试文件（建议新增） | 用例（建议） | 验证点 | 现状 |
|------|------|--------|------|
| `idle_quota_test.go` | `TestCheckIdleQuota_ShouldNotifyWhenIdle` | `NetworkEnabled=false` 且 `used/ total < 70%` → `ShouldNotify=true`，`RemainingQuota=total-used`，`Message` 非空 | **建议新增** |
| | `TestCheckIdleQuota_NoNotifyWhenShared` | `NetworkEnabled=true` → `ShouldNotify=false`（即便有闲置额度） | **建议新增** |
| | `TestCheckIdleQuota_NoNotifyWhenHighUsage` | `used/ total ≥ 70%` → `ShouldNotify=false` | **建议新增** |
| | `TestHandleIdleQuotaCheck_HTTP` | `GET /api/network/idle-quota`（带 auth）→ 200，JSON 含 `should_notify`/`remaining_quota`/`has_idle_quota` | **建议新增** |
| `network_toggle_test.go`（handler 级） | `TestToggleNetworkEnabled_Persists` | `_networkMode` / `NetworkEnabled` 持久化并与 `renderNetworkUI` 派生逻辑一致 | **建议新增（需后端开关接口）** |
| `admin_network_e2e_test.go`（chromedp） | `TestOnboardingWizard_FourSteps` | 向导 4 步可走通、助记词强制备份门控、完成页显示 Node ID、进度条推进 | **建议新增（前端 e2e）** |
| | `TestIdleQuotaBanner_ShowHidePersist` | 横幅按 `ShouldNotify` 显隐；"暂不"写入 localStorage 当月不再提示；"了解/加入"打开向导 | **建议新增（前端 e2e）** |
| | `TestThreeTogglesRenderAfter_4_3_9` | 4.3.9 修复后：三开关（加入共享网络/共享剩余额度/开启中继）在 `shared` 模式下正确渲染并生效 | **建议新增（前端 e2e）** |

**诚实说明**：本批次核心为 UI / JS 逻辑（向导流程、横幅显隐、开关渲染与持久化），**纯 Go 单测难以覆盖交互层**。建议分两层补齐：
1. **handler 级（易落地）**：对 `handleIdleQuotaCheck` / `checkIdleQuota` 补充 Go 单测（后端 `ShouldNotify` 逻辑、端点契约），这是本批次唯一可稳定以 `go test` 覆盖的部分。
2. **前端 e2e（高价值但缺基设）**：以 chromedp 覆盖向导流程、横幅显隐与 localStorage 持久化、三开关渲染。注意 CI 已预装 Chrome（见 v4.3.32 说明），但仓库目前无任何 chromedp 测试，需先建立最小 e2e 框架。

### 2.3 建议运行命令（待上述测试新增后生效）

```bash
# 后端 idle-quota 逻辑与端点（建议新增）
go test ./... -run 'IdleQuota|HandleIdleQuotaCheck' -v

# 前端 e2e（建议新增，需 chromedp 基设）
go test ./... -run 'E2E_Onboarding|E2E_IdleQuota|E2E_ThreeToggles' -v
```

> 当前仓库对上述命令无任何匹配用例；本节标注"建议新增"即代表尚未落地。

---

## 3. 集成 / QA 手册（手动）

### 3.1 三开关在 4.3.9 修复后正确渲染与生效（P0-核心）

| 步骤 | 操作（管理面板 → 网络页） | 期望结果 |
|---|---|---|
| 3.1.1 | 个人模式升级后打开网络页 | 页面初始化调用 `loadNetworkStatus()`；`renderNetworkUI()` 据服务端状态派生 `window._networkMode` |
| 3.1.2 | 服务端 `mode=shared` 时刷新 | `window._networkMode === 'shared'`，`loadShareInfo()` / `loadGuestKeys()` 被触发；三开关可见且状态正确 |
| 3.1.3 | 点击「加入共享网络」 | `toggleNetworkEnabled()` 被调用并持久化；页面不再静默失效（对比 4.3.8 缺陷） |
| 3.1.4 | 点击「共享剩余额度」 | `toggleShareToPool()` 被调用并持久化 |
| 3.1.5 | 点击「开启中继」 | `saveRelayToggle()` 被调用并持久化；4.3.8 中因 SyntaxError 整文件丢弃导致的"点击无反应"已修复 |
| 3.1.6 | 中继已启用状态下刷新页面 | 4.3.9 修复后 `loadFederationConfig()` 被恢复调用，中继开关**正确渲染为 ON**（非恒 OFF） |

### 3.2 4 步引导向导流程（v4.3.5 / 4.3.10 复用）

| 步骤 | 操作 | 期望结果 |
|---|---|---|
| 3.2.1 | 点开「加入共享网络」/ 空闲配额横幅「了解/加入」 | 弹出 4 步向导，进度条高亮第 1 步 |
| 3.2.2 | Step 1 须知说明 → 「下一步」 | 进入 Step 2 |
| 3.2.3 | Step 2 助记词生成 | 显示助记词；**未勾选/未确认备份前「下一步」禁用**（强制备份门控）；「上一步」可回退 |
| 3.2.4 | 确认备份 → 「下一步」 | 进入 Step 3 |
| 3.2.5 | Step 3 共享边界配置 | 可设置 `DailyContribCap` / `ShareIdleOnly` / `ModelWhitelist`；「下一步」 |
| 3.2.6 | Step 4 完成确认 | 显示本节点 **Node ID**，点击「完成」启用网络 |

### 3.3 空闲配额横幅显隐逻辑（v4.3.6 / 4.3.10）

| 步骤 | 操作 / 条件 | 期望结果 |
|---|---|---|
| 3.3.1 | 个人模式 + 额度使用率 < 70%（`checkIdleQuota().ShouldNotify=true`） | 页面显示持久横幅，含使用率百分比与剩余 tokens（取自 `/api/network/idle-quota`） |
| 3.3.2 | 点击「暂不」 | 横幅消失；localStorage 按月记录，**同月内不再提示** |
| 3.3.3 | 次月（或清除 localStorage）重访 | 若仍满足 3.3.1 条件，横幅再次出现 |
| 3.3.4 | 点击「了解/加入」 | 直接打开 3.2 的 4 步引导向导 |
| 3.3.5 | `NetworkEnabled=true` 或 使用率 ≥ 70% | `ShouldNotify=false`，横幅不出现 |

### 3.4 失败判据

- 3.1.3–3.1.5 任一开关点击后状态未持久化 / 控制台报函数未定义 → **4.3.8 缺陷回归**，P0 失败。
- 3.1.6 中继开关刷新后仍为 OFF（尽管服务端已启用）→ **4.3.9 修复回归**，P0 失败。
- 3.2.3 助记词未备份即可进入下一步（强制备份门控失效）→ 向导验收失败。
- 3.2.6 完成页未显示 Node ID，或点击完成后网络未启用 → 向导验收失败。
- 3.3.1 满足 `ShouldNotify` 却无横幅，或横幅数据非来自 `/api/network/idle-quota` → 横幅验收失败。
- 3.3.2 「暂不」后同月内仍重复弹出 → localStorage 持久化失败。

---

## 4. 质量门禁（CI）

| 门禁 | 命令 | 预期 |
|------|------|------|
| 格式化 | `gofmt -l .`（仅校验 Go 文件） | 无 diff 残留 |
| 构建 | `go build ./...` | 0 error |
| 静态 | `go vet ./...` | 0 新增 warning |
| 特性单测（建议） | `go test ./... -run 'IdleQuota|HandleIdleQuotaCheck' -v` | 全部 PASS（待 2.2 新增后生效） |
| 全量 | `go test -race -count=1 -timeout 25m ./...` | 通过（参考 v4.3.32 统一门禁；Windows 下预存文件权限/锁相关失败与本次无关） |

> 注：本批次前端改动（JS/HTML）不计入 `go vet`/`go build` 门禁，需依赖 §3 手动 QA 与（建议新增的）chromedp e2e 兜底。

---

## 5. 一致性复核（IS_PASS）

最终人工 / 自动复核清单：

- [ ] `admin-network.js` 无语法错误（无多余 `}}`），`toggleNetworkEnabled` / `toggleShareToPool` / `saveRelayToggle` 均被定义并可调用（4.3.8 修复成立）
- [ ] `admin.html` 页面初始化调用 `loadNetworkStatus()`，`renderNetworkUI()` 据服务端状态派生 `window._networkMode`（4.3.9 修复成立）
- [ ] 中继开关在 `mode=shared` 且服务端启用时正确渲染为 ON（恢复 `loadFederationConfig()` 调用成立）
- [ ] 4 步向导：须知 → 助记词强制备份 → 共享边界配置 → 完成显示 Node ID，进度条推进正常
- [ ] 助记词未备份前「下一步」禁用（强制备份门控）
- [ ] 空闲配额横幅按 `/api/network/idle-quota` 的 `ShouldNotify` 显隐；"暂不"经 localStorage 当月持久化；"了解/加入"打开向导
- [ ] `checkIdleQuota()` 逻辑正确：`ShouldNotify` 仅在 `!NetworkEnabled` 且使用率 `< 70%` 且存在闲置额度时为 true
- [ ] 建议新增 `idle_quota_test.go` 与（可选）chromedp e2e 已补充并全绿
- [ ] `main.go` `AppVersion` 与发行版本一致；无 `TODO` / 占位 / `pass` / `...`
