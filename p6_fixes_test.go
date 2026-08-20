package main

// p6_fixes_test.go — regression tests for the batch 6 (v4.5.23) hardening:
//
//	B6-1  concurrency semaphore split (management pool vs model traffic)
//	B6-2  audit wiring (withAuth identity propagation + auditRecord calls)
//	B6-3  global recover middleware
//	B6-5  federation signature binds body hash (with legacy fallback)
//	B6-6/B6-extra forwarded header sanitization + admin downgrade
//	B6-7  IP rate limiter lastSeen atomic refresh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ============================================================
// B6-3: recoverMiddleware
// ============================================================

func TestP6_RecoverMiddleware_PanicReturns500(t *testing.T) {
	handler := recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Error.Code != "panic_recovered" {
		t.Fatalf("expected panic_recovered code, got %s", body.Error.Code)
	}
}

func TestP6_RecoverMiddleware_NoPanicPassesThrough(t *testing.T) {
	handler := recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("expected passthrough 200 ok, got %d %s", w.Code, w.Body.String())
	}
}

// ============================================================
// B6-1: semaphore split
// ============================================================

func TestP6_IsModelTrafficPath(t *testing.T) {
	model := []string{
		"/v1/chat/completions", "/v1/models", "/v1beta/models/gemini",
		"/openai/deployments/x/chat/completions", "/api/federation/relay",
	}
	for _, p := range model {
		if !isModelTrafficPath(p) {
			t.Errorf("expected %q to be model traffic", p)
		}
	}
	admin := []string{"/api/config", "/api/login", "/api/version", "/health", "/api/federation/status"}
	for _, p := range admin {
		if isModelTrafficPath(p) {
			t.Errorf("expected %q to NOT be model traffic", p)
		}
	}
}

func TestP6_InitConcurrencyLimiter_CreatesAdminPool(t *testing.T) {
	initConcurrencyLimiter(0)
	if cap(requestSemaphore) != defaultMaxConcurrentRequests {
		t.Fatalf("main pool: expected %d, got %d", defaultMaxConcurrentRequests, cap(requestSemaphore))
	}
	if adminRequestSemaphore == nil {
		t.Fatal("admin pool not created")
	}
	if cap(adminRequestSemaphore) < 5 || cap(adminRequestSemaphore) > adminMaxConcurrentRequests {
		t.Fatalf("admin pool size out of range: %d", cap(adminRequestSemaphore))
	}
	initConcurrencyLimiter(50)
	if cap(requestSemaphore) != 50 {
		t.Fatalf("custom size ignored: %d", cap(requestSemaphore))
	}
}

func TestP6_AdminPoolNotStarvedByModelTraffic(t *testing.T) {
	initConcurrencyLimiter(2)
	// Exhaust the model-traffic pool.
	release1 := acquireSemaphoreFrom(requestSemaphore, context.Background())
	release2 := acquireSemaphoreFrom(requestSemaphore, context.Background())
	defer func() {
		if release1 {
			releaseSemaphoreFrom(requestSemaphore)
		}
		if release2 {
			releaseSemaphoreFrom(requestSemaphore)
		}
	}()

	served := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		close(served)
	})
	go concurrencyMiddleware(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/config", nil))

	select {
	case <-served:
		// management request went through the dedicated pool despite the main
		// pool being full — exactly the B6-1 guarantee.
	case <-time.After(semaphoreAcquireTimeout + 2*time.Second):
		t.Fatal("management request starved by exhausted model-traffic pool")
	}
}

// ============================================================
// B6-2: audit wiring
// ============================================================

func newTestAuditLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "audit.log"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	old := auditLog
	auditLog = &AuditLogger{file: f, enabled: true, path: f.Name()}
	t.Cleanup(func() {
		auditLog = old
		f.Close()
	})
	return f.Name()
}

func readAuditLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestP6_AuditRecord_AttributesUsernameFromContext(t *testing.T) {
	path := newTestAuditLog(t)
	req := httptest.NewRequest("POST", "/api/config", nil)
	req = req.WithContext(context.WithValue(req.Context(), "username", "alice"))
	auditRecord(req, "config.save", "config", "2 keys", true)
	lines := readAuditLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("expected 1 audit line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "alice") || !strings.Contains(lines[0], "config.save") || !strings.Contains(lines[0], "success") {
		t.Fatalf("audit line missing attribution: %q", lines[0])
	}
}

func TestP6_WithAuth_PropagatesUsernameToAudit(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	path := newTestAuditLog(t)

	access, _ := auth.CreateToken("boss", false)

	var seenUser string
	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		seenUser = r.Context().Value("username").(string)
		// simulate a handler that audits
		auditRecord(r, "gateway.set", "is_gateway", "true", true)
	})
	req := httptest.NewRequest("POST", "/api/gateway", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	handler(httptest.NewRecorder(), req)

	if seenUser != "boss" {
		t.Fatalf("withAuth did not propagate username, got %q", seenUser)
	}
	lines := readAuditLines(t, path)
	if len(lines) != 1 || !strings.Contains(lines[0], "boss") {
		t.Fatalf("audit line not attributed to boss: %q", lines)
	}
}

// ============================================================
// B6-6 / B6-extra: sanitizeForwardedHeaders
// ============================================================

