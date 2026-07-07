package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	// Initialize all components
	os.MkdirAll("data", 0755)
	initConfig("data/config.json")
	initProviderManager("data/providers.json")
	initTracker("data/usage.json")
	initSiderMonitor("data/sider_token_status.json")
	initAuth("data/admin.json")

	// Setup HTTP mux
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /health", handleHealth)

	// OpenAI-compatible endpoints
	mux.HandleFunc("GET /v1/models", withOptionalAPIKey(handleListModels))
	mux.HandleFunc("POST /v1/chat/completions", withOptionalAPIKey(handleChatCompletions))

	// Auth (public)
	mux.HandleFunc("GET /api/setup/status", handleSetupStatus)
	mux.HandleFunc("POST /api/setup", handleSetup)
	mux.HandleFunc("POST /api/login", handleLogin)
	mux.HandleFunc("POST /api/forgot-password", handleForgotPassword)
	mux.HandleFunc("POST /api/reset-password", handleResetPassword)
	mux.HandleFunc("POST /api/reset-password/verify", handleVerifyResetToken)

	// Auth (protected)
	mux.HandleFunc("GET /api/auth/verify", withAuth(handleVerifyAuth))
	mux.HandleFunc("GET /api/config", withAuth(handleGetConfig))
	mux.HandleFunc("POST /api/config", withAuth(handleSaveConfig))
	mux.HandleFunc("GET /api/status", withAuth(handleStatus))
	mux.HandleFunc("GET /api/admin/info", withAuth(handleAdminInfo))
	mux.HandleFunc("POST /api/admin/change-password", withAuth(handleChangePassword))
	mux.HandleFunc("POST /api/admin/update-email", withAuth(handleUpdateEmail))

	// Provider management (protected)
	mux.HandleFunc("GET /api/providers", withAuth(handleListProviders))
	mux.HandleFunc("GET /api/providers/presets", handleGetPresets)
	mux.HandleFunc("POST /api/providers", withAuth(handleCreateProvider))
	mux.HandleFunc("GET /api/providers/{id}", withAuth(handleGetProvider))
	mux.HandleFunc("PUT /api/providers/{id}", withAuth(handleUpdateProvider))
	mux.HandleFunc("DELETE /api/providers/{id}", withAuth(handleDeleteProvider))
	mux.HandleFunc("POST /api/providers/{id}/test", withAuth(handleTestProvider))
	mux.HandleFunc("GET /api/providers/{id}/models", withAuth(handleGetProviderModels))
	mux.HandleFunc("POST /api/providers/{id}/sync-url", withAuth(handleSyncProviderURL))
	mux.HandleFunc("POST /api/providers/sync-all-urls", withAuth(handleSyncAllURLs))

	// Sider status
	mux.HandleFunc("GET /api/providers/sider/status", withAuth(handleSiderStatus))
	mux.HandleFunc("POST /api/providers/sider/test", withAuth(handleSiderTest))

	// Usage & routing (protected)
	mux.HandleFunc("GET /api/usage/summary", withAuth(handleUsageSummary))
	mux.HandleFunc("GET /api/usage/providers", withAuth(handleUsageProviders))
	mux.HandleFunc("GET /api/usage/records", withAuth(handleUsageRecords))
	mux.HandleFunc("DELETE /api/usage/reset", withAuth(handleUsageReset))
	mux.HandleFunc("GET /api/routing/mode", withAuth(handleGetRoutingMode))
	mux.HandleFunc("POST /api/routing/mode", withAuth(handleSetRoutingMode))
	mux.HandleFunc("GET /api/routing/weights", withAuth(handleGetRoutingWeights))
	mux.HandleFunc("POST /api/routing/weights", withAuth(handleSetRoutingWeights))
	mux.HandleFunc("GET /api/routing/advice/{model}", withAuth(handleRoutingAdvice))

	// SMTP (protected)
	mux.HandleFunc("GET /api/smtp/status", handleSMTPStatus)
	mux.HandleFunc("GET /api/smtp/config", withAuth(handleGetSMTPConfig))
	mux.HandleFunc("POST /api/smtp/config", withAuth(handleSaveSMTPConfig))
	mux.HandleFunc("POST /api/smtp/test", withAuth(handleSMTPTest))

	// Logs (protected)
	mux.HandleFunc("GET /api/logs", withAuth(handleLogs))

	// Static pages
	mux.HandleFunc("GET /", handleAdminPage)
	mux.HandleFunc("GET /admin", handleAdminPage)
	mux.HandleFunc("GET /setup", handleSetupPage)
	mux.HandleFunc("GET /login", handleLoginPage)

	// CORS middleware
	handler := corsMiddleware(mux)

	port := cfg.Get("service_port", "8000")
	addr := ":" + port

	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // long for streaming
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down...")
		tracker.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	slog.Info("ModelMux started", "port", port, "providers", len(pm.Enabled()))
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// ============================================================
// Middleware
// ============================================================

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withOptionalAPIKey(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := cfg.Get("proxy_api_key", "")
		if apiKey == "" {
			handler(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			if authHeader[7:] == apiKey {
				handler(w, r)
				return
			}
		}
		writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
			Message: "Invalid API key",
			Type:    "authentication_error",
			Code:    "invalid_api_key",
		}})
	}
}

func withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			writeJSON(w, 401, map[string]string{"error": "not authenticated"})
			return
		}
		_, err := auth.VerifyToken(token)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "token expired"})
			return
		}
		handler(w, r)
	}
}

func extractToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return authHeader[7:]
	}
	cookie, _ := r.Cookie("admin_token")
	if cookie != nil {
		return cookie.Value
	}
	return ""
}

// ============================================================
// Helpers
// ============================================================

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: ErrorDetail{
		Message: msg, Type: "api_error", Code: fmt.Sprintf("%d", status),
	}})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ============================================================
// Handlers - Health & Models
// ============================================================

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status":           "ok",
		"version":          "1.0.0",
		"providers_enabled": len(pm.Enabled()),
		"models_available": len(pm.AllModels()),
	})
}

func handleListModels(w http.ResponseWriter, r *http.Request) {
	models := pm.AllModels()
	writeJSON(w, 200, ModelListResponse{Object: "list", Data: models})
}

// ============================================================
// Handlers - Chat Completions (core)
// ============================================================

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, 400, "messages cannot be empty")
		return
	}

	model := req.Model
	stream := req.Stream

	// Build extra params
	extra := make(map[string]any)
	if req.Temperature != nil { extra["temperature"] = *req.Temperature }
	if req.TopP != nil { extra["top_p"] = *req.TopP }
	if req.MaxTokens != nil { extra["max_tokens"] = *req.MaxTokens }
	for k, v := range req.Extra {
		extra[k] = v
	}

	// Coze-specific routing
	if strings.HasPrefix(model, "coze-") {
		handleCozeRequest(w, r, model, req.Messages, stream, extra)
		return
	}

	// Smart routing with fallback
	routingMode := cfg.Get("routing_mode", "priority")
	candidates := pm.OrderedCandidates(model, routingMode)

	if len(candidates) == 0 {
		models := pm.AllModels()
		var names []string
		for i, m := range models {
			if i >= 20 { break }
			names = append(names, m.ID)
		}
		hint := ""
		if len(names) > 0 {
			hint = ", available models: " + strings.Join(names, ", ")
		}
		writeError(w, 404, fmt.Sprintf("no provider found for model '%s'%s", model, hint))
		return
	}

	var lastErr error
	for idx, c := range candidates {
		p := c.Provider
		actualModel := c.Model

		if idx > 0 {
			slog.Warn("fallback", "model", model, "to", p.Name, "idx", idx, "mode", routingMode)
		}

		if p.APIKey == "" && p.Type != "coze" {
			lastErr = fmt.Errorf("provider '%s' has no API key", p.Name)
			continue
		}

		startTime := time.Now()

		if stream {
			err := handleStreamProxy(w, p, actualModel, req.Messages, extra, model, startTime)
			if err == nil {
				return
			}
			lastErr = err
		} else {
			resp, err := doNonStream(p, actualModel, req.Messages, extra)
			if err != nil {
				lastErr = err
				tracker.Record(p.ID, p.Name, model, 0, 0, float64(time.Since(startTime).Milliseconds()), false, err.Error())
				continue
			}
			resp.Model = model
			latencyMS := float64(time.Since(startTime).Milliseconds())
			var promptTok, compTok int
			if resp.Usage != nil {
				promptTok = resp.Usage.PromptTokens
				compTok = resp.Usage.CompletionTokens
			}
			tracker.Record(p.ID, p.Name, model, promptTok, compTok, latencyMS, true, "")
			writeJSON(w, 200, resp)
			return
		}
	}

	writeError(w, 502, fmt.Sprintf("all providers failed: %v", lastErr))
}

func handleStreamProxy(w http.ResponseWriter, p Provider, model string, messages []ChatMessage, extra map[string]any, origModel string, startTime time.Time) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	// Use a wrapper that implements Flush
	sw := &streamWriter{w: w, flusher: flusher}

	err := doStream(p, model, messages, extra, sw)

	latencyMS := float64(time.Since(startTime).Milliseconds())
	if err != nil {
		tracker.Record(p.ID, p.Name, origModel, 0, 0, latencyMS, false, err.Error())
		return err
	}
	tracker.Record(p.ID, p.Name, origModel, 0, 0, latencyMS, true, "")
	return nil
}

type streamWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *streamWriter) Write(p []byte) (n int, err error) {
	n, err = s.w.Write(p)
	s.flusher.Flush()
	return
}

func handleCozeRequest(w http.ResponseWriter, r *http.Request, model string, messages []ChatMessage, stream bool, extra map[string]any) {
	token := cfg.Get("coze_api_token", "")
	if token == "" {
		writeError(w, 500, "Coze API token not configured")
		return
	}

	// Get coze provider or use a synthetic one
	p, _ := pm.GetRaw("coze")

	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, _ := w.(http.Flusher)
		sw := &streamWriter{w: w, flusher: flusher}
		cozeStream(p, model, messages, sw)
		return
	}

	resp, err := cozeNonStream(p, model, messages)
	if err != nil {
		writeError(w, 502, fmt.Sprintf("Coze error: %v", err))
		return
	}
	writeJSON(w, 200, resp)
}
