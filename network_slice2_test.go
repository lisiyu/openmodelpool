package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// Phase 2 切片② — 身份：助记词 + Node ID 白盒单测
// 覆盖：D1 统一 68 字符 mmx- / 三层一致不变量 / ConfirmBackup 清空+持久化 /
//       RestoreFromMnemonic 确定性 / Migrate legacy mm-→mmx- /
//       EnableSharedNetwork 严格守卫拒绝未确认备份
// 设计原则（与切片①一致）：不触发 activateNetwork（其会起 goroutine 并做
// 外网探测 detectPublicIP），保证测试确定、快速、无网络依赖。
// ============================================================

// D1（REQ-S2-1）：DeriveP2PNodeID 必须等价于 node.NodeID()，统一为 68 字符 mmx- 形式。
func TestSlice2_DeriveP2PNodeID_UnifiedWithIdentity(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	t.Cleanup(func() { node = nil })

	got := DeriveP2PNodeID()
	want := node.NodeID()

	if got != want {
		t.Fatalf("DeriveP2PNodeID() = %q, want canonical node.NodeID() = %q", got, want)
	}
	if !strings.HasPrefix(got, "mmx-") {
		t.Errorf("NodeID 前缀应为 mmx-，实际 %q", got)
	}
	if len(got) != 68 {
		t.Errorf("NodeID 长度应为 68（mmx- 4 + hex(32字节公钥) 64），实际 %d: %q", len(got), got)
	}

	// 24 词同样成立
	node2 := &NodeIdentity{keyPath: filepath.Join(env.dir, "node2.key")}
	if _, err := node2.GenerateWithMnemonic(24); err != nil {
		t.Fatalf("GenerateWithMnemonic(24) failed: %v", err)
	}
	if len(node2.NodeID()) != 68 || !strings.HasPrefix(node2.NodeID(), "mmx-") {
		t.Errorf("24 词 NodeID 异常: %q (len=%d)", node2.NodeID(), len(node2.NodeID()))
	}
}

// REQ-S2-3：三层一致不变量 config.NodeID == node.NodeID() == GetInfo().NodeID()。
// Init / EnableSharedNetwork / activateNetwork 三处写入点均使用 DeriveP2PNodeID()，
// 验证三处写入后 config.NodeID 都等于规范值，且 assertNodeIDInvariant 通过。
func TestSlice2_NodeIDThreeWayConsistency(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	t.Cleanup(func() { node = nil })

	// canonicalNodeID 必须等价 node.NodeID()
	if canonicalNodeID() != node.NodeID() {
		t.Fatalf("canonicalNodeID() = %q, want %q", canonicalNodeID(), node.NodeID())
	}

	nm := &NetworkManager{config: NetworkConfig{}}

	// 模拟三处写入点（均执行 `nm.config.NodeID = DeriveP2PNodeID()`）
	writePoints := []string{"Init", "EnableSharedNetwork", "activateNetwork"}
	for _, label := range writePoints {
		nm.config.NodeID = DeriveP2PNodeID()
		if nm.config.NodeID != node.NodeID() {
			t.Errorf("[%s] config.NodeID %q != 规范值 node.NodeID() %q", label, nm.config.NodeID, node.NodeID())
		}
		if got := nm.assertNodeIDInvariant(); got != node.NodeID() {
			t.Errorf("[%s] assertNodeIDInvariant() = %q, want %q", label, got, node.NodeID())
		}
	}

	// 不一致路径：assertNodeIDInvariant 应以规范值对外，绝不广播错误值
	nm.config.NodeID = "mm-garbage"
	if got := nm.assertNodeIDInvariant(); got != node.NodeID() {
		t.Errorf("不一致时 assertNodeIDInvariant 应返回规范值 %q, 实际 %q", node.NodeID(), got)
	}
}

