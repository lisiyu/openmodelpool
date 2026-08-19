package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// ============================================================
// BalanceEngine tests
// ============================================================

func TestHB9_BalanceEngine_RecordContribution(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0),
	}
	be.RecordContributionBalance("node1", 1000)
	if nb, ok := be.nodeBalance["node1"]; !ok || nb.TotalContributed != 1000 {
		t.Fatalf("expected contributed=1000, got %v", be.nodeBalance["node1"])
	}
}

func TestHB9_BalanceEngine_RecordContribution_Nil(t *testing.T) {
	var be *BalanceEngine
	be.RecordContributionBalance("node1", 1000)
}

func TestHB9_BalanceEngine_RecordContribution_Zero(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.RecordContributionBalance("node1", 0)
	if _, ok := be.nodeBalance["node1"]; ok {
		t.Fatal("zero contribution should not create entry")
	}
}

func TestHB9_BalanceEngine_RecordConsumption(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.RecordConsumptionBalance("node1", 500)
	if nb, ok := be.nodeBalance["node1"]; !ok || nb.TotalConsumed != 500 {
		t.Fatalf("expected consumed=500, got %v", be.nodeBalance["node1"])
	}
}

func TestHB9_BalanceEngine_RecordConsumption_Nil(t *testing.T) {
	var be *BalanceEngine
	be.RecordConsumptionBalance("node1", 500)
}

func TestHB9_BalanceEngine_RecordConsumption_Negative(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.RecordConsumptionBalance("node1", -100)
	if _, ok := be.nodeBalance["node1"]; ok {
		t.Fatal("negative consumption should not create entry")
	}
}

func TestHB9_BalanceEngine_CalculateAdjustment_Unknown(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	adj := be.CalculateAdjustment("unknown")
	if adj.Type != "balanced" {
		t.Fatalf("expected balanced, got %s", adj.Type)
	}
	if adj.RoutingWeightMultiplier != 1.0 {
		t.Fatalf("expected 1.0, got %f", adj.RoutingWeightMultiplier)
	}
}

func TestHB9_BalanceEngine_CalculateAdjustment_OverConsumer(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {NodeID: "n1", TotalContributed: 10, TotalConsumed: 100, Balance: 0.1},
		},
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "reduce_priority" {
		t.Fatalf("expected reduce_priority, got %s", adj.Type)
	}
	if adj.PriorityDelta != -1 {
		t.Fatalf("expected -1, got %d", adj.PriorityDelta)
	}
}

func TestHB9_BalanceEngine_CalculateAdjustment_OverContributor(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {NodeID: "n1", TotalContributed: 1000, TotalConsumed: 10, Balance: 100.0},
		},
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "boost_priority" {
		t.Fatalf("expected boost_priority, got %s", adj.Type)
	}
	if adj.PriorityDelta != 1 {
		t.Fatalf("expected 1, got %d", adj.PriorityDelta)
	}
}

func TestHB9_BalanceEngine_CalculateAdjustment_Balanced(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {NodeID: "n1", TotalContributed: 100, TotalConsumed: 100, Balance: 1.0},
		},
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "balanced" {
		t.Fatalf("expected balanced, got %s", adj.Type)
	}
}

func TestHB9_BalanceEngine_GetBalanceStatus(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	status := be.GetBalanceStatus()
	if status == nil {
		t.Fatal("expected non-nil status")
	}
}

func TestHB9_BalanceEngine_GetAllNodeBalances_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	result := be.GetAllNodeBalances()
	if len(result) != 0 {
		t.Fatalf("expected 0, got %d", len(result))
	}
}

func TestHB9_BalanceEngine_GetAllNodeBalances_Sorted(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {NodeID: "n1", Balance: 2.0},
			"n2": {NodeID: "n2", Balance: 0.5},
		},
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	result := be.GetAllNodeBalances()
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0].Balance > result[1].Balance {
		t.Fatalf("expected sorted ascending, got %f > %f", result[0].Balance, result[1].Balance)
	}
}

func TestHB9_BalanceEngine_GetAllAdjustments_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	result := be.GetAllAdjustments()
	if len(result) != 0 {
		t.Fatalf("expected 0, got %d", len(result))
	}
}

func TestHB9_BalanceEngine_GetAdjustmentForNode_Missing(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	adj := be.GetAdjustmentForNode("missing")
	if adj.Type != "balanced" {
		t.Fatalf("expected balanced, got %s", adj.Type)
	}
}

func TestHB9_BalanceEngine_GetBalanceHistory_Empty(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0),
	}
	result := be.GetBalanceHistory(10)
	if len(result) != 0 {
		t.Fatalf("expected 0, got %d", len(result))
	}
}

func TestHB9_BalanceEngine_GetRoutingWeightMultiplier_Nil(t *testing.T) {
	var be *BalanceEngine
	if v := be.GetRoutingWeightMultiplier("n1"); v != 1.0 {
		t.Fatalf("expected 1.0, got %f", v)
	}
}

func TestHB9_BalanceEngine_GetPriorityDelta_Nil(t *testing.T) {
	var be *BalanceEngine
	if v := be.GetPriorityDelta("n1"); v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}
}

func TestHB9_BalanceEngine_UpdateConfig_InvalidValues(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
	}
	be.UpdateConfig(BalanceConfig{TargetRatio: -1, UnderConsumerThreshold: -1, OverContributorThreshold: -1, AdjustmentStrength: 5})
	cfg := be.GetConfig()
	if cfg.TargetRatio != 1.0 {
		t.Fatalf("expected 1.0, got %f", cfg.TargetRatio)
	}
	if cfg.UnderConsumerThreshold != 0.5 {
		t.Fatalf("expected 0.5, got %f", cfg.UnderConsumerThreshold)
	}
	if cfg.OverContributorThreshold != 3.0 {
		t.Fatalf("expected 3.0, got %f", cfg.OverContributorThreshold)
	}
	if cfg.AdjustmentStrength != 0.3 {
		t.Fatalf("expected 0.3, got %f", cfg.AdjustmentStrength)
	}
}

func TestHB9_BalanceEngine_RunBalanceCycle(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {NodeID: "n1", TotalContributed: 100, TotalConsumed: 100, Balance: 1.0},
		},
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0),
	}
	be.RunBalanceCycle(nil)
	if len(be.history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(be.history))
	}
}

func TestHB9_BalanceEngine_GetRoutingWeightMultiplier_WithAdj(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: map[string]*BalanceAdjustment{
			"n1": {NodeID: "n1", RoutingWeightMultiplier: 1.5},
		},
	}
	if v := be.GetRoutingWeightMultiplier("n1"); v != 1.5 {
		t.Fatalf("expected 1.5, got %f", v)
	}
}

