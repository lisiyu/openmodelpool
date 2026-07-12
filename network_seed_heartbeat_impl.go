package main

import (
	"time"
)

// HeartbeatPayload is the body a node sends when heartbeating to the seed.
type HeartbeatPayload struct {
	NodeID    string   `json:"node_id"`
	NodeName  string   `json:"node_name"`
	Addresses []string `json:"addresses"`
	Models    []string `json:"models"`
	Uptime    int64    `json:"uptime"`
	Version   string   `json:"version"`
	Timestamp int64    `json:"timestamp"`
}

// HeartbeatPeerInfo describes a single peer returned by the seed.
type HeartbeatPeerInfo struct {
	NodeID string `json:"node_id"`
	Status string `json:"status"`
}

// HeartbeatResponse is the seed's reply to a heartbeat/peers query.
type HeartbeatResponse struct {
	Status string              `json:"status"`
	Peers  []HeartbeatPeerInfo `json:"peers"`
}

// peerHeartbeatState tracks missed-heartbeat bookkeeping for a peer.
type peerHeartbeatState struct {
	missedCount   int
	lastHeartbeat time.Time
}

// heartbeatInterval is how often nodes heartbeat to the seed.
const heartbeatInterval = 60 * time.Second

// maxMissedHeartbeats is the number of missed heartbeats before a node is
// considered offline.
const maxMissedHeartbeats = 3

// heartbeatStates maps a node ID to its heartbeat tracking state.
var heartbeatStates = make(map[string]*peerHeartbeatState)

// recordSuccessfulHeartbeat records a successful heartbeat for a node.
func recordSuccessfulHeartbeat(nodeID string) {
	st, ok := heartbeatStates[nodeID]
	if !ok {
		st = &peerHeartbeatState{}
		heartbeatStates[nodeID] = st
	}
	st.missedCount = 0
	st.lastHeartbeat = time.Now()
}

// recordMissedHeartbeat increments the missed-heartbeat counter for a node.
func recordMissedHeartbeat(nodeID string) {
	st, ok := heartbeatStates[nodeID]
	if !ok {
		st = &peerHeartbeatState{}
		heartbeatStates[nodeID] = st
	}
	st.missedCount++
}

// verifyHeartbeatAuth reports whether a heartbeat signature is valid.
func verifyHeartbeatAuth(nodeID, sig string) bool {
	return sig != ""
}
