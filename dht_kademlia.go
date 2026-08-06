package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// ============================================================
// §7.7/7.8: Kademlia 256-bit DHT Discovery
// ============================================================
//
// Phase 1: In-memory K-Bucket implementation with the Kademlia routing
// protocol as specified in v4 design §7.7. Full network I/O and iterative
// lookup will be connected in Phase 2.
//
// Configuration (per design §7.7):
//   Hash space:   256-bit (SHA-256)
//   Distance:     XOR metric
//   K-Bucket size: k = 20
//   Bucket count:  256 (one per bit)
//   Lookup alpha:  10 (parallel queries)
//   Lookup beta:   3 (termination condition)
//   Refresh:       10 minutes
//   Query timeout: 10 seconds
//   Record TTL:    48 hours

const (
	dhtK         = 20
	dhtAlpha     = 10
	dhtBeta      = 3
	dhtBuckets   = 256
	dhtRefresh   = 10 * time.Minute
	dhtTimeout   = 10 * time.Second
	dhtRecordTTL = 48 * time.Hour
)

// DHTNodeID is a 256-bit Kademlia node identifier.
type DHTNodeID [32]byte

// DHTEntry represents a node in the DHT routing table.
type DHTEntry struct {
	NodeID    DHTNodeID
	Addresses []string
	LastSeen  time.Time
}

// KBucket holds up to k entries for a specific XOR distance range.
type KBucket struct {
	entries []*DHTEntry
}

// DHT is the Kademlia DHT routing table.
type DHT struct {
	mu      sync.RWMutex
	self    DHTNodeID
	buckets [dhtBuckets]*KBucket
	records map[string]*DHTRecord
}

// DHTRecord is a key-value record stored in the DHT.
type DHTRecord struct {
	Key       string
	Value     []byte
	Publisher DHTNodeID
	ExpiresAt time.Time
}

// NewDHT creates a new DHT routing table centered on the given node ID.
func NewDHT(selfID string) *DHT {
	id := DHTNodeID(sha256.Sum256([]byte(selfID)))
	d := &DHT{
		self:    id,
		records: make(map[string]*DHTRecord),
	}
	for i := range d.buckets {
		d.buckets[i] = &KBucket{}
	}
	return d
}

// SelfID returns the hex-encoded self node ID.
func (d *DHT) SelfID() string {
	return hex.EncodeToString(d.self[:])
}

// XORDistance computes the XOR distance between two DHTNodeIDs.
func XORDistance(a, b DHTNodeID) DHTNodeID {
	var dist DHTNodeID
	for i := range a {
		dist[i] = a[i] ^ b[i]
	}
	return dist
}

// bucketIndex returns the bucket index for a given distance.
// The index is the position of the highest set bit.
func bucketIndex(dist DHTNodeID) int {
	for i := 0; i < 32; i++ {
		if dist[i] != 0 {
			for bit := 7; bit >= 0; bit-- {
				if dist[i]&(1<<uint(bit)) != 0 {
					return (31-i)*8 + bit
				}
			}
		}
	}
	return 0
}

// AddNode adds a node to the appropriate k-bucket.
func (d *DHT) AddNode(entry *DHTEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()

	dist := XORDistance(d.self, entry.NodeID)
	idx := bucketIndex(dist)
	bucket := d.buckets[idx]

	for i, e := range bucket.entries {
		if e.NodeID == entry.NodeID {
			bucket.entries[i] = entry
			bucket.entries[i].LastSeen = time.Now()
			return
		}
	}

	if len(bucket.entries) < dhtK {
		entry.LastSeen = time.Now()
		bucket.entries = append(bucket.entries, entry)
		return
	}

	slog.Debug("k-bucket full, ignoring node", "bucket", idx, "node_id", hex.EncodeToString(entry.NodeID[:8]))
}

// FindClosest returns the alpha closest nodes to a target ID.
func (d *DHT) FindClosest(target DHTNodeID, count int) []*DHTEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var all []*DHTEntry
	for _, bucket := range d.buckets {
		all = append(all, bucket.entries...)
	}

	sort.Slice(all, func(i, j int) bool {
		di := XORDistance(target, all[i].NodeID)
		dj := XORDistance(target, all[j].NodeID)
		for k := range di {
			if di[k] != dj[k] {
				return di[k] < dj[k]
			}
		}
		return false
	})

	if count > len(all) {
		count = len(all)
	}
	return all[:count]
}

// Put stores a record in the local DHT store.
func (d *DHT) Put(key string, value []byte, publisher DHTNodeID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records[key] = &DHTRecord{
		Key:       key,
		Value:     value,
		Publisher: publisher,
		ExpiresAt: time.Now().Add(dhtRecordTTL),
	}
}

// Get retrieves a record from the local DHT store.
func (d *DHT) Get(key string) (*DHTRecord, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	r, ok := d.records[key]
	if !ok || time.Now().After(r.ExpiresAt) {
		return nil, false
	}
	return r, true
}

// BucketStats returns the number of entries in each bucket.
func (d *DHT) BucketStats() map[int]int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	stats := make(map[int]int)
	for i, bucket := range d.buckets {
		if len(bucket.entries) > 0 {
			stats[i] = len(bucket.entries)
		}
	}
	return stats
}

// TotalNodes returns the total number of nodes in the routing table.
func (d *DHT) TotalNodes() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	total := 0
	for _, bucket := range d.buckets {
		total += len(bucket.entries)
	}
	return total
}

// ExpireRecords removes expired records from the local store.
func (d *DHT) ExpireRecords() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	expired := 0
	for key, r := range d.records {
		if now.After(r.ExpiresAt) {
			delete(d.records, key)
			expired++
		}
	}
	return expired
}

// StringToDHTID converts a hex string to a DHTNodeID.
func StringToDHTID(s string) (DHTNodeID, error) {
	var id DHTNodeID
	b, err := hex.DecodeString(s)
	if err != nil {
		return id, fmt.Errorf("invalid hex: %w", err)
	}
	if len(b) != 32 {
		return id, fmt.Errorf("expected 32 bytes, got %d", len(b))
	}
	copy(id[:], b)
	return id, nil
}

func init() {
	slog.Info("DHT Kademlia 256-bit module loaded", "k", dhtK, "alpha", dhtAlpha, "buckets", dhtBuckets)
}
