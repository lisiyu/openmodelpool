package main

import (
	"log/slog"
	"testing"
)

// ============================================================
// parseLogLevel tests
// ============================================================

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"Debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"unknown", slog.LevelInfo},  // default to info
		{"", slog.LevelInfo},         // default to info
		{"verbose", slog.LevelInfo},  // default to info
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLogLevel(tt.input)
			if got != tt.expected {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// ============================================================
// maskIP tests
// ============================================================

func TestMaskIP_IPv4(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"ipv4 with port",
			"192.168.1.42:54321",
			"192.168.1.xxx",
		},
		{
			"ipv4 no port",
			"10.0.0.1",
			"10.0.0.xxx",
		},
		{
			"localhost",
			"127.0.0.1",
			"127.0.0.xxx",
		},
		{
			"localhost with port",
			"127.0.0.1:8080",
			"127.0.0.xxx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskIP(tt.input)
			if got != tt.expected {
				t.Errorf("maskIP(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestMaskIP_IPv6(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"ipv6 localhost with port",
			"[::1]:54321",
			"::1:xxx", // brackets stripped, last group masked
		},
		{
			"ipv6 full address",
			"2001:db8:85a3:0000:0000:8a2e:0370:7334",
			"2001:db8:85a3:xxx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskIP(tt.input)
			if got != tt.expected {
				t.Errorf("maskIP(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestMaskIP_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", "xxx"},
		{"single char", "a", "xxx"}, // no dots or colons → falls back to "xxx"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskIP(tt.input)
			if got != tt.expected {
				t.Errorf("maskIP(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
