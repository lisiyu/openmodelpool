package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// NATManager handles NAT traversal: direct connectivity probing, STUN-based
// public address discovery, and the direct→relay fallback routing strategy
// per v4 design §7.5.
type NATManager struct {
	mu          sync.RWMutex
	publicAddr  string // discovered public IP:port via STUN
	natType     string // "open", "full_cone", "symmetric", "unknown"
	probeCache  map[string]probeResult
	stunServers []string

	// udpConn is a single long-lived UDP socket reused for BOTH STUN discovery
	// and hole-punching. A fresh per-query socket would remap the source port
	// each time, which defeats NAT-type detection (symmetric vs cone) and makes
	// the peer aim its punch at the wrong port. Sharing one port keeps the
	// reflexive address stable and the punches consistent.
	udpConn  *net.UDPConn
	localUDP string
	stunCh   chan string // STUN responses surfaced by udpRecvLoop
}

type probeResult struct {
	DirectOK  bool      `json:"direct_ok"`
	LatencyMS float64   `json:"latency_ms"`
	ProbedAt  time.Time `json:"probed_at"`
}

var natMgr *NATManager

func initNATManager() {
	natMgr = &NATManager{
		probeCache: make(map[string]probeResult),
		stunServers: []string{
			"stun:stun.l.google.com:19302",
			"stun:stun1.l.google.com:19302",
		},
		stunCh: make(chan string, 4),
	}
	// Bind one long-lived UDP socket reused for STUN discovery and
	// hole-punching (see udpConn doc above). On bind failure we degrade
	// gracefully: STUN discovery and UDP punching are disabled, relay stays.
	if uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0}); err != nil {
		slog.Warn("NAT UDP socket bind failed; STUN/punch disabled", "error", err)
	} else {
		natMgr.udpConn = uc
		natMgr.localUDP = uc.LocalAddr().String()
		go natMgr.udpRecvLoop()
	}
	go natMgr.stunLoop()
	ensureDirectLinkMgr()
	slog.Info("NAT traversal manager initialized", "local_udp", natMgr.localUDP)
}

// stunLoop periodically discovers the public address via STUN.
func (n *NATManager) stunLoop() {
	n.discoverPublicAddr()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-globalStopCh:
			return
		case <-ticker.C:
			n.discoverPublicAddr()
		}
	}
}

// discoverPublicAddr attempts STUN binding against every configured server to
// learn the public address and infer the NAT behaviour (see classifyNAT).
// All servers are queried so the NAT type can be distinguished; the first
// successful mapping is advertised as this node's public address.
func (n *NATManager) discoverPublicAddr() {
	if n.udpConn == nil {
		return
	}
	var addrs []string
	for _, server := range n.stunServers {
		addr, err := n.stunQueryOnConn(server)
		if err != nil {
			slog.Debug("STUN query failed", "server", server, "error", err)
			continue
		}
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 {
		return
	}
	n.mu.Lock()
	n.publicAddr = addrs[0]
	n.natType = classifyNAT(addrs)
	pub := n.publicAddr
	n.mu.Unlock()
	slog.Info("STUN discovered public address", "addr", pub, "nat_type", n.natType)
	// Keep the direct-link manager's advertised reflexive address in sync so
	// the offers it sends to peers point at the port we actually punch from.
	if directLinkMgr != nil {
		directLinkMgr.SetPubAddr(pub)
	}
}

// parseSTUNResponse extracts the XOR-MAPPED-ADDRESS from a STUN Binding
// Response (RFC 5389). It returns the mapped "ip:port", or an error if the
// packet is malformed or lacks the attribute. Kept as a pure function so the
// (over-the-network) STUN handshake can be verified with crafted packets in
// nat_traversal_test.go.
func parseSTUNResponse(buf []byte) (string, error) {
	if len(buf) < 28 || buf[0] != 0x01 || buf[1] != 0x01 {
		return "", fmt.Errorf("invalid STUN response")
	}
	// Walk the attribute list starting after the 20-byte message header.
	for i := 20; i+4 <= len(buf); {
		attrType := uint16(buf[i])<<8 | uint16(buf[i+1])
		attrLen := uint16(buf[i+2])<<8 | uint16(buf[i+3])
		if attrType == 0x0020 { // XOR-MAPPED-ADDRESS
			if int(attrLen) < 8 || i+8 > len(buf) {
				break
			}
			family := buf[i+5]
			xorPort := uint16(buf[i+6])<<8 | uint16(buf[i+7])
			port := xorPort ^ 0x2112
			if family == 0x01 { // IPv4
				xorIP := uint32(buf[i+8])<<24 | uint32(buf[i+9])<<16 |
					uint32(buf[i+10])<<8 | uint32(buf[i+11])
				ip := xorIP ^ 0x2112A442
				return fmt.Sprintf("%d.%d.%d.%d:%d",
					(ip>>24)&0xFF, (ip>>16)&0xFF, (ip>>8)&0xFF, ip&0xFF, port), nil
			}
		}
		i += 4 + int(attrLen)
		if i%4 != 0 {
			i += 4 - (i % 4)
		}
	}
	return "", fmt.Errorf("no XOR-MAPPED-ADDRESS in STUN response")
}

// classifyNAT infers a coarse NAT behaviour from the mapped addresses returned
// by two or more distinct STUN servers (RFC 5780 §4.3 lightweight test):
//   - identical (ip,port) across servers  -> "full_cone" (cone NAT or open)
//   - differing port (or ip) across servers -> "symmetric"
// A single successful response yields "unknown" (insufficient data to decide).
// Behind a symmetric NAT direct peering is unreliable, so callers should fall
// back to relay — erring toward "symmetric" here is the safe choice.
func classifyNAT(addrs []string) string {
	if len(addrs) < 2 {
		return "unknown"
	}
	firstIP, firstPort := splitHostPortSafe(addrs[0])
	for _, a := range addrs[1:] {
		ip, port := splitHostPortSafe(a)
		if ip != firstIP || port != firstPort {
			return "symmetric"
		}
	}
	return "full_cone"
}

// splitHostPortSafe parses "host:port"; on failure it returns empty strings so
// classifyNAT treats any unparseable address as a mismatch (conservative).
func splitHostPortSafe(addr string) (string, string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", ""
	}
	return host, port
}

