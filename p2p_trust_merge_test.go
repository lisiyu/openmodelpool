package main

import (
	"testing"
	"time"
)

// mkMergeNode 构造合并测试用的 NodeInfo。
func mkMergeNode(id, endpoint, status string, lastSeen time.Time) NodeInfo {
	return NodeInfo{
		NodeID:   id,
		Endpoint: endpoint,
		Status:   status,
		LastSeen: lastSeen.UTC().Format(time.RFC3339),
		JoinedAt: lastSeen.UTC().Format(time.RFC3339),
	}
}

func mergeNodeIDs(pool TrustPool) map[string]NodeInfo {
	out := make(map[string]NodeInfo, len(pool.Nodes))
	for _, n := range pool.Nodes {
		out[n.NodeID] = n
	}
	return out
}

// 分区愈合：两边各有独有节点，合并后都可见；版本号取 incoming 的。
func TestMergeTrustPools_PartitionHealKeepsLocalExclusive(t *testing.T) {
	now := time.Now()
	local := TrustPool{Version: 5, Nodes: []NodeInfo{
		mkMergeNode("mmx-a", "http://10.0.0.1:8080", "active", now.Add(-time.Hour)),
		mkMergeNode("mmx-b", "http://10.0.0.2:8080", "active", now.Add(-time.Hour)),
	}}
	incoming := TrustPool{Version: 6, UpdatedAt: "2026-09-30T00:00:00Z", Nodes: []NodeInfo{
		mkMergeNode("mmx-b", "http://10.0.0.2:8080", "active", now.Add(-2*time.Hour)),
		mkMergeNode("mmx-c", "http://10.0.0.3:8080", "active", now.Add(-time.Hour)),
	}}

	merged := mergeTrustPools(local, incoming)
	if merged.Version != 6 {
		t.Fatalf("merged version = %d, want 6 (incoming)", merged.Version)
	}
	if merged.UpdatedAt != "2026-09-30T00:00:00Z" {
		t.Fatalf("merged UpdatedAt = %q, want incoming's", merged.UpdatedAt)
	}
	got := mergeNodeIDs(merged)
	for _, id := range []string{"mmx-a", "mmx-b", "mmx-c"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("node %s missing after partition-heal merge", id)
		}
	}
	// mmx-b 双方都有：本地 LastSeen 更新者胜。
	if got["mmx-b"].Endpoint != "http://10.0.0.2:8080" {
		t.Fatal("unexpected endpoint for mmx-b")
	}
	wantSeen := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	if got["mmx-b"].LastSeen != wantSeen {
		t.Fatalf("mmx-b LastSeen = %q, want local fresher %q", got["mmx-b"].LastSeen, wantSeen)
	}
}

// 本地独有的 suspended/inactive 节点不被复活。
func TestMergeTrustPools_SuspendedNotResurrected(t *testing.T) {
	now := time.Now()
	local := TrustPool{Version: 5, Nodes: []NodeInfo{
		mkMergeNode("mmx-dead", "http://10.0.0.9:8080", "suspended", now.Add(-time.Hour)),
		mkMergeNode("mmx-gone", "http://10.0.0.8:8080", "inactive", now.Add(-time.Hour)),
		mkMergeNode("mmx-live", "http://10.0.0.7:8080", "active", now.Add(-time.Hour)),
	}}
	incoming := TrustPool{Version: 6, Nodes: []NodeInfo{
		mkMergeNode("mmx-c", "http://10.0.0.3:8080", "active", now.Add(-time.Hour)),
	}}

	merged := mergeTrustPools(local, incoming)
	got := mergeNodeIDs(merged)
	if _, ok := got["mmx-dead"]; ok {
		t.Fatal("suspended local-only node was resurrected by merge")
	}
	if _, ok := got["mmx-gone"]; ok {
		t.Fatal("inactive local-only node was resurrected by merge")
	}
	if _, ok := got["mmx-live"]; !ok {
		t.Fatal("active local-only node lost in merge")
	}
	if _, ok := got["mmx-c"]; !ok {
		t.Fatal("incoming node lost in merge")
	}
}

// LastSeen 解析失败时保留 incoming 的数据（不 panic、不丢节点）。
func TestMergeTrustPools_UnparseableLastSeenKeepsIncoming(t *testing.T) {
	local := TrustPool{Version: 5, Nodes: []NodeInfo{
		{NodeID: "mmx-b", Endpoint: "http://local:8080", Status: "active", LastSeen: "not-a-time"},
	}}
	incoming := TrustPool{Version: 6, Nodes: []NodeInfo{
		{NodeID: "mmx-b", Endpoint: "http://incoming:8080", Status: "active", LastSeen: "also-bad"},
	}}
	merged := mergeTrustPools(local, incoming)
	got := mergeNodeIDs(merged)
	if got["mmx-b"].Endpoint != "http://incoming:8080" {
		t.Fatalf("unparseable LastSeen: got endpoint %q, want incoming's", got["mmx-b"].Endpoint)
	}
}

