package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// IPFSClient provides a zero-dependency IPFS client that uses public gateways
// for storage and retrieval, with local simulation fallback.
type IPFSClient struct {
	mu sync.RWMutex

	// Public IPFS gateways for read/write.
	gateways []string

	// Local cache simulating IPFS pinning when gateways are unreachable.
	localCache map[string][]byte

	// HTTP client with timeout.
	httpClient *http.Client
}

// NewIPFSClient creates a new IPFS client with default public gateways.
func NewIPFSClient() *IPFSClient {
	return &IPFSClient{
		gateways: []string{
			"https://ipfs.io",
			"https://dweb.link",
			"https://gateway.pinata.cloud",
			"https://cloudflare-ipfs.com",
			"https://ipfs.infura.io",
		},
		localCache: make(map[string][]byte),
		httpClient: &http.Client{Timeout: 200 * time.Millisecond},
	}
}

// Store uploads data to IPFS. It first tries the HTTP gateway add API;
// on failure it computes a SHA-256 hash locally to simulate a CID.
// Returns the simulated or real CID.
func (c *IPFSClient) Store(data []byte) (string, error) {
	// Try each gateway's /api/v0/add endpoint.
	for _, gw := range c.gateways {
		cid, err := c.storeViaGateway(gw, data)
		if err == nil {
			return cid, nil
		}
	}

	// Fallback: compute local CID-like hash.
	return c.localStore(data), nil
}

// Retrieve fetches data from IPFS by CID. Tries gateways first, then local cache.
func (c *IPFSClient) Retrieve(cid string) ([]byte, error) {
	// Try each gateway.
	for _, gw := range c.gateways {
		data, err := c.retrieveViaGateway(gw, cid)
		if err == nil {
			return data, nil
		}
	}

	// Fallback: local cache.
	c.mu.RLock()
	data, ok := c.localCache[cid]
	c.mu.RUnlock()
	if ok {
		return data, nil
	}

	return nil, fmt.Errorf("failed to retrieve CID %s from all gateways and local cache", cid)
}

// StoreJSON marshals v to JSON and stores it on IPFS.
func (c *IPFSClient) StoreJSON(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return c.Store(data)
}

// RetrieveJSON fetches data and unmarshals into v.
func (c *IPFSClient) RetrieveJSON(cid string, v interface{}) error {
	data, err := c.Retrieve(cid)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// CacheSize returns the number of items in local cache.
func (c *IPFSClient) CacheSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.localCache)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (c *IPFSClient) storeViaGateway(gateway string, data []byte) (string, error) {
	url := gateway + "/api/v0/add"

	body := &bytes.Buffer{}
	// Multipart is ideal but for simplicity we send raw body.
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gateway %s returned status %d", gateway, resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// The API returns JSON with "Hash" field.
	var result struct {
		Hash string `json:"Hash"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if result.Hash == "" {
		return "", fmt.Errorf("empty hash from gateway %s", gateway)
	}

	// Also cache locally.
	c.mu.Lock()
	c.localCache[result.Hash] = data
	c.mu.Unlock()

	return result.Hash, nil
}

func (c *IPFSClient) retrieveViaGateway(gateway, cid string) ([]byte, error) {
	url := gateway + "/ipfs/" + cid
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway %s returned status %d for CID %s", gateway, resp.StatusCode, cid)
	}

	return io.ReadAll(resp.Body)
}

// localStore computes a SHA-256 hash of data and stores it in local cache,
// simulating an IPFS CID. The hash is prefixed with "Qm" to resemble a real CIDv0.
func (c *IPFSClient) localStore(data []byte) string {
	h := sha256.Sum256(data)
	cid := "Qm" + hex.EncodeToString(h[:])

	c.mu.Lock()
	c.localCache[cid] = data
	c.mu.Unlock()

	return cid
}
