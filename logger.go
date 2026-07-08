package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger provides structured logging with configurable levels and file output.
type Logger struct {
	mu         sync.Mutex
	accessFile *os.File
	logger     *slog.Logger
	level      slog.Level
	logPath    string
	maxSize    int64 // max file size in bytes before rotation
}

var appLogger *Logger

// Default max log file size: 50MB
const defaultMaxLogSize int64 = 50 * 1024 * 1024

// initLogger sets up the structured logging system.
// Logs go to both stdout and data/access.log with automatic rotation.
func initLogger(dataDir string) {
	level := parseLogLevel(cfg.Get("log_level", "info"))

	// Ensure data directory exists
	os.MkdirAll(dataDir, 0755)

	logPath := filepath.Join(dataDir, "access.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Warn("failed to open access log file, using stdout only", "error", err)
		appLogger = &Logger{
			logger:  slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})),
			level:   level,
			logPath: logPath,
		}
		return
	}

	// Write to both stdout and file
	multiWriter := io.MultiWriter(os.Stdout, f)

	handler := slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
		Level: level,
	})

	appLogger = &Logger{
		accessFile: f,
		logger:     slog.New(handler),
		level:      level,
		logPath:    logPath,
		maxSize:    defaultMaxLogSize,
	}

	// Set as default logger
	slog.SetDefault(appLogger.logger)

	slog.Info("structured logger initialized", "level", level.String(), "log_file", logPath, "max_size_mb", appLogger.maxSize/1024/1024)

	// Start periodic log rotation check
	go appLogger.rotationLoop()
}

// rotationLoop periodically checks if the log file needs rotation.
func (l *Logger) rotationLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.checkAndRotate()
	}
}

// checkAndRotate checks if the current log file exceeds max size and rotates if needed.
func (l *Logger) checkAndRotate() {
	if l == nil || l.logPath == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.accessFile == nil {
		return
	}

	info, err := l.accessFile.Stat()
	if err != nil {
		return
	}

	if info.Size() < l.maxSize {
		return
	}

	// Rotate the log file
	l.rotate()
}

// rotate performs the actual log file rotation.
// Current file is renamed to access.log.{timestamp} and a new file is opened.
func (l *Logger) rotate() {
	// Close current file
	if l.accessFile != nil {
		l.accessFile.Close()
	}

	// Rename current file with timestamp
	timestamp := time.Now().Format("20060102-150405")
	rotatedPath := fmt.Sprintf("%s.%s", l.logPath, timestamp)
	os.Rename(l.logPath, rotatedPath)

	// Keep only the last 5 rotated files (cleanup old ones)
	l.cleanupOldLogs(5)

	// Open new file
	f, err := os.OpenFile(l.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("failed to open new log file after rotation", "error", err)
		// Re-create with stdout-only fallback
		l.accessFile = nil
		l.logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: l.level}))
		slog.SetDefault(l.logger)
		return
	}

	l.accessFile = f
	multiWriter := io.MultiWriter(os.Stdout, f)
	handler := slog.NewTextHandler(multiWriter, &slog.HandlerOptions{Level: l.level})
	l.logger = slog.New(handler)
	slog.SetDefault(l.logger)

	slog.Info("log file rotated", "old_file", rotatedPath)
}

// cleanupOldLogs removes old rotated log files, keeping only the most recent n.
func (l *Logger) cleanupOldLogs(keep int) {
	dir := filepath.Dir(l.logPath)
	base := filepath.Base(l.logPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var rotatedFiles []string
	for _, entry := range entries {
		name := entry.Name()
		if name != base && strings.HasPrefix(name, base+".") {
			rotatedFiles = append(rotatedFiles, filepath.Join(dir, name))
		}
	}

	// Sort by name (which includes timestamp, so alphabetical = chronological)
	// Remove oldest if we have more than keep
	if len(rotatedFiles) > keep {
		for i := 0; i < len(rotatedFiles)-keep; i++ {
			os.Remove(rotatedFiles[i])
		}
	}
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// RequestLog is a structured middleware that logs every HTTP request.
// It records method, path, status code, latency, and consumer info.
func requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap ResponseWriter to capture status code
		sw := &statusWriter{ResponseWriter: w, status: 200}

		next.ServeHTTP(sw, r)

		latency := time.Since(start)
		consumer := getRequestOwner(r)
		if consumer == "" {
			consumer = "admin"
		}

		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"latency_ms", latency.Milliseconds(),
			"consumer", consumer,
			"remote", r.RemoteAddr,
			"user_agent", r.Header.Get("User-Agent"),
		)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.wroteHeader {
		sw.status = code
		sw.wroteHeader = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wroteHeader {
		sw.wroteHeader = true
	}
	return sw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for SSE/streaming support.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// FlushAccessLog ensures the access log file is flushed.
func FlushAccessLog() {
	if appLogger != nil && appLogger.accessFile != nil {
		appLogger.accessFile.Sync()
	}
}

// CloseAccessLog closes the access log file.
func CloseAccessLog() {
	if appLogger != nil && appLogger.accessFile != nil {
		appLogger.accessFile.Close()
	}
}

// Ensure fmt is used (for potential future formatting)
var _ = fmt.Sprintf
