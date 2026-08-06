package main

import (
	"strings"
	"testing"
)

// ============================================================
// ValidateGuestKey Security Tests
// ============================================================

func TestValidateGuestKey_RejectsUnknownKey(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Initialize guest key store
	initGuestKeyStore(env.dir)

	// A key that is not in the store should be rejected (fail-closed)
	_, valid := ValidateGuestKey("sk-guest-unknown-node-abcdef1234567890")
	if valid {
		t.Fatal("unknown guest key should be rejected when store is initialized")
	}
}

func TestValidateGuestKey_RejectsNonGuestPrefix(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	initGuestKeyStore(env.dir)

	// Non-guest keys should be rejected
	tests := []string{
		"sk-abc123",
		"sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1",
		"pk-abc123",
		"",
		"random-string",
	}
	for _, key := range tests {
		_, valid := ValidateGuestKey(key)
		if valid {
			t.Fatalf("key %q should be rejected", key)
		}
	}
}

func TestValidateGuestKey_RejectsRevokedKey(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Initialize guest key store
	initGuestKeyStore(env.dir)

	// Generate a guest key
	nodeID := "mmx-testnode123"
	key, err := GenerateGuestKey(nodeID)
	if err != nil {
		t.Fatalf("failed to generate guest key: %v", err)
	}

	// Should be valid initially
	returnedNodeID, valid := ValidateGuestKey(key)
	if !valid {
		t.Fatal("newly generated guest key should be valid")
	}
	if returnedNodeID != nodeID {
		t.Fatalf("expected node_id %q, got %q", nodeID, returnedNodeID)
	}

	// Revoke the key
	if err := guestKeyStore.RevokeGuestKey(key); err != nil {
		t.Fatalf("failed to revoke key: %v", err)
	}

	// Should be rejected after revocation
	_, valid = ValidateGuestKey(key)
	if valid {
		t.Fatal("revoked guest key should be rejected")
	}
}

func TestValidateGuestKey_AcceptsValidKey(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	initGuestKeyStore(env.dir)

	nodeID := "mmx-validnode456"
	key, err := GenerateGuestKey(nodeID)
	if err != nil {
		t.Fatalf("failed to generate guest key: %v", err)
	}

	returnedNodeID, valid := ValidateGuestKey(key)
	if !valid {
		t.Fatal("valid guest key should be accepted")
	}
	if returnedNodeID != nodeID {
		t.Fatalf("expected node_id %q, got %q", nodeID, returnedNodeID)
	}
}

func TestValidateGuestKey_RejectsExpiredKey(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	initGuestKeyStore(env.dir)

	nodeID := "mmx-expirednode"
	opts := GuestKeyOptions{ExpDays: 0} // will manually set expiry
	key, err := GenerateGuestKey(nodeID, opts)
	if err != nil {
		t.Fatalf("failed to generate guest key: %v", err)
	}

	// Manually set expiry to the past
	guestKeyStore.mu.Lock()
	for _, rec := range guestKeyStore.keys {
		if rec.Key == key {
			rec.ExpiresAt = "2020-01-01T00:00:00Z" // past date
			break
		}
	}
	guestKeyStore.mu.Unlock()

	// Should be rejected (expired)
	_, valid := ValidateGuestKey(key)
	if valid {
		t.Fatal("expired guest key should be rejected")
	}
}

func TestValidateGuestKey_MalformedKeys(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	initGuestKeyStore(env.dir)

	malformed := []string{
		"sk-guest-",
		"sk-guest--abc",
		"sk-guest-node-",
		"sk-guest-",
	}
	for _, key := range malformed {
		_, valid := ValidateGuestKey(key)
		if valid {
			t.Fatalf("malformed key %q should be rejected", key)
		}
	}
}

// ============================================================
// ClassifyKey Additional Tests
// ============================================================

func TestClassifyKey_GuestKey(t *testing.T) {
	keyType := ClassifyKey("sk-guest-nodeid-abcdef")
	if keyType != KeyTypeGuest {
		t.Fatalf("expected KeyTypeGuest, got %v", keyType)
	}
}

func TestClassifyKey_ProxyKey(t *testing.T) {
	keyType := ClassifyKey("sk-abcdef123456")
	if keyType != KeyTypeProxy {
		t.Fatalf("expected KeyTypeProxy, got %v", keyType)
	}
}

// Ensure the key starts with sk-guest-
func TestGenerateGuestKey_Format(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	initGuestKeyStore(env.dir)

	key, err := GenerateGuestKey("mmx-test123")
	if err != nil {
		t.Fatalf("failed to generate guest key: %v", err)
	}
	if !strings.HasPrefix(key, "sk-guest-mmx-test123-") {
		t.Fatalf("guest key should have correct prefix, got: %s", key)
	}
}
