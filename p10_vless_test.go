package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ============================================================
// B10-WL6: vless:// share-link support (sider provider on prod uses VLESS
// REALITY — previously unsupported, leaving both chat and the built-in
// browser with an unparseable proxy)
// ============================================================

const testVLESSReality = "vless://2c4365bd-628a-4764-824e-a4ce3a19a0d0@104.243.26.217:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=portal.citygrainla.com&fp=chrome&pbk=BlSccfT5tvDIOS7v9VaPd0ulUNhUdvlMjGiNzDNDUHE&sid=051f20c2344a&type=tcp&headerType=none#JMS-test"

func TestP10_ParseVLESSLink_RealityTCP(t *testing.T) {
	cfg, err := ParseVLESSLink(testVLESSReality)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Add != "104.243.26.217" || cfg.Port != "443" {
		t.Fatalf("addr/port: %s:%s", cfg.Add, cfg.Port)
	}
	if cfg.ID != "2c4365bd-628a-4764-824e-a4ce3a19a0d0" {
		t.Fatalf("uuid: %s", cfg.ID)
	}
	if !cfg.IsVLESS || cfg.Security != "reality" || cfg.Flow != "xtls-rprx-vision" {
		t.Fatalf("vless fields: is=%v sec=%q flow=%q", cfg.IsVLESS, cfg.Security, cfg.Flow)
	}
	if cfg.PBK == "" || cfg.SID != "051f20c2344a" || cfg.FP != "chrome" || cfg.SNI != "portal.citygrainla.com" {
		t.Fatalf("reality params: pbk=%q sid=%q fp=%q sni=%q", cfg.PBK, cfg.SID, cfg.FP, cfg.SNI)
	}
}

func TestP10_ParseVLESSLink_WSTLS(t *testing.T) {
	link := "vless://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee@example.com:2053?encryption=none&security=tls&sni=cdn.example.com&type=ws&host=cdn.example.com&path=%2Fws%3Fed%3D2048#ws-node"
	cfg, err := ParseVLESSLink(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Net != "ws" || cfg.TLS != "tls" || cfg.Path != "/ws?ed=2048" || cfg.Host != "cdn.example.com" {
		t.Fatalf("ws/tls fields: net=%q tls=%q path=%q host=%q", cfg.Net, cfg.TLS, cfg.Path, cfg.Host)
	}
}

func TestP10_ParseVLESSLink_Invalid(t *testing.T) {
	cases := []string{
		"vmess://notavless",
		"vless://example.com:443",                     // no uuid
		"vless://uuid@host:notaport?security=reality&pbk=x", // bad port
		"vless://uuid@host:443?security=reality",      // reality without pbk
	}
	for i, c := range cases {
		if _, err := ParseVLESSLink(c); err == nil {
			t.Fatalf("case %d (%q): expected error", i, c)
		}
	}
}

func TestP10_GenerateConfig_VLESSOutbound(t *testing.T) {
	cfg, err := ParseVLESSLink(testVLESSReality)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := &VMessProxy{}
	full := m.generateConfig(cfg, 20801)
	outbounds := full["outbounds"].([]map[string]any)
	ob := outbounds[0]
	if ob["protocol"] != "vless" {
		t.Fatalf("protocol = %v, want vless", ob["protocol"])
	}
	b, _ := json.Marshal(ob)
	s := string(b)
	for _, want := range []string{`"flow":"xtls-rprx-vision"`, `"encryption":"none"`, `"security":"reality"`, `"publicKey":"BlSccfT5tvDIOS7v9VaPd0ulUNhUdvlMjGiNzDNDUHE"`, `"shortId":"051f20c2344a"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("outbound missing %s in %s", want, s)
		}
	}
	// vmess path must be unaffected by the vless fields.
	vcfg := &VMessConfig{Add: "1.2.3.4", Port: "443", ID: "uuid-1", Net: "tcp"}
	full2 := (&VMessProxy{}).generateConfig(vcfg, 20802)
	ob2 := full2["outbounds"].([]map[string]any)[0]
	if ob2["protocol"] != "vmess" {
		t.Fatalf("plain vmess protocol = %v", ob2["protocol"])
	}
}

// B10-WL8: login-status endpoint answers from the raw record — must report
// saved=true even though Safe() masks api_key (the old poll could never
// succeed against the masked form).
func TestP10_ProviderLoginStatus(t *testing.T) {
	setupTestEnv(t) // isolates globals (pm/cfg/...) and registers cleanup
	pm.Add(Provider{ID: "p10-login-status", Type: "web_session", APIKey: ""})
	pm.Add(Provider{ID: "p10-login-status2", Type: "web_session", APIKey: "sid_session_abcdef123456"})

	// Route through a mux so {id} PathValue is populated (plain
	// httptest.NewRequest leaves PathValue empty).
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/providers/{id}/login-status", handleProviderLoginStatus)

	get := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/providers/"+id+"/login-status", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := get("p10-login-status")
	var d struct{ TokenSaved bool `json:"token_saved"` }
	if err := json.NewDecoder(rec.Body).Decode(&d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.TokenSaved {
		t.Fatalf("expected token_saved=false for empty key")
	}

	rec2 := get("p10-login-status2")
	if rec2.Code != 200 {
		t.Fatalf("status = %d", rec2.Code)
	}
	var d2 map[string]any
	if err := json.NewDecoder(rec2.Body).Decode(&d2); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if d2["token_saved"] != true {
		t.Fatalf("token_saved = %v, want true", d2["token_saved"])
	}

	// Unknown provider → 404 (ownership guard semantics).
	if rec3 := get("nope"); rec3.Code != 404 {
		t.Fatalf("unknown provider status = %d, want 404", rec3.Code)
	}
}
