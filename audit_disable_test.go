package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Route3 "数据主权" — zero-log privacy mode: audit_enabled=false must leave the
// whole audit trail off (no file created, no webhook forwarding, auditRecord a
// no-op). Default stays on for existing deployments (no behavior change).

func TestInitAuditLog_ZeroLogPrivacyMode(t *testing.T) {
	setupTestEnv(t)
	old := auditLog
	defer func() { auditLog = old }()

	tmp := t.TempDir()
	cfg.Set("audit_enabled", "false")
	defer cfg.Set("audit_enabled", "true")

	initAuditLog(tmp)

	if auditLog != nil {
		t.Fatalf("auditLog = %+v, want nil when audit_enabled=false", auditLog)
	}
	if _, err := os.Stat(filepath.Join(tmp, "audit")); !os.IsNotExist(err) {
		t.Errorf("audit dir should not exist under zero-log mode, err=%v", err)
	}
	// auditRecord stays a no-op (no panic, nothing written, no webhook fired).
	req := httptest.NewRequest(http.MethodGet, "/api/admin/info", nil)
	auditRecord(req, "test.action", "test-target", "should-not-persist", true)
}

func TestInitAuditLog_DefaultEnabledWrites(t *testing.T) {
	setupTestEnv(t)
	old := auditLog
	defer func() {
		CloseAuditLog()
		auditLog = old
	}()

	tmp := t.TempDir()
	cfg.Set("audit_enabled", "true")

	initAuditLog(tmp)
	if auditLog == nil || !auditLog.enabled {
		t.Fatalf("auditLog not enabled by default, got %+v", auditLog)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/info", nil)
	auditRecord(req, "default.on", "target", "persisted", true)

	logPath := filepath.Join(tmp, "audit", "audit.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read %s: %v", logPath, err)
	}
	if len(b) == 0 {
		t.Error("audit.log written but empty")
	}
	if !strings.Contains(string(b), "default.on") {
		t.Errorf("audit.log missing the recorded action, got %q", string(b))
	}
}
