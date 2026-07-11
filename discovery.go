package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

// fetchFromSeedNodes queries known bootstrap/seed nodes for the trust pool.
// This serves as a fallback when the GitHub registry is unreachable.
func (f *FederationManager) fetchFromSeedNodes() (*TrustPool, error) {
	if netMgr == nil {
		return nil, fmt.Errorf("network manager not available")
	}

	netMgr.mu.RLock()
	bootstrapNodes := make([]string, len(netMgr.config.BootstrapNodes))
	copy(bootstrapNodes, netMgr.config.BootstrapNodes)
	netMgr.mu.RUnlock()

	if len(bootstrapNodes) == 0 {
		return nil, fmt.Errorf("no bootstrap nodes configured")
	}

	client := GetSharedHTTPClient()
	for _, bootstrapURL := range bootstrapNodes {
		poolURL := fmt.Sprintf("%s/federation/pool", strings.TrimRight(bootstrapURL, "/"))
		resp, err := client.Get(poolURL)
		if err != nil {
			slog.Debug("seed node unreachable", "url", bootstrapURL, "error", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		var pool TrustPool
		if err := json.Unmarshal(body, &pool); err != nil {
			slog.Debug("invalid trust pool from seed", "url", bootstrapURL, "error", err)
			continue
		}

		if len(pool.Nodes) > 0 {
			slog.Info("fetched trust pool from seed node",
				"url", bootstrapURL, "version", pool.Version, "nodes", len(pool.Nodes))
			return &pool, nil
		}
	}

	return nil, fmt.Errorf("all seed nodes unreachable or returned no data")
}

// refreshLoop runs in a goroutine and periodically refreshes the trust pool.
// Trust sources (in priority order):
//  1. GitHub Registry (canonical source)
//  2. Bootstrap/seed nodes (fallback when registry is unreachable)
//  3. Active peers (P2P fallback)
func (f *FederationManager) refreshLoop() {
	intervalSecs := cfg.Get("federation_refresh_interval_s", "300")
	interval := parseDurationSecs(intervalSecs, 300)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("federation refresh loop started", "interval_s", interval.Seconds())

	// Perform an initial refresh immediately
	f.doRefresh()

	for {
		select {
		case <-ticker.C:
			f.doRefresh()
		case <-f.stopCh:
			slog.Info("federation refresh loop exiting")
			return
		}
	}
}

// doRefresh performs a single refresh cycle: GitHub → seed nodes → peers
func (f *FederationManager) doRefresh() {
	// 1. Try GitHub Registry (canonical source)
	if err := f.refreshFromGitHub(); err == nil {
		return
	} else {
		slog.Warn("GitHub registry unreachable, trying seed nodes", "error", err)
	}

	// 2. Try bootstrap/seed nodes
	if pool, err := f.fetchFromSeedNodes(); err == nil && pool != nil {
		f.mu.Lock()
		if pool.Version > f.trustPool.Version {
			f.trustPool = *pool
			slog.Info("trust pool refreshed from seed nodes",
				"version", pool.Version, "nodes", len(pool.Nodes))
			_ = f.saveLocked()
		}
		f.mu.Unlock()
		return
	} else {
		slog.Warn("seed nodes unavailable, falling back to peers", "error", err)
	}

	// 3. Try active peers (P2P fallback)
	f.fetchFromPeers()
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
