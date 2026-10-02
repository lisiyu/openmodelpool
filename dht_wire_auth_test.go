package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

// dht_wire_auth_test.go — authentication for the UDP DHT wire (dht_wire_auth.go).
//
// Fixture nodes sign with real ed25519 keys over real UDP sockets
// (newUDPDHTNodeForTest); trust is an explicit map so each test controls
// exactly which keys verify.

// dhtAuthFixture is one test node's identity + trust view.
type dhtAuthFixture struct {
	nodeID string
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	trust  map[string]string // federation node ID -> base64 pubkey
}

func newDHTAuthFixture(nodeID string, trust map[string]string) *dhtAuthFixture {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	if trust == nil {
		trust = map[string]string{}
	}
	return &dhtAuthFixture{nodeID: nodeID, pub: pub, priv: priv, trust: trust}
}

func (f *dhtAuthFixture) pubB64() string {
	return base64.StdEncoding.EncodeToString(f.pub)
}

// enable wires wire-auth onto tp with this fixture's key and trust view.
// The DHT selfID passed to newUDPDHTNodeForTest MUST equal f.nodeID so the
// From-binding (From == sha256(KeyID)) holds.
func (f *dhtAuthFixture) enable(tp *UDPDHTTransport) {
	priv, pub := f.priv, f.pub
	tp.EnableWireAuth(newDHTWireAuth(
		func() (string, string, bool) {
			return f.nodeID, base64.StdEncoding.EncodeToString(pub), true
		},
		func(payload []byte) (string, bool) {
			sig := ed25519.Sign(priv, payload)
			return base64.StdEncoding.EncodeToString(sig), true
		},
		func(nodeID string) (string, bool) {
			k, ok := f.trust[nodeID]
			return k, ok
		},
	))
}

// shortCtx bounds negative-path Sends (a dropped datagram otherwise waits out
// the full dhtTimeout).
func shortCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 1500*time.Millisecond)
}

// TestDHTWireAuth_SignedPingAccepted proves the full signed round trip: A's
// signed PING verifies at B (B learns A), and B's signed PONG verifies at A.
func TestDHTWireAuth_SignedPingAccepted(t *testing.T) {
	fa := newDHTAuthFixture("mmx-wire-a", nil)
	fb := newDHTAuthFixture("mmx-wire-b", nil)
	fa.trust["mmx-wire-b"] = fb.pubB64()
	fb.trust["mmx-wire-a"] = fa.pubB64()

	a, _, aAddr := newUDPDHTNodeForTest(t, fa.nodeID)
	b, _, bAddr := newUDPDHTNodeForTest(t, fb.nodeID)
	fa.enable(a.net.(*UDPDHTTransport))
	fb.enable(b.net.(*UDPDHTTransport))

	ctx, cancel := shortCtx()
	defer cancel()
	resp, err := a.net.Send(ctx, bAddr, DHTMessage{From: a.id, FromAddr: aAddr, Type: DHTMsgPing})
	if err != nil {
		t.Fatalf("signed ping failed: %v", err)
	}
	if resp.Type != DHTMsgPong {
		t.Fatalf("expected PONG, got %s", resp.Type)
	}
	if got := b.TableSize(); got != 1 {
		t.Fatalf("B should have learned A (table size 1), got %d", got)
	}
	if !dhtTableHas(b, a.id) {
		t.Fatal("B's table must contain A after a verified PING")
	}
}

// TestDHTWireAuth_UnsignedDropped proves legacy unsigned datagrams are
// dropped by auth-enabled transports: no answer (Send times out) and, more
// importantly, no routing-table injection.
func TestDHTWireAuth_UnsignedDropped(t *testing.T) {
	fb := newDHTAuthFixture("mmx-wire-b", nil)
	b, _, bAddr := newUDPDHTNodeForTest(t, fb.nodeID)
	fb.enable(b.net.(*UDPDHTTransport))

	c, _, cAddr := newUDPDHTNodeForTest(t, "mmx-wire-c") // no auth: sends unsigned

	ctx, cancel := shortCtx()
	defer cancel()
	if _, err := c.net.Send(ctx, bAddr, DHTMessage{From: c.id, FromAddr: cAddr, Type: DHTMsgPing}); err == nil {
		t.Fatal("unsigned PING must get no answer, got nil error")
	}
	if got := b.TableSize(); got != 0 {
		t.Fatalf("B must not learn from an unsigned datagram (table size %d)", got)
	}
}

