package main

import (
	"crypto/ed25519"
	crypto_rand "crypto/rand"
	"encoding/base64"
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
// Signed Key System (Phase 2)
// ============================================================
//
// Key format: mk_{consumer_id}.{payload_base64}.{signature_hex}
//
// The issuer (this node) signs the payload with its Ed25519 private key.
// Consumers present the mk_ key in the Authorization header when making
// relay requests. The target node verifies the signature using the
// issuer's public key (obtained from the route table or bootstrap).

// KeyPayload is the JSON structure embedded in a signed key.
type KeyPayload struct {
	Sub    string   `json:"sub"`    // consumer_id
	Iss    string   `json:"iss"`    // issuer NodeID
	Quota  int64    `json:"quota"`  // total allowed requests
	Used   int64    `json:"used"`   // requests consumed so far
	Models []string `json:"models"` // allowed model list
	Iat    int64    `json:"iat"`    // issued-at unix timestamp
	Exp    int64    `json:"exp"`    // expiration unix timestamp
	Nonce  string   `json:"nonce,omitempty"` // SA-06: anti-replay nonce
}

// IssuedKey is the on-disk record for a key issued by this node.
type IssuedKey struct {
	ConsumerID  string   `json:"consumer_id"`
	Key         string   `json:"key"` // full mk_ format
	Payload     KeyPayload `json:"payload"`
	IssuedAt    string   `json:"issued_at"`
	Revoked     bool     `json:"revoked"`
}

// KeyStore manages all keys issued by this node.
type KeyStore struct {
	mu       sync.RWMutex
	keys     map[string]*IssuedKey // keyed by consumer_id
	dataPath string
}

var keyStore *KeyStore

// SA-06: usedNonces tracks consumed trial key nonces to prevent replay attacks.
// Keys are nonce -> expiry time. Expired entries are cleaned up periodically.
var usedNonces = struct {
	sync.RWMutex
	m map[string]time.Time
}{m: make(map[string]time.Time)}

// generateNonce creates a cryptographically secure random nonce.
func generateNonce() string {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		// fallback: use timestamp + random (still better than no nonce)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// checkNonceReplay checks if a nonce has been used and records it if not.
func checkNonceReplay(nonce string, exp int64) error {
	if nonce == "" {
		return nil // no nonce = old format, allow for backward compatibility
	}
	usedNonces.Lock()
	defer usedNonces.Unlock()

	if expiry, exists := usedNonces.m[nonce]; exists && time.Now().Before(expiry) {
		return fmt.Errorf("nonce already used (replay attack)")
	}
	usedNonces.m[nonce] = time.Unix(exp, 0)
	return nil
}

func initKeyStore(dataDir string) {
	keyStore = &KeyStore{
		keys:     make(map[string]*IssuedKey),
		dataPath: filepath.Join(dataDir, "issued_keys.json"),
	}
	keyStore.load()
	slog.Info("key store initialized", "issued_keys", len(keyStore.keys))
}

func (ks *KeyStore) load() {
	// SA-15: Load with HMAC integrity verification
	var issued []*IssuedKey
	if err := loadWithIntegrity(ks.dataPath, &issued); err != nil {
		slog.Warn("key store load failed", "error", err)
		return
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for _, ik := range issued {
		ks.keys[ik.ConsumerID] = ik
	}
}

func (ks *KeyStore) save() {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	ks.doSave()
}

func (ks *KeyStore) doSave() {
	all := make([]*IssuedKey, 0, len(ks.keys))
	for _, ik := range ks.keys {
		all = append(all, ik)
	}
	// SA-15: Save with HMAC integrity protection
	os.MkdirAll(filepath.Dir(ks.dataPath), 0755)
	if err := saveWithIntegrity(ks.dataPath, all); err != nil {
		slog.Error("key store save failed", "error", err)
	}
}

// IssueKey creates a new signed key for a consumer.
func (ks *KeyStore) IssueKey(consumerID string, quota int64, models []string, expDays int) (string, error) {
	if node == nil || !node.IsInitialized() {
		return "", fmt.Errorf("node identity not initialized")
	}
	if consumerID == "" {
		return "", fmt.Errorf("consumer_id is required")
	}
	if quota <= 0 {
		quota = 15000 // default
	}
	if expDays <= 0 {
		expDays = 30
	}

	// Check contribution points vs frozen quota
	if netMgr != nil {
		netMgr.mu.RLock()
		contribPoints := netMgr.config.ContribPoints
		netMgr.mu.RUnlock()

		frozen := ks.totalFrozenQuota()
		if contribPoints > 0 && (contribPoints - frozen) < quota {
			return "", fmt.Errorf("insufficient contribution points: have %d (frozen %d), need %d", contribPoints, frozen, quota)
		}
	}

	now := time.Now()
	payload := KeyPayload{
		Sub:    consumerID,
		Iss:    netMgr.GetNodeID(),
		Quota:  quota,
		Used:   0,
		Models: models,
		Iat:    now.Unix(),
		Exp:    now.Add(time.Duration(expDays) * 24 * time.Hour).Unix(),
	}

	// Marshal payload to JSON, then base64
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// SA-13: Sign using decrypt-on-demand method (avoids keeping private key in memory)
	sigHex := node.SignHex([]byte(payloadB64))
	if sigHex == "" {
		return "", fmt.Errorf("node private key not available")
	}

	// Build mk_ key
	fullKey := fmt.Sprintf("mk_%s.%s.%s", consumerID, payloadB64, sigHex)

	// Store issued key record
	ik := &IssuedKey{
		ConsumerID: consumerID,
		Key:        fullKey,
		Payload:    payload,
		IssuedAt:   now.Format(time.RFC3339),
		Revoked:    false,
	}

	ks.mu.Lock()
	ks.keys[consumerID] = ik
	ks.mu.Unlock()
	ks.save()

	slog.Info("issued signed key", "consumer_id", consumerID, "quota", quota, "exp_days", expDays)
	return fullKey, nil
}

// RevokeKey marks a key as revoked.
func (ks *KeyStore) RevokeKey(consumerID string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ik, ok := ks.keys[consumerID]
	if !ok {
		return fmt.Errorf("key not found for consumer: %s", consumerID)
	}
	ik.Revoked = true
	ks.doSave()
	slog.Info("revoked signed key", "consumer_id", consumerID)
	return nil
}

// GetAllKeys returns all issued keys (non-revoked).
func (ks *KeyStore) GetAllKeys() []*IssuedKey {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	result := make([]*IssuedKey, 0, len(ks.keys))
	for _, ik := range ks.keys {
		result = append(result, ik)
	}
	return result
}

// totalFrozenQuota returns the sum of quotas for all active (non-revoked) keys.
func (ks *KeyStore) totalFrozenQuota() int64 {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	var total int64
	for _, ik := range ks.keys {
		if !ik.Revoked {
			total += ik.Payload.Quota
		}
	}
	return total
}

// RecordUsage increments the used counter for a consumer key.
func (ks *KeyStore) RecordUsage(consumerID string) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ik, ok := ks.keys[consumerID]; ok && !ik.Revoked {
		ik.Payload.Used++
		// save periodically, not on every request (performance)
	}
}

// SaveAsync saves the key store (called periodically).
func (ks *KeyStore) SaveAsync() {
	ks.save()
}

// ============================================================
// Key Validation
// ============================================================

// ValidateKey parses and validates a mk_ format key.
// Returns the payload if valid, or an error.
// Supports all key types: standard, trial, open_unbound, open_bound.
func ValidateKey(mkKey string) (*KeyPayload, error) {
	if !strings.HasPrefix(mkKey, "mk_") {
		return nil, fmt.Errorf("not a signed key (missing mk_ prefix)")
	}

	keyType := ClassifyKey(mkKey)
	var rest string
	var consumerID string

	switch keyType {
	case KeyTypeTrial:
		// Format: mk_trial_{node_id}_{timestamp}.{payload_b64}.{sig_hex}
		rest = mkKey[7:] // strip "mk_trial_"
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid trial key format")
		}
		// consumer_id = "trial_{node_id}_{timestamp}"
		consumerID = "trial_" + parts[0]
		return validateKeyParts(consumerID, parts[1], parts[2])

	case KeyTypeOpenUnbound:
		// Format: mk_open_{random}.{payload_b64}.{sig_hex}
		rest = mkKey[8:] // strip "mk_open_"
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid open unbound key format")
		}
		consumerID = "open_unbound_" + parts[0]
		return validateKeyParts(consumerID, parts[1], parts[2])

	case KeyTypeOpenBound:
		// Format: mk_open_{node_id}_{random}.{payload_b64}.{sig_hex}
		rest = mkKey[8:] // strip "mk_open_"
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid open bound key format")
		}
		consumerID = "open_bound_" + parts[0]
		return validateKeyParts(consumerID, parts[1], parts[2])

	case KeyTypeGlobal:
		// Format: mk_open_global_{node_id}_{random}.{payload_b64}.{sig_hex}
		rest = mkKey[15:] // strip "mk_open_global_"
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid global key format")
		}
		consumerID = "global_" + parts[0]
		return validateKeyParts(consumerID, parts[1], parts[2])

	default:
		// Standard: mk_{consumer_id}.{payload_b64}.{signature_hex}
		rest = mkKey[3:] // strip "mk_"
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid key format: expected mk_{consumer_id}.{payload}.{signature}")
		}
		return validateKeyParts(parts[0], parts[1], parts[2])
	}
}

