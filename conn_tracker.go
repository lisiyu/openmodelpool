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