// ProbeDirect attempts a direct HTTP connection to the target node with a
// 5-second timeout. Returns true if direct connection succeeded.
func (n *NATManager) ProbeDirect(nodeID, targetURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", targetURL+"/api/network/status", nil)
	if err != nil {
		n.recordProbe(nodeID, false, 0)
		return false
	}

	start := time.Now()
	resp, err := GetSharedHTTPClient().Do(req)
	latency := float64(time.Since(start).Milliseconds())

	if err != nil {
		n.recordProbe(nodeID, false, 0)
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	ok := resp.StatusCode < 500
	n.recordProbe(nodeID, ok, latency)
	return ok
}

// GetProbeResult returns the cached probe result for a node.
func (n *NATManager) GetProbeResult(nodeID string) (probeResult, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	r, ok := n.probeCache[nodeID]
	return r, ok
}

// recordProbe stores the result of a direct connectivity probe.
func (n *NATManager) recordProbe(nodeID string, ok bool, latencyMs float64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.probeCache[nodeID] = probeResult{
		DirectOK:  ok,
		LatencyMS: latencyMs,
		ProbedAt:  time.Now(),
	}
}

// cleanupProbeCache evicts probe results older than maxAge (PERF-P1-7).
func (n *NATManager) cleanupProbeCache(maxAge time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for id, r := range n.probeCache {
		if r.ProbedAt.Before(cutoff) {
			delete(n.probeCache, id)
		}
	}
}

// GetPublicAddr returns the discovered public address (may be empty).
func (n *NATManager) GetPublicAddr() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.publicAddr
}

// ShouldUseDirect determines whether to attempt a direct connection to a node
// based on cached probe results. Returns true if the last probe succeeded and
// is less than 5 minutes old.
func (n *NATManager) ShouldUseDirect(nodeID string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	r, ok := n.probeCache[nodeID]
	if !ok {
		return false
	}
	return r.DirectOK && time.Since(r.ProbedAt) < 5*time.Minute
}

// PreferRelay reports whether this node's NAT behaviour makes direct peering
// unreliable, so relay must be the primary transport. A symmetric NAT remaps
// the source port for every distinct destination, defeating both TCP/UDP hole
// punching and direct connections — there is no point probing direct, and
// callers should fall back to relay. full-cone / open NATs may attempt direct.
func (n *NATManager) PreferRelay() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.natType == "symmetric"
}

// handleNATStatus returns the current NAT traversal status.
func handleNATStatus(w http.ResponseWriter, r *http.Request) {
	if natMgr == nil {
		writeJSON(w, 200, map[string]any{
			"initialized":  false,
			"public_addr":  "",
			"nat_type":     "unknown",
			"probe_count":  0,
		})
		return
	}
	natMgr.mu.RLock()
	defer natMgr.mu.RUnlock()
	writeJSON(w, 200, map[string]any{
		"initialized":  true,
		"public_addr":  natMgr.publicAddr,
		"nat_type":     natMgr.natType,
		"probe_count":  len(natMgr.probeCache),
	})
}

