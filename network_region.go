package main

import (
	"encoding/json"
	"log/slog"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// ============================================================
// Phase 4: Cross-Region Routing & Region Awareness
// ============================================================
//
// RegionManager detects node regions based on IP addresses or
// self-reported information in heartbeats. It provides routing
// preferences that favor same-region nodes while still allowing
// cross-region fallback when needed.

// Region defines a network region.
type Region string

const (
	RegionAsiaPacific Region = "ap"       // 亚太
	RegionAmericas    Region = "americas" // 美洲
	RegionEurope      Region = "eu"       // 欧洲
	RegionUnknown     Region = "unknown"
)

// AllRegions returns all defined regions.
func AllRegions() []Region {
	return []Region{RegionAsiaPacific, RegionAmericas, RegionEurope, RegionUnknown}
}

// RegionConfig holds region routing configuration.
type RegionConfig struct {
	// PreferLocal enables same-region routing preference (default true).
	PreferLocal bool `json:"prefer_local"`
	// CrossRegionThreshold is the latency tolerance multiplier for cross-region routing (default 2.0).
	CrossRegionThreshold float64 `json:"cross_region_threshold"`
	// RegionWeights maps each region to a weight for load balancing.
	RegionWeights map[Region]float64 `json:"region_weights"`
}

// DefaultRegionConfig returns sensible defaults.
func DefaultRegionConfig() RegionConfig {
	return RegionConfig{
		PreferLocal:          true,
		CrossRegionThreshold: 2.0,
		RegionWeights: map[Region]float64{
			RegionAsiaPacific: 1.0,
			RegionAmericas:    1.0,
			RegionEurope:      1.0,
			RegionUnknown:     0.5,
		},
	}
}

// NodeRegion stores region information for a single node.
type NodeRegion struct {
	NodeID     string    `json:"node_id"`
	Region     Region    `json:"region"`
	SubRegion  string    `json:"sub_region"`            // e.g. ap-cn, ap-jp, us-west, eu-west
	Latitude   float64   `json:"latitude,omitempty"`    // optional, for precise distance
	Longitude  float64   `json:"longitude,omitempty"`
	DetectedAt time.Time `json:"detected_at"`
	Source     string    `json:"source"`                // "ip_detect" or "self_report"
}

// RegionManager manages region detection and routing preferences.
type RegionManager struct {
	mu     sync.RWMutex
	nodes  map[string]*NodeRegion // nodeID -> region info
	config RegionConfig
}

var regionMgr *RegionManager

// initRegionManager creates the region manager.
func initRegionManager() {
	regionMgr = &RegionManager{
		nodes:  make(map[string]*NodeRegion),
		config: DefaultRegionConfig(),
	}
	slog.Info("region manager initialized")
}

// ============================================================
// Region Detection
// ============================================================

// DetectRegion detects a node's region based on its IP address.
// Uses simple IP prefix heuristics for common ranges.
func (rm *RegionManager) DetectRegion(nodeID, ip string) Region {
	if ip == "" {
		return RegionUnknown
	}

	// Extract host from IP:port
	host := ip
	if h, _, err := net.SplitHostPort(ip); err == nil {
		host = h
	}

	parsedIP := net.ParseIP(host)
	if parsedIP == nil {
		return RegionUnknown
	}

	// Private/reserved ranges → unknown
	if parsedIP.IsPrivate() || parsedIP.IsLoopback() || parsedIP.IsLinkLocalUnicast() || parsedIP.IsLinkLocalMulticast() {
		return RegionUnknown
	}

	ip4 := parsedIP.To4()
	if ip4 == nil {
		// IPv6 — use rough heuristics
		return rm.detectRegionIPv6(parsedIP)
	}

	// IPv4 region detection based on common allocation blocks
	first := int(ip4[0])
	second := int(ip4[1])

	// Asia-Pacific ranges (simplified)
	// 1.x.x.x (APNIC), 14.x, 27.x, 36.x, 39.x, 42.x, 43.x, 49.x, 58.x, 59.x,
	// 60.x, 61.x, 101.x, 103.x, 106.x, 110.x, 111.x, 112.x, 113.x, 114.x,
	// 115.x, 116.x, 117.x, 118.x, 119.x, 120.x, 121.x, 122.x, 123.x,
	// 124.x, 125.x, 126.x (JP), 133.x, 150.x, 153.x, 163.x, 175.x,
	// 180.x, 182.x, 183.x, 202.x, 203.x, 210.x, 211.x, 218.x, 219.x, 220.x, 221.x, 222.x, 223.x
	apRanges := []int{1, 14, 27, 36, 39, 42, 43, 49, 58, 59, 60, 61,
		101, 103, 106, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119,
		120, 121, 122, 123, 124, 125, 126, 133, 150, 153, 163, 175,
		180, 182, 183, 202, 203, 210, 211, 218, 219, 220, 221, 222, 223}
	for _, r := range apRanges {
		if first == r {
			return RegionAsiaPacific
		}
	}

	// Some 5.x, 31.x, 37.x, 46.x, 62.x, 77.x, 78.x, 79.x, 80-95.x,
	// 109.x, 151.x, 176.x, 178.x, 185.x, 188.x, 193.x, 194.x, 195.x, 212.x, 213.x, 217.x → Europe
	euRanges := []int{5, 31, 37, 46, 62, 77, 78, 79}
	for _, r := range euRanges {
		if first == r {
			return RegionEurope
		}
	}
	if first >= 80 && first <= 95 {
		return RegionEurope
	}
	euRanges2 := []int{109, 151, 176, 178, 185, 188, 193, 194, 195, 212, 213, 217}
	for _, r := range euRanges2 {
		if first == r {
			return RegionEurope
		}
	}

	// Americas ranges (simplified)
	// 3.x, 4.x, 6.x-13.x, 15.x-26.x, 28.x-35.x, 40.x, 44.x, 45.x, 47.x, 48.x,
	// 50.x-57.x, 63.x-69.x, 70.x-76.x, 96.x-100.x, 104.x-105.x, 107.x-108.x,
	// 140.x-149.x, 152.x, 154.x-174.x, 184.x, 192.x (partial), 198.x-201.x,
	// 204.x-209.x, 214.x-216.x
	americasRanges := []int{3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 15, 16, 17, 18, 19,
		20, 21, 22, 23, 24, 25, 26, 28, 29, 30, 31, 32, 33, 34, 35, 40, 44, 45, 47, 48}
	for _, r := range americasRanges {
		if first == r {
			return RegionAmericas
		}
	}
	if first >= 50 && first <= 57 {
		return RegionAmericas
	}
	if first >= 63 && first <= 76 {
		return RegionAmericas
	}
	if first >= 96 && first <= 100 {
		return RegionAmericas
	}
	americasRanges2 := []int{104, 105, 107, 108, 140, 141, 142, 143, 144, 145, 146, 147, 148, 149,
		152, 154, 155, 156, 157, 158, 159, 160, 161, 162, 164, 165, 166, 167, 168, 169, 170, 171,
		172, 173, 174, 184, 198, 199, 200, 201, 204, 205, 206, 207, 208, 209, 214, 215, 216}
	for _, r := range americasRanges2 {
		if first == r {
			return RegionAmericas
		}
	}

	// 172.16-31 is private, already handled. But 172.x (non-private) may be americas
	if first == 172 && second < 16 || second > 31 {
		return RegionAmericas
	}

	// 192.x (partial): 192.0-192.167, 192.169-192.255 → mostly Americas
	if first == 192 && (second < 168 || second > 168) {
		return RegionAmericas
	}

	_ = second
	return RegionUnknown
}

// detectRegionIPv6 uses rough IPv6 prefix heuristics.
func (rm *RegionManager) detectRegionIPv6(ip net.IP) Region {
	// Very rough: check the first few bytes
	if len(ip) < 4 {
		return RegionUnknown
	}
	first := int(ip[0])
	// 2001:0200-03ff → AP (Japan, Korea, etc.)
	if first == 0x20 {
		second := int(ip[1])
		if second >= 0x02 && second <= 0x03 {
			return RegionAsiaPacific
		}
		// 2001:0400-05ff → EU
		if second >= 0x04 && second <= 0x05 {
			return RegionEurope
		}
		// 2001:0600-09ff → mixed, but mostly Americas
		if second >= 0x06 && second <= 0x09 {
			return RegionAmericas
		}
		// 2400-24ff → AP
		if first == 0x24 {
			return RegionAsiaPacific
		}
		// 2600-26ff → Americas
		if first == 0x26 {
			return RegionAmericas
		}
		// 2a00-2aff → Europe
		if first == 0x2a {
			return RegionEurope
		}
	}
	return RegionUnknown
}

// detectSubRegion attempts to determine a more specific sub-region.
func detectSubRegion(region Region, ip string) string {
	if ip == "" || region == RegionUnknown {
		return ""
	}

	host := ip
	if h, _, err := net.SplitHostPort(ip); err == nil {
		host = h
	}
	parsedIP := net.ParseIP(host)
	if parsedIP == nil {
		return ""
	}
	ip4 := parsedIP.To4()
	if ip4 == nil {
		return string(region)
	}

	first := int(ip4[0])
	second := int(ip4[1])
	_ = second // reserved for subnet-level region detection

	switch region {
	case RegionAsiaPacific:
		// China: 36.x, 39.x, 42.x, 43.x, 49.x, 58.x, 59.x, 60.x, 61.x, 101.x, 103.x, 106.x, 110-126.x, etc.
		if first == 36 || first == 39 || first == 42 || first == 43 || first == 49 {
			return "ap-cn"
		}
		if first >= 58 && first <= 61 {
			return "ap-cn"
		}
		if first >= 101 && first <= 126 {
			return "ap-cn"
		}
		if first == 126 || first == 133 || first == 150 || first == 153 || first == 163 {
			return "ap-jp"
		}
		if first == 203 || first == 210 || first == 211 || first == 218 || first == 219 || first == 220 || first == 221 || first == 222 {
			return "ap-cn"
		}
		return "ap-other"
	case RegionAmericas:
		if first >= 3 && first <= 4 {
			return "us-east"
		}
		if first >= 8 && first <= 13 {
			return "us-west"
		}
		if first >= 15 && first <= 26 {
			return "us-east"
		}
		if first >= 63 && first <= 76 {
			return "us-central"
		}
		if first >= 184 && first <= 201 {
			return "us-west"
		}
		return "americas-other"
	case RegionEurope:
		if first == 5 || first == 31 || first == 37 || first == 46 {
			return "eu-west"
		}
		if first >= 77 && first <= 95 {
			return "eu-central"
		}
		if first == 185 || first == 188 {
			return "eu-east"
		}
		return "eu-other"
	}
	return ""
}

// ============================================================
// Registration & Lookup
// ============================================================

// RegisterNode records a node's region information.
func (rm *RegionManager) RegisterNode(nodeID, ip, source string) {
	region := rm.DetectRegion(nodeID, ip)
	subRegion := detectSubRegion(region, ip)

	rm.mu.Lock()
	defer rm.mu.Unlock()

	existing, ok := rm.nodes[nodeID]
	if ok && existing.Source == "self_report" && source != "self_report" {
		// Don't overwrite self-reported info with IP-detected info
		return
	}

	rm.nodes[nodeID] = &NodeRegion{
		NodeID:     nodeID,
		Region:     region,
		SubRegion:  subRegion,
		DetectedAt: time.Now(),
		Source:     source,
	}
}

// RegisterNodeSelfReport records a self-reported region from a heartbeat.
func (rm *RegionManager) RegisterNodeSelfReport(nodeID, region, subRegion string, lat, lon float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.nodes[nodeID] = &NodeRegion{
		NodeID:     nodeID,
		Region:     Region(region),
		SubRegion:  subRegion,
		Latitude:   lat,
		Longitude:  lon,
		DetectedAt: time.Now(),
		Source:     "self_report",
	}
}

// GetNodeRegion returns the region info for a node.
func (rm *RegionManager) GetNodeRegion(nodeID string) *NodeRegion {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	nr, ok := rm.nodes[nodeID]
	if !ok {
		return nil
	}
	cp := *nr
	return &cp
}

// GetAllRegions returns all registered region information.
func (rm *RegionManager) GetAllRegions() map[string]*NodeRegion {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	result := make(map[string]*NodeRegion, len(rm.nodes))
	for k, v := range rm.nodes {
		cp := *v
		result[k] = &cp
	}
	return result
}

// GetRegionSummary returns a count of nodes per region.
func (rm *RegionManager) GetRegionSummary() map[Region]int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	summary := make(map[Region]int)
	for _, nr := range rm.nodes {
		summary[nr.Region]++
	}
	return summary
}

