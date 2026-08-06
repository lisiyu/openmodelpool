package main

import (
	"testing"
)

// ============================================================
// AlgorithmChain tests
// ============================================================

func TestDefaultAlgorithmParams(t *testing.T) {
	params := DefaultAlgorithmParams()

	if params.OpenKeyRatio != 0.30 {
		t.Errorf("OpenKeyRatio = %f, want 0.30", params.OpenKeyRatio)
	}
	if params.GlobalPoolAvailabilityRatio != 0.80 {
		t.Errorf("GlobalPoolAvailabilityRatio = %f, want 0.80", params.GlobalPoolAvailabilityRatio)
	}
	if params.TrustWeight != 0.25 {
		t.Errorf("TrustWeight = %f, want 0.25", params.TrustWeight)
	}
	if params.ReputationWeight != 0.25 {
		t.Errorf("ReputationWeight = %f, want 0.25", params.ReputationWeight)
	}
	if params.LatencyWeight != 0.20 {
		t.Errorf("LatencyWeight = %f, want 0.20", params.LatencyWeight)
	}
	if params.AvailabilityWeight != 0.15 {
		t.Errorf("AvailabilityWeight = %f, want 0.15", params.AvailabilityWeight)
	}
	if params.ContributionWeight != 0.15 {
		t.Errorf("ContributionWeight = %f, want 0.15", params.ContributionWeight)
	}
}

func TestNewAlgorithmChain(t *testing.T) {
	chain := NewAlgorithmChain()
	if chain == nil {
		t.Fatal("NewAlgorithmChain() returned nil")
	}

	params := chain.GetCurrentParams()
	expected := DefaultAlgorithmParams()
	if params != expected {
		t.Error("GetCurrentParams() should return DefaultAlgorithmParams initially")
	}
}

func TestAlgorithmChain_GetCurrentParams(t *testing.T) {
	chain := NewAlgorithmChain()
	params := chain.GetCurrentParams()

	if params.OpenKeyRatio != 0.30 {
		t.Errorf("expected default OpenKeyRatio, got %f", params.OpenKeyRatio)
	}
}

func TestAlgorithmChain_UpdateParams(t *testing.T) {
	chain := NewAlgorithmChain()

	newParams := AlgorithmParams{
		OpenKeyRatio:                0.50,
		GlobalPoolAvailabilityRatio: 0.90,
		TrustWeight:                 0.30,
		ReputationWeight:            0.30,
		LatencyWeight:               0.15,
		AvailabilityWeight:          0.10,
		ContributionWeight:          0.15,
	}

	chain.UpdateParams(newParams)
	got := chain.GetCurrentParams()

	if got.OpenKeyRatio != newParams.OpenKeyRatio {
		t.Errorf("OpenKeyRatio = %f, want %f", got.OpenKeyRatio, newParams.OpenKeyRatio)
	}
	if got.TrustWeight != newParams.TrustWeight {
		t.Errorf("TrustWeight = %f, want %f", got.TrustWeight, newParams.TrustWeight)
	}
}

func TestInitAlgorithmChain(t *testing.T) {
	// Save original
	orig := algoChain
	defer func() { algoChain = orig }()

	initAlgorithmChain("/tmp/test-data")

	if algoChain == nil {
		t.Fatal("initAlgorithmChain did not set algoChain")
	}

	params := algoChain.GetCurrentParams()
	if params.OpenKeyRatio != 0.30 {
		t.Error("expected default params after init")
	}
}
