package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// TunnelManager manages the Cloudflare Tunnel lifecycle.
type TunnelManager struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	url      string
	running  bool
	mode     string // "quick" or "named"
	domain   string // custom domain for named mode
	port     int
}

var tunnel *TunnelManager

// initTunnel creates the tunnel manager and starts the tunnel if enabled.
func initTunnel(port int) {
	tunnel = &TunnelManager{port: port}
	
	enabled := cfg.Get("tunnel_enabled", "false") == "true"
	mode := cfg.Get("tunnel_mode", "quick")
	domain := cfg.Get("tunnel_domain", "")
	
	tunnel.mode = mode
	tunnel.domain = domain
	
	if enabled {
		go tunnel.start()
	}
}

// start begins the cloudflared tunnel process.
func (t *TunnelManager) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.running {
		slog.Warn("tunnel already running")
		return
	}
	
	// Check if cloudflared is available
	if _, err := exec.LookPath("cloudflared"); err != nil {
		slog.Error("cloudflared not found in PATH", "error", err)
		return
	}
	
	// Quick tunnel mode (default) - no account required
	// For named tunnels with custom domains, users need to set up cloudflared manually
	args := []string{"tunnel", "--url", fmt.Sprintf("http://localhost:%d", t.port)}
	slog.Info("starting quick tunnel", "port", t.port)
	
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "cloudflared", args...)
	
	// Capture stdout to extract the tunnel URL
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		slog.Error("failed to create stdout pipe", "error", err)
		cancel()
		return
	}
	
	// Also capture stderr (cloudflared outputs to stderr)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		slog.Error("failed to create stderr pipe", "error", err)
		cancel()
		return
	}
	
	if err := cmd.Start(); err != nil {
		slog.Error("failed to start cloudflared", "error", err)
		cancel()
		return
	}
	
	t.cmd = cmd
	t.cancel = cancel
	t.running = true
	
	// URL pattern: https://xxx-xxx-xxx.trycloudflare.com
	urlPattern := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	
	// Scan stdout and stderr for the tunnel URL
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if matches := urlPattern.FindString(line); matches != "" {
				t.mu.Lock()
				t.url = matches
				t.mu.Unlock()
				cfg.Set("tunnel_url", matches)
				slog.Info("tunnel URL detected", "url", matches)
			}
		}
	}()
	
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if matches := urlPattern.FindString(line); matches != "" {
				t.mu.Lock()
				t.url = matches
				t.mu.Unlock()
				cfg.Set("tunnel_url", matches)
				slog.Info("tunnel URL detected", "url", matches)
			}
		}
	}()
	
	// Wait for the process to exit
	go func() {
		err := cmd.Wait()
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()
		if err != nil {
			slog.Warn("cloudflared exited", "error", err)
		} else {
			slog.Info("cloudflared exited normally")
		}
	}()
	
	// Wait a bit for URL to be detected
	time.Sleep(5 * time.Second)
}

// stop terminates the cloudflared tunnel process.
func (t *TunnelManager) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if !t.running {
		return
	}
	
	if t.cancel != nil {
		t.cancel()
	}
	
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}
	
	t.running = false
	t.url = ""
	cfg.Set("tunnel_url", "")
	slog.Info("tunnel stopped")
}

// restart stops and restarts the tunnel with new config.
func (t *TunnelManager) restart() {
	t.stop()
	time.Sleep(500 * time.Millisecond)
	
	t.mode = cfg.Get("tunnel_mode", "quick")
	t.domain = cfg.Get("tunnel_domain", "")
	
	enabled := cfg.Get("tunnel_enabled", "false") == "true"
	if enabled {
		go t.start()
	}
}

// GetURL returns the current tunnel URL.
func (t *TunnelManager) GetURL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

// IsRunning reports whether the tunnel is currently running.
func (t *TunnelManager) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// handleTunnelStatus returns the current tunnel status.
func handleTunnelStatus() map[string]any {
	if tunnel == nil {
		return map[string]any{
			"running": false,
			"url":     "",
		}
	}
	return map[string]any{
		"running": tunnel.IsRunning(),
		"url":     tunnel.GetURL(),
	}
}

// applyTunnelConfig is called after saving federation config to apply tunnel changes.
func applyTunnelConfig() {
	if tunnel == nil {
		return
	}
	
	enabled := cfg.Get("tunnel_enabled", "false") == "true"
	
	if !enabled && tunnel.IsRunning() {
		slog.Info("tunnel disabled via config, stopping")
		go tunnel.stop()
	} else if enabled && !tunnel.IsRunning() {
		slog.Info("tunnel enabled via config, starting")
		go tunnel.restart()
	} else if enabled && tunnel.IsRunning() {
		// Check if mode or domain changed
		newMode := cfg.Get("tunnel_mode", "quick")
		newDomain := cfg.Get("tunnel_domain", "")
		
		tunnel.mu.Lock()
		modeChanged := tunnel.mode != newMode
		domainChanged := tunnel.domain != newDomain
		tunnel.mu.Unlock()
		
		if modeChanged || domainChanged {
			slog.Info("tunnel config changed, restarting", "mode", newMode, "domain", newDomain)
			go tunnel.restart()
		}
	}
}

// formatTunnelStatus returns a human-readable tunnel status string.
func formatTunnelStatus() string {
	if tunnel == nil || !tunnel.IsRunning() {
		return "未运行"
	}
	url := tunnel.GetURL()
	if url == "" {
		return "运行中（等待分配地址...）"
	}
	return fmt.Sprintf("运行中 → %s", url)
}

// extractSubdomain extracts the subdomain from a tunnel URL.
func extractSubdomain(url string) string {
	// https://xxx-xxx-xxx.trycloudflare.com -> xxx-xxx-xxx
	parts := strings.Split(url, "://")
	if len(parts) != 2 {
		return ""
	}
	host := strings.Split(parts[1], "/")[0]
	subdomain := strings.Split(host, ".")[0]
	return subdomain
}
