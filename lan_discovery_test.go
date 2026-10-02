package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	pkt, err := buildLANAnnouncement(self, lanCaptureSigner(self.NodeID))
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
	peers := parseLANAnnouncement(m, net.ParseIP("10.0.0.5"), lanTrustPubKey)
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
	pkt, err := buildLANAnnouncement(self, lanCaptureSigner(self.NodeID))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	peers := parseLANAnnouncement(m, net.ParseIP("192.168.2.3"), lanTrustPubKey)
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
	pkt, err := buildLANAnnouncement(self, lanCaptureSigner(self.NodeID))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	peers := parseLANAnnouncement(m, net.ParseIP("192.168.1.1"), lanTrustPubKey)
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

// ---------------------------------------------------------------------------
// TXT announcement authentication (ed25519 signatures)
// ---------------------------------------------------------------------------

// testLANSigKey generates a fresh ed25519 identity for signature tests.
func testLANSigKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// testLANSignedStrings builds a canonical TXT string set and signs it with
// priv, returning the full wire strings including ts and sig.
func testLANSignedStrings(t *testing.T, priv ed25519.PrivateKey, ts time.Time) []string {
	t.Helper()
	strs := []string{
		"v=1",
		"id=mmx-testnode",
		"region=cn-east",
		"share=1",
		"models=gpt-4o,claude-3-5-sonnet",
		"ts=" + strconv.FormatInt(ts.Unix(), 10),
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, lanTXTSignPayload(strs)))
	return append(strs, "sig="+sig)
}

// stubLANTrust installs a fake trust-pool key lookup for one node ID.
func stubLANTrust(t *testing.T, nodeID string, pub ed25519.PublicKey) {
	t.Helper()
	old := lanTrustPubKey
	lanTrustPubKey = func(id string) (ed25519.PublicKey, bool) {
		if id == nodeID {
			return pub, true
		}
		return nil, false
	}
	t.Cleanup(func() { lanTrustPubKey = old })
}