// GetConfig returns the current region configuration.
func (rm *RegionManager) GetConfig() RegionConfig {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	cp := rm.config
	if cp.RegionWeights != nil {
		weights := make(map[Region]float64, len(cp.RegionWeights))
		for k, v := range cp.RegionWeights {
			weights[k] = v
		}
		cp.RegionWeights = weights
	}
	return cp
}

// UpdateConfig updates the region configuration.
func (rm *RegionManager) UpdateConfig(cfg RegionConfig) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.config = cfg
}

// ============================================================
// Routing Strategies
// ============================================================

// SelectNodeForRegion selects nodes with region preference.
// Returns candidates sorted by region affinity.
func (rm *RegionManager) SelectNodeForRegion(candidates []string, preferredRegion Region) []string {
	if len(candidates) == 0 {
		return candidates
	}

	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var sameRegion, crossRegion []string
	for _, nodeID := range candidates {
		nr, ok := rm.nodes[nodeID]
		if ok && nr.Region == preferredRegion {
			sameRegion = append(sameRegion, nodeID)
		} else {
			crossRegion = append(crossRegion, nodeID)
		}
	}

	// If same region nodes available, return them first
	if rm.config.PreferLocal && len(sameRegion) > 0 {
		return append(sameRegion, crossRegion...)
	}

	// Sort cross-region by distance (if coordinates available)
	if len(crossRegion) > 1 && preferredRegion != RegionUnknown {
		sort.Slice(crossRegion, func(i, j int) bool {
			return rm.nodeDistance(nodeID(crossRegion[i]), preferredRegion) <
				rm.nodeDistance(nodeID(crossRegion[j]), preferredRegion)
		})
	}

	return crossRegion
}

