package main

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"testing"
	"time"
)

// ============================================================
// Test: SHA-256 Hash Generation
// ============================================================

func TestComputeNodeHash(t *testing.T) {
	// Test that hash is deterministic
	h1 := ComputeNodeHash("mmx-node-alpha")
	h2 := ComputeNodeHash("mmx-node-alpha")
	if h1 != h2 {
		t.Fatal("hash should be deterministic for same input")
	}

	// Test that different inputs produce different hashes
	h3 := ComputeNodeHash("mmx-node-beta")
	if h1 == h3 {
		t.Fatal("different inputs should produce different hashes")
	}

	// Verify it's actually SHA-256
	expected := sha256.Sum256([]byte("mmx-node-alpha"))
	if h1 != expected {
		t.Fatal("hash should match SHA-256 output")
	}

	// Verify hash is 256 bits (32 bytes)
	if len(h1) != HashSizeBytes {
		t.Fatalf("hash should be %d bytes, got %d", HashSizeBytes, len(h1))
	}

	t.Logf("Node hash for 'mmx-node-alpha': %s", h1.String()[:32]+"...")
}

func TestNodeHashBigInt(t *testing.T) {
	h := ComputeNodeHash("test-node")
	bi := h.BigInt()

	// Should be a positive number
	if bi.Sign() <= 0 {
		t.Fatal("BigInt should be positive")
	}

	// Should fit in 256 bits
	maxVal := new(big.Int).Lsh(big.NewInt(1), 256)
	if bi.Cmp(maxVal) >= 0 {
		t.Fatal("BigInt should be less than 2^256")
	}

	t.Logf("BigInt value: %s", bi.String()[:40]+"...")
}

// ============================================================
// Test: XOR Distance Metric
// ============================================================

func TestXORDistance(t *testing.T) {
	a := ComputeNodeHash("node-a")
	b := ComputeNodeHash("node-b")

	dist1 := XORDistance(a, b)
	dist2 := XORDistance(b, a)

	// XOR distance should be symmetric
	if dist1.Cmp(dist2) != 0 {
		t.Fatal("XOR distance should be symmetric")
	}

	// Distance to self should be zero
	distSelf := XORDistance(a, a)
	if distSelf.Sign() != 0 {
		t.Fatal("distance to self should be 0")
	}

	t.Logf("XOR distance: %s", dist1.String()[:40]+"...")
}

func TestXOR(t *testing.T) {
	var a, b NodeHash
	a[0] = 0xFF
	b[0] = 0x0F
	result := XOR(a, b)
	if result[0] != 0xF0 {
		t.Fatalf("XOR mismatch: expected 0xF0, got 0x%02X", result[0])
	}
	// Other bytes should be 0
	for i := 1; i < HashSizeBytes; i++ {
		if result[i] != 0 {
			t.Fatalf("byte %d should be 0, got %d", i, result[i])
		}
	}
}

// ============================================================
// Test: Common Prefix Length
// ============================================================

func TestCommonPrefixLength(t *testing.T) {
	// Same hash → prefix length = NumBuckets
	a := ComputeNodeHash("same")
	cpl := CommonPrefixLength(a, a)
	if cpl != NumBuckets {
		t.Fatalf("same hash should have CPL=%d, got %d", NumBuckets, cpl)
	}

	// Different first bit → CPL = 0
	var x, y NodeHash
	x[0] = 0x80 // 1000 0000
	y[0] = 0x00 // 0000 0000
	cpl = CommonPrefixLength(x, y)
	if cpl != 0 {
		t.Fatalf("different first bit should have CPL=0, got %d", cpl)
	}

	// Same first byte, different second byte first bit
	var p, q NodeHash
	p[0] = 0xFF
	q[0] = 0xFF
	p[1] = 0x80 // 1000 0000
	q[1] = 0x00 // 0000 0000
	cpl = CommonPrefixLength(p, q)
	if cpl != 8 {
		t.Fatalf("CPL should be 8 (first byte same, second differs at bit 0), got %d", cpl)
	}

	t.Logf("CPL tests passed")
}

// ============================================================
// Test: K-Bucket Operations
// ============================================================

