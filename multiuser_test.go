package main

import (
	"strings"
	"testing"
)

func TestMultiUser_CreateInviteCode(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "") // single use
	if code == "" {
		t.Fatal("CreateInviteCode returned empty string")
	}
	if len(code) != 12 {
		t.Fatalf("expected 12-char code, got %d: %s", len(code), code)
	}

	// Should be valid
	if !multiUser.ValidateInviteCode(code) {
		t.Fatal("new invite code should be valid")
	}
}

func TestMultiUser_CreateInviteCode_MultiUse(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(3, "") // 3 uses

	for i := 0; i < 3; i++ {
		if !multiUser.ValidateInviteCode(code) {
			t.Fatalf("invite code should be valid (use %d/3)", i+1)
		}
		// Simulate use by a consumer
		multiUser.mu.Lock()
		multiUser.invites[code].UseCount++
		multiUser.mu.Unlock()
	}

	// 4th use should fail validation
	if multiUser.ValidateInviteCode(code) {
		t.Fatal("invite code should be invalid after max uses reached")
	}
}

func TestMultiUser_ValidateInviteCode_Invalid(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	if multiUser.ValidateInviteCode("nonexistent-code") {
		t.Fatal("nonexistent code should be invalid")
	}
	if multiUser.ValidateInviteCode("") {
		t.Fatal("empty code should be invalid")
	}
}

func TestMultiUser_UseInviteCode(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "") // single use

	if !multiUser.UseInviteCode(code, "consumer-1") {
		t.Fatal("UseInviteCode should succeed for valid code")
	}

	// Should now be invalid (single use already consumed)
	if multiUser.ValidateInviteCode(code) {
		t.Fatal("single-use code should be invalid after first use")
	}

	// Second use should fail
	if multiUser.UseInviteCode(code, "consumer-2") {
		t.Fatal("UseInviteCode should fail for already-used code")
	}
}

func TestMultiUser_UseInviteCode_InvalidCode(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	if multiUser.UseInviteCode("bad-code", "consumer-1") {
		t.Fatal("UseInviteCode should fail for nonexistent code")
	}
}

func TestMultiUser_CreateConsumer(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, err := multiUser.CreateConsumer("Alice", code)
	if err != nil {
		t.Fatalf("CreateConsumer error: %v", err)
	}
	if consumer.ID == "" {
		t.Fatal("consumer ID should not be empty")
	}
	if !strings.HasPrefix(consumer.ID, "consumer-") {
		t.Fatalf("consumer ID should start with 'consumer-', got: %s", consumer.ID)
	}
	if !strings.HasPrefix(consumer.APIKey, "sk-") {
		t.Fatalf("API key should start with 'sk-', got: %s", consumer.APIKey)
	}
	if len(consumer.APIKey) != 3+48 { // "sk-" + 48 chars
		t.Fatalf("API key length should be 51, got %d", len(consumer.APIKey))
	}
	if consumer.Name != "Alice" {
		t.Fatalf("name should be Alice, got %s", consumer.Name)
	}
	if !consumer.Enabled {
		t.Fatal("new consumer should be enabled")
	}
	if consumer.InviteCode != code {
		t.Fatal("invite code should be recorded on consumer")
	}
}

func TestMultiUser_CreateConsumer_InvalidInvite(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	_, err := multiUser.CreateConsumer("Bob", "invalid-code")
	if err == nil {
		t.Fatal("CreateConsumer should fail with invalid invite code")
	}
}

func TestMultiUser_CreateConsumer_UsedInvite(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	_, err := multiUser.CreateConsumer("First", code)
	if err != nil {
		t.Fatalf("first consumer creation should succeed: %v", err)
	}

	_, err = multiUser.CreateConsumer("Second", code)
	if err == nil {
		t.Fatal("second consumer with same single-use invite should fail")
	}
}

func TestMultiUser_ValidateAPIKey(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("Alice", code)

	// Valid key
	c, ok := multiUser.ValidateAPIKey(consumer.APIKey)
	if !ok {
		t.Fatal("valid API key should authenticate")
	}
	if c.ID != consumer.ID {
		t.Fatalf("expected consumer %s, got %s", consumer.ID, c.ID)
	}

	// Invalid key
	_, ok = multiUser.ValidateAPIKey("sk-bad-key")
	if ok {
		t.Fatal("invalid API key should not authenticate")
	}
}

func TestMultiUser_ValidateAPIKey_DisabledConsumer(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("Alice", code)

	// Disable consumer
	multiUser.ToggleConsumer(consumer.ID, false)

	// API key should no longer validate
	_, ok := multiUser.ValidateAPIKey(consumer.APIKey)
	if ok {
		t.Fatal("disabled consumer's API key should not authenticate")
	}

	// Re-enable
	multiUser.ToggleConsumer(consumer.ID, true)

	// Should work again
	_, ok = multiUser.ValidateAPIKey(consumer.APIKey)
	if !ok {
		t.Fatal("re-enabled consumer's API key should authenticate")
	}
}

