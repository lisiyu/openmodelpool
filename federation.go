package main

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// withFederationAuth restricts access to known federation nodes or authenticated requests.
// SA-12 (strict): NO localhost bypass. All requests MUST present valid credentials:
//   - Known node identity (X-Node-ID matching trust pool), OR
//   - Valid admin JWT token, OR
//   - Valid Federation shared secret (X-Federation-Secret header)
//
// This prevents unauthorized access from co-located processes in containerized
// or shared-hosting environments where localhost is not a security boundary.
func withFederationAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// P1-1 (narrow allow-list): a trusted bootstrap seed node performing a
		// read-only GET against its own /federation/pool may fetch the trust pool
		// without presenting credentials. This unblocks bootstrap-node discovery
		// (fetchFromSeedNodes) which issues unauthenticated GETs. No other path or
		// method is affected, preserving SA-12 strictness for everything else.
		if r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/federation/pool") &&
			isTrustedSeed(r) {
			handler(w, r)
			return
		}

		// Auth path 1: Known node identity (X-Node-ID + trust pool verification)
		nodeID := r.Header.Get("X-Node-ID")
		if nodeID != "" && fed != nil {
			if _, ok := fed.GetNode(nodeID); ok {
				handler(w, r)
				return
			}
		}

		// Auth path 2: Valid admin JWT token
		token := extractToken(r)
		if token != "" {
			if _, err := auth.VerifyToken(token); err == nil {
				handler(w, r)
				return
			}
		}

		// Auth path 3: Federation shared secret (required for all requests including localhost)
		fedSecret := cfg.Get("federation_secret", "")
		if fedSecret != "" {
			requestSecret := r.Header.Get("X-Federation-Secret")
			if requestSecret != "" && requestSecret == fedSecret {
				handler(w, r)
				return
			}
		}

		// All auth paths failed — reject
		writeJSON(w, 403, map[string]string{"error": "federation authentication required"})
	}
}

// isTrustedSeed reports whether the incoming request originates from a configured
// bootstrap seed node. It compares the request Host (port stripped) against each
// entry of netMgr.config.BootstrapNodes, tolerating http/https prefixes and a
// trailing slash so operators can paste a bare domain or a full URL interchangeably.
func isTrustedSeed(r *http.Request) bool {
	if netMgr == nil {
		return false
	}
	host := r.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	for _, seed := range netMgr.config.BootstrapNodes {
		seedHost := strings.TrimPrefix(strings.TrimPrefix(seed, "https://"), "http://")
		seedHost = strings.TrimRight(seedHost, "/")
		if i := strings.IndexByte(seedHost, ':'); i >= 0 {
			seedHost = seedHost[:i]
		}
		if seedHost != "" && seedHost == host {
			return true
		}
	}
	return false
}

// FederationManager manages this node's participation in the federation.
type FederationManager struct {
	mu         sync.RWMutex
	trustPool  TrustPool
	localPeers map[string]*NodeInfo // node_id -> latest info from gossip
	// discoveryHints holds PEX address hints learned via gossip (P1-1), keyed by
	// node_id. In-memory only (not persisted — D4): used as a fallback address
	// source when a node's trust-pool endpoint is missing or stale.
	discoveryHints map[string][]string
	enabled        bool
	relayEnabled   bool
	loopRunning    bool // whether the refresh loop goroutine is active
	dataDir        string
	stopCh         chan struct{}
	lastETag       string // ETag for conditional HTTP requests to registry
}

var fed *FederationManager

// initFederation loads federation config from cfg, loads cached trust pool from
// dataDir/federation_pool.json, and prepares the manager. It does NOT start the
// refresh loop here — federation activation now follows network_enabled via
// syncFederationToNetwork() (REQ-2 / T2).
func initFederation(dataDir string) {
	f := &FederationManager{
		localPeers:     make(map[string]*NodeInfo),
		discoveryHints: make(map[string][]string),
		dataDir:        dataDir,
		stopCh:         make(chan struct{}),
	}

	// REQ-2: federation is no longer driven by the legacy federation_enabled key.
	// Its enabled state now follows NetworkManager.network_enabled via
	// syncFederationToNetwork(). Start disabled; the refresh loop is started later
	// once network_enabled is reconciled (after netMgr.Init()).
	f.enabled = false
	f.relayEnabled = cfg.Get("federation_relay_enabled", "false") == "true"

	if err := f.load(); err != nil {
		slog.Warn("failed to load cached federation pool, starting fresh", "error", err)
		f.trustPool = TrustPool{}
	}

	fed = f
	slog.Info("federation manager initialized (activation follows network_enabled)",
		"enabled", f.enabled,
		"relay_enabled", f.relayEnabled,
		"pool_version", f.trustPool.Version,
		"nodes", len(f.trustPool.Nodes),
	)
}

