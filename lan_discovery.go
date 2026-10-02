// lan_discovery.go — LAN node auto-discovery via mDNS (RFC 6762 / RFC 6763 subset).
//
// Phase 2 (PRD): closes the "LAN Discovery — not implemented (no mDNS)" gap in
// docs/FEATURES.md. Implemented with the standard library only (net UDP
// multicast) — no new external dependencies, per project style.
//
// Design:
//   - Service type: _omp._tcp.local. This node announces its identity
//     (node ID, API port, claimed models/capabilities, region) as PTR/SRV/TXT
//     records and listens for the same from peers on 224.0.0.251:5353.
//   - Discovered peers are injected into the EXISTING discovery chain via
//     netMgr.AddPeer, which already bridges into the route table, the on-disk
//     node registry and the federation trust pool (bridgePeerToFederation).
//     No second peer store is introduced.
//   - Gating: discovery starts only from activateNetwork(), i.e. only when
//     network_enabled is true. In personal mode (network_enabled=false, the
//     default) Init() never calls activateNetwork(), so this file performs
//     zero network activity. A separate "lan_discovery" config key
//     (default "true") allows opting out without leaving the network.
//
// Wire format (TXT, versioned with v=1):
//
//	id=<node id>  name=<node name>  region=<region>  share=1|0
//	models=<csv, chunked into ≤255-byte strings as needed>
//	ts=<unix seconds>  sig=<base64 ed25519 signature over the canonical payload>
//
// ts/sig are emitted when the node identity is available (v4.5.61+); receivers
// verify them against the federation trust pool when the signer's key is
// known and drop forged announcements. See the lanSigDomain block below.
//
// The sender's IP comes from the A record for the SRV target when present,
// otherwise from the UDP source address.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	lanMDNSIPv4    = "224.0.0.251"
	lanMDNSPort    = 5353
	lanServiceType = "_omp._tcp.local"  // service type, no trailing dot
	lanServiceFQDN = "_omp._tcp.local." // wire form, trailing dot
	lanMaxDatagram = 9000               // mDNS legacy max (RFC 6762 §17)
	lanRecordTTL   = 120                // seconds, our announcements
	lanTXTVersion  = "1"

	lanQueryInterval    = 60 * time.Second
	lanAnnounceInterval = 60 * time.Second
	lanSeenTTL          = 15 * time.Minute
	lanCleanupInterval  = 5 * time.Minute
)

// TXT announcement authentication (ed25519, v4.5.61+).
//
// Threat model: anyone on the L2 LAN can multicast. An unsigned announcement
// is therefore only as trustworthy as the LAN itself. To bind an announcement
// to the node's federation identity, the announcer signs the canonical TXT
// payload with its node identity key (the same key the federation trust pool
// already associates with the node ID) and appends:
//
//	ts=<unix seconds>              covered by the signature (freshness)
//	sig=<base64 ed25519 signature>  over lanSigDomain + sorted k=v strings
//
// Verification policy (fail-closed where a key is known, permissive otherwise
// for backward compatibility with pre-signature nodes):
//   - sig present, signer key known, signature valid, ts fresh  -> verified
//   - sig present, signer unknown (no key in trust pool)        -> accepted
//     provisionally (unchanged LAN trust boundary), unverified
//   - no sig                                                    -> accepted
//     (backward compatible), unverified
//   - sig present but malformed / bad signature / ts missing or
//     outside the freshness window                              -> DROPPED
//
// This strictly improves on unsigned announcements: spoofing a *known*
// (trust-pool) node's identity on the LAN is now detected and dropped, while
// unknown and legacy nodes behave exactly as before. The parser tolerates the
// new keys, so old receivers simply ignore them.
const (
	lanSigDomain     = "omp-lan-announce-v1"
	lanSigMaxAge     = 10 * time.Minute
	lanSigFutureSkew = 2 * time.Minute
)

// DNS type/class codes used by the minimal codec below.
const (
	dnsTypeA    = 1
	dnsTypePTR  = 12
	dnsTypeTXT  = 16
	dnsTypeSRV  = 33
	dnsTypeAAAA = 28
	dnsClassIN  = 1
)

// ---------------------------------------------------------------------------
// Minimal DNS codec (encode + decode). Handles exactly what mDNS needs:
// questions and resource records of type A/AAAA/PTR/SRV/TXT, including name
// compression pointers on decode. Encode never emits compression.
// ---------------------------------------------------------------------------

type dnsQuestion struct {
	name   string // canonical, trailing dot
	qtype  uint16
	qclass uint16
}

type dnsRR struct {
	name   string // canonical, trailing dot
	rtype  uint16
	rclass uint16
	ttl    uint32
	rdata  []byte
}

type dnsMessage struct {
	id            uint16
	response      bool
	authoritative bool
	questions     []dnsQuestion
	answers       []dnsRR
}

var errDNSMalformed = fmt.Errorf("malformed DNS packet")

// dnsEncodeName appends a domain name in label format. Accepts with or without
// trailing dot; the root name encodes as a single zero byte.
func dnsEncodeName(buf []byte, name string) ([]byte, error) {
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return append(buf, 0), nil
	}
	total := 0
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil, fmt.Errorf("%w: bad label length %d", errDNSMalformed, len(label))
		}
		total += 1 + len(label)
		if total > 255 {
			return nil, fmt.Errorf("%w: name too long", errDNSMalformed)
		}
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	return append(buf, 0), nil
}

