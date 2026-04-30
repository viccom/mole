package builtin

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"moleAgent_client"
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

	// 静态文件（模块化前端）
	subFS, _ := fs.Sub(staticFS, "static")
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(subFS, "index.html")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(subFS))))

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

	// API
	registerTunnelAPI(mux, client)

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
	// GET /api/tunnels — 所有隧道（含运行时状态）
	// POST /api/tunnels — 添加/更新隧道
	mux.HandleFunc("/api/tunnels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(c.AllTunnelStatus())
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

	// /api/tunnels/{name} — 单隧道查询/删除
	// /api/tunnels/{name}/{action} — 类型特定操作（start/stop/logs）
	mux.HandleFunc("/api/tunnels/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
		if path == "" {
			if r.Method == http.MethodGet {
				json.NewEncoder(w).Encode(c.AllTunnelStatus())
				return
			}
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		// 检查子操作: /api/tunnels/:name/:action
		if idx := strings.Index(path, "/"); idx >= 0 {
			name := path[:idx]
			action := path[idx+1:]
			handleTunnelAction(w, r, c, name, action)
			return
		}

		// 单隧道: GET 查询 / DELETE 删除
		switch r.Method {
		case http.MethodGet:
			status, err := c.TunnelStatusByName(path)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(status)
		case http.MethodDelete:
			if err := c.RemoveTunnel(path); err != nil {
				code := http.StatusInternalServerError
				if strings.Contains(err.Error(), "not found") {
					code = http.StatusNotFound
				}
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), code)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	})
}

// handleTunnelAction 处理隧道类型特定操作
func handleTunnelAction(w http.ResponseWriter, r *http.Request, c *moleAgent_client.Client, name, action string) {
	switch action {
	case "start":
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if err := c.VPNStart(name); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	case "stop":
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if err := c.VPNStop(name); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	case "logs":
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		logs, err := c.VPNCrashLogs(name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(logs)
	default:
		http.Error(w, fmt.Sprintf(`{"error":"unknown action: %s"}`, action), http.StatusBadRequest)
	}
}