package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPeersNotify_PubKeySubstitution_Rejected is the precise regression for the
// v4.4.44 trust-pool poisoning fix (SEC-P1-4). It proves the receiver NEVER
// trusts the attacker-supplied pub_key embedded in the notify payload: even
// when the claimant's authoritative /api/node/pubkey endpoint IS reachable, if
// it returns a key different from the one that produced the signature, the
// notify is rejected (401) and the attacker is neither registered locally nor
// bridged into the federation trust pool.
//
// The existing TestPeersNotify_FetchFail_Rejected covers the unreachable case
// and TestPeersNotify_ForgedSignature_Rejected covers a garbage signature; this
// test closes the gap where the endpoint is live but serves a swapped key.
func TestPeersNotify_PubKeySubstitution_Rejected(t *testing.T) {
	setupDiscoveryTestEnv(t)

	// Attacker generates its own keypair and signs the canonical string with it.
	attackerPub, attackerPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	attackerID := "mmx-" + hex.EncodeToString(attackerPub)

	// The authoritative endpoint returns a DIFFERENT (victim) key — exactly the
	// substitution an attacker hopes we'll trust.
	victimPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	pubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/node/pubkey":
			writeJSON(w, 200, map[string]any{"pub_key": base64.StdEncoding.EncodeToString(victimPub)})
		case "/api/network/heartbeat/ping":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer pubSrv.Close()

	addrs := []string{pubSrv.URL}
	ts := time.Now().UTC().Format(time.RFC3339)
	canonical := fmt.Sprintf("%s|%s|%s", attackerID, strings.Join(addrs, ","), ts)
	rawSig := ed25519.Sign(attackerPriv, []byte(canonical))
	sig := base64.StdEncoding.EncodeToString(rawSig)

	payload := PeerNotifyPayload{
		NodeID:     attackerID,
		Addresses:  addrs,
		PubKey:     base64.StdEncoding.EncodeToString(attackerPub), // claims its own key
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
		t.Fatalf("expected 401 on pubkey substitution, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	for _, p := range netMgr.GetPeers() {
		if p.NodeID == attackerID {
			t.Errorf("pubkey-substitution notify must NOT register peer locally")
		}
	}
	if _, ok := fed.GetNode(attackerID); ok {
		t.Errorf("pubkey-substitution notify must NOT bridge into trust pool (poisoning)")
	}
}
