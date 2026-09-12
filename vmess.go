package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// VMessConfig represents a parsed vmess:// link (or vless:// link — see the
// VLESS-only fields below; both share the same xray instance machinery).
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

	// VLESS-only fields (populated by ParseVLESSLink from URI query params;
	// ignored by the vmess base64-JSON path thanks to omitempty / "-").
	IsVLESS  bool   `json:"-"`                  // outbound protocol switch in generateConfig
	Flow     string `json:"flow,omitempty"`     // e.g. xtls-rprx-vision
	Security string `json:"security,omitempty"` // stream security: reality | tls | none
	PBK      string `json:"pbk,omitempty"`      // REALITY public key
	SID      string `json:"sid,omitempty"`      // REALITY short id
	FP       string `json:"fp,omitempty"`       // uTLS fingerprint (e.g. chrome)
}

// VMessProxy manages a local Xray instance for a VMess proxy
type VMessProxy struct {
	mu       sync.Mutex
	proxies  map[string]*vmessInstance // key: provider ID
	xrayPath string
	basePort int // starting port for SOCKS5 proxies
	nextPort int
}

type vmessInstance struct {
	cmd      *exec.Cmd
	port     int    // local SOCKS5 proxy port
	provider string // provider ID
	config   VMessConfig
}

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
	// Auto-fix padding: strip existing padding then re-add correct amount
	b64Clean := strings.TrimRight(b64, "=")
	switch len(b64Clean) % 4 {
	case 2:
		b64Clean += "=="
	case 3:
		b64Clean += "="
	}
	// Try standard encoding first, then URL-safe
	decoded, err := base64.StdEncoding.DecodeString(b64Clean)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(b64Clean)
		if err != nil {
			decoded, err = base64.URLEncoding.DecodeString(b64Clean)
			if err != nil {
				decoded, err = base64.RawURLEncoding.DecodeString(b64Clean)
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
	// B10-P1: cached — ResolveProxy runs per request on the hot path.
	if cachedIsPrivateHost(net.JoinHostPort(config.Add, config.Port)) {
		return nil, fmt.Errorf("vmess address resolves to private/loopback IP: %s", config.Add)
	}
	return &config, nil
}

// ParseVLESSLink parses a vless:// URI link (B10-WL6). Standard share-link
// format:
//
//	vless://uuid@host:port?encryption=none&flow=xtls-rprx-vision&security=reality
//	  &sni=example.com&fp=chrome&pbk=...&sid=...&type=tcp#remark
//
// Xray supports VLESS natively, so we reuse the same local-SOCKS instance
// machinery as vmess — only the outbound block differs (see generateConfig).
func ParseVLESSLink(link string) (*VMessConfig, error) {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "vless://") {
		return nil, fmt.Errorf("invalid vless link: must start with vless://")
	}
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("invalid vless URI: %w", err)
	}
	uuid := u.User.Username()
	if uuid == "" {
		return nil, fmt.Errorf("vless link missing UUID")
	}
	host := u.Hostname()
	portStr := u.Port()
	if host == "" || portStr == "" {
		return nil, fmt.Errorf("vless link missing host/port")
	}
	if p, err := strconv.Atoi(portStr); err != nil || p <= 0 || p > 65535 {
		return nil, fmt.Errorf("vless link invalid port %q", portStr)
	}
	q := u.Query()

	cfg := &VMessConfig{
		Add:      host,
		Port:     portStr,
		ID:       uuid,
		Net:      q.Get("type"),
		SNI:      q.Get("sni"),
		Host:     q.Get("host"),
		Path:     q.Get("path"),
		PS:       u.Fragment,
		IsVLESS:  true,
		Flow:     q.Get("flow"),
		Security: q.Get("security"),
		PBK:      q.Get("pbk"),
		SID:      q.Get("sid"),
		FP:       q.Get("fp"),
	}
	if cfg.Net == "" {
		cfg.Net = "tcp"
	}
	switch cfg.Security {
	case "reality":
		if cfg.PBK == "" {
			return nil, fmt.Errorf("vless reality link missing pbk (public key)")
		}
	case "tls", "":
		cfg.Security = "tls"
		cfg.TLS = "tls"
	case "none":
		cfg.TLS = ""
	default:
		return nil, fmt.Errorf("unsupported vless security %q", cfg.Security)
	}
	if enc := q.Get("encryption"); enc != "" && enc != "none" {
		return nil, fmt.Errorf("unsupported vless encryption %q", enc)
	}
	// B10-P1: cached — same private-IP guard as vmess links.
	if cachedIsPrivateHost(net.JoinHostPort(cfg.Add, cfg.Port)) {
		return nil, fmt.Errorf("vless address resolves to private/loopback IP: %s", cfg.Add)
	}
	return cfg, nil
}

