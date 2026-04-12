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

// parseVirtualHostByHyphen 解析泛域名访问[mappingName和clientId以-分割的域名]
func parseVirtualHostByHyphen(host string) (clientId, mappingName string, isVhost bool) {
	host = stripPort(host)
	if net.ParseIP(host) != nil {
		return "", "", false
	}

	hostParts := strings.Split(host, ".")
	if len(hostParts) < 2 {
		return "", "", false
	}

	vhostParts := strings.Split(hostParts[0], "-")
	if len(vhostParts) < 2 {
		return "", "", false
	}

	return vhostParts[1], vhostParts[0], true
}

// parseVirtualHost 解析泛域名访问[以.分割的域名]
func parseVirtualHost(host string) (clientId, mappingName string, isVhost bool) {
	host = stripPort(host)
	if net.ParseIP(host) != nil {
		return "", "", false
	}

	hostParts := strings.Split(host, ".")
	if len(hostParts) == 3 && hostParts[2] == "localhost" {
		isVhost = true
	} else if len(hostParts) > 3 && hostParts[len(hostParts)-1] != "localhost" {
		isVhost = true
	}

	if isVhost {
		mappingName = hostParts[0]
		clientId = hostParts[1]
	}
	return
}

// parsePathRoute 解析路径访问
func parsePathRoute(path string) (clientId, mappingName string, err error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid request path: %s, expected /clientId/mappingName/...", path)
	}
	return parts[0], parts[1], nil
}

// buildVirtualHostPath 构建泛域名访问的路径
func buildVirtualHostPath(originalPath, mappingName string) string {
	if originalPath == "/" {
		return "/" + mappingName + "/"
	}
	if strings.HasPrefix(originalPath, "/") {
		return "/" + mappingName + originalPath
	}
	return "/" + mappingName + "/" + originalPath
}

// buildPathRoutePath 构建路径访问的路径
func buildPathRoutePath(originalPath string) string {
	parts := strings.Split(strings.Trim(originalPath, "/"), "/")
	if len(parts) < 2 {
		return "/"
	}
	return "/" + strings.Join(parts[1:], "/")
}

// findNodeTunnel 在指定节点中查找隧道名匹配的隧道
func (tg *TunnelGateway) findNodeTunnel(ctx context.Context, nodeID, mappingName string) (*core.Node, string) {
	node, ok := tg.nodeMgr.Get(ctx, nodeID)
	if !ok || node.Status != core.NodeStatusOnline {
		return nil, ""
	}
	for _, t := range node.Tunnels {
		if t.Name == mappingName {
			return node, t.Name
		}
	}
	return nil, ""
}

// RegisterHTTP 注册 HTTP 隧道网关
func (tg *TunnelGateway) RegisterHTTP(ctx context.Context, tunnel core.Tunnel) error {
	listenAddr := fmt.Sprintf(":%d", tunnel.ListenPort)
	if tunnel.ListenPort == 0 {
		listenAddr = ""
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
		node, tunnelName = tg.findNodeTunnel(r.Context(), clientId, mappingName)
		if tunnelName != "" {
			r.URL.Path = buildPathRoutePath(r.URL.Path)
			slog.Debug("Virtual host path rewritten", "newPath", r.URL.Path)
		}
	} else {
		// 2. 尝试路径路由: /clientId/mappingName/...
		pathClientId, pathMappingName, err := parsePathRoute(r.URL.Path)
		if err == nil {
			slog.Debug("Path routing", "clientId", pathClientId, "mappingName", pathMappingName, "path", r.URL.Path)
			node, tunnelName = tg.findNodeTunnel(r.Context(), pathClientId, pathMappingName)
			if tunnelName != "" {
				r.URL.Path = buildPathRoutePath(r.URL.Path)
				slog.Debug("Path route rewritten", "newPath", r.URL.Path)
			}
		}
	}

	// 3. 如果都没匹配到，尝试精确域名匹配（使用索引）
	if node == nil || tunnelName == "" {
		host := stripPort(r.Host)
		node, tunnelName = tg.findByDomain(r.Context(), host)
	}

	// 4. 索引未命中，全量扫描兜底（节点可能刚注册尚未触发索引重建）
	if node == nil || tunnelName == "" {
		host := stripPort(r.Host)
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

func (tg *TunnelGateway) handleHTTPProxy(w http.ResponseWriter, r *http.Request, node *core.Node, tunnelName string) {
	stream, err := node.Session.OpenStream()
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

	if err := r.Write(stream); err != nil {
		slog.Error("Failed to write request to stream", "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}

	br := bufio.NewReader(stream)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		slog.Error("Failed to read response from stream", "error", err)
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		w.Header()[key] = values
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (tg *TunnelGateway) handleWebSocketGateway(w http.ResponseWriter, r *http.Request, node *core.Node, tunnelName string) {
	stream, err := node.Session.OpenStream()
	if err != nil {
		slog.Error("Failed to open stream for WS", "tunnel", tunnelName, "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	if err := r.Write(stream); err != nil {
		http.Error(w, "Failed to forward upgrade", http.StatusBadGateway)
		return
	}

	br := bufio.NewReader(stream)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		for key, values := range resp.Header {
			w.Header()[key] = values
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

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

	resp.Write(clientConn)
	slog.Debug("WebSocket connected", "tunnel", tunnelName, "nodeId", node.ID)
	biCopy(stream, clientConn)
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket" &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}
