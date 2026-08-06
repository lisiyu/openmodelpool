package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// QA 独立补充测试（严过关 / Yan）—— OMP v4.1.6 私有节点联邦组网修复
// 目的：在工程师 federation_auth_test.go / network_peer_test.go 之上，补齐对
//       关键路径中"仅被间接覆盖 / 完全未覆盖"的验收点的直接断言：
//   • resolvePublicEndpoint 来源优先级（P0-1 根因修复 + 架构 §6 约定）—— 纯单测
//   • handleNetworkAddPeer 空 node_id 但对端不可达时的优雅错误处理（不 panic / 400）
//   • NodeIdentity.GetInfo() 实际产出的 Endpoint 是否已是公网可达（P0-1 端到端验收）
// 全部离线（httptest / 全局 cfg + t.Setenv），不依赖外网，不修改任何业务源码。
// ============================================================================

// TestResolvePublicEndpoint_Priority 直接验证 P0-1 的根因修复函数
// resolvePublicEndpoint 的优先级链：federation_endpoint > public_domain >
// 请求 Host 头 > LAN 兜底（仅内网、打 WARN）。这是本次修复的"心脏"，但工程师
// 的测试只通过 GetInfo 间接触发、并未断言返回值，故此处做确定性的纯单测。
// 用 t.Setenv 配置（与 cfg.Get 的配置/环境变量双通道一致），避免写盘 debounce 等待。
func TestResolvePublicEndpoint_Priority(t *testing.T) {
	cases := []struct {
		name         string
		fedEndpoint  string // "" 表示显式设为空（中和可能残留的环境变量）
		publicDomain string
		host         string
		servicePort  string
		want         string // 期望精确匹配（为空则改用前缀/端口断言）
		wantPrefix   string
	}{
		{
			name:        "federation_endpoint_wins_over_all",
			fedEndpoint: "https://explicit.example.com",
			publicDomain: "https://public.example.com",
			host:        "host.example.com",
			want:        "https://explicit.example.com",
		},
		{
			name:        "public_domain_wins_when_no_federation_endpoint",
			fedEndpoint: "",
			publicDomain: "https://public.example.com",
			host:        "host.example.com",
			want:        "https://public.example.com",
		},
		{
			name:        "host_header_used_when_no_config",
			fedEndpoint: "",
			publicDomain: "",
			host:        "host.example.com",
			want:        "https://host.example.com",
		},
		{
			name:        "lan_fallback_warn_when_nothing_set",
			fedEndpoint: "",
			publicDomain: "",
			host:        "",
			servicePort: "8123",
			wantPrefix:  "http://",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_ = setupTestEnv(t) // 全新隔离 cfg
			// 通过环境变量通道配置（与 cfg.Get 的生产配置/环境变量双通道一致）。
			t.Setenv("FEDERATION_ENDPOINT", tc.fedEndpoint)
			t.Setenv("PUBLIC_DOMAIN", tc.publicDomain)
			if tc.servicePort != "" {
				t.Setenv("PORT", tc.servicePort)
			} else {
				t.Setenv("PORT", "8000")
			}

			got := resolvePublicEndpoint(tc.host)
			if tc.want != "" {
				if got != tc.want {
					t.Errorf("resolvePublicEndpoint(%q) = %q, want %q", tc.host, got, tc.want)
				}
				return
			}
			if tc.wantPrefix != "" {
				if !strings.HasPrefix(got, tc.wantPrefix) {
					t.Errorf("resolvePublicEndpoint(%q) = %q, want prefix %q", tc.host, got, tc.wantPrefix)
				}
				if tc.servicePort != "" && !strings.Contains(got, ":"+tc.servicePort) {
					t.Errorf("LAN fallback = %q, want it to embed configured service_port :%s", got, tc.servicePort)
				}
			}
		})
	}
}

// TestNetworkAddPeer_EmptyNodeIDUnreachable 验证 P0-2 的"优雅错误处理"路径：
// 当管理员留空 node_id、而所给地址的对端心跳端点不可达（连接被拒）时，
// handleNetworkAddPeer 必须返回 400 且附带清晰错误信息，绝不能 panic 或 500。
// 用"先起 httptest 假对端再 Close()"，使 ping 端点确定性地不可达，完全离线。
func TestNetworkAddPeer_EmptyNodeIDUnreachable(t *testing.T) {
	env := setupTestEnv(t)
	nm := &NetworkManager{
		dataPath: filepath.Join(env.dir, "network.json"),
		config:   NetworkConfig{NetworkEnabled: true, Mode: NetworkModeShared},
	}
	netMgr = nm
	t.Cleanup(func() { netMgr = nil })
	ensureRouteTable()
	t.Cleanup(func() { routeTable = nil })

	// 起一个假对端，拿到 URL 后立即关闭，使 /api/network/heartbeat/ping 不可达。
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachable := peerSrv.URL
	peerSrv.Close()

	body := strings.NewReader(`{"addresses":["` + unreachable + `"],"node_id":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/network/peers", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleNetworkAddPeer(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unreachable peer, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "panic") {
		t.Errorf("response must not contain a panic: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "resolve") {
		t.Errorf("error should mention node_id resolution failure: %s", rec.Body.String())
	}
}

// TestNodeGetInfo_UsesConfiguredPublicEndpoint 端到端验证 P0-1 的验收标准：
// "由 getEndpoint() 生成的所有邀请码 / peer 注册信息中 endpoint 字段必须是公网
// 可达地址"。当配置了 public_domain 且未配置 federation_endpoint 时，节点身份
// (GetInfo) 产出的 Endpoint 必须是该公网域名，而绝不能回落到内网 http:// 地址。
func TestNodeGetInfo_UsesConfiguredPublicEndpoint(t *testing.T) {
	env := setupTestEnv(t)
	t.Setenv("FEDERATION_ENDPOINT", "")              // 确保显式端点未配置
	t.Setenv("PUBLIC_DOMAIN", "https://openmodelpool.io")

	node = &NodeIdentity{keyPath: filepath.Join(env.dir, "node.key")}
	t.Cleanup(func() { node = nil })
	// 跳过 GetInfo 的共享 provider 分支，保持断言只关注 endpoint 解析。
	fed = nil
	netMgr = &NetworkManager{config: NetworkConfig{NetworkEnabled: true, Mode: NetworkModeShared}}
	t.Cleanup(func() { netMgr = nil })

	info := node.GetInfo()
	if info.Endpoint != "https://openmodelpool.io" {
		t.Errorf("GetInfo().Endpoint = %q, want https://openmodelpool.io (P0-1: must be public-reachable, not a LAN fallback)", info.Endpoint)
	}
	// 额外确认：绝不可能是内网 http:// 兜底。
	if strings.HasPrefix(info.Endpoint, "http://") {
		t.Errorf("GetInfo().Endpoint fell back to LAN http:// address: %q", info.Endpoint)
	}
}
