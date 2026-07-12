package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MultiUserManager handles invite codes and consumer (API key) management.
type MultiUserManager struct {
	mu         sync.RWMutex
	invites    map[string]*InviteCode // code -> InviteCode
	consumers  map[string]*Consumer   // id -> Consumer
	apiKeyMap  map[string]string      // sha256(api_key) -> consumer_id (hashed for security)
	dataPath   string

	// Batch save fields (P-3)
	dirtyCount atomic.Int64
	saveStopCh chan struct{}
}

var multiUser *MultiUserManager

func initMultiUser(dataDir string) {
	multiUser = &MultiUserManager{
		invites:    make(map[string]*InviteCode),
		consumers:  make(map[string]*Consumer),
		apiKeyMap:  make(map[string]string),
		dataPath:   filepath.Join(dataDir, "consumers.json"),
		saveStopCh: make(chan struct{}),
	}
	multiUser.load()
	// Start batch save goroutine
	go multiUser.batchSaveLoop()
}

// batchSaveLoop flushes consumer stats to disk periodically.
func (m *MultiUserManager) batchSaveLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if m.dirtyCount.Load() > 0 {
				m.mu.Lock()
				m.dirtyCount.Store(0)
				m.mu.Unlock()
				m.save()
			}
		case <-m.saveStopCh:
			// Final flush on shutdown
			if m.dirtyCount.Load() > 0 {
				m.mu.Lock()
				m.dirtyCount.Store(0)
				m.mu.Unlock()
				m.save()
			}
			return
		}
	}
}

// hashAPIKey returns the SHA-256 hex digest of an API key.
func hashAPIKey(apiKey string) string {
	h := sha256.Sum256([]byte(apiKey))
	return fmt.Sprintf("%x", h[:])
}

func (m *MultiUserManager) load() {
	b, err := os.ReadFile(m.dataPath)
	if err != nil {
		return
	}
	var data struct {
		Invites   map[string]*InviteCode `json:"invites"`
		Consumers map[string]*Consumer   `json:"consumers"`
	}
	if json.Unmarshal(b, &data) != nil {
		return
	}
	if data.Invites != nil {
		m.invites = data.Invites
	}
	if data.Consumers != nil {
		m.consumers = data.Consumers
		for id, c := range m.consumers {
			if c.APIKey != "" {
				// Decrypt stored key, then store SHA-256 hash in apiKeyMap
				plaintext := c.APIKey
				if IsEncrypted(c.APIKey) {
					plaintext = decryptField(c.APIKey)
				}
				if plaintext != "" {
					m.apiKeyMap[hashAPIKey(plaintext)] = id
				}
			}
		}
	}
	slog.Info("multi-user data loaded", "invites", len(m.invites), "consumers", len(m.consumers))
}

