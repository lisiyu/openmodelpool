package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400") // Cache preflight for 24h
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isOriginAllowed checks if an origin match the whitelist.
// Supports exact match and wildcard subdomain (*.example.com).
// M1-fix: Implemented wildcard subdomain matching.
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
		// M1-fix: Support wildcard subdomain matching (e.g. *.example.com)
		if strings.HasPrefix(allowed, "*.") {
			suffix := allowed[1:] // ".example.com"
			// origin must end with the suffix and have at least one char before it
			if strings.HasSuffix(origin, suffix) && len(origin) > len(suffix) {
				return true
			}
		}
	}
	return false
}

// withProxyAuth authenticates v1 proxy endpoints.
// Accepts: public trial key, admin proxy API key, or consumer API key.
// C3-fix: Anonymous admin access is only allowed from localhost/private networks.
// Public internet requests without credentials are always rejected.
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
			proxyKey := cfg.Get("proxy_api_key", "")
			if proxyKey == "" && len(multiUser.consumers) == 0 {
				// C3-fix: Only allow anonymous admin access from localhost/private networks
				clientIP := extractClientIP(r.RemoteAddr)
				if isLocalOrPrivateIP(clientIP) {
					r.Header.Set("X-Request-Owner", "")
					r.Header.Set("X-Request-Role", "admin")
					handler(w, r)
					return
				}
				// Non-local anonymous access rejected even in unprotected mode
				slog.Warn("rejected anonymous access from non-local IP", "ip", clientIP, "path", r.URL.Path)
				writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
					Message: "API key required",
					Type:    "authentication_error",
					Code:    "missing_api_key",
				}})
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

		// C3-fix: Fallback anonymous admin only from localhost/private networks
		if proxyKey == "" {
			if len(multiUser.consumers) == 0 {
				clientIP := extractClientIP(r.RemoteAddr)
				if isLocalOrPrivateIP(clientIP) {
					r.Header.Set("X-Request-Owner", "")
					r.Header.Set("X-Request-Role", "admin")
					handler(w, r)
					return
				}
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
// A3-fix: Unified error response format using ErrorResponse.
func withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
				Message: "not authenticated",
				Type:    "authentication_error",
				Code:    "missing_token",
			}})
			return
		}
		_, err := auth.VerifyToken(token)
		if err != nil {
			writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
				Message: "token expired",
				Type:    "authentication_error",
				Code:    "invalid_token",
			}})
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

// localOnly restricts access to localhost and private network IPs only.
// Used for sensitive endpoints like password reset that should not be accessible from the public internet.
func localOnly(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r.RemoteAddr)
		if !isLocalOrPrivateIP(ip) {
			slog.Warn("blocked non-local access to sensitive endpoint", "ip", ip, "path", r.URL.Path)
			writeError(w, 403, "this endpoint is only accessible from localhost or private network")
			return
		}
		handler(w, r)
	}
}

// isLocalOrPrivateIP checks if an IP is localhost or in a private network range.
func isLocalOrPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	// Loopback: 127.0.0.0/8, ::1
	if parsed.IsLoopback() {
		return true
	}
	// Private networks: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
	privateRanges := []struct {
		network string
	}{
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
	}
	for _, r := range privateRanges {
		_, cidr, _ := net.ParseCIDR(r.network)
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}

// ============================================================
// F1: Request ID middleware — assigns a unique ID to every request
// for distributed tracing and log correlation.
// ============================================================

const requestIDHeader = "X-Request-ID"

// requestIDMiddleware injects a unique request ID into the context and response headers.
// If the client provides an X-Request-ID header, it is preserved (up to 64 chars).
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" || len(id) > 64 {
			id = generateRequestID()
		}
		r.Header.Set(requestIDHeader, id)
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r)
	})
}

// generateRequestID creates a 16-byte random hex string.
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID (should never happen)
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
