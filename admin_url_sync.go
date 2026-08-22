package main

import (
	"fmt"
	"net/http"
)

// ============================================================
// Sync URL handlers
// ============================================================

func handleSyncProviderURL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := checkProviderWriteAccess(r, id) // B8-2
	if !ok {
		writeError(w, 404, fmt.Sprintf("provider '%s' not found", id))
		return
	}

	// Find matching preset
	var presetBaseURL string
	for _, preset := range presetProviders {
		if preset.ID == id {
			presetBaseURL = preset.BaseURL
			break
		}
	}

	if presetBaseURL == "" {
		writeJSON(w, 200, map[string]any{"changed": false, "message": "无匹配的预设平台，无法同步"})
		return
	}

	if p.BaseURL == presetBaseURL {
		writeJSON(w, 200, map[string]any{"changed": false, "message": "地址已是最新，无需更新"})
		return
	}

	oldURL := p.BaseURL
	p.BaseURL = presetBaseURL
	pm.Add(p)
	writeJSON(w, 200, map[string]any{
		"changed": true,
		"message": fmt.Sprintf("地址已从 %s 更新为 %s", oldURL, presetBaseURL),
		"old_url": oldURL,
		"new_url": presetBaseURL,
	})
}

func handleSyncAllURLs(w http.ResponseWriter, r *http.Request) {
	changed := 0
	allProviders := pm.GetAll()

	for _, p := range allProviders {
		if !p.Enabled {
			continue
		}
		var presetBaseURL string
		for _, preset := range presetProviders {
			if preset.ID == p.ID {
				presetBaseURL = preset.BaseURL
				break
			}
		}
		if presetBaseURL != "" && p.BaseURL != presetBaseURL {
			p.BaseURL = presetBaseURL
			pm.Add(p)
			changed++
		}
	}

	writeJSON(w, 200, map[string]any{"changed": changed, "total": len(allProviders)})
}