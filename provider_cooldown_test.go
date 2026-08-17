package main

import (
	"strings"
	"testing"
	"time"
)

func TestIsRateLimitError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429 wrapped", &upstreamErr{code: 429, msg: "too many"}, true},
		{"429 in message", errString("upstream error (429): rate exceeded"), true},
		{"429 too many requests", errString("429 Too Many Requests"), true},
		{"rate limit text", errString("rate limit exceeded"), true},
		{"500", errString("upstream error (500): boom"), false},
		{"connection refused", errString("connection refused"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRateLimitError(c.err); got != c.want {
				t.Errorf("isRateLimitError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestFilterCooldownProviders_SkipsCooled(t *testing.T) {
	// reset global state
	cooldownMu.Lock()
	cooldownMap = make(map[string]time.Time)
	cooldownMu.Unlock()

	p1 := Provider{ID: "free-kilo-code"}
	p2 := Provider{ID: "free-ovhcloud"}
	cands := []candidate{{Provider: p1}, {Provider: p2}}

	// no cooldown yet -> all kept
	if got := filterCooldownProviders(cands); len(got) != 2 {
		t.Fatalf("expected 2 candidates with no cooldown, got %d", len(got))
	}

	// cool down p1 -> only p2 remains
	recordProviderCooldown("free-kilo-code")
	got := filterCooldownProviders(cands)
	if len(got) != 1 || got[0].Provider.ID != "free-ovhcloud" {
		t.Fatalf("expected only free-ovhcloud after cooldown, got %+v", got)
	}

	// all cooled -> keep original so caller reports a real error
	recordProviderCooldown("free-ovhcloud")
	got = filterCooldownProviders(cands)
	if len(got) != 2 {
		t.Fatalf("expected original candidates when all cooled, got %d", len(got))
	}
}

func TestProviderCooldown_Expires(t *testing.T) {
	cooldownMu.Lock()
	cooldownMap = make(map[string]time.Time)
	cooldownMu.Unlock()

	recordProviderCooldown("free-kilo-code")
	if !providerInCooldown("free-kilo-code") {
		t.Fatal("expected in cooldown right after record")
	}

	// force expiry
	cooldownMu.Lock()
	cooldownMap["free-kilo-code"] = time.Now().Add(-time.Second)
	cooldownMu.Unlock()
	if providerInCooldown("free-kilo-code") {
		t.Fatal("expected cooldown expired")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// upstreamErr mimics the wrapped upstream error shape from client.go.
type upstreamErr struct {
	code int
	msg  string
}

func (e *upstreamErr) Error() string {
	return "upstream error (" + intToStr(e.code) + "): " + e.msg
}

func intToStr(n int) string {
	var sb strings.Builder
	if n == 0 {
		return "0"
	}
	for n > 0 {
		sb.WriteByte(byte('0' + n%10))
		n /= 10
	}
	b := []byte(sb.String())
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}