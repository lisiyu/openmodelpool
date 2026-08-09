package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ============================================================
// Google Gemini downstream compatibility layer
// Accepts the Gemini Generative AI native format so users can point the
// official Google AI / Vertex SDK at OMP by setting the base URL to
//   <omp-host>/v1beta/models/
// Endpoints:
//   POST /v1beta/models/{model}:generateContent
//   POST /v1beta/models/{model}:streamGenerateContent   (?alt=sse)
// The request is translated to OpenAI chat format, routed through the existing
// gateway, and the OpenAI response is translated back to Gemini format.
//
// NOTE: type names use the "geminiChat" prefix to avoid colliding with the
// Gemini *upstream* adapter types in platform_adapter.go.
// ============================================================

// geminiAuthAdapter accepts the Gemini SDK's "x-goog-api-key" header and the
// "?key=" query parameter, normalizing them to "Authorization: Bearer".
func geminiAuthAdapter(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if k := r.Header.Get("x-goog-api-key"); k != "" {
				r.Header.Set("Authorization", "Bearer "+k)
			} else if qk := r.URL.Query().Get("key"); qk != "" {
				r.Header.Set("Authorization", "Bearer "+qk)
				// Strip the token from the query so it is not echoed downstream.
				q := r.URL.Query()
				q.Del("key")
				r.URL.RawQuery = q.Encode()
			}
		}
		handler(w, r)
	}
}

// ---- Gemini inbound request types ----

type geminiChatRequest struct {
	Contents          []geminiChatContent `json:"contents"`
	SystemInstruction *geminiChatContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  geminiChatGenConfig `json:"generationConfig,omitempty"`
	Stream            bool                `json:"stream,omitempty"`
}

type geminiChatContent struct {
	Role  string           `json:"role"`
	Parts []geminiChatPart `json:"parts"`
}

type geminiChatPart struct {
	Text       string                `json:"text,omitempty"`
	InlineData *geminiChatInlineData `json:"inlineData,omitempty"`
}

type geminiChatInlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiChatGenConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
	CandidateCount  *int     `json:"candidateCount,omitempty"`
}

// handleGeminiGenerateContent handles POST /v1beta/models/{model}:generateContent
// and POST /v1beta/models/{model}:streamGenerateContent.
func handleGeminiGenerateContent(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("model") // e.g. "gemini-1.5-flash:generateContent"
	if raw == "" {
		writeGeminiError(w, 400, "invalid model path")
		return
	}
	model, suffix := splitGeminiModelSuffix(raw)
	if model == "" {
		writeGeminiError(w, 400, "invalid model path")
		return
	}
	stream := strings.HasSuffix(suffix, "streamGenerateContent")

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxGatewayBodySize+1))
	if err != nil {
		writeGeminiError(w, 400, "failed to read request body")
		return
	}
	if len(bodyBytes) > maxGatewayBodySize {
		writeGeminiError(w, 413, "request body too large")
		return
	}
	r.Body.Close()

	var gr geminiChatRequest
	if err := json.Unmarshal(bodyBytes, &gr); err != nil {
		writeGeminiError(w, 400, "invalid request body: "+err.Error())
		return
	}

	// Build OpenAI messages.
	var oaMessages []ChatMessage
	if gr.SystemInstruction != nil {
		if text := extractGeminiText(gr.SystemInstruction.Parts); text != "" {
			oaMessages = append(oaMessages, ChatMessage{Role: "system", Content: text})
		}
	}
	for _, c := range gr.Contents {
		role := c.Role
		if role == "model" {
			role = "assistant"
		}
		if role == "" {
			role = "user"
		}
		oaMessages = append(oaMessages, ChatMessage{Role: role, Content: extractGeminiText(c.Parts)})
	}
	if stream {
		gr.Stream = true
	}

	oaBody := map[string]any{
		"model":    model,
		"messages": oaMessages,
		"stream":   stream,
	}
	if gr.GenerationConfig.Temperature != nil {
		oaBody["temperature"] = *gr.GenerationConfig.Temperature
	}
	if gr.GenerationConfig.TopP != nil {
		oaBody["top_p"] = *gr.GenerationConfig.TopP
	}
	if gr.GenerationConfig.MaxOutputTokens != nil {
		oaBody["max_tokens"] = *gr.GenerationConfig.MaxOutputTokens
	}
	if len(gr.GenerationConfig.StopSequences) > 0 {
		oaBody["stop"] = gr.GenerationConfig.StopSequences
	}
	oaBytes, _ := json.Marshal(oaBody)

	modifiedReq := &http.Request{
		Method:     "POST",
		URL:        &url.URL{Path: "/v1/chat/completions"},
		Header:     r.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(oaBytes)),
		RemoteAddr: r.RemoteAddr,
		Host:       r.Host,
	}
	modifiedReq.ContentLength = int64(len(oaBytes))

	interceptor := &geminiResponseWriter{
		realWriter:    w,
		header:        make(http.Header),
		statusCode:    200,
		model:         model,
		streamStarted: false,
	}
	handleGatewayRequest(interceptor, modifiedReq)
	interceptor.finalize()
}

