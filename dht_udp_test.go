package main

import (
	"context"
	"net"
	"testing"
)

// newUDPDHTNodeForTest spins up a DHTNode served by a real UDPDHTTransport on an
// ephemeral loopback port, and stops it on test cleanup.
func newUDPDHTNodeForTest(t *testing.T, selfID string) (*DHTNode, *UDPDHTTransport, string) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	addr := conn.LocalAddr().String()
	n := NewDHTNode(selfID, addr, nil)
	tp := NewUDPDHTTransport(n, conn)
	n.SetTransport(tp)
	tp.Start()
	t.Cleanup(tp.Stop)
	return n, tp, addr
}

// TestUDPDHT_PingPong proves a PING travels over a real UDP socket and a PONG
// comes back, and that the responder learned the requester's address.
func TestUDPDHT_PingPong(t *testing.T) {
	a, _, aAddr := newUDPDHTNodeForTest(t, "udp-test-a")
	b, _, bAddr := newUDPDHTNodeForTest(t, "udp-test-b")

	resp, err := a.net.Send(context.Background(), bAddr, DHTMessage{
		From:     a.id,
		FromAddr: aAddr,
		Type:     DHTMsgPing,
	})
	if err != nil {
		t.Fatalf("ping over UDP failed: %v", err)
	}
	if resp.Type != DHTMsgPong {
		t.Fatalf("expected PONG, got %s", resp.Type)
	}
	// B should have learned A from the inbound request.
	if got := b.TableSize(); got != 1 {
		t.Fatalf("B should have learned A (table size 1), got %d", got)
	}
}

// TestUDPDHT_IterativeFindNodeMultiHop proves iterative lookup discovers a node
// it has no direct link to, by routing through an intermediate over real UDP.
func TestUDPDHT_IterativeFindNodeMultiHop(t *testing.T) {
	a, _, aAddr := newUDPDHTNodeForTest(t, "udp-test-a")
	b, _, bAddr := newUDPDHTNodeForTest(t, "udp-test-b")
	c, _, cAddr := newUDPDHTNodeForTest(t, "udp-test-c")

	// Seed: B knows C; A knows B. A has no direct knowledge of C.
	b.learn(&DHTEntry{NodeID: c.id, Addresses: []string{cAddr}})
	a.learn(&DHTEntry{NodeID: b.id, Addresses: []string{bAddr}})

	// A looks up C. It must reach C via B (multi-hop over UDP).
	a.IterativeFindNode(c.id)

	if got := a.TableSize(); got < 2 {
		t.Fatalf("A should have learned B and C via lookup (table >= 2), got %d", got)
	}
	if _, ok := a.addrOf(c.id); !ok {
		t.Fatalf("A should know C's address after lookup")
	}
	_ = aAddr
}

// TestUDPDHT_StoreFindValue proves a STORE replicates over UDP to the closest
// node and a later FIND_VALUE retrieves it across the socket.
func TestUDPDHT_StoreFindValue(t *testing.T) {
	a, _, aAddr := newUDPDHTNodeForTest(t, "udp-test-a")
	b, _, bAddr := newUDPDHTNodeForTest(t, "udp-test-b")

	// A knows B so STORE routes to it.
	a.learn(&DHTEntry{NodeID: b.id, Addresses: []string{bAddr}})

	if err := a.Store(context.Background(), "hello", []byte("world")); err != nil {
		t.Fatalf("store failed: %v", err)
	}
	// The closest node (B) should now hold the record.
	if rec, ok := b.dht.Get("hello"); !ok || string(rec.Value) != "world" {
		t.Fatalf("B did not receive the stored record over UDP")
	}
	// And a full iterative FindValue from A retrieves it.
	val, found := a.IterativeFindValue(context.Background(), "hello")
	if !found || string(val) != "world" {
		t.Fatalf("IterativeFindValue did not retrieve the stored record (found=%v)", found)
	}
	_ = aAddr
}
