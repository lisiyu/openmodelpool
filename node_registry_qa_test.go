// node_registry_qa_test.go — supplementary white-box QA tests for the on-disk
// node registry (快速模式 QA 验证, 子团队 software-omp-node-registry).
//
// These tests harden the coverage beyond the engineer's own node_registry_test.go
// and specifically target the items called out for QA verification:
//   - atomic write correctness + stale temp-file residue handling
//   - LoadAll fault tolerance (corrupt JSON, empty node_id, missing directory)
//   - file-name safety / path-traversal prevention (unit + on-disk)
//   - concurrent SaveNode + LoadAll under -race
//   - cold-start refill integration (initNodeRegistry -> routeTable.GetAll)
//   - federation gossip persistence hook (UpdateNodeInfo -> SaveNode)
//
// All tests live in package main so they can reach the unexported helpers and the
// package-level nodeRegistry / routeTable globals. Global state mutated by the
// integration tests is restored via t.Cleanup to avoid cross-test pollution.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 1. Atomic write correctness
// ---------------------------------------------------------------------------

// TestNodeRegistry_AtomicWrite_NoTempLeftover verifies that after SaveNode the
// final <id>.json exists, parses as valid persistedNode JSON, carries the
// expected fields, and that NO .tmp file is left behind (rename consumed it).
func TestNodeRegistry_AtomicWrite_NoTempLeftover(t *testing.T) {
	r := newTestRegistry(t)
	entry := &RouteEntry{
		NodeID:    "mmx-atomic",
		NodeName:  "Atomic",
		Addresses: []string{"https://a.example.com"},
		Status:    "online",
		Models:    []string{"m1"},
		LatencyMS: 3.3,
		LastSeen:  time.Unix(1700000000, 0),
	}
	r.SaveNode(entry)

	data, err := os.ReadFile(filepath.Join(r.dir, "mmx-atomic.json"))
	if err != nil {
		t.Fatalf("final file missing after SaveNode: %v", err)
	}
	var pn persistedNode
	if err := json.Unmarshal(data, &pn); err != nil {
		t.Fatalf("final file is not valid JSON: %v", err)
	}
	if pn.NodeID != "mmx-atomic" {
		t.Errorf("persisted node_id = %q", pn.NodeID)
	}
	if pn.PersistedAtUnix == 0 {
		t.Error("persisted_at_unix not written")
	}
	if pn.LastSeenUnix != 1700000000 {
		t.Errorf("last_seen_unix = %d", pn.LastSeenUnix)
	}
	if pn.LatencyMS != 3.3 {
		t.Errorf("latency_ms = %v", pn.LatencyMS)
	}

	// No .tmp residue may remain.
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("stale temp file leftover: %s", e.Name())
		}
	}
}

// TestNodeRegistry_AtomicWrite_TempResidueIgnored simulates a stale/corrupt temp
// file that lingered from a previous crashed write. The fresh SaveNode must
// overwrite it and the resulting final file must contain the NEW entry (not the
// stale garbage), with no .tmp left behind.
func TestNodeRegistry_AtomicWrite_TempResidueIgnored(t *testing.T) {
	r := newTestRegistry(t)
	finalPath := filepath.Join(r.dir, "mmx-residue.json")
	tmpPath := finalPath + ".tmp"

	// Seed a stale temp file with junk BEFORE the real write.
	if err := os.WriteFile(tmpPath, []byte("STALE GARBAGE NOT JSON"), 0o644); err != nil {
		t.Fatalf("seed stale temp: %v", err)
	}

	entry := &RouteEntry{NodeID: "mmx-residue", NodeName: "Res", Addresses: []string{"https://r.example.com"}}
	r.SaveNode(entry)

	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("final file missing: %v", err)
	}
	var pn persistedNode
	if err := json.Unmarshal(data, &pn); err != nil {
		t.Fatalf("final file invalid despite stale temp: %v", err)
	}
	if pn.NodeID != "mmx-residue" {
		t.Errorf("final file should hold the fresh entry, got node_id=%q", pn.NodeID)
	}
	// The stale temp must be gone (rename consumed it / it was overwritten).
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("stale temp not cleaned up: %v", err)
	}
	// LoadAll must return exactly the fresh node, never the garbage.
	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 || loaded[0].NodeID != "mmx-residue" {
		t.Fatalf("LoadAll returned wrong set: %+v", loaded)
	}
}

