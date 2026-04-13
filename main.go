package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_client/internal/protocol"
)

// ===== 内置 HTTP Server =====

func startBuiltinHTTP(addr string, c *client) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message":   "Hello from moleAgent_client",
			"method":    r.Method,
			"path":      r.URL.Path,
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"echo":      string(body),
			"method":    r.Method,
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	// 动态隧道管理 API
	registerTunnelAPI(mux, c)

	log.Printf("Built-in HTTP server listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// registerTunnelAPI 注册隧道管理 API
func registerTunnelAPI(mux *http.ServeMux, c *client) {
	// GET /api/tunnels — 查看当前隧道列表
	mux.HandleFunc("/api/tunnels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(c.cfg.Tunnels)
		case http.MethodPost:
			var newTunnel tunnelConfig
			if err := json.NewDecoder(r.Body).Decode(&newTunnel); err != nil {
				http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
				return
			}
			if newTunnel.Name == "" || newTunnel.Type == "" || newTunnel.Target == "" {
				http.Error(w, `{"error":"name, type, target are required"}`, http.StatusBadRequest)
				return
			}
			// 构建新的隧道列表：追加或替换同名隧道
			updated := make([]tunnelConfig, 0, len(c.cfg.Tunnels)+1)
			replaced := false
			for _, t := range c.cfg.Tunnels {
				if t.Name == newTunnel.Name {
					updated = append(updated, newTunnel)
					replaced = true
				} else {
					updated = append(updated, t)
				}
			}
			if !replaced {
				updated = append(updated, newTunnel)
			}
			if err := c.requestTunnelUpdate(updated); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "tunnels": len(updated)})
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
		updated := make([]tunnelConfig, 0, len(c.cfg.Tunnels))
		found := false
		for _, t := range c.cfg.Tunnels {
			if t.Name == name {
				found = true
				continue
			}
			updated = append(updated, t)
		}
		if !found {
			http.Error(w, fmt.Sprintf(`{"error":"tunnel %q not found"}`, name), http.StatusNotFound)
			return
		}
		if err := c.requestTunnelUpdate(updated); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "tunnels": len(updated)})
	})
}

// ===== 配置 =====

type tunnelConfig struct {
	Name       string `json:"name"`
	Type       string `json:"type"`        // http, tcp, udp
	Target     string `json:"target"`      // 后端地址
	Domain     string `json:"domain"`      // HTTP 隧道域名
	ListenPort int    `json:"listen_port"` // TCP/UDP 监听端口
}

type config struct {
	ServerAddr string         `json:"server_addr"`
	Token      string         `json:"token"`
	NodeID     string         `json:"node_id"`
	NodeName   string         `json:"node_name"`
	UseTLS     bool           `json:"tls"`
	HTTPPort   string         `json:"http_port"` // 内置 HTTP 端口
	Tunnels    []tunnelConfig `json:"tunnels"`
}

func defaultConfig() *config {
	return &config{
		ServerAddr: "127.0.0.1:9981",
		Token:      "default-node-token-change-me",
		HTTPPort:   "127.0.0.1:18080",
	}
}

func loadConfigFile(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := defaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ===== 客户端核心 =====

type tunnelUpdateReq struct {
	tunnels []tunnelConfig
	resp    chan error
}

type client struct {
	cfg           *config
	session       *smux.Session
	conn          net.Conn
	mu            sync.Mutex
	tunnelUpdates chan tunnelUpdateReq
}

func newClient(cfg *config) *client {
	return &client{
		cfg:           cfg,
		tunnelUpdates: make(chan tunnelUpdateReq, 16),
	}
}

// requestTunnelUpdate 通过通道请求更新隧道（线程安全）
func (c *client) requestTunnelUpdate(tunnels []tunnelConfig) error {
	req := tunnelUpdateReq{
		tunnels: tunnels,
		resp:    make(chan error, 1),
	}
	select {
	case c.tunnelUpdates <- req:
		return <-req.resp
	default:
		return fmt.Errorf("tunnel update queue full")
	}
}

// processTunnelUpdates 处理隧道更新请求
func (c *client) processTunnelUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-c.tunnelUpdates:
			req.resp <- c.updateTunnels(req.tunnels)
		}
	}
}

