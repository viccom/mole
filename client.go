package moleAgent_client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_client/internal/protocol"
	"moleAgent_client/internal/proxy"
	"moleAgent_client/internal/transport"
)

// ===== 公共 API =====

// Client 是 moleAgent_client 的核心类型。
// 外部程序通过 New() 创建、Run() 启动、AddTunnel/RemoveTunnel 管理隧道。
type Client struct {
	cfg       *Config
	transport *transport.SessionManager
	events    *EventBus

	mu      sync.RWMutex
	tunnels []Tunnel

	ctrlMu  sync.Mutex        // 控制命令发送锁
	tunReqs chan tunnelReq    // 隧道更新请求队列
	cancel  context.CancelFunc
}

type tunnelReq struct {
	tunnels []Tunnel
	resp    chan error
}

// New 创建客户端实例
func New(cfg *Config) (*Client, error) {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	tlsCfg := &transport.TLSConfig{Enabled: cfg.UseTLS}
	dial := transport.DefaultDialer(tlsCfg)

	return &Client{
		cfg:       cfg,
		transport: transport.NewSessionManager(dial),
		events:    newEventBus(),
		tunnels:   append([]Tunnel{}, cfg.Tunnels...),
		tunReqs:   make(chan tunnelReq, 16),
	}, nil
}

// Run 连接服务端并运行主循环（阻塞，直到 ctx 取消）
func (c *Client) Run(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)
	defer c.cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 连接 + 认证
		if err := c.transport.Connect(ctx, c.cfg.ServerAddr, c.cfg.Token); err != nil {
			c.events.Emit(Event{Type: EventReconnecting, Data: map[string]any{"error": err.Error()}})
			log.Printf("Connect failed: %v, retrying in %s...", err, c.cfg.ReconnectInterval)
			c.sleep(ctx, c.cfg.ReconnectInterval)
			continue
		}
		c.events.Emit(Event{Type: EventConnected})
		c.events.Emit(Event{Type: EventAuthenticated})

		// 注册
		if err := c.register(ctx); err != nil {
			log.Printf("Register failed: %v, retrying in %s...", err, c.cfg.ReconnectInterval)
			c.close()
			c.sleep(ctx, c.cfg.ReconnectInterval)
			continue
		}

		// 启动心跳和隧道更新处理
		hbCtx, hbCancel := context.WithCancel(ctx)
		updCtx, updCancel := context.WithCancel(ctx)
		go c.heartbeat(hbCtx)
		go c.processTunnelUpdates(updCtx)

		// 接受服务端数据流（含 panic recovery）
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("acceptLoop panic: %v", r)
				}
			}()
			c.acceptLoop(ctx)
		}()

		// 连接断开，确保子 context 取消
		updCancel()
		hbCancel()
		c.close()
		c.events.Emit(Event{Type: EventDisconnected})
		log.Printf("Disconnected, reconnecting in %s...", c.cfg.ReconnectInterval)
		c.sleep(ctx, c.cfg.ReconnectInterval)
	}
}

// Close 优雅关闭
func (c *Client) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	c.close()
}

// OnEvent 注册事件处理器
func (c *Client) OnEvent(eventType EventType, handler EventHandler) {
	c.events.On(eventType, handler)
}

// NodeID 返回节点 ID
func (c *Client) NodeID() string {
	return c.cfg.NodeID
}

// Connected 返回是否已连接
func (c *Client) Connected() bool {
	return c.transport.IsConnected()
}

// ===== 隧道管理（线程安全） =====

// Tunnels 返回当前隧道列表快照
func (c *Client) Tunnels() []Tunnel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]Tunnel, len(c.tunnels))
	copy(result, c.tunnels)
	return result
}

// AddTunnel 添加隧道并同步到服务端（需要已连接服务端）
func (c *Client) AddTunnel(t Tunnel) error {
	if !c.Connected() {
		return fmt.Errorf("not connected to server, tunnel operations require active connection")
	}
	if err := t.Validate(); err != nil {
		return err
	}

	c.mu.Lock()
	updated := make([]Tunnel, 0, len(c.tunnels)+1)
	replaced := false
	for _, existing := range c.tunnels {
		if existing.Name == t.Name {
			updated = append(updated, t)
			replaced = true
		} else {
			updated = append(updated, existing)
		}
	}
	if !replaced {
		updated = append(updated, t)
	}
	c.mu.Unlock()

	return c.requestTunnelUpdate(updated)
}

// RemoveTunnel 移除隧道并同步到服务端（需要已连接服务端）
func (c *Client) RemoveTunnel(name string) error {
	if !c.Connected() {
		return fmt.Errorf("not connected to server, tunnel operations require active connection")
	}
	c.mu.Lock()
	updated := make([]Tunnel, 0, len(c.tunnels))
	found := false
	for _, t := range c.tunnels {
		if t.Name == name {
			found = true
			continue
		}
		updated = append(updated, t)
	}
	c.mu.Unlock()

	if !found {
		return fmt.Errorf("tunnel %q not found", name)
	}
	return c.requestTunnelUpdate(updated)
}

