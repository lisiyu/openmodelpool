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

	// UX-P0-1: validate restart.sh BEFORE acknowledging success — a missing or
	// unsafe script previously produced a silent success:true with nothing
	// happening (confusing on Windows / bare-binary deployments).
	exePath, err := os.Executable()
	if err != nil {
		writeJSON(w, 200, map[string]any{
			"success": false,
			"message": "无法定位可执行文件，重启失败",
		})
		return
	}
	exeDir := filepath.Dir(exePath)
	scriptPath := filepath.Join(exeDir, "restart.sh")
	scriptPath = filepath.Clean(scriptPath)
	info, err := os.Stat(scriptPath)
	if err != nil || info.IsDir() {
		writeJSON(w, 200, map[string]any{
			"success": false,
			"message": "未找到 restart.sh，无法自动重启（请确认服务由脚本启动）",
		})
		return
	}
	// SEC-P2-11: refuse a group/world-writable restart script.
	if info.Mode().Perm()&0o022 != 0 {
		writeJSON(w, 200, map[string]any{
			"success": false,
			"message": "restart.sh 权限不安全（group/world 可写），已拒绝执行",
		})
		return
	}

	writeJSON(w, 200, map[string]any{"success": true, "message": "Service restarting..."})

	go func() {
		time.Sleep(500 * time.Millisecond)
		pid := os.Getpid()
		slog.Info("Initiating restart", "current_pid", pid)

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