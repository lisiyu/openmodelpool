package main

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"
)

// directLinkMgr is the process-wide manager wired onto the shared UDP socket
// by ensureDirectLinkMgr (called from initNATManager). It is nil when the UDP
// socket failed to bind, in which case punching is disabled and relay is used.
var directLinkMgr *DirectLinkManager

// PunchSession holds the state of a single hole-punch attempt: the local and
// peer offers, whether the direct channel was established, and the peer's
// direct address once it is. It is a pure state machine — actual send/receive
// is driven by DirectLinkManager so that one shared UDP socket can multiplex
// punches to many peers without read races.
type PunchSession struct {
	OurOffer  PunchOffer
	PeerOffer PunchOffer

	mu          sync.Mutex
	established bool
	directAddr  *net.UDPAddr

	done chan struct{}
	once sync.Once
}

// NewPunchSession constructs a punch session for an (our, peer) offer pair.
func NewPunchSession(our, peer PunchOffer) *PunchSession {
	return &PunchSession{
		OurOffer:  our,
		PeerOffer: peer,
		done:      make(chan struct{}),
	}
}

// Established reports whether the direct channel has been built.
func (s *PunchSession) Established() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.established
}

// DirectAddr returns the peer's direct address (valid after establishment).
func (s *PunchSession) DirectAddr() *net.UDPAddr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.directAddr
}

func (s *PunchSession) markEstablished(addr *net.UDPAddr) {
	s.mu.Lock()
	if s.established {
		s.mu.Unlock()
		return
	}
	s.established = true
	s.directAddr = addr
	s.mu.Unlock()
	s.once.Do(func() { close(s.done) })
}

// DirectLinkManager builds UDP direct channels between this node and its peers.
// It owns the sending goroutines for each punch and, when running in shared
// mode, receives frames via Ingest (called by NATManager.udpRecvLoop, the
// single reader of the shared socket). In tests it can own its own recv loop.
type DirectLinkManager struct {
	mu        sync.RWMutex
	conn      *net.UDPConn
	nodeID    string
	pubAddr   string
	localAddr string

	sessions map[string]*PunchSession   // peerNodeID -> in-progress punch
	links    map[string]*net.UDPAddr    // peerNodeID -> established direct addr

	ctx    context.Context
	cancel context.CancelFunc
	ownRecv bool
}

// NewDirectLinkManager creates a manager. When ownRecv is true the manager
// runs its own receive loop (used in tests and when no NATManager multiplexes
// the socket); when false, frames must be delivered via Ingest.
func NewDirectLinkManager(conn *net.UDPConn, nodeID, localAddr, pubAddr string, ownRecv bool) *DirectLinkManager {
	ctx, cancel := context.WithCancel(context.Background())
	d := &DirectLinkManager{
		conn:      conn,
		nodeID:    nodeID,
		localAddr: localAddr,
		pubAddr:   pubAddr,
		sessions:  make(map[string]*PunchSession),
		links:     make(map[string]*net.UDPAddr),
		ctx:       ctx,
		cancel:    cancel,
		ownRecv:   ownRecv,
	}
	if ownRecv && conn != nil {
		go d.recvLoop()
	}
	return d
}

// Offer builds a punch offer for this node (reflexive = STUN public addr,
// local = the UDP listen address peers punch against).
func (d *DirectLinkManager) Offer() (PunchOffer, error) {
	return NewPunchOffer(d.nodeID, d.pubAddr, d.localAddr)
}

// SetPubAddr updates the advertised reflexive address (called after STUN).
func (d *DirectLinkManager) SetPubAddr(addr string) {
	d.mu.Lock()
	d.pubAddr = addr
	d.mu.Unlock()
}

// BeginPunch starts a punch toward a peer whose offer we received. It registers
// a session and fires a sender goroutine; the matching inbound punch frame
// (delivered via Ingest) marks the channel established.
func (d *DirectLinkManager) BeginPunch(peer PunchOffer, interval time.Duration, maxAttempts int) *PunchSession {
	if d.conn == nil {
		return nil
	}
	our, err := d.Offer()
	if err != nil {
		slog.Warn("punch offer build failed", "error", err)
		return nil
	}
	s := NewPunchSession(our, peer)

	d.mu.Lock()
	d.sessions[peer.NodeID] = s
	d.mu.Unlock()

	target, err := ParseUDPAddr(peer.ReflexiveAddr)
	if err != nil {
		slog.Warn("punch target invalid", "addr", peer.ReflexiveAddr, "error", err)
		return s
	}
	frame, err := EncodePunchOffer(our)
	if err != nil {
		slog.Warn("punch frame encode failed", "error", err)
		return s
	}

	go func() {
		defer func() {
			d.mu.Lock()
			if s.Established() {
				d.links[peer.NodeID] = s.DirectAddr()
			}
			delete(d.sessions, peer.NodeID)
			d.mu.Unlock()
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Keep punching for the whole window. We must NOT stop when we receive
		// the peer's first frame: NAT hole-punching needs both sides to keep
		// sending so each side's mapping opens. The peer may not have received
		// ANY of our packets yet when its frame first arrives, so stopping early
		// would strand it. s.done is only a readiness signal, not a send stop.
		for i := 0; i < maxAttempts; i++ {
			select {
			case <-d.ctx.Done():
				return
			default:
			}
			_, _ = d.conn.WriteToUDP(frame, target)
			select {
			case <-d.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

// Ingest is called with a decoded punch frame and the sender's address. It
// marks the matching peer session established (idempotent per session) and
// records the direct address immediately so HasDirect reflects the channel as
// soon as it opens (rather than only after the send goroutine exits).
func (d *DirectLinkManager) Ingest(offer PunchOffer, addr *net.UDPAddr) {
	d.mu.RLock()
	s, ok := d.sessions[offer.NodeID]
	d.mu.RUnlock()
	if ok {
		s.markEstablished(addr)
		d.mu.Lock()
		d.links[offer.NodeID] = addr
		d.mu.Unlock()
	}
}

// recvLoop is the manager-owned receiver (ownRecv mode, e.g. tests).
func (d *DirectLinkManager) recvLoop() {
	buf := make([]byte, 1500)
	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}
		_ = d.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-d.ctx.Done():
				return
			default:
			}
			continue
		}
		offer, derr := DecodePunchOffer(buf[:n])
		if derr != nil {
			continue
		}
		d.Ingest(offer, addr)
	}
}

// HasDirect reports whether a direct channel to the peer is established.
func (d *DirectLinkManager) HasDirect(nodeID string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.links[nodeID]
	return ok
}

// DirectAddr returns the peer's direct address (valid after establishment).
func (d *DirectLinkManager) DirectAddr(nodeID string) *net.UDPAddr {
	d.mu.RLock()
	defer d.mu.RUnlock()
	a, _ := d.links[nodeID]
	return a
}

// Stop cancels all punches. It does NOT close conn — the socket lifetime is
// owned by NATManager (or the test, which closes its own).
func (d *DirectLinkManager) Stop() {
	d.cancel()
}
