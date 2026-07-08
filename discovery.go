package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// fetchFromRegistry performs an HTTP GET to the GitHub raw trust_pool.json URL.
// It sends an If-None-Match header with the stored ETag for conditional fetching.
// Returns (nil, nil) on 304 Not Modified.
func (f *FederationManager) fetchFromRegistry() (*TrustPool, error) {
	registryURL := cfg.Get("federation_registry_url",
		"https://raw.githubusercontent.com/lisiyu/openmodelpool/main/federation/trust_pool.json")

	req, err := http.NewRequest("GET", registryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create registry request: %w", err)
	}

	f.mu.RLock()
	etag := f.lastETag
	f.mu.RUnlock()

	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	req.Header.Set("Accept", "application/json")

	client := GetSharedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch registry: %w", err)
	}
	defer resp.Body.Close()

	// 304 Not Modified — pool hasn't changed
	if resp.StatusCode == http.StatusNotModified {
		slog.Debug("trust pool unchanged (304)")
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read registry response: %w", err)
	}

	var pool TrustPool
	if err := json.Unmarshal(body, &pool); err != nil {
		return nil, fmt.Errorf("parse trust pool JSON: %w", err)
	}

	// Persist new ETag for next conditional request
	if newETag := resp.Header.Get("ETag"); newETag != "" {
		f.mu.Lock()
		f.lastETag = newETag
		f.mu.Unlock()
		cfg.Set("federation_pool_etag", newETag)
	}

	return &pool, nil
}

// fetchFromPeers attempts to retrieve the trust pool from known active peers
// when the GitHub registry is unreachable. Uses the first successful response.
func (f *FederationManager) fetchFromPeers() {
	peers := f.GetActiveNodes()
	if len(peers) == 0 {
		slog.Debug("no active peers available for P2P pool fallback")
		return
	}

	client := GetSharedHTTPClient()
	myID := node.NodeID()

	for _, peer := range peers {
		if peer.NodeID == myID || peer.Endpoint == "" {
			continue
		}

		url := fmt.Sprintf("%s/federation/pool", peer.Endpoint)
		resp, err := client.Get(url)
		if err != nil {
			slog.Debug("failed to fetch pool from peer",
				"peer_id", peer.NodeID, "error", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			slog.Debug("failed to read pool response from peer",
				"peer_id", peer.NodeID, "error", err)
			continue
		}

		var pool TrustPool
		if err := json.Unmarshal(body, &pool); err != nil {
			slog.Debug("failed to parse pool JSON from peer",
				"peer_id", peer.NodeID, "error", err)
			continue
		}

		slog.Info("fetched trust pool from peer via P2P fallback",
			"peer_id", peer.NodeID,
			"version", pool.Version,
			"nodes", len(pool.Nodes))
		f.UpdateTrustPool(pool)
		return
	}

	slog.Warn("failed to fetch trust pool from any peer")
}

// refreshLoop runs in a goroutine and periodically refreshes the trust pool.
// It first tries the GitHub registry; on failure it falls back to peer fetching.
func (f *FederationManager) refreshLoop() {
	intervalSecs := cfg.Get("federation_refresh_interval_s", "300")
	interval := parseDurationSecs(intervalSecs, 300)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("federation refresh loop started", "interval_s", interval.Seconds())

	// Perform an initial refresh immediately
	if err := f.refreshFromGitHub(); err != nil {
		slog.Warn("initial federation refresh failed, falling back to peers", "error", err)
		f.fetchFromPeers()
	}

	for {
		select {
		case <-ticker.C:
			if err := f.refreshFromGitHub(); err != nil {
				slog.Warn("federation refresh from GitHub failed, falling back to peers",
					"error", err)
				f.fetchFromPeers()
			}
		case <-f.stopCh:
			slog.Info("federation refresh loop exiting")
			return
		}
	}
}

// handleFederationPool is the HTTP handler for GET /federation/pool.
// It returns this node's cached trust pool so other peers can fetch it.
func handleFederationPool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if fed == nil || !fed.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "federation is not enabled")
		return
	}

	pool := fed.GetTrustPool()
	writeJSON(w, http.StatusOK, pool)
}

// parseDurationSecs converts a string (in seconds) to a time.Duration.
// Returns defaultSecs on parse failure or non-positive values.
func parseDurationSecs(s string, defaultSecs int) time.Duration {
	secs, err := strconv.Atoi(s)
	if err != nil || secs <= 0 {
		return time.Duration(defaultSecs) * time.Second
	}
	return time.Duration(secs) * time.Second
}
