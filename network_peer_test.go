package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestNetworkAddPeer_EmptyNodeIDResolvedByPing verifies P0-2: when node_id is
// omitted but a valid address is supplied, handleNetworkAddPeer resolves the
// node_id from the peer's public heartbeat ping endpoint and adds the peer.
func TestNetworkAddPeer_EmptyNodeIDResolvedByPing(t *testing.T) {
	env := setupTestEnv(t)
	nm := &NetworkManager{
		dataPath: filepath.Join(env.dir, "network.json"),
		config:   NetworkConfig{NetworkEnabled: true, Mode: NetworkModeShared},
	}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })
	ensureRouteTable()
	t.Cleanup(func() { routeTable = nil })

	// Fake peer exposing the public heartbeat ping endpoint.
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/network/heartbeat/ping" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "node_id": "mmx-test"})
			return
		}
		http.NotFound(w, r)
	}))
	defer peerSrv.Close()

	body := strings.NewReader(`{"addresses":["` + peerSrv.URL + `"],"node_id":"","name":"` + peerSrv.URL + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkAddPeer(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
		Peer   struct {
			NodeID string `json:"node_id"`
		} `json:"peer"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "added" {
		t.Errorf("status = %q, want added", resp.Status)
	}
	if resp.Peer.NodeID != "mmx-test" {
		t.Errorf("resolved node_id = %q, want mm-test", resp.Peer.NodeID)
	}
}

// TestNetworkAddPeer_MissingAddressAndNodeID verifies that omitting both
// addresses and node_id yields a 400.
func TestNetworkAddPeer_MissingAddressAndNodeID(t *testing.T) {
	env := setupTestEnv(t)
	nm := &NetworkManager{
		dataPath: filepath.Join(env.dir, "network.json"),
		config:   NetworkConfig{NetworkEnabled: true, Mode: NetworkModeShared},
	}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })
	ensureRouteTable()
	t.Cleanup(func() { routeTable = nil })

	req := httptest.NewRequest(http.MethodPost, "/api/network/peers", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkAddPeer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

// TestNetworkAddPeer_NonSharedMode verifies the existing guard: adding a peer
// while the network mode is not "shared" is rejected (shared network not active).
func TestNetworkAddPeer_NonSharedMode(t *testing.T) {
	env := setupTestEnv(t)
	nm := &NetworkManager{
		dataPath: filepath.Join(env.dir, "network.json"),
		config:   NetworkConfig{NetworkEnabled: false, Mode: NetworkModePersonal},
	}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })
	ensureRouteTable()
	t.Cleanup(func() { routeTable = nil })

	body := strings.NewReader(`{"addresses":["https://peer.example.com"],"node_id":"mmx-other"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkAddPeer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-shared mode, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}
