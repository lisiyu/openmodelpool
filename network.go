package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ============================================================
// Network Mode & Data Models
// ============================================================

type NetworkMode string

const (
	NetworkModePersonal NetworkMode = "personal" // 个人模式（默认）
	NetworkModeShared   NetworkMode = "shared"   // 共享网络模式
)

const (
	p2pNodeIDPrefix = "mmx-"
	maxRelayHops    = 3
	routeTTL        = 10 * time.Minute // 路由条目 TTL
	refreshInterval = 5 * time.Minute  // 地址刷新间隔
)

// ContribRecord tracks individual contribution events (Phase 2)
type ContribRecord struct {
	Timestamp  string `json:"timestamp"`
	TokensUsed int64  `json:"tokens_used"`
	Requests   int64  `json:"requests"`
	FromNodeID string `json:"from_node_id"`
}

// ShareBoundaryConfig defines the contribution boundary for sharing idle quota
// to the shared pool. Introduced in Phase 1 slice ① as the schema foundation for
// REQ-12 (enforcement lands in a later slice). It is persisted but not yet enforced.
type ShareBoundaryConfig struct {
	DailyContribCap int64    `json:"daily_contrib_cap"` // 每日贡献上限(Token)：0 = 不限制
	ShareIdleOnly   bool     `json:"share_idle_only"`   // 仅共享空闲额度：默认 true
	ModelWhitelist  []string `json:"model_whitelist"`   // 模型/Provider 白名单：空 = 全部
}

// NetworkConfig holds all shared network configuration
type NetworkConfig struct {
	Mode              NetworkMode     `json:"mode"`
	ConsentAccepted   bool            `json:"consent_accepted"`
	ConsentTime       string          `json:"consent_time"`
	NodeName          string          `json:"node_name"`
	NodeID            string          `json:"node_id"`
	BootstrapNodes    []string        `json:"bootstrap_nodes"`
	SharedModels      []string        `json:"shared_models"`
	MaxDailyRequests  int             `json:"max_daily_requests"`
	ContribPoints     int64           `json:"contrib_points"`
	ContribRecords    []ContribRecord `json:"contrib_records"`
	Peers             []PeerInfo      `json:"peers"`
	Stats             NetworkStats    `json:"stats"`
	Addresses         []string        `json:"addresses"`
	LastAddressUpdate string          `json:"last_address_update"`
	RelayEnabled      bool            `json:"relay_enabled"`

	// v3.2: Independent network_enabled toggle — separate from Mode.
	// Controls whether this node participates in the shared network at all.
	// Three-level model: Personal (network_enabled=false) → Network (network_enabled=true, share_to_pool=false) → Shared Peer (both true)
	NetworkEnabled bool `json:"network_enabled"`

	// v3.1: Unified Peer Model — share_to_pool toggle
	// Controls whether this node contributes its providers to the shared pool.
	// Default: false — nodes join the network by default but do NOT share resources
	// unless explicitly opted in. This is independent of network participation.
	ShareToPool bool `json:"share_to_pool"`

	// v3.1: Peer capabilities (replaces preset node types)
	Capabilities PeerCapabilities `json:"capabilities"`

	// v2.0 Quota Allocation
	QuotaAllocation QuotaAllocation `json:"quota_allocation"`

	// v3.2: REQ-12 foundation — contribution boundary for the shared pool.
	// Persisted in slice ①; enforcement arrives in a later slice.
	ShareBoundary ShareBoundaryConfig `json:"share_boundary"`
}

// PeerInfo represents a connected peer in the shared network
type PeerInfo struct {
	NodeID       string           `json:"node_id"`
	Name         string           `json:"name"`
	Region       string           `json:"region"`
	Models       []string         `json:"models"`
	Status       string           `json:"status"`
	LastSeen     string           `json:"last_seen"`
	TrustScore   float64          `json:"trust_score"`
	JoinedAt     string           `json:"joined_at"`
	Addresses    []string         `json:"addresses,omitempty"`
	Unlocked     bool             `json:"unlocked"`
	Capabilities PeerCapabilities `json:"capabilities,omitempty"` // v3.1: capability declarations
	ShareToPool  bool             `json:"share_to_pool"`          // v3.1: whether this peer shares resources
	// PubKey is the peer's ed25519 public key (base64). Populated when the peer
	// is learned via the signed /api/network/peers/notify channel (P0-1), used
	// later to verify its gossip signatures (best-effort; may be empty for
	// manually-added peers whose key was not exchanged — see R6).
	PubKey string `json:"pub_key,omitempty"`
}

// NetworkStats holds network statistics
type NetworkStats struct {
	TotalPeers        int    `json:"total_peers"`
	OnlinePeers       int    `json:"online_peers"`
	TotalModelsShared int    `json:"total_models_shared"`
	RequestsRelayed   int64  `json:"requests_relayed"`
	RequestsReceived  int64  `json:"requests_received"`
	RelaySuccess      int64  `json:"relay_success"`
	RelayFailed       int64  `json:"relay_failed"`
	UptimeSeconds     int64  `json:"uptime_seconds"`
	JoinedAt          string `json:"joined_at"`
}

// DisclaimerSection for the disclaimer endpoint
type DisclaimerSection struct {
	Heading string `json:"heading"`
	Content string `json:"content"`
	IsRisk  bool   `json:"is_risk,omitempty"`
}

// DisclaimerResponse is the response for the disclaimer endpoint
type DisclaimerResponse struct {
	Title            string              `json:"title"`
	Sections         []DisclaimerSection `json:"sections"`
	ConfirmationText string              `json:"confirmation_text"`
}

// ============================================================
// Route Table — Phase 1 simplified DHT (replaced by Kademlia in Phase 2)
// ============================================================

// RouteEntry maps a NodeID to its reachable addresses
type RouteEntry struct {
	NodeID    string    `json:"node_id"`
	NodeName  string    `json:"node_name"`
	Addresses []string  `json:"addresses"`
	Status    string    `json:"status"` // online/offline/degraded/unreachable

	// Gateway routing fields
	Models    []string  `json:"models,omitempty"`
	LatencyMS float64   `json:"latency_ms,omitempty"`
	LoadScore float64   `json:"load_score,omitempty"`
	LastSeen  time.Time `json:"last_seen,omitempty"`

	// AddrMan fields (§7.8.4)
	FailCount   int     `json:"fail_count,omitempty"`
	UptimeScore float64 `json:"uptime_score,omitempty"`
	IsGateway   bool    `json:"is_gateway,omitempty"`
	IsSeed      bool    `json:"is_seed,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`

	// NAT traversal reachability (§7.5): how to punch a direct UDP channel to
	// this node. Populated from the node's own STUN discovery and shared over
	// the federation so peers can aim their hole-punch at the right port.
	ReflexiveUDP string `json:"reflexive_udp,omitempty"`
	NATType      string `json:"nat_type,omitempty"`
}

// RouteTable is a simplified DHT routing table (Phase 1)
// Phase 2 will replace this with libp2p Kademlia
type RouteTable struct {
	mu      sync.RWMutex
	entries map[string]*RouteEntry
}

func initRouteTable() *RouteTable {
	return &RouteTable{entries: make(map[string]*RouteEntry)}
}

// initNodeRegistry initializes the package-level on-disk node registry and
// restores any previously persisted peers into the in-memory route table. This
// gives a cold start known nodes before gossip or GitHub bootstrap completes,
// improving restart resilience. It is safe to call once at startup.
func initNodeRegistry(dataDir string) {
	nodeRegistry = NewNodeRegistry(filepath.Join(dataDir, ".nodes"))
	loaded, err := nodeRegistry.LoadAll()
	if err != nil {
		slog.Warn("failed to load persisted node registry", "error", err)
		return
	}
	if len(loaded) == 0 {
		return
	}
	for _, e := range loaded {
		routeTable.UpsertEntry(e)
	}
	slog.Info("restored known nodes from local registry", "count", len(loaded))
}

// Put adds or updates a route entry
func (rt *RouteTable) Put(nodeID, nodeName string, addresses []string) {
	rt.mu.Lock()
	e := &RouteEntry{
		NodeID:    nodeID,
		NodeName:  nodeName,
		Addresses: addresses,
		Status:    "online",
		UpdatedAt: time.Now(),
	}
	rt.entries[nodeID] = e
	rt.mu.Unlock()
	// Mirror to the on-disk registry. No-op until nodeRegistry is initialized
	// (e.g. in unit tests it stays nil and routing behavior is untouched).
	if nodeRegistry != nil {
		nodeRegistry.SaveNode(e)
	}
}

// UpsertEntry stores a fully-formed RouteEntry without deriving fresh fields. It
// is used during cold-start restore to preserve rich metadata (models, latency,
// last-seen) that Put() would otherwise discard. It does NOT persist because the
// entry already originates from the on-disk registry.
func (rt *RouteTable) UpsertEntry(e *RouteEntry) {
	if e == nil {
		return
	}
	rt.mu.Lock()
	rt.entries[e.NodeID] = e
	rt.mu.Unlock()
}

// Get looks up a route entry by NodeID
func (rt *RouteTable) Get(nodeID string) *RouteEntry {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	e, ok := rt.entries[nodeID]
	if !ok {
		return nil
	}
	// Check TTL
	if time.Since(e.UpdatedAt) > routeTTL {
		return nil // expired
	}
	// Return copy
	cp := *e
	addrs := make([]string, len(e.Addresses))
	copy(addrs, e.Addresses)
	cp.Addresses = addrs
	return &cp
}

// Remove deletes a route entry
func (rt *RouteTable) Remove(nodeID string) {
	rt.mu.Lock()
	delete(rt.entries, nodeID)
	rt.mu.Unlock()
	if nodeRegistry != nil {
		nodeRegistry.RemoveNode(nodeID)
	}
}

