package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================
// B10-V1: public quota settlement (reserve -> adjust with real usage)
// ============================================================

func TestP10_PublicQuota_Settlement(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  100000,
		IPDailyLimit:      50000,
		HourlyWindowLimit: 20000,
		ModelLimits:       map[string]int64{"gpt-4o": 30000},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        make(map[string]int64),
	}

	ok, reason, _ := q.ReserveQuota("1.2.3.4", "gpt-4o", 5000)
	if !ok {
		t.Fatalf("reserve failed: %s", reason)
	}
	if q.globalUsedToday != 5000 {
		t.Fatalf("after reserve: globalUsedToday = %d, want 5000", q.globalUsedToday)
	}

	// Refund path: actual usage smaller than the estimate.
	q.AdjustQuota("1.2.3.4", "gpt-4o", 5000, 1200)
	if q.globalUsedToday != 1200 {
		t.Fatalf("after refund adjust: globalUsedToday = %d, want 1200", q.globalUsedToday)
	}
	if tr := q.ipUsage["1.2.3.4"]; tr.DailyUsed != 1200 {
		t.Fatalf("ip daily used = %d, want 1200", tr.DailyUsed)
	}
	if q.modelUsage["gpt-4o"] != 1200 {
		t.Fatalf("model usage = %d, want 1200", q.modelUsage["gpt-4o"])
	}

	// Charge path: a second request uses more than reserved.
	ok, _, _ = q.ReserveQuota("1.2.3.4", "gpt-4o", 100)
	if !ok {
		t.Fatal("second reserve failed")
	}
	q.AdjustQuota("1.2.3.4", "gpt-4o", 100, 800)
	want := int64(1200 + 800)
	if q.globalUsedToday != want {
		t.Fatalf("after charge adjust: globalUsedToday = %d, want %d", q.globalUsedToday, want)
	}
}

func TestP10_PQHandoff_SettleSemantics(t *testing.T) {
	pq := &pqHandoff{clientIP: "9.8.7.6", model: "m1", reserved: 400, actual: 400}
	if pq.settled {
		t.Fatal("fresh handoff must not be settled")
	}
	pq.settle(150)
	if !pq.settled || pq.actual != 150 {
		t.Fatalf("settle(150): settled=%v actual=%d, want true/150", pq.settled, pq.actual)
	}
	// Double settle must be ignored - the first settlement is authoritative.
	pq.settle(99999)
	if pq.actual != 150 {
		t.Fatalf("double settle changed actual to %d, want 150", pq.actual)
	}

	// Context round-trip.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if pqHandoffFromContext(r.Context()) != nil {
		t.Fatal("request without handoff must return nil")
	}
	r = withPQHandoff(r, pq)
	got := pqHandoffFromContext(r.Context())
	if got != pq {
		t.Fatal("context round-trip lost the handoff pointer")
	}
}

// ============================================================
// B10-V2: CalculateNodeGlobalPoolShare no longer deadlocks
// ============================================================

