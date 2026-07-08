package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// EventBus manages real-time event broadcasting to connected clients.
// Uses Server-Sent Events (SSE) for browser-compatible push notifications.
type EventBus struct {
	mu      sync.RWMutex
	clients map[string]chan SSEEvent // clientID -> event channel
	nextID  int
}

// SSEEvent represents a push event sent to connected clients.
type SSEEvent struct {
	Type string `json:"type"` // "provider_status", "health_change", "config_update", etc.
	Data any    `json:"data"`
	Time string `json:"time"`
}

var eventBus *EventBus

func initEventBus() {
	eventBus = &EventBus{
		clients: make(map[string]chan SSEEvent),
	}
	slog.Info("event bus initialized")
}

// Subscribe creates a new SSE subscription and returns the client ID and channel.
func (eb *EventBus) Subscribe() (string, <-chan SSEEvent) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	eb.nextID++
	clientID := fmt.Sprintf("client-%d-%d", time.Now().UnixNano(), eb.nextID)
	ch := make(chan SSEEvent, 64) // buffered to avoid blocking
	eb.clients[clientID] = ch

	slog.Debug("sse client subscribed", "client_id", clientID, "total", len(eb.clients))
	return clientID, ch
}

// Unsubscribe removes a client subscription.
func (eb *EventBus) Unsubscribe(clientID string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if ch, ok := eb.clients[clientID]; ok {
		close(ch)
		delete(eb.clients, clientID)
	}
	slog.Debug("sse client unsubscribed", "client_id", clientID, "total", len(eb.clients))
}

// Broadcast sends an event to all connected clients.
func (eb *EventBus) Broadcast(event SSEEvent) {
	if event.Time == "" {
		event.Time = time.Now().Format(time.RFC3339)
	}

	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for id, ch := range eb.clients {
		select {
		case ch <- event:
			// sent successfully
		default:
			// client channel full, skip (slow consumer)
			slog.Debug("sse client slow, dropping event", "client_id", id)
		}
	}
}

// BroadcastProviderStatus sends a provider status update to all clients.
func BroadcastProviderStatus(providerID, status string) {
	if eventBus == nil {
		return
	}
	eventBus.Broadcast(SSEEvent{
		Type: "provider_status",
		Data: map[string]string{
			"provider_id": providerID,
			"status":      status,
		},
	})
}

// BroadcastHealthChange sends a health status change event.
func BroadcastHealthChange(providerID, oldStatus, newStatus string) {
	if eventBus == nil {
		return
	}
	eventBus.Broadcast(SSEEvent{
		Type: "health_change",
		Data: map[string]string{
			"provider_id": providerID,
			"old_status":  oldStatus,
			"new_status":  newStatus,
		},
	})
}

// BroadcastConfigUpdate sends a configuration update event.
func BroadcastConfigUpdate(key string) {
	if eventBus == nil {
		return
	}
	eventBus.Broadcast(SSEEvent{
		Type: "config_update",
		Data: map[string]string{"key": key},
	})
}

// handleSSE is the HTTP handler for the /events SSE endpoint.
func handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientID, ch := eventBus.Subscribe()
	defer eventBus.Unsubscribe(clientID)

	// Send initial connection event
	initData, _ := json.Marshal(SSEEvent{
		Type: "connected",
		Data: map[string]string{"client_id": clientID},
		Time: time.Now().Format(time.RFC3339),
	})
	fmt.Fprintf(w, "data: %s\n\n", initData)
	flusher.Flush()

	ctx := r.Context()
	keepAlive := time.NewTicker(30 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		case <-keepAlive.C:
			// Send keepalive comment to prevent connection timeout
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// GetEventBusStats returns event bus statistics.
func GetEventBusStats() map[string]any {
	if eventBus == nil {
		return map[string]any{"enabled": false}
	}
	eventBus.mu.RLock()
	defer eventBus.mu.RUnlock()
	return map[string]any{
		"enabled":        true,
		"connected_clients": len(eventBus.clients),
	}
}
