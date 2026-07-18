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
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Enabled       bool            `json:"enabled"`
	EnabledByKeys map[string]bool `json:"enabled_by_keys,omitempty"` // per-key enabled state: keyID -> enabled
	AvailableKeys []string        `json:"available_keys,omitempty"`  // keyIDs that have this model available
}

// APIKeyConfig represents a single API key with its own quota and access control.
type APIKeyConfig struct {
	ID            string `json:"id"`              // unique identifier
	Alias         string `json:"alias,omitempty"` // human-readable alias (optional)
	Key           string `json:"key"`             // API key (encrypted at rest)
	Quota         int64  `json:"quota"`           // total quota (tokens), 0=unlimited
	QuotaDaily    int64  `json:"quota_daily"`     // daily quota limit, 0=unlimited
	QuotaMonthly  int64  `json:"quota_monthly"`   // monthly quota limit, 0=unlimited
	Used          int64  `json:"used"`            // used quota (total)
	UsedDaily     int64  `json:"used_daily"`      // used today
	UsedMonthly   int64  `json:"used_monthly"`    // used this month
	LastDailyReset   string `json:"last_daily_reset,omitempty"`   // date of last daily reset (YYYY-MM-DD)
	LastMonthlyReset string `json:"last_monthly_reset,omitempty"` // month of last monthly reset (YYYY-MM)
	AccessControl string `json:"access_control"`  // "private" | "shared" | "public"
	Enabled       bool   `json:"enabled"`         // whether this key is enabled
	Priority      int    `json:"priority"`        // priority for rotation (higher = preferred)
	ExpiresAt     string `json:"expires_at"`      // expiration time (optional, RFC3339)
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type Provider struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"` // "openai_compatible", "coze", "sider", "anthropic", "web_session"
	BaseURL     string     `json:"base_url"`
	APIKey      string     `json:"api_key,omitempty"` // deprecated: use APIKeys instead
	Enabled     bool       `json:"enabled"`
	Models      []ModelDef `json:"models"`
	Priority    int        `json:"priority"`
	TokenLimit  int64      `json:"token_limit,omitempty"` // monthly token budget, 0=unlimited
	RateLimitEnabled  bool  `json:"rate_limit_enabled,omitempty"`    // master toggle for rate limiting
	DailyRequestLimit int64 `json:"daily_request_limit,omitempty"` // max requests per day, 0=unlimited
	RateLimitPerMin     int   `json:"rate_limit_per_min,omitempty"`     // requests per minute, 0=unlimited
	PrivateTokensDaily   int64 `json:"private_tokens_daily,omitempty"`   // private pool max tokens per day, 0=unlimited
	PrivateQuotaMonthly int64 `json:"private_quota_monthly,omitempty"`  // private key quota limit per month, 0=unlimited
	PrivateQuotaTotal   int64 `json:"private_quota_total,omitempty"`    // private key total quota cap, 0=unlimited
	SharedTokensDaily    int64 `json:"shared_tokens_daily,omitempty"`    // shared pool max tokens per day, 0=unlimited
	SharedQuotaMonthly  int64 `json:"shared_quota_monthly,omitempty"`   // shared key quota limit per month, 0=unlimited
	SharedQuotaTotal    int64 `json:"shared_quota_total,omitempty"`     // shared key total quota cap, 0=unlimited
	Description string     `json:"description,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	APIKeyURL   string     `json:"api_key_url,omitempty"`
	Proxy                 string     `json:"proxy,omitempty"` // http://, socks5://, or vmess:// link
	HealthCheckEndpoint   string     `json:"health_check_endpoint,omitempty"` // "/models" (default), "/chat/completions", or custom
	Owner                 string     `json:"owner,omitempty"` // consumer ID; empty = admin/system
	AccessControl ProviderAccessControl `json:"access_control"`

	// Multi API key support
	APIKeys     []APIKeyConfig `json:"api_keys,omitempty"` // multiple API keys

	// Web session template (for web_session type providers)
	WebSession *WebSessionConfig `json:"web_session,omitempty"`

	CreatedAt   string     `json:"created_at,omitempty"`
	UpdatedAt   string     `json:"updated_at,omitempty"`
}

// WebSessionConfig defines the template for web-session-based providers
// (platforms without official API, using browser session tokens).
type WebSessionConfig struct {
	LoginURL        string            `json:"login_url"`                   // login page URL for token extraction guide
	APIEndpoint     string            `json:"api_endpoint"`                // internal API endpoint for chat requests
	AuthMode        string            `json:"auth_mode"`                   // "bearer" or "cookie"
	TokenCookieName string            `json:"token_cookie_name,omitempty"` // cookie name (for cookie auth mode)
	TokenPrefix     string            `json:"token_prefix,omitempty"`      // prefix added to token (e.g. "Bearer ")
	ExtraHeaders    map[string]string `json:"extra_headers,omitempty"`     // required HTTP headers
	PromptField     string            `json:"prompt_field"`                // body field name for formatted messages
	ModelField      string            `json:"model_field,omitempty"`       // body field name for model
	StreamField     string            `json:"stream_field,omitempty"`      // body field name for stream bool
	ExtraBody       map[string]any    `json:"extra_body,omitempty"`        // extra fixed body fields
	MessageFormat   string            `json:"message_format"`              // "prefix_role" or "openai"
	MessageSep      string            `json:"message_sep,omitempty"`       // separator between messages (default: \n)
	ResponseType    string            `json:"response_type"`               // "sse" or "json"
	TextPath        string            `json:"text_path"`                   // JSON path to text in response (e.g. "data.text")
	DoneMarker      string            `json:"done_marker,omitempty"`       // SSE done marker (default: [DONE])
	ExtraCookies    string            `json:"extra_cookies,omitempty"`     // raw cookie string from browser (e.g. cf_clearance, __cf_bm)
}

// ProviderAccessControl defines which key types can access a provider (v2.0).
type ProviderAccessControl struct {
	// ShareToPool controls whether this provider's resources are shared to the
	// global public pool accessible via sk-openmodelpool-com-github-lisiyu-openmodelpool-public-key-v1 keys.
	// Default: true — providers are shared to the pool when the node joins the network.
	// Set to false to opt out of the shared pool (admin/proxy keys are unaffected).
	ShareToPool bool `json:"share_to_pool"`

	// GuestPoolPercent defines what percentage of shared key quota is allocated
	// to the Guest pool (0-100). The remainder goes to the Public pool.
	// Default: 50.
	GuestPoolPercent int `json:"guest_pool_percent,omitempty"`

	// MigrationAllowPublic is a legacy field for backward compatibility.
	// It is read from old JSON data with "allow_public" and migrated to ShareToPool.
	MigrationAllowPublic *bool `json:"allow_public,omitempty"`
}

// UnmarshalJSON handles backward-compatible migration from allow_public to share_to_pool.
func (ac *ProviderAccessControl) UnmarshalJSON(data []byte) error {
	// Use an alias to avoid infinite recursion
	type Alias ProviderAccessControl
	aux := &struct{ *Alias }{Alias: (*Alias)(ac)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	// Migrate legacy allow_public → share_to_pool
	if ac.MigrationAllowPublic != nil && !ac.ShareToPool {
		ac.ShareToPool = *ac.MigrationAllowPublic
		ac.MigrationAllowPublic = nil // clear after migration
	}
	return nil
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
	// Mask API keys in the multi-key array
	if len(safe.APIKeys) > 0 {
		maskedKeys := make([]APIKeyConfig, len(safe.APIKeys))
		for i, k := range safe.APIKeys {
			maskedKeys[i] = k
			if len(maskedKeys[i].Key) > 8 {
				maskedKeys[i].Key = maskedKeys[i].Key[:4] + "..." + maskedKeys[i].Key[len(maskedKeys[i].Key)-4:]
			} else if maskedKeys[i].Key != "" {
				maskedKeys[i].Key = "***"
			} else {
				maskedKeys[i].Key = ""
			}
		}
		safe.APIKeys = maskedKeys
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

// Collaborator represents a collaborator account with limited admin access
type Collaborator struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	GuestKey     string `json:"guest_key"`        // associated guest key
	CreatedAt    string `json:"created_at"`
	LastLogin    string `json:"last_login,omitempty"`
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
	JWTSecret        string     `json:"jwt_secret"`
	JWTRefreshSecret string     `json:"jwt_refresh_secret"`
	SMTP      SMTPConfig `json:"smtp"`
	Reset     *ResetToken `json:"reset_token,omitempty"`
	Initialized bool     `json:"initialized"`
	// P0-2: Independent reset code (replaces Proxy API Key reuse for password reset)
	ResetCodeHash   string `json:"reset_code_hash,omitempty"`
	ResetCodeExpires string `json:"reset_code_expires,omitempty"`
	Collaborators []Collaborator `json:"collaborators,omitempty"`
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
	FailedKeyCount   int     `json:"failed_key_count"` // number of keys that failed health check
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
	SeedNode       bool               `json:"seed_node,omitempty"` // deprecated: use Capabilities.CanSeed instead
	Reputation     int                `json:"reputation"`
	Version        string             `json:"version"`
	InviteBy       string             `json:"invite_by,omitempty"`
	TokenBudget    int64              `json:"token_budget"`    // monthly token budget declaration (0 = unlimited)
	TokenUsed      int64              `json:"token_used"`      // tokens used this month
	Addresses      []string           `json:"addresses,omitempty"` // multi-address support (P2P)
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

// FederationConfig holds federation-specific configuration.
type FederationConfig struct {
	Enabled          bool   `json:"enabled"`
	NodeID           string `json:"node_id"`
	SeedNode         bool   `json:"seed_node,omitempty"`          // deprecated: all nodes are seeds in unified Peer model
	RelayEnabled     bool   `json:"relay_enabled"`                // deprecated: relay is a capability, not a type toggle
	MaxConcurrentRelay int  `json:"max_concurrent_relay"`
	RegistryURL      string `json:"registry_url"`      // GitHub raw URL
	RegistryRepo     string `json:"registry_repo"`     // "lisiyu/openmodelpool"
	GossipIntervalS  int    `json:"gossip_interval_s"` // default 30
	HeartbeatIntervalS int  `json:"heartbeat_interval_s"` // default 60
}

// ============================================================
// Unified Peer Model (v3.1)
// ============================================================
//
// Design Principles:
// 1. All deployed nodes automatically form a P2P network — unified Peer model
// 2. Nodes with public access join the network by default
// 3. Resource sharing is controlled by the independent share_to_pool toggle
// 4. Node roles are determined by capability declarations, not preset types

// PeerCapabilities declares what a peer can do in the network.
// Roles are emergent from capabilities, not from preset node types.
type PeerCapabilities struct {
	// Providers lists the provider platforms this peer can serve (e.g., ["openai", "anthropic"])
	Providers []string `json:"providers"`
	// Bandwidth indicates the peer's network capacity (e.g., "100Mbps", "1Gbps")
	Bandwidth string `json:"bandwidth,omitempty"`
	// CanRelay indicates whether this peer can relay requests for others
	CanRelay bool `json:"can_relay"`
	// CanSeed indicates whether this peer can serve as a discovery seed
	CanSeed bool `json:"can_seed"`
}

// Peer represents a unified node in the P2P network.
// Every deployed node is a Peer — there are no distinct "node types".
type Peer struct {
	// Identity
	PeerID       string           `json:"peer_id"`
	NodeID       string           `json:"node_id"`       // legacy alias for PeerID
	Name         string           `json:"name"`
	Endpoint     string           `json:"endpoint"`
	PubKey       string           `json:"pub_key"`       // ed25519 base64

	// Capabilities (replaces preset node types)
	Capabilities PeerCapabilities `json:"capabilities"`

	// Network presence
	Addresses    []string         `json:"addresses,omitempty"`
	Status       string           `json:"status"`        // active, inactive, suspended
	JoinedAt     string           `json:"joined_at"`
	LastSeen     string           `json:"last_seen"`
	Version      string           `json:"version"`

	// Shared resources (only populated when share_to_pool=true)
	SharedModels     []string          `json:"shared_models,omitempty"`
	SharedProviders  []SharedProvider  `json:"shared_providers,omitempty"`

	// Metrics
	Reputation   int              `json:"reputation"`
	TokenBudget  int64            `json:"token_budget"`
	TokenUsed    int64            `json:"token_used"`

	// Social
	InviteBy     string           `json:"invite_by,omitempty"`
	GitHubUser   string           `json:"github_user,omitempty"`
	GitHubID     int64            `json:"github_id,omitempty"`
}

// ToNodeInfo converts a Peer to the legacy NodeInfo format for backward compatibility.
func (p *Peer) ToNodeInfo() NodeInfo {
	return NodeInfo{
		NodeID:          firstNonEmpty(p.PeerID, p.NodeID),
		GitHubUser:      p.GitHubUser,
		GitHubID:        p.GitHubID,
		Endpoint:        p.Endpoint,
		PubKey:          p.PubKey,
		SharedModels:    p.SharedModels,
		SharedProviders: p.SharedProviders,
		JoinedAt:        p.JoinedAt,
		LastSeen:        p.LastSeen,
		Status:          p.Status,
		SeedNode:        p.Capabilities.CanSeed, // backward compat: derive from capability
		Reputation:      p.Reputation,
		Version:         p.Version,
		InviteBy:        p.InviteBy,
		TokenBudget:     p.TokenBudget,
		TokenUsed:       p.TokenUsed,
	}
}

// NodeInfoToPeer converts a legacy NodeInfo to the unified Peer format.
func NodeInfoToPeer(n NodeInfo) Peer {
	return Peer{
		PeerID:          n.NodeID,
		NodeID:          n.NodeID,
		Name:            "",
		Endpoint:        n.Endpoint,
		PubKey:          n.PubKey,
		Capabilities: PeerCapabilities{
			CanRelay: true,        // all nodes can relay by default
			CanSeed:  n.SeedNode,  // backward compat: SeedNode → CanSeed capability
		},
		Status:          n.Status,
		JoinedAt:        n.JoinedAt,
		LastSeen:        n.LastSeen,
		Version:         n.Version,
		SharedModels:    n.SharedModels,
		SharedProviders: n.SharedProviders,
		Reputation:      n.Reputation,
		TokenBudget:     n.TokenBudget,
		TokenUsed:       n.TokenUsed,
		InviteBy:        n.InviteBy,
		GitHubUser:      n.GitHubUser,
		GitHubID:        n.GitHubID,
	}
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
