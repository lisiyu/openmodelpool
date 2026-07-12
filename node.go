package main

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tyler-smith/go-bip39"
)

// NodeIdentity manages this node's identity in the federation.
// SA-13: The private key is stored encrypted in memory and only decrypted
// on-demand for signing operations. After use, the decrypted key is zeroed
// to minimize the window of exposure in process memory.
// v4.0: Now supports BIP39 mnemonic-based identity generation with BIP32/SLIP-0010 derivation.
type NodeIdentity struct {
	mu              sync.RWMutex
	nodeID          string
	privKey         ed25519.PrivateKey // kept only during active use; cleared after sign
	encPrivKey      string             // encrypted private key (AES-256-GCM), always in memory
	pubKey          ed25519.PublicKey
	mnemonic        string // BIP39 mnemonic (only set in Network Mode, kept in memory temporarily)
	hasMnemonic     bool   // whether identity was derived from mnemonic
	githubUser      string
	githubID        int64
	joinedAt        time.Time
	keyPath         string // path to encrypted key file
	tokenBudget     int64  // monthly token budget declaration
	backupConfirmed bool   // whether user has confirmed mnemonic backup
	needsMigration  bool   // true if loaded from legacy mm- format, needs migration to mmx- format
}

var node *NodeIdentity

// NodeKeyStore is the on-disk format for the node's keys.
type NodeKeyStore struct {
	NodeID          string `json:"node_id"`
	PrivKeyB64      string `json:"priv_key"`                 // encrypted with AES-256-GCM
	PubKeyB64       string `json:"pub_key"`
	Mnemonic        string `json:"mnemonic,omitempty"`       // encrypted mnemonic (AES-256-GCM)
	HasMnemonic     bool   `json:"has_mnemonic"`
	BackupConfirmed bool   `json:"backup_confirmed"`
	GitHubUser      string `json:"github_user,omitempty"`
	GitHubID        int64  `json:"github_id,omitempty"`
	JoinedAt        string `json:"joined_at"`
	Version         int    `json:"version"` // storage version for migration
}

