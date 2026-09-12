package main

import (
	"net/http"
	"strconv"
)

// ledger_honor.go — Public-welfare honor roll (P2 "让贡献者被看见").
//
// The honor roll lists the top contributors by donated compute. It is
// deliberately NON-economic: no points, no exchange rate, no rewards, and it
// is never consulted by quota / priority / governance code. It exists so
// donors and volunteers can see the real impact of their contribution.

// ContributorHonorView is the API shape of one honor-roll entry. Name is
// resolved opportunistically from the federation trust pool for readability
// and falls back to a short node id.
type ContributorHonorView struct {
	PeerID  string `json:"peer_id"`
	Name    string `json:"name"`
	Tokens  int64  `json:"tokens"`
	Records int    `json:"records"`
}

// handleAdminLedgerContributors serves the honor roll (P2): top contributors
// by donated tokens, purely for visibility. Supports ?limit= (default 10,
// max 100). Admin-authenticated.
func handleAdminLedgerContributors(w http.ResponseWriter, r *http.Request) {
	if contributionLedger == nil {
		http.Error(w, "ledger not ready", http.StatusServiceUnavailable)
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	honors := contributionLedger.GetContributorHonors()
	views := make([]ContributorHonorView, 0, len(honors))
	for i, h := range honors {
		if i >= limit {
			break
		}
		name := shortNodeID(h.PeerID)
		if fed != nil {
			if info, ok := fed.GetNode(h.PeerID); ok && info.GitHubUser != "" {
				name = info.GitHubUser
			}
		}
		views = append(views, ContributorHonorView{
			PeerID:  h.PeerID,
			Name:    name,
			Tokens:  h.Tokens,
			Records: h.Records,
		})
	}
	writeJSON(w, 200, map[string]any{
		"contributors":       views,
		"total_contributors": len(honors),
	})
}
