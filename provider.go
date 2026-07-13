package main

import (
	cryptoRand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProviderManager handles provider CRUD, model discovery, and smart routing.
type ProviderManager struct {
	mu        sync.RWMutex
	providers map[string]Provider // user-configured
	dataPath  string

	// cache
	cacheValid bool
	cachedAll  []Provider

	// P0-4: saveMu serializes file write operations to prevent data corruption
	saveMu sync.Mutex
}

var pm *ProviderManager

func initProviderManager(path string) {
	pm = &ProviderManager{
		providers: make(map[string]Provider),
		dataPath:  path,
	}
	pm.load()
}

func (m *ProviderManager) load() {
	b, err := os.ReadFile(m.dataPath)
	if err != nil {
		return
	}
	var list []Provider
	if json.Unmarshal(b, &list) == nil {
		for _, p := range list {
			// Decrypt API key if encrypted (legacy single key)
			if p.APIKey != "" && IsEncrypted(p.APIKey) {
				p.APIKey = decryptField(p.APIKey)
			}
			// Decrypt API keys in multi-key array
			for i := range p.APIKeys {
				if p.APIKeys[i].Key != "" && IsEncrypted(p.APIKeys[i].Key) {
					p.APIKeys[i].Key = decryptField(p.APIKeys[i].Key)
				}
			}
			// Migrate legacy single APIKey to APIKeys array
			migrated := migrateProviderKeys(&p)
			// Apply default access control if not set
			p.AccessControl = normalizeAccessControl(p.AccessControl)
			m.providers[p.ID] = p
			if migrated {
				slog.Info("migrated provider to multi-key format", "provider", p.ID)
			}
		}
		slog.Info("providers loaded", "count", len(m.providers))
	for _, p := range m.providers {
		for _, k := range p.APIKeys {
			slog.Info("after load", "provider", p.ID, "keyID", k.ID, "keyLen", len(k.Key), "isEncrypted", IsEncrypted(k.Key))
		}
	}
	}
}

// migrateProviderKeys migrates a legacy single APIKey to the APIKeys array.
// Returns true if migration occurred.
func migrateProviderKeys(p *Provider) bool {
	if p.APIKey != "" && len(p.APIKeys) == 0 {
		p.APIKeys = []APIKeyConfig{
			{
				ID:            generateKeyID(),
				Key:           p.APIKey,
				Quota:         p.TokenLimit, // inherit global quota
				AccessControl: "private",
				Enabled:       true,
				Priority:      1,
				CreatedAt:     time.Now().Format(time.RFC3339),
			},
		}
		p.APIKey = "" // clear legacy field
		return true
	}
	return false
}

// generateKeyID generates a unique ID for an API key config.
func generateKeyID() string {
	b := make([]byte, 8)
	cryptoRand.Read(b)
	return fmt.Sprintf("key_%x", b)
}

// save persists providers to disk safely. Call this when m.mu is NOT held.
// P0-4: Uses saveMu to serialize file writes, preventing data corruption.
func (m *ProviderManager) save() {
	// Take a snapshot of providers under the data lock
	m.mu.RLock()
	list := m.makeProviderListLocked()
	wasValid := m.cacheValid
	m.mu.RUnlock()

	// Serialize file writes
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	m.writeFile(list)
	if wasValid {
		m.mu.Lock()
		m.cacheValid = false
		m.mu.Unlock()
	}
}

// saveLocked persists providers to disk. Caller MUST hold m.mu (Lock or RLock).
// Used internally by methods that already hold the data lock.
func (m *ProviderManager) saveLocked() {
	list := m.makeProviderListLocked()
	m.cacheValid = false

	// Serialize file writes (saveMu never held with m.mu to prevent deadlock)
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.writeFile(list)
}

// makeProviderListLocked creates an encrypted copy of all providers.
// Caller must hold at least m.mu.RLock().
func (m *ProviderManager) makeProviderListLocked() []Provider {
	list := make([]Provider, 0, len(m.providers))
	for _, p := range m.providers {
		// Deep copy APIKeys slice to avoid modifying in-memory data
		if len(p.APIKeys) > 0 {
			keysCopy := make([]APIKeyConfig, len(p.APIKeys))
			copy(keysCopy, p.APIKeys)
			p.APIKeys = keysCopy
		}
		
		if p.APIKey != "" && !IsEncrypted(p.APIKey) {
			p.APIKey = encryptField(p.APIKey)
		}
		for i := range p.APIKeys {
			if p.APIKeys[i].Key != "" && !IsEncrypted(p.APIKeys[i].Key) {
				p.APIKeys[i].Key = encryptField(p.APIKeys[i].Key)
			}
		}
		list = append(list, p)
	}
	return list
}

// writeFile writes the provider list to disk. Called under saveMu only.
func (m *ProviderManager) writeFile(list []Provider) {
	b, _ := json.MarshalIndent(list, "", "  ")
	os.MkdirAll("data", 0755)
	os.WriteFile(m.dataPath, b, 0600) // P0-4: restrict file permissions to owner-only
}

// GetAll returns all providers (configured + preset), cached. This is the unified pool for routing.
func (m *ProviderManager) GetAll() []Provider {
	m.mu.RLock()
	if m.cacheValid {
		defer m.mu.RUnlock()
		return m.cachedAll
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	seen := make(map[string]bool)
	var result []Provider

	// Configured first
	for _, p := range m.providers {
		result = append(result, p.Safe())
		seen[p.ID] = true
	}
	// Then presets
	for _, p := range presetProviders {
		if !seen[p.ID] {
			result = append(result, p.Safe())
			seen[p.ID] = true
		}
	}

	m.cachedAll = result
	m.cacheValid = true
	return result
}

// GetConfigured returns only user-configured providers (excluding preset templates without keys).
func (m *ProviderManager) GetConfigured() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []Provider
	for _, p := range m.providers {
		result = append(result, p.Safe())
	}
	return result
}

// GetAllRaw returns all providers with full API keys (internal use, for routing).
func (m *ProviderManager) GetAllRaw() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]bool)
	var result []Provider

	for _, p := range m.providers {
		result = append(result, p)
		seen[p.ID] = true
	}
	for _, p := range presetProviders {
		if !seen[p.ID] {
			result = append(result, p)
			seen[p.ID] = true
		}
	}
	return result
}

