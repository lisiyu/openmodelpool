// node_registry.go — disk-backed node registry for OpenModelPool.
//
// This file adds a pure-incremental, dependency-free persistence layer on top of
// the existing in-memory route table (see network.go / federation.go). Each known
// node is mirrored to a single JSON file under a directory (.nodes/ by default):
//
//	<dir>/<node_id>.json
//
// The goals are:
//   - Cold-start resilience: on restart the node can rehydrate known peers from
//     local disk before gossip or GitHub bootstrap completes.
//   - Strictly additive: it never changes the in-memory routing behavior; all
//     writes funnel through the route table / federation manager, and a nil
//     registry is a safe no-op (so existing unit tests stay green).
//
// Only the Go standard library is used (encoding/json, os, path/filepath, sync,
// strings, crypto/sha256, regexp, time, log/slog).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// safeNodeIDPattern matches Node IDs that are safe to use verbatim as a file name
// component. Anything else is hashed (see registryFileName) to guarantee we never
// write outside dir, preventing path traversal.
var safeNodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// persistedNode is the on-disk JSON representation of a known node. It captures the
// mappable fields from RouteEntry / PeerInfo plus a persistence timestamp so the
// cold-start loader can reconstruct a usable RouteEntry.
type persistedNode struct {
	NodeID          string   `json:"node_id"`
	NodeName        string   `json:"node_name"`
	Addresses       []string `json:"addresses"`
	IsGateway       bool     `json:"is_gateway"`
	IsSeed          bool     `json:"is_seed"`
	Models          []string `json:"models"`
	Status          string   `json:"status"`
	LastSeenUnix    int64    `json:"last_seen_unix"`
	LatencyMS       float64  `json:"latency_ms"`
	FailCount       int      `json:"fail_count"`
	UptimeScore     float64  `json:"uptime_score"`
	PersistedAtUnix int64    `json:"persisted_at_unix"`
}

// NodeRegistry manages per-node JSON files under a directory (.nodes/ by default).
// It is a small persistence layer that complements the in-memory route table:
// nodes learned via gossip, manual peers, or seed lists are mirrored to disk so a
// cold start can restore known peers without depending on GitHub bootstrap.
type NodeRegistry struct {
	dir string
	mu  sync.Mutex // serializes writes to avoid concurrent writes to the same dir.
}

// NewNodeRegistry creates a registry rooted at dir. When dir is empty the default
// ".nodes" directory is used. The directory (and any parents) is created if missing.
func NewNodeRegistry(dir string) *NodeRegistry {
	if dir == "" {
		dir = ".nodes"
	}
	r := &NodeRegistry{dir: dir}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Error("failed to create node registry directory", "dir", dir, "error", err)
	}
	return r
}

// registryFileName maps a node ID to a safe file name. Safe IDs are used verbatim
// (with a .json suffix); unsafe IDs are hashed with SHA-256 so the resulting name
// is stable, collision-resistant, and free of path separators. filepath.Base is
// applied defensively so a malformed node ID can never introduce a separator.
func registryFileName(nodeID string) string {
	if nodeID != "" && safeNodeIDPattern.MatchString(nodeID) {
		return filepath.Base(nodeID + ".json")
	}
	if nodeID == "" {
		// Deterministic name for empty IDs to avoid colliding with real nodes.
		nodeID = "<empty>"
	}
	sum := sha256.Sum256([]byte(nodeID))
	return filepath.Base(hex.EncodeToString(sum[:]) + ".json")
}

// SaveNode persists a single route entry to <dir>/<node_id>.json. It is a no-op
// for a nil receiver or nil entry.
func (r *NodeRegistry) SaveNode(entry *RouteEntry) {
	if r == nil || entry == nil {
		return
	}
	pn := persistedNode{
		NodeID:          entry.NodeID,
		NodeName:        entry.NodeName,
		Addresses:       cloneStrings(entry.Addresses),
		Models:          cloneStrings(entry.Models),
		Status:          entry.Status,
		LastSeenUnix:    entry.LastSeen.Unix(),
		LatencyMS:       entry.LatencyMS,
		FailCount:       entry.FailCount,
		UptimeScore:     entry.UptimeScore,
		IsGateway:       entry.IsGateway,
		IsSeed:          entry.IsSeed,
		PersistedAtUnix: time.Now().Unix(),
	}
	r.writeLocked(pn)
}