// StartProxy starts a local Xray SOCKS5 proxy for the given VMess config
func (m *VMessProxy) StartProxy(providerID string, config *VMessConfig) (string, error) {
	m.mu.Lock()
	if m.xrayPath == "" {
		m.mu.Unlock()
		return "", fmt.Errorf("xray binary not found")
	}

	if inst, ok := m.proxies[providerID]; ok {
		if inst.cmd.ProcessState == nil || !inst.cmd.ProcessState.Exited() {
			proxyAddr := fmt.Sprintf("socks5://127.0.0.1:%d", inst.port)
			m.mu.Unlock()
			return proxyAddr, nil
		}
		m.stopInstance(inst)
	}

	port := m.nextPort
	m.nextPort++
	m.mu.Unlock()

	xrayConfig := m.generateConfig(config, port)
	sanitized := filepath.Base(providerID)
	if sanitized != providerID || sanitized == "." || sanitized == ".." {
		return "", fmt.Errorf("invalid provider ID: %q", providerID)
	}
	configFile := "data/xray-" + sanitized + ".json"
	b, _ := json.MarshalIndent(xrayConfig, "", "  ")
	if err := atomicWriteFile(configFile, b, 0600); err != nil {
		return "", fmt.Errorf("failed to write xray config: %w", err)
	}

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

	proxyAddr := fmt.Sprintf("socks5://127.0.0.1:%d", port)

	// B10-WL2: wait until the SOCKS port actually accepts connections — the
	// old fixed 500ms sleep raced xray's bind on slow hosts.
	socksAddr := fmt.Sprintf("127.0.0.1:%d", port)
	listening := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", socksAddr, 500*time.Millisecond); err == nil {
			c.Close()
			listening = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	exited := func() bool { return cmd.ProcessState != nil && cmd.ProcessState.Exited() }
	if !listening || exited() {
		m.mu.Lock()
		m.stopInstance(inst)
		delete(m.proxies, providerID)
		m.mu.Unlock()
		return "", fmt.Errorf("xray 未能在本地端口监听，请检查配置")
	}

	// B10-WL2b: verify the tunnel actually passes traffic. A dead VMess node
	// keeps the xray process alive but silently drops every connection — the
	// built-in browser then showed "This site can't be reached" for ALL sites
	// (its DNS is forced through the proxy), which looked like "代理没有生效".
	// Fail fast here with an actionable message instead.
	if err := checkSocksEgress(socksAddr); err != nil {
		m.mu.Lock()
		m.stopInstance(inst)
		delete(m.proxies, providerID)
		m.mu.Unlock()
		slog.Warn("VMess tunnel egress check failed", "provider", providerID, "error", err)
		return "", fmt.Errorf("VMess 隧道无法连通（节点可能失效），请更新代理链接后重试")
	}

	m.mu.Lock()
	m.proxies[providerID] = inst
	m.mu.Unlock()

	slog.Info("VMess proxy started (egress verified)", "provider", providerID, "proxy", proxyAddr, "server", config.Add)

	return proxyAddr, nil
}

// checkSocksEgress dials an HTTP probe target THROUGH the given SOCKS5 addr to
// prove the tunnel forwards traffic end-to-end. Any HTTP response counts as
// success; transport errors on ALL probe targets mean the tunnel is dead.
// B10-WL7: multiple targets — a single blocked probe host (e.g. Google
// services unreachable from some exits) must not mark a healthy tunnel dead.
func checkSocksEgress(socksAddr string) error {
	dialer, err := socksProxyDialer(socksAddr)
	if err != nil {
		return fmt.Errorf("socks dialer: %w", err)
	}
	ctxDialer, ok := dialer.(interface {
		DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	})
	if !ok {
		return fmt.Errorf("dialer does not support context")
	}
	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			DialContext: ctxDialer.DialContext,
		},
	}
	var lastErr error
	for _, target := range egressProbeURLs() {
		resp, err := client.Get(target)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return nil
	}
	return lastErr
}

// egressProbeURLs returns the connectivity probe targets in priority order
// (var for tests).
var egressProbeURLs = func() []string {
	return []string{
		"https://www.gstatic.com/generate_204",
		"https://cp.cloudflare.com/generate_204",
	}
}

// socksProxyDialer builds a SOCKS5 dialer for the given host:port (var-wrapped
// so tests can inject failures without a real proxy).
var socksProxyDialer = func(socksAddr string) (proxy.Dialer, error) {
	return proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
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
	port, err := strconv.Atoi(vmess.Port)
	if err != nil || port <= 0 || port > 65535 { // B2: validate port range
		port = 443 // default to HTTPS
	}
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

	// Security (TLS / REALITY)
	if vmess.Security == "reality" {
		// B10-WL6: VLESS+REALITY — xray realitySettings; serverName falls back
		// to the dial address like the TLS branch below.
		streamSettings["security"] = "reality"
		realitySettings := map[string]any{
			"show": false,
		}
		if vmess.SNI != "" {
			realitySettings["serverName"] = vmess.SNI
		} else {
			realitySettings["serverName"] = vmess.Add
		}
		if vmess.FP != "" {
			realitySettings["fingerprint"] = vmess.FP
		} else {
			realitySettings["fingerprint"] = "chrome"
		}
		realitySettings["publicKey"] = vmess.PBK
		realitySettings["shortId"] = vmess.SID
		streamSettings["realitySettings"] = realitySettings
	} else if vmess.TLS == "tls" {
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

	// Outbound: vmess or vless (B10-WL6)
	var outbound map[string]any
	if vmess.IsVLESS {
		user := map[string]any{
			"id":         vmess.ID,
			"encryption": "none",
		}
		if vmess.Flow != "" {
			user["flow"] = vmess.Flow
		}
		outbound = map[string]any{
			"protocol": "vless",
			"settings": map[string]any{
				"vnext": []map[string]any{
					{
						"address": vmess.Add,
						"port":    port,
						"users":   []map[string]any{user},
					},
				},
			},
			"streamSettings": streamSettings,
		}
	} else {
		outbound = map[string]any{
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
		}
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
					"auth": "noauth",
					"udp":  true,
				},
			},
		},
		"outbounds": []map[string]any{
			outbound,
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

	// B10-WL6: VLESS link — same xray instance machinery, different outbound.
	if strings.HasPrefix(proxy, "vless://") {
		config, err := ParseVLESSLink(proxy)
		if err != nil {
			return "", fmt.Errorf("invalid vless link: %w", err)
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
