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
	mu       sync.RWMutex
	data     map[string]any
	path     string
	dirty    bool
	dirtyCh  chan struct{}
	stopCh   chan struct{}
}

var cfg *Config

// envMap maps config keys to environment variable names.
var envMap = map[string]string{
	"coze_api_token": "COZE_API_TOKEN",
	"coze_bot_id":    "COZE_BOT_ID",
	"service_port":   "PORT",
}

func initConfig(path string) {
	cfg = &Config{
		path:    path,
		data:    make(map[string]any),
		dirtyCh: make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
	}
	cfg.load()
	go cfg.debounceWriter()
}

func (c *Config) debounceWriter() {
	for {
		select {
		case <-c.dirtyCh:
			time.Sleep(3 * time.Second)
			// Drain any additional signals during sleep
			for len(c.dirtyCh) > 0 {
				<-c.dirtyCh
			}
			c.mu.Lock()
			if c.dirty {
				c.doSave()
				c.dirty = false
			}
			c.mu.Unlock()
		case <-c.stopCh:
			// Final flush on shutdown
			c.mu.Lock()
			if c.dirty {
				c.doSave()
				c.dirty = false
			}
			c.mu.Unlock()
			return
		}
	}
}

// sensitiveKeys lists config keys that must be encrypted at rest.
var sensitiveKeys = []string{"proxy_api_key", "coze_api_token", "cf_api_token", "cf_zone_id"}

func (c *Config) load() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Use loadWithIntegrity to handle HMAC-prefixed files
	if err := loadWithIntegrity(c.path, &c.data); err != nil {
		// Fallback: try plain JSON (pre-upgrade or corrupted file)
		b, ferr := os.ReadFile(c.path)
		if ferr == nil {
			json.Unmarshal(b, &c.data)
		}
	}
	// Decrypt sensitive fields
	for _, key := range sensitiveKeys {
		if v, ok := c.data[key].(string); ok && v != "" && IsEncrypted(v) {
			c.data[key] = decryptField(v)
		}
	}
	slog.Info("config loaded", "path", c.path, "keys", len(c.data))
}

func (c *Config) save() {
	c.mu.Lock()
	c.dirty = true
	c.mu.Unlock()
	// Signal debounce writer
	select {
	case c.dirtyCh <- struct{}{}:
	default:
	}
}

// saveSync forces synchronous save (used during shutdown).
func (c *Config) saveSync() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.doSave()
	c.dirty = false
}

func (c *Config) doSave() {
	os.MkdirAll(filepath.Dir(c.path), 0755)
	safe := make(map[string]any, len(c.data))
	for k, v := range c.data {
		safe[k] = v
	}
	for _, key := range sensitiveKeys {
		if v, ok := safe[key].(string); ok && v != "" && !IsEncrypted(v) {
			safe[key] = encryptField(v)
		}
	}
	// SA-15: Save with HMAC integrity protection
	if err := saveWithIntegrity(c.path, safe); err != nil {
		slog.Error("failed to save config with integrity", "error", err)
	}
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

func (c *Config) stop() { close(c.stopCh) }

func toUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// atomicWriteFile writes data to a file atomically by first writing to a temp
// file and then renaming. This prevents data corruption from partial writes.
// P-1: Used by all save() methods across the project.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