// SavePeer persists a single peer (richer than a bare route entry) to disk. When
// both SaveNode and SavePeer are called for the same node, the later write wins;
// callers typically invoke SavePeer after routeTable.Put so model/status metadata
// from PeerInfo is preserved on disk.
func (r *NodeRegistry) SavePeer(p PeerInfo) {
	if r == nil {
		return
	}
	pn := persistedNode{
		NodeID:          p.NodeID,
		NodeName:        p.Name,
		Addresses:       cloneStrings(p.Addresses),
		Models:          cloneStrings(p.Models),
		Status:          p.Status,
		LastSeenUnix:    parseRFC3339Unix(p.LastSeen),
		PersistedAtUnix: time.Now().Unix(),
	}
	r.writeLocked(pn)
}

// RemoveNode deletes the JSON file for the given node ID, if present. Missing
// files are not an error.
func (r *NodeRegistry) RemoveNode(nodeID string) {
	if r == nil || nodeID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	name := registryFileName(nodeID)
	path := filepath.Join(r.dir, name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to remove node file", "node_id", nodeID, "path", path, "error", err)
	}
}

// LoadAll reads every <dir>/*.json file and reconstructs RouteEntry values. Files
// that fail to parse are skipped (with a warning) so one corrupt entry cannot block
// cold-start recovery. A missing directory yields an empty slice and nil error
// (i.e. no persisted nodes yet).
func (r *NodeRegistry) LoadAll() ([]*RouteEntry, error) {
	if r == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]*RouteEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.dir, e.Name()))
		if err != nil {
			slog.Warn("failed to read node file", "file", e.Name(), "error", err)
			continue
		}
		var pn persistedNode
		if err := json.Unmarshal(data, &pn); err != nil {
			slog.Warn("failed to parse node file, skipping", "file", e.Name(), "error", err)
			continue
		}
		if pn.NodeID == "" {
			slog.Warn("skipping node file with empty node_id", "file", e.Name())
			continue
		}
		lastSeen := time.Unix(pn.LastSeenUnix, 0)
		result = append(result, &RouteEntry{
			NodeID:      pn.NodeID,
			NodeName:    pn.NodeName,
			Addresses:   cloneStrings(pn.Addresses),
			Status:      orDefault(pn.Status, "online"),
			Models:      cloneStrings(pn.Models),
			LatencyMS:   pn.LatencyMS,
			LastSeen:    lastSeen,
			FailCount:   pn.FailCount,
			UptimeScore: pn.UptimeScore,
			IsGateway:   pn.IsGateway,
			IsSeed:      pn.IsSeed,
			UpdatedAt:   time.Now(),
		})
	}
	return result, nil
}

// writeLocked serializes pn to JSON and writes it atomically (write temp, then
// rename) under the registry mutex. The mutex prevents concurrent writes to the
// same directory/files from racing. Callers must not hold r.mu.
func (r *NodeRegistry) writeLocked(pn persistedNode) {
	r.mu.Lock()
	name := registryFileName(pn.NodeID)
	path := filepath.Join(r.dir, name)
	r.mu.Unlock()

	tmp := path + ".tmp"
	data, err := json.MarshalIndent(pn, "", "  ")
	if err != nil {
		slog.Error("failed to marshal node", "node_id", pn.NodeID, "error", err)
		return
	}
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		slog.Error("failed to write node temp file", "node_id", pn.NodeID, "error", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		slog.Error("failed to persist node", "node_id", pn.NodeID, "error", err)
	}
}

// routeEntryFromNodeInfo maps a federation NodeInfo onto a RouteEntry for disk
// persistence. It is a best-effort projection: fields not present on NodeInfo
// (e.g. LoadScore) are left at zero values. Addresses falls back to Endpoint when
// the multi-address list is empty.
func routeEntryFromNodeInfo(info NodeInfo) *RouteEntry {
	addrs := info.Addresses
	if len(addrs) == 0 && info.Endpoint != "" {
		addrs = []string{info.Endpoint}
	}
	var lastSeen time.Time
	if info.LastSeen != "" {
		if t, err := time.Parse(time.RFC3339, info.LastSeen); err == nil {
			lastSeen = t
		}
	}
	return &RouteEntry{
		NodeID:    info.NodeID,
		NodeName:  info.NodeID,
		Addresses: addrs,
		Status:    info.Status,
		Models:    info.SharedModels,
		LastSeen:  lastSeen,
		UpdatedAt: time.Now(),
	}
}

// cloneStrings returns a copy of s (or an empty non-nil slice) so persisted data
// is decoupled from the caller's backing arrays.
func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return []string{}
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// parseRFC3339Unix parses an RFC3339 timestamp into a unix-seconds value,
// returning 0 when the input is empty or unparseable.
func parseRFC3339Unix(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// orDefault returns v when non-empty, otherwise def.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
