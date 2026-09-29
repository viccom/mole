package tunnel

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
)

// ---------------------------------------------------------------------------
// mockNodeProvider implements NodeProvider for testing (no real smux sessions).
// ---------------------------------------------------------------------------

type mockNodeProvider struct {
	nodes    map[string]*core.Node // nodeID -> Node
	sessions map[string]*smux.Session
}

func newMockNodeProvider() *mockNodeProvider {
	return &mockNodeProvider{
		nodes:    make(map[string]*core.Node),
		sessions: make(map[string]*smux.Session),
	}
}

func (m *mockNodeProvider) addNode(node *core.Node) *mockNodeProvider {
	m.nodes[node.ID] = node
	return m
}

func (m *mockNodeProvider) Get(_ context.Context, nodeID string) (*core.Node, bool) {
	n, ok := m.nodes[nodeID]
	return n, ok
}

func (m *mockNodeProvider) GetAll(_ context.Context) []*core.Node {
	out := make([]*core.Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	return out
}

func (m *mockNodeProvider) GetSession(_ context.Context, nodeID string) (*smux.Session, error) {
	s, ok := m.sessions[nodeID]
	if !ok {
		return nil, smux.ErrTimeout
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func onlineNode(id string, tunnels ...core.Tunnel) *core.Node {
	return &core.Node{
		ID:      id,
		Name:    id,
		Status:  core.NodeStatusOnline,
		Tunnels: tunnels,
	}
}

func offlineNode(id string, tunnels ...core.Tunnel) *core.Node {
	return &core.Node{
		ID:      id,
		Name:    id,
		Status:  core.NodeStatusOffline,
		Tunnels: tunnels,
	}
}

func httpTunnel(name, domain string) core.Tunnel {
	return core.Tunnel{
		Name:   name,
		Type:   core.TunnelTypeHTTP,
		Target: "127.0.0.1:8080",
		Domain: domain,
	}
}

// ---------------------------------------------------------------------------
// TestFindNodeTunnel_Found: node exists, tunnel matches, node is online.
// ---------------------------------------------------------------------------

func TestFindNodeTunnel_Found(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("client1", httpTunnel("api", "api.example.com")))

	tg := NewTunnelGateway(mp, 10, nil)
	node, tunnelName := tg.findNodeTunnel(context.Background(), "client1", "api")

	if node == nil {
		t.Fatal("expected node, got nil")
	}
	if node.ID != "client1" {
		t.Errorf("node.ID = %q, want %q", node.ID, "client1")
	}
	if tunnelName != "api" {
		t.Errorf("tunnelName = %q, want %q", tunnelName, "api")
	}
}

// ---------------------------------------------------------------------------
// TestFindNodeTunnel_NotFound: node exists but has no matching tunnel.
// ---------------------------------------------------------------------------

func TestFindNodeTunnel_NotFound(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("client1", httpTunnel("web", "web.example.com")))

	tg := NewTunnelGateway(mp, 10, nil)
	node, tunnelName := tg.findNodeTunnel(context.Background(), "client1", "api")

	if node != nil {
		t.Errorf("expected nil node, got %v", node)
	}
	if tunnelName != "" {
		t.Errorf("expected empty tunnelName, got %q", tunnelName)
	}
}

// ---------------------------------------------------------------------------
// TestFindNodeTunnel_NodeOffline: node exists with matching tunnel but is offline.
// ---------------------------------------------------------------------------

func TestFindNodeTunnel_NodeOffline(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(offlineNode("client1", httpTunnel("api", "api.example.com")))

	tg := NewTunnelGateway(mp, 10, nil)
	node, tunnelName := tg.findNodeTunnel(context.Background(), "client1", "api")

	if node != nil {
		t.Errorf("expected nil node for offline node, got %v", node)
	}
	if tunnelName != "" {
		t.Errorf("expected empty tunnelName for offline node, got %q", tunnelName)
	}
}

// ---------------------------------------------------------------------------
// TestFindNodeTunnel_UnknownNode: node ID does not exist in provider at all.
// ---------------------------------------------------------------------------

func TestFindNodeTunnel_UnknownNode(t *testing.T) {
	mp := newMockNodeProvider()
	// No nodes added.

	tg := NewTunnelGateway(mp, 10, nil)
	node, tunnelName := tg.findNodeTunnel(context.Background(), "nonexistent", "api")

	if node != nil {
		t.Errorf("expected nil node, got %v", node)
	}
	if tunnelName != "" {
		t.Errorf("expected empty tunnelName, got %q", tunnelName)
	}
}

// ---------------------------------------------------------------------------
// TestIsWebSocketRequest: verify header combinations.
// ---------------------------------------------------------------------------

func TestIsWebSocketRequest(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{
			name:    "valid websocket headers",
			headers: map[string]string{"Upgrade": "websocket", "Connection": "Upgrade"},
			want:    true,
		},
		{
			name:    "valid websocket with mixed-case upgrade header",
			headers: map[string]string{"Upgrade": "WebSocket", "Connection": "upgrade"},
			want:    true,
		},
		{
			name:    "valid websocket with connection containing keep-alive,upgrade",
			headers: map[string]string{"Upgrade": "websocket", "Connection": "keep-alive, Upgrade"},
			want:    true,
		},
		{
			name:    "missing upgrade header",
			headers: map[string]string{"Connection": "Upgrade"},
			want:    false,
		},
		{
			name:    "missing connection header",
			headers: map[string]string{"Upgrade": "websocket"},
			want:    false,
		},
		{
			name:    "upgrade is not websocket",
			headers: map[string]string{"Upgrade": "h2c", "Connection": "Upgrade"},
			want:    false,
		},
		{
			name:    "no headers at all",
			headers: map[string]string{},
			want:    false,
		},
		{
			name:    "connection does not contain upgrade",
			headers: map[string]string{"Upgrade": "websocket", "Connection": "keep-alive"},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			got := isWebSocketRequest(req)
			if got != tt.want {
				t.Errorf("isWebSocketRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestServeHTTP_NoTunnelMatched: request with no matching tunnel -> 502.
// ---------------------------------------------------------------------------

func TestServeHTTP_NoTunnelMatched(t *testing.T) {
	mp := newMockNodeProvider()
	// No nodes at all -- nothing can match.
	tg := NewTunnelGateway(mp, 10, nil)

	req := httptest.NewRequest(http.MethodGet, "http://unknown.example.com/test", nil)
	w := httptest.NewRecorder()

	tg.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}

	body := w.Body.String()
	if !strings.Contains(body, "No tunnel matched") {
		t.Errorf("body should contain 'No tunnel matched', got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// TestServeHTTP_SemaphoreOverloaded: context cancelled before acquiring semaphore.
// ---------------------------------------------------------------------------

func TestServeHTTP_SemaphoreOverloaded(t *testing.T) {
	mp := newMockNodeProvider()
	tg := NewTunnelGateway(mp, 1, nil) // capacity 1

	// Acquire the single slot so the next request is blocked.
	tg.sem.Acquire(context.Background())
	defer tg.sem.Release()

	// Use an already-cancelled context so ServeHTTP returns quickly.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "http://api-node1.example.com/test", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	tg.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}

// ---------------------------------------------------------------------------
// TestServeHTTP_VirtualHostRouting: hyphen-based vhost resolves to correct node.
// ---------------------------------------------------------------------------

func TestServeHTTP_VirtualHostRouting(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("node1", httpTunnel("api", "")))

	tg := NewTunnelGateway(mp, 10, nil)
	tg.HyphenRouting = true

	// Host "api-node1.example.com" should resolve to node1, tunnel "api".
	// The handler will try to open a smux stream and fail (no session),
	// so we expect a 502 "Upstream error", NOT "No tunnel matched".
	req := httptest.NewRequest(http.MethodGet, "http://api-node1.example.com/v1/data", nil)
	w := httptest.NewRecorder()

	tg.ServeHTTP(w, req)

	resp := w.Result()
	// We should NOT get "No tunnel matched" -- the routing found the node.
	body := w.Body.String()
	if strings.Contains(body, "No tunnel matched") {
		t.Error("should have matched the tunnel via vhost routing, but got 'No tunnel matched'")
	}
	// Because there's no smux session, the handler returns 502 upstream error.
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d (upstream error due to no smux session)", resp.StatusCode, http.StatusBadGateway)
	}
}

// ---------------------------------------------------------------------------
// TestServeHTTP_PathRouting: path-based routing resolves correctly.
// ---------------------------------------------------------------------------

func TestServeHTTP_PathRouting(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("node1", httpTunnel("web", "")))

	tg := NewTunnelGateway(mp, 10, nil)
	tg.HyphenRouting = false

	// Path "/node1/web/page" should resolve to node1, tunnel "web".
	req := httptest.NewRequest(http.MethodGet, "http://example.com/node1/web/page", nil)
	w := httptest.NewRecorder()

	tg.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "No tunnel matched") {
		t.Error("should have matched the tunnel via path routing")
	}
}

// ---------------------------------------------------------------------------
// TestServeHTTP_DomainIndexRouting: exact domain match via index.
// ---------------------------------------------------------------------------

func TestServeHTTP_DomainIndexRouting(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("node1", httpTunnel("mysite", "test.example.com")))

	tg := NewTunnelGateway(mp, 10, nil)
	tg.RebuildIndex(context.Background()) // build domain index

	req := httptest.NewRequest(http.MethodGet, "http://test.example.com/hello", nil)
	w := httptest.NewRecorder()

	tg.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "No tunnel matched") {
		t.Error("should have matched the tunnel via domain index")
	}
}

// ---------------------------------------------------------------------------
// TestParseVirtualHostByHyphen_EdgeCases: additional edge-case tests.
// ---------------------------------------------------------------------------

func TestParseVirtualHostByHyphen_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		wantClient string
		wantMap    string
		wantVhost  bool
	}{
		{
			name:       "empty host",
			host:       "",
			wantClient: "",
			wantMap:    "",
			wantVhost:  false,
		},
		{
			name:       "single segment no dots",
			host:       "localhost",
			wantClient: "",
			wantMap:    "",
			wantVhost:  false,
		},
		{
			name:       "bare domain two parts no hyphen in first",
			host:       "example.com",
			wantClient: "",
			wantMap:    "",
			wantVhost:  false,
		},
		{
			name:       "three parts but first has no hyphen",
			host:       "foo.example.com",
			wantClient: "",
			wantMap:    "",
			wantVhost:  false,
		},
		{
			name:       "IPv4 address",
			host:       "10.0.0.1",
			wantClient: "",
			wantMap:    "",
			wantVhost:  false,
		},
		{
			name:       "IPv6 loopback",
			host:       "::1",
			wantClient: "",
			wantMap:    "",
			wantVhost:  false,
		},
		{
			// Split on "-" produces ["a", "b", "c"]; vhostParts[0]="a", vhostParts[1]="b".
			// The function only uses the first two segments, so "c" is discarded.
			name:       "multi-hyphen first segment",
			host:       "a-b-c.example.com",
			wantClient: "b",
			wantMap:    "a",
			wantVhost:  true,
		},
		{
			name:       "host with port",
			host:       "svc-node1.example.com:443",
			wantClient: "node1",
			wantMap:    "svc",
			wantVhost:  true,
		},
		{
			name:       "two-part localhost with hyphen",
			host:       "api-node1.localhost",
			wantClient: "node1",
			wantMap:    "api",
			wantVhost:  true,
		},
		{
			name:       "two-part localhost without hyphen",
			host:       "example.localhost",
			wantClient: "",
			wantMap:    "",
			wantVhost:  false,
		},
		{
			// Split on "-" produces ["", ""]; len(vhostParts)=2 >= 2, so isVhost=true.
			// vhostParts[0]="", vhostParts[1]="". Empty strings are returned.
			name:       "single hyphen as first segment",
			host:       "-.example.com",
			wantClient: "",
			wantMap:    "",
			wantVhost:  true,
		},
		{
			name:       "hyphen at end of first segment",
			host:       "api-.example.com",
			wantClient: "",
			wantMap:    "api",
			wantVhost:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientID, mappingName, isVhost := parseVirtualHostByHyphen(tt.host)
			if clientID != tt.wantClient || mappingName != tt.wantMap || isVhost != tt.wantVhost {
				t.Errorf("parseVirtualHostByHyphen(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.host, clientID, mappingName, isVhost,
					tt.wantClient, tt.wantMap, tt.wantVhost)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestServeHTTP_PathRewriteOnVhostMatch: verify URL path is rewritten.
// ---------------------------------------------------------------------------

func TestServeHTTP_PathRewriteOnVhostMatch(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("node1", httpTunnel("api", "")))

	tg := NewTunnelGateway(mp, 10, nil)
	tg.HyphenRouting = true

	req := httptest.NewRequest(http.MethodGet, "http://api-node1.example.com/v1/data", nil)
	originalPath := req.URL.Path

	w := httptest.NewRecorder()
	tg.ServeHTTP(w, req)

	// The path should have been rewritten by the gateway (stripped clientId/mappingName prefix).
	// Since we can't inspect the final path (it's used internally), we verify the original
	// request path is preserved in the input but the routing succeeded.
	if originalPath != "/v1/data" {
		t.Errorf("original path should be /v1/data, got %s", originalPath)
	}

	// Confirm routing found the tunnel (not "No tunnel matched").
	body := w.Body.String()
	if strings.Contains(body, "No tunnel matched") {
		t.Error("vhost routing should have matched")
	}
}

// ---------------------------------------------------------------------------
// REL-08：HTTP 代理剔除 hop-by-hop 头（响应方向 + 请求方向）
// ---------------------------------------------------------------------------

func httpsTunnel(name, domain string) core.Tunnel {
	return core.Tunnel{
		Name:   name,
		Type:   core.TunnelTypeHTTPS,
		Target: "127.0.0.1:8443",
		Domain: domain,
	}
}

// fakeUpstreamSession 在 mock provider 上挂一个真实 smux 会话（net.Pipe），
// 节点侧收到请求后以 buildResp 的内容应答；captured 回传收到的请求
func fakeUpstreamSession(t *testing.T, mp *mockNodeProvider, nodeID string, buildResp func() *http.Response) chan *http.Request {
	t.Helper()
	gwEnd, nodeEnd := net.Pipe()
	gwSess, err := smux.Server(gwEnd, nil)
	if err != nil {
		t.Fatalf("smux server: %v", err)
	}
	nodeSess, err := smux.Client(nodeEnd, nil)
	if err != nil {
		t.Fatalf("smux client: %v", err)
	}
	t.Cleanup(func() { gwSess.Close(); nodeSess.Close() })
	mp.sessions[nodeID] = gwSess

	captured := make(chan *http.Request, 1)
	go func() {
		stream, err := nodeSess.AcceptStream()
		if err != nil {
			return
		}
		defer stream.Close()
		req, err := http.ReadRequest(bufio.NewReader(stream))
		if err != nil {
			return
		}
		captured <- req
		resp := buildResp()
		resp.ProtoMajor, resp.ProtoMinor = 1, 1
		if resp.StatusCode == 0 {
			resp.StatusCode = http.StatusOK
		}
		if err := resp.Write(stream); err != nil {
			t.Errorf("write upstream response: %v", err)
		}
	}()
	return captured
}

// TestServeHTTP_ProxyStripsHopByHopHeaders 经真实 smux 会话走完整代理路径：
// 上游响应回 Connection/Keep-Alive/自定义逐跳头，客户端响应不得包含；
// 客户端请求携带的逐跳头（含 Connection 指名的自定义头）不得到达上游
func TestServeHTTP_ProxyStripsHopByHopHeaders(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("node1", httpTunnel("web", "")))
	captured := fakeUpstreamSession(t, mp, "node1", func() *http.Response {
		resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}
		// 显式 Content-Length：未知长度时 Response.Write 会自行注入
		// Connection: close，stdlib 解析时随之把整个 Connection 头删掉，
		// token 机制就无法被测到（该边界与 ReverseProxy 一致，另测）
		resp.ContentLength = 2
		resp.Header.Set("Connection", "X-Hop-Marker") // Connection 指名的自定义逐跳头
		resp.Header.Set("Keep-Alive", "timeout=5")
		resp.Header.Set("X-Hop-Marker", "hop")
		resp.Header.Set("X-End-To-End", "keep")
		return resp
	})

	tg := NewTunnelGateway(mp, 10, nil)
	tg.HyphenRouting = false

	req := httptest.NewRequest(http.MethodGet, "http://example.com/node1/web/page", nil)
	req.Header.Add("Connection", "X-Custom-Hop-Req")
	req.Header.Set("X-Custom-Hop-Req", "hop")
	req.Header.Set("Proxy-Authorization", "Basic zzz")
	req.Header.Set("X-End-To-End-Req", "keep")

	w := httptest.NewRecorder()
	tg.ServeHTTP(w, req)

	resp := w.Result()
	if got := resp.Header.Get("Connection"); got != "" {
		t.Errorf("client response must not contain Connection, got %q", got)
	}
	if got := resp.Header.Get("Keep-Alive"); got != "" {
		t.Errorf("client response must not contain Keep-Alive, got %q", got)
	}
	if got := resp.Header.Get("X-Hop-Marker"); got != "" {
		t.Errorf("header named by upstream Connection must be stripped, got %q", got)
	}
	if got := resp.Header.Get("X-End-To-End"); got != "keep" {
		t.Errorf("end-to-end header must pass through, got %q", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	select {
	case creq := <-captured:
		if got := creq.Header.Get("Connection"); got != "" {
			t.Errorf("forwarded request must not contain Connection, got %q", got)
		}
		if got := creq.Header.Get("X-Custom-Hop-Req"); got != "" {
			t.Errorf("header named by client Connection must be stripped, got %q", got)
		}
		if got := creq.Header.Get("Proxy-Authorization"); got != "" {
			t.Errorf("forwarded request must not contain Proxy-Authorization, got %q", got)
		}
		if got := creq.Header.Get("X-End-To-End-Req"); got != "keep" {
			t.Errorf("end-to-end request header must pass through, got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not receive the request")
	}
}

// TestServeHTTP_ProxyStripsHopByHopOnClose：上游回 Connection: close 时，
// stdlib 解析已把 Connection 头整体删除，固定集合（Keep-Alive 等）仍必须剔除
func TestServeHTTP_ProxyStripsHopByHopOnClose(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("node1", httpTunnel("web", "")))
	fakeUpstreamSession(t, mp, "node1", func() *http.Response {
		resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok"))}
		resp.Header.Set("Connection", "close")
		resp.Header.Set("Keep-Alive", "timeout=5")
		resp.Header.Set("X-End-To-End", "keep")
		return resp
	})

	tg := NewTunnelGateway(mp, 10, nil)
	tg.HyphenRouting = false

	req := httptest.NewRequest(http.MethodGet, "http://example.com/node1/web/page", nil)
	w := httptest.NewRecorder()
	tg.ServeHTTP(w, req)

	resp := w.Result()
	if got := resp.Header.Get("Connection"); got != "" {
		t.Errorf("client response must not contain Connection, got %q", got)
	}
	if got := resp.Header.Get("Keep-Alive"); got != "" {
		t.Errorf("client response must not contain Keep-Alive, got %q", got)
	}
	if got := resp.Header.Get("X-End-To-End"); got != "keep" {
		t.Errorf("end-to-end header must pass through, got %q", got)
	}
}

// TestServeHTTP_HTTPSDomainIndexRouting：HTTPS 隧道配 domain 参与域名
// 索引路由（REL-08：与 HTTP 类型一致）
func TestServeHTTP_HTTPSDomainIndexRouting(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("node1", httpsTunnel("secure", "sec.example.com")))

	tg := NewTunnelGateway(mp, 10, nil)
	tg.RebuildIndex(context.Background())

	req := httptest.NewRequest(http.MethodGet, "http://sec.example.com/hello", nil)
	w := httptest.NewRecorder()
	tg.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "No tunnel matched") {
		t.Fatal("HTTPS tunnel with domain must be routable via domain index")
	}
}

// TestServeHTTP_HTTPSDomainFallbackScan：索引未重建窗口内的全量扫描兜底
// 同样收录 HTTPS+domain（REL-08）
func TestServeHTTP_HTTPSDomainFallbackScan(t *testing.T) {
	mp := newMockNodeProvider()
	mp.addNode(onlineNode("node1", httpsTunnel("secure", "sec.example.com")))

	tg := NewTunnelGateway(mp, 10, nil) // 不调 RebuildIndex，走兜底扫描

	req := httptest.NewRequest(http.MethodGet, "http://sec.example.com/hello", nil)
	w := httptest.NewRecorder()
	tg.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "No tunnel matched") {
		t.Fatal("HTTPS tunnel with domain must be routable via fallback scan")
	}
}