// handleDirectProbe triggers a direct connectivity probe to a target node.
func handleDirectProbe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NodeID    string `json:"node_id"`
		TargetURL string `json:"target_url"`
	}
	if err := readJSON(w, r, &body); err != nil || body.TargetURL == "" {
		writeError(w, 400, "node_id and target_url required")
		return
	}

	// SEC-P3-23: ProbeDirect issues HTTP GETs against the target URL, so it is
	// an SSRF primitive. Restrict the scheme and reject private/loopback
	// addresses (same guard as provider BaseURLs).
	u, err := url.Parse(body.TargetURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeError(w, 400, "target_url must be a valid http/https URL")
		return
	}
	if isLocalOrPrivateIP(u.Hostname()) {
		writeError(w, 400, "target_url must not point to a private or loopback address")
		return
	}

	ok := natMgr.ProbeDirect(body.NodeID, body.TargetURL)
	natMgr.mu.RLock()
	result, _ := natMgr.probeCache[body.NodeID]
	natMgr.mu.RUnlock()

	writeJSON(w, 200, map[string]any{
		"success":    ok,
		"direct_ok":  result.DirectOK,
		"latency_ms": result.LatencyMS,
		"probed_at":  result.ProbedAt.Format(time.RFC3339),
	})
}

// UDPConn returns the shared UDP socket (nil if bind failed). Callers use it
// for hole-punching so the source port matches the advertised reflexive addr.
func (n *NATManager) UDPConn() *net.UDPConn { return n.udpConn }

// LocalUDP returns the bound "ip:port" of the shared UDP socket.
func (n *NATManager) LocalUDP() string { return n.localUDP }

// stunQueryOnConn performs a STUN binding request over the shared UDP socket so
// the source port matches the one peers will punch against. The STUN response
// is surfaced by udpRecvLoop — the SINGLE reader of the socket — on stunCh and
// consumed here (PERF-P3-24); this function never reads the socket directly, so
// the two-reader race (SetReadDeadline interference, datagram stealing) is gone.
func (n *NATManager) stunQueryOnConn(serverAddr string) (string, error) {
	if n.udpConn == nil {
		return "", fmt.Errorf("udp socket unavailable")
	}
	host := strings.TrimPrefix(serverAddr, "stun:")
	srv, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return "", fmt.Errorf("resolve STUN server: %w", err)
	}
	req := []byte{
		0x00, 0x01, 0x00, 0x00, // Binding Request, length 0
		0x21, 0x12, 0xA4, 0x42, // Magic Cookie
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // Transaction ID
	}
	if _, err := n.udpConn.WriteToUDP(req, srv); err != nil {
		return "", fmt.Errorf("write STUN request: %w", err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case addr := <-n.stunCh:
			return addr, nil
		case <-deadline.C:
			return "", fmt.Errorf("read STUN response: timeout")
		}
	}
}

// udpRecvLoop is the SINGLE reader of the shared UDP socket. It multiplexes
// inbound datagrams: hole-punch frames are handed to the DirectLinkManager,
// STUN responses are surfaced on stunCh for stunQueryOnConn. A single reader
// avoids concurrent ReadFromUDP races between STUN polling and punching.
func (n *NATManager) udpRecvLoop() {
	buf := make([]byte, 1500)
	for {
		_ = n.udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		nn, addr, err := n.udpConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		if offer, derr := DecodePunchOffer(buf[:nn]); derr == nil {
			if directLinkMgr != nil {
				directLinkMgr.Ingest(offer, addr)
			}
			continue
		}
		if a, perr := parseSTUNResponse(buf[:nn]); perr == nil {
			select {
			case n.stunCh <- a:
			default:
			}
		}
	}
}

// ensureDirectLinkMgr lazily wires the DirectLinkManager onto the shared UDP
// socket once netMgr (which supplies the node id) is available.
func ensureDirectLinkMgr() {
	if directLinkMgr != nil || natMgr == nil || natMgr.udpConn == nil {
		return
	}
	if netMgr == nil {
		return
	}
	directLinkMgr = NewDirectLinkManager(natMgr.udpConn, netMgr.GetNodeID(), natMgr.localUDP, natMgr.publicAddr, false)
}

func init() {
	// Register the NAT status and probe endpoints when routes are set up.
	// These are added via routes.go to avoid import cycles.
}

// RegisterNATRoutes adds NAT traversal API endpoints to the mux.
func RegisterNATRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nat/status", withAuth(handleNATStatus))
	mux.HandleFunc("POST /api/nat/probe", withAuth(handleDirectProbe))
}
