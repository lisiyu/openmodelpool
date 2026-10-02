package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"sync"
)

// dht_wire_auth.go — authentication for the UDP DHT wire protocol.
//
// Threat model (the wire was previously fully unauthenticated):
//   - forged responses: message IDs were predictable (our own ID hex plus a
//     counter), and takePending matched on ID alone without checking the
//     sender — a spoofed PONG/FIND_NODE_RESP was accepted;
//   - routing-table injection: handle() learned From/FromAddr from EVERY
//     inbound datagram, so one spoofed packet planted an arbitrary entry;
//   - unbounded STORE writes from the network (no size cap).
//
// Design:
//   - every outbound datagram on an auth-enabled UDPDHTTransport is signed
//     with the node's ed25519 key. The message carries KeyID (the sender's
//     federation mmx- node ID), PubKey (base64, used only for TOFU) and Sig
//     over the canonical encoding (all fields except Sig).
//   - inbound datagrams are verified BEFORE takePending delivery / handle():
//     the signer must be a trust-pool member, a TOFU-pinned peer, or — for
//     responses to requests we initiated — a first-contact key pinned now.
//     Anything else (unsigned legacy shape, unknown signer, bad signature,
//     or a From that is not sha256(KeyID)) is dropped: never learned, never
//     answered, never delivered to a waiter.
//   - trust-pool keys always win over pins; pins are in-memory only (re-TOFU
//     after restart, same threat posture as first boot).
//   - STORE values are capped (dhtMaxStoreValue); oversized stores are ACKed
//     but not stored, so a flooding peer cannot grow memory.
//   - legacy in-memory transports (tests, fake doubles) bypass auth entirely:
//     the security boundary is the UDP socket. handle() itself stays pure so
//     protocol unit tests do not need keys.

const (
	// dhtMaxStoreValue caps a single DHT record value accepted from the
	// network. STORE/FIND_VALUE are currently unused in production (the DHT
	// serves mesh membership only), so this is purely a flood guard.
	dhtMaxStoreValue = 16 << 10 // 16 KiB
	// dhtMaxKeyLen caps DHT record keys for the same reason.
	dhtMaxKeyLen = 256
	// dhtMaxDatagram caps an inbound UDP datagram; anything bigger cannot be
	// a legitimate protocol message (a full FIND_NODE_RESP is ~2 KiB) and is
	// dropped before JSON decoding to bound unmarshal expansion.
	dhtMaxDatagram = 32 << 10 // 32 KiB
)

// dhtWireAuth carries the signing identity and the trust sources for one
// UDPDHTTransport. Nil *dhtWireAuth on a transport means legacy/test mode
// (unsigned traffic accepted); production always wires one in startDHTNode.
type dhtWireAuth struct {
	mu sync.Mutex
	// pins maps federation node ID -> base64 pubkey for TOFU-pinned peers
	// (operator-chosen addresses whose keys were unknown at first contact).
	pins map[string]string
	// identity reports our federation node ID and base64 pubkey. It is a
	// func (not a snapshot) so key rotation / test fixtures stay live.
	identity func() (keyID, pubKeyB64 string, ok bool)
	// sign signs payload. ok=false means no signing key is available; the
	// caller must fail the send, never downgrade to unsigned.
	sign func(payload []byte) (sig string, ok bool)
	// trustLookup resolves a federation node ID to its authoritative base64
	// pubkey (the trust pool). It always wins over pins.
	trustLookup func(nodeID string) (pubKeyB64 string, ok bool)
}

func newDHTWireAuth(
	identity func() (keyID, pubKeyB64 string, ok bool),
	sign func(payload []byte) (sig string, ok bool),
	trustLookup func(nodeID string) (pubKeyB64 string, ok bool),
) *dhtWireAuth {
	return &dhtWireAuth{pins: make(map[string]string), identity: identity, sign: sign, trustLookup: trustLookup}
}

