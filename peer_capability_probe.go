package main

// peer_capability_probe.go — Phase 2: capability-claim probe verification /
// false-capability defense.
//
// PRD-phase1.md §Q5 left this for Phase 2: "虚假能力防御与探测请求留 Phase 2
// （配合声誉系统）". The existing reputation system (reputation.go, EWMA
// S/A/B/C/D grades) had no production feed — nothing called RecordCall /
// RecordAccuracy — and the ledger-driven CapabilityVerifier probed with an
// X-OMP-NodeID header that withProxyAuth does not recognize as federation
// auth, so probes against real peers could never authenticate.
//
// This subsystem closes both gaps for federation trust-pool nodes:
//
//  1. It enumerates each trust-pool node's declared capabilities
//     (NodeInfo.SharedModels + SharedProviders[].Models) and periodically
//     probes them with a two-stage check:
//     stage 1 — GET /v1/models existence check (cheap, burns no tokens);
//     stage 2 — POST /v1/chat/completions with max_tokens: 1 (authoritative,
//     minimal-token probe).
//  2. Probe results feed the existing reputation EWMA via
//     repMgr.RecordCall (availability/latency) and repMgr.RecordAccuracy
//     (a false capability claim is an *accuracy* failure). Repeated
//     failures demote the node to grade D; repMgr.cleanup() maintains
//     DGradeSince so the existing ShouldRemoveNode 7-day removal path can
//     fire. Routing already weights by OverallScore (network_global_pool.go,
//     network_loadbalancer.go), so a demoted node sinks out of candidacy.
//
// Auth: probes authenticate as signed relay-forwards using the established
// signRelayForward + attachRelayAuth mechanism (the same one
// gatewayForwardToRemote/relayToRemote use), so they pass the peer's
// withProxyAuth like any relayed request. They consume only the peer's
// provider capacity for 1 token — never local quota, never consumer keys
// (consumer Authorization headers are never attached).
//
// Conservatism (no hammering):
//   - per-(node,model) due gating reuses probeSchedule (reputation-aware:
//     suspicious 1m, default 30m, high-rep 2h);
//   - bounded fan-out (default 4 concurrent), bounded models per node per
//     cycle (default 5), short timeouts (10s / 15s);
//   - 401/403/429 probe responses are INCONCLUSIVE (our auth problem or the
//     peer is busy — a claim not disproven), never failures;
//   - stage-1 "model not listed" is advisory only: relay-signed requests get
//     the "unknown" key type, for which AllModelsFiltered fail-closes the
//     peer's local models, so absence from /v1/models cannot prove a false
//     claim. The token probe is authoritative.
//
// Gating: the loop self-checks every tick and only probes when all of
//   capability_probe_enabled=true (config, default true),
//   NetworkManager network_enabled (network mode),
//   fed.IsEnabled(), and repMgr != nil.
// Personal mode therefore never emits probes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config keys for the capability prober. All are plain cfg strings with
// conservative defaults; e.g. CAPABILITY_PROBE_ENABLED env also works via
// the cfg env fallback.
const (
	cfgCapabilityProbeEnabled   = "capability_probe_enabled"     // "true"/"false", default "true"
	cfgCapabilityProbeTick      = "capability_probe_tick"        // loop tick duration, default "1m"
	cfgCapabilityProbeMaxModels = "capability_probe_max_models"  // models probed per node per cycle, default "5"
	cfgCapabilityProbeMaxConc   = "capability_probe_concurrency" // max concurrent probe HTTP requests, default "4"
)

const (
	probeModelsTimeout = 10 * time.Second // stage-1 GET /v1/models deadline
	probeTokenTimeout  = 15 * time.Second // stage-2 token probe deadline
	probeMaxBody       = 1 << 20          // 1MB cap on probe response reads
)

// probeOutcome is the verdict of one (node, model) probe.
type probeOutcome int

const (
	probeOutcomeSuccess probeOutcome = iota
	probeOutcomeFailure
	probeOutcomeInconclusive
)

// capabilityProbeResult is the outcome of probing one claimed model.
type capabilityProbeResult struct {
	outcome   probeOutcome
	listed    bool    // stage-1: model appeared in the peer's /v1/models
	latencyMS float64 // stage-2 latency (0 when stage 2 did not run)
	detail    string  // human-readable reason, for logs/tests
}

// capabilityProbeTarget is one trust-pool node plus its claimed models.
type capabilityProbeTarget struct {
	nodeID   string
	endpoint string // base URL, no trailing slash
	models   []string
}

