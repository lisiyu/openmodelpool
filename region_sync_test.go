package main

import (
	"testing"
	"time"
)

// These tests exercise reconcileRegionsOnce directly, passing an injected
// clock (now) and an explicit seen map so nothing has to sleep or touch the
// network. They lock down the three behaviours the real loop exists for:
// filling gaps, never pruning on an untrustworthy (empty) view, and pruning
// only genuinely departed nodes — never this node itself.

func TestReconcileFillsGapFromPoolReport(t *testing.T) {
	rm := NewRegionManager()
	// Peer we know about from the pool self-report, but whose region the
	// RegionManager does not yet have.
	known := map[string]knownNode{
		"peer-ap": {Region: "ap"}, // raw "ap" -> RegionAsiaPacific
	}
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	filled, pruned := reconcileRegionsOnce(rm, known, regionSeenAt{}, now)
	if filled != 1 || pruned != 0 {
		t.Fatalf("expected fill=1 prune=0, got fill=%d prune=%d", filled, pruned)
	}
	got := rm.GetNodeRegion("peer-ap")
	if got == nil {
		t.Fatal("peer-ap region was not filled in")
	}
	if got.Region != RegionAsiaPacific {
		t.Errorf("peer-ap region = %q, want ap", got.Region)
	}
	if got.Source != "reconcile_pool_report" {
		t.Errorf("peer-ap source = %q, want reconcile_pool_report", got.Source)
	}
}

func TestReconcileFillsGapFromIPDetect(t *testing.T) {
	rm := NewRegionManager()
	// No pool region reported, but an endpoint that is a literal IP we can
	// classify. 198.x is in the Americas range.
	known := map[string]knownNode{
		"peer-ip": {Region: "", Endpoint: "https://198.51.100.1:8000"},
	}
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)

	filled, pruned := reconcileRegionsOnce(rm, known, regionSeenAt{}, now)
	if filled != 1 || pruned != 0 {
		t.Fatalf("expected fill=1 prune=0, got fill=%d prune=%d", filled, pruned)
	}
	got := rm.GetNodeRegion("peer-ip")
	if got == nil {
		t.Fatal("peer-ip region was not filled in")
	}
	if got.Region != RegionAmericas {
		t.Errorf("peer-ip region = %q, want americas", got.Region)
	}
	if got.Source != "reconcile_addr" {
		t.Errorf("peer-ip source = %q, want reconcile_addr", got.Source)
	}
}

func TestReconcileEmptyKnownMapDisablesPruning(t *testing.T) {
	rm := NewRegionManager()
	// A stale entry that, under a non-empty view, would be pruned.
	rm.mu.Lock()
	rm.nodes["ghost"] = &NodeRegion{Region: RegionEurope, Source: "heartbeat"}
	rm.mu.Unlock()

	// A transient empty view must NOT wipe the table: pruning is disabled.
	prevTTL := regionEntryTTL
	regionEntryTTL = time.Millisecond
	defer func() { regionEntryTTL = prevTTL }()

	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	seen := regionSeenAt{"ghost": now.Add(-time.Hour)} // aged out

	filled, pruned := reconcileRegionsOnce(rm, map[string]knownNode{}, seen, now)
	if filled != 0 || pruned != 0 {
		t.Fatalf("empty view must prune nothing: got fill=%d prune=%d", filled, pruned)
	}
	if rm.GetNodeRegion("ghost") == nil {
		t.Error("stale entry was pruned on an empty (untrustworthy) view")
	}
}

