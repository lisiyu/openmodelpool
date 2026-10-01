package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeDHTPeerTransport 按地址把 DHT 消息路由到内存里的假 peer；未知/死亡地址
// 直接失败。用于测试 seedless bootstrap 与 bucket refresh，不碰真实网络。
type fakeDHTPeerTransport struct {
	mu    sync.Mutex
	peers map[string]*DHTNode // dht udp addr -> peer node
	dead  map[string]bool
}

func (f *fakeDHTPeerTransport) Send(ctx context.Context, addr string, msg DHTMessage) (DHTMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead[addr] {
		return DHTMessage{}, errors.New("fake: peer dead")
	}
	p, ok := f.peers[addr]
	if !ok {
		return DHTMessage{}, errors.New("fake: unknown addr")
	}
	select {
	case <-ctx.Done():
		return DHTMessage{}, ctx.Err()
	default:
	}
	return p.handle(msg), nil
}

// backdateDHTEntry 把某条目的 LastSeen 往前拨 age，用于构造 stale 条目。
func backdateDHTEntry(t *testing.T, n *DHTNode, id DHTNodeID, age time.Duration) {
	t.Helper()
	n.dht.mu.Lock()
	defer n.dht.mu.Unlock()
	for _, b := range n.dht.buckets {
		for _, e := range b.entries {
			if e.NodeID == id {
				e.LastSeen = time.Now().Add(-age)
				return
			}
		}
	}
	t.Fatalf("entry not found in table")
}

// waitForTableSize 等待路由表达到期望大小，超时则失败。
func waitForTableSize(t *testing.T, n *DHTNode, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n.TableSize() == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("table size = %d, want %d after %v", n.TableSize(), want, timeout)
}

// dhtTableHas 报告路由表中是否存在该节点条目。
func dhtTableHas(n *DHTNode, id DHTNodeID) bool {
	n.dht.mu.RLock()
	defer n.dht.mu.RUnlock()
	for _, b := range n.dht.buckets {
		for _, e := range b.entries {
			if e.NodeID == id {
				return true
			}
		}
	}
	return false
}

func TestDHTStaleEntriesAndRemoveNode(t *testing.T) {
	d := NewDHT("self-stale")
	oldPeer := NewDHTNode("old-peer", "fake://old", nil)
	newPeer := NewDHTNode("new-peer", "fake://new", nil)
	d.AddNode(&DHTEntry{NodeID: oldPeer.ID(), Addresses: []string{"fake://old"}})
	d.AddNode(&DHTEntry{NodeID: newPeer.ID(), Addresses: []string{"fake://new"}})

	// 把 old 条目拨老。
	d.mu.Lock()
	for _, b := range d.buckets {
		for _, e := range b.entries {
			if e.NodeID == oldPeer.ID() {
				e.LastSeen = time.Now().Add(-2 * dhtRefresh)
			}
		}
	}
	d.mu.Unlock()

	stale := d.StaleEntries(dhtRefresh)
	if len(stale) != 1 || stale[0].NodeID != oldPeer.ID() {
		t.Fatalf("stale entries = %d, want exactly the old peer", len(stale))
	}
	if !d.RemoveNode(oldPeer.ID()) {
		t.Fatal("RemoveNode(old) = false, want true")
	}
	if d.RemoveNode(oldPeer.ID()) {
		t.Fatal("RemoveNode(old) twice = true, want false")
	}
	if d.TotalNodes() != 1 {
		t.Fatalf("total nodes = %d, want 1", d.TotalNodes())
	}
}

func TestRefreshBuckets_EvictsDeadKeepsLive(t *testing.T) {
	ft := &fakeDHTPeerTransport{peers: map[string]*DHTNode{}, dead: map[string]bool{}}
	livePeer := NewDHTNode("live-peer", "fake://live", nil)
	ft.peers["fake://live"] = livePeer

	n := NewDHTNode("self-refresh", "fake://self", ft)
	deadPeer := NewDHTNode("dead-peer", "fake://dead", nil)
	freshPeer := NewDHTNode("fresh-peer", "fake://fresh", nil)
	ft.peers["fake://fresh"] = freshPeer

	n.learn(&DHTEntry{NodeID: deadPeer.ID(), Addresses: []string{"fake://dead"}})
	n.learn(&DHTEntry{NodeID: livePeer.ID(), Addresses: []string{"fake://live"}})
	n.learn(&DHTEntry{NodeID: freshPeer.ID(), Addresses: []string{"fake://fresh"}})
	backdateDHTEntry(t, n, deadPeer.ID(), 2*dhtRefresh)
	backdateDHTEntry(t, n, livePeer.ID(), 2*dhtRefresh)
	// fresh-peer 保持新鲜：不应被 ping。

	pinged, evicted := n.RefreshBuckets(context.Background())
	if pinged != 2 {
		t.Fatalf("pinged = %d, want 2 (dead + stale-live)", pinged)
	}
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1 (dead only)", evicted)
	}
	if n.dht.RemoveNode(deadPeer.ID()) {
		t.Fatal("dead entry still in table after refresh")
	}
	if n.TableSize() != 2 {
		t.Fatalf("table size = %d, want 2", n.TableSize())
	}
	// 活条目的 LastSeen 应被刷新到最近。
	stale := n.dht.StaleEntries(dhtRefresh)
	for _, e := range stale {
		if e.NodeID == livePeer.ID() {
			t.Fatal("live entry still stale after successful ping")
		}
	}
	if _, ok := n.addrOf(livePeer.ID()); !ok {
		t.Fatal("live entry dropped from addrBook")
	}
	if _, ok := n.addrOf(deadPeer.ID()); ok {
		t.Fatal("dead entry still in addrBook after eviction")
	}
}

