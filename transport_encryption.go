package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"
)

const (
	transportEncryptedPayloadKey = "encrypted_payload"
	transportSenderPubKeyKey     = "sender_pub_key"
	transportReceiverNodeIDKey   = "receiver_node_id"
	transportTimestampKey        = "timestamp"
	transportNonceKey            = "nonce"
)

// TransportEncryptedMessage is the wire format for relay-not-visible encryption.
// v4 design §8.2: AES-256-GCM encrypted payload, sender's Ed25519 public key,
// receiver's NodeID, timestamp, and nonce.
type TransportEncryptedMessage struct {
	EncryptedPayload string `json:"encrypted_payload"`
	SenderPubKey     string `json:"sender_pub_key"`
	ReceiverNodeID   string `json:"receiver_node_id"`
	Timestamp        string `json:"timestamp"`
	Nonce            string `json:"nonce"`
}

// EncryptForTransport encrypts a request body for relay-not-visible transport.
// The payload is encrypted with AES-256-GCM using a shared secret derived from
// the sender's Ed25519 private key and the receiver's Ed25519 public key.
// Returns the JSON-encoded TransportEncryptedMessage.
func EncryptForTransport(payload []byte, receiverNodeID string) ([]byte, error) {
	if node == nil || !node.IsInitialized() {
		return nil, fmt.Errorf("node identity not initialized")
	}

	receiverPubKey, err := resolveNodePubKey(receiverNodeID)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve receiver public key: %w", err)
	}

	sharedSecret, err := deriveSharedSecret(receiverPubKey)
	if err != nil {
		return nil, fmt.Errorf("cannot derive shared secret: %w", err)
	}

	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		return nil, fmt.Errorf("cannot create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cannot create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("cannot generate nonce: %w", err)
	}

	encrypted := gcm.Seal(nil, nonce, payload, nil)

	msg := TransportEncryptedMessage{
		EncryptedPayload: base64.StdEncoding.EncodeToString(encrypted),
		SenderPubKey:     node.PubKeyB64(),
		ReceiverNodeID:   receiverNodeID,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		Nonce:            base64.StdEncoding.EncodeToString(nonce),
	}

	return json.Marshal(msg)
}

// DecryptFromTransport decrypts a transport-encrypted message.
// Returns the plaintext payload.
func DecryptFromTransport(data []byte) ([]byte, error) {
	var msg TransportEncryptedMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("cannot unmarshal transport message: %w", err)
	}

	if node == nil || !node.IsInitialized() {
		return nil, fmt.Errorf("node identity not initialized")
	}

	senderPubKey, err := resolveNodePubKey(msg.SenderPubKey)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve sender public key: %w", err)
	}

	sharedSecret, err := deriveSharedSecret(senderPubKey)
	if err != nil {
		return nil, fmt.Errorf("cannot derive shared secret: %w", err)
	}

	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		return nil, fmt.Errorf("cannot create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cannot create GCM: %w", err)
	}

	nonce, err := base64.StdEncoding.DecodeString(msg.Nonce)
	if err != nil {
		return nil, fmt.Errorf("cannot decode nonce: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid nonce size: got %d, want %d", len(nonce), gcm.NonceSize())
	}

	encrypted, err := base64.StdEncoding.DecodeString(msg.EncryptedPayload)
	if err != nil {
		return nil, fmt.Errorf("cannot decode encrypted payload: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// IsTransportEncrypted checks if a JSON payload is a transport-encrypted message.
func IsTransportEncrypted(data []byte) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	_, hasPayload := probe[transportEncryptedPayloadKey]
	_, hasNonce := probe[transportNonceKey]
	return hasPayload && hasNonce
}

// resolveNodePubKey resolves a node's Ed25519 public key from either
// a direct base64-encoded key or a node ID lookup in the federation.
func resolveNodePubKey(idOrKey string) (string, error) {
	if idOrKey == "" {
		return "", fmt.Errorf("empty id or key")
	}

	decoded, err := base64.StdEncoding.DecodeString(idOrKey)
	if err == nil && len(decoded) == 32 {
		return idOrKey, nil
	}

	if fed != nil {
		if n, ok := fed.GetNode(idOrKey); ok && n.PubKey != "" {
			return n.PubKey, nil
		}
	}

	return "", fmt.Errorf("cannot resolve public key for: %s", idOrKey)
}

// deriveSharedSecret derives a 32-byte shared secret for AES-256-GCM.
// Uses Ed25519 key agreement: the sender's private key and the receiver's public key.
// For Ed25519 keys, we use a simple KDF: SHA-256(privKey || pubKey) to derive
// a deterministic shared secret. This is a simplified approach for Phase 1;
// a proper X25519 key exchange should be used in production.
func deriveSharedSecret(peerPubKeyB64 string) ([]byte, error) {
	peerPubBytes, err := base64.StdEncoding.DecodeString(peerPubKeyB64)
	if err != nil {
		return nil, fmt.Errorf("cannot decode peer public key: %w", err)
	}
	if len(peerPubBytes) != 32 {
		return nil, fmt.Errorf("invalid peer public key size: %d", len(peerPubBytes))
	}

	privKey := node.privateKey()
	if privKey == nil {
		return nil, fmt.Errorf("node private key not available")
	}

	secret := deriveTransportKey(privKey, peerPubBytes)
	return secret[:], nil
}

// deriveTransportKey derives a 32-byte AES-256 key from an Ed25519 private key
// and a peer's Ed25519 public key using a simple KDF.
// Phase 1: SHA-256(privKey.Seed() || peerPubKey) — deterministic per key pair.
// Production should use X25519 key exchange (curve25519-xsalsa20-poly1305).
func deriveTransportKey(privKey ed25519.PrivateKey, peerPubKey []byte) [32]byte {
	seed := privKey.Seed()
	h := sha256.New()
	h.Write(seed)
	h.Write(peerPubKey)
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

func init() {
	slog.Info("transport encryption module loaded")
}
