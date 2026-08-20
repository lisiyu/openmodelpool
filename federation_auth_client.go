package main

// federation_auth_client.go — Outbound federation auth helper.
//
// withFederationAuth (federation.go) admits a cross-node request via any of:
//   - path-1: X-Node-ID + X-Node-Signature + X-Node-Timestamp (node identity)
//   - path-2: admin JWT
//   - path-3: X-Federation-Secret == local federation_secret
//
// Every cross-node client (gossip, ledger replication, discovery pool fetch,
// update signal/report) previously sent only X-Node-ID, which satisfies none
// of the three paths when the trust pool lacks a populated pub_key — the
// deployment reality. This helper attaches X-Node-ID plus the shared secret
// (path-3) so requests authenticate regardless of pub_key propagation.
//
// B6-5: when this node holds a private key it additionally signs the full
// request envelope (nodeID:method:path:sha256(body)) so peers can verify
// payload integrity via path-1 even over plain HTTP — the shared secret alone
// authenticates the sender but provides no body integrity.
//
// No new dependencies; reuses package-level cfg / node singletons.

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// attachFederationAuth decorates an outbound request with the node identity
// header and, when a federation_secret is configured, the shared-secret
// header required by withFederationAuth path-3. It is safe to call on any
// request and adds nothing when node / secret are unavailable.
func attachFederationAuth(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	nodeID := ""
	if node != nil && node.NodeID() != "" {
		nodeID = node.NodeID()
		req.Header.Set("X-Node-ID", nodeID)
	}
	if cfg != nil {
		if secret := cfg.Get("federation_secret", ""); secret != "" {
			req.Header.Set("X-Federation-Secret", secret)
		}
	}
	// Best-effort monotonic clock for signature auth (path-1) when a future
	// version populates trust-pool pub_keys; harmless for path-3.
	req.Header.Set("X-Node-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	// B6-5: sign the request envelope when we can, so peers verify integrity.
	if nodeID != "" && req.Method != http.MethodGet {
		if sig, ts := signFederationRequest(req); sig != "" {
			req.Header.Set("X-Node-Signature", sig)
			req.Header.Set("X-Node-Timestamp", ts)
		}
	}
	return req
}

// signFederationRequest signs nodeID:method:path:sha256(body) with this node's
// ed25519 key and returns (signature, unix-timestamp). The request body is
// read (up to maxFederationSigBody), restored, and included in the signature.
// Returns ("","") when signing is not possible.
func signFederationRequest(req *http.Request) (string, string) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, maxFederationSigBody))
		if err != nil {
			return "", ""
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
		body = b
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	payload := []byte(fmt.Sprintf("%s:%s:%s:%s",
		req.Header.Get("X-Node-ID"), req.Method, req.URL.Path, sha256Hex(body)))
	sig := node.Sign(payload)
	if sig == "" {
		return "", ""
	}
	return sig, ts
}