// initNode initializes the node identity from disk.
// v4.0: No longer auto-generates identity on startup.
// If no key file exists, the node stays in uninitialized (Personal Mode) state.
func initNode(dataDir string) {
	node = &NodeIdentity{
		keyPath: dataDir + "/node.key",
	}

	data, err := os.ReadFile(node.keyPath)
	if err != nil {
		// No existing key file - stay in uninitialized state (Personal Mode).
		// Identity will be generated only when user explicitly joins the shared network.
		slog.Info("no node identity found, running in personal mode")
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
	node.hasMnemonic = store.HasMnemonic
	node.backupConfirmed = store.BackupConfirmed

	// SA-13: Store encrypted private key in memory (not decrypted)
	node.encPrivKey = store.PrivKeyB64

	// Derive public key from encrypted key (decrypt temporarily, derive pub, then zero)
	decrypted := decryptField(store.PrivKeyB64)
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
	// Decrypt and store mnemonic if present
	if store.HasMnemonic && store.Mnemonic != "" {
		decMnemonic := decryptField(store.Mnemonic)
		if decMnemonic != "" {
			node.mnemonic = decMnemonic
		}
	}

	node.githubUser = store.GitHubUser
	node.githubID = store.GitHubID
	if store.JoinedAt != "" {
		node.joinedAt, _ = time.Parse(time.RFC3339, store.JoinedAt)
	}

	// Check if this is a legacy mm- format that needs migration
	if strings.HasPrefix(node.nodeID, "mm-") && !strings.HasPrefix(node.nodeID, "mmx-") {
		node.needsMigration = true
		slog.Warn("legacy node ID format detected, migration recommended", "node_id", node.nodeID)
	}

	slog.Info("node identity loaded", "node_id", node.nodeID, "has_mnemonic", node.hasMnemonic, "needs_migration", node.needsMigration)
}

// GenerateWithMnemonic generates a new identity using BIP39 mnemonic.
// wordCount must be 12 or 24 (default 12).
// Returns the mnemonic plaintext that MUST be shown to the user for backup.
func (n *NodeIdentity) GenerateWithMnemonic(wordCount int) (string, error) {
	if wordCount != 12 && wordCount != 24 {
		return "", fmt.Errorf("word count must be 12 or 24, got %d", wordCount)
	}

	// Determine entropy bits: 12 words = 128 bits, 24 words = 256 bits
	entropyBits := 128
	if wordCount == 24 {
		entropyBits = 256
	}

	// Generate entropy
	entropy, err := bip39.NewEntropy(entropyBits)
	if err != nil {
		return "", fmt.Errorf("failed to generate entropy: %w", err)
	}

	// Generate mnemonic from entropy
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		// Zero entropy before returning
		for i := range entropy {
			entropy[i] = 0
		}
		return "", fmt.Errorf("failed to generate mnemonic: %w", err)
	}

	// Zero entropy immediately after use
	for i := range entropy {
		entropy[i] = 0
	}

	// Derive Ed25519 key from mnemonic via BIP32/SLIP-0010
	privKey, pubKey, err := deriveKeyFromMnemonic(mnemonic)
	if err != nil {
		return "", fmt.Errorf("failed to derive key from mnemonic: %w", err)
	}

	// Update identity
	n.mu.Lock()
	n.privKey = privKey
	n.pubKey = pubKey
	n.hasMnemonic = true
	n.backupConfirmed = false
	n.mnemonic = mnemonic // keep in memory until user confirms backup
	n.nodeID = "mmx-" + hex.EncodeToString(pubKey)
	n.joinedAt = time.Now().UTC()
	n.needsMigration = false

	// SA-13: Encrypt private key for storage, then clear plaintext
	privKeyB64 := base64.StdEncoding.EncodeToString(privKey)
	n.encPrivKey = encryptField(privKeyB64)
	// Clear the private key from memory (will be decrypted on-demand for signing)
	for i := range privKey {
		privKey[i] = 0
	}
	n.privKey = nil

	// Encrypt and store mnemonic
	n.mu.Unlock()

	// Save to disk (including encrypted mnemonic)
	n.save()

	slog.Info("new mnemonic-based node identity generated", "node_id", n.nodeID, "word_count", wordCount)

	// Return mnemonic plaintext - caller MUST show this to user for backup
	return mnemonic, nil
}

// RestoreFromMnemonic restores identity from an existing mnemonic phrase.
// Used when user reinstalls or switches devices.
func (n *NodeIdentity) RestoreFromMnemonic(mnemonic string) error {
	mnemonic = strings.TrimSpace(mnemonic)

	// Validate mnemonic
	if !bip39.IsMnemonicValid(mnemonic) {
		return fmt.Errorf("invalid mnemonic phrase")
	}

	// Derive Ed25519 key from mnemonic via BIP32/SLIP-0010
	privKey, pubKey, err := deriveKeyFromMnemonic(mnemonic)
	if err != nil {
		return fmt.Errorf("failed to derive key from mnemonic: %w", err)
	}

	// Update identity
	n.mu.Lock()
	n.privKey = privKey
	n.pubKey = pubKey
	n.hasMnemonic = true
	n.backupConfirmed = true // restored from existing mnemonic, assume already backed up
	n.mnemonic = mnemonic
	n.nodeID = "mmx-" + hex.EncodeToString(pubKey)
	n.joinedAt = time.Now().UTC()
	n.needsMigration = false

	// SA-13: Encrypt private key for storage, then clear plaintext
	privKeyB64 := base64.StdEncoding.EncodeToString(privKey)
	n.encPrivKey = encryptField(privKeyB64)
	for i := range privKey {
		privKey[i] = 0
	}
	n.privKey = nil
	n.mu.Unlock()

	// Save to disk
	n.save()

	slog.Info("node identity restored from mnemonic", "node_id", n.nodeID)
	return nil
}

