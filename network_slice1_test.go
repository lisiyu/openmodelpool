package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// Phase 1 切片① — 双模式 + 两级开关底座 白盒单测
// 覆盖：个人版零回归 / 升级迁移 / REQ-4 前置校验 / 启动守卫 / 全局收敛
// 设计原则：不触发 activateNetwork（其会起 goroutine 并调用 detectPublicIP
// 做外网探测），以保证测试确定、快速、无网络依赖。
// ============================================================

// writeRawNetworkJSON 写出一个模拟的 network.json 磁盘状态，返回其路径。
func writeRawNetworkJSON(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "network.json")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	return p
}

// REQ-1 / REQ-2 启动守卫 + 个人版零回归：
// network_enabled=false 时 Init() 不得派生 NodeID、不得起任何出站 loop。
func TestSlice1_Init_PersonalMode_StartupGuard(t *testing.T) {
	env := setupTestEnv(t)
	// 保证 federation 全局为 nil，使 syncFederationToNetwork 为安全的 no-op。
	fed = nil

	path := writeRawNetworkJSON(t, env.dir, `{"network_enabled": false, "mode": "personal"}`)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{}}

	if err := nm.Init(); err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}

	if nm.config.NetworkEnabled {
		t.Error("personal mode: NetworkEnabled 应保持 false")
	}
	if nm.config.Mode != NetworkModePersonal {
		t.Errorf("personal mode: 期望 mode=personal, 实际 %q", nm.config.Mode)
	}
	if nm.config.NodeID != "" {
		t.Errorf("personal mode: 绝对不应派生 NodeID, 实际 %q", nm.config.NodeID)
	}
	if nm.stopRefresh != nil {
		t.Error("personal mode: 启动守卫失效 — 不应启动 refresh loop（无多余出站连接）")
	}
	if fed != nil {
		t.Error("personal mode: federation manager 不应被触碰（保持 nil）")
	}
}

// REQ-3 收敛 + 升级迁移：旧全局键 federation_enabled=true 且 network_enabled=false
// ⇒ 收敛为 network_enabled=true、Mode=Shared，并清除旧键。
func TestSlice1_Load_MigrateLegacyFederationEnabled(t *testing.T) {
	env := setupTestEnv(t)
	// 模拟遗留状态：全局 cfg 仍残留 federation_enabled=true
	cfg.Set("federation_enabled", "true")

	path := writeRawNetworkJSON(t, env.dir, `{"network_enabled": false, "mode": "personal"}`)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{}}

	nm.load()

	if !nm.config.NetworkEnabled {
		t.Error("迁移失败: NetworkEnabled 应为 true")
	}
	if nm.config.Mode != NetworkModeShared {
		t.Errorf("迁移失败: 期望 mode=shared, 实际 %q", nm.config.Mode)
	}
	if got := cfg.Get("federation_enabled", "false"); got != "false" {
		t.Errorf("迁移失败: 旧键 federation_enabled 必须被清除, 实际 %q", got)
	}
}

// REQ-3 收敛负向：federation_enabled=false 时不应发生误迁移。
func TestSlice1_Load_NoMigrationWhenLegacyDisabled(t *testing.T) {
	env := setupTestEnv(t)
	cfg.Set("federation_enabled", "false")

	path := writeRawNetworkJSON(t, env.dir, `{"network_enabled": false, "mode": "personal"}`)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{}}

	nm.load()

	if nm.config.NetworkEnabled {
		t.Error("federation_enabled=false 时不应发生迁移, NetworkEnabled 应保持 false")
	}
	if nm.config.Mode != NetworkModePersonal {
		t.Errorf("mode 应保持 personal, 实际 %q", nm.config.Mode)
	}
}

// REQ-2 不变量：SetNetworkEnabled(false) 强制 ShareToPool=false 且 Mode=Personal。
// 走 deactivateNetwork 路径（stopRefreshLoop nil 安全 + syncFederationToNetwork fed nil 安全），
// 不触发任何出站 goroutine。
func TestSlice1_SetNetworkEnabled_FalseForcesPersonal(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	fed = nil

	nm := &NetworkManager{
		config: NetworkConfig{
			Mode:           NetworkModeShared,
			NetworkEnabled: true,
			ShareToPool:    true,
		},
	}
	nm.SetNetworkEnabled(false)

	if nm.config.Mode != NetworkModePersonal {
		t.Errorf("SetNetworkEnabled(false): 期望 mode=personal, 实际 %q", nm.config.Mode)
	}
	if nm.config.ShareToPool {
		t.Error("SetNetworkEnabled(false): ShareToPool 必须被强制为 false")
	}
	if nm.config.NetworkEnabled {
		t.Error("SetNetworkEnabled(false): NetworkEnabled 必须为 false")
	}
}

