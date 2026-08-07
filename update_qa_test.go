package main

// update_qa_test.go — Independent QA validation suite for the
// "admin one-click version update" incremental feature.
//
// These tests supplement (and do not modify) the engineer's update_test.go.
// They target the skeptical QA mandate: prove the code actually works,
// exercise more boundaries, and surface real source bugs.
//
// Coverage:
//   * Version comparison boundaries (v-prefix, pre-release, multi-segment).
//   * Persistence integrity: HMAC round-trip, tamper detection, plain-JSON fallback.
//   * reconcilePending edge cases (target mismatch, corrupt marker).
//   * Capability negotiation (old node -> unsupported).
//   * Replay protection (±5min).
//   * ed25519 signature round-trip (the critical security path).
//   * All 5 new HTTP routes via net/http/httptest.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// qaSetUpdateManager installs a test UpdateManager as the global and restores
// the previous value on cleanup.
func qaSetUpdateManager(t *testing.T) *UpdateManager {
	t.Helper()
	um := newTestManager(t)
	prev := updateManager
	updateManager = um
	t.Cleanup(func() { updateManager = prev })
	return um
}

// qaFedEnv wires node + fed + updateManager globals for federation-handler tests.
func qaFedEnv(t *testing.T) {
	t.Helper()
	setupDiscoveryTestEnv(t)
	// Register the local node into the federation trust pool so the
	// handlers' fed.GetNode(X-Node-ID) lookup succeeds.
	fed.AddKnownNode(NodeInfo{
		NodeID:   node.NodeID(),
		PubKey:   node.PubKeyB64(),
		Status:   "active",
		Endpoint: "http://self.local",
	})
	qaSetUpdateManager(t)
}

// qaParseJSON decodes a recorder body into a generic map.
func qaParseJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("response body is not JSON: %q (%v)", body, err)
	}
	return m
}

// ---------------------------------------------------------------------------
// T-1: richer version-comparison boundaries
// ---------------------------------------------------------------------------

func TestQACompareVersionBoundaries(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v4.2.0", "4.1.7", 1},          // leading v on one side only
		{"v4.1.7", "v4.1.7", 0},        // both have v
		{"4.1.7-rc1", "4.1.7", -1},      // pre-release < release
		{"4.1.7", "4.1.7-rc1", 1},       // release > pre-release
		{" 4.2.0 ", "4.1.7", 1},         // surrounding whitespace trimmed
		{"4.2", "4.1.7", 1},             // short vs long, padded zero
		{"4.1.7", "4.2", -1},
		{"5", "4.1.7", 1},               // single segment
		{"4.1.7", "4.1.7.0", 0},         // equal after zero-pad
		{"4.1.10", "4.1.9", 1},          // multi-digit segment
		{"0.0.1", "0.0.0", 1},
		{"", "", 0},                     // both empty
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// T-3: persistence integrity (HMAC + plain-JSON fallback + tamper detection)
// ---------------------------------------------------------------------------

func TestQAPersistRoundTrip(t *testing.T) {
	setupDiscoveryTestEnv(t) // ensures enc is ready -> HMAC path
	um := newTestManager(t)
	path := filepath.Join(um.dataDir, updateStatusFile)

	snap := updateStatusSnapshot{
		Local: UpdateStatus{Env: "local", Name: "node-x", Phase: PhaseSuccess, TargetVersion: "4.2.0"},
		Peers: map[string]UpdateStatus{
			"peer-1": {Env: "peer-1", Name: "peer-one", Phase: PhaseDownloading, Progress: 30},
		},
	}
	if err := saveWithIntegrity(path, snap); err != nil {
		t.Fatalf("saveWithIntegrity: %v", err)
	}

	// The on-disk file must carry a 32-byte HMAC prefix (integrity protected).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	if len(raw) <= hmacSize {
		t.Fatalf("expected HMAC-prefixed file, got %d bytes", len(raw))
	}

	// Load it back (fresh manager, same dir) and confirm fields survive.
	um2 := newTestManager(t)
	um2.dataDir = um.dataDir
	um2.Load()

	if um2.local.Phase != PhaseSuccess || um2.local.TargetVersion != "4.2.0" {
		t.Errorf("local not restored: %+v", um2.local)
	}
	p, ok := um2.peers["peer-1"]
	if !ok {
		t.Fatal("peer-1 not restored")
	}
	if p.Phase != PhaseDownloading || p.Progress != 30 {
		t.Errorf("peer-1 not restored correctly: %+v", p)
	}
}

