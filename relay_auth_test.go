package main

// relay_auth_test.go — G1 hardening validation: ed25519 signature verification
// on the relay / gateway forward path (design §18.3 P1-5).
//
// Coverage:
//   ① 合法签名转发通过     — a legitimately signed forward is accepted by the receiver.
//   ② 缺签名/错误签名被拒   — an unsigned or forged relay claim is rejected (401/403).
//   ③ 重放时间戳被拒       — a relay forward with a stale/future timestamp is rejected (401).
//   Plus a sender integration test proving the production forward functions
//   (gatewayForwardToRemote / relayToRemote) actually emit verifiable signatures.

import (
	"bytes"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// relayTestEnv wires node + fed + routeTable so we can exercise the relay
// forward signing/verification path end-to-end in-process (mirrors
// update_qa_test.go's qaFedEnv setup).
func relayTestEnv(t *testing.T) {
	t.Helper()
	setupDiscoveryTestEnv(t)
	// Make the network manager report this node's id (as it would be once the
	// network is fully initialized), so the forward senders sign as this node.
	netMgr.config.NodeID = node.NodeID()
	// Register the local node as a known federation node so the receiver's
	// fed.GetNode(X-Node-ID) lookup succeeds (withFederationAuth path-1).
	fed.AddKnownNode(NodeInfo{
		NodeID:   node.NodeID(),
		PubKey:   node.PubKeyB64(),
		Status:   "active",
		Endpoint: "http://self.local",
	})
}

// newSignedRelayRequest builds an httptest request that mimics a forward emitted
// by a legitimate forwarding node (signed via signRelayForward).
func newSignedRelayRequest(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	sig, ts := signRelayForward(node.NodeID(), method, path, body)
	if sig == "" {
		t.Fatalf("signRelayForward returned empty signature (node initialized?)")
	}
	req.Header.Set("X-Node-ID", node.NodeID())
	req.Header.Set("X-Node-Auth", node.NodeID())
	req.Header.Set(headerRelaySig, sig)
	req.Header.Set(headerRelayTs, ts)
	return req
}

// ① 合法签名转发通过：a valid signed relay forward is accepted by the receiver.
func TestRelayForwardAuth_ValidSignature_Passes(t *testing.T) {
	relayTestEnv(t)
	body := []byte(`{"model":"gpt-4","messages":[]}`)

	req := newSignedRelayRequest(t, http.MethodPost, "/v1/chat/completions", body)
	if status, msg := verifyRelayForwardAuth(req, body); status != 0 {
		t.Fatalf("valid signed relay forward rejected: status=%d msg=%s", status, msg)
	}
}

// ② 缺签名/错误签名被拒：forged or unsigned relay claims are rejected.
func TestRelayForwardAuth_MissingSignature_Rejected(t *testing.T) {
	relayTestEnv(t)
	body := []byte(`{"model":"gpt-4"}`)

	// (a) Claim relay identity but provide NO signature -> 401.
	unsigned := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	unsigned.Header.Set("X-Node-ID", node.NodeID())
	unsigned.Header.Set("X-Node-Auth", node.NodeID())
	if status, _ := verifyRelayForwardAuth(unsigned, body); status != 401 {
		t.Fatalf("unsigned relay claim should be 401, got %d", status)
	}

	// (b) Garbage signature (correct length, won't verify) -> 403.
	forged := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	forged.Header.Set("X-Node-ID", node.NodeID())
	forged.Header.Set("X-Node-Auth", node.NodeID())
	forged.Header.Set(headerRelaySig, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==")
	forged.Header.Set(headerRelayTs, time.Now().UTC().Format(time.RFC3339))
	if status, _ := verifyRelayForwardAuth(forged, body); status != 403 {
		t.Fatalf("forged signature should be 403, got %d", status)
	}

	// (c) Sender not in the trust pool -> 403.
	unknown := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	unknown.Header.Set("X-Node-ID", "mmx-not-in-pool")
	unknown.Header.Set("X-Node-Auth", "mmx-not-in-pool")
	unknown.Header.Set(headerRelaySig, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==")
	unknown.Header.Set(headerRelayTs, time.Now().UTC().Format(time.RFC3339))
	if status, _ := verifyRelayForwardAuth(unknown, body); status != 403 {
		t.Fatalf("unknown sender should be 403, got %d", status)
	}

	// (d) A normal direct consumer request (no relay headers) must pass through.
	consumer := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if status, _ := verifyRelayForwardAuth(consumer, body); status != 0 {
		t.Fatalf("direct consumer request should pass (0), got %d", status)
	}
}

// ③ 重放时间戳被拒：a relay forward with a stale or future timestamp is rejected.
func TestRelayForwardAuth_ReplayTimestamp_Rejected(t *testing.T) {
	relayTestEnv(t)
	body := []byte(`{"model":"gpt-4"}`)

	// Stale timestamp (10 min ago) -> 401 even with a valid signature.
	oldTs := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	oldEnv := RelayAuthEnvelope{
		NodeID:    node.NodeID(),
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		BodyHash:  sha256Hex(body),
		Timestamp: oldTs,
	}
	oldEnv.Signature = node.SignJSON(oldEnv)
	stale := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	stale.Header.Set("X-Node-ID", node.NodeID())
	stale.Header.Set("X-Node-Auth", node.NodeID())
	stale.Header.Set(headerRelaySig, oldEnv.Signature)
	stale.Header.Set(headerRelayTs, oldTs)
	if status, _ := verifyRelayForwardAuth(stale, body); status != 401 {
		t.Fatalf("stale timestamp should be 401, got %d", status)
	}

	// Future timestamp (clock skew beyond window) -> 401.
	futureTs := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
	futureEnv := RelayAuthEnvelope{
		NodeID:    node.NodeID(),
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		BodyHash:  sha256Hex(body),
		Timestamp: futureTs,
	}
	futureEnv.Signature = node.SignJSON(futureEnv)
	future := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	future.Header.Set("X-Node-ID", node.NodeID())
	future.Header.Set("X-Node-Auth", node.NodeID())
	future.Header.Set(headerRelaySig, futureEnv.Signature)
	future.Header.Set(headerRelayTs, futureTs)
	if status, _ := verifyRelayForwardAuth(future, body); status != 401 {
		t.Fatalf("future timestamp should be 401, got %d", status)
	}
}

// TestGatewayForwardToRemoteSignsRequest proves the production gateway forward
// function signs the outbound request and the receiving node accepts it.
func TestGatewayForwardToRemoteSignsRequest(t *testing.T) {
	relayTestEnv(t)

	var captured struct {
		nodeID, sig, ts, method, path string
		body                          []byte
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.nodeID = r.Header.Get("X-Node-ID")
		captured.sig = r.Header.Get(headerRelaySig)
		captured.ts = r.Header.Get(headerRelayTs)
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// Trust the test server's self-signed cert for the shared client.
	client := GetSharedHTTPClient()
	prevTransport := client.Transport
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { client.Transport = prevTransport }()

	body := []byte(`{"model":"gpt-4","messages":[]}`)
	inReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	entry := &RouteEntry{NodeID: "mmx-remote", Addresses: []string{srv.URL}}
	gatewayForwardToRemote(rec, inReq, entry, body, 0, false, "test-model")

	if rec.Code != http.StatusOK {
		t.Fatalf("gateway forward returned %d (body=%s)", rec.Code, rec.Body.String())
	}
	if captured.sig == "" {
		t.Fatalf("gateway forward did not attach relay signature")
	}
	if captured.nodeID != node.NodeID() {
		t.Errorf("relay node id = %q, want %q", captured.nodeID, node.NodeID())
	}

	// The receiving node must accept the legitimately signed forward.
	verifyReq := httptest.NewRequest(captured.method, captured.path, bytes.NewReader(captured.body))
	verifyReq.Header.Set("X-Node-ID", captured.nodeID)
	verifyReq.Header.Set("X-Node-Auth", captured.nodeID)
	verifyReq.Header.Set(headerRelaySig, captured.sig)
	verifyReq.Header.Set(headerRelayTs, captured.ts)
	if status, msg := verifyRelayForwardAuth(verifyReq, captured.body); status != 0 {
		t.Fatalf("receiver rejected a legitimately signed gateway forward: status=%d msg=%s", status, msg)
	}
}

// TestRelayToRemoteSignsRequest proves the production /network/{id} relay
// forward function signs the outbound request over the *stripped* (forwarded)
// path and the receiving node accepts it.
func TestRelayToRemoteSignsRequest(t *testing.T) {
	relayTestEnv(t)

	var captured struct {
		nodeID, sig, ts, method, path string
		body                          []byte
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.nodeID = r.Header.Get("X-Node-ID")
		captured.sig = r.Header.Get(headerRelaySig)
		captured.ts = r.Header.Get(headerRelayTs)
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := GetSharedHTTPClient()
	prevTransport := client.Transport
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { client.Transport = prevTransport }()

	body := []byte(`{"model":"gpt-4","messages":[]}`)
	targetID := "mmx-target"
	// Inbound request as it arrives at the entry relay: /network/{id}/v1/chat/completions
	inReq := httptest.NewRequest(http.MethodPost, "/network/"+targetID+"/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	entry := &RouteEntry{NodeID: targetID, Addresses: []string{srv.URL}}
	parts := []string{targetID, "v1/chat/completions"}
	relayToRemote(rec, inReq, entry, parts, 0)

	if rec.Code != http.StatusOK {
		t.Fatalf("relay forward returned %d (body=%s)", rec.Code, rec.Body.String())
	}
	if captured.sig == "" {
		t.Fatalf("relay forward did not attach relay signature")
	}
	// The receiving node sees the stripped path "/v1/chat/completions".
	if captured.path != "/v1/chat/completions" {
		t.Errorf("receiver path = %q, want %q", captured.path, "/v1/chat/completions")
	}

	verifyReq := httptest.NewRequest(captured.method, captured.path, bytes.NewReader(captured.body))
	verifyReq.Header.Set("X-Node-ID", captured.nodeID)
	verifyReq.Header.Set("X-Node-Auth", captured.nodeID)
	verifyReq.Header.Set(headerRelaySig, captured.sig)
	verifyReq.Header.Set(headerRelayTs, captured.ts)
	if status, msg := verifyRelayForwardAuth(verifyReq, captured.body); status != 0 {
		t.Fatalf("receiver rejected a legitimately signed relay forward: status=%d msg=%s", status, msg)
	}
}

// ④ Path 篡改导致验签失败：a forward signed for one path must be rejected when
// replayed against a different path. This proves the Path is bound into the
// RelayAuthEnvelope signature (not just the body hash), so a MITM relabeling the
// forwarded path cannot replay a valid signature.
func TestRelayForwardAuth_PathTamper_Rejected(t *testing.T) {
	relayTestEnv(t)
	body := []byte(`{"model":"gpt-4"}`)
	signedPath := "/v1/chat/completions"
	tamperedPath := "/v1/embeddings"

	// Legitimately sign over the original path.
	sig, ts := signRelayForward(node.NodeID(), http.MethodPost, signedPath, body)
	if sig == "" {
		t.Fatalf("signRelayForward returned empty signature (node initialized?)")
	}

	// Replay the signature against a different path. The receiver reconstructs
	// the envelope from r.URL.Path (now tampered), so VerifyJSONSig must fail.
	req := httptest.NewRequest(http.MethodPost, tamperedPath, bytes.NewReader(body))
	req.Header.Set("X-Node-ID", node.NodeID())
	req.Header.Set("X-Node-Auth", node.NodeID())
	req.Header.Set(headerRelaySig, sig)
	req.Header.Set(headerRelayTs, ts)

	if status, _ := verifyRelayForwardAuth(req, body); status != 403 {
		t.Fatalf("path-tampered relay forward should be 403, got %d", status)
	}
}
