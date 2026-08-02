package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// MajorEvent represents a significant network event that should be persisted
// to Layer 2 (IPFS/blockchain) for tamper-proof record keeping.
// v4 design §9.6: events exceeding $100 contribution, node bans, dispute resolutions.
type MajorEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
	Signature string    `json:"signature,omitempty"`
}

// BlockchainLedger is the interface for Layer 2 persistence.
// v4 design §9.8: IPFS for major events, blockchain for dispute resolution.
type BlockchainLedger interface {
	SubmitMajorEvent(event *MajorEvent) (txHash string, err error)
	QueryEvent(txHash string) (*MajorEvent, error)
	QueryPeerHistory(peerID string) ([]*MajorEvent, error)
	ResolveDispute(dispute *Dispute) (string, error)
}

// Dispute represents a network dispute requiring resolution.
type Dispute struct {
	ID           string    `json:"id"`
	Complainant  string    `json:"complainant"`
	Respondent   string    `json:"respondent"`
	Type         string    `json:"type"`
	Description  string    `json:"description"`
	Evidence     []string  `json:"evidence"`
	Timestamp    time.Time `json:"timestamp"`
	Resolution   string    `json:"resolution,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy   []string  `json:"resolved_by,omitempty"`
}

// IPFSLedger implements BlockchainLedger using IPFS for persistence.
// Phase 1: local JSON file storage with IPFS gateway upload stub.
// Phase 3: full IPFS pinning + content addressing.
type IPFSLedger struct {
	mu     sync.RWMutex
	events map[string]*MajorEvent
	path   string
}

// NewIPFSLedger creates a new IPFS-backed ledger.
func NewIPFSLedger(dataDir string) *IPFSLedger {
	return &IPFSLedger{
		events: make(map[string]*MajorEvent),
		path:   dataDir,
	}
}

func (l *IPFSLedger) SubmitMajorEvent(event *MajorEvent) (string, error) {
	if event.ID == "" {
		event.ID = fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	l.mu.Lock()
	l.events[event.ID] = event
	l.mu.Unlock()

	if err := l.persist(); err != nil {
		slog.Warn("failed to persist major event to IPFS ledger", "event_id", event.ID, "error", err)
	}

	slog.Info("major event submitted to IPFS ledger", "event_id", event.ID, "type", event.Type)
	return event.ID, nil
}

func (l *IPFSLedger) QueryEvent(txHash string) (*MajorEvent, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	event, ok := l.events[txHash]
	if !ok {
		return nil, fmt.Errorf("event not found: %s", txHash)
	}
	return event, nil
}

func (l *IPFSLedger) QueryPeerHistory(peerID string) ([]*MajorEvent, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var results []*MajorEvent
	for _, e := range l.events {
		if e.NodeID == peerID {
			results = append(results, e)
		}
	}
	return results, nil
}

func (l *IPFSLedger) ResolveDispute(dispute *Dispute) (string, error) {
	event := &MajorEvent{
		ID:        fmt.Sprintf("dispute-%d", time.Now().UnixNano()),
		Type:      "dispute_resolution",
		NodeID:    dispute.Respondent,
		Timestamp: time.Now().UTC(),
		Data:      dispute,
	}
	return l.SubmitMajorEvent(event)
}

func (l *IPFSLedger) persist() error {
	l.mu.RLock()
	data, err := json.MarshalIndent(l.events, "", "  ")
	l.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(l.path, data, 0600)
}