func TestHB9_BalanceEngine_GetPriorityDelta_WithAdj(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: map[string]*BalanceAdjustment{
			"n1": {NodeID: "n1", PriorityDelta: -1},
		},
	}
	if v := be.GetPriorityDelta("n1"); v != -1 {
		t.Fatalf("expected -1, got %d", v)
	}
}

func TestHB9_GetBalanceRoutingWeight_NilEngine(t *testing.T) {
	orig := balanceEngine
	balanceEngine = nil
	defer func() { balanceEngine = orig }()
	if v := GetBalanceRoutingWeight("n1"); v != 1.0 {
		t.Fatalf("expected 1.0, got %f", v)
	}
}

func TestHB9_GetBalancePriorityDelta_NilEngine(t *testing.T) {
	orig := balanceEngine
	balanceEngine = nil
	defer func() { balanceEngine = orig }()
	if v := GetBalancePriorityDelta("n1"); v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}
}

// ============================================================
// AlgorithmGovernor tests
// ============================================================

func TestHB9_Governor_CreateProposal_EmptyTitle(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	_, err := g.CreateProposal("", "desc", "admin", "", nil)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestHB9_Governor_CreateProposal_Success(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	p, err := g.CreateProposal("Test Proposal", "description", "admin", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Title != "Test Proposal" {
		t.Fatalf("expected 'Test Proposal', got %s", p.Title)
	}
	if p.Status != ProposalStatusOpen {
		t.Fatalf("expected open, got %s", p.Status)
	}
}

func TestHB9_Governor_CastVote_InvalidChoice(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	_, err := g.CastVote("nonexistent", "admin", "", "maybe", "")
	if err == nil {
		t.Fatal("expected error for invalid choice")
	}
}

func TestHB9_Governor_CastVote_ProposalNotFound(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	_, err := g.CastVote("nonexistent", "admin", "", "yes", "")
	if err == nil {
		t.Fatal("expected error for missing proposal")
	}
}

func TestHB9_Governor_CastVote_Success(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	p, _ := g.CreateProposal("Vote Test", "desc", "admin", "", nil)
	updated, err := g.CastVote(p.ID, "voter1", "", "yes", "looks good")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updated.Votes) != 1 {
		t.Fatalf("expected 1 vote, got %d", len(updated.Votes))
	}
}

func TestHB9_Governor_CastVote_Dedup(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	p, _ := g.CreateProposal("Dedup Test", "desc", "admin", "", nil)
	g.CastVote(p.ID, "voter1", "", "yes", "")
	updated, _ := g.CastVote(p.ID, "voter1", "", "no", "")
	if len(updated.Votes) != 1 {
		t.Fatalf("expected 1 vote after dedup, got %d", len(updated.Votes))
	}
	if updated.Votes[0].Choice != "no" {
		t.Fatalf("expected 'no', got %s", updated.Votes[0].Choice)
	}
}

func TestHB9_Governor_ResolveProposal_InvalidStatus(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	_, err := g.ResolveProposal("x", "admin", "", ProposalStatusOpen)
	if err == nil {
		t.Fatal("expected error for non-terminal status")
	}
}

func TestHB9_Governor_ResolveProposal_NotFound(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	_, err := g.ResolveProposal("x", "admin", "", ProposalStatusPassed)
	if err == nil {
		t.Fatal("expected error for missing proposal")
	}
}

func TestHB9_Governor_ResolveProposal_Passed(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	p, _ := g.CreateProposal("Resolve Test", "desc", "admin", "", nil)
	resolved, err := g.ResolveProposal(p.ID, "admin", "approved", ProposalStatusPassed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Status != ProposalStatusPassed {
		t.Fatalf("expected passed, got %s", resolved.Status)
	}
}

func TestHB9_Governor_ResolveProposal_Idempotent(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	p, _ := g.CreateProposal("Idempotent Test", "desc", "admin", "", nil)
	g.ResolveProposal(p.ID, "admin", "", ProposalStatusPassed)
	resolved, err := g.ResolveProposal(p.ID, "admin", "", ProposalStatusRejected)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Status != ProposalStatusPassed {
		t.Fatalf("expected passed (idempotent), got %s", resolved.Status)
	}
}

func TestHB9_Governor_GetProposal_NotFound(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	_, ok := g.GetProposal("nonexistent")
	if ok {
		t.Fatal("expected false")
	}
}

func TestHB9_Governor_ListProposals_Filter(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	p1, _ := g.CreateProposal("Open One", "desc", "admin", "", nil)
	g.ResolveProposal(p1.ID, "admin", "", ProposalStatusPassed)
	g.CreateProposal("Open Two", "desc", "admin", "", nil)
	open := g.ListProposals("open")
	if len(open) != 1 {
		t.Fatalf("expected 1 open, got %d", len(open))
	}
}

func TestHB9_Governor_GetHistory(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	g.CreateProposal("Hist Test", "desc", "admin", "", nil)
	hist := g.GetHistory()
	if len(hist) != 1 {
		t.Fatalf("expected 1 history, got %d", len(hist))
	}
	if hist[0].Type != "proposal_created" {
		t.Fatalf("expected proposal_created, got %s", hist[0].Type)
	}
}

func TestHB9_Governor_VoteOnClosedProposal(t *testing.T) {
	g := &AlgorithmGovernor{proposals: make(map[string]*AlgorithmProposal), dataDir: t.TempDir()}
	p, _ := g.CreateProposal("Closed Test", "desc", "admin", "", nil)
	g.ResolveProposal(p.ID, "admin", "", ProposalStatusClosed)
	_, err := g.CastVote(p.ID, "voter1", "", "yes", "")
	if err == nil {
		t.Fatal("expected error for voting on closed proposal")
	}
}

func TestHB9_ProposalTally(t *testing.T) {
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

func TestHB9_ProposalStatus_IsTerminal(t *testing.T) {
	if ProposalStatusOpen.isTerminal() {
		t.Fatal("open should not be terminal")
	}
	if !ProposalStatusPassed.isTerminal() {
		t.Fatal("passed should be terminal")
	}
	if !ProposalStatusRejected.isTerminal() {
		t.Fatal("rejected should be terminal")
	}
	if !ProposalStatusClosed.isTerminal() {
		t.Fatal("closed should be terminal")
	}
}

func TestHB9_VoteChoice_IsValid(t *testing.T) {
	if !VoteYes.isValid() || !VoteNo.isValid() || !VoteAbstain.isValid() {
		t.Fatal("valid choices should be valid")
	}
	if VoteChoice("maybe").isValid() {
		t.Fatal("invalid choice should not be valid")
	}
}

func TestHB9_TrimSpace(t *testing.T) {
	if trimSpace("  hello  ") != "hello" {
		t.Fatalf("unexpected: %q", trimSpace("  hello  "))
	}
	if trimSpace("") != "" {
		t.Fatal("expected empty")
	}
	if trimSpace("noextra") != "noextra" {
		t.Fatal("expected same")
	}
}

// ============================================================
// WAFEngine tests
// ============================================================

func TestHB9_WAFEngine_AddBan(t *testing.T) {
	e := NewWAFEngine()
	e.AddBan("1.2.3.4", "abuse", 5*time.Minute)
	bans := e.Bans()
	if len(bans) != 1 {
		t.Fatalf("expected 1 ban, got %d", len(bans))
	}
}

func TestHB9_WAFEngine_RemoveBan(t *testing.T) {
	e := NewWAFEngine()
	e.AddBan("1.2.3.4", "abuse", 5*time.Minute)
	if !e.RemoveBan("1.2.3.4") {
		t.Fatal("expected true")
	}
	if e.RemoveBan("1.2.3.4") {
		t.Fatal("expected false for already-removed")
	}
}

func TestHB9_WAFEngine_Violations_Empty(t *testing.T) {
	e := NewWAFEngine()
	if len(e.Violations()) != 0 {
		t.Fatal("expected empty violations")
	}
}

func TestHB9_WAFEngine_Status(t *testing.T) {
	e := NewWAFEngine()
	status := e.Status()
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status["enabled"] != false {
		t.Fatal("expected disabled by default")
	}
}

func TestHB9_WAFEngine_Check_Disabled(t *testing.T) {
	e := NewWAFEngine()
	req := httptest.NewRequest("GET", "/test", nil)
	allowed, v := e.Check(req)
	if !allowed {
		t.Fatal("disabled engine should allow all")
	}
	if v != nil {
		t.Fatal("disabled engine should return nil violation")
	}
}

func TestHB9_WAFEngine_Bans_Expired(t *testing.T) {
	e := NewWAFEngine()
	e.AddBan("1.2.3.4", "test", -1*time.Second)
	bans := e.Bans()
	if len(bans) != 0 {
		t.Fatalf("expired ban should not be returned, got %d", len(bans))
	}
}

// ============================================================
// Region tests
// ============================================================

func TestHB9_RegionCanonical(t *testing.T) {
	tests := []struct{ in string; want Region }{
		{"ap", RegionAsiaPacific},
		{"asia", RegionAsiaPacific},
		{"eu", RegionEurope},
		{"europe", RegionEurope},
		{"us", RegionAmericas},
		{"americas", RegionAmericas},
		{"xyz", RegionUnknown},
	}
	for _, tt := range tests {
		if got := regionCanonical(tt.in); got != tt.want {
			t.Errorf("regionCanonical(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHB9_RegionManager_RegisterNodeSelfReport(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "tokyo", 35.6, 139.6)
	nr := rm.GetNodeRegion("n1")
	if nr == nil || nr.Region != RegionAsiaPacific {
		t.Fatal("expected ap region")
	}
	if nr.Source != "self_report" {
		t.Fatalf("expected self_report, got %s", nr.Source)
	}
}

func TestHB9_RegionManager_GetNodeRegion_Missing(t *testing.T) {
	rm := NewRegionManager()
	if rm.GetNodeRegion("missing") != nil {
		t.Fatal("expected nil for missing node")
	}
}

func TestHB9_RegionManager_GetAllRegions(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "eu", "", 0, 0)
	regions := rm.GetAllRegions()
	if len(regions) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(regions))
	}
}

func TestHB9_RegionManager_GetRegionSummary(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n3", "eu", "", 0, 0)
	summary := rm.GetRegionSummary()
	if summary[RegionAsiaPacific] != 2 {
		t.Fatalf("expected 2 ap, got %d", summary[RegionAsiaPacific])
	}
}

func TestHB9_RegionManager_SelectNodeForRegion(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "eu", "", 0, 0)
	result := rm.SelectNodeForRegion([]string{"n2", "n1"}, RegionAsiaPacific)
	if len(result) != 2 || result[0] != "n1" {
		t.Fatalf("expected n1 first, got %v", result)
	}
}

func TestHB9_RegionManager_GetOptimalRoute(t *testing.T) {
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.RegisterNodeSelfReport("n2", "eu", "", 0, 0)
	result := rm.GetOptimalRoute([]string{"n2", "n1"}, RegionAsiaPacific, nil)
	if len(result) != 2 || result[0] != "n1" {
		t.Fatalf("expected n1 first, got %v", result)
	}
}

func TestHB9_RegionManager_ProcessHeartbeatRegion_NilInfo(t *testing.T) {
	rm := NewRegionManager()
	rm.ProcessHeartbeatRegion("n1", nil, "")
	if rm.GetNodeRegion("n1") != nil {
		t.Fatal("expected nil for empty info and IP")
	}
}

func TestHB9_RegionManager_ProcessHeartbeatRegion_WithInfo(t *testing.T) {
	rm := NewRegionManager()
	info := &HeartbeatRegionInfo{Region: "eu", SubRegion: "london", Latitude: 51.5, Longitude: -0.1}
	rm.ProcessHeartbeatRegion("n1", info, "")
	nr := rm.GetNodeRegion("n1")
	if nr == nil || nr.Region != RegionEurope {
		t.Fatal("expected eu region from heartbeat")
	}
}

func TestHB9_RegionManager_UpdateConfig(t *testing.T) {
	rm := NewRegionManager()
	newCfg := RegionConfig{PreferLocal: false, CrossRegionThreshold: 5.0}
	rm.UpdateConfig(newCfg)
	got := rm.GetConfig()
	if got.PreferLocal != false || got.CrossRegionThreshold != 5.0 {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestHB9_HaversineDistance(t *testing.T) {
	d := haversineDistance(0, 0, 0, 0)
	if d != 0 {
		t.Fatalf("same point should be 0, got %f", d)
	}
	d2 := haversineDistance(0, 0, 0, 180)
	if d2 <= 0 {
		t.Fatalf("antipodal should be positive, got %f", d2)
	}
}

func TestHB9_RegionDistance(t *testing.T) {
	if regionDistance(RegionAsiaPacific, RegionAsiaPacific) != 0 {
		t.Fatal("same region should be 0")
	}
	if regionDistance(RegionAsiaPacific, RegionAmericas) != 12000 {
		t.Fatal("AP-Americas should be 12000")
	}
	if regionDistance(RegionAmericas, RegionAsiaPacific) != 12000 {
		t.Fatal("reverse lookup should work")
	}
}

func TestHB9_RegionCenter(t *testing.T) {
	lat, lon := regionCenter(RegionAsiaPacific)
	if lat != 35 || lon != 110 {
		t.Fatalf("unexpected AP center: %f, %f", lat, lon)
	}
	lat, lon = regionCenter(RegionUnknown)
	if lat != 0 || lon != 0 {
		t.Fatalf("expected 0,0 for unknown: %f, %f", lat, lon)
	}
}

func TestHB9_AllRegions(t *testing.T) {
	regions := AllRegions()
	if len(regions) != 4 {
		t.Fatalf("expected 4 regions, got %d", len(regions))
	}
}

func TestHB9_Region_MarshalUnmarshal(t *testing.T) {
	r := RegionAsiaPacific
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var r2 Region
	if err := json.Unmarshal(b, &r2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if r2 != r {
		t.Fatalf("expected %s, got %s", r, r2)
	}
}

func TestHB9_Region_UnmarshalJSON_Custom(t *testing.T) {
	var r Region
	if err := json.Unmarshal([]byte(`"custom-region"`), &r); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != "custom-region" {
		t.Fatalf("expected custom-region, got %s", r)
	}
}

// ============================================================
// MessageManager tests
// ============================================================

func TestHB9_MessageManager_GetInbox_Empty(t *testing.T) {
	m := &MessageManager{inbox: []FederationMessage{}, outbox: []FederationMessage{}, dataDir: t.TempDir()}
	result := m.GetInbox(10)
	if len(result) != 0 {
		t.Fatalf("expected 0, got %d", len(result))
	}
}

func TestHB9_MessageManager_GetOutbox_Empty(t *testing.T) {
	m := &MessageManager{inbox: []FederationMessage{}, outbox: []FederationMessage{}, dataDir: t.TempDir()}
	result := m.GetOutbox(10)
	if len(result) != 0 {
		t.Fatalf("expected 0, got %d", len(result))
	}
}

func TestHB9_MessageManager_GetInbox_WithMessages(t *testing.T) {
	m := &MessageManager{
		inbox: []FederationMessage{
			{ID: "1", Subject: "first"},
			{ID: "2", Subject: "second"},
		},
		outbox: []FederationMessage{},
		dataDir: t.TempDir(),
	}
	result := m.GetInbox(10)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0].ID != "2" {
		t.Fatalf("expected most recent first, got %s", result[0].ID)
	}
}

func TestHB9_MessageManager_MarkAsRead(t *testing.T) {
	m := &MessageManager{
		inbox: []FederationMessage{{ID: "1", Read: false}},
		outbox: []FederationMessage{},
		dataDir: t.TempDir(),
	}
	if !m.MarkAsRead("1") {
		t.Fatal("expected true")
	}
	if !m.inbox[0].Read {
		t.Fatal("expected read=true")
	}
}

func TestHB9_MessageManager_MarkAsRead_NotFound(t *testing.T) {
	m := &MessageManager{inbox: []FederationMessage{}, outbox: []FederationMessage{}, dataDir: t.TempDir()}
	if m.MarkAsRead("nonexistent") {
		t.Fatal("expected false")
	}
}

func TestHB9_MessageManager_GetUnreadCount(t *testing.T) {
	m := &MessageManager{
		inbox: []FederationMessage{
			{ID: "1", Read: false},
			{ID: "2", Read: true},
			{ID: "3", Read: false},
		},
		outbox: []FederationMessage{},
		dataDir: t.TempDir(),
	}
	if count := m.GetUnreadCount(); count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

func TestHB9_MessageManager_TrimInbox(t *testing.T) {
	msgs := make([]FederationMessage, maxInboxSize+10)
	for i := range msgs {
		msgs[i] = FederationMessage{ID: string(rune(i)), Timestamp: time.Now().Format(time.RFC3339)}
	}
	m := &MessageManager{inbox: msgs, outbox: []FederationMessage{}, dataDir: t.TempDir()}
	m.trimInbox()
	if len(m.inbox) > maxInboxSize {
		t.Fatalf("expected <= %d, got %d", maxInboxSize, len(m.inbox))
	}
}

func TestHB9_MessageManager_TrimOutbox(t *testing.T) {
	msgs := make([]FederationMessage, maxOutboxSize+10)
	for i := range msgs {
		msgs[i] = FederationMessage{ID: string(rune(i)), Timestamp: time.Now().Format(time.RFC3339)}
	}
	m := &MessageManager{inbox: []FederationMessage{}, outbox: msgs, dataDir: t.TempDir()}
	m.trimOutbox()
	if len(m.outbox) > maxOutboxSize {
		t.Fatalf("expected <= %d, got %d", maxOutboxSize, len(m.outbox))
	}
}

// ============================================================
// FederationManager tests
// ============================================================

func TestHB9_FederationManager_IsEnabled(t *testing.T) {
	f := &FederationManager{enabled: true}
	if !f.IsEnabled() {
		t.Fatal("expected true")
	}
}

func TestHB9_FederationManager_IsRelayEnabled(t *testing.T) {
	f := &FederationManager{relayEnabled: true}
	if !f.IsRelayEnabled() {
		t.Fatal("expected true")
	}
}

func TestHB9_FederationManager_GetTrustPool(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{Version: 5, Nodes: []NodeInfo{{NodeID: "n1"}}},
	}
	pool := f.GetTrustPool()
	if pool.Version != 5 {
		t.Fatalf("expected 5, got %d", pool.Version)
	}
}

func TestHB9_FederationManager_GetActiveNodes(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{Nodes: []NodeInfo{
			{NodeID: "n1", Status: "active"},
			{NodeID: "n2", Status: "offline"},
		}},
		localPeers: map[string]*NodeInfo{
			"n3": {NodeID: "n3", Status: "active"},
		},
	}
	active := f.GetActiveNodes()
	if len(active) != 2 {
		t.Fatalf("expected 2 active, got %d", len(active))
	}
}

func TestHB9_FederationManager_GetNode(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{Nodes: []NodeInfo{{NodeID: "n1"}}},
		localPeers: map[string]*NodeInfo{"n2": {NodeID: "n2"}},
	}
	if _, ok := f.GetNode("n1"); !ok {
		t.Fatal("expected to find n1 in trust pool")
	}
	if _, ok := f.GetNode("n2"); !ok {
		t.Fatal("expected to find n2 in local peers")
	}
	if _, ok := f.GetNode("n3"); ok {
		t.Fatal("expected not to find n3")
	}
}

func TestHB9_FederationManager_RemoveNode(t *testing.T) {
	f := &FederationManager{
		trustPool:  TrustPool{Nodes: []NodeInfo{{NodeID: "n1"}, {NodeID: "n2"}}},
		localPeers: map[string]*NodeInfo{"n3": {NodeID: "n3"}},
	}
	f.RemoveNode("n1")
	if _, ok := f.GetNode("n1"); ok {
		t.Fatal("n1 should be removed")
	}
}

func TestHB9_FederationManager_MergePeerHints(t *testing.T) {
	f := &FederationManager{discoveryHints: make(map[string][]string)}
	f.MergePeerHints([]PeerHint{
		{NodeID: "n1", Addresses: []string{"http://n1"}},
		{NodeID: "", Addresses: []string{"http://n2"}},
	})
	if len(f.discoveryHints) != 1 {
		t.Fatalf("expected 1 hint, got %d", len(f.discoveryHints))
	}
}

func TestHB9_FederationManager_MergePeerHints_Dedup(t *testing.T) {
	f := &FederationManager{discoveryHints: map[string][]string{"n1": {"http://old"}}}
	f.MergePeerHints([]PeerHint{{NodeID: "n1", Addresses: []string{"http://new"}}})
	if f.discoveryHints["n1"][0] != "http://old" {
		t.Fatal("first-known should win")
	}
}

func TestHB9_FederationManager_HintAddresses(t *testing.T) {
	f := &FederationManager{discoveryHints: map[string][]string{"n1": {"http://n1"}}}
	if addrs := f.HintAddresses("n1"); len(addrs) != 1 {
		t.Fatalf("expected 1, got %d", len(addrs))
	}
	if addrs := f.HintAddresses("n2"); addrs != nil {
		t.Fatalf("expected nil for missing, got %v", addrs)
	}
}

func TestHB9_FederationManager_FindProvidersForModel(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{Nodes: []NodeInfo{
			{NodeID: "n1", Status: "active", SharedModels: []string{"gpt-4", "claude"}},
			{NodeID: "n2", Status: "active", SharedModels: []string{"gpt-4"}},
			{NodeID: "n3", Status: "offline", SharedModels: []string{"gpt-4"}},
		}},
		localPeers: map[string]*NodeInfo{},
	}
	result := f.FindProvidersForModel("gpt-4")
	if len(result) != 2 {
		t.Fatalf("expected 2 active providers, got %d", len(result))
	}
}

func TestHB9_FederationManager_AllKnownEndpoints(t *testing.T) {
	f := &FederationManager{
		trustPool:  TrustPool{Nodes: []NodeInfo{{NodeID: "n1", Endpoint: "http://n1"}, {NodeID: "n2"}}},
		localPeers: map[string]*NodeInfo{"n3": {NodeID: "n3", Endpoint: "http://n3"}},
	}
	endpoints := f.allKnownEndpoints()
	if len(endpoints) != 2 {
		t.Fatalf("expected 2, got %d", len(endpoints))
	}
}

func TestHB9_FederationManager_HasActivePeers_True(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{Nodes: []NodeInfo{{NodeID: "n1", Status: "active", Endpoint: "http://n1"}}},
	}
	if !f.hasActivePeers() {
		t.Fatal("expected true")
	}
}