func TestQAPersistTamperDetected(t *testing.T) {
	setupDiscoveryTestEnv(t)
	um := newTestManager(t)
	path := filepath.Join(um.dataDir, updateStatusFile)

	snap := updateStatusSnapshot{
		Local: UpdateStatus{Env: "local", Phase: PhaseIdle},
		Peers: map[string]UpdateStatus{},
	}
	if err := saveWithIntegrity(path, snap); err != nil {
		t.Fatalf("saveWithIntegrity: %v", err)
	}

	// Flip a byte INSIDE the payload (after the 32-byte HMAC prefix) to
	// simulate on-disk tampering by an attacker without the HMAC key.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(raw) <= hmacSize+5 {
		t.Fatalf("file too small to tamper: %d", len(raw))
	}
	tampered := make([]byte, len(raw))
	copy(tampered, raw)
	tampered[hmacSize+5] ^= 0xFF
	if err := os.WriteFile(path, tampered, 0600); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	// loadWithIntegrity MUST reject the tampered file.
	var got updateStatusSnapshot
	if err := loadWithIntegrity(path, &got); err == nil {
		t.Fatal("expected integrity error on tampered file, got nil")
	}
}

func TestQAPersistPlainJSONFallback(t *testing.T) {
	setupDiscoveryTestEnv(t)
	um := newTestManager(t)
	path := filepath.Join(um.dataDir, updateStatusFile)

	// Pre-upgrade / enc-not-ready format: plain JSON, no HMAC prefix.
	plain := `{"local":{"env":"local","phase":"success","target_version":"4.2.0"},"peers":{}}`
	if err := os.WriteFile(path, []byte(plain), 0600); err != nil {
		t.Fatalf("write plain: %v", err)
	}

	var got updateStatusSnapshot
	if err := loadWithIntegrity(path, &got); err != nil {
		t.Fatalf("plain JSON should load via fallback, got error: %v", err)
	}
	if got.Local.Phase != PhaseSuccess || got.Local.TargetVersion != "4.2.0" {
		t.Errorf("plain JSON not parsed: %+v", got.Local)
	}
}

// ---------------------------------------------------------------------------
// T-3 / restart: reconcilePending extra branches
// ---------------------------------------------------------------------------

// A pending marker whose target does NOT match the running version must NOT be
// promoted to success; if the local phase was in-flight it is reset to idle and
// the marker is left on disk for an operator to inspect.
func TestQARecPendingTargetMismatch(t *testing.T) {
	um := newTestManager(t)
	um.local.Phase = PhaseDownloading
	um.local.TargetVersion = "4.1.8"

	pending := map[string]any{
		"target_version": "9.9.9", // does not match AppVersion (4.1.7)
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(pending)
	if err := os.WriteFile(filepath.Join(um.dataDir, updatePendingFile), data, 0600); err != nil {
		t.Fatal(err)
	}

	um.reconcilePending()

	if um.local.Phase == PhaseSuccess {
		t.Error("local must NOT be promoted to success on target mismatch")
	}
	if um.local.Phase != PhaseIdle {
		t.Errorf("in-flight phase should reset to idle, got %q", um.local.Phase)
	}
	if _, err := os.Stat(filepath.Join(um.dataDir, updatePendingFile)); err != nil {
		t.Error("mismatched pending marker should NOT be removed")
	}
}

// A corrupt pending marker must not panic and must not promote.
func TestQARecPendingCorruptMarker(t *testing.T) {
	um := newTestManager(t)
	um.local.Phase = PhaseReplacing
	if err := os.WriteFile(filepath.Join(um.dataDir, updatePendingFile), []byte("not-json{"), 0600); err != nil {
		t.Fatal(err)
	}
	// Must not panic.
	um.reconcilePending()
	if um.local.Phase != PhaseIdle {
		t.Errorf("expected idle after corrupt marker, got %q", um.local.Phase)
	}
}

// ---------------------------------------------------------------------------
// Q4 capability negotiation (old node -> unsupported)
// ---------------------------------------------------------------------------

func TestQACapabilityNegotiationUnsupported(t *testing.T) {
	qaFedEnv(t)

	// Use MinSupportedVersion higher than current AppVersion to trigger unsupported path
	// without modifying the global AppVersion (avoids race with background goroutines)

	sig := UpdateSignal{
		BroadcastBy:         node.NodeID(),
		TargetVersion:       "4.2.0",
		MinSupportedVersion: "99.0.0", // higher than current AppVersion
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
		OriginAddresses:     []string{"http://origin.local"},
	}
	sig.Signature = node.SignJSON(sig) // valid signature over the (empty-sig) struct

	body, _ := json.Marshal(sig)
	req := httptest.NewRequest(http.MethodPost, "/api/federation/update-signal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", node.NodeID())
	rec := httptest.NewRecorder()

	handleFederationUpdateSignal(rec, req)
	// Wait for background reportToOrigin goroutine to complete
	reportToOriginWG.Wait()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	m := qaParseJSON(t, rec.Body.String())
	if unsupported, ok := m["unsupported"].(bool); !ok || !unsupported {
		t.Errorf("expected unsupported:true for old node, got %v", m)
	}
}

// ---------------------------------------------------------------------------
// Replay protection (±5min)
// ---------------------------------------------------------------------------

func TestQAReplayProtectionTimestamp(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		ts   string
		want bool
	}{
		{now.Format(time.RFC3339), true},
		{now.Add(-4 * time.Minute).Format(time.RFC3339), true},
		{now.Add(4 * time.Minute).Format(time.RFC3339), true},
		{now.Add(-10 * time.Minute).Format(time.RFC3339), false},
		{now.Add(10 * time.Minute).Format(time.RFC3339), false},
		{"", false},
		{"not-a-time", false},
	}
	for _, c := range cases {
		if got := validTimestamp(c.ts); got != c.want {
			t.Errorf("validTimestamp(%q) = %v, want %v", c.ts, got, c.want)
		}
	}
}