type nodeID = string

// nodeDistance estimates distance from a region to a node.
func (rm *RegionManager) nodeDistance(nid nodeID, sourceRegion Region) float64 {
	nr, ok := rm.nodes[nodeID(nid)]
	if !ok {
		return 1000 // unknown nodes get worst score
	}

	// Same region = distance 0
	if nr.Region == sourceRegion {
		return 0
	}

	// If coordinates available, use haversine distance
	if nr.Latitude != 0 && nr.Longitude != 0 {
		centerLat, centerLon := regionCenter(sourceRegion)
		return haversineDistance(centerLat, centerLon, nr.Latitude, nr.Longitude)
	}

	// Otherwise use region-pair rough distance
	return regionDistance(sourceRegion, nr.Region)
}

// regionCenter returns approximate lat/lon center of a region.
func regionCenter(r Region) (float64, float64) {
	switch r {
	case RegionAsiaPacific:
		return 35.0, 110.0 // roughly center of China
	case RegionAmericas:
		return 40.0, -100.0 // roughly center of US
	case RegionEurope:
		return 50.0, 10.0 // roughly center of Europe
	default:
		return 0, 0
	}
}

// haversineDistance calculates the distance in km between two coordinates.
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371 // Earth radius in km
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// regionDistance returns a rough inter-region distance in km.
func regionDistance(a, b Region) float64 {
	if a == b {
		return 0
	}
	distances := map[[2]Region]float64{
		{RegionAsiaPacific, RegionAmericas}: 12000,
		{RegionAsiaPacific, RegionEurope}:    8000,
		{RegionAmericas, RegionEurope}:       7000,
	}
	if d, ok := distances[[2]Region{a, b}]; ok {
		return d
	}
	if d, ok := distances[[2]Region{b, a}]; ok {
		return d
	}
	return 10000 // default large distance
}

