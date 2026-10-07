package main

import (
	"testing"
)

// v4.6.56 regression: ReserveQuota panicked with nil-pointer dereference
// when IPDailyLimit == 0 (unlimited, the v4.6.54 default). Layer 2 only
// created the IP tracker when IPDailyLimit > 0, but the reservation block
// unconditionally dereferenced q.ipUsage[ip].
func TestReserveQuota_UnlimitedIPNoPanic(t *testing.T) {
	q := &PublicKeyQuota{
		GlobalDailyLimit:  0, // unlimited
		IPDailyLimit:      0, // unlimited (v4.6.54 default) — triggers the bug
		HourlyWindowLimit: 0, // unlimited
		ModelLimits:       map[string]int64{},
		ipUsage:           make(map[string]*IPUsageTracker),
		hourlyUsage:       make(map[string]int64),
		modelUsage:        make(map[string]int64),
	}

	// New IP, never seen before — must not panic
	ok, _, _ := q.ReserveQuota("192.0.2.1", "test-model", 20)
	if !ok {
		t.Fatalf("ReserveQuota should succeed with unlimited quotas")
	}

	// Tracker should have been created and charged
	tracker, exists := q.ipUsage["192.0.2.1"]
	if !exists {
		t.Fatalf("IP tracker was not created")
	}
	if tracker.DailyUsed != 20 {
		t.Errorf("expected DailyUsed=20, got %d", tracker.DailyUsed)
	}
	if tracker.HourlyUsed != 20 {
		t.Errorf("expected HourlyUsed=20, got %d", tracker.HourlyUsed)
	}

	// Second call from same IP — must not panic either
	ok, _, _ = q.ReserveQuota("192.0.2.1", "test-model", 30)
	if !ok {
		t.Fatalf("second ReserveQuota should succeed")
	}
	if q.ipUsage["192.0.2.1"].DailyUsed != 50 {
		t.Errorf("expected DailyUsed=50, got %d", q.ipUsage["192.0.2.1"].DailyUsed)
	}
}
