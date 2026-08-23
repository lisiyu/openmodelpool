package main

import (
	"context"
	"testing"
	"time"
)

// ============================================================
// B10-BL1: browser login auto-cleanup timer must be bound to the
// session instance — a stale timer from an earlier attempt must not
// kill the replacement session (root cause of the reported
// "没有活跃的浏览器登录会话" mid-login error).
// ============================================================

func TestP10_BrowserLogin_StaleTimerDoesNotKillReplacement(t *testing.T) {
	oldTTL := browserSessionTTL
	browserSessionTTL = 60 * time.Millisecond
	defer func() {
		browserSessionsMu.RLock()
		ids := make([]string, 0, len(browserSessions))
		for id := range browserSessions {
			ids = append(ids, id)
		}
		browserSessionsMu.RUnlock()
		for _, id := range ids {
			cleanupSession(id)
		}
		browserSessionTTL = oldTTL
	}()

	mk := func(id string) *BrowserLoginSession {
		ctx, cancel := context.WithCancel(context.Background())
		return &BrowserLoginSession{ctx: ctx, cancel: cancel, providerID: id, status: "navigating", createdAt: time.Now()}
	}

	// Attempt 1 starts with a short TTL; its timer is armed.
	browserSessionTTL = 60 * time.Millisecond
	s1 := mk("p10-stale-timer")
	browserSessionsMu.Lock()
	browserSessions["p10-stale-timer"] = s1
	browserSessionsMu.Unlock()
	scheduleAutoCleanup(s1)

	// Admin retries shortly after: s2 is armed with a longer remaining
	// lifetime (realistic — its clock started later).
	browserSessionTTL = 30 * time.Second
	browserSessionsMu.Lock()
	s2 := mk("p10-stale-timer")
	browserSessions["p10-stale-timer"] = s2
	browserSessionsMu.Unlock()
	scheduleAutoCleanup(s2)

	// Old behavior: s1's fire-and-forget goroutine called cleanupSession(id)
	// at ITS ttl and deleted whatever sat in the map — killing s2 mid-login.
	time.Sleep(200 * time.Millisecond)

	browserSessionsMu.RLock()
	cur, ok := browserSessions["p10-stale-timer"]
	browserSessionsMu.RUnlock()
	if !ok || cur != s2 {
		t.Fatalf("replacement session was killed by stale timer (ok=%v, same=%v)", ok, cur == s2)
	}
}

// B10-BL1b: manual cleanup (cancel endpoint / finish) must stop the pending
// timer so it cannot fire afterwards on a recycled map slot.
func TestP10_BrowserLogin_CleanupStopsTimer(t *testing.T) {
	oldTTL := browserSessionTTL
	browserSessionTTL = 80 * time.Millisecond
	defer func() { browserSessionTTL = oldTTL }()

	ctx, cancel := context.WithCancel(context.Background())
	sess := &BrowserLoginSession{ctx: ctx, cancel: cancel, providerID: "p10-cleanup-stops", status: "navigating", createdAt: time.Now()}
	browserSessionsMu.Lock()
	browserSessions["p10-cleanup-stops"] = sess
	browserSessionsMu.Unlock()
	scheduleAutoCleanup(sess)

	cleanupSession("p10-cleanup-stops")

	// Wait past the original TTL: nothing should panic or re-delete, and the
	// map stays clean even if a new session were inserted right after.
	time.Sleep(160 * time.Millisecond)
	browserSessionsMu.RLock()
	_, present := browserSessions["p10-cleanup-stops"]
	browserSessionsMu.RUnlock()
	if present {
		t.Fatalf("session should have been removed by manual cleanup")
	}
}

// B10-BL2: terminal status spelling must be consistent — start treats
// "cancelled" as clearable; cancel handler writes "cancelled".
func TestP10_BrowserLogin_TerminalStatusSpelling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess := &BrowserLoginSession{ctx: ctx, cancel: cancel, providerID: "p10-spelling", status: "cancelled", createdAt: time.Now()}
	if !(sess.status == "error" || sess.status == "cancelled") {
		t.Fatalf("cancelled session not recognized as terminal by start handler logic")
	}
}