// testLANNodeIdentity installs a real NodeIdentity (backed by the test
// encryptor) as the global node, so lanBuildTXT signs announcements.
func testLANNodeIdentity(t *testing.T, nodeID string) ed25519.PublicKey {
	t.Helper()
	setupTestEnv(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encPriv := encryptField(base64.StdEncoding.EncodeToString(priv))
	if !strings.HasPrefix(encPriv, encPrefix) {
		t.Fatalf("test encryptor did not encrypt the private key")
	}
	old := node
	node = &NodeIdentity{nodeID: nodeID, encPrivKey: encPriv, pubKey: pub}
	t.Cleanup(func() { node = old })
	return pub
}

// TestLANSigPayloadCanonical verifies the signed payload is deterministic,
// order-independent, and excludes the sig string itself.
func TestLANSigPayloadCanonical(t *testing.T) {
	a := []string{"v=1", "id=x", "models=b,a", "ts=123", "sig=should-be-excluded"}
	b := []string{"ts=123", "sig=should-be-excluded", "id=x", "v=1", "models=b,a"}
	if string(lanTXTSignPayload(a)) != string(lanTXTSignPayload(b)) {
		t.Fatal("payload not order-independent")
	}
	p := string(lanTXTSignPayload(a))
	if !strings.HasPrefix(p, lanSigDomain+"\n") {
		t.Fatalf("missing domain separation: %q", p[:40])
	}
	if strings.Contains(p, "should-be-excluded") {
		t.Fatal("sig string leaked into signed payload")
	}
}

// TestLANVerifyAnnouncement is a table-driven policy check for the TXT
// signature verifier.
func TestLANVerifyAnnouncement(t *testing.T) {
	pub, priv := testLANSigKey(t)
	stubLANTrust(t, "mmx-testnode", pub)
	now := time.Now()

	mk := func(mut func([]string) []string) []string {
		s := testLANSignedStrings(t, priv, now)
		if mut != nil {
			s = mut(s)
		}
		return s
	}
	replace := func(old, new string) func([]string) []string {
		return func(s []string) []string {
			out := append([]string(nil), s...)
			for i, v := range out {
				if v == old {
					out[i] = new
				}
			}
			return out
		}
	}
	drop := func(prefix string) func([]string) []string {
		return func(s []string) []string {
			var out []string
			for _, v := range s {
				if !strings.HasPrefix(v, prefix) {
					out = append(out, v)
				}
			}
			return out
		}
	}

	// badSigStrs returns signed strings with the signature replaced by s.
	badSigStrs := func(s string) []string {
		strs := testLANSignedStrings(t, priv, now)
		strs[len(strs)-1] = s
		return strs
	}

	cases := []struct {
		name string
		strs []string
		node string
		want lanTXTAuthn
	}{
		{"valid", mk(nil), "mmx-testnode", lanTXTVerified},
		{"tampered region", mk(replace("region=cn-east", "region=cn-west")), "mmx-testnode", lanTXTRejected},
		{"tampered models", mk(replace("models=gpt-4o,claude-3-5-sonnet", "models=gpt-4o,evil-model")), "mmx-testnode", lanTXTRejected},
		{"tampered sig", badSigStrs("sig=" + strings.Repeat("A", 88)), "mmx-testnode", lanTXTRejected},
		{"unsigned legacy", drop("sig=")(mk(nil)), "mmx-testnode", lanTXTUnsigned},
		{"unsigned legacy, no ts", drop("sig=")(drop("ts=")(mk(nil))), "mmx-testnode", lanTXTUnsigned},
		{"unknown signer accepted provisionally", mk(nil), "mmx-stranger", lanTXTUnverifiable},
		{"stale ts", testLANSignedStrings(t, priv, now.Add(-time.Hour)), "mmx-testnode", lanTXTRejected},
		{"future ts beyond skew", testLANSignedStrings(t, priv, now.Add(10*time.Minute)), "mmx-testnode", lanTXTRejected},
		{"missing ts", drop("ts=")(mk(nil)), "mmx-testnode", lanTXTRejected},
		{"bad base64 sig", badSigStrs("sig=!!!not-base64!!!"), "mmx-testnode", lanTXTRejected},
		{"duplicate sig", append(mk(nil), mk(nil)[len(mk(nil))-1]), "mmx-testnode", lanTXTRejected},
		{"wrong key", testLANSignedStrings(t, mustLANSigKey(t), now), "mmx-testnode", lanTXTRejected},
	}
	for _, c := range cases {
		if got := lanVerifyAnnouncement(c.node, c.strs, lanTrustPubKey); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

// mustLANSigKey returns a fresh private key, failing the test on error.
func mustLANSigKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv := testLANSigKey(t)
	return priv
}

// TestLANAnnouncementSignedEndToEnd builds a real announcement with the node
// identity, parses it back, and expects a verified peer.
func TestLANAnnouncementSignedEndToEnd(t *testing.T) {
	const nodeID = "mmx-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pub := testLANNodeIdentity(t, nodeID)
	stubLANTrust(t, nodeID, pub)

	self := &lanSelfInfo{
		NodeID: nodeID, Name: "signed-node", Port: 8000,
		Region: "cn-east", Models: []string{"gpt-4o"}, Share: true,
		IP: net.ParseIP("192.168.9.9"),
	}
	pkt, err := buildLANAnnouncement(self, lanCaptureSigner(self.NodeID))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	peers := parseLANAnnouncement(m, net.ParseIP("10.0.0.5"), lanTrustPubKey)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if !peers[0].Verified {
		t.Error("expected verified peer for a correctly signed announcement")
	}
}

// TestLANAnnouncementTamperedEndToEnd flips one signed field on the wire and
// expects the announcement to be dropped when the signer's key is known.
func TestLANAnnouncementTamperedEndToEnd(t *testing.T) {
	const nodeID = "mmx-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	pub := testLANNodeIdentity(t, nodeID)
	stubLANTrust(t, nodeID, pub)

	self := &lanSelfInfo{
		NodeID: nodeID, Port: 8000, Region: "cn-east",
		Models: []string{"gpt-4o"}, IP: net.ParseIP("192.168.9.9"),
	}
	pkt, err := buildLANAnnouncement(self, lanCaptureSigner(self.NodeID))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	tampered := false
	for i, rr := range m.answers {
		if rr.rtype != dnsTypeTXT {
			continue
		}
		strs, err := dnsDecodeTXT(rr.rdata)
		if err != nil {
			t.Fatal(err)
		}
		for j, s := range strs {
			if s == "region=cn-east" { // same length as "region=cn-west"
				strs[j] = "region=cn-west"
				tampered = true
			}
		}
		var out []byte
		for _, s := range strs {
			if len(s) > 255 {
				t.Fatalf("tampered string too long: %d", len(s))
			}
			out = append(out, byte(len(s)))
			out = append(out, s...)
		}
		m.answers[i].rdata = out
	}
	if !tampered {
		t.Fatal("no region string found to tamper")
	}
	pkt2, err := encodeDNSMessage(m)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	m2, err := decodeDNSMessage(pkt2)
	if err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if peers := parseLANAnnouncement(m2, net.ParseIP("10.0.0.5"), lanTrustPubKey); len(peers) != 0 {
		t.Fatalf("tampered announcement must be dropped, got %d peers", len(peers))
	}
}

// TestLANAnnouncementUnsignedBackwardCompat verifies that announcements
// without a signature (legacy nodes, or no local identity) are still
// accepted and marked unverified.
func TestLANAnnouncementUnsignedBackwardCompat(t *testing.T) {
	old := node
	node = nil // no identity -> unsigned announcement
	t.Cleanup(func() { node = old })

	self := &lanSelfInfo{
		NodeID: "mmx-legacy-node", Port: 8000,
		Region: "cn-east", Models: []string{"gpt-4o"},
		IP: net.ParseIP("192.168.9.10"),
	}
	pkt, err := buildLANAnnouncement(self, lanCaptureSigner(self.NodeID))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	peers := parseLANAnnouncement(m, net.ParseIP("10.0.0.5"), lanTrustPubKey)
	if len(peers) != 1 {
		t.Fatalf("unsigned announcement must be accepted, got %d peers", len(peers))
	}
	if peers[0].Verified {
		t.Error("unsigned announcement must not be marked verified")
	}
}

// TestLANSigNeverSignsForeignID ensures we never capture a signer for a node
// ID that is not our own identity.
func TestLANSigNeverSignsForeignID(t *testing.T) {
	testLANNodeIdentity(t, "mmx-real-id")
	if lanCaptureSigner("mmx-someone-else") != nil {
		t.Fatal("captured a signer for a foreign node ID")
	}
	if lanCaptureSigner("") != nil {
		t.Fatal("captured a signer with empty node ID")
	}
	if lanCaptureSigner("mmx-real-id") == nil {
		t.Fatal("expected a signer for our own node ID")
	}
	old := node
	node = nil
	t.Cleanup(func() { node = old })
	if lanCaptureSigner("mmx-real-id") != nil {
		t.Fatal("captured a signer with no identity")
	}
}

// TestLANAnnouncementUnsignedKnownNodeWarns verifies P3-6: an unsigned
// announcement for a node whose key we already hold logs a downgrade warning
// (possible downgrade attack or outdated peer). Unknown-node unsigned traffic
// stays quiet, and both still parse as unverified (the bridge gate in
// lanRegisterPeer is what refuses them).
func TestLANAnnouncementUnsignedKnownNodeWarns(t *testing.T) {
	const nodeID = "mmx-known-unsigned"
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	stubLANTrust(t, nodeID, pub)

	buildUnsigned := func(id string) *dnsMessage {
		old := node
		node = nil // no identity -> unsigned announcement
		defer func() { node = old }()
		self := &lanSelfInfo{NodeID: id, Port: 8000, Region: "cn-east",
			Models: []string{"gpt-4o"}, IP: net.ParseIP("192.168.9.10")}
		pkt, err := buildLANAnnouncement(self, lanCaptureSigner(self.NodeID))
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		m, err := decodeDNSMessage(pkt)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return m
	}

	var buf bytes.Buffer
	oldLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLog) })

	peers := parseLANAnnouncement(buildUnsigned(nodeID), net.ParseIP("10.0.0.5"), lanTrustPubKey)
	if len(peers) != 1 || peers[0].Verified {
		t.Fatalf("known-node unsigned announcement must parse unverified, got %+v", peers)
	}
	if got := buf.String(); !strings.Contains(got, "unsigned announcement for a known node") {
		t.Fatalf("expected downgrade warning, got %q", got)
	}

	buf.Reset()
	peers = parseLANAnnouncement(buildUnsigned("mmx-unknown-node"), net.ParseIP("10.0.0.5"), lanTrustPubKey)
	if len(peers) != 1 {
		t.Fatalf("unknown-node unsigned announcement must still parse, got %d", len(peers))
	}
	if got := buf.String(); strings.Contains(got, "unsigned announcement for a known node") {
		t.Fatalf("unknown nodes must not warn, got %q", got)
	}
}

