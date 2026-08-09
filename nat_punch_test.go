package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewPunchOffer(t *testing.T) {
	o, err := NewPunchOffer("nodeA", "1.2.3.4:5678", "192.168.1.2:9000")
	if err != nil {
		t.Fatalf("NewPunchOffer error: %v", err)
	}
	if o.NodeID != "nodeA" || o.ReflexiveAddr != "1.2.3.4:5678" || o.LocalAddr != "192.168.1.2:9000" {
		t.Fatalf("fields not set correctly: %+v", o)
	}
	if len(o.Nonce) != 16 {
		t.Fatalf("nonce should be 16 bytes, got %d", len(o.Nonce))
	}
	if o.SenderTS == 0 {
		t.Fatalf("SenderTS should be set")
	}
}

func TestEncodeDecodeOfferRoundtrip(t *testing.T) {
	o, err := NewPunchOffer("nodeB", "5.6.7.8:1234", "10.0.0.5:9000")
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	frame, err := EncodePunchOffer(o)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	// 必须以 magic 前缀开头
	if !bytes.HasPrefix(frame, PunchMagic) {
		t.Fatalf("frame missing magic prefix")
	}
	dec, err := DecodePunchOffer(frame)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if dec.NodeID != o.NodeID || dec.ReflexiveAddr != o.ReflexiveAddr || dec.LocalAddr != o.LocalAddr {
		t.Fatalf("decoded fields mismatch: %+v vs %+v", dec, o)
	}
	if !bytes.Equal(dec.Nonce, o.Nonce) {
		t.Fatalf("nonce mismatch after roundtrip")
	}
}

func TestDecodeRejectsBadMagic(t *testing.T) {
	o, _ := NewPunchOffer("n", "1.1.1.1:1", "2.2.2.2:2")
	payload, _ := json.Marshal(o)
	bad := append([]byte("XXXX"), payload...)
	if _, err := DecodePunchOffer(bad); err == nil {
		t.Fatalf("should reject bad magic")
	}
}

func TestDecodeRejectsShortFrame(t *testing.T) {
	if _, err := DecodePunchOffer([]byte{0x01, 0x02}); err == nil {
		t.Fatalf("should reject too-short frame")
	}
}

func TestDecodeRejectsMissingFields(t *testing.T) {
	// 缺 reflexice_addr
	bad := PunchOffer{NodeID: "n", Nonce: make([]byte, 16)}
	payload, _ := json.Marshal(bad)
	frame := append(append([]byte{}, PunchMagic...), payload...)
	if _, err := DecodePunchOffer(frame); err == nil {
		t.Fatalf("should reject missing reflexive_addr")
	}
}

func TestDecodeRejectsBadNonceLen(t *testing.T) {
	bad := PunchOffer{NodeID: "n", ReflexiveAddr: "1.1.1.1:1", Nonce: []byte{1, 2, 3}}
	payload, _ := json.Marshal(bad)
	frame := append(append([]byte{}, PunchMagic...), payload...)
	if _, err := DecodePunchOffer(frame); err == nil {
		t.Fatalf("should reject wrong nonce length")
	}
}

func TestNonceEqual(t *testing.T) {
	a := make([]byte, 16)
	b := make([]byte, 16)
	copy(b, a)
	if !NonceEqual(a, b) {
		t.Fatalf("identical nonces should be equal")
	}
	b[0] = 0xff
	if NonceEqual(a, b) {
		t.Fatalf("different nonces should not be equal")
	}
	if NonceEqual(a, a[:8]) {
		t.Fatalf("length mismatch should not be equal")
	}
}

func TestParseUDPAddr(t *testing.T) {
	addr, err := ParseUDPAddr("1.2.3.4:5678")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if addr.Port != 5678 || addr.IP.String() != "1.2.3.4" {
		t.Fatalf("parsed incorrectly: %+v", addr)
	}
	if _, err := ParseUDPAddr("not-an-addr"); err == nil {
		t.Fatalf("should reject invalid addr")
	}
	if _, err := ParseUDPAddr("1.2.3.4:abc"); err == nil {
		t.Fatalf("should reject non-numeric port")
	}
}

func TestCandidate4Tuple(t *testing.T) {
	local, _ := NewPunchOffer("L", "1.1.1.1:1000", "10.0.0.1:2000")
	remote, _ := NewPunchOffer("R", "2.2.2.2:3000", "10.0.0.2:4000")
	la, ra, err := Candidate4Tuple(local, remote)
	if err != nil {
		t.Fatalf("Candidate4Tuple error: %v", err)
	}
	if la.String() != "1.1.1.1:1000" || ra.String() != "2.2.2.2:3000" {
		t.Fatalf("4-tuple mismatch: %s -> %s", la, ra)
	}
}

func TestPunchTarget(t *testing.T) {
	// PunchTarget 应返回本节点作为打洞目标的公网 reflexive 地址
	o, _ := NewPunchOffer("n", "9.9.9.9:9999", "should not matter")
	tgt, err := o.PunchTarget()
	if err != nil {
		t.Fatalf("PunchTarget error: %v", err)
	}
	if tgt != "9.9.9.9:9999" {
		t.Fatalf("PunchTarget wrong: %s", tgt)
	}
}

func TestPackUnpackUint64(t *testing.T) {
	b := packUint64(0x0102030405060708)
	if len(b) != 8 {
		t.Fatalf("pack length wrong: %d", len(b))
	}
	v, err := unpackUint64(b)
	if err != nil {
		t.Fatalf("unpack error: %v", err)
	}
	if v != 0x0102030405060708 {
		t.Fatalf("unpack value wrong: %x", v)
	}
	if _, err := unpackUint64([]byte{1, 2, 3}); err == nil {
		t.Fatalf("should reject short buffer")
	}
}