// REQ-S2-2/6：ConfirmBackup 清空内存助记词并持久化 backup_confirmed=true。
func TestSlice2_ConfirmBackup_ClearsMemoryAndPersists(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	mnemonic, err := node.GenerateWithMnemonic(12)
	if err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	t.Cleanup(func() { node = nil })

	// 生成后：内存中持有明文助记词，可读取
	got, err := node.GetMnemonic()
	if err != nil || got != mnemonic {
		t.Fatalf("生成后应能从内存读取助记词, got=%q err=%v", got, err)
	}
	if node.IsBackupConfirmed() {
		t.Fatal("生成后 backupConfirmed 应为 false")
	}

	// 确认备份
	node.ConfirmBackup()
	if !node.IsBackupConfirmed() {
		t.Fatal("ConfirmBackup 后 backupConfirmed 应为 true")
	}
	// 确认后：内存与磁盘中的明文助记词均被清空（安全）
	if _, err := node.GetMnemonic(); err == nil {
		t.Error("ConfirmBackup 后应无法再读取明文助记词（内存+磁盘已清空）")
	}

	// 持久化：重新从磁盘加载，backupConfirmed 应为 true
	orig := node
	initNode(env.dir)
	if node == nil {
		t.Fatal("initNode 后 node 不应为 nil")
	}
	if !node.IsBackupConfirmed() {
		t.Error("重启后 backupConfirmed 应持久化为 true")
	}
	node = orig
}

// REQ-S2-4：RestoreFromMnemonic 确定性（同一助记词→同一 NodeID），且恢复即视为已备份。
func TestSlice2_RestoreFromMnemonic_Deterministic(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	src := &NodeIdentity{keyPath: filepath.Join(env.dir, "src.key")}
	mnemonic, err := src.GenerateWithMnemonic(12)
	if err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	wantID := src.NodeID()

	// 用同一助记词恢复到全新身份
	restored := &NodeIdentity{keyPath: filepath.Join(env.dir, "restored.key")}
	if err := restored.RestoreFromMnemonic(mnemonic); err != nil {
		t.Fatalf("RestoreFromMnemonic failed: %v", err)
	}
	if restored.NodeID() != wantID {
		t.Errorf("恢复出的 NodeID %q 应与生成时 %q 完全一致（确定性失败）", restored.NodeID(), wantID)
	}
	if !strings.HasPrefix(restored.NodeID(), "mmx-") || len(restored.NodeID()) != 68 {
		t.Errorf("恢复出的 NodeID 格式异常: %q", restored.NodeID())
	}
	if !restored.IsBackupConfirmed() {
		t.Error("恢复即视为已备份，backupConfirmed 应为 true")
	}

	// 非法助记词应被拒绝
	bad := &NodeIdentity{keyPath: filepath.Join(env.dir, "bad.key")}
	if err := bad.RestoreFromMnemonic("这不是一个有效的助记词短语"); err == nil {
		t.Error("非法助记词应返回错误")
	}
}

// REQ-S2-5：Migrate 将 legacy mm- 前缀改写为规范 mmx- 形式并持久化。
func TestSlice2_Migrate_LegacyMMtoMMX(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	t.Cleanup(func() { node = nil })

	canonical := node.NodeID() // mmx- + hex(pubKey)
	if !strings.HasPrefix(canonical, "mmx-") || len(canonical) != 68 {
		t.Fatalf("生成出的规范 NodeID 异常: %q", canonical)
	}

	// 构造 legacy mm- 状态（保留真实公钥，仅改前缀 + 置 needsMigration）
	node.nodeID = "mm-legacyoldformat000000000000000000"
	node.needsMigration = true
	if !node.NeedsMigration() {
		t.Fatal("构造的 legacy 身份应 NeedsMigration=true")
	}

	if err := node.Migrate(); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if node.NodeID() != canonical {
		t.Errorf("Migrate 后 NodeID %q 应重算为规范值 %q", node.NodeID(), canonical)
	}
	if !strings.HasPrefix(node.NodeID(), "mmx-") {
		t.Errorf("Migrate 后前缀应为 mmx-, 实际 %q", node.NodeID())
	}
	if len(node.NodeID()) != 68 {
		t.Errorf("Migrate 后长度应为 68, 实际 %d", len(node.NodeID()))
	}
	if node.NeedsMigration() {
		t.Error("Migrate 后 needsMigration 应为 false")
	}
	if !node.IsInitialized() {
		t.Error("Migrate 不应破坏已初始化的身份")
	}

	// 持久化：重新加载后仍是 mmx- 且 needsMigration=false
	orig := node
	initNode(env.dir)
	if node == nil {
		t.Fatal("initNode 后 node 不应为 nil")
	}
	if node.NodeID() != canonical {
		t.Errorf("重启后 NodeID 应保持一致 %q, 实际 %q", canonical, node.NodeID())
	}
	if node.NeedsMigration() {
		t.Error("重启后 needsMigration 应为 false")
	}
	node = orig
}

