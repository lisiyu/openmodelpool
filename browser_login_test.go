package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/chromedp/chromedp"
)

func TestSanitizeProfileName(t *testing.T) {
	cases := map[string]string{
		"valid-id_123":    "valid-id_123",
		"a b/c:d":         "a_b_c_d",
		"provider@1!":     "provider_1_",
		"":                "default",
		"UPPER and lower": "UPPER_and_lower",
		"---___---":       "---___---",
	}
	for in, want := range cases {
		if got := sanitizeProfileName(in); got != want {
			t.Errorf("sanitizeProfileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBrowserLaunchFlags(t *testing.T) {
	const dir = "/tmp/omp-test-profile"
	flags := browserLaunchFlags(dir, "", "")

	// Headless + software rendering flags required for modern Chrome.
	if flags["headless"] != "new" {
		t.Errorf("headless flag = %v, want \"new\"", flags["headless"])
	}
	if flags["disable-gpu"] != true {
		t.Errorf("disable-gpu flag = %v, want true", flags["disable-gpu"])
	}
	if flags["enable-unsafe-swiftshader"] != true {
		t.Errorf("enable-unsafe-swiftshader flag = %v, want true", flags["enable-unsafe-swiftshader"])
	}
	if flags["user-data-dir"] != dir {
		t.Errorf("user-data-dir flag = %v, want %q", flags["user-data-dir"], dir)
	}

	// OS-specific flags.
	if runtime.GOOS == "linux" {
		if flags["no-sandbox"] != true {
			t.Errorf("linux build missing no-sandbox flag")
		}
		if flags["disable-dev-shm-usage"] != true {
			t.Errorf("linux build missing disable-dev-shm-usage flag")
		}
	} else {
		if _, ok := flags["no-sandbox"]; ok {
			t.Errorf("non-linux build should not set no-sandbox, got %v", flags["no-sandbox"])
		}
	}

	// chromePath must not leak into the flag map (it is applied separately).
	if _, ok := flags["exec-path"]; ok {
		t.Errorf("exec-path should not appear in browserLaunchFlags map")
	}
}

func TestBrowserLaunchFlagsWithProxy(t *testing.T) {
	flags := browserLaunchFlags("/tmp/d", "http://127.0.0.1:7890", "")
	if flags["proxy-server"] != "http://127.0.0.1:7890" {
		t.Errorf("proxy-server flag = %v, want http://127.0.0.1:7890", flags["proxy-server"])
	}
	const wantRules = "MAP * ~NOTFOUND, EXCLUDE 127.0.0.1"
	if flags["host-resolver-rules"] != wantRules {
		t.Errorf("host-resolver-rules flag = %v, want %q", flags["host-resolver-rules"], wantRules)
	}
}

func TestBuildBrowserAllocatorOptionsValid(t *testing.T) {
	opts := buildBrowserAllocatorOptions("/tmp/omp-test-profile", "", "")
	if len(opts) == 0 {
		t.Fatal("buildBrowserAllocatorOptions returned no options")
	}
	// Applying the options to a real allocator must not panic and must yield a
	// usable context. This validates the chromedp option funcs are well-formed.
	ctx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	if ctx == nil {
		t.Fatal("NewExecAllocator returned nil context")
	}
}

func TestFindBrowserExecutableEnvOverride(t *testing.T) {
	// Create a fake executable file so the override path is exercised.
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "chrome-fake")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake chrome: %v", err)
	}

	t.Setenv("OMP_CHROME_PATH", fake)
	got := findBrowserExecutable()
	if got != fake {
		t.Errorf("findBrowserExecutable() with OMP_CHROME_PATH = %q, want %q", got, fake)
	}

	// A non-existent env override must be ignored (returns "" or a discovered path).
	t.Setenv("OMP_CHROME_PATH", filepath.Join(tmp, "does-not-exist"))
	got = findBrowserExecutable()
	if got != "" {
		// Allowed to fall back to a discovered real browser, but the bad path
		// must not be returned.
		if _, err := os.Stat(got); err != nil {
			t.Errorf("findBrowserExecutable returned a non-existent path %q", got)
		}
	}
}

func TestUserDatadirForLifecycle(t *testing.T) {
	dir, err := userDataDirFor("test-provider_1")
	if err != nil {
		t.Fatalf("userDataDirFor error: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("userDataDirFor did not create a directory at %q: %v", dir, err)
	}
	// Calling again should reset to a clean directory.
	dir2, err := userDataDirFor("test-provider_1")
	if err != nil {
		t.Fatalf("second userDataDirFor error: %v", err)
	}
	if dir == dir2 {
		t.Errorf("expected a fresh directory path, got same %q", dir)
	}
	// Cleanup.
	if err := os.RemoveAll(dir); err != nil {
		t.Errorf("remove dir %q: %v", dir, err)
	}
	if err := os.RemoveAll(dir2); err != nil {
		t.Errorf("remove dir %q: %v", dir2, err)
	}
}

// TestBrowserErrorJSONShape verifies that error responses are always valid JSON
// with a Content-Type, so the frontend never hits "Unexpected end of JSON input".
func TestBrowserErrorJSONShape(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, 500, "浏览器启动失败: chrome failed to start")
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if resp.Error.Message == "" {
		t.Errorf("error response missing message; body=%q", rec.Body.String())
	}
}

// TestRecoverHandlerWritesJSON verifies that a panic in a handler produces a
// valid JSON error instead of an empty body.
func TestRecoverHandlerWritesJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	func() {
		defer recoverHandler(rec, "test")
		panic("boom")
	}()

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("after panic, Content-Type = %q, want application/json", ct)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("after panic, status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("panic recovery body is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if resp.Error.Message == "" {
		t.Errorf("panic recovery body missing message; body=%q", rec.Body.String())
	}
}

// TestRecoverHandlerNoOverwrite verifies that if a response was already written,
// the recover handler does not attempt to write again (avoiding superfluous
// WriteHeader panics).
func TestRecoverHandlerNoOverwrite(t *testing.T) {
	rec := httptest.NewRecorder()
	func() {
		defer recoverHandler(rec, "test")
		writeJSON(rec, 200, map[string]any{"status": "ok"})
		panic("boom after write")
	}()
	if rec.Code != http.StatusOK {
		t.Errorf("expected original 200 to be preserved, got %d", rec.Code)
	}
	if rec.Body.String() != `{"status":"ok"}`+"\n" && rec.Body.String() != `{"status":"ok"}` {
		t.Errorf("body mutated by recover handler: %q", rec.Body.String())
	}
}

// TestRecoverHandlerInWrappedHandler reproduces the exact production pattern:
// each browser handler registers `defer recoverHandler(w, label)` at the top,
// so a panic anywhere in the handler must be converted into a valid JSON error
// instead of an empty (non-JSON) 500 body. This is the fix for the Linux
// "Unexpected end of JSON input" symptom.
func TestRecoverHandlerInWrappedHandler(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		defer recoverHandler(w, "start")
		// Mimic real handler work that can panic (nil deref, type assertion, etc.).
		_ = r
		panic("unexpected nil dereference in browser handler")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/providers/x/browser/start", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	// Must produce a NON-ZERO status code.
	if rec.Code == 0 {
		t.Fatalf("expected a non-zero status code after panic in wrapped handler, got 0")
	}
	// The specific status must be 500 (matches recoverHandler's writeError call).
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("after panic, status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	// Body must be valid JSON with the correct Content-Type.
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("after panic in wrapped handler, Content-Type = %q, want application/json", ct)
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("panic in wrapped handler produced non-JSON body: %v (body=%q)", err, rec.Body.String())
	}
	if resp.Error.Message == "" {
		t.Errorf("panic in wrapped handler: error body missing message; body=%q", rec.Body.String())
	}
}

// TestRecoverHandlerWrappedHandlerStatusCode verifies that the wrapped-handler
// pattern yields a 500 even when the handler has NOT written anything yet, and
// that the status is non-zero (guards against a regression where the deferred
// recover runs after the response was already committed with status 0).
func TestRecoverHandlerWrappedHandlerStatusCode(t *testing.T) {
	cases := []string{"status", "login", "action", "finish", "cancel"}
	for _, label := range cases {
		handler := func(w http.ResponseWriter, r *http.Request) {
			defer recoverHandler(w, label)
			panic("boom in " + label)
		}
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("label %q: status = %d, want %d", label, rec.Code, http.StatusInternalServerError)
		}
		if rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("label %q: Content-Type = %q, want application/json", label, rec.Header().Get("Content-Type"))
		}
	}
}
