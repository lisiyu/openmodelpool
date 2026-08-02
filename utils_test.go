package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ============================================================
// clamp tests
// ============================================================

func TestClamp_WithinRange(t *testing.T) {
	if got := clamp(5, 0, 10); got != 5 {
		t.Errorf("clamp(5, 0, 10) = %f, want 5", got)
	}
}

func TestClamp_BelowMin(t *testing.T) {
	if got := clamp(-5, 0, 10); got != 0 {
		t.Errorf("clamp(-5, 0, 10) = %f, want 0", got)
	}
}

func TestClamp_AboveMax(t *testing.T) {
	if got := clamp(15, 0, 10); got != 10 {
		t.Errorf("clamp(15, 0, 10) = %f, want 10", got)
	}
}

func TestClamp_AtBoundary(t *testing.T) {
	if got := clamp(0, 0, 10); got != 0 {
		t.Errorf("clamp(0, 0, 10) = %f, want 0", got)
	}
	if got := clamp(10, 0, 10); got != 10 {
		t.Errorf("clamp(10, 0, 10) = %f, want 10", got)
	}
}

// ============================================================
// normalize tests
// ============================================================

func TestNormalize_Middle(t *testing.T) {
	if got := normalize(5, 0, 10); got != 0.5 {
		t.Errorf("normalize(5, 0, 10) = %f, want 0.5", got)
	}
}

func TestNormalize_Min(t *testing.T) {
	if got := normalize(0, 0, 10); got != 0.0 {
		t.Errorf("normalize(0, 0, 10) = %f, want 0.0", got)
	}
}

func TestNormalize_Max(t *testing.T) {
	if got := normalize(10, 0, 10); got != 1.0 {
		t.Errorf("normalize(10, 0, 10) = %f, want 1.0", got)
	}
}

func TestNormalize_SameMinMax(t *testing.T) {
	if got := normalize(5, 5, 5); got != 0 {
		t.Errorf("normalize(5, 5, 5) = %f, want 0", got)
	}
}

// ============================================================
// minMax tests
// ============================================================

func TestMinMax_Empty(t *testing.T) {
	minV, maxV := minMax([]float64{})
	if minV != 0 || maxV != 1 {
		t.Errorf("minMax([]) = (%f, %f), want (0, 1)", minV, maxV)
	}
}

func TestMinMax_SingleValue(t *testing.T) {
	minV, maxV := minMax([]float64{3.14})
	if minV != 3.14 || maxV != 3.14 {
		t.Errorf("minMax([3.14]) = (%f, %f), want (3.14, 3.14)", minV, maxV)
	}
}

func TestMinMax_Multiple(t *testing.T) {
	minV, maxV := minMax([]float64{3.0, 1.0, 2.0})
	if minV != 1.0 || maxV != 3.0 {
		t.Errorf("minMax([3,1,2]) = (%f, %f), want (1, 3)", minV, maxV)
	}
}

func TestMinMax_Negative(t *testing.T) {
	minV, maxV := minMax([]float64{-5.0, 0.0, -3.0, 2.0})
	if minV != -5.0 || maxV != 2.0 {
		t.Errorf("minMax([-5,0,-3,2]) = (%f, %f), want (-5, 2)", minV, maxV)
	}
}

// ============================================================
// roundTo6 tests
// ============================================================

func TestRoundTo6_WholeNumbers(t *testing.T) {
	if got := roundTo6(1.0); got != 1.0 {
		t.Errorf("roundTo6(1.0) = %f, want 1.0", got)
	}
	if got := roundTo6(0.0); got != 0.0 {
		t.Errorf("roundTo6(0.0) = %f, want 0.0", got)
	}
}

func TestRoundTo6_Precision(t *testing.T) {
	if got := roundTo6(1.1234567); got != 1.123457 {
		t.Errorf("roundTo6(1.1234567) = %f, want 1.123457", got)
	}
}

func TestRoundTo6_Negative(t *testing.T) {
	if got := roundTo6(-1.1234567); got != -1.123457 {
		t.Errorf("roundTo6(-1.1234567) = %f, want -1.123457", got)
	}
}

// ============================================================
// dedupeStrings tests
// ============================================================

func TestDedupeStrings_Empty(t *testing.T) {
	if got := dedupeStrings([]string{}); len(got) != 0 {
		t.Errorf("dedupeStrings([]) = %v, want empty", got)
	}
	if got := dedupeStrings(nil); len(got) != 0 {
		t.Errorf("dedupeStrings(nil) = %v, want empty", got)
	}
}

func TestDedupeStrings_NoDuplicates(t *testing.T) {
	in := []string{"a", "b", "c"}
	got := dedupeStrings(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(got))
	}
	for i, want := range in {
		if got[i] != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestDedupeStrings_WithDuplicates(t *testing.T) {
	in := []string{"a", "b", "a", "c", "b"}
	got := dedupeStrings(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 elements, got %d: %v", len(got), got)
	}
	expected := []string{"a", "b", "c"}
	for i, want := range expected {
		if got[i] != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestDedupeStrings_FiltersEmpty(t *testing.T) {
	in := []string{"a", "", "b", "", "c"}
	got := dedupeStrings(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 elements, got %d: %v", len(got), got)
	}
	expected := []string{"a", "b", "c"}
	for i, want := range expected {
		if got[i] != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want)
		}
	}
}

// ============================================================
// shortNodeID tests
// ============================================================

func TestShortNodeID_Short(t *testing.T) {
	if got := shortNodeID("mmx-123"); got != "mmx-123" {
		t.Errorf("shortNodeID('mmx-123') = %q, want 'mmx-123'", got)
	}
}

func TestShortNodeID_Long(t *testing.T) {
	id := "mmx-1234567890abcdef1234567890abcdef1234567890abcdef"
	got := shortNodeID(id)
	if got != "mmx-1234…cdef" {
		t.Errorf("shortNodeID(long) = %q, want 'mmx-1234…cdef'", got)
	}
}

func TestShortNodeID_Exactly12(t *testing.T) {
	id := "123456789012"
	got := shortNodeID(id)
	if got != id {
		t.Errorf("shortNodeID('123456789012') = %q, want same", got)
	}
}

// ============================================================
// peerDisplayName tests
// ============================================================.

func TestPeerDisplayName_GitHubUser(t *testing.T) {
	peer := NodeInfo{GitHubUser: "octocat", Endpoint: "https://example.com", NodeID: "mmx-1234567890abcdef"}
	got := peerDisplayName(peer)
	if got != "octocat" {
		t.Errorf("peerDisplayName = %q, want 'octocat'", got)
	}
}

func TestPeerDisplayName_Endpoint(t *testing.T) {
	peer := NodeInfo{Endpoint: "https://example.com", NodeID: "mmx-1234567890abcdef"}
	got := peerDisplayName(peer)
	if got != "https://example.com" {
		t.Errorf("peerDisplayName = %q, want 'https://example.com'", got)
	}
}

func TestPeerDisplayName_NodeIDFallback(t *testing.T) {
	peer := NodeInfo{NodeID: "mmx-1234567890abcdef1234567890abcdef1234567890abcdef"}
	got := peerDisplayName(peer)
	if got != "mmx-1234…cdef" {
		t.Errorf("peerDisplayName = %q, want 'mmx-1234…cdef'", got)
	}
}

// ============================================================
// extractSubdomain tests
// ============================================================

func TestExtractSubdomain_Valid(t *testing.T) {
	if got := extractSubdomain("https://my-subdomain.example.com"); got != "my-subdomain" {
		t.Errorf("extractSubdomain = %q, want 'my-subdomain'", got)
	}
}

func TestExtractSubdomain_CloudflareFormat(t *testing.T) {
	if got := extractSubdomain("https://xxx-xxx-xxx.trycloudflare.com"); got != "xxx-xxx-xxx" {
		t.Errorf("extractSubdomain = %q, want 'xxx-xxx-xxx'", got)
	}
}

func TestExtractSubdomain_NoScheme(t *testing.T) {
	if got := extractSubdomain("example.com"); got != "" {
		t.Errorf("extractSubdomain(no-scheme) = %q, want ''", got)
	}
}

func TestExtractSubdomain_Empty(t *testing.T) {
	if got := extractSubdomain(""); got != "" {
		t.Errorf("extractSubdomain('') = %q, want ''", got)
	}
}

// ============================================================
// sanitizeDomain tests
// ============================================================

func TestSanitizeDomain_Simple(t *testing.T) {
	if got := sanitizeDomain("example.com"); got != "example-com" {
		t.Errorf("sanitizeDomain('example.com') = %q, want 'example-com'", got)
	}
}

func TestSanitizeDomain_Uppercase(t *testing.T) {
	if got := sanitizeDomain("Example.COM"); got != "example-com" {
		t.Errorf("sanitizeDomain('Example.COM') = %q, want 'example-com'", got)
	}
}

func TestSanitizeDomain_Spaces(t *testing.T) {
	if got := sanitizeDomain("my domain"); got != "my-domain" {
		t.Errorf("sanitizeDomain('my domain') = %q, want 'my-domain'", got)
	}
}

func TestSanitizeDomain_Truncation(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz12345.com"
	got := sanitizeDomain(long)
	if len(got) > 40 {
		t.Errorf("sanitizeDomain long = %q, len=%d, want <= 40", got, len(got))
	}
	expectedPrefix := "abcdefghijklmnopqrstuvwxyzabcdefghijklmn"
	if got != expectedPrefix {
		t.Errorf("sanitizeDomain long = %q, want prefix %q", got, expectedPrefix)
	}
}

// ============================================================
// validTimestamp tests
// ============================================================

func TestValidTimestamp_Valid(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	if !validTimestamp(ts) {
		t.Errorf("validTimestamp(%q) should be true for current time", ts)
	}
}

func TestValidTimestamp_Expired(t *testing.T) {
	past := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	if validTimestamp(past) {
		t.Errorf("validTimestamp(%q) should be false for 10 min ago", past)
	}
}

func TestValidTimestamp_Future(t *testing.T) {
	future := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if validTimestamp(future) {
		t.Errorf("validTimestamp(%q) should be false for 10 min ahead", future)
	}
}

func TestValidTimestamp_InvalidFormat(t *testing.T) {
	if validTimestamp("not-a-timestamp") {
		t.Error("validTimestamp('not-a-timestamp') should be false")
	}
}

func TestValidTimestamp_Empty(t *testing.T) {
	if validTimestamp("") {
		t.Error("validTimestamp('') should be false")
	}
}

// ============================================================
// validMsgType tests
// ============================================================

func TestValidMsgType_Valid(t *testing.T) {
	for _, mt := range []string{"request", "collaboration", "system", "general"} {
		if !validMsgType(mt) {
			t.Errorf("validMsgType(%q) should be true", mt)
		}
	}
}

func TestValidMsgType_Invalid(t *testing.T) {
	for _, mt := range []string{"", "unknown", "spam", "REQuEST"} {
		if validMsgType(mt) {
			t.Errorf("validMsgType(%q) should be false", mt)
		}
	}
}

// ============================================================
// generateKeyID tests
// ============================================================

func TestGenerateKeyID_Format(t *testing.T) {
	id := generateKeyID()
	if !strings.HasPrefix(id, "key_") {
		t.Errorf("generateKeyID() = %q, should start with 'key_'", id)
	}
	hexPart := id[4:]
	if len(hexPart) != 16 {
		t.Errorf("generateKeyID hex part = %q, len=%d, want 16", hexPart, len(hexPart))
	}
}

func TestGenerateKeyID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateKeyID()
		if seen[id] {
			t.Errorf("generateKeyID produced duplicate: %q", id)
		}
		seen[id] = true
	}
}

// ============================================================
// isOrgPrefix tests
// ============================================================

func TestIsOrgPrefix_Valid(t *testing.T) {
	for _, s := range []string{"Qwen", "deepseek-ai", "meta-llama", "openai", "anthropic", "google"} {
		if !isOrgPrefix(s) {
			t.Errorf("isOrgPrefix(%q) should be true", s)
		}
	}
}

func TestIsOrgPrefix_Invalid(t *testing.T) {
	for _, s := range []string{"", "unknown", "my-corp", "OpenAI"} {
		if isOrgPrefix(s) {
			t.Errorf("isOrgPrefix(%q) should be false", s)
		}
	}
}

// ============================================================
// isInFlightPhase tests
// ============================================================

func TestIsInFlightPhase_True(t *testing.T) {
	for _, p := range []UpdatePhase{PhaseDownloading, PhaseReplacing, PhaseRestarting} {
		if !isInFlightPhase(p) {
			t.Errorf("isInFlightPhase(%q) should be true", p)
		}
	}
}

func TestIsInFlightPhase_False(t *testing.T) {
	for _, p := range []UpdatePhase{PhaseIdle, PhaseSuccess, PhaseFailed, PhaseUnsupported, PhaseNeedsManualRestart} {
		if isInFlightPhase(p) {
			t.Errorf("isInFlightPhase(%q) should be false", p)
		}
	}
}

// ============================================================
// compareVersion tests
// ============================================================

func TestCompareVersion_Equal(t *testing.T) {
	pairs := [][]string{
		{"1.0.0", "1.0.0"},
		{"v2.1.3", "2.1.3"},
		{"0.0.0", "0.0.0"},
	}
	for _, p := range pairs {
		if compareVersion(p[0], p[1]) != 0 {
			t.Errorf("compareVersion(%q, %q) should be 0", p[0], p[1])
		}
	}
}