// GetAll returns all non-expired entries
func (rt *RouteTable) GetAll() []*RouteEntry {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	now := time.Now()
	result := make([]*RouteEntry, 0, len(rt.entries))
	for _, e := range rt.entries {
		if now.Sub(e.UpdatedAt) > routeTTL {
			continue
		}
		cp := *e
		addrs := make([]string, len(e.Addresses))
		copy(addrs, e.Addresses)
		cp.Addresses = addrs
		result = append(result, &cp)
	}
	return result
}

// PurgeExpired removes stale entries
func (rt *RouteTable) PurgeExpired() int {
	rt.mu.Lock()
	now := time.Now()
	purged := 0
	var removed []string
	for id, e := range rt.entries {
		if now.Sub(e.UpdatedAt) > routeTTL {
			delete(rt.entries, id)
			purged++
			removed = append(removed, id)
		}
	}
	rt.mu.Unlock()
	// Keep the on-disk registry in sync with the in-memory purge so expired
	// nodes do not reappear on the next cold start.
	if nodeRegistry != nil {
		for _, id := range removed {
			nodeRegistry.RemoveNode(id)
		}
	}
	return purged
}

// Count returns the number of active entries
func (rt *RouteTable) Count() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.entries)
}

// GetByModel returns all non-expired entries that can serve the specified model.
// If an entry has no Models list, it's considered able to serve any model.
func (rt *RouteTable) GetByModel(model string) []RouteEntry {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	now := time.Now()
	result := make([]RouteEntry, 0)
	for _, e := range rt.entries {
		if now.Sub(e.UpdatedAt) > routeTTL {
			continue
		}
		// If Models is empty/nil, the node can serve any model
		if len(e.Models) == 0 {
			cp := *e
			result = append(result, cp)
			continue
		}
		// Check if model is in the list
		for _, m := range e.Models {
			if m == model {
				cp := *e
				result = append(result, cp)
				break
			}
		}
	}
	return result
}

// SelectBestNode selects the optimal node for a given model based on latency, load, and contribution ratio.
// Scoring formula: LatencyMS * 0.4 + LoadScore * 1000 * 0.3 + (1/ContribRatio) * 500 * 0.3
// Returns nil if no suitable node is found.
func (rt *RouteTable) SelectBestNode(model string) *RouteEntry {
	candidates := rt.GetByModel(model)
	if len(candidates) == 0 {
		return nil
	}

	type scored struct {
		entry *RouteEntry
		score float64
	}

	scored_list := make([]scored, 0, len(candidates))
	for i := range candidates {
		e := &candidates[i]
		// Contribution ratio: use TrustScore as proxy, default 0.5 if not set
		contribRatio := 0.5
		if contribRatio <= 0 {
			contribRatio = 0.1
		}

		// Score: lower is better
		score := e.LatencyMS*0.4 + e.LoadScore*1000*0.3 + (1.0/contribRatio)*500*0.3
		scored_list = append(scored_list, scored{entry: e, score: score})
	}

	// Find minimum score
	bestIdx := 0
	for i := 1; i < len(scored_list); i++ {
		if scored_list[i].score < scored_list[bestIdx].score {
			bestIdx = i
		}
	}

	return scored_list[bestIdx].entry
}

// RecordSuccess marks a successful interaction with a node, resetting FailCount
// and updating UptimeScore and LatencyMS.
func (rt *RouteTable) RecordSuccess(nodeID string, latencyMs float64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	e, ok := rt.entries[nodeID]
	if !ok {
		return
	}
	e.FailCount = 0
	e.UptimeScore = e.UptimeScore*0.9 + 1.0*0.1
	e.LatencyMS = e.LatencyMS*0.8 + latencyMs*0.2
	e.LastSeen = time.Now()
	e.Status = "online"
	e.UpdatedAt = time.Now()
}

// RecordFail increments FailCount for a node. When FailCount >= 3, marks it
// unreachable. Returns the new FailCount.
func (rt *RouteTable) RecordFail(nodeID string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	e, ok := rt.entries[nodeID]
	if !ok {
		return 0
	}
	e.FailCount++
	e.UptimeScore = e.UptimeScore * 0.8
	if e.FailCount >= 3 {
		e.Status = "unreachable"
	}
	e.UpdatedAt = time.Now()
	return e.FailCount
}

// GetGateways returns all entries marked as Gateway.
func (rt *RouteTable) GetGateways() []*RouteEntry {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var result []*RouteEntry
	for _, e := range rt.entries {
		if e.IsGateway && e.Status != "unreachable" {
			cp := *e
			result = append(result, &cp)
		}
	}
	return result
}

// GetSeeds returns all entries marked as Seed.
func (rt *RouteTable) GetSeeds() []*RouteEntry {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var result []*RouteEntry
	for _, e := range rt.entries {
		if e.IsSeed {
			cp := *e
			result = append(result, &cp)
		}
	}
	return result
}

// PurgeStale removes entries that have not been seen for 7 days and
// increments FailCount for entries not seen in 30 minutes.
func (rt *RouteTable) PurgeStale() (removed int, failed int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	now := time.Now()
	for id, e := range rt.entries {
		if e.IsSeed {
			continue
		}
		if now.Sub(e.LastSeen) > 7*24*time.Hour {
			delete(rt.entries, id)
			removed++
			continue
		}
		if now.Sub(e.LastSeen) > 30*time.Minute && e.Status != "unreachable" {
			e.FailCount++
			if e.FailCount >= 3 {
				e.Status = "unreachable"
			}
			failed++
		}
	}
	return
}

// MarkGateway sets or clears the IsGateway flag for a node.
func (rt *RouteTable) MarkGateway(nodeID string, isGateway bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if e, ok := rt.entries[nodeID]; ok {
		e.IsGateway = isGateway
		e.UpdatedAt = time.Now()
	}
}

// MarkSeed sets the IsSeed flag for a node.
func (rt *RouteTable) MarkSeed(nodeID string, isSeed bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if e, ok := rt.entries[nodeID]; ok {
		e.IsSeed = isSeed
		e.UpdatedAt = time.Now()
	}
}

// ============================================================
// NetworkManager
// ============================================================

type NetworkManager struct {
	mu          sync.RWMutex
	config      NetworkConfig
	dataPath    string
	startTime   time.Time
	stopRefresh chan struct{}
}

func initNetworkManager(dataDir string) {
	netMgr = &NetworkManager{
		dataPath: filepath.Join(dataDir, "network.json"),
		config: NetworkConfig{
			Mode:             NetworkModePersonal,
			NetworkEnabled:   false, // §4.2 Level 1: Personal Mode by default
			ConsentAccepted:  false,
			BootstrapNodes:   []string{},
			SharedModels:     []string{},
			Peers:            []PeerInfo{},
			MaxDailyRequests: 1000,
			Addresses:        []string{},
			RelayEnabled:     true, // default on when in shared mode
			// REQ-12 foundation: conservative defaults — only share idle quota,
			// no daily cap, all models (matches PRD Q6).
			ShareBoundary: ShareBoundaryConfig{
				DailyContribCap: 0,
				ShareIdleOnly:   true,
				ModelWhitelist:  []string{},
			},
		},
	}
	netMgr.load()
	routeTable = initRouteTable()
	// Restore known peers from the on-disk registry so a cold start has nodes
	// before gossip / GitHub bootstrap completes (pure-incremental extension).
	initNodeRegistry(dataDir)

	// Re-register self in route table if we have addresses
	if netMgr.config.NodeID != "" && len(netMgr.config.Addresses) > 0 {
		routeTable.Put(netMgr.config.NodeID, netMgr.config.NodeName, netMgr.config.Addresses)
	}

	slog.Info("network manager initialized", "mode", netMgr.config.Mode, "node_id", netMgr.config.NodeID)

	if netMgr.config.NetworkEnabled {
		go routeTableHealthLoop()
	}
}

func routeTableHealthLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	slog.Info("route table health check loop started", "interval", "30m")
	for {
		select {
		case <-ticker.C:
			if routeTable == nil {
				return
			}
			removed, failed := routeTable.PurgeStale()
			if removed > 0 || failed > 0 {
				slog.Info("route table health check", "removed_7d", removed, "failed_30m", failed)
			}
		}
	}
}

func (nm *NetworkManager) load() {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	b, err := os.ReadFile(nm.dataPath)
	if err != nil {
		return
	}
	json.Unmarshal(b, &nm.config)
	if nm.config.BootstrapNodes == nil {
		nm.config.BootstrapNodes = []string{}
	}
	if nm.config.SharedModels == nil {
		nm.config.SharedModels = []string{}
	}
	if nm.config.Peers == nil {
		nm.config.Peers = []PeerInfo{}
	}
	if nm.config.Addresses == nil {
		nm.config.Addresses = []string{}
	}
	// v2.0: Initialize quota allocation with defaults if not set
	if nm.config.QuotaAllocation.GuestKeyPercent == 0 && nm.config.QuotaAllocation.PublicKeyPercent == 0 {
		nm.config.QuotaAllocation = DefaultQuotaAllocation()
	}
	// v3.2: Backward compat — old configs may not have network_enabled.
	// If Mode is "shared", infer network_enabled = true.
	if nm.config.Mode == NetworkModeShared && !nm.config.NetworkEnabled {
		nm.config.NetworkEnabled = true
	}

	// REQ-2 升级迁移：旧全局键 federation_enabled=true 且当前未入网
	// ⇒ 收敛为单一真值源 network_enabled=true，并清除旧键避免状态漂移。
	if cfg.Get("federation_enabled", "false") == "true" && !nm.config.NetworkEnabled {
		nm.config.NetworkEnabled = true
		nm.config.Mode = NetworkModeShared
		cfg.Set("federation_enabled", "false")
		cfg.save()
		slog.Info("migrated legacy federation_enabled=true → network_enabled=true")
	}

	// Ensure ShareBoundary slices are never nil (clean JSON round-trip).
	if nm.config.ShareBoundary.ModelWhitelist == nil {
		nm.config.ShareBoundary.ModelWhitelist = []string{}
	}
}

