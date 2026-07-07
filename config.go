package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config manages persistent JSON config with env var fallback.
type Config struct {
	mu   sync.RWMutex
	data map[string]any
	path string
}

var cfg *Config

// envMap maps config keys to environment variable names.
var envMap = map[string]string{
	"coze_api_token": "COZE_API_TOKEN",
	"coze_bot_id":    "COZE_BOT_ID",
	"service_port":   "PORT",
}

func initConfig(path string) {
	cfg = &Config{path: path, data: make(map[string]any)}
	cfg.load()
}

func (c *Config) load() {
	c.mu.Lock()
	defer c.mu.Unlock()

	b, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	json.Unmarshal(b, &c.data)
	slog.Info("config loaded", "path", c.path, "keys", len(c.data))
}

func (c *Config) save() {
	os.MkdirAll(filepath.Dir(c.path), 0755)
	b, _ := json.MarshalIndent(c.data, "", "  ")
	os.WriteFile(c.path, b, 0644)
}

// Get returns config value: file > env > default.
func (c *Config) Get(key, def string) string {
	c.mu.RLock()
	if v, ok := c.data[key]; ok {
		if s, ok := v.(string); ok && s != "" {
			c.mu.RUnlock()
			return s
		}
	}
	c.mu.RUnlock()

	envKey := envMap[key]
	if envKey == "" {
		envKey = toUpper(key)
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return def
}

// Set updates a config key and persists.
func (c *Config) Set(key string, value any) {
	c.mu.Lock()
	c.data[key] = value
	c.data["updated_at"] = time.Now().Format(time.RFC3339)
	c.mu.Unlock()
	c.save()
}

// SetMany updates multiple keys at once.
func (c *Config) SetMany(m map[string]any) {
	c.mu.Lock()
	for k, v := range m {
		if v != nil && v != "" {
			c.data[k] = v
		}
	}
	c.data["updated_at"] = time.Now().Format(time.RFC3339)
	c.mu.Unlock()
	c.save()
}

// Masked returns config with sensitive fields masked.
func (c *Config) Masked() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]any, len(c.data))
	for k, v := range c.data {
		out[k] = v
	}
	// Mask tokens
	for _, key := range []string{"coze_api_token", "proxy_api_key"} {
		if tok, ok := out[key].(string); ok && tok != "" {
			out[key+"_masked"] = maskToken(tok)
			delete(out, key)
		}
	}
	return out
}

func maskToken(s string) string {
	if len(s) < 12 {
		return "***"
	}
	return s[:6] + "..." + s[len(s)-4:]
}

func toUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
