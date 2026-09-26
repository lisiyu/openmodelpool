package main

import (
	"net"
	"testing"
)

// makeSTUNBindingResponse builds a minimal RFC 5389 Binding Response carrying a
// single XOR-MAPPED-ADDRESS attribute whose XORed IPv4/IPv4-port are xorIP/xorPort.
// Plaintext_ip = XOR(ip, 0x2112A442); plaintext_port = XOR(port, 0x2112).
func makeSTUNBindingResponse(xorIP, xorPort []byte) []byte {
	buf := make([]byte, 32)
	buf[0], buf[1] = 0x01, 0x01                             // Binding Response (success)
	buf[2], buf[3] = 0x00, 0x08                             // Message length = 8 (one attribute)
	buf[4], buf[5], buf[6], buf[7] = 0x21, 0x12, 0xA4, 0x42 // Magic cookie
	buf[20], buf[21] = 0x00, 0x20                           // XOR-MAPPED-ADDRESS
	buf[22], buf[23] = 0x00, 0x08                           // length 8
	buf[25] = 0x01                                          // IPv4 family
	buf[26], buf[27] = xorPort[0], xorPort[1]
	buf[28], buf[29], buf[30], buf[31] = xorIP[0], xorIP[1], xorIP[2], xorIP[3]
	return buf
}

func TestParseSTUNResponse_XORMappedIPv4(t *testing.T) {
	// Plaintext 1.2.3.4:5678 -> XOR with magic cookie.
	xorIP := []byte{0x20, 0x10, 0xA7, 0x46}
	xorPort := []byte{0x37, 0x3C}
	got, _, err := parseSTUNResponse(makeSTUNBindingResponse(xorIP, xorPort))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.2.3.4:5678" {
		t.Fatalf("got %q, want 1.2.3.4:5678", got)
	}
}

// B8-3: the transaction ID must survive parsing so queries can correlate
// replies with their own request.
func TestParseSTUNResponse_TxidRoundTrip(t *testing.T) {
	buf := makeSTUNBindingResponse([]byte{0x20, 0x10, 0xA7, 0x46}, []byte{0x37, 0x3C})
	want := [stunTxidLen]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	copy(buf[8:20], want[:])
	_, got, err := parseSTUNResponse(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("txid mismatch: got %v want %v", got, want)
	}
}

func TestParseSTUNResponse_Truncated(t *testing.T) {
	if _, _, err := parseSTUNResponse([]byte{0x01, 0x01, 0x00}); err == nil {
		t.Fatal("expected error for truncated packet")
	}
}

func TestParseSTUNResponse_WrongType(t *testing.T) {
	buf := makeSTUNBindingResponse([]byte{0x01, 0x02, 0x03, 0x04}, []byte{0x00, 0x50})
	buf[0], buf[1] = 0x00, 0x01 // Binding Request, not a Response
	if _, _, err := parseSTUNResponse(buf); err == nil {
		t.Fatal("expected error for non-response message type")
	}
}

func TestParseSTUNResponse_NoXorAddr(t *testing.T) {
	buf := make([]byte, 20)
	buf[0], buf[1] = 0x01, 0x01
	buf[4], buf[5], buf[6], buf[7] = 0x21, 0x12, 0xA4, 0x42
	if _, _, err := parseSTUNResponse(buf); err == nil {
		t.Fatal("expected error when XOR-MAPPED-ADDRESS is missing")
	}
}