func (nm *NetworkManager) save() {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	nm.doSave()
}

func (nm *NetworkManager) doSave() {
	if err := os.MkdirAll(filepath.Dir(nm.dataPath), 0700); err != nil {
		slog.Error("failed to create data directory", "error", err)
	}
	b, _ := json.MarshalIndent(nm.config, "", "  ")
	atomicWriteFile(nm.dataPath, b, 0600)
}

// Init loads config and activates network subsystems only when network_enabled
// is true. In personal mode (network_enabled=false) it deliberately does NOT
// derive a NodeID nor start the refresh loop — keeping the node with zero
// extra outbound connections (REQ-1 / REQ-2 startup guard, T6).
func (nm *NetworkManager) Init() error {
	nm.load()

	// Only derive an identity and bring network subsystems online when the
	// single source of truth (network_enabled) is set.
	if nm.config.NetworkEnabled {
		if nm.config.NodeID == "" && node != nil && node.IsInitialized() {
			nm.config.NodeID = DeriveP2PNodeID()
			nm.doSave()
			slog.Info("derived P2P NodeID", "node_id", nm.config.NodeID)
		}
		nm.activateNetwork()
		slog.Info("shared network mode active", "node_id", nm.config.NodeID)
	} else {
		slog.Info("personal mode (network disabled) — no network subsystems started")
	}
	return nil
}

// DeriveP2PNodeID returns this node's canonical P2P NodeID.
//
// REQ-S2-1 (D1 fix): it must be byte-for-byte identical to node.NodeID(),
// i.e. the 68-character form "mmx-" + hex(Ed25519 public key). The previous
// implementation used hex(sha256(pubkey)[:16]) which produced a 36-character
// value that diverged from the identity object. Unifying on node.NodeID()
// makes config.NodeID, the broadcast value and the identity object all share
// the same 68-character "mmx-" string (REQ-S2-3 three-way invariant).
func DeriveP2PNodeID() string {
	if node == nil {
		return ""
	}
	return node.NodeID()
}

// canonicalNodeID returns the authoritative Node ID from the identity object.
// This is the single source of truth (68-char mmx- form). The persisted
// config.NodeID is only a cache that must always equal this value
// (REQ-S2-3 invariant: config.NodeID == node.NodeID() == GetInfo().NodeID()).
func canonicalNodeID() string {
	if node == nil {
		return ""
	}
	return node.NodeID()
}

// assertNodeIDInvariant verifies that config.NodeID matches the canonical
// identity Node ID. If they diverge (e.g. a stale cached value), it logs a
// warning and returns the canonical value so callers always broadcast/serve
// the truth rather than a corrupted value. It never panics.
func (nm *NetworkManager) assertNodeIDInvariant() string {
	canonical := canonicalNodeID()
	if canonical == "" {
		// Personal mode (no identity): nothing to assert.
		return ""
	}
	if nm.config.NodeID != canonical {
		slog.Warn("node id invariant violated: config.NodeID diverges from canonical identity NodeID; using canonical for broadcast",
			"cached", nm.config.NodeID, "canonical", canonical)
	}
	return canonical
}

// registerSelf registers this node's addresses in the route table
func (nm *NetworkManager) registerSelf() {
	nm.mu.RLock()
	nodeID := nm.config.NodeID
	nodeName := nm.config.NodeName
	nm.mu.RUnlock()

	if nodeID == "" {
		return
	}
	addresses := nm.collectAddresses()

	nm.mu.Lock()
	nm.config.Addresses = addresses
	nm.config.LastAddressUpdate = time.Now().Format(time.RFC3339)
	nm.mu.Unlock()

	routeTable.Put(nodeID, nodeName, addresses)
	slog.Info("registered self in route table", "node_id", nodeID, "addresses", addresses)
}

// collectAddresses gathers all reachable URLs for this node.
// Includes Cloudflare tunnel URL, custom domain, public IP (HTTPS), and localhost (HTTPS).
func (nm *NetworkManager) collectAddresses() []string {
	var addrs []string

	// 1. Cloudflare tunnel URL (already HTTPS)
	if u := cfg.Get("tunnel_url", ""); u != "" {
		addrs = append(addrs, u)
	}

	// 2. Custom domain (HTTPS)
	if d := cfg.Get("tunnel_domain", ""); d != "" {
		addrs = append(addrs, "https://"+d)
	}

	// 3. Public IP detection (HTTPS with self-signed cert)
	if pubIP := detectPublicIP(); pubIP != "" {
		port := cfg.Get("service_port", "8000")
		pubAddr := fmt.Sprintf("https://%s:%s", pubIP, port)
		// Avoid duplicate if already present
		found := false
		for _, a := range addrs {
			if a == pubAddr {
				found = true
				break
			}
		}
		if !found {
			addrs = append(addrs, pubAddr)
		}
	}

	// 4. Localhost (HTTPS)
	port := cfg.Get("service_port", "8000")
	addrs = append(addrs, fmt.Sprintf("https://localhost:%s", port))

	return addrs
}

// startRefreshLoop periodically refreshes addresses and purges stale routes
func (nm *NetworkManager) startRefreshLoop() {
	stopCh := make(chan struct{})
	nm.stopRefresh = stopCh
	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				nm.registerSelf()
				purged := routeTable.PurgeExpired()
				if purged > 0 {
					slog.Debug("purged expired route entries", "count", purged)
				}
			case <-stopCh:
				return
			}
		}
	}()
}

func (nm *NetworkManager) stopRefreshLoop() {
	if nm.stopRefresh != nil {
		close(nm.stopRefresh)
		nm.stopRefresh = nil
	}
}

// EnableSharedNetwork activates the shared network. Requires prior consent
// (recorded via /api/network/consent) AND a fully prepared identity: the node
// must have been initialized (generated or restored from a mnemonic) and the
// user must have confirmed backup. After preparing identity it funnels through
// activateNetwork() so the refresh loop and federation manager stay in sync
// with the single source of truth (REQ-2 / T3).
//
// REQ-S2-2/3: the strict guard below enforces the backup-confirmation gate.
// The old behavior of auto-generating a mnemonic inside this call has been
// removed — identity generation/confirmation is now driven explicitly by the
// frontend wizard via /api/network/identity/* endpoints before enable.
func (nm *NetworkManager) EnableSharedNetwork() error {
	nm.mu.Lock()

	if !nm.config.ConsentAccepted {
		nm.mu.Unlock()
		return fmt.Errorf("请先阅读并同意共享网络须知")
	}

	// REQ-S2-2/3: strict identity guard. Network capability is granted only when
	// a mnemonic-based identity exists AND the user has confirmed backup.
	if node == nil || !node.IsInitialized() {
		nm.mu.Unlock()
		return fmt.Errorf("请先生成或恢复助记词以创建节点身份")
	}
	if !node.IsBackupConfirmed() {
		nm.mu.Unlock()
		return fmt.Errorf("请先在备份向导中确认助记词已安全备份，再启用共享网络")
	}

	// REQ-S2-5: legacy mm- format migration (non-blocking safeguard). If the
	// loaded identity still uses the old mm- prefix, rewrite it to the canonical
	// mmx- form and persist before we derive the NodeID.
	if node.NeedsMigration() {
		slog.Warn("legacy mm- node identity detected; migrating to mmx- format", "node_id", node.NodeID())
		if err := node.Migrate(); err != nil {
			nm.mu.Unlock()
			return fmt.Errorf("旧格式身份迁移失败: %w", err)
		}
	}

	// Write the canonical NodeID (68-char mmx- form) from the identity object.
	if nm.config.NodeID == "" {
		nm.config.NodeID = DeriveP2PNodeID()
	}

	if nm.config.NodeName == "" {
		suffix := nm.config.NodeID
		if len(suffix) > 8 {
			suffix = suffix[4:8]
		}
		nm.config.NodeName = "node-" + suffix
	}

	nm.config.Mode = NetworkModeShared
	nm.config.NetworkEnabled = true // §4.2: Level 2 — Network Mode (join network, don't share by default)
	nm.config.Stats.JoinedAt = time.Now().Format(time.RFC3339)
	nm.mu.Unlock()

	// v2.0: Public key is now a fixed constant, no generation needed.
	nm.doSave()
	nm.activateNetwork()

	slog.Info("shared network enabled", "node_id", nm.config.NodeID, "name", nm.config.NodeName)
	return nil
}

// DisableSharedNetwork returns to personal mode. Funnels through
// deactivateNetwork() so the federation manager and refresh loop are torn down
// consistently (REQ-2 / T3).
func (nm *NetworkManager) DisableSharedNetwork() error {
	nm.mu.Lock()

	nm.stopRefreshLoop()

	if nm.config.NodeID != "" {
		routeTable.Remove(nm.config.NodeID)
	}

	nm.config.Mode = NetworkModePersonal
	nm.config.NetworkEnabled = false // §4.2: Level 1 — Personal Mode
	nm.config.ShareToPool = false    // §4.2: disable sharing when leaving network
	nm.config.Peers = []PeerInfo{}
	nm.config.Stats.OnlinePeers = 0
	nm.config.Addresses = []string{}
	nm.mu.Unlock()

	nm.doSave()
	nm.deactivateNetwork()

	slog.Info("shared network disabled")
	return nil
}

