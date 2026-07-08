package main

import (
	"testing"
)

// ============================================================
// pickBestAddress Tests
// ============================================================

func TestPickBestAddress(t *testing.T) {
	tests := []struct {
		name      string
		addresses []string
		expected  string
	}{
		{
			name:      "empty list",
			addresses: []string{},
			expected:  "",
		},
		{
			name:      "nil list",
			addresses: nil,
			expected:  "",
		},
		{
			name:      "single custom domain",
			addresses: []string{"https://node1.example.com"},
			expected:  "https://node1.example.com",
		},
		{
			name:      "prefer custom domain over tunnel",
			addresses: []string{"https://abc.trycloudflare.com", "https://node1.example.com"},
			expected:  "https://node1.example.com",
		},
		{
			name:      "prefer tunnel over localhost",
			addresses: []string{"http://localhost:8000", "https://abc.trycloudflare.com"},
			expected:  "https://abc.trycloudflare.com",
		},
		{
			name:      "only localhost",
			addresses: []string{"http://localhost:8000"},
			expected:  "http://localhost:8000",
		},
		{
			name:      "only tunnel",
			addresses: []string{"https://abc.trycloudflare.com"},
			expected:  "https://abc.trycloudflare.com",
		},
		{
			name:      "multiple custom domains returns first",
			addresses: []string{"https://node1.example.com", "https://node2.example.com"},
			expected:  "https://node1.example.com",
		},
		{
			name:      "mixed addresses",
			addresses: []string{"http://localhost:8000", "https://abc.trycloudflare.com", "https://mynode.com"},
			expected:  "https://mynode.com",
		},
		{
			name:      "non-standard address falls back",
			addresses: []string{"ftp://something.com"},
			expected:  "ftp://something.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pickBestAddress(tt.addresses)
			if result != tt.expected {
				t.Errorf("pickBestAddress(%v) = %q, want %q", tt.addresses, result, tt.expected)
			}
		})
	}
}

// ============================================================
// Relay Header Constants Tests
// ============================================================

func TestRelayHeaderConstants(t *testing.T) {
	if headerRelayHop != "X-OpenModelPool-Agent-Hop" {
		t.Errorf("headerRelayHop = %q", headerRelayHop)
	}
	if headerRelayFrom != "X-OpenModelPool-Agent-Relay-From" {
		t.Errorf("headerRelayFrom = %q", headerRelayFrom)
	}
}

// ============================================================
// RouteEntry Structure Tests
// ============================================================

func TestRouteEntryFields(t *testing.T) {
	entry := RouteEntry{
		NodeID:    "mmx-test",
		NodeName:  "Test Node",
		Addresses: []string{"https://test.example.com"},
		Status:    "online",
		Models:    []string{"gpt-4", "claude-3"},
		LatencyMS: 42.5,
		LoadScore: 0.7,
	}

	if entry.NodeID != "mmx-test" {
		t.Errorf("NodeID mismatch")
	}
	if entry.LatencyMS != 42.5 {
		t.Errorf("LatencyMS mismatch")
	}
	if entry.LoadScore != 0.7 {
		t.Errorf("LoadScore mismatch")
	}
	if len(entry.Models) != 2 {
		t.Errorf("Models length mismatch")
	}
}

// ============================================================
// Relay Hop Count Logic Tests (unit-level)
// ============================================================

func TestRelayHopCountValidation(t *testing.T) {
	tests := []struct {
		name     string
		hopCount int
		maxHops  int
		allowed  bool
	}{
		{"zero hops", 0, 3, true},
		{"one hop", 1, 3, true},
		{"two hops", 2, 3, true},
		{"at max", 3, 3, false},
		{"exceeds max", 4, 3, false},
		{"negative", -1, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed := tt.hopCount < tt.maxHops
			if allowed != tt.allowed {
				t.Errorf("hop %d < max %d: got %v, want %v", tt.hopCount, tt.maxHops, allowed, tt.allowed)
			}
		})
	}
}

