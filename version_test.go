package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleVersion verifies the public /api/version endpoint:
//   - returns HTTP 200 with no auth required
//   - response JSON contains "version" equal to the current AppVersion
//   - response JSON contains the Go runtime "go_version" field
//
// AppVersion is set to a known value first so the assertion is deterministic.
func TestHandleVersion(t *testing.T) {
	// Arrange: pin a deterministic version, then restore the original value.
	orig := AppVersion
	defer func() { AppVersion = orig }()
	AppVersion = "9.9.9-rectify"

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	w := httptest.NewRecorder()

	// Act
	handleVersion(w, req)

	// Assert: status code
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d (body=%q)", http.StatusOK, w.Code, w.Body.String())
	}

	// Assert: response is valid JSON with the expected fields
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v (body=%q)", err, w.Body.String())
	}

	if v, ok := body["version"]; !ok || v != AppVersion {
		t.Fatalf("expected version=%q, got %v (present=%v)", AppVersion, v, ok)
	}

	if _, ok := body["go_version"]; !ok {
		t.Fatalf("expected go_version field in response, got body=%v", body)
	}
}
