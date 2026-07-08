package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"sync"
	"time"
)

// ============================================================
// Kademlia DHT — 256-bit Hash Space with K-Buckets
// ============================================================
//
// This implements a Kademlia-style Distributed Hash Table (DHT)
// routing table for efficient peer discovery in the OpenModelPool
// shared network.
//
// Key design decisions:
//   - 256-bit hash space (SHA-256), exceeding BitTorrent's 160-bit
//     and matching IPFS/Kademlia standard
//   - XOR distance metric for node proximity
//   - K-buckets (k=20) per prefix-length for routing
//   - Iterative lookup algorithm for O(log N) node discovery
//
// Integration:
//   - Works alongside existing gossip protocol (Phase 1/2) and
//     RouteTable (application-level routing) for Phase 3 hybrid
//     discovery.

const (
	// HashSizeBytes is the size of the DHT hash (256 bits = 32 bytes).
	HashSizeBytes = 32

	// KBucketSize is the maximum number of nodes per k-bucket (k=20).
	// Matches standard Kademlia parameter.
	KBucketSize = 20

	// NumBuckets is the number of k-buckets (one per bit of the hash).
	NumBuckets = HashSizeBytes * 8 // 256

	// DHTLookupParallelism is the number of parallel requests during
	// iterative lookup (alpha parameter in Kademlia).
	DHTLookupParallelism = 3
)

// ============================================================
// NodeID Hash — 256-bit Position in DHT Ring
// ============================================================

// NodeHash is a 256-bit hash representing a node's position in the
// DHT hash space. Derived via SHA-256 of the node's string ID.
type NodeHash [HashSizeBytes]byte

// ComputeNodeHash computes the SHA-256 hash of a node ID string,
// producing its position in the 256-bit DHT ring.
func ComputeNodeHash(nodeID string) NodeHash {
	return sha256.Sum256([]byte(nodeID))
}

// String returns the hex representation of the hash.
func (h NodeHash) String() string {
	return hex.EncodeToString(h[:])
}

// BigInt converts the hash to a *big.Int for arithmetic operations.
func (h NodeHash) BigInt() *big.Int {
	return new(big.Int).SetBytes(h[:])
}

// XORDistance computes the XOR distance between two NodeHash values.
// This is the standard Kademlia distance metric.
func XORDistance(a, b NodeHash) *big.Int {
	aInt := a.BigInt()
	bInt := b.BigInt()
	return new(big.Int).Xor(aInt, bInt)
}

// XOR returns the XOR of two NodeHash values.
func XOR(a, b NodeHash) NodeHash {
	var result NodeHash
	for i := 0; i < HashSizeBytes; i++ {
		result[i] = a[i] ^ b[i]
	}
	return result
}

// CommonPrefixLength returns the number of leading zero bits in the
// XOR distance between two hashes. This determines which k-bucket
// a node belongs to relative to another node.
func CommonPrefixLength(a, b NodeHash) int {
	xored := XOR(a, b)
	for i := 0; i < HashSizeBytes; i++ {
		if xored[i] == 0 {
			continue
		}
		// Count leading zeros in this byte
		b := xored[i]
		zeros := 0
		for bit := 7; bit >= 0; bit-- {
			if b&(1<<uint(bit)) == 0 {
				zeros++
			} else {
				break
			}
		}
		return i*8 + zeros
	}
	return NumBuckets // identical hashes
}

// ============================================================
// K-Bucket
// ============================================================

// KBucketEntry represents a node stored in a k-bucket.
type KBucketEntry struct {
	NodeID    string    `json:"node_id"`
	Hash      NodeHash  `json:"-"`
	Endpoint  string    `json:"endpoint"`
	LastSeen  time.Time `json:"last_seen"`
	AddedAt   time.Time `json:"added_at"`
	FailCount int       `json:"fail_count"` // consecutive failed pings
}

// KBucket holds up to K nodes that share the same prefix length
// relative to the local node.
type KBucket struct {
	Entries     []KBucketEntry `json:"entries"`
	LastUpdated time.Time      `json:"last_updated"`
}

// NewKBucket creates an empty k-bucket.
func NewKBucket() *KBucket {
	return &KBucket{
		Entries:     make([]KBucketEntry, 0, KBucketSize),
		LastUpdated: time.Now(),
	}
}

// Contains checks if a node is already in the bucket.
func (kb *KBucket) Contains(nodeID string) bool {
	for _, e := range kb.Entries {
		if e.NodeID == nodeID {
			return true
		}
	}
	return false
}

