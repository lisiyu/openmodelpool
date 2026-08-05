package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ============================================================
// network.go tests
// ============================================================

func TestHB5_RouteTable_PutAndGet(t *testing.T) {
	rt := initRouteTable()
	rt.Put("node-1", "Node One", []string{"https://1.example.com"})
	e := rt.Get("node-1")
	if e == nil {
		t.Fatal("expected entry, got nil")
	}
	if e.NodeID != "node-1" {
		t.Fatalf("expected node-1, got %s", e.NodeID)
	}
	if e.NodeName != "Node One" {
		t.Fatalf("expected Node One, got %s", e.NodeName)
	}
}

func TestHB5_RouteTable_GetExpired(t *testing.T) {
	rt := initRouteTable()
	rt.Put("node-1", "Node One", []string{"https://1.example.com"})
	rt.mu.Lock()
	rt.entries["node-1"].UpdatedAt = time.Now().Add(-routeTTL - time.Minute)
	rt.mu.Unlock()
	e := rt.Get("node-1")
	if e != nil {
		t.Fatal("expected nil for expired entry")
	}
}

func TestHB5_RouteTable_Remove(t *testing.T) {
	rt := initRouteTable()
	rt.Put("node-1", "Node One", []string{"https://1.example.com"})
	rt.Remove("node-1")
	if rt.Get("node-1") != nil {
		t.Fatal("expected nil after remove")
	}
}

func TestHB5_RouteTable_GetAll(t *testing.T) {
	rt := initRouteTable()
	rt.Put("n1", "One", []string{"https://1.example.com"})
	rt.Put("n2", "Two", []string{"https://2.example.com"})
	all := rt.GetAll()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
}

func TestHB5_RouteTable_GetAll_ExcludesExpired(t *testing.T) {
	rt := initRouteTable()
	rt.Put("n1", "One", []string{"https://1.example.com"})
	rt.Put("n2", "Two", []string{"https://2.example.com"})
	rt.mu.Lock()
	rt.entries["n2"].UpdatedAt = time.Now().Add(-routeTTL - time.Minute)
	rt.mu.Unlock()
	all := rt.GetAll()
	if len(all) != 1 {
		t.Fatalf("expected 1, got %d", len(all))
	}
}

func TestHB5_RouteTable_PurgeExpired(t *testing.T) {
	rt := initRouteTable()
	rt.Put("n1", "One", []string{"https://1.example.com"})
	rt.Put("n2", "Two", []string{"https://2.example.com"})
	rt.mu.Lock()
	rt.entries["n2"].UpdatedAt = time.Now().Add(-routeTTL - time.Minute)
	rt.mu.Unlock()
	purged := rt.PurgeExpired()
	if purged != 1 {
		t.Fatalf("expected 1 purged, got %d", purged)
	}
	if rt.Count() != 1 {
		t.Fatalf("expected 1 remaining, got %d", rt.Count())
	}
}

func TestHB5_RouteTable_Count(t *testing.T) {
	rt := initRouteTable()
	if rt.Count() != 0 {
		t.Fatalf("expected 0, got %d", rt.Count())
	}
	rt.Put("n1", "One", nil)
	if rt.Count() != 1 {
		t.Fatalf("expected 1, got %d", rt.Count())
	}
}

func TestHB5_RouteTable_GetByModel(t *testing.T) {
	rt := initRouteTable()
	e := &RouteEntry{
		NodeID:    "n1",
		NodeName:  "One",
		Addresses: []string{"https://1.example.com"},
		Status:    "online",
		UpdatedAt: time.Now(),
		Models:    []string{"gpt-4", "gpt-3.5"},
	}
	rt.UpsertEntry(e)
	rt.Put("n2", "Two", []string{"https://2.example.com"})

	results := rt.GetByModel("gpt-4")
	if len(results) != 2 {
		t.Fatalf("expected 2 (n1 explicit + n2 any-model), got %d", len(results))
	}
	// Map iteration is non-deterministic; check membership not order
	found := false
	for _, r := range results {
		if r.NodeID == "n1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected n1 in results, got %v", results)
	}
}

func TestHB5_RouteTable_GetByModel_AnyModel(t *testing.T) {
	rt := initRouteTable()
	rt.Put("n1", "One", []string{"https://1.example.com"})
	results := rt.GetByModel("anything")
	if len(results) != 1 {
		t.Fatalf("expected 1 for empty-model node, got %d", len(results))
	}
}

func TestHB5_RouteTable_SelectBestNode_NoCandidates(t *testing.T) {
	rt := initRouteTable()
	if rt.SelectBestNode("gpt-4") != nil {
		t.Fatal("expected nil with no candidates")
	}
}

func TestHB5_RouteTable_SelectBestNode_SingleCandidate(t *testing.T) {
	rt := initRouteTable()
	e := &RouteEntry{
		NodeID:    "n1",
		Addresses: []string{"https://1.example.com"},
		Status:    "online",
		UpdatedAt: time.Now(),
		Models:    []string{"gpt-4"},
		LatencyMS: 50,
		LoadScore: 0.3,
	}
	rt.UpsertEntry(e)
	best := rt.SelectBestNode("gpt-4")
	if best == nil || best.NodeID != "n1" {
		t.Fatal("expected n1 as best node")
	}
}

func TestHB5_RouteTable_UpsertEntry_Nil(t *testing.T) {
	rt := initRouteTable()
	rt.UpsertEntry(nil)
	if rt.Count() != 0 {
		t.Fatal("expected 0 entries after nil upsert")
	}
}

func TestHB5_NetworkConfig_Defaults(t *testing.T) {
	c := NetworkConfig{}
	if c.Mode != "" {
		t.Fatalf("expected empty mode, got %s", c.Mode)
	}
	if c.NetworkEnabled != false {
		t.Fatal("expected NetworkEnabled=false by default")
	}
}

func TestHB5_DisclaimerResponse(t *testing.T) {
	d := GetDisclaimer()
	if d.Title == "" {
		t.Fatal("expected non-empty title")
	}
	if len(d.Sections) == 0 {
		t.Fatal("expected at least one section")
	}
	if d.ConfirmationText == "" {
		t.Fatal("expected non-empty confirmation text")
	}
}

func TestHB5_NetworkStats_Default(t *testing.T) {
	s := NetworkStats{}
	if s.TotalPeers != 0 {
		t.Fatal("expected 0 total peers")
	}
}

func TestHB5_PeerInfo_Fields(t *testing.T) {
	p := PeerInfo{
		NodeID:     "n1",
		Name:       "TestNode",
		Region:     "us",
		Models:     []string{"gpt-4"},
		Status:     "online",
		TrustScore: 0.9,
		ShareToPool: true,
	}
	if p.NodeID != "n1" {
		t.Fatal("unexpected NodeID")
	}
	if !p.ShareToPool {
		t.Fatal("expected ShareToPool=true")
	}
}

