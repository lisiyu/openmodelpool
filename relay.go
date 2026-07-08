package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ============================================================
// Per-node rate limiter (fixed window, 60 req/min default)
// ============================================================

type rateLimitEntry struct {
	count     int
	windowStart time.Time
}

var (
	rateLimitMu   sync.Mutex
	rateLimitMap  = make(map[string]*rateLimitEntry)
	rateLimitMax  = 60
	rateLimitWin  = time.Minute
)

func rateLimitCheck(nodeID string) bool {
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()

	now := time.Now()
	e, ok := rateLimitMap[nodeID]
	if !ok || now.Sub(e.windowStart) >= rateLimitWin {
		rateLimitMap[nodeID] = &rateLimitEntry{count: 1, windowStart: now}
		return true
	}
	if e.count >= rateLimitMax {
		return false
	}
	e.count++
	return true
}

// ============================================================
// Incoming relay handler: POST /federation/relay
// ============================================================

func handleRelayRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}

	nodeID := r.Header.Get("X-Node-ID")
	signature := r.Header.Get("X-Signature")
	if nodeID == "" || signature == "" {
		writeError(w, 401, "missing X-Node-ID or X-Signature headers")
		return
	}

	// Verify sender is in trust pool
	trustPool := fed.GetTrustPool()
	var senderNode *NodeInfo
	for i := range trustPool.Nodes {
		if trustPool.Nodes[i].NodeID == nodeID {
			senderNode = &trustPool.Nodes[i]
			break
		}
	}
	if senderNode == nil {
		writeError(w, 403, "node not in trust pool")
		return
	}

	// Verify ed25519 signature
	pubKeyBytes, err := base64.StdEncoding.DecodeString(senderNode.PubKey)
	if err != nil {
		writeError(w, 400, "invalid public key")
		return
	}
	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		writeError(w, 400, "invalid signature encoding")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "failed to read request body")
		return
	}

	pubKey := ed25519.PublicKey(pubKeyBytes)
	if !ed25519.Verify(pubKey, bodyBytes, sigBytes) {
		writeError(w, 403, "signature verification failed")
		return
	}

	// Rate limit check
	if !rateLimitCheck(nodeID) {
		writeError(w, 429, "rate limit exceeded (max 60 req/min)")
		return
	}

	// Parse relay request
	var relayReq RelayRequest
	if err := json.Unmarshal(bodyBytes, &relayReq); err != nil {
		writeError(w, 400, "invalid relay request body")
		return
	}

	if relayReq.Model == "" || len(relayReq.Messages) == 0 {
		writeError(w, 400, "model and messages are required")
		return
	}

	// Check relay is enabled
	if !fed.IsRelayEnabled() {
		writeError(w, 503, "relay is not enabled on this node")
		return
	}

	// Find local provider for the model
	routingMode := cfg.Get("routing_mode", "priority")
	candidates := pm.OrderedCandidates(relayReq.Model, routingMode)
	if len(candidates) == 0 {
		writeError(w, 404, fmt.Sprintf("no local provider for model '%s'", relayReq.Model))
		return
	}

	// Build extra params from relay request
	extra := make(map[string]any)
	for k, v := range relayReq.Extra {
		extra[k] = v
	}

	startTime := time.Now()

	if relayReq.Stream {
		// Streaming relay
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, 500, "streaming not supported")
			return
		}

		sw := &streamWriter{w: w, flusher: flusher}

		var lastErr error
		for _, c := range candidates {
			p := c.Provider
			actualModel := c.Model

			if p.APIKey == "" && p.Type != "coze" {
				continue
			}

			err := doStream(p, actualModel, relayReq.Messages, extra, sw)
			latencyMS := float64(time.Since(startTime).Milliseconds())
			if err != nil {
				tracker.Record(p.ID, p.Name, relayReq.Model, 0, 0, latencyMS, false, err.Error())
				if sw.bytesWritten > 0 {
					// Data already sent, cannot retry
					slog.Error("relay stream failed after data sent", "provider", p.Name, "from", nodeID, "error", err)
					return
				}
				lastErr = err
				continue
			}
			tracker.Record(p.ID, p.Name, relayReq.Model, 0, 0, latencyMS, true, "")
			slog.Info("relay stream completed", "provider", p.Name, "from", nodeID, "latency_ms", latencyMS)
			return
		}
		// All providers failed
		errMsg := fmt.Sprintf("all providers failed for relay: %v", lastErr)
		sseData := fmt.Sprintf(`data: {"error":{"message":"%s","type":"upstream_error"}}`, errMsg)
		fmt.Fprintf(sw, "%s\n\n", sseData)
		slog.Error("relay stream all providers failed", "from", nodeID, "error", lastErr)
		return
	}

	// Non-streaming relay
	var lastErr error
	for _, c := range candidates {
		p := c.Provider
		actualModel := c.Model

		if p.APIKey == "" && p.Type != "coze" {
			continue
		}

		resp, err := doNonStream(p, actualModel, relayReq.Messages, extra)
		latencyMS := float64(time.Since(startTime).Milliseconds())
		if err != nil {
			tracker.Record(p.ID, p.Name, relayReq.Model, 0, 0, latencyMS, false, err.Error())
			lastErr = err
			continue
		}

		resp.Model = relayReq.Model
		var promptTok, compTok int
		if resp.Usage != nil {
			promptTok = resp.Usage.PromptTokens
			compTok = resp.Usage.CompletionTokens
		}
		tracker.Record(p.ID, p.Name, relayReq.Model, promptTok, compTok, latencyMS, true, "")

		totalTokens := promptTok + compTok
		slog.Info("relay request completed", "provider", p.Name, "from", nodeID, "tokens", totalTokens, "latency_ms", latencyMS)

		writeJSON(w, 200, RelayResponse{
			Success:   true,
			Data:      mustMarshalJSON(resp),
			Tokens:    totalTokens,
			LatencyMS: latencyMS,
		})
		return
	}

	writeError(w, 502, fmt.Sprintf("all local providers failed: %v", lastErr))
}

func mustMarshalJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

// ============================================================
// Outgoing relay: send request to a remote node
// ============================================================

func (f *FederationManager) RelayToRemote(nodeInfo NodeInfo, req ChatRequest, model string) (*ChatResponse, error) {
	relayReq := RelayRequest{
		Model:    model,
		Messages: req.Messages,
		Stream:   false,
		Extra:    req.Extra,
	}

	bodyBytes, err := json.Marshal(relayReq)
	if err != nil {
		return nil, fmt.Errorf("marshal relay request: %w", err)
	}

	// Sign the request body
	sig := node.Sign(bodyBytes)

	url := nodeInfo.Endpoint + "/federation/relay"
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create relay request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Node-ID", node.NodeID())
	httpReq.Header.Set("X-Signature", sig)

	client := GetSharedHTTPClientWithTimeout(90 * time.Second)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("relay request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read relay response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var relayResp RelayResponse
	if err := json.Unmarshal(respBody, &relayResp); err != nil {
		return nil, fmt.Errorf("unmarshal relay response: %w", err)
	}

	if !relayResp.Success {
		return nil, fmt.Errorf("relay failed: %s", relayResp.Error)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(relayResp.Data, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal relay data: %w", err)
	}

	return &chatResp, nil
}

// ============================================================
// Outgoing relay: streaming version
// ============================================================

func (f *FederationManager) RelayStreamToRemote(nodeInfo NodeInfo, req ChatRequest, model string, sw *streamWriter, origModel string, startTime time.Time) (bool, error) {
	relayReq := RelayRequest{
		Model:    model,
		Messages: req.Messages,
		Stream:   true,
		Extra:    req.Extra,
	}

	bodyBytes, err := json.Marshal(relayReq)
	if err != nil {
		return false, fmt.Errorf("marshal relay request: %w", err)
	}

	sig := node.Sign(bodyBytes)

	url := nodeInfo.Endpoint + "/federation/relay"
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return false, fmt.Errorf("create relay request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Node-ID", node.NodeID())
	httpReq.Header.Set("X-Signature", sig)

	client := GetSharedHTTPClientWithTimeout(5 * time.Minute)
	resp, err := client.Do(httpReq)
	if err != nil {
		return false, fmt.Errorf("relay stream request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("relay stream returned status %d: %s", resp.StatusCode, string(body))
	}

	// Forward SSE chunks from remote node to our client
	buf := make([]byte, 4096)
	dataSent := false
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			_, writeErr := sw.Write(buf[:n])
			if writeErr != nil {
				return true, fmt.Errorf("write to client failed: %w", writeErr)
			}
			dataSent = true
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return dataSent, fmt.Errorf("read from remote stream: %w", readErr)
		}
	}

	latencyMS := float64(time.Since(startTime).Milliseconds())
	slog.Info("relay stream from remote completed", "remote_node", nodeInfo.NodeID, "latency_ms", latencyMS)
	tracker.Record("relay:"+nodeInfo.NodeID, "relay:"+nodeInfo.NodeID, origModel, 0, 0, latencyMS, true, "")

	return dataSent, nil
}