func (m *MultiUserManager) save() {
	os.MkdirAll(filepath.Dir(m.dataPath), 0755)
	data := struct {
		Invites   map[string]*InviteCode `json:"invites"`
		Consumers map[string]*Consumer   `json:"consumers"`
	}{
		Invites:   m.invites,
		Consumers: m.consumers,
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	atomicWriteFile(m.dataPath, b, 0600)
}

// CreateInviteCode generates a new invite code with optional role.
func (m *MultiUserManager) CreateInviteCode(maxUses int, role string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if role == "" {
		role = "consumer"
	}
	code := randomString(12)
	m.invites[code] = &InviteCode{
		Code:      code,
		CreatedAt: time.Now().Format(time.RFC3339),
		MaxUses:   maxUses,
		Role:      role,
	}
	m.save()
	slog.Info("invite code created", "code", code, "max_uses", maxUses, "role", role)
	return code
}

// ValidateInviteCode checks if an invite code is valid and can be used.
func (m *MultiUserManager) ValidateInviteCode(code string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	inv, ok := m.invites[code]
	if !ok {
		return false
	}
	if inv.MaxUses > 0 && inv.UseCount >= inv.MaxUses {
		return false
	}
	// Check if single-use and already used
	if inv.MaxUses == 0 && inv.UsedBy != "" {
		return false
	}
	return true
}

// UseInviteCode marks an invite code as used by a consumer.
func (m *MultiUserManager) UseInviteCode(code, consumerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	inv, ok := m.invites[code]
	if !ok {
		return false
	}
	if inv.MaxUses > 0 && inv.UseCount >= inv.MaxUses {
		return false
	}
	if inv.MaxUses == 0 && inv.UsedBy != "" {
		return false
	}
	inv.UseCount++
	inv.UsedBy = consumerID
	inv.UsedAt = time.Now().Format(time.RFC3339)
	m.save()
	return true
}

// CreateConsumer creates a new consumer with an API key.
func (m *MultiUserManager) CreateConsumer(name, inviteCode string) (*Consumer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate invite code
	inv, ok := m.invites[inviteCode]
	if !ok {
		return nil, fmt.Errorf("invalid invite code")
	}
	if inv.MaxUses > 0 && inv.UseCount >= inv.MaxUses {
		return nil, fmt.Errorf("invite code already fully used")
	}
	if inv.MaxUses == 0 && inv.UsedBy != "" {
		return nil, fmt.Errorf("invite code already used")
	}

	apiKey := "sk-" + randomString(48)
	id := "consumer-" + randomString(8)
	encryptedKey := encryptField(apiKey)

	consumer := &Consumer{
		ID:         id,
		Name:       name,
		APIKey:     encryptedKey,
		InviteCode: inviteCode,
		CreatedAt:  time.Now().Format(time.RFC3339),
		Enabled:    true,
	}
	m.consumers[id] = consumer
	// Store SHA-256 hash of the API key instead of plaintext
	m.apiKeyMap[hashAPIKey(apiKey)] = id

	// Update invite code usage
	inv.UseCount++
	inv.UsedBy = id
	inv.UsedAt = time.Now().Format(time.RFC3339)

	m.save()
	slog.Info("consumer created", "id", id, "name", name)

	// Return a copy with plaintext key for caller display
	result := *consumer
	result.APIKey = apiKey
	return &result, nil
}

// ValidateAPIKey checks if an API key belongs to a valid consumer.
func (m *MultiUserManager) ValidateAPIKey(apiKey string) (*Consumer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Look up by SHA-256 hash
	consumerID, ok := m.apiKeyMap[hashAPIKey(apiKey)]
	if !ok {
		return nil, false
	}
	consumer, ok := m.consumers[consumerID]
	if !ok || !consumer.Enabled {
		return nil, false
	}
	return consumer, true
}

// RecordConsumerUsage updates a consumer's usage stats (batched save).
func (m *MultiUserManager) RecordConsumerUsage(consumerID string, tokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.consumers[consumerID]; ok {
		c.TotalTokens += int64(tokens)
		c.TotalRequests++
		// Batch save: increment dirty counter, let batchSaveLoop handle disk write
		count := m.dirtyCount.Add(1)
		if count >= 10 {
			// Immediate save if 10+ changes accumulated
			m.dirtyCount.Store(0)
			m.mu.Unlock()
			m.save()
			m.mu.Lock()
		}
	}
}

// ListInviteCodes returns all invite codes.
func (m *MultiUserManager) ListInviteCodes() []InviteCode {
	m.mu.RLock()
	defer m.mu.RUnlock()

	codes := make([]InviteCode, 0, len(m.invites))
	for _, inv := range m.invites {
		codes = append(codes, *inv)
	}
	return codes
}

// ListConsumers returns all consumers (with masked API keys).
func (m *MultiUserManager) ListConsumers() []Consumer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]Consumer, 0, len(m.consumers))
	for _, c := range m.consumers {
		safe := *c
		// Decrypt for display then mask
		displayKey := c.APIKey
		if IsEncrypted(c.APIKey) {
			displayKey = decryptField(c.APIKey)
		}
		if len(displayKey) > 8 {
			safe.APIKey = displayKey[:6] + "..." + displayKey[len(displayKey)-4:]
		} else {
			safe.APIKey = "***"
		}
		list = append(list, safe)
	}
	return list
}

// GetConsumerFull returns a consumer with full API key (admin only).
func (m *MultiUserManager) GetConsumerFull(id string) (*Consumer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.consumers[id]
	if !ok {
		return nil, false
	}
	result := *c
	// Decrypt API key for admin display
	if IsEncrypted(result.APIKey) {
		result.APIKey = decryptField(result.APIKey)
	}
	return &result, true
}

// DeleteConsumer removes a consumer and their providers.
func (m *MultiUserManager) DeleteConsumer(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.consumers[id]
	if !ok {
		return false
	}
	// Remove the SHA-256 hash from apiKeyMap
	plaintextKey := c.APIKey
	if IsEncrypted(c.APIKey) {
		plaintextKey = decryptField(c.APIKey)
	}
	delete(m.apiKeyMap, hashAPIKey(plaintextKey))
	delete(m.consumers, id)
	m.save()

	// Also remove all providers owned by this consumer
	pm.DeleteByOwner(id)

	slog.Info("consumer deleted", "id", id)
	return true
}

// ToggleConsumer enables/disables a consumer.
func (m *MultiUserManager) ToggleConsumer(id string, enabled bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.consumers[id]
	if !ok {
		return false
	}
	c.Enabled = enabled
	m.save()
	return true
}

// DeleteInviteCode removes an invite code.
func (m *MultiUserManager) DeleteInviteCode(code string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.invites[code]
	if !ok {
		return false
	}
	delete(m.invites, code)
	m.save()
	return true
}

// FlushSaves forces a save of any pending consumer usage data.
func (m *MultiUserManager) FlushSaves() {
	if m.dirtyCount.Load() > 0 {
		m.mu.Lock()
		m.dirtyCount.Store(0)
		m.mu.Unlock()
		m.save()
	}
}