func TestHB9_FederationManager_HasActivePeers_False(t *testing.T) {
	f := &FederationManager{
		trustPool: TrustPool{Nodes: []NodeInfo{{NodeID: "n1", Status: "offline"}}},
	}
	if f.hasActivePeers() {
		t.Fatal("expected false")
	}
}

func TestHB9_FederationManager_SetEnabled(t *testing.T) {
	f := &FederationManager{
		localPeers:     make(map[string]*NodeInfo),
		discoveryHints: make(map[string][]string),
		stopCh:         make(chan struct{}),
	}
	f.mu.Lock()
	f.enabled = true
	f.mu.Unlock()
	if !f.IsEnabled() {
		t.Fatal("expected enabled")
	}
}

func TestHB9_FederationManager_AddKnownNode_Disabled(t *testing.T) {
	f := &FederationManager{enabled: false}
	f.AddKnownNode(NodeInfo{NodeID: "n1"})
	if len(f.trustPool.Nodes) != 0 {
		t.Fatal("should not add when disabled")
	}
}

// ============================================================
// Config tests
// ============================================================

func TestHB9_Config_MaskToken(t *testing.T) {
	if maskToken("short") != "***" {
		t.Fatal("short token should be masked")
	}
	if maskToken("123456789012") != "123456...9012" {
		t.Fatalf("unexpected: %s", maskToken("123456789012"))
	}
}