// modelProbeOutcome pairs a model with its probe result for aggregation.
type modelProbeOutcome struct {
	model string
	res   capabilityProbeResult
}

// PeerCapabilityProber periodically verifies trust-pool nodes' declared
// capabilities and feeds the results into the reputation system.
type PeerCapabilityProber struct {
	mu         sync.Mutex
	lastProbe  map[string]time.Time // "nodeID|model" -> completion time of last probe
	consecFail map[string]int       // "nodeID|model" -> consecutive hard failures

	httpClient  *http.Client                                              // nil → GetSharedHTTPClient()
	signRequest func(req *http.Request, method, path string, body []byte) // nil → relay-forward signing
	listTargets func() []capabilityProbeTarget                            // nil → trust-pool enumeration
}

// peerCapabilityProber is the process-wide instance, started once from
// initContributionLedger.
var peerCapabilityProber *PeerCapabilityProber

func newPeerCapabilityProber() *PeerCapabilityProber {
	return &PeerCapabilityProber{
		lastProbe:  make(map[string]time.Time),
		consecFail: make(map[string]int),
	}
}

// startPeerCapabilityProber constructs the prober and starts its loop. The
// loop self-gates on network mode + config every tick, so it is safe to
// start unconditionally here; in personal mode it stays dormant.
func startPeerCapabilityProber() {
	if peerCapabilityProber != nil {
		return
	}
	peerCapabilityProber = newPeerCapabilityProber()
	goSafe("peer-capability-prober", peerCapabilityProber.loop)
	slog.Info("peer capability prober initialized",
		"enabled", probeCfgGet(cfgCapabilityProbeEnabled, "true"),
		"tick", capabilityProbeTickInterval(),
		"max_models_per_node", capabilityProbeMaxModels(),
		"max_concurrency", capabilityProbeMaxConcurrency())
}

// probeCfgGet reads a capability-prober config key, tolerating a nil cfg
// (falls back to the default).
func probeCfgGet(key, def string) string {
	if cfg == nil {
		return def
	}
	return cfg.Get(key, def)
}

func capabilityProbeTickInterval() time.Duration {
	if d, err := time.ParseDuration(probeCfgGet(cfgCapabilityProbeTick, "1m")); err == nil && d >= 10*time.Second {
		return d
	}
	return time.Minute
}

func capabilityProbeMaxModels() int {
	if n, err := strconv.Atoi(probeCfgGet(cfgCapabilityProbeMaxModels, "5")); err == nil && n >= 1 && n <= 50 {
		return n
	}
	return 5
}

func capabilityProbeMaxConcurrency() int {
	if n, err := strconv.Atoi(probeCfgGet(cfgCapabilityProbeMaxConc, "4")); err == nil && n >= 1 && n <= 32 {
		return n
	}
	return 4
}

// capabilityProbeGateOpen reports whether probing may run right now: the
// config toggle, network mode (network_enabled), federation enabled, and a
// reputation manager to record into must all be present.
func capabilityProbeGateOpen() bool {
	if probeCfgGet(cfgCapabilityProbeEnabled, "true") != "true" {
		return false
	}
	if netMgr == nil || !netMgr.IsNetworkEnabled() {
		return false
	}
	if fed == nil || !fed.IsEnabled() {
		return false
	}
	if repMgr == nil {
		return false
	}
	return true
}

// loop is the scheduler, modeled on HealthChecker.run: tick, gate-check,
// probe what is due, then refresh reputation grades.
func (p *PeerCapabilityProber) loop() {
	ticker := time.NewTicker(capabilityProbeTickInterval())
	defer ticker.Stop()
	seeded := false
	for {
		select {
		case <-globalStopCh:
			slog.Info("peer capability prober stopped")
			return
		case <-ticker.C:
			if !capabilityProbeGateOpen() {
				continue
			}
			if !seeded {
				// Conservative start: do not burst-probe every known claim
				// at boot. Seed the last-probe index so the first real
				// probes happen after one full per-node interval.
				p.seedLastProbe()
				seeded = true
				continue
			}
			if n := p.runDueProbes(); n > 0 {
				// Refresh grades and maintain DGradeSince (§8.4) so
				// sustained probe failures actually reach grade D and the
				// existing ShouldRemoveNode 7-day removal path can fire.
				repMgr.cleanup()
			}
		}
	}
}

