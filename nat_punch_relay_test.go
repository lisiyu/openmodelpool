package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestPunchOfferExchange exercises the real offer-exchange path: node A POSTs
// its punch offer to node B's production /network/__punch handler (as
// relayToRemote would), B starts a punch back and echoes its real frame offer,
// A binds its session to it, and both sides punch concurrently over real UDP
// sockets until a direct channel is established on each side. B's receive loop
// is test-owned here so we can observe exactly what frames arrive.
//
// Both sides sign their offers and verify against a fixture trust pool, so
// this also covers the ed25519 exchange hardening end to end.
func TestPunchOfferExchange(t *testing.T) {
	aConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	bConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer aConn.Close()
	defer bConn.Close()

	pubA, privA := testPunchKeypair(t)
	pubB, privB := testPunchKeypair(t)
	testFedWithPunchNodes(t, map[string]ed25519.PublicKey{"nodeA": pubA, "nodeB": pubB})
	testPunchSignerWithKeys(t, map[string]ed25519.PrivateKey{"nodeA": privA, "nodeB": privB})

	aMgr := NewDirectLinkManager(aConn, "nodeA", aConn.LocalAddr().String(), aConn.LocalAddr().String(), true)
	// B uses a test-owned receive loop (ownRecv=false) so we can log arrivals.
	bMgr := NewDirectLinkManager(bConn, "nodeB", bConn.LocalAddr().String(), bConn.LocalAddr().String(), false)
	defer aMgr.Stop()
	defer bMgr.Stop()

	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, rerr := bConn.ReadFromUDP(buf)
			if rerr != nil {
				return
			}
			off, derr := DecodePunchOffer(buf[:n])
			if derr != nil {
				t.Logf("B recv non-punch frame from %v", addr)
				continue
			}
			t.Logf("B recv punch from %v nodeID=%q", addr, off.NodeID)
			bMgr.Ingest(off, addr)
		}
	}()

	// B's exchange endpoint is the production handler.
	old := directLinkMgr
	directLinkMgr = bMgr
	defer func() { directLinkMgr = old }()
	bSrv := httptest.NewServer(http.HandlerFunc(handlePunchExchange))
	defer bSrv.Close()

	// A initiates exactly like relayToRemote: punch first, then exchange the
	// exact offer the sender emits (s.OurOffer), then bind the session to B's
	// real offer from the response so Ingest can verify B's frames by nonce.
	aSess := aMgr.BeginPunch(PunchOffer{NodeID: "nodeB", ReflexiveAddr: bConn.LocalAddr().String()}, 30*time.Millisecond, 100)
	if aSess == nil {
		t.Fatal("A BeginPunch returned nil")
	}
	bOffer, err := ExchangePunchWithPeer(bSrv.URL, aSess.OurOffer)
	if err != nil {
		t.Fatalf("ExchangePunchWithPeer failed: %v", err)
	}
	if len(bOffer.Nonce) != 16 {
		t.Fatalf("exchange response offer has bad nonce length %d", len(bOffer.Nonce))
	}
	if bOffer.NodeID != "nodeB" {
		t.Fatalf("exchange response nodeID = %q, want nodeB", bOffer.NodeID)
	}
	aSess.SetPeerOffer(bOffer)

	time.Sleep(2 * time.Second)
	aDirect := aMgr.HasDirect("nodeB")
	bDirect := bMgr.HasDirect("nodeA")
	t.Logf("after 2s: A->B direct=%v, B->A direct=%v", aDirect, bDirect)

	if !aDirect || !bDirect {
		t.Fatalf("direct link not established via offer exchange: A->B=%v B->A=%v", aDirect, bDirect)
	}
}

// TestHandlePunchExchange verifies the HTTP handler parses a peer offer and
// triggers a punch without error. It uses the package-global directLinkMgr so
// the handler under test is exactly the production one. The offer must be
// signed by the peer's trust-pool key; unsigned/forged/stale offers are
// rejected before any session is created (fail closed).
func TestHandlePunchExchange(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mgr := NewDirectLinkManager(conn, "self", conn.LocalAddr().String(), conn.LocalAddr().String(), true)
	defer mgr.Stop()

	old := directLinkMgr
	directLinkMgr = mgr
	defer func() { directLinkMgr = old }()

	pub, priv := testPunchKeypair(t)
	_, otherPriv := testPunchKeypair(t)
	testFedWithPunchNodes(t, map[string]ed25519.PublicKey{"peerX": pub})

	post := func(offer PunchOffer) *httptest.ResponseRecorder {
		body, _ := json.Marshal(offer)
		w := httptest.NewRecorder()
		handlePunchExchange(w, httptest.NewRequest(http.MethodPost, "/network/__punch", bytes.NewReader(body)))
		return w
	}

	// Signed offer: accepted.
	offer, err := NewPunchOffer("peerX", "127.0.0.1:12345", "127.0.0.1:12345")
	if err != nil {
		t.Fatal(err)
	}
	offer.Sign(priv)
	w := post(offer)
	if w.Code != 200 {
		t.Fatalf("signed offer: status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Accepted bool       `json:"accepted"`
		Peer     string     `json:"peer"`
		Offer    PunchOffer `json:"offer"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted || resp.Peer != "peerX" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	// P0: the handler must echo OUR real frame offer so the initiator can bind
	// the session nonce in Ingest.
	if resp.Offer.NodeID != "self" {
		t.Fatalf("echoed offer nodeID = %q, want self", resp.Offer.NodeID)
	}
	if len(resp.Offer.Nonce) != 16 {
		t.Fatalf("echoed offer nonce length = %d, want 16", len(resp.Offer.Nonce))
	}

	// An offer with a missing/malformed nonce can never validate in Ingest —
	// it must be rejected at the exchange (fail closed).
	bad := offer
	bad.Nonce = []byte{1, 2, 3}
	bad.Sign(priv) // re-sign so only the nonce is wrong
	if w2 := post(bad); w2.Code != 400 {
		t.Fatalf("bad-nonce offer: status = %d, want 400", w2.Code)
	}

	// Unsigned offer: rejected before any session is created.
	unsigned, _ := NewPunchOffer("peerX", "127.0.0.1:12345", "127.0.0.1:12345")
	if w3 := post(unsigned); w3.Code != 403 {
		t.Fatalf("unsigned offer: status = %d, want 403", w3.Code)
	}

	// Forged signature (signed by a different key): rejected.
	forged, _ := NewPunchOffer("peerX", "127.0.0.1:12345", "127.0.0.1:12345")
	forged.Sign(otherPriv)
	if w4 := post(forged); w4.Code != 403 {
		t.Fatalf("forged-signature offer: status = %d, want 403", w4.Code)
	}

	// Impersonation of an unknown node: rejected.
	stranger, _ := NewPunchOffer("stranger", "127.0.0.1:12345", "127.0.0.1:12345")
	stranger.Sign(otherPriv)
	if w5 := post(stranger); w5.Code != 403 {
		t.Fatalf("unknown-node offer: status = %d, want 403", w5.Code)
	}

	// Replay of a stale signed offer: rejected.
	stale, _ := NewPunchOffer("peerX", "127.0.0.1:12345", "127.0.0.1:12345")
	stale.SenderTS = time.Now().Add(-10 * time.Minute).UnixNano()
	stale.Sign(priv)
	if w6 := post(stale); w6.Code != 403 {
		t.Fatalf("stale offer: status = %d, want 403", w6.Code)
	}
}
