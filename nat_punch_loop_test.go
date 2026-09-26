package main

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// waitDirect polls until a direct link to nodeID is established or times out.
func waitDirect(t *testing.T, d *DirectLinkManager, nodeID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if d.HasDirect(nodeID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("direct link to %s not established within %s", nodeID, timeout)
}

// TestPunchLoopback verifies the full punch handshake over real UDP sockets:
// two nodes exchange offers and, through concurrent outbound punches plus the
// receive loop, each establishes a direct channel to the other. No real NAT is
// involved (loopback), but the multiplexing, frame decode, and state machine
// are exercised exactly as in production.
func TestPunchLoopback(t *testing.T) {
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

	a := NewDirectLinkManager(aConn, "nodeA", aConn.LocalAddr().String(), aConn.LocalAddr().String(), true)
	b := NewDirectLinkManager(bConn, "nodeB", bConn.LocalAddr().String(), bConn.LocalAddr().String(), true)
	defer a.Stop()
	defer b.Stop()

	// P0: punch frames carry s.OurOffer, so the "exchange" must deliver each
	// side's real OurOffer — otherwise Ingest rejects the frames as forged
	// (nonce mismatch). This mirrors the production flow: BeginPunch, then
	// ExchangePunchWithPeer(s.OurOffer), then SetPeerOffer(response).
	aSess := a.BeginPunch(PunchOffer{NodeID: "nodeB", ReflexiveAddr: bConn.LocalAddr().String()}, 20*time.Millisecond, 100)
	if aSess == nil {
		t.Fatal("A BeginPunch returned nil")
	}
	bSess := b.BeginPunch(PunchOffer{NodeID: "nodeA", ReflexiveAddr: aConn.LocalAddr().String()}, 20*time.Millisecond, 100)
	if bSess == nil {
		t.Fatal("B BeginPunch returned nil")
	}
	aSess.SetPeerOffer(bSess.OurOffer)
	bSess.SetPeerOffer(aSess.OurOffer)

	waitDirect(t, a, "nodeB", 5*time.Second)
	waitDirect(t, b, "nodeA", 5*time.Second)

	aDirect := a.DirectAddr("nodeB")
	bDirect := b.DirectAddr("nodeA")
	if aDirect == nil || bDirect == nil {
		t.Fatal("nil direct addr after establishment")
	}
	// Each node's direct address must be the OTHER node's listening socket.
	if aDirect.String() != bConn.LocalAddr().String() {
		t.Fatalf("A direct mismatch: got %s want %s", aDirect, bConn.LocalAddr())
	}
	if bDirect.String() != aConn.LocalAddr().String() {
		t.Fatalf("B direct mismatch: got %s want %s", bDirect, aConn.LocalAddr())
	}
}

// TestIngest_RejectsForgedNonce (P0): a punch frame whose nonce does not match
// the session's bound peer offer must be rejected WITHOUT touching links —
// otherwise anyone on the network can forge an OMP1 frame with an arbitrary
// NodeID and permanently hijack links[peer], turning RelayOverUDP traffic
// (including request bodies) into a MITM.
func TestIngest_RejectsForgedNonce(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	d := NewDirectLinkManager(conn, "self", conn.LocalAddr().String(), conn.LocalAddr().String(), false)
	defer d.Stop()

	attackerAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 59999}
	peerOffer, err := NewPunchOffer("peer", "127.0.0.1:9999", "127.0.0.1:9999")
	if err != nil {
		t.Fatal(err)
	}

	// No session for the peer: Ingest must be a no-op (no panic, no link).
	d.Ingest(peerOffer, attackerAddr)
	if d.HasDirect("peer") {
		t.Fatal("Ingest without a session established a direct link")
	}

	s := d.BeginPunch(peerOffer, time.Second, 100)
	if s == nil {
		t.Fatal("BeginPunch returned nil")
	}

	// Forged frame: correct NodeID, attacker-chosen nonce.
	forged := peerOffer
	forged.Nonce = bytes.Repeat([]byte{0xAA}, 16)
	d.Ingest(forged, attackerAddr)
	if d.HasDirect("peer") {
		t.Fatal("forged-nonce frame established a direct link")
	}
	if addr := d.DirectAddr("peer"); addr != nil {
		t.Fatalf("links[peer] overwritten by forged frame: %v", addr)
	}

	// Wrong-length nonce must also be rejected.
	short := peerOffer
	short.Nonce = []byte{1, 2, 3}
	d.Ingest(short, attackerAddr)
	if d.HasDirect("peer") {
		t.Fatal("short-nonce frame established a direct link")
	}

	// Legitimate frame: the peer's real offer (matching nonce) must pass and
	// record the peer's actual source address.
	legitAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 49999}
	d.Ingest(peerOffer, legitAddr)
	if !d.HasDirect("peer") {
		t.Fatal("legitimate-nonce frame was rejected")
	}
	if got := d.DirectAddr("peer"); got == nil || got.String() != legitAddr.String() {
		t.Fatalf("links[peer] = %v, want %v", got, legitAddr)
	}
}

// TestBeginPunch_DoesNotOverwriteSession (P0): an unauthenticated
// /network/__punch offer for a peer we already punch must not rebind the
// in-flight session — otherwise an attacker could replace the expected peer
// nonce with one they chose and then satisfy the Ingest check themselves.
func TestBeginPunch_DoesNotOverwriteSession(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	d := NewDirectLinkManager(conn, "self", conn.LocalAddr().String(), conn.LocalAddr().String(), false)
	defer d.Stop()

	legitOffer, err := NewPunchOffer("peer", "127.0.0.1:9999", "127.0.0.1:9999")
	if err != nil {
		t.Fatal(err)
	}
	s1 := d.BeginPunch(legitOffer, time.Second, 100)
	if s1 == nil {
		t.Fatal("first BeginPunch returned nil")
	}

	// Attacker forges an exchange for the same NodeID with their own nonce.
	evilOffer, err := NewPunchOffer("peer", "127.0.0.1:6666", "127.0.0.1:6666")
	if err != nil {
		t.Fatal(err)
	}
	s2 := d.BeginPunch(evilOffer, time.Second, 100)
	if s2 != s1 {
		t.Fatal("second BeginPunch overwrote the in-flight session")
	}

	// The attacker's frames (carrying their own nonce) must still be rejected.
	attackerAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 59999}
	d.Ingest(evilOffer, attackerAddr)
	if d.HasDirect("peer") {
		t.Fatal("attacker rebound the session via a forged exchange")
	}

	// The legitimate peer's frames still validate against the original nonce.
	legitAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 49999}
	d.Ingest(legitOffer, legitAddr)
	if !d.HasDirect("peer") {
		t.Fatal("legitimate frame rejected after forged exchange attempt")
	}
	if got := d.DirectAddr("peer"); got == nil || got.String() != legitAddr.String() {
		t.Fatalf("links[peer] = %v, want %v", got, legitAddr)
	}
}
