package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHandleNetworkHeartbeatRecords verifies the RECEIVING side of the
// node-to-node heartbeat: a well-authenticated heartbeat from a known sender
// must (a) return 200, (b) refresh that sender's LastHeartbeat / status in
// the shared global pool, and (c) bump the sender's LastSeen in the local
// network-manager peer record.
func TestHandleNetworkHeartbeatRecords(t *testing.T) {
	// federation secret for auth
	cfg = &Config{data: map[string]any{"federation_secret": "s3cr3t"}}
	t.Cleanup(func() { cfg = nil })

	// shared global pool with a stale, degraded participant
	stale := time.Now().Add(-20 * time.Minute)
	gp := &GlobalPool{
		NodeContributions: map[string]int64{"peer-abc": 10000},
		NodeConsumptions:  map[string]int64{"peer-abc": 0},
		ParticipantNodes: []GlobalPoolNode{
			{NodeID: "peer-abc", Status: "degraded", LastHeartbeat: stale},
		},
	}
	globalPool = gp
	t.Cleanup(func() { globalPool = nil })

	// local peer record for the sender
	nm := &NetworkManager{config: NetworkConfig{
		Mode: NetworkModeShared,
		Peers: []PeerInfo{
			{NodeID: "peer-abc", Status: "online", LastSeen: time.Now().Add(-30 * time.Minute).Format(time.RFC3339)},
		},
	}}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })

	// send the heartbeat
	body := strings.NewReader(`{"node_id":"peer-abc","endpoint":"https://peer.example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/network/heartbeat", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", "peer-abc")
	req.Header.Set("X-Federation-Secret", "s3cr3t")
	rec := httptest.NewRecorder()
	handleNetworkHeartbeat(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// (b) global pool heartbeat effect
	nodes := globalPool.GetNodes()
	var found *GlobalPoolNode
	for i := range nodes {
		if nodes[i].NodeID == "peer-abc" {
			found = &nodes[i]
		}
	}
	if found == nil {
		t.Fatal("participant peer-abc missing from global pool")
	}
	if found.Status != "active" {
		t.Errorf("pool status = %q, want active", found.Status)
	}
	if time.Since(found.LastHeartbeat) > time.Minute {
		t.Errorf("pool LastHeartbeat not refreshed: %v", found.LastHeartbeat)
	}

	// (c) local peer LastSeen refreshed
	nm.mu.RLock()
	var ls string
	for _, p := range nm.config.Peers {
		if p.NodeID == "peer-abc" {
			ls = p.LastSeen
		}
	}
	nm.mu.RUnlock()
	if ls == "" {
		t.Fatal("peer peer-abc not present after heartbeat")
	}
	parsed, err := time.Parse(time.RFC3339, ls)
	if err != nil {
		t.Fatalf("peer LastSeen not RFC3339: %q", ls)
	}
	if time.Since(parsed) > time.Minute {
		t.Errorf("peer LastSeen not refreshed: %q", ls)
	}
}

// TestPostHeartbeatToPeer verifies the SENDING helper: it must POST the
// heartbeat JSON to the given peer URL with the correct X-Node-ID and
// X-Federation-Secret headers, and return no error on a 200.
func TestPostHeartbeatToPeer(t *testing.T) {
	var gotNodeID, gotSecret, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotNodeID = r.Header.Get("X-Node-ID")
		gotSecret = r.Header.Get("X-Federation-Secret")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	err := postHeartbeatToPeer(client, srv.URL+"/api/network/heartbeat", "mm-self", "https://self.example.com", "s3cr3t")
	if err != nil {
		t.Fatalf("postHeartbeatToPeer returned error: %v", err)
	}

	if gotNodeID != "mm-self" {
		t.Errorf("X-Node-ID = %q, want mm-self", gotNodeID)
	}
	if gotSecret != "s3cr3t" {
		t.Errorf("X-Federation-Secret = %q, want s3cr3t", gotSecret)
	}

	var parsed struct {
		NodeID  string `json:"node_id"`
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal([]byte(gotBody), &parsed); err != nil {
		t.Fatalf("body not valid JSON: %v (body=%s)", err, gotBody)
	}
	if parsed.NodeID != "mm-self" {
		t.Errorf("body node_id = %q, want mm-self", parsed.NodeID)
	}
	if parsed.Endpoint != "https://self.example.com" {
		t.Errorf("body endpoint = %q, want https://self.example.com", parsed.Endpoint)
	}
}
