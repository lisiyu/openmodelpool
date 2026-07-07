package main

import (
	"encoding/json"
	"strings"
)

// ============================================================
// OpenAI-compatible request/response models
// ============================================================

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model          string        `json:"model"`
	Messages       []ChatMessage `json:"messages"`
	Stream         bool          `json:"stream"`
	Temperature    *float64      `json:"temperature,omitempty"`
	TopP           *float64      `json:"top_p,omitempty"`
	MaxTokens      *int          `json:"max_tokens,omitempty"`
	ConversationID string        `json:"conversation_id,omitempty"`
	Extra          map[string]any `json:"-"` // extra fields preserved
}

// UnmarshalJSON preserves unknown fields
func (r *ChatRequest) UnmarshalJSON(data []byte) error {
	type Alias ChatRequest
	aux := &struct{ *Alias }{Alias: (*Alias)(r)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	// Capture extra fields
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	known := map[string]bool{
		"model": true, "messages": true, "stream": true,
		"temperature": true, "top_p": true, "max_tokens": true,
		"conversation_id": true,
	}
	r.Extra = make(map[string]any)
	for k, v := range raw {
		if !known[k] {
			var val any
			json.Unmarshal(v, &val)
			r.Extra[k] = val
		}
	}
	return nil
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      *Msg    `json:"message,omitempty"`
	Delta        *Msg    `json:"delta,omitempty"`
	FinishReason *string `json:"finish_reason"`
}

type Msg struct {
	Role    string  `json:"role,omitempty"`
	Content *string `json:"content,omitempty"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

type ChatChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

type ModelInfo struct {
	ID       string `json:"id"`
	Object   string `json:"object"`
	Created  int64  `json:"created"`
	OwnedBy  string `json:"owned_by"`
}

type ModelListResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// ============================================================
// Provider & Model definitions
// ============================================================

type ModelDef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type Provider struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"` // "openai_compatible", "coze", "sider"
	BaseURL     string     `json:"base_url"`
	APIKey      string     `json:"api_key"`
	Enabled     bool       `json:"enabled"`
	Models      []ModelDef `json:"models"`
	Priority    int        `json:"priority"`
	TokenLimit  int64      `json:"token_limit,omitempty"` // monthly token budget, 0=unlimited
	Description string     `json:"description,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	APIKeyURL   string     `json:"api_key_url,omitempty"`
	Proxy       string     `json:"proxy,omitempty"`          // http://, socks5://, or vmess:// link
	CreatedAt   string     `json:"created_at,omitempty"`
	UpdatedAt   string     `json:"updated_at,omitempty"`
}

// Safe returns a copy with API key masked
func (p *Provider) Safe() Provider {
	safe := *p
	if len(safe.APIKey) > 8 {
		safe.APIKey = safe.APIKey[:4] + "..." + safe.APIKey[len(safe.APIKey)-4:]
	} else if safe.APIKey != "" {
		safe.APIKey = "***"
	} else {
		safe.APIKey = ""
	}
	// Mask vmess proxy links (contains sensitive UUID)
	if strings.HasPrefix(safe.Proxy, "vmess://") {
		safe.Proxy = "vmess://***"
	}
	return safe
}

// ============================================================
// Usage record
// ============================================================

type UsageRecord struct {
	Timestamp        string  `json:"timestamp"`
	ProviderID       string  `json:"provider_id"`
	ProviderName     string  `json:"provider_name"`
	Model            string  `json:"model"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	LatencyMS        float64 `json:"latency_ms"`
	Success          bool    `json:"success"`
	Error            string  `json:"error,omitempty"`
}

// ============================================================
// Pricing
// ============================================================

type Price struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// ============================================================
// Admin / Auth
// ============================================================

type AdminData struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	Email        string `json:"email"`
	CreatedAt    string `json:"created_at"`
}

type SMTPConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	FromEmail string `json:"from_email"`
	UseTLS    bool   `json:"use_tls"`
}

type ResetToken struct {
	Token  string `json:"token"`
	Email  string `json:"email"`
	Expire string `json:"expire"`
	Used   bool   `json:"used"`
}

type AdminStore struct {
	Admin     AdminData  `json:"admin"`
	JWTSecret string     `json:"jwt_secret"`
	SMTP      SMTPConfig `json:"smtp"`
	Reset     *ResetToken `json:"reset_token,omitempty"`
	Initialized bool     `json:"initialized"`
}

// ============================================================
// Sider token status
// ============================================================

type SiderStatus struct {
	TokenStatus        string `json:"token_status"`
	LastSuccessAt      string `json:"last_success_at"`
	LastFailureAt      string `json:"last_failure_at"`
	ConsecutiveFailures int   `json:"consecutive_failures"`
	FailureMessage     string `json:"failure_message"`
	CheckedAt          string `json:"checked_at"`
}

// ============================================================
// Coze API models (internal)
// ============================================================

type CozeChatPayload struct {
	BotID             string            `json:"bot_id"`
	UserID            string            `json:"user_id"`
	Stream            bool              `json:"stream"`
	AutoSaveHistory   bool              `json:"auto_save_history"`
	AdditionalMessages []CozeMessage     `json:"additional_messages"`
	ConversationID    string            `json:"conversation_id,omitempty"`
}

type CozeMessage struct {
	Role        string `json:"role"`
	Content     string `json:"content"`
	ContentType string `json:"content_type"`
}

type CozeResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		ID             string         `json:"id"`
		ConversationID string         `json:"conversation_id"`
		Status         string         `json:"status"`
		Usage          map[string]any `json:"usage,omitempty"`
	} `json:"data"`
}
