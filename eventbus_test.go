package main

import (
	"testing"
	"time"
)

// ============================================================
// EventBus tests
// ============================================================

func TestEventBus_Subscribe(t *testing.T) {
	eb := &EventBus{
		clients: make(map[string]chan SSEEvent),
	}

	clientID, ch := eb.Subscribe()
	if clientID == "" {
		t.Error("Subscribe() returned empty clientID")
	}
	if ch == nil {
		t.Error("Subscribe() returned nil channel")
	}

	eb.mu.RLock()
	if _, ok := eb.clients[clientID]; !ok {
		t.Error("client not found in clients map after Subscribe")
	}
	eb.mu.RUnlock()
}

func TestEventBus_Unsubscribe(t *testing.T) {
	eb := &EventBus{
		clients: make(map[string]chan SSEEvent),
	}

	clientID, ch := eb.Subscribe()

	// Drain the channel in a goroutine so Unsubscribe's close(ch) doesn't panic
	go func() {
		for range ch {
		}
	}()

	eb.Unsubscribe(clientID)

	eb.mu.RLock()
	if _, ok := eb.clients[clientID]; ok {
		t.Error("client still in map after Unsubscribe")
	}
	eb.mu.RUnlock()
}

func TestEventBus_Broadcast(t *testing.T) {
	eb := &EventBus{
		clients: make(map[string]chan SSEEvent),
	}

	_, ch1 := eb.Subscribe()
	_, ch2 := eb.Subscribe()

	event := SSEEvent{
		Type: "test_event",
		Data: map[string]string{"key": "value"},
	}

	eb.Broadcast(event)

	// Both channels should receive the event (with auto-filled Time)
	select {
	case received := <-ch1:
		if received.Type != "test_event" {
			t.Errorf("ch1: Type = %s, want test_event", received.Type)
		}
		if received.Time == "" {
			t.Error("ch1: Time should be auto-filled")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch1: timeout waiting for broadcast event")
	}

	select {
	case received := <-ch2:
		if received.Type != "test_event" {
			t.Errorf("ch2: Type = %s, want test_event", received.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2: timeout waiting for broadcast event")
	}

	// Clean up: drain the remaining goroutine channel
	go func() {
		for range ch1 {
		}
	}()
	go func() {
		for range ch2 {
		}
	}()
	eb.Unsubscribe(clientIDFromChan(t, eb, ch1))
	eb.Unsubscribe(clientIDFromChan(t, eb, ch2))
}

func TestEventBus_Broadcast_PreservesExistingTime(t *testing.T) {
	eb := &EventBus{
		clients: make(map[string]chan SSEEvent),
	}

	_, ch := eb.Subscribe()

	existingTime := "2026-07-01T00:00:00Z"
	event := SSEEvent{
		Type: "preset_time",
		Time: existingTime,
	}

	eb.Broadcast(event)

	select {
	case received := <-ch:
		if received.Time != existingTime {
			t.Errorf("Time = %s, want %s (should preserve existing time)", received.Time, existingTime)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for broadcast event")
	}

	go func() { for range ch {} }()
	eb.Unsubscribe(clientIDFromChan(t, eb, ch))
}

func TestEventBus_Broadcast_SlowConsumer(t *testing.T) {
	eb := &EventBus{
		clients: make(map[string]chan SSEEvent),
	}

	// Create a channel with no buffer (overrides Subscribe default)
	eb.mu.Lock()
	eb.nextID++
	clientID := "test-slow"
	smallCh := make(chan SSEEvent, 1) // only 1 capacity
	eb.clients[clientID] = smallCh
	eb.mu.Unlock()

	// Fill the channel
	smallCh <- SSEEvent{Type: "fill"}

	// Broadcast should not block (slow consumer is skipped)
	eb.Broadcast(SSEEvent{Type: "dropped"})

	// Cleanup
	go func() { for range smallCh {} }()
	eb.Unsubscribe(clientID)
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	eb := &EventBus{
		clients: make(map[string]chan SSEEvent),
	}

	// Subscribe 10 clients
	var channels []<-chan SSEEvent
	for i := 0; i < 10; i++ {
		_, ch := eb.Subscribe()
		channels = append(channels, ch)
	}

	eb.mu.RLock()
	if len(eb.clients) != 10 {
		t.Errorf("expected 10 clients, got %d", len(eb.clients))
	}
	eb.mu.RUnlock()

	eb.Broadcast(SSEEvent{Type: "mass_event"})

	for i, ch := range channels {
		select {
		case received := <-ch:
			if received.Type != "mass_event" {
				t.Errorf("client %d: Type = %s, want mass_event", i, received.Type)
			}
		case <-time.After(200 * time.Millisecond):
			t.Errorf("client %d: timeout waiting for event", i)
		}
	}

	// Cleanup: unsubscribe all (with goroutines draining)
	for _, ch := range channels {
		go func(c <-chan SSEEvent) {
			for range c {
			}
		}(ch)
	}
	for id := range eb.clients {
		eb.Unsubscribe(id)
	}
}

// ============================================================
// Broadcast helper functions
// ============================================================

func TestBroadcastProviderStatus_NilEventBus(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()

	// Should not panic
	BroadcastProviderStatus("provider-1", "online")
}

func TestBroadcastHealthChange_NilEventBus(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()

	// Should not panic
	BroadcastHealthChange("provider-1", "online", "offline")
}

func TestBroadcastConfigUpdate_NilEventBus(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()

	// Should not panic
	BroadcastConfigUpdate("some_key")
}

// ============================================================
// GetEventBusStats tests
// ============================================================

func TestGetEventBusStats_Nil(t *testing.T) {
	orig := eventBus
	eventBus = nil
	defer func() { eventBus = orig }()

	stats := GetEventBusStats()
	if stats["enabled"] != false {
		t.Error("expected enabled=false when eventBus is nil")
	}
}

func TestGetEventBusStats_Active(t *testing.T) {
	orig := eventBus
	eventBus = &EventBus{
		clients: make(map[string]chan SSEEvent),
	}
	defer func() { eventBus = orig }()

	stats := GetEventBusStats()
	if stats["enabled"] != true {
		t.Error("expected enabled=true")
	}
	if stats["connected_clients"].(int) != 0 {
		t.Error("expected 0 connected clients initially")
	}
}

// ============================================================
// initEventBus test
// ============================================================

func TestInitEventBus(t *testing.T) {
	orig := eventBus
	defer func() { eventBus = orig }()

	initEventBus()
	if eventBus == nil {
		t.Fatal("initEventBus did not set eventBus")
	}
	if eventBus.clients == nil {
		t.Error("eventBus.clients should be initialized")
	}
}

// ============================================================
// Helper
// ============================================================

// clientIDFromChan finds the client ID for a given channel in the event bus.
func clientIDFromChan(t *testing.T, eb *EventBus, target <-chan SSEEvent) string {
	t.Helper()
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for id, ch := range eb.clients {
		if ch == target {
			return id
		}
	}
	return ""
}