// TestLANRegisterPeer_RequiresVerified is the regression guard for the LAN
// trust-anchor poisoning: an unverified (unsigned / unknown-signer)
// announcement must never enter the route table or the trust pool, even when
// it claims an existing trusted NodeID, while a verified one still bridges.
func TestLANRegisterPeer_RequiresVerified(t *testing.T) {
	setupDiscoveryTestEnv(t)

	// Authoritative pubkey fixture so the verified path has no network gap.
	peerPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	pubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/node/pubkey" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]any{"pub_key": base64.StdEncoding.EncodeToString(peerPub)})
	}))
	defer pubSrv.Close()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(pubSrv.URL, "http://"))
	if err != nil {
		t.Fatalf("split hostport: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	// 1. Unverified announcement: hard refusal, zero side effects.
	unverified := lanPeerInfo{NodeID: "mmx-lan-evil", IP: "127.0.0.1", Port: port, Verified: false}
	if err := lanRegisterPeer(unverified); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("unverified announcement must be refused, got %v", err)
	}
	if netMgr.HasPeer("mmx-lan-evil") {
		t.Fatal("unverified peer entered the peer list")
	}
	if routeTable.Get("mmx-lan-evil") != nil {
		t.Fatal("unverified peer entered the route table")
	}
	if _, ok := fed.GetNode("mmx-lan-evil"); ok {
		t.Fatal("unverified peer entered the trust pool")
	}

	// 2. Same-shaped but verified announcement: bridges normally.
	verified := lanPeerInfo{NodeID: "mmx-lan-good", IP: "127.0.0.1", Port: port, Verified: true}
	if err := lanRegisterPeer(verified); err != nil {
		t.Fatalf("verified peer must bridge, got %v", err)
	}
	if _, ok := fed.GetNode("mmx-lan-good"); !ok {
		t.Fatal("verified peer was not bridged into the trust pool")
	}
	if routeTable.Get("mmx-lan-good") == nil {
		t.Fatal("verified peer missing from the route table")
	}
}

