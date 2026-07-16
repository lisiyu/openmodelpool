package main

// CheckProviderNow triggers an immediate health check for a single provider.
func (h *HealthChecker) CheckProviderNow(providerID string) {
	p, ok := pm.Get(providerID)
	if !ok {
		return
	}
	if !p.Enabled {
		return
	}
	h.checkProvider(p)
}

// SetFailedKeyCount updates the failed key count for a provider from manual test results.
func (h *HealthChecker) SetFailedKeyCount(providerID string, count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hs, ok := h.statuses[providerID]; ok {
		hs.FailedKeyCount = count
	}
}
