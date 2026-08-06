package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
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
// X25519 ECDH between the sender's Ed25519 private key and the receiver's
// Ed25519 public key, with HKDF-SHA256 key derivation.
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

// deriveSharedSecret derives a 32-byte shared secret for AES-256-GCM using
// X25519 ECDH key agreement and HKDF-SHA256 key derivation.
//
// The Ed25519 keys are converted to X25519 (Curve25519) format:
// - Private key: SHA-512(seed)[0:32] with Curve25519 clamping
// - Public key: birational map from Edwards to Montgomery form (u = (1+y)/(1-y))
//
// The X25519 ECDH shared secret is then expanded via HKDF-SHA256 to produce
// the final 32-byte AES-256 key.
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

	// Convert Ed25519 private key to X25519 private key
	x25519Priv := ed25519PrivateKeyToCurve25519(privKey)

	// Convert Ed25519 public key to X25519 public key
	x25519PeerPub, err := ed25519PublicKeyToCurve25519(peerPubBytes)
	if err != nil {
		return nil, fmt.Errorf("cannot convert peer public key to X25519: %w", err)
	}

	// X25519 ECDH
	shared, err := curve25519.X25519(x25519Priv, x25519PeerPub)
	if err != nil {
		return nil, fmt.Errorf("X25519 ECDH failed: %w", err)
	}

	// HKDF-SHA256 to derive the final AES-256 key
	hkdfReader := hkdf.New(sha256.New, shared, nil, []byte("omp-transport-encryption-v2"))
	aesKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, aesKey); err != nil {
		return nil, fmt.Errorf("HKDF key derivation failed: %w", err)
	}

	return aesKey, nil
}

// ed25519PrivateKeyToCurve25519 converts an Ed25519 private key to an X25519
// private key using the method described in RFC 7748 §4.1.
// The Ed25519 private key seed is hashed with SHA-512, and the first 32 bytes
// are clamped according to Curve25519 rules.
func ed25519PrivateKeyToCurve25519(privKey ed25519.PrivateKey) []byte {
	h := sha512.New()
	h.Write(privKey.Seed())
	digest := h.Sum(nil)

	key := digest[:32]
	// Clamp according to Curve25519 rules
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64

	return key
}

// ed25519PublicKeyToCurve25519 converts an Ed25519 public key to an X25519
// public key using the birational map from the twisted Edwards curve to the
// Montgomery curve: u = (1 + y) / (1 - y) mod p, where p = 2^255 - 19.
//
// The Ed25519 public key encodes the y-coordinate (little-endian) with the
// sign bit of x in the MSB. We clear the sign bit, interpret the remaining
// 255 bits as y, and compute the Montgomery u-coordinate.
func ed25519PublicKeyToCurve25519(pubKey []byte) ([]byte, error) {
	if len(pubKey) != 32 {
		return nil, fmt.Errorf("invalid Ed25519 public key size: %d", len(pubKey))
	}

	// p = 2^255 - 19
	p, _ := new(big.Int).SetString("57896044618658097711785492504343953926634992332820282019728792003956564819949", 10)

	// Copy public key and clear the sign bit (MSB of last byte)
	pubCopy := make([]byte, 32)
	copy(pubCopy, pubKey)
	pubCopy[31] &= 0x7f

	// y = little-endian interpretation of pubCopy
	// Reverse bytes to get big-endian for big.Int
	yBytes := make([]byte, 32)
	for i := 0; i < 32; i++ {
		yBytes[i] = pubCopy[31-i]
	}
	y := new(big.Int).SetBytes(yBytes)

	// u = (1 + y) / (1 - y) mod p
	one := big.NewInt(1)

	numerator := new(big.Int).Add(one, y)
	numerator.Mod(numerator, p)

	denominator := new(big.Int).Sub(one, y)
	denominator.Mod(denominator, p)

	denominatorInv := new(big.Int).ModInverse(denominator, p)
	if denominatorInv == nil {
		return nil, fmt.Errorf("cannot compute modular inverse (y=1 is not a valid Edwards point)")
	}

	u := new(big.Int).Mul(numerator, denominatorInv)
	u.Mod(u, p)

	// Encode u as little-endian 32 bytes
	result := make([]byte, 32)
	uBytes := u.Bytes()
	// uBytes is big-endian, we need little-endian
	for i, b := range uBytes {
		result[len(uBytes)-1-i] = b
	}

	return result, nil
}

func init() {
	slog.Info("transport encryption module loaded (X25519 ECDH + HKDF-SHA256)")
}
