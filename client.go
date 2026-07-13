package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	socksproxy "golang.org/x/net/proxy"
)

// Client handles forwarding requests to upstream AI providers.
// Supports: openai_compatible, sider, coze, anthropic.


// sharedTransport is a connection-pooled transport for all proxy requests.
var sharedTransport = &http.Transport{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     90 * time.Second,
	DisableCompression:  false,
}

// sharedHTTPClient reuses connections across requests.
var sharedHTTPClient = &http.Client{
	Transport: sharedTransport,
}

const siderChatURL = "https://sider.ai/api/v3/completion/text"

var siderHeadersBase = map[string]string{
	"Accept":          "*/*",
	"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
	"Cache-Control":   "no-cache",
	"Origin":          "chrome-extension://dhoenijjpgpeimemopealfcbiecgceod",
	"Content-Type":    "application/json",
	"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
}

// proxyHTTPClient returns an HTTP client configured with the provider's proxy.
func proxyHTTPClient(p Provider, timeout time.Duration) *http.Client {
	proxy := p.Proxy
	// For vmess:// links, the proxy should already be resolved to socks5://localhost:port
	// by ResolveProxy during provider save. If not resolved yet, try now.
	if strings.HasPrefix(proxy, "vmess://") {
		resolved, err := ResolveProxy(p.ID, proxy)
		if err != nil {
			slog.Warn("failed to resolve VMess proxy", "provider", p.ID, "error", err)
			proxy = ""
		} else {
			proxy = resolved
		}
	}

	if proxy == "" {
		return &http.Client{Transport: sharedTransport, Timeout: timeout}
	}

	// For socks5:// proxies, use golang.org/x/net/proxy
	if strings.HasPrefix(proxy, "socks5://") || strings.HasPrefix(proxy, "socks5h://") {
		proxyURL, err := url.Parse(proxy)
		if err == nil {
			socksDialer, err := socksproxy.SOCKS5("tcp", proxyURL.Host, nil, socksproxy.Direct)
			if err == nil {
				transport := &http.Transport{
					DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
						return socksDialer.Dial(network, addr)
					},
				}
				return &http.Client{Timeout: timeout, Transport: transport}
			}
		}
		return &http.Client{Timeout: timeout}
	}

	// For http:// and https:// proxies, use standard http.Transport
	transport := &http.Transport{
		Proxy: http.ProxyURL(mustParseURL(proxy)),
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func mustParseURL(rawurl string) *url.URL {
	u, _ := url.Parse(rawurl)
	return u
}

// doNonStream sends a non-streaming request and returns the OpenAI-format response.
func doNonStream(p Provider, model string, messages []ChatMessage, extra map[string]any) (*ChatResponse, error) {
	switch p.Type {
	case "sider":
		return siderNonStream(p, model, messages)
	case "coze":
		return cozeNonStream(p, model, messages)
	case "anthropic":
		return anthropicNonStream(p, model, messages)
	default:
		return openaiNonStream(p, model, messages, extra)
	}
}

// doStream writes SSE chunks to w. Returns when stream completes.
func doStream(p Provider, model string, messages []ChatMessage, extra map[string]any, w io.Writer) error {
	switch p.Type {
	case "sider":
		return siderStream(p, model, messages, w)
	case "coze":
		return cozeStream(p, model, messages, w)
	case "anthropic":
		return anthropicStream(p, model, messages, w)
	default:
		return openaiStream(p, model, messages, extra, w)
	}
}

// ============================================================
// OpenAI-compatible
// ============================================================

func openaiNonStream(p Provider, model string, messages []ChatMessage, extra map[string]any) (*ChatResponse, error) {
	body := buildOpenAIBody(model, messages, false, extra)
	req, _ := http.NewRequest("POST", p.BaseURL+"/chat/completions", jsonBody(body))
	setOpenAIHeaders(req, p.APIKey)

	resp, err := proxyHTTPClient(p, 300 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upstream error (%d): %s", resp.StatusCode, truncate(string(b), 200))
	}

	var result ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}
	return &result, nil
}