// RecordConsent records user consent
func (nm *NetworkManager) RecordConsent() error {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.config.ConsentAccepted = true
	nm.config.ConsentTime = time.Now().Format(time.RFC3339)
	nm.doSave()
	return nil
}

// GetStatus returns current network status (thread-safe, read-only copy)
func (nm *NetworkManager) GetStatus() map[string]any {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	uptime := int64(0)
	if nm.config.Mode == NetworkModeShared && !nm.startTime.IsZero() {
		uptime = int64(time.Since(nm.startTime).Seconds())
	}

	// REQ-S2-3: node_id uses the canonical identity value (68-char mmx- form).
	// When an identity is initialized we prefer the canonical NodeID over the
	// cached config.NodeID so callers/广播 always see the single source of truth.
	nodeID := nm.config.NodeID
	backupConfirmed := false
	needsMigration := false
	identityInitialized := false
	hasMnemonic := false
	if node != nil {
		if node.IsInitialized() {
			nodeID = canonicalNodeID()
			identityInitialized = true
		}
		backupConfirmed = node.IsBackupConfirmed()
		needsMigration = node.NeedsMigration()
		hasMnemonic = node.HasMnemonic()
	}

	// Build relay consumer URL hint
	relayURL := ""
	if nodeID != "" && len(nm.config.Addresses) > 0 {
		// Pick the first public address as relay hint
		for _, a := range nm.config.Addresses {
			if strings.HasPrefix(a, "https://") {
				relayURL = fmt.Sprintf("%s/network/%s/v1", a, nodeID)
				break
			}
		}
		if relayURL == "" && len(nm.config.Addresses) > 0 {
			relayURL = fmt.Sprintf("%s/network/%s/v1", nm.config.Addresses[0], nodeID)
		}
	}

	status := map[string]any{
		"mode":               nm.config.Mode,
		"consent_accepted":   nm.config.ConsentAccepted,
		"consent_time":       nm.config.ConsentTime,
		"node_name":          nm.config.NodeName,
		"node_id":            nodeID,
		"shared_models":      nm.config.SharedModels,
		"max_daily_requests": nm.config.MaxDailyRequests,
		"contrib_points":     nm.config.ContribPoints,
		"bootstrap_nodes":    nm.config.BootstrapNodes,
		"stats":              nm.config.Stats,
		"peers_count":        len(nm.config.Peers),
		"addresses":          nm.config.Addresses,
		"uptime_seconds":     uptime,
		"relay_enabled":      nm.config.RelayEnabled,
		"relay_consumer_url": relayURL,
		"route_table_size":   routeTable.Count(),

		// v3.2: Three-level state model (§4.2)
		"network_enabled": nm.config.NetworkEnabled,

		// v3.1: Unified Peer Model
		"share_to_pool": nm.config.ShareToPool,
		"capabilities":  nm.config.Capabilities,

		// v2.0 Quota Allocation
		"quota_allocation": nm.config.QuotaAllocation,

		// v3.2: REQ-12 foundation — contribution boundary (persisted, not yet enforced)
		"share_boundary": nm.config.ShareBoundary,

		// REQ-S2-3/5/6: identity-state fields exposed to the admin UI
		"identity_initialized": identityInitialized,
		"has_mnemonic":         hasMnemonic,
		"backup_confirmed":     backupConfirmed,
		"needs_migration":      needsMigration,
	}

	// 治理（P2-1）：软提醒，绝不强制。个人模式下若节点仍有闲置的「自有额度」，
	// 温和建议（非强制）加入共享网络，把闲置额度变成社区公共池资源。
	// 注意：社区免费池（预设公共上游）始终开箱即用，与此软提醒无关。
	if !nm.config.NetworkEnabled {
		if iq := checkIdleQuota(); iq.ShouldNotify {
			status["shared_network_suggestion"] = map[string]any{
				"should_join": true,
				"reason":      iq.Message,
				"idle_quota":  iq.RemainingQuota,
			}
		}
	}
	return status
}

// GetNetworkStats returns aggregated network statistics including provider and consumer data.
func (nm *NetworkManager) GetNetworkStats() map[string]any {
	nm.mu.RLock()
	defer nm.mu.RUnlock()

	st := nm.config.Stats
	totalReqs := st.RequestsRelayed + st.RequestsReceived
	successRate := 0.0
	if totalReqs > 0 {
		successRate = float64(st.RelaySuccess) / float64(totalReqs)
	}

	uptime := int64(0)
	if nm.config.Mode == NetworkModeShared && !nm.startTime.IsZero() {
		uptime = int64(time.Since(nm.startTime).Seconds())
	}

	// Count active consumers
	activeUsers := 0
	if multiUser != nil {
		for _, c := range multiUser.ListConsumers() {
			if c.Enabled {
				activeUsers++
			}
		}
	}

	// Calculate total quota from all providers
	var totalQuota int64
	if pm != nil {
		for _, p := range pm.GetAllRaw() {
			if !p.Enabled {
				continue
			}
			if p.TokenLimit > 0 {
				totalQuota += p.TokenLimit
			}
			for _, k := range p.APIKeys {
				if k.Enabled && k.Quota > 0 {
					totalQuota += k.Quota
				}
			}
		}
	}

	return map[string]any{
		"total_nodes":    len(nm.config.Peers) + 1, // peers + self
		"online_nodes":   nm.countOnlinePeers(),
		"active_users":   activeUsers,
		"total_requests": totalReqs,
		"relay_requests": st.RequestsRelayed,
		"success_rate":   successRate,
		"total_quota":    totalQuota,
		"models_shared":  st.TotalModelsShared,
		"uptime":         uptime,
	}
}

// countOnlinePeers returns the number of peers seen within the last 5 minutes.
func (nm *NetworkManager) countOnlinePeers() int {
	cutoff := time.Now().Add(-5 * time.Minute)
	count := 0
	for _, p := range nm.config.Peers {
		if t, err := time.Parse(time.RFC3339, p.LastSeen); err == nil && t.After(cutoff) {
			count++
		}
	}
	return count
}

func (nm *NetworkManager) IsSharedMode() bool {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	return nm.config.Mode == NetworkModeShared
}

func (nm *NetworkManager) GetNodeID() string {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	return nm.config.NodeID
}

// AddPeer adds/updates a peer and registers in route table
func (nm *NetworkManager) AddPeer(peer PeerInfo) error {
	// P0-2 / D2: best-effort backfill of the peer's ed25519 public key from its
	// authoritative /api/node/pubkey endpoint when the caller didn't supply one
	// (e.g. a manual UI add only knows the address). Done BEFORE acquiring the
	// lock so we never block the network manager on a network call.
	if peer.PubKey == "" && len(peer.Addresses) > 0 {
		if pk := fetchNodePubKey(peer.Addresses[0]); pk != "" {
			peer.PubKey = pk
		}
	}
	nm.mu.Lock()
	defer nm.mu.Unlock()
	if nm.config.Mode != NetworkModeShared {
		return fmt.Errorf("shared network not active")
	}
	for i, p := range nm.config.Peers {
		if p.NodeID == peer.NodeID {
			// Preserve unlock state
			peer.Unlocked = p.Unlocked
			nm.config.Peers[i] = peer
			nm.doSave()
			if len(peer.Addresses) > 0 {
				routeTable.Put(peer.NodeID, peer.Name, peer.Addresses)
				if nodeRegistry != nil {
					nodeRegistry.SavePeer(peer)
				}
			}
			// P0-2: keep the federation trust pool in sync with manual peers so
			// that gossip can see and propagate them (bridge into fed).
			bridgePeerToFederation(peer)
			return nil
		}
	}
	nm.config.Peers = append(nm.config.Peers, peer)
	nm.config.Stats.TotalPeers = len(nm.config.Peers)
	nm.updateOnlineCount()
	nm.doSave()
	if len(peer.Addresses) > 0 {
		routeTable.Put(peer.NodeID, peer.Name, peer.Addresses)
		if nodeRegistry != nil {
			nodeRegistry.SavePeer(peer)
		}
	}
	// P0-2: bridge the newly-added manual peer into the federation trust pool.
	bridgePeerToFederation(peer)
	return nil
}

// HasPeer reports whether a peer with the given node ID is already known.
func (nm *NetworkManager) HasPeer(nodeID string) bool {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	for _, p := range nm.config.Peers {
		if p.NodeID == nodeID {
			return true
		}
	}
	return false
}

// bridgePeerToFederation upserts a manually-added (or notify-added) peer into the
// federation trust pool so gossip can see and propagate it (P0-2). It maps the
// PeerInfo onto a NodeInfo (status=active) and delegates to fed.AddKnownNode,
// which bumps the trust-pool version and persists. It is a no-op when federation
// is disabled (personal mode) or unavailable, so it never blocks the local add.
func bridgePeerToFederation(peer PeerInfo) {
	if fed == nil || !fed.IsEnabled() {
		return
	}
	nodeInfo := NodeInfo{
		NodeID:    peer.NodeID,
		Addresses: peer.Addresses,
		Endpoint:  firstAddress(peer.Addresses),
		Status:    "active",
		LastSeen:  peer.LastSeen,
		PubKey:    peer.PubKey,
	}
	fed.AddKnownNode(nodeInfo)
}

// firstAddress returns the first element of addrs, or "" if empty.
func firstAddress(addrs []string) string {
	if len(addrs) > 0 {
		return addrs[0]
	}
	return ""
}

