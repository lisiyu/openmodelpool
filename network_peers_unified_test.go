package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBuildUnifiedPeerList_ExcludesSelfAndDedups verifies that the network
// peers endpoint now renders the mesh as a single consistent view: every node
// in the federation trust pool (minus self) appears exactly once, and a
// locally configured manual peer that is also in the pool is not duplicated.
func TestBuildUnifiedPeerList_ExcludesSelfAndDedups(t *testing.T) {
	setupDiscoveryTestEnv(t)

	// Three trust pool peers. AddKnownNode forces Status=active and the manual
	// peer bridge may not carry GitHubUser, so status mapping is covered by the
	// peerInfoFromNodeInfo unit test below; here we assert the mesh view.
	fed.AddKnownNode(NodeInfo{
		NodeID:       "mmx-peer-a",
		GitHubUser:   "alice",
		SharedModels: []string{"gpt-4o", "claude-3"},
		Status:       "active",
	})
	fed.AddKnownNode(NodeInfo{
		NodeID:     "mmx-peer-b",
		GitHubUser: "bob",
		Status:     "active",
	})
	// Self must never appear.
	fed.AddKnownNode(NodeInfo{
		NodeID:   node.NodeID(),
		Status:   "active",
		Endpoint: "https://self.example.com",
	})

	// Manual peer for mmx-peer-c (added BEFORE it enters the pool, mirroring the
	// real "添加节点" flow); the pool then learns it with a full identity.
	if err := netMgr.AddPeer(PeerInfo{
		NodeID:    "mmx-peer-c",
		Name:      "carol-manual",
		Addresses: []string{"https://carol.example.com"},
		Status:    "online",
	}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	fed.AddKnownNode(NodeInfo{
		NodeID:     "mmx-peer-c",
		GitHubUser: "carol",
		Status:     "active",
	})

	peers := buildUnifiedPeerList()

	got := map[string]PeerInfo{}
	for _, p := range peers {
		got[p.NodeID] = p
	}

	if len(got) != 3 {
		t.Errorf("got %d peers, want 3 (pool minus self, dedup): %v", len(got), keysOf(got))
	}
	if _, ok := got[node.NodeID()]; ok {
		t.Error("self node must not appear in unified peer list")
	}
	a, ok := got["mmx-peer-a"]
	if !ok {
		t.Fatal("mmx-peer-a missing")
	}
	if a.Name != "alice" || a.Status != "online" {
		t.Errorf("peer-a mapping wrong: name=%q status=%q", a.Name, a.Status)
	}
	if len(a.Models) != 2 || a.Models[0] != "gpt-4o" {
		t.Errorf("peer-a models wrong: %v", a.Models)
	}
	// Dedup: the manual peer for mmx-peer-c must not duplicate the entry, and
	// the pool's GitHubUser should win when present.
	if c := got["mmx-peer-c"]; c.Name != "carol" {
		t.Errorf("peer-c name = %q, want carol (trust pool entry wins)", c.Name)
	}
}

// TestPeerInfoFromNodeInfo_StatusMapping covers the NodeInfo→PeerInfo status
// translation used by the unified peer list.
func TestPeerInfoFromNodeInfo_StatusMapping(t *testing.T) {
	cases := []struct {
		poolStatus string
		want       string
	}{
		{"active", "online"},
		{"inactive", "degraded"},
		{"suspended", "degraded"},
		{"", "online"},
	}
	for _, c := range cases {
		p := peerInfoFromNodeInfo(NodeInfo{NodeID: "mmx-x", Status: c.poolStatus})
		if p.Status != c.want {
			t.Errorf("pool status %q → %q, want %q", c.poolStatus, p.Status, c.want)
		}
	}
}

// TestHandleNetworkPeers_ReturnsUnifiedView drives the actual endpoint to make
// sure the dashboard payload stays backward compatible (array under "peers").
func TestHandleNetworkPeers_ReturnsUnifiedView(t *testing.T) {
	setupDiscoveryTestEnv(t)

	fed.AddKnownNode(NodeInfo{NodeID: "mmx-remote-1", Status: "active"})
	fed.AddKnownNode(NodeInfo{NodeID: "mmx-remote-2", Status: "active"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/peers", nil)
	handleNetworkPeers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		Peers []PeerInfo `json:"peers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Peers) != 2 {
		t.Errorf("got %d peers, want 2", len(out.Peers))
	}
}

func keysOf[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
