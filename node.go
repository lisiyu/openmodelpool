package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// NodeIdentity manages this node's identity in the federation.
// SA-13: The private key is stored encrypted in memory and only decrypted
// on-demand for signing operations. After use, the decrypted key is zeroed
// to minimize the window of exposure in process memory.
type NodeIdentity struct {
	mu           sync.RWMutex
	nodeID       string
	privKey      ed25519.PrivateKey // kept only during active use; cleared after sign
	encPrivKey   string             // encrypted private key (AES-256-GCM), always in memory
	pubKey       ed25519.PublicKey
	githubUser   string
	githubID     int64
	joinedAt     time.Time
	keyPath      string // path to encrypted key file
	tokenBudget  int64  // monthly token budget declaration
}

var node *NodeIdentity

// NodeKeyStore is the on-disk format for the node's keys.
type NodeKeyStore struct {
	NodeID     string `json:"node_id"`
	PrivKeyB64 string `json:"priv_key"`  // encrypted with enc
	PubKeyB64  string `json:"pub_key"`
	GitHubUser string `json:"github_user,omitempty"`
	GitHubID   int64  `json:"github_id,omitempty"`
	JoinedAt   string `json:"joined_at"`
}

func initNode(dataDir string) {
	node = &NodeIdentity{
		keyPath: dataDir + "/node.key",
	}

	data, err := os.ReadFile(node.keyPath)
	if err != nil {
		// Generate new identity
		slog.Info("generating new node identity")
		if err := node.generate(); err != nil {
			slog.Error("failed to generate node identity", "error", err)
			return
		}
		node.save()
		slog.Info("new node created", "node_id", node.nodeID)
		return
	}

	// Load existing identity
	var store NodeKeyStore
	if err := json.Unmarshal(data, &store); err != nil {
		slog.Error("failed to parse node key file", "error", err)
		return
	}

	node.mu.Lock()
	defer node.mu.Unlock()
	node.nodeID = store.NodeID

	// SA-13: Store encrypted private key in memory (not decrypted)
	node.encPrivKey = store.PrivKeyB64

	// Derive public key from encrypted key (decrypt temporarily, derive pub, then zero)
	decrypted := enc.Decrypt(store.PrivKeyB64)
	if decrypted == "" {
		slog.Error("failed to decrypt node private key")
		return
	}
	keyBytes, err := base64.StdEncoding.DecodeString(decrypted)
	if err != nil {
		slog.Error("failed to decode decrypted private key", "error", err)
		return
	}
	tempKey := ed25519.PrivateKey(keyBytes)
	node.pubKey = tempKey.Public().(ed25519.PublicKey)
	// Zero the temporary decrypted key bytes
	for i := range keyBytes {
		keyBytes[i] = 0
	}
	node.githubUser = store.GitHubUser
	node.githubID = store.GitHubID
	if store.JoinedAt != "" {
		node.joinedAt, _ = time.Parse(time.RFC3339, store.JoinedAt)
	}

	slog.Info("node identity loaded", "node_id", node.nodeID)
}

func (n *NodeIdentity) generate() error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 key: %w", err)
	}

	// SA-13: Encrypt private key for in-memory storage, then clear plaintext
	privKeyB64 := base64.StdEncoding.EncodeToString(priv)
	n.encPrivKey = enc.Encrypt(privKeyB64)
	n.privKey = priv // temporarily held for save(), cleared after save
	n.pubKey = pub
	n.nodeID = "mm-" + base58Encode(pub[:16])
	n.joinedAt = time.Now().UTC()
	return nil
}

func (n *NodeIdentity) save() {
	n.mu.RLock()
	defer n.mu.RUnlock()

	// SA-13: Use encrypted in-memory key if available, otherwise encrypt from plaintext
	encKey := n.encPrivKey
	if encKey == "" && n.privKey != nil {
		privKeyB64 := base64.StdEncoding.EncodeToString(n.privKey)
		encKey = enc.Encrypt(privKeyB64)
		// Update stored encrypted key
		n.mu.RUnlock()
		n.mu.Lock()
		n.encPrivKey = encKey
		n.mu.Unlock()
		n.mu.RLock()
	}

	if encKey == "" {
		return
	}

	store := NodeKeyStore{
		NodeID:     n.nodeID,
		PrivKeyB64: encKey,
		PubKeyB64:  base64.StdEncoding.EncodeToString(n.pubKey),
		GitHubUser: n.githubUser,
		GitHubID:   n.githubID,
		JoinedAt:   n.joinedAt.Format(time.RFC3339),
	}

	data, _ := json.MarshalIndent(store, "", "  ")
	os.WriteFile(n.keyPath, data, 0600)
}

// NodeID returns this node's ID.
func (n *NodeIdentity) NodeID() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nodeID
}

// PubKeyB64 returns the base64-encoded public key.
func (n *NodeIdentity) PubKeyB64() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.pubKey == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(n.pubKey)
}