// 端到端：UpdateTrustPool 旧版本忽略、新版本按节点合并。
func TestUpdateTrustPool_MergesOnNewerVersion(t *testing.T) {
	fed := newRejoinTestFed(t)
	now := time.Now()
	fed.trustPool = TrustPool{Version: 5, Nodes: []NodeInfo{
		mkMergeNode("mmx-a", "http://10.0.0.1:8080", "active", now.Add(-time.Hour)),
	}}

	// 旧版本：忽略。
	fed.UpdateTrustPool(TrustPool{Version: 4, Nodes: []NodeInfo{
		mkMergeNode("mmx-z", "http://10.0.0.9:8080", "active", now),
	}})
	if fed.trustPool.Version != 5 || len(fed.trustPool.Nodes) != 1 {
		t.Fatal("older version pool was applied (version monotonicity broken)")
	}

	// 新版本：合并，本地独有节点保留。
	fed.UpdateTrustPool(TrustPool{Version: 6, Nodes: []NodeInfo{
		mkMergeNode("mmx-c", "http://10.0.0.3:8080", "active", now),
	}})
	if fed.trustPool.Version != 6 {
		t.Fatalf("version = %d, want 6", fed.trustPool.Version)
	}
	got := mergeNodeIDs(fed.trustPool)
	if _, ok := got["mmx-a"]; !ok {
		t.Fatal("local-exclusive node mmx-a lost on UpdateTrustPool")
	}
	if _, ok := got["mmx-c"]; !ok {
		t.Fatal("incoming node mmx-c missing after UpdateTrustPool")
	}
}

// ============================================================
// Tombstones：显式下架必须传播并保持，不被合并/八卦复活。
// ============================================================

// 对端的 tombstone 能压制本地的 active 副本：下架传播。
func TestMergeTrustPools_TombstoneSuppressesRemoval(t *testing.T) {
	now := time.Now()
	local := TrustPool{Version: 5, Nodes: []NodeInfo{
		mkMergeNode("mmx-revoked", "http://10.0.0.9:8080", "active", now.Add(-time.Hour)),
		mkMergeNode("mmx-live", "http://10.0.0.7:8080", "active", now.Add(-time.Hour)),
	}}
	incoming := TrustPool{Version: 6, Nodes: []NodeInfo{
		mkMergeNode("mmx-live", "http://10.0.0.7:8080", "active", now.Add(-time.Hour)),
	}, Tombstones: []NodeTombstone{
		{NodeID: "mmx-revoked", RemovedAt: now.UTC().Format(time.RFC3339), Reason: "operator removal"},
	}}

	merged := mergeTrustPools(local, incoming)
	got := mergeNodeIDs(merged)
	if _, ok := got["mmx-revoked"]; ok {
		t.Fatal("tombstoned node resurrected by merge from the local active copy")
	}
	if _, ok := got["mmx-live"]; !ok {
		t.Fatal("unrelated node lost in merge")
	}
	if len(merged.Tombstones) != 1 || merged.Tombstones[0].NodeID != "mmx-revoked" {
		t.Fatalf("tombstone lost in merge: %+v", merged.Tombstones)
	}
}

// LastSeen 严格新于 tombstone 的记录是活着回来：节点恢复，tombstone 清除。
func TestMergeTrustPools_RejoinBeatsTombstone(t *testing.T) {
	now := time.Now()
	removedAt := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	local := TrustPool{Version: 5, Tombstones: []NodeTombstone{
		{NodeID: "mmx-back", RemovedAt: removedAt},
	}}
	incoming := TrustPool{Version: 6, Nodes: []NodeInfo{
		mkMergeNode("mmx-back", "http://10.0.0.5:8080", "active", now),
	}}

	merged := mergeTrustPools(local, incoming)
	got := mergeNodeIDs(merged)
	if _, ok := got["mmx-back"]; !ok {
		t.Fatal("fresh rejoin must beat an older tombstone")
	}
	for _, tb := range merged.Tombstones {
		if tb.NodeID == "mmx-back" {
			t.Fatal("tombstone must be cleared once the node rejoins")
		}
	}
}

