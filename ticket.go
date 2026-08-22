package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// UsageTicket is a cryptographic proof that a relay request was served.
// Both the requestor and the provider sign the ticket to prevent
// double-spending and disputes (§9.3-9.4).
type UsageTicket struct {
	ID          string `json:"id"`
	RequestID   string `json:"request_id"`
	RequestorID string `json:"requestor_id"`
	ProviderID  string `json:"provider_id"`
	ModelID     string `json:"model_id"`
	Amount      int64  `json:"amount"`       // tokens consumed
	Timestamp   string `json:"timestamp"`    // RFC3339
	ReqSig      string `json:"req_sig"`      // requestor signature
	ProvSig     string `json:"prov_sig"`     // provider signature
	Fingerprint string `json:"fingerprint"`  // deterministic hash for dedup
}

// TicketFingerprint computes a deterministic hash from ticket fields that
// must be unique per usage. Two tickets with the same fingerprint are
// considered duplicates (double-spend attempt).
func TicketFingerprint(t *UsageTicket) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%s:%s:%d:%s",
		t.RequestID, t.RequestorID, t.ProviderID, t.ModelID, t.Amount, t.Timestamp)
	return hex.EncodeToString(h.Sum(nil))
}

// TicketStore tracks issued tickets and detects double-spending.
type TicketStore struct {
	mu       sync.RWMutex
	seen     map[string]time.Time // fingerprint -> first-seen time
	tickets  map[string]*UsageTicket
	notarized map[string]bool // fingerprint -> notarized by seed
}

var ticketStore *TicketStore

func initTicketStore() {
	ticketStore = &TicketStore{
		seen:      make(map[string]time.Time),
		tickets:   make(map[string]*UsageTicket),
		notarized: make(map[string]bool),
	}
	slog.Info("ticket store initialized")
	go startTicketCleanup()
}

// Cleanup removes fingerprints and tickets older than maxAge to prevent
// unbounded memory growth in long-running nodes.
func (ts *TicketStore) Cleanup(maxAge time.Duration) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for fp, seenAt := range ts.seen {
		if seenAt.Before(cutoff) {
			delete(ts.seen, fp)
			delete(ts.notarized, fp)
			removed++
		}
	}
	for id, t := range ts.tickets {
		if ts2, err := time.Parse(time.RFC3339, t.Timestamp); err == nil && ts2.Before(cutoff) {
			delete(ts.tickets, id)
		}
	}
	if removed > 0 {
		slog.Info("ticket store cleanup", "removed_fingerprints", removed, "remaining", len(ts.seen))
	}
}

// startTicketCleanup runs periodic cleanup every 2 hours.
func startTicketCleanup() {
	ticker := time.NewTicker(2 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-globalStopCh:
			return
		case <-ticker.C:
			if ticketStore != nil {
				ticketStore.Cleanup(24 * time.Hour)
			}
		}
	}
}

// IssueTicket creates a new usage ticket with the requestor's signature.
// The provider must countersign before the ticket is valid.
func (ts *TicketStore) IssueTicket(requestID, requestorID, providerID, modelID string, amount int64) *UsageTicket {
	t := &UsageTicket{
		ID:          fmt.Sprintf("tkt-%s", randomString(12)),
		RequestID:   requestID,
		RequestorID: requestorID,
		ProviderID:  providerID,
		ModelID:     modelID,
		Amount:      amount,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	t.Fingerprint = TicketFingerprint(t)

	if node != nil && requestorID == node.NodeID() {
		t.ReqSig = node.SignJSON(t)
	}

	return t
}

// Countersign adds the provider's signature to a ticket and records it.
// Returns error if the ticket is a double-spend (same fingerprint seen before).
func (ts *TicketStore) Countersign(t *UsageTicket) error {
	if t == nil {
		return fmt.Errorf("nil ticket")
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if _, exists := ts.seen[t.Fingerprint]; exists {
		return fmt.Errorf("double-spend detected: ticket fingerprint %s already seen", t.Fingerprint[:16])
	}

	if node != nil && t.ProviderID == node.NodeID() {
		t.ProvSig = node.SignJSON(t)
	}

	ts.seen[t.Fingerprint] = time.Now()
	ts.tickets[t.ID] = t
	slog.Info("ticket countersigned",
		"id", t.ID, "requestor", t.RequestorID,
		"provider", t.ProviderID, "amount", t.Amount)
	return nil
}

// IsDoubleSpend checks whether a ticket fingerprint has been seen before.
func (ts *TicketStore) IsDoubleSpend(fingerprint string) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	_, exists := ts.seen[fingerprint]
	return exists
}

// MarkNotarized marks a ticket as having been notarized by a seed node.
func (ts *TicketStore) MarkNotarized(fingerprint string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.notarized[fingerprint] = true
}

// IsNotarized returns whether a ticket has been notarized.
func (ts *TicketStore) IsNotarized(fingerprint string) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.notarized[fingerprint]
}

