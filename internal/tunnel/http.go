package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/core"
)

// HyphenRouting 是否使用 hyphen(-) 作为泛域名分隔符，false 则使用 dot(.)
var HyphenRouting = true

// isIPAddress 检查字符串是否是有效的IP地址
func isIPAddress(host string) bool {
	return net.ParseIP(host) != nil
}

// parseVirtualHostByHyphen 解析泛域名访问[mappingName和clientId以-分割的域名]
// 例如: api-idabc001.example.com -> clientId="idabc001", mappingName="api"
func parseVirtualHostByHyphen(host string) (clientId, mappingName string, isVhost bool) {
	// 去掉端口
	if colonIndex := strings.Index(host, ":"); colonIndex != -1 {
		host = host[:colonIndex]
	}

	// 如果是IP地址，不使用泛域名解析
	if isIPAddress(host) {
		return "", "", false
	}

	hostParts := strings.Split(host, ".")
	// 泛域名判断逻辑：
	// 1. 至少有2个部分 (如: api-node001.localhost 或 api-node001.example.com)
	// 2. 第一部分必须包含 hyphen 分隔符 (mappingName-clientId)
	// 3. 不是IP地址
	if len(hostParts) < 2 {
		return "", "", false
	}

	// 第一部分按 hyphen 分割: api-node001 -> [api, node001]
	VhostPartStr := hostParts[0]
	VhostParts := strings.Split(VhostPartStr, "-")
	if len(VhostParts) < 2 {
		return "", "", false
	}

	isVhost = true
	mappingName = VhostParts[0]
	clientId = VhostParts[1]
	return clientId, mappingName, true
}

// parseVirtualHost 解析泛域名访问[以.分割的域名]
// 例如: api.idabc001.example.com -> clientId="idabc001", mappingName="api"
func parseVirtualHost(host string) (clientId, mappingName string, isVhost bool) {
	// 去掉端口
	if colonIndex := strings.Index(host, ":"); colonIndex != -1 {
		host = host[:colonIndex]
	}

	// 如果是IP地址，不使用泛域名解析
	if isIPAddress(host) {
		return "", "", false
	}

	hostParts := strings.Split(host, ".")
	// 泛域名判断逻辑：
	// 1. 至少有3个部分 (如: api.idabc001.example.com)
	//    如果末段是 localhost，则必须正好3部分；否则大于3部分
	// 2. 不是IP地址
	if len(hostParts) == 3 && hostParts[2] == "localhost" {
		isVhost = true
	} else if len(hostParts) > 3 && hostParts[len(hostParts)-1] != "localhost" {
		isVhost = true
	} else {
		return "", "", false
	}

	if isVhost {
		mappingName = hostParts[0]
		clientId = hostParts[1]
	}
	return
}

// parsePathRoute 解析路径访问
// 例如: /idabc001/api/v1/sysinfo -> clientId="idabc001", mappingName="api"
func parsePathRoute(path string) (clientId, mappingName string, err error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid request path: %s, expected /clientId/mappingName/...", path)
	}

	clientId = parts[0]
	mappingName = parts[1]
	return clientId, mappingName, nil
}

// buildVirtualHostPath 构建泛域名访问的路径
// 例如: /api/v1 -> /api/
func buildVirtualHostPath(originalPath, mappingName string) string {
	if originalPath == "/" {
		return "/" + mappingName + "/"
	} else if strings.HasPrefix(originalPath, "/") {
		return "/" + mappingName + originalPath
	} else {
		return "/" + mappingName + "/" + originalPath
	}
}

// buildPathRoutePath 构建路径访问的路径
// 例如: /idabc001/api/v1 -> /api/v1
func buildPathRoutePath(originalPath string) string {
	parts := strings.Split(strings.Trim(originalPath, "/"), "/")
	// 去掉 clientId，只保留 mappingName 及后续
	if len(parts) < 2 {
		return "/"
	}
	return "/" + strings.Join(parts[1:], "/")
}

// RegisterHTTP 注册 HTTP 隧道网关
func (tg *TunnelGateway) RegisterHTTP(ctx context.Context, tunnel core.Tunnel) error {
	listenAddr := fmt.Sprintf(":%d", tunnel.ListenPort)
	if tunnel.ListenPort == 0 {
		listenAddr = "" // 使用共享的网关端口
	}

	if listenAddr != "" {
		listener, err := net.Listen("tcp", listenAddr)
		if err != nil {
			return fmt.Errorf("http listen %s: %w", listenAddr, err)
		}
		tg.registry.Register(tunnel.Name, listener)

		srv := &http.Server{
			Handler: tg,
		}
		go func() {
			<-ctx.Done()
			srv.Close()
			tg.registry.Unregister(tunnel.Name)
		}()
		go srv.Serve(listener)
		slog.Info("HTTP tunnel listening", "tunnel", tunnel.Name, "addr", listenAddr)
	}

	return nil
}

