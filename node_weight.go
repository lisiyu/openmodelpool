package main

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"
)

// ============================================================
// Per-Node Weight Configuration & Approval Workflow
// ============================================================
//
// Node owners declare their token budget.
// Consumers can set per-node weight multipliers to prioritize specific nodes.
// When a consumer prioritizes another node, the node owner can configure
// auto-approve or manual-approve mode.

// NodeWeightOverride stores a per-node weight multiplier set by the local admin.
type NodeWeightOverride struct {
	NodeID    string  `json:"node_id"`
	Weight    float64 `json:"weight"`     // multiplier: 1.0 = normal, 2.0 = double priority, 0 = excluded
	Approved  bool    `json:"approved"`   // whether the node owner approved this priority
	UpdatedAt string  `json:"updated_at"`
}

// ApprovalRequest represents a pending approval from consumer → node owner.
type ApprovalRequest struct {
	ID          string `json:"id"`
	FromNodeID  string `json:"from_node_id"`
	ToNodeID    string `json:"to_node_id"`
	Weight      float64 `json:"weight"`
	Status      string  `json:"status"` // pending, approved, rejected
	CreatedAt   string `json:"created_at"`
	ResolvedAt  string `json:"resolved_at,omitempty"`
}

// nodeWeightManager handles per-node weights and approvals.
type nodeWeightManager struct {
	mu           sync.RWMutex
	overrides    map[string]*NodeWeightOverride   // nodeID → override
	pending      map[string]*ApprovalRequest       // requestID → request
	approvalMode string                            // "auto" or "manual"
	ownTokenBudget int64                           // this node's declared monthly token budget
	dataDir      string
}

var nwm *nodeWeightManager

func initNodeWeightManager(dataDir string) {
	m := &nodeWeightManager{
		overrides:    make(map[string]*NodeWeightOverride),
		pending:      make(map[string]*ApprovalRequest),
		approvalMode: cfg.Get("node_approval_mode", "auto"),
		ownTokenBudget: 0,
		dataDir:      dataDir,
	}

	// Load token budget
	if v := cfg.Get("node_token_budget", "0"); v != "" && v != "0" {
		var budget int64
		json.Unmarshal([]byte(v), &budget)
		m.ownTokenBudget = budget
	}

	// Load overrides
	if err := m.load(); err != nil {
		slog.Debug("no node weight overrides found", "error", err)
	}

	nwm = m
	slog.Info("node weight manager initialized",
		"overrides", len(m.overrides),
		"approval_mode", m.approvalMode,
		"token_budget", m.ownTokenBudget)
}

// GetWeightMultiplier returns the weight multiplier for a given node.
// Returns 1.0 if no override, or the configured multiplier.
// Unapproved overrides are treated as 1.0 unless approval mode is "auto".
func (m *nodeWeightManager) GetWeightMultiplier(nodeID string) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Own node always gets default priority boost
	if node != nil && nodeID == node.NodeID() {
		return 1.5 // 50% default boost for own node
	}

	if o, ok := m.overrides[nodeID]; ok {
		if m.approvalMode == "auto" || o.Approved {
			return o.Weight
		}
	}
	return 1.0
}

// SetOverride sets a per-node weight multiplier.
func (m *nodeWeightManager) SetOverride(nodeID string, weight float64) *ApprovalRequest {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if this is for own node
	isOwn := node != nil && nodeID == node.NodeID()

	o := &NodeWeightOverride{
		NodeID:    nodeID,
		Weight:    weight,
		Approved:  isOwn || m.approvalMode == "auto",
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	m.overrides[nodeID] = o
	m.save()

	var req *ApprovalRequest
	if !isOwn && m.approvalMode == "manual" {
		// Create approval request
		reqID := generateReqID()
		req = &ApprovalRequest{
			ID:         reqID,
			FromNodeID: node.NodeID(),
			ToNodeID:   nodeID,
			Weight:     weight,
			Status:     "pending",
			CreatedAt:  time.Now().Format(time.RFC3339),
		}
		m.pending[reqID] = req

		slog.Info("approval request created",
			"request_id", reqID,
			"from", req.FromNodeID,
			"to", nodeID,
			"weight", weight)
	}

	return req
}

// ResolveApproval approves or rejects a pending request.
func (m *nodeWeightManager) ResolveApproval(requestID string, approve bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.pending[requestID]
	if !ok {
		return errNotFound
	}
	if req.Status != "pending" {
		return errAlreadyResolved
	}

	if approve {
		req.Status = "approved"
		// Update the override
		if o, ok := m.overrides[req.ToNodeID]; ok {
			o.Approved = true
			o.UpdatedAt = time.Now().Format(time.RFC3339)
		}
	} else {
		req.Status = "rejected"
		// Remove the override
		delete(m.overrides, req.ToNodeID)
	}
	req.ResolvedAt = time.Now().Format(time.RFC3339)
	m.save()

	return nil
}

// GetOverrides returns all weight overrides.
func (m *nodeWeightManager) GetOverrides() []*NodeWeightOverride {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*NodeWeightOverride
	for _, o := range m.overrides {
		result = append(result, o)
	}
	return result
}

// GetPendingRequests returns all pending approval requests.
func (m *nodeWeightManager) GetPendingRequests() []*ApprovalRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*ApprovalRequest
	for _, r := range m.pending {
		if r.Status == "pending" {
			result = append(result, r)
		}
	}
	return result
}

// GetAllRequests returns all approval requests.
func (m *nodeWeightManager) GetAllRequests() []*ApprovalRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*ApprovalRequest
	for _, r := range m.pending {
		result = append(result, r)
	}
	return result
}

// GetApprovalMode returns the current approval mode.
func (m *nodeWeightManager) GetApprovalMode() string {
	return m.approvalMode
}

// SetApprovalMode updates the approval mode.
func (m *nodeWeightManager) SetApprovalMode(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.approvalMode = mode
	cfg.Set("node_approval_mode", mode)
}

// SetTokenBudget updates this node's declared token budget.
func (m *nodeWeightManager) SetTokenBudget(budget int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ownTokenBudget = budget
	// Store as JSON number in config
	b, _ := json.Marshal(budget)
	cfg.Set("node_token_budget", string(b))

	// Update NodeInfo if available
	if node != nil && node.IsInitialized() {
		node.SetTokenBudget(budget)
	}
}

// GetTokenBudget returns this node's declared token budget.
func (m *nodeWeightManager) GetTokenBudget() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ownTokenBudget
}

// persist

type nodeWeightData struct {
	Overrides map[string]*NodeWeightOverride `json:"overrides"`
	Pending   map[string]*ApprovalRequest    `json:"pending"`
}

func (m *nodeWeightManager) save() {
	data := nodeWeightData{Overrides: m.overrides, Pending: m.pending}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(m.dataDir+"/node_weights.json", b, 0644)
}

func (m *nodeWeightManager) load() error {
	b, err := os.ReadFile(m.dataDir + "/node_weights.json")
	if err != nil {
		return err
	}
	var data nodeWeightData
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	if data.Overrides != nil {
		m.overrides = data.Overrides
	}
	if data.Pending != nil {
		m.pending = data.Pending
	}
	return nil
}

// helper

func generateReqID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "req-" + base58Encode(b)
}

var (
	errNotFound        = &simpleError{"request not found"}
	errAlreadyResolved = &simpleError{"request already resolved"}
)

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }
