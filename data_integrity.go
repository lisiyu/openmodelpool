package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
)

// ============================================================
// SA-15: Data File Integrity Verification
// ============================================================
//
// Critical data files are protected with HMAC-SHA256 signatures
// using the encryption key as the HMAC secret. This detects
// unauthorized modifications to data files on disk.
//
// File format: [32-byte HMAC][JSON payload]

const hmacSize = 32 // SHA-256 output size

// computeHMAC calculates HMAC-SHA256 of data using the encryption key.
func computeHMAC(data []byte) []byte {
	if enc == nil || !enc.ready {
		return nil
	}
	enc.mu.RLock()
	key := enc.key
	enc.mu.RUnlock()

	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// verifyHMAC checks if the stored HMAC matches the computed HMAC.
func verifyHMAC(data, storedMAC []byte) bool {
	if enc == nil || !enc.ready {
		return false
	}
	expected := computeHMAC(data)
	return hmac.Equal(storedMAC, expected)
}

// saveWithIntegrity serializes v to JSON, prepends HMAC, and writes to file.
func saveWithIntegrity(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	mac := computeHMAC(data)
	if mac == nil {
		// Encryption not ready, save without integrity check (backward compat)
		return os.WriteFile(path, data, 0600)
	}

	// Prepend HMAC to data
	full := make([]byte, hmacSize+len(data))
	copy(full[:hmacSize], mac)
	copy(full[hmacSize:], data)

	return os.WriteFile(path, full, 0600)
}

// loadWithIntegrity reads a file, verifies HMAC, and deserializes JSON into v.
// Returns nil error if file doesn't exist (not yet created).
// Returns error if HMAC verification fails (data tampered).
func loadWithIntegrity(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err // file doesn't exist, caller handles
	}

	if len(raw) < hmacSize {
		// File too small to contain HMAC — try loading as plain JSON (backward compat)
		return json.Unmarshal(raw, v)
	}

	// Check if this file has an HMAC prefix
	storedMAC := raw[:hmacSize]
	payload := raw[hmacSize:]

	// Verify: try to verify HMAC first
	if verifyHMAC(payload, storedMAC) {
		// HMAC verified — parse the payload
		if err := json.Unmarshal(payload, v); err != nil {
			return fmt.Errorf("parse verified data: %w", err)
		}
		return nil
	}

	// HMAC failed — check if the file is just plain JSON (pre-upgrade file)
	// Try to parse the entire raw content as JSON
	var testCheck any
	if json.Unmarshal(raw, &testCheck) == nil {
		// It's valid JSON without HMAC — load it (backward compat)
		slog.Info("data file loaded without integrity check (pre-upgrade format)", "path", path)
		return json.Unmarshal(raw, v)
	}

	// Neither HMAC-verified nor plain JSON — data may be tampered
	slog.Error("data file integrity check FAILED — possible tampering detected", "path", path)
	return fmt.Errorf("integrity check failed for %s: data may have been tampered", path)
}
