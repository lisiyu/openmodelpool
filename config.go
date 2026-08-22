package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// configDebounceWindow is how long the writer coalesces rapid config writes
// before flushing to disk. The wait is interruptible by stopCh so shutdown is
// never delayed by a full window.
const configDebounceWindow = 3 * time.Second

// configDebounceOverride, when non-zero, replaces configDebounceWindow. It exists
// so tests can shrink the coalescing window: with the production value every test
// that writes config would otherwise wait out a full 3s window on teardown.
// Production code never sets this.
var configDebounceOverride time.Duration

// debounceWindow returns the effective coalescing window for this Config.
func (c *Config) debounceWindow() time.Duration {
	if configDebounceOverride > 0 {
		return configDebounceOverride
	}
	return configDebounceWindow
}

// Config manages persistent JSON config with env var fallback.
type Config struct {
	mu      sync.RWMutex
	data    map[string]any
	path    string
	dirty   bool
	dirtyCh chan struct{}
	stopCh  chan struct{}
	done    chan struct{} // closed when debounceWriter exits
}

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
		done:    make(chan struct{}),
	}
	cfg.load()
	go cfg.debounceWriter()
}

func (c *Config) debounceWriter() {
	defer close(c.done)
	for {
		select {
		case <-c.dirtyCh:
			// Debounce window: coalesce rapid writes into one save. The wait
			// must stay interruptible so shutdown (and tests) are not blocked
			// for the full window; stopCh falls through to the final flush.
			timer := time.NewTimer(c.debounceWindow())
			select {
			case <-timer.C:
			case <-c.stopCh:
				timer.Stop()
				c.mu.Lock()
				if c.dirty {
					c.doSave()
					c.dirty = false
				}
				c.mu.Unlock()
				return
			}
			// Drain any additional signals accumulated during the window
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
	path := c.path
	c.mu.Unlock()

	if err := loadWithIntegrity(path, &c.data); err != nil {
		b, ferr := os.ReadFile(path)
		if ferr == nil {
			func() {
				c.mu.Lock()
				defer c.mu.Unlock()
				// Try parsing as plain JSON first
				if uerr := json.Unmarshal(b, &c.data); uerr != nil {
					// Plain JSON failed — file may have a binary HMAC header (32 bytes).
					// Try to find the first '{' and parse from there.
					start := -1
					for i, ch := range b {
						if ch == '{' {
							start = i
							break
						}
					}
					if start > 0 {
						if uerr2 := json.Unmarshal(b[start:], &c.data); uerr2 == nil {
							slog.Warn("config loaded by skipping binary header (HMAC/encryption prefix)",
								"path", path, "skipped_bytes", start)
						} else {
							slog.Error("failed to parse config data, using defaults", "error", uerr2)
							c.data = make(map[string]any)
						}
					} else {
						slog.Error("failed to parse config data, using defaults", "error", uerr)
						c.data = make(map[string]any)
					}
				}
			}()
		}
	}
	c.mu.Lock()
	for _, key := range sensitiveKeys {
		if v, ok := c.data[key].(string); ok && v != "" && IsEncrypted(v) {
			c.data[key] = decryptField(v)
		}
	}
	c.mu.Unlock()
	slog.Info("config loaded", "path", path, "keys", len(c.data))
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
// B5-note: safe map is a shallow copy; encryptField only replaces string values
// (immutable in Go), so in-memory c.data is not mutated — unlike auth.go which
// needed deepCopyDataLocked due to struct field assignment.
func (c *Config) saveSync() {
	c.mu.Lock()
	safe := make(map[string]any, len(c.data))
	for k, v := range c.data {
		safe[k] = v
	}
	for _, key := range sensitiveKeys {
		if v, ok := safe[key].(string); ok && v != "" && !IsEncrypted(v) {
			safe[key] = encryptField(v)
		}
	}
	c.dirty = false
	c.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		slog.Error("failed to create config directory", "error", err)
	}
	if err := saveWithIntegrity(c.path, safe); err != nil {
		slog.Error("failed to save config with integrity", "error", err)
	}
}

func (c *Config) doSave() {
	if err := os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		slog.Error("failed to create config directory", "error", err)
		return
	}
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

// validateConfigValue reports whether a config value can be safely persisted
// to the JSON config file (UX-P1-11). Functions, channels, complex numbers and
// arbitrary structs would corrupt the file on save.
func validateConfigValue(value any) bool {
	switch value.(type) {
	case string, bool, float64, float32, int, int64, int32, uint, uint64, uint32, uintptr, []any, []string, map[string]any, nil:
		return true
	default:
		return false
	}
}

// Set updates a config key and persists (UX-P1-11: rejects unserializable
// values so the config file can never be corrupted).
func (c *Config) Set(key string, value any) {
	if !validateConfigValue(value) {
		slog.Warn("config Set rejected unserializable value", "key", key)
		return
	}
	c.mu.Lock()
	c.data[key] = value
	c.data["updated_at"] = time.Now().Format(time.RFC3339)
	c.mu.Unlock()
	c.save()
}

// SetMany updates multiple keys at once (UX-P1-11: values are validated; nil
// and empty-string values are STORED so "clear this setting" works instead of
// being silently skipped; unserializable values are rejected with a warning).
func (c *Config) SetMany(m map[string]any) {
	c.mu.Lock()
	for k, v := range m {
		if k == "" {
			continue
		}
		if !validateConfigValue(v) {
			slog.Warn("config SetMany rejected unserializable value", "key", k)
			continue
		}
		c.data[k] = v
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
	return strings.ToUpper(s)
}

// atomicWriteFile writes data to a file atomically by first writing to a temp
// file and then renaming. This prevents data corruption from partial writes.
// P-1: Used by all save() methods across the project.
// B7-8: the temp file is fsynced before the rename (without it, a power loss
// could persist the rename metadata before the data blocks, leaving an empty
// or stale file) and carries a pid+random suffix so two concurrent savers of
// the same path cannot clobber each other's temp file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := fmt.Sprintf("%s.tmp.%d.%s", path, os.Getpid(), randomString(8))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// B7-8: Windows can transiently fail rename-replace while another handle
	// (AV scanner, concurrent closer) holds the destination; production Linux
	// is immune but retry keeps local dev and mixed hosts reliable.
	var rerr error
	for i := 0; i < 5; i++ {
		rerr = os.Rename(tmp, path)
		if rerr == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 10 * time.Millisecond)
	}
	os.Remove(tmp)
	return rerr
}