func TestCompareVersion_AGreater(t *testing.T) {
	pairs := [][]string{
		{"2.0.0", "1.0.0"},
		{"1.1.0", "1.0.9"},
		{"1.0.1", "1.0.0"},
		{"v3.0.0", "2.9.9"},
	}
	for _, p := range pairs {
		if compareVersion(p[0], p[1]) <= 0 {
			t.Errorf("compareVersion(%q, %q) should be > 0", p[0], p[1])
		}
	}
}

func TestCompareVersion_BGreater(t *testing.T) {
	pairs := [][]string{
		{"1.0.0", "2.0.0"},
		{"1.0.9", "1.1.0"},
		{"1.0.0", "1.0.1"},
	}
	for _, p := range pairs {
		if compareVersion(p[0], p[1]) >= 0 {
			t.Errorf("compareVersion(%q, %q) should be < 0", p[0], p[1])
		}
	}
}

func TestCompareVersion_ShortPadded(t *testing.T) {
	// "4.1" < "4.1.7"
	if compareVersion("4.1", "4.1.7") >= 0 {
		t.Error("compareVersion('4.1', '4.1.7') should be < 0")
	}
	if compareVersion("5", "4.9.9") <= 0 {
		t.Error("compareVersion('5', '4.9.9') should be > 0")
	}
}

// ============================================================
// providerAllowsKeyType tests
// ============================================================

func TestProviderAllowsKeyType_AdminProxy(t *testing.T) {
	p := Provider{AccessControl: ProviderAccessControl{}}
	if !providerAllowsKeyType(p, "admin") {
		t.Error("admin key should always be allowed")
	}
	if !providerAllowsKeyType(p, "proxy") {
		t.Error("proxy key should always be allowed")
	}
}

func TestProviderAllowsKeyType_Unknown(t *testing.T) {
	p := Provider{AccessControl: ProviderAccessControl{}}
	if providerAllowsKeyType(p, "unknown") {
		t.Error("unknown key type should be denied")
	}
	if providerAllowsKeyType(p, "") {
		t.Error("empty key type should be denied")
	}
}

func TestProviderAllowsKeyType_Guest(t *testing.T) {
	// Provider with non-private keys should allow guest
	p := Provider{
		AccessControl: ProviderAccessControl{},
		APIKeys: []APIKeyConfig{
			{Enabled: true, AccessControl: "shared"},
		},
	}
	if !providerAllowsKeyType(p, "guest") {
		t.Error("guest key should be allowed when provider has non-private keys")
	}

	// Provider with only private keys should deny guest
	pPrivate := Provider{
		AccessControl: ProviderAccessControl{},
		APIKeys: []APIKeyConfig{
			{Enabled: true, AccessControl: "private"},
		},
	}
	if providerAllowsKeyType(pPrivate, "guest") {
		t.Error("guest key should be denied when all keys are private")
	}
}

// ============================================================
// hasNonPrivateKey tests
// ============================================================

func TestHasNonPrivateKey_True(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{Enabled: true, AccessControl: "shared"},
			{Enabled: true, AccessControl: "private"},
		},
	}
	if !hasNonPrivateKey(p) {
		t.Error("hasNonPrivateKey should return true when provider has a non-private key")
	}
}

func TestHasNonPrivateKey_AllPrivate(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{Enabled: true, AccessControl: "private"},
		},
	}
	if hasNonPrivateKey(p) {
		t.Error("hasNonPrivateKey should return false when all keys are private")
	}
}

func TestHasNonPrivateKey_LegacyAPIKey(t *testing.T) {
	p := Provider{APIKey: "sk-legacy-12345"}
	if !hasNonPrivateKey(p) {
		t.Error("hasNonPrivateKey should return true when legacy APIKey is set and APIKeys is empty")
	}
}

func TestHasNonPrivateKey_DisabledKey(t *testing.T) {
	p := Provider{
		APIKeys: []APIKeyConfig{
			{Enabled: false, AccessControl: "shared"},
		},
	}
	if hasNonPrivateKey(p) {
		t.Error("hasNonPrivateKey should return false when all non-private keys are disabled")
	}
}

func TestHasNonPrivateKey_Empty(t *testing.T) {
	p := Provider{}
	if hasNonPrivateKey(p) {
		t.Error("hasNonPrivateKey should return false for empty provider")
	}
}

// ============================================================
// slugify tests
// ============================================================

func TestSlugify_Simple(t *testing.T) {
	if got := slugify("My Provider"); got != "my-provider" {
		t.Errorf("slugify('My Provider') = %q, want 'my-provider'", got)
	}
}

func TestSlugify_SpecialChars(t *testing.T) {
	if got := slugify("OpenAI (GPT-4)"); got != "openai-gpt-4" {
		t.Errorf("slugify('OpenAI (GPT-4)') = %q, want 'openai-gpt-4'", got)
	}
}

func TestSlugify_DotsAndSlashes(t *testing.T) {
	if got := slugify("api.example.com/v1"); got != "apiexamplecom-v1" {
		t.Errorf("slugify('api.example.com/v1') = %q, want 'apiexamplecom-v1'", got)
	}
}

func TestSlugify_DoubleDashes(t *testing.T) {
	if got := slugify("a -- b"); got != "a-b" {
		t.Errorf("slugify('a -- b') = %q, want 'a-b'", got)
	}
}

func TestSlugify_LeadingTrailingDashes(t *testing.T) {
	if got := slugify("-hello-world-"); got != "hello-world" {
		t.Errorf("slugify('-hello-world-') = %q, want 'hello-world'", got)
	}
}

// ============================================================
// trimTrailingSlash tests
// ============================================================

func TestTrimTrailingSlash_None(t *testing.T) {
	if got := trimTrailingSlash("http://example.com"); got != "http://example.com" {
		t.Errorf("trimTrailingSlash = %q, want unchanged", got)
	}
}

func TestTrimTrailingSlash_Single(t *testing.T) {
	if got := trimTrailingSlash("http://example.com/"); got != "http://example.com" {
		t.Errorf("trimTrailingSlash = %q, want 'http://example.com'", got)
	}
}

func TestTrimTrailingSlash_Multiple(t *testing.T) {
	if got := trimTrailingSlash("http://example.com///"); got != "http://example.com" {
		t.Errorf("trimTrailingSlash = %q, want 'http://example.com'", got)
	}
}

func TestTrimTrailingSlash_Empty(t *testing.T) {
	if got := trimTrailingSlash(""); got != "" {
		t.Errorf("trimTrailingSlash('') = %q, want ''", got)
	}
}

// ============================================================
// firstNonEmpty tests
// ============================================================

func TestFirstNonEmpty_First(t *testing.T) {
	if got := firstNonEmpty("a", "b", "c"); got != "a" {
		t.Errorf("firstNonEmpty('a','b','c') = %q, want 'a'", got)
	}
}

func TestFirstNonEmpty_SkipEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "c"); got != "c" {
		t.Errorf("firstNonEmpty('','','c') = %q, want 'c'", got)
	}
}

func TestFirstNonEmpty_AllEmpty(t *testing.T) {
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("firstNonEmpty('','') = %q, want ''", got)
	}
}

func TestFirstNonEmpty_NoArgs(t *testing.T) {
	if got := firstNonEmpty(); got != "" {
		t.Errorf("firstNonEmpty() = %q, want ''", got)
	}
}

// ============================================================
// convertFinishReason tests
// ============================================================

func TestConvertFinishReason_Stop(t *testing.T) {
	if got := convertFinishReason("stop"); got != "end_turn" {
		t.Errorf("convertFinishReason('stop') = %q, want 'end_turn'", got)
	}
}

func TestConvertFinishReason_Length(t *testing.T) {
	if got := convertFinishReason("length"); got != "max_tokens" {
		t.Errorf("convertFinishReason('length') = %q, want 'max_tokens'", got)
	}
}

func TestConvertFinishReason_ToolCalls(t *testing.T) {
	if got := convertFinishReason("tool_calls"); got != "tool_use" {
		t.Errorf("convertFinishReason('tool_calls') = %q, want 'tool_use'", got)
	}
	if got := convertFinishReason("function_call"); got != "tool_use" {
		t.Errorf("convertFinishReason('function_call') = %q, want 'tool_use'", got)
	}
}

func TestConvertFinishReason_Unknown(t *testing.T) {
	// Unknown reasons default to "end_turn"
	if got := convertFinishReason("unknown"); got != "end_turn" {
		t.Errorf("convertFinishReason('unknown') = %q, want 'end_turn'", got)
	}
}

// ============================================================
// filterPlaceholder tests
// ============================================================

func TestFilterPlaceholder_Normal(t *testing.T) {
	if got := filterPlaceholder("myhost.com"); got != "myhost.com" {
		t.Errorf("filterPlaceholder('myhost.com') = %q, want 'myhost.com'", got)
	}
}

func TestFilterPlaceholder_Placeholder(t *testing.T) {
	if got := filterPlaceholder("api.example.com"); got != "" {
		t.Errorf("filterPlaceholder('api.example.com') = %q, want ''", got)
	}
	if got := filterPlaceholder("https://api.example.com"); got != "" {
		t.Errorf("filterPlaceholder('https://api.example.com') = %q, want ''", got)
	}
}

func TestFilterPlaceholder_Empty(t *testing.T) {
	if got := filterPlaceholder(""); got != "" {
		t.Errorf("filterPlaceholder('') = %q, want ''", got)
	}
}

// ============================================================
// keyTypeFromString tests
// ============================================================

func TestKeyTypeFromString_Guest(t *testing.T) {
	if got := keyTypeFromString("guest"); got != KeyTypeGuest {
		t.Errorf("keyTypeFromString('guest') = %q, want KeyTypeGuest", got)
	}
}

func TestKeyTypeFromString_ProxyAdmin(t *testing.T) {
	if got := keyTypeFromString("proxy"); got != KeyTypeProxy {
		t.Errorf("keyTypeFromString('proxy') = %q, want KeyTypeProxy", got)
	}
	if got := keyTypeFromString("admin"); got != KeyTypeProxy {
		t.Errorf("keyTypeFromString('admin') = %q, want KeyTypeProxy", got)
	}
}

func TestKeyTypeFromString_Public(t *testing.T) {
	if got := keyTypeFromString("public"); got != KeyTypePublic {
		t.Errorf("keyTypeFromString('public') = %q, want KeyTypePublic", got)
	}
}

func TestKeyTypeFromString_Unknown(t *testing.T) {
	if got := keyTypeFromString(""); got != KeyTypeUnknown {
		t.Errorf("keyTypeFromString('') = %q, want KeyTypeUnknown", got)
	}
	if got := keyTypeFromString("bogus"); got != KeyTypeUnknown {
		t.Errorf("keyTypeFromString('bogus') = %q, want KeyTypeUnknown", got)
	}
}

// ============================================================
// jsonBody tests
// ============================================================

func TestJSONBody_Simple(t *testing.T) {
	r := jsonBody(map[string]string{"hello": "world"})
	if r == nil {
		t.Fatal("jsonBody returned nil reader")
	}
}

func TestJSONBody_ProducesValidJSON(t *testing.T) {
	var buf bytes.Buffer
	r := jsonBody(map[string]any{"key": "value", "num": 42})
	buf.ReadFrom(r)
	if !json.Valid(buf.Bytes()) {
		t.Error("jsonBody should produce valid JSON")
	}
}

// ============================================================
// truncate (string) tests
// ============================================================

func TestTruncate_Short(t *testing.T) {
	if got := truncate("hi", 10); got != "hi" {
		t.Errorf("truncate('hi', 10) = %q, want 'hi'", got)
	}
}

func TestTruncate_Long(t *testing.T) {
	if got := truncate("hello world", 5); got != "hello" {
		t.Errorf("truncate('hello world', 5) = %q, want 'hello'", got)
	}
}

func TestTruncate_Zero(t *testing.T) {
	if got := truncate("hello", 0); got != "" {
		t.Errorf("truncate('hello', 0) = %q, want ''", got)
	}
}

func TestTruncate_Exact(t *testing.T) {
	if got := truncate("abc", 3); got != "abc" {
		t.Errorf("truncate('abc', 3) = %q, want 'abc'", got)
	}
}

// ============================================================
// strPtr tests
// ============================================================

func TestStrPtr_Deref(t *testing.T) {
	p := strPtr("test")
	if p == nil {
		t.Fatal("strPtr returned nil")
	}
	if *p != "test" {
		t.Errorf("*strPtr('test') = %q, want 'test'", *p)
	}
}

func TestStrPtr_Empty(t *testing.T) {
	p := strPtr("")
	if p == nil {
		t.Fatal("strPtr returned nil")
	}
	if *p != "" {
		t.Errorf("*strPtr('') = %q, want ''", *p)
	}
}

// ============================================================
// parseRPM tests
// ============================================================

func TestParseRPM_Normal(t *testing.T) {
	if got := parseRPM("30 RPM, 1,000 RPD"); got != 30 {
		t.Errorf("parseRPM = %d, want 30", got)
	}
}

func TestParseRPM_NoComma(t *testing.T) {
	if got := parseRPM("500RPM"); got != 500 {
		t.Errorf("parseRPM('500RPM') = %d, want 500", got)
	}
}

func TestParseRPM_NoRPM(t *testing.T) {
	if got := parseRPM("1,000 RPD"); got != 0 {
		t.Errorf("parseRPM('1,000 RPD') = %d, want 0", got)
	}
}

