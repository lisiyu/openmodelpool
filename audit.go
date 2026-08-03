package main

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditLogger records admin actions for compliance and debugging.
// B161: Audit trail for all administrative operations.

type AuditLogger struct {
	mu      sync.Mutex
	file    *os.File
	enabled bool
	path    string
}

var auditLog *AuditLogger

func initAuditLog() {
	dataDir := "data"
	auditDir := filepath.Join(dataDir, "audit")
	if err := os.MkdirAll(auditDir, 0700); err != nil {
		slog.Error("failed to create audit directory", "error", err)
		return
	}
	auditPath := filepath.Join(auditDir, "audit.log")
	f, err := os.OpenFile(auditPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Error("failed to open audit log", "error", err)
		return
	}
	auditLog = &AuditLogger{
		file:    f,
		enabled: true,
		path:    auditPath,
	}
	slog.Info("audit logging enabled", "path", auditPath)
}

// auditRecord writes an audit record for an admin action.
func auditRecord(r *http.Request, action, target, detail string, success bool) {
	if auditLog == nil || !auditLog.enabled {
		return
	}
	username := "anonymous"
	if u, ok := r.Context().Value("username").(string); ok && u != "" {
		username = u
	}
	clientIP := extractClientIP(r.RemoteAddr)
	status := "success"
	if !success {
		status = "failure"
	}
	ts := time.Now().Format(time.RFC3339)
	line := ts + " | " + username + " | " + clientIP + " | " + action + " | " + target + " | " + detail + " | " + status + "\n"

	auditLog.mu.Lock()
	defer auditLog.mu.Unlock()
	if _, err := auditLog.file.WriteString(line); err != nil {
		slog.Error("audit log write failed", "error", err)
	}
}

// CloseAuditLog closes the audit log file.
func CloseAuditLog() {
	if auditLog != nil && auditLog.file != nil {
		auditLog.Close()
	}
}

func (a *AuditLogger) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		a.file.Close()
		a.file = nil
		a.enabled = false
	}
}