func TestMultiUser_RecordConsumerUsage(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("Alice", code)

	multiUser.RecordConsumerUsage(consumer.ID, 500)
	multiUser.RecordConsumerUsage(consumer.ID, 300)

	multiUser.mu.RLock()
	c := multiUser.consumers[consumer.ID]
	multiUser.mu.RUnlock()

	if c.TotalTokens != 800 {
		t.Fatalf("expected 800 total tokens, got %d", c.TotalTokens)
	}
	if c.TotalRequests != 2 {
		t.Fatalf("expected 2 total requests, got %d", c.TotalRequests)
	}
}

func TestMultiUser_DeleteConsumer(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("Alice", code)

	if !multiUser.DeleteConsumer(consumer.ID) {
		t.Fatal("DeleteConsumer should return true")
	}
	if multiUser.DeleteConsumer(consumer.ID) {
		t.Fatal("DeleteConsumer should return false for already-deleted consumer")
	}

	// API key should no longer work
	_, ok := multiUser.ValidateAPIKey(consumer.APIKey)
	if ok {
		t.Fatal("deleted consumer's API key should not authenticate")
	}
}

func TestMultiUser_DeleteConsumer_RemovesProviders(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("Alice", code)

	// Add a provider owned by this consumer
	p := makeProvider("cons-p1", "Consumer Provider", makeModelDef("m1"), 1, true)
	p.Owner = consumer.ID
	pm.Add(p)

	if _, ok := pm.GetRaw("cons-p1"); !ok {
		t.Fatal("provider should exist before consumer deletion")
	}

	// Delete consumer should also remove their providers
	multiUser.DeleteConsumer(consumer.ID)

	if _, ok := pm.GetRaw("cons-p1"); ok {
		t.Fatal("consumer's provider should be deleted when consumer is deleted")
	}
}

func TestMultiUser_ToggleConsumer(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("Alice", code)

	if !multiUser.ToggleConsumer(consumer.ID, false) {
		t.Fatal("ToggleConsumer should succeed")
	}
	if multiUser.ToggleConsumer("nonexistent", true) {
		t.Fatal("ToggleConsumer should fail for nonexistent consumer")
	}
}

func TestMultiUser_DeleteInviteCode(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")

	if !multiUser.DeleteInviteCode(code) {
		t.Fatal("DeleteInviteCode should return true")
	}
	if multiUser.DeleteInviteCode(code) {
		t.Fatal("DeleteInviteCode should return false for already-deleted code")
	}
	if multiUser.ValidateInviteCode(code) {
		t.Fatal("deleted invite code should not be valid")
	}
}

func TestMultiUser_ListInviteCodes(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	multiUser.CreateInviteCode(0, "")
	multiUser.CreateInviteCode(5, "")
	multiUser.CreateInviteCode(0, "")

	codes := multiUser.ListInviteCodes()
	if len(codes) != 3 {
		t.Fatalf("expected 3 invite codes, got %d", len(codes))
	}
}

func TestMultiUser_ListConsumers_MasksAPIKey(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("Alice", code)

	consumers := multiUser.ListConsumers()
	if len(consumers) != 1 {
		t.Fatalf("expected 1 consumer, got %d", len(consumers))
	}
	if consumers[0].APIKey == consumer.APIKey {
		t.Fatal("ListConsumers should mask API key")
	}
	if !strings.Contains(consumers[0].APIKey, "...") {
		t.Fatalf("masked key should contain '...', got: %s", consumers[0].APIKey)
	}
}

func TestMultiUser_GetConsumerFull(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("Alice", code)

	full, ok := multiUser.GetConsumerFull(consumer.ID)
	if !ok {
		t.Fatal("GetConsumerFull should return true for existing consumer")
	}
	if full.APIKey != consumer.APIKey {
		t.Fatal("GetConsumerFull should return unmasked API key")
	}

	_, ok = multiUser.GetConsumerFull("nonexistent")
	if ok {
		t.Fatal("GetConsumerFull should return false for nonexistent consumer")
	}
}

func TestMultiUser_ProviderOwnershipIsolation(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Create two consumers
	code1 := multiUser.CreateInviteCode(0, "")
	c1, _ := multiUser.CreateConsumer("User1", code1)

	code2 := multiUser.CreateInviteCode(0, "")
	c2, _ := multiUser.CreateConsumer("User2", code2)

	// Add providers for each
	p1 := makeProvider("iso-p1", "P1", makeModelDef("m1"), 1, true)
	p1.Owner = c1.ID
	pm.Add(p1)

	p2 := makeProvider("iso-p2", "P2", makeModelDef("m2"), 1, true)
	p2.Owner = c2.ID
	pm.Add(p2)

	// Delete consumer 1's providers
	deleted := pm.DeleteByOwner(c1.ID)
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// Consumer 2's provider should still exist
	if _, ok := pm.GetRaw("iso-p2"); !ok {
		t.Fatal("consumer 2's provider should not be affected")
	}
	if _, ok := pm.GetRaw("iso-p1"); ok {
		t.Fatal("consumer 1's provider should be deleted")
	}
}