func TestReconcilePrunesStaleUnknownNode(t *testing.T) {
	rm := NewRegionManager()
	rm.mu.Lock()
	rm.nodes["departed"] = &NodeRegion{Region: RegionEurope, Source: "heartbeat"}
	// Pre-register the still-known peer so the fill step has nothing to do;
	// this isolates the prune assertion.
	rm.nodes["peer-alive"] = &NodeRegion{Region: RegionEurope, Source: "heartbeat"}
	rm.mu.Unlock()

	prevTTL := regionEntryTTL
	regionEntryTTL = time.Millisecond
	defer func() { regionEntryTTL = prevTTL }()

	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	seen := regionSeenAt{"departed": now.Add(-time.Hour)} // well past TTL

	// A non-empty view (the reconciler still knows about real peers) enables
	// pruning; it simply does not include the departed node.
	known := map[string]knownNode{"peer-alive": {Region: "eu"}}
	filled, pruned := reconcileRegionsOnce(rm, known, seen, now)
	if filled != 0 {
		t.Fatalf("expected fill=0 (peer pre-registered), got %d", filled)
	}
	if pruned != 1 {
		t.Fatalf("expected prune=1 for the departed node, got %d", pruned)
	}
	if rm.GetNodeRegion("departed") != nil {
		t.Error("departed node should have been pruned")
	}
}

func TestReconcileKeepsSelfEvenWhenStale(t *testing.T) {
	env := setupTestEnv(t)
	prevNode := node
	node = newTestNodeIdentity(t, env.dir)
	t.Cleanup(func() { node = prevNode })

	selfID := node.NodeID()

	rm := NewRegionManager()
	rm.mu.Lock()
	rm.nodes[selfID] = &NodeRegion{Region: RegionAsiaPacific, Source: "auto_detect"}
	// A genuine departed node that is NOT self.
	rm.nodes["departed"] = &NodeRegion{Region: RegionEurope, Source: "heartbeat"}
	rm.mu.Unlock()

	prevTTL := regionEntryTTL
	regionEntryTTL = time.Millisecond
	defer func() { regionEntryTTL = prevTTL }()

	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	seen := regionSeenAt{
		selfID:    now.Add(-time.Hour), // stale self
		"departed": now.Add(-time.Hour), // stale other
	}

	// `known` does not include either — only the empty view would matter, but
	// here the view is non-empty (the reconciler knows about real peers), so
	// pruning is enabled. Self must survive regardless.
	known := map[string]knownNode{"peer-alive": {Region: "eu"}}
	filled, pruned := reconcileRegionsOnce(rm, known, seen, now)

	if rm.GetNodeRegion(selfID) == nil {
		t.Error("self node was pruned — must never happen")
	}
	if rm.GetNodeRegion("departed") != nil {
		t.Error("departed node should have been pruned")
	}
	// Only the departed node is pruned; self is protected.
	if pruned != 1 {
		t.Fatalf("expected prune=1 (self protected), got %d", pruned)
	}
	_ = filled
}

func TestReconcileKeepsKnownNodes(t *testing.T) {
	rm := NewRegionManager()
	rm.mu.Lock()
	rm.nodes["alive"] = &NodeRegion{Region: RegionAmericas, Source: "heartbeat"}
	rm.mu.Unlock()

	prevTTL := regionEntryTTL
	regionEntryTTL = time.Millisecond
	defer func() { regionEntryTTL = prevTTL }()

	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	// Pre-stamp old so it would be pruned IF it were not still known.
	seen := regionSeenAt{"alive": now.Add(-time.Hour)}

	known := map[string]knownNode{"alive": {Region: "us"}}
	filled, pruned := reconcileRegionsOnce(rm, known, seen, now)
	if filled != 0 || pruned != 0 {
		t.Fatalf("known node must be kept: got fill=%d prune=%d", filled, pruned)
	}
	if rm.GetNodeRegion("alive") == nil {
		t.Error("known node was wrongly pruned")
	}
}

func TestHostFromEndpoint(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://198.51.100.1:8000/path", "198.51.100.1"},
		{"http://example.com:9000", "example.com"},
		{"10.0.0.5:7000", "10.0.0.5"},
		{"host-only", "host-only"},
		{"", ""},
	}
	for _, c := range cases {
		if got := hostFromEndpoint(c.in); got != c.want {
			t.Errorf("hostFromEndpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
