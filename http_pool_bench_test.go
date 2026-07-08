package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ============================================================
// Benchmark: Connection Pool Reuse vs New Client Per Request
// ============================================================

func benchServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"status":"ok","node_id":"test"}`)
	}))
}

// BenchmarkNewClientPerRequest simulates the OLD behavior.
func BenchmarkNewClientPerRequest(b *testing.B) {
	server := benchServer()
	defer server.Close()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(server.URL + "/federation/gossip")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkSharedClient simulates the NEW behavior.
func BenchmarkSharedClient(b *testing.B) {
	server := benchServer()
	defer server.Close()

	sharedClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    false,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		Timeout: 30 * time.Second,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		resp, err := sharedClient.Get(server.URL + "/federation/gossip")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkConcurrentRequests tests concurrent access patterns.
func BenchmarkConcurrentRequests(b *testing.B) {
	server := benchServer()
	defer server.Close()

	b.Run("NewClient", func(b *testing.B) {
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				client := &http.Client{Timeout: 10 * time.Second}
				resp, err := client.Get(server.URL + "/federation/gossip")
				if err != nil {
					b.Fatal(err)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		})
	})

	b.Run("SharedClient", func(b *testing.B) {
		sharedClient := &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
			Timeout: 30 * time.Second,
		}

		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				resp, err := sharedClient.Get(server.URL + "/federation/gossip")
				if err != nil {
					b.Fatal(err)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		})
	})
}

// TestConnectionReuse verifies that the shared client reuses connections.
func TestConnectionReuse(t *testing.T) {
	var mu sync.Mutex

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			fmt.Fprintf(w, `{"ok":true}`)
		}),
	}
	go server.Serve(ln)
	defer server.Close()

	addr := ln.Addr().String()
	testURL := fmt.Sprintf("http://%s/test", addr)

	// --- Shared client test ---
	dialCount := 0
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	origDial := transport.DialContext
	transport.DialContext = func(ctx interface{ Deadline() (time.Time, bool) }, network, addr string) (net.Conn, error) {
		mu.Lock()
		dialCount++
		mu.Unlock()
		return origDial(ctx, network, addr)
	}
	sharedClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}

	for i := 0; i < 20; i++ {
		resp, err := sharedClient.Get(testURL)
		if err != nil {
			t.Fatalf("shared request %d failed: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	// --- New client per request test ---
	newClientDials := 0
	for i := 0; i < 20; i++ {
		newTransport := &http.Transport{
			DialContext: func(ctx interface{ Deadline() (time.Time, bool) }, network, addr string) (net.Conn, error) {
				mu.Lock()
				newClientDials++
				mu.Unlock()
				return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, addr)
			},
		}
		client := &http.Client{Transport: newTransport, Timeout: 10 * time.Second}
		resp, err := client.Get(testURL)
		if err != nil {
			t.Fatalf("new client request %d failed: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	t.Logf("Shared client:  20 requests, %d TCP connections", dialCount)
	t.Logf("New client:     20 requests, %d TCP connections", newClientDials)
	t.Logf("Connections saved: %d (%.0f%% reduction)",
		newClientDials-dialCount,
		float64(newClientDials-dialCount)/float64(newClientDials)*100)

	if dialCount > 2 {
		t.Errorf("shared client created %d connections, expected <=2", dialCount)
	}
}

// TestGetSharedHTTPClientWithTimeout verifies shared transport across clients.
func TestGetSharedHTTPClientWithTimeout(t *testing.T) {
	initSharedHTTPClient()

	client30 := GetSharedHTTPClient()
	client90 := GetSharedHTTPClientWithTimeout(90 * time.Second)
	client5m := GetSharedHTTPClientWithTimeout(5 * time.Minute)

	if client30.Transport != internalTransport {
		t.Error("default client does not share internalTransport")
	}
	if client90.Transport != internalTransport {
		t.Error("90s client does not share internalTransport")
	}
	if client5m.Transport != internalTransport {
		t.Error("5min client does not share internalTransport")
	}

	if client30.Timeout != 30*time.Second {
		t.Errorf("default timeout = %v, want 30s", client30.Timeout)
	}
	if client90.Timeout != 90*time.Second {
		t.Errorf("90s timeout = %v, want 90s", client90.Timeout)
	}
	if client5m.Timeout != 5*time.Minute {
		t.Errorf("5min timeout = %v, want 5m", client5m.Timeout)
	}

	t.Log("All clients share internalTransport")
	t.Log("Timeouts: default=30s, relay=90s, stream=5m")
}
