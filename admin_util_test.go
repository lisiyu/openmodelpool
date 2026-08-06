package main

import (
	"testing"
)

// ============================================================
// maskKey tests
// ============================================================

func TestMaskKey_Short(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"", "***"},
		{"a", "***"},
		{"12345678", "***"},
	}
	for _, tt := range tests {
		if got := maskKey(tt.key); got != tt.want {
			t.Errorf("maskKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestMaskKey_Long(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"123456789", "1234***6789"},
		{"sk-1234567890123456", "sk-1***3456"},
		{"abcdefghij", "abcd***ghij"},
	}
	for _, tt := range tests {
		if got := maskKey(tt.key); got != tt.want {
			t.Errorf("maskKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

// ============================================================
// mapKeys tests
// ============================================================

func TestMapKeys_Empty(t *testing.T) {
	keys := mapKeys(map[string]string{})
	if len(keys) != 0 {
		t.Errorf("mapKeys of empty map should return empty slice, got %d", len(keys))
	}
}

func TestMapKeys_Single(t *testing.T) {
	keys := mapKeys(map[string]string{"foo": "bar"})
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0] != "foo" {
		t.Errorf("expected key 'foo', got %q", keys[0])
	}
}

func TestMapKeys_Multiple(t *testing.T) {
	m := map[string]string{"a": "1", "b": "2", "c": "3"}
	keys := mapKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	// Verify all keys are present (order is random for maps).
	found := make(map[string]bool)
	for _, k := range keys {
		found[k] = true
	}
	for k := range m {
		if !found[k] {
			t.Errorf("key %q missing from result", k)
		}
	}
}

func TestMapKeys_Nil(t *testing.T) {
	keys := mapKeys(nil)
	if len(keys) != 0 {
		t.Errorf("mapKeys(nil) should return empty slice, got %d", len(keys))
	}
}