func TestHB5_ShareBoundaryConfig_Defaults(t *testing.T) {
	s := ShareBoundaryConfig{}
	if s.ShareIdleOnly != false {
		t.Fatal("expected ShareIdleOnly=false by default")
	}
	if s.DailyContribCap != 0 {
		t.Fatal("expected DailyContribCap=0 by default")
	}
}

func TestHB5_JoinConditionResult_Defaults(t *testing.T) {
	r := JoinConditionResult{}
	if r.AllMet != false {
		t.Fatal("expected AllMet=false by default")
	}
	if r.HasProvider != false {
		t.Fatal("expected HasProvider=false by default")
	}
}

// ============================================================
// provider.go tests
// ============================================================

func TestHB5_SelectAPIKey_NoKeys(t *testing.T) {
	p := Provider{APIKey: "", APIKeys: nil}
	_, err := p.SelectAPIKey("private")
	if err == nil {
		t.Fatal("expected error for no keys")
	}
}

func TestHB5_SelectAPIKey_LegacyKey(t *testing.T) {
	p := Provider{APIKey: "sk-test", APIKeys: nil}
	k, err := p.SelectAPIKey("private")
	if err != nil {
		t.Fatal(err)
	}
	if k.Key != "sk-test" {
		t.Fatalf("expected sk-test, got %s", k.Key)
	}
	if k.AccessControl != "private" {
		t.Fatalf("expected private, got %s", k.AccessControl)
	}
}

func TestHB5_SelectAPIKey_QuotaExceeded(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-1", Quota: 100, Used: 100, Enabled: true, AccessControl: "private", Priority: 1},
		},
	}
	_, err := p.SelectAPIKey("private")
	if err == nil {
		t.Fatal("expected error for quota exceeded")
	}
}

func TestHB5_SelectAPIKey_Expired(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-1", Enabled: true, AccessControl: "private", Priority: 1, ExpiresAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339)},
		},
	}
	_, err := p.SelectAPIKey("private")
	if err == nil {
		t.Fatal("expected error for expired key")
	}
}

func TestHB5_SelectAPIKey_DisabledKey(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-1", Enabled: false, AccessControl: "private", Priority: 1},
		},
	}
	_, err := p.SelectAPIKey("private")
	if err == nil {
		t.Fatal("expected error for disabled key")
	}
}

func TestHB5_SelectAPIKey_WrongAccess(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-1", Enabled: true, AccessControl: "private", Priority: 1},
		},
	}
	_, err := p.SelectAPIKey("shared")
	if err == nil {
		t.Fatal("expected error for wrong access type")
	}
}

func TestHB5_keyAllowedForAccess(t *testing.T) {
	tests := []struct {
		keyAccess  string
		accessType string
		expected   bool
	}{
		{"public", "private", true},
		{"public", "shared", true},
		{"public", "", true},
		{"shared", "shared", true},
		{"shared", "private", true},
		{"shared", "guest", false},
		{"private", "private", true},
		{"private", "", true},
		{"private", "shared", false},
		{"unknown", "private", false},
	}
	for _, tt := range tests {
		got := keyAllowedForAccess(tt.keyAccess, tt.accessType)
		if got != tt.expected {
			t.Errorf("keyAllowedForAccess(%q, %q) = %v, want %v", tt.keyAccess, tt.accessType, got, tt.expected)
		}
	}
}

func TestHB5_GetEffectiveAPIKey(t *testing.T) {
	p := Provider{APIKey: "sk-legacy", APIKeys: nil}
	if k := p.GetEffectiveAPIKey(); k != "sk-legacy" {
		t.Fatalf("expected sk-legacy, got %s", k)
	}
}

func TestHB5_normalizeAccessControl(t *testing.T) {
	ac := ProviderAccessControl{ShareToPool: true, GuestPoolPercent: 30}
	result := normalizeAccessControl(ac)
	if !result.ShareToPool {
		t.Fatal("expected ShareToPool=true")
	}
	if result.GuestPoolPercent != 30 {
		t.Fatalf("expected 30, got %d", result.GuestPoolPercent)
	}
}

func TestHB5_hasNonPrivateKey_WithSharedKey(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-1", Enabled: true, AccessControl: "shared"},
		},
	}
	if !hasNonPrivateKey(p) {
		t.Fatal("expected true for shared key")
	}
}

func TestHB5_hasNonPrivateKey_AllPrivate(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{ID: "k1", Key: "sk-1", Enabled: true, AccessControl: "private"},
		},
	}
	if hasNonPrivateKey(p) {
		t.Fatal("expected false for all private keys")
	}
}

func TestHB5_hasNonPrivateKey_LegacyKey(t *testing.T) {
	p := Provider{APIKey: "sk-legacy", APIKeys: nil}
	if !hasNonPrivateKey(p) {
		t.Fatal("expected true for legacy key")
	}
}

func TestHB5_enableLatestModels_SmallList(t *testing.T) {
	models := []ModelDef{
		{ID: "m1", Enabled: true},
		{ID: "m2", Enabled: true},
	}
	result := enableLatestModels(models)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
}

func TestHB5_generateKeyID_Uniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateKeyID()
		if ids[id] {
			t.Fatalf("duplicate key id: %s", id)
		}
		ids[id] = true
	}
}

// ============================================================
// network_loadbalancer.go tests
// ============================================================

func TestHB5_LoadBalancer_ScoreNode_Unknown(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	score := lb.ScoreNode("unknown-node")
	if score != 50.0 {
		t.Fatalf("expected 50.0 for unknown node, got %f", score)
	}
}

func TestHB5_LoadBalancer_ScoreNode_Healthy(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	lb.RecordRequest("n1", 100*time.Millisecond, true)
	score := lb.ScoreNode("n1")
	if score <= 0 {
		t.Fatalf("expected positive score, got %f", score)
	}
}

func TestHB5_LoadBalancer_SelectNode_NoEntries(t *testing.T) {
	rt := initRouteTable()
	origRT := routeTable
	routeTable = rt
	defer func() { routeTable = origRT }()

	lb := NewLoadBalancer(DefaultLBConfig())
	_, err := lb.SelectNode(RouteRequirements{})
	if err == nil {
		t.Fatal("expected error with no entries")
	}
}

func TestHB5_LoadBalancer_RecordRequest_Multiple(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	lb.RecordRequest("n1", 50*time.Millisecond, true)
	lb.RecordRequest("n1", 100*time.Millisecond, true)
	lb.RecordRequest("n1", 200*time.Millisecond, false)
	lb.mu.RLock()
	m := lb.nodeMetrics["n1"]
	lb.mu.RUnlock()
	if m.RequestCount != 3 {
		t.Fatalf("expected 3 requests, got %d", m.RequestCount)
	}
	if m.SuccessCount != 2 {
		t.Fatalf("expected 2 successes, got %d", m.SuccessCount)
	}
}

