package builtin

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"moleAgent_client"
	"moleAgent_client/internal/proxy/ser2mq"
	"moleAgent_client/internal/proxy/vpn"
)

//go:embed static
var staticFS embed.FS

// StartHTTPServer 启动内置 HTTP 服务（含隧道管理 API）
func StartHTTPServer(addr string, client *moleAgent_client.Client) error {
	// 绑定所有网卡：localhost:18080 → 0.0.0.0:18080
	if host, port, err := net.SplitHostPort(addr); err == nil && host != "" && host != "0.0.0.0" {
		addr = "0.0.0.0:" + port
	}
	mux := http.NewServeMux()

	// 静态文件（单页面 dashboard）
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.Handle("/ui/", http.FileServer(http.FS(staticFS)))

	// 默认首页
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message":   "Hello from moleAgent_client",
			"method":    r.Method,
			"path":      r.URL.Path,
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	// Echo 端点
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"echo":      string(body),
			"method":    r.Method,
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	// 隧道管理 API
	registerTunnelAPI(mux, client)
	registerVPNAPI(mux, client)
	registerSer2MQAPI(mux, client)

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(client.Stats())
	})

	log.Printf("Built-in HTTP server listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

func registerTunnelAPI(mux *http.ServeMux, c *moleAgent_client.Client) {
	// GET /api/tunnels — 查看当前隧道列表
	// POST /api/tunnels — 添加/更新隧道
	mux.HandleFunc("/api/tunnels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(c.Tunnels())
		case http.MethodPost:
			var t moleAgent_client.Tunnel
			if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			if err := c.AddTunnel(t); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})

	// DELETE /api/tunnels/{name} — 移除指定隧道
	mux.HandleFunc("/api/tunnels/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
		if name == "" {
			http.Error(w, `{"error":"tunnel name required"}`, http.StatusBadRequest)
			return
		}
		if err := c.RemoveTunnel(name); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), status)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
}

// registerVPNAPI 注册 VPN 管理 API
func registerVPNAPI(mux *http.ServeMux, client *moleAgent_client.Client) {
	// GET /api/vpn — 列出所有 VPN 实例
	mux.HandleFunc("/api/vpn", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(client.VPNList())
	})

	// GET /api/vpn/:name/status — 查询状态
	mux.HandleFunc("/api/vpn/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		name := strings.TrimPrefix(r.URL.Path, "/api/vpn/")

		// /api/vpn/:name/logs
		if strings.HasSuffix(name, "/logs") {
			tunnelName := strings.TrimSuffix(name, "/logs")
			logs, err := client.VPNCrashLogs(tunnelName)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(logs)
			return
		}

		// /api/vpn/:name/start
		if strings.HasSuffix(name, "/start") {
			tunnelName := strings.TrimSuffix(name, "/start")
			if err := client.VPNStart(tunnelName); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
			return
		}

		// /api/vpn/:name/stop
		if strings.HasSuffix(name, "/stop") {
			tunnelName := strings.TrimSuffix(name, "/stop")
			if err := client.VPNStop(tunnelName); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
			return
		}

		// /api/vpn/:name/status
		status, err := client.VPNStatus(name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(status)
	})
}

// registerSer2MQAPI 注册 ser2mq API
func registerSer2MQAPI(mux *http.ServeMux, client *moleAgent_client.Client) {
	// GET /api/ser2mq — 列出所有 ser2mq 实例
	mux.HandleFunc("/api/ser2mq", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(client.Ser2MQList())
	})

	// GET /api/ser2mq/:name/status — 查询状态
	mux.HandleFunc("/api/ser2mq/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/ser2mq/")
		status, err := client.Ser2MQStatus(name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(status)
	})
}

// RegisterVPNAPI 注册 VPN API 到 server
func RegisterVPNAPI(mux *http.ServeMux, client *moleAgent_client.Client) {
	registerVPNAPI(mux, client)
}

// RegisterSer2MQAPI 注册 ser2mq API 到 server
func RegisterSer2MQAPI(mux *http.ServeMux, client *moleAgent_client.Client) {
	registerSer2MQAPI(mux, client)
}

// GetVPNManager 获取 VPN 管理器
func GetVPNManager(client *moleAgent_client.Client) *vpn.Manager {
	return client.VPNManager()
}

// GetSer2MQManager 获取 ser2mq 管理器
func GetSer2MQManager(client *moleAgent_client.Client) *ser2mq.Manager {
	return client.Ser2MQManager()
}