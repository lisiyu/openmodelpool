package main

import (
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

var siderMon *SiderMonitor

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
	os.MkdirAll("data", 0755)
	os.WriteFile(s.filePath, b, 0644)
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

// IsExpired returns whether the token is expired (lock-free fast path).
func (s *SiderMonitor) IsExpired() bool {
	return s.status.TokenStatus == "expired"
}

// GetStatus returns the current status snapshot.
func (s *SiderMonitor) GetStatus() SiderStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}
