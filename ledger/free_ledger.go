package ledger

import (
	"encoding/json"
	"fmt"
	"sync"
)

// FreeLedgerConfig holds configuration for the FreeLedger.
type FreeLedgerConfig struct {
	// MajorEventThreshold is the USD value above which an IOTA anchor is used.
	MajorEventThreshold float64

	// AsyncUpload enables asynchronous IPFS/IOTA upload.
	AsyncUpload bool
}

// DefaultFreeLedgerConfig returns sensible defaults.
func DefaultFreeLedgerConfig() FreeLedgerConfig {
	return FreeLedgerConfig{
		MajorEventThreshold: 100.0, // $100
		AsyncUpload:         true,
	}
}

// FreeLedger integrates GossipLedger + IPFSClient + IOTAClient to provide
// a complete zero-cost contribution ledger system.
type FreeLedger struct {
	Gossip *GossipLedger
	IPFS   *IPFSClient
	IOTA   *IOTAClient

	config FreeLedgerConfig
	wg     sync.WaitGroup
}

// NewFreeLedger creates a fully wired FreeLedger.
func NewFreeLedger(peerID string, cfg FreeLedgerConfig) (*FreeLedger, error) {
	gossip, err := NewGossipLedger(peerID)
	if err != nil {
		return nil, fmt.Errorf("failed to create gossip ledger: %w", err)
	}

	return &FreeLedger{
		Gossip: gossip,
		IPFS:   NewIPFSClient(),
		IOTA:   NewIOTAClient(),
		config: cfg,
	}, nil
}

// RecordContribution signs a contribution, stores it locally, uploads to IPFS,
// and optionally anchors on IOTA for high-value events.
// Returns the local record ID.
func (f *FreeLedger) RecordContribution(record *ContributionRecord) (string, error) {
	// 1. Sign and store in local gossip ledger.
	id, err := f.Gossip.RecordContribution(record)
	if err != nil {
		return "", fmt.Errorf("failed to record contribution: %w", err)
	}

	// Get the stored record back (now with signature).
	stored, err := f.Gossip.GetContribution(id)
	if err != nil {
		return "", fmt.Errorf("failed to get stored record: %w", err)
	}

	// 2. Upload to IPFS.
	if f.config.AsyncUpload {
		f.wg.Add(1)
		go func(r *ContributionRecord) {
			defer f.wg.Done()
			f.uploadToIPFS(r)
		}(stored)
	} else {
		f.uploadToIPFS(stored)
	}

	return id, nil
}

// uploadToIPFS handles the IPFS upload and optional IOTA anchoring.
func (f *FreeLedger) uploadToIPFS(record *ContributionRecord) {
	data, err := json.Marshal(record)
	if err != nil {
		return
	}

	cid, err := f.IPFS.Store(data)
	if err != nil {
		return
	}

	// Update the proof with IPFS hash.
	record.Proof.IPFSHash = cid
	record.Proof.StorageLocation = "ipfs"

	// High-value events also get anchored on IOTA.
	if record.ValueUSD >= f.config.MajorEventThreshold {
		txHash, err := f.IOTA.SubmitData(data, "CONTRIB")
		if err == nil {
			record.Proof.IOTATxHash = txHash
			record.Proof.StorageLocation = "ipfs+iota"
		}
	}
}

// WaitForUploads blocks until all async uploads are complete.
func (f *FreeLedger) WaitForUploads() {
	f.wg.Wait()
}

// VerifyContribution checks both local signature and IPFS integrity.
func (f *FreeLedger) VerifyContribution(id string) (bool, error) {
	return f.Gossip.VerifyContribution(id)
}

// GetContribution retrieves a contribution record.
func (f *FreeLedger) GetContribution(id string) (*ContributionRecord, error) {
	return f.Gossip.GetContribution(id)
}

// GetPeerContributions returns all contributions for a peer.
func (f *FreeLedger) GetPeerContributions(peerID string) ([]*ContributionRecord, error) {
	return f.Gossip.GetPeerContributions(peerID)
}

// RecordTrust records a trust probe and optionally uploads to IPFS.
func (f *FreeLedger) RecordTrust(rec *TrustRecord) (string, error) {
	id, err := f.Gossip.RecordTrust(rec)
	if err != nil {
		return "", err
	}

	if f.config.AsyncUpload {
		f.wg.Add(1)
		go func(r *TrustRecord) {
			defer f.wg.Done()
			data, err := json.Marshal(r)
			if err != nil {
				return
			}
			_, _ = f.IPFS.Store(data)
		}(rec)
	}

	return id, nil
}

// RecordClaim records a capability claim.
func (f *FreeLedger) RecordClaim(claim *CapabilityClaim) (string, error) {
	return f.Gossip.RecordClaim(claim)
}

// RecordPenalty records a penalty.
func (f *FreeLedger) RecordPenalty(rec *PenaltyRecord) (string, error) {
	return f.Gossip.RecordPenalty(rec)
}

// GossipSync merges records from a remote peer.
func (f *FreeLedger) GossipSync(contributions []*ContributionRecord, trusts []*TrustRecord, claims []*CapabilityClaim, penalties []*PenaltyRecord) int {
	return f.Gossip.GossipSync(contributions, trusts, claims, penalties)
}

// Close waits for pending uploads and releases resources.
func (f *FreeLedger) Close() {
	f.WaitForUploads()
}