func TestParseRPM_Empty(t *testing.T) {
	if got := parseRPM(""); got != 0 {
		t.Errorf("parseRPM('') = %d, want 0", got)
	}
}

// ============================================================
// toUpper tests
// ============================================================

func TestToUpper_Lowercase(t *testing.T) {
	if got := toUpper("hello"); got != "HELLO" {
		t.Errorf("toUpper('hello') = %q, want 'HELLO'", got)
	}
}

func TestToUpper_Mixed(t *testing.T) {
	if got := toUpper("Hello World"); got != "HELLO WORLD" {
		t.Errorf("toUpper('Hello World') = %q, want 'HELLO WORLD'", got)
	}
}

func TestToUpper_AlreadyUpper(t *testing.T) {
	if got := toUpper("HELLO"); got != "HELLO" {
		t.Errorf("toUpper('HELLO') = %q, want 'HELLO'", got)
	}
}

func TestToUpper_Empty(t *testing.T) {
	if got := toUpper(""); got != "" {
		t.Errorf("toUpper('') = %q, want ''", got)
	}
}

// ============================================================
// mustMarshalJSON tests
// ============================================================

func TestMustMarshalJSON_Map(t *testing.T) {
	data := mustMarshalJSON(map[string]int{"a": 1})
	if !json.Valid(data) {
		t.Error("mustMarshalJSON should produce valid JSON")
	}
}

func TestMustMarshalJSON_String(t *testing.T) {
	data := mustMarshalJSON("hello")
	// JSON string is "hello" with quotes
	if string(data) != "\"hello\"" {
		t.Errorf("mustMarshalJSON('hello') = %s, want \"hello\"", data)
	}
}

func TestMustMarshalJSON_Nil(t *testing.T) {
	data := mustMarshalJSON(nil)
	if string(data) != "null" {
		t.Errorf("mustMarshalJSON(nil) = %s, want null", data)
	}
}

// ============================================================
// maskToken tests
// ============================================================

func TestMaskToken_Short(t *testing.T) {
	if got := maskToken("sk-abc123"); got != "***" {
		t.Errorf("maskToken('sk-abc123') = %q, want '***'", got)
	}
}

func TestMaskToken_Long(t *testing.T) {
	if got := maskToken("sk-1234567890abcdef1234567890"); got != "sk-123...7890" {
		t.Errorf("maskToken('sk-1234567890abcdef1234567890') = %q, want 'sk-123...7890'", got)
	}
}

func TestMaskToken_Exactly12(t *testing.T) {
	s := "123456789012"
	if got := maskToken(s); got != "123456...9012" {
		t.Errorf("maskToken(%q) = %q, want '123456...9012'", s, got)
	}
}

// ============================================================
// sha256Hex tests
// ============================================================

func TestSHA256Hex_Known(t *testing.T) {
	// SHA-256 of empty string is a known constant
	got := sha256Hex([]byte{})
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("sha256Hex([]) = %q, want %q", got, want)
	}
}

func TestSHA256Hex_NonEmpty(t *testing.T) {
	got := sha256Hex([]byte("hello"))
	if len(got) != 64 {
		t.Errorf("sha256Hex should produce 64 hex chars, got %d", len(got))
	}
}

// ============================================================
// pickBestAddress tests
// ============================================================

func TestPickBestAddress_Empty(t *testing.T) {
	if got := pickBestAddress([]string{}); got != "" {
		t.Errorf("pickBestAddress([]) = %q, want ''", got)
	}
	if got := pickBestAddress(nil); got != "" {
		t.Errorf("pickBestAddress(nil) = %q, want ''", got)
	}
}

func TestPickBestAddress_CustomDomain(t *testing.T) {
	addrs := []string{"http://localhost:8000", "https://my.example.com"}
	if got := pickBestAddress(addrs); got != "https://my.example.com" {
		t.Errorf("pickBestAddress = %q, want 'https://my.example.com'", got)
	}
}

func TestPickBestAddress_TunnelPreferredOverLocal(t *testing.T) {
	addrs := []string{"http://localhost:8000", "https://xxx-xxx.trycloudflare.com"}
	if got := pickBestAddress(addrs); got != "https://xxx-xxx.trycloudflare.com" {
		t.Errorf("pickBestAddress = %q, want 'https://xxx-xxx.trycloudflare.com'", got)
	}
}

func TestPickBestAddress_LocalFallback(t *testing.T) {
	addrs := []string{"http://localhost:8000", "http://127.0.0.1:9000"}
	if got := pickBestAddress(addrs); got != "http://localhost:8000" {
		t.Errorf("pickBestAddress = %q, want 'http://localhost:8000'", got)
	}
}

func TestPickBestAddress_FallbackToFirst(t *testing.T) {
	// No custom domain, no tunnel, no localhost — fallback to first
	addrs := []string{"http://10.0.0.1:8080", "http://10.0.0.2:8080"}
	if got := pickBestAddress(addrs); got != "http://10.0.0.1:8080" {
		t.Errorf("pickBestAddress = %q, want 'http://10.0.0.1:8080'", got)
	}
}

// ============================================================
// PriorityOrder tests
// ============================================================

func TestPriorityOrder_Guest(t *testing.T) {
	order := PriorityOrder(KeyTypeGuest)
	if len(order) != 3 {
		t.Fatalf("expected 3 pools, got %d", len(order))
	}
	if order[0] != PoolPrivate || order[1] != PoolShared || order[2] != PoolRemoteShared {
		t.Errorf("Guest order wrong: %v", order)
	}
}

func TestPriorityOrder_Proxy(t *testing.T) {
	order := PriorityOrder(KeyTypeProxy)
	if len(order) != 3 || order[0] != PoolPrivate {
		t.Errorf("Proxy order wrong: %v", order)
	}
}

func TestPriorityOrder_Public(t *testing.T) {
	order := PriorityOrder(KeyTypePublic)
	if len(order) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(order))
	}
	if order[0] != PoolShared || order[1] != PoolRemoteShared {
		t.Errorf("Public order wrong: %v", order)
	}
}

func TestPriorityOrder_Unknown(t *testing.T) {
	order := PriorityOrder(KeyTypeUnknown)
	if len(order) != 3 || order[0] != PoolPrivate {
		t.Errorf("Unknown order wrong: %v", order)
	}
}

// ============================================================
// parseInt64Config tests
// ============================================================

func TestParseInt64Config_Empty(t *testing.T) {
	if got := parseInt64Config(""); got != -1 {
		t.Errorf("parseInt64Config('') = %d, want -1", got)
	}
}

func TestParseInt64Config_Valid(t *testing.T) {
	if got := parseInt64Config("1000"); got != 1000 {
		t.Errorf("parseInt64Config('1000') = %d, want 1000", got)
	}
}

func TestParseInt64Config_Zero(t *testing.T) {
	if got := parseInt64Config("0"); got != 0 {
		t.Errorf("parseInt64Config('0') = %d, want 0", got)
	}
}

func TestParseInt64Config_Negative(t *testing.T) {
	if got := parseInt64Config("-5"); got != -1 {
		t.Errorf("parseInt64Config('-5') = %d, want -1", got)
	}
}

func TestParseInt64Config_Invalid(t *testing.T) {
	if got := parseInt64Config("abc"); got != -1 {
		t.Errorf("parseInt64Config('abc') = %d, want -1", got)
	}
}

// ============================================================
// parseFloat64 tests
// ============================================================

func TestParseFloat64_Valid(t *testing.T) {
	if got := parseFloat64("3.14", 1.0); got != 3.14 {
		t.Errorf("parseFloat64('3.14', 1.0) = %f, want 3.14", got)
	}
}

func TestParseFloat64_Default(t *testing.T) {
	if got := parseFloat64("", 5.0); got != 5.0 {
		t.Errorf("parseFloat64('', 5.0) = %f, want 5.0", got)
	}
}

func TestParseFloat64_Invalid(t *testing.T) {
	if got := parseFloat64("abc", 10.0); got != 10.0 {
		t.Errorf("parseFloat64('abc', 10.0) = %f, want 10.0", got)
	}
}

func TestParseFloat64_ZeroOrNegative(t *testing.T) {
	if got := parseFloat64("0", 5.0); got != 5.0 {
		t.Errorf("parseFloat64('0', 5.0) = %f, want 5.0", got)
	}
	if got := parseFloat64("-1.5", 5.0); got != 5.0 {
		t.Errorf("parseFloat64('-1.5', 5.0) = %f, want 5.0", got)
	}
}

// ============================================================
// generateReqID tests
// ============================================================

func TestGenerateReqID_Format(t *testing.T) {
	id := generateReqID()
	if id == "" {
		t.Fatal("generateReqID returned empty string")
	}
	if len(id) < 5 {
		t.Errorf("generateReqID = %q, too short", id)
	}
}

func TestGenerateReqID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		id := generateReqID()
		if seen[id] {
			t.Errorf("duplicate generateReqID: %q", id)
		}
		seen[id] = true
	}
}

// ============================================================
// base58Encode tests
// ============================================================

func TestBase58Encode_Empty(t *testing.T) {
	if got := base58Encode([]byte{}); got != "" {
		t.Errorf("base58Encode([]) = %q, want ''", got)
	}
}

func TestBase58Encode_Single(t *testing.T) {
	got := base58Encode([]byte{0})
	if got != "11" {
		t.Errorf("base58Encode([0]) = %q, want '11'", got)
	}
}

func TestBase58Encode_Known(t *testing.T) {
	// Test vector: empty string SHA256 = "e3b0c442..."
	got := base58Encode([]byte("test"))
	if len(got) == 0 {
		t.Fatal("base58Encode returned empty for non-empty input")
	}
	// Encode is deterministic
	got2 := base58Encode([]byte("test"))
	if got != got2 {
		t.Errorf("base58Encode should be deterministic: %q vs %q", got, got2)
	}
}

func TestBase58Encode_LeadingZeros(t *testing.T) {
	got := base58Encode([]byte{0, 0, 1})
	if got[0] != '1' || got[1] != '1' {
		t.Errorf("base58Encode([0,0,1]) = %q, should start with '11'", got)
	}
}

// ============================================================
// PoolKind.String() tests
// ============================================================

func TestPoolKind_String_Unknown(t *testing.T) {
	if got := PoolKind(99).String(); got != "unknown" {
		t.Errorf("PoolKind(99).String() = %q, want 'unknown'", got)
	}
}

// ============================================================
// isValidDomain tests
// ============================================================

func TestIsValidDomain_Valid(t *testing.T) {
	for _, d := range []string{"example.com", "sub.example.com", "my-host.example.co.uk"} {
		if !isValidDomain(d) {
			t.Errorf("isValidDomain(%q) should be true", d)
		}
	}
}

func TestIsValidDomain_Invalid(t *testing.T) {
	for _, d := range []string{"", "localhost", "notadomain", "a", "has space.com", "under_score.com"} {
		if isValidDomain(d) {
			t.Errorf("isValidDomain(%q) should be false", d)
		}
	}
}

func TestIsValidDomain_SinglePart(t *testing.T) {
	if isValidDomain("com") {
		t.Error("single-part domain should be invalid")
	}
}

// ============================================================
// firstAddress tests
// ============================================================

func TestFirstAddress_NonEmpty(t *testing.T) {
	if got := firstAddress([]string{"a", "b"}); got != "a" {
		t.Errorf("firstAddress = %q, want 'a'", got)
	}
}

func TestFirstAddress_Empty(t *testing.T) {
	if got := firstAddress([]string{}); got != "" {
		t.Errorf("firstAddress([]) = %q, want ''", got)
	}
	if got := firstAddress(nil); got != "" {
		t.Errorf("firstAddress(nil) = %q, want ''", got)
	}
}

// ============================================================
// webSessionFormatMessages tests
// ============================================================

func TestWebSessionFormatMessages_System(t *testing.T) {
	msgs := []ChatMessage{{Role: "system", Content: "Be helpful"}}
	got := webSessionFormatMessages(msgs, "", "\n")
	if !strings.Contains(got, "[System Instructions]") {
		t.Errorf("expected system prefix, got %q", got)
	}
}

func TestWebSessionFormatMessages_Assistant(t *testing.T) {
	msgs := []ChatMessage{{Role: "assistant", Content: "Hello!"}}
	got := webSessionFormatMessages(msgs, "", "\n")
	if !strings.Contains(got, "[Assistant]: ") {
		t.Errorf("expected assistant prefix, got %q", got)
	}
}

func TestWebSessionFormatMessages_User(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: "Hi"}}
	got := webSessionFormatMessages(msgs, "", "\n")
	if !strings.Contains(got, "[User]: ") {
		t.Errorf("expected user prefix, got %q", got)
	}
}

func TestWebSessionFormatMessages_Separator(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello"},
	}
	got := webSessionFormatMessages(msgs, "", "---")
	if !strings.Contains(got, "---") {
		t.Errorf("expected separator '---', got %q", got)
	}
}

func TestWebSessionFormatMessages_DefaultSeparator(t *testing.T) {
	msgs := []ChatMessage{{Role: "user", Content: "Hi"}, {Role: "assistant", Content: "Hey"}}
	got := webSessionFormatMessages(msgs, "", "")
	if !strings.Contains(got, "\n") {
		t.Errorf("expected default newline separator, got %q", got)
	}
}

// ============================================================
// jsonEncodePool tests
// ============================================================

