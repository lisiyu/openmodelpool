package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
)

// ============================================================================
// §7.7/7.8: Kademlia 256-bit DHT — real network I/O (P1-1)
// ============================================================================
//
// The in-memory K-Bucket routing table in dht_kademlia.go was previously
// inert: nodes could only be added via AddNode, with no message exchange.
// This file adds the actual Kademlia RPC layer on top of that table:
//
//   - DHTMessage: PING/PONG, FIND_NODE, FIND_VALUE, STORE
//   - DHTTransport: a pluggable delivery abstraction (in-memory for tests,
//     real UDP/HTTP can be added later without touching the protocol)
//   - DHTNode: wraps a *DHT routing table and speaks the protocol, including
//     iterative lookup (the core Kademlia algorithm)
//   - InMemoryDHTNetwork: routes messages between DHTNodes inside one process,
//     used for single-process multi-node verification (the P1-1 first milestone)
//
// This is additive and does NOT modify the existing federation/gossip code
// path. It is the foundation that a future real transport (UDP/QUIC/HTTP)
// can be bridged onto.

// DHTMsgType enumerates the Kademlia RPC message types.
type DHTMsgType string

const (
	DHTMsgPing          DHTMsgType = "PING"
	DHTMsgPong          DHTMsgType = "PONG"
	DHTMsgFindNode      DHTMsgType = "FIND_NODE"
	DHTMsgFindNodeResp  DHTMsgType = "FIND_NODE_RESP"
	DHTMsgFindValue     DHTMsgType = "FIND_VALUE"
	DHTMsgFindValueResp DHTMsgType = "FIND_VALUE_RESP"
	DHTMsgStore         DHTMsgType = "STORE"
	DHTMsgStoreAck      DHTMsgType = "STORE_ACK"
)

// DHTMessage is a single Kademlia RPC message.
type DHTMessage struct {
	ID       string      `json:"id"`
	From     DHTNodeID   `json:"from"`
	FromAddr string      `json:"from_addr"`
	Type     DHTMsgType  `json:"type"`
	Target   DHTNodeID   `json:"target,omitempty"` // lookup target (node ID or sha256(key))
	Key      string      `json:"key,omitempty"`    // record key (STORE / FIND_VALUE)
	Value    []byte      `json:"value,omitempty"`  // record value (STORE / FIND_VALUE_RESP)
	Entries  []*DHTEntry `json:"entries,omitempty"` // closest nodes (FIND_*_RESP)
	Found    bool        `json:"found,omitempty"`   // FIND_VALUE_RESP: value present locally
}

// DHTTransport delivers a message to a remote node identified by its address
// and returns that node's response. Implementations may be in-memory (tests)
// or a real network transport (future work).
type DHTTransport interface {
	Send(ctx context.Context, addr string, msg DHTMessage) (DHTMessage, error)
}

// DHTNode is a single Kademlia node: a routing table plus the RPC protocol.
type DHTNode struct {
	id   DHTNodeID
	addr string
	dht  *DHT
	net  DHTTransport

	mu       sync.RWMutex
	addrBook map[DHTNodeID]string // learned nodeID -> address for routing
}

// NewDHTNode creates a Kademlia node bound to a transport under the given
// address. selfID is any stable string used to derive the 256-bit node ID.
func NewDHTNode(selfID, addr string, net DHTTransport) *DHTNode {
	d := NewDHT(selfID)
	return &DHTNode{
		id:       d.self,
		addr:     addr,
		dht:      d,
		net:      net,
		addrBook: make(map[DHTNodeID]string),
	}
}

// ID returns the node's 256-bit identifier.
func (n *DHTNode) ID() DHTNodeID { return n.id }

// idHex returns the hex encoding of the node ID (used for log lines and for
// deriving unique outbound message IDs in the UDP transport).
func (n *DHTNode) idHex() string { return hex.EncodeToString(n.id[:]) }

// Addr returns the node's address.
func (n *DHTNode) Addr() string { return n.addr }

// SetTransport attaches the delivery transport after construction. It exists so
// a node can be created before its transport is built (the transport needs the
// node reference to dispatch inbound requests, creating a construction cycle).
func (n *DHTNode) SetTransport(t DHTTransport) { n.net = t }

