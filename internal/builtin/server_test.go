package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	moleAgent_client "moleAgent_client"
	"moleAgent_client/internal/proxy/ser2net"
)

func newTestClient(t *testing.T, tunnels []moleAgent_client.Tunnel) *moleAgent_client.Client {
	t.Helper()
	cfg := moleAgent_client.DefaultConfig()
	cfg.NodeID = "node0001"
	cfg.NodeName = "node0001"
	cfg.Tunnels = append([]moleAgent_client.Tunnel(nil), tunnels...)

	client, err := moleAgent_client.New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

func newTestMux(client *moleAgent_client.Client) *http.ServeMux {
	mux := http.NewServeMux()
	registerTunnelAPI(mux, func() *moleAgent_client.Client { return client })
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(client.Stats())
	})
	return mux
}

func TestStatusAPIRejectsNonGET(t *testing.T) {
	client := newTestClient(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	rec := httptest.NewRecorder()

	newTestMux(client).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestTunnelAPIListAndDetail(t *testing.T) {
	client := newTestClient(t, []moleAgent_client.Tunnel{
		{Name: "alpha", Type: moleAgent_client.TunnelTypeHTTP, Target: "127.0.0.1:8080"},
		{Name: "beta", Type: moleAgent_client.TunnelTypeVPNMgr, Target: "easytier-core"},
	})
	mux := newTestMux(client)

	listReq := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("list status code = %d, want %d", listRec.Code, http.StatusOK)
	}

	var gotList []moleAgent_client.TunnelStatus
	if err := json.Unmarshal(listRec.Body.Bytes(), &gotList); err != nil {
		t.Fatalf("list json error = %v", err)
	}
	if len(gotList) != 2 {
		t.Fatalf("list len = %d, want 2", len(gotList))
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/alpha", nil)
	detailRec := httptest.NewRecorder()
	mux.ServeHTTP(detailRec, detailReq)

	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status code = %d, want %d", detailRec.Code, http.StatusOK)
	}

	var got moleAgent_client.TunnelStatus
	if err := json.Unmarshal(detailRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("detail json error = %v", err)
	}
	if got.Name != "alpha" || got.Target != "127.0.0.1:8080" {
		t.Fatalf("detail = %#v", got)
	}
}

func TestTunnelAPIDeleteMissingTunnelReturnsNotFound(t *testing.T) {
	client := newTestClient(t, []moleAgent_client.Tunnel{
		{Name: "alpha", Type: moleAgent_client.TunnelTypeHTTP, Target: "127.0.0.1:8080"},
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/tunnels/missing", nil)
	rec := httptest.NewRecorder()

	newTestMux(client).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTunnelActionRejectsWrongMethod(t *testing.T) {
	client := newTestClient(t, []moleAgent_client.Tunnel{
		{Name: "vpn-a", Type: moleAgent_client.TunnelTypeVPNMgr, Target: "easytier-core"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/tunnels/vpn-a/start", nil)
	rec := httptest.NewRecorder()

	newTestMux(client).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestNewHandlerStatusAPIIncludesCORSHeaders(t *testing.T) {
	client := newTestClient(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Origin", "wails://wails")
	rec := httptest.NewRecorder()

	NewHandler(func() *moleAgent_client.Client { return client }).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
}

func TestNewHandlerTunnelsAPIIncludesCORSHeaders(t *testing.T) {
	client := newTestClient(t, []moleAgent_client.Tunnel{
		{Name: "alpha", Type: moleAgent_client.TunnelTypeHTTP, Target: "127.0.0.1:8080"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	req.Header.Set("Origin", "wails://wails")
	rec := httptest.NewRecorder()

	NewHandler(func() *moleAgent_client.Client { return client }).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
}

// /api/version 携带 p2p 构建能力标志（值随构建标签变化，断言键存在且为布尔）
func TestVersionAPIIncludesP2PCapability(t *testing.T) {
	client := newTestClient(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rec := httptest.NewRecorder()

	NewHandler(func() *moleAgent_client.Client { return client }).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal /api/version: %v", err)
	}
	if _, isBool := payload["p2p"].(bool); !isBool {
		t.Fatalf("/api/version must contain bool \"p2p\", got %T", payload["p2p"])
	}
}

func TestTunnelStreamSupportsSer2Net(t *testing.T) {
	client := newTestClient(t, []moleAgent_client.Tunnel{
		{
			Name: "s2n",
			Type: moleAgent_client.TunnelTypeSer2TCP,
			Target: "COM1",
			Para: json.RawMessage(`{"enable":true,"mode":"server","address":"127.0.0.1:0","serial":{"port":"COM1","baudrate":9600,"databits":8,"stopbits":1,"parity":"N","timeout":1000}}`),
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/tunnels/s2n/stream?tail=0", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		newTestMux(client).ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	client.Ser2NetStreamHub().Publish(ser2net.PacketInfo{
		Tunnel:  "s2n",
		Dir:     "TCP_IN",
		DataHex: "616263",
		DataLen: 3,
	})

	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q, want %q", got, "text/event-stream")
	}
	if !strings.Contains(body, `"tunnel":"s2n"`) {
		t.Fatalf("stream body missing ser2net event, body=%q", body)
	}
}