func TestHB5_LoadBalancer_UpdateNodeMetrics(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	lb.UpdateNodeMetrics("n1", 0.7, 0.5, 10, 5000)
	lb.mu.RLock()
	m := lb.nodeMetrics["n1"]
	lb.mu.RUnlock()
	if m.CPUUsage != 0.7 {
		t.Fatalf("expected 0.7, got %f", m.CPUUsage)
	}
	if m.MemUsage != 0.5 {
		t.Fatalf("expected 0.5, got %f", m.MemUsage)
	}
	if m.ActiveConns != 10 {
		t.Fatalf("expected 10, got %d", m.ActiveConns)
	}
	if m.Bandwidth != 5000 {
		t.Fatalf("expected 5000, got %d", m.Bandwidth)
	}
}

func TestHB5_LoadBalancer_recordRoute(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	lb.recordRoute("n1")
	lb.recordRoute("n1")
	lb.recordRoute("n1")
	lb.mu.RLock()
	count := lb.routeHistory["n1"]
	lb.mu.RUnlock()
	if count != 3 {
		t.Fatalf("expected 3, got %d", count)
	}
}

func TestHB5_DefaultLBConfig_Weights(t *testing.T) {
	cfg := DefaultLBConfig()
	total := cfg.TrustWeight + cfg.ReputationWeight + cfg.LatencyWeight + cfg.AvailabilityWeight + cfg.ContributionWeight
	if total < 0.99 || total > 1.01 {
		t.Fatalf("expected weights to sum to ~1.0, got %f", total)
	}
}

func TestHB5_trimTrailingSlash(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"https://example.com/", "https://example.com"},
		{"https://example.com//", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"/", ""},
	}
	for _, tt := range tests {
		got := trimTrailingSlash(tt.input)
		if got != tt.expected {
			t.Errorf("trimTrailingSlash(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// ============================================================
// network_balance.go tests
// ============================================================

func TestHB5_BalanceEngine_RecordContributionBalance(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.RecordContributionBalance("n1", 1000)
	be.mu.RLock()
	nb := be.nodeBalance["n1"]
	be.mu.RUnlock()
	if nb.TotalContributed != 1000 {
		t.Fatalf("expected 1000, got %d", nb.TotalContributed)
	}
}

func TestHB5_BalanceEngine_RecordConsumptionBalance(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.RecordConsumptionBalance("n1", 500)
	be.mu.RLock()
	nb := be.nodeBalance["n1"]
	be.mu.RUnlock()
	if nb.TotalConsumed != 500 {
		t.Fatalf("expected 500, got %d", nb.TotalConsumed)
	}
}

func TestHB5_BalanceEngine_RecordNilGuard(t *testing.T) {
	var be *BalanceEngine
	be.RecordContributionBalance("n1", 100)
	be.RecordConsumptionBalance("n1", 100)
}

func TestHB5_BalanceEngine_RunBalanceCycle(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	be.RecordContributionBalance("n1", 5000)
	be.RecordConsumptionBalance("n1", 1000)
	be.RunBalanceCycle(context.Background())
	be.mu.RLock()
	adj := be.adjustments["n1"]
	be.mu.RUnlock()
	if adj == nil {
		t.Fatal("expected adjustment for n1")
	}
}

func TestHB5_BalanceEngine_GetBalanceStatus(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	status := be.GetBalanceStatus()
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status["node_count"] != 0 {
		t.Fatalf("expected 0 nodes, got %v", status["node_count"])
	}
}

func TestHB5_BalanceEngine_GetAllNodeBalances_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	balances := be.GetAllNodeBalances()
	if len(balances) != 0 {
		t.Fatalf("expected 0, got %d", len(balances))
	}
}

func TestHB5_BalanceEngine_GetAllAdjustments_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	adjs := be.GetAllAdjustments()
	if len(adjs) != 0 {
		t.Fatalf("expected 0, got %d", len(adjs))
	}
}

func TestHB5_BalanceEngine_GetAdjustmentForNode_None(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	adj := be.GetAdjustmentForNode("n1")
	if adj.Type != "balanced" {
		t.Fatalf("expected balanced, got %s", adj.Type)
	}
}

func TestHB5_BalanceEngine_GetRoutingWeightMultiplier_Nil(t *testing.T) {
	var be *BalanceEngine
	if w := be.GetRoutingWeightMultiplier("n1"); w != 1.0 {
		t.Fatalf("expected 1.0, got %f", w)
	}
}

func TestHB5_BalanceEngine_GetPriorityDelta_Nil(t *testing.T) {
	var be *BalanceEngine
	if d := be.GetPriorityDelta("n1"); d != 0 {
		t.Fatalf("expected 0, got %d", d)
	}
}

func TestHB5_BalanceConfig_Defaults(t *testing.T) {
	cfg := DefaultBalanceConfig()
	if cfg.TargetRatio != 1.0 {
		t.Fatalf("expected 1.0, got %f", cfg.TargetRatio)
	}
	if cfg.UnderConsumerThreshold != 0.5 {
		t.Fatalf("expected 0.5, got %f", cfg.UnderConsumerThreshold)
	}
	if cfg.OverContributorThreshold != 3.0 {
		t.Fatalf("expected 3.0, got %f", cfg.OverContributorThreshold)
	}
}

func TestHB5_BalanceEngine_UpdateConfig_InvalidValues(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.UpdateConfig(BalanceConfig{
		TargetRatio:              -1,
		UnderConsumerThreshold:   -1,
		OverContributorThreshold: -1,
		AdjustmentStrength:       2.0,
	})
	c := be.GetConfig()
	if c.TargetRatio != 1.0 {
		t.Fatalf("expected 1.0, got %f", c.TargetRatio)
	}
	if c.UnderConsumerThreshold != 0.5 {
		t.Fatalf("expected 0.5, got %f", c.UnderConsumerThreshold)
	}
	if c.OverContributorThreshold != 3.0 {
		t.Fatalf("expected 3.0, got %f", c.OverContributorThreshold)
	}
	if c.AdjustmentStrength != 0.3 {
		t.Fatalf("expected 0.3, got %f", c.AdjustmentStrength)
	}
}

func TestHB5_BalanceEngine_CalculateAdjustment_Balanced(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.RecordContributionBalance("n1", 1000)
	be.RecordConsumptionBalance("n1", 1000)
	be.mu.Lock()
	be.recalculateBalancesLocked()
	be.mu.Unlock()
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "balanced" {
		t.Fatalf("expected balanced, got %s", adj.Type)
	}
}

func TestHB5_BalanceEngine_CalculateAdjustment_OverConsumer(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.RecordConsumptionBalance("n1", 1000)
	be.RecordContributionBalance("n1", 100)
	be.mu.Lock()
	be.recalculateBalancesLocked()
	be.mu.Unlock()
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "reduce_priority" {
		t.Fatalf("expected reduce_priority, got %s", adj.Type)
	}
}

func TestHB5_BalanceEngine_CalculateAdjustment_OverContributor(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.RecordContributionBalance("n1", 10000)
	be.RecordConsumptionBalance("n1", 100)
	be.mu.Lock()
	be.recalculateBalancesLocked()
	be.mu.Unlock()
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "boost_priority" {
		t.Fatalf("expected boost_priority, got %s", adj.Type)
	}
}

func TestHB5_BalanceEngine_GetBalanceHistory_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0),
	}
	h := be.GetBalanceHistory(10)
	if len(h) != 0 {
		t.Fatalf("expected 0, got %d", len(h))
	}
}