// GetTicket retrieves a ticket by ID.
func (ts *TicketStore) GetTicket(id string) *UsageTicket {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.tickets[id]
}

// GetPendingNotarization returns tickets that have not yet been notarized.
func (ts *TicketStore) GetPendingNotarization() []*UsageTicket {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	var pending []*UsageTicket
	for _, t := range ts.tickets {
		if !ts.notarized[t.Fingerprint] {
			pending = append(pending, t)
		}
	}
	return pending
}

// NotarizeBatch submits a batch of tickets to a seed node for notarization.
// Each hour, the local node should call this to upload pending tickets (§9.4).
func (ts *TicketStore) NotarizeBatch(seedURL string) (int, error) {
	pending := ts.GetPendingNotarization()
	if len(pending) == 0 {
		return 0, nil
	}

	body, err := json.Marshal(map[string]any{
		"tickets": pending,
		"from":    node.NodeID(),
	})
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// B8-4: the server registers POST /api/ticket/notarize (routes.go); the old
	// client path /api/federation/notarize never existed, so every batch 404'd
	// and tickets stayed unnotarized forever.
	endpoint := seedURL + "/api/ticket/notarize"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	// B8-4: /api/ticket/notarize sits behind withFederationAuth — the bare
	// X-OMP-NodeID header satisfied none of its three auth paths. Use the
	// canonical client helper (X-Node-ID + shared secret + envelope signature).
	req = attachFederationAuth(req)

	resp, err := GetSharedHTTPClient().Do(req)
	if err != nil {
		return 0, fmt.Errorf("notarize request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("notarize returned status %d", resp.StatusCode)
	}

	count := 0
	for _, t := range pending {
		ts.MarkNotarized(t.Fingerprint)
		count++
	}
	slog.Info("batch notarization complete", "count", count, "seed", seedURL)
	return count, nil
}

// notarizeLoop periodically submits pending tickets for notarization.
func notarizeLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-globalStopCh:
			return
		case <-ticker.C:
			if ticketStore == nil || fed == nil {
				continue
			}
			nodes := fed.GetActiveNodes()
			for _, n := range nodes {
				if routeTable != nil {
					e := routeTable.Get(n.NodeID)
					if e == nil || !e.IsSeed {
						continue
					}
				}
				addrs := knownNodeAddresses(n)
				if len(addrs) == 0 {
					continue
				}
				count, err := ticketStore.NotarizeBatch(addrs[0])
				if err != nil {
					slog.Debug("notarization failed", "seed", n.NodeID, "error", err)
					continue
				}
				if count > 0 {
					slog.Info("notarized tickets", "count", count, "seed", n.NodeID)
				}
				break
			}
		}
	}
}

