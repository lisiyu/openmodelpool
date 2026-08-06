package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// g6Post drives handleChatCompletions with a minimal chat body and a forced key
// type. RequestKeyType honors the X-OMP-KeyType header at Priority 1, so we can
// exercise the guest / admin(proxy) branches without real key crypto. The
// handler reaches the G6 block before any provider selection, so with the empty
// provider manager from setupTestEnv it returns a clean 404 and leaves the
// X-Quota-Pool header (set by the G6 block) intact for inspection.
func g6Post(t *testing.T, keyType string, maxTokens int) *httptest.ResponseRecorder {
	t.Helper()
	if maxTokens <= 0 {
		maxTokens = 4096 // matches the handler's default estimate
	}
	body := fmt.Sprintf(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"max_tokens":%d}`, maxTokens)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	if keyType != "" {
		req.Header.Set("X-OMP-KeyType", keyType)
	}
	w := httptest.NewRecorder()
	handleChatCompletions(w, req)
	return w
}

// G6-1 (ZERO-IMPACT CONTRACT): when quota priority is DISABLED (the default),
// the handler must NOT emit X-Quota-Pool and must not return 429. This is the
// explicit "默认 false，线上零影响 / 只在 quota_priority_enabled=true 时生效"
// contract from the SOP.
//
// EXPECTED TO FAIL against the current code: the G6 block is gated only on
// `quotaPriorityMgr != nil` (always true after initCore), so the header is set
// unconditionally even when the feature is disabled.
func TestG6_HandlerDisabled_NoHeader(t *testing.T) {
	_ = setupTestEnv(t)
	// Simulate the default: initQuotaPriority() with no quota_priority_enabled flag.
	quotaPriorityMgr = &quotaPriorityManager{enabled: false, privateLimit: -1, sharedLimit: -1, remoteLimit: -1}

	w := g6Post(t, "guest", 100)
	if h := w.Header().Get("X-Quota-Pool"); h != "" {
		t.Fatalf("DISABLED G6 must not set X-Quota-Pool header (zero-impact contract); got %q (status %d)", h, w.Code)
	}
	if w.Code == 429 {
		t.Fatalf("DISABLED G6 must never return 429; got 429")
	}
}

// G6-2: enabled with every pool below the request amount => 429, header present.
func TestG6_HandlerEnabled_Exhausted_429(t *testing.T) {
	_ = setupTestEnv(t)
	quotaPriorityMgr = &quotaPriorityManager{enabled: true, privateLimit: 10, sharedLimit: 10, remoteLimit: 10}

	w := g6Post(t, "guest", 100) // 100 > every pool limit
	if w.Code != 429 {
		t.Fatalf("expected 429 when all pools exhausted; got %d", w.Code)
	}
	if h := w.Header().Get("X-Quota-Pool"); h == "" {
		t.Fatalf("expected X-Quota-Pool header on the 429 path")
	}
}

// G6-3: enabled, all pools ample => guest charges the private pool first.
func TestG6_HandlerEnabled_PrivateCharged(t *testing.T) {
	_ = setupTestEnv(t)
	quotaPriorityMgr = &quotaPriorityManager{enabled: true, privateLimit: 100000, sharedLimit: 100000, remoteLimit: 100000}

	w := g6Post(t, "guest", 100)
	if h := w.Header().Get("X-Quota-Pool"); h != "private" {
		t.Fatalf("expected X-Quota-Pool=private (private-first); got %q (status %d)", h, w.Code)
	}
}

// G6-4: enabled, private too small => downgrade to the shared pool.
func TestG6_HandlerEnabled_SharedCharged(t *testing.T) {
	_ = setupTestEnv(t)
	quotaPriorityMgr = &quotaPriorityManager{enabled: true, privateLimit: 10, sharedLimit: 100000, remoteLimit: 100000}

	w := g6Post(t, "guest", 100)
	if h := w.Header().Get("X-Quota-Pool"); h != "shared" {
		t.Fatalf("expected X-Quota-Pool=shared (private insufficient); got %q (status %d)", h, w.Code)
	}
}

// G6-5: enabled, private+shared too small => downgrade to the remote_shared pool.
func TestG6_HandlerEnabled_RemoteCharged(t *testing.T) {
	_ = setupTestEnv(t)
	quotaPriorityMgr = &quotaPriorityManager{enabled: true, privateLimit: 10, sharedLimit: 10, remoteLimit: 100000}

	w := g6Post(t, "guest", 100)
	if h := w.Header().Get("X-Quota-Pool"); h != "remote_shared" {
		t.Fatalf("expected X-Quota-Pool=remote_shared; got %q (status %d)", h, w.Code)
	}
}

// G6-6: Admin/Proxy keys also follow the private-first priority order.
func TestG6_HandlerEnabled_AdminKeyPrivate(t *testing.T) {
	_ = setupTestEnv(t)
	quotaPriorityMgr = &quotaPriorityManager{enabled: true, privateLimit: 100000, sharedLimit: 100000, remoteLimit: 100000}

	w := g6Post(t, "admin", 100)
	if h := w.Header().Get("X-Quota-Pool"); h != "private" {
		t.Fatalf("expected admin key X-Quota-Pool=private; got %q (status %d)", h, w.Code)
	}
}

// G6-7: initQuotaPriority must honor quota_priority_enabled and the per-pool
// limit knobs (cfg -> manager wiring), and Resolve must route by those limits.
func TestG6_InitFromConfig(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	cfg.Set("quota_priority_enabled", "true")
	cfg.Set("quota_private_pool_limit", "50")
	cfg.Set("quota_shared_pool_limit", "60")
	cfg.Set("quota_remote_pool_limit", "70")
	initQuotaPriority()

	if quotaPriorityMgr == nil {
		t.Fatal("initQuotaPriority did not initialize the manager")
	}
	if !quotaPriorityMgr.enabled {
		t.Fatal("expected manager enabled when quota_priority_enabled=true")
	}
	res := quotaPriorityMgr.Resolve(KeyTypeGuest, 55) // private(50) insufficient -> shared(60)
	if !res.OK || res.Kind != PoolShared {
		t.Fatalf("expected shared pool charged via cfg limits; got ok=%v kind=%s reason=%s", res.OK, res.Kind, res.Reason)
	}
}
