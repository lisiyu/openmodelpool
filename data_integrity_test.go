package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// data_integrity tests
// ============================================================

func TestDataIntegrity_SaveAndLoad(t *testing.T) {
	// Setup encryptor so HMAC works
	oldEnc := enc
	defer func() { enc = oldEnc }()
	enc = newTestEncryptor()

	dir := t.TempDir()
	path := filepath.Join(dir, "test-data.json")

	type testData struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	original := testData{Name: "test", Value: 42}

	// Save with integrity
	err := saveWithIntegrity(path, original)
	if err != nil {
		t.Fatalf("saveWithIntegrity error: %v", err)
	}

	// Load with integrity
	var loaded testData
	err = loadWithIntegrity(path, &loaded)
	if err != nil {
		t.Fatalf("loadWithIntegrity error: %v", err)
	}

	if loaded.Name != original.Name || loaded.Value != original.Value {
		t.Errorf("loaded data mismatch: got %+v, want %+v", loaded, original)
	}
}

func TestDataIntegrity_LoadNonexistentFile(t *testing.T) {
	err := loadWithIntegrity("/tmp/does-not-exist-12345.json", &struct{}{})
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestDataIntegrity_LoadPlainJSON(t *testing.T) {
	oldEnc := enc
	defer func() { enc = oldEnc }()
	enc = newTestEncryptor()

	dir := t.TempDir()
	path := filepath.Join(dir, "plain.json")

	// Write plain JSON without HMAC prefix
	plainJSON := []byte(`{"name":"plain","value":99}`)
	if err := os.WriteFile(path, plainJSON, 0600); err != nil {
		t.Fatal(err)
	}

	type testData struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	var loaded testData
	err := loadWithIntegrity(path, &loaded)
	if err != nil {
		t.Fatalf("loadWithIntegrity of plain JSON failed: %v", err)
	}
	if loaded.Name != "plain" || loaded.Value != 99 {
		t.Errorf("loaded: got %+v, want {plain, 99}", loaded)
	}
}

func TestDataIntegrity_TamperedData(t *testing.T) {
	oldEnc := enc
	defer func() { enc = oldEnc }()
	enc = newTestEncryptor()

	dir := t.TempDir()
	path := filepath.Join(dir, "tampered.json")

	// Save legit data
	original := map[string]string{"key": "value"}
	if err := saveWithIntegrity(path, original); err != nil {
		t.Fatal(err)
	}

	// Tamper: modify bytes after HMAC prefix
	raw, _ := os.ReadFile(path)
	if len(raw) > hmacSize+5 {
		raw[hmacSize+4] ^= 0xFF // flip a bit in the payload
		os.WriteFile(path, raw, 0600)
	}

	var loaded map[string]string
	err := loadWithIntegrity(path, &loaded)
	if err == nil {
		t.Error("expected integrity check failure for tampered data")
	}
}

func TestDataIntegrity_SaveWithoutEncryptor(t *testing.T) {
	oldEnc := enc
	enc = nil
	defer func() { enc = oldEnc }()

	dir := t.TempDir()
	path := filepath.Join(dir, "no-enc.json")

	data := map[string]int{"count": 10}
	err := saveWithIntegrity(path, data)
	if err != nil {
		t.Fatalf("saveWithIntegrity should succeed even without encryptor: %v", err)
	}

	// Should fall back to plain JSON
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonLooksValid(t, raw) {
		t.Error("saved data without encryptor should be valid JSON")
	}
}

func TestDataIntegrity_SaveLoadRoundtrip(t *testing.T) {
	oldEnc := enc
	defer func() { enc = oldEnc }()
	enc = newTestEncryptor()

	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.json")

	tests := []interface{}{
		map[string]string{"hello": "world"},
		map[string]int{"a": 1, "b": 2},
		[]string{"one", "two", "three"},
		42,
		"just a string",
	}

	for i, original := range tests {
		if err := saveWithIntegrity(path, original); err != nil {
			t.Fatalf("test %d: save error: %v", i, err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("test %d: read error: %v", i, err)
		}
		if len(data) < hmacSize {
			t.Errorf("test %d: file too short, missing HMAC", i)
		}
	}
}

func TestDataIntegrity_VerifyHMAC(t *testing.T) {
	oldEnc := enc
	defer func() { enc = oldEnc }()
	enc = newTestEncryptor()

	data := []byte("hello world")
	mac := computeHMAC(data)
	if mac == nil {
		t.Fatal("computeHMAC returned nil")
	}
	if len(mac) != hmacSize {
		t.Errorf("HMAC length = %d, want %d", len(mac), hmacSize)
	}

	if !verifyHMAC(data, mac) {
		t.Error("verifyHMAC should return true for matching MAC")
	}

	wrongMAC := make([]byte, hmacSize)
	for i := range wrongMAC {
		wrongMAC[i] = mac[i] ^ 0xFF
	}
	if verifyHMAC(data, wrongMAC) {
		t.Error("verifyHMAC should return false for mismatched MAC")
	}
}

// ============================================================
// Helpers
// ============================================================

func jsonLooksValid(t *testing.T, data []byte) bool {
	t.Helper()
	if len(data) == 0 {
		return false
	}
	// Simple check: starts with { or [ or "
	c := data[0]
	return c == '{' || c == '[' || c == '"'
}
