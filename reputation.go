package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ============================================================
// Reputation Manager
// ============================================================

type ReputationManager struct {
	mu       sync.RWMutex
	scores   map[string]*NodeReputation // nodeID -> reputation data (persisted via save/load)
	myScores map[string]*PeerScore      // our scores of other nodes (persisted via save/load)
	dataDir  string
}

type NodeReputation struct {
	NodeID         string      `json:"node_id"`
	Availability   float64     `json:"availability"`   // 0-100, EWMA
	Latency        float64     `json:"latency"`        // 0-100, EWMA
	Accuracy       float64     `json:"accuracy"`       // 0-100, EWMA
	PeerScores     []PeerScore `json:"peer_scores"`    // scores from other nodes
	OverallScore   float64     `json:"overall_score"`  // weighted composite
	Grade          string      `json:"grade"`          // S/A/B/C/D
	LastUpdated    string      `json:"last_updated"`
	TotalRequests  int64       `json:"total_requests"`
	FailedRequests int64       `json:"failed_requests"`
	DGradeSince    string      `json:"d_grade_since,omitempty"` // when grade first became D
}

const repEwmaAlpha = 0.3

var repMgr *ReputationManager

func initReputation(dataDir string) {
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dataDir,
	}
	repMgr.load()
	slog.Info("reputation manager initialized", "nodes", len(repMgr.scores))
}

// ============================================================
// Recording metrics
// ============================================================

func (r *ReputationManager) RecordCall(nodeID string, success bool, latencyMS float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rep := r.getOrCreate(nodeID)
	rep.TotalRequests++
	if !success {
		rep.FailedRequests++
	}

	// Update availability EWMA
	availSample := 0.0
	if success {
		availSample = 100.0
	}
	rep.Availability = repEwmaAlpha*availSample + (1-repEwmaAlpha)*rep.Availability

	// Update latency EWMA (convert to 0-100 scale: 0ms=100 score, 5000ms+=0 score)
	latSample := 100.0 - (latencyMS / 50.0)
	if latSample < 0 {
		latSample = 0
	}
	if latSample > 100 {
		latSample = 100
	}
	rep.Latency = repEwmaAlpha*latSample + (1-repEwmaAlpha)*rep.Latency

	rep.LastUpdated = time.Now().UTC().Format(time.RFC3339)
	rep.OverallScore = r.calculateOverallScoreLocked(rep)
	rep.Grade = r.calculateGradeLocked(rep)

	r.save()
}

func (r *ReputationManager) RecordAccuracy(nodeID string, accurate bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rep := r.getOrCreate(nodeID)

	accSample := 0.0
	if accurate {
		accSample = 100.0
	}
	rep.Accuracy = repEwmaAlpha*accSample + (1-repEwmaAlpha)*rep.Accuracy

	rep.LastUpdated = time.Now().UTC().Format(time.RFC3339)
	rep.OverallScore = r.calculateOverallScoreLocked(rep)
	rep.Grade = r.calculateGradeLocked(rep)

	r.save()
}

// ============================================================
// Query
// ============================================================

func (r *ReputationManager) GetReputation(nodeID string) *NodeReputation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rep, ok := r.scores[nodeID]
	if !ok {
		return nil
	}
	cp := *rep
	return &cp
}

func (r *ReputationManager) GetAllReputations() map[string]*NodeReputation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]*NodeReputation, len(r.scores))
	for k, v := range r.scores {
		cp := *v
		result[k] = &cp
	}
	return result
}

// ============================================================
// Grading
// ============================================================

func (r *ReputationManager) CalculateGrade(rep *NodeReputation) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.calculateGradeLocked(rep)
}

func (r *ReputationManager) calculateGradeLocked(rep *NodeReputation) string {
	// §8.4: 0-100 scale grading thresholds
	score := rep.OverallScore
	switch {
	case score >= 95:
		return "S"
	case score >= 80:
		return "A"
	case score >= 60:
		return "B"
	case score >= 40:
		return "C"
	default:
		return "D"
	}
}

// ============================================================
// Peer scores
// ============================================================

func (r *ReputationManager) AddPeerScore(score PeerScore) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rep := r.getOrCreate(score.TargetNode)

	// Update or append
	found := false
	for i, ps := range rep.PeerScores {
		if ps.FromNode == score.FromNode {
			rep.PeerScores[i] = score
			found = true
			break
		}
	}
	if !found {
		rep.PeerScores = append(rep.PeerScores, score)
	}

	rep.LastUpdated = time.Now().UTC().Format(time.RFC3339)
	rep.OverallScore = r.calculateOverallScoreLocked(rep)
	rep.Grade = r.calculateGradeLocked(rep)

	r.save()
}

func (r *ReputationManager) GetOurScore(targetNodeID string) *PeerScore {
	r.mu.RLock()
	defer r.mu.RUnlock()
	score, ok := r.myScores[targetNodeID]
	if !ok {
		return nil
	}
	cp := *score
	return &cp
}

func (r *ReputationManager) SetOurScore(targetNodeID string, availability, latency, accuracy float64, comment string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	score := &PeerScore{
		FromNode:     node.NodeID(),
		TargetNode:   targetNodeID,
		Availability: availability,
		Latency:      latency,
		Accuracy:     accuracy,
		Comment:      comment,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Signature:    "",
	}
	// Sign the score using canonical payload format (must match handlePostScore verification)
	payload := fmt.Sprintf("%s:%s:%.0f:%.0f:%.0f:%s",
		score.FromNode, score.TargetNode,
		score.Availability, score.Latency, score.Accuracy,
		score.Timestamp)
	score.Signature = node.Sign([]byte(payload))

	r.myScores[targetNodeID] = score
	r.save()
}

