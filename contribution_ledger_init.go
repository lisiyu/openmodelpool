package main

import (
	"log/slog"
	"os"
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

	capabilityVerifier = NewCapabilityVerifier(nil, 2)
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
