package proxy

import (
	"bufio"
	"bytes"
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
	tunnelName := ""
	// 尝试从路径第一段匹配隧道名
	pathParts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	if len(pathParts) > 0 && pathParts[0] != "" {
		if t, ok := lookup.Targets()[pathParts[0]]; ok {
			target = t
			tunnelName = pathParts[0]
			// 剥掉 mappingName 前缀
			req.URL.Path = "/" + strings.Join(pathParts[1:], "/")
		}
	}
	// fallback: 通过 Host 头匹配
	if target == "" {
		target = matchHTTPTunnel(req.Host, lookup.NodeID, lookup.Targets())
		if target != "" {
			for name, t := range lookup.Targets() {
				if t == target {
					tunnelName = name
					break
				}
			}
		}
	}
	if target == "" {
		writeHTTPError(stream, http.StatusBadGateway, "no tunnel matched")
		return
	}

	if isWebSocketRequest(req) {
		handleWebSocket(stream, req, target, tunnelName)
		return
	}

	handleHTTP(stream, req, target, tunnelName)
}

// ensureHTTPScheme 确保 HTTP 隧道 target 包含 scheme，缺失时默认补 http://
func ensureHTTPScheme(target string) string {
	if target == "" {
		return target
	}
	// 已有 scheme（含 ://）直接返回
	if strings.Contains(target, "://") {
		return target
	}
	return "http://" + target
}

func handleHTTP(stream io.Writer, req *http.Request, target, tunnelName string) {
	target = ensureHTTPScheme(target)
	targetURL, err := url.Parse(target)
	if err != nil {
		writeHTTPError(stream, http.StatusInternalServerError, "invalid target URL")
		return
	}
	proxyURL := targetURL.ResolveReference(req.URL)
	proxyURL.RawQuery = req.URL.RawQuery

	// 读取请求体并计数
	var reqBytes int64
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		reqBytes = int64(len(body))
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	proxyReq, err := http.NewRequest(req.Method, proxyURL.String(), req.Body)
	if err != nil {
		writeHTTPError(stream, http.StatusInternalServerError, "bad request")
		return
	}
	copyHeaders(proxyReq.Header, req.Header)

	// nginx 风格转发头
	proxyReq.Header.Set("X-Forwarded-For", getClientIP(req))
	proxyReq.Header.Set("X-Real-IP", getClientIP(req))
	proxyReq.Header.Set("X-Forwarded-Host", req.Host)
	proxyReq.Header.Set("X-Forwarded-Proto", scheme(req))
	proxyReq.Header.Set("Host", targetURL.Host)
	proxyReq.Header.Set("Connection", "close")

	client := &http.Client{Timeout: httpClientTimeout}
	resp, err := client.Do(proxyReq)
	if err != nil {
		writeHTTPError(stream, http.StatusBadGateway, "backend error: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 流式传输响应（保持 206 等状态码，支持 Range 请求的视频播放）
	if err := writeResponseStart(stream, resp); err != nil {
		log.Printf("write HTTP response start failed: %v", err)
		return
	}

	buf := make([]byte, 512*1024)
	var respBytes int64
	respBytes, _ = io.CopyBuffer(stream, resp.Body, buf)

	if tunnelName != "" {
		AddHTTPBytes(tunnelName, uint64(reqBytes), uint64(respBytes))
	}
}

// writeResponseStart 写入 HTTP 响应行和 headers，为流式 body 传输做准备
func writeResponseStart(w io.Writer, resp *http.Response) error {
	// 写入状态行：HTTP/1.1 206 Partial Content 或其他状态码
	status := fmt.Sprintf("HTTP/%d.%d %d %s\r\n", resp.ProtoMajor, resp.ProtoMinor, resp.StatusCode, resp.Status)
	if _, err := io.WriteString(w, status); err != nil {
		return err
	}

	// 写入所有 headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			line := fmt.Sprintf("%s: %s\r\n", k, v)
			if _, err := io.WriteString(w, line); err != nil {
				return err
			}
		}
	}

	// 写入 headers 结束分隔符
	if _, err := io.WriteString(w, "\r\n"); err != nil {
		return err
	}

	return nil
}

func handleWebSocket(stream io.ReadWriteCloser, req *http.Request, target, tunnelName string) {
	target = ensureHTTPScheme(target)
	targetURL, err := url.Parse(target)
	if err != nil {
		log.Printf("WebSocket parse target URL failed: %v", err)
		return
	}

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

	// nginx 风格转发头
	req.Header.Set("X-Forwarded-For", getClientIP(req))
	req.Header.Set("X-Real-IP", getClientIP(req))
	req.Header.Set("X-Forwarded-Host", req.Host)
	req.Header.Set("X-Forwarded-Proto", wsScheme)
	req.Header.Set("Host", targetURL.Host)
	req.Header.Set("Upgrade", req.Header.Get("Upgrade"))
	req.Header.Set("Connection", req.Header.Get("Connection"))

	req.URL.Scheme = wsScheme
	req.URL.Host = targetURL.Host
	req.RequestURI = ""

	if err := req.Write(backendConn); err != nil {
		log.Printf("WebSocket write upgrade request failed: %v", err)
		return
	}

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

	if resp.StatusCode != http.StatusSwitchingProtocols {
		return
	}

	buf := make([]byte, 32*1024)
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		n, _ := io.CopyBuffer(backendConn, stream, buf)
		AddHTTPBytes(tunnelName, 0, uint64(n))
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		n, _ := io.CopyBuffer(stream, br, buf)
		AddHTTPBytes(tunnelName, uint64(n), 0)
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

func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

func scheme(r *http.Request) string {
	if r.URL.Scheme == "https" || r.Header.Get("X-Forwarded-Proto") == "https" {
		return "https"
	}
	return "http"
}
