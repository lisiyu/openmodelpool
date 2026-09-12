package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ============================================================
// OpenAI v1/responses, v1/images, v1/audio downstream passthrough (P3-3(iii))
//
// These endpoints are NOT translated to /v1/chat/completions: their request
// bodies (input/prompt/voice), their request/response shapes, and their
// response content types (SSE for /v1/responses, audio bytes for
// /v1/audio/speech, JSON for /v1/images/generations) differ fundamentally
// from chat. The correct concrete is to forward the RAW body to the upstream
// OpenAI-compatible endpoint chosen for the model, using the provider's key,
// and stream the upstream response back verbatim.
// ============================================================

// passthroughSubPath maps an inbound gateway path to the suffix appended to a
// provider BaseURL. Provider BaseURLs are "/v1" style, so the leading "/v1"
// is dropped. Returns "" when the path is not a supported passthrough.
func passthroughSubPath(path string) string {
	switch path {
	case "/v1/responses":
		return "/responses"
	case "/v1/images/generations":
		return "/images/generations"
	case "/v1/audio/speech":
		return "/audio/speech"
	}
	return ""
}

// handleRawPassthrough forwards the raw request body to the upstream provider
// selected for model and streams the response back verbatim. Returns true when
// the request was fully handled (matched a passthrough path).
func handleRawPassthrough(w http.ResponseWriter, r *http.Request, bodyBytes []byte, model string) bool {
	sub := passthroughSubPath(r.URL.Path)
	if sub == "" {
		return false
	}

	// Reconstruct the request body for provider selection below, mirroring
	// handleGatewayFallback.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	r.ContentLength = int64(len(bodyBytes))

	// Stream detection: only /v1/responses supports streaming today.
	stream := false
	if r.URL.Path == "/v1/responses" {
		var m map[string]json.RawMessage
		if json.Unmarshal(bodyBytes, &m) == nil {
			if raw, ok := m["stream"]; ok {
				_ = json.Unmarshal(raw, &stream)
			}
		}
	}

	// Provider selection: same rules as handleChatCompletions.
	keyType := KeyTypeUnknown
	if auth := r.Header.Get("Authorization"); auth != "" {
		keyType = ClassifyKey(strings.TrimPrefix(auth, "Bearer "))
	}
	routingMode := cfg.Get("routing_mode", "priority")
	allCandidates := pm.OrderedCandidates(model, routingMode)
	candidates := FilterByAccessControl(allCandidates, string(keyType))
	if (keyType == KeyTypeGuest || keyType == KeyTypeProxy) && pm != nil {
		if local := filterLocalOnly(candidates); len(local) > 0 {
			candidates = local
		}
	}

	if len(candidates) == 0 {
		writeError(w, 404, fmt.Sprintf("no provider available for model '%s'", model))
		return true
	}

	consumerID := getRequestOwner(r)
	accessType := "private"
	switch keyType {
	case KeyTypePublic:
		accessType = "public"
	case KeyTypeGuest:
		accessType = "guest"
	}

	var lastErr error
	for idx, c := range candidates {
		p := c.Provider
		actualModel := c.Model

		if idx > 0 {
			slog.Warn("passthrough fallback", "model", model, "to", p.Name, "idx", idx, "mode", routingMode)
		}

		if p.APIKey == "" && len(p.APIKeys) > 0 {
			p.APIKey = p.GetEffectiveAPIKey()
		}
		if p.APIKey == "" {
			lastErr = fmt.Errorf("provider '%s' has no API key", p.Name)
			continue
		}

		startTime := time.Now()
		upstream := strings.TrimRight(p.BaseURL, "/") + sub
		req, err := http.NewRequestWithContext(r.Context(), "POST", upstream, bytes.NewReader(bodyBytes))
		if err != nil {
			lastErr = err
			continue
		}
		setOpenAIHeaders(req, p.APIKey)
		if stream {
			req.Header.Set("Accept", "text/event-stream")
		}

		// 300s deadline covers both SSE and long-running audio/image generation.
		resp, err := proxyHTTPClient(p, 300*time.Second).Do(req)
		latencyMS := float64(time.Since(startTime).Milliseconds())
		if err != nil {
			slog.Warn("passthrough upstream failed", "provider", p.Name, "model", actualModel, "error", err)
			lastErr = err
			recordProviderFailure(p.ID)
			tracker.RecordWithOwner(p.ID, p.Name, model, 0, 0, latencyMS, false, err.Error(), stream, 0, accessType, consumerID)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			slog.Warn("passthrough upstream non-2xx", "provider", p.Name, "status", resp.StatusCode)
			lastErr = fmt.Errorf("upstream error (%d): %s", resp.StatusCode, truncate(string(b), 200))
			recordProviderFailure(p.ID)
			tracker.RecordWithOwner(p.ID, p.Name, model, 0, 0, latencyMS, false, lastErr.Error(), stream, 0, accessType, consumerID)
			continue
		}

		// Success: mirror upstream response verbatim (content type, status,
		// and body — which may be SSE, JSON, or audio bytes).
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
		} else if ct := resp.Header.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)

		recordProviderSuccess(p.ID)
		tracker.RecordWithOwner(p.ID, p.Name, model, 0, 0, latencyMS, true, "", stream, 0, accessType, consumerID)
		return true
	}

	// All providers failed. Do not echo upstream error text verbatim to
	// arbitrary clients — same hardening as command paths in this codebase.
	slog.Warn("passthrough exhausted providers", "model", model, "error", lastErr)
	writeError(w, 502, "upstream passthrough failed")
	return true
}
