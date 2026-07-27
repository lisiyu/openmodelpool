package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTestManager builds an UpdateManager backed by a temp data dir so that
// persistence helpers (save/loadWithIntegrity) have somewhere to write and
// never touch the real working tree.
func newTestManager(t *testing.T) *UpdateManager {
	t.Helper()
	dir := t.TempDir()
	return &UpdateManager{
		local: UpdateStatus{
			Env:           "local",
			Name:          "test-node",
			IsLocal:       true,
			Role:          "origin",
			Phase:         PhaseIdle,
			TargetVersion: AppVersion,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		},
		peers:   make(map[string]UpdateStatus),
		cache:   &versionCache{},
		dataDir: dir,
	}
}

// ---------------------------------------------------------------------------
// T-1: semantic version comparison
// ---------------------------------------------------------------------------
func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"4.1.7", "4.1.7", 0},
		{"4.1.8", "4.1.7", 1},
		{"4.1.7", "4.1.8", -1},
		{"v4.2.0", "4.1.9", 1},
		{"4.1", "4.1.7", -1},
		{"4.1.7", "4.1", 1},
		{"4.10.0", "4.9.9", 1},
		{"5.0.0", "4.99.99", 1},
		{"4.0.0", MinSupportedUpdateVersion, -1},   // old node < min supported
		{AppVersion, MinSupportedUpdateVersion, 0}, // current == min supported
		{"", "4.1.7", -1},
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// T-2: atomic replace (tmp -> rename), Windows .bak backup
// ---------------------------------------------------------------------------
func TestAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "openmodelpool.exe")
	tmpPath := filepath.Join(dir, ".omp-update-4.1.8.tmp")

	if err := os.WriteFile(exePath, []byte("OLD-BINARY"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpPath, []byte("NEW-BINARY-CONTENT"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := atomicReplace(exePath, tmpPath); err != nil {
		t.Fatalf("atomicReplace returned error: %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW-BINARY-CONTENT" {
		t.Errorf("exe not replaced: got %q", string(got))
	}
	// The temp file must be consumed by the rename.
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should be gone after atomic replace")
	}
	// On Windows the original binary must be backed up to <exe>.bak.
	if runtime.GOOS == "windows" {
		bak := exePath + ".bak"
		b, err := os.ReadFile(bak)
		if err != nil {
			t.Fatalf("expected .bak backup on windows: %v", err)
		}
		if string(b) != "OLD-BINARY" {
			t.Errorf(".bak content wrong: got %q", string(b))
		}
	}
}

// ---------------------------------------------------------------------------
// T-2: download failure on HTTP 404 (mocked endpoint, no real GitHub)
// ---------------------------------------------------------------------------
func TestDownloadFile404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	um := newTestManager(t)
	dest := filepath.Join(um.dataDir, "dl.tmp")
	err := um.downloadFile(srv.URL, dest)
	if err == nil {
		t.Fatal("expected error for 404 download, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention HTTP 404, got: %v", err)
	}
	// A failed download must not leave a partial / empty file behind.
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("failed download should not create a destination file")
	}
}

func TestDownloadFileOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("REAL-BINARY-BYTES"))
	}))
	defer srv.Close()

	um := newTestManager(t)
	dest := filepath.Join(um.dataDir, "dl.tmp")
	if err := um.downloadFile(srv.URL, dest); err != nil {
		t.Fatalf("downloadFile error: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("downloaded file is empty")
	}
}

// ---------------------------------------------------------------------------
// T-6: old peer without the update-signal endpoint (HTTP 404) is marked
// "unsupported" rather than stalling the broadcast.
// ---------------------------------------------------------------------------
func TestOldPeerUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	um := newTestManager(t)
	peer := NodeInfo{
		NodeID:    "peer-old",
		Addresses: []string{srv.URL},
	}
	um.sendUpdateSignalToPeer(peer, []byte(`{}`), &http.Client{})

	var found *UpdateStatus
	for i := range um.ListStatuses() {
		s := um.ListStatuses()[i]
		if s.NodeID == "peer-old" {
			found = &s
		}
	}
	if found == nil {
		t.Fatal("peer status was not recorded")
	}
	if found.Phase != PhaseUnsupported {
		t.Errorf("expected phase %q, got %q", PhaseUnsupported, found.Phase)
	}
}

// ---------------------------------------------------------------------------
// T-3 / restart: reconcilePending persistence semantics
// ---------------------------------------------------------------------------

// A pending marker whose target matches the now-running version means the
// previous self-update completed on restart: promote to success and remove
// the marker.
func TestReconcilePendingSuccess(t *testing.T) {
	um := newTestManager(t)
	um.local.Phase = PhaseDownloading // left in-flight from before restart
	um.local.TargetVersion = "4.1.8"

	pending := map[string]any{
		"target_version": AppVersion, // matches running version
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(pending)
	if err := os.WriteFile(filepath.Join(um.dataDir, updatePendingFile), data, 0600); err != nil {
		t.Fatal(err)
	}

	um.reconcilePending()

	if um.local.Phase != PhaseSuccess {
		t.Errorf("expected phase %q, got %q", PhaseSuccess, um.local.Phase)
	}
	if um.local.Progress != 100 {
		t.Errorf("expected progress 100, got %d", um.local.Progress)
	}
	if _, err := os.Stat(filepath.Join(um.dataDir, updatePendingFile)); !os.IsNotExist(err) {
		t.Error("pending marker should be removed after successful reconcile")
	}
}

// No pending marker + an in-flight phase left over from a crashed restart
// must be reset to idle so the UI does not hang forever.
func TestReconcilePendingInFlightReset(t *testing.T) {
	um := newTestManager(t)
	um.local.Phase = PhaseReplacing
	um.local.Progress = 50

	um.reconcilePending()

	if um.local.Phase != PhaseIdle {
		t.Errorf("expected phase %q after reset, got %q", PhaseIdle, um.local.Phase)
	}
	if um.local.Progress != 0 {
		t.Errorf("expected progress reset to 0, got %d", um.local.Progress)
	}
}