// seedLastProbe marks every currently-known (node,model) as just probed so
// the loop's first tick does not hammer peers at startup.
func (p *PeerCapabilityProber) seedLastProbe() {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range p.getTargets() {
		for _, m := range t.models {
			key := t.nodeID + "|" + m
			if _, ok := p.lastProbe[key]; !ok {
				p.lastProbe[key] = now
			}
		}
	}
}

func (p *PeerCapabilityProber) getTargets() []capabilityProbeTarget {
	if p.listTargets != nil {
		return p.listTargets()
	}
	return defaultProbeTargets()
}

func (p *PeerCapabilityProber) getHTTPClient() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return GetSharedHTTPClient()
}

func (p *PeerCapabilityProber) sign(req *http.Request, method, path string, body []byte) {
	if p.signRequest != nil {
		p.signRequest(req, method, path, body)
		return
	}
	signProbeRequestDefault(req, method, path, body)
}

// signProbeRequestDefault authenticates an outbound probe as a signed
// relay-forward from this node (signRelayForward + attachRelayAuth), the
// same mechanism gatewayForwardToRemote uses. The peer's withProxyAuth
// admits it without any consumer credential.
func signProbeRequestDefault(req *http.Request, method, path string, body []byte) {
	if node == nil {
		return
	}
	nodeID := node.NodeID()
	if nodeID == "" {
		return
	}
	sig, ts, nonce := signRelayForward(nodeID, method, path, body)
	attachRelayAuth(req, nodeID, sig, ts, nonce)
}

// defaultProbeTargets enumerates trust-pool nodes and their declared models.
func defaultProbeTargets() []capabilityProbeTarget {
	if fed == nil {
		return nil
	}
	selfID := ""
	if node != nil {
		selfID = node.NodeID()
	}
	return targetsFromTrustPool(fed.GetTrustPool(), selfID)
}

// targetsFromTrustPool builds probe targets from a trust pool snapshot.
// Pure function (no globals) so it is unit-testable.
func targetsFromTrustPool(pool TrustPool, selfID string) []capabilityProbeTarget {
	maxModels := capabilityProbeMaxModels()
	var out []capabilityProbeTarget
	for _, n := range pool.Nodes {
		if n.NodeID == "" || n.NodeID == selfID {
			continue
		}
		if n.Status == "inactive" || n.Status == "suspended" {
			continue
		}
		base := pickBestAddress(n.Addresses)
		if base == "" {
			base = n.Endpoint
		}
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base == "" {
			continue
		}
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			// Unknown scheme: default to https. Probing (like relaying)
			// carries a 5-minute-valid signature, so plaintext must never
			// be the silent default for an unknown endpoint.
			base = "https://" + base
		}
		if u, err := url.Parse(base); err != nil || u.Hostname() == "" {
			continue
		} else if u.Scheme != "https" && !isProbeLoopbackTarget(u.Hostname()) {
			// Plaintext probes expose the relay signature to LAN sniffing
			// (replayable within its window). Skip non-loopback http
			// targets; serve https to be probed. Loopback stays allowed
			// for tests and local development (not sniffable off-host).
			slog.Warn("capability probe: skipping plaintext target (serve https to be probed)",
				"node_id", n.NodeID, "endpoint", base)
			continue
		}
		seen := make(map[string]bool)
		var models []string
		addModel := func(m string) {
			m = strings.TrimSpace(m)
			if m == "" || seen[m] || len(models) >= maxModels {
				return
			}
			seen[m] = true
			models = append(models, m)
		}
		for _, m := range n.SharedModels {
			addModel(m)
		}
		for _, sp := range n.SharedProviders {
			for _, m := range sp.Models {
				addModel(m)
			}
		}
		if len(models) == 0 {
			continue
		}
		out = append(out, capabilityProbeTarget{nodeID: n.NodeID, endpoint: base, models: models})
	}
	return out
}

