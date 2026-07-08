package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// ClassifyKey Tests
// ============================================================

func TestClassifyKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected KeyType
	}{
		// Public key
		{"exact public key", PublicKeyValue, KeyTypePublic},
		{"public key full string", "sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1", KeyTypePublic},

		// Guest keys
		{"guest key basic", "sk-guest-node1-abc123", KeyTypeGuest},
		{"guest key with long random", "sk-guest-mmx-abc123-" + strings.Repeat("a", 32), KeyTypeGuest},
		{"guest key minimal", "sk-guest-a-b", KeyTypeGuest},
		{"guest key with dashes in node id", "sk-guest-mmx-node-1-abcdef", KeyTypeGuest},

		// Proxy keys
		{"proxy key basic", "sk-abc123def456", KeyTypeProxy},
		{"proxy key short", "sk-x", KeyTypeProxy},
		{"proxy key long", "sk-" + strings.Repeat("z", 100), KeyTypeProxy},
		{"proxy key with numbers", "sk-1234567890", KeyTypeProxy},

		// Unknown keys
		{"empty string", "", KeyTypeUnknown},
		{"no prefix", "abc123", KeyTypeUnknown},
		{"wrong prefix", "pk-abc123", KeyTypeUnknown},
		{"bearer token style", "Bearer sk-abc", KeyTypeUnknown},
		{"just sk", "sk", KeyTypeUnknown},
		{"sk- prefix missing", "s-abc", KeyTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyKey(tt.key)
			if result != tt.expected {
				t.Errorf("ClassifyKey(%q) = %q, want %q", tt.key, result, tt.expected)
			}
		})
	}
}

func TestClassifyKeyPriority(t *testing.T) {
	// Public key must be checked before proxy prefix
	// Since PublicKeyValue starts with "sk-", order matters
	if ClassifyKey(PublicKeyValue) != KeyTypePublic {
		t.Error("public key must be classified as public, not proxy")
	}

	// Guest key must be checked before proxy prefix
	guestKey := "sk-guest-node1-abc"
	if ClassifyKey(guestKey) != KeyTypeGuest {
		t.Error("guest key must be classified as guest, not proxy")
	}
}

// ============================================================
// PublicKeyValue Constant Tests
// ============================================================

func TestPublicKeyValue(t *testing.T) {
	if PublicKeyValue == "" {
		t.Error("PublicKeyValue should not be empty")
	}
	if !strings.HasPrefix(PublicKeyValue, "sk-") {
		t.Error("PublicKeyValue should start with sk-")
	}
	if ClassifyKey(PublicKeyValue) != KeyTypePublic {
		t.Error("PublicKeyValue must classify as KeyTypePublic")
	}
}

// ============================================================
// GuestKeyStore Tests
// ============================================================

func setupGuestKeyStore(t *testing.T) (*GuestKeyStore, string) {
	t.Helper()
	tmpDir := t.TempDir()
	store := &GuestKeyStore{
		keys:     make([]*GuestKeyRecord, 0),
		dataPath: filepath.Join(tmpDir, "guest_keys.json"),
	}
	return store, tmpDir
}

func TestGuestKeyStore_GenerateGuestKey(t *testing.T) {
	store, _ := setupGuestKeyStore(t)
	oldStore := guestKeyStore
	guestKeyStore = store
	defer func() { guestKeyStore = oldStore }()

	tests := []struct {
		name    string
		nodeID  string
		wantErr bool
	}{
		{"valid node id", "mmx-node1", false},
		{"another valid", "mmx-abc123", false},
		{"empty node id", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := GenerateGuestKey(tt.nodeID)
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateGuestKey(%q) error = %v, wantErr %v", tt.nodeID, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if !strings.HasPrefix(key, "sk-guest-") {
					t.Errorf("key should start with sk-guest-, got %q", key)
				}
				if !strings.Contains(key, tt.nodeID) {
					t.Errorf("key should contain nodeID %q, got %q", tt.nodeID, key)
				}
			}
		})
	}
}

func TestGuestKeyStore_GenerateGuestKeyUnique(t *testing.T) {
	store, _ := setupGuestKeyStore(t)
	oldStore := guestKeyStore
	guestKeyStore = store
	defer func() { guestKeyStore = oldStore }()

	key1, err := GenerateGuestKey("mmx-node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	key2, err := GenerateGuestKey("mmx-node1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key1 == key2 {
		t.Error("two generated keys should be different")
	}
}

func TestGuestKeyStore_ValidateGuestKey(t *testing.T) {
	store, _ := setupGuestKeyStore(t)
	oldStore := guestKeyStore
	guestKeyStore = store
	defer func() { guestKeyStore = oldStore }()

	// Generate a key first
	key, _ := GenerateGuestKey("mmx-node1")

	tests := []struct {
		name     string
		key      string
		wantNode string
		wantValid bool
	}{
		{"valid generated key", key, "mmx-node1", true},
		{"valid format unknown key", "sk-guest-mmx-unknown-abc123", "mmx-unknown", true},
		{"not guest key", "sk-abc123", "", false},
		{"empty key", "", "", false},
		{"guest key no random part", "sk-guest-node1", "", false},
		{"guest key empty node", "sk-guest--abc", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeID, valid := ValidateGuestKey(tt.key)
			if valid != tt.wantValid {
				t.Errorf("ValidateGuestKey(%q) valid = %v, want %v", tt.key, valid, tt.wantValid)
			}
			if valid && nodeID != tt.wantNode {
				t.Errorf("ValidateGuestKey(%q) nodeID = %q, want %q", tt.key, nodeID, tt.wantNode)
			}
		})
	}
}

