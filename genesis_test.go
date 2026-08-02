package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// computeNetworkID tests
// ============================================================

func TestComputeNetworkID_Deterministic(t *testing.T) {
	g := GenesisBlock{
		NetworkName: "test-net",
		GenesisNode: "mmx-TEST123",
		CreatedAt:   "2026-01-01T00:00:00Z",
		Version:     1,
	}

	id1 := computeNetworkID(g)
	id2 := computeNetworkID(g)

	if id1 != id2 {
		t.Errorf("computeNetworkID should be deterministic: %s vs %s", id1, id2)
	}
}

func TestComputeNetworkID_DifferentConfigs(t *testing.T) {
	g1 := GenesisBlock{
		NetworkName: "net-a",
		GenesisNode: "mmx-NODE1",
		CreatedAt:   "2026-01-01T00:00:00Z",
		Version:     1,
	}
	g2 := GenesisBlock{
		NetworkName: "net-b",
		GenesisNode: "mmx-NODE1",
		CreatedAt:   "2026-01-01T00:00:00Z",
		Version:     1,
	}

	id1 := computeNetworkID(g1)
	id2 := computeNetworkID(g2)

	if id1 == id2 {
		t.Errorf("different configs should produce different network IDs")
	}
}

func TestComputeNetworkID_Format(t *testing.T) {
	g := GenesisBlock{
		NetworkName: "test",
		GenesisNode: "mmx-NODE",
		CreatedAt:   "2026-01-01T00:00:00Z",
		Version:     1,
	}

	id := computeNetworkID(g)

	if !strings.HasPrefix(id, "0x") {
		t.Errorf("NetworkID should start with '0x', got %q", id)
	}
	if len(id) != 34 { // "0x" + 32 hex chars (16 bytes = 32 hex chars)
		t.Errorf("NetworkID should be 34 chars (0x + 32 hex), got %d chars: %q", len(id), id)
	}
}

// ============================================================
// VerifyNetworkID tests
// ============================================================

func TestVerifyNetworkID(t *testing.T) {
	// Save original NetworkID
	origNetworkID := NetworkID
	defer func() { NetworkID = origNetworkID }()

	NetworkID = "0xabc123def456"

	if !VerifyNetworkID("0xabc123def456") {
		t.Error("VerifyNetworkID should return true for matching ID")
	}
	if VerifyNetworkID("0xdifferent") {
		t.Error("VerifyNetworkID should return false for non-matching ID")
	}
}

// ============================================================
// GenesisInfo tests
// ============================================================

func TestGenesisInfo(t *testing.T) {
	origConfig := GenesisConfig
	origNetworkID := NetworkID
	defer func() {
		GenesisConfig = origConfig
		NetworkID = origNetworkID
	}()

	GenesisConfig = GenesisBlock{
		NetworkName: "test-info-net",
		GenesisNode: "mmx-INFO123",
		CreatedAt:   "2026-07-01T00:00:00Z",
		Version:     2,
	}
	NetworkID = computeNetworkID(GenesisConfig)

	info := GenesisInfo()
	if info["network_id"] != NetworkID {
		t.Errorf("network_id mismatch: got %v, want %v", info["network_id"], NetworkID)
	}
	if info["network_name"] != "test-info-net" {
		t.Errorf("network_name mismatch: got %v", info["network_name"])
	}
	if info["genesis_node"] != "mmx-INFO123" {
		t.Errorf("genesis_node mismatch: got %v", info["genesis_node"])
	}
	if info["version"] != 2 {
		t.Errorf("version mismatch: got %v", info["version"])
	}
}

// ============================================================
// HandleJoinRequest tests
// ============================================================

func TestHandleJoinRequest_NetworkMismatch(t *testing.T) {
	origNetworkID := NetworkID
	defer func() { NetworkID = origNetworkID }()

	NetworkID = "0xtest123"

	req := NodeJoinRequest{
		NetworkID: "0xwrong456",
		NodeID:    "mmx-VALID01",
		PubKey:    "pubkey",
	}

	resp := HandleJoinRequest(req)
	if resp.Accepted {
		t.Error("should reject nodes with mismatched NetworkID")
	}
	if !strings.Contains(resp.Reason, "network_id mismatch") {
		t.Errorf("expected 'network_id mismatch' in reason, got: %s", resp.Reason)
	}
}