func TestJsonEncodePool_Map(t *testing.T) {
	data, err := jsonEncodePool(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("jsonEncodePool error: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("jsonEncodePool produced invalid JSON: %s", data)
	}
}

func TestJsonEncodePool_Nil(t *testing.T) {
	data, err := jsonEncodePool(nil)
	if err != nil {
		t.Fatalf("jsonEncodePool error: %v", err)
	}
	if string(data) != "null\n" {
		t.Errorf("jsonEncodePool(nil) = %q, want 'null\\n'", data)
	}
}

// ============================================================
// keyAllowedForAccess tests
// ============================================================

func TestKeyAllowedForAccess_Public(t *testing.T) {
	if !keyAllowedForAccess("public", "shared") {
		t.Error("public key should allow any access type")
	}
	if !keyAllowedForAccess("public", "private") {
		t.Error("public key should allow private access")
	}
	if !keyAllowedForAccess("public", "") {
		t.Error("public key should allow empty access type")
	}
}

func TestKeyAllowedForAccess_Shared(t *testing.T) {
	if !keyAllowedForAccess("shared", "shared") {
		t.Error("shared key should allow shared access")
	}
	if !keyAllowedForAccess("shared", "private") {
		t.Error("shared key should allow private access")
	}
	if !keyAllowedForAccess("shared", "") {
		t.Error("shared key should allow empty access type")
	}
	if keyAllowedForAccess("shared", "public") {
		t.Error("shared key should NOT allow public access")
	}
}

func TestKeyAllowedForAccess_Private(t *testing.T) {
	if !keyAllowedForAccess("private", "private") {
		t.Error("private key should allow private access")
	}
	if !keyAllowedForAccess("private", "") {
		t.Error("private key should allow empty access type")
	}
	if keyAllowedForAccess("private", "shared") {
		t.Error("private key should NOT allow shared access")
	}
	if keyAllowedForAccess("private", "public") {
		t.Error("private key should NOT allow public access")
	}
}

func TestKeyAllowedForAccess_Unknown(t *testing.T) {
	if keyAllowedForAccess("unknown", "private") {
		t.Error("unknown key type should be denied")
	}
	if keyAllowedForAccess("", "private") {
		t.Error("empty key type should be denied")
	}
}

// ============================================================
// mapAwesomeProvider tests
// ============================================================

func TestMapAwesomeProvider_Skipped(t *testing.T) {
	for _, name := range []string{"Cohere", "Ollama Cloud", "Cloudflare Workers AI"} {
		_, _, _, _, skip := mapAwesomeProvider(awesomeProvider{Name: name})
		if !skip {
			t.Errorf("mapAwesomeProvider(%q) should be skipped", name)
		}
	}
}

func TestMapAwesomeProvider_Valid(t *testing.T) {
	ap := awesomeProvider{
		Name:    "Test Provider",
		BaseURL: "https://api.test.com/v1",
		Models: []awesomeModel{
			{ID: "model-1", Name: "Model One"},
			{ID: "model-2", Name: "Model Two"},
		},
	}
	pid, models, _, _, skip := mapAwesomeProvider(ap)
	if skip {
		t.Error("valid provider should not be skipped")
	}
	if pid != "free-test-provider" {
		t.Errorf("providerID = %q, want 'free-test-provider'", pid)
	}
	if len(models) != 2 {
		t.Errorf("expected 2 models, got %d", len(models))
	}
}

func TestMapAwesomeProvider_NullModels(t *testing.T) {
	ap := awesomeProvider{
		Name:    "Null Models",
		BaseURL: "https://api.test.com/v1",
		Models: []awesomeModel{
			{ID: "null", Name: "Null"},
			{ID: "", Name: "Empty"},
		},
	}
	_, _, _, _, skip := mapAwesomeProvider(ap)
	if !skip {
		t.Error("provider with only null/empty models should be skipped")
	}
}

func TestMapAwesomeProvider_OVHcloud(t *testing.T) {
	ap := awesomeProvider{
		Name:    "OVHcloud AI Endpoints",
		BaseURL: "https://api.ovh.com/v1",
		Models:  []awesomeModel{{ID: "ovh-model", Name: "OVH"}},
	}
	_, _, anonymous, _, skip := mapAwesomeProvider(ap)
	if skip {
		t.Error("OVHcloud should not be skipped")
	}
	if !anonymous {
		t.Error("OVHcloud should be anonymous")
	}
}

func TestMapAwesomeProvider_GoogleGemini(t *testing.T) {
	ap := awesomeProvider{
		Name:    "Google Gemini",
		BaseURL: "https://generativelanguage.googleapis.com/v1",
		Models:  []awesomeModel{{ID: "gemini-pro", Name: "Gemini Pro"}},
	}
	_, _, _, baseURL, skip := mapAwesomeProvider(ap)
	if skip {
		t.Error("Google Gemini should not be skipped")
	}
	if baseURL != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Errorf("Google Gemini baseURL = %q, want openai-compatible endpoint", baseURL)
	}
}

// ============================================================
// QuotaPool tests
// ============================================================

func TestQuotaPool_CanDeduct_Unlimited(t *testing.T) {
	p := &QuotaPool{Balance: -1}
	if !p.CanDeduct(1000) {
		t.Error("unlimited pool should always allow deduction")
	}
}

func TestQuotaPool_CanDeduct_Sufficient(t *testing.T) {
	p := &QuotaPool{Balance: 100}
	if !p.CanDeduct(50) {
		t.Error("pool with 100 should allow deduction of 50")
	}
}

func TestQuotaPool_CanDeduct_Insufficient(t *testing.T) {
	p := &QuotaPool{Balance: 10}
	if p.CanDeduct(50) {
		t.Error("pool with 10 should not allow deduction of 50")
	}
}

func TestQuotaPool_CanDeduct_Exact(t *testing.T) {
	p := &QuotaPool{Balance: 50}
	if !p.CanDeduct(50) {
		t.Error("pool with 50 should allow deduction of 50")
	}
}

func TestQuotaPool_Deduct_Unlimited(t *testing.T) {
	p := &QuotaPool{Balance: -1}
	if !p.Deduct(100) {
		t.Error("unlimited pool deduction should succeed")
	}
	if p.Balance != -1 {
		t.Error("unlimited pool balance should remain -1")
	}
}

func TestQuotaPool_Deduct_Success(t *testing.T) {
	p := &QuotaPool{Balance: 100}
	if !p.Deduct(30) {
		t.Error("deduction of 30 from 100 should succeed")
	}
	if p.Remaining() != 70 {
		t.Errorf("remaining = %d, want 70", p.Remaining())
	}
}

func TestQuotaPool_Deduct_Fail(t *testing.T) {
	p := &QuotaPool{Balance: 10}
	if p.Deduct(50) {
		t.Error("deduction of 50 from 10 should fail")
	}
	if p.Remaining() != 10 {
		t.Errorf("remaining should be unchanged, got %d", p.Remaining())
	}
}

func TestQuotaPool_Remaining(t *testing.T) {
	p := &QuotaPool{Balance: 42}
	if p.Remaining() != 42 {
		t.Errorf("Remaining = %d, want 42", p.Remaining())
	}
}

// ============================================================
// ConsumeWithPriority tests
// ============================================================

func TestConsumeWithPriority_PrivateFirst(t *testing.T) {
	pools := map[PoolKind]*QuotaPool{
		PoolPrivate: {Kind: PoolPrivate, NodeID: "self", Balance: 100},
		PoolShared:  {Kind: PoolShared, NodeID: "self", Balance: 100},
	}
	res := ConsumeWithPriority(KeyTypeGuest, 50, pools)
	if !res.OK {
		t.Fatal("ConsumeWithPriority should succeed")
	}
	if res.Kind != PoolPrivate {
		t.Errorf("expected PoolPrivate, got %v", res.Kind)
	}
}

func TestConsumeWithPriority_FallbackToShared(t *testing.T) {
	pools := map[PoolKind]*QuotaPool{
		PoolShared: {Kind: PoolShared, NodeID: "self", Balance: 100},
	}
	res := ConsumeWithPriority(KeyTypeGuest, 50, pools)
	if !res.OK {
		t.Fatal("ConsumeWithPriority should succeed with shared pool")
	}
	if res.Kind != PoolShared {
		t.Errorf("expected PoolShared, got %v", res.Kind)
	}
}

func TestConsumeWithPriority_PublicSkipsPrivate(t *testing.T) {
	pools := map[PoolKind]*QuotaPool{
		PoolPrivate: {Kind: PoolPrivate, NodeID: "self", Balance: 100},
		PoolShared:  {Kind: PoolShared, NodeID: "self", Balance: 100},
	}
	res := ConsumeWithPriority(KeyTypePublic, 50, pools)
	if !res.OK {
		t.Fatal("ConsumeWithPriority should succeed")
	}
	if res.Kind != PoolShared {
		t.Errorf("public key should not use private pool, got %v", res.Kind)
	}
}

func TestConsumeWithPriority_NoPoolAvailable(t *testing.T) {
	pools := map[PoolKind]*QuotaPool{
		PoolPrivate: {Kind: PoolPrivate, NodeID: "self", Balance: 5},
	}
	res := ConsumeWithPriority(KeyTypeGuest, 50, pools)
	if res.OK {
		t.Error("should fail when no pool has sufficient balance")
	}
}

func TestConsumeWithPriority_EmptyPools(t *testing.T) {
	res := ConsumeWithPriority(KeyTypeGuest, 50, nil)
	if res.OK {
		t.Error("should fail with nil pools map")
	}
}

func TestConsumeWithPriority_NilPool(t *testing.T) {
	pools := map[PoolKind]*QuotaPool{
		PoolPrivate: nil,
	}
	res := ConsumeWithPriority(KeyTypeGuest, 50, pools)
	if res.OK {
		t.Error("should fail when pool is nil")
	}
}

// ============================================================
// DefaultBalanceConfig tests
// ============================================================

func TestDefaultBalanceConfig(t *testing.T) {
	cfg := DefaultBalanceConfig()
	if cfg.TargetRatio != 1.0 {
		t.Errorf("TargetRatio = %f, want 1.0", cfg.TargetRatio)
	}
	if cfg.UnderConsumerThreshold != 0.5 {
		t.Errorf("UnderConsumerThreshold = %f, want 0.5", cfg.UnderConsumerThreshold)
	}
	if cfg.OverContributorThreshold != 3.0 {
		t.Errorf("OverContributorThreshold = %f, want 3.0", cfg.OverContributorThreshold)
	}
	if !cfg.QuotaAdjustment || !cfg.PriorityAdjustment || !cfg.RoutingPreference {
		t.Error("all adjustment flags should be true by default")
	}
}

// ============================================================
// DefaultLBConfig tests
// ============================================================

func TestDefaultLBConfig(t *testing.T) {
	cfg := DefaultLBConfig()
	if cfg.TrustWeight+cfg.ReputationWeight+cfg.LatencyWeight+cfg.AvailabilityWeight+cfg.ContributionWeight != 1.0 {
		t.Error("LB weights should sum to 1.0")
	}
	if cfg.HealthCheckInterval != 30*time.Second {
		t.Errorf("HealthCheckInterval = %v, want 30s", cfg.HealthCheckInterval)
	}
	if cfg.MetricsWindow != 20 {
		t.Errorf("MetricsWindow = %d, want 20", cfg.MetricsWindow)
	}
}

// ============================================================
// NewLoadBalancer tests
// ============================================================

func TestNewLoadBalancer(t *testing.T) {
	cfg := DefaultLBConfig()
	lb := NewLoadBalancer(cfg)
	if lb == nil {
		t.Fatal("NewLoadBalancer returned nil")
	}
	if lb.config.TrustWeight != cfg.TrustWeight {
		t.Error("config not set correctly")
	}
}

// ============================================================
// RateLimiter tests
// ============================================================

func TestNewRateLimiter_BlockAll(t *testing.T) {
	rl := NewRateLimiter(0)
	if rl.Allow() {
		t.Error("zero QPS limiter should block all requests")
	}
}

func TestNewRateLimiter_Negative(t *testing.T) {
	rl := NewRateLimiter(-1)
	if rl.Allow() {
		t.Error("negative QPS limiter should block all requests")
	}
}

func TestNewRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(10)
	if !rl.Allow() {
		t.Error("new limiter with 10 QPS should allow first request")
	}
}

func TestNewRateLimiterWithBurst(t *testing.T) {
	rl := NewRateLimiterWithBurst(10, 5)
	if rl.maxTokens != 5 {
		t.Errorf("maxTokens = %f, want 5", rl.maxTokens)
	}
	if rl.tokens != 5 {
		t.Errorf("initial tokens = %f, want 5", rl.tokens)
	}
}

func TestNewRateLimiterWithBurst_MinBurst(t *testing.T) {
	rl := NewRateLimiterWithBurst(10, 0.5)
	if rl.maxTokens < 1.0 {
		t.Errorf("burst should be at least 1.0, got %f", rl.maxTokens)
	}
}

func TestRateLimiter_Exhaust(t *testing.T) {
	rl := NewRateLimiterWithBurst(100, 3)
	passed := 0
	for i := 0; i < 10; i++ {
		if rl.Allow() {
			passed++
		}
	}
	if passed != 3 {
		t.Errorf("expected 3 passes from burst=3, got %d", passed)
	}
}

// ============================================================
// simpleError.Error tests
// ============================================================

func TestSimpleError_Error(t *testing.T) {
	err := &simpleError{msg: "test error"}
	if err.Error() != "test error" {
		t.Errorf("simpleError.Error() = %q, want 'test error'", err.Error())
	}
}

