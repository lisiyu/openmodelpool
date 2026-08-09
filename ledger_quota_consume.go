package main

import (
	"log/slog"
	"net/http"
)

// ============================================================
// P2-3(ii) — Consumption-side wiring for the contribution entitlement
// ============================================================
//
// P2-3(i) accrued a 1:1 free-quota entitlement for every node that donated
// compute. This file spends it, under one hard constraint that comes straight
// from the project's governance philosophy:
//
//	善意默认 —— everyone is assumed to be acting in good faith. The system only
//	defends against malicious abuse, never against "this person did not
//	contribute". There is no freeloader penalty, no trust score, no economy.
//
// So the entitlement is wired as an EXTRA LANE, not a toll gate:
//
//   - A verified contributor with remaining entitlement draws on it, and in
//     exchange skips the anonymous per-IP abuse guard for that request. They
//     already added capacity to the pool; throttling them at the anonymous
//     rate would be nonsense.
//   - Everyone else — anonymous callers, contributors who ran their
//     entitlement down, nodes we cannot cryptographically identify — takes the
//     exact same community free-pool path as before this file existed. Nothing
//     is denied to them that was previously granted.
//
// Identity is NOT a new user system. It is the node identity that federation
// already proves with ed25519: verifyRelayForwardAuth checks X-Node-ID against
// the trust pool, verifies the signature over the request envelope, and
// enforces a replay window. Only after that check passes do we treat the node
// ID as a spendable identity.

// headerQuotaCharged is an INTERNAL marker. The gateway sets it after it has
// already accounted for a request so the downstream local handler does not
// charge the same request a second time. It is stripped from every inbound
// request before use, so a client cannot forge it to dodge the abuse guard.
const headerQuotaCharged = "X-OMP-Quota-Charged"

// headerQuotaSource is a RESPONSE header exposing which lane paid for the
// request: "contributor" (drawn from an earned entitlement) or "community"
// (the open free pool). Transparency, not billing.
const headerQuotaSource = "X-OMP-Quota-Source"

const (
	quotaSourceContributor = "contributor"
	quotaSourceCommunity   = "community"
)

// stripInternalQuotaHeaders removes client-supplied values of internal quota
// markers. Must be called at every external entry point before the markers are
// trusted.
func stripInternalQuotaHeaders(r *http.Request) {
	r.Header.Del(headerQuotaCharged)
}

// verifiedContributorID returns the contributor node identity for a request.
//
// PRECONDITION: verifyRelayForwardAuth must already have returned status 0 for
// this request. A non-empty X-Node-ID that survived that call has had its
// ed25519 signature verified against the trust-pool public key, so it is safe
// to spend against. Calling this without the precondition would trust a
// spoofable header.
func verifiedContributorID(r *http.Request) string {
	id := sanitizeNodeID(r.Header.Get("X-Node-ID"))
	if id == "" {
		id = sanitizeNodeID(r.Header.Get("X-Node-Auth"))
	}
	return id
}

// contributorDraw is the outcome of trying to pay for a request out of a
// contributor's earned entitlement.
type contributorDraw struct {
	PeerID    string // contributor node id ("" when no verified identity)
	Tokens    int64  // tokens drawn (0 when the draw did not happen)
	Remaining int64  // entitlement left after the draw
	OK        bool   // true when the entitlement covered the request
}

// tryContributorDraw attempts to pay for `tokens` out of the verified
// contributor's entitlement.
//
// It returns OK=false whenever there is no verified identity, no tracker, or
// not enough entitlement — and in every one of those cases the caller MUST
// fall through to the ordinary community free-pool path. Returning false is
// never a rejection.
func tryContributorDraw(r *http.Request, tokens int64) contributorDraw {
	if contribQuotaTracker == nil || tokens <= 0 {
		return contributorDraw{}
	}
	peerID := verifiedContributorID(r)
	if peerID == "" {
		return contributorDraw{}
	}
	ok, remaining := contribQuotaTracker.Consume(peerID, tokens)
	if !ok {
		return contributorDraw{PeerID: peerID, Remaining: remaining}
	}
	slog.Debug("contribution entitlement drawn",
		"peer_id", peerID, "tokens", tokens, "remaining", remaining)
	return contributorDraw{PeerID: peerID, Tokens: tokens, Remaining: remaining, OK: true}
}

// settle refunds the unused part of a reservation once the real token usage is
// known. actual <= 0 means "usage unknown" and the reservation stands, which
// keeps the accounting conservative rather than over-crediting.
func (d contributorDraw) settle(actual int64) {
	if !d.OK || contribQuotaTracker == nil {
		return
	}
	if actual <= 0 || actual >= d.Tokens {
		return
	}
	contribQuotaTracker.Refund(d.PeerID, d.Tokens-actual)
}
