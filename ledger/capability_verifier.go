package ledger

import (
	"fmt"
	"sync"
	"time"
)

// ProbeResult is the outcome of a single capability probe.
type ProbeResult struct {
	PeerID    string
	ModelID   string
	Success   bool
	LatencyMS int64
	Error     string
	Timestamp time.Time
}

// CapabilityVerifier performs active probing to verify peer capability claims.
type CapabilityVerifier struct {
	mu sync.RWMutex

	// probeFn is the actual probe function (injectable for testing).
	probeFn func(peerID, modelID string) (bool, int64, error)

	// Cross-verification: modelID -> list of verifier results.
	crossResults map[string][]ProbeResult

	// Minimum independent verifiers to confirm a claim.
	minVerifiers int
}

// NewCapabilityVerifier creates a verifier with a probe function.
// If probeFn is nil, a default no-op probe is used (always succeeds).
func NewCapabilityVerifier(probeFn func(peerID, modelID string) (bool, int64, error), minVerifiers int) *CapabilityVerifier {
	if minVerifiers <= 0 {
		minVerifiers = 2
	}
	return &CapabilityVerifier{
		probeFn:      probeFn,
		crossResults: make(map[string][]ProbeResult),
		minVerifiers: minVerifiers,
	}
}

// Probe performs a single capability probe against a peer.
func (cv *CapabilityVerifier) Probe(peerID, modelID string) *ProbeResult {
	var result ProbeResult
	result.PeerID = peerID
	result.ModelID = modelID
	result.Timestamp = time.Now().UTC()

	if cv.probeFn != nil {
		ok, latency, err := cv.probeFn(peerID, modelID)
		result.Success = ok
		result.LatencyMS = latency
		if err != nil {
			result.Error = err.Error()
		}
	} else {
		// Default: simulate a successful probe.
		result.Success = true
		result.LatencyMS = 50
	}

	// Store for cross-verification.
	cv.mu.Lock()
	cv.crossResults[modelID] = append(cv.crossResults[modelID], result)
	cv.mu.Unlock()

	return &result
}

// VerifyClaim verifies a capability claim by probing all declared models.
func (cv *CapabilityVerifier) VerifyClaim(claim *CapabilityClaim) ([]*ProbeResult, bool) {
	var results []*ProbeResult
	allOK := true

	for _, modelID := range claim.Models {
		r := cv.Probe(claim.PeerID, modelID)
		results = append(results, r)
		if !r.Success {
			allOK = false
		}
	}

	return results, allOK
}

// CrossVerify checks whether a model has been independently verified
// by the minimum number of distinct verifiers.
// Returns the count of successful probes and whether the claim is confirmed.
func (cv *CapabilityVerifier) CrossVerify(modelID string) (int, bool) {
	cv.mu.RLock()
	defer cv.mu.RUnlock()

	results := cv.crossResults[modelID]
	successCount := 0
	seen := make(map[string]bool)

	for _, r := range results {
		if r.Success && !seen[r.PeerID] {
			successCount++
			seen[r.PeerID] = true
		}
	}

	return successCount, successCount >= cv.minVerifiers
}

// GetProbeHistory returns all probe results for a given model.
func (cv *CapabilityVerifier) GetProbeHistory(modelID string) []ProbeResult {
	cv.mu.RLock()
	defer cv.mu.RUnlock()

	results := cv.crossResults[modelID]
	out := make([]ProbeResult, len(results))
	copy(out, results)
	return out
}

// CrossVerifySummary returns a human-readable cross-verification summary.
func (cv *CapabilityVerifier) CrossVerifySummary(modelID string) string {
	count, confirmed := cv.CrossVerify(modelID)
	status := "UNCONFIRMED"
	if confirmed {
		status = "CONFIRMED"
	}
	return fmt.Sprintf("model=%s verifiers=%d status=%s min_required=%d",
		modelID, count, status, cv.minVerifiers)
}
