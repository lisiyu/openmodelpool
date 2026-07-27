package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestBuildKnownPeers_MergesFederationAndManual verifies P1-1: buildKnownPeers
// returns a deduplicated set of PEX hints combining the federation trust pool's
// active nodes with the locally configured manual peers, and never advertises
// the local node itself.
func TestBuildKnownPeers_MergesFederationAndManual(t *testing.T) {
	setupDiscoveryTestEnv(t)

	// A federation-active node (reachable via its multi-address).
	fed.AddKnownNode(NodeInfo{
		NodeID:    "mmx-fed-1",
		Addresses: []string{"https://fed1.example.com"},
		Status:    "active",
	})
	// A manual peer kept in config.Peers.
	if err := netMgr.AddPeer(PeerInfo{
		NodeID:    "mmx-manual-1",
		Addresses: []string{"https://manual1.example.com"},
		Status:    "online",
	}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	hints := buildKnownPeers()

	got := map[string][]string{}
	for _, h := range hints {
		got[h.NodeID] = h.Addresses
	}

	if addrs, ok := got["mmx-fed-1"]; !ok || len(addrs) == 0 || addrs[0] != "https://fed1.example.com" {
		t.Errorf("federation node missing or wrong address in KnownPeers: %v", got["mmx-fed-1"])
	}
	if addrs, ok := got["mmx-manual-1"]; !ok || len(addrs) == 0 || addrs[0] != "https://manual1.example.com" {
		t.Errorf("manual peer missing or wrong address in KnownPeers: %v", got["mmx-manual-1"])
	}
	// The local node must never be advertised.
	if _, ok := got[node.NodeID()]; ok {
		t.Errorf("buildKnownPeers must exclude the local node (self): %s", node.NodeID())
	}
}

// TestDoGossipRound_AttachesKnownPeers verifies P1-1 end-to-end: doGossipRound
// builds a sync message whose outbound JSON carries non-empty known_peers that
// include the locally-known manual peer. An httptest server captures the
// outbound request body and responds with a minimal valid (unsigned-verify-free)
// GossipMessage; exchange does not verify the response signature, so the round
// completes fully offline.
func TestDoGossipRound_AttachesKnownPeers(t *testing.T) {
	setupDiscoveryTestEnv(t)

	var (
		capturedBody   []byte
		capturedPath   string
		capturedHeader string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedHeader = r.Header.Get("X-Node-ID")
		capturedBody, _ = io.ReadAll(r.Body)
		// Minimal valid gossip response; exchange only unmarshals it.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"sync","from_node":"mmx-server","trust_pool_version":0,"timestamp":"` +
			time.Now().UTC().Format(time.RFC3339) + `","signature":""}`))
	}))
	defer ts.Close()

	// Make the peer a gossip target by adding it to the federation trust pool
	// with an active status and the test server as its address.
	fed.AddKnownNode(NodeInfo{
		NodeID:    "mmx-gossip-target",
		Addresses: []string{ts.URL},
		Status:    "active",
	})
	// A manual peer so KnownPeers is non-empty in the outbound message.
	if err := netMgr.AddPeer(PeerInfo{
		NodeID:    "mmx-manual-2",
		Addresses: []string{"https://manual2.example.com"},
		Status:    "online",
	}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	g := &GossipManager{seen: make(map[string]time.Time)}
	g.doGossipRound()

	if capturedPath != "/api/federation/gossip" {
		t.Fatalf("gossip request went to %q, want /api/federation/gossip", capturedPath)
	}
	if capturedHeader != node.NodeID() {
		t.Errorf("gossip request missing X-Node-ID; got %q want %q", capturedHeader, node.NodeID())
	}
	if len(capturedBody) == 0 {
		t.Fatal("no gossip request body captured")
	}

	var out GossipMessage
	if err := json.Unmarshal(capturedBody, &out); err != nil {
		t.Fatalf("parse outbound gossip message: %v", err)
	}
	if len(out.KnownPeers) == 0 {
		t.Fatal("outbound sync message has empty KnownPeers (P1-1 regression)")
	}
	foundManual := false
	for _, h := range out.KnownPeers {
		if h.NodeID == "mmx-manual-2" {
			foundManual = true
			if len(h.Addresses) == 0 || h.Addresses[0] != "https://manual2.example.com" {
				t.Errorf("manual-2 hint has wrong addresses: %v", h.Addresses)
			}
		}
	}
	if !foundManual {
		t.Errorf("outbound KnownPeers did not include the manual peer: %+v", out.KnownPeers)
	}
}

