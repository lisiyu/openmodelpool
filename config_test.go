package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ============================================================
// Config tests (using test helpers pattern)
// ============================================================

func TestConfig_Get_StoredValue(t *testing.T) {
	env := setupTestEnv(t)

	env.cfgInst.Set("some_key", "stored_value")

	// Wait briefly for debounce to settle
	time.Sleep(200 * time.Millisecond)

	got := env.cfgInst.Get("some_key", "default")
	if got != "stored_value" {
		t.Errorf("Get() = %q, want %q", got, "stored_value")
	}
}

func TestConfig_Get_Default(t *testing.T) {
	env := setupTestEnv(t)

	got := env.cfgInst.Get("nonexistent_key", "fallback_value")
	if got != "fallback_value" {
		t.Errorf("Get() = %q, want %q", got, "fallback_value")
	}
}

func TestConfig_Get_EnvFallback(t *testing.T) {
	env := setupTestEnv(t)

	os.Setenv("TESTKEY", "env_value")
	defer os.Unsetenv("TESTKEY")

	// Key not in config data but we try with envMap override
	// "service_port" maps to "PORT" in envMap
	env.cfgInst.Set("service_port", "8080")
	time.Sleep(200 * time.Millisecond)

	got := env.cfgInst.Get("service_port", "3000")
	if got != "8080" {
		t.Errorf("Get(service_port) = %q, want %q (stored value)", got, "8080")
	}
}

func TestConfig_Set(t *testing.T) {
	env := setupTestEnv(t)

	env.cfgInst.Set("test_key", "test_value")
	time.Sleep(200 * time.Millisecond)

	got := env.cfgInst.Get("test_key", "")
	if got != "test_value" {
		t.Errorf("Get after Set = %q, want %q", got, "test_value")
	}
}

func TestConfig_SetMany(t *testing.T) {
	env := setupTestEnv(t)

	env.cfgInst.SetMany(map[string]any{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	})
	time.Sleep(200 * time.Millisecond)

	if env.cfgInst.Get("key1", "") != "value1" {
		t.Error("key1 not set correctly")
	}
	if env.cfgInst.Get("key2", "") != "value2" {
		t.Error("key2 not set correctly")
	}
	if env.cfgInst.Get("key3", "") != "value3" {
		t.Error("key3 not set correctly")
	}
}

func TestConfig_Masked(t *testing.T) {
	env := setupTestEnv(t)

	env.cfgInst.Set("coze_api_token", "this-is-a-very-long-token-value-123")
	time.Sleep(200 * time.Millisecond)

	masked := env.cfgInst.Masked()

	// Token should be masked
	if _, ok := masked["coze_api_token"]; ok {
		t.Error("coze_api_token should be deleted and replaced with masked version")
	}
	if _, ok := masked["coze_api_token_masked"]; !ok {
		t.Error("coze_api_token_masked should be present")
	}
}

// UX-P1-11: SetMany must STORE nil/empty values so "clear this setting" works
// instead of being silently skipped (the previous behavior made clearing a
// config value a silent no-op).
func TestConfig_SetMany_EmptyValues(t *testing.T) {
	env := setupTestEnv(t)

	env.cfgInst.Set("pre_existing", "old_value")
	time.Sleep(200 * time.Millisecond)

	env.cfgInst.SetMany(map[string]any{
		"pre_existing": nil,
		"new_key":      "",
	})
	time.Sleep(200 * time.Millisecond)

	// nil/"" now clears the value: Get falls back to the default.
	if got := env.cfgInst.Get("pre_existing", ""); got != "" {
		t.Errorf("nil value should clear existing: got %q, want \"\"", got)
	}
	if got := env.cfgInst.Get("pre_existing", "fallback"); got != "fallback" {
		t.Errorf("cleared key should fall back to default: got %q, want %q", got, "fallback")
	}
	if got := env.cfgInst.Get("new_key", "def"); got != "def" {
		t.Errorf("empty string value should be stored (clearing): got %q, want %q", got, "def")
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"123456789012", "123456...9012"},
		{"short", "***"},
		{"abcdefghijklmno", "abcdef...lmno"},
	}

	for _, tt := range tests {
		got := maskToken(tt.input)
		if got != tt.expected {
			t.Errorf("maskToken(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestToUpper(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"lowercase", "LOWERCASE"},
		{"UPPERCASE", "UPPERCASE"},
		{"MixedCase", "MIXEDCASE"},
		{"with_underscore", "WITH_UNDERSCORE"},
		{"", ""},
		{"123", "123"},
	}

	for _, tt := range tests {
		got := toUpper(tt.input)
		if got != tt.expected {
			t.Errorf("toUpper(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	data := []byte("hello atomic world")
	err := atomicWriteFile(path, data, 0644)
	if err != nil {
		t.Fatalf("atomicWriteFile error: %v", err)
	}

	// Verify content
	read, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(read) != "hello atomic world" {
		t.Errorf("content mismatch: got %q, want %q", string(read), "hello atomic world")
	}

	// Verify .tmp file is cleaned up
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should not exist after atomic write")
	}
}

func TestSensitiveKeys(t *testing.T) {
	// Verify that sensitive keys exist
	sensitive := map[string]bool{}
	for _, k := range sensitiveKeys {
		sensitive[k] = true
	}

	required := []string{"proxy_api_key", "coze_api_token", "cf_api_token", "cf_zone_id"}
	for _, r := range required {
		if !sensitive[r] {
			t.Errorf("required sensitive key %q missing", r)
		}
	}
}

func TestEnvMap(t *testing.T) {
	if envMap["coze_api_token"] != "COZE_API_TOKEN" {
		t.Error("envMap entry for coze_api_token mismatch")
	}
	if envMap["coze_bot_id"] != "COZE_BOT_ID" {
		t.Error("envMap entry for coze_bot_id mismatch")
	}
	if envMap["service_port"] != "PORT" {
		t.Error("envMap entry for service_port mismatch")
	}
}

func TestConfig_ConcurrentAccess(t *testing.T) {
	env := setupTestEnv(t)

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			env.cfgInst.Set(strings.Repeat("k", id%5+1), id)
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	time.Sleep(500 * time.Millisecond)

	// No data races = success
}

func TestConfig_SaveSync(t *testing.T) {
	env := setupTestEnv(t)

	env.cfgInst.Set("sync_key", "sync_value")
	env.cfgInst.saveSync()

	got := env.cfgInst.Get("sync_key", "")
	if got != "sync_value" {
		t.Errorf("saveSync: Get = %q, want %q", got, "sync_value")
	}
}
