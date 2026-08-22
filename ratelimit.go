package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RateLimiter implements a token bucket rate limiter.
type RateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewRateLimiter creates a new rate limiter with the given QPS limit.
// QPS <= 0 means block all requests (zero tokens, zero refill).
// Burst (maxTokens) is set to max(qps, 1.0) to ensure at least 1 request can pass.
func NewRateLimiter(qps float64) *RateLimiter {
	if qps <= 0 {
		return &RateLimiter{
			tokens:     0,
			maxTokens:  0,
			refillRate: 0,
			lastRefill: time.Now(),
		}
	}
	maxTok := qps
	if maxTok < 1.0 {
		maxTok = 1.0
	}
	return &RateLimiter{
		tokens:     maxTok,
		maxTokens:  maxTok,
		refillRate: qps,
		lastRefill: time.Now(),
	}
}

// NewRateLimiterWithBurst creates a rate limiter with explicit burst capacity.
func NewRateLimiterWithBurst(qps float64, burst float64) *RateLimiter {
	if burst < 1.0 {
		burst = 1.0
	}
	return &RateLimiter{
		tokens:     burst,
		maxTokens:  burst,
		refillRate: qps,
		lastRefill: time.Now(),
	}
}

// Allow checks if a request is allowed under the rate limit.
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}
	rl.lastRefill = now

	if rl.tokens >= 1.0 {
		rl.tokens -= 1.0
		return true
	}
	return false
}

// GlobalRateLimiter manages global and per-consumer rate limiting.
const consumersMaxEntries = 10000

type GlobalRateLimiter struct {
	global      *RateLimiter
	consumers   map[string]*RateLimiter
	mu          sync.RWMutex
	globalQPS   float64
	consumerQPS float64
}

func initRateLimiter() {
	globalQPS := parseFloat64("rate_limit_global", cfg.Get("rate_limit_global", "100"), 100)
	consumerQPS := parseFloat64("rate_limit_per_consumer", cfg.Get("rate_limit_per_consumer", "20"), 20)

	rateLimiter = &GlobalRateLimiter{
		global:      NewRateLimiter(globalQPS),
		consumers:   make(map[string]*RateLimiter),
		globalQPS:   globalQPS,
		consumerQPS: consumerQPS,
	}
	slog.Info("rate limiter initialized", "global_qps", globalQPS, "consumer_qps", consumerQPS)
}

// getConsumerLimiter returns or creates a rate limiter for a specific consumer.
func (g *GlobalRateLimiter) getConsumerLimiter(consumerID string) *RateLimiter {
	g.mu.RLock()
	limiter, ok := g.consumers[consumerID]
	g.mu.RUnlock()
	if ok {
		return limiter
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	// Double-check after acquiring write lock
	if limiter, ok = g.consumers[consumerID]; ok {
		return limiter
	}
	limiter = NewRateLimiter(g.consumerQPS)
	if len(g.consumers) >= consumersMaxEntries {
		g.cleanupConsumersLocked()
	}
	g.consumers[consumerID] = limiter
	return limiter
}

func (g *GlobalRateLimiter) cleanupConsumersLocked() {
	now := time.Now()
	for id, lim := range g.consumers {
		if now.Sub(lim.lastRefill) > 10*time.Minute {
			delete(g.consumers, id)
		}
		if len(g.consumers) < consumersMaxEntries/2 {
			break
		}
	}
}

// rateLimitMiddleware enforces rate limits. Should be placed after auth middleware.
func rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rateLimiter == nil {
			next(w, r)
			return
		}

		// Check global limit first
		if !rateLimiter.global.Allow() {
			metrics.requestErrors.Add(1)
			slog.Warn("global rate limit exceeded", "remote", r.RemoteAddr)
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", rateLimiter.globalQPS))
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, ErrorResponse{Error: ErrorDetail{
				Message: "global rate limit exceeded",
				Type:    "rate_limit_error",
				Code:    "rate_limit_global",
			}})
			return
		}

		// Check per-consumer limit
		consumerID := getRequestOwner(r)
		if consumerID == "" {
			consumerID = "admin:" + r.RemoteAddr
		}

		limiter := rateLimiter.getConsumerLimiter(consumerID)
		if !limiter.Allow() {
			metrics.requestErrors.Add(1)
			slog.Warn("consumer rate limit exceeded", "consumer", consumerID, "remote", r.RemoteAddr)
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", rateLimiter.consumerQPS))
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusTooManyRequests, ErrorResponse{Error: ErrorDetail{
				Message: fmt.Sprintf("per-consumer rate limit exceeded (%.0f req/s)", rateLimiter.consumerQPS),
				Type:    "rate_limit_error",
				Code:    "rate_limit_per_consumer",
			}})
			return
		}

		next(w, r)
	}
}

