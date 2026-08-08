package main

import (
	"os"
	"testing"
	"time"
)

// TestMain shrinks the config write-coalescing window for the whole test binary.
//
// In production the debounce writer waits 3s before flushing so that bursts of
// config writes collapse into a single disk write. That is the right behaviour
// for a long-running server, but it is pathological under test: setupTestEnv's
// cleanup waits for the writer goroutine to exit, so every test that touches
// config would pay the full 3s window. With several hundred such tests the
// suite spent most of its wall time asleep and blew past the timeout.
//
// Shrinking the window keeps the coalescing semantics intact (writes are still
// batched, the writer still exits via stopCh) while making teardown effectively
// instant.
func TestMain(m *testing.M) {
	configDebounceOverride = 5 * time.Millisecond
	os.Exit(m.Run())
}