func TestP10_GlobalPoolShare_ConcurrentNoDeadlock(t *testing.T) {
	orig := globalPool
	defer func() { globalPool = orig }()

	globalPool = &GlobalPool{
		TotalContributed:  100000,
		TotalConsumed:     500,
		AvailableQuota:    80000,
		NodeContributions: map[string]int64{"node-1": 60000},
		NodeConsumptions:  map[string]int64{},
		ParticipantNodes:  []GlobalPoolNode{},
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // writer contention - queued writers between the two RLock
		// acquisitions were the deadlock trigger before the fix.
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			select {
			case <-stop:
				return
			default:
			}
			globalPool.mu.Lock()
			globalPool.AvailableQuota += 1
			globalPool.mu.Unlock()
			time.Sleep(time.Microsecond)
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			share := CalculateNodeGlobalPoolShare("node-1")
			if share <= 0 {
				t.Errorf("share = %d, want > 0", share)
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(stop)
		t.Fatal("CalculateNodeGlobalPoolShare deadlocked under writer contention")
	}
	close(stop)
	wg.Wait()
}

// ============================================================
// B10-V3: Gemini API key in header, not URL query
// ============================================================

func TestP10_GeminiKeyInHeader(t *testing.T) {
	var gotQuery, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotKey = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":5,"totalTokenCount":8}}`)
	}))
	defer srv.Close()

	p := Provider{ID: "gem-test", BaseURL: srv.URL, APIKey: "secret-gemini-key"}
	resp, err := geminiNonStream(context.Background(), p, "gemini-2.0-flash",
		[]ChatMessage{{Role: "user", Content: "hello"}}, map[string]any{})
	if err != nil {
		t.Fatalf("geminiNonStream: %v", err)
	}
	if gotQuery != "" {
		t.Fatalf("URL query = %q, want empty (key must not leak into URL)", gotQuery)
	}
	if gotKey != "secret-gemini-key" {
		t.Fatalf("x-goog-api-key header = %q, want secret-gemini-key", gotKey)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

// ============================================================
// B10-V4: browser-login endpoints enforce provider ownership
// ============================================================

func TestP10_BrowserLogin_OwnershipEnforced(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	pm.Add(Provider{ID: "bl-own", Name: "BL", BaseURL: "https://bl.example.com", Owner: "c1"})

	for _, h := range []http.HandlerFunc{handleBrowserLoginStatus, handleBrowserLoginCancel} {
		// Wrong owner: indistinguishable 404 "not found" - no state leak.
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/providers/bl-own/browser-login/status", nil)
		r.SetPathValue("id", "bl-own")
		r.Header.Set("X-Request-Owner", "c2")
		h(w, r)
		if w.Code != 404 || !strings.Contains(w.Body.String(), "not found") {
			t.Fatalf("wrong owner: got %d %q, want 404 not-found", w.Code, w.Body.String())
		}

		// Correct owner passes the ownership gate and reaches session lookup
		// (which reports no active session - a different error).
		w = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetPathValue("id", "bl-own")
		r.Header.Set("X-Request-Owner", "c1")
		h(w, r)
		if w.Code == 404 && strings.Contains(w.Body.String(), "not found") {
			t.Fatalf("correct owner was wrongly rejected by ownership check: %q", w.Body.String())
		}
	}
}

// ============================================================
// B10-V5: rate limiter keys buckets on rightmost XFF entry
// ============================================================

func TestP10_RateLimit_RightmostXFF(t *testing.T) {
	oldTrusted := trustedReverseProxy
	trustedReverseProxy = true
	defer func() { trustedReverseProxy = oldTrusted }()

	handler := rateLimitByIP(1, "p10_v5_test")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	doReq := func(remoteAddr, xff string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		r.RemoteAddr = remoteAddr
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Code
	}

	// Same proxy RemoteAddr, same real client (rightmost), spoofed leftmost
	// varies - before the fix each fake leftmost IP bought a fresh bucket.
	if code := doReq("10.0.0.1:1111", "fake-a, 5.6.7.8"); code != 200 {
		t.Fatalf("first request: %d, want 200", code)
	}
	if code := doReq("10.0.0.1:1111", "fake-b, 5.6.7.8"); code != 429 {
		t.Fatalf("same rightmost client with rotated leftmost: %d, want 429", code)
	}
	if code := doReq("10.0.0.1:1111", "fake-c, 9.9.9.9"); code != 200 {
		t.Fatalf("different rightmost client: %d, want 200", code)
	}
}

// ============================================================
// B10-U2: dead guest-key routes are registered
// ============================================================

func TestP10_GuestKeyRoutesRegistered(t *testing.T) {
	mux := setupRoutes()

	cases := []struct{ method, path string }{
		{http.MethodDelete, "/api/network/guest-keys/somekey/permanent"},
		{http.MethodPost, "/api/network/guest-keys/somekey/mark-collaborator"},
		{http.MethodPost, "/api/network/guest-keys/somekey/share-type"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		// Unauthenticated -> auth middleware rejects with 401. A mux miss
		// would be 404/405 - exactly what users saw before these routes
		// existed.
		if w.Code == 404 || w.Code == 405 {
			t.Fatalf("%s %s: route missing (got %d)", tc.method, tc.path, w.Code)
		}
	}
}

// ============================================================
// B10-U3: anthropic streaming propagates real usage
// ============================================================

func TestP10_AnthropicStream_UsagePassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	iw := &anthropicResponseWriter{
		realWriter:  rec,
		header:      make(http.Header),
		statusCode:  200,
		isStreaming: true,
		model:       "test-model",
	}

	chunks := []string{
		`data: {"choices":[{"delta":{"content":"Hel"}}]}`,
		`data: {"choices":[{"delta":{"content":"lo"}}],"usage":{"prompt_tokens":11,"completion_tokens":42}}`,
		"data: [DONE]",
	}
	for _, c := range chunks {
		if _, err := iw.writeStreaming([]byte(c + "\n\n")); err != nil {
			t.Fatalf("writeStreaming: %v", err)
		}
	}

	out := rec.Body.String()
	var delta struct {
		Type  string `json:"type"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	foundDelta := false
	for _, ev := range strings.Split(out, "\n\n") {
		ev = strings.TrimSpace(ev)
		if ev == "" || !strings.HasPrefix(ev, "event: message_delta") {
			continue
		}
		for _, line := range strings.Split(ev, "\n") {
			if strings.HasPrefix(line, "data: ") {
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &delta); err != nil {
					t.Fatalf("decode message_delta: %v (%s)", err, line)
				}
				foundDelta = true
			}
		}
	}
	if !foundDelta {
		t.Fatalf("no message_delta event in output:\n%s", out)
	}
	if delta.Usage.OutputTokens != 42 {
		t.Fatalf("message_delta output_tokens = %d, want 42", delta.Usage.OutputTokens)
	}
}

// ============================================================
// B10-P1: DNS result cache
// ============================================================

func TestP10_DNSCache(t *testing.T) {
	// Restore the real guard: TestMain globally disables the private-host
	// check so loopback httptest servers work; these assertions need it on.
	oldAllow := allowLocalProviderForTest
	allowLocalProviderForTest = false
	defer func() { allowLocalProviderForTest = oldAllow }()

	dnsCacheMu.Lock()
	dnsCache = make(map[string]dnsCacheEntry)
	dnsCacheMu.Unlock()

	if !cachedIsPrivateHost("") {
		t.Fatal("empty host must fail closed")
	}
	if !cachedIsPrivateHost("127.0.0.1") {
		t.Fatal("loopback IP must be private")
	}
	if cachedIsPrivateHost("203.0.113.77") {
		t.Fatal("public TEST-NET-3 IP must not be private")
	}

	// Cache hit path: seed an entry and verify it is served without lookup.
	dnsCacheMu.Lock()
	dnsCache["cache-hit.example.invalid"] = dnsCacheEntry{private: false, expires: time.Now().Add(time.Minute)}
	dnsCacheMu.Unlock()
	if cachedIsPrivateHost("cache-hit.example.invalid") {
		t.Fatal("seeded cache entry should short-circuit to false")
	}

	// Expiry: expired entries fall through to a fresh resolution.
	// .invalid never resolves, so the fresh lookup must fail closed.
	dnsCacheMu.Lock()
	dnsCache["expired.example.invalid"] = dnsCacheEntry{private: false, expires: time.Now().Add(-time.Minute)}
	dnsCacheMu.Unlock()
	if !cachedIsPrivateHost("expired.example.invalid") {
		t.Fatal("expired entry must be re-resolved (fail-closed for unresolvable host)")
	}
}