// RemoveNode 留 tombstone、 bump 版本；陈旧记录加不回来，新鲜记录可以。
func TestRemoveNode_LeavesTombstoneAndSticks(t *testing.T) {
	fed := newRejoinTestFed(t)
	now := time.Now()
	fed.trustPool = TrustPool{Version: 5, Nodes: []NodeInfo{
		mkMergeNode("mmx-x", "http://10.0.0.9:8080", "active", now.Add(-time.Hour)),
	}}
	before := fed.GetTrustPool().Version

	fed.RemoveNode("mmx-x")
	pool := fed.GetTrustPool()
	if _, ok := mergeNodeIDs(pool)["mmx-x"]; ok {
		t.Fatal("removed node still present")
	}
	if pool.Version <= before {
		t.Fatalf("removal must bump the version (before=%d after=%d) or peers never pull it", before, pool.Version)
	}
	if len(pool.Tombstones) != 1 || pool.Tombstones[0].NodeID != "mmx-x" {
		t.Fatalf("removal must leave a tombstone: %+v", pool.Tombstones)
	}

	// 陈旧记录（早于下架）加不回来。
	fed.AddKnownNode(mkMergeNode("mmx-x", "http://10.0.0.9:8080", "active", now.Add(-2*time.Hour)))
	if _, ok := fed.GetNode("mmx-x"); ok {
		t.Fatal("stale record resurrected a tombstoned node")
	}
	// 新鲜记录（严格晚于下架；+1min 而非 now，避开 RFC3339 秒级粒度的
	// 同秒误判）是活着回来：接受并清除 tombstone。
	fed.AddKnownNode(mkMergeNode("mmx-x", "http://10.0.0.9:8080", "active", now.Add(time.Minute)))
	if _, ok := fed.GetNode("mmx-x"); !ok {
		t.Fatal("fresh rejoin must be accepted")
	}
	for _, tb := range fed.GetTrustPool().Tombstones {
		if tb.NodeID == "mmx-x" {
			t.Fatal("tombstone must be cleared on rejoin")
		}
	}
}

// 八卦单节点 upsert 同样受 tombstone 约束。
func TestUpdateNodeInfo_TombstonedSuppressed(t *testing.T) {
	fed := newRejoinTestFed(t)
	now := time.Now()
	fed.trustPool = TrustPool{Version: 5, Tombstones: []NodeTombstone{
		{NodeID: "mmx-x", RemovedAt: now.UTC().Format(time.RFC3339)},
	}}

	fed.UpdateNodeInfo(mkMergeNode("mmx-x", "http://10.0.0.9:8080", "active", now.Add(-time.Hour)))
	if _, ok := fed.GetNode("mmx-x"); ok {
		t.Fatal("stale gossip must not resurrect a tombstoned node")
	}
	fed.UpdateNodeInfo(mkMergeNode("mmx-x", "http://10.0.0.9:8080", "active", now.Add(time.Minute)))
	if _, ok := fed.GetNode("mmx-x"); !ok {
		t.Fatal("fresh gossip rejoin must be accepted")
	}
}

// 重启恢复：tombstone 下的对等点不再被桥接回来；正常对等点保留其持久化的
// LastSeen（而不是被盖上 now）——陈旧者因此仍可被 cullInactivePeers 回收，
// 而不是每次重启都被当成 brand-new。
func TestBridgeRestoredPeers_TombstonedSkippedLastSeenPreserved(t *testing.T) {
	oldFed, oldNode, oldRestored := fed, node, restoredRouteEntries
	t.Cleanup(func() { fed, node, restoredRouteEntries = oldFed, oldNode, oldRestored })

	fed = newRejoinTestFed(t)
	node = &NodeIdentity{}
	node.mu.Lock()
	node.nodeID = "mmx-self"
	node.mu.Unlock()

	now := time.Now()
	stale := now.Add(-10 * 24 * time.Hour)
	fed.trustPool.Tombstones = []NodeTombstone{
		{NodeID: "mmx-removed", RemovedAt: now.Add(-time.Hour).UTC().Format(time.RFC3339)},
	}
	restoredRouteEntries = []*RouteEntry{
		{NodeID: "mmx-removed", Addresses: []string{"http://10.0.0.9:8080"}, LastSeen: stale},
		{NodeID: "mmx-stale", Addresses: []string{"http://10.0.0.8:8080"}, LastSeen: stale},
		{NodeID: "mmx-fresh", Addresses: []string{"http://10.0.0.7:8080"}, LastSeen: now},
	}

	nm := &NetworkManager{}
	nm.bridgeRestoredPeersToFederation()

	if _, ok := fed.GetNode("mmx-removed"); ok {
		t.Fatal("tombstoned peer must not be re-bridged on restart")
	}
	staleNode, ok := fed.GetNode("mmx-stale")
	if !ok {
		t.Fatal("non-tombstoned peer missing after bridge")
	}
	if got, want := staleNode.LastSeen, stale.UTC().Format(time.RFC3339); got != want {
		t.Fatalf("restored LastSeen = %q, want persisted %q", got, want)
	}
	freshNode, ok := fed.GetNode("mmx-fresh")
	if !ok {
		t.Fatal("fresh peer missing after bridge")
	}
	if got, want := freshNode.LastSeen, now.UTC().Format(time.RFC3339); got != want {
		t.Fatalf("fresh LastSeen = %q, want %q", got, want)
	}
}