func TestHB9_Config_ToUpper(t *testing.T) {
	if toUpper("hello_world") != "HELLO_WORLD" {
		t.Fatalf("unexpected: %s", toUpper("hello_world"))
	}
	if toUpper("") != "" {
		t.Fatal("expected empty")
	}
	if toUpper("ALREADY") != "ALREADY" {
		t.Fatal("expected same")
	}
}

func TestHB9_Config_SetAndGet(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("test_key", "test_value")
	if v := cfg.Get("test_key", "default"); v != "test_value" {
		t.Fatalf("expected test_value, got %s", v)
	}
}

func TestHB9_Config_Get_Default(t *testing.T) {
	setupTestEnv(t)
	if v := cfg.Get("nonexistent_key_12345", "fallback"); v != "fallback" {
		t.Fatalf("expected fallback, got %s", v)
	}
}

func TestHB9_Config_Masked(t *testing.T) {
	setupTestEnv(t)
	cfg.Set("proxy_api_key", "sk-12345678901234567890")
	masked := cfg.Masked()
	if _, ok := masked["proxy_api_key"]; ok {
		t.Fatal("raw key should be removed")
	}
	if _, ok := masked["proxy_api_key_masked"]; !ok {
		t.Fatal("masked key should be present")
	}
}

func TestHB9_Config_SetMany(t *testing.T) {
	setupTestEnv(t)
	cfg.SetMany(map[string]any{"k1": "v1", "k2": "v2"})
	if v := cfg.Get("k1", ""); v != "v1" {
		t.Fatalf("expected v1, got %s", v)
	}
}