// TableSize returns the number of nodes currently in the routing table.
func (n *DHTNode) TableSize() int { return n.dht.TotalNodes() }

// learn records a peer's address and inserts it into the routing table.
// Self and entries without an address are ignored.
func (n *DHTNode) learn(e *DHTEntry) {
	if e == nil || e.NodeID == n.id || len(e.NodeID) == 0 {
		return
	}
	if len(e.Addresses) > 0 {
		n.mu.Lock()
		n.addrBook[e.NodeID] = e.Addresses[0]
		n.mu.Unlock()
	}
	n.dht.AddNode(e)
}

// addrOf resolves a known node ID to its address.
func (n *DHTNode) addrOf(id DHTNodeID) (string, bool) {
	n.mu.RLock()
	a, ok := n.addrBook[id]
	n.mu.RUnlock()
	return a, ok
}

func (n *DHTNode) selfEntry() *DHTEntry {
	return &DHTEntry{NodeID: n.id, Addresses: []string{n.addr}}
}

// handle processes an inbound message and produces the response. It is pure
// local computation: no transport calls happen here, so it is safe to invoke
// synchronously from a transport's Send.
func (n *DHTNode) handle(msg DHTMessage) DHTMessage {
	// Learn the requester so routing tables stay bidirectional.
	if msg.From != n.id && len(msg.FromAddr) > 0 {
		n.learn(&DHTEntry{NodeID: msg.From, Addresses: []string{msg.FromAddr}})
	}

	resp := DHTMessage{ID: msg.ID, From: n.id, FromAddr: n.addr, Type: respType(msg.Type)}
	switch msg.Type {
	case DHTMsgPing:
		// PONG carries no payload.
	case DHTMsgFindNode:
		closest := n.dht.FindClosest(msg.Target, dhtK)
		resp.Entries = append([]*DHTEntry{n.selfEntry()}, closest...)
	case DHTMsgFindValue:
		if rec, ok := n.dht.Get(msg.Key); ok {
			resp.Found = true
			resp.Value = rec.Value
		} else {
			closest := n.dht.FindClosest(msg.Target, dhtK)
			resp.Entries = append([]*DHTEntry{n.selfEntry()}, closest...)
		}
	case DHTMsgStore:
		n.dht.Put(msg.Key, msg.Value, msg.From)
		// STORE_ACK.
	default:
		slog.Warn("dht: unknown message type", "type", msg.Type, "from", hex.EncodeToString(msg.From[:8]))
	}
	return resp
}

// respType maps a request message type to its response message type.
func respType(t DHTMsgType) DHTMsgType {
	switch t {
	case DHTMsgPing:
		return DHTMsgPong
	case DHTMsgFindNode:
		return DHTMsgFindNodeResp
	case DHTMsgFindValue:
		return DHTMsgFindValueResp
	case DHTMsgStore:
		return DHTMsgStoreAck
	default:
		return t
	}
}

// Bootstrap contacts a single known seed address and runs an iterative lookup
// for the node's own ID to populate the routing table.
func (n *DHTNode) Bootstrap(ctx context.Context, seedAddr string) error {
	resp, err := n.net.Send(ctx, seedAddr, DHTMessage{
		From:     n.id,
		FromAddr: n.addr,
		Type:     DHTMsgFindNode,
		Target:   n.id,
	})
	if err != nil {
		return fmt.Errorf("bootstrap to %s: %w", seedAddr, err)
	}
	for _, e := range resp.Entries {
		n.learn(e)
	}
	n.IterativeFindNode(n.id)
	return nil
}

// IterativeFindNode runs the Kademlia iterative lookup for target, querying
// the alpha closest known nodes and incorporating their responses until the
// k-closest set is fully queried. The routing table is populated as a side
// effect. Bounded by dhtBuckets rounds to guarantee termination.
func (n *DHTNode) IterativeFindNode(target DHTNodeID) {
	queried := make(map[string]bool)
	for round := 0; round < dhtBuckets; round++ {
		next := n.nextUnqueried(target, queried)
		if next == nil {
			return
		}
		queried[hex.EncodeToString(next.NodeID[:])] = true
		addr, _ := n.addrOf(next.NodeID)
		resp, err := n.net.Send(context.Background(), addr, DHTMessage{
			From:     n.id,
			FromAddr: n.addr,
			Type:     DHTMsgFindNode,
			Target:   target,
		})
		if err != nil {
			continue
		}
		for _, e := range resp.Entries {
			n.learn(e)
		}
	}
}