// ---------------------------------------------------------------------------
// 2. LoadAll fault tolerance
// ---------------------------------------------------------------------------

// TestNodeRegistry_LoadAllSkipsEmptyNodeID ensures a file that is valid JSON but
// carries an empty/missing node_id is skipped (not returned, not fatal). This
// exercises the pn.NodeID == "" guard inside LoadAll.
func TestNodeRegistry_LoadAllSkipsEmptyNodeID(t *testing.T) {
	r := newTestRegistry(t)
	r.SaveNode(&RouteEntry{NodeID: "mmx-ok", NodeName: "Ok", Addresses: []string{"https://ok.example.com"}})

	// Valid JSON, explicit empty node_id -> must be skipped.
	if err := os.WriteFile(filepath.Join(r.dir, "empty.json"), []byte(`{"node_id":"","node_name":"ghost"}`), 0o644); err != nil {
		t.Fatalf("seed empty-id file: %v", err)
	}
	// Valid JSON, missing node_id key -> must be skipped.
	if err := os.WriteFile(filepath.Join(r.dir, "noid.json"), []byte(`{"node_name":"no-id"}`), 0o644); err != nil {
		t.Fatalf("seed no-id file: %v", err)
	}

	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 || loaded[0].NodeID != "mmx-ok" {
		t.Fatalf("expected exactly mmx-ok, got %+v", loaded)
	}
}

// TestNodeRegistry_LoadAllMissingDirReturnsEmpty genuinely exercises the
// os.IsNotExist branch of LoadAll by constructing a registry whose directory was
// NEVER created (bypassing NewNodeRegistry, which MkdirAlls). It must return an
// empty slice and a nil error — cold start before any persistence.
func TestNodeRegistry_LoadAllMissingDirReturnsEmpty(t *testing.T) {
	r := &NodeRegistry{dir: filepath.Join(t.TempDir(), "no-such-dir")}
	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("expected nil error for missing directory, got %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected empty slice for missing directory, got %d", len(loaded))
	}
}

// ---------------------------------------------------------------------------
// 3. File-name safety / path-traversal prevention
// ---------------------------------------------------------------------------

// TestNodeRegistry_FileNameSafety_Unit checks the pure mapping registryFileName
// for a battery of hostile / odd node IDs: no path separator, no "..", always
// ends with .json, and deterministic. This is I/O free so it is safe even with
// very long IDs on Windows (MAX_PATH).
func TestNodeRegistry_FileNameSafety_Unit(t *testing.T) {
	cases := []string{
		"mmx-safe_node-1",
		"../../etc/passwd",
		"../../../tmp/evil",
		"a/../b",
		"foo.json",
		"has space!@#$",
		"节点-test",
		strings.Repeat("a", 500), // very long, but only unit-level here
		".",
		"..",
		"",
	}
	for _, id := range cases {
		name := registryFileName(id)
		if !strings.HasSuffix(name, ".json") {
			t.Errorf("id %q -> %q: missing .json suffix", id, name)
		}
		if strings.Contains(name, "/") {
			t.Errorf("id %q -> %q: contains path separator", id, name)
		}
		if strings.Contains(name, "..") {
			t.Errorf("id %q -> %q: contains '..' traversal", id, name)
		}
		if registryFileName(id) != name {
			t.Errorf("id %q: registryFileName not deterministic", id)
		}
	}
}