// The signal handler must reject a stale timestamp with 400 BEFORE signature
// verification, so a replayed (old) but otherwise valid signature is stopped.
func TestQASignalReplayRejected(t *testing.T) {
	qaFedEnv(t)

	sig := UpdateSignal{
		BroadcastBy:         node.NodeID(),
		TargetVersion:       "4.2.0",
		MinSupportedVersion: MinSupportedUpdateVersion,
		Timestamp:           time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
		OriginAddresses:     []string{"http://origin.local"},
	}
	sig.Signature = node.SignJSON(sig)

	body, _ := json.Marshal(sig)
	req := httptest.NewRequest(http.MethodPost, "/api/federation/update-signal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", node.NodeID())
	rec := httptest.NewRecorder()

	handleFederationUpdateSignal(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for stale timestamp, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// CRITICAL: ed25519 signature round-trip (sign empty-sig struct, verify
// populated-sig struct — same pattern used by BroadcastUpdateSignal /
// reportToOrigin and by gossip).
// ---------------------------------------------------------------------------

func TestQASignatureRoundTrip(t *testing.T) {
	setupDiscoveryTestEnv(t)

	// --- UpdateSignal ---
	sig := UpdateSignal{
		BroadcastBy:         node.NodeID(),
		TargetVersion:       "4.2.0",
		MinSupportedVersion: MinSupportedUpdateVersion,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
	}
	sig.Signature = node.SignJSON(sig)
	if !VerifyJSONSig(node.PubKeyB64(), sig, sig.Signature) {
		t.Error("UpdateSignal signature round-trip FAILED — valid signature rejected")
	}

	// --- UpdateReport ---
	rep := UpdateReport{
		NodeID:        node.NodeID(),
		Name:          "peer",
		TargetVersion: "4.2.0",
		Phase:         PhaseSuccess,
		Progress:      100,
	}
	rep.Signature = node.SignJSON(rep)
	if !VerifyJSONSig(node.PubKeyB64(), rep, rep.Signature) {
		t.Error("UpdateReport signature round-trip FAILED — valid signature rejected")
	}

	// A tampered signature must be rejected.
	bad := sig.Signature
	if len(bad) > 0 {
		b := []byte(bad)
		b[len(b)-1] ^= 0xFF
		if VerifyJSONSig(node.PubKeyB64(), sig, string(b)) {
			t.Error("tampered UpdateSignal signature was (wrongly) accepted")
		}
	}
}

// ---------------------------------------------------------------------------
// Routing / integration (net/http/httptest)
// ---------------------------------------------------------------------------

// GET /api/admin/version/latest — structure + real fetch from a local mock
// GitHub server (no external network, no rate limit).
func TestQARouteVersionLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v99.0.0"}`))
	}))
	defer srv.Close()

	um := qaSetUpdateManager(t)
	um.githubURL = srv.URL
	// Force a fetch (cache expired).
	um.cache = &versionCache{expireAt: time.Time{}}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/version/latest", nil)
	rec := httptest.NewRecorder()
	handleAdminVersionLatest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	m := qaParseJSON(t, rec.Body.String())
	for _, key := range []string{"current_version", "latest_version", "has_update", "checked_at"} {
		if _, ok := m[key]; !ok {
			t.Errorf("response missing field %q (body=%s)", key, rec.Body.String())
		}
	}
	if m["latest_version"] != "v99.0.0" {
		t.Errorf("latest_version = %v, want v4.2.0", m["latest_version"])
	}
	if hasUpdate, ok := m["has_update"].(bool); !ok || !hasUpdate {
		t.Errorf("has_update = %v, want true", m["has_update"])
	}
}

// GET /api/admin/update/status — statuses[] array with full fields.
func TestQARouteUpdateStatus(t *testing.T) {
	um := qaSetUpdateManager(t)
	um.upsertPeer("peer-1", func(s *UpdateStatus) {
		s.Name = "peer-one"
		s.Phase = PhaseDownloading
		s.Progress = 42
		s.TargetVersion = "4.2.0"
	})

	req := httptest.NewRequest(http.MethodGet, "/api/admin/update/status", nil)
	rec := httptest.NewRecorder()
	handleAdminUpdateStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	m := qaParseJSON(t, rec.Body.String())
	if _, ok := m["current_version"]; !ok {
		t.Error("response missing current_version")
	}
	arr, ok := m["statuses"].([]any)
	if !ok {
		t.Fatalf("statuses is not an array: %v", m["statuses"])
	}
	if len(arr) < 1 {
		t.Fatal("expected at least the local node in statuses[]")
	}
	// Local node must carry the full field set.
	local := arr[0].(map[string]any)
	for _, key := range []string{"env", "phase", "target_version", "progress"} {
		if _, ok := local[key]; !ok {
			t.Errorf("status entry missing field %q: %v", key, local)
		}
	}
}

// POST /api/admin/update/start — guard: no update available -> 400.
func TestQARouteUpdateStartNoUpdate(t *testing.T) {
	um := qaSetUpdateManager(t)
	um.cache = &versionCache{
		latest:   VersionInfo{CurrentVersion: AppVersion, LatestVersion: AppVersion, HasUpdate: false},
		expireAt: time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/update/start", nil)
	rec := httptest.NewRecorder()
	handleAdminUpdateStart(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when no update available, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// POST /api/federation/update-signal — valid signature from a known node must
// pass verification (here exercised via the unsupported path to avoid the
// self-update os.Exit side effect of the accepted path).
func TestQARouteSignalValidSignature(t *testing.T) {
	qaFedEnv(t)

	// Use high MinSupportedVersion to trigger unsupported branch without
	// modifying global AppVersion (avoids race with background goroutines).

	sig := UpdateSignal{
		BroadcastBy:         node.NodeID(),
		TargetVersion:       "4.2.0",
		MinSupportedVersion: "99.0.0",
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
		OriginAddresses:     []string{"http://origin.local"},
	}
	sig.Signature = node.SignJSON(sig)

	body, _ := json.Marshal(sig)
	req := httptest.NewRequest(http.MethodPost, "/api/federation/update-signal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", node.NodeID())
	rec := httptest.NewRecorder()

	handleFederationUpdateSignal(rec, req)
	reportToOriginWG.Wait()

	if rec.Code != http.StatusOK {
		t.Fatalf("valid signature wrongly rejected: got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// POST /api/federation/update-signal — wrong signature must be 403.
func TestQARouteSignalWrongSignature(t *testing.T) {
	qaFedEnv(t)

	sig := UpdateSignal{
		BroadcastBy:         node.NodeID(),
		TargetVersion:       "4.2.0",
		MinSupportedVersion: MinSupportedUpdateVersion,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
		OriginAddresses:     []string{"http://origin.local"},
	}
	sig.Signature = node.SignJSON(sig)
	// Tamper the signature.
	b := []byte(sig.Signature)
	b[len(b)-1] ^= 0xFF
	sig.Signature = string(b)

	body, _ := json.Marshal(sig)
	req := httptest.NewRequest(http.MethodPost, "/api/federation/update-signal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", node.NodeID())
	rec := httptest.NewRecorder()

	handleFederationUpdateSignal(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for wrong signature, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// POST /api/federation/update-signal — unknown sender (not in trust pool) -> 403
// before any signature check.
func TestQARouteSignalUnknownNode(t *testing.T) {
	qaFedEnv(t)

	sig := UpdateSignal{
		BroadcastBy:         "unknown-node-xyz",
		TargetVersion:       "4.2.0",
		MinSupportedVersion: MinSupportedUpdateVersion,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
	}
	sig.Signature = node.SignJSON(sig)

	body, _ := json.Marshal(sig)
	req := httptest.NewRequest(http.MethodPost, "/api/federation/update-signal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", "unknown-node-xyz")
	rec := httptest.NewRecorder()

	handleFederationUpdateSignal(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unknown node, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// POST /api/federation/update-report — valid signature must be accepted (200).
func TestQARouteReportValidSignature(t *testing.T) {
	qaFedEnv(t)

	rep := UpdateReport{
		NodeID:        node.NodeID(),
		Name:          "peer-one",
		TargetVersion: "4.2.0",
		Phase:         PhaseSuccess,
		Progress:      100,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	rep.Signature = node.SignJSON(rep)

	body, _ := json.Marshal(rep)
	req := httptest.NewRequest(http.MethodPost, "/api/federation/update-report", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", node.NodeID())
	rec := httptest.NewRecorder()

	handleFederationUpdateReport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("valid report signature wrongly rejected: got %d (body=%s)", rec.Code, rec.Body.String())
	}
	// The report must have been folded into peer state.
	found := false
	for _, s := range updateManager.ListStatuses() {
		if s.NodeID == node.NodeID() && s.Phase == PhaseSuccess {
			found = true
		}
	}
	if !found {
		t.Error("accepted report was not stored into peer status")
	}
}

// POST /api/federation/update-report — wrong signature must be 403.
func TestQARouteReportWrongSignature(t *testing.T) {
	qaFedEnv(t)

	rep := UpdateReport{
		NodeID:        node.NodeID(),
		TargetVersion: "4.2.0",
		Phase:         PhaseSuccess,
		Progress:      100,
	}
	rep.Signature = node.SignJSON(rep)
	b := []byte(rep.Signature)
	b[len(b)-1] ^= 0xFF
	rep.Signature = string(b)

	body, _ := json.Marshal(rep)
	req := httptest.NewRequest(http.MethodPost, "/api/federation/update-report", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", node.NodeID())
	rec := httptest.NewRecorder()

	handleFederationUpdateReport(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for wrong report signature, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// GET /admin-update.js — the route is wired, but admin-update.js is NOT in the
// //go:embed directive (embed.go). This test asserts the CORRECT behaviour
// (the JS must be served). A 404 here proves a source bug.
func TestQARouteAdminUpdateJS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin-update.js", nil)
	rec := httptest.NewRecorder()
	handleAdminUpdateJS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin-update.js returned %d — embedded JS is missing (body=%s)",
			rec.Code, rec.Body.String())
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("expected javascript content-type, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// Frontend wiring static checks
// ---------------------------------------------------------------------------

func TestQAFrontendWiring(t *testing.T) {
	html, err := os.ReadFile("admin.html")
	if err != nil {
		t.Fatalf("read admin.html: %v", err)
	}
	h := string(html)
	for _, need := range []string{
		`id="versionUpdateCard"`,
		`id="versionUpdateBody"`,
		`id="updateStatusArea"`,
		`/admin-update.js?v=346`,
	} {
		if !strings.Contains(h, need) {
			t.Errorf("admin.html missing required fragment: %q", need)
		}
	}

	js, err := os.ReadFile("admin-update.js")
	if err != nil {
		t.Fatalf("read admin-update.js: %v", err)
	}
	j := string(js)
	for _, need := range []string{
		`versionUpdateBody`,
		`updateStatusArea`,
		`startVersionUpdate`,
	} {
		if !strings.Contains(j, need) {
			t.Errorf("admin-update.js missing expected reference: %q", need)
		}
	}
}

// ensure io is referenced (used indirectly via httptest bodies)
var _ = io.Discard