// ============================================================
// Invite tests
// ============================================================

func TestHB9_EncodeDecodeInvite(t *testing.T) {
	invite := &FederationInvite{
		NetworkID: "net1",
		Inviter:   "inv1",
		Type:      FederationInvitePublic,
		Signature: "sig1",
	}
	encoded, err := EncodeInvite(invite)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	decoded, err := DecodeInvite(encoded)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if decoded.NetworkID != "net1" || decoded.Inviter != "inv1" {
		t.Fatalf("unexpected decoded: %+v", decoded)
	}
}

func TestHB9_DecodeInvite_Invalid(t *testing.T) {
	_, err := DecodeInvite("!!invalid-base64!!")
	if err == nil {
		t.Fatal("expected error for invalid encoding")
	}
}

func TestHB9_VerifyPayloadSignature_WrongSize(t *testing.T) {
	payload := FederationInvitePayload{NetworkID: "net1", Inviter: "inv1"}
	if verifyPayloadSignature(payload, []byte("sig"), []byte("short")) {
		t.Fatal("should fail with wrong-size pubkey")
	}
}

// ============================================================
// NodeRegistry tests
// ============================================================

func TestHB9_RegistryFileName_Safe(t *testing.T) {
	name := registryFileName("my-node-123")
	if name != "my-node-123.json" {
		t.Fatalf("unexpected: %s", name)
	}
}

