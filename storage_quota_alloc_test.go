package main

// G4: quota_allocation 域 bbolt 后端测试。

import (
	"os"
	"path/filepath"
	"testing"
)

// 打开测试 DB；调用方 defer cleanup。句柄由 importer 绑定到实例。
func testAllocBbolt(t *testing.T, dir string) (*bboltHandle, func()) {
	t.Helper()
	h, err := openBbolt(dir)
	if err != nil {
		t.Fatalf("openBbolt: %v", err)
	}
	return h, func() {
		closeBbolt(dir)
	}
}

func TestQuotaAlloc_BboltMigrateFromJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "quota_allocation.json")
	if err := os.WriteFile(jsonPath, []byte(`{"guest_key_percent":30,"public_key_percent":70}`), 0600); err != nil {
		t.Fatal(err)
	}
	h, cleanup := testAllocBbolt(t, dir)
	defer cleanup()

	am := &AllocationManager{config: DefaultQuotaAllocation(), dataDir: dir}
	if err := am.importToBbolt(h); err != nil {
		t.Fatalf("importToBbolt: %v", err)
	}
	if am.config.GuestKeyPercent != 30 || am.config.PublicKeyPercent != 70 {
		t.Fatalf("config = %+v, want 30/70", am.config)
	}
	if !h.isMigrated("quota_alloc") {
		t.Fatal("migrated marker not set")
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatal("original json should be renamed to .bak")
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Fatalf(".bak missing: %v", err)
	}
	// 幂等：再跑一次不报错不丢数据
	if err := am.importToBbolt(h); err != nil {
		t.Fatalf("second importToBbolt: %v", err)
	}
	if am.config.GuestKeyPercent != 30 {
		t.Fatalf("config lost after re-import: %+v", am.config)
	}
}

func TestQuotaAlloc_BboltMigrateV3Format(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "quota_allocation.json")
	// v3 遗留格式
	if err := os.WriteFile(jsonPath, []byte(`{"free_consumer_percent":40,"network_node_percent":60}`), 0600); err != nil {
		t.Fatal(err)
	}
	h, cleanup := testAllocBbolt(t, dir)
	defer cleanup()

	am := &AllocationManager{config: DefaultQuotaAllocation(), dataDir: dir}
	if err := am.importToBbolt(h); err != nil {
		t.Fatalf("importToBbolt: %v", err)
	}
	// v3: public=free_consumer(40), guest=100-40=60
	if am.config.GuestKeyPercent != 60 || am.config.PublicKeyPercent != 40 {
		t.Fatalf("v3 migrated config = %+v, want 60/40", am.config)
	}
}

func TestQuotaAlloc_BboltFreshNode(t *testing.T) {
	dir := t.TempDir()
	h, cleanup := testAllocBbolt(t, dir)
	defer cleanup()

	am := &AllocationManager{config: DefaultQuotaAllocation(), dataDir: dir}
	if err := am.importToBbolt(h); err != nil {
		t.Fatalf("importToBbolt: %v", err)
	}
	if am.config != DefaultQuotaAllocation() {
		t.Fatalf("fresh node config = %+v, want default", am.config)
	}
	if !h.isMigrated("quota_alloc") {
		t.Fatal("migrated marker not set for fresh node")
	}
	// 无 JSON 文件时不应产生 .bak
	if _, err := os.Stat(filepath.Join(dir, "quota_allocation.json.bak")); !os.IsNotExist(err) {
		t.Fatal("unexpected .bak for fresh node")
	}
}

func TestQuotaAlloc_BboltSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	h, cleanup := testAllocBbolt(t, dir)
	defer cleanup()

	am := &AllocationManager{config: DefaultQuotaAllocation(), dataDir: dir}
	am.bbolt = h // 走 bbolt 后端
	if err := am.SetAllocation(25); err != nil {
		t.Fatalf("SetAllocation: %v", err)
	}
	// 新实例从 bbolt 读回
	am2 := &AllocationManager{config: DefaultQuotaAllocation(), dataDir: dir}
	am2.loadBbolt(h)
	if am2.config.GuestKeyPercent != 25 || am2.config.PublicKeyPercent != 75 {
		t.Fatalf("round-trip config = %+v, want 25/75", am2.config)
	}
	assertFileMode0600(t, filepath.Join(dir, bboltFileName))
}

func TestQuotaAlloc_JSONFallbackStillWorks(t *testing.T) {
	dir := t.TempDir()
	// am.bbolt 保持 nil：回滚路径走 JSON
	am := &AllocationManager{config: DefaultQuotaAllocation(), dataDir: dir}
	if err := am.SetAllocation(35); err != nil {
		t.Fatalf("SetAllocation: %v", err)
	}
	jsonPath := filepath.Join(dir, "quota_allocation.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("json fallback did not write file: %v", err)
	}
	assertFileMode0600(t, jsonPath)
	am2 := &AllocationManager{config: DefaultQuotaAllocation(), dataDir: dir}
	am2.load()
	if am2.config.GuestKeyPercent != 35 {
		t.Fatalf("json fallback round-trip = %+v, want 35", am2.config)
	}
}