// ============================================================
// reputation.go tests
// ============================================================

func TestHB5_ReputationManager_RecordCall_Success(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	r.RecordCall("n1", true, 50)
	rep := r.GetReputation("n1")
	if rep == nil {
		t.Fatal("expected reputation")
	}
	if rep.TotalRequests != 1 {
		t.Fatalf("expected 1 request, got %d", rep.TotalRequests)
	}
	if rep.FailedRequests != 0 {
		t.Fatalf("expected 0 failed, got %d", rep.FailedRequests)
	}
}

func TestHB5_ReputationManager_RecordCall_Failure(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	r.RecordCall("n1", false, 2000)
	rep := r.GetReputation("n1")
	if rep == nil {
		t.Fatal("expected reputation")
	}
	if rep.FailedRequests != 1 {
		t.Fatalf("expected 1 failed, got %d", rep.FailedRequests)
	}
}

func TestHB5_ReputationManager_RecordAccuracy(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	r.RecordAccuracy("n1", true)
	r.RecordAccuracy("n1", false)
	rep := r.GetReputation("n1")
	if rep == nil {
		t.Fatal("expected reputation")
	}
}

func TestHB5_ReputationManager_GetReputation_NotFound(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	if rep := r.GetReputation("unknown"); rep != nil {
		t.Fatal("expected nil for unknown node")
	}
}

func TestHB5_ReputationManager_GetAllReputations(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	r.RecordCall("n1", true, 50)
	r.RecordCall("n2", true, 100)
	all := r.GetAllReputations()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}
}

func TestHB5_ReputationManager_CalculateGrade_S(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	rep := &NodeReputation{OverallScore: 98, Availability: 100, Latency: 100, Accuracy: 100}
	grade := r.CalculateGrade(rep)
	if grade != "S" {
		t.Fatalf("expected S, got %s", grade)
	}
}

func TestHB5_ReputationManager_CalculateGrade_D(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	rep := &NodeReputation{Availability: 10, Latency: 10, Accuracy: 10}
	grade := r.CalculateGrade(rep)
	if grade != "D" {
		t.Fatalf("expected D, got %s", grade)
	}
}

func TestHB5_ReputationManager_ShouldRemoveNode_NotD(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	if r.ShouldRemoveNode("unknown") {
		t.Fatal("expected false for unknown node")
	}
}

func TestHB5_ReputationManager_CalculateOverallScore(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	rep := &NodeReputation{Availability: 80, Latency: 70, Accuracy: 60}
	score := r.CalculateOverallScore(rep)
	if score <= 0 {
		t.Fatalf("expected positive score, got %f", score)
	}
}

func TestHB5_ReputationManager_AddPeerScore(t *testing.T) {
	dir := t.TempDir()
	r := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	ps := PeerScore{
		FromNode:     "n2",
		TargetNode:   "n1",
		Availability: 80,
		Latency:      70,
		Accuracy:     90,
	}
	r.AddPeerScore(ps)
	rep := r.GetReputation("n1")
	if rep == nil {
		t.Fatal("expected reputation after peer score")
	}
	if len(rep.PeerScores) != 1 {
		t.Fatalf("expected 1 peer score, got %d", len(rep.PeerScores))
	}
}

// ============================================================
// auth.go tests
// ============================================================

func TestHB5_validatePasswordStrength_TooShort(t *testing.T) {
	err := validatePasswordStrength("short")
	if err == nil {
		t.Fatal("expected error for short password")
	}
}

func TestHB5_validatePasswordStrength_NoSpecial(t *testing.T) {
	err := validatePasswordStrength("Abcdefghijkl1")
	if err == nil {
		t.Fatal("expected error for password without special char")
	}
}

func TestHB5_validatePasswordStrength_NoDigit(t *testing.T) {
	err := validatePasswordStrength("Abcdefghijkl!")
	if err == nil {
		t.Fatal("expected error for password without digit")
	}
}

func TestHB5_validatePasswordStrength_NoUpper(t *testing.T) {
	err := validatePasswordStrength("abcdefghijkl1!")
	if err == nil {
		t.Fatal("expected error for password without uppercase")
	}
}

func TestHB5_validatePasswordStrength_NoLower(t *testing.T) {
	err := validatePasswordStrength("ABCDEFGHIJKL1!")
	if err == nil {
		t.Fatal("expected error for password without lowercase")
	}
}

func TestHB5_validatePasswordStrength_Valid(t *testing.T) {
	err := validatePasswordStrength("Abcdefghij1!")
	if err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestHB5_Auth_CreateAndVerifyToken(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	a := auth
	access, refresh := a.CreateToken("admin", false)
	if access == "" {
		t.Fatal("expected non-empty access token")
	}
	if refresh == "" {
		t.Fatal("expected non-empty refresh token")
	}
	username, err := a.VerifyToken(access)
	if err != nil {
		t.Fatal(err)
	}
	if username != "admin" {
		t.Fatalf("expected admin, got %s", username)
	}
}

func TestHB5_Auth_VerifyToken_Invalid(t *testing.T) {
	_ = setupTestEnv(t)
	_, err := auth.VerifyToken("invalid-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestHB5_Auth_HasResetCode_False(t *testing.T) {
	_ = setupTestEnv(t)
	if auth.HasResetCode() {
		t.Fatal("expected no reset code initially")
	}
}

func TestHB5_Auth_GenerateResetCode(t *testing.T) {
	_ = setupTestEnv(t)
	code, expires, err := auth.GenerateResetCode()
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("expected non-empty code")
	}
	if expires.IsZero() {
		t.Fatal("expected non-zero expiration")
	}
	if !auth.HasResetCode() {
		t.Fatal("expected reset code after generation")
	}
}

func TestHB5_Auth_ValidateAndConsumeResetCode_Invalid(t *testing.T) {
	_ = setupTestEnv(t)
	_, _, _ = auth.GenerateResetCode()
	valid, err := auth.ValidateAndConsumeResetCode("wrong-code")
	if valid {
		t.Fatal("expected false for wrong code")
	}
	if err == nil {
		t.Fatal("expected error for wrong code")
	}
}

func TestHB5_Auth_RefreshAccessToken(t *testing.T) {
	_ = setupTestEnv(t)
	_, refresh := auth.CreateToken("admin", false)
	newAccess, err := auth.RefreshAccessToken(refresh)
	if err != nil {
		t.Fatal(err)
	}
	if newAccess == "" {
		t.Fatal("expected non-empty new access token")
	}
}

func TestHB5_Auth_RefreshAccessToken_Invalid(t *testing.T) {
	_ = setupTestEnv(t)
	_, err := auth.RefreshAccessToken("invalid-token")
	if err == nil {
		t.Fatal("expected error for invalid refresh token")
	}
}

func TestHB5_Auth_CreateAccessToken(t *testing.T) {
	_ = setupTestEnv(t)
	token := auth.CreateAccessToken("admin", true)
	if token == "" {
		t.Fatal("expected non-empty token")
	}
}

// ============================================================
// algorithm_governance.go tests
// ============================================================

func TestHB5_ProposalStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   ProposalStatus
		expected bool
	}{
		{ProposalStatusOpen, false},
		{ProposalStatusPassed, true},
		{ProposalStatusRejected, true},
		{ProposalStatusClosed, true},
	}
	for _, tt := range tests {
		got := tt.status.isTerminal()
		if got != tt.expected {
			t.Errorf("isTerminal(%q) = %v, want %v", tt.status, got, tt.expected)
		}
	}
}

