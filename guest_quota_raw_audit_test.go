package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================
// v4.5.59: guest per-key quota on the raw passthrough endpoints
// (/v1/responses, /v1/images/generations, /v1/audio/speech). These never go
// through handleChatCompletions, so D-4 used to be bypassed for them.
// ============================================================

// TestRawGuestEstimateTokens locks down the token estimate used to charge a
// guest key on raw passthrough calls: text-derived tokens (chars/4) plus any
// explicit output-token bound set by the client, floored at 1.
func TestRawGuestEstimateTokens(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
		want int64
	}{
		{"empty body", "/v1/audio/speech", "", 1},
		{"invalid json", "/v1/images/generations", "not-json", 1},
		{"numbers only", "/v1/responses", `{"temperature":0.7,"stream":true}`, 1},
		{"audio input", "/v1/audio/speech", `{"model":"tts-1","input":"hello world"}`, 5},
		{"image prompt", "/v1/images/generations", `{"model":"dall-e-3","prompt":"a cat"}`, 4},
		{"responses with output bound", "/v1/responses", `{"model":"gpt-5","input":"hello","max_output_tokens":120}`, 123},
		{"responses array input", "/v1/responses", `{"model":"gpt-5","input":[{"role":"user","content":"hi"}],"max_output_tokens":50}`, 53},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := estimateRawGuestTokens(c.path, []byte(c.body)); got != c.want {
				t.Fatalf("estimateRawGuestTokens(%q) = %d, want %d", c.body, got, c.want)
			}
		})
	}
}

// TestGuestQuota_RawImageDenied is the regression guard for the raw endpoints:
// a guest key whose remaining daily quota cannot cover the estimate must get a
// 429 from the passthrough path just like the chat path, instead of the
// request sailing through uncharged.
func TestGuestQuota_RawImageDenied(t *testing.T) {
	env := setupTestEnv(t)

	netMgr = &NetworkManager{config: NetworkConfig{NodeID: "mmx-raw-denied", Mode: NetworkModeShared}}
	initGuestKeyStore(env.dir)

	key := "sk-guest-mmx-raw-denied-" + strings.Repeat("c", 32)
	guestKeyStore.keys = append(guestKeyStore.keys, &GuestKeyRecord{
		Key: key, NodeID: "mmx-raw-denied", RandomPart: strings.Repeat("c", 32), Quota: 30,
	})
	initGuestKeyUsageTracker()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"created":1,"data":[]}`))
	}))
	defer srv.Close()

	pm.providers["p-raw-img"] = Provider{
		ID: "p-raw-img", Name: "RawImg", Type: "openai_compatible",
		BaseURL: srv.URL, APIKey: "sk-provider-secret", Enabled: true,
		AccessControl: ProviderAccessControl{ShareToPool: true},
		Models:        []ModelDef{{ID: "dall-e-3", Enabled: true}},
	}

	// Prompt long enough that the estimate (len/4+1) exceeds the 30t daily quota.
	prompt := strings.Repeat("a", 200)
	body := `{"model":"dall-e-3","size":"1024x1024","prompt":"` + prompt + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()

	withProxyAuth(handleGatewayRequest)(w, req)

	if w.Code != 429 {
		t.Fatalf("guest quota 30 / est>30 expected 429 on /v1/images/generations, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "额度") {
		t.Errorf("expected a quota-denial message, got %s", w.Body.String())
	}
	if got := guestKeyUsage.GetUsage(key); got != 0 {
		t.Fatalf("denied request must not consume quota, usage=%d", got)
	}
}

// TestGuestQuota_RawAudioAllowedAndCounted proves the positive path: an
// unlimited-enough guest can complete a raw /v1/audio/speech request, and the
// reservation (est) is retained as consumption because a raw forward reports no
// structured usage to settle against.
func TestGuestQuota_RawAudioAllowedAndCounted(t *testing.T) {
	env := setupTestEnv(t)

	netMgr = &NetworkManager{config: NetworkConfig{NodeID: "mmx-raw-audio", Mode: NetworkModeShared}}
	initGuestKeyStore(env.dir)

	key := "sk-guest-mmx-raw-audio-" + strings.Repeat("d", 32)
	guestKeyStore.keys = append(guestKeyStore.keys, &GuestKeyRecord{
		Key: key, NodeID: "mmx-raw-audio", RandomPart: strings.Repeat("d", 32), Quota: 100,
	})
	initGuestKeyUsageTracker()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("MP3BYTES"))
	}))
	defer srv.Close()

	pm.providers["p-raw-tts"] = Provider{
		ID: "p-raw-tts", Name: "RawTTS", Type: "openai_compatible",
		BaseURL: srv.URL, APIKey: "sk-provider-secret", Enabled: true,
		AccessControl: ProviderAccessControl{ShareToPool: true},
		Models:        []ModelDef{{ID: "tts-1", Enabled: true}},
	}

	body := `{"model":"tts-1","voice":"alloy","input":"hello world"}` // est = 6
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		withProxyAuth(handleGatewayRequest)(w, req)
		if w.Code != 200 {
			t.Fatalf("request %d expected 200 with remaining quota, got %d (body=%s)", i+1, w.Code, w.Body.String())
		}
	}

	if got := guestKeyUsage.GetUsage(key); got != 12 {
		t.Fatalf("two audio calls of est 6 expected usage 12, got %d", got)
	}
}

// TestGuestQuota_RawAudioRPM proves the RPM dimension is enforced on the raw
// passthrough endpoints too: a key with rpm=1 must be throttled on the second
// request within the same minute window.
func TestGuestQuota_RawAudioRPM(t *testing.T) {
	env := setupTestEnv(t)

	netMgr = &NetworkManager{config: NetworkConfig{NodeID: "mmx-raw-rpm", Mode: NetworkModeShared}}
	initGuestKeyStore(env.dir)

	key := "sk-guest-mmx-raw-rpm-" + strings.Repeat("e", 32)
	guestKeyStore.keys = append(guestKeyStore.keys, &GuestKeyRecord{
		Key: key, NodeID: "mmx-raw-rpm", RandomPart: strings.Repeat("e", 32), RPM: 1,
	})
	initGuestKeyUsageTracker()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("MP3BYTES"))
	}))
	defer srv.Close()

	pm.providers["p-raw-rpm"] = Provider{
		ID: "p-raw-rpm", Name: "RawRPM", Type: "openai_compatible",
		BaseURL: srv.URL, APIKey: "sk-provider-secret", Enabled: true,
		AccessControl: ProviderAccessControl{ShareToPool: true},
		Models:        []ModelDef{{ID: "tts-1", Enabled: true}},
	}

	body := `{"model":"tts-1","input":"hello"}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		withProxyAuth(handleGatewayRequest)(w, req)
		if i == 0 {
			if w.Code != 200 {
				t.Fatalf("first request expected 200, got %d (body=%s)", w.Code, w.Body.String())
			}
		} else {
			if w.Code != 429 {
				t.Fatalf("second request with rpm=1 expected 429, got %d (body=%s)", w.Code, w.Body.String())
			}
		}
	}
}
