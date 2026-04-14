package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	httpClientTimeout  = 30 * time.Second
	backendDialTimeout = 10 * time.Second
)

// TunnelLookup 隧道查找接口
type TunnelLookup struct {
	Targets func() map[string]string // 隧道名 → 目标地址（仅 HTTP 类型）
	NodeID  string
}

// HandleHTTPStream 处理从 smux stream 接收到的 HTTP 请求
// 服务端虚拟域名路由会将路径重写为 /mappingName/real/path，客户端需要：
// 1. 通过路径第一段识别隧道目标
// 2. 剥掉 mappingName 前缀后转发给本地后端
func HandleHTTPStream(stream io.ReadWriteCloser, br *bufio.Reader, req *http.Request, lookup *TunnelLookup) {
	target := ""
	// 尝试从路径第一段匹配隧道名
	pathParts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if len(pathParts) > 0 && pathParts[0] != "" {
		if t, ok := lookup.Targets()[pathParts[0]]; ok {
			target = t
			// 剥掉 mappingName 前缀
			req.URL.Path = "/" + strings.Join(pathParts[1:], "/")

		}
	}
	// fallback: 通过 Host 头匹配
	if target == "" {
		target = matchHTTPTunnel(req.Host, lookup.NodeID, lookup.Targets())
	}
	if target == "" {
		writeHTTPError(stream, http.StatusBadGateway, "no tunnel matched")
		return
	}

	if isWebSocketRequest(req) {
		handleWebSocket(stream, req, target)
		return
	}

	handleHTTP(stream, req, target)
}

func handleHTTP(stream io.Writer, req *http.Request, target string) {
	targetURL, err := url.Parse(target)
	if err != nil {
		writeHTTPError(stream, http.StatusInternalServerError, "invalid target URL")
		return
	}
	proxyURL := targetURL.ResolveReference(req.URL)
	proxyURL.RawQuery = req.URL.RawQuery

	proxyReq, err := http.NewRequest(req.Method, proxyURL.String(), req.Body)
	if err != nil {
		writeHTTPError(stream, http.StatusInternalServerError, "bad request")
		return
	}
	copyHeaders(proxyReq.Header, req.Header)

	client := &http.Client{Timeout: httpClientTimeout}
	resp, err := client.Do(proxyReq)
	if err != nil {
		writeHTTPError(stream, http.StatusBadGateway, "backend error: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if err := resp.Write(stream); err != nil {
		log.Printf("write HTTP response to stream: %v", err)
	}
}

func handleWebSocket(stream io.ReadWriteCloser, req *http.Request, target string) {
	targetURL, err := url.Parse(target)
	if err != nil {
		log.Printf("WebSocket parse target URL failed: %v", err)
		return
	}

	// 构建 WebSocket URL：将 http/https 转为 ws/wss
	wsScheme := "ws"
	if targetURL.Scheme == "https" {
		wsScheme = "wss"
	}

	var backendConn net.Conn
	if wsScheme == "wss" {
		backendConn, err = tls.DialWithDialer(
			&net.Dialer{Timeout: backendDialTimeout}, "tcp", targetURL.Host,
			&tls.Config{InsecureSkipVerify: true},
		)
	} else {
		backendConn, err = net.DialTimeout("tcp", targetURL.Host, backendDialTimeout)
	}
	if err != nil {
		log.Printf("WebSocket dial backend %s failed: %v", targetURL.Host, err)
		return
	}
	defer backendConn.Close()

	// 修改请求：使用后端地址，路径已由 HandleHTTPStream 剥掉了 mappingName 前缀
	req.URL.Scheme = wsScheme
	req.URL.Host = targetURL.Host
	req.RequestURI = ""

	// 转发升级请求
	if err := req.Write(backendConn); err != nil {
		log.Printf("WebSocket write upgrade request failed: %v", err)
		return
	}

	// 读取后端响应
	br := bufio.NewReader(backendConn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		log.Printf("WebSocket read upgrade response failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if err := resp.Write(stream); err != nil {
		log.Printf("WebSocket write upgrade response failed: %v", err)
		return
	}

	// 非 101 响应不进入双向转发
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return
	}

	// 双向数据转发
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

// matchHTTPTunnel 匹配 HTTP 隧道目标
// 顺序：精确域名 → 泛域名反解(mappingName-nodeID) → fallback
func matchHTTPTunnel(host, nodeID string, targets map[string]string) string {
	h := stripHostPort(host)

	// 1. 泛域名反解：mappingName-nodeID.xxx（最常用场景）
	if dotIdx := strings.Index(h, "."); dotIdx > 0 {
		subdomain := h[:dotIdx]
		parts := strings.SplitN(subdomain, "-", 2)
		if len(parts) == 2 && parts[1] == nodeID {
			if target, ok := targets[parts[0]]; ok {
				return target
			}
		}
	}

	// 2. 精确域名匹配（targets key 是隧道名时也适用）
	for name, target := range targets {
		if name == h {
			return target
		}
	}

	// 3. fallback：web 隧道 或 第一个 HTTP 隧道
	if target, ok := targets["web"]; ok {
		return target
	}
	for _, target := range targets {
		return target
	}
	return ""
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket"
}

func writeHTTPError(w io.Writer, code int, msg string) {
	resp := &http.Response{
		StatusCode: code,
		Status:     fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(strings.NewReader(msg)),
	}
	_ = resp.Write(w)
}

func stripHostPort(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		return host // 没有端口
	}
	return h
}

func parseHost(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	return u.Host, nil
}

