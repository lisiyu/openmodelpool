package main

import (
	"net/http"
	"strings"
)

// corsMiddleware handles CORS headers based on configured allowed origins.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigins := cfg.Get("cors_allowed_origins", "")

		// Default: allow localhost and tunnel URL, never wildcard *
		if allowedOrigins == "" {
			tunnelURL := cfg.Get("tunnel_url", "")
			defaults := "http://localhost:8000,http://127.0.0.1:8000,http://localhost:3000"
			if tunnelURL != "" {
				defaults += "," + tunnelURL
			}
			allowedOrigins = defaults
		}

		if origin != "" && isOriginAllowed(origin, allowedOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isOriginAllowed checks if an origin match the whitelist.
// Supports exact match and wildcard subdomain (*.example.com).
func isOriginAllowed(origin, whitelist string) bool {
	origins := strings.Split(whitelist, ",")
	for _, allowed := range origins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if allowed == origin {
			return true
		}
		// Wildcard subdomain: *.example.com matches sub.example.com
		if strings.HasPrefix(allowed, "*.") {
			suffix := allowed[1:] // ".example.com"
			if strings.HasSuffix(origin, suffix) {
				return true
			}
		}
	}
	return false
}

// withProxyAuth authenticates v1 proxy endpoints.
// Accepts: public trial key, admin proxy API key, or consumer API key.
// If no proxy API key is set and no consumer key matches, allows anonymous access as admin.
func withProxyAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			key := authHeader[7:]
			// v2.0: public trial key — always accepted
			if key == PublicKeyValue {
				r.Header.Set("X-Request-Owner", "")
				r.Header.Set("X-Request-Role", "public")
				handler(w, r)
				return
			}
		}
		if !strings.HasPrefix(authHeader, "Bearer ") {
			// No auth header - check if proxy key is required
			proxyKey := cfg.Get("proxy_api_key", "")
			if proxyKey == "" {
				r.Header.Set("X-Request-Owner", "")
				r.Header.Set("X-Request-Role", "admin")
				handler(w, r)
				return
			}
			writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
				Message: "API key required",
				Type:    "authentication_error",
				Code:    "missing_api_key",
			}})
			return
		}

		key := authHeader[7:]
		// Check admin proxy API key first
		proxyKey := cfg.Get("proxy_api_key", "")
		if proxyKey != "" && key == proxyKey {
			r.Header.Set("X-Request-Owner", "")
			r.Header.Set("X-Request-Role", "admin")
			handler(w, r)
			return
		}

		// Check consumer API key
		if consumer, ok := multiUser.ValidateAPIKey(key); ok {
			r.Header.Set("X-Request-Owner", consumer.ID)
			r.Header.Set("X-Request-Role", "consumer")
			r.Header.Set("X-Consumer-Name", consumer.Name)
			handler(w, r)
			return
		}

		// No anonymous fallback - require valid credentials
		if proxyKey == "" {
			// Only allow if there's no proxy key AND consumer keys exist (unprotected mode)
			if len(multiUser.consumers) == 0 {
				r.Header.Set("X-Request-Owner", "")
				r.Header.Set("X-Request-Role", "admin")
				handler(w, r)
				return
			}
		}

		// S-9: Generic error message - do not expose internal details
		writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
			Message: "请求处理失败，请稍后重试",
			Type:    "authentication_error",
			Code:    "invalid_api_key",
		}})
	}
}

// withAuth authenticates admin-only endpoints via JWT token.
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
		r.Header.Set("X-Request-Owner", "")
		r.Header.Set("X-Request-Role", "admin")
		handler(w, r)
	}
}

// extractToken extracts the JWT token from Authorization header or cookie.
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
