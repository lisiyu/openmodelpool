package main

import (
	"testing"
	"time"
)

// ============================================================
// Rate limiter tests
// ============================================================

func TestRateLimiter_BasicAllow(t *testing.T) {
	rl := NewRateLimiter(10) // 10 req/s

	// Should allow first 10 requests
	allowed := 0
	for i := 0; i < 15; i++ {
		if rl.Allow() {
			allowed++
		}
	}
	// Should allow approximately 10 (the initial bucket)
	if allowed < 8 || allowed > 12 {
		t.Fatalf("expected ~10 allowed, got %d", allowed)
	}
}

func TestRateLimiter_Refill(t *testing.T) {
	rl := NewRateLimiter(100) // 100 req/s

	// Drain the bucket
	for i := 0; i < 100; i++ {
		rl.Allow()
	}

	// Wait for refill (100ms should give ~10 tokens at 100/s)
	time.Sleep(120 * time.Millisecond)

	allowed := 0
	for i := 0; i < 20; i++ {
		if rl.Allow() {
			allowed++
		}
	}
	// Should have refilled some tokens
	if allowed < 5 {
		t.Fatalf("expected refill to allow some requests, got %d", allowed)
	}
}

func TestRateLimiter_ZeroQPS(t *testing.T) {
	rl := NewRateLimiter(0)
	// With 0 QPS, should not allow anything
	if rl.Allow() {
		t.Fatal("0 QPS should not allow any requests")
	}
}

func TestGlobalRateLimiter_ConsumerLimiters(t *testing.T) {
	g := &GlobalRateLimiter{
		global:      NewRateLimiter(1000),
		consumers:   make(map[string]*RateLimiter),
		globalQPS:   1000,
		consumerQPS: 10,
	}

	// First call creates limiter
	l1 := g.getConsumerLimiter("consumer-1")
	if l1 == nil {
		t.Fatal("should create consumer limiter")
	}

	// Second call returns same limiter
	l2 := g.getConsumerLimiter("consumer-1")
	if l1 != l2 {
		t.Fatal("should return same limiter for same consumer")
	}

	// Different consumer gets different limiter
	l3 := g.getConsumerLimiter("consumer-2")
	if l1 == l3 {
		t.Fatal("different consumer should get different limiter")
	}
}

func TestParseFloat64(t *testing.T) {
	tests := []struct {
		input   string
		def     float64
		want    float64
	}{
		{"100", 50, 100},
		{"0", 50, 50},
		{"-5", 50, 50},
		{"abc", 50, 50},
		{"", 50, 50},
		{"20.5", 50, 20.5},
	}
	for _, tt := range tests {
		got := parseFloat64(tt.input, tt.def)
		if got != tt.want {
			t.Errorf("parseFloat64(%q, %v) = %v, want %v", tt.input, tt.def, got, tt.want)
		}
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(1000)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				rl.Allow()
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
	// Should not panic with concurrent access
}
