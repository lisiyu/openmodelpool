package main

import "log/slog"

// AlgorithmParams holds the tunable parameters of the decentralized
// algorithm-governance chain.
type AlgorithmParams struct {
	OpenKeyRatio                float64 `json:"open_key_ratio"`
	GlobalPoolAvailabilityRatio float64 `json:"global_pool_availability_ratio"`
	TrustWeight                 float64 `json:"trust_weight"`
	ReputationWeight            float64 `json:"reputation_weight"`
	LatencyWeight               float64 `json:"latency_weight"`
	AvailabilityWeight          float64 `json:"availability_weight"`
	ContributionWeight          float64 `json:"contribution_weight"`
}

// DefaultAlgorithmParams returns sane, documented defaults.
func DefaultAlgorithmParams() AlgorithmParams {
	return AlgorithmParams{
		OpenKeyRatio:                0.30,
		GlobalPoolAvailabilityRatio: 0.80,
		TrustWeight:                 0.25,
		ReputationWeight:            0.25,
		LatencyWeight:               0.20,
		AvailabilityWeight:          0.15,
		ContributionWeight:          0.15,
	}
}

// AlgorithmChain is a minimal, in-process governance chain. The original design
// described a decentralized propose/vote/gossip protocol (see network_algorithm
// docs); this implementation keeps a single authoritative parameter set and is
// intended to be replaced by the distributed protocol in a later phase.
type AlgorithmChain struct {
	params AlgorithmParams
}

// NewAlgorithmChain returns a chain initialized with default parameters.
func NewAlgorithmChain() *AlgorithmChain {
	return &AlgorithmChain{params: DefaultAlgorithmParams()}
}

// GetCurrentParams returns the active parameters.
func (c *AlgorithmChain) GetCurrentParams() AlgorithmParams {
	return c.params
}

// UpdateParams replaces the active parameters.
func (c *AlgorithmChain) UpdateParams(p AlgorithmParams) {
	c.params = p
}

// algoChain is the package-level chain used by the quota/balance engines.
var algoChain *AlgorithmChain

// initAlgorithmChain initializes the global algoChain. The dataDir argument is
// accepted for forward compatibility (future on-disk persistence of params).
func initAlgorithmChain(dataDir string) {
	algoChain = NewAlgorithmChain()
	slog.Info("algorithm chain initialized", "data_dir", dataDir)
}