// canonicalDHTPayload returns the bytes covered by the wire signature: the
// message with Sig blanked. encoding/json marshals structs in field order on
// both sides, so the bytes are identical for identical messages.
func canonicalDHTPayload(msg DHTMessage) ([]byte, error) {
	c := msg
	c.Sig = ""
	return json.Marshal(c)
}

// signMessage attaches KeyID/PubKey/Sig to an outbound message. Identity is
// stamped BEFORE the canonical payload is built, so the signature covers the
// full envelope the receiver reconstructs. It returns false when no signing
// identity is available; the caller must fail the send.
func (a *dhtWireAuth) signMessage(msg *DHTMessage) bool {
	if a == nil || a.identity == nil || a.sign == nil {
		return false
	}
	keyID, pubKeyB64, ok := a.identity()
	if !ok || keyID == "" || pubKeyB64 == "" {
		return false
	}
	msg.KeyID = keyID
	msg.PubKey = pubKeyB64
	payload, err := canonicalDHTPayload(*msg)
	if err != nil {
		return false
	}
	sig, ok := a.sign(payload)
	if !ok || sig == "" {
		return false
	}
	msg.Sig = sig
	return true
}

// verifyMessage authenticates one inbound datagram. solicited must be true
// when the datagram arrived as a response to one of our pending requests
// (eligible for TOFU pinning); unsolicited requests from unknown keys are
// always dropped — such peers must enter via gossip/registry/seed paths,
// which carry their own authentication.
func (a *dhtWireAuth) verifyMessage(msg *DHTMessage, solicited bool) bool {
	if a == nil {
		return true // legacy/test transport without auth
	}
	if msg.Sig == "" || msg.KeyID == "" {
		return false // unsigned legacy shape rejected in auth mode
	}
	// Bind the DHT routing slot to the federation identity: From must be
	// sha256(KeyID), otherwise anyone could squat anyone else's slot while
	// signing as themselves.
	if DHTNodeID(sha256.Sum256([]byte(msg.KeyID))) != msg.From {
		return false
	}
	payload, err := canonicalDHTPayload(*msg)
	if err != nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(msg.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	decodeKey := func(s string) (ed25519.PublicKey, bool) {
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, false
		}
		return ed25519.PublicKey(raw), true
	}
	// 1. Trust pool wins over everything (shadows stale pins).
	if keyB64, ok := a.trustLookup(msg.KeyID); ok && keyB64 != "" {
		pub, ok := decodeKey(keyB64)
		if !ok {
			return false
		}
		if !ed25519.Verify(pub, payload, sig) {
			slog.Warn("dht: dropping datagram with bad signature for trust-pool key", "key_id", msg.KeyID)
			return false
		}
		return true
	}
	// 2. Pinned peers (TOFU'd on an earlier solicited exchange).
	a.mu.Lock()
	pinned, ok := a.pins[msg.KeyID]
	a.mu.Unlock()
	if ok {
		pub, ok := decodeKey(pinned)
		if !ok {
			return false
		}
		if !ed25519.Verify(pub, payload, sig) {
			slog.Warn("dht: dropping datagram with bad signature for pinned key", "key_id", msg.KeyID)
			return false
		}
		return true
	}
	// 3. TOFU, solicited responses only: we chose this peer (it answered a
	// request we sent to a trust-derived address), so pin its self-claimed
	// key for this KeyID. The signature is self-consistent by construction;
	// what TOFU buys us is continuity: the same key must sign next time.
	if !solicited {
		return false
	}
	pub, ok := decodeKey(msg.PubKey)
	if !ok || !ed25519.Verify(pub, payload, sig) {
		return false
	}
	a.mu.Lock()
	if a.pins == nil {
		a.pins = make(map[string]string)
	}
	if _, exists := a.pins[msg.KeyID]; !exists {
		a.pins[msg.KeyID] = msg.PubKey
		slog.Info("dht: TOFU-pinned peer key", "key_id", msg.KeyID)
	}
	a.mu.Unlock()
	return true
}