// validateKeyParts validates the payload and signature parts of a key
func validateKeyParts(consumerID, payloadB64, sigHex string) (*KeyPayload, error) {
	// Decode payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("invalid payload base64: %w", err)
	}

	var payload KeyPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("invalid payload JSON: %w", err)
	}

	// Verify consumer_id matches
	if payload.Sub != consumerID {
		return nil, fmt.Errorf("consumer_id mismatch: key=%s, payload=%s", consumerID, payload.Sub)
	}

	// Check if revoked by local store
	if keyStore != nil {
		keyStore.mu.RLock()
		if ik, ok := keyStore.keys[consumerID]; ok && ik.Revoked {
			keyStore.mu.RUnlock()
			// SA-09: Log detail internally, return generic error externally
			slog.Warn("key validation: revoked key presented", "consumer_id", consumerID)
			return nil, fmt.Errorf("invalid key")
		}
		keyStore.mu.RUnlock()
	}

	// Check expiration
	now := time.Now().Unix()
	if payload.Exp > 0 && now > payload.Exp {
		// SA-09: Do not leak exact expiration time
		slog.Warn("key validation: expired key presented", "consumer_id", consumerID, "exp", payload.Exp)
		return nil, fmt.Errorf("invalid key")
	}

	// SA-06: Check for nonce replay (trial keys and keys with nonces)
	if payload.Nonce != "" {
		if err := checkNonceReplay(payload.Nonce, payload.Exp); err != nil {
			// SA-09: Do not reveal replay detection details
			slog.Warn("key validation: nonce replay detected", "consumer_id", consumerID)
			return nil, fmt.Errorf("invalid key")
		}
	}

	// Check quota
	if payload.Quota > 0 && payload.Used >= payload.Quota {
		// SA-09: Do not leak quota/usage numbers
		slog.Warn("key validation: quota exhausted", "consumer_id", consumerID, "used", payload.Used, "quota", payload.Quota)
		return nil, fmt.Errorf("invalid key")
	}

	// For open unbound keys, skip issuer verification (no issuer)
	if payload.Iss == "" {
		// Open unbound: verify with this node's key
		if node == nil || !node.IsInitialized() {
			return nil, fmt.Errorf("cannot verify: node not initialized")
		}
		node.mu.RLock()
		pub := node.pubKey
		node.mu.RUnlock()

		sigBytes, err := hex.DecodeString(sigHex)
		if err != nil {
			return nil, fmt.Errorf("invalid signature hex: %w", err)
		}
		if !ed25519.Verify(pub, []byte(payloadB64), sigBytes) {
			return nil, fmt.Errorf("signature verification failed")
		}
		return &payload, nil
	}

	// Get issuer's public key from route table or known peers
	issuerPubKey := getIssuerPublicKey(payload.Iss)
	if issuerPubKey == nil {
		return nil, fmt.Errorf("cannot find public key for issuer node: %s", payload.Iss)
	}

	// Verify Ed25519 signature
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}

	if !ed25519.Verify(issuerPubKey, []byte(payloadB64), sigBytes) {
		return nil, fmt.Errorf("signature verification failed")
	}

	return &payload, nil
}

