package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Logger provides structured logging with configurable levels and file output.
type Logger struct {
	accessFile *os.File
	logger     *slog.Logger
	level      slog.Level
}

var appLogger *Logger

// initLogger sets up the structured logging system.
// Logs go to both stdout and data/access.log.
func initLogger(dataDir string) {
	level := parseLogLevel(cfg.Get("log_level", "info"))

	// Ensure data directory exists
	os.MkdirAll(dataDir, 0755)

	logPath := filepath.Join(dataDir, "access.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Warn("failed to open access log file, using stdout only", "error", err)
		appLogger = &Logger{
			logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})),
			level:  level,
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
	}

	// Set as default logger
	slog.SetDefault(appLogger.logger)

	slog.Info("structured logger initialized", "level", level.String(), "log_file", logPath)
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
