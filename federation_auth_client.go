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
// No new dependencies; reuses package-level cfg / node singletons.

import (
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
	if node != nil && node.NodeID() != "" {
		req.Header.Set("X-Node-ID", node.NodeID())
	}
	if cfg != nil {
		if secret := cfg.Get("federation_secret", ""); secret != "" {
			req.Header.Set("X-Federation-Secret", secret)
		}
	}
	// Best-effort monotonic clock for signature auth (path-1) when a future
	// version populates trust-pool pub_keys; harmless for path-3.
	req.Header.Set("X-Node-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	return req
}
