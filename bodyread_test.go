package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Normal body is read fully and quickly.
func TestReadRequestBodyOK(t *testing.T) {
	body := `{"model":"gpt-6.1-sol","messages":[]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	got, err := readRequestBody(w, r, 10<<20)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(got) != body {
		t.Fatalf("body mismatch: %q", got)
	}
}

// Oversized body is capped at maxBytes+1 (caller maps to 413).
func TestReadRequestBodyCap(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 100)))
	w := httptest.NewRecorder()
	got, err := readRequestBody(w, r, 10)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 11 {
		t.Fatalf("expected capped 11 bytes, got %d", len(got))
	}
}

// Stalled body fails fast with errBodyReadTimeout (not the 30s server ReadTimeout).
func TestReadRequestBodyTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", pr)
	r.ContentLength = 100 // promise bytes we never send
	w := httptest.NewRecorder()
	start := time.Now()
	_, err := readRequestBody(w, r, 10<<20)
	el := time.Since(start)
	pw.Close()
	if !isBodyReadTimeout(err) {
		t.Fatalf("expected timeout err, got: %v", err)
	}
	if el > 25*time.Second {
		t.Fatalf("too slow: %v (must beat the 30s server ReadTimeout)", el)
	}
	t.Logf("failed fast in %v", el)
}

// Eager 100-continue: interim response is emitted before body read.
func TestReadRequestBodyExpectContinue(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	r.Header.Set("Expect", "100-continue")
	w := httptest.NewRecorder()
	_, err := readRequestBody(w, r, 10<<20)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// httptest.ResponseRecorder captures the first WriteHeader call, which is
	// the interim 100 — proving it was emitted eagerly before body read.
	// A real net/http server forwards it as interim and the final response
	// follows normally.
	if w.Code != http.StatusContinue {
		t.Fatalf("expected eager 100-continue, recorder code=%d", w.Code)
	}
}