// GetOptimalRoute computes optimal routing order considering both
// region affinity and load-balance scores.
func (rm *RegionManager) GetOptimalRoute(candidates []string, sourceRegion Region, lbScores map[string]float64) []string {
	if len(candidates) == 0 {
		return candidates
	}

	rm.mu.RLock()
	defer rm.mu.RUnlock()

	type scoredNode struct {
		nodeID string
		score  float64
	}

	scored := make([]scoredNode, 0, len(candidates))
	for _, nodeID := range candidates {
		// Start with load balance score (0-1 range)
		score := 0.0
		if lbScores != nil {
			score = lbScores[nodeID]
		}

		// Apply region adjustments
		nr, ok := rm.nodes[nodeID]
		if ok {
			if nr.Region == sourceRegion {
				// Same region bonus
				score += 0.15
			} else if nr.Region != RegionUnknown && sourceRegion != RegionUnknown {
				// Cross-region penalty
				score -= 0.10
			}

			// Apply region weight
			if rm.config.RegionWeights != nil {
				if w, exists := rm.config.RegionWeights[nr.Region]; exists {
					score *= w
				}
			}
		}

		scored = append(scored, scoredNode{nodeID: nodeID, score: score})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	result := make([]string, len(scored))
	for i, s := range scored {
		result[i] = s.nodeID
	}
	return result
}

// ============================================================
// Heartbeat Extension
// ============================================================

// HeartbeatRegionInfo is the optional region info carried in heartbeat messages.
type HeartbeatRegionInfo struct {
	Region    string  `json:"region,omitempty"`
	SubRegion string  `json:"sub_region,omitempty"`
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
}

// ProcessHeartbeatRegion handles region information from a heartbeat.
func (rm *RegionManager) ProcessHeartbeatRegion(nodeID string, regionInfo *HeartbeatRegionInfo, remoteAddr string) {
	if regionInfo != nil && regionInfo.Region != "" {
		// Use self-reported region
		rm.RegisterNodeSelfReport(nodeID, regionInfo.Region, regionInfo.SubRegion, regionInfo.Latitude, regionInfo.Longitude)
	} else if remoteAddr != "" {
		// Fall back to IP-based detection
		rm.RegisterNode(nodeID, remoteAddr, "ip_detect")
	}
}

// ============================================================
// API Handlers
// ============================================================

// GET /api/network/regions — list regions and node distribution
func handleNetworkRegions(w http.ResponseWriter, r *http.Request) {
	if regionMgr == nil {
		writeJSON(w, 200, map[string]any{"regions": map[string]int{}, "config": DefaultRegionConfig()})
		return
	}

	summary := regionMgr.GetRegionSummary()
	config := regionMgr.GetConfig()
	allNodes := regionMgr.GetAllRegions()

	// Convert summary to JSON-friendly format
	regionMap := make(map[string]int)
	for _, reg := range AllRegions() {
		regionMap[string(reg)] = summary[reg]
	}

	// Group nodes by region
	nodesByRegion := make(map[string][]*NodeRegion)
	for _, nr := range allNodes {
		regionKey := string(nr.Region)
		nodesByRegion[regionKey] = append(nodesByRegion[regionKey], nr)
	}

	writeJSON(w, 200, map[string]any{
		"regions":        regionMap,
		"total_nodes":    len(allNodes),
		"config":         config,
		"nodes_by_region": nodesByRegion,
	})
}

// GET /api/network/regions/{region}/nodes — nodes in a specific region
func handleNetworkRegionNodes(w http.ResponseWriter, r *http.Request) {
	if regionMgr == nil {
		writeJSON(w, 200, map[string]any{"nodes": []any{}})
		return
	}

	regionStr := r.PathValue("region")
	if regionStr == "" {
		writeError(w, 400, "region is required")
		return
	}

	allNodes := regionMgr.GetAllRegions()
	var nodes []*NodeRegion
	for _, nr := range allNodes {
		if string(nr.Region) == regionStr {
			nodes = append(nodes, nr)
		}
	}
	if nodes == nil {
		nodes = []*NodeRegion{}
	}

	writeJSON(w, 200, map[string]any{
		"region": regionStr,
		"nodes":  nodes,
		"count":  len(nodes),
	})
}

// PUT /api/network/regions/config — update region configuration
func handleNetworkRegionConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if regionMgr == nil {
		writeError(w, 500, "region manager not initialized")
		return
	}

	var cfg RegionConfig
	if err := readJSON(r, &cfg); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	// Validate
	if cfg.CrossRegionThreshold <= 0 {
		cfg.CrossRegionThreshold = 2.0
	}
	if cfg.RegionWeights == nil {
		cfg.RegionWeights = map[Region]float64{
			RegionAsiaPacific: 1.0,
			RegionAmericas:    1.0,
			RegionEurope:      1.0,
			RegionUnknown:     0.5,
		}
	}

	regionMgr.UpdateConfig(cfg)
	slog.Info("region config updated", "prefer_local", cfg.PreferLocal, "cross_region_threshold", cfg.CrossRegionThreshold)

	writeJSON(w, 200, map[string]any{
		"status": "updated",
		"config": regionMgr.GetConfig(),
	})
}

