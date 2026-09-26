package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testPunchKeypair generates an ed25519 keypair for punch-offer tests.
func testPunchKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// testFedWithPunchNodes installs a fake federation trust pool containing the
// given nodeID -> ed25519 public key bindings, restoring the previous fed
// afterwards.
func testFedWithPunchNodes(t *testing.T, nodes map[string]ed25519.PublicKey) {
	t.Helper()
	infos := make([]NodeInfo, 0, len(nodes))
	for id, pub := range nodes {
		infos = append(infos, NodeInfo{
			NodeID: id,
			PubKey: base64.StdEncoding.EncodeToString(pub),
			Status: "active",
		})
	}
	old := fed
	fed = &FederationManager{trustPool: TrustPool{Nodes: infos}}
	t.Cleanup(func() { fed = old })
}

// testPunchSignerWithKeys overrides the outbound offer signer so offers from
// the given node IDs are signed with fixture keys, restoring afterwards.
func testPunchSignerWithKeys(t *testing.T, keys map[string]ed25519.PrivateKey) {
	t.Helper()
	old := punchOfferSigner
	punchOfferSigner = func(o *PunchOffer) {
		if k, ok := keys[o.NodeID]; ok {
			o.Sign(k)
		}
	}
	t.Cleanup(func() { punchOfferSigner = old })
}

func mustSignedOffer(t *testing.T, nodeID string, priv ed25519.PrivateKey) PunchOffer {
	t.Helper()
	o, err := NewPunchOffer(nodeID, "1.2.3.4:5678", "192.168.1.2:9000")
	if err != nil {
		t.Fatal(err)
	}
	o.Sign(priv)
	return o
}

func TestPunchOfferSignVerify_Roundtrip(t *testing.T) {
	pub, priv := testPunchKeypair(t)
	o := mustSignedOffer(t, "nodeA", priv)
	if o.Signature == "" {
		t.Fatal("Sign left Signature empty")
	}
	if !o.VerifySignature(pub) {
		t.Fatal("valid signature rejected")
	}
	// The signature must survive the JSON encode/decode roundtrip used by
	// both the UDP frame path and the HTTP exchange path.
	frame, err := EncodePunchOffer(o)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePunchOffer(frame)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.VerifySignature(pub) {
		t.Fatal("signature invalid after encode/decode roundtrip")
	}
}

func TestPunchOfferSigningPayload_DeterministicAndDomainSeparated(t *testing.T) {
	_, priv := testPunchKeypair(t)
	a := mustSignedOffer(t, "nodeA", priv)
	b := a
	if !bytes.Equal(a.signingPayload(), b.signingPayload()) {
		t.Fatal("signing payload not deterministic")
	}
	if !strings.HasPrefix(string(a.signingPayload()), punchSigDomain+":") {
		t.Fatalf("payload missing domain separation prefix, got %q", a.signingPayload())
	}
}

func TestPunchOfferVerify_RejectsTampering(t *testing.T) {
	pub, priv := testPunchKeypair(t)
	mutate := map[string]func(*PunchOffer){
		"node_id":        func(o *PunchOffer) { o.NodeID = "nodeB" },
		"reflexive_addr": func(o *PunchOffer) { o.ReflexiveAddr = "9.9.9.9:9999" },
		"local_addr":     func(o *PunchOffer) { o.LocalAddr = "10.9.9.9:9999" },
		"nonce":          func(o *PunchOffer) { o.Nonce[0] ^= 0xff },
		"ts":             func(o *PunchOffer) { o.SenderTS++ },
	}
	for name, fn := range mutate {
		o := mustSignedOffer(t, "nodeA", priv)
		fn(&o)
		if o.VerifySignature(pub) {
			t.Fatalf("tampered %s accepted", name)
		}
	}
}

func TestPunchOfferVerify_RejectsWrongKey(t *testing.T) {
	pub, _ := testPunchKeypair(t)
	_, otherPriv := testPunchKeypair(t)
	o := mustSignedOffer(t, "nodeA", otherPriv)
	if o.VerifySignature(pub) {
		t.Fatal("signature from wrong key accepted")
	}
}

func TestPunchOfferVerify_RejectsMalformed(t *testing.T) {
	pub, priv := testPunchKeypair(t)
	cases := map[string]func(*PunchOffer){
		"empty signature": func(o *PunchOffer) { o.Signature = "" },
		"bad base64":      func(o *PunchOffer) { o.Signature = "!!not-base64!!" },
		"short signature": func(o *PunchOffer) { o.Signature = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 10)) },
	}
	for name, fn := range cases {
		o := mustSignedOffer(t, "nodeA", priv)
		fn(&o)
		if o.VerifySignature(pub) {
			t.Fatalf("malformed %s accepted", name)
		}
	}
	// Illegal-length public keys must be rejected, not panic
	// (ed25519.Verify panics on wrong-size keys).
	o := mustSignedOffer(t, "nodeA", priv)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("VerifySignature panicked on short pubkey: %v", r)
		}
	}()
	if o.VerifySignature(ed25519.PublicKey(bytes.Repeat([]byte{2}, 10))) {
		t.Fatal("short pubkey accepted")
	}
	if o.VerifySignature(nil) {
		t.Fatal("nil pubkey accepted")
	}
}

