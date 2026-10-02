package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var nodeIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func sanitizeNodeID(id string) string {
	if len(id) > 128 {
		id = id[:128]
	}
	if !nodeIDPattern.MatchString(id) {
		return ""
	}
	return id
}

// maxFederationSigBody caps how much of the request body participates in the
// path-1 signature. Federation payloads (gossip records, ledger sync,
// governance proposals) are small JSON documents; handlers reject larger
// bodies anyway.
const maxFederationSigBody = 1 << 20 // 1 MiB

// federationSigValid verifies a withFederationAuth path-1 signature.
// B6-5: the preferred format binds the SHA-256 of the request body so an
// on-path attacker cannot tamper with gossip/ledger/governance payloads
// inside the replay window. The legacy body-less format is still accepted for
// rolling-deploy compatibility with older peers.
func federationSigValid(r *http.Request, pubKey, nodeID, sig string) bool {
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxFederationSigBody))
		if err != nil {
			b = nil
		}
		body = b
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	payload := []byte(fmt.Sprintf("%s:%s:%s:%s", nodeID, r.Method, r.URL.Path, sha256Hex(body)))
	if VerifySignature(pubKey, payload, sig) {
		return true
	}
	legacy := []byte(fmt.Sprintf("%s:%s:%s", nodeID, r.Method, r.URL.Path))
	return VerifySignature(pubKey, legacy, sig)
}

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

		// Auth path 1: Known node identity (X-Node-ID + signature verification)
		nodeID := sanitizeNodeID(r.Header.Get("X-Node-ID"))
		if nodeID != "" && fed != nil {
			if n, ok := fed.GetNode(nodeID); ok && n.PubKey != "" {
				sig := r.Header.Get("X-Node-Signature")
				timestamp := r.Header.Get("X-Node-Timestamp")
				if sig != "" && timestamp != "" {
					ts, err := strconv.ParseInt(timestamp, 10, 64)
					if err == nil {
						elapsed := time.Now().Unix() - ts
						if elapsed >= 0 && elapsed <= 300 {
							if federationSigValid(r, n.PubKey, nodeID, sig) {
								handler(w, r)
								return
							}
						}
					}
				}
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
			if requestSecret != "" && subtle.ConstantTimeCompare([]byte(requestSecret), []byte(fedSecret)) == 1 {
				handler(w, r)
				return
			}
		}

		// All auth paths failed — reject
		writeJSON(w, 403, map[string]string{"error": "federation authentication required"})
	}
}

// isTrustedSeed reports whether the incoming request originates from a configured
// bootstrap seed node. It verifies a pre-shared X-Seed-Token header against the
// federation_secret, avoiding reliance on the easily-spoofed Host header.
func isTrustedSeed(r *http.Request) bool {
	seedToken := r.Header.Get("X-Seed-Token")
	if seedToken == "" {
		return false
	}
	fedSecret := cfg.Get("federation_secret", "")
	if fedSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(seedToken), []byte(fedSecret)) == 1
}

// FederationManager manages this node's participation in the federation.
type FederationManager struct {
	mu             sync.RWMutex
	saveMu         sync.Mutex // B7-supp: serializes pool writes (mirrors provider P0-4)
	trustPool      TrustPool
	localPeers     map[string]*NodeInfo
	discoveryHints map[string][]string
	dhtHints       map[string]string // nodeID -> DHT UDP "host:port" learned via gossip PEX
	dht            *DHT
	enabled        bool
	relayEnabled   bool
	loopRunning    bool
	dataDir        string
	stopCh         chan struct{}
	lastETag       string
}