// RemovePeer removes a peer by node ID
func (nm *NetworkManager) RemovePeer(nodeID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	if nm.config.Mode != NetworkModeShared {
		return fmt.Errorf("shared network not active")
	}
	found := false
	newPeers := []PeerInfo{}
	for _, p := range nm.config.Peers {
		if p.NodeID == nodeID {
			found = true
			continue
		}
		newPeers = append(newPeers, p)
	}
	if !found {
		return fmt.Errorf("peer not found: %s", nodeID)
	}
	nm.config.Peers = newPeers
	nm.config.Stats.TotalPeers = len(nm.config.Peers)
	nm.updateOnlineCount()
	nm.doSave()
	// P0-2: keep the federation trust pool in sync — drop the removed peer.
	if fed != nil && fed.IsEnabled() {
		fed.RemoveNode(nodeID)
	}
	// Drop the removed peer from the on-disk registry as well.
	if nodeRegistry != nil {
		nodeRegistry.RemoveNode(nodeID)
	}
	return nil
}

func (nm *NetworkManager) updateOnlineCount() {
	count := 0
	for _, p := range nm.config.Peers {
		if p.Status == "online" {
			count++
		}
	}
	nm.config.Stats.OnlinePeers = count
}

func (nm *NetworkManager) GetPeers() []PeerInfo {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	peers := make([]PeerInfo, len(nm.config.Peers))
	copy(peers, nm.config.Peers)
	return peers
}

// UpdateConfig updates network configuration
func (nm *NetworkManager) UpdateConfig(nodeName string, sharedModels []string, maxDaily int, relayEnabled *bool) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	if nm.config.Mode != NetworkModeShared {
		return fmt.Errorf("shared network not active")
	}
	if nodeName != "" {
		nm.config.NodeName = nodeName
	}
	if sharedModels != nil {
		nm.config.SharedModels = sharedModels
		nm.config.Stats.TotalModelsShared = len(sharedModels)
	}
	if maxDaily > 0 {
		nm.config.MaxDailyRequests = maxDaily
	}
	if relayEnabled != nil {
		nm.config.RelayEnabled = *relayEnabled
	}
	nm.doSave()
	return nil
}

func (nm *NetworkManager) UpdateShareBoundary(boundary *ShareBoundaryConfig) error {
	if boundary == nil {
		return nil
	}
	nm.mu.Lock()
	defer nm.mu.Unlock()
	if boundary.DailyContribCap >= 0 {
		nm.config.ShareBoundary.DailyContribCap = boundary.DailyContribCap
	}
	nm.config.ShareBoundary.ShareIdleOnly = boundary.ShareIdleOnly
	if boundary.ModelWhitelist != nil {
		nm.config.ShareBoundary.ModelWhitelist = boundary.ModelWhitelist
	}
	nm.doSave()
	slog.Info("share boundary updated",
		"daily_cap", nm.config.ShareBoundary.DailyContribCap,
		"idle_only", nm.config.ShareBoundary.ShareIdleOnly,
		"whitelist", nm.config.ShareBoundary.ModelWhitelist)
	return nil
}

func (nm *NetworkManager) CheckShareBoundary(model string, estimatedTokens int64) (bool, string) {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	b := nm.config.ShareBoundary

	if b.DailyContribCap > 0 {
		var todayContributed int64
		if contributionLedger != nil {
			// Lock already held: read NodeID directly instead of calling
			// GetNodeID() which would re-acquire nm.mu.RLock (recursive RLock
			// can deadlock once a writer is queued).
			// PERF-P0-2: the ledger maintains an incremental per-(node,date)
			// counter, so this is O(1) instead of a full ledger scan.
			selfID := nm.config.NodeID
			todayContributed = contributionLedger.GetDailyContributions(selfID, time.Now())
		}
		if todayContributed+estimatedTokens > b.DailyContribCap {
			return false, "daily contribution cap exceeded"
		}
	}

	if len(b.ModelWhitelist) > 0 {
		found := false
		for _, m := range b.ModelWhitelist {
			if m == model {
				found = true
				break
			}
		}
		if !found {
			return false, "model not in whitelist"
		}
	}

	return true, ""
}
// v3.1: This controls whether the node contributes its providers to the shared pool.
// Independent from network participation — a node can be in the network without sharing.
// If enabling share_to_pool auto-activates the network, the full activation path runs.
func (nm *NetworkManager) SetShareToPool(enabled bool) {
	nm.mu.Lock()
	autoEnabled := false
	// §4.2: Can only share if network is enabled (Level 3 requires Level 2)
	if enabled && !nm.config.NetworkEnabled {
		nm.config.NetworkEnabled = true
		nm.config.Mode = NetworkModeShared
		autoEnabled = true
		slog.Info("auto-enabled network for share_to_pool", "node_id", nm.config.NodeID)
	}
	nm.config.ShareToPool = enabled
	nm.mu.Unlock()
	nm.doSave()
	slog.Info("share_to_pool updated", "enabled", enabled, "network_enabled", nm.config.NetworkEnabled, "node_id", nm.config.NodeID)
	if autoEnabled {
		nm.activateNetwork()
	}
}

// activateNetwork brings the node's network subsystems online. It is the single
// activation path shared by EnableSharedNetwork, SetNetworkEnabled(true) and
// Init() (when network_enabled). It derives a NodeID if the identity is ready,
// registers self, starts the refresh loop (idempotent) and synchronizes the
// FederationManager to follow the network_enabled single source of truth (REQ-2).
func (nm *NetworkManager) activateNetwork() {
	// Derive NodeID if not yet present and identity material is available.
	nm.mu.RLock()
	haveID := nm.config.NodeID != ""
	nm.mu.RUnlock()
	if !haveID && node != nil && node.IsInitialized() {
		nm.mu.Lock()
		nm.config.NodeID = DeriveP2PNodeID()
		nm.mu.Unlock()
	}

	// REQ-S2-3: enforce the three-way NodeID invariant at activation time.
	// assertNodeIDInvariant logs a warning (and would surface a diverged value)
	// but never blocks activation, keeping the network resilient.
	nm.assertNodeIDInvariant()

	nm.startTime = time.Now()
	go nm.registerSelf()
	// Start the refresh loop only if it is not already running.
	if nm.stopRefresh == nil {
		nm.startRefreshLoop()
	}
	// Reconcile federation (and, via fed.IsEnabled(), gossip) with network_enabled.
	nm.syncFederationToNetwork()
}

// deactivateNetwork tears down network subsystems. It is the single deactivation
// path shared by DisableSharedNetwork and SetNetworkEnabled(false).
func (nm *NetworkManager) deactivateNetwork() {
	nm.stopRefreshLoop()
	// Reconcile federation to disabled state (stops its refresh loop).
	nm.syncFederationToNetwork()
}

// syncFederationToNetwork reconciles the FederationManager's enabled state with the
// NetworkManager's single source of truth network_enabled (REQ-2). It must be called
// whenever network_enabled changes and after both managers are initialized. In
// personal mode (network_enabled=false) the federation refresh loop is kept stopped,
// guaranteeing no extra outbound connections (REQ-1 startup guard).
func (nm *NetworkManager) syncFederationToNetwork() {
	if fed == nil {
		return
	}
	nm.mu.RLock()
	enabled := nm.config.NetworkEnabled
	nm.mu.RUnlock()
	fed.SetEnabled(enabled)
}

// SetNetworkEnabled toggles network participation independently.
// §4.2: When disabling network, also disable share_to_pool (can't share without network).
// All activation/deactivation routes through activateNetwork/deactivateNetwork so
// the refresh loop and federation manager stay reconciled with the single source of
// truth (REQ-2 / T3).
func (nm *NetworkManager) SetNetworkEnabled(enabled bool) {
	wasEnabled := nm.config.NetworkEnabled
	nm.mu.Lock()
	nm.config.NetworkEnabled = enabled
	if !enabled {
		// §4.2: Disabling network forces personal mode and disables sharing
		nm.config.Mode = NetworkModePersonal
		nm.config.ShareToPool = false
	} else {
		// §4.2: Enabling network without sharing = Level 2 (Network Mode)
		nm.config.Mode = NetworkModeShared
	}
	nm.mu.Unlock()
	nm.doSave()
	slog.Info("network_enabled updated", "enabled", enabled, "mode", nm.config.Mode, "share_to_pool", nm.config.ShareToPool, "node_id", nm.config.NodeID)

	if enabled && !wasEnabled {
		nm.activateNetwork()
	} else if !enabled && wasEnabled {
		nm.deactivateNetwork()
	}
}

// SetCapabilities updates the node's capability declarations.
// v3.1: Node roles are determined by capabilities, not preset types.
func (nm *NetworkManager) SetCapabilities(caps PeerCapabilities) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.config.Capabilities = caps
	nm.doSave()
	slog.Info("capabilities updated",
		"can_relay", caps.CanRelay,
		"can_seed", caps.CanSeed,
		"providers", caps.Providers,
	)
}

// IsSharingToPool reports whether this node is contributing to the shared pool.
func (nm *NetworkManager) IsSharingToPool() bool {
	nm.mu.RLock()
	defer nm.mu.RUnlock()
	return nm.config.ShareToPool
}

// RecordRelayResult records a relay outcome
func (nm *NetworkManager) RecordRelayResult(success bool) {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.config.Stats.RequestsRelayed++
	if success {
		nm.config.Stats.RelaySuccess++
	} else {
		nm.config.Stats.RelayFailed++
	}
}

// RecordReceived records an incoming relay request
func (nm *NetworkManager) RecordReceived() {
	nm.mu.Lock()
	defer nm.mu.Unlock()
	nm.config.Stats.RequestsReceived++
}

// RefreshAddresses re-collects addresses (called on tunnel change)
func (nm *NetworkManager) RefreshAddresses() {
	if !nm.IsSharedMode() {
		return
	}
	nm.registerSelf()
}

