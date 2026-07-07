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
	Type        string     `json:"type"` // "openai_compatible", "coze", "sider", "anthropic"
	BaseURL     string     `json:"base_url"`
	APIKey      string     `json:"api_key"`
	Enabled     bool       `json:"enabled"`
	Models      []ModelDef `json:"models"`
	Priority    int        `json:"priority"`
	TokenLimit  int64      `json:"token_limit,omitempty"` // monthly token budget, 0=unlimited
	Description string     `json:"description,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	APIKeyURL   string     `json:"api_key_url,omitempty"`
	Proxy       string     `json:"proxy,omitempty"` // http://, socks5://, or vmess:// link
	Owner       string     `json:"owner,omitempty"` // consumer ID; empty = admin/system
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
// Request log (in-memory ring buffer)
// ============================================================

type RequestLogEntry struct {
	Timestamp    string  `json:"timestamp"`
	Method       string  `json:"method"`
	Model        string  `json:"model"`
	ProviderID   string  `json:"provider_id"`
	ProviderName string  `json:"provider_name"`
	Success      bool    `json:"success"`
	LatencyMS    float64 `json:"latency_ms"`
	Tokens       int     `json:"tokens"`
	CostUSD      float64 `json:"cost_usd"`
	Error        string  `json:"error,omitempty"`
	Stream       bool    `json:"stream"`
	RetryCount   int     `json:"retry_count"`
}

// ============================================================
// Provider health status
// ============================================================

type ProviderHealth struct {
	ProviderID       string  `json:"provider_id"`
	ProviderName     string  `json:"provider_name"`
	Status           string  `json:"status"` // "healthy", "degraded", "down", "unknown"
	LastCheck        string  `json:"last_check"`
	LatencyMS        float64 `json:"latency_ms"`
	ConsecutiveFails int     `json:"consecutive_fails"`
	LastSuccess      string  `json:"last_success"`
	LastFailure      string  `json:"last_failure"`
	FailureReason    string  `json:"failure_reason,omitempty"`
}

// ============================================================
// Multi-user: invite codes & consumers
// ============================================================

type InviteCode struct {
	Code      string `json:"code"`
	CreatedAt string `json:"created_at"`
	UsedBy    string `json:"used_by,omitempty"`
	UsedAt    string `json:"used_at,omitempty"`
	MaxUses   int    `json:"max_uses"` // 0 = single use
	UseCount  int    `json:"use_count"`
	Role      string `json:"role,omitempty"` // consumer (default) or collaborator
}

type Consumer struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	APIKey       string `json:"api_key"`
	InviteCode   string `json:"invite_code"`
	CreatedAt    string `json:"created_at"`
	TotalTokens  int64  `json:"total_tokens"`
	TotalRequests int   `json:"total_requests"`
	Enabled      bool   `json:"enabled"`
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

// ============================================================
// Federation types (v3.0)
// ============================================================

// NodeInfo represents a node in the federation network.
type NodeInfo struct {
	NodeID         string             `json:"node_id"`
	GitHubUser     string             `json:"github_user"`
	GitHubID       int64              `json:"github_id,omitempty"`
	Endpoint       string             `json:"endpoint"`
	PubKey         string             `json:"pub_key"` // ed25519 base64
	SharedModels   []string           `json:"shared_models"`
	SharedProviders []SharedProvider  `json:"shared_providers"`
	JoinedAt       string             `json:"joined_at"`
	LastSeen       string             `json:"last_seen"`
	Status         string             `json:"status"` // active, inactive, suspended
	SeedNode       bool               `json:"seed_node"`
	Reputation     int                `json:"reputation"`
	Version        string             `json:"version"`
	InviteBy       string             `json:"invite_by,omitempty"`
	TokenBudget    int64              `json:"token_budget"`    // monthly token budget declaration (0 = unlimited)
	TokenUsed      int64              `json:"token_used"`      // tokens used this month
}

// SharedProvider is a provider advertised by a remote node (no API key!).
type SharedProvider struct {
	ProviderID string   `json:"provider_id"`
	Platform   string   `json:"platform"`
	Models     []string `json:"models"`
	Capacity   int      `json:"capacity"` // 0-100 estimated remaining capacity
}

// TrustPool is the global node registry, stored in GitHub.
type TrustPool struct {
	Version   int        `json:"version"`
	UpdatedAt string     `json:"updated_at"`
	Registry  string     `json:"registry"`
	Nodes     []NodeInfo `json:"nodes"`
}

// GossipMessage is exchanged between nodes during gossip rounds.
type GossipMessage struct {
	Type             string `json:"type"` // "sync", "announce", "score", "heartbeat"
	FromNode         string `json:"from_node"`
	TrustPoolVersion int    `json:"trust_pool_version"`
	ScoreDigest      string `json:"score_digest,omitempty"`
	Timestamp        string `json:"timestamp"`
	Signature        string `json:"signature"`
	Payload          []byte `json:"payload,omitempty"` // optional embedded data
}

// PeerScore is a rating one node gives to another.
type PeerScore struct {
	FromNode     string  `json:"from_node"`
	TargetNode   string  `json:"target_node"`
	Availability float64 `json:"availability"` // 0-100
	Latency      float64 `json:"latency"`      // 0-100
	Accuracy     float64 `json:"accuracy"`     // 0-100
	Comment      string  `json:"comment,omitempty"`
	Timestamp    string  `json:"timestamp"`
	Signature    string  `json:"signature"`
}

// ProviderAnnouncement broadcasts a new/updated provider to the federation.
type ProviderAnnouncement struct {
	NodeID     string   `json:"node_id"`
	ProviderID string   `json:"provider_id"`
	Platform   string   `json:"platform"`
	Models     []string `json:"models"`
	Capacity   int      `json:"capacity"`
	Timestamp  string   `json:"timestamp"`
	Signature  string   `json:"signature"`
}

// RelayRequest is sent to a remote node for provider relay.
type RelayRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Extra    map[string]any `json:"extra,omitempty"`
}

// RelayResponse wraps the relay result.
type RelayResponse struct {
	Success   bool          `json:"success"`
	Data      []byte        `json:"data,omitempty"`      // raw response body
	Error     string        `json:"error,omitempty"`
	Tokens    int           `json:"tokens,omitempty"`
	LatencyMS float64      `json:"latency_ms,omitempty"`
}

// FederationVote records a vote on a pending node join request.
type FederationVote struct {
	VoterNode  string `json:"voter_node"`
	TargetNode string `json:"target_node"`
	Approve    bool   `json:"approve"`
	Comment    string `json:"comment,omitempty"`
	Timestamp  string `json:"timestamp"`
	Signature  string `json:"signature"`
}

// PendingJoinRequest is a node waiting for federation approval.
type PendingJoinRequest struct {
	NodeID       string   `json:"node_id"`
	GitHubUser   string   `json:"github_user"`
	Endpoint     string   `json:"endpoint"`
	PubKey       string   `json:"pub_key"`
	SharedModels []string `json:"shared_models"`
	RequestedAt  string   `json:"requested_at"`
	InviteBy     string   `json:"invite_by,omitempty"`
	Votes        []FederationVote `json:"votes"`
	Status       string   `json:"status"` // pending, approved, rejected
	AutoVerified bool     `json:"auto_verified"` // passed auto health check
}

// CreditTransaction records a credit change.
type CreditTransaction struct {
	ID        string `json:"id"`
	FromNode  string `json:"from_node"` // empty for earning
	ToNode    string `json:"to_node"`   // empty for earning
	Amount    int    `json:"amount"`    // positive=credit, negative=debit
	Reason    string `json:"reason"`    // "relay_call", "invite", "message", etc.
	Timestamp string `json:"timestamp"`
}

// NodeCredits tracks a node's credit balance.
type NodeCredits struct {
	NodeID       string              `json:"node_id"`
	Balance      int                 `json:"balance"`
	TotalEarned  int                 `json:"total_earned"`
	TotalSpent   int                 `json:"total_spent"`
	Transactions []CreditTransaction `json:"transactions,omitempty"`
}

// FederationConfig holds federation-specific configuration.
type FederationConfig struct {
	Enabled          bool   `json:"enabled"`
	NodeID           string `json:"node_id"`
	SeedNode         bool   `json:"seed_node"`
	RelayEnabled     bool   `json:"relay_enabled"`
	MaxConcurrentRelay int  `json:"max_concurrent_relay"`
	RegistryURL      string `json:"registry_url"`      // GitHub raw URL
	RegistryRepo     string `json:"registry_repo"`     // "lisiyu/modelmux"
	GossipIntervalS  int    `json:"gossip_interval_s"` // default 30
	HeartbeatIntervalS int  `json:"heartbeat_interval_s"` // default 60
}
