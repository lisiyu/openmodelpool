package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ============================================================
// B9-1: localOnly rejects tunnel-originated requests that present
// a public Cf-Connecting-Ip despite a loopback RemoteAddr
// ============================================================

func TestP9_LocalOnly_TunnelOriginBlocked(t *testing.T) {
	handler := localOnly(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// Loopback RemoteAddr + public CF header → must be blocked.
	req := httptest.NewRequest("GET", "/api/setup/status", nil)
	req.RemoteAddr = "127.0.0.1:55555"
	req.Header.Set("Cf-Connecting-Ip", "203.0.113.7")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != 403 {
		t.Fatalf("public Cf-Connecting-Ip: code = %d, want 403", rec.Code)
	}

	// No CF header → plain loopback still allowed.
	req2 := httptest.NewRequest("GET", "/api/setup/status", nil)
	req2.RemoteAddr = "127.0.0.1:55556"
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("plain loopback: code = %d, want 200", rec2.Code)
	}

	// Spoofed private CF header from a direct LAN client → still allowed
	// (header only disqualifies when it names a public address).
	req3 := httptest.NewRequest("GET", "/api/setup/status", nil)
	req3.RemoteAddr = "192.168.1.20:40000"
	req3.Header.Set("Cf-Connecting-Ip", "10.0.0.9")
	rec3 := httptest.NewRecorder()
	handler(rec3, req3)
	if rec3.Code != 200 {
		t.Fatalf("private Cf-Connecting-Ip from LAN: code = %d, want 200", rec3.Code)
	}
}

// ============================================================
// B9-2: forgot-password returns the same generic response whether
// or not the email matches (SMTP-unconfigured path no longer oracle)
// ============================================================

func TestP9_ForgotPassword_NoEnumeration(t *testing.T) {
	setupTestEnv(t)
	// Initialized admin with a known email but no SMTP configured.
	auth.mu.Lock()
	auth.data.Initialized = true
	auth.data.Admin.Email = "admin@example.com"
	auth.mu.Unlock()

	body := `{"email":"probe@example.com"}`
	req := httptest.NewRequest("POST", "/api/forgot-password", bytes.NewBufferString(body))
	req.RemoteAddr = "127.0.0.1:50001"
	rec := httptest.NewRecorder()
	handleForgotPassword(rec, req)

	// Unknown email → generic success (pre-existing behavior).
	if rec.Code != 200 {
		t.Fatalf("unknown email: code = %d, want 200", rec.Code)
	}
	genericBody := rec.Body.String()

	// Matching admin email but SMTP unconfigured → must now return the same
	// generic success instead of the distinctive 400 error message.
	body2 := `{"email":"admin@example.com"}`
	req2 := httptest.NewRequest("POST", "/api/forgot-password", bytes.NewBufferString(body2))
	req2.RemoteAddr = "127.0.0.1:50002"
	rec2 := httptest.NewRecorder()
	handleForgotPassword(rec2, req2)

	if rec2.Code != 200 {
		t.Fatalf("matching email w/o SMTP: code = %d, want 200 (was an enumeration oracle)", rec2.Code)
	}
	if rec2.Body.String() != genericBody {
		t.Fatalf("responses differ between known/unknown email: %q vs %q",
			rec2.Body.String(), genericBody)
	}
}

// ============================================================
// B9-3: ResetPasswordWithCode enforces full password policy and
// does not consume the code when validation fails
// ============================================================

func TestP9_ResetWithCode_PolicyEnforced(t *testing.T) {
	a := newTestAuth(t)
	code, _, err := a.GenerateResetCode()
	if err != nil {
		t.Fatalf("GenerateResetCode: %v", err)
	}

	// Weak password (8 chars, single class) — old handler accepted this.
	if err := a.ResetPasswordWithCode(code, "password"); err == nil {
		t.Fatal("weak password accepted by ResetPasswordWithCode")
	}

	// Code must NOT have been consumed by the failed attempt.
	if !a.HasResetCode() {
		t.Fatal("reset code was consumed despite rejected password")
	}

	// Strong password succeeds and consumes the code.
	strong := "Str0ng!Passphrase"
	if err := a.ResetPasswordWithCode(code, strong); err != nil {
		t.Fatalf("strong password rejected: %v", err)
	}
	if a.HasResetCode() {
		t.Fatal("reset code should be consumed after successful reset")
	}
	if !a.VerifyCredentials(a.data.Admin.Username, strong) {
		t.Fatal("new password does not authenticate")
	}
}

// ============================================================
// B9-5: AlgorithmChain params are race-safe under concurrent
// readers and writers
// ============================================================

