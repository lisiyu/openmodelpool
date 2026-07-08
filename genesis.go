package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
)

// ============================================================
// Genesis Hash — 网络身份锚定
// ============================================================
//
// 所有 fork 共享同一个 GenesisConfig → 产生相同的 NetworkID
// 节点握手第一步：比对 NetworkID，不同网络直接拒绝
// 这使得网络身份由密码学定义，而非依赖某个 GitHub repo

// GenesisBlock is the immutable network identity configuration.
// All forks of OpenModelPool Agent should use the same genesis to stay on the same network.
var GenesisConfig = GenesisBlock{
	NetworkName:  "openmodelpool-mainnet",
	GenesisNode:  "mm-JG7pKCdqgU8PBijd4m4CXP",
	GenesisPubKey: "", // populated at runtime from node identity if this IS the genesis node
	CreatedAt:    "2026-07-07T00:00:00Z",
	Version:      1,
}

// GenesisBlock defines the network identity.
type GenesisBlock struct {
	NetworkName   string `json:"network_name"`
	GenesisNode   string `json:"genesis_node"`
	GenesisPubKey string `json:"genesis_pubkey,omitempty"`
	CreatedAt     string `json:"created_at"`
	Version       int    `json:"version"`
}

// NetworkID is the SHA256 hash of the GenesisConfig.
// This is the canonical identifier for the OpenModelPool Agent mainnet.
var NetworkID string

func init() {
	NetworkID = computeNetworkID(GenesisConfig)
	slog.Info("genesis hash initialized", "network_id", NetworkID, "network_name", GenesisConfig.NetworkName)
}

func computeNetworkID(g GenesisBlock) string {
	// Deterministic JSON serialization (sorted keys by struct field order)
	b, _ := json.Marshal(g)
	hash := sha256.Sum256(b)
	return "0x" + hex.EncodeToString(hash[:16]) // 128-bit prefix, sufficient for uniqueness
}

// VerifyNetworkID checks if a given network ID matches our genesis.
func VerifyNetworkID(theirID string) bool {
	return theirID == NetworkID
}

// GenesisJSON returns the genesis config as JSON (for sharing with new nodes).
func GenesisJSON() string {
	b, _ := json.MarshalIndent(GenesisConfig, "", "  ")
	return string(b)
}

// SaveGenesisConfig saves a custom genesis config to disk.
// This is used when a fork wants to create a separate network.
func SaveGenesisConfig(dataDir string, config GenesisBlock) error {
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dataDir+"/genesis.json", b, 0644)
}

// LoadGenesisConfig loads a custom genesis config from disk.
// Falls back to the compiled-in default if no file exists.
func LoadGenesisConfig(dataDir string) {
	data, err := os.ReadFile(dataDir + "/genesis.json")
	if err != nil {
		// Use compiled-in default
		slog.Debug("using compiled-in genesis config")
		return
	}
	var config GenesisBlock
	if err := json.Unmarshal(data, &config); err != nil {
		slog.Warn("invalid genesis.json, using compiled-in default", "error", err)
		return
	}
	GenesisConfig = config
	NetworkID = computeNetworkID(config)
	slog.Info("loaded custom genesis config",
		"network_id", NetworkID,
		"network_name", config.NetworkName)
}

// GenesisInfo returns genesis information for the status API.
func GenesisInfo() map[string]any {
	return map[string]any{
		"network_id":   NetworkID,
		"network_name": GenesisConfig.NetworkName,
		"genesis_node": GenesisConfig.GenesisNode,
		"created_at":   GenesisConfig.CreatedAt,
		"version":      GenesisConfig.Version,
	}
}

// NodeJoinRequest is sent when a node wants to join the network.
type NodeJoinRequest struct {
	NetworkID string `json:"network_id"`
	NodeID    string `json:"node_id"`
	PubKey    string `json:"pub_key"`
	Endpoint  string `json:"endpoint"`
	InviteSig string `json:"invite_sig,omitempty"` // signed invite code (Phase 2)
}

// NodeJoinResponse is returned to joining nodes.
type NodeJoinResponse struct {
	Accepted  bool     `json:"accepted"`
	NetworkID string   `json:"network_id"`
	Reason    string   `json:"reason,omitempty"`
	Peers     []NodeInfo `json:"peers,omitempty"` // known peers snapshot
}

// HandleJoinRequest processes a node join request.
func HandleJoinRequest(req NodeJoinRequest) NodeJoinResponse {
	// Step 1: Verify network ID
	if !VerifyNetworkID(req.NetworkID) {
		return NodeJoinResponse{
			Accepted:  false,
			NetworkID: NetworkID,
			Reason:    fmt.Sprintf("network_id mismatch: expected %s, got %s", NetworkID, req.NetworkID),
		}
	}

	// Step 2: Verify node ID format
	if len(req.NodeID) < 4 || req.NodeID[:3] != "mm-" {
		return NodeJoinResponse{
			Accepted: false,
			Reason:   "invalid node_id format",
		}
	}

	// Step 3: Verify public key is present
	if req.PubKey == "" {
		return NodeJoinResponse{
			Accepted: false,
			Reason:   "pub_key is required",
		}
	}

	// Step 4: Phase 2 - Verify invite signature (if provided)
	// Invite-based verification is handled via /api/federation/invites/verify
	// Nodes joining with an invite code are verified through the invite manager

	// Step 5: Build peer snapshot
	var peers []NodeInfo
	if fed != nil {
		fed.mu.RLock()
		for _, n := range fed.trustPool.Nodes {
			if n.Status == "active" {
				peers = append(peers, n)
			}
		}
		fed.mu.RUnlock()
	}

	slog.Info("node join accepted",
		"node_id", req.NodeID,
		"endpoint", req.Endpoint,
		"peers_returned", len(peers))

	return NodeJoinResponse{
		Accepted:  true,
		NetworkID: NetworkID,
		Peers:     peers,
	}
}