// ============================================================
// ReputationManager.calculateGradeLocked tests
// ============================================================

func TestCalculateGrade_S(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	rep := &NodeReputation{OverallScore: 97}
	if got := rm.calculateGradeLocked(rep); got != "S" {
		t.Errorf("grade for 97 = %q, want 'S'", got)
	}
}

func TestCalculateGrade_A(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	rep := &NodeReputation{OverallScore: 85}
	if got := rm.calculateGradeLocked(rep); got != "A" {
		t.Errorf("grade for 85 = %q, want 'A'", got)
	}
}

func TestCalculateGrade_B(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	rep := &NodeReputation{OverallScore: 65}
	if got := rm.calculateGradeLocked(rep); got != "B" {
		t.Errorf("grade for 65 = %q, want 'B'", got)
	}
}

func TestCalculateGrade_C(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	rep := &NodeReputation{OverallScore: 45}
	if got := rm.calculateGradeLocked(rep); got != "C" {
		t.Errorf("grade for 45 = %q, want 'C'", got)
	}
}

func TestCalculateGrade_D(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	rep := &NodeReputation{OverallScore: 20}
	if got := rm.calculateGradeLocked(rep); got != "D" {
		t.Errorf("grade for 20 = %q, want 'D'", got)
	}
}

func TestCalculateGrade_Boundaries(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	cases := []struct {
		score int
		want  string
	}{
		{95, "S"}, {80, "A"}, {60, "B"}, {40, "C"}, {39, "D"}, {0, "D"},
	}
	for _, c := range cases {
		rep := &NodeReputation{OverallScore: float64(c.score)}
		if got := rm.calculateGradeLocked(rep); got != c.want {
			t.Errorf("grade for %d = %q, want %q", c.score, got, c.want)
		}
	}
}

// ============================================================
// ReputationManager.ShouldRemoveNode tests
// ============================================================

func TestShouldRemoveNode_DGradeLong(t *testing.T) {
	rm := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
	}
	past := time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	rm.scores["bad-node"] = &NodeReputation{
		NodeID:      "bad-node",
		Grade:       "D",
		DGradeSince: past,
	}
	if !rm.ShouldRemoveNode("bad-node") {
		t.Error("node in D grade for 8 days should be removed")
	}
}

func TestShouldRemoveNode_DGradeShort(t *testing.T) {
	rm := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
	}
	recent := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	rm.scores["ok-node"] = &NodeReputation{
		NodeID:      "ok-node",
		Grade:       "D",
		DGradeSince: recent,
	}
	if rm.ShouldRemoveNode("ok-node") {
		t.Error("node in D grade for only 1h should not be removed yet")
	}
}

func TestShouldRemoveNode_GoodGrade(t *testing.T) {
	rm := &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
	}
	rm.scores["good-node"] = &NodeReputation{
		NodeID: "good-node",
		Grade:  "A",
	}
	if rm.ShouldRemoveNode("good-node") {
		t.Error("A-grade node should not be removed")
	}
}

// ============================================================
// ReputationManager.calculateOverallScoreLocked tests
// ============================================================

func TestCalculateOverallScore_NoPeerScores(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	rep := &NodeReputation{Availability: 100, Latency: 80, Accuracy: 90}
	score := rm.calculateOverallScoreLocked(rep)
	// 0.4*100 + 0.3*80 + 0.2*90 + 0.1*0 = 40 + 24 + 18 + 0 = 82
	if score != 82.0 {
		t.Errorf("overall score = %f, want 82.0", score)
	}
}

func TestCalculateOverallScore_WithPeerScores(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	rep := &NodeReputation{
		Availability: 80, Latency: 70, Accuracy: 60,
		PeerScores: []PeerScore{
			{Availability: 90},
			{Availability: 70},
		},
	}
	score := rm.calculateOverallScoreLocked(rep)
	// 0.4*80 + 0.3*70 + 0.2*60 + 0.1*80 = 32 + 21 + 12 + 8 = 73
	if score != 73.0 {
		t.Errorf("overall score = %f, want 73.0", score)
	}
}

func TestCalculateOverallScore_ZeroValues(t *testing.T) {
	rm := &ReputationManager{scores: make(map[string]*NodeReputation)}
	rep := &NodeReputation{}
	score := rm.calculateOverallScoreLocked(rep)
	if score != 0.0 {
		t.Errorf("overall score = %f, want 0.0", score)
	}
}

// ============================================================
// WAFEngine.CheckContent tests
// ============================================================

func TestWAFEngine_CheckContent_Disabled(t *testing.T) {
	e := NewWAFEngine()
	ok, _ := e.CheckContent("anything")
	if !ok {
		t.Error("disabled WAF should allow all content")
	}
}

func TestWAFEngine_CheckContent_Enabled_Pass(t *testing.T) {
	e := NewWAFEngine()
	e.enabled = true
	e.contentKw = []string{"badword", "forbidden"}
	ok, kw := e.CheckContent("hello world")
	if !ok {
		t.Errorf("clean content should pass, blocked by %q", kw)
	}
}

func TestWAFEngine_CheckContent_Enabled_Blocked(t *testing.T) {
	e := NewWAFEngine()
	e.enabled = true
	e.contentKw = []string{"badword", "forbidden"}
	ok, kw := e.CheckContent("this has a badword in it")
	if ok {
		t.Error("content with blocked keyword should be rejected")
	}
	if kw != "badword" {
		t.Errorf("blocked keyword = %q, want 'badword'", kw)
	}
}

func TestWAFEngine_CheckContent_CaseInsensitive(t *testing.T) {
	e := NewWAFEngine()
	e.enabled = true
	e.contentKw = []string{"BadWord"}
	ok, _ := e.CheckContent("BADWORD is here")
	if ok {
		t.Error("check should be case insensitive")
	}
}

// ============================================================
// BalanceEngine.CalculateAdjustment tests
// ============================================================

func TestCalculateAdjustment_UnknownNode(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: make(map[string]*NodeBalance),
		adjustments: make(map[string]*BalanceAdjustment),
		config:      DefaultBalanceConfig(),
	}
	adj := be.CalculateAdjustment("unknown")
	if adj.Type != "balanced" {
		t.Errorf("unknown node type = %q, want 'balanced'", adj.Type)
	}
	if adj.RoutingWeightMultiplier != 1.0 {
		t.Errorf("unknown node multiplier = %f, want 1.0", adj.RoutingWeightMultiplier)
	}
}

func TestCalculateAdjustment_Balanced(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {Balance: 1.5},
		},
		adjustments: make(map[string]*BalanceAdjustment),
		config:      DefaultBalanceConfig(),
	}
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "balanced" {
		t.Errorf("balanced node type = %q, want 'balanced'", adj.Type)
	}
	if adj.PriorityDelta != 0 {
		t.Errorf("balanced node priority delta = %d, want 0", adj.PriorityDelta)
	}
}

func TestCalculateAdjustment_UnderConsumer(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {Balance: 0.3},
		},
		adjustments: make(map[string]*BalanceAdjustment),
		config:      DefaultBalanceConfig(),
	}
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "reduce_priority" {
		t.Errorf("under-consumer type = %q, want 'reduce_priority'", adj.Type)
	}
	if adj.PriorityDelta != -1 {
		t.Errorf("under-consumer priority delta = %d, want -1", adj.PriorityDelta)
	}
}

func TestCalculateAdjustment_OverContributor(t *testing.T) {
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {Balance: 4.0},
		},
		adjustments: make(map[string]*BalanceAdjustment),
		config:      DefaultBalanceConfig(),
	}
	adj := be.CalculateAdjustment("n1")
	if adj.Type != "boost_priority" {
		t.Errorf("over-contributor type = %q, want 'boost_priority'", adj.Type)
	}
	if adj.PriorityDelta != 1 {
		t.Errorf("over-contributor priority delta = %d, want 1", adj.PriorityDelta)
	}
}

func TestCalculateAdjustment_UnderConsumerFloor(t *testing.T) {
	cfg := DefaultBalanceConfig()
	cfg.AdjustmentStrength = 1.0
	be := &BalanceEngine{
		nodeBalance: map[string]*NodeBalance{
			"n1": {Balance: 0.01},
		},
		adjustments: make(map[string]*BalanceAdjustment),
		config:      cfg,
	}
	adj := be.CalculateAdjustment("n1")
	if adj.RoutingWeightMultiplier > 0.1+0.001 {
		t.Errorf("under-consumer floor multiplier = %f, should be near 0.1", adj.RoutingWeightMultiplier)
	}
}

// ============================================================
// migrateProviderKeys tests
// ============================================================

func TestMigrateProviderKeys_Migration(t *testing.T) {
	p := &Provider{APIKey: "sk-legacy", APIKeys: nil}
	if !migrateProviderKeys(p) {
		t.Error("should have migrated")
	}
	if p.APIKey != "" {
		t.Error("legacy key should be cleared")
	}
	if len(p.APIKeys) != 1 {
		t.Fatalf("APIKeys len = %d, want 1", len(p.APIKeys))
	}
	if p.APIKeys[0].Key != "sk-legacy" {
		t.Errorf("migrated key = %q, want 'sk-legacy'", p.APIKeys[0].Key)
	}
	if p.APIKeys[0].AccessControl != "private" {
		t.Errorf("default access = %q, want 'private'", p.APIKeys[0].AccessControl)
	}
	if !p.APIKeys[0].Enabled {
		t.Error("migrated key should be enabled")
	}
}

func TestMigrateProviderKeys_NoMigration(t *testing.T) {
	p := &Provider{APIKey: "", APIKeys: []APIKeyConfig{{ID: "k1"}}}
	if migrateProviderKeys(p) {
		t.Error("should not migrate when APIKeys already set")
	}
}

func TestMigrateProviderKeys_HasBoth(t *testing.T) {
	p := &Provider{APIKey: "sk-old", APIKeys: []APIKeyConfig{{ID: "k1"}}}
	if migrateProviderKeys(p) {
		t.Error("should not migrate when APIKeys already populated")
	}
}

// ============================================================
// enableLatestModels tests
// ============================================================

func TestEnableLatestModels_SmallSet(t *testing.T) {
	models := []ModelDef{
		{ID: "gpt-3.5", Enabled: true},
		{ID: "gpt-4", Enabled: true},
	}
	result := enableLatestModels(models)
	if len(result) != 2 {
		t.Errorf("result len = %d, want 2", len(result))
	}
}

func TestEnableLatestModels_LargeSet(t *testing.T) {
	models := []ModelDef{
		{ID: "gpt-3.5-turbo", Enabled: true},
		{ID: "gpt-4", Enabled: true},
		{ID: "gpt-4-turbo", Enabled: true},
		{ID: "gpt-4o", Enabled: true},
		{ID: "gpt-4-preview", Enabled: true},
		{ID: "text-embedding-ada", Enabled: true},
	}
	result := enableLatestModels(models)
	enabledCount := 0
	for _, m := range result {
		if m.Enabled {
			enabledCount++
		}
	}
	if enabledCount != 3 {
		t.Errorf("enabled count = %d, want 3", enabledCount)
	}
}

// ============================================================
// Tracker.rebuildEWMA / GetEWMA / TotalTokensByProvider tests
// ============================================================

func newTestTracker() *Tracker {
	return &Tracker{
		dataPath:            "/tmp/test_tracker_usage.json",
		ewmaCache:           make(map[string]float64),
		lastFlush:           time.Now(),
		stopCh:              make(chan struct{}),
		reqLogMax:           100,
		alertThresholds:     []float64{0.8, 0.9, 1.0},
		alertedTokens:       make(map[string]map[float64]bool),
		tokenUsageByProvider: make(map[string]int64),
	}
}

func TestRebuildEWMA_Basic(t *testing.T) {
	tr := newTestTracker()
	tr.records = []UsageRecord{
		{ProviderID: "p1", LatencyMS: 100, Success: true},
		{ProviderID: "p1", LatencyMS: 200, Success: true},
		{ProviderID: "p2", LatencyMS: 50, Success: true},
	}
	tr.rebuildEWMA()
	if tr.ewmaCache["p2"] != 50.0 {
		t.Errorf("single-entry EWMA = %f, want 50.0", tr.ewmaCache["p2"])
	}
	if tr.ewmaCache["p1"] == 0 {
		t.Error("p1 EWMA should be non-zero")
	}
}

func TestRebuildEWMA_SkipsFailed(t *testing.T) {
	tr := newTestTracker()
	tr.records = []UsageRecord{
		{ProviderID: "p1", LatencyMS: 100, Success: true},
		{ProviderID: "p1", LatencyMS: 999, Success: false},
	}
	tr.rebuildEWMA()
	if tr.ewmaCache["p1"] != 100.0 {
		t.Errorf("failed records should be skipped, got %f", tr.ewmaCache["p1"])
	}
}

func TestRebuildEWMA_SkipsZeroLatency(t *testing.T) {
	tr := newTestTracker()
	tr.records = []UsageRecord{
		{ProviderID: "p1", LatencyMS: 0, Success: true},
		{ProviderID: "p1", LatencyMS: 100, Success: true},
	}
	tr.rebuildEWMA()
	if tr.ewmaCache["p1"] != 100.0 {
		t.Errorf("zero-latency records should be skipped, got %f", tr.ewmaCache["p1"])
	}
}