// UpdateTunnels 替换全部隧道并同步到服务端
func (c *Client) UpdateTunnels(tunnels []Tunnel) error {
	for _, t := range tunnels {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	return c.requestTunnelUpdate(tunnels)
}

// ===== 内部方法 =====

// requestTunnelUpdate 通过通道请求更新隧道（线程安全）
func (c *Client) requestTunnelUpdate(tunnels []Tunnel) error {
	req := tunnelReq{
		tunnels: tunnels,
		resp:    make(chan error, 1),
	}
	select {
	case c.tunReqs <- req:
		return <-req.resp
	default:
		return fmt.Errorf("tunnel update queue full")
	}
}

// processTunnelUpdates 处理隧道更新请求
func (c *Client) processTunnelUpdates(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-c.tunReqs:
			err := c.sendTunnelUpdate(req.tunnels)
			if err == nil {
				c.mu.Lock()
				c.tunnels = make([]Tunnel, len(req.tunnels))
				copy(c.tunnels, req.tunnels)
				c.mu.Unlock()
				c.events.Emit(Event{Type: EventTunnelSynced, Data: map[string]any{"count": len(req.tunnels)}})
			}
			req.resp <- err
		}
	}
}

// register 向服务端注册节点和隧道
func (c *Client) register(ctx context.Context) error {
	session := c.transport.Session()
	if session == nil {
		return fmt.Errorf("no session")
	}

	stream, err := session.OpenStream()
	if err != nil {
		return fmt.Errorf("open register stream: %w", err)
	}
	defer stream.Close()

	c.mu.RLock()
	tunnels := toProtocols(c.tunnels)
	c.mu.RUnlock()

	cmd := protocol.ControlCmd{
		Cmd:     "register",
		NodeID:  c.cfg.NodeID,
		Name:    c.cfg.NodeName,
		Tunnels: tunnels,
	}
	if err := writeCmd(stream, cmd); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	resp, err := readResponse(stream, c.cfg.HeartbeatTimeout)
	if err != nil {
		return fmt.Errorf("read register response: %w", err)
	}
	if resp.Cmd != "ok" {
		return fmt.Errorf("register failed: %s", resp.Msg)
	}

	c.events.Emit(Event{Type: EventRegistered, Data: map[string]any{"tunnels": len(tunnels)}})
	log.Printf("Registered as node %q with %d tunnel(s)", c.cfg.NodeID, len(tunnels))
	return nil
}

// sendTunnelUpdate 向服务端发送 tunnel_update 命令
func (c *Client) sendTunnelUpdate(tunnels []Tunnel) error {
	c.ctrlMu.Lock()
	defer c.ctrlMu.Unlock()

	session := c.transport.Session()
	if session == nil || session.IsClosed() {
		return fmt.Errorf("session not available")
	}

	stream, err := session.OpenStream()
	if err != nil {
		return fmt.Errorf("open update stream: %w", err)
	}
	defer stream.Close()

	cmd := protocol.ControlCmd{
		Cmd:     "tunnel_update",
		NodeID:  c.cfg.NodeID,
		Tunnels: toProtocols(tunnels),
	}
	if err := writeCmd(stream, cmd); err != nil {
		return fmt.Errorf("send tunnel_update: %w", err)
	}

	resp, err := readResponse(stream, c.cfg.HeartbeatTimeout)
	if err != nil {
		return fmt.Errorf("read tunnel_update response: %w", err)
	}
	if resp.Cmd != "ok" {
		return fmt.Errorf("tunnel_update failed: %s", resp.Msg)
	}

	log.Printf("Tunnels updated: %d tunnel(s)", len(tunnels))
	return nil
}

// heartbeat 心跳循环
func (c *Client) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !c.transport.IsConnected() {
				return
			}
			if err := c.sendPing(); err != nil {
				log.Printf("Heartbeat failed: %v", err)
				c.events.Emit(Event{Type: EventHeartbeatFail, Data: map[string]any{"error": err.Error()}})
				return
			}
			c.events.Emit(Event{Type: EventHeartbeatOK})
		}
	}
}

// sendPing 发送心跳
func (c *Client) sendPing() error {
	c.ctrlMu.Lock()
	defer c.ctrlMu.Unlock()

	session := c.transport.Session()
	if session == nil || session.IsClosed() {
		return fmt.Errorf("session closed")
	}

	stream, err := session.OpenStream()
	if err != nil {
		return fmt.Errorf("open ping stream: %w", err)
	}
	defer stream.Close()

	cmd := protocol.ControlCmd{Cmd: "ping"}
	if err := writeCmd(stream, cmd); err != nil {
		return fmt.Errorf("send ping: %w", err)
	}

	resp, err := readResponse(stream, c.cfg.HeartbeatTimeout)
	if err != nil {
		return fmt.Errorf("read pong: %w", err)
	}
	if resp.Cmd != "pong" {
		return fmt.Errorf("unexpected pong response: %s", resp.Cmd)
	}
	return nil
}

// acceptLoop 接受服务端流（数据转发 + 控制推送）
func (c *Client) acceptLoop(ctx context.Context) {
	session := c.transport.Session()
	if session == nil {
		return
	}

	for {
		stream, err := session.AcceptStream()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("AcceptStream error: %v", err)
				return
			}
		}
		go c.dispatchStream(stream)
	}
}

