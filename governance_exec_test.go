package main

import (
	"encoding/json"
	"testing"
)

// fourVoters reaches supermajority at 3 approvals (ceil(2/3*4)=3).
func fourVoters() []string { return []string{"n1", "n2", "n3", "n4"} }

func TestGovernance_AdmitNodeTakesEffect(t *testing.T) {
	g := NewGovernanceLedger("self", fourVoters, "")
	p, err := g.Propose("n1", GovTypeAdmitNode, "admit new-node", json.RawMessage(`{"node_id":"new-node"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"n1", "n2", "n3"} {
		if _, err := g.Ratify(p.ID, v, true); err != nil {
			t.Fatal(err)
		}
	}
	if p.Status != "ratified" {
		t.Fatalf("status=%s, want ratified", p.Status)
	}
	if !g.IsCommunityAdmitted("new-node") {
		t.Fatal("new-node should be on the admitted roster after ratification")
	}
	found := false
	for _, id := range g.AdmittedNodes() {
		if id == "new-node" {
			found = true
		}
	}
	if !found {
		t.Fatal("AdmittedNodes() should contain new-node")
	}
}

func TestGovernance_AllowModelTakesEffect(t *testing.T) {
	g := NewGovernanceLedger("self", fourVoters, "")
	p, err := g.Propose("n1", GovTypeAllowModel, "allow gpt-4", json.RawMessage(`{"model_id":"gpt-4"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"n1", "n2", "n3"} {
		if _, err := g.Ratify(p.ID, v, true); err != nil {
			t.Fatal(err)
		}
	}
	if !g.IsCommunityCuratedModel("gpt-4") {
		t.Fatal("gpt-4 should be on the curated-model roster after ratification")
	}
}

// param_change is audit-only: ratifying it must NOT mutate either curation
// roster, while a previously-ratified admit_node stays intact.
func TestGovernance_ParamChangeAuditOnly(t *testing.T) {
	g := NewGovernanceLedger("self", fourVoters, "")

	pa, _ := g.Propose("n1", GovTypeAdmitNode, "admit x", json.RawMessage(`{"node_id":"x"}`))
	for _, v := range []string{"n1", "n2", "n3"} {
		g.Ratify(pa.ID, v, true)
	}

	pp, err := g.Propose("n1", GovTypeParam, "change ratio", json.RawMessage(`{"key":"open_key_ratio","value":0.9}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"n1", "n2", "n3"} {
		g.Ratify(pp.ID, v, true)
	}
	if pp.Status != "ratified" {
		t.Fatalf("param_change status=%s, want ratified", pp.Status)
	}
	if !g.IsCommunityAdmitted("x") {
		t.Fatal("admit roster must stay intact after a param_change proposal")
	}
	if len(g.AllowedModels()) != 0 {
		t.Fatal("param_change must not populate the curated-model roster")
	}
}

// Ratified effects must survive a reload: rebuilding the roster from the
// persisted ledger on load keeps it consistent with the tamper-evident record.
func TestGovernance_EffectRebuiltOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/governance.json"

	g := NewGovernanceLedger("self", fourVoters, path)
	p, _ := g.Propose("n1", GovTypeAdmitNode, "admit loaded-node", json.RawMessage(`{"node_id":"loaded-node"}`))
	for _, v := range []string{"n1", "n2", "n3"} {
		g.Ratify(p.ID, v, true)
	}

	g2 := NewGovernanceLedger("self", fourVoters, path)
	if !g2.IsCommunityAdmitted("loaded-node") {
		t.Fatal("admitted roster not rebuilt from disk on load")
	}
	if p.Status != "ratified" {
		t.Fatalf("reloaded proposal status=%s", p.Status)
	}
}

// Malformed admit_node payload (missing node_id) ratifies but records nothing.
func TestGovernance_AdmitNodeBadPayloadNoop(t *testing.T) {
	g := NewGovernanceLedger("self", fourVoters, "")
	p, _ := g.Propose("n1", GovTypeAdmitNode, "admit empty", json.RawMessage(`{"node_id":""}`))
	for _, v := range []string{"n1", "n2", "n3"} {
		g.Ratify(p.ID, v, true)
	}
	if p.Status != "ratified" {
		t.Fatalf("status=%s", p.Status)
	}
	if len(g.AdmittedNodes()) != 0 {
		t.Fatal("empty node_id must not be added to the roster")
	}
}
