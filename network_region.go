package main

import (
	"math"
	"net/http/httptest"
	"testing"
)

// ============================================================
// Region Detection Tests
// ============================================================

func newTestRegionManager() *RegionManager {
	return &RegionManager{
		nodes:  make(map[string]*NodeRegion),
		config: DefaultRegionConfig(),
	}
}

func TestDetectRegion_IPv4(t *testing.T) {
	rm := newTestRegionManager()

	tests := []struct {
		name     string
		ip       string
		expected Region
	}{
		// Asia-Pacific ranges
		{"AP: China 36.x", "36.100.1.1", RegionAsiaPacific},
		{"AP: China 39.x", "39.96.1.1", RegionAsiaPacific},
		{"AP: China 42.x", "42.100.1.1", RegionAsiaPacific},
		{"AP: China 43.x", "43.100.1.1", RegionAsiaPacific},
		{"AP: China 49.x", "49.100.1.1", RegionAsiaPacific},
		{"AP: China 101.x", "101.100.1.1", RegionAsiaPacific},
		{"AP: China 103.x", "103.100.1.1", RegionAsiaPacific},
		{"AP: China 106.x", "106.100.1.1", RegionAsiaPacific},
		{"AP: China 110-126.x", "114.100.1.1", RegionAsiaPacific},
		{"AP: China 180.x", "180.100.1.1", RegionAsiaPacific},
		{"AP: China 182.x", "182.100.1.1", RegionAsiaPacific},
		{"AP: Japan 126.x", "126.100.1.1", RegionAsiaPacific},
		{"AP: China 202.x", "202.100.1.1", RegionAsiaPacific},
		{"AP: China 210.x", "210.100.1.1", RegionAsiaPacific},
		{"AP: China 220.x", "220.100.1.1", RegionAsiaPacific},

		// Europe ranges
		{"EU: 5.x", "5.100.1.1", RegionEurope},
		{"EU: 31.x", "31.100.1.1", RegionEurope},
		{"EU: 37.x", "37.100.1.1", RegionEurope},
		{"EU: 46.x", "46.100.1.1", RegionEurope},
		{"EU: 62.x", "62.100.1.1", RegionEurope},
		{"EU: 77.x", "77.100.1.1", RegionEurope},
		{"EU: 80-95.x", "85.100.1.1", RegionEurope},
		{"EU: 109.x", "109.100.1.1", RegionEurope},
		{"EU: 176.x", "176.100.1.1", RegionEurope},
		{"EU: 185.x", "185.100.1.1", RegionEurope},
		{"EU: 188.x", "188.100.1.1", RegionEurope},
		{"EU: 193.x", "193.100.1.1", RegionEurope},
		{"EU: 212.x", "212.100.1.1", RegionEurope},
		{"EU: 217.x", "217.100.1.1", RegionEurope},

		// Americas ranges
		{"US: 3.x", "3.100.1.1", RegionAmericas},
		{"US: 4.x", "4.100.1.1", RegionAmericas},
		{"US: 8.x (Google)", "8.8.8.8", RegionAmericas},
		{"US: 15.x", "15.100.1.1", RegionAmericas},
		{"US: 32.x", "32.100.1.1", RegionAmericas},
		{"US: 35.x", "35.100.1.1", RegionAmericas},
		{"US: 44.x", "44.100.1.1", RegionAmericas},
		{"US: 50-57.x", "52.100.1.1", RegionAmericas},
		{"US: 63-76.x", "70.100.1.1", RegionAmericas},
		{"US: 96-100.x", "98.100.1.1", RegionAmericas},
		{"US: 104.x", "104.100.1.1", RegionAmericas},
		{"US: 140-149.x", "142.100.1.1", RegionAmericas},
		{"US: 154-174.x", "160.100.1.1", RegionAmericas},
		{"US: 184.x", "184.100.1.1", RegionAmericas},
		{"US: 198.x", "198.100.1.1", RegionAmericas},
		{"US: 204-209.x", "206.100.1.1", RegionAmericas},

		// Special cases
		{"Private 192.168.x", "192.168.1.1", RegionUnknown},
		{"Private 10.x", "10.0.0.1", RegionUnknown},
		{"Loopback", "127.0.0.1", RegionUnknown},
		{"Link local", "169.254.1.1", RegionUnknown},
		{"Empty IP", "", RegionUnknown},
		{"Invalid IP", "not-an-ip", RegionUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rm.DetectRegion("test-node", tt.ip)
			if result != tt.expected {
				t.Errorf("DetectRegion(%q) = %q, want %q", tt.ip, result, tt.expected)
			}
		})
	}
}

