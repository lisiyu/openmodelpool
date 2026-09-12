package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// POST /api/admin/update/broadcast — opt-in explicit federation broadcast
// ---------------------------------------------------------------------------

// The admin broadcast handler must refuse to run when the manager is missing.
func TestHandleAdminUpdateBroadcast_RequiresManager(t *testing.T) {
	origUpdate := updateManager
	updateManager = nil
	t.Cleanup(func() { updateManager = origUpdate })

	req := httptest.NewRequest(http.MethodPost, "/api/admin/update/broadcast", nil)
	rec := httptest.NewRecorder()
	handleAdminUpdateBroadcast(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

// With a body-supplied target the handler must broadcast that exact version,
// not the version from the cache.
func TestHandleAdminUpdateBroadcast_BodyTarget(t *testing.T) {
	origUpdate := updateManager
	origNode, origFed := node, fed
	um := newTestManager(t)
	um.cache = &versionCache{
		latest:   VersionInfo{CurrentVersion: AppVersion, LatestVersion: "v8.8.8", HasUpdate: true},
		expireAt: time.Now().Add(time.Hour),
	}
	updateManager = um
	node, fed = nil, nil // force the safe no-op in BroadcastUpdateSignal
	t.Cleanup(func() {
		updateManager = origUpdate
		node, fed = origNode, origFed
	})

	req := httptest.NewRequest(http.MethodPost, "/api/admin/update/broadcast",
		strings.NewReader(`{"target_version":"v9.9.9"}`))
	rec := httptest.NewRecorder()
	handleAdminUpdateBroadcast(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json response: %v", err)
	}
	if resp["accepted"] != true {
		t.Fatalf("accepted = %v, want true", resp["accepted"])
	}
	if resp["target"] != "v9.9.9" {
		t.Fatalf("target = %v, want v9.9.9 (body overrides cache)", resp["target"])
	}
}

// Without a body the handler must fall back to the cached latest version.
func TestHandleAdminUpdateBroadcast_DefaultsToLatest(t *testing.T) {
	origUpdate := updateManager
	origNode, origFed := node, fed
	um := newTestManager(t)
	um.cache = &versionCache{
		latest:   VersionInfo{CurrentVersion: AppVersion, LatestVersion: "v8.8.8", HasUpdate: true},
		expireAt: time.Now().Add(time.Hour),
	}
	updateManager = um
	node, fed = nil, nil
	t.Cleanup(func() {
		updateManager = origUpdate
		node, fed = origNode, origFed
	})

	req := httptest.NewRequest(http.MethodPost, "/api/admin/update/broadcast", nil)
	rec := httptest.NewRecorder()
	handleAdminUpdateBroadcast(rec, req)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json response: %v", err)
	}
	if resp["target"] != "v8.8.8" {
		t.Fatalf("target = %v, want v8.8.8 (from cache)", resp["target"])
	}
}

func TestHandleAdminUpdateBroadcast_InvalidBody(t *testing.T) {
	origUpdate := updateManager
	um := newTestManager(t)
	um.cache = &versionCache{
		latest:   VersionInfo{LatestVersion: "v8.8.8"},
		expireAt: time.Now().Add(time.Hour),
	}
	updateManager = um
	t.Cleanup(func() { updateManager = origUpdate })

	req := httptest.NewRequest(http.MethodPost, "/api/admin/update/broadcast",
		strings.NewReader(`{"target_version":`))
	rec := httptest.NewRecorder()
	handleAdminUpdateBroadcast(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleAdminUpdateBroadcast_NoTarget(t *testing.T) {
	origUpdate := updateManager
	um := newTestManager(t)
	um.cache = &versionCache{} // empty latest, no body → nothing to broadcast
	updateManager = um
	t.Cleanup(func() { updateManager = origUpdate })

	req := httptest.NewRequest(http.MethodPost, "/api/admin/update/broadcast", nil)
	rec := httptest.NewRecorder()
	handleAdminUpdateBroadcast(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// E2E: BroadcastUpdateSignal must reach active federation peers over HTTP with
// federation auth attached, record "downloading" status, and a fast 500 peer
// must be left awaiting rather than stalls the origin.
// ---------------------------------------------------------------------------

func TestBroadcastUpdateSignal_DispatchesToActivePeers(t *testing.T) {
	setupDiscoveryTestEnv(t) // initializes node + fed globals
	origUpdate := updateManager
	um := newTestManager(t)
	updateManager = um
	t.Cleanup(func() { updateManager = origUpdate })

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/federation/update-signal" {
			t.Errorf("path = %s, want /api/federation/update-signal", r.URL.Path)
		}
		if r.Header.Get("X-Node-ID") == "" {
			t.Error("federation identity header missing on outgoing signal")
		}
		if node != nil && node.NodeID() != "" && r.Header.Get("X-Node-Signature") == "" {
			t.Error("federation signature header missing on outgoing POST signal")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"accepted":true}`))
	}))
	defer srv.Close()

	// Inject one active peer into the federation trust pool.
	fed.mu.Lock()
	fed.trustPool.Nodes = append(fed.trustPool.Nodes, NodeInfo{
		NodeID:    "peer-e2e",
		Status:    "active",
		Addresses: []string{srv.URL},
	})
	fed.mu.Unlock()

	um.BroadcastUpdateSignal("v9.9.9")

	// The peer status must be recorded synchronously as "downloading".
	var found *UpdateStatus
	for i := range um.ListStatuses() {
		s := um.ListStatuses()[i]
		if s.NodeID == "peer-e2e" {
			found = &s
		}
	}
	if found == nil {
		t.Fatal("broadcast did not record the peer status")
	}
	if found.Phase != PhaseDownloading {
		t.Errorf("phase = %q, want %q", found.Phase, PhaseDownloading)
	}
	if found.TargetVersion != "v9.9.9" {
		t.Errorf("target = %q, want v9.9.9", found.TargetVersion)
	}

	// The per-peer signal goroutine must deliver over HTTP.
	deadline := time.Now().Add(3 * time.Second)
	for atomic.LoadInt32(&calls) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("signal was never delivered to the active peer")
	}
}