func TestHB9_RegistryFileName_Unsafe(t *testing.T) {
	name := registryFileName("../../etc/passwd")
	if name == "../../etc/passwd.json" {
		t.Fatal("unsafe ID should be hashed")
	}
}

func TestHB9_RegistryFileName_Empty(t *testing.T) {
	name := registryFileName("")
	if name == "" {
		t.Fatal("empty ID should produce a name")
	}
}

func TestHB9_NodeRegistry_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	r := NewNodeRegistry(dir)
	entry := &RouteEntry{
		NodeID:    "test-node",
		NodeName:  "Test",
		Addresses: []string{"http://localhost:8080"},
		Status:    "online",
		Models:    []string{"gpt-4"},
		UpdatedAt: time.Now(),
	}
	r.SaveNode(entry)
	loaded, err := r.LoadAll()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1, got %d", len(loaded))
	}
	if loaded[0].NodeID != "test-node" {
		t.Fatalf("expected test-node, got %s", loaded[0].NodeID)
	}
}

func TestHB9_NodeRegistry_RemoveNode(t *testing.T) {
	dir := t.TempDir()
	r := NewNodeRegistry(dir)
	r.SaveNode(&RouteEntry{NodeID: "to-remove", UpdatedAt: time.Now()})
	r.RemoveNode("to-remove")
	loaded, _ := r.LoadAll()
	if len(loaded) != 0 {
		t.Fatalf("expected 0 after remove, got %d", len(loaded))
	}
}

func TestHB9_NodeRegistry_NilSafe(t *testing.T) {
	var r *NodeRegistry
	r.SaveNode(nil)
	r.RemoveNode("")
	r.SavePeer(PeerInfo{})
	loaded, err := r.LoadAll()
	if err != nil || len(loaded) != 0 {
		t.Fatal("nil registry should be safe")
	}
}

func TestHB9_CloneStrings(t *testing.T) {
	s := []string{"a", "b"}
	c := cloneStrings(s)
	if len(c) != 2 {
		t.Fatalf("expected 2, got %d", len(c))
	}
	s[0] = "x"
	if c[0] != "a" {
		t.Fatal("clone should be independent")
	}
}

func TestHB9_CloneStrings_Empty(t *testing.T) {
	c := cloneStrings(nil)
	if len(c) != 0 {
		t.Fatalf("expected 0, got %d", len(c))
	}
}

func TestHB9_ParseRFC3339Unix(t *testing.T) {
	ts := parseRFC3339Unix("2024-01-01T00:00:00Z")
	if ts == 0 {
		t.Fatal("expected non-zero for valid timestamp")
	}
	if parseRFC3339Unix("") != 0 {
		t.Fatal("expected 0 for empty")
	}
	if parseRFC3339Unix("not-a-date") != 0 {
		t.Fatal("expected 0 for invalid")
	}
}

func TestHB9_OrDefault(t *testing.T) {
	if orDefault("", "def") != "def" {
		t.Fatal("expected def for empty")
	}
	if orDefault("val", "def") != "val" {
		t.Fatal("expected val for non-empty")
	}
}

func TestHB9_RouteEntryFromNodeInfo(t *testing.T) {
	info := NodeInfo{NodeID: "n1", Status: "active", Endpoint: "http://n1", SharedModels: []string{"m1"}, LastSeen: time.Now().Format(time.RFC3339)}
	entry := routeEntryFromNodeInfo(info)
	if entry.NodeID != "n1" {
		t.Fatalf("expected n1, got %s", entry.NodeID)
	}
	if len(entry.Addresses) != 1 || entry.Addresses[0] != "http://n1" {
		t.Fatalf("expected address fallback to endpoint, got %v", entry.Addresses)
	}
}

// ============================================================
// WAF parse helpers
// ============================================================

func TestHB9_ParseList(t *testing.T) {
	if parseList("") != nil {
		t.Fatal("expected nil for empty")
	}
	result := parseList("a, b, c")
	if len(result) != 3 {
		t.Fatalf("expected 3, got %d", len(result))
	}
}

func TestHB9_ParseListToSet(t *testing.T) {
	s := parseListToSet("a, b, a")
	if !s["a"] || !s["b"] || len(s) != 2 {
		t.Fatalf("expected 2 unique, got %d", len(s))
	}
}

// ============================================================
// Balance handler tests
// ============================================================