// dnsDecodeName decodes a (possibly pointer-compressed) domain name at off.
// Returns the canonical name with trailing dot and the offset of the first
// byte after the encoded name in the original position.
func dnsDecodeName(pkt []byte, off int) (string, int, error) {
	var labels []string
	next := off
	jumped := false
	for jumps := 0; ; jumps++ {
		if jumps > 64 {
			return "", 0, fmt.Errorf("%w: name pointer loop", errDNSMalformed)
		}
		if off >= len(pkt) {
			return "", 0, fmt.Errorf("%w: truncated name", errDNSMalformed)
		}
		b := pkt[off]
		switch b & 0xC0 {
		case 0xC0: // compression pointer
			if off+1 >= len(pkt) {
				return "", 0, fmt.Errorf("%w: truncated pointer", errDNSMalformed)
			}
			ptr := int(b&0x3F)<<8 | int(pkt[off+1])
			if ptr >= len(pkt) {
				return "", 0, fmt.Errorf("%w: pointer out of range", errDNSMalformed)
			}
			if !jumped {
				next = off + 2
			}
			jumped = true
			off = ptr
		case 0x00: // label or terminator
			l := int(b)
			off++
			if l == 0 {
				if !jumped {
					next = off
				}
				if len(labels) == 0 {
					return ".", next, nil
				}
				return strings.Join(labels, ".") + ".", next, nil
			}
			if off+l > len(pkt) {
				return "", 0, fmt.Errorf("%w: truncated label", errDNSMalformed)
			}
			labels = append(labels, string(pkt[off:off+l]))
			off += l
			if len(labels) > 127 {
				return "", 0, fmt.Errorf("%w: too many labels", errDNSMalformed)
			}
		default:
			return "", 0, fmt.Errorf("%w: bad label bits", errDNSMalformed)
		}
	}
}

func dnsEncodeQuestion(buf []byte, q dnsQuestion) ([]byte, error) {
	var err error
	if buf, err = dnsEncodeName(buf, q.name); err != nil {
		return nil, err
	}
	var tmp [4]byte
	binary.BigEndian.PutUint16(tmp[0:2], q.qtype)
	binary.BigEndian.PutUint16(tmp[2:4], q.qclass)
	return append(buf, tmp[:]...), nil
}

func dnsEncodeRR(buf []byte, rr dnsRR) ([]byte, error) {
	var err error
	if buf, err = dnsEncodeName(buf, rr.name); err != nil {
		return nil, err
	}
	var tmp [10]byte
	binary.BigEndian.PutUint16(tmp[0:2], rr.rtype)
	binary.BigEndian.PutUint16(tmp[2:4], rr.rclass)
	binary.BigEndian.PutUint32(tmp[4:8], rr.ttl)
	binary.BigEndian.PutUint16(tmp[8:10], uint16(len(rr.rdata)))
	buf = append(buf, tmp[:]...)
	return append(buf, rr.rdata...), nil
}

// encodeDNSMessage serializes a query or response.
func encodeDNSMessage(m *dnsMessage) ([]byte, error) {
	if len(m.questions) > 32 || len(m.answers) > 128 {
		return nil, fmt.Errorf("%w: too many sections", errDNSMalformed)
	}
	buf := make([]byte, 0, 512)
	var hdr [12]byte
	binary.BigEndian.PutUint16(hdr[0:2], m.id)
	var flags uint16
	if m.response {
		flags |= 0x8000 // QR
	}
	if m.authoritative {
		flags |= 0x0400 // AA
	}
	binary.BigEndian.PutUint16(hdr[2:4], flags)
	binary.BigEndian.PutUint16(hdr[4:6], uint16(len(m.questions)))
	binary.BigEndian.PutUint16(hdr[6:8], uint16(len(m.answers)))
	buf = append(buf, hdr[:]...)
	var err error
	for _, q := range m.questions {
		if buf, err = dnsEncodeQuestion(buf, q); err != nil {
			return nil, err
		}
	}
	for _, rr := range m.answers {
		if buf, err = dnsEncodeRR(buf, rr); err != nil {
			return nil, err
		}
	}
	if len(buf) > lanMaxDatagram {
		return nil, fmt.Errorf("DNS message exceeds %d bytes", lanMaxDatagram)
	}
	return buf, nil
}

