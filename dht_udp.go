package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================================
// §7.7/7.8 (P1-1(ii): real UDP transport + production bridge)
// ============================================================================
//
// dht_networking.go defined the Kademlia RPC layer and a pluggable
// DHTTransport, but only shipped an in-memory transport (single-process
// verification). This file bridges a REAL transport onto that interface using
// only the Go standard library (UDP), and wires a local DHTNode into
// production startup in init.go.
//
// QUIC is intentionally NOT implemented: it requires an external dependency,
// which would violate the project's stdlib-only / zero-dependency rule. UDP
// covers the request/response discovery protocol; HTTP could be added later as
// another DHTTransport implementation without touching the protocol.
//
// Design: UDP is connectionless, so a single socket serves both directions.
// The read loop demultiplexes inbound datagrams:
//   - a response to an in-flight Send (matched by message ID) is delivered to
//     the waiting caller;
//   - anything else is treated as an inbound request and answered via
//     DHTNode.handle (PING/PONG, FIND_NODE, FIND_VALUE, STORE).
// This is additive and does NOT modify the existing federation/gossip code
// path. The federation's local f.dht routing table is a separate, read-only
// index and is left untouched.

// UDPDHTTransport is a DHTTransport backed by a UDP socket. It both answers
// inbound DHT RPCs for the local node and sends outbound requests to remote
// nodes.
type UDPDHTTransport struct {
	self *DHTNode
	conn *net.UDPConn

	mu      sync.Mutex
	pending map[string]chan DHTMessage

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup

	seq uint64
}

// NewUDPDHTTransport builds a transport that dispatches inbound requests to the
// given local node and sends outbound requests via conn.
func NewUDPDHTTransport(self *DHTNode, conn *net.UDPConn) *UDPDHTTransport {
	return &UDPDHTTransport{
		self:    self,
		conn:    conn,
		pending: make(map[string]chan DHTMessage),
		stopCh:  make(chan struct{}),
	}
}

// Start launches the read loop that answers inbound DHT requests.
func (t *UDPDHTTransport) Start() {
	t.wg.Add(1)
	go t.readLoop()
}

// readLoop receives datagrams and either routes a response to a waiting Send or
// answers an inbound request.
func (t *UDPDHTTransport) readLoop() {
	defer t.wg.Done()
	buf := make([]byte, 65535)
	for {
		select {
		case <-t.stopCh:
			return
		default:
		}
		n, raddr, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-t.stopCh:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			// Socket closed or other fatal read error: stop serving.
			return
		}
		var msg DHTMessage
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			slog.Debug("dht: ignoring unparseable datagram", "from", raddr, "error", err)
			continue
		}
		// Response to one of our in-flight requests?
		if ch, ok := t.takePending(msg.ID); ok {
			select {
			case ch <- msg:
			default:
			}
			continue
		}
		// Otherwise treat it as an inbound request and answer it.
		resp := t.self.handle(msg)
		out, err := json.Marshal(resp)
		if err != nil {
			slog.Debug("dht: failed to marshal response", "error", err)
			continue
		}
		if _, err := t.conn.WriteToUDP(out, raddr); err != nil {
			slog.Debug("dht: failed to write response", "to", raddr, "error", err)
		}
	}
}

// takePending removes and returns the waiting channel for a message ID.
func (t *UDPDHTTransport) takePending(id string) (chan DHTMessage, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ch, ok := t.pending[id]
	if ok {
		delete(t.pending, id)
	}
	return ch, ok
}

// Send transmits msg to addr and waits for the matching response. The message ID
// is assigned here if absent so responses can be correlated.
func (t *UDPDHTTransport) Send(ctx context.Context, addr string, msg DHTMessage) (DHTMessage, error) {
	if msg.ID == "" {
		msg.ID = fmt.Sprintf("%s-%d", t.self.idHex()[:16], atomic.AddUint64(&t.seq, 1))
	}
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return DHTMessage{}, fmt.Errorf("dht: resolve %s: %w", addr, err)
	}

	ch := make(chan DHTMessage, 1)
	t.mu.Lock()
	t.pending[msg.ID] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, msg.ID)
		t.mu.Unlock()
	}()

	out, err := json.Marshal(msg)
	if err != nil {
		return DHTMessage{}, fmt.Errorf("dht: marshal: %w", err)
	}
	if _, err := t.conn.WriteToUDP(out, raddr); err != nil {
		return DHTMessage{}, fmt.Errorf("dht: send to %s: %w", addr, err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return DHTMessage{}, ctx.Err()
	case <-t.stopCh:
		return DHTMessage{}, fmt.Errorf("dht: transport stopped while waiting for %s from %s", msg.Type, addr)
	case <-time.After(dhtTimeout):
		return DHTMessage{}, fmt.Errorf("dht: timeout waiting for %s from %s", msg.Type, addr)
	}
}

// Stop gracefully shuts down the read loop and closes the socket.
func (t *UDPDHTTransport) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopCh)
		_ = t.conn.Close()
	})
	t.wg.Wait()
}

// dhtNode and dhtTransport are the production-singleton instances, started in
// initAllNetwork and stopped in gracefulShutdown.
var (
	dhtNode     *DHTNode
	dhtTransport *UDPDHTTransport
)

// parseDHTSeeds splits a comma-separated seed list and drops empty entries.
func parseDHTSeeds(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// startDHTNode bridges the real UDP DHT transport into production. It runs only
// when the node has joined the shared network (network_enabled) and has a
// derived NodeID. The UDP socket is optional infrastructure for decentralized
// discovery; a bind failure logs and continues without crashing startup.
func startDHTNode() {
	if netMgr == nil || !netMgr.config.NetworkEnabled {
		return
	}
	if node == nil || node.NodeID() == "" {
		slog.Warn("dht: node identity not ready; skipping DHT bootstrap")
		return
	}
	if cfg.Get("dht_enabled", "true") == "false" {
		slog.Info("dht: disabled via dht_enabled=false")
		return
	}

	addr := cfg.Get("dht_listen_addr", ":19001")
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		slog.Error("dht: invalid dht_listen_addr; DHT discovery disabled", "addr", addr, "error", err)
		return
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		slog.Error("dht: failed to bind UDP socket; DHT discovery disabled", "addr", addr, "error", err)
		return
	}

	n := NewDHTNode(node.NodeID(), conn.LocalAddr().String(), nil)
	tp := NewUDPDHTTransport(n, conn)
	n.SetTransport(tp)
	tp.Start()
	dhtNode = n
	dhtTransport = tp

	seeds := parseDHTSeeds(cfg.Get("dht_seeds", ""))
	if len(seeds) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, s := range seeds {
			if err := n.Bootstrap(ctx, s); err != nil {
				slog.Warn("dht: bootstrap to seed failed", "seed", s, "error", err)
				continue
			}
			slog.Info("dht: bootstrapped from seed", "seed", s)
		}
	}
	slog.Info("dht: node listening for discovery", "id", n.idHex()[:16], "addr", conn.LocalAddr().String(), "seeds", len(seeds))
}

// stopDHTNode shuts down the production DHT node (nil-safe).
func stopDHTNode() {
	if dhtTransport != nil {
		dhtTransport.Stop()
	}
	dhtNode = nil
	dhtTransport = nil
}