// TestNodeRegistry_FileNameSafety_NoTraversalOnDisk performs the equivalent check
// at the filesystem level: every hostile node ID must result in a file that lands
// INSIDE the registry directory (no separator, no ".." in the name, full path
// resolves under r.dir). Only short-enough IDs are used to avoid Windows MAX_PATH.
func TestNodeRegistry_FileNameSafety_NoTraversalOnDisk(t *testing.T) {
	r := newTestRegistry(t)
	ids := []string{
		"../../etc/passwd",
		"../../../tmp/evil",
		"a/../b",
		"foo.json",
		"has space!@#$",
		"节点-test",
		"normal-node",
	}
	for _, id := range ids {
		r.SaveNode(&RouteEntry{NodeID: id, NodeName: "x", Addresses: []string{"https://x.example.com"}})
	}

	entries, err := os.ReadDir(r.dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(ids) {
		t.Fatalf("expected %d files, got %d", len(ids), len(entries))
	}
	for _, e := range entries {
		n := e.Name()
		if strings.Contains(n, "/") || strings.Contains(n, "..") {
			t.Errorf("traversal chars in file name: %q", n)
		}
		full := filepath.Join(r.dir, n)
		if rel, err := filepath.Rel(r.dir, full); err != nil || rel != n {
			t.Errorf("file not physically inside registry dir: %q (rel=%q)", full, rel)
		}
		// And nothing was written outside the registry dir.
		if !strings.HasPrefix(full, r.dir) {
			t.Errorf("file escaped registry dir: %q", full)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. Concurrency (run with -race)
// ---------------------------------------------------------------------------

// TestNodeRegistry_ConcurrentSaveAndLoad hammers the registry with many parallel
// SaveNode (distinct IDs) and LoadAll calls. Under -race this must stay clean:
// writes are serialized by sync.Mutex, and the temp-file suffix (.tmp) is never
// treated as a .json node file by LoadAll, so there is no shared-memory race and
// no corruption.
func TestNodeRegistry_ConcurrentSaveAndLoad(t *testing.T) {
	r := newTestRegistry(t)
	const n = 64

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "mmx-conc-" + strconv.Itoa(i)
			r.SaveNode(&RouteEntry{
				NodeID:    id,
				NodeName:  id,
				Addresses: []string{"https://" + id + ".example.com"},
				Models:    []string{"m"},
				Status:    "online",
			})
		}(i)
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.LoadAll()
		}()
	}
	wg.Wait()

	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll after concurrency: %v", err)
	}
	if len(loaded) != n {
		t.Fatalf("expected %d nodes after concurrent writes, got %d", n, len(loaded))
	}
}

// ---------------------------------------------------------------------------
// 5. network.go route-table integration (UpsertEntry preserves rich fields)
// ---------------------------------------------------------------------------

// TestRouteTable_UpsertEntry_PreservesRichFields verifies the cold-start restore
// path uses UpsertEntry (not Put) so models/latency/last_seen survive the round
// trip from disk. Put() would discard them; this guards against a regression
// where initNodeRegistry accidentally routes through Put.
func TestRouteTable_UpsertEntry_PreservesRichFields(t *testing.T) {
	rt := initRouteTable()
	e := &RouteEntry{
		NodeID:    "mmx-rich",
		NodeName:  "Rich",
		Addresses: []string{"https://r.example.com"},
		Status:    "online",
		Models:    []string{"gpt-4o", "claude-3"},
		LatencyMS: 7.7,
		LastSeen:  time.Unix(1700000000, 0),
		UpdatedAt: time.Now(),
	}
	rt.UpsertEntry(e)

	got := rt.Get("mmx-rich")
	if got == nil {
		t.Fatal("entry not found after UpsertEntry")
	}
	if !equalStrings(got.Models, e.Models) {
		t.Errorf("models lost through UpsertEntry: %v", got.Models)
	}
	if got.LatencyMS != e.LatencyMS {
		t.Errorf("latency lost through UpsertEntry: %v", got.LatencyMS)
	}
	if got.LastSeen.Unix() != 1700000000 {
		t.Errorf("last_seen lost through UpsertEntry: %d", got.LastSeen.Unix())
	}
}

// ---------------------------------------------------------------------------
// 6. Cold-start refill integration (initNodeRegistry -> routeTable.GetAll)
// ---------------------------------------------------------------------------

