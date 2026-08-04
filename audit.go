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

const (
	auditMaxFileSize  = 10 * 1024 * 1024 // 10 MB max per log file
	auditMaxRotated   = 5                 // keep at most 5 rotated files
)

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

	// Check if rotation is needed before writing
	if auditLog.file != nil {
		if info, err := auditLog.file.Stat(); err == nil && info.Size() >= auditMaxFileSize {
			auditLog.rotateLocked()
		}
	}

	if auditLog.file != nil {
		if _, err := auditLog.file.WriteString(line); err != nil {
			slog.Error("audit log write failed", "error", err)
		}
	}
}

// rotateLocked rotates the audit log file. Caller must hold auditLog.mu.
func (a *AuditLogger) rotateLocked() {
	if a.file == nil {
		return
	}
	a.file.Close()

	// Shift rotated files: audit.log.4 -> audit.log.5 (delete oldest), etc.
	for i := auditMaxRotated - 1; i >= 1; i-- {
		oldPath := a.path + "." + itoa(i)
		newPath := a.path + "." + itoa(i+1)
		if i+1 > auditMaxRotated {
			os.Remove(newPath) // remove oldest beyond limit
		}
		os.Rename(oldPath, newPath)
	}

	// Rename current log to .1
	os.Rename(a.path, a.path+".1")

	// Open new file
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Error("failed to reopen audit log after rotation", "error", err)
		a.file = nil
		a.enabled = false
		return
	}
	a.file = f
	slog.Info("audit log rotated", "path", a.path)
}

// itoa is a simple int-to-string conversion to avoid fmt import.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
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