// ============================================================
// Region-Aware Peer Address Selection
// ============================================================

// pickBestAddressForRegion picks the best address for a peer,
// preferring addresses that match the source region.
func pickBestAddressForRegion(addresses []string, targetRegion Region) string {
	if len(addresses) == 0 {
		return ""
	}
	if len(addresses) == 1 {
		return addresses[0]
	}

	if regionMgr == nil || targetRegion == RegionUnknown {
		return pickBestAddress(addresses)
	}

	// Prefer HTTPS addresses in same region
	var sameRegionHTTPS, sameRegionOther, otherHTTPS, other string
	for _, addr := range addresses {
		host := addr
		if h, _, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(addr, "https://"), "http://")); err == nil {
			host = h
		}
		addrRegion := regionMgr.DetectRegion("", host)

		isHTTPS := strings.HasPrefix(addr, "https://")
		if addrRegion == targetRegion {
			if isHTTPS && sameRegionHTTPS == "" {
				sameRegionHTTPS = addr
			} else if sameRegionOther == "" {
				sameRegionOther = addr
			}
		} else {
			if isHTTPS && otherHTTPS == "" {
				otherHTTPS = addr
			} else if other == "" {
				other = addr
			}
		}
	}

	if sameRegionHTTPS != "" {
		return sameRegionHTTPS
	}
	if sameRegionOther != "" {
		return sameRegionOther
	}
	if otherHTTPS != "" {
		return otherHTTPS
	}
	return other
}

