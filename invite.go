package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// ============================================================
// Phase 2: Signed Invite Chain — 去中心化入网机制
// ============================================================
//
// 邀请码可以通过任何渠道传递：微信、邮件、论坛、QR码
// 所有节点都可以验证签名 → 确认邀请合法
// 天然的抗病毒机制：没有有效邀请就无法入网

// FederationInviteType defines the type of invitation.
type FederationInviteType string

const (
	FederationInviteDirected FederationInviteType = "directed" // bound to specific invitee pubkey
	FederationInvitePublic    FederationInviteType = "public"   // anyone can use (invitee_pub = "*")
	FederationInviteChain     FederationInviteType = "chain"    // invitee can also issue invites
)

// FederationInvite is the signed invitation payload.
type FederationInvite struct {
	NetworkID   string               `json:"network_id"`           // must match genesis hash
	Inviter     string               `json:"inviter"`              // inviter's NodeID
	InviterKey  string               `json:"inviter_key"`          // inviter's public key (base64)
	InviteePub  string               `json:"invitee_pub"`          // invitee's public key, or "*" for public
	InviteeName string               `json:"invitee_name,omitempty"` // optional human-readable name
	Endpoint    string               `json:"endpoint"`             // inviter's endpoint for initial connection
	ExpiresAt   string               `json:"expires_at"`           // RFC3339 expiration time
	Type        FederationInviteType `json:"type"`                 // directed, public, chain
	CreatedAt   string               `json:"created_at"`           // RFC3339 creation time
	Signature   string               `json:"signature"`            // Ed25519 signature (base64)
}

// FederationInvitePayload is the unsigned data that gets signed.
type FederationInvitePayload struct {
	NetworkID   string               `json:"network_id"`
	Inviter     string               `json:"inviter"`
	InviteePub  string               `json:"invitee_pub"`
	InviteeName string               `json:"invitee_name,omitempty"`
	Endpoint    string               `json:"endpoint"`
	ExpiresAt   string               `json:"expires_at"`
	Type        FederationInviteType `json:"type"`
	CreatedAt   string               `json:"created_at"`
}

// inviteManager handles invite creation, verification, and tracking.
type inviteManager struct {
	issued   map[string]*FederationInvite // invite_id → code
	used     map[string]bool        // invite_id → used
	dataDir  string
}

var invMgr *inviteManager

func initInviteManager(dataDir string) {
	invMgr = &inviteManager{
		issued:  make(map[string]*FederationInvite),
		used:    make(map[string]bool),
		dataDir: dataDir,
	}
	invMgr.load()
	slog.Info("invite manager initialized", "issued", len(invMgr.issued), "used", len(invMgr.used))
}

// CreateInvite creates a new signed invite code.
func (m *inviteManager) CreateInvite(inviteePub string, inviteeName string, inviteType FederationInviteType, expiresInHours int) (*FederationInvite, error) {
	if node == nil || !node.IsInitialized() {
		return nil, fmt.Errorf("node not initialized")
	}

	now := time.Now().UTC()
	expires := now.Add(time.Duration(expiresInHours) * time.Hour)

	payload := FederationInvitePayload{
		NetworkID:   NetworkID,
		Inviter:     node.NodeID(),
		InviteePub:  inviteePub,
		InviteeName: inviteeName,
		Endpoint:    node.getEndpoint(),
		ExpiresAt:   expires.Format(time.RFC3339),
		Type:        inviteType,
		CreatedAt:   now.Format(time.RFC3339),
	}

	// Sign the payload
	sig, err := m.signPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to sign invite: %w", err)
	}

	invite := &FederationInvite{
		NetworkID:   payload.NetworkID,
		Inviter:     payload.Inviter,
		InviterKey:  node.PubKeyB64(),
		InviteePub:  payload.InviteePub,
		InviteeName: payload.InviteeName,
		Endpoint:    payload.Endpoint,
		ExpiresAt:   payload.ExpiresAt,
		Type:        payload.Type,
		CreatedAt:   payload.CreatedAt,
		Signature:   base64.StdEncoding.EncodeToString(sig),
	}

	// Generate invite ID from payload hash
	inviteID := m.inviteID(payload)
	m.issued[inviteID] = invite
	m.save()

	slog.Info("invite created",
		"inviter", invite.Inviter,
		"invitee", invite.InviteePub,
		"type", invite.Type,
		"expires", invite.ExpiresAt)

	return invite, nil
}

