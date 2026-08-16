package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// ============================================================================
// P1-2b-2(iv): UDP direct-link data bearer (real request/response over the
// hole-punched channel)
// ============================================================================
//
// Before this file, relayToRemote had a "direct UDP link verified, bypassing
// TCP probe" branch (network_relay.go) that only SKIPPED the TCP probe — the
// actual bytes still went out over the HTTPS reverse proxy. This bearer makes
// the verified direct link genuinely carry data: when a direct UDP channel to
// the next hop exists, the relay request is encoded into UDP datagrams and the
// response is read back over the same channel, instead of the HTTPS proxy.
//
// Design constraints (kept deliberately tight to stay safe and stdlib-only):
//   - Reuses the SINGLE shared NAT UDP socket (natMgr.udpConn) via the existing
//     udpRecvLoop dispatch, so there is still exactly one reader — no new race.
//   - A distinct magic ("OMP2") demultiplexes data frames from punch ("OMP1")
//     and STUN (0x0001) frames already handled there.
//   - Authentication reuses the EXISTING relay-forward ed25519 signature: the
//     envelope carries the same X-Node-ID / relay-sig / relay-ts headers the
//     HTTPS Director attaches, so the receiving node's normal relay auth
//     (verifyRelayForwardAuth) verifies the forward identically. No new trust
//     model is introduced.
//   - Payloads are fragmented to fit the UDP MTU; the response is fully
//     reassembled before anything is written to the HTTP client, so ANY failure
//     (timeout, fragmentation gap, decode error) falls back cleanly to the
//     proven HTTPS reverse proxy with no partial write.
//   - Streaming requests (stream=true) are intentionally NOT carried over UDP:
//     they fall back to HTTPS so token streaming is preserved. Only
//     non-streaming traffic uses the direct link.
//
// Safety: every entry point that runs inside the shared socket's reader goroutine
// (HandleInbound and serveReq) is wrapped in a recover so a malformed frame or a
// panicking handler can never crash the single UDP reader (which would also kill
// STUN discovery and hole-punching).

const (
	dataMagic      = "OMP2"        // distinguishes data frames from punch ("OMP1") / STUN
	frameHeaderLen = 27            // magic(4)+type(1)+reqID(16)+fragIndex(2)+fragTotal(2)+payloadLen(2)
	maxFragPayload = 1400          // bytes of body per 'B' fragment datagram
	firstFragCap   = 700           // headroom keeps the envelope frame under the 1500 MTU
	bearerTimeout  = 20 * time.Second
)

// udpDataBearer is the process-wide singleton, created in ensureDirectLinkMgr
// (nat_traversal.go) when the shared UDP socket is available and the feature is
// not disabled via udp_data_bearer_enabled=false. It is nil when unavailable.
var udpDataBearer *UDPDataBearer

// bearerEnvelope is the JSON metadata carried in a request/response envelope
// frame, followed by the first body fragment.
type bearerEnvelope struct {
	Method    string      `json:"m"` // HTTP method (request) / unused (response)
	Path      string      `json:"p"` // request path (already stripped of /network/{id})
	Query     string      `json:"q"` // original raw query string
	Status    int         `json:"s"` // HTTP status code (response only)
	Headers   http.Header `json:"h"` // forwarded request headers (request) / response headers
	BodyTotal int         `json:"bt"`
	FragTotal int         `json:"ft"`
}

// reasmMsg accumulates the fragments of one in-flight message (request on the
// server side, response on the client side) keyed by reqID.
type reasmMsg struct {
	isResp    bool
	meta      bearerEnvelope
	firstFrag []byte
	frags     map[int][]byte
}

// bearerResult is the fully reassembled response delivered to a waiting caller.
type bearerResult struct {
	status int
	header http.Header
	body   []byte
}

// UDPDataBearer carries relay requests/responses over a shared UDP socket. It
// is safe for concurrent use; one instance is wired onto natMgr.udpConn.
type UDPDataBearer struct {
	conn   *net.UDPConn
	nodeID string

	mu      sync.Mutex
	reasm   map[string]*reasmMsg
	waiters map[string]chan bearerResult
	seq     uint64
}

// NewUDPDataBearer builds a bearer bound to the shared UDP socket.
func NewUDPDataBearer(conn *net.UDPConn, nodeID string) *UDPDataBearer {
	return &UDPDataBearer{
		conn:    conn,
		nodeID:  nodeID,
		reasm:   make(map[string]*reasmMsg),
		waiters: make(map[string]chan bearerResult),
	}
}