// ============================================================
// Sync Regions from Peers
// ============================================================

// SyncRegionsFromPeers scans known peers and detects their regions.
func (rm *RegionManager) SyncRegionsFromPeers() {
	if netMgr == nil {
		return
	}

	peers := netMgr.GetPeers()
	for _, peer := range peers {
		if len(peer.Addresses) == 0 {
			continue
		}
		// Use the first address for detection
		addr := peer.Addresses[0]
		rm.RegisterNode(peer.NodeID, addr, "ip_detect")
	}

	// Also register self
	if netMgr.GetNodeID() != "" {
		addrs := netMgr.collectAddresses()
		if len(addrs) > 0 {
			rm.RegisterNode(netMgr.GetNodeID(), addrs[0], "ip_detect")
		}
	}

	slog.Debug("synced regions from peers", "total_regions", len(rm.GetAllRegions()))
}

// ============================================================
// Helper — extract remote IP from heartbeat request
// ============================================================

func extractRemoteIP(r *http.Request) string {
	// Check X-Forwarded-For first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ============================================================
// Initialization helper — called from main.go
// ============================================================

// startRegionSyncLoop periodically syncs region information.
func startRegionSyncLoop() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		// Initial sync after 5 seconds
		time.Sleep(5 * time.Second)
		if regionMgr != nil {
			regionMgr.SyncRegionsFromPeers()
		}

		for range ticker.C {
			if regionMgr != nil {
				regionMgr.SyncRegionsFromPeers()
			}
		}
	}()
}

// ============================================================
// JSON marshaling helpers for Region
// ============================================================

func (r Region) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(r))
}

func (r *Region) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*r = Region(s)
	return nil
}
