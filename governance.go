package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// NetworkGovernanceEventType enumerates governance event types.
type NetworkGovernanceEventType string

const (
	NetworkGovernanceEventNodeAdmit      NetworkGovernanceEventType = "node_admit"
	NetworkGovernanceEventNodeSuspend    NetworkGovernanceEventType = "node_suspend"
	NetworkGovernanceEventNodeReinstate  NetworkGovernanceEventType = "node_reinstate"
	NetworkGovernanceEventParamChange    NetworkGovernanceEventType = "param_change"
	NetworkGovernanceEventDisputeOpen    NetworkGovernanceEventType = "dispute_open"
	NetworkGovernanceEventDisputeResolve NetworkGovernanceEventType = "dispute_resolve"
)

// NetworkGovernanceEvent represents a governance action in the three-layer model.
type NetworkGovernanceEvent struct {
	ID        string                     `json:"id"`
	Type      NetworkGovernanceEventType `json:"type"`
	Proposer  string                     `json:"proposer"`
	Subject   string                     `json:"subject"`
	Reason    string                     `json:"reason"`
	Timestamp time.Time                  `json:"timestamp"`
	Signers   []string                   `json:"signers"`
	Data      any                        `json:"data,omitempty"`
}

// MultiSigThreshold returns the minimum number of distinct node signatures
// required for a governance event to be confirmed (2/3+1).
func MultiSigThreshold(totalNodes int) int {
	if totalNodes <= 3 {
		return totalNodes
	}
	return totalNodes*2/3 + 1
}

// GovernanceManager handles the three-layer governance model.
// Constitution: GitHub registry, Operations: Gossip, Data: Contribution ledger.
type GovernanceManager struct {
	mu     sync.RWMutex
	events map[string]*NetworkGovernanceEvent
}

func initGovernanceManager() {
	governanceMgr = &GovernanceManager{
		events: make(map[string]*NetworkGovernanceEvent),
	}
	slog.Info("governance manager initialized (three-layer model: GitHub + Gossip + Ledger)")
}

func (gm *GovernanceManager) ProposeEvent(eventType NetworkGovernanceEventType, proposer, subject, reason string, data any) *NetworkGovernanceEvent {
	event := &NetworkGovernanceEvent{
		ID:        fmt.Sprintf("gov-%d", time.Now().UnixNano()),
		Type:      eventType,
		Proposer:  proposer,
		Subject:   subject,
		Reason:    reason,
		Timestamp: time.Now().UTC(),
		Signers:   []string{proposer},
		Data:      data,
	}

	gm.mu.Lock()
	gm.events[event.ID] = event
	gm.mu.Unlock()

	slog.Info("governance event proposed", "id", event.ID, "type", string(eventType), "proposer", proposer, "subject", subject)
	return event
}

func (gm *GovernanceManager) SignEvent(eventID, signerNodeID string, signature string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	event, ok := gm.events[eventID]
	if !ok {
		return fmt.Errorf("event not found: %s", eventID)
	}

	for _, s := range event.Signers {
		if s == signerNodeID {
			return fmt.Errorf("node already signed: %s", signerNodeID)
		}
	}

	if fed != nil {
		if n, ok := fed.GetNode(signerNodeID); ok && n.PubKey != "" {
			payload, _ := json.Marshal(map[string]any{
				"event_id": eventID,
				"signer":   signerNodeID,
				"ts":       time.Now().Unix(),
			})
			if !VerifySignature(n.PubKey, payload, signature) {
				return fmt.Errorf("signature verification failed for node: %s", signerNodeID)
			}
		}
	}

	event.Signers = append(event.Signers, signerNodeID)

	totalNodes := 1
	if fed != nil {
		totalNodes = len(fed.GetTrustPool().Nodes)
	}
	threshold := MultiSigThreshold(totalNodes)

	if len(event.Signers) >= threshold {
		slog.Info("governance event confirmed", "id", eventID, "signers", len(event.Signers), "threshold", threshold)
	} else {
		slog.Info("governance event signed", "id", eventID, "signer", signerNodeID, "progress", fmt.Sprintf("%d/%d", len(event.Signers), threshold))
	}

	return nil
}

func (gm *GovernanceManager) IsConfirmed(eventID string) bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	event, ok := gm.events[eventID]
	if !ok {
		return false
	}

	totalNodes := 1
	if fed != nil {
		totalNodes = len(fed.GetTrustPool().Nodes)
	}
	threshold := MultiSigThreshold(totalNodes)

	return len(event.Signers) >= threshold
}

func (gm *GovernanceManager) GetEvent(eventID string) (*NetworkGovernanceEvent, bool) {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	e, ok := gm.events[eventID]
	return e, ok
}

func (gm *GovernanceManager) ListEvents() []*NetworkGovernanceEvent {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	var result []*NetworkGovernanceEvent
	for _, e := range gm.events {
		result = append(result, e)
	}
	return result
}