// splitGeminiModelSuffix splits "model:generateContent" into ("model","generateContent").
func splitGeminiModelSuffix(raw string) (model, suffix string) {
	if i := strings.Index(raw, ":"); i >= 0 {
		return raw[:i], raw[i+1:]
	}
	return raw, ""
}

// extractGeminiText concatenates all text parts of a Gemini content block.
func extractGeminiText(parts []geminiChatPart) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// convertGeminiFinish maps an OpenAI finish_reason to a Gemini finishReason.
func convertGeminiFinish(reason string) string {
	switch reason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "tool_calls", "function_call":
		return "STOP"
	default:
		return "STOP"
	}
}

// ---- Response translation (OpenAI -> Gemini) ----

type geminiResponseWriter struct {
	realWriter    http.ResponseWriter
	header        http.Header
	statusCode    int
	isStreaming   bool
	streamStarted bool
	buf           bytes.Buffer
	model         string
}

func (w *geminiResponseWriter) Header() http.Header { return w.header }

func (w *geminiResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	ct := w.header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		w.isStreaming = true
		w.realWriter.Header().Set("Content-Type", "text/event-stream")
		w.realWriter.Header().Set("Cache-Control", "no-cache")
		w.realWriter.Header().Set("X-Accel-Buffering", "no")
		w.realWriter.WriteHeader(200)
	}
}

func (w *geminiResponseWriter) Write(data []byte) (int, error) {
	if w.isStreaming {
		return w.writeStreaming(data)
	}
	w.buf.Write(data)
	return len(data), nil
}

func (w *geminiResponseWriter) Flush() {
	if w.isStreaming {
		if f, ok := w.realWriter.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func (w *geminiResponseWriter) writeStreaming(data []byte) (int, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataStr := strings.TrimPrefix(line, "data: ")
		if dataStr == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(dataStr), &chunk) != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				w.streamStarted = true
				w.writeGeminiSSE(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{{"text": choice.Delta.Content}},
							"role":  "model",
						},
						"finishReason": nil,
						"index":        0,
					}},
				})
			}
			if choice.FinishReason != nil {
				w.writeGeminiSSE(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{{"text": ""}},
							"role":  "model",
						},
						"finishReason": convertGeminiFinish(*choice.FinishReason),
						"index":        0,
					}},
				})
			}
		}
	}
	return len(data), nil
}

func (w *geminiResponseWriter) writeGeminiSSE(payload any) {
	jsonData, _ := json.Marshal(payload)
	fmt.Fprintf(w.realWriter, "data: %s\n\n", string(jsonData))
	if f, ok := w.realWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *geminiResponseWriter) finalize() {
	if w.isStreaming {
		return
	}

	if w.statusCode >= 400 {
		var ompErr struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		errMsg := string(w.buf.Bytes())
		errType := "API_ERROR"
		if json.Unmarshal(w.buf.Bytes(), &ompErr) == nil && ompErr.Error.Message != "" {
			errMsg = ompErr.Error.Message
			if ompErr.Error.Type != "" {
				errType = strings.ToUpper(ompErr.Error.Type)
			}
		}
		w.realWriter.Header().Set("Content-Type", "application/json")
		w.realWriter.WriteHeader(w.statusCode)
		json.NewEncoder(w.realWriter).Encode(map[string]any{
			"error": map[string]any{
				"code":    errType,
				"message": errMsg,
				"status":  w.statusCode,
			},
		})
		return
	}

	var resp ChatResponse
	if err := json.Unmarshal(w.buf.Bytes(), &resp); err != nil {
		w.realWriter.Header().Set("Content-Type", "application/json")
		w.realWriter.WriteHeader(w.statusCode)
		w.realWriter.Write(w.buf.Bytes())
		return
	}

	var text string
	finishReason := "STOP"
	if len(resp.Choices) > 0 {
		if resp.Choices[0].Message != nil && resp.Choices[0].Message.Content != nil {
			text = *resp.Choices[0].Message.Content
		}
		if resp.Choices[0].FinishReason != nil {
			finishReason = convertGeminiFinish(*resp.Choices[0].FinishReason)
		}
	}

	out := map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{"text": text}},
				"role":  "model",
			},
			"finishReason": finishReason,
			"index":        0,
		}},
	}
	if resp.Usage != nil {
		out["usageMetadata"] = map[string]int{
			"promptTokenCount":     resp.Usage.PromptTokens,
			"candidatesTokenCount": resp.Usage.CompletionTokens,
			"totalTokenCount":      resp.Usage.TotalTokens,
		}
	}

	w.realWriter.Header().Set("Content-Type", "application/json")
	w.realWriter.WriteHeader(200)
	json.NewEncoder(w.realWriter).Encode(out)
}

// writeGeminiError writes an error in Gemini API format.
func writeGeminiError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "INVALID_ARGUMENT",
			"message": msg,
			"status":  status,
		},
	})
}