// FindEntry returns the entry for a given nodeID, or nil.
func (kb *KBucket) FindEntry(nodeID string) *KBucketEntry {
	for i := range kb.Entries {
		if kb.Entries[i].NodeID == nodeID {
			return &kb.Entries[i]
		}
	}
	return nil
}

// AddOrUpdate adds a new node or updates an existing one.
// Returns true if the node was added/updated, false if the bucket
// is full and the node was not added.
func (kb *KBucket) AddOrUpdate(entry KBucketEntry) bool {
	// Check if node already exists — move to tail (most recently seen)
	for i, e := range kb.Entries {
		if e.NodeID == entry.NodeID {
			// Update existing entry
			kb.Entries[i].Endpoint = entry.Endpoint
			kb.Entries[i].LastSeen = entry.LastSeen
			kb.Entries[i].FailCount = 0

			// Move to tail (most recently seen)
			tail := kb.Entries[i]
			copy(kb.Entries[i:], kb.Entries[i+1:])
			kb.Entries[len(kb.Entries)-1] = tail

			kb.LastUpdated = time.Now()
			return true
		}
	}

	// New node — check if bucket has space
	if len(kb.Entries) < KBucketSize {
		kb.Entries = append(kb.Entries, entry)
		kb.LastUpdated = time.Now()
		return true
	}

	// Bucket full — check if head (least recently seen) has failures
	if kb.Entries[0].FailCount > 0 {
		// Replace the head (stale node) with the new entry
		copy(kb.Entries, kb.Entries[1:])
		kb.Entries[KBucketSize-1] = entry
		kb.LastUpdated = time.Now()
		return true
	}

	// Bucket full, head is healthy — cannot add
	return false
}

// Remove removes a node from the bucket by nodeID.
func (kb *KBucket) Remove(nodeID string) bool {
	for i, e := range kb.Entries {
		if e.NodeID == nodeID {
			kb.Entries = append(kb.Entries[:i], kb.Entries[i+1:]...)
			return true
		}
	}
	return false
}

// MarkFailed increments the fail count for a node.
func (kb *KBucket) MarkFailed(nodeID string) {
	for i := range kb.Entries {
		if kb.Entries[i].NodeID == nodeID {
			kb.Entries[i].FailCount++
			return
		}
	}
}

// ============================================================
// DHTTable — Kademlia Routing Table
// ============================================================

// DHTTable implements a Kademlia-style DHT routing table.
// It uses k-buckets organized by XOR distance prefix length.
type DHTTable struct {
	mu        sync.RWMutex
	localID   string              // this node's string ID
	localHash NodeHash            // SHA-256 of localID
	buckets   [NumBuckets]*KBucket // one bucket per prefix length
	nodeMap   map[string]*KBucketEntry // quick lookup by nodeID
}

var dhtTable *DHTTable

// initDHT initializes the Kademlia DHT routing table.
func initDHT() {
	if node == nil || !node.IsInitialized() {
		return
	}
	localID := node.NodeID()
	dhtTable = NewDHTTable(localID)

	// Populate from federation trust pool
	dhtTable.rebuildFromFederation()
	slog.Info("Kademlia DHT routing table initialized",
		"node_id", dhtTable.localID,
		"hash", dhtTable.localHash.String()[:16]+"...",
		"hash_bits", NumBuckets,
		"kbucket_size", KBucketSize,
		"total_nodes", dhtTable.NodeCount(),
	)
}

// NewDHTTable creates a new DHT routing table for the given local node ID.
func NewDHTTable(localID string) *DHTTable {
	d := &DHTTable{
		localID:   localID,
		localHash: ComputeNodeHash(localID),
		nodeMap:   make(map[string]*KBucketEntry),
	}
	for i := 0; i < NumBuckets; i++ {
		d.buckets[i] = NewKBucket()
	}
	return d
}