// connect 连接、认证、建立 smux 会话
func (c *client) connect() error {
	// 1. TCP 连接
	var conn net.Conn
	var err error
	if c.cfg.UseTLS {
		tlsCfg := &tls.Config{InsecureSkipVerify: true}
		conn, err = tls.Dial("tcp", c.cfg.ServerAddr, tlsCfg)
	} else {
		conn, err = net.DialTimeout("tcp", c.cfg.ServerAddr, 10*time.Second)
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	c.conn = conn

	// 2. Challenge-Response 认证
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	challenge := make([]byte, 32)
	if _, err := io.ReadFull(conn, challenge); err != nil {
		conn.Close()
		return fmt.Errorf("read challenge: %w", err)
	}

	authMsg, _ := json.Marshal(map[string]string{"token": c.cfg.Token})
	if _, err := conn.Write(append(authMsg, '\n')); err != nil {
		conn.Close()
		return fmt.Errorf("send auth: %w", err)
	}

	reader := bufio.NewReader(conn)
	authResp, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("read auth response: %w", err)
	}
	conn.SetReadDeadline(time.Time{})

	var authResult struct {
		Cmd string `json:"cmd"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(authResp)), &authResult); err != nil {
		conn.Close()
		return fmt.Errorf("parse auth response: %w", err)
	}
	if authResult.Cmd != "ok" {
		conn.Close()
		return fmt.Errorf("auth failed: %s", authResult.Msg)
	}
	log.Printf("Authenticated to %s", c.cfg.ServerAddr)

	// 3. 建立 smux 会话
	smuxConfig := smux.DefaultConfig()
	smuxConfig.Version = 2
	smuxConfig.KeepAliveInterval = 30 * time.Second
	smuxConfig.KeepAliveTimeout = 90 * time.Second
	smuxConfig.MaxFrameSize = 32768
	smuxConfig.MaxReceiveBuffer = 4194304
	smuxConfig.MaxStreamBuffer = 256 * 1024

	session, err := smux.Client(conn, smuxConfig)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smux client: %w", err)
	}
	c.session = session

	return nil
}

// register 注册隧道
func (c *client) register() error {
	stream, err := c.session.OpenStream()
	if err != nil {
		return fmt.Errorf("open register stream: %w", err)
	}
	defer stream.Close()

	tunnels := make([]protocol.Tunnel, len(c.cfg.Tunnels))
	for i, t := range c.cfg.Tunnels {
		tunnels[i] = protocol.Tunnel{
			Name:       t.Name,
			Type:       protocol.TunnelType(t.Type),
			Target:     t.Target,
			Domain:     t.Domain,
			ListenPort: t.ListenPort,
		}
	}

	cmd := protocol.ControlCmd{
		Cmd:     "register",
		NodeID:  c.cfg.NodeID,
		Name:    c.cfg.NodeName,
		Tunnels: tunnels,
	}
	data, _ := json.Marshal(cmd)
	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	buf := make([]byte, 4096)
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := stream.Read(buf)
	if err != nil {
		return fmt.Errorf("read register response: %w", err)
	}

	var resp protocol.ControlResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return fmt.Errorf("parse register response: %w", err)
	}
	if resp.Cmd != "ok" {
		return fmt.Errorf("register failed: %s", resp.Msg)
	}

	log.Printf("Registered as node %q with %d tunnel(s)", c.cfg.NodeID, len(tunnels))
	return nil
}

// updateTunnels 向服务端发送 tunnel_update 命令动态更新隧道配置
func (c *client) updateTunnels(tunnels []tunnelConfig) error {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session == nil || session.IsClosed() {
		return fmt.Errorf("session not available")
	}

	stream, err := session.OpenStream()
	if err != nil {
		return fmt.Errorf("open update stream: %w", err)
	}
	defer stream.Close()

	protoTunnels := make([]protocol.Tunnel, len(tunnels))
	for i, t := range tunnels {
		protoTunnels[i] = protocol.Tunnel{
			Name:       t.Name,
			Type:       protocol.TunnelType(t.Type),
			Target:     t.Target,
			Domain:     t.Domain,
			ListenPort: t.ListenPort,
		}
	}

	cmd := protocol.ControlCmd{
		Cmd:     "tunnel_update",
		NodeID:  c.cfg.NodeID,
		Tunnels: protoTunnels,
	}
	data, _ := json.Marshal(cmd)
	if _, err := stream.Write(data); err != nil {
		return fmt.Errorf("send tunnel_update: %w", err)
	}

	buf := make([]byte, 4096)
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := stream.Read(buf)
	if err != nil {
		return fmt.Errorf("read tunnel_update response: %w", err)
	}

	var resp protocol.ControlResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return fmt.Errorf("parse tunnel_update response: %w", err)
	}
	if resp.Cmd != "ok" {
		return fmt.Errorf("tunnel_update failed: %s", resp.Msg)
	}

	// 更新本地配置
	c.cfg.Tunnels = make([]tunnelConfig, len(tunnels))
	copy(c.cfg.Tunnels, tunnels)

	log.Printf("Tunnels updated: %d tunnel(s)", len(tunnels))
	for _, t := range c.cfg.Tunnels {
		log.Printf("  - %s (%s) → %s domain=%s", t.Name, t.Type, t.Target, t.Domain)
	}
	return nil
}

// heartbeat 心跳循环
func (c *client) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			session := c.session
			c.mu.Unlock()
			if session == nil || session.IsClosed() {
				return
			}
			stream, err := session.OpenStream()
			if err != nil {
				log.Printf("Heartbeat open stream failed: %v", err)
				return
			}
			cmd, _ := json.Marshal(protocol.ControlCmd{Cmd: "ping"})
			stream.Write(cmd)
			buf := make([]byte, 256)
			stream.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, _ := stream.Read(buf)
			stream.Close()
			var resp protocol.ControlResponse
			if err := json.Unmarshal(buf[:n], &resp); err == nil && resp.Cmd == "pong" {
				log.Printf("Heartbeat OK")
			}
		}
	}
}

// handleStream 处理隧道数据流
func (c *client) handleStream(stream *smux.Stream) {
	defer stream.Close()

	// 根据注册的隧道类型判断如何处理
	// 尝试作为 HTTP 请求读取
	bufReader := bufio.NewReader(stream)
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))

	req, err := http.ReadRequest(bufReader)
	if err == nil {
		// HTTP/WebSocket 请求
		stream.SetReadDeadline(time.Time{})
		c.handleHTTPStream(stream, bufReader, req)
		return
	}

	// 非 HTTP，作为 TCP/UDP 原始数据转发
	stream.SetReadDeadline(time.Time{})
	c.handleRawStream(stream)
}

func (c *client) handleHTTPStream(stream *smux.Stream, br *bufio.Reader, req *http.Request) {
	// 根据 Host 头匹配隧道目标，优先匹配 Domain，再 fallback 到第一个 HTTP 隧道
	host := strings.TrimSuffix(req.Host, ":80")
	var target string
	for _, t := range c.cfg.Tunnels {
		if t.Type == "http" && t.Domain != "" && t.Domain == host {
			target = t.Target
			break
		}
	}
	if target == "" {
		for _, t := range c.cfg.Tunnels {
			if t.Type == "http" {
				target = t.Target
				break
			}
		}
	}
	if target == "" {
		resp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("no tunnel matched"))}
		resp.Write(stream)
		return
	}

	// 转发到本地后端
	proxyReq, err := http.NewRequest(req.Method, target+req.URL.Path+"?"+req.URL.RawQuery, req.Body)
	if err != nil {
		resp := &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("bad request"))}
		resp.Write(stream)
		return
	}
	for k, vv := range req.Header {
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}

	// WebSocket 检测
	isWS := strings.ToLower(req.Header.Get("Upgrade")) == "websocket"
	if isWS {
		c.handleWebSocketProxy(stream, req, target)
		return
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		errResp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("backend error: " + err.Error()))}
		errResp.Write(stream)
		return
	}
	defer resp.Body.Close()
	resp.Write(stream)
}

func (c *client) handleWebSocketProxy(stream *smux.Stream, req *http.Request, target string) {
	// 解析目标地址
	u, err := parseURL(target)
	if err != nil {
		return
	}

	var backendConn net.Conn
	if strings.HasPrefix(target, "https://") {
		backendConn, err = tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", u, &tls.Config{InsecureSkipVerify: true})
	} else {
		backendConn, err = net.DialTimeout("tcp", u, 10*time.Second)
	}
	if err != nil {
		return
	}
	defer backendConn.Close()

	// 转发升级请求
	req.Write(backendConn)

	// 读取 101 响应
	br := bufio.NewReader(backendConn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return
	}
	resp.Write(stream)

	// 双向转发
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(backendConn, stream)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(stream, br)
	}()
	<-done
	<-done
}

func (c *client) handleRawStream(stream *smux.Stream) {
	// TCP/UDP 转发：找到第一个 tcp/udp 隧道目标
	var target string
	for _, t := range c.cfg.Tunnels {
		if t.Type == "tcp" || t.Type == "udp" {
			target = t.Target
			break
		}
	}
	if target == "" {
		return
	}

	backendConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		log.Printf("Dial backend %s failed: %v", target, err)
		return
	}
	defer backendConn.Close()

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(backendConn, stream)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(stream, backendConn)
	}()
	<-done
	<-done
}

// run 主循环（含自动重连）
func (c *client) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := c.connect(); err != nil {
			log.Printf("Connect failed: %v, retrying in 5s...", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if err := c.register(); err != nil {
			log.Printf("Register failed: %v, retrying in 5s...", err)
			c.close()
			time.Sleep(5 * time.Second)
			continue
		}

		// 启动心跳
		hbCtx, hbCancel := context.WithCancel(ctx)
		go c.heartbeat(hbCtx)

		// 启动隧道更新处理
		updateCtx, updateCancel := context.WithCancel(ctx)
		go c.processTunnelUpdates(updateCtx)

		// 接受数据流
		for {
			stream, err := c.session.AcceptStream()
			if err != nil {
				log.Printf("AcceptStream error: %v", err)
				break
			}
			go c.handleStream(stream)
		}

		updateCancel()
		hbCancel()
		c.close()
		log.Printf("Disconnected, reconnecting in 5s...")
		time.Sleep(5 * time.Second)
	}
}

func (c *client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		c.session.Close()
		c.session = nil
	}
}

func parseURL(raw string) (string, error) {
	// ws://host:port/path → host:port
	s := strings.TrimPrefix(raw, "ws://")
	s = strings.TrimPrefix(s, "wss://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	if idx := strings.Index(s, "/"); idx > 0 {
		s = s[:idx]
	}
	return s, nil
}

// ===== 入口 =====

func main() {
	cfgFlag := flag.String("config", "", "配置文件路径 (JSON)")
	serverFlag := flag.String("server", "", "服务端地址 (默认 127.0.0.1:9981)")
	tokenFlag := flag.String("token", "", "节点认证令牌")
	nodeIDFlag := flag.String("id", "", "节点 ID (默认自动生成)")
	nameFlag := flag.String("name", "", "节点名称")
	tlsFlag := flag.Bool("tls", false, "启用 TLS")
	httpPortFlag := flag.String("http", "", "内置 HTTP 服务端口 (默认 127.0.0.1:18080, 设为 off 关闭)")
	flag.Parse()

	// 加载配置
	var cfg *config
	if *cfgFlag != "" {
		var err error
		cfg, err = loadConfigFile(*cfgFlag)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
	} else {
		cfg = defaultConfig()
	}

	// 命令行参数覆盖
	if *serverFlag != "" {
		cfg.ServerAddr = *serverFlag
	}
	if *tokenFlag != "" {
		cfg.Token = *tokenFlag
	}
	if *nodeIDFlag != "" {
		cfg.NodeID = *nodeIDFlag
	} else if cfg.NodeID == "" {
		cfg.NodeID = generateNodeID()
	}
	if *nameFlag != "" {
		cfg.NodeName = *nameFlag
	} else if cfg.NodeName == "" {
		cfg.NodeName = cfg.NodeID
	}
	if *tlsFlag {
		cfg.UseTLS = true
	}
	if *httpPortFlag != "" {
		cfg.HTTPPort = *httpPortFlag
	}

	// 自动生成默认隧道配置
	if len(cfg.Tunnels) == 0 && cfg.HTTPPort != "off" {
		cfg.Tunnels = []tunnelConfig{
			{
				Name:   "web",
				Type:   "http",
				Target: "http://" + cfg.HTTPPort,
			},
		}
	}

	log.Printf("moleAgent_client starting...")
	log.Printf("  Node ID:  %s", cfg.NodeID)
	log.Printf("  Server:   %s (TLS=%v)", cfg.ServerAddr, cfg.UseTLS)
	log.Printf("  Tunnels:  %d", len(cfg.Tunnels))
	for _, t := range cfg.Tunnels {
		log.Printf("    - %s (%s) → %s", t.Name, t.Type, t.Target)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动客户端
	c := newClient(cfg)
	go c.run(ctx)

	// 启动内置 HTTP 服务（含隧道管理 API）
	if cfg.HTTPPort != "off" {
		go func() {
			if err := startBuiltinHTTP(cfg.HTTPPort, c); err != nil {
				log.Fatalf("Built-in HTTP server error: %v", err)
			}
		}()
	}

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("Received %s, shutting down...", sig)
	cancel()
	c.close()
	log.Println("Stopped.")
}

func generateNodeID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("node-%x", b)
}
