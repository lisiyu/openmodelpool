package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Client handles forwarding requests to upstream AI providers.
// Supports: openai_compatible, sider, coze.

const siderChatURL = "https://sider.ai/api/v3/completion/text"

var siderHeadersBase = map[string]string{
	"Accept":          "*/*",
	"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
	"Cache-Control":   "no-cache",
	"Origin":          "chrome-extension://dhoenijjpgpeimemopealfcbiecgceod",
	"Content-Type":    "application/json",
	"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
}

// doNonStream sends a non-streaming request and returns the OpenAI-format response.
func doNonStream(p Provider, model string, messages []ChatMessage, extra map[string]any) (*ChatResponse, error) {
	switch p.Type {
	case "sider":
		return siderNonStream(p, model, messages)
	case "coze":
		return cozeNonStream(p, model, messages)
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

	resp, err := http.DefaultClient.Do(req)
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

	resp, err := http.DefaultClient.Do(req)
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
		// Just copy directly
		_, err = io.Copy(w, resp.Body)
		return err
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprint(w, line+"\n")
		flusher.Flush()
	}
	return scanner.Err()
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

	client := &http.Client{Timeout: 300 * time.Second}
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

	client := &http.Client{Timeout: 300 * time.Second}
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
	token := cfg.Get("coze_api_token", "")
	if token == "" {
		return nil, fmt.Errorf("coze API token not configured")
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

	client := &http.Client{Timeout: 300 * time.Second}
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
	token := cfg.Get("coze_api_token", "")
	if token == "" {
		writeSSEError(w, model, "coze API token not configured")
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

	client := &http.Client{Timeout: 300 * time.Second}
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
	switch p.Type {
	case "coze":
		token := cfg.Get("coze_api_token", "")
		if token == "" {
			return map[string]any{"success": false, "error": "Coze PAT not configured"}
		}
		baseURL := p.BaseURL
		if baseURL == "" {
			baseURL = "https://api.coze.cn"
		}
		req, _ := http.NewRequest("GET", baseURL+"/v1/bots?page_index=0&page_size=1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		client := &http.Client{Timeout: 15 * time.Second}
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
		if p.APIKey == "" {
			return map[string]any{"success": false, "error": "Sider token not configured"}
		}
		h := siderBuildHeaders(p.APIKey)
		payload := siderBuildPayload("auto", []ChatMessage{{Role: "user", Content: "hi"}}, false)
		payload["prompt"] = "ping"
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", siderChatURL, bytes.NewReader(body))
		req.Header = h
		client := &http.Client{Timeout: 30 * time.Second}
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

	default: // openai_compatible
		if p.APIKey == "" {
			return map[string]any{"success": false, "error": "API key not configured"}
		}
		req, _ := http.NewRequest("GET", strings.TrimRight(p.BaseURL, "/")+"/models", nil)
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var data struct {
				Data []any `json:"data"`
			}
			json.NewDecoder(resp.Body).Decode(&data)
			return map[string]any{"success": true, "message": fmt.Sprintf("Connected, %d models", len(data.Data))}
		}
		b, _ := io.ReadAll(resp.Body)
		return map[string]any{"success": false, "error": fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(b), 200))}
	}
}

// fetchRemoteModels fetches model list from an OpenAI-compatible provider.
func fetchRemoteModels(p Provider) []map[string]string {
	if p.Type != "openai_compatible" || p.APIKey == "" {
		return nil
	}
	req, _ := http.NewRequest("GET", strings.TrimRight(p.BaseURL, "/")+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	client := &http.Client{Timeout: 15 * time.Second}
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