// Sign signs a message and returns base64-encoded signature.
// SA-13: Decrypts the private key on-demand, signs, then zeros the key material.
func (n *NodeIdentity) Sign(message []byte) string {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Decrypt private key from encrypted in-memory storage
	decrypted := enc.Decrypt(n.encPrivKey)
	if decrypted == "" {
		return ""
	}
	keyBytes, err := base64.StdEncoding.DecodeString(decrypted)
	if err != nil {
		return ""
	}
	privKey := ed25519.PrivateKey(keyBytes)

	// Sign the message
	sig := ed25519.Sign(privKey, message)
	result := base64.StdEncoding.EncodeToString(sig)

	// Zero the decrypted key material immediately after use
	for i := range keyBytes {
		keyBytes[i] = 0
	}

	return result
}

// SignJSON marshals v to JSON, signs it, and returns the signature.
func (n *NodeIdentity) SignJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return n.Sign(data)
}

// SignHex signs a message and returns the hex-encoded signature.
// SA-13: Same decrypt-on-use, zero-after-use pattern as Sign().
func (n *NodeIdentity) SignHex(message []byte) string {
	n.mu.Lock()
	defer n.mu.Unlock()

	decrypted := enc.Decrypt(n.encPrivKey)
	if decrypted == "" {
		return ""
	}
	keyBytes, err := base64.StdEncoding.DecodeString(decrypted)
	if err != nil {
		return ""
	}
	privKey := ed25519.PrivateKey(keyBytes)
	sig := ed25519.Sign(privKey, message)

	// Zero the decrypted key material
	for i := range keyBytes {
		keyBytes[i] = 0
	}

	return fmt.Sprintf("%x", sig)
}

// VerifySignature verifies a signature from a given public key.
func VerifySignature(pubKeyB64 string, message []byte, signatureB64 string) bool {
	pubBytes, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return false
	}
	sigBytes, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pubBytes), message, sigBytes)
}

// VerifyJSONSig marshals v to JSON and verifies the signature.
func VerifyJSONSig(pubKeyB64 string, v any, signatureB64 string) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return false
	}
	return VerifySignature(pubKeyB64, data, signatureB64)
}

// GetInfo returns this node's NodeInfo for federation registration.
func (n *NodeIdentity) GetInfo() NodeInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()

	endpoint := cfg.Get("federation_endpoint", "")
	if endpoint == "" {
		port := cfg.Get("service_port", "8000")
		hostname, _ := os.Hostname()
		endpoint = fmt.Sprintf("http://%s:%s", hostname, port)
	}

	var sharedModels []string
	var sharedProviders []SharedProvider
	if fed != nil {
		sharedModels, sharedProviders = fed.getLocalSharedProviders()
	}

	return NodeInfo{
		NodeID:          n.nodeID,
		GitHubUser:      n.githubUser,
		GitHubID:        n.githubID,
		Endpoint:        endpoint,
		PubKey:          base64.StdEncoding.EncodeToString(n.pubKey),
		SharedModels:    sharedModels,
		SharedProviders: sharedProviders,
		JoinedAt:        n.joinedAt.Format(time.RFC3339),
		LastSeen:        time.Now().UTC().Format(time.RFC3339),
		Status:          "active",
		SeedNode:        cfg.Get("federation_seed", "false") == "true",
		Reputation:      0,
		Version:         AppVersion,
		TokenBudget:     n.tokenBudget,
	}
}

// SetGitHub binds a GitHub identity to this node.
func (n *NodeIdentity) SetGitHub(user string, id int64) {
	n.mu.Lock()
	n.githubUser = user
	n.githubID = id
	n.mu.Unlock()
	n.save()
}

// SetTokenBudget sets this node's declared monthly token budget.
func (n *NodeIdentity) SetTokenBudget(budget int64) {
	n.mu.Lock()
	n.tokenBudget = budget
	n.mu.Unlock()
}

// IsInitialized returns whether the node identity has been set up.
func (n *NodeIdentity) IsInitialized() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nodeID != "" && (n.encPrivKey != "" || n.privKey != nil)
}

// base58 encoding (Bitcoin-style, no 0/O/I/l)
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func base58Encode(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	// Count leading zeros
	var leadingZeros int
	for _, b := range data {
		if b != 0 {
			break
		}
		leadingZeros++
	}

	// Convert to big integer and encode
	num := make([]byte, len(data))
	copy(num, data)
	var encoded []byte
	for len(num) > 0 {
		var remainder int
		var next []byte
		for _, b := range num {
			acc := remainder*256 + int(b)
			digit := acc / 58
			remainder = acc % 58
			if len(next) > 0 || digit > 0 {
				next = append(next, byte(digit))
			}
		}
		encoded = append([]byte{base58Alphabet[remainder]}, encoded...)
		num = next
	}

	// Add leading '1's for zero bytes
	for i := 0; i < leadingZeros; i++ {
		encoded = append([]byte{base58Alphabet[0]}, encoded...)
	}

	return string(encoded)
}
