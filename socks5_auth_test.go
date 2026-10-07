package main

import (
	"net/url"
	"testing"
)

// TestSocks5ProxyAuth verifies that SOCKS5 credentials embedded in the proxy
// URL userinfo are extracted and passed to the dialer, instead of being
// silently dropped (which previously made every credential-bearing proxy URL
// fail at the SOCKS handshake, misreported as a provider key error).
func TestSocks5ProxyAuth(t *testing.T) {
	cases := []struct {
		name     string
		rawURL   string
		wantUser string
		wantPass string
		wantNil  bool
	}{
		{name: "no userinfo", rawURL: "socks5://127.0.0.1:10808", wantNil: true},
		{name: "no userinfo socks5h", rawURL: "socks5h://127.0.0.1:10808", wantNil: true},
		{name: "user and password", rawURL: "socks5://testuser:testpass0123456789abcdef@127.0.0.1:10808", wantUser: "testuser", wantPass: "testpass0123456789abcdef"},
		{name: "user and password socks5h", rawURL: "socks5h://u:p@example.com:1080", wantUser: "u", wantPass: "p"},
		{name: "user only", rawURL: "socks5://onlyuser@127.0.0.1:10808", wantUser: "onlyuser", wantPass: ""},
		{name: "magicdns host", rawURL: "socks5://siderproxy:secret@zuinew.dab-huchen.ts.net:10808", wantUser: "siderproxy", wantPass: "secret"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.rawURL)
			if err != nil {
				t.Fatalf("url.Parse(%q) failed: %v", tc.rawURL, err)
			}
			auth := socks5ProxyAuth(u)
			if tc.wantNil {
				if auth != nil {
					t.Fatalf("socks5ProxyAuth(%q) = %+v, want nil", tc.rawURL, auth)
				}
				return
			}
			if auth == nil {
				t.Fatalf("socks5ProxyAuth(%q) = nil, want user=%q", tc.rawURL, tc.wantUser)
			}
			if auth.User != tc.wantUser || auth.Password != tc.wantPass {
				t.Fatalf("socks5ProxyAuth(%q) = user=%q pass=%q, want user=%q pass=%q",
					tc.rawURL, auth.User, auth.Password, tc.wantUser, tc.wantPass)
			}
		})
	}
}

// TestSocks5ProxyAuth_NilURL guards against a nil URL input.
func TestSocks5ProxyAuth_NilURL(t *testing.T) {
	if auth := socks5ProxyAuth(nil); auth != nil {
		t.Fatalf("socks5ProxyAuth(nil) = %+v, want nil", auth)
	}
}