// GetDisclaimer returns the disclaimer text
func GetDisclaimer() DisclaimerResponse {
	return DisclaimerResponse{
		Title: "共享网络使用须知",
		Sections: []DisclaimerSection{
			{
				Heading: "什么是共享网络？",
				Content: "OpenModelPool Agent 本质上是一个 AI 智能代理（Agent）——和你使用的任何 AI Agent 没有区别：持有 API Key，向上游模型服务商发送请求，获取响应。\n\n共享网络只是在这个 Agent 的基础上增加了一个可选功能：将你闲置的模型调用能力分享给网络中的其他用户，同时也可以使用他人分享的模型能力。每个节点都可以作为 relay 为他人转发请求，形成去中心化的 P2P 网络。\n\n这和你自己部署一个 Agent 来调用 API 在本质上是相同的——区别仅在于 prompt 来自谁。对上游服务商而言，请求来自同一个 API Key，消耗的是同一个账户配额。",
			},
			{
				Heading: "启用后将发生什么？",
				Content: "• 您的节点将对外公开（节点名称、可用模型列表、大致地区）\n• 您的节点自动成为 relay 节点，可以为其他节点转发请求\n• 消费者可以通过任意 relay 节点使用 URL 格式 https://{relay地址}/network/{NodeID}/v1 访问目标节点\n• 您的 API Key 不会被暴露，请求通过 relay 反向代理转发\n• 您将开始积累贡献积分（积分仅为参与激励，不可变现、不可交易）",
			},
			{
				Heading: "关于模型能力的安全责任",
				Content: "• 所有通过本网络流转的 AI 请求，最终都到达您配置的上游模型服务商（如 OpenAI、Anthropic 等）\n• 模型能力的合法性、安全性由上游服务商负责保障——您使用正规渠道购买的 API Key，通过本网络转发请求，与直接调用并无本质区别\n• 本软件是去中心化工具，不是平台、不是服务商。每个节点使用自己的 API Key，对自己的账户行为负责\n• 不存在\"转售\"行为——每个节点都是在用自己的 Key 转发请求，和用 Agent 调用 API 完全一样",
			},
			{
				Heading: "⚠️ 风险警告",
				IsRisk:  true,
				Content: "• 部分 AI 平台的服务条款可能限制 API 代理行为，启用共享网络可能导致您的 API 账号受限\n• 系统已实施速率限制和行为模拟，但无法完全消除平台检测风险\n• 您分享的计算资源可能被他人生成不当内容，您需承担相应平台的风控后果\n• 不同区域的法律法规可能对 AI 服务的使用有不同要求\n• 贡献积分仅作为参与网络的激励记录，不具有任何货币价值，不可交易或变现",
			},
		},
		ConfirmationText: "我已阅读并理解以上说明，自愿承担相关风险",
	}
}

// ============================================================
// §1.5.2 Join Conditions — check if node is ready for shared network
// ============================================================

// JoinConditionResult describes whether the node meets the conditions to join the shared network.
type JoinConditionResult struct {
	HasProvider     bool   `json:"has_provider"`      // condition 1: has at least one enabled Provider
	HasQuotaManager bool   `json:"has_quota_manager"` // condition 2: quota management is enabled
	HasSharedKey    bool   `json:"has_shared_key"`    // condition 3 (hard): at least one enabled shared (access_control=="shared") key exists
	HasRemaining    bool   `json:"has_remaining"`     // NON-MANDATORY: remaining quota > 0 this month — used only for the idle-capacity reminder, never blocks joining
	AllMet          bool   `json:"all_met"`           // all hard conditions satisfied
	Message         string `json:"message,omitempty"` // gentle prompt message when all conditions met
}

// CheckJoinConditions checks whether the node satisfies the conditions for joining the shared network.
// §1.5.2 (updated): Joining now only requires HasProvider && HasQuotaManager && HasSharedKey.
// HasRemaining is intentionally NOT part of AllMet — it is merely an idle-capacity signal that
// the front-end surface as a non-blocking reminder.
func (nm *NetworkManager) CheckJoinConditions() (bool, JoinConditionResult) {
	result := JoinConditionResult{}

	// Condition 1: at least one enabled Provider with API keys
	if pm != nil {
		for _, p := range pm.Enabled() {
			if len(p.APIKeys) > 0 {
				result.HasProvider = true
				break
			}
			// Also count legacy single API key
			if p.APIKey != "" {
				result.HasProvider = true
				break
			}
		}
	}

	// Condition 2: quota management (allocation manager) is enabled
	result.HasQuotaManager = allocMgr != nil

	// Condition 3 (hard): at least one enabled Provider Key with access_control == "shared".
	if pm != nil {
		for _, p := range pm.Enabled() {
			for _, k := range p.APIKeys {
				if k.Enabled && k.AccessControl == "shared" {
					result.HasSharedKey = true
					break
				}
			}
			if result.HasSharedKey {
				break
			}
		}
	}

	// Idle-capacity signal: remaining quota > 0 this month.
	// NOTE: This is NON-MANDATORY. It is reported only so the front-end can show a gentle
	// "you have idle capacity" reminder. It does NOT affect AllMet.
	if pm != nil {
		var totalQuota int64
		var usedQuota int64
		for _, p := range pm.GetAllRaw() {
			if !p.Enabled {
				continue
			}
			if p.TokenLimit > 0 {
				totalQuota += p.TokenLimit
			}
			for _, k := range p.APIKeys {
				if k.Enabled && k.Quota > 0 {
					totalQuota += k.Quota
					usedQuota += k.Used
				}
			}
		}
		result.HasRemaining = totalQuota > usedQuota
	}

	// All hard conditions: provider + quota manager + at least one shared key.
	// HasRemaining is deliberately excluded from AllMet.
	result.AllMet = result.HasProvider && result.HasQuotaManager && result.HasSharedKey

	if result.AllMet {
		result.Message = "您的节点已具备加入共享网络的条件（已配置至少一个「共享」类型的 Key）。加入后，您可以消费网络中其他节点的资源，也可以选择将自己的闲置额度共享给他人。"
	}

	return result.AllMet, result
}

// ============================================================
// API Handlers — Network Management
// ============================================================

func handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeJSON(w, 200, map[string]any{"mode": "personal", "consent_accepted": false})
		return
	}
	writeJSON(w, 200, netMgr.GetStatus())
}

func handleNetworkStats(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeJSON(w, 200, map[string]any{
			"total_nodes": 1, "online_nodes": 1, "active_users": 0,
			"total_requests": 0, "relay_requests": 0, "success_rate": 0.0,
			"total_quota": 0, "models_shared": 0, "uptime": 0,
		})
		return
	}
	writeJSON(w, 200, netMgr.GetNetworkStats())
}

func handleNetworkConsent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Accepted bool `json:"accepted"`
	}
	if err := readJSON(w, r, &body); err != nil || !body.Accepted {
		writeError(w, 400, "accepted must be true")
		return
	}
	if err := netMgr.RecordConsent(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "consent_recorded", "consent_time": netMgr.config.ConsentTime})
}

func handleNetworkDisclaimer(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, GetDisclaimer())
}

func handleNetworkEnable(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeError(w, 500, "网络管理器未初始化")
		return
	}
	if err := netMgr.EnableSharedNetwork(); err != nil {
		// REQ-S2-7: surface a clear, actionable message (Chinese) instead of a raw error.
		writeError(w, 400, err.Error())
		return
	}
	resp := map[string]any{
		"status":           "enabled",
		"mode":             "shared",
		"network_enabled":  netMgr.config.NetworkEnabled,
		"node_id":          canonicalNodeID(),
		"backup_confirmed": node != nil && node.IsBackupConfirmed(),
		"share_to_pool":    netMgr.config.ShareToPool,
		"capabilities":     netMgr.config.Capabilities,
	}
	writeJSON(w, 200, resp)
}

func handleNetworkDisable(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeJSON(w, 200, map[string]any{"status": "disabled", "mode": "personal", "network_enabled": false, "share_to_pool": false})
		return
	}
	if err := netMgr.DisableSharedNetwork(); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "disabled", "mode": "personal", "network_enabled": false, "share_to_pool": false})
}

// POST /api/network/toggle — toggle network/shared state
// Supports three-level model via JSON body:
//
//	{"enabled": true}                    → Level 2 (Network Mode, no sharing)
//	{"enabled": true, "share_to_pool": true} → Level 3 (Shared Peer)
//	{"enabled": false}                   → Level 1 (Personal Mode)
//	{"network_enabled": true}            → Level 2
//	{"network_enabled": true, "share_to_pool": true} → Level 3
//	{"network_enabled": false}           → Level 1
func handleNetworkToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled        *bool `json:"enabled"`
		NetworkEnabled *bool `json:"network_enabled"`
		ShareToPool    *bool `json:"share_to_pool"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if netMgr == nil {
		writeError(w, 500, "network manager not initialized")
		return
	}

	// Determine network_enabled from either field (backward compat)
	networkEnabled := false
	if body.NetworkEnabled != nil {
		networkEnabled = *body.NetworkEnabled
	} else if body.Enabled != nil {
		networkEnabled = *body.Enabled
	}

	netMgr.SetNetworkEnabled(networkEnabled)

	if body.ShareToPool != nil && *body.ShareToPool && networkEnabled {
		netMgr.SetShareToPool(true)
	}

	netMgr.mu.RLock()
	resp := map[string]any{
		"status":          "updated",
		"mode":            string(netMgr.config.Mode),
		"network_enabled": netMgr.config.NetworkEnabled,
		"share_to_pool":   netMgr.config.ShareToPool,
		"node_id":         netMgr.config.NodeID,
	}
	netMgr.mu.RUnlock()
	writeJSON(w, 200, resp)
}

func handleNetworkConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeError(w, 503, "network manager not available")
		return
	}
	var body struct {
		NodeName      string              `json:"node_name"`
		SharedModels  []string            `json:"shared_models"`
		MaxDaily      int                 `json:"max_daily_requests"`
		RelayEnabled  *bool               `json:"relay_enabled,omitempty"`
		ShareBoundary *ShareBoundaryConfig `json:"share_boundary,omitempty"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if err := netMgr.UpdateConfig(body.NodeName, body.SharedModels, body.MaxDaily, body.RelayEnabled); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if body.ShareBoundary != nil {
		if err := netMgr.UpdateShareBoundary(body.ShareBoundary); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"status": "updated"})
}