// parseFloat64 parses config key s's value to float64 with a default fallback.
// B7-d: a silent fallback hid config typos (e.g. rate_limit_global="abc"
// silently ran at the default) — log so operators notice. An empty value is
// treated as unset (no warning).
func parseFloat64(key, s string, defaultVal float64) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err == nil && v > 0 {
		return v
	}
	if s != "" {
		slog.Warn("invalid numeric config value, using default", "key", key, "value", s, "default", defaultVal)
	}
	return defaultVal
}

// ============================================================
// SA-10: Per-IP Rate Limiting for sensitive public endpoints
// ============================================================

// ipRateLimiters stores per-IP rate limiters for sensitive endpoints.
var ipRateLimiters = struct {
	sync.RWMutex
	limiters map[string]*ipRateLimitEntry
}{limiters: make(map[string]*ipRateLimitEntry)}

type ipRateLimitEntry struct {
	limiter *RateLimiter
	// lastSeen is atomic (unix nano) because it is refreshed on every request
	// outside the map lock; cleanupIPRateLimiters reads it under the lock.
	// B6-7: plain time.Time here was a data race between refresh and cleanup.
	lastSeen atomic.Int64
}

// rateLimitByIP returns a middleware that limits requests per client IP.
// maxRequests defines the allowed requests per minute for each unique IP.
const ipRateLimitersMaxEntries = 10000

func rateLimitByIP(maxRequestsPerMinute float64, endpointName string) func(http.HandlerFunc) http.HandlerFunc {
	qps := maxRequestsPerMinute / 60.0
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// SEC-B5-7: behind a trusted reverse proxy every client shares the
			// proxy's RemoteAddr; use the real client IP from X-Forwarded-For so
			// one client cannot exhaust a shared bucket and lock out everyone.
			// Outside a trusted proxy the XFF header is attacker-controlled and
			// ignored (consistent with WAF/extractRemoteIP).
			ip := extractClientIP(r.RemoteAddr)
			if trustedReverseProxy {
				if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					if idx := strings.IndexByte(xff, ','); idx >= 0 {
						ip = strings.TrimSpace(xff[:idx])
					} else {
						ip = strings.TrimSpace(xff)
					}
				}
			}

			ipRateLimiters.RLock()
			entry, exists := ipRateLimiters.limiters[ip+endpointName]
			ipRateLimiters.RUnlock()

			if !exists {
				ipRateLimiters.Lock()
				entry, exists = ipRateLimiters.limiters[ip+endpointName]
				if !exists {
					if len(ipRateLimiters.limiters) >= ipRateLimitersMaxEntries {
					cutoff := time.Now().Add(-5 * time.Minute)
					for k, e := range ipRateLimiters.limiters {
						if time.Unix(0, e.lastSeen.Load()).Before(cutoff) {
								delete(ipRateLimiters.limiters, k)
							}
						}
					}
					if len(ipRateLimiters.limiters) >= ipRateLimitersMaxEntries {
						ipRateLimiters.Unlock()
						slog.Warn("IP rate limiter map full, rejecting new IP", "ip", ip)
						writeJSON(w, http.StatusTooManyRequests, ErrorResponse{Error: ErrorDetail{
							Message: "rate limit exceeded, please try again later",
							Type:    "rate_limit_error",
							Code:    "rate_limit_ip_map_full",
						}})
						return
					}
				entry = &ipRateLimitEntry{
					limiter: NewRateLimiterWithBurst(qps, maxRequestsPerMinute),
				}
				entry.lastSeen.Store(time.Now().UnixNano())
				ipRateLimiters.limiters[ip+endpointName] = entry
			}
			ipRateLimiters.Unlock()
		}

		entry.lastSeen.Store(time.Now().UnixNano())
			if !entry.limiter.Allow() {
				slog.Warn("IP rate limit exceeded", "ip", ip, "endpoint", endpointName, "remote", r.RemoteAddr)
				w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", maxRequestsPerMinute))
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, ErrorResponse{Error: ErrorDetail{
					Message: "rate limit exceeded, please try again later",
					Type:    "rate_limit_error",
					Code:    "rate_limit_ip",
				}})
				return
			}

			next(w, r)
		}
	}
}

// extractClientIP extracts the IP address from a RemoteAddr string (host:port).
// M4-fix: Use net.SplitHostPort for correct IPv6 handling (e.g. [::1]:8000 → ::1).
func extractClientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr // no port present, return as-is
	}
	return host
}

// cleanupIPRateLimiters removes stale IP limiter entries older than maxAge.
func cleanupIPRateLimiters(maxAge time.Duration) {
	ipRateLimiters.Lock()
	defer ipRateLimiters.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for key, entry := range ipRateLimiters.limiters {
		if time.Unix(0, entry.lastSeen.Load()).Before(cutoff) {
			delete(ipRateLimiters.limiters, key)
		}
	}
}