// isDataFrame reports whether a raw datagram is a UDP-data-bearer frame.
func isDataFrame(b []byte) bool {
	return len(b) >= 4 && string(b[:4]) == dataMagic
}

// ---------------------------------------------------------------------------
// Sending
// ---------------------------------------------------------------------------

// sendFrames encodes meta + body into an envelope frame plus 'B' fragment
// frames and writes them to to. The frame layout is built with explicit
// make/copy/PutUint16 (never chained appends) so the header can never be
// corrupted by slice-capacity aliasing.
//
// Frame (fixed 27-byte header, then payload):
//
//	magic(4) | typ(1) | reqID(16) | fragIndex(2) | fragTotal(2) | payloadLen(2) | payload
//
// Envelope payload: jsonLen(4) | json(meta) | firstFrag.
// 'B' payload: chunk (one fragment of the body).
func (b *UDPDataBearer) sendFrames(to *net.UDPAddr, reqID []byte, typ byte, meta bearerEnvelope, body []byte) error {
	meta.BodyTotal = len(body)
	prov, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("bearer: marshal meta: %w", err)
	}
	avail := maxFragPayload - len(prov) - frameHeaderLen
	if avail < 0 {
		avail = 0
	}
	if avail > firstFragCap {
		avail = firstFragCap
	}
	firstFrag := body
	if len(firstFrag) > avail {
		firstFrag = body[:avail]
	}
	remaining := body[len(firstFrag):]

	fragTotal := 1
	if len(remaining) > 0 {
		fragTotal += (len(remaining) + maxFragPayload - 1) / maxFragPayload
	}
	meta.FragTotal = fragTotal

	jsonMeta, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("bearer: marshal meta: %w", err)
	}

	// Envelope frame.
	payload := make([]byte, 0, 4+len(jsonMeta)+len(firstFrag))
	payload = appendUint32(payload, uint32(len(jsonMeta)))
	payload = append(payload, jsonMeta...)
	payload = append(payload, firstFrag...)
	frame := make([]byte, frameHeaderLen+len(payload))
	copy(frame[0:4], dataMagicBytes())
	frame[4] = typ
	copy(frame[5:21], reqID)
	binary.BigEndian.PutUint16(frame[21:23], 0) // fragIndex
	binary.BigEndian.PutUint16(frame[23:25], uint16(fragTotal))
	binary.BigEndian.PutUint16(frame[25:27], uint16(len(payload)))
	copy(frame[frameHeaderLen:], payload)
	if err := b.writeTo(frame, to); err != nil {
		return err
	}

	// 'B' fragment frames.
	off := 0
	for i := 1; i < fragTotal; i++ {
		end := off + maxFragPayload
		if end > len(remaining) {
			end = len(remaining)
		}
		chunk := remaining[off:end]
		off = end
		fr := make([]byte, frameHeaderLen+len(chunk))
		copy(fr[0:4], dataMagicBytes())
		fr[4] = 'B'
		copy(fr[5:21], reqID)
		binary.BigEndian.PutUint16(fr[21:23], uint16(i))
		binary.BigEndian.PutUint16(fr[23:25], uint16(fragTotal))
		binary.BigEndian.PutUint16(fr[25:27], uint16(len(chunk)))
		copy(fr[frameHeaderLen:], chunk)
		if err := b.writeTo(fr, to); err != nil {
			return err
		}
	}
	return nil
}