func handleNetworkPeers(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil || !netMgr.IsSharedMode() {
		writeJSON(w, 200, map[string]any{"peers": []PeerInfo{}, "message": "shared network not active"})
		return
	}
	writeJSON(w, 200, map[string]any{"peers": netMgr.GetPeers()})
}

// resolvePublicEndpoint resolves the best publicly-reachable base endpoint for
// this node. It is used when advertising this node to peers (invites, peer
// registration, gossip). Resolution priority:
//  1. federation_endpoint  (explicit public base, strongest)
//  2. public_domain        (v4.1.6: explicit public domain, e.g. https://openmodelpool.io)
//  3. Host header          (https://host, only when a request context is available)
//  4. http://<hostname>:<port>  (LAN fallback — only valid on a local network)
func resolvePublicEndpoint(host string) string {
	if ep := cfg.Get("federation_endpoint", ""); ep != "" {
		return ep
	}
	if pd := cfg.Get("public_domain", ""); pd != "" {
		return pd
	}
	if host != "" {
		return "https://" + host
	}
	hostname, _ := os.Hostname()
	port := cfg.Get("service_port", "8000")
	slog.Warn("resolvePublicEndpoint fell back to LAN address")
	return fmt.Sprintf("http://%s:%s", hostname, port)
}

// resolvePeerNodeID resolves the node ID of a peer by querying its public
// heartbeat ping endpoint. Used by handleNetworkAddPeer when the operator adds a
// peer by address only (node_id left empty). The browser cannot do this
// directly due to CORS, so the OMP backend resolves it on the operator's behalf.
func resolvePeerNodeID(addr string) (string, error) {
	// Validate scheme to avoid SSRF / unexpected protocols.
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		return "", fmt.Errorf("invalid peer address %q: must start with http:// or https://", addr)
	}
	pingURL := strings.TrimRight(addr, "/") + "/api/network/heartbeat/ping"

	client := internalHTTPClient
	if client == nil {
		client = GetSharedHTTPClientWithTimeout(5 * time.Second)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, pingURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build ping request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to reach peer %s: %w", addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("peer %s returned status %d", addr, resp.StatusCode)
	}

	var data struct {
		Status string `json:"status"`
		NodeID string `json:"node_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("failed to decode ping response from %s: %w", addr, err)
	}
	if data.NodeID == "" {
		return "", fmt.Errorf("peer %s did not return a node_id", addr)
	}
	return data.NodeID, nil
}

func handleNetworkAddPeer(w http.ResponseWriter, r *http.Request) {
	var peer PeerInfo
	if err := readJSON(w, r, &peer); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if netMgr == nil {
		writeError(w, 503, "network manager not available")
		return
	}
	// node_id may be omitted; when omitted we resolve it from the peer's public
	// heartbeat endpoint using the supplied address (P0-2: manual peer linking
	// without exchanging node IDs out of band).
	if peer.NodeID == "" {
		if len(peer.Addresses) == 0 {
			writeError(w, 400, "addresses or node_id required")
			return
		}
		resolved, err := resolvePeerNodeID(peer.Addresses[0])
		if err != nil {
			writeError(w, 400, "failed to resolve node_id from address: "+err.Error())
			return
		}
		peer.NodeID = resolved
	}
	if peer.Status == "" {
		peer.Status = "online"
	}
	if peer.LastSeen == "" {
		peer.LastSeen = time.Now().Format(time.RFC3339)
	}
	if peer.TrustScore == 0 {
		peer.TrustScore = 0.5
	}
	// Record whether this peer was already known BEFORE we add it. This drives
	// the P0-1 bidirectional-discovery notify: we only reverse-notify on a
	// *human-initiated* successful add of a *new* peer (R3 idempotency + R4
	// "after successful local add"). Re-adds of an existing peer do not notify.
	existed := netMgr.HasPeer(peer.NodeID)
	if err := netMgr.AddPeer(peer); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	// P0-1: after a human-initiated successful add of a new peer, asynchronously
	// notify the peer so it registers us back (bidirectional, mesh discovery).
	// The notify *receiver* path (handleNetworkPeersNotify) never re-notifies,
	// so there is no ping-pong loop (R1).
	if !existed {
		go sendNotifyToPeer(peer)
	}
	writeJSON(w, 200, map[string]any{"status": "added", "peer": peer})
}

// PeerNotifyPayload is the body of POST /api/network/peers/notify (P0-1). The
// sender signs (node_id|addresses|timestamp) with its node private key and
// embeds its public key so the receiver can verify locally without a round-trip
// (which would be a chicken-and-egg problem on first contact).
type PeerNotifyPayload struct {
	NodeID    string   `json:"node_id"`
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
	PubKey    string   `json:"pub_key"`
	Timestamp string   `json:"timestamp"`
	Signature string   `json:"signature"`
	Propagated bool `json:"propagated"`
	Claims    []CapabilityClaimEntry `json:"claims,omitempty"`
}

type CapabilityClaimEntry struct {
	Models    []string `json:"models"`
	Providers []string `json:"providers"`
}

// handleNetworkPeersNotify implements POST /api/network/peers/notify — the P0-1
// reverse-registration endpoint.
//
// Deliberately NOT wrapped in withFederationAuth / withAuth: on first contact
// the sender is not yet in our trust pool (GetNode would miss → 403), and a
// cross-instance admin JWT is not available. Instead it is rate-limited (route
// level) and protected by an ed25519 signature verified against the embedded
// public key (no round-trip fetch). A fresh timestamp (<=5 min) prevents replay.
// Only after a valid signature do we locally AddPeer the sender (P0-2 bridge).
//
// LOOP PREVENTION (R1): this handler NEVER calls sendNotifyToPeer, so the
// originator is never re-notified — a ping-pong is structurally impossible.
func handleNetworkPeersNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var p PeerNotifyPayload
	if err := readJSON(w, r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// 1. Structural validation.
	if p.NodeID == "" || len(p.Addresses) == 0 || p.Signature == "" || p.PubKey == "" {
		writeError(w, http.StatusBadRequest, "missing node_id, addresses, pub_key or signature")
		return
	}

	// 2. Timestamp freshness (anti-replay): must be within the last 5 minutes.
	ts, err := time.Parse(time.RFC3339, p.Timestamp)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid timestamp")
		return
	}
	if age := time.Since(ts); age < 0 || age > 5*time.Minute {
		writeError(w, http.StatusBadRequest, "timestamp outside acceptable window")
		return
	}

	// 3. Signature verification. The authoritative public key served by the
	//    claimant at /api/node/pubkey is the ONLY key accepted (defeats payload
	//    pubkey substitution, see ARCH §9 D1). SEC-P1-4: if the fetch fails or
	//    returns nothing, the notify is REJECTED (fail-closed) — we never fall
	//    back to the attacker-controlled pub_key embedded in the payload.
	canonical := fmt.Sprintf("%s|%s|%s", p.NodeID, strings.Join(p.Addresses, ","), p.Timestamp)
	pubKey := fetchNodePubKey(p.Addresses[0])
	if pubKey == "" {
		slog.Warn("peers/notify: failed to fetch authoritative pubkey; rejecting (fail-closed)", "from", p.NodeID, "addr", p.Addresses[0])
		writeError(w, http.StatusUnauthorized, "cannot verify node public key")
		return
	}
	if !VerifySignature(pubKey, []byte(canonical), p.Signature) {
		slog.Warn("peers/notify: signature verification failed", "from", p.NodeID)
		writeError(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	// 4. (Optional, best-effort) confirm the sender is reachable at the
	//    advertised address. Failure is only a warning; we still register.
	pingPeerOnce(p.Addresses[0])

	// 5. Locally register the sender as a peer. AddPeer bridges it into the
	//    federation trust pool (P0-2). By design this does NOT trigger a
	//    reciprocal notify (R1). The stored public key is the authoritative one
	//    fetched above, never the payload-supplied value.
	peer := PeerInfo{
		NodeID:     p.NodeID,
		Name:       p.Name,
		Addresses:  p.Addresses,
		Status:     "online",
		PubKey:     pubKey,
		LastSeen:   time.Now().Format(time.RFC3339),
		TrustScore: 0.5,
	}
	if netMgr == nil {
		writeError(w, http.StatusServiceUnavailable, "network manager not available")
		return
	}
	if err := netMgr.AddPeer(peer); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(p.Claims) > 0 && contributionLedger != nil {
		for _, c := range p.Claims {
			claim := &CapabilityClaim{
				PeerID:    p.NodeID,
				Models:    c.Models,
				Providers: c.Providers,
			}
			contributionLedger.RecordClaim(claim)
		}
		saveContributionLedger()
		slog.Info("peers/notify: recorded capability claims", "peer", p.NodeID, "count", len(p.Claims))
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "notified", "node_id": p.NodeID})
}

// pingPeerOnce performs a best-effort reachability check against the peer's
// public heartbeat ping endpoint. A failure is logged as a warning and never
// blocks registration (P0-1c: silent failure must not block the local add).
func pingPeerOnce(addr string) {
	pingURL := strings.TrimRight(addr, "/") + "/api/network/heartbeat/ping"
	client := GetSharedHTTPClientWithTimeout(3 * time.Second)
	resp, err := client.Get(pingURL)
	if err != nil {
		slog.Warn("peers/notify: optional ping failed (non-blocking)", "peer", addr, "error", err)
		return
	}
	resp.Body.Close()
	slog.Debug("peers/notify: peer ping OK", "peer", addr)
}

// sendNotifyToPeer asynchronously notifies a peer (that we just added locally,
// human-initiated) to register us back. This implements P0-1 bidirectional
// discovery. It is best-effort: failures are logged as warnings and never block
// or roll back the local addition (P0-1c).
//
// CRITICAL (R1 loop prevention): this function is ONLY ever invoked from the
// human-initiated add path (handleNetworkAddPeer) for a freshly-added peer. The
// notify receiving path (handleNetworkPeersNotify) deliberately never calls back
// into sendNotifyToPeer, so a peer never re-notifies the originator.
// pubKeyCacheEntry caches a node's authoritative public key (PERF-P1-9).
type pubKeyCacheEntry struct {
	pubKey    string
	fetchedAt time.Time
}

// pubKeyCacheTTL bounds how long a fetched pubkey is trusted without refresh.
const pubKeyCacheTTL = 10 * time.Minute

var (
	pubKeyCacheMu sync.Mutex
	pubKeyCache   = map[string]pubKeyCacheEntry{}
)

// fetchNodePubKey performs a best-effort retrieval of a node's authoritative
// ed25519 public key from its public /api/node/pubkey endpoint (P0-1). It is
// used to verify a /api/network/peers/notify signature against a key the
// claimant actually controls, defeating payload pubkey substitution (ARCH §9 D1).
// Returns "" on any failure; the caller decides whether to fail closed.
// PERF-P1-9: results are cached per address (with a TTL) so the peer-notify
// hot path does not re-fetch on every notify, and the fetch reuses the shared
// connection-pooled client.
func fetchNodePubKey(addr string) string {
	if addr == "" {
		return ""
	}
	pubKeyCacheMu.Lock()
	if e, ok := pubKeyCache[addr]; ok && time.Since(e.fetchedAt) < pubKeyCacheTTL {
		pubKeyCacheMu.Unlock()
		return e.pubKey
	}
	pubKeyCacheMu.Unlock()

	u := strings.TrimRight(addr, "/") + "/api/node/pubkey"
	client := GetSharedHTTPClientWithTimeout(3 * time.Second)
	resp, err := client.Get(u)
	if err != nil {
		slog.Debug("peers/notify: failed to fetch authoritative pubkey (fail-closed decision by caller)", "addr", addr, "error", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		PubKey string `json:"pub_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ""
	}
	if out.PubKey != "" {
		pubKeyCacheMu.Lock()
		pubKeyCache[addr] = pubKeyCacheEntry{pubKey: out.PubKey, fetchedAt: time.Now()}
		pubKeyCacheMu.Unlock()
	}
	return out.PubKey
}

