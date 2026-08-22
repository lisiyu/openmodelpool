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
	data, err := marshalWithIntegrity(v)
	if err != nil {
		return err
	}
	return writeWithIntegrity(path, data)
}

// marshalWithIntegrity serializes v to JSON. B7-supp: lets callers snapshot
// state under a lock and defer the (slower) disk write until after unlock.
func marshalWithIntegrity(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// writeWithIntegrity prepends the HMAC for data and writes atomically to path.
func writeWithIntegrity(path string, data []byte) error {
	mac := computeHMAC(data)
	if mac == nil {
		// Encryption not ready, save without integrity check (backward compat)
		return atomicWriteFile(path, data, 0600)
	}

	// Prepend HMAC to data
	full := make([]byte, hmacSize+len(data))
	copy(full[:hmacSize], mac)
	copy(full[hmacSize:], data)

	return atomicWriteFile(path, full, 0600)
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

	// HMAC failed. Two cases remain, and they MUST be told apart:
	//
	//   1. Pre-upgrade / enc-not-ready file: written as plain JSON with NO
	//      HMAC prefix at all. The entire raw content parses as JSON, so we
	//      load it (backward compat). This is detected below by the whole
	//      file parsing as JSON — an HMAC-prefixed file never parses as JSON
	//      because its first 32 bytes are opaque binary.
	//
	//   2. The HMAC genuinely does not verify: the data was tampered with, or
	//      the encryption key changed. We MUST fail closed. Recovering the
	//      payload after the HMAC header and loading it without verification
	//      (the old behaviour) defeats the entire integrity guarantee (SA-15)
	//      and lets tampering pass silently. Key rotation is NOT a reason to
	//      bypass verification — a key change should be handled explicitly
	//      (operator re-saves), not by silently trusting unverified bytes.
	var testCheck any
	if json.Unmarshal(raw, &testCheck) == nil {
		// Whole file is valid JSON => no HMAC prefix => pre-upgrade format.
		slog.Info("data file loaded without integrity check (pre-upgrade format)", "path", path)
		return json.Unmarshal(raw, v)
	}

	// File carries an HMAC prefix but it did not verify, and the file is not
	// valid plain JSON. Treat as a failed integrity check.
	slog.Error("data file integrity check FAILED — possible tampering detected", "path", path)
	return fmt.Errorf("integrity check failed for %s: data may have been tampered", path)
}
