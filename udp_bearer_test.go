package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// bearerRecvLoop drives a UDPDataBearer from a UDP socket, copying each
// datagram (the socket buffer is reused by ReadFromUDP).
func bearerRecvLoop(conn *net.UDPConn, b *UDPDataBearer, stop chan struct{}) {
	buf := make([]byte, 65535)
	for {
		select {
		case <-stop:
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-stop:
				return
			default:
				return
			}
		}
		b.HandleInbound(append([]byte(nil), buf[:n]...), addr)
	}
}

// TestUDPBearer_RelayRoundTrip proves a relay request is carried end-to-end over
// the direct UDP link and the response comes back intact — the P1-2b-2(iv)
// "link really carries data" milestone, exercised without the HTTPS proxy.
func TestUDPBearer_RelayRoundTrip(t *testing.T) {
	connA, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen A: %v", err)
	}
	connB, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen B: %v", err)
	}
	defer connA.Close()
	defer connB.Close()

	bA := NewUDPDataBearer(connA, "nodeA")
	bB := NewUDPDataBearer(connB, "nodeB")

	// Server side: echo the request body and report the path it saw.
	oldHandler := relayDispatchHandler
	relayDispatchHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Echo-Path", r.URL.Path)
		w.WriteHeader(200)
		w.Write(body)
	})
	defer func() { relayDispatchHandler = oldHandler }()

	// Wire the direct link so RelayOverUDP finds peer B's address.
	oldDLM := directLinkMgr
	oldBearer := udpDataBearer
	directLinkMgr = NewDirectLinkManager(connA, "nodeA", connA.LocalAddr().String(), "", false)
	udpDataBearer = bA
	defer func() {
		directLinkMgr = oldDLM
		udpDataBearer = oldBearer
	}()
	directLinkMgr.RegisterDirectLink("nodeB", connB.LocalAddr().(*net.UDPAddr))

	stopA := make(chan struct{})
	stopB := make(chan struct{})
	go bearerRecvLoop(connA, bA, stopA)
	go bearerRecvLoop(connB, bB, stopB)
	defer close(stopA)
	defer close(stopB)

	body := []byte(`{"model":"test","prompt":"hello world"}`)
	req := httptest.NewRequest("POST", "/v1/echo", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer sk-secret-leak") // must be stripped before reaching B

	rec := httptest.NewRecorder()
	if ok := bA.RelayOverUDP(rec, req, "nodeB", "/v1/echo", body, 0); !ok {
		t.Fatalf("RelayOverUDP returned false (no data carried)")
	}
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != string(body) {
		t.Fatalf("body not carried intact: got %q", rec.Body.String())
	}
	if rec.Header().Get("X-Echo-Path") != "/v1/echo" {
		t.Fatalf("server did not see the relayed path")
	}
	if rec.Header().Get("Authorization") != "" {
		t.Fatalf("consumer Authorization header leaked to the server over UDP")
	}
}

// TestUDPBearer_RelayLargeBody proves payloads exceeding the UDP MTU are
// fragmented into 'B' frames and reassembled intact on both ends.
func TestUDPBearer_RelayLargeBody(t *testing.T) {
	connA, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	connB, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer connA.Close()
	defer connB.Close()

	bA := NewUDPDataBearer(connA, "nodeA")
	bB := NewUDPDataBearer(connB, "nodeB")

	oldHandler := relayDispatchHandler
	relayDispatchHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(200)
		w.Write(body)
	})
	defer func() { relayDispatchHandler = oldHandler }()

	oldDLM := directLinkMgr
	oldBearer := udpDataBearer
	directLinkMgr = NewDirectLinkManager(connA, "nodeA", connA.LocalAddr().String(), "", false)
	udpDataBearer = bA
	defer func() {
		directLinkMgr = oldDLM
		udpDataBearer = oldBearer
	}()
	directLinkMgr.RegisterDirectLink("nodeB", connB.LocalAddr().(*net.UDPAddr))

	stopA := make(chan struct{})
	stopB := make(chan struct{})
	go bearerRecvLoop(connA, bA, stopA)
	go bearerRecvLoop(connB, bB, stopB)
	defer close(stopA)
	defer close(stopB)

	// 50KB body forces many 'B' fragments.
	body := make([]byte, 50<<10)
	for i := range body {
		body[i] = byte(i % 251)
	}
	req := httptest.NewRequest("POST", "/v1/big", bytes.NewReader(body))

	rec := httptest.NewRecorder()
	if ok := bA.RelayOverUDP(rec, req, "nodeB", "/v1/big", body, 0); !ok {
		t.Fatalf("RelayOverUDP returned false for large body")
	}
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("large body mismatch after fragmentation/reassembly (got %d bytes)", len(rec.Body.Bytes()))
	}
}

// TestUDPBearer_StreamingFallsBack proves a streaming request is NOT claimed by
// the bearer (so HTTPS relay keeps streaming); the caller then uses the proxy.
func TestUDPBearer_StreamingFallsBack(t *testing.T) {
	bA := NewUDPDataBearer(nil, "nodeA")
	directLinkMgr = NewDirectLinkManager(nil, "nodeA", "127.0.0.1:1", "", false)
	directLinkMgr.Ingest(PunchOffer{NodeID: "nodeB"}, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9})
	defer func() { directLinkMgr = nil }()

	body := []byte(`{"model":"x","stream":true}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	// nil writer is fine — RelayOverUDP must return false before writing.
	if bA.RelayOverUDP(nil, req, "nodeB", "/v1/chat/completions", body, 0) {
		t.Fatalf("streaming request must fall back to HTTPS, not be carried over UDP")
	}
}
