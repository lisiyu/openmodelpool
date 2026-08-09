package main

import (
	"encoding/json"
	"net/http"
)

// handleAdminLedgerTransparency returns the public-welfare transparency report
// (P2-2): where contributed compute came from (by peer / by model) and the
// integrity of the ledger record. Admin-authenticated.
func handleAdminLedgerTransparency(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		http.Error(w, "ledger not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(contributionLedger.GetTransparency())
}