// decodeDNSMessage parses a DNS query/response with strict bounds checking.
// Never panics on hostile input.
func decodeDNSMessage(pkt []byte) (*dnsMessage, error) {
	if len(pkt) < 12 {
		return nil, fmt.Errorf("%w: short header", errDNSMalformed)
	}
	m := &dnsMessage{}
	m.id = binary.BigEndian.Uint16(pkt[0:2])
	flags := binary.BigEndian.Uint16(pkt[2:4])
	m.response = flags&0x8000 != 0
	m.authoritative = flags&0x0400 != 0
	qd := binary.BigEndian.Uint16(pkt[4:6])
	an := binary.BigEndian.Uint16(pkt[6:8])
	// NS/AR sections are ignored for mDNS discovery; skip them.
	ns := binary.BigEndian.Uint16(pkt[8:10])
	ar := binary.BigEndian.Uint16(pkt[10:12])
	if qd > 32 || an > 128 || ns > 128 || ar > 128 {
		return nil, fmt.Errorf("%w: section count too large", errDNSMalformed)
	}
	off := 12
	for i := 0; i < int(qd); i++ {
		name, next, err := dnsDecodeName(pkt, off)
		if err != nil {
			return nil, err
		}
		if next+4 > len(pkt) {
			return nil, fmt.Errorf("%w: truncated question", errDNSMalformed)
		}
		m.questions = append(m.questions, dnsQuestion{
			name:   name,
			qtype:  binary.BigEndian.Uint16(pkt[next : next+2]),
			qclass: binary.BigEndian.Uint16(pkt[next+2 : next+4]),
		})
		off = next + 4
	}
	readRR := func() (dnsRR, int, error) {
		var rr dnsRR
		name, next, err := dnsDecodeName(pkt, off)
		if err != nil {
			return rr, 0, err
		}
		if next+10 > len(pkt) {
			return rr, 0, fmt.Errorf("%w: truncated RR header", errDNSMalformed)
		}
		rr.name = name
		rr.rtype = binary.BigEndian.Uint16(pkt[next : next+2])
		rr.rclass = binary.BigEndian.Uint16(pkt[next+2 : next+4])
		rr.ttl = binary.BigEndian.Uint32(pkt[next+4 : next+8])
		rdlen := int(binary.BigEndian.Uint16(pkt[next+8 : next+10]))
		if next+10+rdlen > len(pkt) {
			return rr, 0, fmt.Errorf("%w: truncated RDATA", errDNSMalformed)
		}
		rr.rdata = pkt[next+10 : next+10+rdlen]
		return rr, next + 10 + rdlen, nil
	}
	for i := 0; i < int(an); i++ {
		rr, next, err := readRR()
		if err != nil {
			return nil, err
		}
		m.answers = append(m.answers, rr)
		off = next
	}
	// Skip NS/AR sections without parsing.
	for i := 0; i < int(ns)+int(ar); i++ {
		_, next, err := readRR()
		if err != nil {
			return nil, err
		}
		off = next
	}
	return m, nil
}