// GetMnemonic returns the plaintext mnemonic.
// Should only be called for display purposes (e.g., re-showing to user).
func (n *NodeIdentity) GetMnemonic() (string, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	if !n.hasMnemonic {
		return "", fmt.Errorf("this identity was not generated from a mnemonic")
	}

	if n.mnemonic == "" {
		// Try to decrypt from disk
		data, err := os.ReadFile(n.keyPath)
		if err != nil {
			return "", fmt.Errorf("failed to read key file: %w", err)
		}

		var store NodeKeyStore
		if err := json.Unmarshal(data, &store); err != nil {
			return "", fmt.Errorf("failed to parse key file: %w", err)
		}

		if store.Mnemonic == "" {
			return "", fmt.Errorf("no mnemonic stored for this identity")
		}

		decrypted := decryptField(store.Mnemonic)
		if decrypted == "" {
			return "", fmt.Errorf("failed to decrypt mnemonic")
		}
		return decrypted, nil
	}

	return n.mnemonic, nil
}

// ConfirmBackup marks that the user has confirmed they backed up the mnemonic.
func (n *NodeIdentity) ConfirmBackup() {
	n.mu.Lock()
	n.backupConfirmed = true
	// After confirmation, zero the in-memory mnemonic for security
	n.mnemonic = ""
	n.mu.Unlock()
	n.save()
	slog.Info("mnemonic backup confirmed, mnemonic cleared from memory")
}

// IsInitialized returns whether the node identity has been set up.
func (n *NodeIdentity) IsInitialized() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.nodeID != "" && (n.encPrivKey != "" || n.privKey != nil)
}

// NeedsMigration returns true if the node uses legacy mm- format.
func (n *NodeIdentity) NeedsMigration() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.needsMigration
}

// HasMnemonic returns whether this identity is mnemonic-based.
func (n *NodeIdentity) HasMnemonic() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.hasMnemonic
}

// IsBackupConfirmed returns whether the user has confirmed mnemonic backup.
func (n *NodeIdentity) IsBackupConfirmed() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.backupConfirmed
}

// deriveKeyFromMnemonic derives an Ed25519 key pair from a BIP39 mnemonic
// using SLIP-0010 derivation path m/44'/2024'/0'.
func deriveKeyFromMnemonic(mnemonic string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	// 1. Mnemonic → seed (BIP39)
	seed := bip39.NewSeed(mnemonic, "")

	// 2. SLIP-0010 Ed25519 master key derivation
	// Master key: HMAC-SHA512(Key="ed25519 seed", Data=seed)
	masterKey, masterChain := slip0010MasterKey(seed)

	// Zero seed after use
	for i := range seed {
		seed[i] = 0
	}

	// 3. Derive path m/44'/2024'/0'
	// All indices are hardened (bit 31 set)
	key, chainCode := slip0010DerivePath(masterKey, masterChain, []uint32{
		44 | 0x80000000,   // 44' (BIP44)
		2024 | 0x80000000, // 2024' (OpenModelPool registered path)
		0 | 0x80000000,    // 0' (default account)
	})

	// Zero parent key material
	for i := range masterKey {
		masterKey[i] = 0
	}
	for i := range chainCode {
		chainCode[i] = 0
	}

	// 4. 32 bytes → Ed25519 private key seed
	privKey := ed25519.NewKeyFromSeed(key)
	pubKey := privKey.Public().(ed25519.PublicKey)

	// Zero the derived key seed (ed25519.NewKeyFromSeed copies the seed)
	for i := range key {
		key[i] = 0
	}

	return privKey, pubKey, nil
}

// slip0010MasterKey generates the master key and chain code from seed per SLIP-0010.
func slip0010MasterKey(seed []byte) ([]byte, []byte) {
	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	mac.Write(seed)
	I := mac.Sum(nil)

	// Left 32 bytes = master secret key, right 32 bytes = master chain code
	key := make([]byte, 32)
	chainCode := make([]byte, 32)
	copy(key, I[:32])
	copy(chainCode, I[32:])

	// Zero the full HMAC output
	for i := range I {
		I[i] = 0
	}

	return key, chainCode
}

