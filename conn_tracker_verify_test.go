package main

import "testing"

// ============================================================
// G5 verification (QA, Edward): connection-tracking lifecycle
//
// The G5 rename changed handlers.go call sites from
//   IncrProviderConn(p.ID) / DecrProviderConn(p.ID)
// to
//   IncrConn(p.ID, accessType) / DecrConn(p.ID, accessType)
// at the request begin/end boundaries. This test mirrors that exact request
// lifecycle (stream and non-stream paths both call IncrConn/DecrConn with the
// access type resolved from the request key) and asserts:
//   1. Counters start at zero (no stale state).
//   2. During concurrent requests the provider active-conn counter reflects
//      the total in-flight count and the guest counter reflects only the
//      accessType=="guest" subset.
//   3. After every request completes (DecrConn at request end) both counters
//      return to zero — i.e. no leak / no regression from the rename.
// ============================================================

func TestConnTracker_RequestLifecycleReturnsToZero(t *testing.T) {
	const pid = "lifecycle-verify-p1"

	if GetProviderConns(pid) != 0 {
		t.Fatalf("provider conns should start at 0, got %d", GetProviderConns(pid))
	}
	if GetGuestConns() != 0 {
		t.Fatalf("guest conns should start at 0, got %d", GetGuestConns())
	}

	// Simulate 3 concurrent private + 2 concurrent guest requests.
	inFlight := []string{"private", "private", "private", "guest", "guest"}
	for _, at := range inFlight {
		IncrConn(pid, at) // mirrors handlers.go IncrConn(p.ID, accessType)
	}

	// Mid-flight: 5 provider conns total, 2 of them are guest.
	if got := GetProviderConns(pid); got != 5 {
		t.Errorf("provider conns expected 5 mid-flight, got %d", got)
	}
	if got := GetGuestConns(); got != 2 {
		t.Errorf("guest conns expected 2 mid-flight, got %d", got)
	}

	// Requests complete (mirrors handlers.go DecrConn(p.ID, accessType)).
	for _, at := range inFlight {
		DecrConn(pid, at)
	}

	// After completion: both counters must return to zero.
	if got := GetProviderConns(pid); got != 0 {
		t.Errorf("provider conns should return to 0 after requests finish, got %d", got)
	}
	if got := GetGuestConns(); got != 0 {
		t.Errorf("guest conns should return to 0 after requests finish, got %d", got)
	}
}

// TestConnTracker_GuestScopeIsolation verifies that only accessType=="guest"
// requests increment the dedicated guest counter, while private/public requests
// touch only the provider counter — confirming accessType scope is correct
// after the G5 rename.
func TestConnTracker_GuestScopeIsolation(t *testing.T) {
	const pid = "scope-verify-p1"

	IncrConn(pid, "private")
	IncrConn(pid, "public")
	if GetGuestConns() != 0 {
		t.Errorf("guest conns must stay 0 for private/public requests, got %d", GetGuestConns())
	}
	if GetProviderConns(pid) != 2 {
		t.Errorf("provider conns expected 2 for private+public, got %d", GetProviderConns(pid))
	}

	IncrConn(pid, "guest")
	if GetGuestConns() != 1 {
		t.Errorf("guest conns expected 1 after a guest request, got %d", GetGuestConns())
	}
	if GetProviderConns(pid) != 3 {
		t.Errorf("provider conns expected 3 (incl. guest), got %d", GetProviderConns(pid))
	}

	// Clean up.
	DecrConn(pid, "private")
	DecrConn(pid, "public")
	DecrConn(pid, "guest")
	if GetProviderConns(pid) != 0 || GetGuestConns() != 0 {
		t.Errorf("counters leaked: provider=%d guest=%d", GetProviderConns(pid), GetGuestConns())
	}
}
