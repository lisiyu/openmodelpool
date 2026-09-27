package main

import (
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestDNSCodecRoundTrip encodes a full mDNS-style response (PTR/SRV/TXT/A)
// and decodes it back, verifying every field survives the round trip.
func TestDNSCodecRoundTrip(t *testing.T) {
	instance := "omp-abc123def456._omp._tcp.local."
	host := "omp-abc123def456.local."

	srvRdata, err := dnsEncodeSRV(8000, host)
	if err != nil {
		t.Fatalf("encode SRV: %v", err)
	}
	ptrTarget, err := dnsEncodeName(nil, instance)
	if err != nil {
		t.Fatalf("encode PTR target: %v", err)
	}
	txt := []byte{3} // "v=1" (len-prefixed)
	txt = append(txt, "v=1"...)
	txt = append(txt, byte(len("id=mmx-abc")))
	txt = append(txt, "id=mmx-abc"...)

	msg := &dnsMessage{
		response:      true,
		authoritative: true,
		questions: []dnsQuestion{
			{name: lanServiceFQDN, qtype: dnsTypePTR, qclass: dnsClassIN},
		},
		answers: []dnsRR{
			{name: lanServiceFQDN, rtype: dnsTypePTR, rclass: dnsClassIN, ttl: 120, rdata: ptrTarget},
			{name: instance, rtype: dnsTypeSRV, rclass: dnsClassIN, ttl: 120, rdata: srvRdata},
			{name: instance, rtype: dnsTypeTXT, rclass: dnsClassIN, ttl: 120, rdata: txt},
			{name: host, rtype: dnsTypeA, rclass: dnsClassIN, ttl: 120, rdata: net.ParseIP("192.168.1.7").To4()},
		},
	}
	pkt, err := encodeDNSMessage(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	dec, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !dec.response || !dec.authoritative {
		t.Fatalf("flags lost: response=%v authoritative=%v", dec.response, dec.authoritative)
	}
	if len(dec.questions) != 1 || dec.questions[0].name != lanServiceFQDN || dec.questions[0].qtype != dnsTypePTR {
		t.Fatalf("question mismatch: %+v", dec.questions)
	}
	if len(dec.answers) != 4 {
		t.Fatalf("expected 4 answers, got %d", len(dec.answers))
	}
	// PTR target decodes back to the instance name.
	target, _, err := dnsDecodeName(dec.answers[0].rdata, 0)
	if err != nil || target != instance {
		t.Fatalf("PTR target = %q, err=%v", target, err)
	}
	// SRV port + target.
	port, starget, err := dnsDecodeSRV(dec.answers[1].rdata)
	if err != nil || port != 8000 || starget != host {
		t.Fatalf("SRV = port %d target %q err %v", port, starget, err)
	}
	// TXT strings.
	strs, err := dnsDecodeTXT(dec.answers[2].rdata)
	if err != nil || len(strs) != 2 || strs[0] != "v=1" || strs[1] != "id=mmx-abc" {
		t.Fatalf("TXT = %q err %v", strs, err)
	}
	// A record.
	if ip := net.IP(dec.answers[3].rdata); !ip.Equal(net.ParseIP("192.168.1.7")) {
		t.Fatalf("A = %v", ip)
	}
}

// TestDNSDecodeMalformed feeds hostile inputs and requires clean errors,
// never a panic.
func TestDNSDecodeMalformed(t *testing.T) {
	cases := [][]byte{
		{},                    // empty
		{0, 1, 2},             // short header
		make([]byte, 12),      // header only, counts zero -> ok actually; covered below
		{0, 0, 0x84, 0, 0, 1}, // short header
		{0, 0, 0x84, 0, 0, 1, 0, 0, 0, 0, 0, 0, 3}, // truncated question name
	}
	for i, pkt := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("case %d panicked: %v", i, r)
				}
			}()
			_, _ = decodeDNSMessage(pkt)
		}()
	}

	// Pointer loop: name at 12 is a pointer to 12.
	loop := []byte{0, 0, 0x84, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xC0, 0x0C, 0, 12, 0, 1}
	if _, err := decodeDNSMessage(loop); err == nil {
		t.Fatalf("pointer loop not detected")
	}

	// Pointer out of range.
	bad := []byte{0, 0, 0x84, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xC0, 0xFF, 0, 12, 0, 1}
	if _, err := decodeDNSMessage(bad); err == nil {
		t.Fatalf("out-of-range pointer not detected")
	}

	// Truncated RDATA.
	tr := []byte{0, 0, 0x84, 0, 0, 0, 0, 1, 0, 0, 0, 0,
		3, 'f', 'o', 'o', 0, 0, 1, 0, 1, 0, 0, 0, 120, 0, 10, 1, 2}
	if _, err := decodeDNSMessage(tr); err == nil {
		t.Fatalf("truncated RDATA not detected")
	}
}