// GetVisible returns providers visible to a specific owner.
// owner="" means admin (sees all). Otherwise only sees own + presets.
func (m *ProviderManager) GetVisible(owner string) []Provider {
	if owner == "" {
		return m.GetAll() // admin sees everything
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]bool)
	var result []Provider

	// Own providers
	for _, p := range m.providers {
		if p.Owner == owner {
			result = append(result, p.Safe())
			seen[p.ID] = true
		}
	}
	// Presets (system-level, visible to all)
	for _, p := range presetProviders {
		if !seen[p.ID] {
			safe := p.Safe()
			safe.Owner = "system"
			result = append(result, safe)
			seen[p.ID] = true
		}
	}

	return result
}

// DeleteByOwner removes all providers owned by a specific consumer.
func (m *ProviderManager) DeleteByOwner(owner string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for id, p := range m.providers {
		if p.Owner == owner {
			delete(m.providers, id)
			count++
		}
	}
	if count > 0 {
		m.saveLocked()
		slog.Info("deleted providers by owner", "owner", owner, "count", count)
	}
	return count
}

// GetRaw returns a provider with full API key (internal use).
func (m *ProviderManager) GetRaw(id string) (Provider, bool) {
	m.mu.RLock()
	if p, ok := m.providers[id]; ok {
		m.mu.RUnlock()
		return p, true
	}
	m.mu.RUnlock()

	for _, p := range presetProviders {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Get returns a masked provider.
func (m *ProviderManager) Get(id string) (Provider, bool) {
	p, ok := m.GetRaw(id)
	if !ok {
		return Provider{}, false
	}
	return p.Safe(), true
}

// normalizeAccessControl ensures sensible defaults for access control (v2.0).
// If neither flag is explicitly set (both false, the zero value), default to guest+shared.
func normalizeAccessControl(ac ProviderAccessControl) ProviderAccessControl {
	// Zero value means "not set" — apply defaults (guest=true, share_to_pool=true)
	if !ac.AllowGuest && !ac.ShareToPool {
		return DefaultAccessControl()
	}
	return ac
}

// Add adds or updates a provider.
func (m *ProviderManager) Add(p Provider) Provider {
	m.mu.Lock()
	now := time.Now().Format(time.RFC3339)
	if existing, ok := m.providers[p.ID]; ok {
		p.CreatedAt = existing.CreatedAt
	} else {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	p.AccessControl = normalizeAccessControl(p.AccessControl)
	m.providers[p.ID] = p
	m.mu.Unlock()

	m.save()
	slog.Info("provider saved", "id", p.ID, "name", p.Name)
	return p.Safe()
}

// Delete removes a provider.
func (m *ProviderManager) Delete(id string) bool {
	m.mu.Lock()
	_, inMemory := m.providers[id]
	if inMemory {
		delete(m.providers, id)
	}
	// For preset providers, add a disabled stub to override the preset
	if !inMemory {
		for _, p := range presetProviders {
			if p.ID == id {
				m.providers[id] = Provider{
					ID: id, Name: p.Name, Type: p.Type,
					BaseURL: p.BaseURL, Enabled: false, APIKey: "",
					Models: []ModelDef{},
				}
				inMemory = true
				break
			}
		}
	}
	m.mu.Unlock()
	if inMemory {
		m.save()
	}
	return inMemory
}

// Enabled returns all enabled providers.
func (m *ProviderManager) Enabled() []Provider {
	var out []Provider
	for _, p := range m.GetAll() {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

// EnabledRaw returns enabled providers with unmasked fields (internal use: health check, routing).
func (m *ProviderManager) EnabledRaw() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]bool)
	var out []Provider
	for _, p := range m.providers {
		if p.Enabled {
			out = append(out, p)
			seen[p.ID] = true
		}
	}
	for _, p := range presetProviders {
		if p.Enabled && !seen[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

// ============================================================
// Routing
// ============================================================

type candidate struct {
	Provider Provider
	Model    string
}

// RequestKeyType classifies the request key type for access control (v2.0).
// Returns "admin", "guest", "public", or "proxy".
func RequestKeyType(r *http.Request) string {
	// Priority 1: If relay already classified the key type, use it
	if mkType := r.Header.Get("X-MK-KeyType"); mkType != "" {
		return mkType
	}

	// Priority 2: Check role header (set by withProxyAuth)
	if role := r.Header.Get("X-Request-Role"); role == "admin" {
		return "admin"
	}

	// Priority 3: Check Authorization header directly
	auth := r.Header.Get("Authorization")
	key := strings.TrimPrefix(auth, "Bearer ")
	switch ClassifyKey(key) {
	case KeyTypePublic:
		return "public"
	case KeyTypeGuest:
		// Check if this guest key has public pool access
		_, accessPool, valid := GetGuestKeyAccessPublicPool(key)
		if valid && accessPool {
			return "public"
		}
		return "guest"
	case KeyTypeProxy:
		return "proxy"
	}

	// Unknown keys are rejected (fail-closed)
	return "unknown"
}

// FilterByAccessControl filters candidates based on the provider's access control
// and the request's key type (v2.0, updated v3.1).
//
// Design principle:
// - Admin keys: unrestricted access to all providers
// - Proxy keys (sk-{random}): v3.1 behavior depends on share_to_pool:
//   - share_to_pool=true: Proxy API Key can access the full network shared pool
//   - share_to_pool=false: Proxy API Key can only access this node's own providers
// - Guest keys (sk-guest-*): controlled by per-provider AllowGuest flag
// - Public key (sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1): accesses providers with ShareToPool=true
func FilterByAccessControl(cands []candidate, keyType string) []candidate {
	if keyType == "admin" {
		return cands // admin keys always have unrestricted access
	}

	// v3.1: Proxy API Key behavior depends on share_to_pool
	if keyType == "proxy" {
		if netMgr != nil && netMgr.IsSharingToPool() {
			return cands // share_to_pool=true: proxy key can access the full network pool
		}
		// share_to_pool=false: proxy key can only access this node's own providers
		// All local providers are accessible; remote (relayed) providers are not
		return cands // local providers are always accessible via proxy key
	}

	filtered := make([]candidate, 0, len(cands))
	for _, c := range cands {
		ac := c.Provider.AccessControl
		switch keyType {
		case "guest":
			if ac.AllowGuest {
				filtered = append(filtered, c)
			}
		case "public":
			// Public key (sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1) accesses all providers shared to the pool.
			// ShareToPool defaults to true for all providers on shared-network nodes.
			if ac.ShareToPool {
				filtered = append(filtered, c)
			}
		default:
			filtered = append(filtered, c) // unknown type → allow
		}
	}
	return filtered
}

// FindCandidates returns all enabled providers that have the given model.
func (m *ProviderManager) FindCandidates(model string) []candidate {
	var out []candidate
	for _, p := range m.GetAll() {
		if !p.Enabled {
			continue
		}
		for _, mdl := range p.Models {
			if mdl.ID == model && mdl.Enabled {
				out = append(out, candidate{Provider: p, Model: model})
				break
			}
		}
	}
	return out
}

// filterExpired removes providers with expired tokens (Sider).
func filterExpired(cands []candidate) []candidate {
	if !siderMon.IsExpired() {
		return cands
	}
	out := cands[:0]
	for _, c := range cands {
		if c.Provider.ID != "sider" {
			out = append(out, c)
		}
	}
	return out
}

// ResolveRoute picks the best provider for a model using the given routing mode.
func (m *ProviderManager) ResolveRoute(model, mode string) (Provider, string, bool) {
	// Explicit provider/model format (e.g. "deepseek/deepseek-chat")
	if idx := strings.Index(model, "/"); idx > 0 {
		prefix := model[:idx]
		// Skip known org prefixes (OpenRouter-style)
		if !isOrgPrefix(prefix) {
			modelID := model[idx+1:]
			if p, ok := m.GetRaw(prefix); ok && p.Enabled {
				return p, modelID, true
			}
		}
	}

	cands := m.FindCandidates(model)
	cands = filterExpired(cands)
	if len(cands) == 0 {
		return Provider{}, model, false
	}
	if len(cands) == 1 {
		return cands[0].Provider, cands[0].Model, true
	}

	m.sortCandidates(&cands, mode)
	return cands[0].Provider, cands[0].Model, true
}

// OrderedCandidates returns sorted candidates for fallback chain.
func (m *ProviderManager) OrderedCandidates(model, mode string) []candidate {
	cands := m.FindCandidates(model)
	cands = filterExpired(cands)
	if len(cands) == 0 {
		return nil
	}
	m.sortCandidates(&cands, mode)
	return cands
}

func isOrgPrefix(s string) bool {
	switch s {
	case "Qwen", "deepseek-ai", "meta-llama", "openai", "anthropic", "google":
		return true
	}
	return false
}

// ============================================================
// Sort logic
// ============================================================

func (m *ProviderManager) sortCandidates(cands *[]candidate, mode string) {
	switch mode {
	case "priority":
		sort.SliceStable(*cands, func(i, j int) bool {
			return (*cands)[i].Provider.Priority < (*cands)[j].Provider.Priority
		})
	case "cheapest":
		sort.SliceStable(*cands, func(i, j int) bool {
			ci, cj := (*cands)[i], (*cands)[j]
			pi := getPricing(ci.Model, ci.Provider.ID)
			pj := getPricing(cj.Model, cj.Provider.ID)
			si := pi.Input + pi.Output
			sj := pj.Input + pj.Output
			if si == 0 { si = 0.001 }
			if sj == 0 { sj = 0.001 }
			return si < sj
		})
	case "fastest":
		sort.SliceStable(*cands, func(i, j int) bool {
			ei := tracker.GetEWMA((*cands)[i].Provider.ID)
			ej := tracker.GetEWMA((*cands)[j].Provider.ID)
			if ei == 0 { ei = 5000 }
			if ej == 0 { ej = 5000 }
			return ei < ej
		})
	case "auto":
		m.sortAuto(cands)
	default:
		sort.SliceStable(*cands, func(i, j int) bool {
			return (*cands)[i].Provider.Priority < (*cands)[j].Provider.Priority
		})
	}
}

func (m *ProviderManager) sortAuto(cands *[]candidate) {
	weights := m.getWeights()
	wp := weights["priority"]
	wc := weights["cost"]
	wl := weights["latency"]
	wt := weights["tokens"]

	c := *cands
	n := len(c)
	if n <= 1 {
		return
	}

	// Compute raw scores
	pries := make([]float64, n)
	costs := make([]float64, n)
	lats := make([]float64, n)
	remaining := make([]float64, n)
	hasLimit := make([]bool, n)

	tokensUsed := tracker.TotalTokensByProvider()

	for i, ci := range c {
		pries[i] = float64(ci.Provider.Priority)
		p := getPricing(ci.Model, ci.Provider.ID)
		costs[i] = p.Input + p.Output
		lats[i] = tracker.GetEWMA(ci.Provider.ID)
		if lats[i] == 0 {
			lats[i] = 5000
		}
		limit := ci.Provider.TokenLimit
		if limit > 0 {
			used := tokensUsed[ci.Provider.ID]
			rem := limit - used
			if rem < 0 {
				rem = 0
			}
			remaining[i] = float64(rem)
			hasLimit[i] = true
		}
	}

	// Normalize
	minPri, maxPri := minMax(pries)
	minCost, maxCost := minMax(costs)
	minLat, maxLat := minMax(lats)

	// Token remaining: only consider providers with limits
	var boundedRem []float64
	for i := range c {
		if hasLimit[i] {
			boundedRem = append(boundedRem, remaining[i])
		}
	}
	minTok, maxTok := 0.0, 1.0
	if len(boundedRem) > 0 {
		minTok, maxTok = minMax(boundedRem)
	}
	tokRange := maxTok - minTok
	if tokRange == 0 {
		tokRange = 1
	}

	scores := make([]float64, n)
	for i := range c {
		priNorm := 1 - normalize(pries[i], minPri, maxPri)
		costNorm := 1 - normalize(costs[i], minCost, maxCost)
		latNorm := 1 - normalize(lats[i], minLat, maxLat)

		var tokNorm float64
		if !hasLimit[i] {
			tokNorm = 0.5 // unlimited → neutral score
		} else {
			tokNorm = normalize(remaining[i], minTok, maxTok)
		}

		scores[i] = wp*priNorm + wc*costNorm + wl*latNorm + wt*tokNorm
	}

	sort.SliceStable(c, func(i, j int) bool {
		return scores[i] > scores[j] // higher is better
	})
}

func (m *ProviderManager) getWeights() map[string]float64 {
	w := map[string]float64{"priority": 0.4, "cost": 0.25, "latency": 0.2, "tokens": 0.15}
	s := cfg.Get("routing_weights", "")
	if s != "" {
		var parsed map[string]float64
		if json.Unmarshal([]byte(s), &parsed) == nil {
			for k, v := range parsed {
				w[k] = v
			}
		}
	}
	return w
}

func normalize(val, min, max float64) float64 {
	if max == min {
		return 0
	}
	return (val - min) / (max - min)
}

func minMax(a []float64) (min, max float64) {
	if len(a) == 0 {
		return 0, 1
	}
	min, max = a[0], a[0]
	for _, v := range a[1:] {
		if v < min { min = v }
		if v > max { max = v }
	}
	return
}

// ============================================================
// Model list (OpenAI format)
// ============================================================

func (m *ProviderManager) AllModels() []ModelInfo {
	seen := make(map[string]bool)
	var models []ModelInfo

	for _, p := range m.GetAll() {
		if !p.Enabled {
			continue
		}
		for _, mdl := range p.Models {
			if !mdl.Enabled || seen[mdl.ID] {
				continue
			}
			models = append(models, ModelInfo{
				ID:      mdl.ID,
				Object:  "model",
				Created: time.Now().Unix(),
				OwnedBy: p.Name,
			})
			seen[mdl.ID] = true
		}
	}

	// Coze bot model
	if raw, ok := m.GetRaw("coze"); ok && raw.Enabled && raw.APIKey != "" {
		botID := cfg.Get("coze_bot_id", "")
		if botID != "" {
			id := "coze-" + botID
			if !seen[id] {
				models = append(models, ModelInfo{
					ID: id, Object: "model",
					Created: time.Now().Unix(), OwnedBy: "Coze",
				})
			}
		}
	}
	return models
}

// AllModelsFiltered returns models filtered by access control based on key type.
// Admin keys see all models; private/shared keys only see models from allowed providers.
func (m *ProviderManager) AllModelsFiltered(keyType string) []ModelInfo {
	if keyType == "admin" {
		return m.AllModels()
	}

	seen := make(map[string]bool)
	var models []ModelInfo

	for _, p := range m.GetAll() {
		if !p.Enabled {
			continue
		}
		// Check access control
		if !providerAllowsKeyType(p, keyType) {
			continue
		}
		for _, mdl := range p.Models {
			if !mdl.Enabled || seen[mdl.ID] {
				continue
			}
			models = append(models, ModelInfo{
				ID:      mdl.ID,
				Object:  "model",
				Created: time.Now().Unix(),
				OwnedBy: p.Name,
			})
			seen[mdl.ID] = true
		}
	}

	// Coze bot model (only for admin)
	if keyType == "admin" {
		if raw, ok := m.GetRaw("coze"); ok && raw.Enabled && raw.APIKey != "" {
			botID := cfg.Get("coze_bot_id", "")
			if botID != "" {
				id := "coze-" + botID
				if !seen[id] {
					models = append(models, ModelInfo{
						ID: id, Object: "model",
						Created: time.Now().Unix(), OwnedBy: "Coze",
					})
				}
			}
		}
	}
	return models
}

// providerAllowsKeyType checks if a provider allows a given key type (v2.0).
// Checks both provider-level and key-level access control.
func providerAllowsKeyType(p Provider, keyType string) bool {
	ac := p.AccessControl
	switch keyType {
	case "admin", "proxy":
		return true // proxy/admin keys always allowed
	case "guest":
		return ac.AllowGuest && hasNonPrivateKey(p)
	case "public":
		// Public key: accessible if provider is shared to the pool AND has at least one non-private key
		return ac.ShareToPool && hasNonPrivateKey(p)
	default:
		return false // unknown → deny (fail-closed)
	}
}

// hasNonPrivateKey checks if a provider has at least one API key that is not "private".
// If all keys are private, the provider's models should not be visible externally.
func hasNonPrivateKey(p Provider) bool {
	// Check multi-key array first
	for _, k := range p.APIKeys {
		if k.Enabled && k.AccessControl != "private" {
			return true
		}
	}
	// If no APIKeys array, check legacy APIKey field (assume shared if present)
	if len(p.APIKeys) == 0 && p.APIKey != "" {
		return true
	}
	return false
}

// RoutingAdvice returns comparison info for a model across providers.
func (m *ProviderManager) RoutingAdvice(model string) []map[string]any {
	cands := m.FindCandidates(model)
	stats := tracker.ProviderStats(7)

	var out []map[string]any
	for _, c := range cands {
		p := c.Provider
		pid := p.ID
		pricing := getPricing(c.Model, pid)
		s := stats[pid]
		ewma := tracker.GetEWMA(pid)

		tokenStatus := ""
		if pid == "sider" {
			if siderMon.IsExpired() {
				tokenStatus = "expired"
			} else {
				tokenStatus = "ok"
			}
		}

		out = append(out, map[string]any{
			"provider_id":     pid,
			"provider_name":   p.Name,
			"priority":        p.Priority,
			"input_price":     pricing.Input,
			"output_price":    pricing.Output,
			"ewma_latency_ms": ewma,
			"request_count_7d": s["request_count"],
			"success_rate":    s["success_rate"],
			"total_cost_usd":  s["total_cost_usd"],
			"token_status":    tokenStatus,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i]["priority"].(int) < out[j]["priority"].(int)
	})
	return out
}

// SyncModels fetches the available model list from an OpenAI-compatible provider
// and updates the provider's Models field. Returns the number of models synced.
// When multiple API keys exist, it fetches models per key and tracks per-key availability.
func (m *ProviderManager) SyncModels(providerID string) (int, error) {
	p, ok := m.GetRaw(providerID)
	if !ok {
		return 0, fmt.Errorf("provider '%s' not found", providerID)
	}

	if p.Type != "openai_compatible" {
		return 0, fmt.Errorf("sync only supported for openai_compatible providers (current type: %s)", p.Type)
	}

	if p.APIKey == "" && len(p.APIKeys) == 0 {
		return 0, fmt.Errorf("provider '%s' has no API key configured", providerID)
	}

	// Preserve existing enabled state
	existingModels := make(map[string]ModelDef)
	for _, md := range p.Models {
		existingModels[md.ID] = md
	}

	// Build per-key model availability
	type keyModels struct {
		keyID   string
		modelIDs []string
	}
	var keyResults []keyModels
	allModelIDs := make(map[string]string) // id -> name

	// Fetch models from each key
	if len(p.APIKeys) > 0 {
		for _, k := range p.APIKeys {
			if !k.Enabled {
				continue
			}
			decryptedKey, err := decryptAPIKey(k.Key)
			if err != nil {
				slog.Warn("failed to decrypt key for sync", "key_id", k.ID, "error", err)
				continue
			}
			tmp := p
			tmp.APIKey = decryptedKey
			models := fetchRemoteModels(tmp)
			var ids []string
			for _, rm := range models {
				ids = append(ids, rm["id"])
				allModelIDs[rm["id"]] = rm["name"]
			}
			keyResults = append(keyResults, keyModels{keyID: k.ID, modelIDs: ids})
		}
	} else {
		// Legacy single key - decrypt if encrypted
		if p.APIKey != "" {
			decrypted, err := decryptAPIKey(p.APIKey)
			if err == nil {
				p.APIKey = decrypted
			} else {
				slog.Warn("failed to decrypt legacy key for sync", "error", err)
			}
		}
		models := fetchRemoteModels(p)
		var ids []string
		for _, rm := range models {
			ids = append(ids, rm["id"])
			allModelIDs[rm["id"]] = rm["name"]
		}
		keyResults = append(keyResults, keyModels{keyID: "default", modelIDs: ids})
	}

	if len(allModelIDs) == 0 {
		return 0, fmt.Errorf("no models returned from provider '%s'", providerID)
	}

	// Build available-by-key lookup
	modelKeyAvailability := make(map[string]map[string]bool) // modelID -> keyID -> available
	for _, kr := range keyResults {
		for _, mid := range kr.modelIDs {
			if modelKeyAvailability[mid] == nil {
				modelKeyAvailability[mid] = make(map[string]bool)
			}
			modelKeyAvailability[mid][kr.keyID] = true
		}
	}

	// Build new Models list
	var newModels []ModelDef
	for mid, mname := range allModelIDs {
		def := ModelDef{
			ID:   mid,
			Name: mname,
		}

		// Preserve existing state
		if existing, ok := existingModels[mid]; ok {
			def.Enabled = existing.Enabled
			def.EnabledByKeys = existing.EnabledByKeys
		}

		// Set available keys
		if avail, ok := modelKeyAvailability[mid]; ok {
			for keyID := range avail {
				def.AvailableKeys = append(def.AvailableKeys, keyID)
			}
			sort.Strings(def.AvailableKeys)
		}

		newModels = append(newModels, def)
	}

	// Sort models by ID for consistent ordering
	sort.Slice(newModels, func(i, j int) bool {
		return newModels[i].ID < newModels[j].ID
	})

	m.mu.Lock()
	if existing, ok := m.providers[providerID]; ok {
		existing.Models = newModels
		existing.UpdatedAt = time.Now().Format(time.RFC3339)
		m.providers[providerID] = existing
	}
	m.saveLocked()
	m.mu.Unlock()

	slog.Info("provider models synced", "provider", providerID, "count", len(newModels), "keys", len(keyResults))
	return len(newModels), nil
}

// ClearAllAPIKeys removes API keys from all providers (security measure for password reset).
func (m *ProviderManager) ClearAllAPIKeys() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for id, p := range m.providers {
		if p.APIKey != "" {
			p.APIKey = ""
			m.providers[id] = p
			count++
		}
		// Also clear multi-key array
		if len(p.APIKeys) > 0 {
			p.APIKeys = nil
			m.providers[id] = p
			count++
		}
	}
	if count > 0 {
		m.saveLocked()
		slog.Info("cleared all provider API keys", "count", count)
	}
	return count
}

// ============================================================
// Multi API Key Selection
// ============================================================

// keyRoundRobin is a per-provider round-robin counter for keys at the same priority.
var keyRoundRobin atomic.Uint64

// SelectAPIKey selects the best available API key for a provider based on
// access control, quota, expiration, and priority. It uses round-robin for
// keys at the same priority level.
// Returns the selected key config and the raw key string, or an error.
func (p *Provider) SelectAPIKey(accessType string) (*APIKeyConfig, error) {
	// If no multi-key array, fall back to legacy single key
	if len(p.APIKeys) == 0 {
		if p.APIKey == "" {
			return nil, errors.New("no API key configured")
		}
		// Return a synthetic key config for the legacy key
		return &APIKeyConfig{
			ID:            "legacy",
			Key:           p.APIKey,
			AccessControl: "private",
			Enabled:       true,
			Priority:      1,
		}, nil
	}

	var candidates []APIKeyConfig

	for _, key := range p.APIKeys {
		if !key.Enabled {
			continue
		}

		// Check access control
		if !keyAllowedForAccess(key.AccessControl, accessType) {
			continue
		}

		// Check quota
		if key.Quota > 0 && key.Used >= key.Quota {
			continue
		}

		// Check expiration
		if key.ExpiresAt != "" {
			expires, err := time.Parse(time.RFC3339, key.ExpiresAt)
			if err == nil && time.Now().After(expires) {
				continue
			}
		}

		candidates = append(candidates, key)
	}

	if len(candidates) == 0 {
		return nil, errors.New("no available API key for the given access level")
	}

	// Sort by priority descending (higher priority first)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Priority > candidates[j].Priority
	})

	// Find the top priority level
	topPriority := candidates[0].Priority

	// Collect all keys at the top priority level for round-robin
	var topCandidates []APIKeyConfig
	for _, c := range candidates {
		if c.Priority == topPriority {
			topCandidates = append(topCandidates, c)
		}
	}

	// Round-robin among same-priority keys
	idx := keyRoundRobin.Add(1) - 1
	selected := topCandidates[idx%uint64(len(topCandidates))]

	return &selected, nil
}

// keyAllowedForAccess checks if a key's access control allows the given access type.
func keyAllowedForAccess(keyAccess, accessType string) bool {
	switch keyAccess {
	case "public":
		return true // public keys are accessible by everyone
	case "shared":
		return accessType == "shared" || accessType == "private" || accessType == ""
	case "private":
		return accessType == "private" || accessType == ""
	default:
		return false // unknown access control → deny (fail-closed)
	}
}

// GetEffectiveAPIKey returns the best available API key string for a provider.
// This is a convenience method that wraps SelectAPIKey.
func (p *Provider) GetEffectiveAPIKey() string {
	key, err := p.SelectAPIKey("")
	if err != nil {
		return p.APIKey // fall back to legacy
	}
	return key.Key
}

// RecordKeyUsage records token usage against a specific API key.
func (m *ProviderManager) RecordKeyUsage(providerID, keyID string, tokens int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.providers[providerID]
	if !ok {
		return
	}
	for i := range p.APIKeys {
		if p.APIKeys[i].ID == keyID {
			p.APIKeys[i].Used += tokens
			m.providers[providerID] = p
			m.saveLocked()
			return
		}
	}
}

// ResetKeyQuota resets the used quota for a specific API key.
func (m *ProviderManager) ResetKeyQuota(providerID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.providers[providerID]
	if !ok {
		return fmt.Errorf("provider '%s' not found", providerID)
	}
	for i := range p.APIKeys {
		if p.APIKeys[i].ID == keyID {
			p.APIKeys[i].Used = 0
			p.APIKeys[i].UpdatedAt = time.Now().Format(time.RFC3339)
			m.providers[providerID] = p
			m.saveLocked()
			return nil
		}
	}
	return fmt.Errorf("key '%s' not found in provider '%s'", keyID, providerID)
}

// AddAPIKey adds a new API key to a provider.
func (m *ProviderManager) AddAPIKey(providerID string, key APIKeyConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.providers[providerID]
	if !ok {
		return fmt.Errorf("provider '%s' not found", providerID)
	}

	if key.ID == "" {
		key.ID = generateKeyID()
	}
	now := time.Now().Format(time.RFC3339)
	key.CreatedAt = now
	key.UpdatedAt = now
	if key.AccessControl == "" {
		key.AccessControl = "private"
	}

	p.APIKeys = append(p.APIKeys, key)
	p.UpdatedAt = now
	m.providers[providerID] = p
	m.saveLocked()

	slog.Info("API key added", "provider", providerID, "key_id", key.ID)
	return nil
}

// UpdateAPIKey updates an existing API key in a provider.
func (m *ProviderManager) UpdateAPIKey(providerID, keyID string, updates map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.providers[providerID]
	if !ok {
		return fmt.Errorf("provider '%s' not found", providerID)
	}

	for i := range p.APIKeys {
		if p.APIKeys[i].ID == keyID {
			if v, ok := updates["key"]; ok {
				if s, ok := v.(string); ok && s != "" && !strings.Contains(s, "...") {
					p.APIKeys[i].Key = s
				}
			}
			if v, ok := updates["quota"]; ok {
				if f, ok := v.(float64); ok {
					p.APIKeys[i].Quota = int64(f)
				}
			}
			if v, ok := updates["access_control"]; ok {
				if s, ok := v.(string); ok {
					p.APIKeys[i].AccessControl = s
				}
			}
			if v, ok := updates["enabled"]; ok {
				if b, ok := v.(bool); ok {
					p.APIKeys[i].Enabled = b
				}
			}
			if v, ok := updates["priority"]; ok {
				if f, ok := v.(float64); ok {
					p.APIKeys[i].Priority = int(f)
				}
			}
			if v, ok := updates["expires_at"]; ok {
				if s, ok := v.(string); ok {
					p.APIKeys[i].ExpiresAt = s
				}
			}
			if v, ok := updates["alias"]; ok {
				if s, ok := v.(string); ok {
					p.APIKeys[i].Alias = s
				}
			}
			p.APIKeys[i].UpdatedAt = time.Now().Format(time.RFC3339)
			m.providers[providerID] = p
			m.saveLocked()
			slog.Info("API key updated", "provider", providerID, "key_id", keyID)
			return nil
		}
	}
	return fmt.Errorf("key '%s' not found in provider '%s'", keyID, providerID)
}

// DeleteAPIKey removes an API key from a provider.
func (m *ProviderManager) DeleteAPIKey(providerID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.providers[providerID]
	if !ok {
		return fmt.Errorf("provider '%s' not found", providerID)
	}

	found := false
	var newKeys []APIKeyConfig
	for _, k := range p.APIKeys {
		if k.ID == keyID {
			found = true
			continue
		}
		newKeys = append(newKeys, k)
	}
	if !found {
		return fmt.Errorf("key '%s' not found in provider '%s'", keyID, providerID)
	}

	p.APIKeys = newKeys
	p.UpdatedAt = time.Now().Format(time.RFC3339)
	m.providers[providerID] = p
	m.saveLocked()

	slog.Info("API key deleted", "provider", providerID, "key_id", keyID)
	return nil
}

// GetAPIKeys returns all API keys for a provider (with masked key values).
func (m *ProviderManager) GetAPIKeys(providerID string) ([]APIKeyConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.providers[providerID]
	if !ok {
		return nil, fmt.Errorf("provider '%s' not found", providerID)
	}

	// Return masked keys with all fields populated
	result := make([]APIKeyConfig, len(p.APIKeys))
	for i, k := range p.APIKeys {
		result[i] = k // copy all fields
		// Mask the actual key value for security
		if len(k.Key) > 8 {
			result[i].Key = k.Key[:4] + "••••••••" + k.Key[len(k.Key)-4:]
		} else if k.Key != "" {
			result[i].Key = "••••••••"
		}
	}
	return result, nil
}

// enableLatestModels takes a list of models and only enables the "latest" ones.
// Strategy: sort by name descending, enable top 3 that look like stable releases.
// Models containing "preview", "beta", "experimental", "exp" are deprioritized.
func enableLatestModels(models []ModelDef) []ModelDef {
	if len(models) <= 3 {
		return models // if 3 or fewer, enable all
	}

	// Separate stable and preview models
	var stable, preview []ModelDef
	for _, m := range models {
		name := strings.ToLower(m.ID)
		if strings.Contains(name, "preview") || strings.Contains(name, "beta") ||
			strings.Contains(name, "experimental") || strings.Contains(name, "-exp") ||
			strings.Contains(name, "emb") || strings.Contains(name, "embed") ||
			strings.Contains(name, "tts") || strings.Contains(name, "image") ||
			strings.Contains(name, "video") || strings.Contains(name, "reward") ||
			strings.Contains(name, "safety") || strings.Contains(name, "guard") ||
			strings.Contains(name, "vision") || strings.Contains(name, "retriever") {
			preview = append(preview, m)
		} else {
			stable = append(stable, m)
		}
	}

	// Sort stable models by name descending (newer versions sort later)
	sort.Slice(stable, func(i, j int) bool {
		return stable[i].ID > stable[j].ID
	})

	// Enable top 3 stable models
	enabledSet := make(map[string]bool)
	count := 0
	for i := range stable {
		if count >= 3 {
			break
		}
		stable[i].Enabled = true
		enabledSet[stable[i].ID] = true
		count++
	}

	// Disable all others
	for i := range models {
		if !enabledSet[models[i].ID] {
			models[i].Enabled = false
		}
	}

	slog.Info("auto-enabled latest models", "count", count)
	return models
}