// TestDHTWireAuth_ForgedSignatureDropped proves a datagram signed with the
// wrong key for a trusted KeyID is dropped (impersonation fails closed).
func TestDHTWireAuth_ForgedSignatureDropped(t *testing.T) {
	fa := newDHTAuthFixture("mmx-wire-a", nil)
	fb := newDHTAuthFixture("mmx-wire-b", map[string]string{"mmx-wire-a": fa.pubB64()})
	b, _, bAddr := newUDPDHTNodeForTest(t, fb.nodeID)
	fb.enable(b.net.(*UDPDHTTransport))

	// Attacker transport: claims KeyID mmx-wire-a but signs with its own key.
	evilPub, evilPriv, _ := ed25519.GenerateKey(rand.Reader)
	_ = evilPub
	c, _, cAddr := newUDPDHTNodeForTest(t, "mmx-wire-evil")
	tp := c.net.(*UDPDHTTransport)
	tp.EnableWireAuth(newDHTWireAuth(
		func() (string, string, bool) {
			return "mmx-wire-a", base64.StdEncoding.EncodeToString(evilPub), true
		},
		func(payload []byte) (string, bool) {
			sig := ed25519.Sign(evilPriv, payload)
			return base64.StdEncoding.EncodeToString(sig), true
		},
		func(string) (string, bool) { return "", false },
	))

	ctx, cancel := shortCtx()
	defer cancel()
	if _, err := c.net.Send(ctx, bAddr, DHTMessage{From: c.id, FromAddr: cAddr, Type: DHTMsgPing}); err == nil {
		t.Fatal("forged-signature PING must get no answer, got nil error")
	}
	if got := b.TableSize(); got != 0 {
		t.Fatalf("B must not learn from a forged datagram (table size %d)", got)
	}
	// Sanity: the real A still verifies fine.
	a, _, aAddr := newUDPDHTNodeForTest(t, fa.nodeID)
	fa.trust["mmx-wire-b"] = fb.pubB64()
	fa.enable(a.net.(*UDPDHTTransport))
	ctx2, cancel2 := shortCtx()
	defer cancel2()
	if resp, err := a.net.Send(ctx2, bAddr, DHTMessage{From: a.id, FromAddr: aAddr, Type: DHTMsgPing}); err != nil || resp.Type != DHTMsgPong {
		t.Fatalf("legitimate A must still verify (resp=%v err=%v)", resp.Type, err)
	}
}

// TestDHTWireAuth_FromMismatchDropped proves the routing-slot binding: a
// valid signature for KeyID X presented with From != sha256(X) is dropped, so
// nobody can squat somebody else's k-bucket slot while signing as themselves.
func TestDHTWireAuth_FromMismatchDropped(t *testing.T) {
	fa := newDHTAuthFixture("mmx-wire-a", nil)
	auth := newDHTWireAuth(
		func() (string, string, bool) { return "", "", false },
		func([]byte) (string, bool) { return "", false },
		func(nodeID string) (string, bool) {
			if nodeID == fa.nodeID {
				return fa.pubB64(), true
			}
			return "", false
		},
	)
	payload, _ := canonicalDHTPayload(DHTMessage{Type: DHTMsgPing})
	sig := ed25519.Sign(fa.priv, payload)
	msg := &DHTMessage{
		From:   NewDHTNode("someone-else", "fake://x", nil).id, // NOT sha256(mmx-wire-a)
		Type:   DHTMsgPing,
		KeyID:  fa.nodeID,
		PubKey: fa.pubB64(),
		Sig:    base64.StdEncoding.EncodeToString(sig),
	}
	if auth.verifyMessage(msg, true) {
		t.Fatal("From/KeyID mismatch must not verify")
	}
}

