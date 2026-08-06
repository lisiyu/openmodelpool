package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

// ============================================================
// Consumer API Key Hash Storage Tests (S-2)
// ============================================================

func TestConsumerSecurity_APIKeyHashed(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, err := multiUser.CreateConsumer("Alice", code)
	if err != nil {
		t.Fatalf("CreateConsumer error: %v", err)
	}

	// The apiKey in the returned consumer should be plaintext (for display)
	if !strings.HasPrefix(consumer.APIKey, "sk-") {
		t.Fatalf("returned API key should be plaintext with sk- prefix, got: %s", consumer.APIKey[:10])
	}

	// The apiKeyMap should store SHA-256 hashes, NOT plaintext
	expectedHash := hashAPIKey(consumer.APIKey)
	multiUser.mu.RLock()
	_, found := multiUser.apiKeyMap[expectedHash]
	multiUser.mu.RUnlock()
	if !found {
		t.Fatal("SHA-256 hash of API key should be in apiKeyMap")
	}

	// Verify plaintext is NOT in the map
	multiUser.mu.RLock()
	_, plainFound := multiUser.apiKeyMap[consumer.APIKey]
	multiUser.mu.RUnlock()
	if plainFound {
		t.Fatal("PLAINTEXT API key should NOT be in apiKeyMap - security issue!")
	}
}

func TestConsumerSecurity_HashLookup(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, err := multiUser.CreateConsumer("Bob", code)
	if err != nil {
		t.Fatalf("CreateConsumer error: %v", err)
	}

	// ValidateAPIKey should work with the plaintext key (hashes it internally)
	c, ok := multiUser.ValidateAPIKey(consumer.APIKey)
	if !ok {
		t.Fatal("ValidateAPIKey should succeed with correct API key")
	}
	if c.ID != consumer.ID {
		t.Fatalf("expected consumer ID %s, got %s", consumer.ID, c.ID)
	}
}

func TestConsumerSecurity_WrongKeyRejected(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	_, err := multiUser.CreateConsumer("Charlie", code)
	if err != nil {
		t.Fatalf("CreateConsumer error: %v", err)
	}

	// Wrong key should be rejected
	_, ok := multiUser.ValidateAPIKey("sk-wrong-key-12345678901234567890")
	if ok {
		t.Fatal("wrong API key should be rejected")
	}
}

func TestConsumerSecurity_MultipleConsumersHashed(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	// Create multiple consumers
	var consumers []*Consumer
	for i := 0; i < 5; i++ {
		code := multiUser.CreateInviteCode(0, "")
		c, err := multiUser.CreateConsumer(fmt.Sprintf("User%d", i), code)
		if err != nil {
			t.Fatalf("CreateConsumer error: %v", err)
		}
		consumers = append(consumers, c)
	}

	// Each key should validate correctly
	for i, c := range consumers {
		found, ok := multiUser.ValidateAPIKey(c.APIKey)
		if !ok {
			t.Fatalf("consumer %d API key should validate", i)
		}
		if found.ID != c.ID {
			t.Fatalf("consumer %d: expected ID %s, got %s", i, c.ID, found.ID)
		}
	}

	// All hashes should be in the map, no plaintext
	multiUser.mu.RLock()
	for key := range multiUser.apiKeyMap {
		// SHA-256 hex digest is 64 characters
		if len(key) != 64 {
			t.Fatalf("apiKeyMap key should be SHA-256 hex (64 chars), got %d chars: %s", len(key), key[:10])
		}
	}
	multiUser.mu.RUnlock()
}

func TestConsumerSecurity_DeleteRemovesHash(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("DeleteMe", code)
	apiKey := consumer.APIKey
	expectedHash := hashAPIKey(apiKey)

	// Hash should exist
	multiUser.mu.RLock()
	_, exists := multiUser.apiKeyMap[expectedHash]
	multiUser.mu.RUnlock()
	if !exists {
		t.Fatal("hash should exist before deletion")
	}

	// Delete consumer
	if !multiUser.DeleteConsumer(consumer.ID) {
		t.Fatal("DeleteConsumer should succeed")
	}

	// Hash should be removed
	multiUser.mu.RLock()
	_, exists = multiUser.apiKeyMap[expectedHash]
	multiUser.mu.RUnlock()
	if exists {
		t.Fatal("hash should be removed after consumer deletion")
	}

	// API key should no longer validate
	_, ok := multiUser.ValidateAPIKey(apiKey)
	if ok {
		t.Fatal("deleted consumer's API key should not validate")
	}
}

func TestConsumerSecurity_PersistAndReload(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("PersistTest", code)
	apiKey := consumer.APIKey
	consumerID := consumer.ID

	// Save and reload
	multiUser.save()

	// Create a new manager and load from the same file
	newMgr := &MultiUserManager{
		invites:   make(map[string]*InviteCode),
		consumers: make(map[string]*Consumer),
		apiKeyMap: make(map[string]string),
		dataPath:  multiUser.dataPath,
	}
	newMgr.load()

	// The hash should be in the reloaded map
	expectedHash := hashAPIKey(apiKey)
	newMgr.mu.RLock()
	foundID, exists := newMgr.apiKeyMap[expectedHash]
	newMgr.mu.RUnlock()
	if !exists {
		t.Fatal("hash should exist after reload")
	}
	if foundID != consumerID {
		t.Fatalf("reloaded hash should map to %s, got %s", consumerID, foundID)
	}
}

func TestHashAPIKey_Consistency(t *testing.T) {
	// Same input should always produce the same hash
	key := "sk-testkey123456789012345678901234567890"
	h1 := hashAPIKey(key)
	h2 := hashAPIKey(key)
	if h1 != h2 {
		t.Fatal("hashAPIKey should be deterministic")
	}

	// Verify it matches manual SHA-256
	expected := sha256.Sum256([]byte(key))
	expectedHex := fmt.Sprintf("%x", expected[:])
	if h1 != expectedHex {
		t.Fatalf("hashAPIKey should match SHA-256 hex: expected %s, got %s", expectedHex, h1)
	}
}

func TestConsumerSecurity_BatchSave(t *testing.T) {
	env := setupTestEnv(t)
	_ = env

	code := multiUser.CreateInviteCode(0, "")
	consumer, _ := multiUser.CreateConsumer("BatchTest", code)

	// Record usage multiple times
	for i := 0; i < 15; i++ {
		multiUser.RecordConsumerUsage(consumer.ID, 100)
	}

	// Force flush
	multiUser.FlushSaves()

	// Verify usage was recorded
	multiUser.mu.RLock()
	c := multiUser.consumers[consumer.ID]
	totalTokens := c.TotalTokens
	totalReqs := c.TotalRequests
	multiUser.mu.RUnlock()

	if totalTokens != 1500 {
		t.Fatalf("expected 1500 total tokens, got %d", totalTokens)
	}
	if totalReqs != 15 {
		t.Fatalf("expected 15 total requests, got %d", totalReqs)
	}
}