// TestUpsertKnownNode_PreservesEstablishedPubKey locks the trust-anchor rule:
// without a signed key-rotation protocol, an incoming record can never
// replace an established PubKey (indistinguishable from impersonation).
func TestUpsertKnownNode_PreservesEstablishedPubKey(t *testing.T) {
	setupDiscoveryTestEnv(t)

	keyA := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xA5}, 32))
	keyB := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5A}, 32))
	fed.AddKnownNode(NodeInfo{NodeID: "mmx-anchor", PubKey: keyA, Addresses: []string{"https://a.example.com"}})

	// A forged "new key" for the same node must not replace the trust anchor.
	fed.AddKnownNode(NodeInfo{NodeID: "mmx-anchor", PubKey: keyB, Addresses: []string{"https://evil.example.com"}})
	got, ok := fed.GetNode("mmx-anchor")
	if !ok {
		t.Fatal("anchor node missing from trust pool")
	}
	if got.PubKey != keyA {
		t.Fatalf("established pubkey overwritten: got %q want %q", got.PubKey, keyA)
	}

	// An empty incoming key keeps the existing one (previous behavior preserved).
	fed.AddKnownNode(NodeInfo{NodeID: "mmx-anchor", Addresses: []string{"https://b.example.com"}})
	if got, _ = fed.GetNode("mmx-anchor"); got.PubKey != keyA {
		t.Fatalf("pubkey lost on empty update: %q", got.PubKey)
	}

	// A brand-new node still records the incoming key.
	fed.AddKnownNode(NodeInfo{NodeID: "mmx-fresh", PubKey: keyB})
	if fresh, ok := fed.GetNode("mmx-fresh"); !ok || fresh.PubKey != keyB {
		t.Fatalf("fresh node key not recorded: %+v", fresh)
	}
}
