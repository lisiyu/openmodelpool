// node_registry_test.go — white-box tests for the on-disk node registry.
//
// These tests live in package main so they can exercise the unexported helpers
// (registryFileName, routeEntryFromNodeInfo, persistedNode) and the NodeRegistry
// methods directly. They use t.TempDir() and never touch the package-level
// nodeRegistry / routeTable, so they cannot pollute other tests or production state.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestRegistry(t *testing.T) *NodeRegistry {
	t.Helper()
	return NewNodeRegistry(filepath.Join(t.TempDir(), ".nodes"))
}

func TestNodeRegistry_NewCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", ".nodes")
	r := NewNodeRegistry(dir)
	if r == nil {
		t.Fatal("NewNodeRegistry returned nil")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("registry dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("registry path is not a directory")
	}
}

func TestNodeRegistry_SaveAndLoadNode(t *testing.T) {
	r := newTestRegistry(t)
	entry := &RouteEntry{
		NodeID:    "mmx-node1",
		NodeName:  "Node One",
		Addresses: []string{"https://node1.example.com", "https://backup.example.com"},
		Status:    "online",
		Models:    []string{"gpt-4o", "claude-3"},
		LatencyMS: 42.5,
		LastSeen:  time.Unix(1700000000, 0),
		UpdatedAt: time.Unix(1700000100, 0),
	}
	r.SaveNode(entry)

	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 node, got %d", len(loaded))
	}
	got := loaded[0]
	if got.NodeID != entry.NodeID || got.NodeName != entry.NodeName {
		t.Errorf("identity mismatch: got %s/%s want %s/%s", got.NodeID, got.NodeName, entry.NodeID, entry.NodeName)
	}
	if !equalStrings(got.Addresses, entry.Addresses) {
		t.Errorf("addresses mismatch: got %v want %v", got.Addresses, entry.Addresses)
	}
	if !equalStrings(got.Models, entry.Models) {
		t.Errorf("models mismatch: got %v want %v", got.Models, entry.Models)
	}
	if got.LatencyMS != entry.LatencyMS {
		t.Errorf("latency mismatch: got %v want %v", got.LatencyMS, entry.LatencyMS)
	}
	if got.LastSeen.Unix() != entry.LastSeen.Unix() {
		t.Errorf("last_seen mismatch: got %d want %d", got.LastSeen.Unix(), entry.LastSeen.Unix())
	}
	// Loaded nodes must be treated as freshly seen (cold-start resilience).
	if time.Since(got.UpdatedAt) > time.Minute {
		t.Errorf("loaded node not refreshed: UpdatedAt=%v", got.UpdatedAt)
	}
}

func TestNodeRegistry_SavePeerRoundTrip(t *testing.T) {
	r := newTestRegistry(t)
	peer := PeerInfo{
		NodeID:    "mmx-peer9",
		Name:      "Peer Nine",
		Addresses: []string{"https://peer9.example.com"},
		Models:    []string{"llama-3"},
		Status:    "online",
		LastSeen:  time.Unix(1699999999, 0).UTC().Format(time.RFC3339),
	}
	r.SavePeer(peer)

	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 node, got %d", len(loaded))
	}
	got := loaded[0]
	if got.NodeID != peer.NodeID || got.NodeName != peer.Name {
		t.Errorf("identity mismatch: got %s/%s want %s/%s", got.NodeID, got.NodeName, peer.NodeID, peer.Name)
	}
	if !equalStrings(got.Models, peer.Models) {
		t.Errorf("models mismatch: got %v want %v", got.Models, peer.Models)
	}
	if got.LastSeen.Unix() != 1699999999 {
		t.Errorf("last_seen mismatch: got %d want %d", got.LastSeen.Unix(), 1699999999)
	}
}

func TestNodeRegistry_RemoveNode(t *testing.T) {
	r := newTestRegistry(t)
	entry := &RouteEntry{NodeID: "mmx-del", NodeName: "Del", Addresses: []string{"https://del.example.com"}}
	r.SaveNode(entry)

	if _, err := os.Stat(filepath.Join(r.dir, "mmx-del.json")); err != nil {
		t.Fatalf("file not written before remove: %v", err)
	}
	r.RemoveNode("mmx-del")
	if _, err := os.Stat(filepath.Join(r.dir, "mmx-del.json")); !os.IsNotExist(err) {
		t.Fatalf("file not removed: %v", err)
	}
	// Removing a non-existent node must not error.
	r.RemoveNode("mmx-does-not-exist")

	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 nodes after removal, got %d", len(loaded))
	}
}

