package main

import (
	crypto_rand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ============================================================
// Key System v2.0 — Simplified 4-Key Architecture
// ============================================================
//
// 1. Proxy API Key (sk-{random})         - Node operator's main key
// 2. Guest Proxy Key (sk-guest-{nid}.x)  - Node owner issues to others
// 3. Public Key (sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1)           - Fixed global constant, trial key
// 4. Provider Key (various formats)      - Upstream API keys (not managed here)

// KeyType represents the type of a key in v2.0 system.
type KeyType string

const (
	KeyTypeProxy   KeyType = "proxy"   // sk-{random} — node operator's key
	KeyTypeGuest   KeyType = "guest"   // sk-guest-{node_id}-{random}
	KeyTypePublic  KeyType = "public"  // sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1 — fixed global constant
	KeyTypeUnknown KeyType = "unknown"
)

// PublicKeyValue is the fixed constant for the global public trial key.
const PublicKeyValue = "sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1"

// ============================================================
// ClassifyKey — determines key type from its prefix/format
// ============================================================

// ClassifyKey determines the type of an API key.
func ClassifyKey(key string) KeyType {
	if key == PublicKeyValue {
		return KeyTypePublic
	}
	if strings.HasPrefix(key, "sk-guest-") {
		return KeyTypeGuest
	}
	if strings.HasPrefix(key, "sk-") {
		return KeyTypeProxy
	}
	return KeyTypeUnknown
}

// ============================================================
// Guest Key Management
// ============================================================

// GuestKeyRecord stores information about an issued guest key.
type GuestKeyRecord struct {
	Key        string `json:"key"`
	NodeID     string `json:"node_id"`     // issuer node ID
	RandomPart string `json:"random_part"` // the random portion after node_id
	IssuedAt   string `json:"issued_at"`
	Revoked    bool   `json:"revoked"`
	Quota      int64  `json:"quota,omitempty"`       // daily token quota (0=unlimited)
	ExpDays    int    `json:"exp_days,omitempty"`    // validity in days (0=never expires)
	ExpiresAt  string `json:"expires_at,omitempty"`  // expiry timestamp
	Note       string `json:"note,omitempty"`        // optional note/label
}

// GuestKeyStore manages all guest keys issued by this node.
type GuestKeyStore struct {
	mu       sync.RWMutex
	keys     []*GuestKeyRecord
	dataPath string
}

var guestKeyStore *GuestKeyStore

func initGuestKeyStore(dataDir string) {
	guestKeyStore = &GuestKeyStore{
		keys:     make([]*GuestKeyRecord, 0),
		dataPath: filepath.Join(dataDir, "guest_keys.json"),
	}
	guestKeyStore.load()
	slog.Info("guest key store initialized", "keys", len(guestKeyStore.keys))
}

func (gks *GuestKeyStore) load() {
	data, err := os.ReadFile(gks.dataPath)
	if err != nil {
		return
	}
	var records []*GuestKeyRecord
	if err := json.Unmarshal(data, &records); err != nil {
		slog.Warn("failed to parse guest keys", "error", err)
		return
	}
	gks.mu.Lock()
	defer gks.mu.Unlock()
	gks.keys = records
}

func (gks *GuestKeyStore) save() {
	gks.mu.RLock()
	defer gks.mu.RUnlock()
	data, err := json.MarshalIndent(gks.keys, "", "  ")
	if err != nil {
		slog.Error("failed to marshal guest keys", "error", err)
		return
	}
	os.MkdirAll(filepath.Dir(gks.dataPath), 0755)
	if err := os.WriteFile(gks.dataPath, data, 0600); err != nil {
		slog.Error("failed to write guest keys", "error", err)
	}
}

// GuestKeyOptions contains optional parameters for generating a guest key.
type GuestKeyOptions struct {
	Quota   int64  // daily token quota (0=unlimited)
	ExpDays int    // validity in days (0=never expires)
	Note    string // optional note/label
}

// GenerateGuestKey creates a new guest key for the given node.
// Format: sk-guest-{node_id}-{random_hex}
func GenerateGuestKey(nodeID string, opts ...GuestKeyOptions) (string, error) {
	if nodeID == "" {
		return "", fmt.Errorf("node_id is required")
	}

	randBytes := make([]byte, 16)
	if _, err := crypto_rand.Read(randBytes); err != nil {
		return "", fmt.Errorf("generate random: %w", err)
	}
	randomPart := hex.EncodeToString(randBytes)

	fullKey := fmt.Sprintf("sk-guest-%s-%s", nodeID, randomPart)

	// Build record
	record := &GuestKeyRecord{
		Key:        fullKey,
		NodeID:     nodeID,
		RandomPart: randomPart,
		IssuedAt:   time.Now().Format(time.RFC3339),
		Revoked:    false,
	}

	// Apply options if provided
	if len(opts) > 0 {
		opt := opts[0]
		record.Quota = opt.Quota
		record.ExpDays = opt.ExpDays
		record.Note = opt.Note
		if opt.ExpDays > 0 {
			expiresAt := time.Now().AddDate(0, 0, opt.ExpDays)
			record.ExpiresAt = expiresAt.Format(time.RFC3339)
		}
	}

	guestKeyStore.mu.Lock()
	guestKeyStore.keys = append(guestKeyStore.keys, record)
	guestKeyStore.mu.Unlock()
	guestKeyStore.save()

	slog.Info("generated guest key", "node_id", nodeID, "key_prefix", fullKey[:min(len(fullKey), 30)]+"...")
	return fullKey, nil
}

// ValidateGuestKey extracts the node_id from a guest key and checks validity.
// Returns the issuer node_id and whether the key is valid.
func ValidateGuestKey(key string) (nodeID string, valid bool) {
	if !strings.HasPrefix(key, "sk-guest-") {
		return "", false
	}

	rest := strings.TrimPrefix(key, "sk-guest-")
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}

	candidateNodeID := parts[0]

	// Check if the key has been revoked
	if guestKeyStore != nil {
		guestKeyStore.mu.RLock()
		for _, rec := range guestKeyStore.keys {
			if rec.Key == key {
				if rec.Revoked {
					guestKeyStore.mu.RUnlock()
					return "", false
				}
				guestKeyStore.mu.RUnlock()
				return rec.NodeID, true
			}
		}
		guestKeyStore.mu.RUnlock()
	}

	// Key not found in store but format is valid — accept it
	// (supports guest keys issued before this node started)
	return candidateNodeID, true
}

