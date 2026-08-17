package main

import (
	"net/http"
	"time"
)

// handleFederationHealth returns a node-level health snapshot of the mesh:
// every trust-pool peer with its status, freshness, version, reputation and
// what it shares. Requires an authenticated admin session.
func handleFederationHealth(w http.ResponseWriter, r *http.Request) {
	if fed == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "nodes": []any{}})
		return
	}

	pool := fed.GetTrustPool()
	selfID := ""
	if node != nil {
		selfID = node.NodeID()
	}

	nodes := make([]map[string]any, 0, len(pool.Nodes))
	activeCount := 0
	for i := range pool.Nodes {
		n := pool.Nodes[i]

		status := n.Status
		if status == "" {
			status = "active"
		}
		if status == "active" {
			activeCount++
		}

		// Freshness: how long ago was last_seen.
		freshness := ""
		if n.LastSeen != "" {
			if t, err := time.Parse(time.RFC3339, n.LastSeen); err == nil {
				age := time.Since(t)
				fresh := age < 15*time.Minute
				if fresh {
					freshness = "fresh"
				} else if age < 24*time.Hour {
					freshness = "stale"
				} else {
					freshness = "dead"
				}
			}
		}

		sharedProviders := 0
		sharedModels := 0
		for _, sp := range n.SharedProviders {
			sharedProviders++
			sharedModels += len(sp.Models)
		}
		sharedModels += len(n.SharedModels)

		entry := map[string]any{
			"node_id":          n.NodeID,
			"github_user":      n.GitHubUser,
			"endpoint":         n.Endpoint,
			"status":           status,
			"version":          n.Version,
			"reputation":       n.Reputation,
			"last_seen":        n.LastSeen,
			"freshness":        freshness,
			"shared_providers": sharedProviders,
			"shared_models":    sharedModels,
			"self":             n.NodeID == selfID,
		}
		nodes = append(nodes, entry)
	}

	writeJSON(w, 200, map[string]any{
		"enabled":       fed.IsEnabled(),
		"relay":         fed.IsRelayEnabled(),
		"pool_version":  pool.Version,
		"total_nodes":   len(pool.Nodes),
		"active_nodes":  activeCount,
		"self_node_id":  selfID,
		"self_version":  AppVersion,
		"nodes":         nodes,
		"generated_at":  time.Now().UTC().Format(time.RFC3339),
	})
}