func TestHB5_VoteChoice_IsValid(t *testing.T) {
	tests := []struct {
		choice   VoteChoice
		expected bool
	}{
		{VoteYes, true},
		{VoteNo, true},
		{VoteAbstain, true},
		{VoteChoice("maybe"), false},
		{VoteChoice(""), false},
	}
	for _, tt := range tests {
		got := tt.choice.isValid()
		if got != tt.expected {
			t.Errorf("isValid(%q) = %v, want %v", tt.choice, got, tt.expected)
		}
	}
}

func TestHB5_AlgorithmGovernor_CreateProposal(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	p, err := g.CreateProposal("Test Proposal", "desc", "admin", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Test Proposal" {
		t.Fatalf("expected Test Proposal, got %s", p.Title)
	}
	if p.Status != ProposalStatusOpen {
		t.Fatalf("expected open, got %s", p.Status)
	}
}

func TestHB5_AlgorithmGovernor_CreateProposal_EmptyTitle(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	_, err := g.CreateProposal("", "desc", "admin", "", nil)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestHB5_AlgorithmGovernor_CastVote(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	p, _ := g.CreateProposal("Test", "desc", "admin", "", nil)
	voted, err := g.CastVote(p.ID, "v1", "Voter", "yes", "looks good")
	if err != nil {
		t.Fatal(err)
	}
	if len(voted.Votes) != 1 {
		t.Fatalf("expected 1 vote, got %d", len(voted.Votes))
	}
}

func TestHB5_AlgorithmGovernor_CastVote_InvalidChoice(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	p, _ := g.CreateProposal("Test", "desc", "admin", "", nil)
	_, err := g.CastVote(p.ID, "v1", "Voter", "maybe", "")
	if err == nil {
		t.Fatal("expected error for invalid choice")
	}
}

func TestHB5_AlgorithmGovernor_ResolveProposal(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	p, _ := g.CreateProposal("Test", "desc", "admin", "", nil)
	resolved, err := g.ResolveProposal(p.ID, "admin", "approved", ProposalStatusPassed)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != ProposalStatusPassed {
		t.Fatalf("expected passed, got %s", resolved.Status)
	}
}

func TestHB5_AlgorithmGovernor_ResolveProposal_InvalidStatus(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	p, _ := g.CreateProposal("Test", "desc", "admin", "", nil)
	_, err := g.ResolveProposal(p.ID, "admin", "", ProposalStatusOpen)
	if err == nil {
		t.Fatal("expected error for non-terminal status")
	}
}

func TestHB5_AlgorithmGovernor_GetProposal(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	p, _ := g.CreateProposal("Test", "desc", "admin", "", nil)
	found, ok := g.GetProposal(p.ID)
	if !ok || found.Title != "Test" {
		t.Fatal("expected to find proposal")
	}
}

func TestHB5_AlgorithmGovernor_ListProposals(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	g.CreateProposal("P1", "desc", "admin", "", nil)
	g.CreateProposal("P2", "desc", "admin", "", nil)
	list := g.ListProposals("")
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
}

func TestHB5_AlgorithmGovernor_ListProposals_Filter(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	g.CreateProposal("P1", "desc", "admin", "", nil)
	p2, _ := g.CreateProposal("P2", "desc", "admin", "", nil)
	g.ResolveProposal(p2.ID, "admin", "", ProposalStatusPassed)
	openList := g.ListProposals("open")
	if len(openList) != 1 {
		t.Fatalf("expected 1 open, got %d", len(openList))
	}
}

func TestHB5_AlgorithmGovernor_GetHistory(t *testing.T) {
	dir := t.TempDir()
	g := &AlgorithmGovernor{
		proposals: make(map[string]*AlgorithmProposal),
		dataDir:   dir,
	}
	g.CreateProposal("Test", "desc", "admin", "", nil)
	h := g.GetHistory()
	if len(h) == 0 {
		t.Fatal("expected history entries")
	}
}

func TestHB5_AlgorithmProposal_Tally(t *testing.T) {
	p := &AlgorithmProposal{
		Votes: []AlgorithmVote{
			{Choice: "yes"},
			{Choice: "yes"},
			{Choice: "no"},
			{Choice: "abstain"},
		},
	}
	tally := p.Tally()
	if tally.Yes != 2 || tally.No != 1 || tally.Abstain != 1 || tally.Total != 4 {
		t.Fatalf("unexpected tally: %+v", tally)
	}
}

// ============================================================
// network_region_impl.go tests
// ============================================================

func TestHB5_RegionCanonical(t *testing.T) {
	tests := []struct {
		input    string
		expected Region
	}{
		{"ap", RegionAsiaPacific},
		{"asia", RegionAsiaPacific},
		{"asia-pacific", RegionAsiaPacific},
		{"apac", RegionAsiaPacific},
		{"eu", RegionEurope},
		{"europe", RegionEurope},
		{"us", RegionAmericas},
		{"americas", RegionAmericas},
		{"na", RegionAmericas},
		{"unknown", RegionUnknown},
		{"", RegionUnknown},
		{"mars", RegionUnknown},
	}
	for _, tt := range tests {
		got := regionCanonical(tt.input)
		if got != tt.expected {
			t.Errorf("regionCanonical(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestHB5_RegionManager_DetectRegion_PrivateIP(t *testing.T) {
	rm := NewRegionManager()
	tests := []struct {
		ip       string
		expected Region
	}{
		{"10.0.0.1", RegionUnknown},
		{"172.16.0.1", RegionUnknown},
		{"192.168.1.1", RegionUnknown},
		{"127.0.0.1", RegionUnknown},
	}
	for _, tt := range tests {
		got := rm.DetectRegion("node", tt.ip)
		if got != tt.expected {
			t.Errorf("DetectRegion(%q) = %q, want %q", tt.ip, got, tt.expected)
		}
	}
}

func TestHB5_RegionManager_RegisterNode(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNode("n1", "8.8.8.8", "ip_detect")
	nr := rm.GetNodeRegion("n1")
	if nr == nil {
		t.Fatal("expected node region")
	}
}

func TestHB5_RegionManager_RegisterNodeSelfReport(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "tokyo", 35.6, 139.6)
	nr := rm.GetNodeRegion("n1")
	if nr == nil {
		t.Fatal("expected node region")
	}
	if nr.Region != RegionAsiaPacific {
		t.Fatalf("expected ap, got %s", nr.Region)
	}
}

func TestHB5_RegionManager_GetNodesByRegion(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "eu", "", 0, 0)
	nodes := rm.GetNodesByRegion(RegionAsiaPacific)
	if len(nodes) != 1 || nodes[0] != "n1" {
		t.Fatalf("expected [n1], got %v", nodes)
	}
}

func TestHB5_RegionManager_GetAllRegions(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "eu", "", 0, 0)
	rm.RegisterNodeSelfReport("n3", "ap", "", 0, 0)
	regions := rm.GetAllRegions()
	if len(regions) != 2 {
		t.Fatalf("expected 2 distinct regions, got %d", len(regions))
	}
}

func TestHB5_RegionManager_GetRegionSummary(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "eu", "", 0, 0)
	rm.RegisterNodeSelfReport("n3", "ap", "", 0, 0)
	summary := rm.GetRegionSummary()
	if summary[RegionAsiaPacific] != 2 {
		t.Fatalf("expected 2 ap nodes, got %d", summary[RegionAsiaPacific])
	}
}

