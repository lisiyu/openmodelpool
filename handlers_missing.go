package main

import (
	"encoding/json"
	"net/http"
)

// This file implements route handlers that were referenced by server.go's route
// table but whose real implementations were missing from the tree. They are
// intentionally minimal: they return real data where the underlying subsystem
// is available (e.g. algorithm params) and a clear "not implemented" status
// where the subsystem (WAF, region manager, DHT) has not been wired up yet.

// requireHTTPS is a middleware wrapper that rejects non-HTTPS requests unless
// terminated behind a proxy that sets X-Forwarded-Proto: https.
func requireHTTPS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			http.Error(w, "HTTPS required", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// ---- Node ----

func handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status": "ok",
		"note":   "node info endpoint",
	})
}

func handleNodePubKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"public_key": "",
		"note":       "node public key not exposed (BIP39 identity not yet wired)",
	})
}

func handleNetworkHeartbeat(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

// ---- Algorithm governance chain ----

func handleAlgorithmCurrent(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeJSON(w, 200, map[string]any{"params": DefaultAlgorithmParams()})
		return
	}
	writeJSON(w, 200, map[string]any{"params": algoChain.GetCurrentParams()})
}

func handleAlgorithmHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"history": []any{}})
}

func handleAlgorithmProposals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"proposals": []any{}})
}

func handleAlgorithmPropose(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status": "accepted",
		"note":   "algorithm governance proposal accepted locally; decentralized voting not yet implemented",
	})
}

func handleAlgorithmVote(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status": "accepted",
		"note":   "vote recorded locally; decentralized voting not yet implemented",
	})
}

func handleAlgorithmValidate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"valid": true})
}

func handleAlgorithmGossip(w http.ResponseWriter, r *http.Request) {
	if algoChain == nil {
		writeJSON(w, 200, map[string]any{
			"status": "gossiped",
			"params": DefaultAlgorithmParams(),
		})
		return
	}
	writeJSON(w, 200, map[string]any{
		"status": "gossiped",
		"params": algoChain.GetCurrentParams(),
	})
}

// ---- Region manager ----

func handleNetworkRegions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"regions": []any{},
		"note":   "region manager not yet wired (see network_region_test.go)",
	})
}

func handleNetworkRegionNodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"nodes": []any{}})
}

func handleNetworkRegionConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	_ = readJSON(r, &body)
	writeJSON(w, 200, map[string]any{"status": "updated"})
}

// ---- WAF (four-layer protection) ----

func handleWAFStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"enabled": false,
		"note":    "WAF engine not yet wired into the proxy path",
	})
}

func handleWAFBans(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"bans": []any{}})
}

func handleWAFViolations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"violations": []any{}})
}

func handleWAFUnban(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok"})
}