// VerifyInvite verifies an invite code's signature and validity.
func (m *inviteManager) VerifyInvite(invite *FederationInvite) error {
	// Check network ID
	if invite.NetworkID != NetworkID {
		return fmt.Errorf("network_id mismatch: expected %s, got %s", NetworkID, invite.NetworkID)
	}

	// Check expiration
	expiresAt, err := time.Parse(time.RFC3339, invite.ExpiresAt)
	if err != nil {
		return fmt.Errorf("invalid expires_at: %w", err)
	}
	if time.Now().After(expiresAt) {
		return fmt.Errorf("invite expired at %s", invite.ExpiresAt)
	}

	// Check if already used
	inviteID := m.inviteIDFromCode(invite)
	if m.used[inviteID] {
		// Public and chain invites can be reused
		if invite.Type == FederationInviteDirected {
			return fmt.Errorf("invite already used")
		}
	}

	// Verify signature
	payload := FederationInvitePayload{
		NetworkID:   invite.NetworkID,
		Inviter:     invite.Inviter,
		InviteePub:  invite.InviteePub,
		InviteeName: invite.InviteeName,
		Endpoint:    invite.Endpoint,
		ExpiresAt:   invite.ExpiresAt,
		Type:        invite.Type,
		CreatedAt:   invite.CreatedAt,
	}

	sigBytes, err := base64.StdEncoding.DecodeString(invite.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}

	pubKeyBytes, err := base64.StdEncoding.DecodeString(invite.InviterKey)
	if err != nil {
		return fmt.Errorf("invalid inviter key encoding: %w", err)
	}

	if !verifyPayloadSignature(payload, sigBytes, pubKeyBytes) {
		return fmt.Errorf("signature verification failed")
	}

	return nil
}

// MarkUsed marks an invite as used (for directed invites).
func (m *inviteManager) MarkUsed(invite *FederationInvite) {
	inviteID := m.inviteIDFromCode(invite)
	m.used[inviteID] = true
	m.save()
}

// GetInvites returns all issued invites.
func (m *inviteManager) GetInvites() []*FederationInvite {
	var result []*FederationInvite
	for _, inv := range m.issued {
		result = append(result, inv)
	}
	return result
}

// EncodeInvite encodes an invite code to a portable base64 string.
func EncodeInvite(invite *FederationInvite) (string, error) {
	b, err := json.Marshal(invite)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// DecodeInvite decodes a portable invite code string.
func DecodeInvite(encoded string) (*FederationInvite, error) {
	b, err := base64.URLEncoding.DecodeString(encoded)
	if err != nil {
		// Try standard encoding
		b, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("invalid invite encoding: %w", err)
		}
	}
	var invite FederationInvite
	if err := json.Unmarshal(b, &invite); err != nil {
		return nil, fmt.Errorf("invalid invite format: %w", err)
	}
	return &invite, nil
}

// internal

func (m *inviteManager) signPayload(payload FederationInvitePayload) ([]byte, error) {
	if node == nil {
		return nil, fmt.Errorf("node not initialized")
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(b)
	// Use existing Sign method and decode the base64 result
	sigB64 := node.Sign(hash[:])
	if sigB64 == "" {
		return nil, fmt.Errorf("signing failed")
	}
	return base64.StdEncoding.DecodeString(sigB64)
}

func verifyPayloadSignature(payload FederationInvitePayload, sig, pubKeyBytes []byte) bool {
	b, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	hash := sha256.Sum256(b)
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pubKeyBytes), hash[:], sig)
}

func (m *inviteManager) inviteID(payload FederationInvitePayload) string {
	b, _ := json.Marshal(payload)
	hash := sha256.Sum256(b)
	return "inv-" + hex.EncodeToString(hash[:8])
}

func (m *inviteManager) inviteIDFromCode(invite *FederationInvite) string {
	payload := FederationInvitePayload{
		NetworkID:  invite.NetworkID,
		Inviter:    invite.Inviter,
		InviteePub: invite.InviteePub,
		Endpoint:   invite.Endpoint,
		ExpiresAt:  invite.ExpiresAt,
		Type:       invite.Type,
		CreatedAt:  invite.CreatedAt,
	}
	return m.inviteID(payload)
}

// persist

type inviteData struct {
	Issued map[string]*FederationInvite `json:"issued"`
	Used   map[string]bool        `json:"used"`
}

func (m *inviteManager) save() {
	data := inviteData{Issued: m.issued, Used: m.used}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(m.dataDir+"/invites.json", b, 0644)
}

func (m *inviteManager) load() {
	data, err := os.ReadFile(m.dataDir + "/invites.json")
	if err != nil {
		return
	}
	var d inviteData
	if err := json.Unmarshal(data, &d); err != nil {
		return
	}
	if d.Issued != nil {
		m.issued = d.Issued
	}
	if d.Used != nil {
		m.used = d.Used
	}
}

// NodeIdentity helpers (used by invite signing)

func (n *NodeIdentity) getEndpoint() string {
	endpoint := cfg.Get("federation_endpoint", "")
	if endpoint == "" {
		// Check tunnel URL first
		tunnelURL := cfg.Get("tunnel_url", "")
		if tunnelURL != "" {
			return tunnelURL
		}
		port := cfg.Get("service_port", "8000")
		hostname, _ := os.Hostname()
		endpoint = fmt.Sprintf("http://%s:%s", hostname, port)
	}
	return endpoint
}
