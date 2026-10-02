package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// v4.5.57 share-centre quota audit tests
// ============================================================

// TestGuestQuotaTracker_Windows exercises the four per-key enforcement
// dimensions of CheckAndReserveFull: daily, hourly, per-request and RPM, plus
// reservation settlement via Adjust and the minute-window roll.
func TestGuestQuotaTracker_Windows(t *testing.T) {
	// Daily window.
	tr := &guestKeyUsageTracker{}
	if ok, _ := tr.CheckAndReserveFull("k", 100, 0, 0, 0, 80); !ok {
		t.Fatal("daily: first 80t reservation should be allowed")
	}
	if ok, _ := tr.CheckAndReserveFull("k", 100, 0, 0, 0, 40); ok {
		t.Fatal("daily: second 40t reservation must be denied (remaining 20)")
	}
	if got := tr.GetUsage("k"); got != 80 {
		t.Fatalf("daily: usage expected 80, got %d", got)
	}

	// Hourly window (independent tracker).
	trH := &guestKeyUsageTracker{}
	if ok, _ := trH.CheckAndReserveFull("k", 0, 60, 0, 0, 50); !ok {
		t.Fatal("hourly: first 50t reservation should be allowed")
	}
	if ok, _ := trH.CheckAndReserveFull("k", 0, 60, 0, 0, 20); ok {
		t.Fatal("hourly: second 20t reservation must be denied (remaining 10)")
	}

	// Per-request cap.
	trP := &guestKeyUsageTracker{}
	if ok, _ := trP.CheckAndReserveFull("k", 0, 0, 50, 0, 60); ok {
		t.Fatal("per-request: estimate 60 above cap 50 must be denied")
	}
	if ok, _ := trP.CheckAndReserveFull("k", 0, 0, 50, 0, 40); !ok {
		t.Fatal("per-request: estimate 40 within cap 50 should be allowed")
	}

	// RPM window.
	trR := &guestKeyUsageTracker{}
	for i := 0; i < 3; i++ {
		if ok, _ := trR.CheckAndReserveFull("k", 0, 0, 0, 3, 0); !ok {
			t.Fatalf("rpm: request %d/3 should be allowed", i+1)
		}
	}
	if ok, _ := trR.CheckAndReserveFull("k", 0, 0, 0, 3, 0); ok {
		t.Fatal("rpm: 4th request must be denied")
	}

	// Minute-window roll resets the RPM counter.
	trR.minute = "2000-01-01T00:00"
	if ok, _ := trR.CheckAndReserveFull("k", 0, 0, 0, 3, 0); !ok {
		t.Fatal("rpm: new minute window should reset the counter")
	}
}

// TestGuestQuotaTracker_Settlement verifies the reservation/actual settlement
// flow used by the D-4 deferred Adjust: a partially consumed reservation is
// returned to the daily (and hourly) journal.
func TestGuestQuotaTracker_Settlement(t *testing.T) {
	tr := &guestKeyUsageTracker{}

	if ok, _ := tr.CheckAndReserveFull("k", 100, 100, 0, 0, 80); !ok {
		t.Fatal("expected reservation to be allowed")
	}
	tr.Adjust("k", 80, 10)
	if got := tr.GetUsage("k"); got != 10 {
		t.Fatalf("daily usage after settlement expected 10, got %d", got)
	}

	// Over-consumption (actual > reservation) charges the difference.
	trOver := &guestKeyUsageTracker{}
	if ok, _ := trOver.CheckAndReserveFull("k", 100, 0, 0, 0, 80); !ok {
		t.Fatal("expected reservation to be allowed")
	}
	trOver.Adjust("k", 80, 100)
	if got := trOver.GetUsage("k"); got != 100 {
		t.Fatalf("daily usage after over-consumption expected 100, got %d", got)
	}

	// Full failure refunds to zero.
	tr2 := &guestKeyUsageTracker{usage: map[string]int64{"k": 80}}
	tr2.Adjust("k", 80, 0)
	if got := tr2.GetUsage("k"); got != 0 {
		t.Fatalf("daily usage after full refund expected 0, got %d", got)
	}
}