func TestKBucketAddOrUpdate(t *testing.T) {
	kb := NewKBucket()

	// Add nodes
	for i := 0; i < KBucketSize; i++ {
		nodeID := fmt.Sprintf("node-%03d", i)
		entry := KBucketEntry{
			NodeID:   nodeID,
			Hash:     ComputeNodeHash(nodeID),
			Endpoint: fmt.Sprintf("https://node%d.example.com", i),
			LastSeen: time.Now(),
			AddedAt:  time.Now(),
		}
		if !kb.AddOrUpdate(entry) {
			t.Fatalf("should be able to add node %d (capacity=%d)", i, KBucketSize)
		}
	}

	// Bucket should be full
	if len(kb.Entries) != KBucketSize {
		t.Fatalf("bucket should have %d entries, got %d", KBucketSize, len(kb.Entries))
	}

	// Adding a new node should fail (bucket full, head healthy)
	newEntry := KBucketEntry{
		NodeID:   "new-node",
		Hash:     ComputeNodeHash("new-node"),
		Endpoint: "https://new.example.com",
		LastSeen: time.Now(),
	}
	if kb.AddOrUpdate(newEntry) {
		t.Fatal("should not be able to add to full bucket with healthy head")
	}

	// Mark head as failed
	kb.Entries[0].FailCount = 1

	// Now adding should succeed (replace stale head)
	if !kb.AddOrUpdate(newEntry) {
		t.Fatal("should be able to add when head has failures")
	}

	// Verify the old head was replaced
	if kb.Contains("node-000") {
		t.Fatal("old head should have been evicted")
	}
	if !kb.Contains("new-node") {
		t.Fatal("new node should have been added")
	}
}

func TestKBucketUpdateExisting(t *testing.T) {
	kb := NewKBucket()

	entry := KBucketEntry{
		NodeID:   "test-node",
		Hash:     ComputeNodeHash("test-node"),
		Endpoint: "https://old.example.com",
		LastSeen: time.Now(),
	}
	kb.AddOrUpdate(entry)

	// Update with new endpoint
	entry.Endpoint = "https://new.example.com"
	kb.AddOrUpdate(entry)

	// Should still have 1 entry
	if len(kb.Entries) != 1 {
		t.Fatalf("should have 1 entry, got %d", len(kb.Entries))
	}

	// Endpoint should be updated
	found := kb.FindEntry("test-node")
	if found == nil || found.Endpoint != "https://new.example.com" {
		t.Fatal("endpoint should be updated")
	}
}

func TestKBucketRemove(t *testing.T) {
	kb := NewKBucket()

	entry := KBucketEntry{
		NodeID:   "remove-me",
		Hash:     ComputeNodeHash("remove-me"),
		Endpoint: "https://example.com",
	}
	kb.AddOrUpdate(entry)

	if !kb.Remove("remove-me") {
		t.Fatal("should be able to remove existing node")
	}
	if kb.Contains("remove-me") {
		t.Fatal("node should be removed")
	}
	if kb.Remove("nonexistent") {
		t.Fatal("should return false for nonexistent node")
	}
}

// ============================================================
// Test: DHTTable Operations
// ============================================================

func TestDHTTableAddAndFind(t *testing.T) {
	localID := "local-node"
	d := NewDHTTable(localID)

	// Add 50 nodes with diverse hash prefixes
	for i := 0; i < 50; i++ {
		nodeID := fmt.Sprintf("node-%03d", i)
		endpoint := fmt.Sprintf("https://node%d.example.com", i)
		d.AddNode(nodeID, endpoint)
	}

	// Note: k-buckets have capacity limits per prefix length.
	// Not all 50 nodes may be accepted if many hash to similar prefixes.
	if d.NodeCount() > 50 {
		t.Fatalf("should have at most 50 nodes, got %d", d.NodeCount())
	}
	if d.NodeCount() < 40 {
		t.Fatalf("expected at least 40 nodes (k-bucket evictions), got %d", d.NodeCount())
	}
	t.Logf("Added %d/50 nodes (k-bucket capacity limited)", d.NodeCount())

	// Self should not be in table
	if d.FindNode(localID) != nil {
		t.Fatal("self should not be in routing table")
	}

	// FindNode for existing node
	entry := d.FindNode("node-025")
	if entry == nil {
		t.Fatal("should find node-025")
	}
	if entry.Endpoint != "https://node25.example.com" {
		t.Fatal("endpoint mismatch")
	}

	// FindNode for nonexistent
	if d.FindNode("nonexistent") != nil {
		t.Fatal("should not find nonexistent node")
	}
}

func TestDHTTableRemove(t *testing.T) {
	d := NewDHTTable("local")
	d.AddNode("node-a", "https://a.com")
	d.AddNode("node-b", "https://b.com")

	if d.NodeCount() != 2 {
		t.Fatalf("expected 2, got %d", d.NodeCount())
	}

	d.RemoveNode("node-a")
	if d.NodeCount() != 1 {
		t.Fatalf("expected 1, got %d", d.NodeCount())
	}
	if d.FindNode("node-a") != nil {
		t.Fatal("node-a should be removed")
	}
}