// IterativeFindValue locates the value stored under key by routing toward
// sha256(key). Returns the value and true if found; otherwise populates the
// routing table and returns (nil, false).
func (n *DHTNode) IterativeFindValue(ctx context.Context, key string) ([]byte, bool) {
	target := DHTNodeID(sha256.Sum256([]byte(key)))
	queried := make(map[string]bool)
	for round := 0; round < dhtBuckets; round++ {
		next := n.nextUnqueried(target, queried)
		if next == nil {
			return nil, false
		}
		queried[hex.EncodeToString(next.NodeID[:])] = true
		addr, _ := n.addrOf(next.NodeID)
		resp, err := n.net.Send(ctx, addr, DHTMessage{
			From:     n.id,
			FromAddr: n.addr,
			Type:     DHTMsgFindValue,
			Target:   target,
			Key:      key,
		})
		if err != nil {
			continue
		}
		if resp.Found {
			return resp.Value, true
		}
		for _, e := range resp.Entries {
			n.learn(e)
		}
	}
	return nil, false
}

// Store replicates a key/value onto the k closest nodes to sha256(key).
func (n *DHTNode) Store(ctx context.Context, key string, value []byte) error {
	target := DHTNodeID(sha256.Sum256([]byte(key)))
	n.IterativeFindNode(target)
	closest := n.dht.FindClosest(target, dhtK)

	stored := 0
	for _, e := range closest {
		if e.NodeID == n.id {
			continue
		}
		addr, ok := n.addrOf(e.NodeID)
		if !ok {
			continue
		}
		resp, err := n.net.Send(ctx, addr, DHTMessage{
			From:     n.id,
			FromAddr: n.addr,
			Type:     DHTMsgStore,
			Target:   target,
			Key:      key,
			Value:    value,
		})
		if err == nil && resp.Type == DHTMsgStoreAck {
			stored++
		}
	}
	if stored == 0 {
		return fmt.Errorf("store failed: no reachable nodes for key %q", key)
	}
	return nil
}

// nextUnqueried returns the closest known node to target that has not yet been
// queried and whose address we know, or nil if none remain.
func (n *DHTNode) nextUnqueried(target DHTNodeID, queried map[string]bool) *DHTEntry {
	closest := n.dht.FindClosest(target, dhtK)
	for _, e := range closest {
		if e.NodeID == n.id {
			continue
		}
		if queried[hex.EncodeToString(e.NodeID[:])] {
			continue
		}
		if _, ok := n.addrOf(e.NodeID); ok {
			return e
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// In-memory transport (single-process multi-node verification)
// ----------------------------------------------------------------------------

// InMemoryDHTNetwork routes DHTMessages between DHTNodes within one process.
// It is the P1-1 verification harness: spin up N nodes, register them, and
// prove multi-hop discovery/content routing without any real socket.
type InMemoryDHTNetwork struct {
	mu    sync.RWMutex
	nodes map[string]*DHTNode // addr -> node
}

// NewInMemoryDHTNetwork creates an empty in-process network.
func NewInMemoryDHTNetwork() *InMemoryDHTNetwork {
	return &InMemoryDHTNetwork{nodes: make(map[string]*DHTNode)}
}

// Register binds a node to its address so messages can be routed to it.
func (net *InMemoryDHTNetwork) Register(addr string, node *DHTNode) {
	net.mu.Lock()
	defer net.mu.Unlock()
	net.nodes[addr] = node
}

// Send delivers msg to the node registered at addr and returns its response.
func (net *InMemoryDHTNetwork) Send(ctx context.Context, addr string, msg DHTMessage) (DHTMessage, error) {
	net.mu.RLock()
	node, ok := net.nodes[addr]
	net.mu.RUnlock()
	if !ok {
		return DHTMessage{}, fmt.Errorf("dht: no node registered at %s", addr)
	}
	select {
	case <-ctx.Done():
		return DHTMessage{}, ctx.Err()
	default:
	}
	return node.handle(msg), nil
}
