package main

import (
	"net"
	"testing"
)

// TestIsPrivateIPv4 verifies RFC1918 private-range detection.
func TestIsPrivateIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.254", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		{"192.168.0.1", true},
		{"192.168.255.254", true},
		{"192.167.0.1", false},
		{"169.254.1.1", false},   // APIPA
		{"127.0.0.1", false},     // loopback
		{"8.8.8.8", false},       // public
		{"::1", false},           // IPv6
		{"fe80::1", false},       // IPv6 link-local
	}
	for _, c := range cases {
		got := isPrivateIPv4(net.ParseIP(c.ip))
		if got != c.want {
			t.Errorf("isPrivateIPv4(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

// TestIsUsableLANIP verifies that loopback, APIPA/link-local, multicast,
// unspecified and non-IPv4 addresses are rejected.
func TestIsUsableLANIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"169.254.227.135", false}, // APIPA link-local (the bug address)
		{"169.254.0.1", false},
		{"127.0.0.1", false},       // loopback
		{"0.0.0.0", false},         // unspecified
		{"224.0.0.1", false},       // multicast
		{"255.255.255.255", false}, // limited broadcast
		{"fe80::1", false},         // IPv6 link-local
		{"::1", false},             // IPv6 loopback
		{"192.168.1.10", true},     // private
		{"10.0.0.5", true},         // private
		{"172.16.5.5", true},       // private
		{"8.8.8.8", true},          // public IPv4 still usable as fallback
	}
	for _, c := range cases {
		got := isUsableLANIP(net.ParseIP(c.ip))
		if got != c.want {
			t.Errorf("isUsableLANIP(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

// TestPickLANIP verifies the selection rules: prefer private over APIPA, never
// return APIPA, prefer private over public, and fall back to public when only
// non-private usable addresses exist.
func TestPickLANIP(t *testing.T) {
	cases := []struct {
		name string
		ips  []string
		want string
	}{
		{
			name: "prefer private over APIPA",
			ips:  []string{"169.254.227.135", "192.168.1.10"},
			want: "192.168.1.10",
		},
		{
			name: "APIPA only is filtered out",
			ips:  []string{"169.254.227.135"},
			want: "",
		},
		{
			name: "loopback and APIPA filtered, public fallback",
			ips:  []string{"127.0.0.1", "169.254.1.1", "203.0.113.5"},
			want: "203.0.113.5",
		},
		{
			name: "prefer 192.168 over public",
			ips:  []string{"8.8.8.8", "192.168.0.50"},
			want: "192.168.0.50",
		},
		{
			name: "first private in order wins (172.16 before 10)",
			ips:  []string{"172.16.0.1", "10.0.0.1"},
			want: "172.16.0.1",
		},
		{
			name: "empty input",
			ips:  []string{},
			want: "",
		},
		{
			name: "multicast filtered out",
			ips:  []string{"224.0.0.1", "192.168.1.20"},
			want: "192.168.1.20",
		},
		{
			name: "APIPA before private still yields private",
			ips:  []string{"169.254.1.1", "172.16.0.9", "192.168.1.5"},
			want: "172.16.0.9",
		},
	}
	for _, c := range cases {
		ips := make([]net.IP, 0, len(c.ips))
		for _, s := range c.ips {
			ips = append(ips, net.ParseIP(s))
		}
		got := pickLANIP(ips)
		if got != c.want {
			t.Errorf("%s: pickLANIP = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestGetLocalIPNotAPIPA is a smoke test on the real machine: getLocalIP must
// never return a loopback or APIPA/link-local address (the original bug).
func TestGetLocalIPNotAPIPA(t *testing.T) {
	ip := getLocalIP()
	if ip == "" {
		t.Skip("no usable LAN interface available in this environment")
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("getLocalIP() returned unparseable value %q", ip)
	}
	if !isUsableLANIP(parsed) {
		t.Errorf("getLocalIP() returned unusable address %q (loopback/link-local/multicast)", ip)
	}
	if parsed.IsLinkLocalUnicast() {
		t.Errorf("getLocalIP() returned link-local/APIPA address %q", ip)
	}
}

// TestPickLANIPPrefersPrivateOverAPIPA strengthens the regression coverage for
// the reported Windows bug (service overview showed 169.254.227.135). When the
// candidate IPs contain BOTH an APIPA/link-local address (169.254.x.x) AND an
// RFC1918 private address (192.168.x.x), pickLANIP must return the private
// address and must never return the APIPA one.
func TestPickLANIPPrefersPrivateOverAPIPA(t *testing.T) {
	cases := []struct {
		name string
		ips  []string
		want string
	}{
		{
			name: "APIPA before 192.168 yields 192.168",
			ips:  []string{"169.254.227.135", "192.168.1.10"},
			want: "192.168.1.10",
		},
		{
			name: "192.168 before APIPA yields 192.168",
			ips:  []string{"192.168.1.10", "169.254.227.135"},
			want: "192.168.1.10",
		},
		{
			name: "APIPA plus 10.x private yields 10.x",
			ips:  []string{"169.254.1.1", "10.0.0.5"},
			want: "10.0.0.5",
		},
		{
			name: "APIPA plus 172.16 private yields 172.16",
			ips:  []string{"169.254.99.99", "172.16.0.9"},
			want: "172.16.0.9",
		},
		{
			name: "APIPA + public + 192.168 still yields 192.168",
			ips:  []string{"169.254.1.1", "8.8.8.8", "192.168.0.50"},
			want: "192.168.0.50",
		},
		{
			name: "two APIPA plus one 192.168 yields 192.168",
			ips:  []string{"169.254.227.135", "169.254.1.1", "192.168.5.5"},
			want: "192.168.5.5",
		},
	}
	for _, c := range cases {
		ips := make([]net.IP, 0, len(c.ips))
		for _, s := range c.ips {
			ips = append(ips, net.ParseIP(s))
		}
		got := pickLANIP(ips)
		if got != c.want {
			t.Errorf("%s: pickLANIP = %q, want %q", c.name, got, c.want)
		}
		// Strong regression assertion: the result must never be an APIPA address.
		if parsed := net.ParseIP(got); parsed != nil && parsed.IsLinkLocalUnicast() {
			t.Errorf("%s: pickLANIP returned link-local/APIPA address %q", c.name, got)
		}
	}
}