// RevokeGuestKey marks a guest key as revoked.
func (gks *GuestKeyStore) RevokeGuestKey(key string) error {
	gks.mu.Lock()
	defer gks.mu.Unlock()

	for _, rec := range gks.keys {
		if rec.Key == key {
			rec.Revoked = true
			gks.doSaveLocked()
			slog.Info("revoked guest key", "node_id", rec.NodeID)
			return nil
		}
	}
	return fmt.Errorf("guest key not found")
}

// GetAllGuestKeys returns all guest keys.
func (gks *GuestKeyStore) GetAllGuestKeys() []*GuestKeyRecord {
	gks.mu.RLock()
	defer gks.mu.RUnlock()
	result := make([]*GuestKeyRecord, len(gks.keys))
	copy(result, gks.keys)
	return result
}

func (gks *GuestKeyStore) doSaveLocked() {
	data, err := json.MarshalIndent(gks.keys, "", "  ")
	if err != nil {
		slog.Error("failed to marshal guest keys", "error", err)
		return
	}
	os.MkdirAll(filepath.Dir(gks.dataPath), 0755)
	if err := os.WriteFile(gks.dataPath, data, 0600); err != nil {
		slog.Error("failed to write guest keys", "error", err)
	}
}

// ============================================================
// API Handlers — Guest Keys
// ============================================================

