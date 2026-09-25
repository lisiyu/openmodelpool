package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