// AddNode adds or updates a node in the routing table.
// Returns true if the node was successfully added.
func (d *DHTTable) AddNode(nodeID, endpoint string) bool {
	if nodeID == d.localID {
		return false // don't add self
	}

	nodeHash := ComputeNodeHash(nodeID)
	prefixLen := CommonPrefixLength(d.localHash, nodeHash)

	// Clamp to valid bucket range (identical hashes would give NumBuckets)
	if prefixLen >= NumBuckets {
		prefixLen = NumBuckets - 1
	}

	entry := KBucketEntry{
		NodeID:   nodeID,
		Hash:     nodeHash,
		Endpoint: endpoint,
		LastSeen: time.Now(),
		AddedAt:  time.Now(),
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	added := d.buckets[prefixLen].AddOrUpdate(entry)
	if added {
		// Update nodeMap
		d.nodeMap[nodeID] = d.buckets[prefixLen].FindEntry(nodeID)
	}
	return added
}

// RemoveNode removes a node from the routing table.
func (d *DHTTable) RemoveNode(nodeID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entry, ok := d.nodeMap[nodeID]
	if !ok {
		return
	}

	prefixLen := CommonPrefixLength(d.localHash, entry.Hash)
	if prefixLen >= NumBuckets {
		prefixLen = NumBuckets - 1
	}

	d.buckets[prefixLen].Remove(nodeID)
	delete(d.nodeMap, nodeID)
}

// FindNode looks up a specific node in the routing table.
func (d *DHTTable) FindNode(nodeID string) *KBucketEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	entry, ok := d.nodeMap[nodeID]
	if !ok {
		return nil
	}
	cp := *entry
	return &cp
}

// NodeCount returns the total number of nodes in the routing table.
func (d *DHTTable) NodeCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.nodeMap)
}

// ============================================================
// dhtEntry — Backward Compatibility Type
// ============================================================

// dhtEntry is kept for backward compatibility with existing code.
// Hash field is now a truncated uint64 of the full 256-bit hash
// (used only for display/sorting purposes).
type dhtEntry struct {
	NodeID   string
	Hash     uint64 // truncated first 8 bytes of SHA-256 for compat
	Endpoint string
}

// ============================================================
// FindClosest — Kademlia Closest Nodes Query
// ============================================================

// FindClosest returns the K closest nodes to a target ID from
// the local routing table using XOR distance metric.
func (d *DHTTable) FindClosest(targetID string, count int) []dhtEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	targetHash := ComputeNodeHash(targetID)

	// Collect all nodes with their XOR distances
	type nodeDist struct {
		entry KBucketEntry
		dist  *big.Int
	}
	var allNodes []nodeDist

	for _, entry := range d.nodeMap {
		if entry.NodeID == d.localID {
			continue
		}
		dist := XORDistance(targetHash, entry.Hash)
		allNodes = append(allNodes, nodeDist{entry: *entry, dist: dist})
	}

	// Sort by XOR distance (ascending)
	sort.Slice(allNodes, func(i, j int) bool {
		return allNodes[i].dist.Cmp(allNodes[j].dist) < 0
	})

	// Return top N
	if count > len(allNodes) {
		count = len(allNodes)
	}
	result := make([]dhtEntry, count)
	for i := 0; i < count; i++ {
		result[i] = dhtEntry{
			NodeID:   allNodes[i].entry.NodeID,
			Hash:     allNodes[i].entry.Hash.BigInt().Uint64(),
			Endpoint: allNodes[i].entry.Endpoint,
		}
	}
	return result
}

// FindClosestEntries is like FindClosest but returns KBucketEntry slice.
func (d *DHTTable) FindClosestEntries(targetID string, count int) []KBucketEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	targetHash := ComputeNodeHash(targetID)

	type nodeDist struct {
		entry KBucketEntry
		dist  *big.Int
	}
	var allNodes []nodeDist

	for _, entry := range d.nodeMap {
		if entry.NodeID == d.localID {
			continue
		}
		dist := XORDistance(targetHash, entry.Hash)
		allNodes = append(allNodes, nodeDist{entry: *entry, dist: dist})
	}

	sort.Slice(allNodes, func(i, j int) bool {
		return allNodes[i].dist.Cmp(allNodes[j].dist) < 0
	})

	if count > len(allNodes) {
		count = len(allNodes)
	}
	result := make([]KBucketEntry, count)
	for i := 0; i < count; i++ {
		result[i] = allNodes[i].entry
	}
	return result
}

// ============================================================
// IterativeLookup — Full Kademlia Node Lookup
// ============================================================