func TestGuestKeyStore_RevokeGuestKey(t *testing.T) {
	store, _ := setupGuestKeyStore(t)

	key, _ := GenerateGuestKey("mmx-node1")
	
	// Key should be valid before revocation
	if _, valid := ValidateGuestKey(key); !valid {
		t.Fatal("key should be valid before revocation")
	}

	// Revoke
	if err := store.RevokeGuestKey(key); err != nil {
		t.Fatalf("RevokeGuestKey failed: %v", err)
	}

	// Key should be invalid after revocation
	if _, valid := ValidateGuestKey(key); valid {
		t.Error("key should be invalid after revocation")
	}
}

func TestGuestKeyStore_RevokeNonexistent(t *testing.T) {
	store, _ := setupGuestKeyStore(t)
	err := store.RevokeGuestKey("sk-guest-nonexistent-abc")
	if err == nil {
		t.Error("revoking nonexistent key should return error")
	}
}

func TestGuestKeyStore_GetAllGuestKeys(t *testing.T) {
	store, _ := setupGuestKeyStore(t)

	// Empty store
	keys := store.GetAllGuestKeys()
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}

	// Add some keys
	GenerateGuestKey("mmx-node1")
	GenerateGuestKey("mmx-node2")
	GenerateGuestKey("mmx-node3")

	keys = store.GetAllGuestKeys()
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}

	// Verify returned slice is a copy
	keys[0] = nil
	keys2 := store.GetAllGuestKeys()
	if keys2[0] == nil {
		t.Error("GetAllGuestKeys should return a copy, not original slice")
	}
}

func TestGuestKeyStore_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	dataPath := filepath.Join(tmpDir, "guest_keys.json")

	// Create store and add keys
	store1 := &GuestKeyStore{
		keys:     make([]*GuestKeyRecord, 0),
		dataPath: dataPath,
	}
	oldStore := guestKeyStore
	guestKeyStore = store1

	key1, _ := GenerateGuestKey("mmx-node1")
	key2, _ := GenerateGuestKey("mmx-node2")

	// Verify file was created
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		t.Fatal("save should create the file")
	}

	// Load into new store
	store2 := &GuestKeyStore{
		keys:     make([]*GuestKeyRecord, 0),
		dataPath: dataPath,
	}
	store2.load()

	guestKeyStore = oldStore

	if len(store2.keys) != 2 {
		t.Fatalf("expected 2 keys after load, got %d", len(store2.keys))
	}
	if store2.keys[0].Key != key1 {
		t.Errorf("first key mismatch: got %q, want %q", store2.keys[0].Key, key1)
	}
	if store2.keys[1].Key != key2 {
		t.Errorf("second key mismatch: got %q, want %q", store2.keys[1].Key, key2)
	}
}

func TestGuestKeyStore_LoadInvalidFile(t *testing.T) {
	tmpDir := t.TempDir()
	dataPath := filepath.Join(tmpDir, "guest_keys.json")
	
	// Write invalid JSON
	os.WriteFile(dataPath, []byte("not json"), 0600)

	store := &GuestKeyStore{
		keys:     make([]*GuestKeyRecord, 0),
		dataPath: dataPath,
	}
	store.load() // should not panic

	if len(store.keys) != 0 {
		t.Error("keys should be empty after loading invalid file")
	}
}

func TestGuestKeyStore_LoadNonexistentFile(t *testing.T) {
	store := &GuestKeyStore{
		keys:     make([]*GuestKeyRecord, 0),
		dataPath: "/nonexistent/path/guest_keys.json",
	}
	store.load() // should not panic

	if len(store.keys) != 0 {
		t.Error("keys should be empty when file doesn't exist")
	}
}

// ============================================================
// KeyType String Tests
// ============================================================

func TestKeyTypeValues(t *testing.T) {
	tests := []struct {
		keyType KeyType
		value   string
	}{
		{KeyTypeProxy, "proxy"},
		{KeyTypeGuest, "guest"},
		{KeyTypePublic, "public"},
		{KeyTypeUnknown, "unknown"},
	}
	for _, tt := range tests {
		if string(tt.keyType) != tt.value {
			t.Errorf("KeyType %v should equal %q", tt.keyType, tt.value)
		}
	}
}