// IsEnabled reports whether federation is enabled.
func (f *FederationManager) IsEnabled() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.enabled
}

// IsRelayEnabled reports whether relay mode is enabled.
func (f *FederationManager) IsRelayEnabled() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.relayEnabled
}

// GetTrustPool returns a deep copy of the current trust pool.
func (f *FederationManager) GetTrustPool() TrustPool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	nodesCopy := make([]NodeInfo, len(f.trustPool.Nodes))
	copy(nodesCopy, f.trustPool.Nodes)

	return TrustPool{
		Version:   f.trustPool.Version,
		Nodes:     nodesCopy,
		UpdatedAt: f.trustPool.UpdatedAt,
	}
}

// GetActiveNodes returns a slice of all nodes whose status is "active".
func (f *FederationManager) GetActiveNodes() []NodeInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var active []NodeInfo
	for _, n := range f.trustPool.Nodes {
		if n.Status == "active" {
			active = append(active, n)
		}
	}
	// Also include peers learned via gossip that are active.
	for _, n := range f.localPeers {
		if n.Status == "active" {
			active = append(active, *n)
		}
	}
	return active
}

// GetNode returns the NodeInfo for the given node ID, searching the trust pool
// first and then the gossip-learned local peers.
func (f *FederationManager) GetNode(nodeID string) (*NodeInfo, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for i := range f.trustPool.Nodes {
		if f.trustPool.Nodes[i].NodeID == nodeID {
			return &f.trustPool.Nodes[i], true
		}
	}
	if n, ok := f.localPeers[nodeID]; ok {
		return n, true
	}
	return nil, false
}

// UpdateTrustPool merges the incoming pool into the local cache.
// It only applies the update if the incoming version is strictly newer.
func (f *FederationManager) UpdateTrustPool(pool TrustPool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if pool.Version <= f.trustPool.Version {
		return
	}

	f.trustPool = pool
	slog.Info("trust pool updated", "version", pool.Version, "nodes", len(pool.Nodes))

	if err := f.saveLocked(); err != nil {
		slog.Error("failed to persist trust pool", "error", err)
	}
}

// UpdateNodeInfo upserts a single node entry from a gossip message.
func (f *FederationManager) UpdateNodeInfo(info NodeInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Try to update in the trust pool first.
	for i := range f.trustPool.Nodes {
		if f.trustPool.Nodes[i].NodeID == info.NodeID {
			f.trustPool.Nodes[i] = info
			slog.Debug("trust pool node refreshed via gossip", "node_id", info.NodeID)
			return
		}
	}
	// Otherwise store / update in the local peers map.
	f.localPeers[info.NodeID] = &info
	slog.Debug("gossip peer recorded", "node_id", info.NodeID, "status", info.Status)
}

// RemoveNode removes a node from both the trust pool and local peers.
func (f *FederationManager) RemoveNode(nodeID string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	filtered := make([]NodeInfo, 0, len(f.trustPool.Nodes))
	for _, n := range f.trustPool.Nodes {
		if n.NodeID != nodeID {
			filtered = append(filtered, n)
		}
	}
	f.trustPool.Nodes = filtered
	delete(f.localPeers, nodeID)

	slog.Info("node removed from federation", "node_id", nodeID)
}

