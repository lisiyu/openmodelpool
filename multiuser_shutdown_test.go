package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// readConsumersFromDisk loads consumers.json directly, bypassing the in-memory
// manager, so assertions reflect what actually reached the filesystem.
func readConsumersFromDisk(t *testing.T, path string) map[string]*Consumer {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read consumers file: %v", err)
	}
	var data struct {
		Invites   map[string]*InviteCode `json:"invites"`
		Consumers map[string]*Consumer   `json:"consumers"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("unmarshal consumers file: %v", err)
	}
	return data.Consumers
}

// TestMultiUserStopBatchSaveFlushesPendingUsage is the regression test for the
// shutdown data-loss bug: RecordConsumerUsage below the batch threshold only
// marks the manager dirty and relies on the 5s ticker, so a shutdown that did
// not flush silently discarded the most recent usage. gracefulShutdown never
// called StopBatchSave at all, meaning every restart could drop up to five
// seconds of token and request counts for every consumer.
func TestMultiUserStopBatchSaveFlushesPendingUsage(t *testing.T) {
	setupTestEnv(t)

	code := multiUser.CreateInviteCode(0, "consumer")
	consumer, err := multiUser.CreateConsumer("flush-test", code)
	if err != nil || consumer == nil {
		t.Fatalf("CreateConsumer failed: %v", err)
	}
	path := multiUser.dataPath

	// One usage record keeps dirtyCount at 1, well below the immediate-save
	// threshold of 10, so nothing has been written yet.
	multiUser.RecordConsumerUsage(consumer.ID, 100)

	if onDisk := readConsumersFromDisk(t, path)[consumer.ID]; onDisk == nil {
		t.Fatal("consumer should already exist on disk after creation")
	} else if onDisk.TotalTokens != 0 {
		t.Fatalf("precondition failed: usage should still be buffered in memory, got %d tokens on disk", onDisk.TotalTokens)
	}

	multiUser.StopBatchSave()

	flushed := readConsumersFromDisk(t, path)[consumer.ID]
	if flushed == nil {
		t.Fatal("consumer missing from disk after shutdown flush")
	}
	if flushed.TotalTokens != 100 {
		t.Errorf("TotalTokens on disk = %d, want 100 (usage lost on shutdown)", flushed.TotalTokens)
	}
	if flushed.TotalRequests != 1 {
		t.Errorf("TotalRequests on disk = %d, want 1 (usage lost on shutdown)", flushed.TotalRequests)
	}
}

// TestMultiUserStopBatchSaveWaitsForExit pins the contract that makes the
// flush observable: when StopBatchSave returns, the goroutine has finished,
// so no write can still be in flight. Without this, the final flush raced
// t.TempDir() cleanup and produced an intermittent "directory is not empty"
// failure in whichever test left the manager dirty.
func TestMultiUserStopBatchSaveWaitsForExit(t *testing.T) {
	setupTestEnv(t)

	multiUser.StopBatchSave()

	select {
	case <-multiUser.saveDone:
	default:
		t.Fatal("saveDone must be closed once StopBatchSave returns")
	}
}

// TestMultiUserStopBatchSaveIdempotent covers shutdown paths that can be
// reached twice (signal handler plus test cleanup). Closing a channel twice
// panics, so this must not regress.
func TestMultiUserStopBatchSaveIdempotent(t *testing.T) {
	setupTestEnv(t)

	multiUser.StopBatchSave()
	multiUser.StopBatchSave()
	multiUser.StopBatchSave()
}

// TestMultiUserStopBatchSaveWithoutLoop guards managers built directly rather
// than through initMultiUser (done in tests that only exercise load()): their
// stop channels are nil and there is no goroutine to wait for.
func TestMultiUserStopBatchSaveWithoutLoop(t *testing.T) {
	mgr := &MultiUserManager{
		invites:   make(map[string]*InviteCode),
		consumers: make(map[string]*Consumer),
		apiKeyMap: make(map[string]string),
		dataPath:  t.TempDir() + "/consumers.json",
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.StopBatchSave()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StopBatchSave blocked on a manager with no batch save loop")
	}
}
