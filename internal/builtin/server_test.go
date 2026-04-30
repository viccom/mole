package builtin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	moleAgent_client "moleAgent_client"
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
	registerTunnelAPI(mux, client)
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
