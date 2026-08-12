package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchChecksum_InvalidFormat verifies a malformed checksum body is
// rejected (fail-closed guard before any binary replacement).
func TestFetchChecksum_InvalidFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-a-valid-checksum"))
	}))
	defer srv.Close()

	um := &UpdateManager{}
	if _, err := um.fetchChecksum(srv.URL); err == nil {
		t.Fatal("expected error on invalid checksum format")
	}
}

// TestFetchChecksum_OK verifies a valid <hash>  <file> checksum body parses.
func TestFetchChecksum_OK(t *testing.T) {
	hash := strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hash + "  omp-binary"))
	}))
	defer srv.Close()

	um := &UpdateManager{}
	got, err := um.fetchChecksum(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != hash {
		t.Fatalf("got %q, want %q", got, hash)
	}
}

// TestFetchSignature_NotFound verifies a missing .sig asset is a hard fail-closed
// error (v4.4.44: no signature -> abort update).
func TestFetchSignature_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	um := &UpdateManager{}
	_, err := um.fetchSignature(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got %v", err)
	}
}

// TestFetchSignature_WrongSize verifies a non-64-byte signature is rejected,
// preventing a truncated/edited signature from passing verification.
func TestFetchSignature_WrongSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("short")) // not 64 raw bytes
	}))
	defer srv.Close()

	um := &UpdateManager{}
	_, err := um.fetchSignature(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "invalid signature size") {
		t.Fatalf("expected size error, got %v", err)
	}
}

// TestFetchSignature_OK verifies a 64-byte raw Ed25519 signature decodes.
func TestFetchSignature_OK(t *testing.T) {
	sig := make([]byte, 64)
	for i := range sig {
		sig[i] = byte(i)
	}
	enc := base64.StdEncoding.EncodeToString(sig)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(enc))
	}))
	defer srv.Close()

	um := &UpdateManager{}
	got, err := um.fetchSignature(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != ed25519.SignatureSize {
		t.Fatalf("got %d bytes, want %d", len(got), ed25519.SignatureSize)
	}
}