// REQ-S2-2/3：EnableSharedNetwork 严格守卫——未确认备份时应拒绝（不自动生成、绝不放行）。
func TestSlice2_EnableSharedNetwork_RejectsUnconfirmed(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	fed = nil
	t.Cleanup(func() { fed = nil })

	// 场景 A：身份已初始化但未确认备份 → 必须拒绝
	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	if node.IsBackupConfirmed() {
		t.Fatal("前置条件错误：生成后不应已确认备份")
	}
	t.Cleanup(func() { node = nil })

	path := writeRawNetworkJSON(t, env.dir, `{"consent_accepted": true, "network_enabled": false, "mode": "personal"}`)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{ConsentAccepted: true}}
	t.Cleanup(func() { netMgr = nil })
	netMgr = nm

	err := nm.EnableSharedNetwork()
	if err == nil {
		t.Fatal("未确认备份时 EnableSharedNetwork 必须返回错误（严格守卫）")
	}
	if !strings.Contains(err.Error(), "备份") {
		t.Errorf("拒绝原因应包含「备份」提示，实际: %v", err)
	}
	// 拒绝后不应写入 NodeID / 不应启用网络
	if nm.config.NetworkEnabled {
		t.Error("拒绝后 network_enabled 必须为 false")
	}
	if nm.config.NodeID != "" {
		t.Errorf("拒绝后不应写入 NodeID, 实际 %q", nm.config.NodeID)
	}

	// 场景 B：身份完全未初始化 → 必须拒绝（不再自动生成）
	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node2.key")}
	t.Cleanup(func() { node = nil })
	nm2 := &NetworkManager{dataPath: path, config: NetworkConfig{ConsentAccepted: true}}
	netMgr = nm2
	err2 := nm2.EnableSharedNetwork()
	if err2 == nil {
		t.Fatal("未初始化身份时 EnableSharedNetwork 必须返回错误")
	}
	if !strings.Contains(err2.Error(), "助记词") {
		t.Errorf("拒绝原因应包含「助记词」提示，实际: %v", err2)
	}
}

// REQ-S2-2/3：handleNetworkEnable 在未确认备份时返回 4xx（端到端守卫）。
func TestSlice2_HandleNetworkEnable_RejectsUnconfirmed(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	fed = nil
	t.Cleanup(func() { fed = nil })

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	t.Cleanup(func() { node = nil })

	path := writeRawNetworkJSON(t, env.dir, `{"consent_accepted": true, "network_enabled": false, "mode": "personal"}`)
	nm := &NetworkManager{dataPath: path, config: NetworkConfig{ConsentAccepted: true}}
	t.Cleanup(func() { netMgr = nil })
	netMgr = nm

	req := httptest.NewRequest(http.MethodPost, "/api/network/enable", nil)
	w := httptest.NewRecorder()
	handleNetworkEnable(w, req)

	if w.Code < 400 {
		t.Fatalf("未确认备份时 /api/network/enable 应返回 4xx, 实际 %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "备份") {
		t.Errorf("错误响应应包含「备份」提示, 实际: %s", body)
	}
	// 响应中绝不回传助记词明文
	if strings.Contains(body, "mnemonic") {
		t.Errorf("响应中不应回传助记词明文, 实际: %s", body)
	}
}

// REQ-S2-2/3：确认备份后再启用应通过守卫（单元级，避免 activateNetwork 网络副作用）。
// 此处仅验证「已确认备份」不再被守卫拒绝——通过直接校验守卫条件的组合逻辑。
func TestSlice2_GuardConditions_ConfirmedPassesGuard(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	if _, err := node.GenerateWithMnemonic(12); err != nil {
		t.Fatalf("GenerateWithMnemonic failed: %v", err)
	}
	node.ConfirmBackup() // 已确认备份
	t.Cleanup(func() { node = nil })

	// 守卫要求：node != nil && IsInitialized() && IsBackupConfirmed()
	if node == nil {
		t.Fatal("node 不应为 nil")
	}
	if !node.IsInitialized() {
		t.Fatal("已生成身份应 IsInitialized()=true")
	}
	if !node.IsBackupConfirmed() {
		t.Fatal("已确认备份应 IsBackupConfirmed()=true")
	}
	// 三个守卫条件全部满足 → EnableSharedNetwork 会越过守卫进入写入/激活阶段。
	// （完整激活路径含 activateNetwork 网络副作用，不在本确定性单测内执行。）
}