func TestHB9_HandleBalanceStatus_NilEngine(t *testing.T) {
	orig := balanceEngine
	balanceEngine = nil
	defer func() { balanceEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/balance/status", nil)
	handleBalanceStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB9_HandleBalanceNodes_NilEngine(t *testing.T) {
	orig := balanceEngine
	balanceEngine = nil
	defer func() { balanceEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/balance/nodes", nil)
	handleBalanceNodes(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB9_HandleBalanceAdjustments_NilEngine(t *testing.T) {
	orig := balanceEngine
	balanceEngine = nil
	defer func() { balanceEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/balance/adjustments", nil)
	handleBalanceAdjustments(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB9_HandleBalanceRecalculate_NilEngine(t *testing.T) {
	orig := balanceEngine
	balanceEngine = nil
	defer func() { balanceEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/balance/recalculate", nil)
	handleBalanceRecalculate(w, r)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB9_HandleBalanceRecalculate_Success(t *testing.T) {
	orig := balanceEngine
	balanceEngine = &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {NodeID: "n1", TotalContributed: 100, TotalConsumed: 100, Balance: 1.0},
		},
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0),
	}
	defer func() { balanceEngine = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/balance/recalculate", nil)
	handleBalanceRecalculate(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ============================================================
// Discovery handler test
// ============================================================

func TestHB9_HandleFederationPool_Disabled(t *testing.T) {
	orig := fed
	fed = &FederationManager{enabled: false}
	defer func() { fed = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/federation/pool", nil)
	handleFederationPool(w, r)
	if w.Code != 503 {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// ============================================================
// Free pool handler tests
// ============================================================

func TestHB9_HandleFreePoolStatus_Nil(t *testing.T) {
	orig := freePool
	freePool = nil
	defer func() { freePool = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/free-pool/status", nil)
	handleFreePoolStatus(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB9_HandleFreePoolSync_Nil(t *testing.T) {
	orig := freePool
	freePool = nil
	defer func() { freePool = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/free-pool/sync", nil)
	handleFreePoolSync(w, r)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB9_HandleFreePoolConfig_Nil(t *testing.T) {
	orig := freePool
	freePool = nil
	defer func() { freePool = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/free-pool/config", nil)
	handleFreePoolConfig(w, r)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB9_HandleFreePoolSetKey_Nil(t *testing.T) {
	orig := freePool
	freePool = nil
	defer func() { freePool = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/free-pool/key/test", nil)
	handleFreePoolSetKey(w, r)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestHB9_HandleFreePoolRemoveKey_Nil(t *testing.T) {
	orig := freePool
	freePool = nil
	defer func() { freePool = orig }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/free-pool/key/test", nil)
	handleFreePoolRemoveKey(w, r)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// ============================================================
// Relay rate limit tests
// ============================================================

func TestHB9_RateLimitCheck_Basic(t *testing.T) {
	orig := rateLimitMap
	rateLimitMap = make(map[string]*rateLimitEntry)
	defer func() { rateLimitMap = orig }()
	if !rateLimitCheck("n1") {
		t.Fatal("first request should be allowed")
	}
}

func TestHB9_RateLimitCheck_WindowReset(t *testing.T) {
	orig := rateLimitMap
	origMax := rateLimitMax
	rateLimitMap = make(map[string]*rateLimitEntry)
	rateLimitMax = 2
	defer func() { rateLimitMap = orig; rateLimitMax = origMax }()
	rateLimitCheck("n1")
	rateLimitCheck("n1")
	if rateLimitCheck("n1") {
		t.Fatal("third request should be blocked")
	}
}

// ============================================================
// Auth handler tests
// ============================================================

func TestHB9_HandleLogin_EmptyBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/admin/login", nil)
	handleLogin(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB9_HandleResetPassword_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/admin/reset-password", nil)
	handleResetPassword(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB9_HandleVerifyResetToken_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/admin/verify-reset-token", nil)
	handleVerifyResetToken(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ============================================================
// Admin handler tests
// ============================================================

func TestHB9_HandleShareInfo(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/admin/share-info", nil)
	handleShareInfo(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB9_HandleRestart(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/admin/restart", nil)
	handleRestart(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB9_HandleRefreshToken_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/admin/refresh-token", nil)
	handleRefreshToken(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB9_HandleExportConfig(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/admin/export", nil)
	handleExportConfig(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB9_HandleImportConfig_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/admin/import", nil)
	handleImportConfig(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ============================================================
// Network handler tests
// ============================================================

func TestHB9_HandleNetworkAddPeer_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	origNetMgr := netMgr
	netMgr = &NetworkManager{config: NetworkConfig{Mode: NetworkModeShared, NetworkEnabled: true, Peers: []PeerInfo{}}}
	defer func() { netMgr = origNetMgr }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/peers/add", nil)
	handleNetworkAddPeer(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB9_HandleNetworkRemovePeer_MissingID(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/peers/remove", nil)
	handleNetworkRemovePeer(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB9_HandleNetworkResolve_InvalidBody(t *testing.T) {
	setupTestEnv(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/network/resolve", nil)
	handleNetworkResolve(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHB9_HandleNetworkRoutes_NilRouteTable(t *testing.T) {
	setupTestEnv(t)
	origRT := routeTable
	routeTable = initRouteTable()
	defer func() { routeTable = origRT }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/routes", nil)
	handleNetworkRoutes(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHB9_HandleNetworkJoinConditions_NilMgr(t *testing.T) {
	setupTestEnv(t)
	origNetMgr := netMgr
	netMgr = nil
	defer func() { netMgr = origNetMgr }()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/network/join-conditions", nil)
	handleNetworkJoinConditions(w, r)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ============================================================
// Extract remote IP test
// ============================================================

func TestHB9_ExtractRemoteIP_XFF(t *testing.T) {
	old := trustedReverseProxy
	trustedReverseProxy = true
	t.Cleanup(func() { trustedReverseProxy = old })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	ip := extractRemoteIP(req)
	if ip != "1.2.3.4" {
		t.Fatalf("expected 1.2.3.4, got %s", ip)
	}
}

func TestHB9_ExtractRemoteIP_XRealIP(t *testing.T) {
	old := trustedReverseProxy
	trustedReverseProxy = true
	t.Cleanup(func() { trustedReverseProxy = old })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "9.8.7.6")
	ip := extractRemoteIP(req)
	if ip != "9.8.7.6" {
		t.Fatalf("expected 9.8.7.6, got %s", ip)
	}
}

func TestHB9_ExtractRemoteIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	ip := extractRemoteIP(req)
	if ip != "10.0.0.1" {
		t.Fatalf("expected 10.0.0.1, got %s", ip)
	}
}

// ============================================================
// Parse duration secs test
// ============================================================

func TestHB9_ParseDurationSecs_Valid(t *testing.T) {
	d := parseDurationSecs("30", 10)
	if d != 30*time.Second {
		t.Fatalf("expected 30s, got %v", d)
	}
}

func TestHB9_ParseDurationSecs_Invalid(t *testing.T) {
	d := parseDurationSecs("abc", 10)
	if d != 10*time.Second {
		t.Fatalf("expected 10s, got %v", d)
	}
}

func TestHB9_ParseDurationSecs_Zero(t *testing.T) {
	d := parseDurationSecs("0", 10)
	if d != 10*time.Second {
		t.Fatalf("expected 10s default, got %v", d)
	}
}

// ============================================================
// NetworkManager additional tests
// ============================================================

func TestHB9_NetworkManager_StopRefreshLoop_Nil(t *testing.T) {
	nm := &NetworkManager{}
	nm.stopRefreshLoop()
}

func TestHB9_NetworkManager_assertNodeIDInvariant_NilNode(t *testing.T) {
	origNode := node
	node = nil
	defer func() { node = origNode }()
	nm := &NetworkManager{}
	result := nm.assertNodeIDInvariant()
	if result != "" {
		t.Fatalf("expected empty, got %s", result)
	}
}

// TestHB9_assertNodeIDInvariant_SelfHealsStaleCache verifies the REQ-S2-3
// invariant repair: when config.NodeID caches a stale value that diverges from
// the canonical identity (node.key) NodeID, assertNodeIDInvariant rewrites the
// canonical id into config AND persists it to network.json, so the divergence
// is gone after a single activation (no manual JSON surgery required).
func TestHB9_assertNodeIDInvariant_SelfHealsStaleCache(t *testing.T) {
	setupDiscoveryTestEnv(t)
	canonical := node.NodeID()
	if canonical == "" {
		t.Fatal("test node identity has empty NodeID")
	}
	// Force a stale cached value that diverges from the canonical identity.
	stale := "mmx-stale00000000000000000000000000000000000000000000000000000000000000"
	netMgr.config.NodeID = stale

	got := netMgr.assertNodeIDInvariant()
	if got != canonical {
		t.Fatalf("assertNodeIDInvariant() = %q, want canonical %q", got, canonical)
	}
	// In-memory config must be repaired.
	if netMgr.config.NodeID != canonical {
		t.Fatalf("self-heal left cached NodeID = %q, want %q", netMgr.config.NodeID, canonical)
	}
	// Repair must be persisted to disk (network.json).
	b, err := os.ReadFile(netMgr.dataPath)
	if err != nil {
		t.Fatalf("read network.json: %v", err)
	}
	var saved NetworkConfig
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatalf("unmarshal network.json: %v", err)
	}
	if saved.NodeID != canonical {
		t.Fatalf("network.json NodeID = %q, want canonical %q", saved.NodeID, canonical)
	}
}

// ============================================================
// Tracker round tests
// ============================================================

func TestHB9_Round1(t *testing.T) {
	if round1(1.23456) != 1.2 {
		t.Fatalf("unexpected: %f", round1(1.23456))
	}
}

func TestHB9_Round4(t *testing.T) {
	if round4(1.23456789) != 1.2346 {
		t.Fatalf("unexpected: %f", round4(1.23456789))
	}
}

// ============================================================
// MustMarshalJSON test
// ============================================================

func TestHB9_MustMarshalJSON(t *testing.T) {
	data := mustMarshalJSON(map[string]string{"key": "value"})
	if len(data) == 0 {
		t.Fatal("expected non-empty data")
	}
}

// ============================================================
// Client pure function tests
// ============================================================

func TestHB9_Truncate(t *testing.T) {
	if truncate("hello", 3) != "hel" {
		t.Fatalf("unexpected: %s", truncate("hello", 3))
	}
	if truncate("hi", 10) != "hi" {
		t.Fatalf("unexpected: %s", truncate("hi", 10))
	}
	if truncate("", 5) != "" {
		t.Fatal("expected empty")
	}
}

func TestHB9_StrPtr(t *testing.T) {
	p := strPtr("test")
	if p == nil || *p != "test" {
		t.Fatal("expected pointer to 'test'")
	}
}

func TestHB9_JSONBody(t *testing.T) {
	r := jsonBody(map[string]string{"a": "b"})
	if r == nil {
		t.Fatal("expected non-nil reader")
	}
}

// ============================================================
// DefaultBalanceConfig test
// ============================================================

func TestHB9_DefaultBalanceConfig_Values(t *testing.T) {
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

// ============================================================
// DefaultRegionConfig test
// ============================================================

func TestHB9_DefaultRegionConfig_Values(t *testing.T) {
	cfg := DefaultRegionConfig()
	if !cfg.PreferLocal {
		t.Fatal("expected PreferLocal=true")
	}
	if cfg.CrossRegionThreshold != 2.0 {
		t.Fatalf("expected 2.0, got %f", cfg.CrossRegionThreshold)
	}
}

// ============================================================
// SimpleError test
// ============================================================

func TestHB9_SimpleError(t *testing.T) {
	e := &simpleError{msg: "test error"}
	if e.Error() != "test error" {
		t.Fatalf("expected 'test error', got %s", e.Error())
	}
}

// ============================================================
// Network relay helpers
// ============================================================

func TestHB9_Sha256Hex(t *testing.T) {
	h := sha256Hex([]byte("test"))
	if len(h) != 64 {
		t.Fatalf("expected 64-char hex, got %d", len(h))
	}
}

// ============================================================
// NetworkManager CheckJoinConditions test
// ============================================================

func TestHB9_NetworkManager_CheckJoinConditions(t *testing.T) {
	nm := &NetworkManager{config: NetworkConfig{NetworkEnabled: true}}
	eligible, result := nm.CheckJoinConditions()
	_ = eligible
	_ = result
}

// ============================================================
// DisclaimerResponse test
// ============================================================

func TestHB9_GetDisclaimer(t *testing.T) {
	d := GetDisclaimer()
	if d.Title == "" {
		t.Fatal("expected non-empty title")
	}
	if len(d.Sections) == 0 {
		t.Fatal("expected non-empty sections")
	}
}

// ============================================================
// NetworkConfig defaults test
// ============================================================

func TestHB9_NetworkConfig_ZeroValue(t *testing.T) {
	nc := NetworkConfig{}
	if nc.RelayEnabled {
		t.Fatal("zero-value RelayEnabled should be false")
	}
}

// ============================================================
// ShareBoundaryConfig defaults
// ============================================================

func TestHB9_ShareBoundaryConfig_Defaults(t *testing.T) {
	sbc := ShareBoundaryConfig{}
	if sbc.DailyContribCap != 0 {
		t.Fatal("expected 0 default")
	}
	if sbc.ShareIdleOnly != false {
		t.Fatal("expected false default")
	}
}

// ============================================================
// Anthropic convertFinishReason
// ============================================================

func TestHB9_ConvertFinishReason(t *testing.T) {
	if convertFinishReason("stop") != "end_turn" {
		t.Fatalf("unexpected: %s", convertFinishReason("stop"))
	}
	if convertFinishReason("length") != "max_tokens" {
		t.Fatalf("unexpected: %s", convertFinishReason("length"))
	}
	if convertFinishReason("tool_calls") != "tool_use" {
		t.Fatalf("unexpected: %s", convertFinishReason("tool_calls"))
	}
	if convertFinishReason("other") != "end_turn" {
		t.Fatalf("unexpected: %s", convertFinishReason("other"))
	}
}

// ============================================================
// AtomicWriteFile test
// ============================================================

func TestHB9_AtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.txt"
	if err := atomicWriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