// ============================================================
// Key-based Relay Routing Tests
// ============================================================

func TestRelayKeyRouting(t *testing.T) {
	// Test that different key types have different routing behaviors
	tests := []struct {
		name    string
		key     string
		keyType KeyType
	}{
		{"public key routes to pool", PublicKeyValue, KeyTypePublic},
		{"guest key routes to issuer", "sk-guest-mmx-node1-abc", KeyTypeGuest},
		{"proxy key routes freely", "sk-abc123", KeyTypeProxy},
		{"unknown key passes through", "random-key", KeyTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kt := ClassifyKey(tt.key)
			if kt != tt.keyType {
				t.Errorf("ClassifyKey(%q) = %q, want %q", tt.key, kt, tt.keyType)
			}
		})
	}
}

// ============================================================
// SelectBestNode Scoring Tests
// ============================================================

func TestSelectBestNodeScoring(t *testing.T) {
	rt := newTestRouteTable()

	// High latency, high load
	rt.Put("mmx-slow", "Slow Node", []string{"https://slow.example.com"})
	rt.mu.Lock()
	e := rt.entries["mmx-slow"]
	e.Models = []string{"gpt-4"}
	e.LatencyMS = 500
	e.LoadScore = 0.9
	rt.mu.Unlock()

	// Low latency, low load
	rt.Put("mmx-fast", "Fast Node", []string{"https://fast.example.com"})
	rt.mu.Lock()
	e2 := rt.entries["mmx-fast"]
	e2.Models = []string{"gpt-4"}
	e2.LatencyMS = 10
	e2.LoadScore = 0.1
	rt.mu.Unlock()

	// Medium latency, medium load
	rt.Put("mmx-medium", "Medium Node", []string{"https://medium.example.com"})
	rt.mu.Lock()
	e3 := rt.entries["mmx-medium"]
	e3.Models = []string{"gpt-4"}
	e3.LatencyMS = 100
	e3.LoadScore = 0.5
	rt.mu.Unlock()

	best := rt.SelectBestNode("gpt-4")
	if best == nil {
		t.Fatal("expected a node")
	}
	if best.NodeID != "mmx-fast" {
		t.Errorf("expected fastest node, got %q", best.NodeID)
	}
}

// ============================================================
// NetworkConfig Default Values Tests
// ============================================================

func TestNetworkConfigDefaults(t *testing.T) {
	cfg := NetworkConfig{
		Mode:            NetworkModePersonal,
		ConsentAccepted: false,
	}
	if cfg.Mode != NetworkModePersonal {
		t.Errorf("default mode should be personal")
	}
	if cfg.ConsentAccepted {
		t.Error("consent should default to false")
	}
	if cfg.ShareToPool {
		t.Error("share_to_pool should default to false")
	}
}

// ============================================================
// PeerInfo Tests
// ============================================================

func TestPeerInfoDefaults(t *testing.T) {
	peer := PeerInfo{
		NodeID: "mmx-test",
		Status: "online",
	}
	if peer.TrustScore != 0 {
		t.Errorf("default TrustScore should be 0, got %f", peer.TrustScore)
	}
	if peer.Unlocked {
		t.Error("default Unlocked should be false")
	}
	if peer.ShareToPool {
		t.Error("default ShareToPool should be false")
	}
}

func TestPeerInfoWithCapabilities(t *testing.T) {
	peer := PeerInfo{
		NodeID: "mmx-test",
		Capabilities: PeerCapabilities{
			Providers: []string{"openai"},
			CanRelay:  true,
			CanSeed:   true,
			Bandwidth: "1Gbps",
		},
	}
	if len(peer.Capabilities.Providers) != 1 {
		t.Error("expected 1 provider")
	}
	if !peer.Capabilities.CanRelay {
		t.Error("CanRelay should be true")
	}
}
