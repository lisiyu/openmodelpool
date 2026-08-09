package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSplitGeminiModelSuffix(t *testing.T) {
	cases := []struct {
		raw, model, suffix string
	}{
		{"gemini-1.5-flash:generateContent", "gemini-1.5-flash", "generateContent"},
		{"gemini-1.5-pro:streamGenerateContent", "gemini-1.5-pro", "streamGenerateContent"},
		{"gemini-x", "gemini-x", ""},
	}
	for _, c := range cases {
		m, s := splitGeminiModelSuffix(c.raw)
		if m != c.model || s != c.suffix {
			t.Fatalf("splitGeminiModelSuffix(%q) = (%q,%q), want (%q,%q)", c.raw, m, s, c.model, c.suffix)
		}
	}
}

func TestExtractGeminiText(t *testing.T) {
	parts := []geminiChatPart{
		{Text: "hello"},
		{Text: "world"},
		{InlineData: &geminiChatInlineData{MIMEType: "image/png", Data: "abc"}},
	}
	if got := extractGeminiText(parts); got != "hello\nworld" {
		t.Fatalf("extractGeminiText = %q, want %q", got, "hello\nworld")
	}
}

func TestConvertGeminiFinish(t *testing.T) {
	if convertGeminiFinish("stop") != "STOP" {
		t.Fatal("stop -> STOP")
	}
	if convertGeminiFinish("length") != "MAX_TOKENS" {
		t.Fatal("length -> MAX_TOKENS")
	}
	if convertGeminiFinish("tool_calls") != "STOP" {
		t.Fatal("tool_calls -> STOP")
	}
	if convertGeminiFinish("unknown") != "STOP" {
		t.Fatal("unknown -> STOP")
	}
}

func TestGeminiAuthAdapter(t *testing.T) {
	cases := []struct {
		name    string
		setHdr  func(r *http.Request)
		want    string
	}{
		{
			name: "x-goog-api-key header",
			setHdr: func(r *http.Request) {
				r.Header.Set("x-goog-api-key", "gkey")
			},
			want: "Bearer gkey",
		},
		{
			name: "key query param (stripped from query)",
			setHdr: func(r *http.Request) {
				// query is set via URL; handled below
			},
			want: "Bearer qkey",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			h := geminiAuthAdapter(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				if c.name == "key query param (stripped from query)" {
					if r.URL.Query().Get("key") != "" {
						t.Fatalf("key should have been stripped from query")
					}
				}
			})
			var req *http.Request
			if c.name == "key query param (stripped from query)" {
				req = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-x:generateContent?key=qkey", nil)
			} else {
				req = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-x:generateContent", nil)
				c.setHdr(req)
			}
			rec := httptest.NewRecorder()
			h(rec, req)
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestGeminiResponseWriter_NonStream(t *testing.T) {
	openai := `{"id":"c1","object":"chat.completion","created":1,"model":"gemini-x","choices":[{"index":0,"message":{"role":"assistant","content":"hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`

	rec := httptest.NewRecorder()
	w := &geminiResponseWriter{realWriter: rec, header: http.Header{}, statusCode: 200, model: "gemini-x"}
	w.WriteHeader(200)
	w.Write([]byte(openai))
	w.finalize()

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Candidates []struct {
			Content      map[string]any `json:"content"`
			FinishReason string         `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid gemini response: %v, body=%s", err, rec.Body.String())
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(resp.Candidates))
	}
	parts, _ := resp.Candidates[0].Content["parts"].([]any)
	if parts[0].(map[string]any)["text"] != "hello world" {
		t.Fatalf("text mismatch: %v", resp.Candidates[0].Content)
	}
	if resp.Candidates[0].FinishReason != "STOP" {
		t.Fatalf("finishReason = %q", resp.Candidates[0].FinishReason)
	}
	if resp.UsageMetadata.PromptTokenCount != 5 || resp.UsageMetadata.CandidatesTokenCount != 3 || resp.UsageMetadata.TotalTokenCount != 8 {
		t.Fatalf("usage mismatch: %+v", resp.UsageMetadata)
	}
}

func TestGeminiResponseWriter_Stream(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	rec := httptest.NewRecorder()
	w := &geminiResponseWriter{realWriter: rec, header: http.Header{}, statusCode: 200, model: "gemini-x"}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	w.Write([]byte(sse))
	w.finalize()

	body := rec.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Fatalf("expected gemini SSE output, got %q", body)
	}
	// Each OpenAI chunk with content should yield a Gemini data event.
	events := strings.Count(body, "data: {")
	if events < 3 {
		t.Fatalf("expected >=3 gemini data events, got %d (body=%s)", events, body)
	}
	if !strings.Contains(body, `"finishReason":"STOP"`) {
		t.Fatalf("expected final finishReason STOP, body=%s", body)
	}
}