// getIssuerPublicKey retrieves the Ed25519 public key for a given NodeID.
// First checks if it's this node, then checks known peers' public keys.
func getIssuerPublicKey(issuerNodeID string) ed25519.PublicKey {
	// Check if it's this node
	if node != nil && node.IsInitialized() {
		selfP2P := DeriveP2PNodeID()
		if issuerNodeID == selfP2P {
			node.mu.RLock()
			pub := node.pubKey
			node.mu.RUnlock()
			return pub
		}
	}

	// Try to find the peer's public key from the network config or federation
	// For Phase 2, we look up from known peers stored in network config
	// In production, this would query the peer's /api/node/pubkey endpoint
	if netMgr != nil {
		netMgr.mu.RLock()
		for _, peer := range netMgr.config.Peers {
			if peer.NodeID == issuerNodeID && len(peer.Addresses) > 0 {
				netMgr.mu.RUnlock()
				// Fetch public key from the peer
				pubKey := fetchPeerPublicKey(peer.Addresses)
				if pubKey != nil {
					return pubKey
				}
				return nil
			}
		}
		netMgr.mu.RUnlock()
	}

	return nil
}

// fetchPeerPublicKey fetches the Ed25519 public key from a peer node.
// SA-02/SA-04: Only uses HTTPS addresses to prevent key interception.
func fetchPeerPublicKey(addresses []string) ed25519.PublicKey {
	client := &httpClient10
	for _, addr := range addresses {
		addr = strings.TrimRight(addr, "/")
		// Only use HTTPS for fetching public keys
		if !strings.HasPrefix(addr, "https://") {
			continue
		}
		resp, err := client.Get(addr + "/api/node/pubkey")
		if err != nil {
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			continue
		}
		var result struct {
			PubKeyB64 string `json:"pub_key"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		pubBytes, err := base64.StdEncoding.DecodeString(result.PubKeyB64)
		if err != nil || len(pubBytes) != ed25519.PublicKeySize {
			continue
		}
		return ed25519.PublicKey(pubBytes)
	}
	return nil
}

// CheckModelAccess checks if a model is allowed by the key's model list.
func CheckModelAccess(payload *KeyPayload, model string) bool {
	if len(payload.Models) == 0 {
		return true // empty list = all models allowed
	}
	for _, m := range payload.Models {
		if m == model || m == "*" {
			return true
		}
	}
	return false
}

// ============================================================
// API Handlers — Signed Keys
// ============================================================

// POST /api/network/keys/issue (JWT) — issue a new signed key
func handleNetworkKeyIssue(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeError(w, 400, "shared network not active")
		return
	}
	var body struct {
		ConsumerID string   `json:"consumer_id"`
		Quota      int64    `json:"quota"`
		Models     []string `json:"models"`
		ExpDays    int      `json:"exp_days"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.ConsumerID == "" {
		writeError(w, 400, "consumer_id is required")
		return
	}

	key, err := keyStore.IssueKey(body.ConsumerID, body.Quota, body.Models, body.ExpDays)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"status":      "issued",
		"key":         key,
		"consumer_id": body.ConsumerID,
		"quota":       body.Quota,
		"models":      body.Models,
	})
}

