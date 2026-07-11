package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// VMessConfig represents a parsed vmess:// link
type VMessConfig struct {
	Add  string `json:"add"`  // server address
	Port string `json:"port"` // server port
	ID   string `json:"id"`   // UUID
	Aid  string `json:"aid"`  // alterId
	Net  string `json:"net"`  // network (tcp, ws, grpc, etc.)
	Type string `json:"type"` // header type (none, http, etc.)
	TLS  string `json:"tls"`  // tls setting ("tls" or "")
	SNI  string `json:"sni"`  // SNI for TLS
	PS   string `json:"ps"`   // remark/name
	Host string `json:"host"` // websocket host
	Path string `json:"path"` // websocket path
}

// VMessProxy manages a local Xray instance for a VMess proxy
type VMessProxy struct {
	mu        sync.Mutex
	proxies   map[string]*vmessInstance // key: provider ID
	xrayPath  string
	basePort  int // starting port for SOCKS5 proxies
	nextPort  int
}

type vmessInstance struct {
	cmd      *exec.Cmd
	port     int       // local SOCKS5 proxy port
	provider string    // provider ID
	config   VMessConfig
}

var vmessManager *VMessProxy

func initVMessManager(dataDir string) {
	xrayPath := filepath.Join(dataDir, "..", "xray", "xray")
	if _, err := os.Stat(xrayPath); err != nil {
		// Try relative to working directory
		xrayPath = "xray/xray"
		if _, err := os.Stat(xrayPath); err != nil {
			slog.Warn("xray binary not found, VMess proxy disabled")
			xrayPath = ""
		}
	}
	vmessManager = &VMessProxy{
		proxies:  make(map[string]*vmessInstance),
		xrayPath: xrayPath,
		basePort: 20800,
		nextPort: 20800,
	}
}

// ParseVMessLink parses a vmess:// link
func ParseVMessLink(link string) (*VMessConfig, error) {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "vmess://") {
		return nil, fmt.Errorf("invalid vmess link: must start with vmess://")
	}
	b64 := strings.TrimPrefix(link, "vmess://")
	// Try standard encoding first, then URL-safe
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			decoded, err = base64.URLEncoding.DecodeString(b64)
			if err != nil {
				decoded, err = base64.RawURLEncoding.DecodeString(b64)
				if err != nil {
					return nil, fmt.Errorf("invalid base64 in vmess link: %w", err)
				}
			}
		}
	}
	var config VMessConfig
	if err := json.Unmarshal(decoded, &config); err != nil {
		return nil, fmt.Errorf("invalid vmess JSON: %w", err)
	}
	if config.Add == "" || config.ID == "" || config.Port == "" {
		return nil, fmt.Errorf("vmess link missing required fields (add/id/port)")
	}
	return &config, nil
}

// StartProxy starts a local Xray SOCKS5 proxy for the given VMess config
func (m *VMessProxy) StartProxy(providerID string, config *VMessConfig) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.xrayPath == "" {
		return "", fmt.Errorf("xray binary not found")
	}

	// Stop existing proxy for this provider
	if inst, ok := m.proxies[providerID]; ok {
		m.stopInstance(inst)
	}

	// Allocate a port
	port := m.nextPort
	m.nextPort++

	// Generate Xray config
	xrayConfig := m.generateConfig(config, port)
	configFile := filepath.Join(os.TempDir(), fmt.Sprintf("xray-%s.json", providerID))
	b, _ := json.MarshalIndent(xrayConfig, "", "  ")
	os.WriteFile(configFile, b, 0644)

	// Start Xray
	cmd := exec.Command(m.xrayPath, "run", "-c", configFile)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start xray: %w", err)
	}

	inst := &vmessInstance{
		cmd:      cmd,
		port:     port,
		provider: providerID,
		config:   *config,
	}
	m.proxies[providerID] = inst

	proxyAddr := fmt.Sprintf("socks5://127.0.0.1:%d", port)
	slog.Info("VMess proxy started", "provider", providerID, "proxy", proxyAddr, "server", config.Add)

	// Wait a moment for Xray to start
	time.Sleep(500 * time.Millisecond)

	// Verify it's running
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		delete(m.proxies, providerID)
		return "", fmt.Errorf("xray exited immediately, check config")
	}

	return proxyAddr, nil
}

// StopProxy stops the Xray proxy for a provider
func (m *VMessProxy) StopProxy(providerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if inst, ok := m.proxies[providerID]; ok {
		m.stopInstance(inst)
		delete(m.proxies, providerID)
		slog.Info("VMess proxy stopped", "provider", providerID)
	}
}

