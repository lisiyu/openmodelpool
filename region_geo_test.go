package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubGeoLookup swaps geoLookupCountry for the duration of the test.
func stubGeoLookup(t *testing.T, fn func(ip string) (string, error)) {
	t.Helper()
	orig := geoLookupCountry
	geoLookupCountry = fn
	t.Cleanup(func() { geoLookupCountry = orig })
}

// stubCfg swaps the global config with an in-memory one for the test.
func stubCfg(t *testing.T, data map[string]any) {
	t.Helper()
	orig := cfg
	cfg = &Config{data: data}
	t.Cleanup(func() { cfg = orig })
}

func TestCountryToRegion(t *testing.T) {
	cases := []struct {
		cc   string
		want Region
	}{
		{"CN", RegionAsiaPacific},
		{"cn", RegionAsiaPacific}, // case-insensitive
		{"HK", RegionAsiaPacific},
		{"JP", RegionAsiaPacific},
		{"AU", RegionAsiaPacific},
		{"IN", RegionAsiaPacific},
		{"SG", RegionAsiaPacific},
		{"US", RegionAmericas},
		{"CA", RegionAmericas},
		{"BR", RegionAmericas},
		{"MX", RegionAmericas},
		{"DE", RegionEurope},
		{"GB", RegionEurope},
		{"FR", RegionEurope},
		{"RU", RegionEurope},
		{"ZA", RegionEurope}, // Africa -> EU (least-wrong of the three)
		{"AE", RegionEurope}, // Middle East -> EU
		{"XX", RegionUnknown},
		{"", RegionUnknown},
		{"USA", RegionUnknown}, // not alpha-2
		{"A", RegionUnknown},
	}
	for _, c := range cases {
		if got := countryToRegion(c.cc); got != c.want {
			t.Errorf("countryToRegion(%q) = %q, want %q", c.cc, got, c.want)
		}
	}
}

func TestIsPublicRoutableIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"203.0.113.5", true}, // TEST-NET-3, still "public" per net stdlib
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"172.16.5.4", false},
		{"127.0.0.1", false},
		{"169.254.1.1", false},
		{"::1", false},
		{"224.0.0.1", false},
		{"not-an-ip", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isPublicRoutableIP(c.ip); got != c.want {
			t.Errorf("isPublicRoutableIP(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestGeoDetectRegionStub(t *testing.T) {
	stubCfg(t, map[string]any{"region_geo_enabled": "true"})
	stubGeoLookup(t, func(ip string) (string, error) {
		if ip == "1.1.1.1" {
			return "AU", nil // the first-octet heuristic says Americas; geo says AP
		}
		return "DE", nil
	})
	rm := NewRegionManager()
	if got := rm.GeoDetectRegion("1.1.1.1"); got != RegionAsiaPacific {
		t.Errorf("GeoDetectRegion(1.1.1.1) = %q, want ap", got)
	}
	if got := rm.GeoDetectRegion("8.8.8.8:443"); got != RegionEurope {
		t.Errorf("GeoDetectRegion(8.8.8.8:443) = %q, want eu", got)
	}
	// Non-routable IPs never reach the lookup.
	if got := rm.GeoDetectRegion("192.168.0.1"); got != RegionUnknown {
		t.Errorf("GeoDetectRegion(192.168.0.1) = %q, want unknown", got)
	}
}

func TestGeoDetectRegionDisabled(t *testing.T) {
	stubCfg(t, map[string]any{"region_geo_enabled": "false"})
	called := false
	stubGeoLookup(t, func(ip string) (string, error) {
		called = true
		return "CN", nil
	})
	rm := NewRegionManager()
	if got := rm.GeoDetectRegion("8.8.8.8"); got != RegionUnknown {
		t.Errorf("GeoDetectRegion with geo disabled = %q, want unknown", got)
	}
	if called {
		t.Error("geo lookup must not run when region_geo_enabled=false")
	}
}

func TestGeoDetectRegionLookupError(t *testing.T) {
	stubCfg(t, map[string]any{"region_geo_enabled": "true"})
	stubGeoLookup(t, func(ip string) (string, error) {
		return "", errors.New("boom")
	})
	rm := NewRegionManager()
	if got := rm.GeoDetectRegion("8.8.8.8"); got != RegionUnknown {
		t.Errorf("GeoDetectRegion on lookup error = %q, want unknown", got)
	}
}

// waitFor polls cond until true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", msg)
}

func TestMaybeEnrichRegionAsyncUpgradesUnknown(t *testing.T) {
	stubCfg(t, map[string]any{"region_geo_enabled": "true"})
	stubGeoLookup(t, func(ip string) (string, error) { return "DE", nil })
	rm := NewRegionManager()
	// 25.0.0.0/8: public per Go stdlib, unknown to the first-octet heuristic.
	rm.RegisterNode("peer-1", "25.0.0.1:443", "test")
	if got := rm.GetNodeRegion("peer-1"); got == nil || got.Region != RegionUnknown {
		t.Fatalf("precondition: peer-1 should start unknown, got %+v", got)
	}
	waitFor(t, 3*time.Second, func() bool {
		got := rm.GetNodeRegion("peer-1")
		return got != nil && got.Region == RegionEurope && got.Source == "geo_ip"
	}, "peer-1 region to be enriched to eu via geo_ip")
	rm.mu.RLock()
	inflight := len(rm.geoInflight)
	rm.mu.RUnlock()
	if inflight != 0 {
		t.Errorf("geoInflight not cleaned up, %d entries remain", inflight)
	}
}

func TestMaybeEnrichRegionAsyncNeverOverwritesKnown(t *testing.T) {
	stubCfg(t, map[string]any{"region_geo_enabled": "true"})
	stubGeoLookup(t, func(ip string) (string, error) { return "DE", nil })
	rm := NewRegionManager()
	rm.RegisterNodeSelfReport("peer-2", "ap", "", 0, 0)
	rm.ProcessHeartbeatRegion("peer-2", nil, "25.0.0.2:443")
	time.Sleep(200 * time.Millisecond) // let any stray enrichment run
	got := rm.GetNodeRegion("peer-2")
	if got == nil || got.Region != RegionAsiaPacific {
		t.Fatalf("self-reported region must win, got %+v", got)
	}
}

func TestValidateRegionConfig(t *testing.T) {
	rc := RegionConfig{PreferLocal: true, CrossRegionThreshold: -1}
	if err := validateRegionConfig(&rc); err == nil {
		t.Error("negative cross_region_threshold must be rejected")
	}
	// Negative weights are rejected (not silently dropped) on the API path.
	rc = RegionConfig{
		PreferLocal:          false,
		CrossRegionThreshold: 1.5,
		RegionWeights:        map[Region]float64{RegionEurope: -1},
	}
	if err := validateRegionConfig(&rc); err == nil {
		t.Error("negative region weight must be rejected")
	}
	rc = RegionConfig{
		PreferLocal:          false,
		CrossRegionThreshold: 1.5,
		RegionWeights:        map[Region]float64{"asia": 2, "xx": 1},
	}
	if err := validateRegionConfig(&rc); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if w := rc.RegionWeights[RegionAsiaPacific]; w != 2 {
		t.Errorf("alias 'asia' should canonicalize to ap with weight 2, got %v", w)
	}
	if _, ok := rc.RegionWeights[Region("xx")]; ok {
		t.Error("unrecognized region key 'xx' should be dropped")
	}
}

func TestRegionConfigPersistenceRoundTrip(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	rc := RegionConfig{
		PreferLocal:          false,
		CrossRegionThreshold: 3.5,
		RegionWeights:        map[Region]float64{RegionAsiaPacific: 2, RegionEurope: 1},
	}
	persistRegionConfig(rc)

	fresh := NewRegionManager()
	loadRegionConfigFromSettings(fresh)
	got := fresh.GetConfig()
	if got.PreferLocal {
		t.Error("PreferLocal should persist as false")
	}
	if got.CrossRegionThreshold != 3.5 {
		t.Errorf("CrossRegionThreshold = %v, want 3.5", got.CrossRegionThreshold)
	}
	if got.RegionWeights[RegionAsiaPacific] != 2 || got.RegionWeights[RegionEurope] != 1 {
		t.Errorf("RegionWeights did not round-trip: %+v", got.RegionWeights)
	}
}

func TestLoadRegionConfigIgnoresInvalid(t *testing.T) {
	env := setupTestEnv(t)
	_ = env
	cfg.Set("region_cross_threshold", "not-a-number")
	cfg.Set("region_weights_json", "{bad json")
	fresh := NewRegionManager()
	loadRegionConfigFromSettings(fresh)
	got := fresh.GetConfig()
	if got.CrossRegionThreshold != 2.0 {
		t.Errorf("invalid threshold should fall back to default 2.0, got %v", got.CrossRegionThreshold)
	}
	if len(got.RegionWeights) != 1 || got.RegionWeights[RegionUnknown] != 0.5 {
		t.Errorf("invalid weights should fall back to default, got %+v", got.RegionWeights)
	}
}

func TestProcessHeartbeatRegionSourceGuard(t *testing.T) {
	rm := NewRegionManager()
	// Self-reported region survives a later heartbeat that only has an IP.
	rm.RegisterNodeSelfReport("n1", "ap", "", 0, 0)
	rm.ProcessHeartbeatRegion("n1", nil, "8.8.8.8") // heuristic: americas
	if got := rm.GetNodeRegion("n1"); got == nil || got.Region != RegionAsiaPacific {
		t.Fatalf("self-reported ap must survive heuristic heartbeat, got %+v", got)
	}
	// A known heuristic region is never clobbered by an unknown guess.
	rm.ProcessHeartbeatRegion("n2", nil, "8.8.8.8")
	rm.ProcessHeartbeatRegion("n2", nil, "25.0.0.9") // heuristic: unknown
	if got := rm.GetNodeRegion("n2"); got == nil || got.Region != RegionAmericas {
		t.Fatalf("known americas must survive unknown guess, got %+v", got)
	}
	// Same-rank heuristic with a *known* new region still updates (peer moved).
	rm.ProcessHeartbeatRegion("n2", nil, "114.100.1.1") // heuristic: ap
	if got := rm.GetNodeRegion("n2"); got == nil || got.Region != RegionAsiaPacific {
		t.Fatalf("same-rank known update should apply, got %+v", got)
	}
}

func TestRegionSourceRank(t *testing.T) {
	if regionSourceRank("self_report") <= regionSourceRank("geo_ip") {
		t.Error("self_report should outrank geo_ip")
	}
	if regionSourceRank("geo_ip") <= regionSourceRank("ip_detect") {
		t.Error("geo_ip should outrank ip_detect")
	}
	if regionSourceRank("whatever") != 0 {
		t.Error("unlisted sources should rank 0")
	}
}

// ============================================================
// GeoIP hardening: opt-in default, provider status checks, shared rate limit.
// ============================================================

// GeoIP is opt-in: with an empty config no lookup may run and no public IP
// may leave the node.
func TestGeoDetectRegion_DisabledByDefault(t *testing.T) {
	stubCfg(t, map[string]any{})
	called := false
	stubGeoLookup(t, func(ip string) (string, error) {
		called = true
		return "CN", nil
	})
	rm := NewRegionManager()
	if got := rm.GeoDetectRegion("8.8.8.8"); got != RegionUnknown {
		t.Errorf("GeoDetectRegion with default config = %q, want unknown (opt-in)", got)
	}
	if called {
		t.Error("no provider lookup may run unless region_geo_enabled=true")
	}
}

// A non-200 provider response is an error, never a country code.
func TestGeoLookupCountry_RejectsNon200(t *testing.T) {
	var hits int
	srv := newGeoTestServer(t, &hits, http.StatusTooManyRequests, "US")
	oldBase := geoLookupBaseURL
	geoLookupBaseURL = srv.URL + "/"
	t.Cleanup(func() { geoLookupBaseURL = oldBase })

	if _, err := defaultGeoLookupCountry("9.9.9.9"); err == nil {
		t.Fatal("HTTP 429 must be an error, not a country code")
	}
	if hits != 1 {
		t.Fatalf("expected exactly 1 provider request, got %d", hits)
	}
}

// The process-wide budget gates cache misses: once exhausted, lookups fail
// WITHOUT touching the network.
func TestGeoLookupCountry_RateLimited(t *testing.T) {
	var hits int
	srv := newGeoTestServer(t, &hits, http.StatusOK, "DE")
	oldBase := geoLookupBaseURL
	geoLookupBaseURL = srv.URL + "/"
	t.Cleanup(func() { geoLookupBaseURL = oldBase })

	geoRateMu.Lock()
	geoRateWindow = time.Now()
	geoRateCount = geoRateMaxPerMinute
	geoRateMu.Unlock()
	t.Cleanup(func() {
		geoRateMu.Lock()
		geoRateWindow = time.Time{}
		geoRateCount = 0
		geoRateMu.Unlock()
	})

	if _, err := defaultGeoLookupCountry("10.9.9.9"); !errors.Is(err, errGeoRateLimited) {
		t.Fatalf("exhausted budget must return errGeoRateLimited, got %v", err)
	}
	if hits != 0 {
		t.Fatalf("rate-limited lookup must not hit the network (hits=%d)", hits)
	}
}

// A healthy provider response still flows through.
func TestGeoLookupCountry_OK(t *testing.T) {
	var hits int
	srv := newGeoTestServer(t, &hits, http.StatusOK, "jp\n")
	oldBase := geoLookupBaseURL
	geoLookupBaseURL = srv.URL + "/"
	t.Cleanup(func() { geoLookupBaseURL = oldBase })

	cc, err := defaultGeoLookupCountry("10.8.8.8")
	if err != nil {
		t.Fatalf("healthy lookup failed: %v", err)
	}
	if cc != "JP" {
		t.Fatalf("country = %q, want JP (uppercased/trimmed)", cc)
	}
}

// newGeoTestServer serves a fixed provider response and counts requests.
func newGeoTestServer(t *testing.T, hits *int, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}