// GET /api/network/keys (JWT) — list all issued keys
func handleNetworkKeyList(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeJSON(w, 200, map[string]any{"keys": []any{}, "message": "shared network not active"})
		return
	}
	keys := keyStore.GetAllKeys()
	writeJSON(w, 200, map[string]any{"keys": keys, "count": len(keys)})
}

// DELETE /api/network/keys/{consumer_id} (JWT) — revoke a key
func handleNetworkKeyRevoke(w http.ResponseWriter, r *http.Request) {
	consumerID := r.PathValue("consumer_id")
	if consumerID == "" {
		writeError(w, 400, "consumer_id is required")
		return
	}
	if err := keyStore.RevokeKey(consumerID); err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "revoked", "consumer_id": consumerID})
}

// POST /api/network/keys/validate (no auth) — validate a signed key
func handleNetworkKeyValidate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Model string `json:"model"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Key == "" {
		writeError(w, 400, "key is required")
		return
	}

	payload, err := ValidateKey(body.Key)
	if err != nil {
		// SA-09: Return generic error, do not expose internal validation details
		writeJSON(w, 200, map[string]any{"valid": false})
		return
	}

	// Check model access if model is specified
	if body.Model != "" && !CheckModelAccess(payload, body.Model) {
		// SA-09: Do not reveal allowed models list
		slog.Warn("key validate: model access denied", "model", body.Model)
		writeJSON(w, 200, map[string]any{"valid": false})
		return
	}

	// SA-09: Only return essential info, do not expose quota/used/models to public
	writeJSON(w, 200, map[string]any{
		"valid":       true,
		"consumer_id": payload.Sub,
		"expires":     payload.Exp,
	})
}

// GET /api/network/contributions (JWT) — view contribution records
func handleNetworkContributions(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeJSON(w, 200, map[string]any{"records": []any{}, "message": "shared network not active"})
		return
	}

	netMgr.mu.RLock()
	status := map[string]any{
		"contrib_points":     netMgr.config.ContribPoints,
		"frozen_quota":       keyStore.totalFrozenQuota(),
		"requests_relayed":   netMgr.config.Stats.RequestsRelayed,
		"relay_success":      netMgr.config.Stats.RelaySuccess,
		"relay_failed":       netMgr.config.Stats.RelayFailed,
		"records":            netMgr.config.ContribRecords,
		"issued_keys_count":  len(keyStore.keys),
	}
	netMgr.mu.RUnlock()

	writeJSON(w, 200, status)
}

// ============================================================
// Phase 2 Economic Model — Trial Pool & Open Keys
// ============================================================

// KeyType represents the type of a key
type KeyType string

const (
	KeyTypeStandard     KeyType = "standard"       // Standard consumer key (mk_{consumer_id}...)
	KeyTypeTrial        KeyType = "trial"           // Trial pool key (mk_trial_{node_id}_{ts})
	KeyTypeOpenUnbound  KeyType = "open_unbound"    // Open unbound key (mk_open_xxxxx)
	KeyTypeOpenBound    KeyType = "open_bound"      // Open bound key (mk_open_{node_id}_xxxxx)
	KeyTypeGlobal       KeyType = "global"          // Global pool key (mk_open_global_{node_id}_xxxxx)
)

// Default trial quota (tokens)
const DefaultTrialQuota int64 = 50000

// Default open unbound quota
const DefaultOpenUnboundQuota int64 = 1000

// Total open quota pool for bound keys
const TotalOpenBoundQuota int64 = 100000

// ============================================================
// Trial Pool Key
// ============================================================

// TrialKeyInfo holds information about a trial pool key
type TrialKeyInfo struct {
	NodeID    string `json:"node_id"`
	Key       string `json:"key"`
	Quota     int64  `json:"quota"`
	Used      int64  `json:"used"`
	IssuedAt  string `json:"issued_at"`
	Active    bool   `json:"active"`
}

// IssueTrialKey creates a trial key for a node joining the shared network.
// Format: mk_trial_{node_id}_{timestamp}.{payload_base64}.{signature_hex}
func (ks *KeyStore) IssueTrialKey(nodeID string, quota int64) (string, *TrialKeyInfo, error) {
	if node == nil || !node.IsInitialized() {
		return "", nil, fmt.Errorf("node identity not initialized")
	}
	if quota <= 0 {
		quota = DefaultTrialQuota
	}

	now := time.Now()
	timestamp := now.Unix()
	consumerID := fmt.Sprintf("trial_%s_%d", nodeID, timestamp)

	payload := KeyPayload{
		Sub:    consumerID,
		Iss:    nodeID,
		Quota:  quota,
		Used:   0,
		Models: []string{}, // all models allowed for trial
		Iat:    now.Unix(),
		Exp:    now.Add(7 * 24 * time.Hour).Unix(), // 7 days
		Nonce:  generateNonce(), // SA-06: anti-replay nonce
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("marshal payload: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// SA-13: Sign using decrypt-on-demand method
	sigHex := node.SignHex([]byte(payloadB64))
	if sigHex == "" {
		return "", nil, fmt.Errorf("node private key not available")
	}

	fullKey := fmt.Sprintf("mk_trial_%s_%d.%s.%s", nodeID, timestamp, payloadB64, sigHex)

	// Store the key
	ik := &IssuedKey{
		ConsumerID: consumerID,
		Key:        fullKey,
		Payload:    payload,
		IssuedAt:   now.Format(time.RFC3339),
		Revoked:    false,
	}

	ks.mu.Lock()
	ks.keys[consumerID] = ik
	ks.mu.Unlock()
	ks.save()

	info := &TrialKeyInfo{
		NodeID:   nodeID,
		Key:      fullKey,
		Quota:    quota,
		Used:     0,
		IssuedAt: now.Format(time.RFC3339),
		Active:   true,
	}

	// Store in trial pool
	if netMgr != nil {
		netMgr.AddTrialKey(info)
	}

	slog.Info("issued trial key", "node_id", nodeID, "quota", quota, "key", fullKey[:min(len(fullKey), 30)]+"...")
	return fullKey, info, nil
}

// ============================================================
// Open Keys — Unbound & Bound
// ============================================================

// OpenKeyInfo holds information about an open key
type OpenKeyInfo struct {
	Key        string  `json:"key"`
	Type       KeyType `json:"type"`        // open_unbound or open_bound
	NodeID     string  `json:"node_id"`     // bound node_id (empty for unbound)
	Quota      int64   `json:"quota"`
	Used       int64   `json:"used"`
	IssuedAt   string  `json:"issued_at"`
	Active     bool    `json:"active"`
	ConsumerID string  `json:"consumer_id"`
}

// openKeyStore manages all open keys
type openKeyStore struct {
	mu        sync.RWMutex
	unbound   []*OpenKeyInfo
	bound     map[string]*OpenKeyInfo // keyed by node_id
	dataPath  string
}

var openKeys *openKeyStore

func initOpenKeyStore(dataDir string) {
	openKeys = &openKeyStore{
		unbound:  make([]*OpenKeyInfo, 0),
		bound:    make(map[string]*OpenKeyInfo),
		dataPath: filepath.Join(dataDir, "open_keys.json"),
	}
	openKeys.load()
	slog.Info("open key store initialized", "unbound", len(openKeys.unbound), "bound", len(openKeys.bound))
}

func (oks *openKeyStore) load() {
	b, err := os.ReadFile(oks.dataPath)
	if err != nil {
		return
	}
	var data struct {
		Unbound []*OpenKeyInfo            `json:"unbound"`
		Bound   map[string]*OpenKeyInfo   `json:"bound"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return
	}
	oks.mu.Lock()
	defer oks.mu.Unlock()
	if data.Unbound != nil {
		oks.unbound = data.Unbound
	}
	if data.Bound != nil {
		oks.bound = data.Bound
	}
}

