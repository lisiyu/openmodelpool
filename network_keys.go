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
	Quota      int64  `json:"quota,omitempty"`       // 本地资源访问额度上限 (0=不限，仅约束访问本节点自身资源)
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
	initGuestKeyUsageTracker()
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
	Quota   int64  // 本地资源访问额度上限 (0=不限)
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
// Security fix (S-1, S-11): Key MUST exist in store, and ExpiresAt is checked.
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

	// Key MUST be found in store — unknown keys are rejected
	if guestKeyStore != nil {
		guestKeyStore.mu.RLock()
		found := false
		for _, rec := range guestKeyStore.keys {
			if rec.Key == key {
				found = true
				// Check if revoked
				if rec.Revoked {
					guestKeyStore.mu.RUnlock()
					return "", false
				}
				// Check if expired
				if rec.ExpiresAt != "" {
					expTime, err := time.Parse(time.RFC3339, rec.ExpiresAt)
					if err == nil && time.Now().After(expTime) {
						guestKeyStore.mu.RUnlock()
						return "", false
					}
				}
				guestKeyStore.mu.RUnlock()
				return rec.NodeID, true
			}
		}
		guestKeyStore.mu.RUnlock()
		// Key not found in store — reject
		if !found {
			slog.Warn("guest key not found in store, rejecting", "node_id", candidateNodeID)
			return "", false
		}
	}

	// No store initialized — reject (fail-closed)
	return "", false
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

// GetGuestKeyRecord returns the record for a specific guest key, or nil if not found.
func (gks *GuestKeyStore) GetGuestKeyRecord(key string) *GuestKeyRecord {
	gks.mu.RLock()
	defer gks.mu.RUnlock()
	for _, rec := range gks.keys {
		if rec.Key == key {
			cp := *rec
			return &cp
		}
	}
	return nil
}

// UpdateGuestKeyQuota updates the quota for a specific guest key.
func (gks *GuestKeyStore) UpdateGuestKeyQuota(key string, quota int64) error {
	gks.mu.Lock()
	defer gks.mu.Unlock()
	for _, rec := range gks.keys {
		if rec.Key == key {
			rec.Quota = quota
			gks.doSaveLocked()
			slog.Info("updated guest key quota", "key_prefix", key[:min(len(key), 30)]+"...", "quota", quota)
			return nil
		}
	}
	return fmt.Errorf("guest key not found")
}

// ============================================================
// Per-Key Usage Tracker (D-4: 逐 Key 本地额度执行)
// ============================================================

// guestKeyUsageTracker tracks per-key usage for local quota enforcement.
type guestKeyUsageTracker struct {
	mu    sync.Mutex
	usage map[string]int64 // key -> tokens used
}

var guestKeyUsage *guestKeyUsageTracker

func initGuestKeyUsageTracker() {
	guestKeyUsage = &guestKeyUsageTracker{
		usage: make(map[string]int64),
	}
}

// CheckAndReserve checks if the key has remaining local quota and reserves estimated tokens.
// Returns (allowed, remaining).
func (t *guestKeyUsageTracker) CheckAndReserve(key string, quota int64, estimated int64) (bool, int64) {
	if quota <= 0 {
		return true, 0 // no local quota limit
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	used := t.usage[key]
	remaining := quota - used
	if remaining <= 0 {
		return false, 0
	}
	// Reserve (pre-deduct)
	if estimated > 0 && estimated <= remaining {
		t.usage[key] = used + estimated
		return true, remaining - estimated
	} else if estimated <= 0 {
		// No estimate — just check
		return true, remaining
	}
	// estimated > remaining
	return false, remaining
}

// Adjust adjusts the reserved quota after a request completes.
func (t *guestKeyUsageTracker) Adjust(key string, reserved, actual int64) {
	if reserved <= 0 && actual <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	diff := actual - reserved
	t.usage[key] += diff
	if t.usage[key] < 0 {
		t.usage[key] = 0
	}
}

// GetUsage returns the current usage for a key.
func (t *guestKeyUsageTracker) GetUsage(key string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.usage[key]
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

// PUT /api/network/guest-keys/{key}/quota (JWT) — update guest key local quota
func handleGuestKeyUpdateQuota(w http.ResponseWriter, r *http.Request) {
	if guestKeyStore == nil {
		writeError(w, 500, "guest key store not initialized")
		return
	}

	keyParam := r.PathValue("key")
	if keyParam == "" {
		writeError(w, 400, "key parameter is required")
		return
	}

	var body struct {
		Quota int64 `json:"quota"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Quota < 0 {
		writeError(w, 400, "quota must be >= 0")
		return
	}

	if err := guestKeyStore.UpdateGuestKeyQuota(keyParam, body.Quota); err != nil {
		writeError(w, 404, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{"status": "updated", "quota": body.Quota})
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
			"guest_key_percent": 50,
			"public_key_percent":  50,
		})
		return
	}

	netMgr.mu.RLock()
	alloc := netMgr.config.QuotaAllocation
	netMgr.mu.RUnlock()

	writeJSON(w, 200, map[string]any{
		"guest_key_percent": alloc.GuestKeyPercent,
		"public_key_percent":  alloc.PublicKeyPercent,
	})
}

// PUT /api/network/quota-allocation — update quota allocation (JWT)
func handleUpdateQuotaAllocation(w http.ResponseWriter, r *http.Request) {
	if netMgr == nil {
		writeError(w, 500, "network manager not initialized")
		return
	}

	var body struct {
		GuestKeyPercent int `json:"guest_key_percent"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	if body.GuestKeyPercent < 0 || body.GuestKeyPercent > 100 {
		writeError(w, 400, "guest_key_percent must be between 0 and 100")
		return
	}

	netMgr.mu.Lock()
	netMgr.config.QuotaAllocation.GuestKeyPercent = body.GuestKeyPercent
	netMgr.config.QuotaAllocation.PublicKeyPercent = 100 - body.GuestKeyPercent
	netMgr.mu.Unlock()
	netMgr.doSave()

	writeJSON(w, 200, map[string]any{
		"guest_key_percent": body.GuestKeyPercent,
		"public_key_percent":  100 - body.GuestKeyPercent,
	})
}