// TestGuestQuotaTracker_WindowRoll verifies that a stale window stamp rolls the
// journal (previous-day/hour data is discarded).
func TestGuestQuotaTracker_WindowRoll(t *testing.T) {
	// Force a genuinely stale window stamp.
	tr := &guestKeyUsageTracker{day: "2000-01-01", hour: "2000-01-01T00", usage: map[string]int64{"k": 900}, hourly: map[string]int64{"k": 900}}
	if ok, _ := tr.CheckAndReserveFull("k", 1000, 1000, 0, 0, 500); !ok {
		t.Fatal("stale-window tracker should roll and be treated as empty")
	}
	if got := tr.GetUsage("k"); got != 500 {
		t.Fatalf("daily usage after roll expected 500, got %d", got)
	}
}

// TestGuestQuota_SharedModeChatEnforced is the regression guard for the core
// share-centre bug: in shared mode a guest key resolves to role "public", so
// the old keyType=="guest" guard silently skipped the per-key daily quota and
// unlimited requests sailed through. Now the verified guest key (context) is
// what triggers D-4, so a key whose remaining daily quota (30t) cannot cover
// the request estimate (80t from max_tokens) must get a 429.
func TestGuestQuota_SharedModeChatEnforced(t *testing.T) {
	env := setupTestEnv(t)

	netMgr = &NetworkManager{config: NetworkConfig{NodeID: "mmx-self-quota", Mode: NetworkModeShared}}
	initGuestKeyStore(env.dir)

	key := "sk-guest-mmx-self-quota-" + strings.Repeat("a", 32)
	guestKeyStore.keys = append(guestKeyStore.keys, &GuestKeyRecord{
		Key: key, NodeID: "mmx-self-quota", RandomPart: strings.Repeat("a", 32), Quota: 30,
	})
	initGuestKeyUsageTracker()

	// Reachable upstream so any non-quota path would succeed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
	}))
	defer srv.Close()

	pm.providers["p-quota"] = Provider{
		ID: "p-quota", Name: "Local", Type: "openai_compatible",
		BaseURL: srv.URL, APIKey: "sk-provider-secret", Enabled: true,
		AccessControl: ProviderAccessControl{ShareToPool: true},
		Models:        []ModelDef{{ID: "gpt-4", Enabled: true}},
	}

	body := `{"model":"gpt-4","max_tokens":80,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()

	withProxyAuth(handleGatewayRequest)(w, req)

	if w.Code != 429 {
		t.Fatalf("shared-mode guest with quota 30 / est 80 expected 429, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "额度") {
		t.Errorf("expected a quota-denial message, got %s", w.Body.String())
	}
}

// TestGuestQuota_SharedModeChatSettlement proves that (a) an unlimited
// (quota=0) shared-mode guest can complete a request — the D-4 rewrite did not
// leak public-pool behaviour — and (b) a bounded key settles the REAL token
// count into the daily journal (reserve 80 → actual 10).
func TestGuestQuota_SharedModeChatSettlement(t *testing.T) {
	env := setupTestEnv(t)

	netMgr = &NetworkManager{config: NetworkConfig{NodeID: "mmx-self-settle", Mode: NetworkModeShared}}
	initGuestKeyStore(env.dir)

	key := "sk-guest-mmx-self-settle-" + strings.Repeat("b", 32)
	guestKeyStore.keys = append(guestKeyStore.keys, &GuestKeyRecord{
		Key: key, NodeID: "mmx-self-settle", RandomPart: strings.Repeat("b", 32), Quota: 100,
	})
	initGuestKeyUsageTracker()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
	}))
	defer srv.Close()

	pm.providers["p-settle"] = Provider{
		ID: "p-settle", Name: "Local", Type: "openai_compatible",
		BaseURL: srv.URL, APIKey: "sk-provider-secret", Enabled: true,
		AccessControl: ProviderAccessControl{ShareToPool: true},
		Models:        []ModelDef{{ID: "gpt-4", Enabled: true}},
	}

	body := `{"model":"gpt-4","max_tokens":80,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()

	withProxyAuth(handleGatewayRequest)(w, req)

	if w.Code != 200 {
		t.Fatalf("shared-mode guest with quota 100 expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := guestKeyUsage.GetUsage(key); got != 10 {
		t.Fatalf("settled usage expected actual 10, got %d", got)
	}

	// A second request within the remaining 90t must also succeed.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+key)
	w2 := httptest.NewRecorder()
	withProxyAuth(handleGatewayRequest)(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("second request expected 200, got %d (body=%s)", w2.Code, w2.Body.String())
	}
	if got := guestKeyUsage.GetUsage(key); got != 20 {
		t.Fatalf("settled usage after two real requests expected 20, got %d", got)
	}
}

// TestGuestQuota_IssueNegativeRejected guards the quota-issuance validation:
// the issue endpoint must reject negative quota/rpm/exp_days values just like
// the update endpoint already did, instead of storing a value that the
// enforcement path would silently treat as "unlimited".
func TestGuestQuota_IssueNegativeRejected(t *testing.T) {
	env := setupTestEnv(t)

	netMgr = &NetworkManager{config: NetworkConfig{NodeID: "neg-node", Mode: NetworkModeShared}}
	initGuestKeyStore(env.dir)

	negCases := []struct {
		name string
		body string
		want string
	}{
		{"quota", `{"quota":-1}`, "quota must be >= 0"},
		{"quota_hourly", `{"quota_hourly":-1}`, "quota_hourly must be >= 0"},
		{"quota_per_request", `{"quota_per_request":-1}`, "quota_per_request must be >= 0"},
		{"rpm", `{"rpm":-1}`, "rpm must be >= 0"},
		{"exp_days", `{"exp_days":-1}`, "exp_days must be >= 0"},
	}
	for _, c := range negCases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/network/guest-keys", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handleGuestKeyIssue(w, req)
			if w.Code != 400 {
				t.Fatalf("expected 400, got %d (body=%s)", w.Code, w.Body.String())
			}
			var e struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if e.Error.Message != c.want {
				t.Errorf("expected message %q, got %q", c.want, e.Error.Message)
			}
		})
	}

	// A valid issuance still works and persists the quota.
	body, _ := json.Marshal(map[string]any{"quota": 100, "rpm": 5})
	req := httptest.NewRequest(http.MethodPost, "/api/network/guest-keys", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleGuestKeyIssue(w, req)
	if w.Code != 200 {
		t.Fatalf("valid issuance expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	issued, _ := resp["key"].(string)
	if issued == "" {
		t.Fatal("expected an issued key in response")
	}
	rec := guestKeyStore.GetGuestKeyRecord(issued)
	if rec == nil || rec.Quota != 100 || rec.RPM != 5 {
		t.Fatalf("issued record does not persist quota: %+v", rec)
	}
}

// ============================================================
// Usage journal persistence: a restart must not reset quota accounting.
// ============================================================

// TestGuestUsageTracker_PersistsAcrossRestart reserves quota, re-initializes
// the tracker from the same dir (simulating a process restart), and requires
// the daily, hourly and RPM journals to survive.
func TestGuestUsageTracker_PersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	initGuestKeyUsageTrackerWithDir(dir)

	if ok, _ := guestKeyUsage.CheckAndReserveFull("k", 100, 100, 0, 5, 80); !ok {
		t.Fatal("first 80t reservation should be allowed")
	}
	if _, err := os.Stat(filepath.Join(dir, "guest_usage.json")); err != nil {
		t.Fatalf("journal file must exist after a mutation: %v", err)
	}

	// Simulate a restart: brand-new tracker instance over the same dir.
	initGuestKeyUsageTrackerWithDir(dir)
	if got := guestKeyUsage.GetUsage("k"); got != 80 {
		t.Fatalf("daily usage after restart = %d, want 80", got)
	}
	guestKeyUsage.mu.Lock()
	rpmCount := guestKeyUsage.rpm["k"]
	hourly := guestKeyUsage.hourly["k"]
	guestKeyUsage.mu.Unlock()
	if hourly != 80 {
		t.Fatalf("hourly usage after restart = %d, want 80", hourly)
	}
	if rpmCount != 1 {
		t.Fatalf("rpm count after restart = %d, want 1", rpmCount)
	}
	// Both windows enforced against the restored state: only 20t left daily.
	if ok, _ := guestKeyUsage.CheckAndReserveFull("k", 100, 100, 0, 5, 30); ok {
		t.Fatal("30t reservation must be denied with 20t remaining after restart")
	}
}

// TestGuestUsageTracker_StaleJournalRolls writes a journal from a previous
// day/hour/minute and requires the first use after load to reset the expired
// windows instead of resurrecting dead quota.
func TestGuestUsageTracker_StaleJournalRolls(t *testing.T) {
	dir := t.TempDir()
	stale := guestUsageJournal{
		Day: "2000-01-01", Usage: map[string]int64{"k": 80},
		Hour: "2000-01-01T00", Hourly: map[string]int64{"k": 50},
		Minute: "2000-01-01T00:00", RPM: map[string]int{"k": 3},
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "guest_usage.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	initGuestKeyUsageTrackerWithDir(dir)
	if got := guestKeyUsage.GetUsage("k"); got != 0 {
		t.Fatalf("stale daily journal must roll to 0, got %d", got)
	}
	guestKeyUsage.mu.Lock()
	hourly := guestKeyUsage.hourly["k"]
	rpmCount := guestKeyUsage.rpm["k"]
	guestKeyUsage.mu.Unlock()
	if hourly != 0 || rpmCount != 0 {
		t.Fatalf("stale hourly/rpm journals must roll to 0, got %d/%d", hourly, rpmCount)
	}
	// And the fresh windows accept reservations again.
	if ok, _ := guestKeyUsage.CheckAndReserveFull("k", 100, 100, 0, 5, 80); !ok {
		t.Fatal("fresh windows after roll must allow reservations")
	}
}

// TestGuestUsageTracker_CorruptJournalStartsFresh requires a corrupt journal
// to degrade to empty accounting, never to a crash or a hard failure.
func TestGuestUsageTracker_CorruptJournalStartsFresh(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "guest_usage.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	initGuestKeyUsageTrackerWithDir(dir)
	if got := guestKeyUsage.GetUsage("k"); got != 0 {
		t.Fatalf("corrupt journal must start fresh, got usage %d", got)
	}
	if ok, _ := guestKeyUsage.CheckAndReserveFull("k", 100, 0, 0, 0, 10); !ok {
		t.Fatal("fresh tracker must allow reservations")
	}
}

// TestGuestUsageTracker_MemoryOnlySkipsIO pins the in-memory constructor:
// no journal path, no disk writes, accounting still works.
func TestGuestUsageTracker_MemoryOnlySkipsIO(t *testing.T) {
	initGuestKeyUsageTracker()
	if guestKeyUsage.dataPath != "" {
		t.Fatalf("in-memory tracker must have no journal path, got %q", guestKeyUsage.dataPath)
	}
	if ok, _ := guestKeyUsage.CheckAndReserveFull("k", 100, 0, 0, 0, 10); !ok {
		t.Fatal("in-memory tracker must enforce normally")
	}
	if got := guestKeyUsage.GetUsage("k"); got != 10 {
		t.Fatalf("usage = %d, want 10", got)
	}
}
