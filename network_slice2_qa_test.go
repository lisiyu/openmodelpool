package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================
// Phase 2 切片② — 严过关（QA）独立补充测试
// 目的：强化工程师 network_slice2_test.go 中偏弱/凑数的断言，并补齐
//       架构 §8 / PRD §3 中明确但原测试未覆盖的验收点：
//   • enable 守卫【放行】路径（工程师的 GuardConditions 测试只检查 node 标志，
//     并未真正调用 EnableSharedNetwork 验证其越过守卫）—— ARCH §9 风险③
//   • 个人版零身份：node 未初始化时 GetStatus.node_id 必须恒为 "" —— US-C / ARCH §9 风险②
//   • 三层一致不变量含广播值 GetInfo().NodeID() —— REQ-S2-3
//   • GET /api/network/status 暴露 identity_initialized/has_mnemonic/
//     backup_confirmed/needs_migration —— REQ-S2-3/5/6
//   • handleNetworkIdentityRestore：合法→200 确定性、非法→400 —— REQ-S2-4/7
// 设计原则同工程师：不主动等待 activateNetwork 的 goroutine 副作用；注册后即时
// 清理 stopRefreshLoop 与全局 routeTable 污染，保证测试确定、无网络阻塞。
// ============================================================

// ensureRouteTable 保证全局 routeTable 非空（GetStatus 路径依赖它）。
func ensureRouteTable() {
	if routeTable == nil {
		routeTable = initRouteTable()
	}
}

// qaInitMinimal 初始化 enc/cfg/routeTable（足够 EnableSharedNetwork 使用），
// 且【不会】把全局 cfg 重置为 nil。原因：EnableSharedNetwork → activateNetwork 会
// 异步启动 registerSelf goroutine，该 goroutine 在 collectAddresses 中读取全局 cfg。
// 若 cleanup 把 cfg 置 nil（如 setupTestEnv 所做），异步 goroutine 可能读到 nil 而 panic。
// 生产环境中 cfg 恒非空，此处保持 cfg 非空以匹配生产不变量，避免测试假阳性 panic。
// 仅用于会触发 activateNetwork 的测试。
func qaInitMinimal(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	initEncryptor(filepath.Join(dir, ".key"))
	initConfig(filepath.Join(dir, "config.json"))
	ensureRouteTable()
	t.Cleanup(func() {
		node = nil
		fed = nil
		netMgr = nil
		// 注意：不重置 cfg（保持非空），以庇护可能仍在运行的异步 registerSelf goroutine
	})
	return dir
}

// drainRegisterSelf 等待 activateNetwork 派生的异步 registerSelf goroutine 完成，
// 避免其后续读取全局状态与用例清理产生竞态。最多等待 8s（detectPublicIP 通常快速失败）。
func drainRegisterSelf(nm *NetworkManager) {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if routeTable != nil && nm.config.NodeID != "" && routeTable.Get(nm.config.NodeID) != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if nm.stopRefresh != nil {
		nm.stopRefreshLoop()
	}
	if routeTable != nil && nm.config.NodeID != "" {
		routeTable.Remove(nm.config.NodeID)
	}
}

// REQ-S2-3（三层一致，含广播值）：node.NodeID() 必须与 GetInfo().NodeID() 逐字符相等，
// 且均为 68 字符 mmx- 形式。GetInfo 是广播/能力声明的来源，必须与身份对象完全一致。
func TestSlice2_QA_NodeIDGetInfoMatchesIdentity(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	t.Cleanup(func() { node = nil })

	info := node.GetInfo()
	if info.NodeID != node.NodeID() {
		t.Fatalf("GetInfo().NodeID %q 必须等于 node.NodeID() %q（广播值与身份对象不一致，三层一致被破坏）",
			info.NodeID, node.NodeID())
	}
	if len(info.NodeID) != 68 || !strings.HasPrefix(info.NodeID, "mmx-") {
		t.Errorf("GetInfo().NodeID 格式异常: %q", info.NodeID)
	}
	if info.NodeID != DeriveP2PNodeID() {
		t.Errorf("GetInfo().NodeID %q 必须等于 DeriveP2PNodeID() %q",
			info.NodeID, DeriveP2PNodeID())
	}
}