func TestGetEWMA_Missing(t *testing.T) {
	tr := newTestTracker()
	if v := tr.GetEWMA("nonexistent"); v != 0 {
		t.Errorf("missing EWMA = %f, want 0", v)
	}
}

func TestTotalTokensByProvider(t *testing.T) {
	tr := newTestTracker()
	tr.records = []UsageRecord{
		{ProviderID: "p1", TotalTokens: 100},
		{ProviderID: "p1", TotalTokens: 200},
		{ProviderID: "p2", TotalTokens: 50},
	}
	tr.rebuildTokenUsage()
	totals := tr.TotalTokensByProvider()
	if totals["p1"] != 300 {
		t.Errorf("p1 total = %d, want 300", totals["p1"])
	}
	if totals["p2"] != 50 {
		t.Errorf("p2 total = %d, want 50", totals["p2"])
	}
}

// ============================================================
// Tracker.GetRequestLog / addRequestLog tests
// ============================================================

func TestAddRequestLog_Basic(t *testing.T) {
	tr := newTestTracker()
	tr.addRequestLog(RequestLogEntry{Model: "m1", ProviderID: "p1"})
	tr.addRequestLog(RequestLogEntry{Model: "m2", ProviderID: "p2"})
	log := tr.GetRequestLog(0)
	if len(log) != 2 {
		t.Errorf("log len = %d, want 2", len(log))
	}
}

func TestGetRequestLog_WithLimit(t *testing.T) {
	tr := newTestTracker()
	tr.addRequestLog(RequestLogEntry{Model: "m1"})
	tr.addRequestLog(RequestLogEntry{Model: "m2"})
	tr.addRequestLog(RequestLogEntry{Model: "m3"})
	log := tr.GetRequestLog(2)
	if len(log) != 2 {
		t.Fatalf("log len = %d, want 2", len(log))
	}
	if log[0].Model != "m2" || log[1].Model != "m3" {
		t.Errorf("limited log = [%q, %q], want [m2, m3]", log[0].Model, log[1].Model)
	}
}

func TestAddRequestLog_RingBuffer(t *testing.T) {
	tr := newTestTracker()
	tr.reqLogMax = 3
	for i := 0; i < 5; i++ {
		tr.addRequestLog(RequestLogEntry{Model: fmt.Sprintf("m%d", i)})
	}
	log := tr.GetRequestLog(0)
	if len(log) != 3 {
		t.Fatalf("ring buffer len = %d, want 3", len(log))
	}
	if log[0].Model != "m2" {
		t.Errorf("oldest kept = %q, want 'm2'", log[0].Model)
	}
}

// ============================================================
// nodeWeightManager pure logic tests (no global deps)
// ============================================================

func newTestNWM() *nodeWeightManager {
	return &nodeWeightManager{
		overrides:    make(map[string]*NodeWeightOverride),
		pending:      make(map[string]*ApprovalRequest),
		approvalMode: "auto",
		dataDir:      "/tmp/test_nwm",
	}
}

func TestNodeWeightManager_GetWeightMultiplier_NoOverride(t *testing.T) {
	m := newTestNWM()
	if v := m.GetWeightMultiplier("n1"); v != 1.0 {
		t.Errorf("no override = %f, want 1.0", v)
	}
}

func TestNodeWeightManager_GetWeightMultiplier_AutoApproved(t *testing.T) {
	m := newTestNWM()
	m.overrides["n1"] = &NodeWeightOverride{NodeID: "n1", Weight: 2.0, Approved: false}
	if v := m.GetWeightMultiplier("n1"); v != 2.0 {
		t.Errorf("auto mode = %f, want 2.0", v)
	}
}

func TestNodeWeightManager_GetWeightMultiplier_ManualUnapproved(t *testing.T) {
	m := newTestNWM()
	m.approvalMode = "manual"
	m.overrides["n1"] = &NodeWeightOverride{NodeID: "n1", Weight: 2.0, Approved: false}
	if v := m.GetWeightMultiplier("n1"); v != 1.0 {
		t.Errorf("manual unapproved = %f, want 1.0", v)
	}
}

func TestNodeWeightManager_GetWeightMultiplier_ManualApproved(t *testing.T) {
	m := newTestNWM()
	m.approvalMode = "manual"
	m.overrides["n1"] = &NodeWeightOverride{NodeID: "n1", Weight: 2.5, Approved: true}
	if v := m.GetWeightMultiplier("n1"); v != 2.5 {
		t.Errorf("manual approved = %f, want 2.5", v)
	}
}

func TestNodeWeightManager_GetOverrides(t *testing.T) {
	m := newTestNWM()
	m.overrides["n1"] = &NodeWeightOverride{NodeID: "n1", Weight: 1.5}
	m.overrides["n2"] = &NodeWeightOverride{NodeID: "n2", Weight: 2.0}
	o := m.GetOverrides()
	if len(o) != 2 {
		t.Errorf("overrides count = %d, want 2", len(o))
	}
}

func TestNodeWeightManager_GetPendingRequests(t *testing.T) {
	m := newTestNWM()
	m.pending["r1"] = &ApprovalRequest{ID: "r1", Status: "pending"}
	m.pending["r2"] = &ApprovalRequest{ID: "r2", Status: "approved"}
	pending := m.GetPendingRequests()
	if len(pending) != 1 {
		t.Errorf("pending count = %d, want 1", len(pending))
	}
}

func TestNodeWeightManager_GetAllRequests(t *testing.T) {
	m := newTestNWM()
	m.pending["r1"] = &ApprovalRequest{ID: "r1", Status: "pending"}
	m.pending["r2"] = &ApprovalRequest{ID: "r2", Status: "approved"}
	all := m.GetAllRequests()
	if len(all) != 2 {
		t.Errorf("all requests count = %d, want 2", len(all))
	}
}

func TestNodeWeightManager_GetApprovalMode(t *testing.T) {
	m := newTestNWM()
	if m.GetApprovalMode() != "auto" {
		t.Errorf("approval mode = %q, want 'auto'", m.GetApprovalMode())
	}
}

func TestNodeWeightManager_GetTokenBudget(t *testing.T) {
	m := newTestNWM()
	m.ownTokenBudget = 100000
	if m.GetTokenBudget() != 100000 {
		t.Errorf("token budget = %d, want 100000", m.GetTokenBudget())
	}
}

func TestNodeWeightManager_ResolveApproval(t *testing.T) {
	m := newTestNWM()
	m.pending["r1"] = &ApprovalRequest{ID: "r1", Status: "pending", ToNodeID: "n1"}
	m.overrides["n1"] = &NodeWeightOverride{NodeID: "n1", Weight: 2.0, Approved: false}

	if err := m.ResolveApproval("r1", true); err != nil {
		t.Fatalf("resolve approve: %v", err)
	}
	if m.pending["r1"].Status != "approved" {
		t.Errorf("status = %q, want 'approved'", m.pending["r1"].Status)
	}
	if !m.overrides["n1"].Approved {
		t.Error("override should be approved after resolution")
	}
}

func TestNodeWeightManager_ResolveApproval_Reject(t *testing.T) {
	m := newTestNWM()
	m.pending["r1"] = &ApprovalRequest{ID: "r1", Status: "pending", ToNodeID: "n1"}
	m.overrides["n1"] = &NodeWeightOverride{NodeID: "n1", Weight: 2.0}

	if err := m.ResolveApproval("r1", false); err != nil {
		t.Fatalf("resolve reject: %v", err)
	}
	if m.pending["r1"].Status != "rejected" {
		t.Errorf("status = %q, want 'rejected'", m.pending["r1"].Status)
	}
	if _, ok := m.overrides["n1"]; ok {
		t.Error("override should be removed on rejection")
	}
}

func TestNodeWeightManager_ResolveApproval_NotFound(t *testing.T) {
	m := newTestNWM()
	if err := m.ResolveApproval("nonexistent", true); err == nil {
		t.Error("should return error for nonexistent request")
	}
}

func TestNodeWeightManager_ResolveApproval_AlreadyResolved(t *testing.T) {
	m := newTestNWM()
	m.pending["r1"] = &ApprovalRequest{ID: "r1", Status: "approved"}
	if err := m.ResolveApproval("r1", true); err == nil {
		t.Error("should return error for already resolved request")
	}
}

// ============================================================
// validatePasswordStrength tests
// ============================================================

func TestValidatePasswordStrength_TooShort(t *testing.T) {
	if err := validatePasswordStrength("Ab1!"); err == nil {
		t.Error("short password should fail")
	}
}

func TestValidatePasswordStrength_NoUpper(t *testing.T) {
	if err := validatePasswordStrength("abcdefghij1!"); err == nil {
		t.Error("no uppercase should fail")
	}
}

func TestValidatePasswordStrength_NoLower(t *testing.T) {
	if err := validatePasswordStrength("ABCDEFGHIJ1!"); err == nil {
		t.Error("no lowercase should fail")
	}
}

func TestValidatePasswordStrength_NoDigit(t *testing.T) {
	if err := validatePasswordStrength("Abcdefghij!!"); err == nil {
		t.Error("no digit should fail")
	}
}

func TestValidatePasswordStrength_NoSpecial(t *testing.T) {
	if err := validatePasswordStrength("Abcdefghij12"); err == nil {
		t.Error("no special char should fail")
	}
}

func TestValidatePasswordStrength_Valid(t *testing.T) {
	if err := validatePasswordStrength("Abcdefghij1!"); err != nil {
		t.Errorf("valid password should pass: %v", err)
	}
}

// ============================================================
// EventBus Subscribe/Unsubscribe/Broadcast tests
// ============================================================

func TestEventBus_SubscribeUnsubscribe(t *testing.T) {
	eb := &EventBus{clients: make(map[string]chan SSEEvent)}
	id, ch := eb.Subscribe()
	if ch == nil {
		t.Error("channel should not be nil")
	}
	if _, ok := eb.clients[id]; !ok {
		t.Error("client should be in map")
	}
	eb.Unsubscribe(id)
	if _, ok := eb.clients[id]; ok {
		t.Error("client should be removed after unsubscribe")
	}
}

func TestEventBus_BroadcastWithTime(t *testing.T) {
	eb := &EventBus{clients: make(map[string]chan SSEEvent)}
	_, ch := eb.Subscribe()
	eb.Broadcast(SSEEvent{Type: "test", Data: "hello"})
	select {
	case evt := <-ch:
		if evt.Type != "test" {
			t.Errorf("event type = %q, want 'test'", evt.Type)
		}
	case <-time.After(time.Second):
		t.Error("broadcast event not received")
	}
}

func TestEventBus_BroadcastAutoTime(t *testing.T) {
	eb := &EventBus{clients: make(map[string]chan SSEEvent)}
	_, ch := eb.Subscribe()
	eb.Broadcast(SSEEvent{Type: "test"})
	select {
	case evt := <-ch:
		if evt.Time == "" {
			t.Error("time should be auto-set")
		}
	case <-time.After(time.Second):
		t.Error("timeout")
	}
}

func TestEventBus_BroadcastNoClients(t *testing.T) {
	eb := &EventBus{clients: make(map[string]chan SSEEvent)}
	eb.Broadcast(SSEEvent{Type: "test"})
}

// ============================================================
// MessageManager trimInbox/trimOutbox tests
// ============================================================

func newTestMsgManager() *MessageManager {
	return &MessageManager{
		inbox:   make([]FederationMessage, 0),
		outbox:  make([]FederationMessage, 0),
		dataDir: "/tmp/test_msg_mgr",
	}
}

func TestTrimInbox_NoTrim(t *testing.T) {
	m := newTestMsgManager()
	m.inbox = []FederationMessage{{ID: "1"}, {ID: "2"}}
	m.trimInbox()
	if len(m.inbox) != 2 {
		t.Errorf("inbox len = %d, want 2", len(m.inbox))
	}
}

func TestTrimInbox_OverSize(t *testing.T) {
	m := newTestMsgManager()
	for i := 0; i < maxInboxSize+10; i++ {
		m.inbox = append(m.inbox, FederationMessage{
			ID:        fmt.Sprintf("msg-%d", i),
			Timestamp: fmt.Sprintf("2026-01-01T00:%02d:00Z", i%60),
		})
	}
	m.trimInbox()
	if len(m.inbox) != maxInboxSize {
		t.Errorf("inbox len = %d, want %d", len(m.inbox), maxInboxSize)
	}
}

func TestTrimOutbox_NoTrim(t *testing.T) {
	m := newTestMsgManager()
	m.outbox = []FederationMessage{{ID: "1"}}
	m.trimOutbox()
	if len(m.outbox) != 1 {
		t.Errorf("outbox len = %d, want 1", len(m.outbox))
	}
}

func TestTrimOutbox_OverSize(t *testing.T) {
	m := newTestMsgManager()
	for i := 0; i < maxOutboxSize+5; i++ {
		m.outbox = append(m.outbox, FederationMessage{
			ID:        fmt.Sprintf("msg-%d", i),
			Timestamp: fmt.Sprintf("2026-01-01T00:%02d:00Z", i%60),
		})
	}
	m.trimOutbox()
	if len(m.outbox) != maxOutboxSize {
		t.Errorf("outbox len = %d, want %d", len(m.outbox), maxOutboxSize)
	}
}

// ============================================================
// ReputationManager.AddPeerScore / RecordCall / GetReputation
// ============================================================

func newTestRepMgr(t *testing.T) *ReputationManager {
	t.Helper()
	dir := t.TempDir()
	return &ReputationManager{
		scores:   make(map[string]*NodeReputation),
		myScores: make(map[string]*PeerScore),
		dataDir:  dir,
	}
}

