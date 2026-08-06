package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// ============================================================
// verifyPayloadSignature tests (pure function)
// ============================================================

func TestVerifyPayloadSignature_Valid(t *testing.T) {
	// Generate a test key pair
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	payload := FederationInvitePayload{
		NetworkID:  "0xabc",
		Inviter:    "mmx-INVITER",
		InviteePub: "key123",
		Endpoint:   "https://example.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInviteDirected,
		CreatedAt:  "2026-01-01T00:00:00Z",
	}

	// Sign payload
	payloadBytes, _ := json.Marshal(payload)
	h := sha256.Sum256(payloadBytes)
	sig := ed25519.Sign(priv, h[:])

	if !verifyPayloadSignature(payload, sig, pub) {
		t.Error("verifyPayloadSignature should return true for valid signature")
	}
}

func TestVerifyPayloadSignature_WrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	pub2, _, _ := ed25519.GenerateKey(nil)

	payload := FederationInvitePayload{
		NetworkID:  "0xabc",
		Inviter:    "mmx-INVITER",
		InviteePub: "key123",
		Endpoint:   "https://example.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInviteDirected,
		CreatedAt:  "2026-01-01T00:00:00Z",
	}

	payloadBytes, _ := json.Marshal(payload)
	h := sha256.Sum256(payloadBytes)
	sig := ed25519.Sign(priv, h[:])

	if verifyPayloadSignature(payload, sig, pub2) {
		t.Error("verifyPayloadSignature should return false for wrong public key")
	}
}

func TestVerifyPayloadSignature_TamperedPayload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)

	payload := FederationInvitePayload{
		NetworkID:  "0xabc",
		Inviter:    "mmx-INVITER",
		InviteePub: "key123",
		Endpoint:   "https://example.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInviteDirected,
		CreatedAt:  "2026-01-01T00:00:00Z",
	}

	payloadBytes, _ := json.Marshal(payload)
	h := sha256.Sum256(payloadBytes)
	sig := ed25519.Sign(priv, h[:])

	// Tamper with payload
	tamperedPayload := payload
	tamperedPayload.InviteePub = "different-key"

	if verifyPayloadSignature(tamperedPayload, sig, pub) {
		t.Error("verifyPayloadSignature should return false for tampered payload")
	}
}

func TestVerifyPayloadSignature_InvalidKeySize(t *testing.T) {
	payload := FederationInvitePayload{
		NetworkID:  "0xabc",
		Inviter:    "mmx-INVITER",
		InviteePub: "key123",
		Endpoint:   "https://example.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInviteDirected,
		CreatedAt:  "2026-01-01T00:00:00Z",
	}

	wrongKey := []byte{1, 2, 3}
	wrongSig := []byte{4, 5, 6}
	if verifyPayloadSignature(payload, wrongSig, wrongKey) {
		t.Error("verifyPayloadSignature should return false for invalid key size")
	}
}

// ============================================================
// EncodeInvite / DecodeInvite tests
// ============================================================

func TestEncodeDecodeInvite_Roundtrip(t *testing.T) {
	invite := &FederationInvite{
		NetworkID:   "0xabc123",
		Inviter:     "mmx-INVITER01",
		InviterKey:  "base64pubkeyhere=",
		InviteePub:  "key123",
		InviteeName: "Test User",
		Endpoint:    "https://example.com",
		ExpiresAt:   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
		Type:        FederationInviteDirected,
		CreatedAt:   time.Now().Format(time.RFC3339),
		Signature:   "base64signature=",
	}

	encoded, err := EncodeInvite(invite)
	if err != nil {
		t.Fatalf("EncodeInvite error: %v", err)
	}
	if encoded == "" {
		t.Error("EncodeInvite returned empty string")
	}

	decoded, err := DecodeInvite(encoded)
	if err != nil {
		t.Fatalf("DecodeInvite error: %v", err)
	}

	if decoded.NetworkID != invite.NetworkID {
		t.Errorf("NetworkID mismatch: got %q, want %q", decoded.NetworkID, invite.NetworkID)
	}
	if decoded.Inviter != invite.Inviter {
		t.Errorf("Inviter mismatch: got %q, want %q", decoded.Inviter, invite.Inviter)
	}
	if decoded.InviteePub != invite.InviteePub {
		t.Errorf("InviteePub mismatch: got %q, want %q", decoded.InviteePub, invite.InviteePub)
	}
	if decoded.Type != invite.Type {
		t.Errorf("Type mismatch: got %q, want %q", decoded.Type, invite.Type)
	}
}