func TestNodeRegistry_FileNameSafety(t *testing.T) {
	// Safe IDs are used verbatim.
	if got := registryFileName("mmx-safe_node-1"); got != "mmx-safe_node-1.json" {
		t.Errorf("safe id name = %q, want mmx-safe_node-1.json", got)
	}
	// Unsafe IDs (path traversal / odd chars) are hashed, never written as-is.
	evil := "../../etc/passwd"
	got := registryFileName(evil)
	if got == evil+".json" || strings.Contains(got, "/") || strings.Contains(got, "..") {
		t.Fatalf("unsafe node id not sanitized: %q", got)
	}
	if !strings.HasSuffix(got, ".json") {
		t.Fatalf("hashed name missing .json suffix: %q", got)
	}
	// Same evil id must map to a stable name.
	if registryFileName(evil) != got {
		t.Fatal("registryFileName not deterministic for same input")
	}
	// Empty id must be handled without panic and produce a stable name.
	if registryFileName("") == "" {
		t.Fatal("empty id produced empty file name")
	}
}

func TestNodeRegistry_SavePreventsTraversal(t *testing.T) {
	r := newTestRegistry(t)
	// An attacker-controlled node id with separators must not write outside dir.
	evil := "../../../tmp/evil"
	r.SaveNode(&RouteEntry{NodeID: evil, NodeName: "x", Addresses: []string{"https://x.example.com"}})

	// The only file in the registry dir must be the hashed one.
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 file in registry dir, got %d", len(entries))
	}
	if strings.Contains(entries[0].Name(), "..") || strings.Contains(entries[0].Name(), "/") {
		t.Fatalf("file name contains traversal chars: %q", entries[0].Name())
	}
	// And the temp/evil path must not exist.
	if _, err := os.Stat(filepath.Join(t.TempDir(), "evil")); err == nil {
		t.Fatal("path traversal succeeded (file written outside registry dir)")
	}
}

func TestNodeRegistry_LoadAllSkipsCorrupt(t *testing.T) {
	r := newTestRegistry(t)
	r.SaveNode(&RouteEntry{NodeID: "mmx-good", NodeName: "Good", Addresses: []string{"https://g.example.com"}})

	// Drop a corrupt file into the directory.
	if err := os.WriteFile(filepath.Join(r.dir, "corrupt.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	// Drop a non-json file that should be ignored.
	if err := os.WriteFile(filepath.Join(r.dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed txt file: %v", err)
	}

	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll returned error on corrupt input: %v", err)
	}
	if len(loaded) != 1 || loaded[0].NodeID != "mmx-good" {
		t.Fatalf("expected exactly the good node, got %+v", loaded)
	}
}

func TestNodeRegistry_LoadAllMissingDir(t *testing.T) {
	r := NewNodeRegistry(filepath.Join(t.TempDir(), "does-not-exist-yet"))
	// No writes → dir is empty but exists; LoadAll should return empty, nil.
	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll on empty registry: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(loaded))
	}
}

func TestRouteEntryFromNodeInfo(t *testing.T) {
	info := NodeInfo{
		NodeID:       "mmx-gossip",
		Endpoint:     "https://gossip.example.com",
		SharedModels: []string{"gpt-4o"},
		Status:       "active",
		LastSeen:     time.Unix(1700000000, 0).UTC().Format(time.RFC3339),
	}
	e := routeEntryFromNodeInfo(info)
	if e.NodeID != info.NodeID {
		t.Errorf("NodeID = %q want %q", e.NodeID, info.NodeID)
	}
	if !equalStrings(e.Addresses, []string{info.Endpoint}) {
		t.Errorf("addresses should fall back to Endpoint: got %v", e.Addresses)
	}
	if !equalStrings(e.Models, info.SharedModels) {
		t.Errorf("models mismatch: got %v", e.Models)
	}
	if e.LastSeen.Unix() != 1700000000 {
		t.Errorf("last_seen mismatch: got %d", e.LastSeen.Unix())
	}
}

func TestNodeRegistry_PersistedAtWritten(t *testing.T) {
	r := newTestRegistry(t)
	r.SaveNode(&RouteEntry{NodeID: "mmx-ts", NodeName: "TS", Addresses: []string{"https://ts.example.com"}})
	data, err := os.ReadFile(filepath.Join(r.dir, "mmx-ts.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var pn persistedNode
	if err := json.Unmarshal(data, &pn); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pn.PersistedAtUnix == 0 {
		t.Error("persisted_at_unix not written")
	}
	if pn.NodeID != "mmx-ts" {
		t.Errorf("node_id = %q", pn.NodeID)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
