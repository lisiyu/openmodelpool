package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// recoverMiddleware is the outermost safety net (B6-3): a panic escaping any
// handler is logged with a stack trace and answered with a JSON 500 instead of
// net/http silently closing the connection mid-response.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered in HTTP handler",
					"method", r.Method, "path", r.URL.Path,
					"panic", rec, "stack", string(debug.Stack()))
				// If the response was already started (e.g. mid-stream) this
				// write fails silently — acceptable, the log entry is what matters.
				writeJSON(w, 500, ErrorResponse{Error: ErrorDetail{
					Message: "internal server error",
					Type:    "internal_error",
					Code:    "panic_recovered",
				}})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

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

		originAllowed := origin != "" && isOriginAllowed(origin, allowedOrigins)
		if originAllowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400") // Cache preflight for 24h
		// SEC-P2-17: only advertise Allow-Credentials when the origin actually
		// matched the whitelist — never for arbitrary origins.
		if originAllowed {
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ============================================================
// Internal request context (SEC-P0-1 / SEC-P0-2)
// ============================================================
//
// Two values ride in the request context (never on the wire):
//   - relayDispatch: set when handleRelayToLocal re-dispatches a request
//     in-process. Middleware that trusts loopback/private RemoteAddr (the C3
//     anonymous-admin fallback in withProxyAuth and localOnly) MUST treat a
//     relay-dispatched request as an untrusted remote, because the relay
//     preserves the original (possibly public-internet) RemoteAddr.
//   - internalKeyType: the effective API key type derived by our own relay
//     from a credential it already validated (e.g. guest→public conversion).
//     It is the ONLY sanctioned replacement for the client-spoofable
//     X-OMP-KeyType wire header (SEC-P0-2).

type internalCtxKey int

const (
	ctxKeyRelayDispatch internalCtxKey = iota + 1
	ctxKeyInternalKeyType
	// ctxKeyGuestKey carries the verified guest API key through a relay
	// dispatch. handleRelayToLocal strips the Authorization header (the key
	// must not travel to provider code), but the per-key quota check still
	// needs the key — so it is propagated via context instead (P1-5).
	ctxKeyGuestKey
)

// isRelayDispatched reports whether the request was re-dispatched in-process
// by our own relay (SEC-P0-1). Such requests must never be granted the
// loopback/private-network trust that a genuine local request would receive.
func isRelayDispatched(r *http.Request) bool {
	v, _ := r.Context().Value(ctxKeyRelayDispatch).(bool)
	return v
}

// withInternalKeyType returns a request whose context carries an internal API
// key type derived by our own relay from a verified credential. This value can
// never be supplied by a client over the wire.
func withInternalKeyType(r *http.Request, keyType string) *http.Request {
	if keyType == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), ctxKeyInternalKeyType, keyType))
}

// internalKeyType returns the internal API key type set by our own relay, or ""
// when none was set.
func internalKeyType(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyInternalKeyType).(string); ok {
		return v
	}
	return ""
}

// withGuestKey returns a request whose context carries the verified guest API
// key (P1-5). Only set by handleRelayToLocal after the key was validated.
func withGuestKey(r *http.Request, key string) *http.Request {
	if key == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), ctxKeyGuestKey, key))
}

// relayGuestKey returns the guest API key verified by our relay, or "" when
// the request is not a relay-dispatched guest request.
func relayGuestKey(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyGuestKey).(string); ok {
		return v
	}
	return ""
}

// stripInternalHeadersMiddleware removes client-supplied internal headers that
// must never be trusted from the wire (SEC-P0-2). It runs before every handler
// so that a forged X-OMP-KeyType can never reach RequestKeyType. Our own relay
// communicates the effective key type via the context instead.
func stripInternalHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("X-OMP-KeyType")
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
		// Security: wildcard subdomain matching removed to prevent origin spoofing.
		// Only exact origin matches are allowed.
	}
	return false
}