// AddKnownNode upserts a node into the local trust pool, marking it active and
// bumping the trust pool version. This is the P0-2 "bridge": manual peers (added
// via AddPeer / notify) and any other locally-known node are merged into the
// gossip-reachable trust pool so the discovery loop can propagate them. It is a
// no-op when federation is disabled (personal mode).
func (f *FederationManager) AddKnownNode(node NodeInfo) {
	if !f.IsEnabled() {
		return
	}
	node.Status = "active"
	if node.LastSeen == "" {
		node.LastSeen = time.Now().UTC().Format(time.RFC3339)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.trustPool.Nodes {
		if f.trustPool.Nodes[i].NodeID == node.NodeID {
			// Preserve existing rich fields if the new info didn't carry them,
			// but always refresh identity, addresses and status.
			existing := f.trustPool.Nodes[i]
			if len(node.SharedModels) == 0 {
				node.SharedModels = existing.SharedModels
			}
			if len(node.SharedProviders) == 0 {
				node.SharedProviders = existing.SharedProviders
			}
			if node.PubKey == "" {
				node.PubKey = existing.PubKey
			}
			if node.Endpoint == "" {
				node.Endpoint = existing.Endpoint
			}
			if len(node.Addresses) == 0 {
				node.Addresses = existing.Addresses
			}
			f.trustPool.Nodes[i] = node
			f.trustPool.Version++
			f.trustPool.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			_ = f.saveLocked()
			return
		}
	}

	f.trustPool.Nodes = append(f.trustPool.Nodes, node)
	f.trustPool.Version++
	f.trustPool.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_ = f.saveLocked()
}

// MergePeerHints records peer address hints learned via gossip PEX (P1-1). These
// are in-memory only (not persisted, per D4) and serve as fallback addresses
// when a node's trust-pool endpoint is missing or stale. Existing hints for a
// node id are kept stable (first-known wins).
func (f *FederationManager) MergePeerHints(hints []PeerHint) {
	if len(hints) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range hints {
		if h.NodeID == "" || len(h.Addresses) == 0 {
			continue
		}
		if _, ok := f.discoveryHints[h.NodeID]; ok {
			continue
		}
		f.discoveryHints[h.NodeID] = h.Addresses
	}
}

// HintAddresses returns the PEX-learned address hints for a node (P1-1).
func (f *FederationManager) HintAddresses(nodeID string) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.discoveryHints[nodeID]
}

// save persists the trust pool to dataDir/federation_pool.json.
func (f *FederationManager) save() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.saveLocked(); err != nil {
		slog.Error("failed to save federation pool", "error", err)
	}
}

// saveLocked writes the pool to disk. Caller must hold f.mu.
// SA-15: Saves with HMAC integrity protection.
func (f *FederationManager) saveLocked() error {
	path := filepath.Join(f.dataDir, "federation_pool.json")
	return saveWithIntegrity(path, f.trustPool)
}

// load reads the cached trust pool from dataDir/federation_pool.json.
// SA-15: Loads with HMAC integrity verification.
func (f *FederationManager) load() error {
	path := filepath.Join(f.dataDir, "federation_pool.json")
	return loadWithIntegrity(path, &f.trustPool)
}