// dnsEncodeSRV builds SRV RDATA: priority, weight, port, target.
func dnsEncodeSRV(port uint16, target string) ([]byte, error) {
	buf := []byte{0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(buf[4:6], port)
	return dnsEncodeName(buf, target)
}

// dnsDecodeSRV parses SRV RDATA.
func dnsDecodeSRV(rdata []byte) (port uint16, target string, err error) {
	if len(rdata) < 6 {
		return 0, "", fmt.Errorf("%w: short SRV rdata", errDNSMalformed)
	}
	target, _, err = dnsDecodeName(rdata, 6)
	if err != nil {
		return 0, "", err
	}
	return binary.BigEndian.Uint16(rdata[4:6]), target, nil
}

// dnsDecodeTXT parses TXT RDATA into its character strings.
func dnsDecodeTXT(rdata []byte) ([]string, error) {
	var out []string
	for len(rdata) > 0 {
		l := int(rdata[0])
		rdata = rdata[1:]
		if l > len(rdata) {
			return nil, fmt.Errorf("%w: truncated TXT string", errDNSMalformed)
		}
		out = append(out, string(rdata[:l]))
		rdata = rdata[l:]
		if len(out) > 64 {
			return nil, fmt.Errorf("%w: too many TXT strings", errDNSMalformed)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// LAN announcement: self description <-> mDNS records
// ---------------------------------------------------------------------------

// lanSelfInfo is what this node advertises to the LAN.
type lanSelfInfo struct {
	NodeID string
	Name   string
	Port   int
	Region string
	Models []string
	Share  bool
	IP     net.IP // optional; used for the A record when known
}

// lanPeerInfo is a parsed remote announcement.
type lanPeerInfo struct {
	NodeID   string
	Name     string
	Instance string // service instance FQDN
	IP       string
	Port     int
	Region   string
	Models   []string
	Share    bool
	// Verified reports that the announcement carried a valid ed25519 signature
	// from a key known in our federation trust pool. Announcements with
	// Verified=false (unsigned, or signed by an unknown key) carry no identity
	// binding at all: lanRegisterPeer refuses to bridge them, so they never
	// enter the route table or the trust pool.
	Verified bool
}

// lanInstanceName derives a stable, DNS-safe service instance name from the
// node ID: omp-<first 12 hex chars>._omp._tcp.local.
func lanInstanceName(nodeID string) string {
	short := strings.TrimPrefix(nodeID, "mmx-")
	if len(short) > 12 {
		short = short[:12]
	}
	var sb strings.Builder
	sb.WriteString("omp-")
	for _, r := range strings.ToLower(short) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	if sb.Len() == 4 { // node id was empty/unsafe; never happens in practice
		sb.WriteString("node")
	}
	return sb.String()
}

// lanHostName is the SRV target / A-record owner for an instance.
func lanHostName(instance string) string {
	return instance + ".local."
}

// fingerprint identifies a peer announcement for dedup: any change in routing
// or advertised content re-triggers registration.
func (p *lanPeerInfo) fingerprint() string {
	models := append([]string(nil), p.Models...)
	sort.Strings(models)
	return strings.Join([]string{p.IP, strconv.Itoa(p.Port), p.Region, strings.Join(models, ","), strconv.FormatBool(p.Share)}, "|")
}

// buildLANAnnouncement renders our PTR/SRV/TXT (+A when IP known) records.
// sign authenticates the TXT payload; nil means an unsigned announcement
// (backward compatible). Callers pass lanCaptureSigner(self.NodeID); the
// discovery engine passes its startup-captured signer so the background
// announce loop never re-reads the node/enc globals.
func buildLANAnnouncement(self *lanSelfInfo, sign func([]byte) string) ([]byte, error) {
	instance := lanInstanceName(self.NodeID) + "." + lanServiceFQDN
	host := lanHostName(lanInstanceName(self.NodeID))

	var answers []dnsRR
	// PTR: service type -> instance
	ptrTarget, err := dnsEncodeName(nil, instance)
	if err != nil {
		return nil, err
	}
	answers = append(answers, dnsRR{name: lanServiceFQDN, rtype: dnsTypePTR, rclass: dnsClassIN, ttl: lanRecordTTL, rdata: ptrTarget})

	// SRV: instance -> port + host
	srvRdata, err := dnsEncodeSRV(uint16(self.Port), host)
	if err != nil {
		return nil, err
	}
	answers = append(answers, dnsRR{name: instance, rtype: dnsTypeSRV, rclass: dnsClassIN, ttl: lanRecordTTL, rdata: srvRdata})

	// TXT: versioned key=value attributes
	txt := lanBuildTXT(self, sign)
	answers = append(answers, dnsRR{name: instance, rtype: dnsTypeTXT, rclass: dnsClassIN, ttl: lanRecordTTL, rdata: txt})

	// A: host -> IPv4, when we know our LAN address
	if ip4 := self.IP.To4(); ip4 != nil {
		answers = append(answers, dnsRR{name: host, rtype: dnsTypeA, rclass: dnsClassIN, ttl: lanRecordTTL, rdata: []byte(ip4)})
	}

	return encodeDNSMessage(&dnsMessage{response: true, authoritative: true, answers: answers})
}

// lanBuildTXT renders the TXT strings; values are length-capped (each TXT
// string ≤ 255 bytes) and the model list is chunked as needed. sign, when
// non-nil, authenticates the canonical payload (see lanCaptureSigner).
func lanBuildTXT(self *lanSelfInfo, sign func([]byte) string) []byte {
	var strs []string
	add := func(k, v string) {
		v = strings.ReplaceAll(v, "\n", " ")
		v = strings.ReplaceAll(v, "\r", " ")
		if len(v) > 200 {
			v = v[:200]
		}
		s := k + "=" + v
		if len(s) > 255 {
			s = s[:255]
		}
		strs = append(strs, s)
	}
	add("v", lanTXTVersion)
	add("id", self.NodeID)
	if self.Name != "" {
		add("name", self.Name)
	}
	if self.Region != "" {
		add("region", self.Region)
	}
	share := "0"
	if self.Share {
		share = "1"
	}
	add("share", share)
	// Chunk the model list so no TXT string exceeds 255 bytes.
	var chunk []string
	chunkLen := len("models=")
	flush := func() {
		if len(chunk) > 0 {
			strs = append(strs, "models="+strings.Join(chunk, ","))
			chunk = nil
			chunkLen = len("models=")
		}
	}
	for _, m := range self.Models {
		m = strings.ReplaceAll(strings.ReplaceAll(m, ",", " "), " ", "")
		if m == "" {
			continue
		}
		if chunkLen+1+len(m) > 255 {
			flush()
		}
		chunk = append(chunk, m)
		chunkLen += 1 + len(m)
	}
	flush()

	// Freshness timestamp, covered by the signature below.
	add("ts", strconv.FormatInt(time.Now().Unix(), 10))

	// Authenticate the announcement with the node identity key when available.
	// Unsigned announcements remain valid for backward compatibility.
	if sign != nil {
		if sig := sign(lanTXTSignPayload(strs)); sig != "" {
			if s := "sig=" + sig; len(s) <= 255 {
				strs = append(strs, s)
			}
		}
	}

	var out []byte
	for _, s := range strs {
		out = append(out, byte(len(s)))
		out = append(out, s...)
	}
	return out
}

// lanTXTSignPayload returns the domain-separated canonical bytes covered by
// the TXT signature: the domain string followed by the k=v strings (excluding
// "sig") in sorted order, newline-joined. Both sides reconstruct exactly these
// bytes from the wire strings, so truncation applied by lanBuildTXT is already
// reflected on both ends.
func lanTXTSignPayload(strs []string) []byte {
	cp := make([]string, 0, len(strs))
	for _, s := range strs {
		if s == "sig" || strings.HasPrefix(s, "sig=") {
			continue
		}
		cp = append(cp, s)
	}
	sort.Strings(cp)
	var b strings.Builder
	b.WriteString(lanSigDomain)
	b.WriteByte('\n')
	for i, s := range cp {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s)
	}
	return []byte(b.String())
}

// lanCaptureSigner snapshots this node's TXT-announcement signing capability.
// The *NodeIdentity pointer AND the *Encryptor are captured once, at discovery
// start: the background announce loop must never re-read the node/enc globals,
// which test fixtures reassign (data race). In production both are fixed at
// startup, so the snapshot is exact. Returns nil when there is no usable
// identity or the advertised ID is not our own — we never sign an
// announcement for an ID we do not own.
func lanCaptureSigner(nodeID string) func([]byte) string {
	n := node
	e := enc
	if n == nil || e == nil || nodeID == "" || !n.IsInitialized() || n.NodeID() != nodeID {
		return nil
	}
	return func(payload []byte) string {
		return n.signWith(e, payload)
	}
}

// lanTrustPubKey resolves a node's ed25519 public key from the federation
// trust pool (same source the punch-offer verifier uses). It is a variable so
// tests can substitute a fixture.
var lanTrustPubKey = func(nodeID string) (ed25519.PublicKey, bool) {
	if fed == nil {
		return nil, false
	}
	info, ok := fed.GetNode(nodeID)
	if !ok || info == nil || info.PubKey == "" {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(info.PubKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, false
	}
	return ed25519.PublicKey(raw), true
}

// lanTXTAuthn is the authentication outcome for one announcement's TXT.
type lanTXTAuthn int

const (
	// lanTXTUnsigned: no sig present — legacy node, accept (LAN trust boundary).
	lanTXTUnsigned lanTXTAuthn = iota
	// lanTXTVerified: sig valid against the trust-pool key, ts fresh.
	lanTXTVerified
	// lanTXTUnverifiable: sig present but the signer is unknown to us —
	// accept provisionally, exactly like an unsigned announcement.
	lanTXTUnverifiable
	// lanTXTRejected: sig malformed, bad signature, or ts missing/stale —
	// drop the announcement.
	lanTXTRejected
)

// lanVerifyAnnouncement authenticates one instance's TXT strings per the
// policy documented on the lanSigDomain block. lookup resolves the signer's
// public key; callers pass lanTrustPubKey, while the discovery engine passes
// its startup-captured snapshot so background goroutines never race the
// lanTrustPubKey variable (reassigned by test fixtures).
func lanVerifyAnnouncement(nodeID string, strs []string, lookup func(string) (ed25519.PublicKey, bool)) lanTXTAuthn {
	var sigVals []string
	for _, s := range strs {
		if s == "sig" || strings.HasPrefix(s, "sig=") {
			sigVals = append(sigVals, s)
		}
	}
	if len(sigVals) == 0 {
		return lanTXTUnsigned
	}
	if len(sigVals) != 1 {
		return lanTXTRejected // never emitted by us; malformed
	}
	sigB64, _ := strings.CutPrefix(sigVals[0], "sig=")
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return lanTXTRejected
	}
	// The signature must cover a fresh timestamp, otherwise a captured
	// announcement could be replayed indefinitely after the node leaves.
	var tsVals []string
	for _, s := range strs {
		if s == "ts" || strings.HasPrefix(s, "ts=") {
			tsVals = append(tsVals, s)
		}
	}
	if len(tsVals) != 1 {
		return lanTXTRejected
	}
	tsStr, _ := strings.CutPrefix(tsVals[0], "ts=")
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || ts <= 0 {
		return lanTXTRejected
	}
	age := time.Since(time.Unix(ts, 0))
	if age > lanSigMaxAge || age < -lanSigFutureSkew {
		slog.Debug("lan discovery: dropping announcement with stale ts",
			"node_id", nodeID, "age", age.Truncate(time.Second))
		return lanTXTRejected
	}
	pub, ok := lookup(nodeID)
	if !ok {
		return lanTXTUnverifiable
	}
	if !ed25519.Verify(pub, lanTXTSignPayload(strs), sig) {
		slog.Warn("lan discovery: dropping announcement with bad signature",
			"node_id", nodeID)
		return lanTXTRejected
	}
	return lanTXTVerified
}

// parseLANAnnouncement extracts peer infos from a decoded mDNS response.
// srcIP is the UDP source address, used when no A record is present.
// lookup resolves announcement signers' public keys (lanTrustPubKey, or the
// discovery engine's snapshot).
func parseLANAnnouncement(m *dnsMessage, srcIP net.IP, lookup func(string) (ed25519.PublicKey, bool)) []lanPeerInfo {
	if !m.response {
		return nil
	}
	var instances []string
	srv := map[string]struct {
		port   uint16
		target string
	}{}
	txt := map[string][]string{}
	aRec := map[string]net.IP{}

	for _, rr := range m.answers {
		if rr.rclass != dnsClassIN {
			continue
		}
		switch rr.rtype {
		case dnsTypePTR:
			if !strings.EqualFold(rr.name, lanServiceFQDN) {
				continue
			}
			target, _, err := dnsDecodeName(rr.rdata, 0)
			if err != nil || !strings.HasSuffix(strings.ToLower(target), "."+lanServiceFQDN) {
				continue
			}
			instances = append(instances, target)
		case dnsTypeSRV:
			port, target, err := dnsDecodeSRV(rr.rdata)
			if err != nil || port == 0 {
				continue
			}
			srv[rr.name] = struct {
				port   uint16
				target string
			}{port, target}
		case dnsTypeTXT:
			strs, err := dnsDecodeTXT(rr.rdata)
			if err != nil {
				continue
			}
			txt[rr.name] = append(txt[rr.name], strs...)
		case dnsTypeA:
			if len(rr.rdata) == 4 {
				aRec[rr.name] = net.IP(append([]byte(nil), rr.rdata...))
			}
		case dnsTypeAAAA:
			if len(rr.rdata) == 16 {
				if _, ok := aRec[rr.name]; !ok {
					aRec[rr.name] = net.IP(append([]byte(nil), rr.rdata...))
				}
			}
		}
	}

	var peers []lanPeerInfo
	for _, inst := range instances {
		s, ok := srv[inst]
		if !ok {
			continue // need SRV for port/target
		}
		kv := map[string]string{}
		var models []string
		for _, str := range txt[inst] {
			k, v, found := strings.Cut(str, "=")
			if !found {
				continue
			}
			switch k {
			case "models":
				if v != "" {
					models = append(models, strings.Split(v, ",")...)
				}
			default:
				if len(k) <= 32 && len(v) <= 200 {
					kv[k] = v
				}
			}
		}
		if kv["v"] != lanTXTVersion {
			continue // unknown TXT schema version
		}
		nodeID := kv["id"]
		if nodeID == "" || len(nodeID) > 128 {
			continue
		}
		// Authenticate the announcement when it carries a signature. Forged
		// announcements for trust-pool nodes are dropped here; unsigned and
		// unknown-signer announcements pass through unchanged.
		verified := false
		switch lanVerifyAnnouncement(nodeID, txt[inst], lookup) {
		case lanTXTRejected:
			continue
		case lanTXTVerified:
			verified = true
		}
		ip := aRec[s.target]
		if ip == nil {
			ip = srcIP
		}
		if ip == nil {
			continue
		}
		peers = append(peers, lanPeerInfo{
			NodeID:   nodeID,
			Name:     kv["name"],
			Instance: inst,
			IP:       ip.String(),
			Port:     int(s.port),
			Region:   kv["region"],
			Models:   models,
			Share:    kv["share"] == "1",
			Verified: verified,
		})
	}
	return peers
}

// ---------------------------------------------------------------------------
// Discovery engine
// ---------------------------------------------------------------------------

type lanSeenEntry struct {
	info        lanPeerInfo
	fingerprint string
	lastSeen    time.Time
	pushed      bool
	// registering is set while a register() call for fingerprint is in
	// flight. notePeer calls register() outside d.mu, so without this claim
	// marker every concurrent duplicate announcement would pass the
	// "already pushed" check and re-register the same peer (netMgr.AddPeer
	// also rewrites the on-disk registry, so the duplicates are not free).
	registering bool
}

// LANDiscovery runs the mDNS announce/listen loops for one node.
type LANDiscovery struct {
	mu       sync.Mutex
	conn     *net.UDPConn
	mcast    *net.UDPAddr
	stopCh   chan struct{}
	wg       sync.WaitGroup
	running  bool
	self     lanSelfInfo
	seen     map[string]*lanSeenEntry
	register func(lanPeerInfo) error // injectable for tests
	// signer authenticates our announcements; lookup resolves peers'
	// announcement signers. Both are snapshotted at startup so the background
	// loops never re-read the node/enc globals or the lanTrustPubKey variable
	// (reassigned by test fixtures → data race).
	signer func([]byte) string
	lookup func(string) (ed25519.PublicKey, bool)
}

var (
	lanDiscMu sync.Mutex
	lanDisc   *LANDiscovery
)

// lanDiscoveryAllowed is the pure gating predicate: network mode must be on
// and the lan_discovery key (default true) must not disable it.
func lanDiscoveryAllowed(networkEnabled bool, lanDiscoveryKey string) bool {
	if !networkEnabled {
		return false
	}
	return lanDiscoveryKey != "false"
}

// lanDiscoveryEnabled reads the live config. In personal mode
// (network_enabled=false, the default) this is false and no socket is ever
// opened — zero network activity.
func lanDiscoveryEnabled() bool {
	if netMgr == nil {
		return false
	}
	if !netMgr.IsNetworkEnabled() {
		return false
	}
	return lanDiscoveryAllowed(true, cfg.Get("lan_discovery", "true"))
}

// startLANDiscovery is called from activateNetwork(). Idempotent; a no-op in
// personal mode or when lan_discovery=false. A bind failure is logged and
// swallowed so startup never depends on the LAN.
func startLANDiscovery() {
	lanDiscMu.Lock()
	defer lanDiscMu.Unlock()
	if lanDisc != nil && lanDisc.isRunning() {
		return
	}
	if !lanDiscoveryEnabled() {
		return
	}
	self, ok := gatherLANSelfInfo()
	if !ok {
		slog.Warn("lan discovery: no node identity yet, skipping")
		return
	}
	d := &LANDiscovery{
		stopCh:   make(chan struct{}),
		seen:     make(map[string]*lanSeenEntry),
		self:     self,
		register: lanRegisterPeer,
		signer:   lanCaptureSigner(self.NodeID),
		lookup:   lanTrustPubKey,
	}
	if err := d.bind(); err != nil {
		slog.Warn("lan discovery: multicast bind failed, disabled", "error", err)
		return
	}
	d.start()
	lanDisc = d
	slog.Info("lan discovery (mDNS) started",
		"service", lanServiceType, "instance", lanInstanceName(self.NodeID), "port", self.Port)
}

// stopLANDiscovery is called from deactivateNetwork(). Idempotent.
func stopLANDiscovery() {
	lanDiscMu.Lock()
	d := lanDisc
	lanDisc = nil
	lanDiscMu.Unlock()
	if d != nil {
		d.stop()
		slog.Info("lan discovery (mDNS) stopped")
	}
}

func (d *LANDiscovery) isRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

// gatherLANSelfInfo snapshots what this node advertises. Returns false when
// there is no usable identity yet (caller logs and skips).
func gatherLANSelfInfo() (lanSelfInfo, bool) {
	if netMgr == nil {
		return lanSelfInfo{}, false
	}
	netMgr.mu.RLock()
	nodeID := netMgr.config.NodeID
	name := netMgr.config.NodeName
	models := append([]string(nil), netMgr.config.SharedModels...)
	share := netMgr.config.ShareToPool
	netMgr.mu.RUnlock()
	if nodeID == "" {
		nodeID = canonicalNodeID()
	}
	if nodeID == "" {
		return lanSelfInfo{}, false
	}
	port := 8000
	if p, err := strconv.Atoi(cfg.Get("port", "8000")); err == nil && p > 0 && p < 65536 {
		port = p
	}
	region := ""
	if regionManager != nil {
		if nr := regionManager.GetNodeRegion(nodeID); nr != nil {
			region = string(nr.Region)
		}
	}
	return lanSelfInfo{
		NodeID: nodeID,
		Name:   name,
		Port:   port,
		Region: region,
		Models: models,
		Share:  share,
		IP:     lanOutboundIP(),
	}, true
}

// lanOutboundIP returns the local IP that would be used to reach the LAN, via
// the classic UDP-dial trick. Nil when it cannot be determined; callers then
// omit the A record and receivers fall back to the packet source address.
func lanOutboundIP() net.IP {
	c, err := net.Dial("udp4", lanMDNSIPv4+":"+strconv.Itoa(lanMDNSPort))
	if err != nil {
		return nil
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok && a.IP != nil {
		return a.IP
	}
	return nil
}

func (d *LANDiscovery) bind() error {
	maddr := &net.UDPAddr{IP: net.ParseIP(lanMDNSIPv4), Port: lanMDNSPort}
	conn, err := net.ListenMulticastUDP("udp4", nil, maddr)
	if err != nil {
		return err
	}
	// Bound multicast sockets in Go carry SO_REUSEADDR, so several instances
	// on one host can share 5353.
	d.conn = conn
	d.mcast = maddr
	return nil
}

func (d *LANDiscovery) start() {
	d.mu.Lock()
	d.running = true
	d.mu.Unlock()
	d.wg.Add(4)
	go d.recvLoop()
	go d.queryLoop()
	go d.announceLoop()
	go d.cleanupLoop()
}

func (d *LANDiscovery) stop() {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	close(d.stopCh)
	conn := d.conn
	d.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
	d.wg.Wait()
}

// sendAnnounce multicasts our PTR/SRV/TXT records.
func (d *LANDiscovery) sendAnnounce() {
	d.mu.Lock()
	self := d.self
	sign := d.signer
	conn := d.conn
	mcast := d.mcast
	d.mu.Unlock()
	if conn == nil {
		return
	}
	pkt, err := buildLANAnnouncement(&self, sign)
	if err != nil {
		slog.Debug("lan discovery: build announcement failed", "error", err)
		return
	}
	if _, err := conn.WriteToUDP(pkt, mcast); err != nil {
		slog.Debug("lan discovery: announce send failed", "error", err)
	}
}

// sendQuery multicasts a PTR query for our service type.
func (d *LANDiscovery) sendQuery() {
	d.mu.Lock()
	conn := d.conn
	mcast := d.mcast
	d.mu.Unlock()
	if conn == nil {
		return
	}
	pkt, err := encodeDNSMessage(&dnsMessage{questions: []dnsQuestion{
		{name: lanServiceFQDN, qtype: dnsTypePTR, qclass: dnsClassIN},
	}})
	if err != nil {
		return
	}
	if _, err := conn.WriteToUDP(pkt, mcast); err != nil {
		slog.Debug("lan discovery: query send failed", "error", err)
	}
}

func (d *LANDiscovery) stopped() bool {
	select {
	case <-d.stopCh:
		return true
	default:
		return false
	}
}

func (d *LANDiscovery) recvLoop() {
	defer d.wg.Done()
	buf := make([]byte, lanMaxDatagram)
	for !d.stopped() {
		d.mu.Lock()
		conn := d.conn
		d.mu.Unlock()
		if conn == nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if d.stopped() {
				return
			}
			slog.Debug("lan discovery: read error", "error", err)
			continue
		}
		if n == 0 || n > lanMaxDatagram {
			continue
		}
		// Copy: the buffer is reused across iterations.
		pkt := append([]byte(nil), buf[:n]...)
		d.handlePacket(pkt, src)
	}
}

func (d *LANDiscovery) handlePacket(pkt []byte, src *net.UDPAddr) {
	m, err := decodeDNSMessage(pkt)
	if err != nil {
		slog.Debug("lan discovery: dropping malformed packet", "error", err)
		return
	}
	if m.response {
		var srcIP net.IP
		if src != nil {
			srcIP = src.IP
		}
		// d.lookup is immutable after construction; no lock needed.
		for _, peer := range parseLANAnnouncement(m, srcIP, d.lookup) {
			d.notePeer(peer)
		}
		return
	}
	// Answer queries for our own service type (polite responder).
	for _, q := range m.questions {
		if q.qtype == dnsTypePTR && strings.EqualFold(q.name, lanServiceFQDN) {
			d.sendAnnounce()
			return
		}
	}
}

func (d *LANDiscovery) queryLoop() {
	defer d.wg.Done()
	d.sendQuery() // immediate, then periodic
	t := time.NewTicker(lanQueryInterval)
	defer t.Stop()
	for {
		select {
		case <-d.stopCh:
			return
		case <-t.C:
			d.sendQuery()
		}
	}
}

func (d *LANDiscovery) announceLoop() {
	defer d.wg.Done()
	d.sendAnnounce()
	// mDNS recommends a second announcement ~1s after the first.
	select {
	case <-d.stopCh:
		return
	case <-time.After(time.Second):
	}
	d.sendAnnounce()
	t := time.NewTicker(lanAnnounceInterval)
	defer t.Stop()
	for {
		select {
		case <-d.stopCh:
			return
		case <-t.C:
			d.sendAnnounce()
		}
	}
}

func (d *LANDiscovery) cleanupLoop() {
	defer d.wg.Done()
	t := time.NewTicker(lanCleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-d.stopCh:
			return
		case <-t.C:
			d.cleanup()
		}
	}
}

func (d *LANDiscovery) cleanup() {
	cutoff := time.Now().Add(-lanSeenTTL)
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, e := range d.seen {
		if e.lastSeen.Before(cutoff) {
			delete(d.seen, id)
		}
	}
}

// notePeer dedupes by node ID + announcement fingerprint. A peer is pushed
// into the discovery chain (netMgr.AddPeer) once, and again only when its
// fingerprint changes (new address/port/region/models/share flag).
func (d *LANDiscovery) notePeer(peer lanPeerInfo) {
	d.mu.Lock()
	if peer.NodeID == "" || peer.NodeID == d.self.NodeID {
		d.mu.Unlock()
		return
	}
	fp := peer.fingerprint()
	if e, ok := d.seen[peer.NodeID]; ok {
		// Any announcement counts as liveness, so refresh lastSeen even when
		// no registration is due (cleanup() prunes by lastSeen).
		e.lastSeen = time.Now()
		// Already registered with this fingerprint, or a registration for it
		// is in flight: concurrent duplicates must not register again.
		if e.fingerprint == fp && (e.pushed || e.registering) {
			d.mu.Unlock()
			return
		}
	}
	// Claim this (re)registration while still holding the lock so that N
	// concurrent identical announcements produce exactly one register() call.
	d.seen[peer.NodeID] = &lanSeenEntry{
		info:        peer,
		fingerprint: fp,
		lastSeen:    time.Now(),
		registering: true,
	}
	register := d.register
	d.mu.Unlock()

	if err := register(peer); err != nil {
		slog.Debug("lan discovery: peer registration failed",
			"node_id", peer.NodeID, "error", err)
		d.mu.Lock()
		// Release the claim so the next announcement retries this peer.
		if e, ok := d.seen[peer.NodeID]; ok && e.fingerprint == fp && e.registering {
			delete(d.seen, peer.NodeID)
		}
		d.mu.Unlock()
		return
	}

	d.mu.Lock()
	// Only finalize our own claim: a newer fingerprint may have taken over
	// the entry while this register() call was in flight.
	if e, ok := d.seen[peer.NodeID]; ok && e.fingerprint == fp && e.registering {
		e.registering = false
		e.pushed = true
	}
	d.mu.Unlock()
	slog.Info("lan discovery: peer found", "node_id", peer.NodeID,
		"addr", peer.IP+":"+strconv.Itoa(peer.Port), "region", peer.Region,
		"models", len(peer.Models), "verified", peer.Verified)
}

// lanRegisterPeer injects a LAN-discovered peer into the existing discovery
// chain. netMgr.AddPeer is the single entry point: it upserts the peer,
// updates the route table and on-disk registry, and bridges the peer into the
// federation trust pool so gossip propagates it (bridgePeerToFederation).
//
// Security boundary: ONLY cryptographically verified announcements are
// bridged. An unverified announcement cannot be bound to any identity, so
// bridging it would let anyone on the L2 segment inject arbitrary node IDs,
// addresses and model claims into the trust pool — and, worse, overwrite the
// PubKey (trust anchor) of an existing node and reroute its traffic to the
// attacker. Unverified announcements are returned as an error (not silently
// skipped) so that notePeer releases the in-flight claim and a later
// properly-signed announcement for the same fingerprint can still register.
func lanRegisterPeer(peer lanPeerInfo) error {
	if !peer.Verified {
		return fmt.Errorf("lan discovery: refusing to bridge unverified announcement for %s", peer.NodeID)
	}
	if netMgr == nil {
		return fmt.Errorf("network manager unavailable")
	}
	name := peer.Name
	if name == "" {
		name = lanInstanceName(peer.NodeID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return netMgr.AddPeer(PeerInfo{
		NodeID:      peer.NodeID,
		Name:        name,
		Region:      peer.Region,
		Models:      peer.Models,
		Status:      "online",
		LastSeen:    now,
		JoinedAt:    now,
		Addresses:   []string{"http://" + peer.IP + ":" + strconv.Itoa(peer.Port)},
		ShareToPool: peer.Share,
	})
}