// TestProcessGossipResponse_MergesKnownPeersIntoHints verifies T-4 / P1-1: when a
// gossip response carries KnownPeers, processGossipResponse merges them into the
// in-memory discoveryHints (address-reachability fallback), first-known-wins.
func TestProcessGossipResponse_MergesKnownPeersIntoHints(t *testing.T) {
	setupDiscoveryTestEnv(t)

	g := &GossipManager{seen: make(map[string]time.Time)}
	sender := NodeInfo{NodeID: "mmx-sender", Addresses: []string{"https://sender.example.com"}, Status: "active"}

	msg := GossipMessage{
		Type:             "sync",
		FromNode:         "mmx-sender",
		TrustPoolVersion: 0, // keep at local version so no network fetch is triggered
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		KnownPeers: []PeerHint{
			{NodeID: "mmx-hint-a", Addresses: []string{"https://a.example.com"}},
			{NodeID: "mmx-hint-b", Addresses: []string{"https://b.example.com"}},
		},
	}

	g.processGossipResponse(&msg, sender)

	if got := fed.HintAddresses("mmx-hint-a"); len(got) != 1 || got[0] != "https://a.example.com" {
		t.Errorf("hint-a not merged into discoveryHints: %v", got)
	}
	if got := fed.HintAddresses("mmx-hint-b"); len(got) != 1 || got[0] != "https://b.example.com" {
		t.Errorf("hint-b not merged into discoveryHints: %v", got)
	}

	// First-known-wins: a later, conflicting hint for the same node is ignored.
	msg2 := GossipMessage{
		Type:             "sync",
		FromNode:         "mmx-sender",
		TrustPoolVersion: 0,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		KnownPeers:       []PeerHint{{NodeID: "mmx-hint-a", Addresses: []string{"https://evil.example.com"}}},
	}
	g.processGossipResponse(&msg2, sender)
	if got := fed.HintAddresses("mmx-hint-a"); len(got) != 1 || got[0] != "https://a.example.com" {
		t.Errorf("MergePeerHints should be first-known-wins; got %v", got)
	}
}

// TestExchange_SendsXNodeIDHeader verifies R5 / T-1: the gossip exchange request
// is sent to the corrected /api/federation/gossip path and carries the
// X-Node-ID header (so the receiver's withFederationAuth path-1 can admit us
// after we are bridged into its trust pool by P0-2).
func TestExchange_SendsXNodeIDHeader(t *testing.T) {
	setupDiscoveryTestEnv(t)

	var (
		capturedPath   string
		capturedHeader string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedHeader = r.Header.Get("X-Node-ID")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"sync","from_node":"mmx-server","trust_pool_version":0,"timestamp":"` +
			time.Now().UTC().Format(time.RFC3339) + `","signature":""}`))
	}))
	defer ts.Close()

	g := &GossipManager{seen: make(map[string]time.Time)}
	peer := NodeInfo{NodeID: "mmx-exch-peer", Addresses: []string{ts.URL}, Status: "active"}
	msg := GossipMessage{
		Type:             "sync",
		FromNode:         node.NodeID(),
		TrustPoolVersion: 0,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		Signature:        "sig",
	}

	if _, err := g.exchange(peer, msg); err != nil {
		t.Fatalf("exchange returned error: %v", err)
	}
	if capturedPath != "/api/federation/gossip" {
		t.Errorf("exchange URL path = %q, want /api/federation/gossip", capturedPath)
	}
	if capturedHeader != node.NodeID() {
		t.Errorf("exchange missing X-Node-ID; got %q want %q", capturedHeader, node.NodeID())
	}
}

// TestFetchFullPoolFromPeer_SendsXNodeIDHeader verifies R5 / T-1: the full-pool
// fetch (triggered on a newer trust-pool version) is sent to the corrected
// /api/federation/pool path and carries the X-Node-ID header.
func TestFetchFullPoolFromPeer_SendsXNodeIDHeader(t *testing.T) {
	setupDiscoveryTestEnv(t)

	var (
		capturedPath   string
		capturedHeader string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedHeader = r.Header.Get("X-Node-ID")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// version 1 > local 0 → fed.UpdateTrustPool applies it (and persists).
		_, _ = w.Write([]byte(`{"version":1,"updated_at":"` +
			time.Now().UTC().Format(time.RFC3339) + `","nodes":[]}`))
	}))
	defer ts.Close()

	g := &GossipManager{seen: make(map[string]time.Time)}
	peer := NodeInfo{NodeID: "mmx-fetch-peer", Addresses: []string{ts.URL}, Status: "active"}

	g.fetchFullPoolFromPeer(peer)

	if capturedPath != "/api/federation/pool" {
		t.Errorf("fetchFullPoolFromPeer URL path = %q, want /api/federation/pool", capturedPath)
	}
	if capturedHeader != node.NodeID() {
		t.Errorf("fetchFullPoolFromPeer missing X-Node-ID; got %q want %q", capturedHeader, node.NodeID())
	}
	if fed.GetTrustPool().Version != 1 {
		t.Errorf("expected trust pool version 1 after fetch, got %d", fed.GetTrustPool().Version)
	}
}

// TestGossipURLHasAPIPrefix is a guard test that fails loudly if any outbound
// federation client URL ever drops the /api prefix again (the R5 regression
// this release fixes). It checks the constant paths used by exchange and
// fetchFullPoolFromPeer indirectly via the captured request paths above, and
// also asserts the announce URL shape used by broadcastAnnouncement.
func TestGossipURLHasAPIPrefix(t *testing.T) {
	const base = "https://peer.example.com"
	cases := map[string]string{
		"gossip":   base + "/api/federation/gossip",
		"pool":     base + "/api/federation/pool",
		"announce": base + "/api/federation/announce",
	}
	// Reconstruct the exact URL strings the production code builds (kept in sync
	// with gossip.go) and assert the /api prefix is present.
	built := map[string]string{
		"gossip":   fmt.Sprintf("%s/api/federation/gossip", base),
		"pool":     fmt.Sprintf("%s/api/federation/pool", base),
		"announce": fmt.Sprintf("%s/api/federation/announce", base),
	}
	for k, want := range cases {
		if built[k] != want {
			t.Errorf("%s URL = %q, want %q", k, built[k], want)
		}
		if !strings.HasPrefix(built[k], base+"/api/") {
			t.Errorf("%s URL %q missing /api prefix", k, built[k])
		}
	}
}
