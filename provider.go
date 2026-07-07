package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
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
			m.providers[p.ID] = p
		}
		slog.Info("providers loaded", "count", len(m.providers))
	}
}

func (m *ProviderManager) save() {
	list := make([]Provider, 0, len(m.providers))
	for _, p := range m.providers {
		list = append(list, p)
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	os.MkdirAll("data", 0755)
	os.WriteFile(m.dataPath, b, 0644)
	m.cacheValid = false
}

// GetAll returns all providers (configured + preset), cached.
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
	m.providers[p.ID] = p
	m.mu.Unlock()

	m.save()
	slog.Info("provider saved", "id", p.ID, "name", p.Name)
	return p.Safe()
}

// Delete removes a provider.
func (m *ProviderManager) Delete(id string) bool {
	m.mu.Lock()
	_, ok := m.providers[id]
	if ok {
		delete(m.providers, id)
	}
	m.mu.Unlock()
	if ok {
		m.save()
	}
	return ok
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

// ============================================================
// Routing
// ============================================================

type candidate struct {
	Provider Provider
	Model    string
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