// isProbeLoopbackTarget reports whether host is loopback-only: plaintext
// probes there are acceptable for tests and local development because the
// traffic never leaves the host (not sniffable off-host, unlike LAN).
func isProbeLoopbackTarget(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// runDueProbes probes every (node,model) whose per-node interval has elapsed
// and records one aggregate verdict per node into the reputation system.
// It returns the number of model probes executed.
func (p *PeerCapabilityProber) runDueProbes() int {
	type job struct {
		target capabilityProbeTarget
		model  string
	}
	targets := p.getTargets()
	if len(targets) == 0 {
		return 0
	}

	now := time.Now()
	var jobs []job
	validKeys := make(map[string]bool, len(targets)*4)
	p.mu.Lock()
	for _, t := range targets {
		interval := probeSchedule(t.nodeID) // reputation-aware: suspicious 1m, default 30m, high-rep 2h
		for _, m := range t.models {
			key := t.nodeID + "|" + m
			validKeys[key] = true
			if last, ok := p.lastProbe[key]; ok && now.Sub(last) < interval {
				continue
			}
			jobs = append(jobs, job{target: t, model: m})
		}
	}
	// Bound the index: drop entries for nodes/models no longer targeted.
	for key := range p.lastProbe {
		if !validKeys[key] {
			delete(p.lastProbe, key)
			delete(p.consecFail, key)
		}
	}
	p.mu.Unlock()

	if len(jobs) == 0 {
		return 0
	}

	maxConc := capabilityProbeMaxConcurrency()
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	var resMu sync.Mutex
	byNode := make(map[string][]modelProbeOutcome)

	for _, j := range jobs {
		j := j
		sem <- struct{}{}
		wg.Add(1)
		// goSafe (B6-3): one panicking probe must not kill the scheduler.
		goSafe("capability-probe", func() {
			defer wg.Done()
			defer func() { <-sem }()
			res := p.probeClaim(j.target, j.model)
			p.mu.Lock()
			p.lastProbe[j.target.nodeID+"|"+j.model] = time.Now()
			p.mu.Unlock()
			resMu.Lock()
			byNode[j.target.nodeID] = append(byNode[j.target.nodeID], modelProbeOutcome{model: j.model, res: res})
			resMu.Unlock()
		})
	}
	wg.Wait()

	for nodeID, outcomes := range byNode {
		p.recordNodeVerdict(nodeID, outcomes)
	}
	return len(jobs)
}

// probeClaim runs the two-stage verification for one claimed model:
// stage 1 — GET /v1/models existence check (cheap, burns no tokens);
// stage 2 — POST /v1/chat/completions with max_tokens: 1 (authoritative).
func (p *PeerCapabilityProber) probeClaim(t capabilityProbeTarget, modelID string) capabilityProbeResult {
	listed, proceed, err := p.checkModelsListed(t.endpoint, modelID)
	if err != nil {
		// Transport-level failure: the node is unreachable. Fail fast
		// without burning a second request.
		return capabilityProbeResult{outcome: probeOutcomeFailure, detail: "models check unreachable: " + err.Error()}
	}
	if !proceed {
		// Auth rejected (401/403): our trust-pool membership may not have
		// propagated yet — our problem, not proof of a false claim.
		return capabilityProbeResult{outcome: probeOutcomeInconclusive, detail: "models check auth rejected"}
	}
	res := p.probeTokenCompletion(t.endpoint, modelID)
	res.listed = listed
	if !listed {
		// Advisory only (see file header): relay-signed requests get the
		// "unknown" key type, for which the peer's /v1/models fail-closes
		// its local models — absence cannot prove a false claim.
		slog.Debug("capability probe: claimed model not listed in peer /v1/models (advisory)",
			"node", t.nodeID, "model", modelID)
	}
	return res
}

// checkModelsListed performs stage 1. It returns (listed, proceed, err):
//   - err != nil: transport failure — the node is unreachable.
//   - proceed == false: the endpoint answered 401/403 — inconclusive.
//   - otherwise proceed == true and listed reports whether the model was
//     advertised (advisory only, see probeClaim).
func (p *PeerCapabilityProber) checkModelsListed(baseURL, modelID string) (listed bool, proceed bool, err error) {
	const path = "/v1/models"
	ctx, cancel := context.WithTimeout(context.Background(), probeModelsTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return false, false, err
	}
	p.sign(req, http.MethodGet, path, nil)

	resp, err := p.getHTTPClient().Do(req)
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Parse below.
	case http.StatusUnauthorized, http.StatusForbidden:
		io.Copy(io.Discard, io.LimitReader(resp.Body, probeMaxBody))
		return false, false, nil
	default:
		// 404 (no models endpoint), 5xx, ... — let the token probe decide.
		io.Copy(io.Discard, io.LimitReader(resp.Body, probeMaxBody))
		return false, true, nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, probeMaxBody))
	if err != nil {
		return false, true, nil
	}
	var ml struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &ml); err != nil {
		return false, true, nil
	}
	for _, m := range ml.Data {
		if m.ID == modelID {
			return true, true, nil
		}
	}
	return false, true, nil
}

