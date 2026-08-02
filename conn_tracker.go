package main

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

var providerConns sync.Map

func IncrProviderConn(providerID string) {
	if v, ok := providerConns.Load(providerID); ok {
		atomic.AddInt64(v.(*int64), 1)
	} else {
		var n int64 = 1
		providerConns.Store(providerID, &n)
	}
}

func DecrProviderConn(providerID string) {
	if v, ok := providerConns.Load(providerID); ok {
		atomic.AddInt64(v.(*int64), -1)
	}
}

func GetProviderConns(providerID string) int {
	if v, ok := providerConns.Load(providerID); ok {
		n := atomic.LoadInt64(v.(*int64))
		if n < 0 {
			return 0
		}
		return int(n)
	}
	return 0
}

func cleanupStaleProviderConns() {
	providerConns.Range(func(key, value any) bool {
		n := atomic.LoadInt64(value.(*int64))
		if n <= 0 {
			providerConns.Delete(key)
		}
		return true
	})
}

var connTrackerStopCh = make(chan struct{})

func startConnTrackerCleanup() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				before := 0
				providerConns.Range(func(_, _ any) bool { before++; return true })
				cleanupStaleProviderConns()
				after := 0
				providerConns.Range(func(_, _ any) bool { after++; return true })
				if after < before {
					slog.Debug("conn tracker cleanup", "removed", before-after, "remaining", after)
				}
			case <-connTrackerStopCh:
				return
			}
		}
	}()
}

func stopConnTracker() { close(connTrackerStopCh) }

// ============================================================
// Guest connection tracking
//
// Guest connections are those served with access type "guest" (guest keys or
// guest API access). The request path already computes the access type, so we
// track guest connections by also incrementing a dedicated counter whenever a
// guest request begins. This feeds the admin panel's per-guest connection
// metric (previously a hardcoded 0 placeholder).
// ============================================================

// guestConns tracks the current number of active guest connections.
var guestConns sync.Map

// IncrGuestConn increments the active guest connection count.
func IncrGuestConn() {
	if v, ok := guestConns.Load("g"); ok {
		atomic.AddInt64(v.(*int64), 1)
	} else {
		var n int64 = 1
		guestConns.Store("g", &n)
	}
}

// DecrGuestConn decrements the active guest connection count.
func DecrGuestConn() {
	if v, ok := guestConns.Load("g"); ok {
		atomic.AddInt64(v.(*int64), -1)
	}
}

// GetGuestConns returns the current active guest connection count.
func GetGuestConns() int {
	if v, ok := guestConns.Load("g"); ok {
		n := atomic.LoadInt64(v.(*int64))
		if n < 0 {
			return 0
		}
		return int(n)
	}
	return 0
}

// ============================================================
// Access-type aware connection helpers
//
// IncrConn / DecrConn wrap the provider connection counters and additionally
// maintain the guest counter when accessType == "guest". They are drop-in
// replacements for IncrProviderConn / DecrProviderConn at request boundaries
// where the access type is already known.
// ============================================================

// IncrConn increments the provider connection counter and, when applicable,
// the guest connection counter.
func IncrConn(providerID, accessType string) {
	IncrProviderConn(providerID)
	if accessType == "guest" {
		IncrGuestConn()
	}
}

// DecrConn decrements the provider connection counter and, when applicable,
// the guest connection counter.
func DecrConn(providerID, accessType string) {
	DecrProviderConn(providerID)
	if accessType == "guest" {
		DecrGuestConn()
	}
}

// GetStats returns connection statistics for diagnostics.
func GetConnStats() map[string]any {
	providerConnsMu.RLock()
	pConns := make(map[string]int, len(providerConns))
	for k, v := range providerConns {
		pConns[k] = v
	}
	providerConnsMu.RUnlock()

	guestConnsMu.RLock()
	gConns := guestConns
	guestConnsMu.RUnlock()

	return map[string]any{
		"provider_connections": pConns,
		"guest_connections":    gConns,
	}
}

