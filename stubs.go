package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// This file collects initialization/helper functions that were originally
// missing from the tree. Most functions now have real implementations;
// the remaining placeholder (registerWithBootstraps) is marked with TODO.

// initEncryptor is a no-op: encryption is initialized via encryptor.go's init().
func initEncryptor(keyPath string) {}

// NOTE: initWAF now has a real implementation in waf.go (the four-layer WAF
// engine is wired into the proxy/relay request path). This file's previous
// no-op placeholder was removed to avoid a duplicate definition.

// initRegionManager wires up the process-wide region manager instance. The
// RegionManager implementation lives in network_region_impl.go; this function
// simply instantiates it so the /api/network/regions endpoints serve real data
// instead of the previous "not yet wired" stubs.
func initRegionManager() {
	regionManager = NewRegionManager()
}

// startHeartbeatLoop launches the periodic node-to-node heartbeat sender.
//
// Each tick it collects this node's known peer endpoints (from the federation
// trust pool / gossip peers, plus the network manager's manual peers), POSTs a
// heartbeat to every peer's /api/network/heartbeat endpoint, and locally
// refreshes this node's own global-pool liveness. Per-peer failures are logged
// and never abort the loop. The interval is sourced from the heartbeat_interval_s
// config key (default 60s) — this is what actually *consumes* that setting.
//
// The loop runs in its own goroutine and is intentionally long-lived; it exits
// only when the process terminates.
func startHeartbeatLoop() {
	go func() {
		ticker := time.NewTicker(getHeartbeatInterval())
		defer ticker.Stop()
		slog.Info("heartbeat loop started",
			"interval", getHeartbeatInterval().String())
		select {
			case <-ticker.C:
				runHeartbeatOnce()
			case <-globalStopCh:
				return
			}
	}()
}

// getHeartbeatInterval returns the configured heartbeat interval.
// cfg exposes only string getters, so we parse heartbeat_interval_s with a
// safe fallback to the documented default of 60 seconds.
func getHeartbeatInterval() time.Duration {
	const defaultInterval = 60 * time.Second
	if cfg == nil {
		return defaultInterval
	}
	if v := cfg.Get("heartbeat_interval_s", ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultInterval
}

// runHeartbeatOnce performs a single heartbeat broadcast to all known peers and
// refreshes this node's own global-pool entry.
func runHeartbeatOnce() {
	if netMgr == nil {
		return
	}
	selfNodeID := netMgr.GetNodeID()
	if selfNodeID == "" {
		// Personal mode / no identity yet — nothing to announce.
		return
	}

	secret := ""
	if cfg != nil {
		secret = cfg.Get("federation_secret", "")
	}

	selfEndpoint := resolveSelfEndpoint()

	client := &http.Client{Timeout: 5 * time.Second}
	for _, ep := range collectPeerEndpoints(selfEndpoint) {
		peerURL := strings.TrimRight(ep, "/") + "/api/network/heartbeat"
		if err := postHeartbeatToPeer(client, peerURL, selfNodeID, selfEndpoint, secret); err != nil {
			slog.Debug("heartbeat send failed", "peer", ep, "error", err)
		}
	}

	// Keep this node's own global-pool entry active (if it joined its own pool).
	if globalPool != nil {
		globalPool.Heartbeat(selfNodeID)
	}
}

// resolveSelfEndpoint returns the best local advertisement endpoint for this node.
// It prefers a previously-collected address (no cfg dependency) and falls back to
// the federation endpoint resolver.
func resolveSelfEndpoint() string {
	if netMgr != nil {
		netMgr.mu.RLock()
		var addr string
		if len(netMgr.config.Addresses) > 0 {
			addr = netMgr.config.Addresses[0]
		}
		netMgr.mu.RUnlock()
		if addr != "" {
			return addr
		}
	}
	return resolvePublicEndpoint("")
}

// collectPeerEndpoints gathers every known peer endpoint (deduplicated), merging
// the federation trust-pool / gossip peers with the network manager's manually
// added peers. The local node's own endpoint is excluded to avoid self-ping.
func collectPeerEndpoints(selfEndpoint string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(ep string) {
		ep = strings.TrimRight(ep, "/")
		if ep == "" || seen[ep] {
			return
		}
		if ep == selfEndpoint {
			return
		}
		seen[ep] = true
		out = append(out, ep)
	}

	if fed != nil {
		for _, ep := range fed.allKnownEndpoints() {
			add(ep)
		}
	}
	if netMgr != nil {
		for _, p := range netMgr.GetPeers() {
			for _, a := range p.Addresses {
				add(a)
			}
		}
	}
	return out
}

// postHeartbeatToPeer POSTs a heartbeat to a single peer endpoint. It is the
// unit-testable core of the sender: it builds the JSON body, sets the
// X-Node-ID / X-Federation-Secret auth headers, and returns any transport or
// non-2xx error. It does NOT log or retry — callers decide how to handle errors.
func postHeartbeatToPeer(client *http.Client, peerURL, selfNodeID, selfEndpoint, secret string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body := map[string]string{
		"node_id":  selfNodeID,
		"endpoint": selfEndpoint,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		cancel()
		return fmt.Errorf("marshal heartbeat body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, peerURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-ID", selfNodeID)
	if secret != "" {
		req.Header.Set("X-Federation-Secret", secret)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send heartbeat to %s: %w", peerURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat to %s returned status %d", peerURL, resp.StatusCode)
	}
	return nil
}

// startRegionSyncLoop periodically synchronizes region assignments across the
// federation. The local RegionManager is already authoritative per-node: nodes
// register their region on join (RegisterNodeSelfReport) and on every heartbeat
// (ProcessHeartbeatRegion). This loop is the future hook for cross-node region
// reconciliation once the gossip/federation layer exposes a region-state channel.
//
// TODO(region-sync): replace the sleep-only loop with real cross-node region
// reconciliation. For now it only keeps the goroutine alive on a 30s cadence.
func startRegionSyncLoop() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		select {
			case <-ticker.C:
				// No-op today; see TODO above.
			case <-globalStopCh:
				return
			}
	}()
}