func TestP9_AlgorithmChain_ConcurrentAccess(t *testing.T) {
	c := NewAlgorithmChain()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		p := DefaultAlgorithmParams()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			p.OpenKeyRatio = float64(i%100) / 100.0
			c.UpdateParams(p)
		}
	}()

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				_ = c.GetCurrentParams().OpenKeyRatio
			}
		}()
	}
	deadline := time.After(300 * time.Millisecond)
	<-deadline
	close(stop)
	wg.Wait()
}

// ============================================================
// B9-6: evictStaleMetrics drops entries for departed nodes
// ============================================================

func TestP9_LoadBalancer_EvictsStaleMetrics(t *testing.T) {
	lb := NewLoadBalancer(DefaultLBConfig())
	lb.recordProbe("gone-node", 10*time.Millisecond, true)
	lb.routeHistory["gone-node"] = 42
	lb.recordProbe("live-node", 10*time.Millisecond, true)

	live := map[string]struct{}{"self": {}, "live-node": {}}
	lb.evictStaleMetrics(live)

	if _, ok := lb.nodeMetrics["gone-node"]; ok {
		t.Fatal("stale nodeMetrics entry survived eviction")
	}
	if _, ok := lb.routeHistory["gone-node"]; ok {
		t.Fatal("stale routeHistory entry survived eviction")
	}
	if _, ok := lb.nodeMetrics["live-node"]; !ok {
		t.Fatal("live node metrics were wrongly evicted")
	}
}

// ============================================================
// B9-7: cryptoRandBelow stays within range for every draw size
// (uniformity smoke check on the rejection sampler)
// ============================================================

func TestP9_CryptoRandBelow_RangeAndSpread(t *testing.T) {
	var buf [8]byte
	for _, max := range []uint64{2, 3, 7, 64, 1000} {
		counts := make([]int, max)
		for i := 0; i < 7000*int(max); i++ {
			v := cryptoRandBelow(max, &buf)
			if v < 0 || v >= int(max) {
				t.Fatalf("cryptoRandBelow(%d) out of range: %d", max, v)
			}
			counts[v]++
		}
		lo := 7000 / 4
		hi := 7000 * 4
		for b, c := range counts {
			if c < lo || c > hi {
				t.Fatalf("bucket %d/%d count %d outside [%d,%d] — biased?", b, max, c, lo, hi)
			}
		}
	}
}

// cryptoShuffle keeps length and multiset identity (existing guarantee).
func TestP9_CryptoShuffle_Permutation(t *testing.T) {
	nodes := make([]NodeInfo, 50)
	for i := range nodes {
		nodes[i].NodeID = fmt.Sprintf("node-%02d", i)
	}
	cryptoShuffle(nodes)
	if len(nodes) != 50 {
		t.Fatalf("length changed: %d", len(nodes))
	}
	seen := make(map[string]bool)
	for _, n := range nodes {
		if seen[n.NodeID] {
			t.Fatalf("duplicate element after shuffle: %s", n.NodeID)
		}
		seen[n.NodeID] = true
	}
	for i := range nodes {
		if !seen[fmt.Sprintf("node-%02d", i)] {
			t.Fatalf("missing element after shuffle: node-%02d", i)
		}
	}
}

// ============================================================
// B9-8: Contribute still persists after moving doSave out of the
// critical section
// ============================================================

func TestP9_GlobalPool_ContributePersists(t *testing.T) {
	dir := t.TempDir()
	gp := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		dataPath:          filepath.Join(dir, "global_pool.json"),
	}
	if err := gp.JoinPool("node-x", "region-1", 10000); err != nil {
		t.Fatalf("JoinPool: %v", err)
	}
	if err := gp.Contribute("node-x", 1234); err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	// JoinPool's initial 10000 plus the explicit contribution.
	if got := gp.NodeContributions["node-x"]; got != 11234 {
		t.Fatalf("contribution = %d, want 11234", got)
	}
	// Reload from disk to prove the save ran after unlocking.
	gp2 := &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		dataPath:          filepath.Join(dir, "global_pool.json"),
	}
	gp2.load()
	if got := gp2.NodeContributions["node-x"]; got != 11234 {
		t.Fatalf("persisted contribution = %d, want 11234", got)
	}
}

// ============================================================
// B9-9: RunBalanceCycle completes without deadlock/race after the
// lock-scope fix (smoke)
// ============================================================

func TestP9_BalanceCycle_Smoke(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		config:      DefaultBalanceConfig(),
		adjustments: make(map[string]*BalanceAdjustment),
		history:     make([]BalanceHistory, 0, 100),
	}
	be.RecordContributionBalance("n1", 1000)
	be.RecordConsumptionBalance("n1", 200)
	done := make(chan struct{})
	go func() {
		defer close(done)
		be.RunBalanceCycle(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunBalanceCycle deadlocked")
	}
}
