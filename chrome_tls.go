package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	utls "github.com/bogdanfinn/utls"
)

// chromeTransport is an http.Transport that mimics Chrome's TLS fingerprint
// using uTLS. This bypasses Cloudflare's bot detection which blocks Go's
// default TLS ClientHello.
var chromeTransport = &http.Transport{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     90 * time.Second,
	DisableCompression:  false,
	// Custom DialTLSContext that uses uTLS with Chrome fingerprint
	DialTLSContext: chromeDialTLSContext,
}

// chromeDialer is the underlying dialer (respects OPENMODELPOOL_PREFER_IPV4)
var chromeDialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
}

// chromeDialTLSContext dials TCP then performs TLS handshake with Chrome's fingerprint
func chromeDialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Force IPv4 if requested
	if preferIPv4() {
		switch network {
		case "tcp":
			network = "tcp4"
		}
	}

	// Dial TCP
	rawConn, err := chromeDialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	// Extract host for SNI
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		rawConn.Close()
		return nil, err
	}

	// Create uTLS client with Chrome fingerprint
	uTlsConn := utls.UClient(rawConn, &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
	}, utls.HelloChrome_Auto, false, false)

	// Perform handshake with context
	if err := uTlsConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, err
	}

	return uTlsConn, nil
}

// siderChromeClient returns an HTTP client with Chrome TLS fingerprint for sider.ai.
// This bypasses Cloudflare bot detection. Only used for direct connections (no proxy).
func siderChromeClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport:     chromeTransport,
		Timeout:       timeout,
		CheckRedirect: ssrfCheckRedirect,
	}
}

// siderHTTPClient returns the appropriate HTTP client for sider.ai requests.
// If the provider has a proxy configured, use the standard proxy client.
// Otherwise, use the Chrome-fingerprint uTLS client to bypass Cloudflare bot detection.
func siderHTTPClient(p Provider, timeout time.Duration) *http.Client {
	if p.Proxy != "" {
		return proxyHTTPClient(p, timeout)
	}
	return siderChromeClient(timeout)
}
