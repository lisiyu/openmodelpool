package main

import (
	"testing"
)

// TestSharedNetworkSoftReminder_PersonalWithIdle verifies the governance
// soft-reminder: a personal-mode node that still has idle OWN capacity gets a
// non-blocking "shared_network_suggestion" in its status; once it joins the
// shared network the nag disappears. The community free pool is always
// available and is NOT part of this suggestion.
func TestSharedNetworkSoftReminder_PersonalWithIdle(t *testing.T) {
	origNetMgr := netMgr
	origPm := pm
	origRT := routeTable
	defer func() {
		netMgr = origNetMgr
		pm = origPm
		routeTable = origRT
	}()

	initProviderManager(t.TempDir())
	routeTable = initRouteTable()

	// Node in personal mode (not joined to shared network).
	netMgr = &NetworkManager{config: NetworkConfig{
		Mode: NetworkModePersonal, NetworkEnabled: false, NodeID: "remind-node",
	}}

	// Provider with idle own-capacity (TokenLimit set, no usage).
	p := makeProvider("p-idle", "Idle Provider", makeModelDef("gpt-4o"), 5, true)
	p.TokenLimit = 100000
	pm.Add(p)

	status := netMgr.GetStatus()
	raw, ok := status["shared_network_suggestion"]
	if !ok {
		t.Fatalf("personal mode with idle capacity should include shared_network_suggestion, got keys only")
	}
	sug, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("shared_network_suggestion should be a map, got %T", raw)
	}
	if sug["should_join"] != true {
		t.Errorf("should_join should be true, got %v", sug["should_join"])
	}

	// Once joined (network_enabled=true), the nag must stop.
	netMgr.config.NetworkEnabled = true
	status2 := netMgr.GetStatus()
	if _, ok := status2["shared_network_suggestion"]; ok {
		t.Errorf("joined node must NOT get shared_network_suggestion nag")
	}
}

// TestSharedNetworkSoftReminder_NoOwnCapacity verifies that a personal-mode node
// with NO own capacity (only the community free pool) does NOT get the join nag —
// it has nothing extra to contribute, and the free pool is already a commons.
func TestSharedNetworkSoftReminder_NoOwnCapacity(t *testing.T) {
	origNetMgr := netMgr
	origPm := pm
	origRT := routeTable
	defer func() {
		netMgr = origNetMgr
		pm = origPm
		routeTable = origRT
	}()

	initProviderManager(t.TempDir())
	routeTable = initRouteTable()

	netMgr = &NetworkManager{config: NetworkConfig{
		Mode: NetworkModePersonal, NetworkEnabled: false, NodeID: "free-only-node",
	}}
	// No own providers added → only community free pool available.

	status := netMgr.GetStatus()
	if _, ok := status["shared_network_suggestion"]; ok {
		t.Errorf("node with no own capacity should NOT be nagged to join")
	}
}
