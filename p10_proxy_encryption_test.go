package main

import (
	"os"
	"strings"
	"testing"
)

// TestProxyEncryption_LongVLESS verifies that long vless proxy URLs
// (with ML-KEM encryption keys, 2000+ chars) survive save/load round-trip
// and are stored encrypted on disk.
func TestProxyEncryption_LongVLESS(t *testing.T) {
	// Create a long vless link with a 2000+ char encryption param
	longEncKey := strings.Repeat("A", 2000) + "==" // ML-KEM style long key
	vlessLink := "vless://test-uuid@example.com:443?encryption=" + longEncKey + "&type=ws&security=tls&path=/ws"

	if len(vlessLink) < 2000 {
		t.Fatalf("test link too short: %d chars", len(vlessLink))
	}

	// Use a temp dir for data
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Re-init encryptor with a fresh key (uses data/.enc_key in cwd)
	enc, _ = NewEncryptor()
	enc.ready = true

	// Re-init provider manager
	initProviderManager("providers.json")

	// Add provider with long proxy
	p := Provider{
		ID:      "test-proxy",
		Name:    "Test",
		Type:    "openai_compatible",
		BaseURL: "https://api.example.com",
		APIKey:  "sk-test",
		Proxy:   vlessLink,
		Models:  []ModelDef{{ID: "gpt-4", Enabled: true}},
	}
	pm.Add(p)

	// Read back from memory - should be plaintext
	got, ok := pm.GetRaw("test-proxy")
	if !ok {
		t.Fatal("provider not found after add")
	}
	if got.Proxy != vlessLink {
		t.Fatalf("in-memory proxy mismatch: got %d chars, want %d chars", len(got.Proxy), len(vlessLink))
	}

	// Force save and check disk file - should be encrypted
	pm.save()

	data, err := os.ReadFile("providers.json")
	if err != nil {
		t.Fatalf("failed to read providers.json: %v", err)
	}

	dataStr := string(data)
	if !strings.Contains(dataStr, "omp:e:") {
		t.Fatal("proxy not encrypted on disk (no omp:e: prefix found)")
	}
	if strings.Contains(dataStr, longEncKey) {
		t.Fatal("proxy plaintext leaked on disk!")
	}

	// Re-load and verify decryption works
	pm2 := &ProviderManager{
		providers: make(map[string]Provider),
		dataPath:  "providers.json",
	}
	pm2.load()

	got2, ok := pm2.GetRaw("test-proxy")
	if !ok {
		t.Fatal("provider not found after reload")
	}
	if got2.Proxy != vlessLink {
		t.Fatalf("reloaded proxy mismatch: got %d chars, want %d chars", len(got2.Proxy), len(vlessLink))
	}
	if !strings.HasPrefix(got2.Proxy, "vless://") {
		t.Fatalf("reloaded proxy doesn't start with vless://: got %s", got2.Proxy[:30])
	}

	t.Logf("Long vless proxy (%d chars) encrypted and decrypted successfully", len(vlessLink))
}
