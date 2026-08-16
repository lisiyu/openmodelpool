package main

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// attachFederationAuth should always set X-Node-ID and X-Node-Timestamp, and
// additionally set X-Federation-Secret when a federation_secret is configured.
func TestAttachFederationAuth_SecretConfigured(t *testing.T) {
	env := setupTestEnv(t)
	node = newTestNodeIdentity(t, env.dir)
	t.Cleanup(func() { node = nil })

	env.cfgInst.Set("federation_secret", "s3cret-shared")
	time.Sleep(200 * time.Millisecond)

	req, err := http.NewRequest(http.MethodPost, "http://peer/api/federation/gossip", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	attachFederationAuth(req)

	if got := req.Header.Get("X-Node-ID"); got == "" {
		t.Error("X-Node-ID not set")
	}
	if got := req.Header.Get("X-Federation-Secret"); got != "s3cret-shared" {
		t.Errorf("X-Federation-Secret = %q, want %q", got, "s3cret-shared")
	}
	ts := req.Header.Get("X-Node-Timestamp")
	if ts == "" {
		t.Fatal("X-Node-Timestamp not set")
	}
	if n, err := strconv.ParseInt(ts, 10, 64); err != nil {
		t.Errorf("X-Node-Timestamp not numeric: %v", err)
	} else if n < time.Now().Add(-time.Minute).Unix() {
		t.Errorf("X-Node-Timestamp too old: %d", n)
	}
}

// With no federation_secret configured the secret header must be absent so a
// receiving node's path-3 comparison still has a chance to reject cleanly,
// and the header never leaks a literal "".
func TestAttachFederationAuth_NoSecret(t *testing.T) {
	env := setupTestEnv(t)
	node = newTestNodeIdentity(t, env.dir)
	t.Cleanup(func() { node = nil })

	req, err := http.NewRequest(http.MethodPost, "http://peer/api/federation/pool", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	attachFederationAuth(req)

	if got := req.Header.Get("X-Federation-Secret"); got != "" {
		t.Errorf("X-Federation-Secret = %q, want empty when unconfigured", got)
	}
	if got := req.Header.Get("X-Node-ID"); got == "" {
		t.Error("X-Node-ID should still be set")
	}
}