// StopBatchSave stops the batch save goroutine.
func (m *MultiUserManager) StopBatchSave() {
	select {
	case m.saveStopCh <- struct{}{}:
	default:
	}
}

// ============================================================
// Consumer-authenticated API middleware and handlers
// ============================================================

// withConsumerOrAdminAuth checks for either admin JWT or consumer API key.
// Sets context values for downstream handlers.
func withConsumerOrAdminAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Try admin JWT first
		token := extractToken(r)
		if token != "" {
			if _, err := auth.VerifyToken(token); err == nil {
				// Admin access - set owner to "" (admin sees all)
				r.Header.Set("X-Request-Owner", "")
				r.Header.Set("X-Request-Role", "admin")
				handler(w, r)
				return
			}
		}

		// Try consumer API key
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			apiKey := authHeader[7:]
			consumer, ok := multiUser.ValidateAPIKey(apiKey)
			if ok {
				r.Header.Set("X-Request-Owner", consumer.ID)
				r.Header.Set("X-Request-Role", "consumer")
				handler(w, r)
				return
			}
		}

		writeJSON(w, 401, map[string]string{"error": "not authenticated"})
	}
}

// getRequestOwner returns the owner ID from request context.
// Empty string means admin (full access).
func getRequestOwner(r *http.Request) string {
	return r.Header.Get("X-Request-Owner")
}

func isAdmin(r *http.Request) bool {
	return r.Header.Get("X-Request-Role") == "admin"
}

// ============================================================
// Invite Code API Handlers (admin only)
// ============================================================

func handleListInviteCodes(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeError(w, 403, "admin only")
		return
	}
	codes := multiUser.ListInviteCodes()
	writeJSON(w, 200, map[string]any{"invite_codes": codes})
}

func handleCreateInviteCode(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeError(w, 403, "admin only")
		return
	}
	var body struct {
		MaxUses int    `json:"max_uses"` // 0 = single use
		Role    string `json:"role"`     // consumer (default) or collaborator
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.MaxUses < 0 {
		body.MaxUses = 0
	}
	if body.Role != "" && body.Role != "consumer" && body.Role != "collaborator" {
		writeError(w, 400, "invalid role, must be consumer or collaborator")
		return
	}
	code := multiUser.CreateInviteCode(body.MaxUses, body.Role)
	writeJSON(w, 200, map[string]any{"success": true, "code": code, "max_uses": body.MaxUses, "role": body.Role})
}

func handleDeleteInviteCode(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeError(w, 403, "admin only")
		return
	}
	code := r.PathValue("code")
	if !multiUser.DeleteInviteCode(code) {
		writeError(w, 404, "invite code not found")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

// ============================================================
// Consumer API Handlers (admin only)
// ============================================================

func handleListConsumers(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeError(w, 403, "admin only")
		return
	}
	consumers := multiUser.ListConsumers()
	writeJSON(w, 200, map[string]any{"consumers": consumers})
}

func handleCreateConsumer(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeError(w, 403, "admin only")
		return
	}
	var body struct {
		Name       string `json:"name"`
		InviteCode string `json:"invite_code"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Name == "" || body.InviteCode == "" {
		writeError(w, 400, "name and invite_code required")
		return
	}
	consumer, err := multiUser.CreateConsumer(body.Name, body.InviteCode)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "consumer": consumer})
}

func handleDeleteConsumer(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeError(w, 403, "admin only")
		return
	}
	id := r.PathValue("id")
	if !multiUser.DeleteConsumer(id) {
		writeError(w, 404, "consumer not found")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

func handleToggleConsumer(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeError(w, 403, "admin only")
		return
	}
	id := r.PathValue("id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if !multiUser.ToggleConsumer(id, body.Enabled) {
		writeError(w, 404, "consumer not found")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}

// handleConsumerRegister - public endpoint for self-registration with invite code
func handleConsumerRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string `json:"name"`
		InviteCode string `json:"invite_code"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.Name == "" || body.InviteCode == "" {
		writeError(w, 400, "name and invite_code required")
		return
	}
	if !multiUser.ValidateInviteCode(body.InviteCode) {
		writeError(w, 400, "invalid or expired invite code")
		return
	}
	consumer, err := multiUser.CreateConsumer(body.Name, body.InviteCode)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"success":  true,
		"consumer": consumer,
		"message":  "注册成功，请保存你的 API Key，后续请求需要携带",
	})
}

// handleUpdateConsumer - PUT /api/consumers/{id} - update consumer disabled state
func handleUpdateConsumer(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		writeError(w, 403, "admin only")
		return
	}
	id := r.PathValue("id")
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	// disabled=true means enabled=false, so invert
	if !multiUser.ToggleConsumer(id, !body.Disabled) {
		writeError(w, 404, "consumer not found")
		return
	}
	writeJSON(w, 200, map[string]any{"success": true})
}
