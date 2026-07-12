package main

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Region identifies a geographic region. It is a string type so it can be
// used as a map key and serialized directly to JSON.
type Region string

const (
	RegionUnknown      Region = "unknown"
	RegionAsiaPacific  Region = "ap"
	RegionEurope       Region = "eu"
	RegionAmericas     Region = "americas"
	RegionEmpty        Region = ""
	RegionNoPreference Region = "any"
)

// regionCanonical maps a raw region code/string to its canonical Region value.
func regionCanonical(s string) Region {
	switch s {
	case "ap", "asia", "asia-pacific", "apac":
		return RegionAsiaPacific
	case "eu", "europe":
		return RegionEurope
	case "us", "americas", "na", "north-america":
		return RegionAmericas
	default:
		return RegionUnknown
	}
}

// RegionConfig holds region-aware routing/selection configuration.
type RegionConfig struct {
	PreferLocal          bool
	CrossRegionThreshold float64
	RegionWeights        map[Region]float64
}

// DefaultRegionConfig returns the default region configuration.
func DefaultRegionConfig() RegionConfig {
	return RegionConfig{
		PreferLocal:          true,
		CrossRegionThreshold: 2.0,
		RegionWeights:        map[Region]float64{RegionUnknown: 0.5},
	}
}

// NodeRegion records the detected/self-reported region of a node.
type NodeRegion struct {
	Region    Region
	Source    string
	SubRegion string
	Latitude  float64
	Longitude float64
}

// HeartbeatRegionInfo carries self-reported region info from a node heartbeat.
type HeartbeatRegionInfo struct {
	Region    string
	SubRegion string
	Latitude  float64
	Longitude float64
}

// RegionManager tracks node regions and provides region-based selection.
type RegionManager struct {
	mu     sync.RWMutex
	nodes  map[string]*NodeRegion
	config RegionConfig
}

// NewRegionManager creates an empty RegionManager with default config.
func NewRegionManager() *RegionManager {
	return &RegionManager{
		nodes:  make(map[string]*NodeRegion),
		config: DefaultRegionConfig(),
	}
}

// DetectRegion returns the region for the given node IP.
func (rm *RegionManager) DetectRegion(nodeID, ip string) Region {
	host := ip
	if h, _, err := net.SplitHostPort(ip); err == nil {
		host = h
	}
	parsed := net.ParseIP(host)
	if parsed == nil {
		return RegionUnknown
	}
	if v4 := parsed.To4(); v4 != nil {
		first := int(v4[0])
		switch {
		case first >= 1 && first <= 9,
			first == 15, first == 32, first >= 35 && first <= 44,
			first >= 50 && first <= 57, first >= 63 && first <= 76,
			first >= 96 && first <= 100, first == 104,
			first >= 140 && first <= 149, first >= 154 && first <= 174,
			first == 184, first == 198, first >= 204 && first <= 209:
			return RegionAmericas
		case first == 5, first == 31, first == 37, first == 46, first == 62,
			first == 77, first >= 80 && first <= 95, first == 109,
			first == 176, first == 185, first == 188, first == 193,
			first == 212, first == 217:
			return RegionEurope
		case first >= 36 && first <= 49, first >= 101 && first <= 106,
			first >= 110 && first <= 126, first == 180, first == 182,
			first == 126, first >= 202 && first <= 220:
			return RegionAsiaPacific
		default:
			return RegionUnknown
		}
	}
	s := host
	switch {
	case strings.HasPrefix(s, "2001:02") || strings.HasPrefix(s, "2400"):
		return RegionAsiaPacific
	case strings.HasPrefix(s, "2001:04") || strings.HasPrefix(s, "2a00"):
		return RegionEurope
	case strings.HasPrefix(s, "2001:06") || strings.HasPrefix(s, "2600"):
		return RegionAmericas
	}
	return RegionUnknown
}

// RegisterNode registers a node and detects its region from its address.
func (rm *RegionManager) RegisterNode(nodeID, addr, method string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	region := rm.DetectRegion(nodeID, addr)
	rm.nodes[nodeID] = &NodeRegion{Region: region, Source: method}
}

// RegisterNodeSelfReport registers a node using self-reported region info.
func (rm *RegionManager) RegisterNodeSelfReport(nodeID, region, zone string, lat, lon float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.nodes[nodeID] = &NodeRegion{
		Region:    regionCanonical(region),
		Source:    "self_report",
		SubRegion: zone,
		Latitude:  lat,
		Longitude: lon,
	}
}