// initFederation loads federation config from cfg, loads cached trust pool from
// dataDir/federation_pool.json, and prepares the manager. It does NOT start the
// refresh loop here — federation activation now follows network_enabled via
// syncFederationToNetwork() (REQ-2 / T2).
func initFederation(dataDir string) {
	f := &FederationManager{
		localPeers:     make(map[string]*NodeInfo),
		discoveryHints: make(map[string][]string),
		dhtHints:       make(map[string]string),
		dataDir:        dataDir,
		stopCh:         make(chan struct{}),
	}

	selfID := "unknown"
	if node != nil {
		selfID = node.NodeID()
	}
	f.dht = NewDHT(selfID)

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
	tombstonesCopy := make([]NodeTombstone, len(f.trustPool.Tombstones))
	copy(tombstonesCopy, f.trustPool.Tombstones)

	return TrustPool{
		Version:    f.trustPool.Version,
		Nodes:      nodesCopy,
		UpdatedAt:  f.trustPool.UpdatedAt,
		Registry:   f.trustPool.Registry,
		Tombstones: tombstonesCopy,
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

	// P1-2: return a deep copy, never the internal pointer. Callers (withAuth
	// path-1, verifyRelayForwardAuth, federation health) must not be able to
	// mutate live federation state through a returned *NodeInfo.
	for i := range f.trustPool.Nodes {
		if f.trustPool.Nodes[i].NodeID == nodeID {
			n := f.trustPool.Nodes[i]
			return cloneNodeInfo(&n), true
		}
	}
	if n, ok := f.localPeers[nodeID]; ok {
		return cloneNodeInfo(n), true
	}
	return nil, false
}

// cloneNodeInfo returns a deep copy of n (slices are re-allocated) so the
// caller cannot mutate shared federation state (P1-2).
func cloneNodeInfo(n *NodeInfo) *NodeInfo {
	if n == nil {
		return nil
	}
	c := *n
	if n.SharedModels != nil {
		c.SharedModels = append([]string(nil), n.SharedModels...)
	}
	if n.Addresses != nil {
		c.Addresses = append([]string(nil), n.Addresses...)
	}
	if n.SharedProviders != nil {
		c.SharedProviders = make([]SharedProvider, len(n.SharedProviders))
		for i := range n.SharedProviders {
			p := n.SharedProviders[i]
			if p.Models != nil {
				p.Models = append([]string(nil), p.Models...)
			}
			c.SharedProviders[i] = p
		}
	}
	return &c
}

// UpdateTrustPool merges the incoming pool into the local cache.
// It only applies the update if the incoming version is strictly newer.
// P2P-G3: 分区愈合时按节点合并——替换前先把本地独有节点并入 incoming 池，
// 而不是整池丢弃，避免分区期间本地学到的节点在愈合时丢失。
func (f *FederationManager) UpdateTrustPool(pool TrustPool) {
	f.mu.Lock()
	if pool.Version <= f.trustPool.Version {
		f.mu.Unlock()
		return
	}

	merged := mergeTrustPools(f.trustPool, pool)
	f.trustPool = merged
	slog.Info("trust pool updated", "version", merged.Version, "nodes", len(merged.Nodes))

	for i := range merged.Nodes {
		f.dhtAddNodeLocked(&merged.Nodes[i])
	}

	snapshot, err := f.snapshotLocked()
	f.mu.Unlock()
	if err != nil {
		slog.Error("failed to persist trust pool", "error", err)
		return
	}
	f.persistSnapshot(snapshot)
}

// mergeTrustPools 按节点合并两个信任池（P2P-G3 分区愈合）。
//   - incoming 独有节点：保留（除非被 tombstone 压制，见下）。
//   - 本地独有节点：仅 Status == "active" 的并入；suspended/inactive 的不复活，
//     避免把注册表已下架的节点重新带回来；同样受 tombstone 压制。
//   - 双方都有的节点：LastSeen 更新者胜；时间解析失败时保留 incoming 的。
//   - tombstone 合并：按 NodeID 取更新者胜；任何一方的新鲜 tombstone 都能压制
//     另一方的陈旧节点记录——显式下架（RemoveNode）因此可以在全网传播并保持，
//     不会被"本地 active 副本"静默撤销。节点 LastSeen 严格新于 tombstone 时
//     视为活着回来（rejoin），tombstone 清除。
//
// 返回池的版本号取 incoming 的版本——调用方已保证 incoming 版本严格更大，
// 版本单调语义不被破坏。
func mergeTrustPools(local, incoming TrustPool) TrustPool {
	merged := TrustPool{
		Version:   incoming.Version,
		UpdatedAt: incoming.UpdatedAt,
		Registry:  incoming.Registry,
		Nodes:     make([]NodeInfo, 0, len(incoming.Nodes)+len(local.Nodes)),
	}
	merged.Tombstones = unionTombstones(local.Tombstones, incoming.Tombstones)
	tombstoned := func(id string) (NodeTombstone, bool) {
		for _, t := range merged.Tombstones {
			if t.NodeID == id {
				return t, true
			}
		}
		return NodeTombstone{}, false
	}
	// clearTombstone drops the tombstone when a record proves the node is
	// back (its LastSeen is strictly newer than the removal).
	clearTombstone := func(id, lastSeen string) {
		for i, t := range merged.Tombstones {
			if t.NodeID == id && !tombstoneSuppresses(lastSeen, t.RemovedAt) {
				merged.Tombstones = append(merged.Tombstones[:i], merged.Tombstones[i+1:]...)
				return
			}
		}
	}
	incomingIdx := make(map[string]int, len(incoming.Nodes))
	for _, n := range incoming.Nodes {
		if n.NodeID == "" {
			continue
		}
		if t, ok := tombstoned(n.NodeID); ok && tombstoneSuppresses(n.LastSeen, t.RemovedAt) {
			continue // removed elsewhere and not rejoined: stay removed
		}
		clearTombstone(n.NodeID, n.LastSeen)
		incomingIdx[n.NodeID] = len(merged.Nodes)
		merged.Nodes = append(merged.Nodes, n)
	}
	for _, ln := range local.Nodes {
		if ln.NodeID == "" {
			continue
		}
		if t, ok := tombstoned(ln.NodeID); ok && tombstoneSuppresses(ln.LastSeen, t.RemovedAt) {
			continue // our copy is stale relative to a removal: stay removed
		}
		clearTombstone(ln.NodeID, ln.LastSeen)
		if idx, ok := incomingIdx[ln.NodeID]; ok {
			if nodeInfoFresher(ln.LastSeen, merged.Nodes[idx].LastSeen) {
				merged.Nodes[idx] = ln
			}
			continue
		}
		if ln.Status != "active" {
			slog.Debug("trust pool merge: skipping non-active local-only node",
				"node_id", ln.NodeID, "status", ln.Status)
			continue
		}
		merged.Nodes = append(merged.Nodes, ln)
	}
	return merged
}

// nodeInfoFresher 比较 RFC3339 格式的 LastSeen；a 更新返回 true。
// 任一解析失败时返回 false（调用方保留 incoming 的数据）。
func nodeInfoFresher(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA != nil || errB != nil {
		return false
	}
	return ta.After(tb)
}

const (
	// trustTombstoneMaxEntries bounds removal markers so a churned pool
	// cannot grow the snapshot without limit; the oldest are dropped first.
	trustTombstoneMaxEntries = 256
	// trustTombstoneTTL retires removal markers: a node gone longer than
	// this may be re-added by live gossip. Explicit removals younger than
	// the TTL always stick.
	trustTombstoneTTL = 30 * 24 * time.Hour
)

// tombstoneSuppresses reports whether a node record loses to a removal
// marker. Removal wins unless the record is strictly newer than the tombstone
// (a live rejoin). Unparseable or missing times fail closed toward removal:
// resurrecting a revoked node is worse than delaying a rejoin by one gossip
// round (the rejoin re-announces with a fresh timestamp and wins next time).
func tombstoneSuppresses(lastSeen, removedAt string) bool {
	if removedAt == "" {
		return true
	}
	if lastSeen == "" {
		return true
	}
	tl, errL := time.Parse(time.RFC3339, lastSeen)
	tr, errR := time.Parse(time.RFC3339, removedAt)
	if errL != nil || errR != nil {
		return true
	}
	return !tl.After(tr)
}

// unionTombstones merges two tombstone sets (newest RemovedAt wins per node),
// drops expired markers, and caps the set size (oldest first).
func unionTombstones(a, b []NodeTombstone) []NodeTombstone {
	byID := make(map[string]NodeTombstone, len(a)+len(b))
	for _, t := range append(append([]NodeTombstone(nil), a...), b...) {
		if t.NodeID == "" {
			continue
		}
		cur, ok := byID[t.NodeID]
		if !ok || tombstoneNewer(t.RemovedAt, cur.RemovedAt) {
			byID[t.NodeID] = t
		}
	}
	now := time.Now()
	out := make([]NodeTombstone, 0, len(byID))
	for _, t := range byID {
		if rt, err := time.Parse(time.RFC3339, t.RemovedAt); err == nil && now.Sub(rt) > trustTombstoneTTL {
			continue // expired marker: the node may return via live gossip
		}
		out = append(out, t)
	}
	if len(out) > trustTombstoneMaxEntries {
		sort.Slice(out, func(i, j int) bool { return out[i].RemovedAt < out[j].RemovedAt })
		out = out[len(out)-trustTombstoneMaxEntries:]
	}
	return out
}

// tombstoneNewer reports whether a is a newer removal marker than b.
// Unparseable times lose to parseable ones (fail closed toward removal).
func tombstoneNewer(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA != nil {
		return false
	}
	if errB != nil {
		return true
	}
	return ta.After(tb)
}

// UpdateNodeInfo upserts a single node entry from a gossip message.
func (f *FederationManager) UpdateNodeInfo(info NodeInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Tombstone gate (same rule as upsertKnownNodeLocked): stale gossip must
	// not resurrect an explicitly removed node; a strictly newer record is a
	// live rejoin and clears the marker.
	for i := range f.trustPool.Tombstones {
		if f.trustPool.Tombstones[i].NodeID != info.NodeID {
			continue
		}
		if tombstoneSuppresses(info.LastSeen, f.trustPool.Tombstones[i].RemovedAt) {
			slog.Debug("trust pool: gossip for removed node suppressed",
				"node_id", info.NodeID)
			return
		}
		f.trustPool.Tombstones = append(f.trustPool.Tombstones[:i], f.trustPool.Tombstones[i+1:]...)
		break
	}

	f.dhtAddNodeLocked(&info)

	for i := range f.trustPool.Nodes {
		if f.trustPool.Nodes[i].NodeID == info.NodeID {
			f.trustPool.Nodes[i] = info
			slog.Debug("trust pool node refreshed via gossip", "node_id", info.NodeID)
			if nodeRegistry != nil {
				nodeRegistry.SaveNode(routeEntryFromNodeInfo(info))
			}
			return
		}
	}
	f.localPeers[info.NodeID] = &info
	slog.Debug("gossip peer recorded", "node_id", info.NodeID, "status", info.Status)
	if nodeRegistry != nil {
		nodeRegistry.SaveNode(routeEntryFromNodeInfo(info))
	}
}

// RemoveNode removes a node from both the trust pool and local peers, and
// leaves a tombstone so the removal propagates and sticks: without it, the
// next gossip merge would resurrect the node from any peer that still lists
// it as active. The version bump drives the version-gated gossip pull, and
// the tombstone rides the pool snapshot to every peer.
func (f *FederationManager) RemoveNode(nodeID string) {
	f.removeNodeReason(nodeID, "operator removal")
}

// removeNodeReason is RemoveNode with an explicit reason recorded on the
// tombstone (e.g. "operator removal", "quarantine: failed capability probes").
func (f *FederationManager) removeNodeReason(nodeID, reason string) {
	if nodeID == "" {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	f.mu.Lock()
	filtered := make([]NodeInfo, 0, len(f.trustPool.Nodes))
	for _, n := range f.trustPool.Nodes {
		if n.NodeID != nodeID {
			filtered = append(filtered, n)
		}
	}
	f.trustPool.Nodes = filtered
	delete(f.localPeers, nodeID)
	if f.dht != nil {
		f.dht.RemoveNode(DHTNodeID(sha256.Sum256([]byte(nodeID))))
	}
	upserted := false
	for i := range f.trustPool.Tombstones {
		if f.trustPool.Tombstones[i].NodeID == nodeID {
			f.trustPool.Tombstones[i].RemovedAt = now
			if reason != "" {
				f.trustPool.Tombstones[i].Reason = reason
			}
			upserted = true
			break
		}
	}
	if !upserted {
		f.trustPool.Tombstones = append(f.trustPool.Tombstones, NodeTombstone{
			NodeID:    nodeID,
			RemovedAt: now,
			Reason:    reason,
		})
	}
	f.trustPool.Tombstones = unionTombstones(f.trustPool.Tombstones, nil)
	f.trustPool.Version++
	f.trustPool.UpdatedAt = now
	snapshot, err := f.snapshotLocked()
	dataDir := f.dataDir
	f.mu.Unlock()
	// Bare test fixtures carry no dataDir: persist only when one is set, so
	// unit tests never spill a federation_pool.json into the working dir.
	if dataDir != "" {
		if err != nil {
			slog.Error("failed to persist trust pool after node removal", "error", err)
			return
		}
		f.persistSnapshot(snapshot)
	}

	slog.Info("node removed from federation", "node_id", nodeID, "reason", reason)
}

// AddKnownNode upserts a node into the local trust pool, marking it active and
// bumping the trust pool version. This is the P0-2 "bridge": manual peers (added
// via AddPeer / notify) and any other locally-known node are merged into the
// gossip-reachable trust pool so the discovery loop can propagate them. It is a
// no-op when federation is disabled (personal mode).
func (f *FederationManager) AddKnownNode(node NodeInfo) {
	f.AddKnownNodes([]NodeInfo{node})
}

// AddKnownNodes merges multiple nodes into the trust pool with a single
// version bump and a single persist. Used at startup to re-bridge persisted
// route-table peers so gossip has someone to talk to even when the GitHub
// registry and seed nodes are unreachable (P2P rejoin: any single live node
// is enough for a restarted node to re-enter the mesh). Peers that stay
// unreachable keep a stale LastSeen and are removed by cullInactivePeers.
func (f *FederationManager) AddKnownNodes(nodes []NodeInfo) {
	if !f.IsEnabled() {
		return
	}
	f.mu.Lock()
	applied := 0
	for _, n := range nodes {
		if n.NodeID == "" {
			continue
		}
		f.upsertKnownNodeLocked(n)
		applied++
	}
	if applied == 0 {
		f.mu.Unlock()
		return
	}
	f.trustPool.Version++
	f.trustPool.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	snapshot, err := f.snapshotLocked()
	f.mu.Unlock()
	if err != nil {
		slog.Error("failed to persist trust pool after batch node addition", "error", err)
		return
	}
	f.persistSnapshot(snapshot)
}

// upsertKnownNodeLocked merges one node into the trust pool, preserving rich
// fields the new info didn't carry. Caller must hold f.mu.
//
// Trust-anchor rule: an established PubKey is NEVER overwritten by an incoming
// record. Node keys have no signed rotation protocol, so any "new key for an
// existing node" claim is indistinguishable from an impersonation attempt
// (e.g. an unsigned LAN announcement or a gossip merge carrying a forged
// identity). The existing key is kept; the key is only taken from the incoming
// record when we have none yet.
func (f *FederationManager) upsertKnownNodeLocked(node NodeInfo) {
	node.Status = "active"
	if node.LastSeen == "" {
		node.LastSeen = time.Now().UTC().Format(time.RFC3339)
	}
	// Tombstone gate: an explicitly removed node must not be resurrected by
	// a stale record (restored registry, old gossip). A strictly newer
	// LastSeen is a live rejoin: accept it and clear the marker.
	for i := range f.trustPool.Tombstones {
		if f.trustPool.Tombstones[i].NodeID != node.NodeID {
			continue
		}
		if tombstoneSuppresses(node.LastSeen, f.trustPool.Tombstones[i].RemovedAt) {
			slog.Debug("trust pool: suppressed resurrection of removed node",
				"node_id", node.NodeID)
			return
		}
		f.trustPool.Tombstones = append(f.trustPool.Tombstones[:i], f.trustPool.Tombstones[i+1:]...)
		break
	}
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
			if node.PubKey == "" || existing.PubKey != "" {
				node.PubKey = existing.PubKey
			}
			if node.Endpoint == "" {
				node.Endpoint = existing.Endpoint
			}
			if len(node.Addresses) == 0 {
				node.Addresses = existing.Addresses
			}
			f.trustPool.Nodes[i] = node
			return
		}
	}
	f.trustPool.Nodes = append(f.trustPool.Nodes, node)
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
		// Seedless DHT: remember the sender's DHT UDP address alongside the
		// HTTP hints so startDHTNode can bootstrap without configured seeds.
		if h.DHTAddr != "" {
			if _, ok := f.dhtHints[h.NodeID]; !ok {
				f.dhtHints[h.NodeID] = h.DHTAddr
			}
		}
	}
}