func TestDetectRegion_IPv4WithPort(t *testing.T) {
	rm := newTestRegionManager()
	// Should extract host from host:port
	result := rm.DetectRegion("test-node", "8.8.8.8:443")
	if result != RegionAmericas {
		t.Errorf("DetectRegion with port = %q, want americas", result)
	}
}

func TestDetectRegion_IPv6(t *testing.T) {
	rm := newTestRegionManager()

	tests := []struct {
		name     string
		ip       string
		expected Region
	}{
		{"IPv6 AP prefix 2001:02xx", "2001:0200::1", RegionAsiaPacific},
		{"IPv6 EU prefix 2001:04xx", "2001:0400::1", RegionEurope},
		{"IPv6 US prefix 2001:06xx", "2001:0600::1", RegionAmericas},
		{"IPv6 AP prefix 2400", "2400::1", RegionAsiaPacific},
		{"IPv6 US prefix 2600", "2600::1", RegionAmericas},
		{"IPv6 EU prefix 2a00", "2a00::1", RegionEurope},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rm.DetectRegion("test-node", tt.ip)
			if result != tt.expected {
				t.Errorf("DetectRegion(%q) = %q, want %q", tt.ip, result, tt.expected)
			}
		})
	}
}

// ============================================================
// RegionManager Registration Tests
// ============================================================

func TestRegionManager_RegisterNode(t *testing.T) {
	rm := newTestRegionManager()

	rm.RegisterNode("mmx-node1", "8.8.8.8:443", "ip_detect")

	nr := rm.GetNodeRegion("mmx-node1")
	if nr == nil {
		t.Fatal("node region should be registered")
	}
	if nr.Region != RegionAmericas {
		t.Errorf("Region = %q, want americas", nr.Region)
	}
	if nr.Source != "ip_detect" {
		t.Errorf("Source = %q, want ip_detect", nr.Source)
	}
}

func TestRegionManager_RegisterNodeSelfReport(t *testing.T) {
	rm := newTestRegionManager()

	rm.RegisterNodeSelfReport("mmx-node1", "ap", "ap-cn", 39.9, 116.4)

	nr := rm.GetNodeRegion("mmx-node1")
	if nr == nil {
		t.Fatal("node region should be registered")
	}
	if nr.Region != RegionAsiaPacific {
		t.Errorf("Region = %q, want ap", nr.Region)
	}
	if nr.SubRegion != "ap-cn" {
		t.Errorf("SubRegion = %q, want ap-cn", nr.SubRegion)
	}
	if nr.Latitude != 39.9 {
		t.Errorf("Latitude = %f, want 39.9", nr.Latitude)
	}
}

func TestRegionManager_RegisterNodeEmptyIP(t *testing.T) {
	rm := newTestRegionManager()
	rm.RegisterNode("mmx-node1", "", "ip_detect")

	nr := rm.GetNodeRegion("mmx-node1")
	// Empty IP results in RegionUnknown but node is still registered
	if nr == nil {
		t.Error("node should still be registered even with empty IP")
	}
	if nr != nil && nr.Region != RegionUnknown {
		t.Errorf("empty IP should result in RegionUnknown, got %q", nr.Region)
	}
}

func TestRegionManager_RegisterNodePrivateIP(t *testing.T) {
	rm := newTestRegionManager()
	rm.RegisterNode("mmx-node1", "192.168.1.1", "ip_detect")

	nr := rm.GetNodeRegion("mmx-node1")
	// Private IP results in RegionUnknown but node is still registered
	if nr == nil {
		t.Error("node should still be registered even with private IP")
	}
	if nr != nil && nr.Region != RegionUnknown {
		t.Errorf("private IP should result in RegionUnknown, got %q", nr.Region)
	}
}