// slip0010DerivePath derives a child key through a series of hardened indices.
func slip0010DerivePath(key, chainCode []byte, path []uint32) ([]byte, []byte) {
	currentKey := make([]byte, 32)
	copy(currentKey, key)
	currentChain := make([]byte, 32)
	copy(currentChain, chainCode)

	for _, index := range path {
		currentKey, currentChain = slip0010DeriveChild(currentKey, currentChain, index)
	}

	return currentKey, currentChain
}

// slip0010DeriveChild derives a single hardened child key per SLIP-0010.
func slip0010DeriveChild(key, chainCode []byte, index uint32) ([]byte, []byte) {
	// For Ed25519 (SLIP-0010), only hardened derivation is supported.
	// Data = 0x00 || ser256(key) || ser32(index)
	data := make([]byte, 1+32+4)
	data[0] = 0x00
	copy(data[1:], key)
	binary.BigEndian.PutUint32(data[33:], index)

	mac := hmac.New(sha512.New, chainCode)
	mac.Write(data)
	I := mac.Sum(nil)

	// Zero data after use
	for i := range data {
		data[i] = 0
	}

	childKey := make([]byte, 32)
	childChain := make([]byte, 32)
	copy(childKey, I[:32])
	copy(childChain, I[32:])

	// Zero full HMAC output
	for i := range I {
		I[i] = 0
	}

	return childKey, childChain
}

// generate generates a new random Ed25519 identity (legacy method, kept for backward compat).
// Deprecated: Use GenerateWithMnemonic for new identities.
func (n *NodeIdentity) generate() error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 key: %w", err)
	}

	// SA-13: Encrypt private key for in-memory storage, then clear plaintext
	privKeyB64 := base64.StdEncoding.EncodeToString(priv)
	n.encPrivKey = encryptField(privKeyB64)
	n.privKey = priv // temporarily held for save(), cleared after save
	n.pubKey = pub
	n.nodeID = "mm-" + base58Encode(pub[:16])
	n.hasMnemonic = false
	n.joinedAt = time.Now().UTC()
	return nil
}

func (n *NodeIdentity) save() {
	n.mu.Lock()
	defer n.mu.Unlock()

	// SA-13: Use encrypted in-memory key if available, otherwise encrypt from plaintext
	encKey := n.encPrivKey
	if encKey == "" && n.privKey != nil {
		privKeyB64 := base64.StdEncoding.EncodeToString(n.privKey)
		encKey = encryptField(privKeyB64)
		n.encPrivKey = encKey
		// Clear plaintext private key after encryption
		for i := range n.privKey {
			n.privKey[i] = 0
		}
		n.privKey = nil
	}

	if encKey == "" {
		return
	}

	// Encrypt mnemonic if available
	encMnemonic := ""
	if n.mnemonic != "" {
		encMnemonic = encryptField(n.mnemonic)
	}

	store := NodeKeyStore{
		NodeID:          n.nodeID,
		PrivKeyB64:      encKey,
		PubKeyB64:       base64.StdEncoding.EncodeToString(n.pubKey),
		Mnemonic:        encMnemonic,
		HasMnemonic:     n.hasMnemonic,
		BackupConfirmed: n.backupConfirmed,
		GitHubUser:      n.githubUser,
		GitHubID:        n.githubID,
		JoinedAt:        n.joinedAt.Format(time.RFC3339),
		Version:         2, // v4.0 storage version
	}

	data, _ := json.MarshalIndent(store, "", "  ")
	atomicWriteFile(n.keyPath, data, 0600)
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
	decrypted := decryptField(n.encPrivKey)
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

	decrypted := decryptField(n.encPrivKey)
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

	// v3.1: Determine capabilities from config
	caps := PeerCapabilities{
		CanRelay: true, // all nodes can relay by default in unified Peer model
		CanSeed:  true, // all nodes can seed by default
	}
	if netMgr != nil {
		caps = netMgr.config.Capabilities
		// Ensure defaults if capabilities not set
		if !caps.CanRelay && !caps.CanSeed {
			caps.CanRelay = true
			caps.CanSeed = true
		}
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
		SeedNode:        caps.CanSeed, // v3.1: derived from capability, not preset type
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