func (oks *openKeyStore) save() {
	oks.mu.RLock()
	defer oks.mu.RUnlock()
	oks.doSave()
}

func (oks *openKeyStore) doSave() {
	data := struct {
		Unbound []*OpenKeyInfo            `json:"unbound"`
		Bound   map[string]*OpenKeyInfo   `json:"bound"`
	}{
		Unbound: oks.unbound,
		Bound:   oks.bound,
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	os.MkdirAll(filepath.Dir(oks.dataPath), 0755)
	os.WriteFile(oks.dataPath, b, 0600)
}

// IssueOpenKeyUnbound creates an unbound open key for zero-barrier experience.
// Format: mk_open_{random_hex}
func (oks *openKeyStore) IssueOpenKeyUnbound() (string, *OpenKeyInfo, error) {
	if node == nil || !node.IsInitialized() {
		return "", nil, fmt.Errorf("node identity not initialized")
	}

	now := time.Now()
	randBytes := make([]byte, 16)
	if _, err := crypto_rand.Read(randBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	randHex := hex.EncodeToString(randBytes)
	consumerID := fmt.Sprintf("open_unbound_%s", randHex)

	payload := KeyPayload{
		Sub:    consumerID,
		Iss:    "", // no issuer for unbound
		Quota:  DefaultOpenUnboundQuota,
		Used:   0,
		Models: []string{},
		Iat:    now.Unix(),
		Exp:    now.Add(24 * time.Hour).Unix(), // 24 hours
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("marshal payload: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// SA-13: Sign using decrypt-on-demand method
	sigHex := node.SignHex([]byte(payloadB64))
	if sigHex == "" {
		return "", nil, fmt.Errorf("node private key not available")
	}

	fullKey := fmt.Sprintf("mk_open_%s.%s.%s", randHex, payloadB64, sigHex)

	info := &OpenKeyInfo{
		Key:        fullKey,
		Type:       KeyTypeOpenUnbound,
		NodeID:     "",
		Quota:      DefaultOpenUnboundQuota,
		Used:       0,
		IssuedAt:   now.Format(time.RFC3339),
		Active:     true,
		ConsumerID: consumerID,
	}

	oks.mu.Lock()
	oks.unbound = append(oks.unbound, info)
	oks.mu.Unlock()
	oks.save()

	slog.Info("issued open unbound key", "key", fullKey[:min(len(fullKey), 30)]+"...")
	return fullKey, info, nil
}

// IssueOpenKeyBound creates a bound open key for a specific node.
// Format: mk_open_{node_id}_{random_hex}
func (oks *openKeyStore) IssueOpenKeyBound(nodeID string) (string, *OpenKeyInfo, error) {
	if node == nil || !node.IsInitialized() {
		return "", nil, fmt.Errorf("node identity not initialized")
	}
	if nodeID == "" {
		return "", nil, fmt.Errorf("node_id is required")
	}

	// Calculate quota based on reputation and contribution
	quota := CalculateOpenKeyQuota(nodeID)
	if quota < 100 {
		quota = 100 // minimum
	}

	now := time.Now()
	randBytes := make([]byte, 16)
	if _, err := crypto_rand.Read(randBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	randHex := hex.EncodeToString(randBytes)
	consumerID := fmt.Sprintf("open_bound_%s_%s", nodeID, randHex)

	payload := KeyPayload{
		Sub:    consumerID,
		Iss:    nodeID,
		Quota:  quota,
		Used:   0,
		Models: []string{},
		Iat:    now.Unix(),
		Exp:    now.Add(30 * 24 * time.Hour).Unix(), // 30 days
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("marshal payload: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// SA-13: Sign using decrypt-on-demand method
	sigHex := node.SignHex([]byte(payloadB64))
	if sigHex == "" {
		return "", nil, fmt.Errorf("node private key not available")
	}

	fullKey := fmt.Sprintf("mk_open_%s_%s.%s.%s", nodeID, randHex, payloadB64, sigHex)

	info := &OpenKeyInfo{
		Key:        fullKey,
		Type:       KeyTypeOpenBound,
		NodeID:     nodeID,
		Quota:      quota,
		Used:       0,
		IssuedAt:   now.Format(time.RFC3339),
		Active:     true,
		ConsumerID: consumerID,
	}

	oks.mu.Lock()
	oks.bound[nodeID] = info
	oks.mu.Unlock()
	oks.save()

	slog.Info("issued open bound key", "node_id", nodeID, "quota", quota, "key", fullKey[:min(len(fullKey), 30)]+"...")
	return fullKey, info, nil
}

// GetActiveUnboundKeys returns all active unbound open keys
func (oks *openKeyStore) GetActiveUnboundKeys() []*OpenKeyInfo {
	oks.mu.RLock()
	defer oks.mu.RUnlock()
	result := make([]*OpenKeyInfo, 0)
	for _, k := range oks.unbound {
		if k.Active {
			result = append(result, k)
		}
	}
	return result
}

// GetBoundKeys returns all bound open keys
func (oks *openKeyStore) GetBoundKeys() map[string]*OpenKeyInfo {
	oks.mu.RLock()
	defer oks.mu.RUnlock()
	result := make(map[string]*OpenKeyInfo)
	for k, v := range oks.bound {
		result[k] = v
	}
	return result
}

// RecordOpenKeyUsage records usage for an open key
func (oks *openKeyStore) RecordOpenKeyUsage(consumerID string) {
	oks.mu.Lock()
	defer oks.mu.Unlock()
	for _, k := range oks.unbound {
		if k.ConsumerID == consumerID {
			k.Used++
			return
		}
	}
	for _, k := range oks.bound {
		if k.ConsumerID == consumerID {
			k.Used++
			return
		}
	}
}

// CalculateOpenKeyQuota calculates the quota for a bound open key.
// quota = totalOpenQuota * (0.4 * reputation/totalRep + 0.6 * contribution/totalContrib)
func CalculateOpenKeyQuota(nodeID string) int64 {
	if netMgr == nil {
		return 1000 // default
	}

	netMgr.mu.RLock()
	defer netMgr.mu.RUnlock()

	// Get network-wide totals
	var totalRep float64
	var totalContrib int64
	nodeRep := 0.0
	nodeContrib := int64(0)

	// Calculate from peers
	for _, peer := range netMgr.config.Peers {
		totalRep += peer.TrustScore
		if peer.NodeID == nodeID {
			nodeRep = peer.TrustScore
		}
	}

	// Add self
	selfContrib := netMgr.config.ContribPoints
	totalContrib += selfContrib
	if nodeID == netMgr.config.NodeID {
		nodeContrib = selfContrib
	}
	for _, peer := range netMgr.config.Peers {
		// Approximate peer contribution from route table stats
		peerContrib := int64(peer.TrustScore * 1000) // approximate
		totalContrib += peerContrib
		if peer.NodeID == nodeID {
			nodeContrib = peerContrib
		}
	}

	// Avoid division by zero
	if totalRep == 0 {
		totalRep = 1
	}
	if totalContrib == 0 {
		totalContrib = 1
	}

	repShare := nodeRep / totalRep
	contribShare := float64(nodeContrib) / float64(totalContrib)

	quota := float64(TotalOpenBoundQuota) * (0.4*repShare + 0.6*contribShare)
	if quota < 100 {
		quota = 100
	}

	return int64(quota)
}

// ============================================================
// Key Classification Helper
// ============================================================

// ClassifyKey determines the type of a mk_ key
func ClassifyKey(mkKey string) KeyType {
	if strings.HasPrefix(mkKey, "mk_trial_") {
		return KeyTypeTrial
	}
	if strings.HasPrefix(mkKey, "mk_open_global_") {
		return KeyTypeGlobal
	}
	if strings.HasPrefix(mkKey, "mk_open_") {
		// Check if it contains a node_id (bound) or not (unbound)
		rest := strings.TrimPrefix(mkKey, "mk_open_")
		// Format: {random} or {node_id}_{random}
		// Node IDs start with mmx-, so if the first segment starts with mmx-, it's bound
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) > 0 {
			prefix := parts[0]
			// Check if prefix contains node_id pattern
			if strings.Contains(prefix, "mmx-") {
				return KeyTypeOpenBound
			}
		}
		return KeyTypeOpenUnbound
	}
	if strings.HasPrefix(mkKey, "mk_") {
		return KeyTypeStandard
	}
	return ""
}

// ============================================================
// API Handlers — Trial Pool & Open Keys
// ============================================================

// GET /api/network/trial — get trial pool info
func handleNetworkTrialPool(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeJSON(w, 200, map[string]any{"trial_keys": []any{}, "message": "shared network not active"})
		return
	}

	trialKeys := netMgr.GetTrialKeys()
	writeJSON(w, 200, map[string]any{
		"trial_keys": trialKeys,
		"count":      len(trialKeys),
	})
}

// POST /api/network/trial/issue (JWT) — issue a new trial key
func handleNetworkTrialIssue(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeError(w, 400, "shared network not active")
		return
	}

	var body struct {
		NodeID string `json:"node_id"`
		Quota  int64  `json:"quota"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	nodeID := body.NodeID
	if nodeID == "" {
		nodeID = netMgr.GetNodeID()
	}
	if body.Quota <= 0 {
		body.Quota = DefaultTrialQuota
	}

	key, info, err := keyStore.IssueTrialKey(nodeID, body.Quota)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"status": "issued",
		"key":    key,
		"info":   info,
	})
}

// GET /api/network/open-keys — get all open keys
func handleNetworkOpenKeys(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeJSON(w, 200, map[string]any{"unbound": []any{}, "bound": []any{}, "message": "shared network not active"})
		return
	}

	unbound := openKeys.GetActiveUnboundKeys()
	bound := openKeys.GetBoundKeys()

	writeJSON(w, 200, map[string]any{
		"unbound": unbound,
		"bound":   bound,
	})
}

// POST /api/network/open-keys/unbound (JWT) — issue an unbound open key
func handleNetworkOpenKeyUnboundIssue(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeError(w, 400, "shared network not active")
		return
	}

	key, info, err := openKeys.IssueOpenKeyUnbound()
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"status": "issued",
		"key":    key,
		"info":   info,
	})
}

// POST /api/network/open-keys/bound (JWT) — issue a bound open key
func handleNetworkOpenKeyBoundIssue(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeError(w, 400, "shared network not active")
		return
	}

	var body struct {
		NodeID string `json:"node_id"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	nodeID := body.NodeID
	if nodeID == "" {
		writeError(w, 400, "node_id is required")
		return
	}

	key, info, err := openKeys.IssueOpenKeyBound(nodeID)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}

	writeJSON(w, 200, map[string]any{
		"status": "issued",
		"key":    key,
		"info":   info,
	})
}

// GET /api/network/unlock-status — get unlock status for this node
func handleNetworkUnlockStatus(w http.ResponseWriter, r *http.Request) {
	if !netMgr.IsSharedMode() {
		writeJSON(w, 200, map[string]any{"unlocked": false, "message": "shared network not active"})
		return
	}

	status := netMgr.GetUnlockStatus()
	writeJSON(w, 200, status)
}