func (b *UDPDataBearer) writeTo(frame []byte, to *net.UDPAddr) error {
	if _, err := b.conn.WriteToUDP(frame, to); err != nil {
		return fmt.Errorf("bearer: write: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Receiving
// ---------------------------------------------------------------------------

// HandleInbound is called from udpRecvLoop for each data-frame datagram. It is
// panic-safe and never blocks the caller for long: request handling is deferred
// to a goroutine; response reassembly signals a waiter without blocking.
func (b *UDPDataBearer) HandleInbound(frame []byte, from *net.UDPAddr) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("bearer: recovered from panic in inbound handler", "panic", r)
		}
	}()
	if len(frame) < frameHeaderLen {
		return
	}
	if string(frame[:4]) != dataMagic {
		return
	}
	typ := frame[4]
	reqID := string(frame[5:21])
	payloadLen := binary.BigEndian.Uint16(frame[25:27])
	if int(payloadLen) > len(frame)-frameHeaderLen {
		return
	}
	payload := frame[frameHeaderLen : frameHeaderLen+int(payloadLen)]

	switch typ {
	case 'Q', 'S':
		var meta bearerEnvelope
		if len(payload) < 4 {
			return
		}
		jsonLen := binary.BigEndian.Uint32(payload[0:4])
		if int(jsonLen) > len(payload)-4 {
			return
		}
		if err := json.Unmarshal(payload[4:4+int(jsonLen)], &meta); err != nil {
			slog.Debug("bearer: bad envelope json", "error", err)
			return
		}
		firstFrag := payload[4+int(jsonLen):]
		rm := &reasmMsg{isResp: typ == 'S', meta: meta, firstFrag: firstFrag, frags: map[int][]byte{}}
		b.mu.Lock()
		b.reasm[reqID] = rm
		b.mu.Unlock()
		if meta.FragTotal <= 1 {
			b.onComplete(reqID, from)
		}
	case 'B':
		fragIndex := binary.BigEndian.Uint16(frame[21:23])
		b.mu.Lock()
		rm, ok := b.reasm[reqID]
		b.mu.Unlock()
		if !ok {
			return
		}
		b.mu.Lock()
		rm.frags[int(fragIndex)] = append([]byte(nil), payload...)
		complete := len(rm.frags) >= int(rm.meta.FragTotal)-1
		b.mu.Unlock()
		if complete {
			b.onComplete(reqID, from)
		}
	}
}

// onComplete finalizes a reassembled message and either signals the waiting
// client (response) or serves the request (server side) in a goroutine.
func (b *UDPDataBearer) onComplete(reqID string, from *net.UDPAddr) {
	b.mu.Lock()
	rm, ok := b.reasm[reqID]
	if !ok {
		b.mu.Unlock()
		return
	}
	delete(b.reasm, reqID)

	if rm.isResp {
		body := b.assemble(rm)
		ch, hasWaiter := b.waiters[reqID]
		if hasWaiter {
			delete(b.waiters, reqID)
		}
		b.mu.Unlock()
		if hasWaiter {
			select {
			case ch <- bearerResult{status: rm.meta.Status, header: rm.meta.Headers, body: body}:
			default:
			}
		}
		return
	}
	b.mu.Unlock()

	// Server side: serve the request and send the response back.
	go b.serveReq(reqID, rm, from)
}

// assemble concatenates the first fragment and all 'B' fragments in order.
func (b *UDPDataBearer) assemble(rm *reasmMsg) []byte {
	total := len(rm.firstFrag)
	for i := 1; i < rm.meta.FragTotal; i++ {
		total += len(rm.frags[i])
	}
	out := make([]byte, 0, total)
	out = append(out, rm.firstFrag...)
	for i := 1; i < rm.meta.FragTotal; i++ {
		out = append(out, rm.frags[i]...)
	}
	return out
}

// serveReq handles an inbound relay request over UDP and returns the response.
func (b *UDPDataBearer) serveReq(reqID string, rm *reasmMsg, from *net.UDPAddr) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("bearer: recovered from panic serving request", "panic", r)
		}
	}()

	body := b.assemble(rm)
	rec := &bearerRespRecorder{}
	if relayDispatchHandler == nil {
		rec.WriteHeader(http.StatusServiceUnavailable)
	} else {
		req := &http.Request{
			Method: rm.meta.Method,
			URL:    &url.URL{Path: rm.meta.Path, RawQuery: rm.meta.Query},
			Header: rm.meta.Headers,
			Body:   io.NopCloser(bytes.NewReader(body)),
		}
		req.RequestURI = rm.meta.Path
		if rm.meta.Query != "" {
			req.RequestURI += "?" + rm.meta.Query
		}
		relayDispatchHandler.ServeHTTP(rec, req)
	}
	if rec.code == 0 {
		rec.code = http.StatusOK
	}

	respMeta := bearerEnvelope{
		Status:  rec.code,
		Headers: rec.header,
	}
	// Keep the internal hop header from leaking to the next hop.
	if respMeta.Headers == nil {
		respMeta.Headers = http.Header{}
	}
	respMeta.Headers.Del(headerRelayHop)
	respMeta.Headers.Del(headerRelayFrom)

	reqIDBytes := []byte(reqID)
	if len(reqIDBytes) > 16 {
		reqIDBytes = reqIDBytes[:16]
	} else if len(reqIDBytes) < 16 {
		padded := make([]byte, 16)
		copy(padded, reqIDBytes)
		reqIDBytes = padded
	}
	if err := b.sendFrames(from, reqIDBytes, 'S', respMeta, rec.body); err != nil {
		slog.Warn("bearer: failed to send response", "peer", from, "error", err)
	}
}