// TestDHTWireAuth_TOFULifecycle proves first-contact pinning: an unknown key's
// unsolicited request is dropped, but once the peer answers a request WE
// initiated, its key is pinned and later unsolicited requests verify.
func TestDHTWireAuth_TOFULifecycle(t *testing.T) {
	fb := newDHTAuthFixture("mmx-wire-b", nil) // empty trust
	fc := newDHTAuthFixture("mmx-wire-c", nil)
	fc.trust["mmx-wire-b"] = fb.pubB64() // C knows B; B knows nobody

	b, _, _ := newUDPDHTNodeForTest(t, fb.nodeID)
	c, _, cAddr := newUDPDHTNodeForTest(t, fc.nodeID)
	fb.enable(b.net.(*UDPDHTTransport))
	fc.enable(c.net.(*UDPDHTTransport))

	ctx, cancel := shortCtx()
	defer cancel()
	// 1. Unsolicited request from an unknown key: dropped, nothing learned.
	if _, err := c.net.Send(ctx, b.Addr(), DHTMessage{From: c.id, FromAddr: cAddr, Type: DHTMsgPing}); err == nil {
		t.Fatal("unsolicited PING from unknown key must get no answer")
	}
	if got := b.TableSize(); got != 0 {
		t.Fatalf("B must not learn unknown keys from unsolicited requests (size %d)", got)
	}
	// 2. B initiates: C's signed answer arrives solicited -> TOFU-pin -> PONG.
	ctx2, cancel2 := shortCtx()
	defer cancel2()
	resp, err := b.net.Send(ctx2, cAddr, DHTMessage{From: b.id, FromAddr: b.Addr(), Type: DHTMsgPing})
	if err != nil || resp.Type != DHTMsgPong {
		t.Fatalf("solicited exchange must TOFU-pin and succeed (resp=%v err=%v)", resp.Type, err)
	}
	// 3. The now-pinned peer's unsolicited requests verify.
	ctx3, cancel3 := shortCtx()
	defer cancel3()
	if resp, err := c.net.Send(ctx3, b.Addr(), DHTMessage{From: c.id, FromAddr: cAddr, Type: DHTMsgPing}); err != nil || resp.Type != DHTMsgPong {
		t.Fatalf("pinned peer's PING must verify (resp=%v err=%v)", resp.Type, err)
	}
	if !dhtTableHas(b, c.id) {
		t.Fatal("B must have learned the pinned peer")
	}
}

// TestDHTWireAuth_StoreSizeCap proves oversized STOREs are ACKed but not
// stored, while normal ones replicate.
func TestDHTWireAuth_StoreSizeCap(t *testing.T) {
	fa := newDHTAuthFixture("mmx-wire-a", nil)
	fb := newDHTAuthFixture("mmx-wire-b", nil)
	fa.trust["mmx-wire-b"] = fb.pubB64()
	fb.trust["mmx-wire-a"] = fa.pubB64()

	a, _, _ := newUDPDHTNodeForTest(t, fa.nodeID)
	b, _, bAddr := newUDPDHTNodeForTest(t, fb.nodeID)
	fa.enable(a.net.(*UDPDHTTransport))
	fb.enable(b.net.(*UDPDHTTransport))
	a.learn(&DHTEntry{NodeID: b.id, Addresses: []string{bAddr}})

	ctx, cancel := shortCtx()
	defer cancel()
	big := make([]byte, dhtMaxStoreValue+1)
	if err := a.Store(ctx, "big-key", big); err != nil {
		t.Fatalf("oversized STORE must still ACK, got %v", err)
	}
	if _, ok := b.dht.Get("big-key"); ok {
		t.Fatal("oversized STORE must not be stored")
	}
	if err := a.Store(ctx, "small-key", []byte("v")); err != nil {
		t.Fatalf("normal STORE failed: %v", err)
	}
	if rec, ok := b.dht.Get("small-key"); !ok || string(rec.Value) != "v" {
		t.Fatal("normal STORE must replicate")
	}
}