// POST /api/network/guest-keys (JWT) — issue a new guest key
func handleGuestKeyIssue(w http.ResponseWriter, r *http.Request) {
	nodeID := netMgr.GetNodeID()
	if nodeID == "" {
		writeError(w, 400, "node not initialized")
		return
	}

	// Parse optional parameters from request body
	var opts GuestKeyOptions
	if r.Body != nil && r.ContentLength > 0 {
		var body struct {
			Quota   int64  `json:"quota"`
			ExpDays int    `json:"exp_days"`
			Note    string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			opts.Quota = body.Quota
			opts.ExpDays = body.ExpDays
			opts.Note = body.Note
		}
	}

	key, err := GenerateGuestKey(nodeID, opts)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"status":  "issued",
		"key":     key,
		"node_id": nodeID,
	})
}

// GET /api/network/guest-keys (JWT) — list all guest keys
func handleGuestKeyList(w http.ResponseWriter, r *http.Request) {
	if guestKeyStore == nil {
		writeJSON(w, 200, map[string]any{"keys": []any{}, "count": 0})
		return
	}
	keys := guestKeyStore.GetAllGuestKeys()
	writeJSON(w, 200, map[string]any{
		"keys":  keys,
		"count": len(keys),
	})
}

// DELETE /api/network/guest-keys/{key} (JWT) — revoke a guest key
func handleGuestKeyRevoke(w http.ResponseWriter, r *http.Request) {
	if guestKeyStore == nil {
		writeError(w, 500, "guest key store not initialized")
		return
	}

	keyParam := r.PathValue("key")
	if keyParam == "" {
		writeError(w, 400, "key parameter is required")
		return
	}

	if err := guestKeyStore.RevokeGuestKey(keyParam); err != nil {
		writeError(w, 404, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{"status": "revoked"})
}

// POST /api/network/keys/validate (no auth) — validate any key type
func handleNetworkKeyValidate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Key == "" {
		writeError(w, 400, "key is required")
		return
	}

	keyType := ClassifyKey(body.Key)

	switch keyType {
	case KeyTypePublic:
		writeJSON(w, 200, map[string]any{
			"valid":    true,
			"key_type": "public",
		})
	case KeyTypeGuest:
		nodeID, valid := ValidateGuestKey(body.Key)
		writeJSON(w, 200, map[string]any{
			"valid":    valid,
			"key_type": "guest",
			"node_id":  nodeID,
		})
	case KeyTypeProxy:
		// Proxy keys are validated via the normal auth flow
		writeJSON(w, 200, map[string]any{
			"valid":    true,
			"key_type": "proxy",
		})
	default:
		writeJSON(w, 200, map[string]any{
			"valid":    false,
			"key_type": "unknown",
		})
	}
}

// ============================================================
// Quota Allocation API
// ============================================================

// GET /api/network/quota-allocation — get current quota allocation settings
func handleGetQuotaAllocation(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeJSON(w, 200, map[string]any{
			"free_consumer_percent": 50,
			"network_node_percent":  50,
		})
		return
	}

	netMgr.mu.RLock()
	alloc := netMgr.config.QuotaAllocation
	netMgr.mu.RUnlock()

	writeJSON(w, 200, map[string]any{
		"free_consumer_percent": alloc.FreeConsumerPercent,
		"network_node_percent":  alloc.NetworkNodePercent,
	})
}

// PUT /api/network/quota-allocation — update quota allocation (JWT)
func handleUpdateQuotaAllocation(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeError(w, 500, "network manager not initialized")
		return
	}

	var body struct {
		FreeConsumerPercent int `json:"free_consumer_percent"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if body.FreeConsumerPercent < 0 || body.FreeConsumerPercent > 100 {
		writeError(w, 400, "free_consumer_percent must be between 0 and 100")
		return
	}

	netMgr.mu.Lock()
	netMgr.config.QuotaAllocation.FreeConsumerPercent = body.FreeConsumerPercent
	netMgr.config.QuotaAllocation.NetworkNodePercent = 100 - body.FreeConsumerPercent
	netMgr.mu.Unlock()
	netMgr.doSave()

	writeJSON(w, 200, map[string]any{
		"free_consumer_percent": body.FreeConsumerPercent,
		"network_node_percent":  100 - body.FreeConsumerPercent,
	})
}

