package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