// StopAll stops all running Xray proxies
func (m *VMessProxy) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, inst := range m.proxies {
		m.stopInstance(inst)
		delete(m.proxies, id)
	}
}

func (m *VMessProxy) stopInstance(inst *vmessInstance) {
	if inst.cmd != nil && inst.cmd.Process != nil {
		inst.cmd.Process.Kill()
		inst.cmd.Wait()
	}
	// Clean up config file
	configFile := filepath.Join(os.TempDir(), fmt.Sprintf("xray-%s.json", inst.provider))
	os.Remove(configFile)
}

func (m *VMessProxy) generateConfig(vmess *VMessConfig, localPort int) map[string]any {
	port, _ := strconv.Atoi(vmess.Port)
	alterID, _ := strconv.Atoi(vmess.Aid)
	if alterID == 0 {
		alterID = 0 // AEAD mode
	}

	// Build stream settings
	streamSettings := map[string]any{
		"network": vmess.Net,
	}
	if vmess.Net == "" {
		streamSettings["network"] = "tcp"
	}

	// Security (TLS)
	if vmess.TLS == "tls" {
		streamSettings["security"] = "tls"
		tlsSettings := map[string]any{}
		if vmess.SNI != "" {
			tlsSettings["serverName"] = vmess.SNI
		} else {
			tlsSettings["serverName"] = vmess.Add
		}
		streamSettings["tlsSettings"] = tlsSettings
	} else {
		streamSettings["security"] = "none"
	}

	// WebSocket settings
	if vmess.Net == "ws" {
		wsSettings := map[string]any{}
		if vmess.Path != "" {
			wsSettings["path"] = vmess.Path
		}
		if vmess.Host != "" {
			wsSettings["headers"] = map[string]any{"Host": vmess.Host}
		}
		streamSettings["wsSettings"] = wsSettings
	}

	// TCP header type
	if vmess.Net == "tcp" && vmess.Type == "http" {
		streamSettings["tcpSettings"] = map[string]any{
			"header": map[string]any{
				"type": "http",
				"request": map[string]any{
					"path": []string{vmess.Path},
				},
			},
		}
	}

	// gRPC settings
	if vmess.Net == "grpc" {
		grpcSettings := map[string]any{}
		if vmess.Path != "" {
			grpcSettings["serviceName"] = vmess.Path
		}
		streamSettings["grpcSettings"] = grpcSettings
	}

	config := map[string]any{
		"log": map[string]any{
			"loglevel": "warning",
		},
		"inbounds": []map[string]any{
			{
				"port":     localPort,
				"protocol": "socks",
				"settings": map[string]any{
					"auth":      "noauth",
					"udp":       true,
				},
			},
		},
		"outbounds": []map[string]any{
			{
				"protocol": "vmess",
				"settings": map[string]any{
					"vnext": []map[string]any{
						{
							"address": vmess.Add,
							"port":    port,
							"users": []map[string]any{
								{
									"id":       vmess.ID,
									"alterId":  alterID,
									"security": "auto",
								},
							},
						},
					},
				},
				"streamSettings": streamSettings,
			},
			{
				"protocol": "freedom",
				"tag":      "direct",
			},
		},
	}
	return config
}

// ResolveProxy resolves a proxy string to an actual proxy URL.
// If proxy starts with "vmess://", starts an Xray instance and returns socks5://localhost:port.
// If proxy is already http:// or socks5://, returns as-is.
// If proxy is empty, returns empty.
func ResolveProxy(providerID, proxy string) (string, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return "", nil
	}

	// Already a standard proxy scheme
	if strings.HasPrefix(proxy, "http://") || strings.HasPrefix(proxy, "https://") || strings.HasPrefix(proxy, "socks5://") || strings.HasPrefix(proxy, "socks5h://") {
		return proxy, nil
	}

	// VMess link
	if strings.HasPrefix(proxy, "vmess://") {
		config, err := ParseVMessLink(proxy)
		if err != nil {
			return "", fmt.Errorf("invalid vmess link: %w", err)
		}
		if vmessManager == nil {
			return "", fmt.Errorf("VMess proxy manager not initialized")
		}
		return vmessManager.StartProxy(providerID, config)
	}

	return "", fmt.Errorf("unsupported proxy scheme: %s", proxy)
}

// StopProviderProxy stops any VMess proxy for a provider
func StopProviderProxy(providerID string) {
	if vmessManager != nil {
		vmessManager.StopProxy(providerID)
	}
}