func openaiStream(p Provider, model string, messages []ChatMessage, extra map[string]any, w io.Writer) error {
	body := buildOpenAIBody(model, messages, true, extra)
	req, _ := http.NewRequest("POST", p.BaseURL+"/chat/completions", jsonBody(body))
	setOpenAIHeaders(req, p.APIKey)

	// Use a long overall timeout but with idle detection via pipe
	client := proxyHTTPClient(p, 300*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upstream error (%d): %s", resp.StatusCode, truncate(string(b), 200))
	}

	// Pipe SSE directly - upstream is already in OpenAI SSE format
	flusher, ok := w.(interface{ Flush() })
	if !ok {
		_, err = io.Copy(w, resp.Body)
		return err
	}

	// Stream with idle timeout: if no data received for 90 seconds, abort
	const streamIdleTimeout = 90 * time.Second
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	done := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprint(w, line+"\n")
			flusher.Flush()
		}
		done <- scanner.Err()
	}()

	// Watchdog: detect if stream stalls
	idleTimer := time.NewTimer(streamIdleTimeout)
	defer idleTimer.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-idleTimer.C:
			return fmt.Errorf("stream idle timeout: no data received for %v", streamIdleTimeout)
		}
	}
}

func buildOpenAIBody(model string, messages []ChatMessage, stream bool, extra map[string]any) map[string]any {
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   stream,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func setOpenAIHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
}

// ============================================================
// Sider.ai adapter
// ============================================================

func siderBuildHeaders(token string) http.Header {
	h := make(http.Header)
	for k, v := range siderHeadersBase {
		h.Set(k, v)
	}
	h.Set("Authorization", "Bearer "+token)
	h.Set("Cookie", "token=Bearer%20"+token+"; refresh_token=discard")
	return h
}

func siderBuildPayload(model string, messages []ChatMessage, stream bool) map[string]any {
	var parts []string
	for _, m := range messages {
		switch m.Role {
		case "system":
			parts = append(parts, "[System Instructions]\n"+m.Content+"\n")
		case "assistant":
			parts = append(parts, "[Assistant]: "+m.Content)
		default:
			parts = append(parts, "[User]: "+m.Content)
		}
	}
	return map[string]any{
		"prompt":           strings.Join(parts, "\n"),
		"stream":           stream,
		"app_name":         "ChitChat_Edge_Ext",
		"app_version":      "4.40.0",
		"tz_name":          "Asia/Shanghai",
		"model":            model,
		"search":           false,
		"auto_search":      false,
		"from":             "chat",
		"group_id":         "default",
		"chat_models":      []any{},
		"files":            []any{},
		"prompt_templates": []any{},
		"tools":            map[string]any{"auto": []any{}},
		"extra_info": map[string]any{
			"origin_url":   "chrome-extension://dhoenijjpgpeimemopealfcbiecgceod/standalone.html",
			"origin_title": "Sider",
		},
	}
}

func siderNonStream(p Provider, model string, messages []ChatMessage) (*ChatResponse, error) {
	payload := siderBuildPayload(model, messages, false)
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", siderChatURL, bytes.NewReader(body))
	req.Header = siderBuildHeaders(p.APIKey)

	client := proxyHTTPClient(p, 300 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		siderMon.RecordFailure(resp.StatusCode, fmt.Sprintf("Token expired (HTTP %d)", resp.StatusCode))
		return nil, fmt.Errorf("sider token expired (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sider error (%d): %s", resp.StatusCode, truncate(string(b), 200))
	}
	siderMon.RecordSuccess()

	// Parse SSE response
	respBody, _ := io.ReadAll(resp.Body)
	var fullText strings.Builder
	for _, line := range strings.Split(string(respBody), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(line[5:])
		if dataStr == "[DONE]" {
			break
		}
		var data map[string]any
		if json.Unmarshal([]byte(dataStr), &data) != nil {
			continue
		}
		if d, ok := data["data"].(map[string]any); ok {
			if text, ok := d["text"].(string); ok {
				fullText.WriteString(text)
			}
		}
	}

	content := fullText.String()
	stop := "stop"
	return &ChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%s", randomString(24)),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{{Message: &Msg{Role: "assistant", Content: &content}, FinishReason: &stop}},
		Usage:   &Usage{},
	}, nil
}

