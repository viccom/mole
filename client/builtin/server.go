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
	"strconv"
	"strings"
	"time"

	"moleAgent_client"
	"moleAgent_client/internal/version"
)

//go:embed static
var staticFS embed.FS

// maxEchoBodyBytes /echo 请求体上限（1 MiB）：该端点只做回显，无业务需要大 body
const maxEchoBodyBytes = 1 << 20

// StartHTTPServer 启动内置 HTTP 服务（含隧道管理 API）
func StartHTTPServer(addr string, client moleAgent_client.BuiltinClient) error {
	// 仅端口形式（":18080"）绑定所有网卡；显式指定的 host（如默认 127.0.0.1）
	// 必须尊重——本服务无鉴权且隧道 Para 含凭据，静默改绑 0.0.0.0 会把凭据暴露给局域网
	if host, port, err := net.SplitHostPort(addr); err == nil && host == "" {
		addr = "0.0.0.0:" + port
	}
	log.Printf("Built-in HTTP server listening on %s", addr)
	return http.ListenAndServe(addr, NewHandler(func() moleAgent_client.BuiltinClient { return client }))
}

// NewHandler 创建内置 HTTP 服务 handler，允许调用方延迟提供当前 client。
func NewHandler(clientProvider func() moleAgent_client.BuiltinClient) http.Handler {
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

	// Echo 端点（请求体有上限：-http 支持纯端口写法并显式绑 0.0.0.0，
	// 无上限的 ReadAll 可被单个大请求打爆内存）
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEchoBodyBytes))
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
	registerTunnelAPI(mux, clientProvider)
	mux.HandleFunc("/api/check-update", withCORS(handleCheckUpdate))
	mux.HandleFunc("/api/self-update", withCORS(handleSelfUpdate))
	mux.HandleFunc("/api/update-progress", withCORS(handleUpdateProgress))

	mux.HandleFunc("/api/version", withCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode(version.GetSystemInfo())
	}))

	mux.HandleFunc("/api/status", withCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		client := currentClient(clientProvider)
		if client == nil {
			json.NewEncoder(w).Encode(map[string]any{
				"connected":   false,
				"node_id":     "",
				"server_addr": "",
				"tunnels":     []any{},
			})
			return
		}
		json.NewEncoder(w).Encode(client.Stats())
	}))
	return mux
}

func registerTunnelAPI(mux *http.ServeMux, clientProvider func() moleAgent_client.BuiltinClient) {
	// GET /api/tunnels — 所有隧道（含运行时状态）
	// POST /api/tunnels — 添加/更新隧道
	mux.HandleFunc("/api/tunnels", withCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		client := currentClient(clientProvider)
		switch r.Method {
		case http.MethodGet:
			if client == nil {
				json.NewEncoder(w).Encode([]any{})
				return
			}
			json.NewEncoder(w).Encode(client.AllTunnelStatus())
		case http.MethodPost:
			if client == nil {
				writeClientUnavailable(w)
				return
			}
			var t moleAgent_client.Tunnel
			if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			if err := client.AddTunnel(t); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
	}))

	// /api/tunnels/{name} — 单隧道查询/删除
	// /api/tunnels/{name}/{action} — 类型特定操作（start/stop/logs）
	mux.HandleFunc("/api/tunnels/", withCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
		if path == "" {
			client := currentClient(clientProvider)
			if r.Method == http.MethodGet {
				if client == nil {
					json.NewEncoder(w).Encode([]any{})
					return
				}
				json.NewEncoder(w).Encode(client.AllTunnelStatus())
				return
			}
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		// 检查子操作: /api/tunnels/:name/:action
		if idx := strings.Index(path, "/"); idx >= 0 {
			name := path[:idx]
			action := path[idx+1:]
			handleTunnelAction(w, r, clientProvider, name, action)
			return
		}

		// 单隧道: GET 查询 / DELETE 删除
		client := currentClient(clientProvider)
		switch r.Method {
		case http.MethodGet:
			if client == nil {
				writeClientUnavailable(w)
				return
			}
			status, err := client.TunnelStatusByName(path)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(status)
		case http.MethodDelete:
			if client == nil {
				writeClientUnavailable(w)
				return
			}
			if _, err := client.TunnelStatusByName(path); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
				return
			}
			if err := client.RemoveTunnel(path); err != nil {
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
	}))
}

// handleTunnelAction 处理隧道类型特定操作
func handleTunnelAction(w http.ResponseWriter, r *http.Request, clientProvider func() moleAgent_client.BuiltinClient, name, action string) {
	client := currentClient(clientProvider)
	if client == nil {
		writeClientUnavailable(w)
		return
	}
	switch action {
	case "start":
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if err := client.VPNStart(name); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	case "stop":
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if err := client.VPNStop(name); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	case "logs":
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		logs, err := client.VPNCrashLogs(name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(logs)
	case "peers":
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		_, peers, _, _, err := client.VPNVNTData(name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(peers)
	case "routes":
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		_, _, routes, _, err := client.VPNVNTData(name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(routes)
	case "chart":
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		chart, err := client.VPNVNTChart(name)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(chart)
	case "stream":
		handleTunnelStream(w, r, client, name)
	default:
		http.Error(w, fmt.Sprintf(`{"error":"unknown action: %s"}`, action), http.StatusBadRequest)
	}
}

// handleTunnelStream SSE 实时事件流（ser2mq / ser2net）
func handleTunnelStream(w http.ResponseWriter, r *http.Request, c moleAgent_client.BuiltinClient, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 校验隧道存在且类型支持 stream
	status, err := c.TunnelStatusByName(name)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusNotFound)
		return
	}
	if status.Type != moleAgent_client.TunnelTypeSer2MQ &&
		status.Type != moleAgent_client.TunnelTypeSer2TCP &&
		status.Type != moleAgent_client.TunnelTypeSer2UDP {
		http.Error(w, `{"error":"stream only supported for ser2mq/ser2net tunnels"}`, http.StatusBadRequest)
		return
	}

	// 解析 tail 参数
	tail := 20
	if t := r.URL.Query().Get("tail"); t != "" {
		if v, e := strconv.Atoi(t); e != nil || v < 0 {
			tail = 20
		} else {
			tail = v
		}
	}
	if tail > 200 {
		tail = 200
	}

	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, canFlush := w.(http.Flusher)

	encode := func(evt any) {
		data, err := json.Marshal(evt)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: packet\ndata: %s\n\n", data)
		if canFlush {
			flusher.Flush()
		}
	}

	if status.Type == moleAgent_client.TunnelTypeSer2MQ {
		hub := c.Ser2MQStreamHub()
		ch, unsub := hub.Subscribe(name, tail)
		defer unsub()

		for {
			select {
			case <-r.Context().Done():
				return
			case evt := <-ch:
				encode(evt)
			}
		}
	}

	hub := c.Ser2NetStreamHub()
	if hub == nil {
		http.Error(w, `{"error":"ser2net stream hub unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	ch, unsub := hub.Subscribe(name, tail)
	defer unsub()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-ch:
			encode(evt)
		}
	}
}

func currentClient(provider func() moleAgent_client.BuiltinClient) moleAgent_client.BuiltinClient {
	if provider == nil {
		return nil
	}
	return provider()
}

func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

func writeClientUnavailable(w http.ResponseWriter) {
	http.Error(w, `{"error":"client not connected"}`, http.StatusServiceUnavailable)
}
