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
	"time"

	"moleAgent_Serv/internal/core"
)

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

// buildPathRoutePath 构建路径访问的路径（去掉 clientId 前缀）
func buildPathRoutePath(originalPath string) string {
	parts := strings.Split(strings.Trim(originalPath, "/"), "/")
	if len(parts) < 2 {
		return "/"
	}
	return "/" + strings.Join(parts[1:], "/")
}

// buildVirtualHostPath 构建泛域名访问的路径（加上 mappingName 前缀）
func buildVirtualHostPath(originalPath, mappingName string) string {
	if originalPath == "/" {
		return "/" + mappingName + "/"
	}
	if strings.HasPrefix(originalPath, "/") {
		return "/" + mappingName + originalPath
	}
	return "/" + mappingName + "/" + originalPath
}

// findNodeTunnel 在指定节点中查找隧道名匹配的隧道
func (tg *TunnelGateway) findNodeTunnel(ctx context.Context, nodeID, mappingName string) (*core.Node, string) {
	node, ok := tg.nodeMgr.Get(ctx, nodeID)
	if !ok || node.Status != core.NodeStatusOnline {
		return nil, ""
	}
	for _, t := range node.Tunnels {
		// 仅匹配 HTTP 类型：不设类型限制时，同名 TCP/UDP/webssh 隧道会把
		// 裸 HTTP 请求写进非 HTTP 流，客户端解析失败后 fallback 到随机
		// HTTP 隧道后端（信息泄露面）。与域名索引/兜底匹配的类型约束对齐
		if t.Name == mappingName && t.IsEnabled() &&
			(t.Type == core.TunnelTypeHTTP || t.Type == core.TunnelTypeHTTPS) {
			return node, t.Name
		}
	}
	return nil, ""
}

// fixedHopByHopHeaders 逐跳头固定集合（RFC 7230 §6.1 + 常见实现扩展）
var fixedHopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// stripHopByHopHeaders 原地剔除头集合中的逐跳头（REL-08）：固定集合 +
// Connection 头声明的所有 token 指名的头（与 httputil.ReverseProxy 的
// removeHopByHopHeaders 同算法）。逐跳头描述「本段连接」的语义，代理不得
// 原样转发给另一侧——上游的 Keep-Alive/连接策略对客户端连接毫无意义，
// 客户端的 Proxy-* 凭据也不应泄漏给隧道后端。
// 已知边界（与 ReverseProxy 一致）：响应的 Connection 含 close 时，
// net/http 解析阶段会把该头整体删除，其声明的自定义 token 届时不可见，
// 固定集合仍然生效
func stripHopByHopHeaders(h http.Header) {
	drop := make(map[string]struct{}, len(fixedHopByHopHeaders)+2)
	for _, name := range fixedHopByHopHeaders {
		drop[http.CanonicalHeaderKey(name)] = struct{}{}
	}
	for _, v := range h.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if tok = strings.TrimSpace(tok); tok != "" {
				drop[http.CanonicalHeaderKey(tok)] = struct{}{}
			}
		}
	}
	for name := range drop {
		h.Del(name)
	}
}