// IterativeLookup performs a full iterative Kademlia lookup.
// Given a target node ID, it:
//  1. Starts with the closest nodes from the local routing table
//  2. Contacts them (via the queryFn callback) to discover closer nodes
//  3. Repeats until no closer nodes are found
//
// The queryFn callback should contact a peer and return its known
// closest nodes. If the callback returns an error, that peer is skipped.
//
// This is the core Kademlia node lookup algorithm.
func (d *DHTTable) IterativeLookup(targetID string, queryFn func(nodeID string) ([]KBucketEntry, error)) []KBucketEntry {
	targetHash := ComputeNodeHash(targetID)

	// Track visited nodes to avoid re-querying
	visited := make(map[string]bool)

	// Seed with closest nodes from local routing table
	initial := d.FindClosestEntries(targetID, DHTLookupParallelism)
	if len(initial) == 0 {
		return nil
	}

	// Candidate list — maintained sorted by XOR distance to target
	type candidate struct {
		entry KBucketEntry
		dist  *big.Int
	}
	candidates := make([]candidate, 0, KBucketSize*2)
	for _, e := range initial {
		candidates = append(candidates, candidate{
			entry: e,
			dist:  XORDistance(targetHash, e.Hash),
		})
		visited[e.NodeID] = true
	}

	// Iterative refinement
	for {
		// Sort candidates by distance
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].dist.Cmp(candidates[j].dist) < 0
		})

		// Pick alpha unvisited candidates closest to target
		var toQuery []KBucketEntry
		for _, c := range candidates {
			if !visited[c.entry.NodeID] {
				toQuery = append(toQuery, c.entry)
				visited[c.entry.NodeID] = true
			}
			if len(toQuery) >= DHTLookupParallelism {
				break
			}
		}

		if len(toQuery) == 0 {
			break // all candidates visited
		}

		// Query each selected node
		for _, q := range toQuery {
			resp, err := queryFn(q.NodeID)
			if err != nil {
				continue // skip failed queries
			}

			// Process response — add new closer nodes
			for _, entry := range resp {
				if entry.NodeID == d.localID || entry.NodeID == targetID {
					continue
				}
				if visited[entry.NodeID] {
					continue
				}

				dist := XORDistance(targetHash, entry.Hash)
				candidates = append(candidates, candidate{
					entry: entry,
					dist:  dist,
				})

				// Also add to our routing table
				d.AddNode(entry.NodeID, entry.Endpoint)
			}
		}
	}

	// Final sort and return top K
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].dist.Cmp(candidates[j].dist) < 0
	})

	result := make([]KBucketEntry, 0, KBucketSize)
	for i := 0; i < len(candidates) && i < KBucketSize; i++ {
		result = append(result, candidates[i].entry)
	}
	return result
}

// ============================================================
// Backward Compatibility — Successors/Predecessors
// ============================================================

// GetSuccessors returns the N successor nodes in XOR space
// (closest nodes to local node).
func (d *DHTTable) GetSuccessors(n int) []dhtEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	type nodeDist struct {
		entry KBucketEntry
		dist  *big.Int
	}
	var nodes []nodeDist

	for _, entry := range d.nodeMap {
		if entry.NodeID == d.localID {
			continue
		}
		dist := XORDistance(d.localHash, entry.Hash)
		nodes = append(nodes, nodeDist{entry: *entry, dist: dist})
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].dist.Cmp(nodes[j].dist) < 0
	})

	if n > len(nodes) {
		n = len(nodes)
	}
	result := make([]dhtEntry, n)
	for i := 0; i < n; i++ {
		result[i] = dhtEntry{
			NodeID:   nodes[i].entry.NodeID,
			Hash:     nodes[i].entry.Hash.BigInt().Uint64(),
			Endpoint: nodes[i].entry.Endpoint,
		}
	}
	return result
}

// GetPredecessors returns the N predecessor nodes in XOR space
// (furthest nodes among the routing table entries).
func (d *DHTTable) GetPredecessors(n int) []dhtEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	type nodeDist struct {
		entry KBucketEntry
		dist  *big.Int
	}
	var nodes []nodeDist

	for _, entry := range d.nodeMap {
		if entry.NodeID == d.localID {
			continue
		}
		dist := XORDistance(d.localHash, entry.Hash)
		nodes = append(nodes, nodeDist{entry: *entry, dist: dist})
	}

	// Sort descending — predecessors are the furthest in XOR space
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].dist.Cmp(nodes[j].dist) > 0
	})

	if n > len(nodes) {
		n = len(nodes)
	}
	result := make([]dhtEntry, n)
	for i := 0; i < n; i++ {
		result[i] = dhtEntry{
			NodeID:   nodes[i].entry.NodeID,
			Hash:     nodes[i].entry.Hash.BigInt().Uint64(),
			Endpoint: nodes[i].entry.Endpoint,
		}
	}
	return result
}

