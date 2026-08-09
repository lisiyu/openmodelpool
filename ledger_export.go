package main

import (
	"io"
	"net/http"
)

// handleLedgerExport serves the contribution ledger for download / research
// (P4-1). Default format is JSON (full ledger); ?format=csv returns the
// contributions as CSV. Admin-authenticated so operators control distribution.
func handleLedgerExport(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		http.Error(w, "ledger not ready", http.StatusServiceUnavailable)
		return
	}
	format := r.URL.Query().Get("format")
	switch format {
	case "csv":
		csvStr, err := contributionLedger.ExportContributionsCSV()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="contributions.csv"`)
		io.WriteString(w, csvStr)
	default:
		data, err := contributionLedger.ExportLedgerJSON()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="ledger.json"`)
		w.Write(data)
	}
}
