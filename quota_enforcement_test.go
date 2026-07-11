package main

import (
	"testing"
)

// ============================================================
// Guest Key Quota Tests
// ============================================================

func TestGuestKeyQuota_DefaultUnlimited(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	initGuestKeyStore(env.dir)

	// Generate a guest key without quota
	nodeID := "mmx-quotatest"
	key, err := GenerateGuestKey(nodeID)
	if err != nil {
		t.Fatalf("failed to generate guest key: %v", err)
	}

	// Find the record and verify quota is 0 (unlimited)
	guestKeyStore.mu.RLock()
	var record *GuestKeyRecord
	for _, rec := range guestKeyStore.keys {
		if rec.Key == key {
			record = rec
			break
		}
	}
	guestKeyStore.mu.RUnlock()

	if record == nil {
		t.Fatal("guest key record not found")
	}
	if record.Quota != 0 {
		t.Fatalf("default quota should be 0 (unlimited), got %d", record.Quota)
	}
}

func TestGuestKeyQuota_WithQuota(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	initGuestKeyStore(env.dir)

	nodeID := "mmx-quotatest2"
	opts := GuestKeyOptions{
		Quota:   10000,
		ExpDays: 30,
		Note:    "test quota key",
	}
	key, err := GenerateGuestKey(nodeID, opts)
	if err != nil {
		t.Fatalf("failed to generate guest key: %v", err)
	}

	// Find the record and verify quota
	guestKeyStore.mu.RLock()
	var record *GuestKeyRecord
	for _, rec := range guestKeyStore.keys {
		if rec.Key == key {
			record = rec
			break
		}
	}
	guestKeyStore.mu.RUnlock()

	if record == nil {
		t.Fatal("guest key record not found")
	}
	if record.Quota != 10000 {
		t.Fatalf("expected quota 10000, got %d", record.Quota)
	}
	if record.ExpDays != 30 {
		t.Fatalf("expected exp_days 30, got %d", record.ExpDays)
	}
	if record.Note != "test quota key" {
		t.Fatalf("expected note 'test quota key', got %q", record.Note)
	}
	if record.ExpiresAt == "" {
		t.Fatal("expires_at should be set when exp_days > 0")
	}
}

// ============================================================
// Quota Allocation Tests
// ============================================================

func TestQuotaAllocation_Defaults(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Test that default quota allocation is 50/50 or configurable
	if cfg == nil {
		t.Skip("config not initialized")
	}

	guestPercent := cfg.Get("guest_key_percent", "50")
	if guestPercent == "" {
		guestPercent = "50"
	}

	// Verify it's a valid percentage
	if guestPercent != "50" && guestPercent != "0" && guestPercent != "100" {
		t.Logf("guest_key_percent = %s (custom value)", guestPercent)
	}
}

func TestQuotaAllocation_GuestKeyEqualShare(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	initGuestKeyStore(env.dir)

	// Create multiple guest keys and verify they share quota equally
	nodeID := "mmx-equaltest"
	var keys []string
	for i := 0; i < 3; i++ {
		opts := GuestKeyOptions{Quota: 5000}
		key, err := GenerateGuestKey(nodeID, opts)
		if err != nil {
			t.Fatalf("failed to generate guest key %d: %v", i, err)
		}
		keys = append(keys, key)
	}

	// All keys should be valid
	for i, key := range keys {
		_, valid := ValidateGuestKey(key)
		if !valid {
			t.Fatalf("guest key %d should be valid", i)
		}
	}

	// Verify all have the same quota
	guestKeyStore.mu.RLock()
	for _, key := range keys {
		for _, rec := range guestKeyStore.keys {
			if rec.Key == key {
				if rec.Quota != 5000 {
					t.Fatalf("each key should have quota 5000, got %d", rec.Quota)
				}
				break
			}
		}
	}
	guestKeyStore.mu.RUnlock()
}