// ============================================================
// Federation Integration
// ============================================================

// rebuildFromFederation populates the DHT from the current federation trust pool.
func (d *DHTTable) rebuildFromFederation() {
	if fed == nil {
		return
	}

	pool := fed.GetTrustPool()
	for _, n := range pool.Nodes {
		if n.Status != "active" || n.Endpoint == "" {
			continue
		}
		d.AddNode(n.NodeID, n.Endpoint)
	}

	slog.Debug("DHT rebuilt from federation", "nodes_added", d.NodeCount())
}

// ============================================================
// Statistics & Info
// ============================================================

// GetDHTStats returns Kademlia DHT routing table statistics.
func GetDHTStats() map[string]any {
	if dhtTable == nil {
		return map[string]any{"enabled": false}
	}

	dhtTable.mu.RLock()
	defer dhtTable.mu.RUnlock()

	successors := dhtTable.GetSuccessors(3)
	predecessors := dhtTable.GetPredecessors(3)

	succIDs := make([]string, 0, len(successors))
	for _, s := range successors {
		succIDs = append(succIDs, s.NodeID)
	}
	predIDs := make([]string, 0, len(predecessors))
	for _, p := range predecessors {
		predIDs = append(predIDs, p.NodeID)
	}

	// Count non-empty buckets
	nonEmptyBuckets := 0
	totalEntries := 0
	for _, b := range dhtTable.buckets {
		if len(b.Entries) > 0 {
			nonEmptyBuckets++
			totalEntries += len(b.Entries)
		}
	}

	return map[string]any{
		"enabled":              true,
		"hash_bits":            NumBuckets,
		"kbucket_size":         KBucketSize,
		"total_nodes":          len(dhtTable.nodeMap),
		"non_empty_buckets":    nonEmptyBuckets,
		"total_bucket_entries": totalEntries,
		"local_hash":           dhtTable.localHash.String()[:32] + "...",
		"successors":           succIDs,
		"predecessors":         predIDs,
	}
}

// RefreshDHT rebuilds the DHT routing table from current federation state.
// Called periodically or when the trust pool changes.
func RefreshDHT() {
	if dhtTable != nil {
		dhtTable.rebuildFromFederation()
	}
}

// ============================================================
// Utility — Hex Distance Display
// ============================================================

// FormatXORDistance returns a human-readable XOR distance string.
func FormatXORDistance(a, b NodeHash) string {
	dist := XORDistance(a, b)
	distBytes := dist.Bytes()
	if len(distBytes) == 0 {
		return "00000000..."
	}
	hexStr := hex.EncodeToString(distBytes)
	if len(hexStr) > 16 {
		return hexStr[:16] + "..."
	}
	return hexStr
}

// ============================================================
// K-Bucket Statistics
// ============================================================

// GetBucketStats returns per-bucket statistics for diagnostics.
func (d *DHTTable) GetBucketStats() []map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := make([]map[string]any, 0, NumBuckets)
	for i, b := range d.buckets {
		if len(b.Entries) == 0 {
			continue
		}
		stats = append(stats, map[string]any{
			"bucket_index": i,
			"prefix_bits":  i,
			"node_count":   len(b.Entries),
			"max_capacity": KBucketSize,
			"last_updated": b.LastUpdated.Format(time.RFC3339),
		})
	}
	return stats
}

// GetRoutingTableDump returns all entries in the routing table for debugging.
func (d *DHTTable) GetRoutingTableDump() []map[string]any {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]map[string]any, 0, len(d.nodeMap))
	for _, entry := range d.nodeMap {
		prefixLen := CommonPrefixLength(d.localHash, entry.Hash)
		dist := XORDistance(d.localHash, entry.Hash)
		result = append(result, map[string]any{
			"node_id":       entry.NodeID,
			"hash":          entry.Hash.String()[:32] + "...",
			"endpoint":      entry.Endpoint,
			"prefix_length": prefixLen,
			"xor_distance":  fmt.Sprintf("%016x...", dist.Bytes()),
			"last_seen":     entry.LastSeen.Format(time.RFC3339),
			"fail_count":    entry.FailCount,
		})
	}

	// Sort by prefix length
	sort.Slice(result, func(i, j int) bool {
		pi := result[i]["prefix_length"].(int)
		pj := result[j]["prefix_length"].(int)
		return pi < pj
	})

	return result
}