func TestRegionManager_GetNodeRegionUnknown(t *testing.T) {
	rm := newTestRegionManager()
	nr := rm.GetNodeRegion("mmx-nonexistent")
	if nr != nil {
		t.Error("should return nil for unknown node")
	}
}

func TestRegionManager_GetAllRegions(t *testing.T) {
	rm := newTestRegionManager()
	rm.RegisterNode("mmx-node1", "8.8.8.8", "ip_detect")
	rm.RegisterNode("mmx-node2", "114.100.1.1", "ip_detect")

	all := rm.GetAllRegions()
	if len(all) != 2 {
		t.Errorf("expected 2 regions, got %d", len(all))
	}
}

func TestRegionManager_GetRegionSummary(t *testing.T) {
	rm := newTestRegionManager()
	rm.RegisterNode("mmx-node1", "8.8.8.8", "ip_detect")       // Americas
	rm.RegisterNode("mmx-node2", "114.100.1.1", "ip_detect")    // AP
	rm.RegisterNode("mmx-node3", "35.100.1.1", "ip_detect")     // Americas

	summary := rm.GetRegionSummary()
	if summary[RegionAmericas] != 2 {
		t.Errorf("Americas count = %d, want 2", summary[RegionAmericas])
	}
	if summary[RegionAsiaPacific] != 1 {
		t.Errorf("AP count = %d, want 1", summary[RegionAsiaPacific])
	}
}

// ============================================================
// Region Config Tests
// ============================================================

func TestDefaultRegionConfig(t *testing.T) {
	cfg := DefaultRegionConfig()
	if !cfg.PreferLocal {
		t.Error("PreferLocal should default to true")
	}
	if cfg.CrossRegionThreshold != 2.0 {
		t.Errorf("CrossRegionThreshold = %f, want 2.0", cfg.CrossRegionThreshold)
	}
	if cfg.RegionWeights[RegionUnknown] != 0.5 {
		t.Errorf("Unknown region weight = %f, want 0.5", cfg.RegionWeights[RegionUnknown])
	}
}

func TestRegionManager_GetSetConfig(t *testing.T) {
	rm := newTestRegionManager()

	newCfg := RegionConfig{
		PreferLocal:          false,
		CrossRegionThreshold: 3.0,
		RegionWeights: map[Region]float64{
			RegionAsiaPacific: 2.0,
		},
	}
	rm.UpdateConfig(newCfg)

	got := rm.GetConfig()
	if got.PreferLocal {
		t.Error("PreferLocal should be false after update")
	}
	if got.CrossRegionThreshold != 3.0 {
		t.Errorf("CrossRegionThreshold = %f, want 3.0", got.CrossRegionThreshold)
	}
}

// ============================================================
// Region Selection Tests
// ============================================================

func TestSelectNodeForRegion(t *testing.T) {
	rm := newTestRegionManager()
	rm.RegisterNodeSelfReport("mmx-ap1", "ap", "ap-cn", 39.9, 116.4)
	rm.RegisterNodeSelfReport("mmx-ap2", "ap", "ap-jp", 35.6, 139.7)
	rm.RegisterNodeSelfReport("mmx-us1", "americas", "us-west", 37.7, -122.4)

	candidates := []string{"mmx-ap1", "mmx-ap2", "mmx-us1"}

	// Same region should come first
	result := rm.SelectNodeForRegion(candidates, RegionAsiaPacific)
	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result))
	}
	if result[0] != "mmx-ap1" && result[0] != "mmx-ap2" {
		t.Errorf("first result should be AP node, got %q", result[0])
	}
}

func TestSelectNodeForRegionEmpty(t *testing.T) {
	rm := newTestRegionManager()
	result := rm.SelectNodeForRegion([]string{}, RegionAsiaPacific)
	if len(result) != 0 {
		t.Error("empty candidates should return empty")
	}
}

