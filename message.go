package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	messageCost   = 5
	maxInboxSize  = 500
	maxOutboxSize = 500
	msgFile       = "messages.json"
)

// FederationMessage represents a peer-to-peer message between federation nodes.
type FederationMessage struct {
	ID        string `json:"id"`
	FromNode  string `json:"from_node"`
	ToNode    string `json:"to_node"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	MsgType   string `json:"msg_type"` // "request", "collaboration", "system", "general"
	Timestamp string `json:"timestamp"`
	Signature string `json:"signature"`
	Encrypted bool   `json:"encrypted"`
	Read      bool   `json:"read"`
}

// MessageManager manages inbox and outbox for federation P2P messaging.
type MessageManager struct {
	mu      sync.RWMutex
	inbox   []FederationMessage
	outbox  []FederationMessage
	dataDir string
}

var msgMgr *MessageManager

// initMessages creates the MessageManager and loads persisted messages from disk.
func initMessages(dataDir string) {
	msgMgr = &MessageManager{
		inbox:   make([]FederationMessage, 0),
		outbox:  make([]FederationMessage, 0),
		dataDir: dataDir,
	}
	msgMgr.load()
	slog.Info("message manager initialized", "inbox", len(msgMgr.inbox), "outbox", len(msgMgr.outbox))
}

// generateMsgID generates a random 16-byte hex-encoded message ID.
func generateMsgID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate message id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// validMsgType checks whether the given message type is allowed.
func validMsgType(t string) bool {
	switch t {
	case "request", "collaboration", "system", "general":
		return true
	}
	return false
}

// SendMessage validates the recipient, spends credits, signs the message,
// delivers it to the remote node via HTTP POST, and persists it to the outbox.
// If the recipient is ourselves the message is also delivered locally.
func (m *MessageManager) SendMessage(toNodeID, subject, body, msgType string) error {
	if msgType == "" {
		msgType = "general"
	}
	if !validMsgType(msgType) {
		return fmt.Errorf("invalid message type: %s", msgType)
	}

	subject = strings.TrimSpace(subject)
	body = strings.TrimSpace(body)
	if subject == "" {
		return fmt.Errorf("subject must not be empty")
	}
	if body == "" {
		return fmt.Errorf("body must not be empty")
	}

	// Check if recipient exists in federation
	targetNode, _ := fed.GetNode(toNodeID)
	if targetNode == nil {
		return fmt.Errorf("unknown recipient node: %s", toNodeID)
	}

	// v2.0: Messages are free (no credit system)

	// Generate message ID
	msgID, err := generateMsgID()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	msg := FederationMessage{
		ID:        msgID,
		FromNode:  node.NodeID(),
		ToNode:    toNodeID,
		Subject:   subject,
		Body:      body,
		MsgType:   msgType,
		Timestamp: now,
		Encrypted: false,
		Read:      false,
	}

	// Sign the message payload
	signPayload := fmt.Sprintf("%s|%s|%s|%s|%s|%s", msg.ID, msg.FromNode, msg.ToNode, msg.Subject, msg.Body, msg.Timestamp)
	msg.Signature = node.Sign([]byte(signPayload))

	// Deliver locally if recipient is ourselves
	if toNodeID == node.NodeID() {
		m.mu.Lock()
		m.inbox = append(m.inbox, msg)
		m.trimInbox()
		m.save()
		m.mu.Unlock()
		slog.Info("message delivered locally", "msg_id", msg.ID)
		return nil
	}

	// Send to remote node via HTTP POST
	endpoint := strings.TrimRight(targetNode.Endpoint, "/")
	url := endpoint + "/federation/message"

	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(msgJSON)))
	if err != nil {
		return fmt.Errorf("create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Node-ID", node.NodeID())

	// Sign the transport for additional verification
	reqSig := node.Sign([]byte(url + "|" + string(msgJSON)))
	httpReq.Header.Set("X-Signature", reqSig)

	client := GetSharedHTTPClient()
	resp, err := client.Do(httpReq)
	if err != nil {
		slog.Error("failed to deliver message to remote node", "to", toNodeID, "url", url, "error", err)
		// Save to outbox to record the attempt even on transport failure
		m.mu.Lock()
		m.outbox = append(m.outbox, msg)
		m.trimOutbox()
		m.save()
		m.mu.Unlock()
		return fmt.Errorf("deliver message to %s: %w", toNodeID, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		slog.Warn("remote node rejected message", "to", toNodeID, "status", resp.StatusCode)
		m.mu.Lock()
		m.outbox = append(m.outbox, msg)
		m.trimOutbox()
		m.save()
		m.mu.Unlock()
		return fmt.Errorf("remote node %s responded with status %d", toNodeID, resp.StatusCode)
	}

	// Save to outbox
	m.mu.Lock()
	m.outbox = append(m.outbox, msg)
	m.trimOutbox()
	m.save()
	m.mu.Unlock()

	slog.Info("message sent successfully", "msg_id", msg.ID, "to", toNodeID)
	return nil
}

// ReceiveMessage verifies the sender's signature and adds the message to the inbox.
func (m *MessageManager) ReceiveMessage(msg FederationMessage) error {
	if msg.ID == "" {
		return fmt.Errorf("message id is required")
	}
	if msg.FromNode == "" {
		return fmt.Errorf("from_node is required")
	}
	if msg.ToNode == "" {
		return fmt.Errorf("to_node is required")
	}
	if msg.ToNode != node.NodeID() {
		return fmt.Errorf("message not addressed to this node (to=%s, us=%s)", msg.ToNode, node.NodeID())
	}
	if msg.Signature == "" {
		return fmt.Errorf("message signature is required")
	}

	// Look up sender's public key from federation trust pool
	senderNode, _ := fed.GetNode(msg.FromNode)
	if senderNode == nil {
		return fmt.Errorf("unknown sender node: %s", msg.FromNode)
	}

	// Verify signature
	signPayload := fmt.Sprintf("%s|%s|%s|%s|%s|%s", msg.ID, msg.FromNode, msg.ToNode, msg.Subject, msg.Body, msg.Timestamp)
	if !verifyMessageSignature(senderNode.PubKey, signPayload, msg.Signature) {
		return fmt.Errorf("invalid message signature from node %s", msg.FromNode)
	}

	// Check for duplicate
	m.mu.RLock()
	for _, existing := range m.inbox {
		if existing.ID == msg.ID {
			m.mu.RUnlock()
			slog.Debug("duplicate message ignored", "msg_id", msg.ID)
			return nil
		}
	}
	m.mu.RUnlock()

	// Add to inbox
	m.mu.Lock()
	msg.Read = false
	m.inbox = append(m.inbox, msg)
	m.trimInbox()
	m.save()
	m.mu.Unlock()

	slog.Info("message received", "msg_id", msg.ID, "from", msg.FromNode, "subject", msg.Subject)
	return nil
}

// verifyMessageSignature verifies the message signature using the sender's public key.
func verifyMessageSignature(pubKeyB64, payload, signature string) bool {
	if pubKeyB64 == "" || signature == "" {
		return false
	}

	// Decode the base64 public key
	pubKeyDER, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil {
		slog.Error("failed to decode public key", "error", err)
		return false
	}

	// Try to parse as PKIX public key
	pub, err := x509.ParsePKIXPublicKey(pubKeyDER)
	if err != nil {
		// Maybe it's raw ed25519 bytes (32 bytes)
		if len(pubKeyDER) == ed25519.PublicKeySize {
			pub = ed25519.PublicKey(pubKeyDER)
		} else {
			slog.Error("failed to parse public key", "error", err)
			return false
		}
	}

	edKey, ok := pub.(ed25519.PublicKey)
	if !ok {
		slog.Error("public key is not ed25519")
		return false
	}

	// Decode the base64 signature
	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		slog.Error("failed to decode signature", "error", err)
		return false
	}

	return ed25519.Verify(edKey, []byte(payload), sigBytes)
}

// GetInbox returns the most recent inbox messages, up to limit.
func (m *MessageManager) GetInbox(limit int) []FederationMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := len(m.inbox)
	if total == 0 {
		return []FederationMessage{}
	}

	if limit <= 0 || limit > total {
		limit = total
	}

	// Return most recent first
	result := make([]FederationMessage, limit)
	for i := 0; i < limit; i++ {
		result[i] = m.inbox[total-1-i]
	}
	return result
}

// GetOutbox returns the most recent outbox messages, up to limit.
func (m *MessageManager) GetOutbox(limit int) []FederationMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := len(m.outbox)
	if total == 0 {
		return []FederationMessage{}
	}

	if limit <= 0 || limit > total {
		limit = total
	}

	// Return most recent first
	result := make([]FederationMessage, limit)
	for i := 0; i < limit; i++ {
		result[i] = m.outbox[total-1-i]
	}
	return result
}

// MarkAsRead marks a message in the inbox as read by its ID.
func (m *MessageManager) MarkAsRead(msgID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.inbox {
		if m.inbox[i].ID == msgID {
			m.inbox[i].Read = true
			m.save()
			slog.Debug("message marked as read", "msg_id", msgID)
			return true
		}
	}
	return false
}

// GetUnreadCount returns the number of unread messages in the inbox.
func (m *MessageManager) GetUnreadCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, msg := range m.inbox {
		if !msg.Read {
			count++
		}
	}
	return count
}

// trimInbox ensures inbox does not exceed maxInboxSize, removing oldest first.
func (m *MessageManager) trimInbox() {
	if len(m.inbox) > maxInboxSize {
		sort.Slice(m.inbox, func(i, j int) bool {
			return m.inbox[i].Timestamp < m.inbox[j].Timestamp
		})
		m.inbox = m.inbox[len(m.inbox)-maxInboxSize:]
	}
}

// trimOutbox ensures outbox does not exceed maxOutboxSize, removing oldest first.
func (m *MessageManager) trimOutbox() {
	if len(m.outbox) > maxOutboxSize {
		sort.Slice(m.outbox, func(i, j int) bool {
			return m.outbox[i].Timestamp < m.outbox[j].Timestamp
		})
		m.outbox = m.outbox[len(m.outbox)-maxOutboxSize:]
	}
}

// messagesData is the on-disk JSON structure.
type messagesData struct {
	Inbox  []FederationMessage `json:"inbox"`
	Outbox []FederationMessage `json:"outbox"`
}

// save persists inbox and outbox to messages.json.
// Must be called with m.mu held.
func (m *MessageManager) save() {
	path := filepath.Join(m.dataDir, msgFile)
	data := messagesData{
		Inbox:  m.inbox,
		Outbox: m.outbox,
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		slog.Error("failed to marshal messages", "error", err)
		return
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		slog.Error("failed to write messages file", "path", path, "error", err)
	}
}

// load reads messages.json from disk and populates inbox/outbox.
func (m *MessageManager) load() {
	path := filepath.Join(m.dataDir, msgFile)
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("failed to read messages file", "path", path, "error", err)
		}
		return
	}

	var data messagesData
	if err := json.Unmarshal(b, &data); err != nil {
		slog.Error("failed to unmarshal messages file", "path", path, "error", err)
		return
	}

	if data.Inbox != nil {
		m.inbox = data.Inbox
	}
	if data.Outbox != nil {
		m.outbox = data.Outbox
	}
}

// ---------------------------------------------------------------------------
// HTTP Handlers
// ---------------------------------------------------------------------------

// sendMessageRequest is the JSON body for POST /federation/message/send.
type sendMessageRequest struct {
	ToNode  string `json:"to_node"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	MsgType string `json:"msg_type"`
}