func TestDecodeInvite_InvalidBase64(t *testing.T) {
	_, err := DecodeInvite("!!!not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestDecodeInvite_InvalidJSON(t *testing.T) {
	// Valid base64 but invalid JSON
	invalidJSON := base64.URLEncoding.EncodeToString([]byte("not json"))
	_, err := DecodeInvite(invalidJSON)
	if err == nil {
		t.Error("expected error for invalid JSON in encoded invite")
	}
}

func TestDecodeInvite_StdEncodingFallback(t *testing.T) {
	invite := &FederationInvite{
		NetworkID:  "0xtest123",
		Inviter:    "mmx-TESTER",
		InviterKey: "keybase64=",
		InviteePub: "*",
		Endpoint:   "https://test.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInvitePublic,
		CreatedAt:  "2026-01-01T00:00:00Z",
		Signature:  "sigbase64=",
	}

	// Use standard encoding (not URL encoding) for fallback test
	payloadBytes, _ := json.Marshal(invite)
	encoded := base64.StdEncoding.EncodeToString(payloadBytes)

	decoded, err := DecodeInvite(encoded)
	if err != nil {
		t.Fatalf("DecodeInvite with std encoding fallback error: %v", err)
	}
	if decoded.NetworkID != invite.NetworkID {
		t.Errorf("NetworkID mismatch: got %q", decoded.NetworkID)
	}
}

// ============================================================
// FederationInvite type constants
// ============================================================

func TestFederationInviteType_Constants(t *testing.T) {
	if FederationInviteDirected != "directed" {
		t.Errorf("FederationInviteDirected = %q, want 'directed'", FederationInviteDirected)
	}
	if FederationInvitePublic != "public" {
		t.Errorf("FederationInvitePublic = %q, want 'public'", FederationInvitePublic)
	}
	if FederationInviteChain != "chain" {
		t.Errorf("FederationInviteChain = %q, want 'chain'", FederationInviteChain)
	}
}

// ============================================================
// inviteManager helpers (pure function tests)
// ============================================================

func TestInviteID_Deterministic(t *testing.T) {
	payload := FederationInvitePayload{
		NetworkID:  "0xabc",
		Inviter:    "mmx-INVITER",
		InviteePub: "key123",
		Endpoint:   "https://example.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInviteDirected,
		CreatedAt:  "2026-01-01T00:00:00Z",
	}

	m := &inviteManager{
		issued: make(map[string]*FederationInvite),
		used:   make(map[string]bool),
	}

	id1 := m.inviteID(payload)
	id2 := m.inviteID(payload)

	if id1 != id2 {
		t.Errorf("inviteID should be deterministic: %s vs %s", id1, id2)
	}
	if len(id1) < 4 {
		t.Errorf("inviteID too short: %s", id1)
	}
}

func TestInviteID_DifferentPayloads(t *testing.T) {
	payload1 := FederationInvitePayload{
		NetworkID:  "0xabc",
		Inviter:    "mmx-A",
		InviteePub: "key1",
		Endpoint:   "https://a.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInviteDirected,
		CreatedAt:  "2026-01-01T00:00:00Z",
	}
	payload2 := FederationInvitePayload{
		NetworkID:  "0xabc",
		Inviter:    "mmx-B",
		InviteePub: "key2",
		Endpoint:   "https://b.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInviteDirected,
		CreatedAt:  "2026-01-01T00:00:00Z",
	}

	m := &inviteManager{
		issued: make(map[string]*FederationInvite),
		used:   make(map[string]bool),
	}

	id1 := m.inviteID(payload1)
	id2 := m.inviteID(payload2)

	if id1 == id2 {
		t.Errorf("different payloads should produce different invite IDs: both = %s", id1)
	}
}

func TestInviteIDFromCode(t *testing.T) {
	invite := &FederationInvite{
		NetworkID:  "0xabc",
		Inviter:    "mmx-INV",
		InviteePub: "key123",
		Endpoint:   "https://example.com",
		ExpiresAt:  "2027-01-01T00:00:00Z",
		Type:       FederationInviteDirected,
		CreatedAt:  "2026-01-01T00:00:00Z",
	}

	m := &inviteManager{
		issued: make(map[string]*FederationInvite),
		used:   make(map[string]bool),
	}

	id := m.inviteIDFromCode(invite)
	if id == "" {
		t.Error("inviteIDFromCode returned empty string")
	}
	if id[:4] != "inv-" {
		t.Errorf("inviteID should start with 'inv-', got %q", id)
	}
}