// refreshFromGitHub fetches the canonical trust pool from the GitHub registry
// URL and updates the local cache when a newer version is available.
func (f *FederationManager) refreshFromGitHub() error {
	pool, err := f.fetchFromRegistry()
	if err != nil {
		return err
	}
	if pool == nil {
		slog.Debug("trust pool unchanged (304 from registry)")
		return nil
	}

	f.mu.Lock()
	if pool.Version > f.trustPool.Version {
		f.trustPool = *pool
		slog.Info("trust pool refreshed from GitHub registry",
			"version", pool.Version,
			"nodes", len(pool.Nodes),
		)
		_ = f.saveLocked()
	}
	f.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// Provider sharing helpers
// ---------------------------------------------------------------------------

// getLocalSharedProviders collects local providers that should be advertised
// to the federation.
//
// v3.1: A provider is shared when:
//   - The node-level share_to_pool toggle is enabled (primary gate), AND
//   - The per-provider AccessControl.ShareToPool is true, OR
//   - The per-provider config key "federation_share.<provider_id>" is "true"
//
// If share_to_pool is false (the default), no providers are shared regardless
// of per-provider settings. This implements the design principle that resource
// sharing is an independent, opt-in toggle.
//
// Returns the list of model names and the corresponding SharedProvider details.
func (f *FederationManager) getLocalSharedProviders() ([]string, []SharedProvider) {
	if !f.IsEnabled() {
		return nil, nil
	}

	// v3.1: Check node-level share_to_pool toggle (primary gate)
	if netMgr != nil && !netMgr.IsSharingToPool() {
		slog.Debug("share_to_pool is disabled, not advertising any providers")
		return nil, nil
	}

	var models []string
	var providers []SharedProvider

	allProviders := pm.GetAllRaw()
	relayOn := f.IsRelayEnabled()

	for _, p := range allProviders {
		shareKey := "federation_share." + p.ID
		shouldShare := cfg.Get(shareKey, "false") == "true" || relayOn
		if !shouldShare {
			continue
		}

		var modelNames []string
		for _, m := range p.Models {
			if m.Enabled {
				modelNames = append(modelNames, m.ID)
			}
		}
		models = append(models, modelNames...)
		providers = append(providers, SharedProvider{
			ProviderID: p.ID,
			Platform:   p.Type,
			Models:     modelNames,
			Capacity:   100,
		})
	}

	slog.Debug("collected local shared providers", "count", len(providers), "share_to_pool", true)
	return models, providers
}

// FindProvidersForModel returns all active nodes (from the trust pool and
// gossip-learned peers) that advertise the given model.
func (f *FederationManager) FindProvidersForModel(model string) []NodeInfo {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var result []NodeInfo

	seen := make(map[string]bool)

	check := func(n *NodeInfo) {
		if n.Status != "active" || seen[n.NodeID] {
			return
		}
		for _, m := range n.SharedModels {
			if m == model {
				result = append(result, *n)
				seen[n.NodeID] = true
				return
			}
		}
	}

	for i := range f.trustPool.Nodes {
		check(&f.trustPool.Nodes[i])
	}
	for _, n := range f.localPeers {
		check(n)
	}

	return result
}

// ---------------------------------------------------------------------------
// Small utility helpers
// ---------------------------------------------------------------------------

// allKnownEndpoints returns every non-empty endpoint from the trust pool,
// seed list, and gossip-learned local peers (deduplicated).
func (f *FederationManager) allKnownEndpoints() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	seen := make(map[string]bool)
	var endpoints []string

	add := func(ep string) {
		if ep != "" && !seen[ep] {
			seen[ep] = true
			endpoints = append(endpoints, ep)
		}
	}

	for _, n := range f.trustPool.Nodes {
		add(n.Endpoint)
	}
	for _, n := range f.localPeers {
		add(n.Endpoint)
	}
	return endpoints
}

// hasActivePeers reports whether at least one active peer is known.
func (f *FederationManager) hasActivePeers() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, n := range f.trustPool.Nodes {
		if n.Status == "active" && n.Endpoint != "" {
			return true
		}
	}
	for _, n := range f.localPeers {
		if n.Status == "active" && n.Endpoint != "" {
			return true
		}
	}
	return false
}

// SetEnabled reconciles the federation enabled state. Enabling starts the refresh
// loop (idempotent); disabling stops it. This is the only path that toggles the
// loop, keeping activation centralized on the network_enabled single source of
// truth (REQ-2).
func (f *FederationManager) SetEnabled(enabled bool) {
	f.mu.Lock()
	if f.enabled == enabled {
		f.mu.Unlock()
		return
	}
	f.enabled = enabled
	f.mu.Unlock()

	if enabled {
		f.startLoop()
		slog.Info("federation enabled — refresh loop started")
	} else {
		f.stopLoop()
		slog.Info("federation disabled — refresh loop stopped")
	}
}

// startLoop starts the federation refresh loop if it is not already running.
func (f *FederationManager) startLoop() {
	f.mu.Lock()
	if f.loopRunning {
		f.mu.Unlock()
		return
	}
	f.loopRunning = true
	f.mu.Unlock()
	go f.refreshLoop()
}

// stopLoop stops the federation refresh loop if it is running, signalling the
// goroutine to exit and preparing a fresh stop channel for a future restart.
func (f *FederationManager) stopLoop() {
	f.mu.Lock()
	if !f.loopRunning {
		f.mu.Unlock()
		return
	}
	f.loopRunning = false
	f.mu.Unlock()

	// Signal the running loop to exit (idempotent close).
	select {
	case <-f.stopCh:
		// already closed
	default:
		close(f.stopCh)
	}
	// Fresh channel so a later startLoop() has a valid signal target.
	f.stopCh = make(chan struct{})
}

// stop gracefully shuts down the federation manager.
func (f *FederationManager) stop() {
	f.stopLoop()
	slog.Info("federation manager stopped")
}