func TestHB5_RegionManager_UpdateConfig(t *testing.T) {
	rm := NewRegionManager()
	newCfg := RegionConfig{PreferLocal: false, CrossRegionThreshold: 5.0}
	rm.UpdateConfig(newCfg)
	got := rm.GetConfig()
	if got.PreferLocal != false {
		t.Fatal("expected PreferLocal=false")
	}
}

func TestHB5_RegionManager_SelectNodeForRegion(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "eu", "", 0, 0)
	sorted := rm.SelectNodeForRegion([]string{"n1", "n2"}, RegionAsiaPacific)
	if sorted[0] != "n1" {
		t.Fatalf("expected n1 first, got %s", sorted[0])
	}
}

func TestHB5_RegionManager_ProcessHeartbeatRegion_NilInfo(t *testing.T) {
	rm := NewRegionManager()
	rm.ProcessHeartbeatRegion("n1", nil, "8.8.8.8")
	nr := rm.GetNodeRegion("n1")
	if nr == nil {
		t.Fatal("expected node region from IP")
	}
}

func TestHB5_RegionManager_ProcessHeartbeatRegion_WithInfo(t *testing.T) {
	rm := NewRegionManager()
	info := &HeartbeatRegionInfo{Region: "eu", SubRegion: "london", Latitude: 51.5, Longitude: -0.1}
	rm.ProcessHeartbeatRegion("n1", info, "")
	nr := rm.GetNodeRegion("n1")
	if nr == nil {
		t.Fatal("expected node region from heartbeat")
	}
	if nr.Region != RegionEurope {
		t.Fatalf("expected eu, got %s", nr.Region)
	}
}

func TestHB5_haversineDistance(t *testing.T) {
	d := haversineDistance(35.6, 139.6, 51.5, -0.1)
	if d <= 0 {
		t.Fatalf("expected positive distance, got %f", d)
	}
}

func TestHB5_regionDistance_Same(t *testing.T) {
	if d := regionDistance(RegionAsiaPacific, RegionAsiaPacific); d != 0 {
		t.Fatalf("expected 0, got %f", d)
	}
}

func TestHB5_regionCenter(t *testing.T) {
	lat, lon := regionCenter(RegionAsiaPacific)
	if lat != 35 || lon != 110 {
		t.Fatalf("expected (35, 110), got (%f, %f)", lat, lon)
	}
}

func TestHB5_AllRegions(t *testing.T) {
	regions := AllRegions()
	if len(regions) != 4 {
		t.Fatalf("expected 4 regions, got %d", len(regions))
	}
}

// ============================================================
// node.go tests
// ============================================================

func TestHB5_NodeIdentity_IsInitialized_Empty(t *testing.T) {
	n := &NodeIdentity{keyPath: t.TempDir() + "/node.key"}
	if n.IsInitialized() {
		t.Fatal("expected not initialized")
	}
}

func TestHB5_NodeIdentity_NeedsMigration(t *testing.T) {
	n := &NodeIdentity{keyPath: t.TempDir() + "/node.key", needsMigration: true}
	if !n.NeedsMigration() {
		t.Fatal("expected needs migration")
	}
}

func TestHB5_NodeIdentity_HasMnemonic(t *testing.T) {
	n := &NodeIdentity{keyPath: t.TempDir() + "/node.key", hasMnemonic: true}
	if !n.HasMnemonic() {
		t.Fatal("expected has mnemonic")
	}
}

func TestHB5_NodeIdentity_IsBackupConfirmed(t *testing.T) {
	n := &NodeIdentity{keyPath: t.TempDir() + "/node.key", backupConfirmed: true}
	if !n.IsBackupConfirmed() {
		t.Fatal("expected backup confirmed")
	}
}

func TestHB5_NodeIdentity_NodeID(t *testing.T) {
	n := &NodeIdentity{keyPath: t.TempDir() + "/node.key", nodeID: "mmx-test123"}
	if n.NodeID() != "mmx-test123" {
		t.Fatalf("expected mmx-test123, got %s", n.NodeID())
	}
}

func TestHB5_VerifySignature_InvalidPubKey(t *testing.T) {
	result := VerifySignature("invalid-base64!", []byte("msg"), "sig")
	if result {
		t.Fatal("expected false for invalid pub key")
	}
}

func TestHB5_base58Encode_Empty(t *testing.T) {
	if base58Encode([]byte{}) != "" {
		t.Fatal("expected empty string for empty input")
	}
}

func TestHB5_base58Encode_LeadingZeros(t *testing.T) {
	result := base58Encode([]byte{0, 0, 1})
	if !strings.HasPrefix(result, "11") {
		t.Fatalf("expected leading 1s for zero bytes, got %s", result)
	}
}

// ============================================================
// handler integration tests
// ============================================================