func TestHandleJoinRequest_InvalidNodeID(t *testing.T) {
	origNetworkID := NetworkID
	defer func() { NetworkID = origNetworkID }()

	NetworkID = "0xabc123"

	tests := []struct {
		name   string
		nodeID string
	}{
		{"too short", "mmx"},
		{"wrong prefix", "xx-VALID123"},
		{"no prefix", "INVALID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := NodeJoinRequest{
				NetworkID: "0xabc123",
				NodeID:    tt.nodeID,
				PubKey:    "pubkey",
			}

			resp := HandleJoinRequest(req)
			if resp.Accepted {
				t.Errorf("should reject invalid nodeID %q", tt.nodeID)
			}
			if !strings.Contains(resp.Reason, "invalid node_id") {
				t.Errorf("expected 'invalid node_id' in reason, got: %s", resp.Reason)
			}
		})
	}
}

func TestHandleJoinRequest_MissingPubKey(t *testing.T) {
	origNetworkID := NetworkID
	defer func() { NetworkID = origNetworkID }()

	NetworkID = "0xabc123"

	req := NodeJoinRequest{
		NetworkID: "0xabc123",
		NodeID:    "mmx-VALID01",
		PubKey:    "",
	}

	resp := HandleJoinRequest(req)
	if resp.Accepted {
		t.Error("should reject nodes without public key")
	}
	if !strings.Contains(resp.Reason, "pub_key is required") {
		t.Errorf("expected 'pub_key is required' in reason, got: %s", resp.Reason)
	}
}

func TestHandleJoinRequest_Valid(t *testing.T) {
	origNetworkID := NetworkID
	origFed := fed
	defer func() {
		NetworkID = origNetworkID
		fed = origFed
	}()

	NetworkID = "0xabc123"
	fed = nil // No federation manager for simple test

	req := NodeJoinRequest{
		NetworkID: "0xabc123",
		NodeID:    "mmx-VALID01",
		PubKey:    "pubkey",
		Endpoint:  "https://node.example.com",
	}

	resp := HandleJoinRequest(req)
	if !resp.Accepted {
		t.Errorf("should accept valid join request, got reason: %s", resp.Reason)
	}
	if resp.NetworkID != NetworkID {
		t.Errorf("response NetworkID mismatch: got %s, want %s", resp.NetworkID, NetworkID)
	}
}

// ============================================================
// SaveGenesisConfig / LoadGenesisConfig tests
// ============================================================

func TestSaveAndLoadGenesisConfig(t *testing.T) {
	dir := t.TempDir()

	config := GenesisBlock{
		NetworkName: "test-save-net",
		GenesisNode: "mmx-SAVETEST",
		CreatedAt:   "2026-07-07T00:00:00Z",
		Version:     3,
	}

	err := SaveGenesisConfig(dir, config)
	if err != nil {
		t.Fatalf("SaveGenesisConfig error: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(dir, "genesis.json")); os.IsNotExist(err) {
		t.Error("genesis.json was not created")
	}
}

func TestGenesisJSON(t *testing.T) {
	origConfig := GenesisConfig
	defer func() { GenesisConfig = origConfig }()

	GenesisConfig = GenesisBlock{
		NetworkName: "json-test-net",
		GenesisNode: "mmx-JSON01",
		CreatedAt:   "2026-01-01T00:00:00Z",
		Version:     1,
	}

	jsonStr := GenesisJSON()
	if !strings.Contains(jsonStr, "json-test-net") {
		t.Error("GenesisJSON should contain network name")
	}
	if !strings.Contains(jsonStr, "mmx-JSON01") {
		t.Error("GenesisJSON should contain genesis node")
	}
}

// ============================================================
// NodeJoinRequest / NodeJoinResponse types
// ============================================================

func TestNodeJoinRequest_Fields(t *testing.T) {
	req := NodeJoinRequest{
		NetworkID: "0xabc",
		NodeID:    "mmx-NODE01",
		PubKey:    "base64pubkey",
		Endpoint:  "https://example.com",
		InviteSig: "signature",
	}

	if req.NetworkID != "0xabc" {
		t.Error("NetworkID field mismatch")
	}
	if req.NodeID != "mmx-NODE01" {
		t.Error("NodeID field mismatch")
	}
	if req.Endpoint != "https://example.com" {
		t.Error("Endpoint field mismatch")
	}
}

func TestNodeJoinResponse_Defaults(t *testing.T) {
	resp := NodeJoinResponse{}
	if resp.Accepted {
		t.Error("Accepted should default to false")
	}
	if resp.NetworkID != "" {
		t.Error("NetworkID should default to empty")
	}
}
