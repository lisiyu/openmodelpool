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
// Quota Allocation Manager (replaces Credits system in v2.0)
// ============================================================
//
// The new model uses a simple percentage-based allocation:
// - FreeConsumerPercent: portion of resources available to free consumers
// - NetworkNodePercent: portion available to network nodes (100 - FreeConsumerPercent)
// Each node configures its own allocation ratio.

// QuotaAllocation defines how a node splits its resources.
type QuotaAllocation struct {
	FreeConsumerPercent int `json:"free_consumer_percent"` // 0-100
	NetworkNodePercent  int `json:"network_node_percent"`  // 100 - FreeConsumerPercent
}

// DefaultQuotaAllocation returns the default allocation (50/50).
func DefaultQuotaAllocation() QuotaAllocation {
	return QuotaAllocation{
		FreeConsumerPercent: 50,
		NetworkNodePercent:  50,
	}
}

// ============================================================
// Allocation Manager (manages runtime quota tracking)
// ============================================================

type AllocationManager struct {
	mu       sync.RWMutex
	config   QuotaAllocation
	dataDir  string
	usedFree int64 // tokens used by free consumers this period
	usedNet  int64 // tokens used by network nodes this period
}

var allocMgr *AllocationManager

func initAllocationManager(dataDir string) {
	allocMgr = &AllocationManager{
		config:  DefaultQuotaAllocation(),
		dataDir: dataDir,
	}
	allocMgr.load()
	slog.Info("allocation manager initialized",
		"free_percent", allocMgr.config.FreeConsumerPercent,
		"network_percent", allocMgr.config.NetworkNodePercent)
}

// GetAllocation returns the current allocation config.
func (am *AllocationManager) GetAllocation() QuotaAllocation {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.config
}

// SetAllocation updates the allocation config.
func (am *AllocationManager) SetAllocation(freePercent int) error {
	if freePercent < 0 || freePercent > 100 {
		return fmt.Errorf("free_consumer_percent must be between 0 and 100")
	}
	am.mu.Lock()
	am.config.FreeConsumerPercent = freePercent
	am.config.NetworkNodePercent = 100 - freePercent
	am.mu.Unlock()
	am.save()
	slog.Info("quota allocation updated", "free_percent", freePercent, "network_percent", 100-freePercent)
	return nil
}

// RecordUsage records token usage against the appropriate pool.
func (am *AllocationManager) RecordUsage(isFreeConsumer bool, tokens int64) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if isFreeConsumer {
		am.usedFree += tokens
	} else {
		am.usedNet += tokens
	}
}

// GetUsageStats returns current usage statistics.
func (am *AllocationManager) GetUsageStats() map[string]any {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return map[string]any{
		"free_consumer_percent": am.config.FreeConsumerPercent,
		"network_node_percent":  am.config.NetworkNodePercent,
		"used_free_tokens":      am.usedFree,
		"used_network_tokens":   am.usedNet,
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

func (am *AllocationManager) load() {
	path := filepath.Join(am.dataDir, "quota_allocation.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to read quota allocation", "error", err)
		}
		return
	}
	var config QuotaAllocation
	if err := json.Unmarshal(raw, &config); err != nil {
		slog.Error("failed to unmarshal quota allocation", "error", err)
		return
	}
	if config.FreeConsumerPercent < 0 || config.FreeConsumerPercent > 100 {
		config = DefaultQuotaAllocation()
	}
	config.NetworkNodePercent = 100 - config.FreeConsumerPercent
	am.config = config
}