// TestInitNodeRegistry_ColdStartRefillsRouteTable simulates a restart: a data dir
// already contains persisted .nodes/*.json from a previous run. After
// initNodeRegistry the in-memory routeTable.GetAll() must contain those nodes
// with their addresses/models restored.
func TestInitNodeRegistry_ColdStartRefillsRouteTable(t *testing.T) {
	dataDir := t.TempDir()
	nodesDir := filepath.Join(dataDir, ".nodes")
	// Simulate a previous run's persisted state: the .nodes dir already exists
	// with node files. initNodeRegistry will reuse (not recreate) it.
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatalf("create nodes dir: %v", err)
	}

	seeds := map[string]persistedNode{
		"mmx-cold-1": {
			NodeID: "mmx-cold-1", NodeName: "Cold One",
			Addresses: []string{"https://cold1.example.com"},
			Models:    []string{"gpt-4o"}, Status: "online",
			LastSeenUnix: 1700000000, LatencyMS: 12.3, PersistedAtUnix: 1700000001,
		},
		"mmx-cold-2": {
			NodeID: "mmx-cold-2", NodeName: "Cold Two",
			Addresses: []string{"https://cold2.example.com"},
			Models:    []string{"llama-3"}, Status: "online",
			LastSeenUnix: 1700000002, LatencyMS: 9.1, PersistedAtUnix: 1700000003,
		},
	}
	for _, pn := range seeds {
		b, err := json.MarshalIndent(pn, "", "  ")
		if err != nil {
			t.Fatalf("marshal seed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(nodesDir, pn.NodeID+".json"), b, 0o644); err != nil {
			t.Fatalf("seed node file: %v", err)
		}
	}

	// initNodeRegistry relies on the package routeTable being initialized.
	ensureRouteTable()
	t.Cleanup(func() { routeTable = nil })
	prevReg := nodeRegistry
	initNodeRegistry(dataDir)
	t.Cleanup(func() { nodeRegistry = prevReg })

	if nodeRegistry == nil {
		t.Error("initNodeRegistry did not set the package-level nodeRegistry")
	}

	got := routeTable.GetAll()
	byID := make(map[string]*RouteEntry, len(got))
	for _, e := range got {
		byID[e.NodeID] = e
	}
	for id, want := range seeds {
		e, ok := byID[id]
		if !ok {
			t.Errorf("cold-start did not restore node %s", id)
			continue
		}
		if !equalStrings(e.Addresses, want.Addresses) {
			t.Errorf("node %s addresses mismatch: got %v want %v", id, e.Addresses, want.Addresses)
		}
		if !equalStrings(e.Models, want.Models) {
			t.Errorf("node %s models mismatch: got %v want %v", id, e.Models, want.Models)
		}
	}
}

// TestInitNodeRegistry_EmptyDirSafe covers the fresh-install path: a data dir with
// no .nodes yet must initialize cleanly (no panic) and leave the route table empty.
func TestInitNodeRegistry_EmptyDirSafe(t *testing.T) {
	dataDir := t.TempDir()

	ensureRouteTable()
	t.Cleanup(func() { routeTable = nil })
	prevReg := nodeRegistry
	initNodeRegistry(dataDir)
	t.Cleanup(func() { nodeRegistry = prevReg })

	if got := routeTable.GetAll(); len(got) != 0 {
		t.Errorf("expected empty route table on fresh dir, got %d entries", len(got))
	}
}

// ---------------------------------------------------------------------------
// 7. Federation gossip persistence hook
// ---------------------------------------------------------------------------

// TestFederationUpdateNodeInfo_PersistsToRegistry verifies that the gossip upsert
// entry point FederationManager.UpdateNodeInfo mirrors the learned node to the
// on-disk registry (federation.go hook). We point the package nodeRegistry at a
// temp registry and assert LoadAll yields the persisted node with the address
// falling back to Endpoint.
func TestFederationUpdateNodeInfo_PersistsToRegistry(t *testing.T) {
	reg := NewNodeRegistry(filepath.Join(t.TempDir(), ".nodes"))
	prev := nodeRegistry
	nodeRegistry = reg
	t.Cleanup(func() { nodeRegistry = prev })

	fm := &FederationManager{localPeers: make(map[string]*NodeInfo)}
	info := NodeInfo{
		NodeID:       "mmx-fed",
		Endpoint:     "https://fed.example.com",
		SharedModels: []string{"gpt-4o"},
		Status:       "active",
		LastSeen:     time.Unix(1700000000, 0).UTC().Format(time.RFC3339),
	}
	fm.UpdateNodeInfo(info)

	loaded, err := reg.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	found := false
	for _, e := range loaded {
		if e.NodeID != "mmx-fed" {
			continue
		}
		found = true
		if !equalStrings(e.Addresses, []string{"https://fed.example.com"}) {
			t.Errorf("address should fall back to Endpoint: got %v", e.Addresses)
		}
		if !equalStrings(e.Models, []string{"gpt-4o"}) {
			t.Errorf("models mismatch: got %v", e.Models)
		}
	}
	if !found {
		t.Fatal("UpdateNodeInfo did not persist the learned node to the registry")
	}
}
