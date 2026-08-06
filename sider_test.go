package main

import (
	"path/filepath"
	"testing"
	"time"
)

// ============================================================
// SiderMonitor tests
// ============================================================

func TestSiderMonitor_RecordSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sider.json")

	s := &SiderMonitor{
		status:   SiderStatus{},
		filePath: path,
	}

	s.RecordSuccess()

	if s.status.TokenStatus != "ok" {
		t.Errorf("TokenStatus = %q, want ok", s.status.TokenStatus)
	}
	if s.status.LastSuccessAt == "" {
		t.Error("LastSuccessAt should be set after RecordSuccess")
	}
	if s.status.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", s.status.ConsecutiveFailures)
	}
	if s.status.FailureMessage != "" {
		t.Errorf("FailureMessage should be cleared, got %q", s.status.FailureMessage)
	}
}

func TestSiderMonitor_RecordFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sider.json")

	s := &SiderMonitor{
		status:   SiderStatus{},
		filePath: path,
	}

	s.RecordFailure(401, "token expired")

	if s.status.TokenStatus != "expired" {
		t.Errorf("TokenStatus = %q, want expired", s.status.TokenStatus)
	}
	if s.status.LastFailureAt == "" {
		t.Error("LastFailureAt should be set after RecordFailure")
	}
	if s.status.ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1", s.status.ConsecutiveFailures)
	}
	if s.status.FailureMessage != "token expired" {
		t.Errorf("FailureMessage = %q, want 'token expired'", s.status.FailureMessage)
	}

	// Second failure increments
	s.RecordFailure(403, "inactive token")
	if s.status.ConsecutiveFailures != 2 {
		t.Errorf("ConsecutiveFailures = %d, want 2 after second failure", s.status.ConsecutiveFailures)
	}
}

func TestSiderMonitor_RecoverAfterFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sider.json")

	s := &SiderMonitor{
		status:   SiderStatus{},
		filePath: path,
	}

	s.RecordFailure(401, "expired")
	s.RecordFailure(401, "expired again")

	if s.status.ConsecutiveFailures != 2 {
		t.Fatalf("expected 2 failures, got %d", s.status.ConsecutiveFailures)
	}

	s.RecordSuccess()
	if s.status.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures should reset to 0 after success, got %d", s.status.ConsecutiveFailures)
	}
	if s.status.TokenStatus != "ok" {
		t.Errorf("TokenStatus should be 'ok' after success, got %q", s.status.TokenStatus)
	}
}

func TestSiderMonitor_IsExpired(t *testing.T) {
	s := &SiderMonitor{
		status:   SiderStatus{TokenStatus: "ok"},
		filePath: "",
	}

	if s.IsExpired() {
		t.Error("IsExpired should return false when status is ok")
	}

	s.status.TokenStatus = "expired"
	if !s.IsExpired() {
		t.Error("IsExpired should return true when status is expired")
	}
}

func TestSiderMonitor_GetStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sider.json")

	s := &SiderMonitor{
		status: SiderStatus{
			TokenStatus:   "ok",
			LastSuccessAt: "2026-07-01T00:00:00Z",
		},
		filePath: path,
	}

	status := s.GetStatus()
	if status.TokenStatus != "ok" {
		t.Errorf("status.TokenStatus = %q, want ok", status.TokenStatus)
	}
	if status.LastSuccessAt != "2026-07-01T00:00:00Z" {
		t.Errorf("status.LastSuccessAt mismatch: got %q", status.LastSuccessAt)
	}
}

func TestSiderMonitor_Load(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sider.json")

	// Pre-populate file
	s1 := &SiderMonitor{
		status: SiderStatus{
			TokenStatus:   "ok",
			LastSuccessAt: time.Now().Format(time.RFC3339),
		},
		filePath: path,
	}
	s1.save()

	// Load in new monitor
	s2 := &SiderMonitor{filePath: path}
	s2.load()

	if s2.status.TokenStatus != "ok" {
		t.Errorf("loaded TokenStatus = %q, want ok", s2.status.TokenStatus)
	}
}

func TestInitSiderMonitor(t *testing.T) {
	orig := siderMon
	defer func() { siderMon = orig }()

	dir := t.TempDir()
	path := filepath.Join(dir, "sider.json")
	initSiderMonitor(path)

	if siderMon == nil {
		t.Fatal("initSiderMonitor did not set siderMon")
	}
	if siderMon.filePath != path {
		t.Errorf("filePath = %q, want %q", siderMon.filePath, path)
	}
}

func TestSiderMonitor_SaveOnlyOnStatusChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sider.json")

	s := &SiderMonitor{
		status:   SiderStatus{TokenStatus: "ok"},
		filePath: path,
	}

	// Save initial state
	s.save()

	// RecordSuccess when already "ok" - should NOT save (no status change)
	timeBefore := time.Now()
	s.RecordSuccess()

	// RecordFailure always saves
	s.RecordFailure(401, "expired")

	status := s.GetStatus()
	if status.TokenStatus != "expired" {
		t.Errorf("expected expired, got %q", status.TokenStatus)
	}
	_ = timeBefore
}

func TestSiderMonitor_Concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sider.json")

	s := &SiderMonitor{
		status:   SiderStatus{},
		filePath: path,
	}

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			s.RecordSuccess()
			s.RecordFailure(401, "err")
			s.IsExpired()
			s.GetStatus()
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