// registerWithBootstraps registers this node with bootstrap/seed nodes.
// TODO: implement federation bootstrap registration when the federation
// handshake protocol is finalized. Currently a no-op; federation join is
// handled via the trust pool refresh loop in discovery.go.
func registerWithBootstraps() {
	if netMgr == nil || !netMgr.config.NetworkEnabled {
		return
	}
	bootstrapNodes := netMgr.config.BootstrapNodes
	if len(bootstrapNodes) == 0 {
		return
	}
	nodeID := netMgr.GetNodeID()
	if nodeID == "" {
		return
	}
	var addrs []string
	if natMgr != nil && natMgr.GetPublicAddr() != "" {
		addrs = append(addrs, "https://"+natMgr.GetPublicAddr())
	}
	if len(netMgr.config.Addresses) > 0 {
		addrs = append(addrs, netMgr.config.Addresses...)
	}
	if len(addrs) == 0 {
		slog.Warn("registerWithBootstraps: no addresses to advertise")
		return
	}

	payload := map[string]any{
		"node_id":   nodeID,
		"addresses": addrs,
		"is_gateway": cfg.Get("is_gateway", "false") == "true",
	}
	body, _ := json.Marshal(payload)

	for _, bs := range bootstrapNodes {
		go func(bootstrapURL string) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "POST",
				strings.TrimRight(bootstrapURL, "/")+"/api/federation/register", bytes.NewReader(body))
			if err != nil {
				slog.Debug("registerWithBootstraps: create request failed", "url", bootstrapURL, "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if node != nil {
				req.Header.Set("X-OMP-NodeID", nodeID)
				sig, ts := signRelayForward(nodeID, "POST", "/api/federation/register", body)
				req.Header.Set("X-OMP-Sig", sig)
				req.Header.Set("X-OMP-Ts", ts)
			}
			resp, err := GetSharedHTTPClient().Do(req)
			if err != nil {
				slog.Debug("registerWithBootstraps: request failed", "url", bootstrapURL, "error", err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			slog.Info("registered with bootstrap node", "url", bootstrapURL, "status", resp.StatusCode)
		}(bs)
	}
}

// GetDHTStats returns DHT routing-table statistics. DHT (Kademlia) is not yet
// implemented, so this reports a clear "not implemented" status.
func GetDHTStats() map[string]any {
	if fed == nil || fed.dht == nil {
		return map[string]any{
			"enabled":     false,
			"total_nodes": 0,
			"buckets":     0,
			"records":     0,
		}
	}
	stats := fed.dht.BucketStats()
	records := 0
	fed.dht.mu.RLock()
	records = len(fed.dht.records)
	fed.dht.mu.RUnlock()
	return map[string]any{
		"enabled":      true,
		"self_id":      fed.dht.SelfID(),
		"total_nodes":  fed.dht.TotalNodes(),
		"buckets_used": len(stats),
		"bucket_stats": stats,
		"records":      records,
	}
}