func sendNotifyToPeer(peer PeerInfo) {
	if node == nil {
		slog.Warn("sendNotifyToPeer skipped: node identity not initialized")
		return
	}
	myID := node.NodeID()
	if myID == "" {
		slog.Warn("sendNotifyToPeer skipped: node id unavailable")
		return
	}
	myAddrs := []string{resolvePublicEndpoint("")}
	ts := time.Now().UTC().Format(time.RFC3339)
	canonical := fmt.Sprintf("%s|%s|%s", myID, strings.Join(myAddrs, ","), ts)
	sig := node.Sign([]byte(canonical))
	if sig == "" {
		slog.Warn("sendNotifyToPeer skipped: signing failed")
		return
	}

	name := ""
	if netMgr != nil {
		netMgr.mu.RLock()
		name = netMgr.config.NodeName
		netMgr.mu.RUnlock()
	}
	if name == "" {
		name = myID
	}

	payload := PeerNotifyPayload{
		NodeID:     myID,
		Name:       name,
		Addresses:  myAddrs,
		PubKey:     node.PubKeyB64(),
		Timestamp:  ts,
		Signature:  sig,
		Propagated: true,
	}

	if contributionLedger != nil {
		localClaims := contributionLedger.GetAllClaims()
		for _, c := range localClaims {
			if c.PeerID == myID {
				payload.Claims = append(payload.Claims, CapabilityClaimEntry{
					Models:    c.Models,
					Providers: c.Providers,
				})
			}
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("sendNotifyToPeer: marshal failed", "error", err)
		return
	}
	if len(peer.Addresses) == 0 {
		slog.Warn("sendNotifyToPeer skipped: peer has no address", "peer", peer.NodeID)
		return
	}
	notifyURL := strings.TrimRight(peer.Addresses[0], "/") + "/api/network/peers/notify"
	nCtx, nCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer nCancel()
	req, err := http.NewRequestWithContext(nCtx, http.MethodPost, notifyURL, bytes.NewReader(body))
	if err != nil {
		slog.Warn("sendNotifyToPeer: build request failed", "peer", peer.NodeID, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// X-Node-ID is set only for receiver log correlation; the receiver does NOT
	// authenticate on this header (it uses the embedded signature instead).
	req.Header.Set("X-Node-ID", myID)

	client := GetSharedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("sendNotifyToPeer failed (best-effort)", "peer", peer.NodeID, "url", notifyURL, "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("sendNotifyToPeer rejected by peer", "peer", peer.NodeID, "status", resp.StatusCode)
		return
	}
	slog.Info("sent discovery notify to peer", "peer", peer.NodeID, "url", notifyURL)
}

func handleNetworkRemovePeer(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeError(w, 400, "peer id required")
		return
	}
	if netMgr == nil {
		writeError(w, 503, "network manager not available")
		return
	}
	if err := netMgr.RemovePeer(nodeID); err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "removed", "node_id": nodeID})
}

// GET /api/network/resolve/{id} — resolve NodeID to addresses
func handleNetworkResolve(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("id")
	if nodeID == "" {
		writeError(w, 400, "node_id required")
		return
	}
	if !strings.HasPrefix(nodeID, p2pNodeIDPrefix) {
		writeError(w, 400, "invalid node_id format; must start with '"+p2pNodeIDPrefix+"'")
		return
	}
	entry := routeTable.Get(nodeID)
	if entry == nil {
		writeJSON(w, 404, map[string]any{"node_id": nodeID, "addresses": []string{}, "status": "not_found"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"node_id":    entry.NodeID,
		"node_name":  entry.NodeName,
		"addresses":  entry.Addresses,
		"status":     entry.Status,
		"updated_at": entry.UpdatedAt.Format(time.RFC3339),
	})
}

// GET /api/network/routes — list all route table entries (admin)
func handleNetworkRoutes(w http.ResponseWriter, r *http.Request) {
	entries := routeTable.GetAll()
	writeJSON(w, 200, map[string]any{"entries": entries, "count": len(entries)})
}

// GET /api/network/join-conditions — check if node meets join conditions (§1.5.2)
func handleNetworkJoinConditions(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeJSON(w, 200, JoinConditionResult{AllMet: false, Message: "network manager not initialized"})
		return
	}
	_, result := netMgr.CheckJoinConditions()
	writeJSON(w, 200, result)
}

type IdleQuotaStatus struct {
	HasIdleQuota   bool   `json:"has_idle_quota"`
	TotalQuota     int64  `json:"total_quota"`
	UsedQuota      int64  `json:"used_quota"`
	RemainingQuota int64  `json:"remaining_quota"`
	ShouldNotify   bool   `json:"should_notify"`
	Message        string `json:"message,omitempty"`
}

func checkIdleQuota() IdleQuotaStatus {
	status := IdleQuotaStatus{}
	if pm == nil {
		return status
	}
	var totalQuota, usedQuota int64
	for _, p := range pm.GetAllRaw() {
		if !p.Enabled {
			continue
		}
		if p.TokenLimit > 0 {
			totalQuota += p.TokenLimit
		}
		for _, k := range p.APIKeys {
			if k.Enabled && k.Quota > 0 {
				totalQuota += k.Quota
				usedQuota += k.Used
			}
		}
	}
	status.TotalQuota = totalQuota
	status.UsedQuota = usedQuota
	status.RemainingQuota = totalQuota - usedQuota
	status.HasIdleQuota = totalQuota > usedQuota && totalQuota > 0

	if status.HasIdleQuota && netMgr != nil && !netMgr.config.NetworkEnabled {
		usagePct := float64(0)
		if totalQuota > 0 {
			usagePct = float64(usedQuota) / float64(totalQuota) * 100
		}
		if usagePct < 70 {
			status.ShouldNotify = true
			status.Message = fmt.Sprintf("您本月额度使用率仅 %.0f%%，尚有 %d tokens 闲置。考虑加入共享网络，将闲置额度贡献给社区？", usagePct, status.RemainingQuota)
		}
	}
	return status
}

func handleIdleQuotaCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, checkIdleQuota())
}

// logSharedNetworkSoftReminder emits a one-time, non-blocking governance nudge at
// startup. If the node runs in personal mode yet still has idle OWN capacity, it
// gently suggests (NEVER forces) joining the shared network so the spare quota
// becomes a community public-pool resource. The community free pool (preset
// public upstream) is always available out-of-the-box and is unaffected.
func logSharedNetworkSoftReminder() {
	if netMgr == nil || netMgr.config.NetworkEnabled {
		return
	}
	iq := checkIdleQuota()
	if iq.ShouldNotify {
		slog.Info("治理软提醒: 节点处于个人模式且有闲置自有额度，鼓励(非强制)加入共享网络将闲置额度贡献为社区公共池资源",
			"idle_quota", iq.RemainingQuota,
			"hint", "管理后台「共享中心」一键加入；不加入也完全可用(仅自用 + 社区免费池)")
	}
}
