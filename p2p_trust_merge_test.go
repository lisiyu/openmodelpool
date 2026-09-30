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