func TestAddPeerScore_New(t *testing.T) {
	rm := newTestRepMgr(t)
	rm.AddPeerScore(PeerScore{FromNode: "n1", TargetNode: "n2", Availability: 80})
	rep := rm.GetReputation("n2")
	if rep == nil {
		t.Fatal("reputation should exist")
	}
	if len(rep.PeerScores) != 1 {
		t.Fatalf("peer scores = %d, want 1", len(rep.PeerScores))
	}
	if rep.PeerScores[0].Availability != 80 {
		t.Errorf("peer score = %f, want 80", rep.PeerScores[0].Availability)
	}
}

func TestAddPeerScore_Update(t *testing.T) {
	rm := newTestRepMgr(t)
	rm.AddPeerScore(PeerScore{FromNode: "n1", TargetNode: "n2", Availability: 80})
	rm.AddPeerScore(PeerScore{FromNode: "n1", TargetNode: "n2", Availability: 90})
	rep := rm.GetReputation("n2")
	if len(rep.PeerScores) != 1 {
		t.Fatalf("peer scores = %d, want 1 (updated)", len(rep.PeerScores))
	}
	if rep.PeerScores[0].Availability != 90 {
		t.Errorf("updated peer score = %f, want 90", rep.PeerScores[0].Availability)
	}
}

func TestAddPeerScore_MultipleSources(t *testing.T) {
	rm := newTestRepMgr(t)
	rm.AddPeerScore(PeerScore{FromNode: "n1", TargetNode: "n3", Availability: 70})
	rm.AddPeerScore(PeerScore{FromNode: "n2", TargetNode: "n3", Availability: 90})
	rep := rm.GetReputation("n3")
	if len(rep.PeerScores) != 2 {
		t.Errorf("peer scores = %d, want 2", len(rep.PeerScores))
	}
}

func TestRecordCall_Success(t *testing.T) {
	rm := newTestRepMgr(t)
	rm.RecordCall("n1", true, 100)
	rep := rm.GetReputation("n1")
	if rep.TotalRequests != 1 {
		t.Errorf("total requests = %d, want 1", rep.TotalRequests)
	}
	if rep.FailedRequests != 0 {
		t.Errorf("failed = %d, want 0", rep.FailedRequests)
	}
	if rep.Availability == 0 {
		t.Error("availability should be > 0 after success")
	}
}

func TestRecordCall_Failure(t *testing.T) {
	rm := newTestRepMgr(t)
	rm.RecordCall("n1", false, 0)
	rep := rm.GetReputation("n1")
	if rep.TotalRequests != 1 {
		t.Errorf("total requests = %d, want 1", rep.TotalRequests)
	}
	if rep.FailedRequests != 1 {
		t.Errorf("failed = %d, want 1", rep.FailedRequests)
	}
}

func TestRecordAccuracy(t *testing.T) {
	rm := newTestRepMgr(t)
	rm.RecordAccuracy("n1", true)
	rep := rm.GetReputation("n1")
	if rep.Accuracy == 0 {
		t.Error("accuracy should be > 0 after accurate record")
	}
}

func TestGetReputation_Missing(t *testing.T) {
	rm := newTestRepMgr(t)
	if rep := rm.GetReputation("nonexistent"); rep != nil {
		t.Error("missing node should return nil")
	}
}

func TestGetAllReputations(t *testing.T) {
	rm := newTestRepMgr(t)
	rm.RecordCall("n1", true, 50)
	rm.RecordCall("n2", true, 100)
	all := rm.GetAllReputations()
	if len(all) != 2 {
		t.Errorf("all reputations = %d, want 2", len(all))
	}
}

// ============================================================
// ProviderManager.AddAPIKey tests (with setupTestEnv)
// ============================================================

func TestProviderManager_AddAPIKey(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "test-prov", Name: "Test", Enabled: true, APIKey: "sk-test"})

	err := pm.AddAPIKey("test-prov", APIKeyConfig{Key: "sk-new", AccessControl: "public"})
	if err != nil {
		t.Fatalf("AddAPIKey: %v", err)
	}

	p, ok := pm.GetRaw("test-prov")
	if !ok {
		t.Fatal("provider not found")
	}
	if len(p.APIKeys) != 1 {
		t.Fatalf("APIKeys len = %d, want 1", len(p.APIKeys))
	}
	if p.APIKeys[0].Key != "sk-new" {
		t.Errorf("key = %q, want 'sk-new'", p.APIKeys[0].Key)
	}
	if p.APIKeys[0].AccessControl != "public" {
		t.Errorf("access = %q, want 'public'", p.APIKeys[0].AccessControl)
	}
}

func TestProviderManager_AddAPIKey_NotFound(t *testing.T) {
	setupTestEnv(t)
	err := pm.AddAPIKey("nonexistent", APIKeyConfig{Key: "sk-test"})
	if err == nil {
		t.Error("should return error for nonexistent provider")
	}
}

func TestProviderManager_AddAPIKey_Defaults(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "prov1", Name: "P1", Enabled: true, APIKey: "sk-1"})

	err := pm.AddAPIKey("prov1", APIKeyConfig{Key: "sk-2"})
	if err != nil {
		t.Fatalf("AddAPIKey: %v", err)
	}
	p, _ := pm.GetRaw("prov1")
	last := p.APIKeys[len(p.APIKeys)-1]
	if last.ID == "" {
		t.Error("ID should be auto-generated")
	}
	if last.AccessControl != "private" {
		t.Errorf("default access = %q, want 'private'", last.AccessControl)
	}
}

// ============================================================
// ProviderManager.ClearAllAPIKeys tests (with setupTestEnv)
// ============================================================

func TestProviderManager_ClearAllAPIKeys(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", Enabled: true, APIKey: "sk-1"})
	pm.Add(Provider{ID: "p2", Name: "P2", Enabled: true, APIKey: "sk-2", APIKeys: []APIKeyConfig{{ID: "k1", Key: "sk-3"}}})

	count := pm.ClearAllAPIKeys()
	if count != 3 {
		t.Errorf("cleared count = %d, want 3", count)
	}
	p1, _ := pm.GetRaw("p1")
	if p1.APIKey != "" {
		t.Error("p1 APIKey should be cleared")
	}
	p2, _ := pm.GetRaw("p2")
	if len(p2.APIKeys) != 0 {
		t.Error("p2 APIKeys should be cleared")
	}
}

// ============================================================
// ProviderManager.EnabledRaw tests (with setupTestEnv)
// ============================================================

func TestProviderManager_EnabledRaw(t *testing.T) {
	setupTestEnv(t)
	pm.Add(Provider{ID: "p1", Name: "P1", Enabled: true, APIKey: "sk-1"})
	pm.Add(Provider{ID: "p2", Name: "P2", Enabled: false, APIKey: "sk-2"})

	enabled := pm.EnabledRaw()
	if len(enabled) != 1 {
		t.Errorf("enabled count = %d, want 1", len(enabled))
	}
	if enabled[0].ID != "p1" {
		t.Errorf("enabled provider = %q, want 'p1'", enabled[0].ID)
	}
}

// ============================================================
// guestKeyUsageTracker.CheckAndReserve / Adjust / GetUsage tests
// ============================================================

func newTestGuestKeyTracker() *guestKeyUsageTracker {
	return &guestKeyUsageTracker{usage: make(map[string]int64)}
}

func TestCheckAndReserve_NoQuota(t *testing.T) {
	tr := newTestGuestKeyTracker()
	ok, rem := tr.CheckAndReserve("k1", 0, 100)
	if !ok {
		t.Error("no quota limit should allow")
	}
	if rem != 0 {
		t.Errorf("remaining = %d, want 0", rem)
	}
}

func TestCheckAndReserve_WithinQuota(t *testing.T) {
	tr := newTestGuestKeyTracker()
	ok, rem := tr.CheckAndReserve("k1", 1000, 100)
	if !ok {
		t.Error("within quota should allow")
	}
	if rem != 900 {
		t.Errorf("remaining = %d, want 900", rem)
	}
}

func TestCheckAndReserve_Exceeded(t *testing.T) {
	tr := newTestGuestKeyTracker()
	tr.usage["k1"] = 1000
	ok, _ := tr.CheckAndReserve("k1", 1000, 100)
	if ok {
		t.Error("exceeded quota should deny")
	}
}

func TestCheckAndReserve_NoEstimate(t *testing.T) {
	tr := newTestGuestKeyTracker()
	ok, rem := tr.CheckAndReserve("k1", 1000, 0)
	if !ok {
		t.Error("no estimate should allow")
	}
	if rem != 1000 {
		t.Errorf("remaining = %d, want 1000", rem)
	}
}

func TestCheckAndReserve_EstimateExceedsRemaining(t *testing.T) {
	tr := newTestGuestKeyTracker()
	tr.usage["k1"] = 900
	ok, _ := tr.CheckAndReserve("k1", 1000, 200)
	if ok {
		t.Error("estimate > remaining should deny")
	}
}

func TestAdjust_PositiveDiff(t *testing.T) {
	tr := newTestGuestKeyTracker()
	tr.usage["k1"] = 100
	tr.Adjust("k1", 50, 80)
	if tr.usage["k1"] != 130 {
		t.Errorf("usage = %d, want 130", tr.usage["k1"])
	}
}

func TestAdjust_NegativeDiff(t *testing.T) {
	tr := newTestGuestKeyTracker()
	tr.usage["k1"] = 100
	tr.Adjust("k1", 80, 50)
	if tr.usage["k1"] != 70 {
		t.Errorf("usage = %d, want 70", tr.usage["k1"])
	}
}

func TestAdjust_NoChange(t *testing.T) {
	tr := newTestGuestKeyTracker()
	tr.usage["k1"] = 100
	tr.Adjust("k1", 0, 0)
	if tr.usage["k1"] != 100 {
		t.Errorf("usage = %d, want 100", tr.usage["k1"])
	}
}

func TestAdjust_FloorAtZero(t *testing.T) {
	tr := newTestGuestKeyTracker()
	tr.usage["k1"] = 10
	tr.Adjust("k1", 100, 0)
	if tr.usage["k1"] != 0 {
		t.Errorf("usage = %d, want 0 (floored)", tr.usage["k1"])
	}
}

func TestGetUsage(t *testing.T) {
	tr := newTestGuestKeyTracker()
	tr.usage["k1"] = 42
	if v := tr.GetUsage("k1"); v != 42 {
		t.Errorf("usage = %d, want 42", v)
	}
}

// ============================================================
// GlobalPool.utilizationLocked / topContributorsLocked / GetNodes
// ============================================================

func newTestGlobalPool(t *testing.T) *GlobalPool {
	t.Helper()
	return &GlobalPool{
		NodeContributions: make(map[string]int64),
		NodeConsumptions:  make(map[string]int64),
		dataPath:          filepath.Join(t.TempDir(), "global_pool.json"),
	}
}

func TestUtilizationLocked_ZeroContrib(t *testing.T) {
	gp := newTestGlobalPool(t)
	if v := gp.utilizationLocked(); v != 0 {
		t.Errorf("utilization = %f, want 0", v)
	}
}

func TestUtilizationLocked_NonZero(t *testing.T) {
	gp := newTestGlobalPool(t)
	gp.TotalContributed = 1000
	gp.TotalConsumed = 300
	if v := gp.utilizationLocked(); v != 0.3 {
		t.Errorf("utilization = %f, want 0.3", v)
	}
}

func TestTopContributorsLocked(t *testing.T) {
	gp := newTestGlobalPool(t)
	gp.NodeContributions = map[string]int64{"n1": 500, "n2": 300, "n3": 100}
	gp.NodeConsumptions = map[string]int64{"n1": 100, "n2": 200, "n3": 50}
	top := gp.topContributorsLocked(2)
	if len(top) != 2 {
		t.Fatalf("top count = %d, want 2", len(top))
	}
	if top[0]["node_id"] != "n1" {
		t.Errorf("top contributor = %v, want n1", top[0]["node_id"])
	}
}

func TestTopContributorsLocked_FewerThanN(t *testing.T) {
	gp := newTestGlobalPool(t)
	gp.NodeContributions = map[string]int64{"n1": 100}
	top := gp.topContributorsLocked(5)
	if len(top) != 1 {
		t.Errorf("top count = %d, want 1", len(top))
	}
}

func TestGlobalPool_GetNodes(t *testing.T) {
	gp := newTestGlobalPool(t)
	gp.ParticipantNodes = []GlobalPoolNode{{NodeID: "n1"}, {NodeID: "n2"}}
	nodes := gp.GetNodes()
	if len(nodes) != 2 {
		t.Errorf("nodes count = %d, want 2", len(nodes))
	}
}

func TestGlobalPool_Contribute(t *testing.T) {
	gp := newTestGlobalPool(t)
	gp.ParticipantNodes = []GlobalPoolNode{{NodeID: "n1", LastHeartbeat: time.Now()}}
	if err := gp.Contribute("n1", 1000); err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	if gp.NodeContributions["n1"] != 1000 {
		t.Errorf("contributions = %d, want 1000", gp.NodeContributions["n1"])
	}
}

func TestGlobalPool_Contribute_NonParticipant(t *testing.T) {
	gp := newTestGlobalPool(t)
	if err := gp.Contribute("n1", 1000); err == nil {
		t.Error("non-participant should fail")
	}
}