// NoteDHTHint records one node's DHT UDP listen address learned from a gossip
// sync message (first-known wins). No-op on empty input.
func (f *FederationManager) NoteDHTHint(nodeID, dhtAddr string) {
	if nodeID == "" || dhtAddr == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.dhtHints[nodeID]; !ok {
		f.dhtHints[nodeID] = dhtAddr
	}
}

// DHTHint returns the learned DHT UDP address for one node, or "".
func (f *FederationManager) DHTHint(nodeID string) string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.dhtHints[nodeID]
}

// DHTNodeIDForAddr returns the node ID that advertised the given DHT UDP
// address, or "" if unknown. P2P-G2: seedless bootstrap 成功后用它找到地址
// 归属节点，把验证过的地址持久化。
func (f *FederationManager) DHTNodeIDForAddr(addr string) string {
	if addr == "" {
		return ""
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	for id, a := range f.dhtHints {
		if a == addr {
			return id
		}
	}
	return ""
}

// DHTBootstrapAddrs returns all learned DHT UDP addresses (deduped, non-empty),
// excluding the given selfID. Used for seedless DHT bootstrap.
func (f *FederationManager) DHTBootstrapAddrs(selfID string) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	seen := make(map[string]bool)
	var out []string
	for id, addr := range f.dhtHints {
		if id == "" || id == selfID || addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		out = append(out, addr)
	}
	return out
}

