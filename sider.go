package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"
)

// SiderMonitor tracks Sider.ai token validity status.
type SiderMonitor struct {
	mu       sync.RWMutex
	status   SiderStatus
	filePath string
}

func initSiderMonitor(path string) {
	siderMon = &SiderMonitor{filePath: path}
	siderMon.load()
}

func (s *SiderMonitor) load() {
	b, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	json.Unmarshal(b, &s.status)
}

func (s *SiderMonitor) save() {
	b, _ := json.MarshalIndent(s.status, "", "  ")
	os.MkdirAll("data", 0700)
	atomicWriteFile(s.filePath, b, 0600)
}

// RecordSuccess records a successful request. Only writes disk on status change.
func (s *SiderMonitor) RecordSuccess() {
	now := time.Now().Format(time.RFC3339)
	s.mu.Lock()
	prev := s.status.TokenStatus
	s.status.TokenStatus = "ok"
	s.status.LastSuccessAt = now
	s.status.ConsecutiveFailures = 0
	s.status.FailureMessage = ""
	s.status.CheckedAt = now
	if prev != "ok" {
		s.save()
	}
	s.mu.Unlock()
}

// RecordFailure records an auth failure (token expired). Always persists.
func (s *SiderMonitor) RecordFailure(httpStatus int, msg string) {
	now := time.Now().Format(time.RFC3339)
	s.mu.Lock()
	s.status.TokenStatus = "expired"
	s.status.LastFailureAt = now
	s.status.ConsecutiveFailures++
	s.status.FailureMessage = msg
	s.status.CheckedAt = now
	s.save()
	s.mu.Unlock()
	slog.Warn("sider token expired", "http_status", httpStatus, "msg", msg)
}

// IsExpired returns whether the token is expired.
func (s *SiderMonitor) IsExpired() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.TokenStatus == "expired"
}

// Reset clears a stale expired status, e.g. after the operator rotates the
// token. The next request will re-probe and set the real status.
func (s *SiderMonitor) Reset() {
	s.mu.Lock()
	s.status.TokenStatus = "ok"
	s.status.ConsecutiveFailures = 0
	s.status.FailureMessage = ""
	s.status.CheckedAt = time.Now().Format(time.RFC3339)
	s.save()
	s.mu.Unlock()
}

// GetStatus returns the current status snapshot.
func (s *SiderMonitor) GetStatus() SiderStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// ---------------------------------------------------------------------------
// Sider conversation session affinity (v4.6.64).
//
// The Sider web API (sider.ai/api/chat/v1/completions) is conversation-aware:
// it accepts a "cid" (conversation id). Sending an empty cid starts a new
// conversation; sending a previously returned cid continues that conversation
// in the same topic on Sider's side.
//
// The /v1 gateway API is stateless, so the gateway derives conversation
// identity from the message history itself (history-chain):
//   - For a request with messages [m1..mn], probe SHA256(apiKey + m1..mk)
//     for k from n-1 down to 0 (longest prefix first).
//   - A hit means this request continues a previous conversation: reuse its cid.
//   - A miss means a new conversation: send empty cid, then store the cid
//     Sider returns under SHA256(apiKey + m1..mn) for future continuations.
// Including the API key in the hash isolates different gateway users.
// ---------------------------------------------------------------------------

// siderSessionTTL is how long a conversation mapping is kept.
const siderSessionTTL = 24 * time.Hour

// siderSessionMaxEntries caps memory usage; oldest entries are evicted first.
const siderSessionMaxEntries = 10000

type siderSessionEntry struct {
	cid       string
	expiresAt time.Time
}

type siderSessionStore struct {
	mu      sync.RWMutex
	entries map[string]siderSessionEntry
}

var siderSessions = &siderSessionStore{entries: make(map[string]siderSessionEntry)}

// siderHistoryHash returns SHA256(apiKey + role/content of messages).
func siderHistoryHash(apiKey string, messages []ChatMessage) string {
	h := sha256.New()
	h.Write([]byte(apiKey))
	h.Write([]byte{0})
	for _, m := range messages {
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// lookup finds the cid for the longest matching history prefix.
// Returns "" when this request starts a new conversation.
func (s *siderSessionStore) lookup(apiKey string, messages []ChatMessage) string {
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Longest prefix first: k = n-1 .. 0.
	for k := len(messages) - 1; k >= 0; k-- {
		key := siderHistoryHash(apiKey, messages[:k])
		if e, ok := s.entries[key]; ok && now.Before(e.expiresAt) {
			return e.cid
		}
	}
	return ""
}

// store records the cid Sider returned for this exact message history.
func (s *siderSessionStore) store(apiKey string, messages []ChatMessage, cid string) {
	if cid == "" {
		return
	}
	key := siderHistoryHash(apiKey, messages)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	// Evict expired entries first; if still over cap, drop arbitrary old ones.
	if len(s.entries) >= siderSessionMaxEntries {
		for k, e := range s.entries {
			if now.After(e.expiresAt) {
				delete(s.entries, k)
			}
		}
	}
	for len(s.entries) >= siderSessionMaxEntries {
		for k := range s.entries {
			delete(s.entries, k)
			break
		}
	}
	s.entries[key] = siderSessionEntry{cid: cid, expiresAt: now.Add(siderSessionTTL)}
}

// siderExtractCid pulls the conversation id from a Sider API response body.
// The cid lives inside the "data" object next to "text"; check the top level
// too for forward compatibility. Returns "" when absent.
func siderExtractCid(respBody []byte) string {
	var parsed map[string]any
	if json.Unmarshal(respBody, &parsed) != nil {
		return ""
	}
	if d, ok := parsed["data"].(map[string]any); ok {
		if cid, ok := d["cid"].(string); ok && cid != "" {
			return cid
		}
	}
	if cid, ok := parsed["cid"].(string); ok && cid != "" {
		return cid
	}
	return ""
}