// TestLANAnnouncementRoundTrip builds our own announcement, decodes and
// parses it, and checks every advertised field is recovered.
func TestLANAnnouncementRoundTrip(t *testing.T) {
	self := &lanSelfInfo{
		NodeID: "mmx-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Name:   "test-node",
		Port:   8000,
		Region: "cn-east",
		Models: []string{"gpt-4o", "claude-3-5-sonnet"},
		Share:  true,
		IP:     net.ParseIP("192.168.9.9"),
	}
	pkt, err := buildLANAnnouncement(self)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(pkt) > lanMaxDatagram {
		t.Fatalf("packet too large: %d", len(pkt))
	}
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	peers := parseLANAnnouncement(m, net.ParseIP("10.0.0.5"))
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	p := peers[0]
	if p.NodeID != self.NodeID {
		t.Errorf("node id = %q", p.NodeID)
	}
	if p.Port != 8000 {
		t.Errorf("port = %d", p.Port)
	}
	if p.Region != "cn-east" {
		t.Errorf("region = %q", p.Region)
	}
	if p.IP != "192.168.9.9" {
		t.Errorf("ip = %q (A record should win over src)", p.IP)
	}
	if len(p.Models) != 2 || p.Models[0] != "gpt-4o" || p.Models[1] != "claude-3-5-sonnet" {
		t.Errorf("models = %q", p.Models)
	}
	if !p.Share {
		t.Errorf("share flag lost")
	}
	if p.Name != "test-node" {
		t.Errorf("name = %q", p.Name)
	}
}

// TestLANAnnouncementNoARecordFallsBackToSrc verifies the receiver uses the
// UDP source address when the announcement carries no A record.
func TestLANAnnouncementNoARecordFallsBackToSrc(t *testing.T) {
	self := &lanSelfInfo{NodeID: "mmx-deadbeef", Port: 8000}
	pkt, err := buildLANAnnouncement(self)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	peers := parseLANAnnouncement(m, net.ParseIP("192.168.2.3"))
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].IP != "192.168.2.3" {
		t.Fatalf("expected src fallback ip, got %q", peers[0].IP)
	}
}

// TestLANLongModelsChunked ensures a huge model list stays within TXT limits
// and round-trips completely.
func TestLANLongModelsChunked(t *testing.T) {
	var models []string
	for i := 0; i < 60; i++ {
		models = append(models, "some-very-long-model-name-number-"+strings.Repeat("x", 10)+"-"+string(rune('a'+i%26)))
	}
	self := &lanSelfInfo{NodeID: "mmx-chunk", Port: 8000, Models: models}
	pkt, err := buildLANAnnouncement(self)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	peers := parseLANAnnouncement(m, net.ParseIP("192.168.1.1"))
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if len(peers[0].Models) != len(models) {
		t.Fatalf("models lost: got %d want %d", len(peers[0].Models), len(models))
	}
}

// TestLANNotePeerDedup drives notePeer directly: same announcement twice must
// register once; a changed fingerprint must register again; our own node id is
// ignored.
func TestLANNotePeerDedup(t *testing.T) {
	var calls atomic.Int64
	d := &LANDiscovery{
		stopCh: make(chan struct{}),
		seen:   make(map[string]*lanSeenEntry),
		self:   lanSelfInfo{NodeID: "mmx-self"},
		register: func(p lanPeerInfo) error {
			calls.Add(1)
			return nil
		},
	}
	peer := lanPeerInfo{NodeID: "mmx-peer1", IP: "192.168.1.10", Port: 8000, Region: "r1", Models: []string{"a"}}

	d.notePeer(peer)
	d.notePeer(peer) // duplicate -> no second registration
	if got := calls.Load(); got != 1 {
		t.Fatalf("duplicate peer registered %d times, want 1", got)
	}

	// Changed models -> new fingerprint -> re-register.
	peer2 := peer
	peer2.Models = []string{"a", "b"}
	d.notePeer(peer2)
	if got := calls.Load(); got != 2 {
		t.Fatalf("changed peer registered %d times total, want 2", got)
	}

	// Self is ignored.
	d.notePeer(lanPeerInfo{NodeID: "mmx-self", IP: "192.168.1.11", Port: 8000})
	if got := calls.Load(); got != 2 {
		t.Fatalf("self announcement registered, calls=%d", got)
	}

	// A different node registers independently.
	d.notePeer(lanPeerInfo{NodeID: "mmx-peer2", IP: "192.168.1.12", Port: 8000})
	if got := calls.Load(); got != 3 {
		t.Fatalf("second peer not registered, calls=%d", got)
	}
}

