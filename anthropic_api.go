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
	"time"
)

// ============================================================
// Anthropic Messages API compatibility layer
// Converts POST /v1/messages (Anthropic format) to internal OpenAI format,
// routes through existing gateway, and converts response back to Anthropic format.
// ============================================================

// anthropicAuthAdapter converts x-api-key header to Authorization: Bearer
// so that withProxyAuth can authenticate the request.
func anthropicAuthAdapter(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if apiKey := r.Header.Get("x-api-key"); apiKey != "" {
				r.Header.Set("Authorization", "Bearer "+apiKey)
			}
		}
		handler(w, r)
	}
}

// Anthropic request types
type anthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []anthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system,omitempty"`
	MaxTokens     int                `json:"max_tokens"`
	Stream        bool               `json:"stream"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// handleAnthropicMessages handles POST /v1/messages in Anthropic API format.
func handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	// Read and parse the Anthropic request body
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeAnthropicError(w, 400, "failed to read request body")
		return
	}
	r.Body.Close()

	var ar anthropicRequest
	if err := json.Unmarshal(bodyBytes, &ar); err != nil {
		writeAnthropicError(w, 400, "invalid request body: "+err.Error())
		return
	}

	if len(ar.Messages) == 0 {
		writeAnthropicError(w, 400, "messages cannot be empty")
		return
	}

	// Convert Anthropic messages to OpenAI ChatMessage format
	var openaiMessages []ChatMessage

	// Convert system field to a system message
	if len(ar.System) > 0 {
		systemText := extractTextFromAnthropicContent(ar.System)
		if systemText != "" {
			openaiMessages = append(openaiMessages, ChatMessage{
				Role:    "system",
				Content: systemText,
			})
		}
	}

	// Convert messages
	for _, msg := range ar.Messages {
		text := extractTextFromAnthropicContent(msg.Content)
		openaiMessages = append(openaiMessages, ChatMessage{
			Role:    msg.Role,
			Content: text,
		})
	}

	// Build OpenAI request body
	openaiBody := map[string]any{
		"model":    ar.Model,
		"messages": openaiMessages,
		"stream":   ar.Stream,
	}
	if ar.MaxTokens > 0 {
		openaiBody["max_tokens"] = ar.MaxTokens
	}
	if ar.Temperature != nil {
		openaiBody["temperature"] = *ar.Temperature
	}
	if ar.TopP != nil {
		openaiBody["top_p"] = *ar.TopP
	}
	if len(ar.StopSequences) > 0 {
		openaiBody["stop"] = ar.StopSequences
	}

	openaiBytes, _ := json.Marshal(openaiBody)

	// Create a modified request that looks like a /v1/chat/completions request
	modifiedReq := &http.Request{
		Method:     "POST",
		URL:        &url.URL{Path: "/v1/chat/completions"},
		Header:     r.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(openaiBytes)),
		RemoteAddr: r.RemoteAddr,
	}
	modifiedReq.ContentLength = int64(len(openaiBytes))
	modifiedReq.RemoteAddr = r.RemoteAddr

	// Create response interceptor
	interceptor := &anthropicResponseWriter{
		realWriter:  w,
		header:      make(http.Header),
		statusCode:  200,
		model:       ar.Model,
		streamStarted: false,
	}

	// Call the existing gateway handler
	handleGatewayRequest(interceptor, modifiedReq)

	// Finalize: convert buffered response or close streaming
	interceptor.finalize()
}

// extractTextFromAnthropicContent extracts text from Anthropic content field.
// Content can be a string or an array of content blocks.
func extractTextFromAnthropicContent(raw json.RawMessage) string {
	// Try string first
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try array of content blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" || b.Type == "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

// anthropicResponseWriter intercepts OpenAI-format responses and converts to Anthropic format.
type anthropicResponseWriter struct {
	realWriter    http.ResponseWriter
	header        http.Header
	statusCode    int
	isStreaming   bool
	streamStarted bool
	buf           bytes.Buffer
	model         string
}

func (w *anthropicResponseWriter) Header() http.Header {
	return w.header
}

func (w *anthropicResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	ct := w.header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		w.isStreaming = true
		// Set up the real writer for streaming
		w.realWriter.Header().Set("Content-Type", "text/event-stream")
		w.realWriter.Header().Set("Cache-Control", "no-cache")
		w.realWriter.Header().Set("X-Accel-Buffering", "no")
		w.realWriter.WriteHeader(200)
	}
}

func (w *anthropicResponseWriter) Write(data []byte) (int, error) {
	if w.isStreaming {
		return w.writeStreaming(data)
	}
	w.buf.Write(data)
	return len(data), nil
}

func (w *anthropicResponseWriter) Flush() {
	if w.isStreaming {
		if f, ok := w.realWriter.(http.Flusher); ok {
			f.Flush()
		}
	}
}

// writeStreaming converts OpenAI SSE chunks to Anthropic SSE events.
func (w *anthropicResponseWriter) writeStreaming(data []byte) (int, error) {
	text := string(data)

	// Send message_start and content_block_start on first write
	if !w.streamStarted {
		w.streamStarted = true
		msgID := "msg_" + fmt.Sprintf("%d", time.Now().UnixNano())

		// message_start event
		startData := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            msgID,
				"type":          "message",
				"role":          "assistant",
				"content":       []any{},
				"model":         w.model,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]int{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		}
		w.writeSSE("message_start", startData)

		// content_block_start event
		blockStart := map[string]any{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]any{"type": "text", "text": ""},
		}
		w.writeSSE("content_block_start", blockStart)
	}

	// Parse OpenAI SSE lines
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataStr := strings.TrimPrefix(line, "data: ")

		if dataStr == "[DONE]" {
			// Send closing events
			// content_block_stop
			w.writeSSE("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": 0,
			})
			// message_delta
			w.writeSSE("message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   "end_turn",
					"stop_sequence": nil,
				},
				"usage": map[string]int{
					"output_tokens": 0,
				},
			})
			// message_stop
			w.writeSSE("message_stop", map[string]any{
				"type": "message_stop",
			})
			continue
		}

		// Parse OpenAI chunk
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
				// content_block_delta
				w.writeSSE("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]any{
						"type": "text_delta",
						"text": choice.Delta.Content,
					},
				})
			}
		}
	}

	return len(data), nil
}

func (w *anthropicResponseWriter) writeSSE(event string, data any) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w.realWriter, "event: %s\ndata: %s\n\n", event, string(jsonData))
	if f, ok := w.realWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finalize converts the buffered OpenAI response to Anthropic format (non-streaming).
func (w *anthropicResponseWriter) finalize() {
	if w.isStreaming {
		return
	}

	// Check if it's an error response
	if w.statusCode >= 400 {
		// Try to parse OMP error format and convert to Anthropic error format
		var ompErr struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		errType := "api_error"
		errMsg := string(w.buf.Bytes())
		if json.Unmarshal(w.buf.Bytes(), &ompErr) == nil && ompErr.Error.Message != "" {
			errMsg = ompErr.Error.Message
			if ompErr.Error.Type != "" {
				errType = ompErr.Error.Type
			}
		}
		// Map HTTP status to Anthropic error type
		if w.statusCode == 401 || w.statusCode == 403 {
			errType = "authentication_error"
		} else if w.statusCode == 404 {
			errType = "not_found_error"
		} else if w.statusCode == 400 {
			errType = "invalid_request_error"
		} else if w.statusCode == 429 {
			errType = "rate_limit_error"
		}
		w.realWriter.Header().Set("Content-Type", "application/json")
		w.realWriter.WriteHeader(w.statusCode)
		json.NewEncoder(w.realWriter).Encode(map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    errType,
				"message": errMsg,
			},
		})
		return
	}

	// Parse the OpenAI ChatResponse
	var resp ChatResponse
	if err := json.Unmarshal(w.buf.Bytes(), &resp); err != nil {
		// Can't parse - return as-is
		w.realWriter.Header().Set("Content-Type", "application/json")
		w.realWriter.WriteHeader(w.statusCode)
		w.realWriter.Write(w.buf.Bytes())
		return
	}

	// Convert to Anthropic format
	var contentText string
	var stopReason string
	if len(resp.Choices) > 0 {
		if resp.Choices[0].Message != nil && resp.Choices[0].Message.Content != nil {
			contentText = *resp.Choices[0].Message.Content
		}
		if resp.Choices[0].FinishReason != nil {
			stopReason = convertFinishReason(*resp.Choices[0].FinishReason)
		}
	}
	if stopReason == "" {
		stopReason = "end_turn"
	}

	inputTokens := 0
	outputTokens := 0
	if resp.Usage != nil {
		inputTokens = resp.Usage.PromptTokens
		outputTokens = resp.Usage.CompletionTokens
	}

	anthropicResp := map[string]any{
		"id":   resp.ID,
		"type": "message",
		"role": "assistant",
		"content": []map[string]string{
			{
				"type": "text",
				"text": contentText,
			},
		},
		"model":          resp.Model,
		"stop_reason":    stopReason,
		"stop_sequence":  nil,
		"usage": map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}

	w.realWriter.Header().Set("Content-Type", "application/json")
	w.realWriter.WriteHeader(200)
	json.NewEncoder(w.realWriter).Encode(anthropicResp)
}

// convertFinishReason maps OpenAI finish_reason to Anthropic stop_reason.
func convertFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// writeAnthropicError writes an error in Anthropic API format.
func writeAnthropicError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    "invalid_request_error",
			"message": msg,
		},
	})
}
