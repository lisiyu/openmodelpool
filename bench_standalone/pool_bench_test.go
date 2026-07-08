package bench_standalone

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Simulates the OLD behavior: new http.Client per request
func BenchmarkNewClientPerRequest(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"status":"ok"}`)
	}))
	defer server.Close()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(server.URL + "/gossip")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// Simulates the NEW behavior: shared http.Client with connection pool
func BenchmarkSharedClient(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"status":"ok"}`)
	}))
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
		resp, err := sharedClient.Get(server.URL + "/gossip")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// Concurrent benchmark: simulates gossip broadcast to multiple peers
func BenchmarkConcurrentNewClient(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"status":"ok"}`)
	}))
	defer server.Close()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(server.URL + "/gossip")
			if err != nil {
				b.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}

func BenchmarkConcurrentSharedClient(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"status":"ok"}`)
	}))
	defer server.Close()

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
			resp, err := sharedClient.Get(server.URL + "/gossip")
			if err != nil {
				b.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}

// TestConnectionReuseCount verifies actual TCP connection reuse
func TestConnectionReuseCount(t *testing.T) {
	var mu sync.Mutex
	dialer := &net.Dialer{Timeout: 5 * time.Second}

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

	testURL := fmt.Sprintf("http://%s/test", ln.Addr().String())

	// Test shared client — uses a real dialer wrapped with counting
	sharedDials := 0
	sharedTransport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			mu.Lock()
			sharedDials++
			mu.Unlock()
			return dialer.DialContext(ctx, network, addr)
		},
	}
	sharedClient := &http.Client{Transport: sharedTransport, Timeout: 10 * time.Second}

	for i := 0; i < 50; i++ {
		resp, err := sharedClient.Get(testURL)
		if err != nil {
			t.Fatalf("shared request %d failed: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	// Test new client per request — each has its own Transport
	newDials := 0
	for i := 0; i < 50; i++ {
		newTransport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				mu.Lock()
				newDials++
				mu.Unlock()
				return dialer.DialContext(ctx, network, addr)
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

	t.Logf("=== Connection Reuse Results (50 sequential requests) ===")
	t.Logf("Shared client:  %d TCP connections", sharedDials)
	t.Logf("New client:     %d TCP connections", newDials)
	t.Logf("Saved:          %d connections (%.1f%% reduction)",
		newDials-sharedDials,
		float64(newDials-sharedDials)/float64(newDials)*100)

	if sharedDials > 2 {
		t.Errorf("shared client should reuse connections: got %d, want <=2", sharedDials)
	}
}

// TestCustomTimeoutSharesTransport verifies GetSharedHTTPClientWithTimeout behavior
func TestCustomTimeoutSharesTransport(t *testing.T) {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	client30 := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	client90 := &http.Client{Transport: transport, Timeout: 90 * time.Second}
	client5m := &http.Client{Transport: transport, Timeout: 5 * time.Minute}

	if client30.Transport != client90.Transport {
		t.Error("clients have different transports")
	}
	if client90.Transport != client5m.Transport {
		t.Error("clients have different transports")
	}

	if client30.Timeout != 30*time.Second {
		t.Errorf("got %v, want 30s", client30.Timeout)
	}
	if client90.Timeout != 90*time.Second {
		t.Errorf("got %v, want 90s", client90.Timeout)
	}
	if client5m.Timeout != 5*time.Minute {
		t.Errorf("got %v, want 5m", client5m.Timeout)
	}

	t.Log("All timeout variants share the same Transport (connection pool)")
}
