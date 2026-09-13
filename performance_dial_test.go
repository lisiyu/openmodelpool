package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// dialPreferIPv4 unit tests
//
// Regression coverage for the product-level bug where dialing an IPv4 literal
// placed the tcp4 failure into err and then *unconditionally* fell back to
// tcp6, returning "no suitable address found" and hiding the real cause
// (e.g. "connection refused"). The mirror-image case for IPv6 literals is also
// covered, plus the explicit-"tcp4" path whose original defect was discarding
// the real dial error and returning the synthetic errNoIPv4 (NOT "not dialing"):
// it did dial tcp4, but threw the resulting error away.
// ---------------------------------------------------------------------------

// dialTestCtx returns a short-lived context so a hung dial cannot stall the
// suite; it is cancelled when the test finishes.
func dialTestCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// closedLoopbackAddr reserves a loopback port and immediately releases it so
// the returned address is (almost certainly) nothing-listening. Using :0 keeps
// the test free of hard-coded ports and immune to environment port clashes.
func closedLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a loopback port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("failed to release reserved port: %v", err)
	}
	return addr
}

// Case 1: IPv4 literal on a dead port must surface the actionable error and NOT
// the meaningless tcp6 family error.
func TestDialPreferIPv4_IPv4LiteralRefused(t *testing.T) {
	ctx := dialTestCtx(t)
	addr := closedLoopbackAddr(t)

	conn, err := dialPreferIPv4(ctx, "tcp", addr)
	if err == nil {
		conn.Close()
		t.Fatalf("dialPreferIPv4(tcp, %s) = (conn, nil), want a dial error", addr)
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no suitable address found") {
		t.Errorf("error was masked by the tcp6 family error: %q", err.Error())
	}
	if !strings.Contains(msg, "refused") {
		t.Errorf("error = %q, want it to mention the connection being refused", err.Error())
	}
}

// Case 2: IPv4 literal with a live listener must connect successfully.
func TestDialPreferIPv4_IPv4LiteralConnected(t *testing.T) {
	ctx := dialTestCtx(t)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}
	defer l.Close()

	conn, err := dialPreferIPv4(ctx, "tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dialPreferIPv4(tcp, %s) returned error: %v", l.Addr().String(), err)
	}
	defer conn.Close()
	if conn == nil {
		t.Fatal("dialPreferIPv4 returned a nil conn with a nil error")
	}
}

// Case 3: an explicit tcp6 request for an IPv4 literal must fail cleanly (never
// panic) — guards the honoured-family path.
func TestDialPreferIPv4_ExplicitTCP6WithIPv4Literal(t *testing.T) {
	ctx := dialTestCtx(t)
	addr := closedLoopbackAddr(t)

	conn, err := dialPreferIPv4(ctx, "tcp6", addr)
	if err == nil {
		conn.Close()
		t.Fatalf("dialPreferIPv4(tcp6, %s) = (conn, nil), want a dial error", addr)
	}
}

// Case 4 (sanity only, NOT a regression guard): an explicit tcp4 request for a
// live IPv4 listener must succeed. This also passes on the OLD implementation —
// which DID dial tcp4 — so it cannot catch that defect; Case 5 is the real guard.
func TestDialPreferIPv4_ExplicitTCP4Connected(t *testing.T) {
	ctx := dialTestCtx(t)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}
	defer l.Close()

	conn, err := dialPreferIPv4(ctx, "tcp4", l.Addr().String())
	if err != nil {
		t.Fatalf("dialPreferIPv4(tcp4, %s) returned error: %v", l.Addr().String(), err)
	}
	defer conn.Close()
	if conn == nil {
		t.Fatal("dialPreferIPv4 returned a nil conn with a nil error")
	}
}

// Case 5 (real guard for the explicit-tcp4 defect): an explicit tcp4 request to
// a dead port must surface the REAL dial error ("connection refused"), not the
// synthetic errNoIPv4 the old implementation returned after discarding it. This
// FAILS on the old code (which returned errNoIPv4 / "no usable IP family").
func TestDialPreferIPv4_ExplicitTCP4Refused(t *testing.T) {
	ctx := dialTestCtx(t)
	addr := closedLoopbackAddr(t)

	conn, err := dialPreferIPv4(ctx, "tcp4", addr)
	if err == nil {
		conn.Close()
		t.Fatalf("dialPreferIPv4(tcp4, %s) = (conn, nil), want a dial error", addr)
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no usable ip family") {
		t.Errorf("error is the synthetic errNoIPv4, not the real dial error: %q", err.Error())
	}
	if !strings.Contains(msg, "refused") {
		t.Errorf("error = %q, want it to mention the connection being refused", err.Error())
	}
}

// Case 6: an IPv6 literal with both families dead must surface the readable IPv6
// error, not the tcp4 "no suitable address found" family error. This is the
// mirror of Case 1 and is the regression guard for the asymmetry that surfaced
// err4 for every dual-family failure.
func TestDialPreferIPv4_IPv6LiteralErrorNotMasked(t *testing.T) {
	// Skip where IPv6 loopback is unavailable: without a listener the error text
	// would be a family/routing error, not "refused".
	l, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback unavailable in this environment")
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil { // released => (almost certainly) nothing listening
		t.Fatalf("failed to release reserved IPv6 port: %v", err)
	}

	ctx := dialTestCtx(t)
	conn, err := dialPreferIPv4(ctx, "tcp", addr)
	if err == nil {
		conn.Close()
		t.Fatalf("dialPreferIPv4(tcp, %s) = (conn, nil), want a dial error", addr)
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no suitable address found") {
		t.Errorf("IPv6 error was masked by the tcp4 family error: %q", err.Error())
	}
	if !strings.Contains(msg, "refused") {
		t.Errorf("error = %q, want it to mention the connection being refused", err.Error())
	}
}

// TestCloudflaredConfigHostnameSizeCap verifies the size guard in
// cloudflaredConfigHostname: an implausibly large config.yml must be refused
// outright rather than parsed from a truncated view (which could yield a
// half-formed hostname like "api.zuin"). The file here DOES contain a valid
// hostname, just far past the cap, so a missing hostname cannot explain a "".
// No qaDomainResetConfig here — it would repoint HOME away from the file.
func TestCloudflaredConfigHostnameSizeCap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir() reads USERPROFILE on Windows

	dir := filepath.Join(home, ".cloudflared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("ingress:\n")
	b.WriteString(strings.Repeat("# padding to exceed the cap\n", 3000)) // ~93 KB > 64 KB
	b.WriteString("  - hostname: api.zuinew.com\n")
	if b.Len() <= maxCloudflaredConfigBytes {
		t.Fatalf("test config too small (%d bytes); need > %d", b.Len(), maxCloudflaredConfigBytes)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := cloudflaredConfigHostname(); got != "" {
		t.Errorf("cloudflaredConfigHostname() = %q, want \"\" (oversized config must be refused, not half-parsed)", got)
	}
}
