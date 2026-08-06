package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ============================================================
// isOriginAllowed tests
// ============================================================

func TestIsOriginAllowed(t *testing.T) {
	tests := []struct {
		name      string
		origin    string
		whitelist string
		expected  bool
	}{
		{"exact match", "http://localhost:8000", "http://localhost:8000", true},
		{"exact match with others", "http://localhost:8000", "http://example.com,http://localhost:8000", true},
		{"no match", "http://evil.com", "http://localhost:8000", false},
		{"empty whitelist", "http://example.com", "", false},
		{"wildcard subdomain not supported", "https://sub.example.com", "*.example.com", false},
		{"wildcard subdomain mismatch", "https://evil.com", "*.example.com", false},
		{"wildcard subdomain exact mismatch", "https://notexample.com", "*.example.com", false},
		{"multiple wildcards not supported", "https://sub.test.io", "*.example.com,*.test.io", false},
		{"match with spaces in whitelist", "http://localhost:8000", "  http://localhost:8000  ", true},
		{"match with port", "http://localhost:3000", "http://localhost:3000", true},
		{"partial origin no match", "http://localhost", "http://localhost:8000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOriginAllowed(tt.origin, tt.whitelist)
			if got != tt.expected {
				t.Errorf("isOriginAllowed(%q, %q) = %v, want %v", tt.origin, tt.whitelist, got, tt.expected)
			}
		})
	}
}

// ============================================================
// isLocalOrPrivateIP tests
// ============================================================

func TestIsLocalOrPrivateIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"localhost ipv4", "127.0.0.1", true},
		{"localhost ipv6", "::1", true},
		{"private 10.x", "10.0.0.1", true},
		{"private 172.16.x", "172.16.0.1", true},
		{"private 172.31.x", "172.31.255.254", true},
		{"private 192.168.x", "192.168.1.1", true},
		{"public ip", "8.8.8.8", false},
		{"public ip 2", "1.1.1.1", false},
		{"invalid ip", "not-an-ip", false},
		{"empty string", "", false},
		{"172.15.x out of range", "172.15.0.1", false},
		{"172.32.x out of range", "172.32.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLocalOrPrivateIP(tt.ip)
			if got != tt.expected {
				t.Errorf("isLocalOrPrivateIP(%q) = %v, want %v", tt.ip, got, tt.expected)
			}
		})
	}
}

// ============================================================
// extractToken tests
// ============================================================

func TestExtractToken(t *testing.T) {
	t.Run("Bearer token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer my-jwt-token-123")
		got := extractToken(req)
		if got != "my-jwt-token-123" {
			t.Errorf("extractToken() = %q, want %q", got, "my-jwt-token-123")
		}
	})

	t.Run("cookie token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(&http.Cookie{Name: "admin_token", Value: "cookie-token-456"})
		got := extractToken(req)
		if got != "cookie-token-456" {
			t.Errorf("extractToken() = %q, want %q", got, "cookie-token-456")
		}
	})

	t.Run("no token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		got := extractToken(req)
		if got != "" {
			t.Errorf("extractToken() = %q, want empty string", got)
		}
	})

	t.Run("mangled auth header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "NotABearer mytoken")
		got := extractToken(req)
		if got != "" {
			t.Errorf("extractToken() = %q, want empty string for non-Bearer auth", got)
		}
	})
}

// ============================================================
// extractClientIP tests (referenced in localOnly)
// ============================================================

func TestExtractClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		expected   string
	}{
		{"ipv4 with port", "192.168.1.1:54321", "192.168.1.1"},
		// extractClientIP uses net.SplitHostPort which strips IPv6 brackets
		{"ipv6 with port", "[::1]:54321", "::1"},
		// IPv6 without port: SplitHostPort fails, returns as-is
		{"ipv6 no port", "::1", "::1"},
		{"ipv4 no port", "192.168.1.1", "192.168.1.1"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractClientIP(tt.remoteAddr)
			if got != tt.expected {
				t.Errorf("extractClientIP(%q) = %q, want %q", tt.remoteAddr, got, tt.expected)
			}
		})
	}
}

// ============================================================
// corsMiddleware integration test
// ============================================================

func TestCorsMiddleware_CORSHeadersSet(t *testing.T) {
	// Setup isolated cfg since corsMiddleware reads from global cfg
	env := setupTestEnv(t)
	_ = env // ensure globals are initialized via setupTestEnv

	t.Run("preflight request with allowed origin", func(t *testing.T) {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Should not reach here for OPTIONS
			t.Error("next handler should not be called for preflight OPTIONS")
		})
		handler := corsMiddleware(next)

		req := httptest.NewRequest("OPTIONS", "/", nil)
		req.Header.Set("Origin", "http://localhost:8000")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("expected 200 for OPTIONS, got %d", w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Error("Access-Control-Allow-Methods header not set")
		}
		if w.Header().Get("Access-Control-Allow-Headers") == "" {
			t.Error("Access-Control-Allow-Headers header not set")
		}
	})

	t.Run("normal request passes through", func(t *testing.T) {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		handler := corsMiddleware(next)

		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if !called {
			t.Error("next handler was not called for non-OPTIONS request")
		}
	})
}

// Note: extractClientIP is already defined in ratelimit.go
