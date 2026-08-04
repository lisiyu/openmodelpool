package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ============================================================

// ============================================================
// Service Restart
// ============================================================

func handleRestart(w http.ResponseWriter, r *http.Request) {
	slog.Info("Restart requested via admin API")
	writeJSON(w, 200, map[string]any{"success": true, "message": "Service restarting..."})

	go func() {
		time.Sleep(500 * time.Millisecond)
		pid := os.Getpid()
		slog.Info("Initiating restart", "current_pid", pid)

		exePath, err := os.Executable()
		if err != nil {
			slog.Error("Failed to get executable path for restart", "error", err)
			return
		}
		exeDir := filepath.Dir(exePath)
		scriptPath := filepath.Join(exeDir, "restart.sh")
		scriptPath = filepath.Clean(scriptPath)
		info, err := os.Stat(scriptPath)
		if err != nil || info.IsDir() {
			slog.Error("restart.sh not found or not a file", "path", scriptPath, "error", err)
			return
		}
		cmd := exec.Command(scriptPath, fmt.Sprintf("%d", pid))
		cmd.Dir = exeDir
		if err := cmd.Start(); err != nil {
			slog.Error("failed to start restart script", "path", scriptPath, "error", err)
			return
		}

		// Current process will be killed by the script
	}()
}

// handleRefreshToken accepts a refresh_token and returns a new access_token.
// S-3: JWT refresh token flow.
func handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	if body.RefreshToken == "" {
		writeError(w, 400, "refresh_token is required")
		return
	}
	newAccessToken, err := auth.RefreshAccessToken(body.RefreshToken)
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "invalid or expired refresh token"})
		return
	}
	writeJSON(w, 200, map[string]string{
		"access_token": newAccessToken,
		"token_type":   "bearer",
	})
}