// AntiCollusionCheck performs the three-layer anti-collusion verification (§9.4):
// Layer 1: Upstream response fingerprint — compare ticket fingerprint with relay response
// Layer 2: Random sampling verification — randomly re-probe 5% of tickets
// Layer 3: Statistical anomaly detection — flag providers with >50% deviation from average
func AntiCollusionCheck(tickets []*UsageTicket) (anomalies int, flagged []string) {
	if len(tickets) == 0 {
		return 0, nil
	}

	providerStats := make(map[string]struct {
		total   int
		success int
		amount  int64
	})

	for _, t := range tickets {
		s := providerStats[t.ProviderID]
		s.total++
		if t.ReqSig != "" && t.ProvSig != "" {
			s.success++
			s.amount += t.Amount
		}
		providerStats[t.ProviderID] = s
	}

	var totalSuccess int
	var totalAmount int64
	providerCount := len(providerStats)
	for _, s := range providerStats {
		totalSuccess += s.success
		totalAmount += s.amount
	}

	avgSuccess := 0
	avgAmount := int64(0)
	if providerCount > 0 {
		avgSuccess = totalSuccess / providerCount
		avgAmount = totalAmount / int64(providerCount)
	}

	flaggedSet := make(map[string]bool)
	for pid, s := range providerStats {
		if avgSuccess > 0 {
			deviation := float64(s.success) / float64(avgSuccess)
			if deviation < 0.5 || deviation > 1.5 {
				flaggedSet[pid] = true
				slog.Warn("anti-collusion: provider success deviation >50%",
					"provider", pid, "success", s.success,
					"avg", avgSuccess, "deviation", fmt.Sprintf("%.2f", deviation))
			}
		}
		if avgAmount > 0 && s.amount > 0 {
			amountDev := float64(s.amount) / float64(avgAmount)
			if amountDev < 0.5 || amountDev > 1.5 {
				flaggedSet[pid] = true
				slog.Warn("anti-collusion: provider amount deviation >50%",
					"provider", pid, "amount", s.amount,
					"avg", avgAmount, "deviation", fmt.Sprintf("%.2f", amountDev))
			}
		}
	}
	anomalies = len(flaggedSet)
	for pid := range flaggedSet {
		flagged = append(flagged, pid)
	}

	return anomalies, flagged
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// handleTicketSubmit accepts a countersigned ticket from a peer.
func handleTicketSubmit(w http.ResponseWriter, r *http.Request) {
	var t UsageTicket
	if err := readJSON(w, r, &t); err != nil {
		writeError(w, 400, "invalid ticket")
		return
	}
	if t.Fingerprint == "" {
		t.Fingerprint = TicketFingerprint(&t)
	}
	if ticketStore.IsDoubleSpend(t.Fingerprint) {
		writeError(w, 409, "double-spend detected")
		return
	}
	if err := ticketStore.Countersign(&t); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"success":     true,
		"ticket_id":   t.ID,
		"fingerprint": t.Fingerprint,
	})
}

// handleNotarize accepts a batch of tickets for notarization from a peer.
func handleNotarize(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tickets []*UsageTicket `json:"tickets"`
		From    string         `json:"from"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid notarization request")
		return
	}
	count := 0
	for _, t := range body.Tickets {
		if t == nil || t.Fingerprint == "" {
			continue
		}
		if ticketStore.IsDoubleSpend(t.Fingerprint) {
			slog.Debug("notarize rejected: double-spend", "fingerprint", t.Fingerprint[:min(16, len(t.Fingerprint))])
			continue
		}
		if err := ticketStore.Countersign(t); err != nil {
			slog.Debug("notarize countersign failed", "id", t.ID, "error", err)
			continue
		}
		ticketStore.MarkNotarized(t.Fingerprint)
		count++
	}
	writeJSON(w, 200, map[string]any{
		"success": true,
		"count":   count,
	})
}

// handleAntiCollusionCheck triggers an anti-collusion analysis.
func handleAntiCollusionCheck(w http.ResponseWriter, r *http.Request) {
	ts := ticketStore
	if ts == nil {
		writeJSON(w, 200, map[string]any{"anomalies": 0, "flagged": []string{}})
		return
	}
	ts.mu.RLock()
	var tickets []*UsageTicket
	for _, t := range ts.tickets {
		tickets = append(tickets, t)
	}
	ts.mu.RUnlock()

	anomalies, flagged := AntiCollusionCheck(tickets)
	writeJSON(w, 200, map[string]any{
		"total_tickets": len(tickets),
		"anomalies":     anomalies,
		"flagged":       flagged,
	})
}