// TestLANNotePeerConcurrent hammers notePeer from many goroutines; the peer
// must be registered exactly once (-race must stay clean).
func TestLANNotePeerConcurrent(t *testing.T) {
	var calls atomic.Int64
	d := &LANDiscovery{
		stopCh: make(chan struct{}),
		seen:   make(map[string]*lanSeenEntry),
		self:   lanSelfInfo{NodeID: "mmx-self"},
		register: func(p lanPeerInfo) error {
			calls.Add(1)
			return nil
		},
	}
	peer := lanPeerInfo{NodeID: "mmx-peerX", IP: "192.168.1.20", Port: 8000}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				d.notePeer(peer)
			}
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent duplicate registered %d times, want 1", got)
	}
}

// TestLANDiscoveryGating checks the pure gating predicate and that
// startLANDiscovery is a strict no-op without a network manager (personal
// mode must perform zero network activity).
func TestLANDiscoveryGating(t *testing.T) {
	if !lanDiscoveryAllowed(true, "true") {
		t.Errorf("network on + default key should allow")
	}
	if lanDiscoveryAllowed(false, "true") {
		t.Errorf("personal mode (network off) must not allow")
	}
	if lanDiscoveryAllowed(true, "false") {
		t.Errorf("explicit lan_discovery=false must not allow")
	}
	if !lanDiscoveryAllowed(true, "") {
		t.Errorf("empty key should default to allow")
	}

	old := netMgr
	netMgr = nil
	defer func() { netMgr = old }()
	startLANDiscovery() // must not bind, must not panic, must not set lanDisc
	lanDiscMu.Lock()
	d := lanDisc
	lanDiscMu.Unlock()
	if d != nil {
		t.Fatalf("startLANDiscovery with nil netMgr created a discovery instance")
	}
	stopLANDiscovery() // no-op, must not panic
}

// TestLANInstanceName ensures instance names are DNS-safe and stable.
func TestLANInstanceName(t *testing.T) {
	got := lanInstanceName("mmx-0123456789ABCDEF")
	if got != "omp-0123456789ab" {
		t.Fatalf("instance = %q", got)
	}
	if lanInstanceName("mmx-0123456789ABCDEF") != got {
		t.Fatalf("instance name not stable")
	}
	// Hostile node id must still yield a DNS-safe label.
	bad := lanInstanceName("mmx-../../../evil!")
	for _, r := range bad {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			t.Fatalf("unsafe char %q in instance %q", r, bad)
		}
	}
}

// TestLANHandlePacketQueryResponds ensures an incoming PTR query for our
// service type does not crash the handler path (announce goes nowhere without
// a bound socket, which is fine).
func TestLANHandlePacketQueryResponds(t *testing.T) {
	d := &LANDiscovery{
		stopCh:   make(chan struct{}),
		seen:     make(map[string]*lanSeenEntry),
		self:     lanSelfInfo{NodeID: "mmx-self"},
		register: func(p lanPeerInfo) error { return nil },
	}
	q, err := encodeDNSMessage(&dnsMessage{questions: []dnsQuestion{
		{name: lanServiceFQDN, qtype: dnsTypePTR, qclass: dnsClassIN},
	}})
	if err != nil {
		t.Fatalf("encode query: %v", err)
	}
	// No conn bound: sendAnnounce is a no-op; must not panic.
	d.handlePacket(q, &net.UDPAddr{IP: net.ParseIP("192.168.1.99"), Port: 5353})

	// A response for a foreign service type must be ignored.
	other, _ := encodeDNSMessage(&dnsMessage{response: true, answers: []dnsRR{
		{name: "_http._tcp.local.", rtype: dnsTypePTR, rclass: dnsClassIN, ttl: 120,
			rdata: mustEncodeName(t, "web._http._tcp.local.")},
	}})
	d.handlePacket(other, &net.UDPAddr{IP: net.ParseIP("192.168.1.99"), Port: 5353})
	if len(d.seen) != 0 {
		t.Fatalf("foreign service leaked into seen map")
	}
}

func mustEncodeName(t *testing.T, name string) []byte {
	t.Helper()
	b, err := dnsEncodeName(nil, name)
	if err != nil {
		t.Fatalf("encode name: %v", err)
	}
	return b
}