func siderStream(p Provider, model string, messages []ChatMessage, w io.Writer) error {
	payload := siderBuildPayload(model, messages, true)
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", siderChatURL, bytes.NewReader(body))
	req.Header = siderBuildHeaders(p.APIKey)

	client := proxyHTTPClient(p, 300 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		siderMon.RecordFailure(resp.StatusCode, fmt.Sprintf("Token expired (HTTP %d)", resp.StatusCode))
		writeSSEError(w, model, fmt.Sprintf("Sider token expired (HTTP %d)", resp.StatusCode))
		return nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		writeSSEError(w, model, fmt.Sprintf("Sider error (%d): %s", resp.StatusCode, truncate(string(b), 200)))
		return nil
	}

	cmplID := fmt.Sprintf("chatcmpl-%s", randomString(24))
	created := time.Now().Unix()
	flusher, hasFlusher := w.(interface{ Flush() })

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(line[5:])
		if dataStr == "[DONE]" {
			break
		}
		var data map[string]any
		if json.Unmarshal([]byte(dataStr), &data) != nil {
			continue
		}
		if d, ok := data["data"].(map[string]any); ok {
			if text, ok := d["text"].(string); ok && text != "" {
				chunk := ChatChunk{
					ID: cmplID, Object: "chat.completion.chunk",
					Created: created, Model: model,
					Choices: []Choice{{Delta: &Msg{Content: &text}}},
				}
				writeSSEChunk(w, chunk)
				if hasFlusher {
					flusher.Flush()
				}
			}
		}
	}

	siderMon.RecordSuccess()

	// Final chunk
	stop := "stop"
	final := ChatChunk{
		ID: cmplID, Object: "chat.completion.chunk",
		Created: created, Model: model,
		Choices: []Choice{{Delta: &Msg{}, FinishReason: &stop}},
	}
	writeSSEChunk(w, final)
	if hasFlusher {
		flusher.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	return nil
}

// ============================================================
// Coze adapter
// ============================================================

