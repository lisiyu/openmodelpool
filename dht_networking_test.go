package main

import (
	"context"
	"testing"
)

// newTestDHTNode is a small helper that creates a node on an in-memory network.
func newTestDHTNode(net *InMemoryDHTNetwork, id, addr string) *DHTNode {
	n := NewDHTNode(id, addr, net)
	net.Register(addr, n)
	return n
}

// TestDHT_PingPong verifies the basic request/response handshake.
func TestDHT_PingPong(t *testing.T) {
	net := NewInMemoryDHTNetwork()
	a := newTestDHTNode(net, "nodeA", "addrA")
	newTestDHTNode(net, "nodeB", "addrB") // B registered into network as ping target

	resp, err := a.net.Send(context.Background(), "addrB", DHTMessage{
		From: a.id, FromAddr: a.addr, Type: DHTMsgPing,
	})
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if resp.Type != DHTMsgPong {
		t.Fatalf("expected PONG, got %s", resp.Type)
	}
}

// TestDHT_IterativeFindNodeDiscoversViaRelay proves multi-hop discovery:
// A only knows B (seed); B knows C. After bootstrap, A must learn C's
// address through B — i.e. the K-Bucket is genuinely filled by RPC, not by
// manual AddNode.
func TestDHT_IterativeFindNodeDiscoversViaRelay(t *testing.T) {
	net := NewInMemoryDHTNetwork()
	a := newTestDHTNode(net, "nodeA", "addrA")
	b := newTestDHTNode(net, "nodeB", "addrB")
	c := newTestDHTNode(net, "nodeC", "addrC")

	// Seed: A knows only B; B knows C. No other a-priori knowledge.
	a.learn(&DHTEntry{NodeID: b.id, Addresses: []string{"addrB"}})
	b.learn(&DHTEntry{NodeID: c.id, Addresses: []string{"addrC"}})

	if err := a.Bootstrap(context.Background(), "addrB"); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	if got := a.TableSize(); got < 2 {
		t.Fatalf("A routing table has %d nodes, expected >=2 (B and C)", got)
	}
	if _, ok := a.addrOf(c.id); !ok {
		t.Fatalf("A never learned C via relay — multi-hop discovery failed")
	}
	// Bidirectional: B should also have learned A from the query.
	if _, ok := b.addrOf(a.id); !ok {
		t.Fatalf("B did not learn A from its query (routing not bidirectional)")
	}
}

// TestDHT_StoreAndFindValueViaRelay proves content routing: A stores a value,
// which is replicated to the k closest nodes it discovers; a separate node D
// (which only knows A initially) bootstraps and retrieves the value across
// the relayed path.
func TestDHT_StoreAndFindValueViaRelay(t *testing.T) {
	net := NewInMemoryDHTNetwork()
	a := newTestDHTNode(net, "nodeA", "addrA")
	b := newTestDHTNode(net, "nodeB", "addrB")
	c := newTestDHTNode(net, "nodeC", "addrC")
	d := newTestDHTNode(net, "nodeD", "addrD")

	a.learn(&DHTEntry{NodeID: b.id, Addresses: []string{"addrB"}})
	b.learn(&DHTEntry{NodeID: c.id, Addresses: []string{"addrC"}})

	if err := a.Bootstrap(context.Background(), "addrB"); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	payload := []byte("hello-openmodelpool")
	if err := a.Store(context.Background(), "models/glm-4", payload); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	// The value must exist on at least one of the discovered relay nodes.
	onB, _ := b.dht.Get("models/glm-4")
	onC, _ := c.dht.Get("models/glm-4")
	if onB == nil && onC == nil {
		t.Fatalf("value was not replicated to any relay node (B or C)")
	}

	// D knows only A; after bootstrap it should retrieve the value across hops.
	d.learn(&DHTEntry{NodeID: a.id, Addresses: []string{"addrA"}})
	if err := d.Bootstrap(context.Background(), "addrA"); err != nil {
		t.Fatalf("D bootstrap failed: %v", err)
	}
	val, ok := d.IterativeFindValue(context.Background(), "models/glm-4")
	if !ok {
		t.Fatalf("D could not find value via relayed lookup")
	}
	if string(val) != string(payload) {
		t.Fatalf("value mismatch: got %q, want %q", string(val), string(payload))
	}
}

// TestDHT_FindValueMissingReturnsNotFound ensures a missing key resolves to
// not-found without panicking or looping forever.
func TestDHT_FindValueMissingReturnsNotFound(t *testing.T) {
	net := NewInMemoryDHTNetwork()
	a := newTestDHTNode(net, "nodeA", "addrA")
	b := newTestDHTNode(net, "nodeB", "addrB")
	a.learn(&DHTEntry{NodeID: b.id, Addresses: []string{"addrB"}})

	if _, ok := a.IterativeFindValue(context.Background(), "absent/key"); ok {
		t.Fatalf("expected not-found for absent key, got found")
	}
}
