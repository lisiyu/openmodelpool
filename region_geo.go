package main

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// region_geo.go — accurate GeoIP-based region detection.
//
// The first-octet heuristic in DetectRegion (network_region_impl.go) is a
// rough offline approximation: it misclassifies whole /8s (e.g. 1.0.0.0/8 is
// APNIC space, not Americas) and can never distinguish countries sharing a
// first octet. This file adds an accurate path: a cached, HTTPS-only country
// lookup (ip-api.com — the same provider update.go's detectRegion already
// uses), mapping ISO country codes onto the three routing regions.
//
// Design rules, consistent with the rest of region routing:
//   - Never block hot paths. Peer detection keeps the synchronous heuristic;
//     GeoIP enrichment for peers is asynchronous and singleflight per IP.
//   - Offline-safe. region_geo_enabled=false (or any lookup failure) falls
//     back to the pure heuristic. The lookup function is a var so the offline
//     test suite can stub it.
//   - Rate-limit friendly. Results are cached per IP (24h for hits, 5min for
//     misses); ip-api.com's free tier allows 45 req/min.

// countryToRegion maps an ISO 3166-1 alpha-2 country code to a routing Region.
// Middle East and Africa map to Europe: with only three regions this is the
// least-wrong choice latency-wise (both are typically closer to EU PoPs than
// to AP or Americas). Unknown/invalid codes map to RegionUnknown.
func countryToRegion(cc string) Region {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 {
		return RegionUnknown
	}
	switch cc {
	// Asia-Pacific + Oceania + Central Asia
	case "CN", "HK", "MO", "TW", "JP", "KR", "MN",
		"SG", "MY", "TH", "VN", "LA", "KH", "MM", "PH", "ID", "BN", "TL",
		"AU", "NZ", "PG", "FJ", "SB", "VU", "NC", "PF", "WS", "TO", "TV",
		"KI", "NR", "PW", "MH", "FM", "CK", "NU",
		"IN", "PK", "BD", "LK", "NP", "BT", "MV",
		"KZ", "UZ", "TM", "KG", "TJ":
		return RegionAsiaPacific
	// Americas
	case "US", "CA", "MX", "GT", "BZ", "SV", "HN", "NI", "CR", "PA",
		"BS", "CU", "JM", "HT", "DO", "PR", "VI", "BB", "TT", "GD", "LC",
		"VC", "AG", "DM", "KN", "GL",
		"BR", "AR", "CL", "CO", "PE", "VE", "EC", "BO", "PY", "UY",
		"GY", "SR", "GF", "FK":
		return RegionAmericas
	// Europe + Russia + Turkey + Middle East + Africa (see doc comment)
	case "AL", "AD", "AT", "BY", "BE", "BA", "BG", "HR", "CY", "CZ", "DK",
		"EE", "FI", "FR", "DE", "GR", "HU", "IS", "IE", "IT", "LV", "LI",
		"LT", "LU", "MT", "MD", "MC", "ME", "NL", "MK", "NO", "PL", "PT",
		"RO", "RU", "SM", "RS", "SK", "SI", "ES", "SE", "CH", "UA", "GB", "VA",
		"TR", "IL", "PS", "JO", "LB", "SY", "IQ", "IR", "SA", "YE", "OM",
		"AE", "QA", "BH", "KW",
		"DZ", "AO", "BJ", "BW", "BF", "BI", "CM", "CV", "CF", "TD", "KM",
		"CG", "CD", "CI", "DJ", "EG", "GQ", "ER", "ET", "GA", "GM", "GH",
		"GN", "GW", "KE", "LS", "LR", "LY", "MG", "MW", "ML", "MR", "MU",
		"MA", "MZ", "NA", "NE", "NG", "RW", "ST", "SN", "SC", "SL", "SO",
		"ZA", "SS", "SD", "SZ", "TZ", "TG", "TN", "UG", "ZM", "ZW":
		return RegionEurope
	default:
		return RegionUnknown
	}
}

// geoCacheTTL bounds how long a successful country result is trusted.
// geoNegativeTTL bounds how long a failed lookup suppresses retries.
var (
	geoCacheTTL    = 24 * time.Hour
	geoNegativeTTL = 5 * time.Minute
)

type geoCacheEntry struct {
	country string // empty + ok == negative cache entry (lookup failed)
	expires time.Time
}

var (
	geoCacheMu sync.Mutex
	geoCache   = make(map[string]geoCacheEntry)
)

// errGeoNegative is returned for IPs inside the negative-cache window.
var errGeoNegative = fmt.Errorf("region geo: negative cache hit")

// geoLookupCountry resolves an IP to an ISO 3166-1 alpha-2 country code.
// It is a var so tests can stub it; the offline suite must not touch the network.
var geoLookupCountry = defaultGeoLookupCountry

