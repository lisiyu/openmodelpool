package main

import (
	"testing"
	"time"
)

// ============================================================
// Health checker tests
// ============================================================

func TestHealthChecker_Init(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Need to initialize health checker for tests
	hc := &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
		interval: 5 * time.Minute,
		stopCh:   make(chan struct{}),
	}
	defer close(hc.stopCh)

	if hc.statuses == nil {
		t.Fatal("statuses should be initialized")
	}
}

func TestHealthChecker_GetHealth_Empty(t *testing.T) {
	hc := &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
		stopCh:   make(chan struct{}),
	}
	defer close(hc.stopCh)

	health := hc.GetHealth()
	if len(health) != 0 {
		t.Fatalf("expected 0 health statuses, got %d", len(health))
	}
}

func TestHealthChecker_GetHealth_WithStatuses(t *testing.T) {
	hc := &HealthChecker{
		statuses: map[string]*ProviderHealth{
			"p1": {ProviderID: "p1", ProviderName: "Test1", Status: "healthy"},
			"p2": {ProviderID: "p2", ProviderName: "Test2", Status: "down"},
		},
		stopCh: make(chan struct{}),
	}
	defer close(hc.stopCh)

	health := hc.GetHealth()
	if len(health) != 2 {
		t.Fatalf("expected 2 health statuses, got %d", len(health))
	}
}

func TestHealthChecker_IsHealthy(t *testing.T) {
	hc := &HealthChecker{
		statuses: map[string]*ProviderHealth{
			"healthy-p": {ProviderID: "healthy-p", Status: "healthy"},
			"degraded-p": {ProviderID: "degraded-p", Status: "degraded"},
			"down-p": {ProviderID: "down-p", Status: "down"},
		},
		stopCh: make(chan struct{}),
	}
	defer close(hc.stopCh)

	if !hc.IsHealthy("healthy-p") {
		t.Fatal("healthy provider should be healthy")
	}
	if !hc.IsHealthy("degraded-p") {
		t.Fatal("degraded provider should still be considered healthy (not down)")
	}
	if hc.IsHealthy("down-p") {
		t.Fatal("down provider should not be healthy")
	}
	// Unknown provider assumed healthy
	if !hc.IsHealthy("unknown-p") {
		t.Fatal("unknown provider should default to healthy")
	}
}

func TestHealthChecker_Stop(t *testing.T) {
	hc := &HealthChecker{
		statuses: make(map[string]*ProviderHealth),
		interval: 1 * time.Hour,
		stopCh:   make(chan struct{}),
	}
	// Just test that stop doesn't panic
	hc.stop()
}

func TestProviderHealth_Fields(t *testing.T) {
	ph := ProviderHealth{
		ProviderID:       "test",
		ProviderName:     "Test Provider",
		Status:           "healthy",
		LastCheck:        time.Now().Format(time.RFC3339),
		LatencyMS:        150.5,
		ConsecutiveFails: 0,
	}
	if ph.ProviderID != "test" {
		t.Fatal("ProviderID mismatch")
	}
	if ph.Status != "healthy" {
		t.Fatal("Status mismatch")
	}
	if ph.LatencyMS != 150.5 {
		t.Fatal("LatencyMS mismatch")
	}
}