func TestP6_SanitizeForwardedHeaders_StripsClientControlled(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Cookie", "session=secret")
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-Ip", "1.2.3.4")
	r.Header.Set("Content-Type", "application/json")

	dst := r.Header.Clone()
	sanitizeForwardedHeaders(dst, r)

	for _, h := range []string{"Cookie", "X-Forwarded-For", "X-Real-Ip"} {
		if dst.Get(h) != "" {
			t.Errorf("%s survived sanitization: %q", h, dst.Get(h))
		}
	}
	if dst.Get("Content-Type") != "application/json" {
		t.Error("benign headers must survive")
	}
}

func TestP6_SanitizeForwardedHeaders_ConsumerIdentityPreserved(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer sk-consumer-key")
	r.Header.Set("X-Request-Owner", "consumer-7")
	r.Header.Set("X-Request-Role", "consumer")

	dst := r.Header.Clone()
	sanitizeForwardedHeaders(dst, r)

	if dst.Get("Authorization") != "" {
		t.Error("consumer key must never be forwarded")
	}
	if dst.Get("X-Request-Owner") != "consumer-7" || dst.Get("X-Request-Role") != "consumer" {
		t.Fatalf("verified consumer identity lost: owner=%q role=%q", dst.Get("X-Request-Owner"), dst.Get("X-Request-Role"))
	}
}

func TestP6_SanitizeForwardedHeaders_AnonymousAdminDowngraded(t *testing.T) {
	// Anonymous private-network fallback: role=admin with NO credential.
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("X-Request-Owner", "")
	r.Header.Set("X-Request-Role", "admin")

	dst := r.Header.Clone()
	sanitizeForwardedHeaders(dst, r)

	if dst.Get("X-Request-Role") != "public" {
		t.Fatalf("anonymous admin must downgrade to public, got %q", dst.Get("X-Request-Role"))
	}
	if dst.Get("X-Request-Owner") != "" {
		t.Fatalf("owner must be cleared, got %q", dst.Get("X-Request-Owner"))
	}
}

func TestP6_SanitizeForwardedHeaders_RealAdminKept(t *testing.T) {
	// Admin authenticated via proxy key: credential present, role stays.
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer proxy-admin-key")
	r.Header.Set("X-Request-Role", "admin")

	dst := r.Header.Clone()
	sanitizeForwardedHeaders(dst, r)

	if dst.Get("X-Request-Role") != "admin" {
		t.Fatalf("credentialed admin must stay admin, got %q", dst.Get("X-Request-Role"))
	}
	if dst.Get("Authorization") != "" {
		t.Error("even admin credentials must not be forwarded")
	}
}

// ============================================================
// B6-5: federation signature binds body hash
// ============================================================

func TestP6_FederationSigValid_BodyBoundFormat(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	body := []byte(`{"records":[1,2,3]}`)
	r := httptest.NewRequest("POST", "/api/federation/gossip", bytes.NewReader(body))
	ts := time.Now().Unix()

	payload := []byte("node-a:POST:/api/federation/gossip:" + sha256Hex(body))
	sig := ed25519.Sign(priv, payload)
	sigB64 := base64.StdEncoding.EncodeToString(sig)
	r.Header.Set("X-Node-Timestamp", strconv.FormatInt(ts, 10))

	if !federationSigValid(r, pubB64, "node-a", sigB64) {
		t.Fatal("body-bound signature must verify")
	}

	// Tampered body must fail the body-bound signature.
	r2 := httptest.NewRequest("POST", "/api/federation/gossip", bytes.NewReader([]byte(`{"records":[9]}`)))
	r2.Header.Set("X-Node-Timestamp", strconv.FormatInt(ts, 10))
	if federationSigValid(r2, pubB64, "node-a", sigB64) {
		t.Fatal("tampered body must NOT verify against body-bound signature")
	}
}

func TestP6_FederationSigValid_LegacyFallback(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	body := []byte(`{"ok":true}`)
	r := httptest.NewRequest("POST", "/api/federation/pool", bytes.NewReader(body))
	ts := time.Now().Unix()
	r.Header.Set("X-Node-Timestamp", strconv.FormatInt(ts, 10))

	legacyPayload := []byte("node-b:POST:/api/federation/pool")
	sig := ed25519.Sign(priv, legacyPayload)

	if !federationSigValid(r, pubB64, "node-b", base64.StdEncoding.EncodeToString(sig)) {
		t.Fatal("legacy body-less signature must still verify (rolling deploy)")
	}
}

// ============================================================
// B6-7: rate limiter lastSeen refresh
// ============================================================

func TestP6_RateLimitByIP_LastSeenRefreshesAtomically(t *testing.T) {
	mw := rateLimitByIP(60, "p6_test_endpoint")
	handler := mw(func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest("GET", "/api/version", nil)
	req.RemoteAddr = "203.0.113.77:44444"
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("first request must pass, got %d", rec.Code)
	}

	key := "203.0.113.77" + "p6_test_endpoint"
	ipRateLimiters.RLock()
	entry, ok := ipRateLimiters.limiters[key]
	ipRateLimiters.RUnlock()
	if !ok {
		t.Fatal("limiter entry missing after request")
	}
	first := entry.lastSeen.Load()
	if first <= 0 {
		t.Fatalf("lastSeen not recorded: %d", first)
	}

	time.Sleep(10 * time.Millisecond)
	req2 := httptest.NewRequest("GET", "/api/version", nil)
	req2.RemoteAddr = "203.0.113.77:55555"
	handler(httptest.NewRecorder(), req2)

	second := entry.lastSeen.Load()
	if second <= first {
		t.Fatalf("lastSeen not refreshed: first=%d second=%d", first, second)
	}
}
