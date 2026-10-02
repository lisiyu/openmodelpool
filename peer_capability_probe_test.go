package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// stubProbeSigner is a test-only signRequest that marks requests instead of
// doing real ed25519 relay-forward signing (node keys are unavailable in
// unit tests).
func stubProbeSigner(req *http.Request, method, path string, body []byte) {
	req.Header.Set("X-Test-Probe", "1")
}

// probeTestServer serves a fake peer gateway: /v1/models with a configurable
// model list and /v1/chat/completions with a configurable status. It records
// hits and whether the probe marker header was present.
type probeTestServer struct {
	srv          *httptest.Server
	models       []string
	modelsStatus int
	tokenStatus  int
	mu           sync.Mutex
	hits         map[string]int
	markedHits   map[string]int
	lastTokenReq map[string]any
}

func newProbeTestServer(models []string, tokenStatus int) *probeTestServer {
	return newProbeTestServerFull(models, http.StatusOK, tokenStatus)
}

func newProbeTestServerFull(models []string, modelsStatus, tokenStatus int) *probeTestServer {
	ps := &probeTestServer{
		models:       models,
		modelsStatus: modelsStatus,
		tokenStatus:  tokenStatus,
		hits:         make(map[string]int),
		markedHits:   make(map[string]int),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.hits["models"]++
		if r.Header.Get("X-Test-Probe") == "1" {
			ps.markedHits["models"]++
		}
		ps.mu.Unlock()
		if ps.modelsStatus != http.StatusOK {
			w.WriteHeader(ps.modelsStatus)
			return
		}
		data := make([]map[string]string, 0, len(ps.models))
		for _, m := range ps.models {
			data = append(data, map[string]string{"id": m, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		ps.mu.Lock()
		ps.hits["token"]++
		if r.Header.Get("X-Test-Probe") == "1" {
			ps.markedHits["token"]++
		}
		ps.lastTokenReq = body
		ps.mu.Unlock()
		w.WriteHeader(ps.tokenStatus)
		if ps.tokenStatus == http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"chatcmpl-probe","object":"chat.completion","choices":[]}`))
		}
	})
	ps.srv = httptest.NewServer(mux)
	return ps
}

func (ps *probeTestServer) close() { ps.srv.Close() }

func (ps *probeTestServer) hitCount(which string) int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.hits[which]
}

func (ps *probeTestServer) markedCount(which string) int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.markedHits[which]
}

// newTestProber builds a prober wired to a fake peer, with an isolated
// reputation manager installed as the global repMgr (restored on cleanup).
func newTestProber(t *testing.T, ps *probeTestServer, models []string) *PeerCapabilityProber {
	t.Helper()
	origRep := repMgr
	t.Cleanup(func() { repMgr = origRep })
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  t.TempDir(),
	}
	p := newPeerCapabilityProber()
	p.httpClient = ps.srv.Client()
	p.signRequest = stubProbeSigner
	p.listTargets = func() []capabilityProbeTarget {
		return []capabilityProbeTarget{{nodeID: "peer1", endpoint: ps.srv.URL, models: models}}
	}
	return p
}

// TestPeerCapabilityProbe_TwoStageSuccess verifies the happy path: the model
// is listed by /v1/models and the max_tokens:1 probe returns 200.
func TestPeerCapabilityProbe_TwoStageSuccess(t *testing.T) {
	ps := newProbeTestServer([]string{"m1", "other"}, http.StatusOK)
	defer ps.close()
	p := newTestProber(t, ps, []string{"m1"})

	res := p.probeClaim(capabilityProbeTarget{nodeID: "peer1", endpoint: ps.srv.URL}, "m1")
	if res.outcome != probeOutcomeSuccess {
		t.Fatalf("expected success, got %v (%s)", res.outcome, res.detail)
	}
	if !res.listed {
		t.Fatal("expected listed=true from stage 1")
	}
	if ps.markedCount("models") != 1 || ps.markedCount("token") != 1 {
		t.Fatalf("both stages must carry probe auth, marked=%v", ps.markedHits)
	}
	// The token probe must be minimal: max_tokens 1, non-streaming.
	ps.mu.Lock()
	body := ps.lastTokenReq
	ps.mu.Unlock()
	if body["model"] != "m1" {
		t.Fatalf("token probe model = %v, want m1", body["model"])
	}
	if mt, ok := body["max_tokens"].(float64); !ok || mt != 1 {
		t.Fatalf("token probe max_tokens = %v, want 1", body["max_tokens"])
	}
	if body["stream"] == true {
		t.Fatal("token probe must not stream")
	}
}

// TestPeerCapabilityProbe_ModelNotListedStillProbes documents the safe stage-1
// semantics: absence from /v1/models is advisory (relay-signed requests get
// the "unknown" key type, for which peers fail-close their local models), so
// the token probe still runs and its verdict is authoritative.
func TestPeerCapabilityProbe_ModelNotListedStillProbes(t *testing.T) {
	ps := newProbeTestServer([]string{"unrelated-model"}, http.StatusOK)
	defer ps.close()
	p := newTestProber(t, ps, []string{"m1"})

	res := p.probeClaim(capabilityProbeTarget{nodeID: "peer1", endpoint: ps.srv.URL}, "m1")
	if res.outcome != probeOutcomeSuccess {
		t.Fatalf("expected success (token probe authoritative), got %v (%s)", res.outcome, res.detail)
	}
	if res.listed {
		t.Fatal("expected listed=false")
	}
	if ps.hitCount("token") != 1 {
		t.Fatal("token probe must still run when the model is not listed")
	}
}

// TestPeerCapabilityProbe_TokenProbeStatuses verifies the conservative status
// mapping: 200 success; 429/401/403 inconclusive (never punished);
// other 4xx/5xx failure.
func TestPeerCapabilityProbe_TokenProbeStatuses(t *testing.T) {
	cases := []struct {
		status int
		want   probeOutcome
	}{
		{200, probeOutcomeSuccess},
		{429, probeOutcomeInconclusive}, // peer busy/share-bounded: claim not disproven
		{401, probeOutcomeInconclusive}, // our auth problem, not the peer's lie
		{403, probeOutcomeInconclusive},
		{404, probeOutcomeFailure}, // model not found: claim disproven
		{400, probeOutcomeFailure},
		{500, probeOutcomeFailure},
		{502, probeOutcomeFailure},
	}
	for _, c := range cases {
		ps := newProbeTestServer([]string{"m1"}, c.status)
		p := newTestProber(t, ps, []string{"m1"})
		res := p.probeClaim(capabilityProbeTarget{nodeID: "peer1", endpoint: ps.srv.URL}, "m1")
		ps.close()
		if res.outcome != c.want {
			t.Errorf("HTTP %d: expected outcome %v, got %v (%s)", c.status, c.want, res.outcome, res.detail)
		}
	}
}

// TestPeerCapabilityProbe_ModelsEndpointAuthRejected verifies that a 401/403
// on stage 1 yields inconclusive without running the token probe.
func TestPeerCapabilityProbe_ModelsEndpointAuthRejected(t *testing.T) {
	ps := newProbeTestServerFull([]string{"m1"}, http.StatusForbidden, http.StatusOK)
	defer ps.close()
	p := newTestProber(t, ps, []string{"m1"})

	res := p.probeClaim(capabilityProbeTarget{nodeID: "peer1", endpoint: ps.srv.URL}, "m1")
	if res.outcome != probeOutcomeInconclusive {
		t.Fatalf("expected inconclusive on stage-1 403, got %v", res.outcome)
	}
	if ps.hitCount("token") != 0 {
		t.Fatal("token probe must not run after stage-1 auth rejection")
	}
}

// TestPeerCapabilityProbe_UnreachableNodeFailsFast verifies a transport
// failure on stage 1 is a hard failure without a second request.
func TestPeerCapabilityProbe_UnreachableNodeFailsFast(t *testing.T) {
	p := newPeerCapabilityProber()
	p.signRequest = stubProbeSigner
	// Unroutable address with a short client timeout would still take the
	// full dial timeout; use a closed server for a fast refusal.
	ps := newProbeTestServer([]string{"m1"}, http.StatusOK)
	url := ps.srv.URL
	ps.close()

	res := p.probeClaim(capabilityProbeTarget{nodeID: "peer1", endpoint: url}, "m1")
	if res.outcome != probeOutcomeFailure {
		t.Fatalf("expected failure for unreachable node, got %v", res.outcome)
	}
	if !strings.Contains(res.detail, "unreachable") {
		t.Fatalf("detail should mention unreachability, got %q", res.detail)
	}
}

// TestPeerCapabilityProbe_FalseClaimDemotesToD is the core false-capability
// defense test: repeated hard probe failures must demote the node to grade D
// via the existing EWMA/grading path, and cleanup() must stamp DGradeSince
// so the 7-day ShouldRemoveNode path can fire.
func TestPeerCapabilityProbe_FalseClaimDemotesToD(t *testing.T) {
	ps := newProbeTestServer([]string{"m1"}, http.StatusOK)
	defer ps.close()
	p := newTestProber(t, ps, []string{"m1"})

	failCycle := func() {
		p.recordNodeVerdict("liar-node", []modelProbeOutcome{
			{model: "m1", res: capabilityProbeResult{outcome: probeOutcomeFailure, latencyMS: 5, detail: "token probe HTTP 404"}},
		})
	}

	failCycle()
	if g := repMgr.GetReputation("liar-node").Grade; g == "D" {
		t.Fatal("one failed cycle must not demote straight to D (EWMA needs repetition)")
	}
	failCycle()
	rep := repMgr.GetReputation("liar-node")
	if rep == nil {
		t.Fatal("expected a reputation record")
	}
	if rep.Grade != "D" {
		t.Fatalf("expected grade D after repeated failures, got %s (overall %.2f)", rep.Grade, rep.OverallScore)
	}
	repMgr.cleanup()
	if got := repMgr.GetReputation("liar-node").DGradeSince; got == "" {
		t.Fatal("cleanup() must stamp DGradeSince once the node reaches D")
	}
}

// TestPeerCapabilityProbe_SuccessRecoversGrade verifies an honest node
// recovers from D when probes start succeeding (no permanent exile).
func TestPeerCapabilityProbe_SuccessRecoversGrade(t *testing.T) {
	ps := newProbeTestServer([]string{"m1"}, http.StatusOK)
	defer ps.close()
	p := newTestProber(t, ps, []string{"m1"})

	for i := 0; i < 3; i++ {
		p.recordNodeVerdict("flaky-node", []modelProbeOutcome{
			{model: "m1", res: capabilityProbeResult{outcome: probeOutcomeFailure, latencyMS: 5}},
		})
	}
	if g := repMgr.GetReputation("flaky-node").Grade; g != "D" {
		t.Fatalf("precondition: expected D, got %s", g)
	}
	for i := 0; i < 3; i++ {
		p.recordNodeVerdict("flaky-node", []modelProbeOutcome{
			{model: "m1", res: capabilityProbeResult{outcome: probeOutcomeSuccess, latencyMS: 120}},
		})
	}
	if g := repMgr.GetReputation("flaky-node").Grade; g == "D" {
		t.Fatal("sustained probe success must lift the node out of D")
	}
}

// TestPeerCapabilityProbe_InconclusiveWritesNothing verifies ambiguous probe
// outcomes never touch the reputation record.
func TestPeerCapabilityProbe_InconclusiveWritesNothing(t *testing.T) {
	ps := newProbeTestServer([]string{"m1"}, http.StatusOK)
	defer ps.close()
	p := newTestProber(t, ps, []string{"m1"})

	p.recordNodeVerdict("quiet-node", []modelProbeOutcome{
		{model: "m1", res: capabilityProbeResult{outcome: probeOutcomeInconclusive, detail: "peer rate-limited"}},
	})
	if rep := repMgr.GetReputation("quiet-node"); rep != nil {
		t.Fatalf("inconclusive probes must not create a reputation record, got %+v", rep)
	}
}

// TestPeerCapabilityProbe_DueGatingNoHammer verifies per-(node,model)
// interval gating: an immediate second runDueProbes issues no new HTTP
// requests.
func TestPeerCapabilityProbe_DueGatingNoHammer(t *testing.T) {
	ps := newProbeTestServer([]string{"m1"}, http.StatusOK)
	defer ps.close()
	p := newTestProber(t, ps, []string{"m1"})

	if n := p.runDueProbes(); n != 1 {
		t.Fatalf("first cycle should probe 1 model, probed %d", n)
	}
	if n := p.runDueProbes(); n != 0 {
		t.Fatalf("immediate second cycle must probe nothing (interval gate), probed %d", n)
	}
	if got := ps.hitCount("models") + ps.hitCount("token"); got != 2 {
		t.Fatalf("expected exactly 2 HTTP hits total, got %d", got)
	}
}

// TestPeerCapabilityProbe_RunDueProbesFeedsReputation runs the full cycle
// against a lying peer (token 404) and asserts the failure reaches the
// reputation manager through runDueProbes.
func TestPeerCapabilityProbe_RunDueProbesFeedsReputation(t *testing.T) {
	ps := newProbeTestServer([]string{"m1"}, http.StatusNotFound)
	defer ps.close()
	p := newTestProber(t, ps, []string{"m1"})

	if n := p.runDueProbes(); n != 1 {
		t.Fatalf("expected 1 probe, got %d", n)
	}
	rep := repMgr.GetReputation("peer1")
	if rep == nil {
		t.Fatal("runDueProbes must feed probe failures into the reputation manager")
	}
	if rep.TotalRequests != 1 || rep.FailedRequests != 1 {
		t.Fatalf("expected 1 total / 1 failed request, got %+v", rep)
	}
}

// TestPeerCapabilityProbe_ConcurrentCyclesSafe hammers runDueProbes from
// several goroutines to shake out map races under -race. Each cycle probes
// a distinct model so all proceed concurrently.
func TestPeerCapabilityProbe_ConcurrentCyclesSafe(t *testing.T) {
	ps := newProbeTestServer([]string{"m1", "m2", "m3", "m4"}, http.StatusOK)
	defer ps.close()

	origRep := repMgr
	t.Cleanup(func() { repMgr = origRep })
	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  t.TempDir(),
	}

	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := newPeerCapabilityProber()
			p.httpClient = ps.srv.Client()
			p.signRequest = stubProbeSigner
			model := []string{"m1", "m2", "m3", "m4"}[i]
			p.listTargets = func() []capabilityProbeTarget {
				return []capabilityProbeTarget{{nodeID: "peer1", endpoint: ps.srv.URL, models: []string{model}}}
			}
			// Exercise the shared-index paths concurrently.
			p.seedLastProbe()
			p.mu.Lock()
			delete(p.lastProbe, "peer1|"+model) // force due
			p.mu.Unlock()
			if n := p.runDueProbes(); n != 1 {
				failures.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatal("concurrent probe cycles must each complete their model")
	}
}

// TestTargetsFromTrustPool verifies target enumeration: self/inactive/
// suspended/unreachable/model-less nodes are skipped, claimed models are
// merged and deduplicated, and scheme-less endpoints are fixed up.
func TestTargetsFromTrustPool(t *testing.T) {
	pool := TrustPool{Nodes: []NodeInfo{
		{NodeID: "self", Endpoint: "http://self:8080", Status: "active", SharedModels: []string{"a"}},
		{NodeID: "inactive-node", Endpoint: "http://x:8080", Status: "inactive", SharedModels: []string{"a"}},
		{NodeID: "suspended-node", Endpoint: "http://x:8080", Status: "suspended", SharedModels: []string{"a"}},
		{NodeID: "no-endpoint", Status: "active", SharedModels: []string{"a"}},
		{NodeID: "no-models", Endpoint: "http://x:8080", Status: "active"},
		{
			NodeID:       "good-node",
			Endpoint:     "example.com:8080", // no scheme → fixed up to https
			Status:       "active",
			SharedModels: []string{"m1", "m2", "m1"}, // dup in SharedModels
			SharedProviders: []SharedProvider{
				{ProviderID: "p1", Models: []string{"m2", "m3"}}, // m2 dup across sources
			},
		},
		{NodeID: "addr-node", Addresses: []string{"https://peer.example.com"}, Status: "active", SharedModels: []string{"z"}},
	}}

	targets := targetsFromTrustPool(pool, "self")
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets (good-node, addr-node), got %d", len(targets))
	}
	byID := map[string]capabilityProbeTarget{}
	for _, tg := range targets {
		byID[tg.nodeID] = tg
	}
	good := byID["good-node"]
	if good.endpoint != "https://example.com:8080" {
		t.Fatalf("scheme fixup failed, endpoint=%q", good.endpoint)
	}
	if len(good.models) != 3 || good.models[0] != "m1" || good.models[1] != "m2" || good.models[2] != "m3" {
		t.Fatalf("models should be deduped [m1 m2 m3], got %v", good.models)
	}
	if addr := byID["addr-node"]; addr.endpoint != "https://peer.example.com" {
		t.Fatalf("addresses should be preferred, got %q", addr.endpoint)
	}
}

// TestTargetsFromTrustPool_MaxModelsCap verifies the per-node model cap.
func TestTargetsFromTrustPool_MaxModelsCap(t *testing.T) {
	models := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		models = append(models, "model-"+string(rune('a'+i)))
	}
	pool := TrustPool{Nodes: []NodeInfo{
		{NodeID: "hog", Endpoint: "https://x:8080", Status: "active", SharedModels: models},
	}}
	targets := targetsFromTrustPool(pool, "self")
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if got := len(targets[0].models); got != capabilityProbeMaxModels() {
		t.Fatalf("expected model cap %d, got %d", capabilityProbeMaxModels(), got)
	}
}

// TestTargetsFromTrustPool_SkipsPlaintext verifies P2-3: plaintext http
// targets are skipped (the probe signature would travel sniffable), except
// loopback which stays allowed for tests and local development.
func TestTargetsFromTrustPool_SkipsPlaintext(t *testing.T) {
	pool := TrustPool{Nodes: []NodeInfo{
		{NodeID: "plain-lan", Endpoint: "http://192.0.2.1:8080", Status: "active", SharedModels: []string{"m1"}},
		{NodeID: "plain-dns", Endpoint: "http://peer.example.com", Status: "active", SharedModels: []string{"m1"}},
		{NodeID: "loop-v4", Endpoint: "http://127.0.0.1:8080", Status: "active", SharedModels: []string{"m1"}},
		{NodeID: "loop-name", Endpoint: "http://localhost:8080", Status: "active", SharedModels: []string{"m1"}},
		{NodeID: "tls-node", Endpoint: "https://peer.example.com", Status: "active", SharedModels: []string{"m1"}},
	}}
	targets := targetsFromTrustPool(pool, "self")
	byID := map[string]capabilityProbeTarget{}
	for _, tg := range targets {
		byID[tg.nodeID] = tg
	}
	for _, id := range []string{"plain-lan", "plain-dns"} {
		if _, ok := byID[id]; ok {
			t.Errorf("plaintext non-loopback target %s must be skipped", id)
		}
	}
	for _, id := range []string{"loop-v4", "loop-name", "tls-node"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("target %s must be kept (loopback/https)", id)
		}
	}
}

// TestSignProbeRequestDefault_NilNodeSafe verifies the default signer is a
// no-op (not a panic) when node identity is unavailable, e.g. in tests.
func TestSignProbeRequestDefault_NilNodeSafe(t *testing.T) {
	origNode := node
	t.Cleanup(func() { node = origNode })
	node = nil

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/v1/models", nil)
	signProbeRequestDefault(req, http.MethodGet, "/v1/models", nil) // must not panic
	if req.Header.Get("X-Node-ID") != "" {
		t.Fatal("no identity headers should be set without a node")
	}
}

// TestCapabilityProbeGate verifies the run gate: config toggle, network
// mode, federation enabled.
func TestCapabilityProbeGate(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	origNetMgr, origFed, origRep := netMgr, fed, repMgr
	t.Cleanup(func() {
		netMgr, fed, repMgr = origNetMgr, origFed, origRep
	})

	repMgr = &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  t.TempDir(),
	}
	initFederation(t.TempDir())
	// Mark federation enabled WITHOUT SetEnabled(true): that would start
	// the trust-pool refreshLoop goroutine, which outlives the test and
	// races a later test's setupTestEnv global restore under -race.
	fed.enabled = true
	t.Cleanup(func() { fed.enabled = false })
	netMgr = newTestNetworkManager(t)
	netMgr.config.NetworkEnabled = true

	if !capabilityProbeGateOpen() {
		t.Fatal("gate should be open with network mode + federation + repMgr")
	}

	// Config toggle off → closed.
	cfg.Set(cfgCapabilityProbeEnabled, "false")
	if capabilityProbeGateOpen() {
		t.Fatal("gate must close when capability_probe_enabled=false")
	}
	cfg.Set(cfgCapabilityProbeEnabled, "true")

	// Personal mode (network_enabled=false) → closed.
	netMgr.config.NetworkEnabled = false
	if capabilityProbeGateOpen() {
		t.Fatal("gate must close in personal mode")
	}
	netMgr.config.NetworkEnabled = true

	// Federation disabled → closed.
	fed.enabled = false
	if capabilityProbeGateOpen() {
		t.Fatal("gate must close when federation is disabled")
	}
	fed.enabled = true

	// No reputation manager → closed.
	repMgr = nil
	if capabilityProbeGateOpen() {
		t.Fatal("gate must close without a reputation manager")
	}
}