// withProxyAuth authenticates v1 proxy endpoints.
// Accepts: public trial key, admin proxy API key, or consumer API key.
// C3-fix: Anonymous admin access is only allowed from localhost/private networks.
// Public internet requests without credentials are always rejected.
// SEC-P0-1: a request re-dispatched by our own relay (with a validated internal
// key type in the context) is accepted here and never granted the C3 anonymous
// admin fallback — the relay preserved the original RemoteAddr, which may be a
// public-internet client.
func withProxyAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// SEC-P0-1/SEC-P0-2: our own relay already validated the credential and
		// derived the effective key type (guest→public conversion etc.). The
		// internal value travels via context, never via a wire header, so it is
		// safe to accept here without re-validating.
		if kt := internalKeyType(r); kt != "" {
			handler(w, r)
			return
		}

		// P1-1: accept a signed relay/gateway forward from a trusted federation
		// node. gatewayForwardToRemote / relayToRemote forward to the destination
		// WITHOUT the origin Authorization header (consumer keys never leave the
		// node) and instead authenticate via X-Node-ID + an ed25519 signature.
		// Previously this request hit the anonymous/unknown-key path and got a
		// spurious 401 before the inner gateway handler could verify the
		// signature — so inter-node forwarding could never complete. The body is
		// read once for signature verification and restored so the inner handler
		// (handleGatewayRequest re-verifies and then routes) sees it unchanged.
		nodeID := sanitizeNodeID(r.Header.Get("X-Node-ID"))
		if nodeID == "" {
			nodeID = sanitizeNodeID(r.Header.Get("X-Node-Auth"))
		}
		if nodeID != "" && r.Header.Get(headerRelaySig) != "" {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxGatewayBodySize+1))
			if err == nil && len(body) <= maxGatewayBodySize {
				r.Body = io.NopCloser(bytes.NewReader(body))
				if status, msg := verifyRelayForwardAuth(r, body); status == 0 {
					// B6-extra: a forwarded request inherits at most consumer
					// identity from the forwarding node — never admin. The peer
					// rewrites these headers from its own verified auth, but a
					// compromised/misconfigured peer must not grant admin here.
					if r.Header.Get("X-Request-Role") == "admin" {
						r.Header.Set("X-Request-Role", "public")
						r.Header.Set("X-Request-Owner", "")
					}
					handler(w, r)
					return
				} else {
					slog.Warn("proxy route: signed forward rejected", "from", nodeID, "status", status, "reason", msg)
				}
			}
		}

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
			if proxyKey == "" && !multiUser.HasConsumers() {
				// C3-fix: Only allow anonymous admin access from localhost/private networks
				clientIP := extractClientIP(r.RemoteAddr)
				// SEC-P0-1: a relay-dispatched request is never anonymous admin,
				// even if its preserved RemoteAddr looks local.
				if !isRelayDispatched(r) && isLocalOrPrivateIP(clientIP) {
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
		// B8-6: constant-time compare — a plain == leaks key-prefix timing to
		// remote callers probing the public /v1 endpoint.
		if proxyKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(proxyKey)) == 1 {
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
			if !multiUser.HasConsumers() {
				clientIP := extractClientIP(r.RemoteAddr)
				// SEC-P0-1: relay-dispatched requests are never anonymous admin.
				if !isRelayDispatched(r) && isLocalOrPrivateIP(clientIP) {
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
		username, err := auth.VerifyToken(token)
		if err != nil {
			writeJSON(w, 401, ErrorResponse{Error: ErrorDetail{
				Message: "token expired",
				Type:    "authentication_error",
				Code:    "invalid_token",
			}})
			return
		}
		// B6-2: propagate the verified identity so auditRecord can attribute
		// admin actions (audit.go reads the "username" context key).
		r = r.WithContext(context.WithValue(r.Context(), "username", username))
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
// SEC-P0-1: a relay-dispatched request is treated as untrusted remote even if
// its preserved RemoteAddr looks local, so /network/{selfID}/api/forgot-password
// can never reach these endpoints from the public internet.
func localOnly(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := extractClientIP(r.RemoteAddr)
		if isRelayDispatched(r) || !isLocalOrPrivateIP(ip) {
			slog.Warn("blocked non-local access to sensitive endpoint", "ip", ip, "path", r.URL.Path, "relay_dispatched", isRelayDispatched(r))
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
	// SEC-P3-21: also cover link-local (169.254.0.0/16), CGNAT
	// (100.64.0.0/10) and IPv6 unique-local (fc00::/7) — SSRF guards must
	// treat all of these as internal.
	privateRanges := []struct {
		network string
	}{
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
		{"169.254.0.0/16"},
		{"100.64.0.0/10"},
		{"fc00::/7"},
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
		// F13: HSTS header for HTTPS connections
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
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

// adminTimeoutMiddleware adds a request timeout for admin API endpoints.
// B162: Prevents long-running admin operations from blocking connections.
// PERF-P2-13: only /api/* admin endpoints get the 60s cap — streaming paths
// (/v1/*, /events, /network/* relays) are exempt so long SSE/relay responses
// are not killed mid-stream by a context cancel.
func adminTimeoutMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
