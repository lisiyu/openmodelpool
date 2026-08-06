package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

var contributionLedger *GossipLedger
var capabilityVerifier *CapabilityVerifier

func initContributionLedger(dataDir string) {
	selfID := "unknown"
	if node != nil {
		selfID = node.NodeID()
	}

	ledgerPath := dataDir + "/ledger.json"
	loaded := false
	if _, err := os.Stat(ledgerPath); err == nil {
		if gl, loadErr := LoadGossipLedger(ledgerPath); loadErr == nil {
			contributionLedger = gl
			loaded = true
			slog.Info("contribution ledger loaded from disk",
				"peer_id", gl.PeerID(),
				"records", gl.Count())
		} else {
			slog.Warn("failed to load contribution ledger, creating new", "error", loadErr)
		}
	}

	if !loaded {
		gl, err := NewGossipLedger(selfID)
		if err != nil {
			slog.Error("failed to create contribution ledger", "error", err)
			return
		}
		contributionLedger = gl
		if saveErr := gl.Save(ledgerPath); saveErr != nil {
			slog.Warn("failed to save initial contribution ledger", "error", saveErr)
		}
		slog.Info("contribution ledger initialized", "peer_id", selfID)
	}

	capabilityVerifier = NewCapabilityVerifier(realProbeFn, 3)
	go capabilityVerifier.ProbeSchedulerLoop()
	slog.Info("capability verifier initialized")
}

func saveContributionLedger() {
	if contributionLedger == nil {
		return
	}
	if err := contributionLedger.Save("data/ledger.json"); err != nil {
		slog.Warn("failed to save contribution ledger", "error", err)
	}
}

// realProbeFn sends a 1-token test request to a remote node to verify
// that the claimed model is actually available (§10.2).
func realProbeFn(peerID, modelID string) (bool, int64, error) {
	if routeTable == nil {
		return false, 0, fmt.Errorf("route table not initialized")
	}
	entry := routeTable.Get(peerID)
	if entry == nil {
		return false, 0, fmt.Errorf("peer %s not found in route table", peerID)
	}
	targetAddr := pickBestAddress(entry.Addresses)
	if targetAddr == "" {
		return false, 0, fmt.Errorf("no address for peer %s", peerID)
	}

	probePayload := map[string]any{
		"model": modelID,
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
		},
		"max_tokens": 1,
		"stream":     false,
	}
	body, _ := json.Marshal(probePayload)

	endpoint := targetAddr + "/v1/chat/completions"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if node != nil {
		req.Header.Set("X-OMP-NodeID", node.NodeID())
	}

	start := time.Now()
	resp, err := GetSharedHTTPClient().Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return false, latency, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	ok := resp.StatusCode == 200
	return ok, latency, nil
}
