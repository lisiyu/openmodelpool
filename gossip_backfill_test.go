package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGossipHandler_BackfillsPubKeyFromEndpoint verifies the gossip-403 fix:
// a sender that IS in the trust pool but whose pool entry carries an empty
// pub_key must still be admitted when its authoritative /api/node/pubkey
// endpoint serves the matching key. handleFederationGossip backfills and
// re-verifies instead of rejecting outright.
func TestGossipHandler_BackfillsPubKeyFromEndpoint(t *testing.T) {
	setupDiscoveryTestEnv(t)

	// Sender identity with a real key (mirrors newTestNodeIdentity).
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	senderID := "mmx-" + hex.EncodeToString(pub)
	senderB64 := base64.StdEncoding.EncodeToString(pub)
	senderNode := &NodeIdentity{
		nodeID:     senderID,
		pubKey:     pub,
		encPrivKey: encryptField(base64.StdEncoding.EncodeToString(priv)),
		keyPath:    filepath.Join(t.TempDir(), "node.key"),
	}

	// Authoritative pubkey endpoint.
	pkSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/node/pubkey" {
			writeJSON(w, 200, map[string]any{"node_id": senderID, "public_key": senderB64})
			return
		}
		http.NotFound(w, r)
	}))
	defer pkSrv.Close()

	// Sender is in the trust pool, active, with an address, but NO pub_key.
	fed.AddKnownNode(NodeInfo{
		NodeID:    senderID,
		Endpoint:  pkSrv.URL,
		Addresses: []string{pkSrv.URL},
		Status:    "active",
	})

	// Sign a sync gossip message with the real private key.
	msg := GossipMessage{
		Type:             "sync",
		FromNode:         senderID,
		TrustPoolVersion: 1,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
	}
	msg.Signature = senderNode.SignJSON(msg)
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/federation/gossip", bytes.NewReader(body))
	handleFederationGossip(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (pub_key backfill should admit sender); body=%s",
			rec.Code, rec.Body.String())
	}

	// The pool entry must now carry the backfilled key for future requests.
	n, ok := fed.GetNode(senderID)
	if !ok {
		t.Fatal("sender missing from pool after gossip")
	}
	if n.PubKey != senderB64 {
		t.Errorf("pool pub_key = %q, want %q (backfill persisted)", n.PubKey, senderB64)
	}
}

// TestGossipHandler_RejectsUnknownSender keeps the security property: a sender
// that is NOT in the trust pool must still be rejected even though a pubkey
// endpoint exists.
func TestGossipHandler_RejectsUnknownSender(t *testing.T) {
	setupDiscoveryTestEnv(t)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	senderID := "mmx-" + hex.EncodeToString(pub)
	senderB64 := base64.StdEncoding.EncodeToString(pub)
	senderNode := &NodeIdentity{
		nodeID:     senderID,
		pubKey:     pub,
		encPrivKey: encryptField(base64.StdEncoding.EncodeToString(priv)),
		keyPath:    filepath.Join(t.TempDir(), "node.key"),
	}

	pkSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/node/pubkey" {
			writeJSON(w, 200, map[string]any{"node_id": senderID, "public_key": senderB64})
			return
		}
		http.NotFound(w, r)
	}))
	defer pkSrv.Close()

	// NOTE: sender deliberately NOT added to the trust pool.

	msg := GossipMessage{
		Type:      "sync",
		FromNode:  senderID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	msg.Signature = senderNode.SignJSON(msg)
	body, _ := json.Marshal(msg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/federation/gossip", bytes.NewReader(body))
	handleFederationGossip(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for sender outside the trust pool", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown") {
		t.Errorf("body = %q, want unknown-sender rejection", rec.Body.String())
	}
}