// REQ-2 启用分支配置效果（无 goroutine 风险）：
// 已从启用态再次 SetNetworkEnabled(true) 时，enabled && !wasEnabled 为假，
// 不会触发 activateNetwork，仅校验 Mode 收敛为 Shared。
func TestSlice1_SetNetworkEnabled_EnableNoOpWhenAlreadyEnabled(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	fed = nil

	nm := &NetworkManager{
		config: NetworkConfig{
			Mode:           NetworkModeShared,
			NetworkEnabled: true, // 已启用
			ShareToPool:    false,
		},
	}
	nm.SetNetworkEnabled(true)

	if !nm.config.NetworkEnabled {
		t.Error("NetworkEnabled 应保持 true")
	}
	if nm.config.Mode != NetworkModeShared {
		t.Errorf("启用分支: 期望 mode=shared, 实际 %q", nm.config.Mode)
	}
}

// REQ-2 启动守卫：syncFederationToNetwork 在 fed==nil 时必须安全返回（不 panic）。
func TestSlice1_SyncFederationToNetwork_FedNilSafe(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	fed = nil

	nm := &NetworkManager{config: NetworkConfig{NetworkEnabled: true}}
	// 个人版 / 预初始化阶段 fed 尚未建立，此处必须安全返回。
	nm.syncFederationToNetwork()
}

// REQ-4 前置校验：三项皆缺失时 AllMet=false，且各项标志均为 false。
func TestSlice1_CheckJoinConditions_AllMissing(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	// 未添加任何 Provider；allocMgr 未初始化（nil）。

	nm := &NetworkManager{config: NetworkConfig{}}
	allMet, res := nm.CheckJoinConditions()

	if allMet {
		t.Error("未配置任何条件时 all_met 应为 false")
	}
	if res.HasProvider {
		t.Error("HasProvider 应为 false")
	}
	if res.HasQuotaManager {
		t.Error("HasQuotaManager 应为 false（allocMgr 为 nil）")
	}
	if res.HasRemaining {
		t.Error("HasRemaining 应为 false（无额度）")
	}
}

// REQ-4 前置校验：有 Provider + 剩余额度，但缺额度管理（allocMgr 为 nil）→ 仍不可入网。
func TestSlice1_CheckJoinConditions_ProviderButNoQuotaManager(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	p := makeProvider("p-jc1", "Provider JC1", makeModelDef("gpt-4o"), 5, true)
	p.TokenLimit = 100000 // 提供剩余额度
	pm.Add(p)

	nm := &NetworkManager{config: NetworkConfig{}}
	allMet, res := nm.CheckJoinConditions()

	if !res.HasProvider {
		t.Error("HasProvider 应为 true")
	}
	if !res.HasRemaining {
		t.Error("HasRemaining 应为 true（TokenLimit>0 且无消耗）")
	}
	if res.HasQuotaManager {
		t.Error("HasQuotaManager 应为 false（allocMgr 为 nil）")
	}
	if allMet {
		t.Error("缺少额度管理时 all_met 必须为 false")
	}
}

// REQ-4 前置校验：三项全部满足时 AllMet=true，且 Message 被填充。
func TestSlice1_CheckJoinConditions_AllMet(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	p := makeProvider("p-jc2", "Provider JC2", makeModelDef("gpt-4o"), 5, true)
	p.TokenLimit = 100000
	pm.Add(p)

	// 注入额度管理（全局 allocMgr 非 nil）
	origAlloc := allocMgr
	allocMgr = &AllocationManager{config: DefaultQuotaAllocation()}
	t.Cleanup(func() { allocMgr = origAlloc })

	nm := &NetworkManager{config: NetworkConfig{}}
	allMet, res := nm.CheckJoinConditions()

	if !allMet {
		t.Errorf("三项条件均满足时 all_met 应为 true, 实际 res=%+v", res)
	}
	if !res.HasProvider || !res.HasQuotaManager || !res.HasRemaining {
		t.Errorf("三项条件应全部为 true, 实际 %+v", res)
	}
	if res.Message == "" {
		t.Error("满足条件时 Message 应被填充（用于前端温和提示）")
	}
}
