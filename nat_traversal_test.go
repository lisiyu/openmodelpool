package main

import "testing"

// makeSTUNBindingResponse builds a minimal RFC 5389 Binding Response carrying a
// single XOR-MAPPED-ADDRESS attribute whose XORed IPv4/IPv4-port are xorIP/xorPort.
// Plaintext_ip = XOR(ip, 0x2112A442); plaintext_port = XOR(port, 0x2112).
func makeSTUNBindingResponse(xorIP, xorPort []byte) []byte {
	buf := make([]byte, 32)
	buf[0], buf[1] = 0x01, 0x01 // Binding Response (success)
	buf[2], buf[3] = 0x00, 0x08 // Message length = 8 (one attribute)
	buf[4], buf[5], buf[6], buf[7] = 0x21, 0x12, 0xA4, 0x42 // Magic cookie
	buf[20], buf[21] = 0x00, 0x20 // XOR-MAPPED-ADDRESS
	buf[22], buf[23] = 0x00, 0x08 // length 8
	buf[25] = 0x01                // IPv4 family
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