// probeTokenCompletion performs stage 2: a minimal max_tokens:1 completion.
// The verdict mapping is deliberately conservative:
//   - 200 → success (capability verified);
//   - 429 → inconclusive (peer busy/share-bounded; claim not disproven);
//   - 401/403 → inconclusive (probe auth problem on our side);
//   - anything else (incl. transport errors) → failure.
func (p *PeerCapabilityProber) probeTokenCompletion(baseURL, modelID string) capabilityProbeResult {
	const path = "/v1/chat/completions"
	body, _ := json.Marshal(map[string]any{
		"model":      modelID,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 1,
		"stream":     false,
	})

	ctx, cancel := context.WithTimeout(context.Background(), probeTokenTimeout)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return capabilityProbeResult{outcome: probeOutcomeFailure, detail: "probe request build failed: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	p.sign(req, http.MethodPost, path, body)

	resp, err := p.getHTTPClient().Do(req)
	latencyMS := float64(time.Since(start).Milliseconds())
	if err != nil {
		return capabilityProbeResult{outcome: probeOutcomeFailure, latencyMS: latencyMS,
			detail: "probe request failed: " + err.Error()}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, probeMaxBody))

	switch {
	case resp.StatusCode == http.StatusOK:
		return capabilityProbeResult{outcome: probeOutcomeSuccess, latencyMS: latencyMS}
	case resp.StatusCode == http.StatusTooManyRequests:
		return capabilityProbeResult{outcome: probeOutcomeInconclusive, latencyMS: latencyMS,
			detail: "peer rate-limited (claim not disproven)"}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return capabilityProbeResult{outcome: probeOutcomeInconclusive, latencyMS: latencyMS,
			detail: fmt.Sprintf("probe auth rejected (HTTP %d)", resp.StatusCode)}
	default:
		return capabilityProbeResult{outcome: probeOutcomeFailure, latencyMS: latencyMS,
			detail: fmt.Sprintf("token probe HTTP %d", resp.StatusCode)}
	}
}

// recordNodeVerdict aggregates one cycle's per-model outcomes for a node and
// writes a single verdict into the reputation EWMA:
//
//   - any hard failure → RecordCall(false) + RecordAccuracy(false). A false
//     capability claim is an accuracy failure; with α=0.3 two consecutive
//     all-fail cycles drive OverallScore below the D threshold (40).
//   - all success → RecordCall(true) + RecordAccuracy(true).
//   - all inconclusive → no write (never punish on ambiguous signals).
func (p *PeerCapabilityProber) recordNodeVerdict(nodeID string, outcomes []modelProbeOutcome) {
	if repMgr == nil || len(outcomes) == 0 {
		return
	}
	var failed, succeeded []string
	var totalLatency float64
	for _, o := range outcomes {
		switch o.res.outcome {
		case probeOutcomeFailure:
			failed = append(failed, o.model)
		case probeOutcomeSuccess:
			succeeded = append(succeeded, o.model)
		}
		totalLatency += o.res.latencyMS
	}
	avgLatency := totalLatency / float64(len(outcomes))

	p.mu.Lock()
	for _, o := range outcomes {
		key := nodeID + "|" + o.model
		if o.res.outcome == probeOutcomeFailure {
			p.consecFail[key]++
			if p.consecFail[key] == 3 {
				slog.Warn("capability probe: suspected false capability claim",
					"node", nodeID, "model", o.model,
					"detail", o.res.detail)
			}
		} else {
			delete(p.consecFail, key)
		}
	}
	p.mu.Unlock()

	switch {
	case len(failed) > 0:
		repMgr.RecordCall(nodeID, false, avgLatency)
		repMgr.RecordAccuracy(nodeID, false)
		slog.Warn("capability probe: claim verification failed",
			"node", nodeID, "failed_models", failed, "succeeded_models", succeeded)
	case len(succeeded) > 0:
		repMgr.RecordCall(nodeID, true, avgLatency)
		repMgr.RecordAccuracy(nodeID, true)
		slog.Debug("capability probe: claim verified",
			"node", nodeID, "models", succeeded, "latency_ms", avgLatency)
	default:
		slog.Debug("capability probe: all inconclusive, no reputation write",
			"node", nodeID, "models_probed", len(outcomes))
	}

	if rep := repMgr.GetReputation(nodeID); rep != nil && rep.Grade == "D" {
		slog.Warn("capability probe: node at grade D (excluded from candidacy by score; removal after 7-day D window)",
			"node", nodeID, "overall_score", rep.OverallScore,
			"d_grade_since", rep.DGradeSince)
	}
}