// isDomainRoutable 域名路由收录判定（REL-08）：HTTP 与 HTTPS 同为
// HTTP 家族（HTTPS 由接入侧终结 TLS 后走同一代理路径，findNodeTunnel
// 亦同时接受两者）；此前索引与兜底扫描只收录 HTTP，HTTPS 隧道配置了
// domain 也无法按域名路由
func isDomainRoutable(t core.Tunnel) bool {
	return (t.Type == core.TunnelTypeHTTP || t.Type == core.TunnelTypeHTTPS) && t.Domain != ""
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
	if tg.HyphenRouting {
		clientId, mappingName, isVhost = parseVirtualHostByHyphen(r.Host)
	} else {
		clientId, mappingName, isVhost = parseVirtualHost(r.Host)
	}

	if isVhost {
		slog.Debug("Virtual host routing", "clientId", clientId, "mappingName", mappingName, "host", r.Host)
		node, tunnelName = tg.findNodeTunnel(r.Context(), clientId, mappingName)
		if tunnelName != "" {
			r.URL.Path = buildVirtualHostPath(r.URL.Path, tunnelName)
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
				if t.IsEnabled() && isDomainRoutable(t) && t.Domain == host {
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
	sKey := statsKey(node.ID, tunnelName)

	if ok, gen := tg.limiter.AcquireConn(node.ID, sKey); !ok {
		slog.Warn("HTTP connection limit exceeded", "tunnel", tunnelName, "nodeId", node.ID, "remote", r.RemoteAddr)
		http.Error(w, "Too many connections", http.StatusTooManyRequests)
		return
	} else {
		defer tg.limiter.ReleaseConn(node.ID, sKey, gen)
	}

	tg.stats.ConnOpened(sKey)
	defer tg.stats.ConnClosed(sKey)

	session, err := tg.nodeMgr.GetSession(r.Context(), node.ID)
	if err != nil {
		slog.Error("Failed to get session for HTTP", "tunnel", tunnelName, "nodeId", node.ID, "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}
	stream, err := session.OpenStream()
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

	// Track bytes + optional bandwidth throttling
	trackedStream := &countingConn{
		Conn:      stream,
		ctx:       r.Context(),
		onWrite:   func(n int) { tg.stats.RecordBytesIn(sKey, int64(n)) },
		onRead:    func(n int) { tg.stats.RecordBytesOut(sKey, int64(n)) },
		bwLimiter: tg.limiter.BWLimiterFor(sKey),
	}

	// REL-08：请求方向同样剔除逐跳头后再转发——原样透传会把客户端的
	// Connection/Proxy-* 等连接级语义强加给隧道后端。Upgrade 类需求走
	// 上方的 WS 分支（该路径必须保留 Connection/Upgrade 供后端识别升级）
	stripHopByHopHeaders(r.Header)
	if err := r.Write(trackedStream); err != nil {
		slog.Error("Failed to write request to stream", "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}

	// 上游无响应不能无限阻塞：限时读响应头；body 阶段观察客户端断开
	//（r.Context 取消时以过期 deadline 解除读阻塞）。裸 smux 流读无
	// deadline 时，挂死后端会让 goroutine/limiter 配额滞留到节点断开
	stream.SetReadDeadline(time.Now().Add(60 * time.Second))
	br := bufio.NewReader(trackedStream)
	resp, err := http.ReadResponse(br, r)
	if err != nil {
		slog.Error("Failed to read response from stream", "error", err)
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	stream.SetReadDeadline(time.Time{})
	defer resp.Body.Close()

	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-r.Context().Done():
			trackedStream.SetReadDeadline(time.Now())
		case <-stopWatch:
		}
	}()

	// REL-08：上游响应中的逐跳头不得透传给客户端——连接级语义由本网关
	// 与客户端的连接自行协商
	stripHopByHopHeaders(resp.Header)
	for key, values := range resp.Header {
		w.Header()[key] = values
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.CopyBuffer(w, resp.Body, make([]byte, biCopyBufferSize)); err != nil {
		slog.Debug("HTTP proxy body copy error", "tunnel", tunnelName, "error", err)
	}
}

func (tg *TunnelGateway) handleWebSocketGateway(w http.ResponseWriter, r *http.Request, node *core.Node, tunnelName string) {
	sKey := statsKey(node.ID, tunnelName)

	if ok, gen := tg.limiter.AcquireConn(node.ID, sKey); !ok {
		slog.Warn("WebSocket connection limit exceeded", "tunnel", tunnelName, "nodeId", node.ID, "remote", r.RemoteAddr)
		http.Error(w, "Too many connections", http.StatusTooManyRequests)
		return
	} else {
		defer tg.limiter.ReleaseConn(node.ID, sKey, gen)
	}

	tg.stats.ConnOpened(sKey)
	defer tg.stats.ConnClosed(sKey)

	session, err := tg.nodeMgr.GetSession(r.Context(), node.ID)
	if err != nil {
		slog.Error("Failed to get session for WS", "tunnel", tunnelName, "nodeId", node.ID, "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}
	stream, err := session.OpenStream()
	if err != nil {
		slog.Error("Failed to open stream for WS", "tunnel", tunnelName, "error", err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	upgradedStream := &countingConn{
		Conn:      stream,
		ctx:       r.Context(),
		bwLimiter: tg.limiter.BWLimiterFor(sKey),
	}
	if err := r.Write(upgradedStream); err != nil {
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
		// REL-08：WS 升级失败的兜底响应同样剔除逐跳头（101 成功路径在
		// hijack 裸流上原样回写，Connection/Upgrade 是升级语义的一部分）
		stripHopByHopHeaders(resp.Header)
		for key, values := range resp.Header {
			w.Header()[key] = values
		}
		w.WriteHeader(resp.StatusCode)
		if _, err := io.CopyBuffer(w, resp.Body, make([]byte, biCopyBufferSize)); err != nil {
			slog.Debug("WS fallback body copy error", "tunnel", tunnelName, "error", err)
		}
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
	// 回放 bufio 预读的后端数据：101 后内网服务立即发送的首批 WS 帧可能已
	// 被 ReadResponse 预读进 br 缓冲，hijack 后直接 biCopy 裸流会把这批
	// 字节永久丢失（WS 帧序列错位，同 golang/go#26479）
	if n := br.Buffered(); n > 0 {
		buffered := make([]byte, n)
		if _, err := io.ReadFull(br, buffered); err == nil {
			if _, err := clientConn.Write(buffered); err != nil {
				slog.Debug("Failed to replay buffered WS data", "tunnel", tunnelName, "error", err)
			}
		}
	}
	slog.Debug("WebSocket connected", "tunnel", tunnelName, "nodeId", node.ID)

	trackedConn := &countingConn{
		Conn:      clientConn,
		ctx:       r.Context(),
		onRead:    func(n int) { tg.stats.RecordBytesIn(sKey, int64(n)) },
		onWrite:   func(n int) { tg.stats.RecordBytesOut(sKey, int64(n)) },
		bwLimiter: tg.limiter.BWLimiterFor(sKey),
	}
	biCopy(stream, trackedConn)
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket" &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}
