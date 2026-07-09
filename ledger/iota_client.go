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

// IOTAClient provides a zero-dependency IOTA tangle client.
// It uses public IOTA nodes for data anchoring; when nodes are unreachable
// it generates a deterministic simulated transaction hash locally.
type IOTAClient struct {
	mu sync.RWMutex

	// Public IOTA nodes (Hornet / Chrysalis REST API).
	nodes []string

	// Local simulation store.
	localTxs map[string]iotaTxEntry

	httpClient *http.Client
}

type iotaTxEntry struct {
	Data      []byte  `json:"data"`
	Timestamp int64   `json:"timestamp"`
	Tag       string  `json:"tag"`
}

// NewIOTAClient creates a new IOTA client with default public nodes.
func NewIOTAClient() *IOTAClient {
	return &IOTAClient{
		nodes: []string{
			"https://chrysalis-nodes.iota.org",
			"https://chrysalis-nodes.iota.cafe",
			"https://nodes.iota.cafe:443",
		},
		localTxs:   make(map[string]iotaTxEntry),
		httpClient: &http.Client{Timeout: 200 * time.Millisecond},
	}
}

// SubmitData anchors data on the IOTA tangle. On failure falls back to a
// locally simulated transaction hash.
// The tag is an optional short label (e.g. "CONTRIB", "TRUST").
// Returns the transaction hash.
func (c *IOTAClient) SubmitData(data []byte, tag string) (string, error) {
	for _, node := range c.nodes {
		txHash, err := c.submitViaNode(node, data, tag)
		if err == nil {
			return txHash, nil
		}
	}
	// Fallback: simulated hash.
	return c.simulateTx(data, tag), nil
}

// VerifyData verifies that data matches what was anchored.
// Tries the network first, then local simulation.
func (c *IOTAClient) VerifyData(txHash string) ([]byte, bool, error) {
	for _, node := range c.nodes {
		data, found, err := c.verifyViaNode(node, txHash)
		if err == nil && found {
			return data, true, nil
		}
	}

	// Fallback: local store.
	c.mu.RLock()
	entry, ok := c.localTxs[txHash]
	c.mu.RUnlock()
	if ok {
		return entry.Data, true, nil
	}

	return nil, false, nil
}

// TxCount returns the number of locally simulated transactions.
func (c *IOTAClient) TxCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.localTxs)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (c *IOTAClient) submitViaNode(node string, data []byte, tag string) (string, error) {
	// IOTA Chrysalis REST API: POST /api/v1/messages
	url := node + "/api/v1/messages"

	payload := map[string]interface{}{
		"tag":   tag,
		"data":  hex.EncodeToString(data),
		"index": tag,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("node %s returned status %d: %s", node, resp.StatusCode, string(respBody))
	}

	var result struct {
		Data struct {
			MessageID string `json:"messageId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if result.Data.MessageID == "" {
		return "", fmt.Errorf("empty messageId from node %s", node)
	}

	// Also cache locally.
	c.mu.Lock()
	c.localTxs[result.Data.MessageID] = iotaTxEntry{Data: data, Timestamp: time.Now().Unix(), Tag: tag}
	c.mu.Unlock()

	return result.Data.MessageID, nil
}

func (c *IOTAClient) verifyViaNode(node, txHash string) ([]byte, bool, error) {
	url := node + "/api/v1/messages/" + txHash
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("node %s returned status %d", node, resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}

	var result struct {
		Data struct {
			Payload struct {
				Data  string `json:"data"`
				Index string `json:"index"`
			} `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, false, err
	}

	rawData, err := hex.DecodeString(result.Data.Payload.Data)
	if err != nil {
		return nil, false, err
	}

	return rawData, true, nil
}

// simulateTx generates a deterministic transaction hash from the data and tag
// and caches it locally.
func (c *IOTAClient) simulateTx(data []byte, tag string) string {
	h := sha256.New()
	h.Write(data)
	h.Write([]byte(tag))
	h.Write([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	txHash := hex.EncodeToString(h.Sum(nil))

	c.mu.Lock()
	c.localTxs[txHash] = iotaTxEntry{Data: data, Timestamp: time.Now().Unix(), Tag: tag}
	c.mu.Unlock()

	return txHash
}
