package main

import (
	"runtime"
	"testing"
)

// ============================================================
// getMemoryUsage tests
// ============================================================

func TestGetMemoryUsage(t *testing.T) {
	stats := getMemoryUsage()

	if stats.NumGoroutine <= 0 {
		t.Error("NumGoroutine should be at least 1 (test goroutine)")
	}
	if stats.NumGC < 0 {
		t.Error("NumGC should not be negative")
	}
	if stats.AllocMB < 0 {
		t.Error("AllocMB should not be negative")
	}
	if stats.SysMB <= 0 {
		t.Error("SysMB should be positive")
	}
}

func TestGetMemoryUsage_Fields(t *testing.T) {
	stats := getMemoryUsage()

	// Verify all fields are populated with sane values
	if stats.TotalAllocMB < stats.AllocMB {
		t.Errorf("TotalAllocMB (%d) should be >= AllocMB (%d)", stats.TotalAllocMB, stats.AllocMB)
	}
}

func TestMemoryStats_Type(t *testing.T) {
	stats := getMemoryUsage()
	// Type assertions - these should always work
	_ = stats.AllocMB
	_ = stats.TotalAllocMB
	_ = stats.SysMB
	_ = stats.NumGC
	_ = stats.NumGoroutine
}

// ============================================================
// initSharedHTTPClient tests
// ============================================================

func TestInitSharedHTTPClient(t *testing.T) {
	// Save original
	orig := internalHTTPClient
	defer func() { internalHTTPClient = orig }()

	initSharedHTTPClient()

	if internalHTTPClient == nil {
		t.Fatal("initSharedHTTPClient did not set internalHTTPClient")
	}
	if internalHTTPClient.Transport == nil {
		t.Error("internalHTTPClient should have a Transport")
	}
}

func TestGetSharedHTTPClient(t *testing.T) {
	orig := internalHTTPClient
	internalHTTPClient = nil
	defer func() { internalHTTPClient = orig }()

	client := GetSharedHTTPClient()
	if client == nil {
		t.Fatal("GetSharedHTTPClient returned nil")
	}
	if internalHTTPClient == nil {
		t.Error("GetSharedHTTPClient should initialize internalHTTPClient if nil")
	}
}

func TestGetSharedHTTPClient_Cached(t *testing.T) {
	client1 := GetSharedHTTPClient()
	client2 := GetSharedHTTPClient()
	if client1 != client2 {
		t.Error("GetSharedHTTPClient should return the same cached client")
	}
}

// ============================================================
// runtime.MemStats validation
// ============================================================

func TestMemStats_AfterAllocation(t *testing.T) {
	before := getMemoryUsage()

	// Allocate some memory to verify tracking works
	data := make([]byte, 10*1024*1024) // 10MB
	runtime.KeepAlive(data)

	after := getMemoryUsage()

	if after.TotalAllocMB < before.TotalAllocMB {
		t.Errorf("TotalAllocMB should increase after allocation: before=%d, after=%d",
			before.TotalAllocMB, after.TotalAllocMB)
	}
}

func TestGetMemoryUsage_Concurrent(t *testing.T) {
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			stats := getMemoryUsage()
			if stats.NumGoroutine <= 0 {
				t.Error("NumGoroutine should be positive in concurrent access")
			}
			done <- true
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
