package main

import (
	"sync"
	"sync/atomic"
)

// providerConns tracks active connections per provider
var providerConns sync.Map

// IncrProviderConn increments the active connection count for a provider
func IncrProviderConn(providerID string) {
	if v, ok := providerConns.Load(providerID); ok {
		atomic.AddInt64(v.(*int64), 1)
	} else {
		var n int64 = 1
		providerConns.Store(providerID, &n)
	}
}

// DecrProviderConn decrements the active connection count for a provider
func DecrProviderConn(providerID string) {
	if v, ok := providerConns.Load(providerID); ok {
		atomic.AddInt64(v.(*int64), -1)
	}
}

// GetProviderConns returns the current active connection count for a provider
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
