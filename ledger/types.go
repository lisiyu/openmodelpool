package ledger

import "time"

// ContributionRecord represents a single contribution made by a peer.
type ContributionRecord struct {
	ID            string       `json:"id"`
	PeerID        string       `json:"peer_id"`
	PeerPublicKey []byte       `json:"peer_public_key"`
	ModelID       string       `json:"model_id"`
	Provider      string       `json:"provider"`
	Tokens        int64        `json:"tokens"`
	ValueUSD      float64      `json:"value_usd"`
	Timestamp     time.Time    `json:"timestamp"`
	Signature     []byte       `json:"signature"`
	Proof         StorageProof `json:"proof"`
}

// StorageProof records where and how a contribution is stored on-chain / off-chain.
type StorageProof struct {
	IPFSHash        string `json:"ipfs_hash"`
	IOTATxHash      string `json:"iota_tx_hash,omitempty"`
	StorageLocation string `json:"storage_location"` // "ipfs" or "ipfs+iota"
	Verified        bool   `json:"verified"`
}

// TrustRecord records the result of a trust probe between two peers.
type TrustRecord struct {
	ID             string    `json:"id"`
	SubjectPeerID  string    `json:"subject_peer_id"`
	VerifierPeerID string    `json:"verifier_peer_id"`
	ModelID        string    `json:"model_id"`
	Success        bool      `json:"success"`
	LatencyMS      int64     `json:"latency_ms"`
	Timestamp      time.Time `json:"timestamp"`
	Signature      []byte    `json:"signature"`
}

// CapabilityClaim is a self-declared capability by a peer.
type CapabilityClaim struct {
	ID         string    `json:"id"`
	PeerID     string    `json:"peer_id"`
	Models     []string  `json:"models"`
	Providers  []string  `json:"providers"`
	MaxQuota   int64     `json:"max_quota"`
	Timestamp  time.Time `json:"timestamp"`
	Signature  []byte    `json:"signature"`
	ValidUntil time.Time `json:"valid_until"`
}

// PenaltyRecord records a penalty issued against a misbehaving peer.
type PenaltyRecord struct {
	ID        string    `json:"id"`
	PeerID    string    `json:"peer_id"`
	Reason    string    `json:"reason"`
	Evidence  []string  `json:"evidence"`
	Action    string    `json:"action"` // "downgrade", "isolate", "ban"
	Timestamp time.Time `json:"timestamp"`
	Verifiers []string  `json:"verifiers"`
	Signature []byte    `json:"signature"`
}

// NodeReliability aggregates probe statistics for a peer.
type NodeReliability struct {
	PeerID          string    `json:"peer_id"`
	TotalProbes     int64     `json:"total_probes"`
	SuccessProbes   int64     `json:"success_probes"`
	FailedProbes    int64     `json:"failed_probes"`
	SuccessRate     float64   `json:"success_rate"`
	AvgLatencyMS    int64     `json:"avg_latency_ms"`
	UptimePercent   float64   `json:"uptime_percent"`
	ReputationScore float64   `json:"reputation_score"`
	PenaltyCount    int       `json:"penalty_count"`
	LastVerified    time.Time `json:"last_verified"`
}

// TrustLevel represents the progressive trust stages of a peer.
type TrustLevel int

const (
	TrustLevelNew       TrustLevel = 0
	TrustLevelLow       TrustLevel = 1
	TrustLevelMedium    TrustLevel = 2
	TrustLevelHigh      TrustLevel = 3
	TrustLevelVerified  TrustLevel = 4
)

// String returns a human-readable trust level name.
func (t TrustLevel) String() string {
	switch t {
	case TrustLevelNew:
		return "new"
	case TrustLevelLow:
		return "low"
	case TrustLevelMedium:
		return "medium"
	case TrustLevelHigh:
		return "high"
	case TrustLevelVerified:
		return "verified"
	default:
		return "unknown"
	}
}

// TokenLedger is a reserved interface for future token economy (Layer 3).
type TokenLedger interface {
	GetBalance(peerID string) (uint64, error)
	Transfer(from, to string, amount uint64) (string, error)
	RewardContribution(peerID string, amount uint64) (string, error)
	ChargeConsumption(peerID string, amount uint64) (string, error)
}

// ContributionLedger defines the interface for the contribution ledger.
type ContributionLedger interface {
	RecordContribution(record *ContributionRecord) (string, error)
	GetContribution(id string) (*ContributionRecord, error)
	VerifyContribution(id string) (bool, error)
	GetPeerContributions(peerID string) ([]*ContributionRecord, error)
}
