package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// DefaultQuotaAllocation tests
// ============================================================

func TestDefaultQuotaAllocation(t *testing.T) {
	q := DefaultQuotaAllocation()
	if q.GuestKeyPercent != 50 {
		t.Errorf("GuestKeyPercent = %d, want 50", q.GuestKeyPercent)
	}
	if q.PublicKeyPercent != 50 {
		t.Errorf("PublicKeyPercent = %d, want 50", q.PublicKeyPercent)
	}
}

// ============================================================
// AllocationManager tests
// ============================================================

func TestAllocationManager_GetAllocation(t *testing.T) {
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: "/tmp",
	}

	alloc := am.GetAllocation()
	if alloc.GuestKeyPercent != 50 {
		t.Errorf("GuestKeyPercent = %d, want 50", alloc.GuestKeyPercent)
	}
}

func TestAllocationManager_SetAllocation_Valid(t *testing.T) {
	dir := t.TempDir()
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}

	err := am.SetAllocation(30)
	if err != nil {
		t.Fatalf("SetAllocation(30) unexpected error: %v", err)
	}

	alloc := am.GetAllocation()
	if alloc.GuestKeyPercent != 30 {
		t.Errorf("GuestKeyPercent = %d, want 30", alloc.GuestKeyPercent)
	}
	if alloc.PublicKeyPercent != 70 {
		t.Errorf("PublicKeyPercent = %d, want 70", alloc.PublicKeyPercent)
	}
}

func TestAllocationManager_SetAllocation_Boundaries(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		input   int
		wantErr bool
	}{
		{"zero", 0, false},
		{"hundred", 100, false},
		{"fifty", 50, false},
		{"negative", -1, true},
		{"over hundred", 101, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			am := &AllocationManager{
				config:  DefaultQuotaAllocation(),
				dataDir: dir,
			}
			err := am.SetAllocation(tt.input)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for input %d", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for input %d: %v", tt.input, err)
			}
		})
	}
}

func TestAllocationManager_RecordUsage_Guest(t *testing.T) {
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: "/tmp",
	}

	am.RecordUsage(true, 1000)
	am.RecordUsage(true, 500)

	stats := am.GetUsageStats()
	if stats["used_guest_tokens"].(int64) != 1500 {
		t.Errorf("used_guest_tokens = %v, want 1500", stats["used_guest_tokens"])
	}
	if stats["used_public_tokens"].(int64) != 0 {
		t.Errorf("used_public_tokens = %v, want 0", stats["used_public_tokens"])
	}
}

func TestAllocationManager_RecordUsage_Public(t *testing.T) {
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: "/tmp",
	}

	am.RecordUsage(false, 2000)

	stats := am.GetUsageStats()
	if stats["used_public_tokens"].(int64) != 2000 {
		t.Errorf("used_public_tokens = %v, want 2000", stats["used_public_tokens"])
	}
	if stats["used_guest_tokens"].(int64) != 0 {
		t.Errorf("used_guest_tokens = %v, want 0", stats["used_guest_tokens"])
	}
}

func TestAllocationManager_GetUsageStats(t *testing.T) {
	am := &AllocationManager{
		config: DefaultQuotaAllocation(),
	}

	stats := am.GetUsageStats()
	if stats["guest_key_percent"].(int) != 50 {
		t.Errorf("guest_key_percent = %v, want 50", stats["guest_key_percent"])
	}
	if stats["public_key_percent"].(int) != 50 {
		t.Errorf("public_key_percent = %v, want 50", stats["public_key_percent"])
	}
}

func TestAllocationManager_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota_allocation.json")

	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}
	am.SetAllocation(25)
	am.RecordUsage(true, 100)

	// Save
	am.save()

	// Verify file exists and has content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("save did not write file: %v", err)
	}

	var readConfig QuotaAllocation
	if err := json.Unmarshal(data, &readConfig); err != nil {
		t.Fatalf("invalid JSON in saved file: %v", err)
	}
	if readConfig.GuestKeyPercent != 25 {
		t.Errorf("saved GuestKeyPercent = %d, want 25", readConfig.GuestKeyPercent)
	}
}

func TestAllocationManager_Load_LegacyFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota_allocation.json")

	// Write legacy format
	legacyData := `{"free_consumer_percent":40}`
	os.WriteFile(path, []byte(legacyData), 0644)

	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}
	am.load()

	alloc := am.GetAllocation()
	if alloc.PublicKeyPercent != 40 {
		t.Errorf("PublicKeyPercent = %d, want 40 (migrated from free_consumer_percent)", alloc.PublicKeyPercent)
	}
	if alloc.GuestKeyPercent != 60 {
		t.Errorf("GuestKeyPercent = %d, want 60", alloc.GuestKeyPercent)
	}
}

func TestAllocationManager_Load_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}
	am.load()

	// Should keep defaults
	alloc := am.GetAllocation()
	if alloc.GuestKeyPercent != 50 {
		t.Errorf("GuestKeyPercent = %d, want 50 (default)", alloc.GuestKeyPercent)
	}
}

func TestInitAllocationManager(t *testing.T) {
	orig := allocMgr
	defer func() { allocMgr = orig }()

	dir := t.TempDir()
	initAllocationManager(dir)

	if allocMgr == nil {
		t.Fatal("initAllocationManager did not set allocMgr")
	}
	alloc := allocMgr.GetAllocation()
	if alloc.GuestKeyPercent != 50 {
		t.Errorf("initial GuestKeyPercent = %d, want 50", alloc.GuestKeyPercent)
	}
}

func TestAllocationManager_Concurrent(t *testing.T) {
	dir := t.TempDir()
	am := &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dir,
	}

	done := make(chan bool)
	for i := 0; i < 20; i++ {
		go func(id int) {
			am.RecordUsage(id%2 == 0, 100)
			am.GetUsageStats()
			done <- true
		}(i)
	}
	for i := 0; i < 20; i++ {
		<-done
	}
}