func TestClassifyNAT(t *testing.T) {
	cases := []struct {
		name  string
		addrs []string
		want  string
	}{
		{"single response", []string{"1.2.3.4:5678"}, "unknown"},
		{"full cone", []string{"1.2.3.4:5678", "1.2.3.4:5678"}, "full_cone"},
		{"symmetric port", []string{"1.2.3.4:5678", "1.2.3.4:9999"}, "symmetric"},
		{"symmetric ip", []string{"1.2.3.4:5678", "9.9.9.9:5678"}, "symmetric"},
	}
	for _, c := range cases {
		if got := classifyNAT(c.addrs); got != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// TestPreferRelay verifies the NAT-type-driven transport decision: a symmetric
// NAT must prefer relay (direct peering is unreliable), everything else may
// attempt direct. This is the gate that keeps P1-2b from wasting probe
// attempts on symmetric NATs.
func TestPreferRelay(t *testing.T) {
	cases := []struct {
		natType string
		want    bool
	}{
		{"symmetric", true},
		{"full_cone", false},
		{"open", false},
		{"unknown", false},
	}
	for _, c := range cases {
		n := &NATManager{natType: c.natType}
		if got := n.PreferRelay(); got != c.want {
			t.Fatalf("natType=%s: PreferRelay=%v want %v", c.natType, got, c.want)
		}
	}
}

// TestParseSTUNResponse_MalformedNoPanic (P0): truncated/malformed STUN packets
// must return an error, never panic. The XOR-MAPPED-ADDRESS bounds check used
// to be i+8 > len(buf) while the parser reads buf[i+11], so a 28..31-byte
// crafted datagram panicked with index out of range. A panic in any case below
// fails the test.
func TestParseSTUNResponse_MalformedNoPanic(t *testing.T) {
	full := makeSTUNBindingResponse([]byte{0x20, 0x10, 0xA7, 0x46}, []byte{0x37, 0x3C})
	mustErr := map[string][]byte{
		"empty":              {},
		"short header":       full[:21],
		"attr header only":   full[:24],
		"attr value 4 bytes": full[:28], // old code panicked: i+8=28 <= 28, read buf[28..31]
		"attr value 7 bytes": full[:31], // old code panicked: read buf[31] out of range
	}
	for name, buf := range mustErr {
		if _, _, err := parseSTUNResponse(buf); err == nil {
			t.Errorf("%s: expected error, got success", name)
		}
	}

	// A declared attribute length larger than the buffer must not panic either
	// (the parser only trusts bytes actually present).
	oversized := append([]byte(nil), full...)
	oversized[22], oversized[23] = 0xFF, 0xFF
	_, _, _ = parseSTUNResponse(oversized) // must not panic; result irrelevant

	// Unknown attribute types are skipped without reading their values.
	unknown := append([]byte(nil), full...)
	unknown[20], unknown[21] = 0x80, 0x22 // SOFTWARE, not XOR-MAPPED-ADDRESS
	if _, _, err := parseSTUNResponse(unknown); err == nil {
		t.Error("expected error when XOR-MAPPED-ADDRESS is absent")
	}
}

// TestHandleDatagram_MalformedNoPanic (P0): malformed datagrams must never
// crash datagram processing — each datagram is isolated by the recover in
// handleDatagram, so after garbage a valid STUN response is still processed
// (this is what keeps udpRecvLoop alive in production).
func TestHandleDatagram_MalformedNoPanic(t *testing.T) {
	n := &NATManager{stunCh: make(chan stunResponse, 4)}
	from := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}

	garbage := [][]byte{
		nil,
		{},
		{0x01},
		make([]byte, 28), // all-zero 28-byte "response"
		makeSTUNBindingResponse([]byte{1, 2, 3, 4}, []byte{0, 80})[:28],
		makeSTUNBindingResponse([]byte{1, 2, 3, 4}, []byte{0, 80})[:31],
		{0x4f, 0x4d, 0x50, 0x31, 0x7b, 0x7d}, // OMP1 + invalid JSON punch frame
	}
	for i, g := range garbage {
		n.handleDatagram(g, from) // must not panic
		select {
		case r := <-n.stunCh:
			t.Fatalf("garbage case %d surfaced STUN response: %+v", i, r)
		default:
		}
	}

	// Processing continues after garbage: a valid STUN response is surfaced.
	wantTxid := [stunTxidLen]byte{9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2}
	valid := makeSTUNBindingResponse([]byte{0x20, 0x10, 0xA7, 0x46}, []byte{0x37, 0x3C})
	copy(valid[8:20], wantTxid[:])
	n.handleDatagram(valid, from)
	select {
	case resp := <-n.stunCh:
		if resp.txid != wantTxid {
			t.Fatalf("txid mismatch: got %v want %v", resp.txid, wantTxid)
		}
		if resp.addr != "1.2.3.4:5678" {
			t.Fatalf("addr = %q, want 1.2.3.4:5678", resp.addr)
		}
	default:
		t.Fatal("valid STUN response not surfaced after malformed datagrams")
	}
}
