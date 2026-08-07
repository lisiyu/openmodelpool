package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
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
	}
	go natMgr.stunLoop()
	slog.Info("NAT traversal manager initialized")
}

// stunLoop periodically discovers the public address via STUN.
func (n *NATManager) stunLoop() {
	n.discoverPublicAddr()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		n.discoverPublicAddr()
	}
}

// discoverPublicAddr attempts STUN binding to learn the public address.
func (n *NATManager) discoverPublicAddr() {
	for _, server := range n.stunServers {
		addr, err := stunQuery(server)
		if err != nil {
			slog.Debug("STUN query failed", "server", server, "error", err)
			continue
		}
		n.mu.Lock()
		n.publicAddr = addr
		n.natType = "unknown"
		n.mu.Unlock()
		slog.Info("STUN discovered public address", "addr", addr)
		return
	}
}

// stunQuery performs a simple STUN binding request over UDP and extracts
// the XOR-MAPPED-ADDRESS from the response.
func stunQuery(serverAddr string) (string, error) {
	host := strings.TrimPrefix(serverAddr, "stun:")
	addr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		return "", fmt.Errorf("resolve STUN server: %w", err)
	}

	conn, err := net.DialTimeout("udp", addr.String(), 3*time.Second)
	if err != nil {
		return "", fmt.Errorf("dial STUN server: %w", err)
	}
	defer conn.Close()

	// RFC 5389 Binding Request (magic cookie)
	req := []byte{
		0x00, 0x01, // Type: Binding Request
		0x00, 0x00, // Length: 0
		0x21, 0x12, 0xA4, 0x42, // Magic Cookie
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // Transaction ID
	}

	_, err = conn.Write(req)
	if err != nil {
		return "", fmt.Errorf("write STUN request: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 576)
	n, err := conn.Read(buf)
	if err != nil {
		return "", fmt.Errorf("read STUN response: %w", err)
	}

	if n < 28 || buf[0] != 0x01 || buf[1] != 0x01 {
		return "", fmt.Errorf("invalid STUN response")
	}

	// Parse XOR-MAPPED-ADDRESS attribute
	for i := 20; i+4 <= n; {
		attrType := uint16(buf[i])<<8 | uint16(buf[i+1])
		attrLen := uint16(buf[i+2])<<8 | uint16(buf[i+3])
		if attrType == 0x0020 { // XOR-MAPPED-ADDRESS
			if int(attrLen) < 8 || i+8 > n {
				break
			}
			family := buf[i+5]
			xorPort := uint16(buf[i+6])<<8 | uint16(buf[i+7])
			port := xorPort ^ 0x2112
			if family == 0x01 { // IPv4
				xorIP := uint32(buf[i+8])<<24 | uint32(buf[i+9])<<16 | uint32(buf[i+10])<<8 | uint32(buf[i+11])
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

func init() {
	// Register the NAT status and probe endpoints when routes are set up.
	// These are added via routes.go to avoid import cycles.
}

// RegisterNATRoutes adds NAT traversal API endpoints to the mux.
func RegisterNATRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nat/status", withAuth(handleNATStatus))
	mux.HandleFunc("POST /api/nat/probe", withAuth(handleDirectProbe))
}