// ============================================================
// Overall score calculation
// ============================================================

func (r *ReputationManager) CalculateOverallScore(rep *NodeReputation) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.calculateOverallScoreLocked(rep)
}

func (r *ReputationManager) calculateOverallScoreLocked(rep *NodeReputation) float64 {
	// Weighted: 40% availability + 30% latency + 20% accuracy + 10% peer consensus
	base := 0.4*rep.Availability + 0.3*rep.Latency + 0.2*rep.Accuracy

	// Peer consensus: average of peer scores' availability
	peerConsensus := 0.0
	if len(rep.PeerScores) > 0 {
		var sum float64
		for _, ps := range rep.PeerScores {
			sum += ps.Availability
		}
		peerConsensus = sum / float64(len(rep.PeerScores))
	}

	return base + 0.1*peerConsensus
}

// ============================================================
// Node removal check
// ============================================================

func (r *ReputationManager) ShouldRemoveNode(nodeID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rep, ok := r.scores[nodeID]
	if !ok {
		return false
	}
	if rep.Grade != "D" {
		return false
	}
	if rep.DGradeSince == "" {
		return false
	}
	since, err := time.Parse(time.RFC3339, rep.DGradeSince)
	if err != nil {
		return false
	}
	return time.Since(since) > 7*24*time.Hour
}

// ============================================================
// Periodic cleanup
// ============================================================

func (r *ReputationManager) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)

	for _, rep := range r.scores {
		rep.OverallScore = r.calculateOverallScoreLocked(rep)
		newGrade := r.calculateGradeLocked(rep)

		// Track D-grade duration
		if newGrade == "D" {
			if rep.Grade != "D" || rep.DGradeSince == "" {
				rep.DGradeSince = now
			}
		} else {
			rep.DGradeSince = ""
		}

		rep.Grade = newGrade
		rep.LastUpdated = now
	}

	r.save()
	slog.Info("reputation cleanup completed", "nodes", len(r.scores))
}

// ============================================================
// Persistence
// ============================================================

func (r *ReputationManager) save() {
	path := filepath.Join(r.dataDir, "reputation.json")

	data := struct {
		Scores   map[string]*NodeReputation `json:"scores"`
		MyScores map[string]*PeerScore      `json:"my_scores"`
	}{
		Scores:   r.scores,
		MyScores: r.myScores,
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		slog.Error("failed to marshal reputation data", "error", err)
		return
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		slog.Error("failed to write reputation file", "error", err)
	}
}

func (r *ReputationManager) load() {
	path := filepath.Join(r.dataDir, "reputation.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to read reputation file", "error", err)
		}
		return
	}

	var data struct {
		Scores   map[string]*NodeReputation `json:"scores"`
		MyScores map[string]*PeerScore      `json:"my_scores"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		slog.Error("failed to unmarshal reputation data", "error", err)
		return
	}

	if data.Scores != nil {
		r.scores = data.Scores
	}
	if data.MyScores != nil {
		r.myScores = data.MyScores
	}
}

func (r *ReputationManager) getOrCreate(nodeID string) *NodeReputation {
	rep, ok := r.scores[nodeID]
	if !ok {
		rep = &NodeReputation{
			NodeID:       nodeID,
			Availability: 50.0, // start at midpoint
			Latency:      50.0,
			Accuracy:     50.0,
			PeerScores:   []PeerScore{},
			Grade:        "C",
			LastUpdated:  time.Now().UTC().Format(time.RFC3339),
		}
		r.scores[nodeID] = rep
	}
	return rep
}

// ============================================================
// HTTP Handlers
// ============================================================

// handleGetReputations returns all reputation data.
// GET /federation/reputations
func handleGetReputations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}

	reps := repMgr.GetAllReputations()
	writeJSON(w, 200, map[string]any{
		"reputations": reps,
		"total":       len(reps),
	})
}

// handlePostScore receives a PeerScore from another node.
// POST /federation/score
func handlePostScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}

	var score PeerScore
	if err := readJSON(r, &score); err != nil {
		writeError(w, 400, "invalid score body")
		return
	}

	if score.FromNode == "" || score.TargetNode == "" {
		writeError(w, 400, "from_node and target_node are required")
		return
	}

	// Verify the sender is in the trust pool
	trustPool := fed.GetTrustPool()
	var senderInfo *NodeInfo
	for i := range trustPool.Nodes {
		if trustPool.Nodes[i].NodeID == score.FromNode {
			senderInfo = &trustPool.Nodes[i]
			break
		}
	}
	if senderInfo == nil {
		writeError(w, 403, "scoring node not in trust pool")
		return
	}

	// Verify signature over the score payload
	if score.Signature != "" && senderInfo.PubKey != "" {
		pubKeyBytes, err := base64.StdEncoding.DecodeString(senderInfo.PubKey)
		if err == nil && len(pubKeyBytes) == ed25519.PublicKeySize {
			payload := fmt.Sprintf("%s:%s:%.0f:%.0f:%.0f:%s",
				score.FromNode, score.TargetNode,
				score.Availability, score.Latency, score.Accuracy,
				score.Timestamp)
			sigBytes, err := base64.StdEncoding.DecodeString(score.Signature)
			if err == nil && len(sigBytes) == ed25519.SignatureSize {
				pubKey := ed25519.PublicKey(pubKeyBytes)
				if !ed25519.Verify(pubKey, []byte(payload), sigBytes) {
					writeError(w, 403, "score signature verification failed")
					return
				}
			}
		}
	}

	repMgr.AddPeerScore(score)
	writeJSON(w, 200, map[string]string{"status": "accepted"})
}
