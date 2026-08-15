package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestNodeIdentity builds a fully-initialized NodeIdentity backed by a real
// ed25519 keypair (encrypted via the test encryptor). It lets the production
// node.Sign / node.PubKeyB64 / node.NodeID paths work inside tests.
func newTestNodeIdentity(t *testing.T, dir string) *NodeIdentity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	n := &NodeIdentity{
		nodeID:     "mmx-" + hex.EncodeToString(pub),
		pubKey:     pub,
		encPrivKey: encryptField(base64.StdEncoding.EncodeToString(priv)),
		keyPath:    filepath.Join(dir, "node.key"),
	}
	return n
}

// setupDiscoveryTestEnv wires up the globals needed by the v4.1.7 discovery
// features: isolated cfg/enc (via setupTestEnv), a real node identity, an
// enabled federation manager (with persistence in a temp dir), and a shared
// network manager. All globals are restored on test cleanup.
func setupDiscoveryTestEnv(t *testing.T) {
	t.Helper()
	env := setupTestEnv(t)

	node = newTestNodeIdentity(t, env.dir)
	t.Cleanup(func() { node = nil })

	initFederation(env.dir)
	fed.enabled = true // enabled without starting the refresh loop goroutine
	t.Cleanup(func() { fed = nil })

	nm := &NetworkManager{
		dataPath: filepath.Join(env.dir, "network.json"),
		config:   NetworkConfig{NetworkEnabled: true, Mode: NetworkModeShared},
	}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })

	ensureRouteTable()
	t.Cleanup(func() { routeTable = nil })
}

