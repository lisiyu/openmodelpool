package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// ============================================================
// Quota Allocation Manager (v4.0 — Guest/Public Key Pool Model)
// ============================================================
//
// The v4.0 model uses a percentage-based allocation:
// - GuestKeyPercent: portion of resources contributed to the Guest Key Pool
// - PublicKeyPercent: portion of resources contributed to the Public Key Pool (100 - GuestKeyPercent)
// Each node configures its own allocation ratio.

// QuotaAllocation defines how a node splits its resources.
type QuotaAllocation struct {
	GuestKeyPercent  int `json:"guest_key_percent"`  // 0-100, 贡献给 Guest Key Pool 的比例
	PublicKeyPercent   int `json:"public_key_percent"` // 100 - GuestKeyPercent, 贡献给 Public Key Pool 的比例
}

// DefaultQuotaAllocation returns the default allocation (50/50).
func DefaultQuotaAllocation() QuotaAllocation {
	return QuotaAllocation{
		GuestKeyPercent: 50,
		PublicKeyPercent:  50,
	}
}

// ============================================================
// Allocation Manager (manages runtime quota tracking)
// ============================================================

type AllocationManager struct {
	mu          sync.RWMutex
	config      QuotaAllocation
	dataDir     string
	usedGuest   int64 // tokens used via Guest Keys this period
	usedPublic  int64 // tokens used via Public Keys this period
}

var allocMgr *AllocationManager

func initAllocationManager(dataDir string) {
	allocMgr = &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dataDir,
	}
	allocMgr.load()
	slog.Info("allocation manager initialized",
		"guest_key_percent", allocMgr.config.GuestKeyPercent,
		"public_key_percent", allocMgr.config.PublicKeyPercent)
}

// GetAllocation returns the current allocation config.
func (am *AllocationManager) GetAllocation() QuotaAllocation {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.config
}

// SetAllocation updates the allocation config.
func (am *AllocationManager) SetAllocation(guestKeyPercent int) error {
	if guestKeyPercent < 0 || guestKeyPercent > 100 {
		return fmt.Errorf("guest_key_percent must be between 0 and 100")
	}
	am.mu.Lock()
	am.config.GuestKeyPercent = guestKeyPercent
	am.config.PublicKeyPercent = 100 - guestKeyPercent
	am.mu.Unlock()
	am.save()
	slog.Info("quota allocation updated", "guest_key_percent", guestKeyPercent, "public_key_percent", 100-guestKeyPercent)
	return nil
}

// RecordUsage records token usage against the appropriate pool.
func (am *AllocationManager) RecordUsage(isGuestKey bool, tokens int64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if isGuestKey {
		am.usedGuest += tokens
	} else {
		am.usedPublic += tokens
	}
}

// GetUsageStats returns current usage statistics.
func (am *AllocationManager) GetUsageStats() map[string]any {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return map[string]any{
		"guest_key_percent":  am.config.GuestKeyPercent,
		"public_key_percent": am.config.PublicKeyPercent,
		"used_guest_tokens":  am.usedGuest,
		"used_public_tokens": am.usedPublic,
	}
}

// ============================================================
// Persistence
// ============================================================

func (am *AllocationManager) save() {
	path := filepath.Join(am.dataDir, "quota_allocation.json")
	am.mu.RLock()
	data, err := json.MarshalIndent(am.config, "", "  ")
	am.mu.RUnlock()
	if err != nil {
		slog.Error("failed to marshal quota allocation", "error", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		slog.Error("failed to write quota allocation", "error", err)
	}
}

// rawQuotaAllocation is used for backward-compatible loading.
type rawQuotaAllocation struct {
	GuestKeyPercent     *int `json:"guest_key_percent"`
	PublicKeyPercent    *int `json:"public_key_percent"`
	FreeConsumerPercent *int `json:"free_consumer_percent"`     // v3 legacy
	NetworkNodePercent  *int `json:"network_node_percent"`      // v3 legacy
}

func (am *AllocationManager) load() {
	path := filepath.Join(am.dataDir, "quota_allocation.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to read quota allocation", "error", err)
		}
		return
	}

	// First try to parse with backward compatibility
	var rawCfg rawQuotaAllocation
	if err := json.Unmarshal(raw, &rawCfg); err != nil {
		slog.Error("failed to unmarshal quota allocation", "error", err)
		return
	}

	var config QuotaAllocation

	if rawCfg.GuestKeyPercent != nil && rawCfg.PublicKeyPercent != nil {
		// New format (v4.0+)
		config.GuestKeyPercent = *rawCfg.GuestKeyPercent
		config.PublicKeyPercent = *rawCfg.PublicKeyPercent
	} else if rawCfg.FreeConsumerPercent != nil {
		// Legacy format (v3): map free_consumer_percent → public_key_percent
		// In old model: free_consumer was for free users, network_node was for other nodes' keys
		// In new model: guest_key_percent maps to (100 - free_consumer_percent)
		// and public_key_percent maps to free_consumer_percent
		config.PublicKeyPercent = *rawCfg.FreeConsumerPercent
		config.GuestKeyPercent = 100 - *rawCfg.FreeConsumerPercent
		slog.Info("migrated quota allocation from v3 format",
			"old_free_consumer_percent", *rawCfg.FreeConsumerPercent,
			"new_guest_key_percent", config.GuestKeyPercent,
			"new_public_key_percent", config.PublicKeyPercent)
	} else {
		config = DefaultQuotaAllocation()
	}

	if config.GuestKeyPercent < 0 || config.GuestKeyPercent > 100 {
		config = DefaultQuotaAllocation()
	}
	config.PublicKeyPercent = 100 - config.GuestKeyPercent
	am.config = config
}