func TestGlobalPool_Contribute_ZeroAmount(t *testing.T) {
	gp := newTestGlobalPool(t)
	if err := gp.Contribute("n1", 0); err == nil {
		t.Error("zero amount should fail")
	}
}

// ============================================================
// Auth.RegisterCollaborator / VerifyCollaboratorCredentials / ChangePassword
// ============================================================

func newTestAuth(t *testing.T) *Auth {
	t.Helper()
	dir := t.TempDir()
	a := &Auth{path: filepath.Join(dir, "auth.json")}
	a.data = AdminStore{
		JWTSecret:        randomString(32),
		JWTRefreshSecret: randomString(32),
		SMTP:             SMTPConfig{Port: 587, UseTLS: true},
		Admin: AdminData{
			Username:     "admin",
			PasswordHash: mustBcryptHash("Admin1!password"),
		},
		Initialized: true,
	}
	return a
}

func mustBcryptHash(pwd string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}

func TestAuth_RegisterCollaborator(t *testing.T) {
	a := newTestAuth(t)
	err := a.RegisterCollaborator("collab1", "Collab1!password", "gk-123")
	if err != nil {
		t.Fatalf("RegisterCollaborator: %v", err)
	}
	if len(a.data.Collaborators) != 1 {
		t.Errorf("collaborators = %d, want 1", len(a.data.Collaborators))
	}
	if a.data.Collaborators[0].Username != "collab1" {
		t.Errorf("username = %q, want 'collab1'", a.data.Collaborators[0].Username)
	}
}

func TestAuth_RegisterCollaborator_EmptyFields(t *testing.T) {
	a := newTestAuth(t)
	if err := a.RegisterCollaborator("", "pass", "key"); err == nil {
		t.Error("empty username should fail")
	}
	if err := a.RegisterCollaborator("user", "", "key"); err == nil {
		t.Error("empty password should fail")
	}
	if err := a.RegisterCollaborator("user", "pass", ""); err == nil {
		t.Error("empty guest key should fail")
	}
}

func TestAuth_RegisterCollaborator_DuplicateUsername(t *testing.T) {
	a := newTestAuth(t)
	_ = a.RegisterCollaborator("collab1", "Collab1!password", "gk-1")
	if err := a.RegisterCollaborator("collab1", "Collab2!password", "gk-2"); err == nil {
		t.Error("duplicate username should fail")
	}
}

func TestAuth_RegisterCollaborator_AdminUsername(t *testing.T) {
	a := newTestAuth(t)
	if err := a.RegisterCollaborator("admin", "Admin1!newpass", "gk-1"); err == nil {
		t.Error("admin username should fail")
	}
}

func TestAuth_RegisterCollaborator_WeakPassword(t *testing.T) {
	a := newTestAuth(t)
	if err := a.RegisterCollaborator("user", "short", "gk-1"); err == nil {
		t.Error("weak password should fail")
	}
}

func TestAuth_VerifyCollaboratorCredentials(t *testing.T) {
	a := newTestAuth(t)
	_ = a.RegisterCollaborator("collab1", "Collab1!password", "gk-1")
	c := a.VerifyCollaboratorCredentials("collab1", "Collab1!password")
	if c == nil {
		t.Error("valid credentials should return collaborator")
	}
}

func TestAuth_VerifyCollaboratorCredentials_Wrong(t *testing.T) {
	a := newTestAuth(t)
	_ = a.RegisterCollaborator("collab1", "Collab1!password", "gk-1")
	if c := a.VerifyCollaboratorCredentials("collab1", "wrong"); c != nil {
		t.Error("wrong password should return nil")
	}
}

func TestAuth_VerifyCollaboratorCredentials_NotFound(t *testing.T) {
	a := newTestAuth(t)
	if c := a.VerifyCollaboratorCredentials("nonexistent", "pass"); c != nil {
		t.Error("nonexistent user should return nil")
	}
}

func TestAuth_ChangePassword(t *testing.T) {
	a := newTestAuth(t)
	if err := a.ChangePassword("Admin1!password", "NewPass1!word"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if !a.VerifyCredentials("admin", "NewPass1!word") {
		t.Error("new password should work")
	}
}

func TestAuth_ChangePassword_WrongOld(t *testing.T) {
	a := newTestAuth(t)
	if err := a.ChangePassword("wrong", "NewPass1!word"); err == nil {
		t.Error("wrong old password should fail")
	}
}

func TestAuth_ChangePassword_WeakNew(t *testing.T) {
	a := newTestAuth(t)
	if err := a.ChangePassword("Admin1!password", "short"); err == nil {
		t.Error("weak new password should fail")
	}
}

func TestAuth_ChangePassword_NotInit(t *testing.T) {
	a := newTestAuth(t)
	a.data.Initialized = false
	a.data.Admin.Username = ""
	if err := a.ChangePassword("old", "NewPass1!word"); err == nil {
		t.Error("uninitialized admin should fail")
	}
}

func TestAuth_RefreshAccessToken(t *testing.T) {
	a := newTestAuth(t)
	_, refresh := a.CreateToken("admin", false)
	newAccess, err := a.RefreshAccessToken(refresh)
	if err != nil {
		t.Fatalf("RefreshAccessToken: %v", err)
	}
	if newAccess == "" {
		t.Error("new access token should not be empty")
	}
}

func TestAuth_RefreshAccessToken_InvalidToken(t *testing.T) {
	a := newTestAuth(t)
	_, err := a.RefreshAccessToken("invalid-token")
	if err == nil {
		t.Error("invalid token should fail")
	}
}

func TestAuth_RefreshAccessToken_WrongType(t *testing.T) {
	a := newTestAuth(t)
	access, _ := a.CreateToken("admin", false)
	_, err := a.RefreshAccessToken(access)
	if err == nil {
		t.Error("access token used as refresh should fail")
	}
}

// ============================================================
// RouteTable.Put / Get / Count / Remove / GetByModel / PurgeExpired
// ============================================================

func newTestRT() *RouteTable {
	return &RouteTable{entries: make(map[string]*RouteEntry)}
}

func TestRouteTable_PutAndGet(t *testing.T) {
	rt := newTestRT()
	rt.Put("n1", "node1", []string{"10.0.0.1:8080"})
	e := rt.Get("n1")
	if e == nil {
		t.Fatal("entry should exist")
	}
	if e.NodeName != "node1" {
		t.Errorf("name = %q, want 'node1'", e.NodeName)
	}
	if e.Status != "online" {
		t.Errorf("status = %q, want 'online'", e.Status)
	}
}

func TestRouteTable_GetMissing(t *testing.T) {
	rt := newTestRT()
	if e := rt.Get("nonexistent"); e != nil {
		t.Error("missing entry should return nil")
	}
}

func TestRouteTable_Count(t *testing.T) {
	rt := newTestRT()
	rt.Put("n1", "node1", nil)
	rt.Put("n2", "node2", nil)
	if rt.Count() != 2 {
		t.Errorf("count = %d, want 2", rt.Count())
	}
}

func TestRouteTable_Remove(t *testing.T) {
	rt := newTestRT()
	rt.Put("n1", "node1", nil)
	rt.Remove("n1")
	if rt.Count() != 0 {
		t.Errorf("count after remove = %d, want 0", rt.Count())
	}
}

func TestRouteTable_GetByModel_AnyModel(t *testing.T) {
	rt := newTestRT()
	rt.UpsertEntry(&RouteEntry{NodeID: "n1", UpdatedAt: time.Now()})
	result := rt.GetByModel("gpt-4")
	if len(result) != 1 {
		t.Errorf("entries with no Models should match any model, got %d", len(result))
	}
}

func TestRouteTable_GetByModel_SpecificModel(t *testing.T) {
	rt := newTestRT()
	rt.UpsertEntry(&RouteEntry{NodeID: "n1", Models: []string{"gpt-4"}, UpdatedAt: time.Now()})
	rt.UpsertEntry(&RouteEntry{NodeID: "n2", Models: []string{"claude-3"}, UpdatedAt: time.Now()})
	result := rt.GetByModel("gpt-4")
	if len(result) != 1 {
		t.Errorf("entries = %d, want 1", len(result))
	}
	if result[0].NodeID != "n1" {
		t.Errorf("node = %q, want 'n1'", result[0].NodeID)
	}
}

func TestRouteTable_PurgeExpired(t *testing.T) {
	rt := newTestRT()
	rt.UpsertEntry(&RouteEntry{NodeID: "n1", UpdatedAt: time.Now().Add(-routeTTL - time.Minute)})
	rt.UpsertEntry(&RouteEntry{NodeID: "n2", UpdatedAt: time.Now()})
	purged := rt.PurgeExpired()
	if purged != 1 {
		t.Errorf("purged = %d, want 1", purged)
	}
	if rt.Count() != 1 {
		t.Errorf("remaining = %d, want 1", rt.Count())
	}
}

func TestRouteTable_GetAll(t *testing.T) {
	rt := newTestRouteTable()
	rt.UpsertEntry(&RouteEntry{NodeID: "n1", UpdatedAt: time.Now()})
	rt.UpsertEntry(&RouteEntry{NodeID: "n2", UpdatedAt: time.Now().Add(-routeTTL - time.Minute)})
	all := rt.GetAll()
	if len(all) != 1 {
		t.Errorf("non-expired entries = %d, want 1", len(all))
	}
}

// ============================================================
// GlobalPool.JoinPool tests
// ============================================================

func TestGlobalPool_JoinPool(t *testing.T) {
	gp := newTestGlobalPool(t)
	if err := gp.JoinPool("n1", "us-east", 10000); err != nil {
		t.Fatalf("JoinPool: %v", err)
	}
	if len(gp.ParticipantNodes) != 1 {
		t.Errorf("participants = %d, want 1", len(gp.ParticipantNodes))
	}
	if gp.NodeContributions["n1"] != 10000 {
		t.Errorf("contributions = %d, want 10000", gp.NodeContributions["n1"])
	}
}

func TestGlobalPool_JoinPool_EmptyNodeID(t *testing.T) {
	gp := newTestGlobalPool(t)
	if err := gp.JoinPool("", "us-east", 10000); err == nil {
		t.Error("empty node ID should fail")
	}
}

func TestGlobalPool_JoinPool_BelowMinContribution(t *testing.T) {
	gp := newTestGlobalPool(t)
	if err := gp.JoinPool("n1", "us-east", 100); err == nil {
		t.Error("below minimum contribution should fail")
	}
}

func TestGlobalPool_JoinPool_UpdateExisting(t *testing.T) {
	gp := newTestGlobalPool(t)
	_ = gp.JoinPool("n1", "us-east", 10000)
	if err := gp.JoinPool("n1", "us-east", 15000); err != nil {
		t.Fatalf("second JoinPool: %v", err)
	}
	if len(gp.ParticipantNodes) != 1 {
		t.Errorf("participants = %d, want 1 (updated)", len(gp.ParticipantNodes))
	}
	if gp.NodeContributions["n1"] != 25000 {
		t.Errorf("contributions = %d, want 25000", gp.NodeContributions["n1"])
	}
}

// ============================================================
// GlobalPool.GetStats tests
// ============================================================

func TestGlobalPool_GetStats(t *testing.T) {
	gp := newTestGlobalPool(t)
	gp.ParticipantNodes = []GlobalPoolNode{
		{NodeID: "n1", Status: "active", Region: "us-east"},
		{NodeID: "n2", Status: "degraded", Region: "eu-west"},
	}
	gp.NodeContributions = map[string]int64{"n1": 1000, "n2": 500}
	gp.NodeConsumptions = map[string]int64{"n1": 200, "n2": 100}
	stats := gp.GetStats()
	if stats == nil {
		t.Fatal("stats should not be nil")
	}
}

// ============================================================
// Auth.ResetPassword / CreateResetToken / VerifyResetToken
// ============================================================

func TestAuth_CreateResetToken(t *testing.T) {
	a := newTestAuth(t)
	token := a.CreateResetToken()
	if token == "" {
		t.Error("reset token should not be empty")
	}
}

func TestAuth_VerifyResetToken(t *testing.T) {
	a := newTestAuth(t)
	token := a.CreateResetToken()
	if !a.VerifyResetToken(token) {
		t.Error("valid reset token should verify")
	}
}

func TestAuth_VerifyResetToken_Invalid(t *testing.T) {
	a := newTestAuth(t)
	if a.VerifyResetToken("invalid-token") {
		t.Error("invalid token should not verify")
	}
}

func TestAuth_VerifyResetToken_Consumed(t *testing.T) {
	a := newTestAuth(t)
	token := a.CreateResetToken()
	_ = a.ResetPassword(token, "NewPass1!word")
	if a.VerifyResetToken(token) {
		t.Error("consumed token should not verify after ResetPassword")
	}
}

func TestAuth_ResetPassword(t *testing.T) {
	a := newTestAuth(t)
	token := a.CreateResetToken()
	if err := a.ResetPassword(token, "NewPass1!word"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if !a.VerifyCredentials("admin", "NewPass1!word") {
		t.Error("new password should work after reset")
	}
}

func TestAuth_ResetPassword_InvalidToken(t *testing.T) {
	a := newTestAuth(t)
	if err := a.ResetPassword("bad-token", "NewPass1!word"); err == nil {
		t.Error("invalid token should fail")
	}
}

func TestAuth_ResetPassword_WeakPassword(t *testing.T) {
	a := newTestAuth(t)
	token := a.CreateResetToken()
	if err := a.ResetPassword(token, "short"); err == nil {
		t.Error("weak password should fail")
	}
}

