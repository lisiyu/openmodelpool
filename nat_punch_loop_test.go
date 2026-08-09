package main

import (
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

	aOffer, err := a.Offer()
	if err != nil {
		t.Fatal(err)
	}
	bOffer, err := b.Offer()
	if err != nil {
		t.Fatal(err)
	}

	if a.BeginPunch(bOffer, 20*time.Millisecond, 100) == nil {
		t.Fatal("A BeginPunch returned nil")
	}
	if b.BeginPunch(aOffer, 20*time.Millisecond, 100) == nil {
		t.Fatal("B BeginPunch returned nil")
	}

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