// ============================================================
// Test: FindClosest (XOR-based proximity)
// ============================================================

func TestFindClosest(t *testing.T) {
	d := NewDHTTable("local-node")

	// Add nodes
	nodeIDs := []string{
		"alpha", "beta", "gamma", "delta", "epsilon",
		"zeta", "eta", "theta", "iota", "kappa",
	}
	for _, id := range nodeIDs {
		d.AddNode(id, "https://"+id+".example.com")
	}

	// Find closest to a target
	closest := d.FindClosest("target-node", 5)
	if len(closest) != 5 {
		t.Fatalf("expected 5 closest, got %d", len(closest))
	}

	// Verify they are sorted by XOR distance (ascending)
	targetHash := ComputeNodeHash("target-node")
	var prevDist *big.Int
	for i, c := range closest {
		nodeHash := ComputeNodeHash(c.NodeID)
		dist := XORDistance(targetHash, nodeHash)
		if prevDist != nil && dist.Cmp(prevDist) < 0 {
			t.Fatalf("node %d should be farther than node %d", i, i-1)
		}
		prevDist = dist
	}

	t.Logf("FindClosest results:")
	for i, c := range closest {
		t.Logf("  %d: %s", i, c.NodeID)
	}
}

// ============================================================
// Test: Iterative Lookup
// ============================================================

func TestIterativeLookup(t *testing.T) {
	// Simulate a 10-node network
	d := NewDHTTable("local")

	nodeIDs := []string{
		"node-a", "node-b", "node-c", "node-d", "node-e",
		"node-f", "node-g", "node-h", "node-i", "node-j",
	}
	for _, id := range nodeIDs {
		d.AddNode(id, "https://"+id+".example.com")
	}

	// Create mock network: each node knows about its neighbors
	networkMap := make(map[string][]KBucketEntry)
	for _, id := range nodeIDs {
		hash := ComputeNodeHash(id)
		var neighbors []KBucketEntry
		for _, otherID := range nodeIDs {
			if otherID == id || otherID == "local" {
				continue
			}
			neighbors = append(neighbors, KBucketEntry{
				NodeID:   otherID,
				Hash:     ComputeNodeHash(otherID),
				Endpoint: "https://" + otherID + ".example.com",
			})
		}
		// Sort by XOR distance from this node
		for i := range neighbors {
			for j := i + 1; j < len(neighbors); j++ {
				di := XORDistance(hash, neighbors[i].Hash)
				dj := XORDistance(hash, neighbors[j].Hash)
				if dj.Cmp(di) < 0 {
					neighbors[i], neighbors[j] = neighbors[j], neighbors[i]
				}
			}
		}
		networkMap[id] = neighbors
	}

	// Define query function — simulates contacting a remote node
	queryFn := func(nodeID string) ([]KBucketEntry, error) {
		entries, ok := networkMap[nodeID]
		if !ok {
			return nil, fmt.Errorf("unknown node")
		}
		// Return up to 3 closest
		if len(entries) > 3 {
			entries = entries[:3]
		}
		return entries, nil
	}

	// Perform iterative lookup
	targetID := "target-xyz"
	results := d.IterativeLookup(targetID, queryFn)

	if len(results) == 0 {
		t.Fatal("iterative lookup should return results")
	}

	// Verify results are sorted by distance
	targetHash := ComputeNodeHash(targetID)
	var prevDist *big.Int
	for i, r := range results {
		dist := XORDistance(targetHash, r.Hash)
		if prevDist != nil && dist.Cmp(prevDist) < 0 {
			t.Fatalf("result %d should be farther than %d", i, i-1)
		}
		prevDist = dist
	}

	t.Logf("Iterative lookup returned %d nodes:", len(results))
	for i, r := range results {
		dist := XORDistance(targetHash, r.Hash)
		t.Logf("  %d: %s (dist: %s...)", i, r.NodeID, dist.String()[:16])
	}
}

// ============================================================
// Test: K-Bucket Capacity (k=20)
// ============================================================

func TestKBucketCapacity(t *testing.T) {
	kb := NewKBucket()

	// Fill to capacity
	for i := 0; i < KBucketSize+5; i++ {
		nodeID := fmt.Sprintf("node-%03d", i)
		entry := KBucketEntry{
			NodeID:   nodeID,
			Hash:     ComputeNodeHash(nodeID),
			Endpoint: fmt.Sprintf("https://node%d.com", i),
			LastSeen: time.Now(),
		}
		kb.AddOrUpdate(entry)
	}

	// Should have exactly KBucketSize entries
	if len(kb.Entries) != KBucketSize {
		t.Fatalf("k-bucket should hold exactly %d entries, got %d", KBucketSize, len(kb.Entries))
	}

	if KBucketSize != 20 {
		t.Fatalf("KBucketSize should be 20, got %d", KBucketSize)
	}
}

