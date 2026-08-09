package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestPunchOfferExchange exercises the real offer-exchange path: node A POSTs
// its PunchOffer to node B over HTTP (as relayToRemote would), B starts a punch
// back, and both sides punch concurrently over real UDP sockets until a direct
// channel is established on each side. B's receive loop is test-owned here so we
// can observe exactly what frames arrive.
func TestPunchOfferExchange(t *testing.T) {
	aConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	bConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer aConn.Close()
	defer bConn.Close()

	aMgr := NewDirectLinkManager(aConn, "nodeA", aConn.LocalAddr().String(), aConn.LocalAddr().String(), true)
	// B uses a test-owned receive loop (ownRecv=false) so we can log arrivals.
	bMgr := NewDirectLinkManager(bConn, "nodeB", bConn.LocalAddr().String(), bConn.LocalAddr().String(), false)
	defer aMgr.Stop()
	defer bMgr.Stop()

	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, rerr := bConn.ReadFromUDP(buf)
			if rerr != nil {
				return
			}
			off, derr := DecodePunchOffer(buf[:n])
			if derr != nil {
				t.Logf("B recv non-punch frame from %v", addr)
				continue
			}
			t.Logf("B recv punch from %v nodeID=%q", addr, off.NodeID)
			bMgr.Ingest(off, addr)
		}
	}()

	bSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var off PunchOffer
		if err := json.NewDecoder(r.Body).Decode(&off); err != nil {
			w.WriteHeader(400)
			return
		}
		t.Logf("B http got offer nodeID=%q reflexive=%q nonceLen=%d", off.NodeID, off.ReflexiveAddr, len(off.Nonce))
		bMgr.BeginPunch(off, 30*time.Millisecond, 100)
		w.WriteHeader(200)
	}))
	defer bSrv.Close()

	aOffer, err := aMgr.Offer()
	if err != nil {
		t.Fatal(err)
	}
	go ExchangePunchWithPeer(bSrv.URL, aOffer)
	aMgr.BeginPunch(PunchOffer{NodeID: "nodeB", ReflexiveAddr: bConn.LocalAddr().String()}, 30*time.Millisecond, 100)

	time.Sleep(2 * time.Second)
	aDirect := aMgr.HasDirect("nodeB")
	bDirect := bMgr.HasDirect("nodeA")
	t.Logf("after 2s: A->B direct=%v, B->A direct=%v", aDirect, bDirect)

	if !aDirect || !bDirect {
		t.Fatalf("direct link not established via offer exchange: A->B=%v B->A=%v", aDirect, bDirect)
	}
}

// TestHandlePunchExchange verifies the HTTP handler parses a peer offer and
// triggers a punch without error. It uses the package-global directLinkMgr so
// the handler under test is exactly the production one.
func TestHandlePunchExchange(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	mgr := NewDirectLinkManager(conn, "self", conn.LocalAddr().String(), conn.LocalAddr().String(), true)
	defer mgr.Stop()

	old := directLinkMgr
	directLinkMgr = mgr
	defer func() { directLinkMgr = old }()

	offer, err := NewPunchOffer("peerX", "127.0.0.1:12345", "127.0.0.1:12345")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(offer)
	req := httptest.NewRequest(http.MethodPost, "/network/__punch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handlePunchExchange(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Accepted bool   `json:"accepted"`
		Peer     string `json:"peer"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted || resp.Peer != "peerX" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