// dispatchStream 分发服务端发来的流
// 启发式检测：
//   - 首字节 '{' 且 JSON 含 cmd:"tunnel_push" → 控制推送
//   - 首字节 '\x00' → TCP/UDP 代理（含隧道名标识头）
//   - 可解析为 HTTP 请求 → HTTP 代理
//   - 其他 → TCP/UDP 原始转发（兼容旧服务端）
func (c *Client) dispatchStream(stream *smux.Stream) {
	defer stream.Close()

	br := bufio.NewReader(stream)
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))

	peek, err := br.Peek(1)
	if err != nil {
		return
	}

	// 尝试作为控制命令（tunnel_push）
	if peek[0] == '{' {
		if c.handlePossiblePush(stream, br) {
			return
		}
	}

	// 检查 TCP/UDP 代理协议头：\x00<tunnel-name>\n
	if peek[0] == 0x00 {
		line, err := br.ReadBytes('\n')
		if err == nil && len(line) > 1 {
			tunnelName := string(line[1 : len(line)-1]) // 跳过 \x00 和 \n
			stream.SetReadDeadline(time.Time{})
			proxy.HandleRawStream(stream, br, func() string {
				c.mu.RLock()
				defer c.mu.RUnlock()
				for _, t := range c.tunnels {
					if t.Name == tunnelName && t.IsEnabled() && (t.Type == TunnelTypeTCP || t.Type == TunnelTypeUDP) {
						return t.Target
					}
				}
				return ""
			})
			return
		}
	}

	// 尝试作为 HTTP 请求
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	req, err := http.ReadRequest(br)
	if err == nil {
		stream.SetReadDeadline(time.Time{})
		lookup := &proxy.TunnelLookup{
			Targets: func() map[string]string {
				c.mu.RLock()
				defer c.mu.RUnlock()
				targets := make(map[string]string, len(c.tunnels))
				for _, t := range c.tunnels {
					if !t.IsEnabled() {
						continue
					}
					if t.Type == TunnelTypeHTTP || t.Type == TunnelTypeHTTPS {
						if !strings.Contains(t.Target, "://") {
							scheme := "http"
							if t.Type == TunnelTypeHTTPS {
								scheme = "https"
							}
							targets[t.Name] = scheme + "://" + t.Target
						} else {
							targets[t.Name] = t.Target
						}
					}
				}
				return targets
			},
			NodeID: c.cfg.NodeID,
		}
		proxy.HandleHTTPStream(stream, br, req, lookup)
		return
	}

	// fallback：TCP/UDP 原始转发（兼容旧服务端，匹配第一个 TCP/UDP 隧道）
	stream.SetReadDeadline(time.Time{})
	proxy.HandleRawStream(stream, br, func() string {
		c.mu.RLock()
		defer c.mu.RUnlock()
		for _, t := range c.tunnels {
			if t.IsEnabled() && (t.Type == TunnelTypeTCP || t.Type == TunnelTypeUDP) {
				return t.Target
			}
		}
		return ""
	})
}

// handlePossiblePush 尝试处理 tunnel_push 控制推送
func (c *Client) handlePossiblePush(stream *smux.Stream, br *bufio.Reader) bool {
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := br.ReadBytes('\n')
	if err != nil {
		// 可能没有换行符，尝试读取剩余数据
		rest, _ := br.ReadBytes(0)
		line = append(line, rest...)
	}

	var cmd struct {
		Cmd     string            `json:"cmd"`
		Tunnels []protocol.Tunnel `json:"tunnels"`
	}
	if json.Unmarshal(line, &cmd) != nil || cmd.Cmd != "tunnel_push" {
		return false // 不是控制推送，交给后续处理
	}

	// 更新本地隧道配置
	tunnels := fromProtocols(cmd.Tunnels)
	c.mu.Lock()
	c.tunnels = make([]Tunnel, len(tunnels))
	copy(c.tunnels, tunnels)
	c.mu.Unlock()

	log.Printf("Received tunnel_push from server: %d tunnel(s)", len(tunnels))
	c.events.Emit(Event{Type: EventTunnelUpdated, Data: map[string]any{"tunnels": len(tunnels)}})

	// 响应成功
	resp, _ := json.Marshal(map[string]string{"cmd": "ok", "msg": "tunnels updated"})
	stream.Write(resp)
	return true
}

// close 关闭连接
func (c *Client) close() {
	c.transport.Close()
}

// sleep 可取消的休眠
func (c *Client) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// ===== 协议工具函数 =====

func writeCmd(w io.Writer, cmd protocol.ControlCmd) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func readResponse(r io.Reader, timeout time.Duration) (*protocol.ControlResponse, error) {
	if timeout > 0 {
		if s, ok := r.(interface{ SetReadDeadline(time.Time) error }); ok {
			s.SetReadDeadline(time.Now().Add(timeout))
			defer s.SetReadDeadline(time.Time{})
		}
	}

	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var resp protocol.ControlResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &resp, nil
}