// handleSendMessage is the HTTP handler for POST /federation/message/send.
// Requires admin authentication (withAuth).
func handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req sendMessageRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.ToNode == "" {
		writeError(w, http.StatusBadRequest, "to_node is required")
		return
	}

	if err := msgMgr.SendMessage(req.ToNode, req.Subject, req.Body, req.MsgType); err != nil {
		slog.Warn("send message failed", "to", req.ToNode, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "sent",
		"message": "message sent successfully",
	})
}

// handleReceiveMessage is the HTTP handler for POST /federation/message.
// Receives an incoming message from another federation node.
func handleReceiveMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	senderNodeID := r.Header.Get("X-Node-ID")

	var msg FederationMessage
	if err := readJSON(r, &msg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid message body: "+err.Error())
		return
	}

	// Use header node ID as fallback for from_node
	if senderNodeID != "" && msg.FromNode == "" {
		msg.FromNode = senderNodeID
	}

	// Ensure to_node is us
	msg.ToNode = node.NodeID()

	if err := msgMgr.ReceiveMessage(msg); err != nil {
		slog.Warn("receive message rejected", "from", senderNodeID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "received",
	})
}

// handleGetInbox is the HTTP handler for GET /federation/messages/inbox.
func handleGetInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	messages := msgMgr.GetInbox(limit)
	unread := msgMgr.GetUnreadCount()

	writeJSON(w, http.StatusOK, map[string]any{
		"messages": messages,
		"total":    len(messages),
		"unread":   unread,
	})
}

// handleGetOutbox is the HTTP handler for GET /federation/messages/outbox.
func handleGetOutbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	messages := msgMgr.GetOutbox(limit)

	writeJSON(w, http.StatusOK, map[string]any{
		"messages": messages,
		"total":    len(messages),
	})
}

// handleMarkAsRead is the HTTP handler for POST /federation/messages/read.
func handleMarkAsRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		MessageID string `json:"message_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MessageID == "" {
		writeError(w, http.StatusBadRequest, "message_id is required")
		return
	}

	if !msgMgr.MarkAsRead(req.MessageID) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"status": "not_found",
			"error":  "message not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}