// US-C / ARCH §9 风险②：个人版（node 未初始化）GetStatus 必须不泄露任何 NodeID，
// 且 identity_initialized 必须为 false（零身份、零出站）。
func TestSlice2_QA_PersonalMode_NoNodeIDLeak(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	ensureRouteTable()
	fed = nil
	node = nil
	t.Cleanup(func() { node = nil; fed = nil })

	nm := &NetworkManager{
		dataPath: filepath.Join(env.dir, "network.json"),
		config:   NetworkConfig{NetworkEnabled: false, Mode: NetworkModePersonal},
	}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })

	st := nm.GetStatus()

	got, _ := st["node_id"].(string)
	if got != "" {
		t.Errorf("个人版 GetStatus.node_id 必须为空（零身份泄露），实际 %q", got)
	}
	init, _ := st["identity_initialized"].(bool)
	if init {
		t.Error("个人版 identity_initialized 必须为 false")
	}
}

// REQ-S2-2/3：已生成且已确认备份的身份，EnableSharedNetwork 必须【放行】并写入
// 68 字符 mmx- NodeID（三层一致）。这是对工程师 "凑数" 守卫测试的强化——
// 工程师仅检查 node 标志，未真正验证函数越过守卫。
func TestSlice2_QA_EnableSharedNetwork_PassWhenConfirmed(t *testing.T) {
	dir := qaInitMinimal(t)
	fed = nil
	t.Cleanup(func() { fed = nil })

	node = &NodeIdentity{keyPath: filepath.Join(dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	node.ConfirmBackup() // 已确认备份 → 守卫放行
	t.Cleanup(func() { node = nil })

	path := writeRawNetworkJSON(t, dir, `{"consent_accepted": true, "network_enabled": false, "mode": "personal"}`)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{ConsentAccepted: true, NetworkEnabled: false}}
	netMgr = nm

	if err := nm.EnableSharedNetwork(); err != nil {
		t.Fatalf("已确认备份时 EnableSharedNetwork 必须成功（守卫应放行），实际错误: %v", err)
	}
	defer drainRegisterSelf(nm)

	if nm.config.NodeID == "" {
		t.Fatal("启用后必须写入 NodeID")
	}
	if len(nm.config.NodeID) != 68 || !strings.HasPrefix(nm.config.NodeID, "mmx-") {
		t.Errorf("启用后 NodeID 格式异常: %q", nm.config.NodeID)
	}
	if nm.config.NodeID != node.NodeID() {
		t.Errorf("启用后 config.NodeID %q 必须等于规范值 node.NodeID() %q（三层一致被破坏）",
			nm.config.NodeID, node.NodeID())
	}
	if !nm.config.NetworkEnabled {
		t.Error("启用后 NetworkEnabled 必须为 true")
	}
	if nm.config.Mode != NetworkModeShared {
		t.Errorf("启用后 Mode 应为 shared, 实际 %q", nm.config.Mode)
	}
}

// 一致性契约（切片①守卫 + 切片②未回归）：EnableSharedNetwork 在未记录用户同意
// （ConsentAccepted==false）时必须拒绝，返回明确中文同意提示。后端端点
// POST /api/network/consent 仍存活(server.go:244)，但切片②前端向导重写后
// 不再调用它——本测试锁定后端契约，防止将来误删同意校验导致静默放行。
func TestSlice2_QA_EnableSharedNetwork_RejectsWithoutConsent(t *testing.T) {
	dir := qaInitMinimal(t)
	fed = nil
	t.Cleanup(func() { fed = nil })

	node = &NodeIdentity{keyPath: filepath.Join(dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	node.ConfirmBackup() // 身份已就绪、备份已确认，唯独未记录同意
	t.Cleanup(func() { node = nil })

	path := writeRawNetworkJSON(t, dir, `{"consent_accepted": false, "network_enabled": false, "mode": "personal"}`)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{ConsentAccepted: false, NetworkEnabled: false}}
	netMgr = nm

	if err := nm.EnableSharedNetwork(); err == nil {
		t.Fatal("未记录用户同意时 EnableSharedNetwork 必须拒绝（同意守卫契约）")
	} else if !strings.Contains(err.Error(), "请先阅读并同意共享网络须知") {
		t.Errorf("未同意时应返回明确中文同意提示, 实际: %v", err)
	}
}

// 端到端契约（对应前端 completeJoin 第 0 步修复）：模拟"先记录同意 → 再启用"的
// 正常路径。直接驱动 handleNetworkConsent（POST /api/network/consent, accepted:true）
// 将 ConsentAccepted 置 true，随后 EnableSharedNetwork 必须【放行】并写入规范 68 字符
// mmx- NodeID。此测试锁定"记录同意后 enable 放行"的后端链路，作为前端补回 consent
// 调用的后端侧背书（Go 单测无法覆盖 JS 向导，但后端契约必须可信）。
func TestSlice2_QA_ConsentThenEnable_Passes(t *testing.T) {
	dir := qaInitMinimal(t)
	fed = nil
	t.Cleanup(func() { fed = nil })

	node = &NodeIdentity{keyPath: filepath.Join(dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	node.ConfirmBackup() // 身份就绪、备份已确认，仅缺同意
	t.Cleanup(func() { node = nil })

	path := writeRawNetworkJSON(t, dir, `{"consent_accepted": false, "network_enabled": false, "mode": "personal"}`)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{ConsentAccepted: false, NetworkEnabled: false}}
	netMgr = nm

	// 第 0 步：记录同意（等价前端 completeJoin 的 authFetch('/api/network/consent')）
	cbody := `{"accepted": true}`
	creq := httptest.NewRequest(http.MethodPost, "/api/network/consent", strings.NewReader(cbody))
	creq.Header.Set("Content-Type", "application/json")
	cw := httptest.NewRecorder()
	handleNetworkConsent(cw, creq)
	if cw.Code != 200 {
		t.Fatalf("记录同意应 200, 实际 %d: %s", cw.Code, cw.Body.String())
	}
	if !nm.config.ConsentAccepted {
		t.Fatal("记录同意后 ConsentAccepted 必须为 true")
	}

	// 第 2 步：enable 必须放行（同意守卫已满足）
	if err := nm.EnableSharedNetwork(); err != nil {
		t.Fatalf("记录同意后 EnableSharedNetwork 必须放行, 实际: %v", err)
	}
	defer drainRegisterSelf(nm)

	if nm.config.NodeID == "" || len(nm.config.NodeID) != 68 || !strings.HasPrefix(nm.config.NodeID, "mmx-") {
		t.Errorf("启用后 NodeID 格式异常: %q", nm.config.NodeID)
	}
	if !nm.config.NetworkEnabled || nm.config.Mode != NetworkModeShared {
		t.Errorf("启用后状态异常: enabled=%v mode=%q", nm.config.NetworkEnabled, nm.config.Mode)
	}
}

// REQ-S2-2/3：handleNetworkEnable 对已确认身份返回 200，响应含 68 字符规范 node_id，
// 且绝不回传助记词明文（确认后内存已清空）。
func TestSlice2_QA_HandleNetworkEnable_PassReturnsNodeIDNoMnemonic(t *testing.T) {
	dir := qaInitMinimal(t)
	fed = nil
	t.Cleanup(func() { fed = nil })

	node = &NodeIdentity{keyPath: filepath.Join(dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	node.ConfirmBackup()
	t.Cleanup(func() { node = nil })

	// 预置已启用的 network.json（含正确 68 字符 node_id 与 consent）
	raw := `{"consent_accepted": true, "network_enabled": true, "mode": "shared", "node_id": "` + node.NodeID() + `"}`
	path := writeRawNetworkJSON(t, dir, raw)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{ConsentAccepted: true, NetworkEnabled: true, NodeID: node.NodeID()}}
	netMgr = nm

	req := httptest.NewRequest(http.MethodPost, "/api/network/enable", nil)
	w := httptest.NewRecorder()
	handleNetworkEnable(w, req)
	defer drainRegisterSelf(nm)

	if w.Code != 200 {
		t.Fatalf("/api/network/enable 已确认身份应返回 200, 实际 %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "mmx-") || !strings.Contains(body, node.NodeID()) {
		t.Errorf("响应必须包含 68 字符规范 node_id %q, 实际: %s", node.NodeID(), body)
	}
	if strings.Contains(body, "mnemonic") {
		t.Errorf("响应绝不回传助记词明文, 实际: %s", body)
	}
	// 二次确认：确认备份后内存明文必须已清空
	if _, e := node.GetMnemonic(); e == nil {
		t.Error("确认备份后 GetMnemonic 必须失败（内存明文已清空）")
	}
}

// REQ-S2-3/5/6：GetStatus 暴露 identity_initialized / has_mnemonic /
// backup_confirmed / needs_migration，且 node_id 使用规范值。
func TestSlice2_QA_GetStatus_ExposesIdentityFields(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	ensureRouteTable()
	fed = nil
	t.Cleanup(func() { fed = nil })

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	node.ConfirmBackup()
	t.Cleanup(func() { node = nil })

	nm := &NetworkManager{dataPath: filepath.Join(env.dir, "network.json"), config: NetworkConfig{}}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })

	st := nm.GetStatus()

	if init, _ := st["identity_initialized"].(bool); !init {
		t.Error("已初始化身份 GetStatus.identity_initialized 必须为 true")
	}
	if hm, _ := st["has_mnemonic"].(bool); !hm {
		t.Error("由助记词生成 GetStatus.has_mnemonic 必须为 true")
	}
	if bc, _ := st["backup_confirmed"].(bool); !bc {
		t.Error("已确认备份 GetStatus.backup_confirmed 必须为 true")
	}
	if nmig, _ := st["needs_migration"].(bool); nmig {
		t.Error("新生成身份 needs_migration 必须为 false")
	}
	if nid, _ := st["node_id"].(string); nid != node.NodeID() {
		t.Errorf("GetStatus.node_id %q 必须等于规范值 node.NodeID() %q", nid, node.NodeID())
	}
}

// REQ-S2-4/7：handleNetworkIdentityRestore —— 合法助记词 200 且确定性、
// 非法助记词 400（明确中文报错，不放行）。
func TestSlice2_QA_HandleRestore_DeterministicAndRejectsInvalid(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	fed = nil
	t.Cleanup(func() { fed = nil; netMgr = nil })

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	mnemonic, err := node.GenerateWithMnemonic(12)
	if err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	wantID := node.NodeID()
	t.Cleanup(func() { node = nil })

	// 合法助记词 → 200
	body := `{"mnemonic":"` + mnemonic + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/network/identity/restore", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleNetworkIdentityRestore(w, req)
	if w.Code != 200 {
		t.Fatalf("合法助记词 restore 应 200, 实际 %d: %s", w.Code, w.Body.String())
	}
	if node.NodeID() != wantID {
		t.Errorf("恢复后 NodeID %q 应与生成时 %q 完全一致（确定性失败）", node.NodeID(), wantID)
	}

	// 非法助记词 → 400
	bad := `{"mnemonic":"这不是一个有效的助记词短语"}`
	badReq := httptest.NewRequest(http.MethodPost, "/api/network/identity/restore", strings.NewReader(bad))
	badReq.Header.Set("Content-Type", "application/json")
	badW := httptest.NewRecorder()
	handleNetworkIdentityRestore(badW, badReq)
	if badW.Code != 400 {
		t.Errorf("非法助记词 restore 应 400, 实际 %d: %s", badW.Code, badW.Body.String())
	}
	if !strings.Contains(badW.Body.String(), "助记词无效") {
		t.Errorf("非法助记词应返回明确中文报错, 实际: %s", badW.Body.String())
	}
}
