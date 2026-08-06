package main

import (
	"testing"
)

// newTestNetworkManager returns a minimal NetworkManager for use in tests.
func newTestNetworkManager(t *testing.T) *NetworkManager {
	t.Helper()
	return &NetworkManager{config: NetworkConfig{NodeID: "test-node"}}
}
