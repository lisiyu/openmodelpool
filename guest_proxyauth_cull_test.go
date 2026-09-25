package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Guest key direct /v1 access (withProxyAuth fix)
//
// Regression: the share centre hands guests an {origin}/v1 address, but
// withProxyAuth only recognized public/proxy/consumer keys and returned 401
// for every guest key. These tests pin the new behaviour: a sk-guest key held
// in this node's store is accepted on /v1 endpoints and marked with the same
// role the /network/{node_id} relay path derives (local-only guest, or public
// when the node is in shared mode and the key therefore has public-pool
// access). Keys absent from the store stay rejected (fail-closed).
// ---------------------------------------------------------------------------

func TestWithProxyAuth_AcceptsValidGuestKey(t *testing.T) {
	setupTestEnv(t)

	origStore := guestKeyStore
	guestKeyStore = &GuestKeyStore{
		keys: []*GuestKeyRecord{
			{Key: "sk-guest-mmx-issuer-abc123", NodeID: "mmx-issuer", Revoked: false},
		},
	}
	defer func() { guestKeyStore = origStore }()

	var gotRole, gotOwner, gotKeyType string
	okHandler := func(w http.ResponseWriter, r *http.Request) {
		gotRole = r.Header.Get("X-Request-Role")
		gotOwner = r.Header.Get("X-Request-Owner")
		gotKeyType = RequestKeyType(r)
		w.WriteHeader(http.StatusOK)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-guest-mmx-issuer-abc123")
	rec := httptest.NewRecorder()
	withProxyAuth(okHandler)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid guest key expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	// netMgr is nil here 鈫?v3.2 falls back to personal mode: local resources only.
	if gotRole != "guest" {
		t.Fatalf("expected X-Request-Role=guest, got %q", gotRole)
	}
	if gotOwner != "" {
		t.Fatalf("expected empty X-Request-Owner, got %q", gotOwner)
	}
	if gotKeyType != "guest" {
		t.Fatalf("expected RequestKeyType=guest, got %q", gotKeyType)
	}
}

func TestWithProxyAuth_GuestKeySharedModeGetsPublicRole(t *testing.T) {
	setupTestEnv(t)

	origStore := guestKeyStore
	origNetMgr := netMgr
	guestKeyStore = &GuestKeyStore{
		keys: []*GuestKeyRecord{{Key: "sk-guest-mmx-self-a1b2c3d4", NodeID: "mmx-self", Revoked: false}},
	}
	netMgr = &NetworkManager{config: NetworkConfig{Mode: NetworkModeShared}}
	defer func() { guestKeyStore = origStore; netMgr = origNetMgr }()

	var gotRole string
	okHandler := func(w http.ResponseWriter, r *http.Request) {
		gotRole = r.Header.Get("X-Request-Role")
		w.WriteHeader(http.StatusOK)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-guest-mmx-self-a1b2c3d4")
	rec := httptest.NewRecorder()
	withProxyAuth(okHandler)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid guest key expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	// Shared mode 鈫?v3.2 grants public-pool access, exactly like the relay path.
	if gotRole != "public" {
		t.Fatalf("expected X-Request-Role=public in shared mode, got %q", gotRole)
	}
}

func TestWithProxyAuth_RejectsUnknownGuestKey(t *testing.T) {
	setupTestEnv(t)

	origStore := guestKeyStore
	guestKeyStore = &GuestKeyStore{keys: []*GuestKeyRecord{}}
	defer func() { guestKeyStore = origStore }()

	okHandler := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-guest-mmx-unknown-zzzzffff")
	rec := httptest.NewRecorder()
	withProxyAuth(okHandler)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown guest key expected 401, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// A local-only guest key (no public-pool access) must never be gateway-
// forwarded to a remote node — the remote does not hold the key and would 401.
// When a remote route exists for the model we assert the request FALLS BACK
// to local handling (501 embeddings) instead of attempting an outbound relay
// (which would surface as 502).
func TestGatewayRequest_LocalOnlyGuestNotForwarded(t *testing.T) {
	setupTestEnv(t)

	origStore := guestKeyStore
	origNetMgr := netMgr
	origRouteTable := routeTable
	guestKeyStore = &GuestKeyStore{
		keys: []*GuestKeyRecord{{Key: "sk-guest-mmx-self-c5g1x2p3", NodeID: "mmx-self", Revoked: false}},
	}
	// Personal mode (netMgr nil) → v3.2 resolves no public-pool access.
	plusNetMgr := &NetworkManager{config: NetworkConfig{Mode: NetworkModePersonal}}
	netMgr = plusNetMgr
	routeTable = &RouteTable{entries: map[string]*RouteEntry{
		"mmx-remote": {
			NodeID:    "mmx-remote",
			Models:    []string{"m1"},
			Addresses: []string{"https://relay.invalid"},
		},
	}}
	defer func() { guestKeyStore = origStore; netMgr = origNetMgr; routeTable = origRouteTable }()

	body := `{"model":"m1","stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-guest-mmx-self-c5g1x2p3")
	rec := httptest.NewRecorder()
	handleGatewayRequest(rec, req)
	// 501 = local fallback (embeddings unsupported locally); 502 = relay attempt.
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("local-only guest gateway request expected 501 (local fallback), got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Inactive peer culling
// ---------------------------------------------------------------------------

func TestCullInactivePeers(t *testing.T) {
	setupTestEnv(t)

	now := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)

	origNetMgr := netMgr
	origFed := fed
	origRouteTable := routeTable
	netMgr = &NetworkManager{
		config: NetworkConfig{
			Mode: NetworkModeShared,
			Peers: []PeerInfo{
				{NodeID: "mmx-manual-dead", LastSeen: old},
				{NodeID: "mmx-manual-alive", LastSeen: now},
				{NodeID: "mmx-manual-never", LastSeen: ""},
			},
		},
		dataPath: filepath.Join(t.TempDir(), "network.json"),
	}
	fed = &FederationManager{
		trustPool: TrustPool{Nodes: []NodeInfo{
			{NodeID: "mmx-pool-dead", LastSeen: old},
			{NodeID: "mmx-pool-alive", LastSeen: now},
		}},
	}
	routeTable = &RouteTable{entries: map[string]*RouteEntry{
		"mmx-pool-dead":   {NodeID: "mmx-pool-dead"},
		"mmx-manual-dead": {NodeID: "mmx-manual-dead"},
	}}
	defer func() { netMgr = origNetMgr; fed = origFed; routeTable = origRouteTable }()

	cullInactivePeers(netMgr)

	// 1. Trust pool: stale node gone, fresh node kept.
	pool := fed.GetTrustPool()
	seenPool := map[string]bool{}
	for _, n := range pool.Nodes {
		seenPool[n.NodeID] = true
	}
	if seenPool["mmx-pool-dead"] {
		t.Fatal("stale trust-pool node was not culled")
	}
	if !seenPool["mmx-pool-alive"] {
		t.Fatal("fresh trust-pool node was wrongly culled")
	}

	// 2. Route table: stale entries removed for both sources.
	if e := routeTable.Get("mmx-pool-dead"); e != nil {
		t.Fatal("stale trust-pool route entry was not removed")
	}
	if e := routeTable.Get("mmx-manual-dead"); e != nil {
		t.Fatal("stale manual-peer route entry was not removed")
	}

	// 3. Manual peers: stale gone, fresh and never-seen (=empty LastSeen) kept.
	peers := netMgr.GetPeers()
	seen := map[string]bool{}
	for _, p := range peers {
		seen[p.NodeID] = true
	}
	if seen["mmx-manual-dead"] {
		t.Fatal("stale manual peer was not culled")
	}
	if !seen["mmx-manual-alive"] {
		t.Fatal("fresh manual peer was wrongly culled")
	}
	if !seen["mmx-manual-never"] {
		t.Fatal("peer with empty LastSeen was wrongly culled")
	}
}

func TestCullInactivePeers_NoOpInPersonalMode(t *testing.T) {
	setupTestEnv(t)

	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	origNetMgr := netMgr
	origFed := fed
	netMgr = &NetworkManager{config: NetworkConfig{Mode: NetworkModePersonal}}
	fed = &FederationManager{
		trustPool: TrustPool{Nodes: []NodeInfo{
			{NodeID: "mmx-pool-dead", LastSeen: old},
		}},
	}
	defer func() { netMgr = origNetMgr; fed = origFed }()

	cullInactivePeers(netMgr)

	pool := fed.GetTrustPool()
	if len(pool.Nodes) != 1 || pool.Nodes[0].NodeID != "mmx-pool-dead" {
		t.Fatal("cull must be a no-op outside shared mode")
	}
}

func TestPeerCullDaysConfig(t *testing.T) {
	setupTestEnv(t)

	cfg.Set("network_cull_days", "3")
	if d := peerCullDays(); d != 3 {
		t.Fatalf("expected 3 days, got %d", d)
	}
	cfg.Set("network_cull_days", "0")
	if d := peerCullDays(); d != 0 {
		t.Fatalf("expected 0 (disabled), got %d", d)
	}
	cfg.Set("network_cull_days", "")
	if d := peerCullDays(); d != defaultPeerCullDays {
		t.Fatalf("expected default %d days, got %d", defaultPeerCullDays, d)
	}
}