func TestSelectNodeForRegionNoPreference(t *testing.T) {
	rm := newTestRegionManager()
	rm.config.PreferLocal = false

	rm.RegisterNodeSelfReport("mmx-us1", "americas", "us-west", 37.7, -122.4)
	rm.RegisterNodeSelfReport("mmx-ap1", "ap", "ap-cn", 39.9, 116.4)

	candidates := []string{"mmx-us1", "mmx-ap1"}
	result := rm.SelectNodeForRegion(candidates, RegionAsiaPacific)

	// Without preference, order may vary but should still return all
	if len(result) != 2 {
		t.Errorf("expected 2 results, got %d", len(result))
	}
}

// ============================================================
// Optimal Route Tests
// ============================================================

func TestGetOptimalRoute(t *testing.T) {
	rm := newTestRegionManager()
	rm.RegisterNodeSelfReport("mmx-ap1", "ap", "ap-cn", 39.9, 116.4)
	rm.RegisterNodeSelfReport("mmx-us1", "americas", "us-west", 37.7, -122.4)

	candidates := []string{"mmx-us1", "mmx-ap1"}
	lbScores := map[string]float64{
		"mmx-us1": 0.5,
		"mmx-ap1": 0.5,
	}

	result := rm.GetOptimalRoute(candidates, RegionAsiaPacific, lbScores)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	// AP node should rank higher due to same-region bonus
	if result[0] != "mmx-ap1" {
		t.Errorf("AP node should be first, got %q", result[0])
	}
}

func TestGetOptimalRouteEmpty(t *testing.T) {
	rm := newTestRegionManager()
	result := rm.GetOptimalRoute([]string{}, RegionAsiaPacific, nil)
	if len(result) != 0 {
		t.Error("empty candidates should return empty")
	}
}

// ============================================================
// Haversine Distance Tests
// ============================================================

func TestHaversineDistance(t *testing.T) {
	tests := []struct {
		name     string
		lat1, lon1, lat2, lon2 float64
		expectedMin, expectedMax float64
	}{
		{"same point", 0, 0, 0, 0, 0, 0.01},
		{"Beijing to Tokyo", 39.9, 116.4, 35.6, 139.7, 2000, 2200},
		{"Beijing to NYC", 39.9, 116.4, 40.7, -74.0, 10000, 11000},
		{"London to Paris", 51.5, -0.1, 48.8, 2.3, 300, 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dist := haversineDistance(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			if dist < tt.expectedMin || dist > tt.expectedMax {
				t.Errorf("haversineDistance = %f, expected between %f and %f", dist, tt.expectedMin, tt.expectedMax)
			}
		})
	}
}

func TestRegionDistance(t *testing.T) {
	tests := []struct {
		a, b     Region
		expected float64
	}{
		{RegionAsiaPacific, RegionAsiaPacific, 0},
		{RegionAsiaPacific, RegionAmericas, 12000},
		{RegionAsiaPacific, RegionEurope, 8000},
		{RegionAmericas, RegionEurope, 7000},
		{RegionUnknown, RegionAsiaPacific, 10000},
	}

	for _, tt := range tests {
		t.Run(string(tt.a)+"-"+string(tt.b), func(t *testing.T) {
			dist := regionDistance(tt.a, tt.b)
			if math.Abs(dist-tt.expected) > 0.01 {
				t.Errorf("regionDistance(%q, %q) = %f, want %f", tt.a, tt.b, dist, tt.expected)
			}
		})
	}
}

func TestRegionCenter(t *testing.T) {
	tests := []struct {
		region Region
		latMin, latMax float64
		lonMin, lonMax float64
	}{
		{RegionAsiaPacific, 30, 40, 100, 120},
		{RegionAmericas, 35, 45, -110, -90},
		{RegionEurope, 45, 55, 5, 15},
	}

	for _, tt := range tests {
		t.Run(string(tt.region), func(t *testing.T) {
			lat, lon := regionCenter(tt.region)
			if lat < tt.latMin || lat > tt.latMax {
				t.Errorf("lat %f not in range [%f, %f]", lat, tt.latMin, tt.latMax)
			}
			if lon < tt.lonMin || lon > tt.lonMax {
				t.Errorf("lon %f not in range [%f, %f]", lon, tt.lonMin, tt.lonMax)
			}
		})
	}
}

// ============================================================
// AllRegions Tests
// ============================================================

