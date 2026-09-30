package main

// P2P-G1 网关转发 failover 测试。

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// failoverTestClient 让共享 HTTP 客户端信任测试 TLS 服务器的自签证书。
func failoverTestClient(t *testing.T) {
	t.Helper()
	client := GetSharedHTTPClient()
	prev := client.Transport
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	t.Cleanup(func() { client.Transport = prev })
}

// withFailoverRouteTable 把全局 routeTable 换成测试用表，测试后恢复。
func withFailoverRouteTable(t *testing.T) *RouteTable {
	t.Helper()
	orig := routeTable
	rt := initRouteTable()
	routeTable = rt
	t.Cleanup(func() { routeTable = orig })
	return rt
}

func TestSelectRankedNodes_ExcludesHighFailCount(t *testing.T) {
	rt := initRouteTable()
	mk := func(id string, fails int) {
		rt.Put(id, id, []string{"https://example.com"})
		rt.UpdateEntry(id, func(e *RouteEntry) {
			e.Models = []string{"m"}
			e.LatencyMS = 10
			e.LoadScore = 0.1
			e.FailCount = fails
		})
	}
	mk("mmx-dead", 3)  // 达到阈值：排除
	mk("mmx-flaky", 1) // 降权但保留
	mk("mmx-good", 0)

	ranked := rt.SelectRankedNodes("m", 0)
	if len(ranked) != 2 {
		t.Fatalf("ranked = %d nodes, want 2 (dead excluded)", len(ranked))
	}
	if ranked[0].NodeID != "mmx-good" || ranked[1].NodeID != "mmx-flaky" {
		t.Fatalf("wrong order: %+v", ranked)
	}

	// limit 生效
	ranked = rt.SelectRankedNodes("m", 1)
	if len(ranked) != 1 || ranked[0].NodeID != "mmx-good" {
		t.Fatalf("limit=1 wrong: %+v", ranked)
	}
}

func TestGatewayFailover_DeadFirstCandidateFallsToSecond(t *testing.T) {
	relayTestEnv(t)
	failoverTestClient(t)
	rt := withFailoverRouteTable(t)

	// 第二个候选：活的测试服务器
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"node":"second"}`))
	}))
	defer srv.Close()

	rt.Put("mmx-dead", "Dead Node", []string{"https://127.0.0.1:1"})

	candidates := []RouteEntry{
		{NodeID: "mmx-dead", Addresses: []string{"https://127.0.0.1:1"}},
		{NodeID: "mmx-live", Addresses: []string{srv.URL}},
	}
	body := []byte(`{"model":"test","messages":[]}`)
	inReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	done := gatewayForwardFailover(rec, inReq, candidates, body, 0, false, "test-model", "mmx-self")
	if !done {
		t.Fatal("failover should succeed via the second candidate")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"node":"second"`) {
		t.Fatalf("response did not come from second node: %s", rec.Body.String())
	}
	// 死节点的 FailCount 被累计，供后续请求降权/排除
	if e := rt.Get("mmx-dead"); e == nil || e.FailCount != 1 {
		t.Fatalf("dead node FailCount = %+v, want 1", e)
	}
}

func TestGatewayFailover_AllDeadReturnsFalse(t *testing.T) {
	relayTestEnv(t)
	failoverTestClient(t)

	candidates := []RouteEntry{
		{NodeID: "mmx-dead1", Addresses: []string{"https://127.0.0.1:1"}},
		{NodeID: "mmx-dead2", Addresses: []string{"https://127.0.0.1:2"}},
	}
	body := []byte(`{"model":"test","messages":[]}`)
	inReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	done := gatewayForwardFailover(rec, inReq, candidates, body, 0, false, "test-model", "mmx-self")
	if done {
		t.Fatal("all candidates dead: should return false so caller falls back locally")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("nothing should be written when all candidates fail, got: %s", rec.Body.String())
	}
}

func TestGatewayFailover_SingleCandidate(t *testing.T) {
	relayTestEnv(t)
	failoverTestClient(t)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body := []byte(`{"model":"test","messages":[]}`)

	// 单活候选：成功
	inReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	if !gatewayForwardFailover(rec, inReq, []RouteEntry{{NodeID: "mmx-live", Addresses: []string{srv.URL}}}, body, 0, false, "test-model", "mmx-self") {
		t.Fatal("single live candidate should succeed")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// 单死候选：返回 false（调用方本地兜底），行为与原来"失败即兜底"一致
	inReq2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	if gatewayForwardFailover(rec2, inReq2, []RouteEntry{{NodeID: "mmx-dead", Addresses: []string{"https://127.0.0.1:1"}}}, body, 0, false, "test-model", "mmx-self") {
		t.Fatal("single dead candidate should return false")
	}
}