func cozeNonStream(p Provider, model string, messages []ChatMessage) (*ChatResponse, error) {
	token := p.APIKey
	if token == "" {
		token = cfg.Get("coze_api_token", "")
	}
	if token == "" {
		return nil, fmt.Errorf("coze API token not configured (set API Key in provider config)")
	}
	botID := model
	if strings.HasPrefix(botID, "coze-") {
		botID = botID[5:]
	}
	if botID == "" {
		botID = cfg.Get("coze_bot_id", "")
	}

	baseURL := p.BaseURL
	if baseURL == "" {
		baseURL = "https://api.coze.cn"
	}

	// Create chat
	payload := CozeChatPayload{
		BotID:  botID,
		UserID: "proxy-user",
		Stream: false,
	}
	for _, m := range messages {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		payload.AdditionalMessages = append(payload.AdditionalMessages, CozeMessage{
			Role: role, Content: m.Content, ContentType: "text",
		})
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", baseURL+"/v3/chat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := proxyHTTPClient(p, 300 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var chatResp struct {
		Code int `json:"code"`
		Data struct {
			ID             string `json:"id"`
			ConversationID string `json:"conversation_id"`
			Status         string `json:"status"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&chatResp)

	if chatResp.Data.Status == "" {
		return nil, fmt.Errorf("coze API error: empty response")
	}

	// Poll for completion
	convID := chatResp.Data.ConversationID
	chatID := chatResp.Data.ID
	status := chatResp.Data.Status

	for status != "completed" && status != "failed" {
		time.Sleep(time.Second)
		pollBody, _ := json.Marshal(map[string]string{
			"conversation_id": convID, "chat_id": chatID,
		})
		pollReq, _ := http.NewRequest("POST", baseURL+"/v3/chat/retrieve", bytes.NewReader(pollBody))
		pollReq.Header.Set("Authorization", "Bearer "+token)
		pollReq.Header.Set("Content-Type", "application/json")

		pollResp, err := client.Do(pollReq)
		if err != nil {
			return nil, err
		}
		var pollResult struct {
			Data struct{ Status string `json:"status"` } `json:"data"`
		}
		json.NewDecoder(pollResp.Body).Decode(&pollResult)
		pollResp.Body.Close()
		status = pollResult.Data.Status
	}

	if status == "failed" {
		return nil, fmt.Errorf("coze chat failed")
	}

	// Get messages
	msgReq, _ := http.NewRequest("GET", baseURL+"/v3/conversation/message/list?conversation_id="+convID+"&chat_id="+chatID, nil)
	msgReq.Header.Set("Authorization", "Bearer "+token)
	msgReq.Header.Set("Content-Type", "application/json")

	msgResp, err := client.Do(msgReq)
	if err != nil {
		return nil, err
	}
	defer msgResp.Body.Close()

	var msgList struct {
		Data []struct {
			Role        string `json:"role"`
			Content     string `json:"content"`
			ContentType string `json:"content_type"`
			Type        string `json:"type"`
		} `json:"data"`
	}
	json.NewDecoder(msgResp.Body).Decode(&msgList)

	var assistantContent string
	for _, m := range msgList.Data {
		if m.Role == "assistant" && (m.Type == "answer" || m.Type == "verbose") {
			assistantContent += m.Content
		}
	}
	if assistantContent == "" {
		for _, m := range msgList.Data {
			if m.Role == "assistant" && m.Content != "" {
				assistantContent += m.Content
				break
			}
		}
	}

	return &ChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%s", randomString(24)),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "coze-" + botID,
		Choices: []Choice{{
			Message:      &Msg{Role: "assistant", Content: &assistantContent},
			FinishReason: strPtr("stop"),
		}},
		Usage: &Usage{},
	}, nil
}

func cozeStream(p Provider, model string, messages []ChatMessage, w io.Writer) error {
	token := p.APIKey
	if token == "" {
		token = cfg.Get("coze_api_token", "")
	}
	if token == "" {
		writeSSEError(w, model, "coze API token not configured (set API Key in provider config)")
		return nil
	}
	botID := model
	if strings.HasPrefix(botID, "coze-") {
		botID = botID[5:]
	}
	if botID == "" {
		botID = cfg.Get("coze_bot_id", "")
	}

	baseURL := p.BaseURL
	if baseURL == "" {
		baseURL = "https://api.coze.cn"
	}

	payload := CozeChatPayload{
		BotID:  botID,
		UserID: "proxy-user",
		Stream: true,
	}
	for _, m := range messages {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		payload.AdditionalMessages = append(payload.AdditionalMessages, CozeMessage{
			Role: role, Content: m.Content, ContentType: "text",
		})
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", baseURL+"/v3/chat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := proxyHTTPClient(p, 300 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		writeSSEError(w, model, err.Error())
		return nil
	}
	defer resp.Body.Close()

	cmplID := fmt.Sprintf("chatcmpl-%s", randomString(24))
	created := time.Now().Unix()
	flusher, hasFlusher := w.(interface{ Flush() })

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(line[5:])
		if dataStr == "" || dataStr == "[DONE]" {
			continue
		}
		var event map[string]any
		if json.Unmarshal([]byte(dataStr), &event) != nil {
			continue
		}
		if role, _ := event["role"].(string); role != "assistant" {
			continue
		}
		content, _ := event["content"].(string)
		if content == "" {
			continue
		}
		chunk := ChatChunk{
			ID: cmplID, Object: "chat.completion.chunk",
			Created: created, Model: "coze-" + botID,
			Choices: []Choice{{Delta: &Msg{Content: &content}}},
		}
		writeSSEChunk(w, chunk)
		if hasFlusher {
			flusher.Flush()
		}
	}

	stop := "stop"
	final := ChatChunk{
		ID: cmplID, Object: "chat.completion.chunk",
		Created: created, Model: "coze-" + botID,
		Choices: []Choice{{Delta: &Msg{}, FinishReason: &stop}},
	}
	writeSSEChunk(w, final)
	fmt.Fprint(w, "data: [DONE]\n\n")
	return nil
}

// ============================================================
// Anthropic Claude adapter (Messages API → OpenAI format)
// ============================================================

func anthropicBuildMessages(messages []ChatMessage) ([]map[string]any, string) {
	// Anthropic Messages API requires system as a separate field,
	// and messages must alternate user/assistant.
	var systemMsg string
	var out []map[string]any

	for _, m := range messages {
		switch m.Role {
		case "system":
			systemMsg = m.Content
		case "assistant":
			out = append(out, map[string]any{"role": "assistant", "content": m.Content})
		default: // "user"
			out = append(out, map[string]any{"role": "user", "content": m.Content})
		}
	}
	return out, systemMsg
}

func anthropicNonStream(p Provider, model string, messages []ChatMessage) (*ChatResponse, error) {
	anthMessages, systemMsg := anthropicBuildMessages(messages)

	payload := map[string]any{
		"model":      model,
		"messages":   anthMessages,
		"max_tokens": 8192,
		"stream":     false,
	}
	if systemMsg != "" {
		payload["system"] = systemMsg
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", p.BaseURL+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := proxyHTTPClient(p, 300*time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic upstream (%d): %s", resp.StatusCode, truncate(string(b), 300))
	}

	var result struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}

	var text strings.Builder
	for _, c := range result.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	content := text.String()
	stop := "stop"
	if result.StopReason == "end_turn" {
		stop = "stop"
	}

	return &ChatResponse{
		ID:      result.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{{
			Message:      &Msg{Role: "assistant", Content: &content},
			FinishReason: &stop,
		}},
		Usage: &Usage{
			PromptTokens:     result.Usage.InputTokens,
			CompletionTokens: result.Usage.OutputTokens,
			TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
		},
	}, nil
}

func anthropicStream(p Provider, model string, messages []ChatMessage, w io.Writer) error {
	anthMessages, systemMsg := anthropicBuildMessages(messages)

	payload := map[string]any{
		"model":      model,
		"messages":   anthMessages,
		"max_tokens": 8192,
		"stream":     true,
	}
	if systemMsg != "" {
		payload["system"] = systemMsg
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", p.BaseURL+"/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := proxyHTTPClient(p, 300*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		writeSSEError(w, model, fmt.Sprintf("anthropic upstream (%d): %s", resp.StatusCode, truncate(string(b), 200)))
		return nil
	}

	cmplID := fmt.Sprintf("chatcmpl-%s", randomString(24))
	created := time.Now().Unix()
	flusher, hasFlusher := w.(interface{ Flush() })

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "event: ") && !strings.HasPrefix(line, "data: ") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataStr := strings.TrimSpace(line[6:])
		if dataStr == "" {
			continue
		}

		var event map[string]any
		if json.Unmarshal([]byte(dataStr), &event) != nil {
			continue
		}

		evtType, _ := event["type"].(string)
		switch evtType {
		case "content_block_delta":
			if delta, ok := event["delta"].(map[string]any); ok {
				if text, _ := delta["text"].(string); text != "" {
					chunk := ChatChunk{
						ID: cmplID, Object: "chat.completion.chunk",
						Created: created, Model: model,
						Choices: []Choice{{Delta: &Msg{Content: &text}}},
					}
					writeSSEChunk(w, chunk)
					if hasFlusher {
						flusher.Flush()
					}
				}
			}
		case "message_stop":
			// done
		}
	}

	// Final chunk
	stop := "stop"
	final := ChatChunk{
		ID: cmplID, Object: "chat.completion.chunk",
		Created: created, Model: model,
		Choices: []Choice{{Delta: &Msg{}, FinishReason: &stop}},
	}
	writeSSEChunk(w, final)
	if hasFlusher {
		flusher.Flush()
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	return nil
}

// ============================================================
// SSE helpers
// ============================================================

func writeSSEChunk(w io.Writer, chunk ChatChunk) {
	b, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func writeSSEError(w io.Writer, model, msg string) {
	errResp := map[string]any{"error": map[string]any{"message": msg, "type": "api_error"}}
	b, _ := json.Marshal(errResp)
	fmt.Fprintf(w, "data: %s\n\n", b)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func strPtr(s string) *string { return &s }

// testConnection tests a provider's API connectivity.
func testConnection(p Provider) map[string]any {
	// Multi-key migration: if legacy APIKey is empty, try APIKeys array
	if p.APIKey == "" && len(p.APIKeys) > 0 {
		p.APIKey = p.GetEffectiveAPIKey()
	}
	// Decrypt API key if encrypted
	if IsEncrypted(p.APIKey) {
		decrypted, err := decryptAPIKey(p.APIKey)
		if err != nil {
			return map[string]any{"success": false, "error": "failed to decrypt API key: " + err.Error()}
		}
		p.APIKey = decrypted
	}
	return testConnectionWithKey(p, p.APIKey)
}

// testConnectionWithKey tests the provider connection with a specific API key.
// If keyOverride is provided, it will be used instead of p.APIKey.
func testConnectionWithKey(p Provider, keyOverride string) map[string]any {
	// Create a copy of the provider to avoid modifying the original
	testProvider := p
	if keyOverride != "" {
		testProvider.APIKey = keyOverride
	}

	switch testProvider.Type {
	case "coze":
		token := testProvider.APIKey
		if token == "" {
			token = cfg.Get("coze_api_token", "")
		}
		if token == "" {
			return map[string]any{"success": false, "error": "Coze PAT not configured (set API Key in provider config)"}
		}
		baseURL := testProvider.BaseURL
		if baseURL == "" {
			baseURL = "https://api.coze.cn"
		}
		req, _ := http.NewRequest("GET", baseURL+"/v1/bots?page_index=0&page_size=1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		client := proxyHTTPClient(testProvider, 15 * time.Second)
		resp, err := client.Do(req)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return map[string]any{"success": true, "message": "Coze API connected"}
		}
		return map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d", resp.StatusCode)}

	case "sider":
		if testProvider.APIKey == "" {
			return map[string]any{"success": false, "error": "Sider token not configured"}
		}
		h := siderBuildHeaders(testProvider.APIKey)
		payload := siderBuildPayload("auto", []ChatMessage{{Role: "user", Content: "hi"}}, false)
		payload["prompt"] = "ping"
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", siderChatURL, bytes.NewReader(body))
		req.Header = h
		client := proxyHTTPClient(testProvider, 30 * time.Second)
		resp, err := client.Do(req)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		resp.Body.Close()
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return map[string]any{"success": false, "error": fmt.Sprintf("Token expired (HTTP %d)", resp.StatusCode)}
		}
		if resp.StatusCode >= 400 {
			return map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d", resp.StatusCode)}
		}
		return map[string]any{"success": true, "message": "Sider token valid"}

	case "anthropic":
		if testProvider.APIKey == "" {
			return map[string]any{"success": false, "error": "Anthropic API key not configured"}
		}
		// Use a lightweight messages request to verify connectivity
		testPayload := map[string]any{
			"model":      "claude-3-haiku-20240307",
			"max_tokens": 5,
			"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		}
		testBody, _ := json.Marshal(testPayload)
		testReq, _ := http.NewRequest("POST", testProvider.BaseURL+"/v1/messages", bytes.NewReader(testBody))
		testReq.Header.Set("Content-Type", "application/json")
		testReq.Header.Set("x-api-key", testProvider.APIKey)
		testReq.Header.Set("anthropic-version", "2023-06-01")
		testClient := proxyHTTPClient(testProvider, 15*time.Second)
		testResp, err := testClient.Do(testReq)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		testResp.Body.Close()
		if testResp.StatusCode == 200 {
			return map[string]any{"success": true, "message": "Anthropic API connected"}
		}
		return map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d", testResp.StatusCode)}

	default: // openai_compatible
		if testProvider.APIKey == "" {
			return map[string]any{"success": false, "error": "API key not configured"}
		}
		client := proxyHTTPClient(testProvider, 15 * time.Second)
		baseURL := strings.TrimRight(testProvider.BaseURL, "/")

		// Step 1: Fetch /models to verify key and get available model names
		modelsReq, _ := http.NewRequest("GET", baseURL+"/models", nil)
		modelsReq.Header.Set("Authorization", "Bearer "+testProvider.APIKey)
		modelsResp, err := client.Do(modelsReq)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		modelsBody, _ := io.ReadAll(modelsResp.Body)
		modelsResp.Body.Close()

		if modelsResp.StatusCode == 401 || modelsResp.StatusCode == 403 {
			return map[string]any{"success": false, "error": fmt.Sprintf("API key invalid (HTTP %d)", modelsResp.StatusCode)}
		}

		// Collect available model IDs
		var availableModels []string
		if modelsResp.StatusCode == 200 {
			var modelsData struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.Unmarshal(modelsBody, &modelsData) == nil {
				for _, m := range modelsData.Data {
					if m.ID != "" {
						availableModels = append(availableModels, m.ID)
					}
				}
			}
		}

		// Step 2: Pick a model to chat-test with
		var testModel string
		availSet := make(map[string]bool)
		for _, m := range availableModels {
			availSet[m] = true
		}
		// Prefer an enabled model from config that the provider actually has
		for _, m := range testProvider.Models {
			if m.Enabled && availSet[m.ID] {
				testModel = m.ID
				break
			}
		}
		// Fallback: first available model from API
		if testModel == "" && len(availableModels) > 0 {
			testModel = availableModels[0]
		}
		// Fallback: first model from provider config
		if testModel == "" && len(testProvider.Models) > 0 {
			for _, m := range testProvider.Models {
				if m.Enabled {
					testModel = m.ID
					break
				}
			}
			if testModel == "" {
				testModel = testProvider.Models[0].ID
			}
		}

		if testModel == "" {
			if modelsResp.StatusCode == 200 {
				return map[string]any{"success": true, "message": fmt.Sprintf("Connected, %d models available", len(availableModels))}
			}
			return map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d: %s", modelsResp.StatusCode, truncate(string(modelsBody), 200))}
		}

		// Step 3: Verify key with a lightweight chat request
		testPayload := map[string]any{
			"model":      testModel,
			"max_tokens": 1,
			"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		}
		testBody, _ := json.Marshal(testPayload)
		testReq, _ := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(testBody))
		testReq.Header.Set("Authorization", "Bearer "+testProvider.APIKey)
		testReq.Header.Set("Content-Type", "application/json")
		testResp, err := client.Do(testReq)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		chatBody, _ := io.ReadAll(testResp.Body)
		testResp.Body.Close()

		if testResp.StatusCode == 200 {
			return map[string]any{"success": true, "message": fmt.Sprintf("Connected (chat verified with %s)", testModel)}
		}
		if testResp.StatusCode == 401 || testResp.StatusCode == 403 {
			return map[string]any{"success": false, "error": fmt.Sprintf("API key invalid (HTTP %d)", testResp.StatusCode)}
		}
		// Chat 404 but /models returned 200 means key is valid
		if testResp.StatusCode == 404 && modelsResp.StatusCode == 200 {
			return map[string]any{"success": true, "message": fmt.Sprintf("Connected (key valid, %d models available)", len(availableModels))}
		}
		return map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d: %s", testResp.StatusCode, truncate(string(chatBody), 200))}
	}
}
// fetchRemoteModels fetches model list from an OpenAI-compatible provider.
func fetchRemoteModels(p Provider) []map[string]string {
	// Multi-key migration: if legacy APIKey is empty, try APIKeys array
	if p.APIKey == "" && len(p.APIKeys) > 0 {
		p.APIKey = p.GetEffectiveAPIKey()
	}
	if p.Type != "openai_compatible" || p.APIKey == "" {
		return nil
	}
	req, _ := http.NewRequest("GET", strings.TrimRight(p.BaseURL, "/")+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	client := proxyHTTPClient(p, 15 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("fetch remote models failed", "provider", p.ID, "error", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&data)
	var out []map[string]string
	for _, m := range data.Data {
		if m.ID != "" {
			out = append(out, map[string]string{"id": m.ID, "name": m.ID})
		}
	}
	return out
}
