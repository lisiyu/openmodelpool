package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// G3: Algorithm governance / voting — real endpoint behavior tests.
// These exercise the previously-stubbed /api/network/algorithm/* handlers
// against an isolated, persisted node-local governor.
// ---------------------------------------------------------------------------

// testProposalView is the decoded API view of a proposal (proposal + tally).
type testProposalView struct {
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	Status        string          `json:"status"`
	Proposer      string          `json:"proposer"`
	Votes         []AlgorithmVote `json:"votes"`
	Tally         ProposalTally   `json:"tally"`
	ResolvedBy    string          `json:"resolved_by"`
	ResolveReason string          `json:"resolve_reason"`
}

type testListResp struct {
	Proposals []testProposalView `json:"proposals"`
	Count     int                `json:"count"`
	Scope     string             `json:"governance_scope"`
}

type testHistoryResp struct {
	History []GovernanceEvent `json:"history"`
	Count   int               `json:"count"`
	Scope   string            `json:"governance_scope"`
}

// toJSON marshals v to a JSON string for request bodies.
func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return string(b)
}

// govPost issues a POST to an http.HandlerFunc with a JSON body.
func govPost(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// govGet issues a GET to an http.HandlerFunc.
func govGet(t *testing.T, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// govResolve issues a resolve request with the proposal id set as a path value.
func govResolve(t *testing.T, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	handleAlgorithmProposalResolve(w, r)
	return w
}

// decodeProposalResp extracts the wrapped proposal view from a handler response.
func decodeProposalResp(t *testing.T, w *httptest.ResponseRecorder) testProposalView {
	t.Helper()
	var resp struct {
		Proposal testProposalView `json:"proposal"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode proposal resp: %v (body=%s)", err, w.Body.String())
	}
	return resp.Proposal
}

// ① 发起提案成功并出现在 proposals
func TestAlgorithmProposeAppearsInProposals(t *testing.T) {
	env := setupTestEnv(t)
	initAlgorithmGovernance(env.dir)

	w := govPost(t, handleAlgorithmPropose, toJSON(t, map[string]any{
		"title":       "提高开放密钥比例至 0.4",
		"description": "放宽开放密钥上限",
		"target":      map[string]any{"open_key_ratio": 0.4},
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("propose status=%d body=%s", w.Code, w.Body.String())
	}
	p := decodeProposalResp(t, w)
	if p.ID == "" {
		t.Fatal("proposal id is empty")
	}
	if p.Title != "提高开放密钥比例至 0.4" {
		t.Fatalf("title=%q", p.Title)
	}
	if p.Status != string(ProposalStatusOpen) {
		t.Fatalf("status=%q, want open", p.Status)
	}

	// It must now appear in the proposals list.
	lw := govGet(t, handleAlgorithmProposals)
	var lr testListResp
	if err := json.Unmarshal(lw.Body.Bytes(), &lr); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if lr.Count != 1 || len(lr.Proposals) != 1 {
		t.Fatalf("expected 1 proposal, got count=%d len=%d", lr.Count, len(lr.Proposals))
	}
	if lr.Proposals[0].ID != p.ID {
		t.Fatal("created proposal not present in list")
	}
	if lr.Scope != GovernanceScope {
		t.Fatalf("governance_scope=%q, want %q", lr.Scope, GovernanceScope)
	}
}

// ② 投票被记录且计票正确（含 voter 去重）
func TestAlgorithmVoteRecordedAndTallied(t *testing.T) {
	env := setupTestEnv(t)
	initAlgorithmGovernance(env.dir)
	p := decodeProposalResp(t, govPost(t, handleAlgorithmPropose, toJSON(t, map[string]any{"title": "t"})))

	// Three distinct voters → yes / no / abstain.
	govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": p.ID, "voter": "v1", "choice": "yes"}))
	govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": p.ID, "voter": "v2", "choice": "no"}))
	govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": p.ID, "voter": "v3", "choice": "abstain"}))

	lw := govGet(t, handleAlgorithmProposals)
	var lr testListResp
	_ = json.Unmarshal(lw.Body.Bytes(), &lr)
	got := lr.Proposals[0]
	if got.Tally.Yes != 1 || got.Tally.No != 1 || got.Tally.Abstain != 1 || got.Tally.Total != 3 {
		t.Fatalf("tally after 3 votes = %+v", got.Tally)
	}
	if len(got.Votes) != 3 {
		t.Fatalf("votes len=%d, want 3", len(got.Votes))
	}

	// De-duplicate: v1 changes to "no" → total stays 3, yes→0, no→2.
	govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": p.ID, "voter": "v1", "choice": "no"}))
	lw = govGet(t, handleAlgorithmProposals)
	_ = json.Unmarshal(lw.Body.Bytes(), &lr)
	got = lr.Proposals[0]
	if got.Tally.Yes != 0 || got.Tally.No != 2 || got.Tally.Total != 3 {
		t.Fatalf("tally after dedup = %+v", got.Tally)
	}
	if len(got.Votes) != 3 {
		t.Fatalf("dedup votes len=%d, want 3", len(got.Votes))
	}

	// Invalid choice must be rejected.
	bad := govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": p.ID, "voter": "v4", "choice": "maybe"}))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid choice: want 400, got %d", bad.Code)
	}

	// Unknown proposal must be rejected.
	unknown := govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": "nope", "voter": "v5", "choice": "yes"}))
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown proposal: want 400, got %d", unknown.Code)
	}
}

// ③ history 反映提案 + 投票
func TestAlgorithmHistoryReflectsProposeAndVote(t *testing.T) {
	env := setupTestEnv(t)
	initAlgorithmGovernance(env.dir)
	p := decodeProposalResp(t, govPost(t, handleAlgorithmPropose, toJSON(t, map[string]any{"title": "t"})))
	govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": p.ID, "voter": "v1", "choice": "yes"}))

	hw := govGet(t, handleAlgorithmHistory)
	var hr testHistoryResp
	if err := json.Unmarshal(hw.Body.Bytes(), &hr); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if hr.Count < 2 {
		t.Fatalf("history count=%d, want >= 2", hr.Count)
	}
	if hr.Scope != GovernanceScope {
		t.Fatalf("governance_scope=%q, want %q", hr.Scope, GovernanceScope)
	}
	types := map[string]bool{}
	for _, e := range hr.History {
		types[e.Type] = true
		if e.ProposalID != p.ID {
			t.Fatalf("event proposal_id=%q, want %q", e.ProposalID, p.ID)
		}
	}
	if !types["proposal_created"] || !types["vote_cast"] {
		t.Fatalf("missing expected event types: %+v", types)
	}
}

// ④ 状态端点真实反映（开放 → 通过/否决/已关闭）
func TestAlgorithmStatusReflectsResolve(t *testing.T) {
	env := setupTestEnv(t)
	initAlgorithmGovernance(env.dir)
	p := decodeProposalResp(t, govPost(t, handleAlgorithmPropose, toJSON(t, map[string]any{"title": "t"})))

	// Resolve as passed.
	rw := govResolve(t, p.ID, toJSON(t, map[string]any{"decision": "passed", "reason": "quorum reached"}))
	if rw.Code != http.StatusOK {
		t.Fatalf("resolve passed status=%d body=%s", rw.Code, rw.Body.String())
	}
	rp := decodeProposalResp(t, rw)
	if rp.Status != string(ProposalStatusPassed) {
		t.Fatalf("status=%q, want passed", rp.Status)
	}
	if rp.ResolvedBy == "" {
		t.Fatal("resolved_by is empty after resolve")
	}

	// Voting on a terminal proposal must be rejected.
	vw := govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": p.ID, "voter": "v9", "choice": "yes"}))
	if vw.Code != http.StatusBadRequest {
		t.Fatalf("vote on terminal: want 400, got %d", vw.Code)
	}

	// Rejected path.
	p2 := decodeProposalResp(t, govPost(t, handleAlgorithmPropose, toJSON(t, map[string]any{"title": "t2"})))
	rw2 := govResolve(t, p2.ID, toJSON(t, map[string]any{"decision": "rejected"}))
	if rw2.Code != http.StatusOK {
		t.Fatalf("resolve rejected status=%d", rw2.Code)
	}
	if decodeProposalResp(t, rw2).Status != string(ProposalStatusRejected) {
		t.Fatal("p2 not rejected")
	}

	// Closed path.
	p3 := decodeProposalResp(t, govPost(t, handleAlgorithmPropose, toJSON(t, map[string]any{"title": "t3"})))
	rw3 := govResolve(t, p3.ID, toJSON(t, map[string]any{"decision": "closed"}))
	if rw3.Code != http.StatusOK {
		t.Fatalf("resolve closed status=%d", rw3.Code)
	}
	if decodeProposalResp(t, rw3).Status != string(ProposalStatusClosed) {
		t.Fatal("p3 not closed")
	}

	// Invalid decision must be rejected.
	bad := govResolve(t, p3.ID, toJSON(t, map[string]any{"decision": "banana"}))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid decision: want 400, got %d", bad.Code)
	}

	// Resolve reflects in proposals listing.
	lw := govGet(t, handleAlgorithmProposals)
	var lr testListResp
	_ = json.Unmarshal(lw.Body.Bytes(), &lr)
	byID := map[string]testProposalView{}
	for _, x := range lr.Proposals {
		byID[x.ID] = x
	}
	if byID[p.ID].Status != string(ProposalStatusPassed) ||
		byID[p2.ID].Status != string(ProposalStatusRejected) ||
		byID[p3.ID].Status != string(ProposalStatusClosed) {
		t.Fatalf("status not reflected in list: %+v", byID)
	}
}

// ⑤ 持久化：重启（重新初始化）后提案/投票/历史可恢复
func TestAlgorithmGovernancePersistsAcrossReload(t *testing.T) {
	env := setupTestEnv(t)
	dir := env.dir
	initAlgorithmGovernance(dir)

	p := decodeProposalResp(t, govPost(t, handleAlgorithmPropose, toJSON(t, map[string]any{"title": "persist-me"})))
	govPost(t, handleAlgorithmVote, toJSON(t, map[string]any{"proposal_id": p.ID, "voter": "v1", "choice": "yes"}))

	// Simulate a restart: re-initialize the governor on the same data dir.
	initAlgorithmGovernance(dir)
	if governor == nil {
		t.Fatal("governor nil after reload")
	}
	got, ok := governor.GetProposal(p.ID)
	if !ok {
		t.Fatal("proposal lost after reload")
	}
	if got.Title != "persist-me" {
		t.Fatalf("title=%q after reload", got.Title)
	}
	if got.Tally().Total != 1 {
		t.Fatalf("votes lost after reload: total=%d", got.Tally().Total)
	}
	if len(governor.GetHistory()) < 2 {
		t.Fatalf("history lost after reload: %d events", len(governor.GetHistory()))
	}
}
