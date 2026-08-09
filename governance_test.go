package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestGovernance_ProposeRatifyPass(t *testing.T) {
	voters := func() []string { return []string{"n1", "n2", "n3"} }
	g := NewGovernanceLedger("self", voters, "")
	p, err := g.Propose("n1", GovTypeAllowModel, "allow gpt-x", json.RawMessage(`{"model":"gpt-x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "open" {
		t.Fatalf("new proposal should be open, got %s", p.Status)
	}

	// 3 eligible voters → supermajority = ceil(2/3*3) = 2 approvals.
	if _, err := g.Ratify(p.ID, "n1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Ratify(p.ID, "n2", true); err != nil {
		t.Fatal(err)
	}

	got, open, approved, eligible, need := g.Tally(p.ID)
	if !got || open {
		t.Fatalf("proposal should be closed after supermajority, open=%v", open)
	}
	if approved != 2 || eligible != 3 || need != 2 {
		t.Fatalf("tally wrong: approved=%d eligible=%d need=%d", approved, eligible, need)
	}
	if !g.VerifyChain() {
		t.Fatal("governance chain should verify")
	}
	pp, _ := g.Get(p.ID)
	if pp.Status != "ratified" {
		t.Fatalf("proposal should be ratified, got %s", pp.Status)
	}
}

func TestGovernance_DoubleRatifyRejected(t *testing.T) {
	g := NewGovernanceLedger("self", func() []string { return []string{"n1", "n2"} }, "")
	p, _ := g.Propose("n1", GovTypeAdmitNode, "admit x", nil)
	if _, err := g.Ratify(p.ID, "n1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Ratify(p.ID, "n1", true); err == nil {
		t.Fatal("second ratification by the same node must be rejected")
	}
}

func TestGovernance_SpamGuard(t *testing.T) {
	g := NewGovernanceLedger("self", func() []string { return []string{"n1"} }, "")
	for i := 0; i < govMaxOpenPerProposer; i++ {
		if _, err := g.Propose("spammer", GovTypeParam, fmt.Sprintf("p%d", i), nil); err != nil {
			t.Fatalf("proposal %d should be accepted: %v", i, err)
		}
	}
	if _, err := g.Propose("spammer", GovTypeParam, "one-too-many", nil); err == nil {
		t.Fatal("exceeding the open-proposal cap should be rejected (spam guard)")
	}
}

func TestGovernance_RejectByMajority(t *testing.T) {
	// 4 eligible voters → supermajority = ceil(2/3*4) = 3 rejections.
	g := NewGovernanceLedger("self", func() []string { return []string{"n1", "n2", "n3", "n4"} }, "")
	p, _ := g.Propose("n1", GovTypeParam, "change x", nil)
	g.Ratify(p.ID, "n1", false)
	g.Ratify(p.ID, "n2", false)
	g.Ratify(p.ID, "n3", false)
	pp, _ := g.Get(p.ID)
	if pp.Status != "rejected" {
		t.Fatalf("proposal should be rejected by supermajority, got %s", pp.Status)
	}
}

func TestGovernance_SingleNodeSelfRatify(t *testing.T) {
	// A lone node (no contributors yet) can still self-ratify its commons.
	g := NewGovernanceLedger("self", func() []string { return nil }, "")
	p, _ := g.Propose("self", GovTypeAllowModel, "bootstrap allowlist", nil)
	if _, err := g.Ratify(p.ID, "self", true); err != nil {
		t.Fatal(err)
	}
	pp, _ := g.Get(p.ID)
	if pp.Status != "ratified" {
		t.Fatalf("single node should self-ratify, got %s", pp.Status)
	}
}