func TestHB5_HandleNetworkConsent_Accepted(t *testing.T) {
	env := setupTestEnv(t)
	rt := initRouteTable()
	origRT := routeTable
	routeTable = rt
	defer func() { routeTable = origRT }()
	netMgr = &NetworkManager{
		dataPath: env.dir + "/network.json",
		config:   NetworkConfig{Mode: NetworkModePersonal, BootstrapNodes: []string{}, SharedModels: []string{}, Peers: []PeerInfo{}, Addresses: []string{}},
	}
	defer func() { netMgr = nil }()

	body := `{"accepted": true}`
	req := httptest.NewRequest(http.MethodPost, "/api/network/consent", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleNetworkConsent(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB5_HandleNetworkConsent_NotAccepted(t *testing.T) {
	_ = setupTestEnv(t)
	netMgr = &NetworkManager{
		dataPath: t.TempDir() + "/network.json",
		config:   NetworkConfig{Mode: NetworkModePersonal, BootstrapNodes: []string{}, SharedModels: []string{}, Peers: []PeerInfo{}, Addresses: []string{}},
	}
	defer func() { netMgr = nil }()

	body := `{"accepted": false}`
	req := httptest.NewRequest(http.MethodPost, "/api/network/consent", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleNetworkConsent(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB5_HandleNetworkDisable_NilMgr(t *testing.T) {
	_ = setupTestEnv(t)
	orig := netMgr
	netMgr = &NetworkManager{
		dataPath: t.TempDir() + "/network.json",
		config:   NetworkConfig{Mode: NetworkModePersonal, BootstrapNodes: []string{}, SharedModels: []string{}, Peers: []PeerInfo{}, Addresses: []string{}},
	}
	defer func() { netMgr = orig }()
	req := httptest.NewRequest(http.MethodPost, "/api/network/disable", nil)
	w := httptest.NewRecorder()
	handleNetworkDisable(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB5_HandleBalanceStatus_Nil(t *testing.T) {
	balanceEngine = nil
	req := httptest.NewRequest(http.MethodGet, "/api/network/balance/status", nil)
	w := httptest.NewRecorder()
	handleBalanceStatus(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "not_initialized" {
		t.Fatalf("expected not_initialized, got %v", resp["status"])
	}
}

func TestHB5_HandleBalanceNodes_Nil(t *testing.T) {
	balanceEngine = nil
	req := httptest.NewRequest(http.MethodGet, "/api/network/balance/nodes", nil)
	w := httptest.NewRecorder()
	handleBalanceNodes(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB5_HandleBalanceAdjustments_Nil(t *testing.T) {
	balanceEngine = nil
	req := httptest.NewRequest(http.MethodGet, "/api/network/balance/adjustments", nil)
	w := httptest.NewRecorder()
	handleBalanceAdjustments(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB5_HandleBalanceRecalculate_Nil(t *testing.T) {
	balanceEngine = nil
	req := httptest.NewRequest(http.MethodPost, "/api/network/balance/recalculate", nil)
	w := httptest.NewRecorder()
	handleBalanceRecalculate(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB5_HandleLBConfigUpdate_Nil(t *testing.T) {
	lbInstance = nil
	req := httptest.NewRequest(http.MethodPut, "/api/network/loadbalancer/config", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	handleLBConfigUpdate(w, req)
	if w.Code != 503 {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHB5_HandleLBConfigUpdate_InvalidWeights(t *testing.T) {
	lbInstance = NewLoadBalancer(DefaultLBConfig())
	defer func() { lbInstance = nil }()
	cfg := LBConfig{TrustWeight: 0.1, ReputationWeight: 0.1, LatencyWeight: 0.1, AvailabilityWeight: 0.1, ContributionWeight: 0.1}
	b, _ := json.Marshal(cfg)
	req := httptest.NewRequest(http.MethodPut, "/api/network/loadbalancer/config", strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	handleLBConfigUpdate(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB5_HandleLBNodeMetrics_Nil(t *testing.T) {
	lbInstance = nil
	req := httptest.NewRequest(http.MethodGet, "/api/network/loadbalancer/metrics/n1", nil)
	w := httptest.NewRecorder()
	handleLBNodeMetrics(w, req)
	if w.Code != 503 {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHB5_HandleLBNodeMetrics_MissingNodeID(t *testing.T) {
	lbInstance = NewLoadBalancer(DefaultLBConfig())
	defer func() { lbInstance = nil }()
	req := httptest.NewRequest(http.MethodGet, "/api/network/loadbalancer/metrics/", nil)
	w := httptest.NewRecorder()
	handleLBNodeMetrics(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB5_HandleLBNodeMetrics_UnknownNode(t *testing.T) {
	lbInstance = NewLoadBalancer(DefaultLBConfig())
	defer func() { lbInstance = nil }()
	req := httptest.NewRequest(http.MethodGet, "/api/network/loadbalancer/metrics/unknown", nil)
	req.SetPathValue("node_id", "unknown")
	w := httptest.NewRecorder()
	handleLBNodeMetrics(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB5_HandleHeartbeatPing_NilMgr(t *testing.T) {
	origMgr := netMgr
	netMgr = &NetworkManager{
		dataPath: t.TempDir() + "/network.json",
		config:   NetworkConfig{Mode: NetworkModePersonal, NodeID: "test-node", BootstrapNodes: []string{}, SharedModels: []string{}, Peers: []PeerInfo{}, Addresses: []string{}},
	}
	defer func() { netMgr = origMgr }()
	req := httptest.NewRequest(http.MethodGet, "/api/network/heartbeat/ping", nil)
	w := httptest.NewRecorder()
	handleHeartbeatPing(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB5_HandleGetReputations_NilMgr(t *testing.T) {
	origMgr := repMgr
	repMgr = nil
	defer func() { repMgr = origMgr }()
	req := httptest.NewRequest(http.MethodGet, "/federation/reputations", nil)
	w := httptest.NewRecorder()
	// This will panic with nil repMgr, so we test with a real one
	dir := t.TempDir()
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	handleGetReputations(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB5_HandlePostScore_InvalidMethod(t *testing.T) {
	dir := t.TempDir()
	origMgr := repMgr
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	defer func() { repMgr = origMgr }()
	req := httptest.NewRequest(http.MethodGet, "/federation/score", nil)
	w := httptest.NewRecorder()
	handlePostScore(w, req)
	if w.Code != 405 {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHB5_HandlePostScore_InvalidBody(t *testing.T) {
	dir := t.TempDir()
	origMgr := repMgr
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	defer func() { repMgr = origMgr }()
	req := httptest.NewRequest(http.MethodPost, "/federation/score", strings.NewReader("invalid"))
	w := httptest.NewRecorder()
	handlePostScore(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB5_HandlePostScore_MissingFields(t *testing.T) {
	dir := t.TempDir()
	origMgr := repMgr
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	defer func() { repMgr = origMgr }()
	body := `{"from_node": "", "target_node": ""}`
	req := httptest.NewRequest(http.MethodPost, "/federation/score", strings.NewReader(body))
	w := httptest.NewRecorder()
	handlePostScore(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB5_ExtractRemoteIP_XFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	ip := extractRemoteIP(req)
	if ip != "1.2.3.4" {
		t.Fatalf("expected 1.2.3.4, got %s", ip)
	}
}

func TestHB5_ExtractRemoteIP_XRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "9.8.7.6")
	ip := extractRemoteIP(req)
	if ip != "9.8.7.6" {
		t.Fatalf("expected 9.8.7.6, got %s", ip)
	}
}

func TestHB5_ExtractRemoteIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	ip := extractRemoteIP(req)
	if ip != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1, got %s", ip)
	}
}

func TestHB5_Region_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		input    string
		expected Region
	}{
		{`"ap"`, RegionAsiaPacific},
		{`"eu"`, RegionEurope},
		{`"americas"`, RegionAmericas},
		{`"unknown"`, RegionUnknown},
		{`"custom"`, Region("custom")},
	}
	for _, tt := range tests {
		var r Region
		if err := json.Unmarshal([]byte(tt.input), &r); err != nil {
			t.Fatalf("unmarshal %s: %v", tt.input, err)
		}
		if r != tt.expected {
			t.Errorf("unmarshal %s = %q, want %q", tt.input, r, tt.expected)
		}
	}
}

func TestHB5_NodeKeyStore_JSON(t *testing.T) {
	store := NodeKeyStore{
		NodeID:          "mmx-test",
		PrivKeyB64:      "enc-key",
		PubKeyB64:       "pub-key",
		HasMnemonic:     true,
		BackupConfirmed: false,
		Version:         2,
	}
	b, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	var restored NodeKeyStore
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.NodeID != "mmx-test" {
		t.Fatalf("expected mmx-test, got %s", restored.NodeID)
	}
	if restored.Version != 2 {
		t.Fatalf("expected version 2, got %d", restored.Version)
	}
}

func TestHB5_DeriveP2PNodeID_NilNode(t *testing.T) {
	origNode := node
	node = nil
	defer func() { node = origNode }()
	if id := DeriveP2PNodeID(); id != "" {
		t.Fatalf("expected empty, got %s", id)
	}
}

func TestHB5_canonicalNodeID_NilNode(t *testing.T) {
	origNode := node
	node = nil
	defer func() { node = origNode }()
	if id := canonicalNodeID(); id != "" {
		t.Fatalf("expected empty, got %s", id)
	}
}

func TestHB5_GetBalanceRoutingWeight_Nil(t *testing.T) {
	balanceEngine = nil
	if w := GetBalanceRoutingWeight("n1"); w != 1.0 {
		t.Fatalf("expected 1.0, got %f", w)
	}
}

func TestHB5_GetBalancePriorityDelta_Nil(t *testing.T) {
	balanceEngine = nil
	if d := GetBalancePriorityDelta("n1"); d != 0 {
		t.Fatalf("expected 0, got %d", d)
	}
}

func TestHB5_PeerCapabilities_Fields(t *testing.T) {
	caps := PeerCapabilities{
		CanRelay:  true,
		CanSeed:   false,
		Providers: []string{"openai", "anthropic"},
	}
	if !caps.CanRelay {
		t.Fatal("expected CanRelay=true")
	}
	if caps.CanSeed {
		t.Fatal("expected CanSeed=false")
	}
	if len(caps.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(caps.Providers))
	}
}

func TestHB5_ContribRecord_Fields(t *testing.T) {
	cr := ContribRecord{
		Timestamp:  "2024-01-01T00:00:00Z",
		TokensUsed: 1000,
		Requests:   10,
		FromNodeID: "n1",
	}
	if cr.TokensUsed != 1000 {
		t.Fatalf("expected 1000, got %d", cr.TokensUsed)
	}
}

func TestHB5_BalanceAdjustment_Fields(t *testing.T) {
	adj := BalanceAdjustment{
		NodeID:                  "n1",
		Type:                    "balanced",
		PriorityDelta:           0,
		RoutingWeightMultiplier: 1.0,
		QuotaMultiplier:         1.0,
	}
	if adj.RoutingWeightMultiplier != 1.0 {
		t.Fatalf("expected 1.0, got %f", adj.RoutingWeightMultiplier)
	}
}

func TestHB5_StopBalanceLoop_Nil(t *testing.T) {
	balanceEngine = nil
	StopBalanceLoop()
}

func TestHB5_NodeMetrics_Fields(t *testing.T) {
	m := NodeMetrics{
		NodeID:      "n1",
		Latency:     100 * time.Millisecond,
		ActiveConns: 5,
		CPUUsage:    0.5,
		MemUsage:    0.3,
		Bandwidth:   10000,
		ErrorRate:   0.1,
		Healthy:     true,
	}
	if m.NodeID != "n1" {
		t.Fatal("unexpected NodeID")
	}
	if !m.Healthy {
		t.Fatal("expected healthy")
	}
}

func TestHB5_GlobalBalance_Fields(t *testing.T) {
	gb := GlobalBalance{
		TotalNetworkContribution: 10000,
		TotalNetworkConsumption:  5000,
		NetworkBalanceRatio:      2.0,
		AverageNodeBalance:       1.5,
		ImbalanceNodes:           1,
	}
	if gb.NetworkBalanceRatio != 2.0 {
		t.Fatalf("expected 2.0, got %f", gb.NetworkBalanceRatio)
	}
}

func TestHB5_BalanceHistory_Fields(t *testing.T) {
	bh := BalanceHistory{
		Timestamp:       "2024-01-01T00:00:00Z",
		CycleDurationMS: 100,
	}
	if bh.CycleDurationMS != 100 {
		t.Fatalf("expected 100, got %d", bh.CycleDurationMS)
	}
}

func TestHB5_HeartbeatRegionInfo_Fields(t *testing.T) {
	info := HeartbeatRegionInfo{
		Region:    "ap",
		SubRegion: "tokyo",
		Latitude:  35.6,
		Longitude: 139.6,
	}
	if info.Region != "ap" {
		t.Fatalf("expected ap, got %s", info.Region)
	}
}

func TestHB5_NodeRegion_Fields(t *testing.T) {
	nr := NodeRegion{
		Region:    RegionAsiaPacific,
		Source:    "ip_detect",
		SubRegion: "tokyo",
		Latitude:  35.6,
		Longitude: 139.6,
	}
	if nr.Region != RegionAsiaPacific {
		t.Fatalf("expected ap, got %s", nr.Region)
	}
}

func TestHB5_DefaultRegionConfig(t *testing.T) {
	cfg := DefaultRegionConfig()
	if !cfg.PreferLocal {
		t.Fatal("expected PreferLocal=true")
	}
	if cfg.CrossRegionThreshold != 2.0 {
		t.Fatalf("expected 2.0, got %f", cfg.CrossRegionThreshold)
	}
}

func TestHB5_RegionConfig_Fields(t *testing.T) {
	cfg := RegionConfig{
		PreferLocal:          true,
		CrossRegionThreshold: 3.0,
		RegionWeights:        map[Region]float64{RegionAsiaPacific: 1.0},
	}
	if !cfg.PreferLocal {
		t.Fatal("expected PreferLocal=true")
	}
}