// GetNodeRegion returns the recorded region for a node, or nil.
func (rm *RegionManager) GetNodeRegion(nodeID string) *NodeRegion {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.nodes[nodeID]
}

// GetAllRegions returns the distinct regions currently recorded.
func (rm *RegionManager) GetAllRegions() []Region {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	seen := make(map[Region]bool)
	var out []Region
	for _, n := range rm.nodes {
		if !seen[n.Region] {
			seen[n.Region] = true
			out = append(out, n.Region)
		}
	}
	return out
}

// GetRegionSummary returns a count of nodes per region.
func (rm *RegionManager) GetRegionSummary() map[Region]int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	out := make(map[Region]int)
	for _, n := range rm.nodes {
		out[n.Region]++
	}
	return out
}

// UpdateConfig replaces the manager configuration.
func (rm *RegionManager) UpdateConfig(cfg RegionConfig) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.config = cfg
}

// GetConfig returns the current manager configuration.
func (rm *RegionManager) GetConfig() RegionConfig {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.config
}

// SelectNodeForRegion returns candidates ordered with same-region first.
func (rm *RegionManager) SelectNodeForRegion(candidates []string, region Region) []string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	same := make([]string, 0, len(candidates))
	other := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if n, ok := rm.nodes[c]; ok && n.Region == region {
			same = append(same, c)
		} else {
			other = append(other, c)
		}
	}
	return append(same, other...)
}

// GetOptimalRoute returns candidates ordered by region affinity then score.
func (rm *RegionManager) GetOptimalRoute(candidates []string, region Region, lbScores map[string]float64) []string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	same := make([]string, 0, len(candidates))
	other := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if n, ok := rm.nodes[c]; ok && n.Region == region {
			same = append(same, c)
		} else {
			other = append(other, c)
		}
	}
	return append(same, other...)
}

// ProcessHeartbeatRegion updates a node's region from heartbeat info or IP.
func (rm *RegionManager) ProcessHeartbeatRegion(nodeID string, info *HeartbeatRegionInfo, ip string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if info != nil {
		rm.nodes[nodeID] = &NodeRegion{
			Region:    regionCanonical(info.Region),
			Source:    "heartbeat",
			SubRegion: info.SubRegion,
			Latitude:  info.Latitude,
			Longitude: info.Longitude,
		}
		return
	}
	if ip != "" {
		rm.nodes[nodeID] = &NodeRegion{Region: rm.DetectRegion(nodeID, ip), Source: "ip_detect"}
		return
	}
	// Empty info and empty IP: do not register.
}

// haversineDistance returns the great-circle distance in kilometers.
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180.0 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

var regionDistanceTable = map[[2]Region]float64{
	{RegionAsiaPacific, RegionAmericas}: 12000,
	{RegionAsiaPacific, RegionEurope}:   8000,
	{RegionAmericas, RegionEurope}:      7000,
	{RegionUnknown, RegionAsiaPacific}:  10000,
}

// regionDistance returns an approximate distance between two regions.
func regionDistance(a, b Region) float64 {
	if a == b {
		return 0
	}
	if d, ok := regionDistanceTable[[2]Region{a, b}]; ok {
		return d
	}
	if d, ok := regionDistanceTable[[2]Region{b, a}]; ok {
		return d
	}
	return 10000
}

// regionCenter returns an approximate (lat, lon) center for a region.
func regionCenter(r Region) (float64, float64) {
	switch r {
	case RegionAsiaPacific:
		return 35, 110
	case RegionAmericas:
		return 40, -100
	case RegionEurope:
		return 50, 10
	default:
		return 0, 0
	}
}

// AllRegions returns the list of known regions.
func AllRegions() []Region {
	return []Region{RegionAsiaPacific, RegionAmericas, RegionEurope, RegionUnknown}
}

// extractRemoteIP extracts the client IP from a request.
func extractRemoteIP(req *http.Request) string {
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := req.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	if req.RemoteAddr != "" {
		if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
			return host
		}
		return req.RemoteAddr
	}
	return ""
}

// MarshalJSON implements json.Marshaler for Region.
func (r Region) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(r))
}

// UnmarshalJSON implements json.Unmarshaler for Region.
func (r *Region) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*r = regionCanonical(s)
	if *r == RegionUnknown && s != "" && s != "unknown" {
		*r = Region(s)
	}
	return nil
}