// defaultGeoLookupCountry queries ip-api.com over HTTPS and caches the result.
// SEC: HTTPS only — the plaintext HTTP endpoint would let a network MITM forge
// the geo response and poison region routing (same rationale as update.go SEC-B3-5).
func defaultGeoLookupCountry(ip string) (string, error) {
	now := time.Now()
	geoCacheMu.Lock()
	if e, ok := geoCache[ip]; ok && now.Before(e.expires) {
		geoCacheMu.Unlock()
		if e.country == "" {
			return "", errGeoNegative
		}
		return e.country, nil
	}
	geoCacheMu.Unlock()

	url := "https://ip-api.com/line/" + ip + "?fields=countryCode"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	resp, err := GetSharedHTTPClientWithTimeout(5 * time.Second).Do(req)
	if err != nil {
		cacheGeoNegative(ip, now)
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16))
	if err != nil {
		cacheGeoNegative(ip, now)
		return "", err
	}
	cc := strings.ToUpper(strings.TrimSpace(string(body)))
	if !isCountryCode(cc) {
		cacheGeoNegative(ip, now)
		return "", fmt.Errorf("region geo: unexpected country code %q", cc)
	}

	geoCacheMu.Lock()
	geoCache[ip] = geoCacheEntry{country: cc, expires: now.Add(geoCacheTTL)}
	geoCacheMu.Unlock()
	return cc, nil
}

// cacheGeoNegative records a failed lookup so we don't hammer the provider.
func cacheGeoNegative(ip string, now time.Time) {
	geoCacheMu.Lock()
	geoCache[ip] = geoCacheEntry{country: "", expires: now.Add(geoNegativeTTL)}
	geoCacheMu.Unlock()
}

// isCountryCode reports whether s looks like an ISO 3166-1 alpha-2 code.
func isCountryCode(s string) bool {
	if len(s) != 2 {
		return false
	}
	return s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

// isPublicRoutableIP reports whether ip is globally routable unicast.
// GeoIP lookups are meaningless (and wasteful) for private, loopback,
// link-local, multicast or unspecified addresses.
func isPublicRoutableIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return !parsed.IsPrivate() && !parsed.IsLoopback() &&
		!parsed.IsLinkLocalUnicast() && !parsed.IsLinkLocalMulticast() &&
		!parsed.IsMulticast() && !parsed.IsUnspecified()
}

// regionGeoEnabled reports whether GeoIP-based region detection is on.
// Config key: region_geo_enabled (default true). Nil-safe for unit tests that
// don't initialize the global config.
func regionGeoEnabled() bool {
	if cfg == nil {
		return true
	}
	return cfg.Get("region_geo_enabled", "true") != "false"
}

// stripPortForGeo removes a trailing :port if present, returning the bare host.
func stripPortForGeo(ip string) string {
	if h, _, err := net.SplitHostPort(ip); err == nil {
		return h
	}
	return ip
}

// GeoDetectRegion resolves ip to a routing Region via GeoIP country lookup.
// It returns RegionUnknown when geo detection is disabled, the IP is not
// publicly routable, or the lookup fails — callers fall back to the offline
// first-octet heuristic in that case.
func (rm *RegionManager) GeoDetectRegion(ip string) Region {
	if !regionGeoEnabled() {
		return RegionUnknown
	}
	host := stripPortForGeo(ip)
	if !isPublicRoutableIP(host) {
		return RegionUnknown
	}
	cc, err := geoLookupCountry(host)
	if err != nil {
		slog.Debug("region geo lookup failed, falling back to heuristic", "ip", host, "error", err)
		return RegionUnknown
	}
	return countryToRegion(cc)
}

// regionSourceRank ranks region sources for conflict resolution: a stronger
// source is never overwritten by a weaker one. Self-reported regions
// (self_report/heartbeat) always win over heuristics; GeoIP outranks the
// first-octet heuristic. Unlisted sources (reconcile_*, test, …) rank 0 and
// are treated as weak.
func regionSourceRank(source string) int {
	switch source {
	case "self_report", "heartbeat":
		return 3
	case "geo_ip":
		return 2
	case "ip_detect", "auto_detect":
		return 1
	default:
		return 0
	}
}

// maybeEnrichRegionAsync upgrades an unknown-region entry via GeoIP without
// blocking the caller. It is singleflight per IP and a no-op when geo
// detection is disabled or the entry already has a known region.
func (rm *RegionManager) maybeEnrichRegionAsync(nodeID, ip string) {
	if !regionGeoEnabled() {
		return
	}
	host := stripPortForGeo(ip)
	if !isPublicRoutableIP(host) {
		return
	}
	rm.mu.Lock()
	if rm.geoInflight == nil {
		rm.geoInflight = make(map[string]struct{})
	}
	if _, dup := rm.geoInflight[host]; dup {
		rm.mu.Unlock()
		return
	}
	if e, ok := rm.nodes[nodeID]; ok && e.Region != RegionUnknown && e.Region != RegionEmpty {
		rm.mu.Unlock()
		return
	}
	rm.geoInflight[host] = struct{}{}
	rm.mu.Unlock()

	go func() {
		defer func() {
			rm.mu.Lock()
			delete(rm.geoInflight, host)
			rm.mu.Unlock()
		}()
		region := rm.GeoDetectRegion(host)
		if region == RegionUnknown {
			return
		}
		rm.mu.Lock()
		defer rm.mu.Unlock()
		// Only upgrade entries that are still unknown: never overwrite a
		// region that was resolved (or self-reported) in the meantime.
		// Replace the entry instead of mutating it, so readers holding an
		// older snapshot never observe a torn update.
		if e, ok := rm.nodes[nodeID]; ok && (e.Region == RegionUnknown || e.Region == RegionEmpty) {
			rm.nodes[nodeID] = &NodeRegion{Region: region, Source: "geo_ip"}
			slog.Info("region enriched via GeoIP", "node_id", nodeID, "region", region)
		}
	}()
}