func TestRefreshBuckets_NilTransport(t *testing.T) {
	n := NewDHTNode("self-nil", "fake://self", nil)
	if p, e := n.RefreshBuckets(context.Background()); p != 0 || e != 0 {
		t.Fatalf("nil transport: got (%d,%d), want (0,0)", p, e)
	}
}

func TestRetrySeedlessBootstrap_ReactivatesWhenTableEmptied(t *testing.T) {
	oldFed, oldNode, oldRegistry := fed, node, nodeRegistry
	oldInterval := dhtSeedlessRetryInterval
	fed = newRejoinTestFed(t)
	node = nil         // trySeedlessBootstrapOnce 走 selfID == "" 分支
	nodeRegistry = nil // 本测试不碰磁盘持久化
	dhtSeedlessRetryInterval = 50 * time.Millisecond

	ft := &fakeDHTPeerTransport{peers: map[string]*DHTNode{}, dead: map[string]bool{}}
	peerA := NewDHTNode("peer-a", "fake://a", nil)
	peerB := NewDHTNode("peer-b", "fake://b", nil)
	ft.peers["fake://a"] = peerA
	ft.peers["fake://b"] = peerB
	fed.NoteDHTHint("mmx-peer-a", "fake://a")
	fed.NoteDHTHint("mmx-peer-b", "fake://b")

	n := NewDHTNode("self-loop", "fake://self", ft)
	stopCh := make(chan struct{})
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		n.retrySeedlessBootstrap(stopCh)
	}()
	defer func() {
		close(stopCh)
		<-loopDone // 等 loop 真正退出再恢复全局，避免与循环内的全局读取竞态
		fed, node, nodeRegistry = oldFed, oldNode, oldRegistry
		dhtSeedlessRetryInterval = oldInterval
	}()

	// 第一轮：表空 → 从某个 hint bootstrap 成功。
	waitForTableSize(t, n, 1, 5*time.Second)

	// trySeedlessBootstrapOnce returns after the FIRST successful hint, and
	// DHTBootstrapAddrs iterates a map, so the peer that lands in the table is
	// A or B depending on map order. Build the "mass death" scenario around
	// whoever actually made it in instead of assuming A.
	dead, live := peerA, peerB
	if !dhtTableHas(n, dead.ID()) {
		dead, live = peerB, peerA
	}
	ft.mu.Lock()
	ft.dead[dead.Addr()] = true
	ft.mu.Unlock()
	if !n.dht.RemoveNode(dead.ID()) {
		t.Fatal("in-table peer not in table, test setup broken")
	}
	if n.TableSize() != 0 {
		t.Fatalf("table size = %d, want 0 after simulated mass death", n.TableSize())
	}

	// loop 常驻：下一轮 tick 发现表空 → 跳过已死的那个，从存活 hint 重新 bootstrap。
	waitForTableSize(t, n, 1, 5*time.Second)
	if !dhtTableHas(n, live.ID()) {
		t.Fatal("table reactivated but the surviving hint peer is not in it (failover to live hint failed)")
	}
}

func TestSaveDHTAddr_PersistAndRestore(t *testing.T) {
	dir := t.TempDir()
	r := NewNodeRegistry(dir)

	// 先有验证地址（无既有文件时也能写）。
	r.SaveDHTAddr("mmx-peer-x", "10.0.0.5:19001")
	// 后续 SaveNode（不带 DHTAddr）不得把验证地址清掉。
	r.SaveNode(&RouteEntry{
		NodeID:    "mmx-peer-x",
		Addresses: []string{"http://10.0.0.5:8080"},
		LastSeen:  time.Now(),
	})

	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d entries, want 1", len(loaded))
	}
	if loaded[0].DHTAddr != "10.0.0.5:19001" {
		t.Fatalf("DHTAddr = %q, want %q", loaded[0].DHTAddr, "10.0.0.5:19001")
	}

	// 同一节点重复写入相同地址：文件不应被改动（mtime 不变）。
	path := filepath.Join(dir, registryFileName("mmx-peer-x"))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	r.SaveDHTAddr("mmx-peer-x", "10.0.0.5:19001")
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("SaveDHTAddr rewrote file on identical addr (should skip no-op write)")
	}

	// 地址变更时更新。
	r.SaveDHTAddr("mmx-peer-x", "10.0.0.6:19001")
	loaded2, err := r.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if loaded2[0].DHTAddr != "10.0.0.6:19001" {
		t.Fatalf("DHTAddr = %q, want updated addr", loaded2[0].DHTAddr)
	}

	// 空输入是 no-op。
	r.SaveDHTAddr("", "10.0.0.7:19001")
	r.SaveDHTAddr("mmx-peer-x", "")
	loaded3, _ := r.LoadAll()
	if len(loaded3) != 1 || loaded3[0].DHTAddr != "10.0.0.6:19001" {
		t.Fatal("empty SaveDHTAddr input disturbed persisted state")
	}
}

func TestDHTNodeIDForAddr(t *testing.T) {
	fed := newRejoinTestFed(t)
	fed.NoteDHTHint("mmx-peer-a", "10.0.0.2:19001")
	fed.NoteDHTHint("mmx-peer-b", "10.0.0.3:19001")
	if got := fed.DHTNodeIDForAddr("10.0.0.2:19001"); got != "mmx-peer-a" {
		t.Fatalf("DHTNodeIDForAddr = %q, want mmx-peer-a", got)
	}
	if got := fed.DHTNodeIDForAddr("10.9.9.9:19001"); got != "" {
		t.Fatalf("DHTNodeIDForAddr(unknown) = %q, want \"\"", got)
	}
	if got := fed.DHTNodeIDForAddr(""); got != "" {
		t.Fatalf("DHTNodeIDForAddr(\"\") = %q, want \"\"", got)
	}
}