// ServeHTTP 实现 http.Handler，作为 HTTP 网关入口
func (tg *TunnelGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := tg.sem.Acquire(r.Context()); err != nil {
		http.Error(w, "Server overloaded", http.StatusServiceUnavailable)
		return
	}
	defer tg.sem.Release()

	var node *core.Node
	var tunnelName string

	// 1. 尝试泛域名解析
	var clientId, mappingName string
	var isVhost bool
	if HyphenRouting {
		clientId, mappingName, isVhost = parseVirtualHostByHyphen(r.Host)
	} else {
		clientId, mappingName, isVhost = parseVirtualHost(r.Host)
	}

	if isVhost {

		slog.Debug("Virtual host routing", "clientId", clientId, "mappingName", mappingName, "host", r.Host)

		// 查找节点
		node = tg.findNodeByID(r.Context(), clientId)
		if node != nil {
			// 在节点中查找 mappingName 对应的隧道
			for _, t := range node.Tunnels {
				if t.Name == mappingName {
					tunnelName = t.Name
					break
				}
			}
		}

		if tunnelName != "" {
			// 修正路径：去掉前缀 /{clientId}，保留 /{mappingName}/...
			r.URL.Path = buildPathRoutePath(r.URL.Path)
			slog.Debug("Virtual host path rewritten", "newPath", r.URL.Path)
		}
	} else {
		// 2. 尝试路径路由: /clientId/mappingName/...
		clientId, mappingName, err := parsePathRoute(r.URL.Path)
		if err == nil {
			slog.Debug("Path routing", "clientId", clientId, "mappingName", mappingName, "path", r.URL.Path)

			// 查找节点
			node = tg.findNodeByID(r.Context(), clientId)
			if node != nil {
				// 在节点中查找 mappingName 对应的隧道
				for _, t := range node.Tunnels {
					if t.Name == mappingName {
						tunnelName = t.Name
						break
					}
				}
			}

			if tunnelName != "" {
				// 修正路径：去掉前缀 /{clientId}，保留 /{mappingName}/...
				r.URL.Path = buildPathRoutePath(r.URL.Path)
				slog.Debug("Path route rewritten", "newPath", r.URL.Path)
			}
		}
	}

	// 3. 如果都没匹配到，尝试精确域名匹配（兼容旧模式）
	if node == nil || tunnelName == "" {
		host := r.Host
		if idx := strings.Index(host, ":"); idx != -1 {
			host = host[:idx]
		}

		nodes := tg.nodeMgr.GetAll(r.Context())
		for _, n := range nodes {
			if n.Status != core.NodeStatusOnline {
				continue
			}
			for _, t := range n.Tunnels {
				if t.Type == core.TunnelTypeHTTP && t.Domain == host {
					node = n
					tunnelName = t.Name
					break
				}
			}
			if node != nil {
				break
			}
		}
	}

	if node == nil || tunnelName == "" {
		http.Error(w, "No tunnel matched for: "+r.Host+r.URL.Path, http.StatusBadGateway)
		return
	}

	// 检查节点是否离线
	if node.Status != core.NodeStatusOnline {
		http.Error(w, "Node offline for tunnel: "+tunnelName, http.StatusBadGateway)
		return
	}

	// WebSocket 升级检测
	if isWebSocketRequest(r) {
		tg.handleWebSocketGateway(w, r, node, tunnelName)
		return
	}

	// HTTP 透传
	tg.handleHTTPProxy(w, r, node, tunnelName)
}

// findNodeByID 根据节点ID查找节点
func (tg *TunnelGateway) findNodeByID(ctx context.Context, nodeID string) *core.Node {
	node, ok := tg.nodeMgr.Get(ctx, nodeID)
	if !ok {
		return nil
	}
	if node.Status != core.NodeStatusOnline {
		return nil
	}
	return node
}

func (tg *TunnelGateway) handleHTTPProxy(w http.ResponseWriter, r *http.Request, node *core.Node, tunnelName string) {
	stream, err := node.YamuxSession.OpenStream()
	if err != nil {
		slog.Error("Failed to open stream for HTTP", "tunnel", tunnelName, "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	slog.Debug("HTTP request forwarded",
		"tunnel", tunnelName,
		"path", r.URL.Path,
		"nodeId", node.ID,
	)

	// 将 HTTP 请求写入 stream
	if err := r.Write(stream); err != nil {
		slog.Error("Failed to write request to stream", "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}

	// 读取后端响应
	br := bufio.NewReader(stream)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		slog.Error("Failed to read response from stream", "error", err)
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 复制响应头
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (tg *TunnelGateway) handleWebSocketGateway(w http.ResponseWriter, r *http.Request, node *core.Node, tunnelName string) {
	stream, err := node.YamuxSession.OpenStream()
	if err != nil {
		slog.Error("Failed to open stream for WS", "tunnel", tunnelName, "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	// 发送升级请求到节点
	if err := r.Write(stream); err != nil {
		http.Error(w, "Failed to forward upgrade", http.StatusBadGateway)
		return
	}

	// 读取升级响应
	br := bufio.NewReader(stream)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// Hijack
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Hijack failed", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// 发送响应到浏览器
	resp.Write(clientConn)

	slog.Debug("WebSocket connected", "tunnel", tunnelName, "nodeId", node.ID)

	// 双向转发
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(stream, clientConn)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(clientConn, stream)
	}()
	<-done
	<-done
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket" &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}
