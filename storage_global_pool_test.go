package main

// G4: global_pool 域 bbolt 后端测试。

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testGPoolBbolt(t *testing.T, dir string) (*bboltHandle, func()) {
	t.Helper()
	h, err := openBbolt(dir)
	if err != nil {
		t.Fatalf("openBbolt: %v", err)
	}
	return h, func() {
		closeBbolt(dir)
	}
}

func newTestGlobalPoolWithData(dir string) *GlobalPool {
	return &GlobalPool{
		NodeContributions: map[string]int64{"node-a": 20000},
		NodeConsumptions:  map[string]int64{"node-a": 1000},
		ParticipantNodes:  []GlobalPoolNode{{NodeID: "node-a", Region: "cn", Contributed: 20000, Status: "active"}},
		TotalContributed:  20000,
		TotalConsumed:     1000,
		AvailableQuota:    19000,
		LastUpdated:       time.Now().Truncate(time.Second),
		dataPath:          filepath.Join(dir, "global_pool.json"),
	}
}

func TestGPool_BboltMigrateFromJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "global_pool.json")

	// JSON 路径写一份真实数据
	gp := newTestGlobalPoolWithData(dir)
	gp.writeStore(gp.snapshotStore())
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("json not written: %v", err)
	}

	h, cleanup := testGPoolBbolt(t, dir)
	defer cleanup()

	gp2 := newTestGlobalPoolWithData(dir)
	// 清空内存，模拟重启后迁移
	gp2.NodeContributions = make(map[string]int64)
	gp2.TotalContributed = 0
	if err := gp2.importToBbolt(h); err != nil {
		t.Fatalf("importToBbolt: %v", err)
	}
	if gp2.TotalContributed != 20000 || gp2.NodeContributions["node-a"] != 20000 {
		t.Fatalf("migrated = %+v", gp2)
	}
	if len(gp2.ParticipantNodes) != 1 || gp2.ParticipantNodes[0].NodeID != "node-a" {
		t.Fatalf("participants lost: %+v", gp2.ParticipantNodes)
	}
	if !h.isMigrated("global_pool") {
		t.Fatal("migrated marker not set")
	}
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Fatalf(".bak missing: %v", err)
	}
}

func TestGPool_BboltSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "global_pool.json")
	h, cleanup := testGPoolBbolt(t, dir)
	defer cleanup()

	gp := newTestGlobalPoolWithData(dir)
	gp.bbolt = h // 走 bbolt 后端
	gp.save()    // bbolt 分支
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatal("json file should not be written in bbolt mode")
	}
	gp2 := newTestGlobalPoolWithData(dir)
	gp2.loadBbolt(h)
	if gp2.TotalContributed != 20000 || gp2.AvailableQuota != 19000 {
		t.Fatalf("round-trip = %+v", gp2)
	}
	if gp2.NodeConsumptions["node-a"] != 1000 {
		t.Fatalf("consumptions lost: %+v", gp2.NodeConsumptions)
	}
	assertFileMode0600(t, filepath.Join(dir, bboltFileName))
}

func TestGPool_JSONFallbackStillWorks(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "global_pool.json")
	gp := newTestGlobalPoolWithData(dir)
	gp.save()
	assertFileMode0600(t, jsonPath)
	gp2 := newTestGlobalPoolWithData(dir)
	gp2.TotalContributed = 0
	gp2.load()
	if gp2.TotalContributed != 20000 {
		t.Fatalf("json fallback round-trip failed: %+v", gp2)
	}
}