// ============================================================
// Test: 256-bit Hash Space Size
// ============================================================

func TestHashSpaceSize(t *testing.T) {
	// Verify hash space is at least 160 bits
	if NumBuckets < 160 {
		t.Fatalf("hash space should be at least 160 bits, got %d", NumBuckets)
	}

	// Verify it's exactly 256 bits (SHA-256)
	if NumBuckets != 256 {
		t.Fatalf("hash space should be 256 bits, got %d", NumBuckets)
	}

	// Compare with BitTorrent (160) and IPFS (256)
	t.Logf("Hash space: %d bits (BitTorrent: 160, IPFS: 256)", NumBuckets)
	t.Logf("Improvement over 16-bit: %dx", NumBuckets/16)
	t.Logf("Hash space size: 2^%d ≈ 10^%d positions", NumBuckets, 77)
}

// ============================================================
// Test: GetSuccessors / GetPredecessors
// ============================================================

func TestGetSuccessorsPredecessors(t *testing.T) {
	d := NewDHTTable("local")

	for i := 0; i < 10; i++ {
		d.AddNode(fmt.Sprintf("node-%d", i), fmt.Sprintf("https://n%d.com", i))
	}

	succs := d.GetSuccessors(3)
	if len(succs) != 3 {
		t.Fatalf("expected 3 successors, got %d", len(succs))
	}

	preds := d.GetPredecessors(3)
	if len(preds) != 3 {
		t.Fatalf("expected 3 predecessors, got %d", len(preds))
	}

	t.Logf("Successors: %v", []string{succs[0].NodeID, succs[1].NodeID, succs[2].NodeID})
	t.Logf("Predecessors: %v", []string{preds[0].NodeID, preds[1].NodeID, preds[2].NodeID})
}

// ============================================================
// Test: Node joining the DHT network
// ============================================================

func TestNodeJoinDHT(t *testing.T) {
	// Simulate: Node A creates DHT, Node B joins
	d := NewDHTTable("node-A")

	// Node B joins by adding itself to Node A's table
	added := d.AddNode("node-B", "https://nodeB.example.com")
	if !added {
		t.Fatal("Node B should be added to DHT")
	}

	// Verify Node B is findable
	closest := d.FindClosest("node-B", 1)
	if len(closest) != 1 {
		t.Fatal("should find Node B")
	}
	if closest[0].NodeID != "node-B" {
		t.Fatalf("closest to node-B should be node-B itself, got %s", closest[0].NodeID)
	}

	// Add more nodes
	for i := 0; i < 20; i++ {
		d.AddNode(fmt.Sprintf("node-%02d", i), fmt.Sprintf("https://n%02d.com", i))
	}

	// Verify node discovery works
	allClosest := d.FindClosest("target", 5)
	if len(allClosest) != 5 {
		t.Fatalf("should find 5 closest nodes, got %d", len(allClosest))
	}

	t.Logf("DHT network with %d nodes, closest 5 to 'target':", d.NodeCount())
	for _, c := range allClosest {
		t.Logf("  %s", c.NodeID)
	}
}

// ============================================================
// Test: Concurrent Access Safety
// ============================================================

func TestConcurrentAccess(t *testing.T) {
	d := NewDHTTable("local")

	done := make(chan bool, 10)

	// Concurrent adds
	for i := 0; i < 5; i++ {
		go func(idx int) {
			for j := 0; j < 100; j++ {
				nodeID := fmt.Sprintf("node-%d-%d", idx, j)
				d.AddNode(nodeID, fmt.Sprintf("https://n%d-%d.com", idx, j))
			}
			done <- true
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				d.FindClosest("some-target", 5)
				d.NodeCount()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Due to k-bucket capacity limits (20 per prefix), not all 500 nodes
	// may be retained. This is correct Kademlia behavior.
	if d.NodeCount() > 500 {
		t.Fatalf("should have at most 500 nodes, got %d", d.NodeCount())
	}
	if d.NodeCount() == 0 {
		t.Fatal("should have at least some nodes after concurrent adds")
	}
	t.Logf("Concurrent test: %d/500 nodes retained (k-bucket capacity)", d.NodeCount())
}