func TestAllRegions(t *testing.T) {
	regions := AllRegions()
	if len(regions) != 4 {
		t.Errorf("expected 4 regions, got %d", len(regions))
	}

	regionSet := make(map[Region]bool)
	for _, r := range regions {
		regionSet[r] = true
	}
	for _, expected := range []Region{RegionAsiaPacific, RegionAmericas, RegionEurope, RegionUnknown} {
		if !regionSet[expected] {
			t.Errorf("missing region %q", expected)
		}
	}
}

// ============================================================
// ProcessHeartbeatRegion Tests
// ============================================================

func TestProcessHeartbeatRegion_SelfReport(t *testing.T) {
	rm := newTestRegionManager()

	info := &HeartbeatRegionInfo{
		Region:    "ap",
		SubRegion: "ap-cn",
		Latitude:  39.9,
		Longitude: 116.4,
	}
	rm.ProcessHeartbeatRegion("mmx-node1", info, "")

	nr := rm.GetNodeRegion("mmx-node1")
	if nr == nil {
		t.Fatal("node should be registered")
	}
	if nr.Region != RegionAsiaPacific {
		t.Errorf("Region = %q, want ap", nr.Region)
	}
}

func TestProcessHeartbeatRegion_IPFallback(t *testing.T) {
	rm := newTestRegionManager()

	rm.ProcessHeartbeatRegion("mmx-node1", nil, "8.8.8.8")

	nr := rm.GetNodeRegion("mmx-node1")
	if nr == nil {
		t.Fatal("node should be registered via IP fallback")
	}
	if nr.Region != RegionAmericas {
		t.Errorf("Region = %q, want americas", nr.Region)
	}
}

func TestProcessHeartbeatRegion_EmptyInfoWithEmptyIP(t *testing.T) {
	rm := newTestRegionManager()
	rm.ProcessHeartbeatRegion("mmx-node1", nil, "")

	nr := rm.GetNodeRegion("mmx-node1")
	if nr != nil {
		t.Error("should not register with empty info and empty IP")
	}
}

// ============================================================
// extractRemoteIP Tests
// ============================================================

func TestExtractRemoteIP(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		xri        string
		remoteAddr string
		expected   string
	}{
		{"X-Forwarded-For", "1.2.3.4, 5.6.7.8", "", "10.0.0.1:8080", "1.2.3.4"},
		{"X-Real-IP", "", "9.8.7.6", "10.0.0.1:8080", "9.8.7.6"},
		{"RemoteAddr fallback", "", "", "10.0.0.1:8080", "10.0.0.1"},
		{"RemoteAddr no port", "", "", "10.0.0.1", "10.0.0.1"},
		{"XFF takes priority", "1.2.3.4", "9.8.7.6", "10.0.0.1:8080", "1.2.3.4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				req.Header.Set("X-Real-IP", tt.xri)
			}
			result := extractRemoteIP(req)
			if result != tt.expected {
				t.Errorf("extractRemoteIP = %q, want %q", result, tt.expected)
			}
		})
	}
}

// ============================================================
// Region JSON Marshal/Unmarshal Tests
// ============================================================

func TestRegionMarshalJSON(t *testing.T) {
	r := RegionAsiaPacific
	data, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(data) != `"ap"` {
		t.Errorf("marshaled = %s, want %q", string(data), "ap")
	}
}

func TestRegionUnmarshalJSON(t *testing.T) {
	var r Region
	if err := r.UnmarshalJSON([]byte(`"americas"`)); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if r != RegionAmericas {
		t.Errorf("unmarshaled = %q, want americas", r)
	}
}

// ============================================================
// RegionConstants Tests
// ============================================================

func TestRegionValues(t *testing.T) {
	if RegionAsiaPacific != "ap" {
		t.Errorf("RegionAsiaPacific = %q, want ap", RegionAsiaPacific)
	}
	if RegionAmericas != "americas" {
		t.Errorf("RegionAmericas = %q, want americas", RegionAmericas)
	}
	if RegionEurope != "eu" {
		t.Errorf("RegionEurope = %q, want eu", RegionEurope)
	}
	if RegionUnknown != "unknown" {
		t.Errorf("RegionUnknown = %q, want unknown", RegionUnknown)
	}
}
