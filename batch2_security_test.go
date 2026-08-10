package main

// batch2_security_test.go — regression tests for Batch 2 security fixes:
//   - SEC-P2-9  CSV formula injection neutralization
//   - SEC-P1-7  WAF X-Forwarded-For gated by trustedReverseProxy
//   - SEC-P3-23 handleDirectProbe SSRF guard
//   - SEC-P2-13 heartbeat default-deny when no auth mechanism is configured
//   - PERF-P0-1 CheckShareBoundary lock pairing / correct self NodeID read

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// SEC-P2-9: CSV export must neutralize spreadsheet formulas.
func TestExportContributionsCSV_FormulaInjectionNeutralized(t *testing.T) {
	gl, err := NewGossipLedger("mmx-self")
	if err != nil {
		t.Fatalf("NewGossipLedger: %v", err)
	}

	// Records whose string fields carry spreadsheet formula payloads (as a
	// remote peer could inject via unauthenticated notify claims).
	gl.RecordContribution(&ContributionRecord{
		ID:       "=HYPERLINK(\"http://evil.example\")",
		PeerID:   "+SUM(A1:A9)",
		ModelID:  "@cmd",
		Provider: "-2+3",
		Tokens:   10,
	})
	gl.RecordContribution(&ContributionRecord{
		ID:       "safe-id",
		PeerID:   "mmx-peer",
		ModelID:  "gpt-4",
		Provider: "openai",
		Tokens:   5,
	})

	csvOut, err := gl.ExportContributionsCSV()
	if err != nil {
		t.Fatalf("ExportContributionsCSV: %v", err)
	}
	// Each formula-prefixed cell must be neutralized with a leading single
	// quote (the encoding/csv writer may additionally quote cells containing
	// quotes/commas — we assert the leading-quote neutralization, not the
	// exact quoting form).
	for _, want := range []string{
		`'=HYPERLINK(`,
		`'+SUM(A1:A9)`,
		`'@cmd`,
		`'-2+3`,
	} {
		if !strings.Contains(csvOut, want) {
			t.Errorf("CSV output missing neutralized cell %q\nfull:\n%s", want, csvOut)
		}
	}
	// A safe value must NOT be prefixed.
	if strings.Contains(csvOut, `'safe-id`) {
		t.Errorf("safe cell was unexpectedly prefixed:\n%s", csvOut)
	}
}

// SEC-P1-7: clientIPs must ignore X-Forwarded-For unless OMP_TRUSTED_PROXY=1,
// and parse right-to-left when trusted.
func TestClientIPs_XFFGatedByTrustedProxy(t *testing.T) {
	old := trustedReverseProxy
	t.Cleanup(func() { trustedReverseProxy = old })

	// Not trusted: XFF is ignored entirely; only RemoteAddr counts.
	trustedReverseProxy = false
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.RemoteAddr = "203.0.113.7:9999"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 5.5.5.5")
	r.Header.Set("X-Real-IP", "4.4.4.4")
	ips := clientIPs(r)
	if len(ips) != 1 || ips[0] != "203.0.113.7" {
		t.Fatalf("untrusted proxy: expected only RemoteAddr, got %v", ips)
	}

	// Trusted: XFF parsed right-to-left (rightmost = real client), then X-Real-IP,
	// then RemoteAddr.
	trustedReverseProxy = true
	r2 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r2.RemoteAddr = "127.0.0.1:9999"
	r2.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2, 3.3.3.3")
	ips2 := clientIPs(r2)
	if len(ips2) != 4 || ips2[0] != "3.3.3.3" {
		t.Fatalf("trusted proxy: expected right-to-left XFF (3.3.3.3 first), got %v", ips2)
	}
}

// SEC-P3-23: handleDirectProbe must reject non-http(s) and private targets.
func TestHandleDirectProbe_RejectsPrivateTarget(t *testing.T) {
	// Minimal NATManager so ProbeDirect is not nil-dereferenced.
	orig := natMgr
	natMgr = &NATManager{probeCache: make(map[string]probeResult)}
	t.Cleanup(func() { natMgr = orig })

	cases := []struct {
		name    string
		body    string
		want    int
	}{
		{"ftp_scheme", `{"node_id":"n1","target_url":"ftp://example.com/x"}`, 400},
		{"loopback", `{"node_id":"n1","target_url":"http://127.0.0.1:8000/api/network/status"}`, 400},
		{"private", `{"node_id":"n1","target_url":"http://192.168.1.1:8000/x"}`, 400},
		{"missing", `{"node_id":"n1"}`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/network/__probe", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handleDirectProbe(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("expected %d, got %d (body=%s)", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

// SEC-P2-13: /api/network/heartbeat defaults to DENY when no auth mechanism
// (secret nor federation manager) is configured.
func TestNetworkHeartbeat_DefaultDeny(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	origFed := fed
	fed = nil
	t.Cleanup(func() { fed = origFed })
	cfg.Set("federation_secret", "")

	req := httptest.NewRequest(http.MethodPost, "/api/network/heartbeat", strings.NewReader(`{"node_id":"mmx-peer"}`))
	req.Header.Set("X-Node-ID", "mmx-peer")
	rec := httptest.NewRecorder()
	handleNetworkHeartbeat(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("heartbeat with no auth mechanism must default to 403, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// PERF-P0-1: CheckShareBoundary reads self NodeID directly from config (no
// recursive RLock) and stays correct under concurrent writers.
func TestCheckShareBoundary_LockPairingAndCorrectness(t *testing.T) {
	origLedger := contributionLedger
	gl, err := NewGossipLedger("mmx-self")
	if err != nil {
		t.Fatalf("NewGossipLedger: %v", err)
	}
	contributionLedger = gl
	t.Cleanup(func() { contributionLedger = origLedger })

	origNet := netMgr
	nm := &NetworkManager{config: NetworkConfig{
		NodeID: "mmx-self",
		ShareBoundary: ShareBoundaryConfig{
			DailyContribCap: 100,
			ModelWhitelist:  []string{"gpt-4"},
		},
	}}
	netMgr = nm
	t.Cleanup(func() { netMgr = origNet })

	// Whitelist enforcement.
	if ok, reason := nm.CheckShareBoundary("gpt-3.5", 10); ok {
		t.Fatalf("non-whitelisted model must be rejected, got ok=true reason=%q", reason)
	}
	if ok, _ := nm.CheckShareBoundary("gpt-4", 10); !ok {
		t.Fatalf("whitelisted model must be allowed")
	}

	// Daily cap enforcement: self has contributed 90, +20 exceeds 100.
	gl.RecordContribution(&ContributionRecord{PeerID: "mmx-self", ModelID: "gpt-4", Provider: "x", Tokens: 0})
	gl.AppendTransaction("contribution", "mmx-self", 90, "gpt-4", "")
	if ok, reason := nm.CheckShareBoundary("gpt-4", 20); ok {
		t.Fatalf("cap 100 with 90+20 must be rejected, got ok=true reason=%q", reason)
	}
	if ok, _ := nm.CheckShareBoundary("gpt-4", 5); !ok {
		t.Fatalf("cap 100 with 90+5 must be allowed")
	}

	// Concurrent writers must not deadlock (would hang the test on the old
	// recursive-RLock + queued-writer pattern).
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nm.UpdateShareBoundary(&ShareBoundaryConfig{
				DailyContribCap: 100,
				ModelWhitelist:  []string{"gpt-4"},
			})
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = nm.CheckShareBoundary("gpt-4", 5)
		}()
	}
	wg.Wait()
}