// TestPeersNotify_ValidSignature_RegistersPeer verifies P0-1: a notify carrying
// a valid ed25519 signature (over node_id|addresses|timestamp) with an embedded
// public key is accepted, the sender is registered locally, and the addition is
// bridged into the federation trust pool (P0-2).
//
// SEC-P1-4: the receiver verifies against the sender's AUTHORITATIVE public key
// (fetched from the advertised /api/node/pubkey) and never falls back to the
// payload-embedded key, so the test serves that endpoint.
func TestPeersNotify_ValidSignature_RegistersPeer(t *testing.T) {
	setupDiscoveryTestEnv(t)

	peerPub, peerPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	peerID := "mmx-" + hex.EncodeToString(peerPub)

	// Serve the peer's authoritative pubkey + heartbeat ping so the fail-closed
	// fetch succeeds and the optional ping is clean.
	pubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/node/pubkey":
			writeJSON(w, 200, map[string]any{"pub_key": base64.StdEncoding.EncodeToString(peerPub)})
		case "/api/network/heartbeat/ping":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer pubSrv.Close()

	addrs := []string{pubSrv.URL}
	ts := time.Now().UTC().Format(time.RFC3339)
	canonical := fmt.Sprintf("%s|%s|%s", peerID, strings.Join(addrs, ","), ts)
	rawSig := ed25519.Sign(peerPriv, []byte(canonical))
	sig := base64.StdEncoding.EncodeToString(rawSig)

	payload := PeerNotifyPayload{
		NodeID:     peerID,
		Name:       "peer-b",
		Addresses:  addrs,
		PubKey:     base64.StdEncoding.EncodeToString(peerPub),
		Timestamp:  ts,
		Signature:  sig,
		Propagated: true,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers/notify", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkPeersNotify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// Locally registered as a peer.
	found := false
	for _, p := range netMgr.GetPeers() {
		if p.NodeID == peerID {
			found = true
		}
	}
	if !found {
		t.Errorf("notify did not register peer locally; peers=%v", netMgr.GetPeers())
	}

	// Bridged into the federation trust pool as active.
	n, ok := fed.GetNode(peerID)
	if !ok {
		t.Errorf("notify did not bridge into trust pool")
	} else if n.Status != "active" {
		t.Errorf("bridged node status = %q, want active", n.Status)
	}
}

// TestPeersNotify_ValidSignature_RealPubKeyFieldName is a REGRESSION for the
// v4.5.x notify outage: the live /api/node/pubkey endpoint serves the key under
// "public_key" (handleNodePubKey in handlers_missing.go), but fetchNodePubKey
// historically decoded "pub_key" — so the decoded key was always empty and
// EVERY notify was fail-closed 401 "cannot verify node public key" across the
// whole mesh. The earlier test fixture mocked "pub_key", which masked the bug.
// This test serves the REAL field name and asserts the notify now succeeds.
func TestPeersNotify_ValidSignature_RealPubKeyFieldName(t *testing.T) {
	setupDiscoveryTestEnv(t)

	peerPub, peerPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	peerID := "mmx-" + hex.EncodeToString(peerPub)

	pubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/node/pubkey":
			// REAL wire format returned by handleNodePubKey.
			writeJSON(w, 200, map[string]any{"public_key": base64.StdEncoding.EncodeToString(peerPub)})
		case "/api/network/heartbeat/ping":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer pubSrv.Close()

	addrs := []string{pubSrv.URL}
	ts := time.Now().UTC().Format(time.RFC3339)
	canonical := fmt.Sprintf("%s|%s|%s", peerID, strings.Join(addrs, ","), ts)
	rawSig := ed25519.Sign(peerPriv, []byte(canonical))
	sig := base64.StdEncoding.EncodeToString(rawSig)

	payload := PeerNotifyPayload{
		NodeID:     peerID,
		Name:       "peer-b",
		Addresses:  addrs,
		PubKey:     base64.StdEncoding.EncodeToString(peerPub),
		Timestamp:  ts,
		Signature:  sig,
		Propagated: true,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers/notify", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkPeersNotify(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for real 'public_key' field, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestPeersNotify_FetchFail_Rejected verifies SEC-P1-4 fail-closed behavior:
// when the sender's authoritative /api/node/pubkey cannot be fetched, the
// notify is rejected even though the payload embeds a (plausible) public key.
// This closes the "advertise an unreachable address, sign with a self-chosen
// key" poisoning vector.
func TestPeersNotify_FetchFail_Rejected(t *testing.T) {
	setupDiscoveryTestEnv(t)

	peerPub, peerPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	peerID := "mmx-" + hex.EncodeToString(peerPub)
	// Unreachable address: the authoritative pubkey fetch will fail.
	addrs := []string{"https://peer-unreachable.invalid"}
	ts := time.Now().UTC().Format(time.RFC3339)
	canonical := fmt.Sprintf("%s|%s|%s", peerID, strings.Join(addrs, ","), ts)
	rawSig := ed25519.Sign(peerPriv, []byte(canonical))
	sig := base64.StdEncoding.EncodeToString(rawSig)

	payload := PeerNotifyPayload{
		NodeID:     peerID,
		Addresses:  addrs,
		PubKey:     base64.StdEncoding.EncodeToString(peerPub),
		Timestamp:  ts,
		Signature:  sig,
		Propagated: true,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers/notify", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkPeersNotify(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when authoritative pubkey fetch fails, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	for _, p := range netMgr.GetPeers() {
		if p.NodeID == peerID {
			t.Errorf("failed-fetch notify must NOT register peer")
		}
	}
	if _, ok := fed.GetNode(peerID); ok {
		t.Errorf("failed-fetch notify must NOT bridge into trust pool")
	}
}

// TestPeersNotify_ForgedSignature_Rejected verifies that a notify with an invalid
// signature is rejected (401) and the sender is NOT written to disk.
func TestPeersNotify_ForgedSignature_Rejected(t *testing.T) {
	setupDiscoveryTestEnv(t)

	peerPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	peerID := "mmx-" + hex.EncodeToString(peerPub)
	addrs := []string{"https://peer-x.example.com"}
	ts := time.Now().UTC().Format(time.RFC3339)
	// Garbage signature (correct length, won't verify against the embedded key).
	badSig := base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))

	payload := PeerNotifyPayload{
		NodeID:     peerID,
		Addresses:  addrs,
		PubKey:     base64.StdEncoding.EncodeToString(peerPub),
		Timestamp:  ts,
		Signature:  badSig,
		Propagated: true,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers/notify", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkPeersNotify(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for forged signature, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	for _, p := range netMgr.GetPeers() {
		if p.NodeID == peerID {
			t.Errorf("forged notify must NOT register peer")
		}
	}
	if _, ok := fed.GetNode(peerID); ok {
		t.Errorf("forged notify must NOT bridge into trust pool")
	}
}

// TestPeersNotify_ExpiredTimestamp_Rejected verifies the anti-replay window: a
// notify whose timestamp is older than 5 minutes is rejected (400) before any
// signature check.
func TestPeersNotify_ExpiredTimestamp_Rejected(t *testing.T) {
	setupDiscoveryTestEnv(t)

	peerPub, peerPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	peerID := "mmx-" + hex.EncodeToString(peerPub)
	addrs := []string{"https://peer-y.example.com"}
	old := time.Now().UTC().Add(-10 * time.Minute)
	ts := old.Format(time.RFC3339)
	// Sign over the (old) canonical so the signature would be valid if the
	// timestamp were accepted — isolating the rejection to the time window.
	canonical := fmt.Sprintf("%s|%s|%s", peerID, strings.Join(addrs, ","), ts)
	rawSig := ed25519.Sign(peerPriv, []byte(canonical))
	sig := base64.StdEncoding.EncodeToString(rawSig)

	payload := PeerNotifyPayload{
		NodeID:     peerID,
		Addresses:  addrs,
		PubKey:     base64.StdEncoding.EncodeToString(peerPub),
		Timestamp:  ts,
		Signature:  sig,
		Propagated: true,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers/notify", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkPeersNotify(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for expired timestamp, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestAddPeer_TriggersNotifyNoLoop verifies P0-1 bidirectional discovery and the
// R1 loop-prevention invariant end to end:
//   - A human-initiated add of a NEW peer B triggers exactly one outbound notify
//     to B (via sendNotifyToPeer).
//   - B's notify receiver registers A but NEVER notifies back to A (no ping-pong).
//   - Re-adding an existing peer does not re-notify (R3 idempotency).
func TestAddPeer_TriggersNotifyNoLoop(t *testing.T) {
	setupDiscoveryTestEnv(t)

	var bNotifyCount int32
	var aNotifyCount int32
	bSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/network/peers/notify":
			atomic.AddInt32(&bNotifyCount, 1)
			// B receives the notify: register A locally (must NOT notify back).
			handleNetworkPeersNotify(w, r)
		case "/api/network/heartbeat/ping":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer bSrv.Close()

	// A's would-be回发 sink — must stay at zero (proves no loop).
	aSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/network/peers/notify" {
			atomic.AddInt32(&aNotifyCount, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer aSrv.Close()

	// SEC-P1-4: A's notify advertises a reachable authoritative pubkey endpoint
	// (federation_endpoint), so B's fail-closed pubkey fetch succeeds and the
	// reverse registration (and trust-pool bridge) completes.
	pubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/node/pubkey":
			writeJSON(w, 200, map[string]any{"pub_key": node.PubKeyB64()})
		case "/api/network/heartbeat/ping":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer pubSrv.Close()
	cfg.Set("federation_endpoint", pubSrv.URL)

	// Human (A) adds B. B's address is bSrv.
	body := strings.NewReader(fmt.Sprintf(`{"addresses":["%s"],"node_id":"mmx-B","name":"B"}`, bSrv.URL))
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkAddPeer(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add peer failed: %d %s", rec.Code, rec.Body.String())
	}

	// Wait for the async notify to land.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&bNotifyCount) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := atomic.LoadInt32(&bNotifyCount); got != 1 {
		t.Errorf("B should receive exactly 1 notify, got %d", got)
	}
	if got := atomic.LoadInt32(&aNotifyCount); got != 0 {
		t.Errorf("A must NOT receive any回发 notify (no loop), got %d", got)
	}

	// B should have bridged A into its trust pool after the notify.
	bridged := false
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := fed.GetNode(node.NodeID()); ok {
			bridged = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !bridged {
		t.Errorf("B did not bridge A into trust pool after notify")
	}

	// Idempotency (R3): re-adding B must NOT re-notify.
	body2 := strings.NewReader(fmt.Sprintf(`{"addresses":["%s"],"node_id":"mmx-B","name":"B"}`, bSrv.URL))
	req2 := httptest.NewRequest(http.MethodPost, "/api/network/peers", body2)
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handleNetworkAddPeer(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("re-add peer failed: %d %s", rec2.Code, rec2.Body.String())
	}
	time.Sleep(300 * time.Millisecond)
	if got := atomic.LoadInt32(&bNotifyCount); got != 1 {
		t.Errorf("re-adding existing peer should not re-notify; bNotifyCount=%d", got)
	}
}

// TestAddPeer_ConcurrentMutualAddNoInfiniteLoop proves the discovery loop cannot
// run away: many concurrent human-initiated adds of the same peer each trigger
// at most one bounded outbound notify, and the receiver never notifies back, so
// the total is bounded (not infinite) and A never receives a回发.
func TestAddPeer_ConcurrentMutualAddNoInfiniteLoop(t *testing.T) {
	setupDiscoveryTestEnv(t)

	var bNotifyCount int32
	var aNotifyCount int32
	bSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/network/peers/notify" {
			atomic.AddInt32(&bNotifyCount, 1)
			handleNetworkPeersNotify(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer bSrv.Close()
	aSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/network/peers/notify" {
			atomic.AddInt32(&aNotifyCount, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer aSrv.Close()

	// SEC-P1-4: advertise a reachable authoritative pubkey endpoint so B's
	// fail-closed pubkey fetch succeeds for every concurrent notify.
	pubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/node/pubkey":
			writeJSON(w, 200, map[string]any{"pub_key": node.PubKeyB64()})
		case "/api/network/heartbeat/ping":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer pubSrv.Close()
	cfg.Set("federation_endpoint", pubSrv.URL)

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := strings.NewReader(fmt.Sprintf(`{"addresses":["%s"],"node_id":"mmx-B","name":"B"}`, bSrv.URL))
			req := httptest.NewRequest(http.MethodPost, "/api/network/peers", body)
			req.Header.Set("Content-Type", "application/json")
			handleNetworkAddPeer(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()

	// Allow async notifies to settle.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&bNotifyCount) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	bc := atomic.LoadInt32(&bNotifyCount)
	if bc < 1 || bc > n {
		t.Errorf("notify count should be bounded in [1,%d], got %d", n, bc)
	}
	if ac := atomic.LoadInt32(&aNotifyCount); ac != 0 {
		t.Errorf("A must not receive any回发 notify (no loop), got %d", ac)
	}
}