// HintAddresses returns the PEX-learned address hints for a node (P1-1).
func (f *FederationManager) HintAddresses(nodeID string) []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.discoveryHints[nodeID]
}

// save persists the trust pool to dataDir/federation_pool.json.
// B7-supp: snapshots (marshal+HMAC input) under f.mu and performs the file
// write outside it, so relay/gossip readers are never stalled behind disk I/O.
func (f *FederationManager) save() {
	f.mu.Lock()
	b, err := f.snapshotLocked()
	f.mu.Unlock()
	if err != nil {
		slog.Error("failed to save federation pool", "error", err)
		return
	}
	f.persistSnapshot(b)
}

// snapshotLocked serializes the pool for persistence. Caller must hold f.mu.
// SA-15: integrity-protected via persistSnapshot.
func (f *FederationManager) snapshotLocked() ([]byte, error) {
	return marshalWithIntegrity(f.trustPool)
}

// persistSnapshot writes a snapshot to disk outside f.mu. Writes are
// serialized by saveMu so an older snapshot can never land after a newer one.
func (f *FederationManager) persistSnapshot(b []byte) {
	f.saveMu.Lock()
	defer f.saveMu.Unlock()
	path := filepath.Join(f.dataDir, "federation_pool.json")
	if err := writeWithIntegrity(path, b); err != nil {
		slog.Error("failed to save federation pool", "error", err)
	}
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
		snapshot, err := f.snapshotLocked()
		f.mu.Unlock()
		if err != nil {
			slog.Error("failed to persist trust pool after GitHub refresh", "error", err)
			return nil
		}
		f.persistSnapshot(snapshot)
		return nil
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

func (f *FederationManager) dhtAddNodeLocked(info *NodeInfo) {
	if f.dht == nil || info.NodeID == "" {
		return
	}
	id := DHTNodeID(sha256.Sum256([]byte(info.NodeID)))
	addresses := info.Endpoint
	if addresses == "" {
		addresses = "unknown"
	}
	f.dht.AddNode(&DHTEntry{
		NodeID:    id,
		Addresses: []string{addresses},
	})
}

func (f *FederationManager) DHTLookup(targetNodeID string, count int) []*DHTEntry {
	if f.dht == nil {
		return nil
	}
	id := DHTNodeID(sha256.Sum256([]byte(targetNodeID)))
	if count <= 0 {
		count = dhtAlpha
	}
	return f.dht.FindClosest(id, count)
}

func (f *FederationManager) DHTPut(key string, value []byte) {
	if f.dht == nil {
		return
	}
	f.dht.Put(key, value, f.dht.self)
}

func (f *FederationManager) DHTGet(key string) (*DHTRecord, bool) {
	if f.dht == nil {
		return nil, false
	}
	return f.dht.Get(key)
}
