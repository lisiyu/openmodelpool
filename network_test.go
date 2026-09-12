package main

import (
	"testing"
	"time"
)

// ============================================================
// NetworkMode / Config / Struct Tests
// ============================================================

func TestNetworkModeConstants(t *testing.T) {
	if NetworkModePersonal != "personal" {
		t.Fatalf("NetworkModePersonal = %q, want personal", NetworkModePersonal)
	}
	if NetworkModeShared != "shared" {
		t.Fatalf("NetworkModeShared = %q, want shared", NetworkModeShared)
	}
}

func TestShareBoundaryConfigDefaults(t *testing.T) {
	cfg := ShareBoundaryConfig{}
	if cfg.DailyContribCap != 0 {
		t.Fatalf("DailyContribCap = %d, want 0", cfg.DailyContribCap)
	}
	if cfg.ShareIdleOnly != false {
		t.Fatalf("ShareIdleOnly = %v, want false", cfg.ShareIdleOnly)
	}
	if len(cfg.ModelWhitelist) != 0 {
		t.Fatalf("ModelWhitelist len = %d, want 0", len(cfg.ModelWhitelist))
	}
}

func TestNetworkConfigStruct(t *testing.T) {
	nc := NetworkConfig{
		Mode:           NetworkModePersonal,
		NetworkEnabled: false,
		ShareToPool:    false,
		ShareBoundary:  ShareBoundaryConfig{DailyContribCap: 1000, ShareIdleOnly: true},
	}
	if nc.Mode != NetworkModePersonal {
		t.Fatalf("Mode = %v", nc.Mode)
	}
	if nc.ShareBoundary.DailyContribCap != 1000 {
		t.Fatalf("DailyContribCap = %d", nc.ShareBoundary.DailyContribCap)
	}
}

func TestContribRecord(t *testing.T) {
	now := time.Now()
	rec := ContribRecord{
		Timestamp:  now.Format(time.RFC3339),
		TokensUsed: 42,
		Requests:   5,
		FromNodeID: "mmx-test",
	}
	if rec.TokensUsed != 42 {
		t.Fatalf("TokensUsed = %d", rec.TokensUsed)
	}
	if rec.Requests != 5 {
		t.Fatalf("Requests = %d", rec.Requests)
	}
	if rec.FromNodeID != "mmx-test" {
		t.Fatalf("FromNodeID = %q", rec.FromNodeID)
	}
}

// ============================================================
// GossipManager Constants / Init Tests
// ============================================================

func TestGossipManagerInit(t *testing.T) {
	g := &GossipManager{
		seen: make(map[string]time.Time),
	}
	if g == nil {
		t.Fatal("GossipManager init returned nil")
	}
	if len(g.seen) != 0 {
		t.Fatal("seen map should be empty after init")
	}
}
