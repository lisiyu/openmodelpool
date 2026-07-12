package main

// This file collects initialization/helper functions that the wiring in
// init.go / handlers.go references but whose real implementations were missing
// from the tree (lost or never committed). They are intentionally minimal
// placeholders so the project compiles; each is marked for follow-up work.

// initEncryptor is a no-op: encryption is initialized via encryptor.go's init().
func initEncryptor(keyPath string) {}

// initWAF initializes the four-layer WAF. The WAF engine is not yet wired into
// the proxy path; this is a placeholder so startup does not fail.
func initWAF(dataDir string) {}

// initRegionManager initializes the region manager. The RegionManager
// implementation currently lives only in the test file (network_region_test.go);
// region-aware routing is therefore not active yet.
func initRegionManager() {}

// startHeartbeatLoop periodically announces this node to peers. Placeholder
// until the heartbeat/region sync layer is implemented.
func startHeartbeatLoop() {}

// startRegionSyncLoop periodically synchronizes region assignments. Placeholder.
func startRegionSyncLoop() {}

// registerWithBootstraps registers this node with bootstrap/seed nodes. Placeholder.
func registerWithBootstraps() {}

// GetDHTStats returns DHT routing-table statistics. DHT (Kademlia) is not yet
// implemented, so this reports a clear "not implemented" status.
func GetDHTStats() map[string]any {
	return map[string]any{
		"enabled": false,
		"note":    "DHT (Kademlia) not yet implemented; discovery uses the seed/gossip layer",
	}
}
