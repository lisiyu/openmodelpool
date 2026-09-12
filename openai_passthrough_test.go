package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================
// passthroughSubPath Tests
// ============================================================

func TestPassthroughSubPath(t *testing.T) {
	cases := map[string]string{
		"/v1/responses":          "/responses",
		"/v1/images/generations": "/images/generations",
		"/v1/audio/speech":       "/audio/speech",
		"/v1/chat/completions":   "",
		"/v1/completions":        "",
		"/v1/embeddings":         "",
		"/api/version":           "",
	}
	for in, want := range cases {
		if got := passthroughSubPath(in); got != want {
			t.Fatalf("passthroughSubPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleRawPassthrough_NonPassthroughReturnsFalse(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	if handleRawPassthrough(httptest.NewRecorder(), req, []byte(`{}`), "some-model") {
		t.Fatal("handleRawPassthrough must return false for non-passthrough path")
	}
}

// ============================================================
// Verbatim passthrough tests against a local fake upstream
// ============================================================

func TestHandleRawPassthrough_ResponsesVerbatim(t *testing.T) {
	oldAllow := allowLocalProviderForTest
	allowLocalProviderForTest = true
	defer func() { allowLocalProviderForTest = oldAllow }()

	var gotPath, gotAuth, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"resp-1","object":"response"}`))
	}))
	defer upstream.Close()

	setupTestEnv(t)
	pm.Add(Provider{
		ID:      "passthrough-responses",
		Name:    "PT Responses",
		Type:    "openai_compatible",
		BaseURL: upstream.URL,
		APIKey:  "sk-upstream",
		Enabled: true,
		Models:  []ModelDef{{ID: "gpt-5", Enabled: true}},
	})

	body := `{"model":"gpt-5","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")

	rec := httptest.NewRecorder()
	if !handleRawPassthrough(rec, req, []byte(body), "gpt-5") {
		t.Fatal("handleRawPassthrough returned false for /v1/responses")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"id":"resp-1","object":"response"}` {
		t.Fatalf("response not verbatim: %q", rec.Body.String())
	}
	if gotPath != "/responses" {
		t.Fatalf("upstream path = %q, want /responses", gotPath)
	}
	if gotAuth != "Bearer sk-upstream" {
		t.Fatalf("upstream auth = %q, want Bearer sk-upstream", gotAuth)
	}
	if strings.TrimSpace(gotBody) != `{"model":"gpt-5","input":"hello"}` {
		t.Fatalf("upstream body not verbatim: %q", gotBody)
	}
}

func TestHandleRawPassthrough_AudioBinary(t *testing.T) {
	oldAllow := allowLocalProviderForTest
	allowLocalProviderForTest = true
	defer func() { allowLocalProviderForTest = oldAllow }()

	audio := []byte{0xFF, 0xF3, 0x22, 0xC4} // MPEG frame header bytes
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		w.Write(audio)
	}))
	defer upstream.Close()

	setupTestEnv(t)
	pm.Add(Provider{
		ID:      "passthrough-audio",
		Name:    "PT Audio",
		Type:    "openai_compatible",
		BaseURL: upstream.URL,
		APIKey:  "sk-upstream",
		Enabled: true,
		Models:  []ModelDef{{ID: "tts-1", Enabled: true}},
	})

	body := `{"model":"tts-1","input":"你好","voice":"alloy"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")

	rec := httptest.NewRecorder()
	if !handleRawPassthrough(rec, req, []byte(body), "tts-1") {
		t.Fatal("handleRawPassthrough returned false for /v1/audio/speech")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "audio/mpeg") {
		t.Fatalf("Content-Type = %q, want audio/mpeg", rec.Header().Get("Content-Type"))
	}
	if len(rec.Body.Bytes()) != len(audio) {
		t.Fatalf("audio body len = %d, want %d", len(rec.Body.Bytes()), len(audio))
	}
	for i := range audio {
		if rec.Body.Bytes()[i] != audio[i] {
			t.Fatalf("audio byte %d = %x, want %x", i, rec.Body.Bytes()[i], audio[i])
		}
	}
}

func TestHandleRawPassthrough_ResponsesStream(t *testing.T) {
	oldAllow := allowLocalProviderForTest
	allowLocalProviderForTest = true
	defer func() { allowLocalProviderForTest = oldAllow }()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"response.output_text.delta\"}\n\n"))
	}))
	defer upstream.Close()

	setupTestEnv(t)
	pm.Add(Provider{
		ID:      "passthrough-stream",
		Name:    "PT Stream",
		Type:    "openai_compatible",
		BaseURL: upstream.URL,
		APIKey:  "sk-upstream",
		Enabled: true,
		Models:  []ModelDef{{ID: "gpt-5", Enabled: true}},
	})

	body := `{"model":"gpt-5","input":"hello","stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-test")

	rec := httptest.NewRecorder()
	if !handleRawPassthrough(rec, req, []byte(body), "gpt-5") {
		t.Fatal("handleRawPassthrough returned false for streaming /v1/responses")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "response.output_text.delta") {
		t.Fatalf("SSE body not verbatim: %q", rec.Body.String())
	}
}

func TestHandleRawPassthrough_NoProvider(t *testing.T) {
	setupTestEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"nope"}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	rec := httptest.NewRecorder()
	if !handleRawPassthrough(rec, req, []byte(`{"model":"nope"}`), "nope") {
		t.Fatal("handleRawPassthrough returned false even though path matches")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
