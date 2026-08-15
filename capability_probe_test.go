package main

import (
	"errors"
	"testing"
	"time"
)

// TestProbeSchedule_IntervalsByReputation verifies the active-probing scheduler
// picks the right interval by node reputation:
//   - suspicious node (OverallScore < 30) → 1min
//   - high-rep node (OverallScore > 80)  → 2h
//   - default (no reputation / no recent route) → 30min
//
// This covers the scheduling half of the "主动探测交叉验证" (active probe +
// cross-verification) subsystem that previously had no automated unit test.
func TestProbeSchedule_IntervalsByReputation(t *testing.T) {
	origRep, origRT := repMgr, routeTable
	defer func() { repMgr, routeTable = origRep, origRT }()
	dir := t.TempDir()

	// default: no repMgr and no routeTable → 30min
	repMgr, routeTable = nil, nil
	if got := probeSchedule("node-unknown"); got != 30*time.Minute {
		t.Fatalf("default expected 30m, got %v", got)
	}

	// suspicious node (score < 30) → 1min
	repMgr = &ReputationManager{
		scores:   map[string]*NodeReputation{"susp": {OverallScore: 10}},
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	if got := probeSchedule("susp"); got != 1*time.Minute {
		t.Fatalf("suspicious expected 1m, got %v", got)
	}

	// high-rep node (score > 80) → 2h
	repMgr = &ReputationManager{
		scores:   map[string]*NodeReputation{"vip": {OverallScore: 95}},
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
	if got := probeSchedule("vip"); got != 2*time.Hour {
		t.Fatalf("high-rep expected 2h, got %v", got)
	}
}

// TestCapabilityVerifier_CrossVerifyQuorum verifies the cross-verification quorum:
// distinct successful peers are counted, duplicates are de-duplicated by PeerID,
// and the quorum (minVerifiers) decision is returned correctly.
func TestCapabilityVerifier_CrossVerifyQuorum(t *testing.T) {
	cv := NewCapabilityVerifier(func(peerID, modelID string) (bool, int64, error) {
		return true, 50, nil
	}, 3)

	cv.Probe("peerA", "m1")
	cv.Probe("peerB", "m1")
	cv.Probe("peerC", "m1")
	n, ok := cv.CrossVerify("m1")
	if n != 3 || !ok {
		t.Fatalf("expected quorum (3,true), got (%d,%v)", n, ok)
	}

	// duplicate peer must not double-count
	cv.Probe("peerA", "m2")
	cv.Probe("peerA", "m2")
	n2, _ := cv.CrossVerify("m2")
	if n2 != 1 {
		t.Fatalf("duplicate peer must count once, got %d", n2)
	}

	// below quorum (2 distinct < minVerifiers 3) → not verified
	cv2 := NewCapabilityVerifier(func(peerID, modelID string) (bool, int64, error) {
		return true, 50, nil
	}, 3)
	cv2.Probe("p1", "mx")
	cv2.Probe("p2", "mx")
	n3, ok3 := cv2.CrossVerify("mx")
	if n3 != 2 || ok3 {
		t.Fatalf("expected (2,false) below quorum=3, got (%d,%v)", n3, ok3)
	}
}

// TestCapabilityVerifier_VerifyClaim verifies that a capability claim triggers a
// probe per claimed model and that a single failed model flips allOK to false.
func TestCapabilityVerifier_VerifyClaim(t *testing.T) {
	// all models succeed
	cv := NewCapabilityVerifier(func(peerID, modelID string) (bool, int64, error) {
		return true, 10, nil
	}, 2)
	claim := &CapabilityClaim{PeerID: "peerX", Models: []string{"a", "b"}}
	results, allOK := cv.VerifyClaim(claim)
	if len(results) != 2 || !allOK {
		t.Fatalf("expected 2 results allOK, got %d / %v", len(results), allOK)
	}

	// one model fails → allOK false
	cv2 := NewCapabilityVerifier(func(peerID, modelID string) (bool, int64, error) {
		if modelID == "bad" {
			return false, 0, errors.New("probe failed")
		}
		return true, 10, nil
	}, 2)
	claim2 := &CapabilityClaim{PeerID: "peerY", Models: []string{"good", "bad"}}
	_, allOK2 := cv2.VerifyClaim(claim2)
	if allOK2 {
		t.Fatal("expected allOK false when a claimed model fails to probe")
	}
}