// ---------------------------------------------------------------------------
// Client entry point (used by relayToRemote)
// ---------------------------------------------------------------------------

// RelayOverUDP sends r to the peer over the verified direct UDP link and writes
// the reassembled response to w. It returns false (without writing anything) if
// the bearer cannot be used or the exchange fails, so the caller falls back to
// the HTTPS reverse proxy.
func (b *UDPDataBearer) RelayOverUDP(w http.ResponseWriter, r *http.Request, peerNodeID, restPath string, body []byte, hopCount int) bool {
	if directLinkMgr == nil {
		return false
	}
	peerAddr := directLinkMgr.DirectAddr(peerNodeID)
	if peerAddr == nil {
		return false
	}
	// Streaming responses are not carried over UDP (no token streaming); let
	// HTTPS relay handle them so behaviour is unchanged.
	var probe struct {
		Stream bool `json:"stream"`
	}
	if json.Unmarshal(body, &probe) == nil && probe.Stream {
		return false
	}

	reqIDBytes := make([]byte, 16)
	if _, err := rand.Read(reqIDBytes); err != nil {
		return false
	}
	reqID := string(reqIDBytes)

	// Mirror the HTTPS Director's header treatment: strip the consumer key,
	// then attach this node's signed relay-forward identity so the peer verifies
	// it exactly like an HTTPS relay forward.
	hdrs := http.Header{}
	for k, vs := range r.Header {
		if k == "Authorization" || k == "X-OMP-KeyType" {
			continue
		}
		for _, v := range vs {
			hdrs.Add(k, v)
		}
	}
	relayFrom := ""
	if netMgr != nil {
		relayFrom = netMgr.GetNodeID()
	}
	sig, ts := signRelayForward(relayFrom, r.Method, restPath, body)
	hdrs.Set(headerRelayHop, strconv.Itoa(hopCount+1))
	if relayFrom != "" {
		hdrs.Set("X-Node-ID", relayFrom)
		hdrs.Set("X-Node-Auth", relayFrom)
		if sig != "" {
			hdrs.Set(headerRelaySig, sig)
			hdrs.Set(headerRelayTs, ts)
		}
	}

	meta := bearerEnvelope{
		Method:  r.Method,
		Path:    restPath,
		Query:   r.URL.RawQuery,
		Headers: hdrs,
	}

	ch := make(chan bearerResult, 1)
	b.mu.Lock()
	b.waiters[reqID] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.waiters, reqID)
		b.mu.Unlock()
	}()

	if err := b.sendFrames(peerAddr, reqIDBytes, 'Q', meta, body); err != nil {
		slog.Warn("bearer: failed to send request", "peer", peerNodeID, "error", err)
		return false
	}

	select {
	case res := <-ch:
		for k, vs := range res.header {
			if k == "X-OpenModelPool-Agent-Hop" || k == "X-Node-ID" || k == "X-Node-Auth" ||
				k == headerRelaySig || k == headerRelayTs {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(res.status)
		if _, err := w.Write(res.body); err != nil {
			slog.Debug("bearer: client write failed", "error", err)
		}
		return true
	case <-time.After(bearerTimeout):
		slog.Warn("bearer: response timeout; falling back to HTTPS relay", "peer", peerNodeID)
		return false
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func dataMagicBytes() []byte { return []byte(dataMagic) }

func appendUint16(b []byte, v uint16) []byte {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, v)
	return append(b, buf...)
}

func appendUint32(b []byte, v uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, v)
	return append(b, buf...)
}

// bearerRespRecorder captures an http.Response into memory so it can be sent
// back over UDP. It does not stream.
type bearerRespRecorder struct {
	mu     sync.Mutex
	code   int
	header http.Header
	body   []byte
}

func (r *bearerRespRecorder) Header() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}

func (r *bearerRespRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.body = append(r.body, b...)
	r.mu.Unlock()
	return len(b), nil
}

func (r *bearerRespRecorder) WriteHeader(c int) {
	r.mu.Lock()
	if r.code == 0 {
		r.code = c
	}
	r.mu.Unlock()
}

// Flush is a no-op so handlers that type-assert http.Flusher never panic; the
// UDP bearer buffers the whole response before sending, so flushing is moot.
func (r *bearerRespRecorder) Flush() {}