func TestVerifyPunchOffer(t *testing.T) {
	pub, priv := testPunchKeypair(t)
	_, otherPriv := testPunchKeypair(t)

	setup := func(t *testing.T) {
		t.Helper()
		testFedWithPunchNodes(t, map[string]ed25519.PublicKey{"peerX": pub})
	}

	t.Run("valid", func(t *testing.T) {
		setup(t)
		o := mustSignedOffer(t, "peerX", priv)
		if !verifyPunchOffer(&o) {
			t.Fatal("valid offer rejected")
		}
	})
	t.Run("missing signature", func(t *testing.T) {
		setup(t)
		o, _ := NewPunchOffer("peerX", "1.2.3.4:5678", "192.168.1.2:9000")
		if verifyPunchOffer(&o) {
			t.Fatal("unsigned offer accepted")
		}
	})
	t.Run("unknown node", func(t *testing.T) {
		setup(t)
		o := mustSignedOffer(t, "stranger", priv)
		if verifyPunchOffer(&o) {
			t.Fatal("offer from unknown node accepted")
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		setup(t)
		o := mustSignedOffer(t, "peerX", otherPriv)
		if verifyPunchOffer(&o) {
			t.Fatal("offer signed by wrong key accepted")
		}
	})
	t.Run("tampered address", func(t *testing.T) {
		setup(t)
		o := mustSignedOffer(t, "peerX", priv)
		o.ReflexiveAddr = "6.6.6.6:6666"
		if verifyPunchOffer(&o) {
			t.Fatal("offer with tampered address accepted")
		}
	})
	t.Run("stale timestamp", func(t *testing.T) {
		setup(t)
		o := mustSignedOffer(t, "peerX", priv)
		o.SenderTS = time.Now().Add(-10 * time.Minute).UnixNano()
		o.Sign(priv) // re-sign so the signature itself is valid; only ts is old
		if verifyPunchOffer(&o) {
			t.Fatal("replayed offer with stale timestamp accepted")
		}
	})
	t.Run("future timestamp", func(t *testing.T) {
		setup(t)
		o := mustSignedOffer(t, "peerX", priv)
		o.SenderTS = time.Now().Add(2 * time.Minute).UnixNano()
		o.Sign(priv)
		if verifyPunchOffer(&o) {
			t.Fatal("offer with future timestamp accepted")
		}
	})
	t.Run("no federation", func(t *testing.T) {
		old := fed
		fed = nil
		t.Cleanup(func() { fed = old })
		o := mustSignedOffer(t, "peerX", priv)
		if verifyPunchOffer(&o) {
			t.Fatal("offer accepted with no trust pool (must fail closed)")
		}
	})
}

// TestPunchSignedExchangeRoundtrip exercises the full signed offer handshake
// over HTTP without needing UDP hole-punching to complete (which the sandbox
// blocks): A's signed offer is verified by B's production handler, and B's
// signed response offer verifies against the trust pool — the exact property
// relayToRemote relies on before SetPeerOffer.
func TestPunchSignedExchangeRoundtrip(t *testing.T) {
	aConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer aConn.Close()
	bConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer bConn.Close()

	pubA, privA := testPunchKeypair(t)
	pubB, privB := testPunchKeypair(t)
	testFedWithPunchNodes(t, map[string]ed25519.PublicKey{"nodeA": pubA, "nodeB": pubB})
	testPunchSignerWithKeys(t, map[string]ed25519.PrivateKey{"nodeA": privA, "nodeB": privB})

	aMgr := NewDirectLinkManager(aConn, "nodeA", aConn.LocalAddr().String(), aConn.LocalAddr().String(), false)
	defer aMgr.Stop()
	bMgr := NewDirectLinkManager(bConn, "nodeB", bConn.LocalAddr().String(), bConn.LocalAddr().String(), false)
	defer bMgr.Stop()

	old := directLinkMgr
	directLinkMgr = bMgr
	defer func() { directLinkMgr = old }()
	srv := httptest.NewServer(http.HandlerFunc(handlePunchExchange))
	defer srv.Close()

	aOffer, err := aMgr.Offer()
	if err != nil {
		t.Fatal(err)
	}
	if aOffer.Signature == "" {
		t.Fatal("Offer() did not sign with the node key")
	}
	if !aOffer.VerifySignature(pubA) {
		t.Fatal("A's offer signature does not verify")
	}

	bOffer, err := ExchangePunchWithPeer(srv.URL, aOffer)
	if err != nil {
		t.Fatalf("signed exchange rejected: %v", err)
	}
	if bOffer.NodeID != "nodeB" {
		t.Fatalf("response nodeID = %q, want nodeB", bOffer.NodeID)
	}
	// The initiator-side check from relayToRemote: the bound offer must be
	// authentic per the trust pool.
	if !verifyPunchOffer(&bOffer) {
		t.Fatal("B's response offer failed trust-pool verification")
	}
	if !bOffer.VerifySignature(pubB) {
		t.Fatal("B's response signature does not verify against B's key")
	}

	// A forged exchange (attacker's key, victim's nodeID) must be rejected.
	_, evilPriv := testPunchKeypair(t)
	evil := mustSignedOffer(t, "nodeA", evilPriv)
	if _, err := ExchangePunchWithPeer(srv.URL, evil); err == nil {
		t.Fatal("forged offer accepted by exchange handler")
	}
}
