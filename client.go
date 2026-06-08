package moleAgent_client

import (
	"bufio"
	"context"
	"crypto/tls"
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
	"moleAgent_client/internal/proxy/ser2mq"
	"moleAgent_client/internal/proxy/ser2net"
	"moleAgent_client/internal/proxy/vpn"
	"moleAgent_client/internal/proxy/webssh"
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

	// ser2mq 隧道管理器
	ser2mqMgr *ser2mq.Manager

	// ser2net 隧道管理器（ser2tcp/ser2udp）
	ser2netMgr *ser2net.Manager

	// vpn-manager 进程管理器
	vpnMgr *vpn.Manager

	// webssh 远程终端管理器
	websshMgr *webssh.Manager
}

type tunnelMutationKind int

const (
	tunnelMutationAdd tunnelMutationKind = iota + 1
	tunnelMutationRemove
	tunnelMutationReplaceAll
)

type tunnelMutation struct {
	kind    tunnelMutationKind
	tunnel   Tunnel
	name     string
	tunnels  []Tunnel
}

type tunnelReq struct {
	mutation tunnelMutation
	resp     chan error
}

// New 创建客户端实例
func New(cfg *Config) (*Client, error) {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	var dial transport.DialFunc
	switch cfg.Transport {
	case "ws":
		var wsTLS *tls.Config
		if cfg.UseTLS {
			wsTLS = &tls.Config{}
		}
		dial = transport.NewWSDialer(transport.WSDialerConfig{TLSConfig: wsTLS})
	case "kcp":
		dial = transport.NewKCPDialer(transport.KCPDialerConfig{
			Key:          cfg.KCP.Key,
			DataShards:   cfg.KCP.DataShards,
			ParityShards: cfg.KCP.ParityShards,
			NoDelay:      cfg.KCP.NoDelay,
			Interval:     cfg.KCP.Interval,
			Resend:       cfg.KCP.Resend,
			NoCongestion: cfg.KCP.NoCongestion,
			SendWindow:   cfg.KCP.SendWindow,
			RecvWindow:   cfg.KCP.RecvWindow,
		})
	default:
		tlsCfg := &transport.TLSConfig{Enabled: cfg.UseTLS}
		dial = transport.DefaultDialer(tlsCfg)
	}

	// 创建管理器（使用背景 context，生命周期由 Client 统一管理）
	// nodeID 在 ApplyDefaults 中已生成
	ser2mqMgr := ser2mq.NewManager(context.Background(), cfg.NodeID)
	ser2netMgr := ser2net.NewManager(context.Background())
	vpnMgr := vpn.NewManager(context.Background())
	websshMgr := webssh.NewManager()

	sm := transport.NewSessionManager(dial)
	if cfg.Transport == "kcp" {
		sm.SetSmuxOverride(&smux.Config{
			Version:           2,
			KeepAliveDisabled: false,
			KeepAliveInterval: 5 * time.Second,
			KeepAliveTimeout:  15 * time.Second,
			MaxFrameSize:      32768,
			MaxReceiveBuffer:  32 * 1024 * 1024,
			MaxStreamBuffer:   4 * 1024 * 1024,
		})
	}
	return &Client{
		cfg:        cfg,
		transport:  sm,
		events:     newEventBus(),
		tunnels:    append([]Tunnel{}, cfg.Tunnels...),
		tunReqs:    make(chan tunnelReq, 16),
		ser2mqMgr:  ser2mqMgr,
		ser2netMgr: ser2netMgr,
		vpnMgr:     vpnMgr,
		websshMgr:  websshMgr,
	}, nil
}

// Run 连接服务端并运行主循环（阻塞，直到 ctx 取消）
func (c *Client) Run(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)
	defer c.cancel()

	// 通知各管理器当前节点 ID（在隧道更新前就绪）
	if c.ser2mqMgr != nil {
		c.ser2mqMgr.SetNodeID(c.cfg.NodeID)
	}

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

	// 关闭管理器
	if c.ser2mqMgr != nil {
		c.ser2mqMgr.Close()
	}
	if c.ser2netMgr != nil {
		c.ser2netMgr.Close()
	}
	if c.vpnMgr != nil {
		c.vpnMgr.Close()
	}
	if c.websshMgr != nil {
		c.websshMgr.Close()
	}
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

	return c.requestTunnelMutation(tunnelMutation{
		kind:   tunnelMutationAdd,
		tunnel: t,
	})
}

// RemoveTunnel 移除隧道并同步到服务端（需要已连接服务端）
func (c *Client) RemoveTunnel(name string) error {
	if !c.Connected() {
		return fmt.Errorf("not connected to server, tunnel operations require active connection")
	}
	return c.requestTunnelMutation(tunnelMutation{
		kind: tunnelMutationRemove,
		name: name,
	})
}

// UpdateTunnels 替换全部隧道并同步到服务端
func (c *Client) UpdateTunnels(tunnels []Tunnel) error {
	for _, t := range tunnels {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	return c.requestTunnelMutation(tunnelMutation{
		kind:    tunnelMutationReplaceAll,
		tunnels: append([]Tunnel(nil), tunnels...),
	})
}

// ===== 内部方法 =====

// requestTunnelMutation 通过通道请求更新隧道（线程安全）
func (c *Client) requestTunnelMutation(mutation tunnelMutation) error {
	req := tunnelReq{
		mutation: mutation,
		resp:     make(chan error, 1),
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
			c.mu.RLock()
			current := append([]Tunnel(nil), c.tunnels...)
			c.mu.RUnlock()

			next, err := applyTunnelMutation(current, req.mutation)
			if err == nil {
				err = c.sendTunnelUpdate(next)
			}
			if err == nil {
				c.mu.Lock()
				c.tunnels = append([]Tunnel(nil), next...)
				c.mu.Unlock()
				c.notifyManagers(next)
				c.events.Emit(Event{Type: EventTunnelSynced, Data: map[string]any{"count": len(next)}})
			}
			req.resp <- err
		}
	}
}

// notifyManagers 提取 ser2mq/ser2net/vpn-manager 配置并通知各管理器
func (c *Client) notifyManagers(tunnels []Tunnel) {
	ser2mqConfigs := make(map[string]ser2mq.Ser2MQConfig)
	ser2netConfigs := make(map[string]ser2net.TunnelConfig)
	vpnConfigs := make(map[string]vpn.Config)
	websshConfigs := make(map[string]webssh.WebSSHConfig)
	for _, t := range tunnels {
		if !t.IsEnabled() {
			continue
		}
		if t.Type == TunnelTypeSer2MQ && t.Para != nil {
			var cfg ser2mq.Ser2MQConfig
			if err := json.Unmarshal(t.Para, &cfg); err != nil {
				log.Printf("notifyManagers: unmarshal ser2mq %q failed: %v", t.Name, err)
			} else {
				cfg.Enable = t.IsEnabled()
				ser2mqConfigs[t.Name] = cfg
			}
		}
		if (t.Type == TunnelTypeSer2TCP || t.Type == TunnelTypeSer2UDP) && t.Para != nil {
			var cfg ser2net.Ser2NetConfig
			if err := json.Unmarshal(t.Para, &cfg); err != nil {
				log.Printf("notifyManagers: unmarshal ser2net %q failed: %v", t.Name, err)
			} else {
				cfg.Enable = t.IsEnabled()
				ser2netConfigs[t.Name] = ser2net.TunnelConfig{Type: string(t.Type), Config: cfg}
			}
		}
		if t.Type == TunnelTypeVPNMgr && t.Para != nil {
			var cfg vpn.Config
			if err := json.Unmarshal(t.Para, &cfg); err != nil {
				log.Printf("notifyManagers: unmarshal vpn %q failed: %v", t.Name, err)
			} else {
				vpnConfigs[t.Name] = cfg
			}
		}
		if t.Type == TunnelTypeWebSSH && t.Para != nil {
			var cfg webssh.WebSSHConfig
			if err := json.Unmarshal(t.Para, &cfg); err != nil {
				log.Printf("notifyManagers: unmarshal webssh %q failed: %v", t.Name, err)
			} else {
				cfg.Enable = t.IsEnabled()
				websshConfigs[t.Name] = cfg
			}
		}
	}

	if c.ser2mqMgr != nil {
		c.ser2mqMgr.OnTunnelUpdate(ser2mqConfigs)
	}
	if c.ser2netMgr != nil {
		c.ser2netMgr.OnTunnelUpdate(ser2netConfigs)
	}
	if c.vpnMgr != nil {
		vpnTypes := make([]string, 0, len(vpnConfigs))
		for name := range vpnConfigs {
			vpnTypes = append(vpnTypes, name)
		}
		c.vpnMgr.OnTunnelUpdate(vpnTypes, vpnConfigs)
	}
	if c.websshMgr != nil {
		c.websshMgr.OnTunnelUpdate(websshConfigs)
	}
}

func applyTunnelMutation(current []Tunnel, mutation tunnelMutation) ([]Tunnel, error) {
	switch mutation.kind {
	case tunnelMutationAdd:
		next := make([]Tunnel, 0, len(current)+1)
		replaced := false
		for _, existing := range current {
			if existing.Name == mutation.tunnel.Name {
				next = append(next, mutation.tunnel)
				replaced = true
				continue
			}
			next = append(next, existing)
		}
		if !replaced {
			next = append(next, mutation.tunnel)
		}
		return next, nil
	case tunnelMutationRemove:
		next := make([]Tunnel, 0, len(current))
		found := false
		for _, existing := range current {
			if existing.Name == mutation.name {
				found = true
				continue
			}
			next = append(next, existing)
		}
		if !found {
			return nil, fmt.Errorf("tunnel %q not found", mutation.name)
		}
		return next, nil
	case tunnelMutationReplaceAll:
		return append([]Tunnel(nil), mutation.tunnels...), nil
	default:
		return nil, fmt.Errorf("unknown tunnel mutation kind: %d", mutation.kind)
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
				c.transport.Close()
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

// readerStream wraps bufio reader + smux stream as io.ReadWriteCloser
type readerStream struct {
	io.Reader
	io.Writer
	io.Closer
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
			}, tunnelName)
			return
		}
	}

	// 检查 WebSSH 代理协议头：<tunnel-name>

	if peek[0] == 0x01 {
		line, err := br.ReadBytes('\n')
		if err == nil && len(line) > 1 {
			tunnelName := string(line[1 : len(line)-1])
			stream.SetReadDeadline(time.Time{})
			c.websshMgr.HandleStream(tunnelName, &readerStream{
				Reader: io.MultiReader(br, stream),
				Writer: stream,
				Closer: stream,
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
	}, "")
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

	c.notifyManagers(tunnels)

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

type Stats struct {
	NodeID       string               `json:"node_id"`
	Connected    bool                 `json:"connected"`
	ServerAddr  string               `json:"server_addr"`
	TCPBytesIn  uint64               `json:"tcp_bytes_in"`
	TCPBytesOut uint64               `json:"tcp_bytes_out"`
	HTTPBytesIn uint64               `json:"http_bytes_in"`
	HTTPBytesOut uint64              `json:"http_bytes_out"`
	Tunnels     []proxy.TunnelTraffic `json:"tunnels"`
}

func (c *Client) Stats() Stats {
	tunnelStats := proxy.TunnelTrafficStats()
	tunnels := make([]proxy.TunnelTraffic, 0, len(c.tunnels))
	for _, t := range c.Tunnels() {
		if ts, ok := tunnelStats[t.Name]; ok {
			tunnels = append(tunnels, ts)
		} else {
			// 未产生流量的隧道也保留（显示 0）
			tunnels = append(tunnels, proxy.TunnelTraffic{Name: t.Name})
		}
	}
	return Stats{
		NodeID:       c.cfg.NodeID,
		Connected:    c.Connected(),
		ServerAddr:  c.cfg.ServerAddr,
		TCPBytesIn:  proxy.GetTCPBytesIn(),
		TCPBytesOut: proxy.GetTCPBytesOut(),
		HTTPBytesIn: proxy.GetHTTPBytesIn(),
		HTTPBytesOut: proxy.GetHTTPBytesOut(),
		Tunnels:     tunnels,
	}
}

// ===== 统一隧道状态 =====

// TunnelStatus 统一隧道状态（合并配置 + 运行时）
type TunnelStatus struct {
	Name       string          `json:"name"`
	Type       TunnelType     `json:"type"`
	Target     string          `json:"target"`
	Domain     string          `json:"domain,omitempty"`
	ListenPort int            `json:"listen_port,omitempty"`
	Enabled    bool           `json:"enabled"`
	Connected  bool           `json:"connected"`
	BytesIn    uint64         `json:"bytes_in"`
	BytesOut   uint64         `json:"bytes_out"`
	Status     any            `json:"status,omitempty"` // 类型特定状态（Ser2MQStats / vpn.Status）
	Para       json.RawMessage `json:"para,omitempty"`  // 扩展配置（前端编辑表单需要）
}

// buildTunnelStatus 构建单个隧道的统一状态
func (c *Client) buildTunnelStatus(t Tunnel, connected bool, trafficStats map[string]proxy.TunnelTraffic) TunnelStatus {
	ts := TunnelStatus{
		Name:       t.Name,
		Type:       t.Type,
		Target:     t.Target,
		Domain:     t.Domain,
		ListenPort: t.ListenPort,
		Enabled:    t.IsEnabled(),
		Para:       t.Para,
	}

	switch t.Type {
	case TunnelTypeHTTP, TunnelTypeHTTPS, TunnelTypeTCP, TunnelTypeUDP:
		ts.Connected = connected && t.IsEnabled()
		if traffic, ok := trafficStats[t.Name]; ok {
			ts.BytesIn = traffic.TCPBytesIn + traffic.HTTPBytesIn
			ts.BytesOut = traffic.TCPBytesOut + traffic.HTTPBytesOut
		}
	case TunnelTypeSer2MQ:
		stats, _ := c.ser2mqMgr.Status(t.Name)
		ts.Connected = stats.Running
		ts.BytesIn = stats.BytesIn
		ts.BytesOut = stats.BytesOut
		ts.Status = stats
	case TunnelTypeSer2TCP, TunnelTypeSer2UDP:
		if stats, err := c.ser2netMgr.Status(t.Name); err == nil {
			ts.Connected = stats.Running
			ts.BytesIn = stats.BytesIn
			ts.BytesOut = stats.BytesOut
			ts.Status = stats
		}
	case TunnelTypeVPNMgr:
		if status, err := c.vpnMgr.Status(t.Name); err == nil {
			ts.Connected = status.Running
			// 填充 vnt-cli REST API 实时数据
			if status.Running {
				if info, peers, routes, buildInfo, _ := c.vpnMgr.VNTData(t.Name); info != nil {
					status.VNTInfo = info
					status.VNTPeers = peers
					status.VNTRoutes = routes
					status.VNTStatus = buildInfo
				}
			}
			ts.Status = status
		}
	case TunnelTypeWebSSH:
		if stats, err := c.websshMgr.Status(t.Name); err == nil {
			ts.Connected = stats.Running
			ts.BytesIn = stats.BytesIn
			ts.BytesOut = stats.BytesOut
			ts.Status = stats
		}
	}
	return ts
}

// AllTunnelStatus 返回所有隧道的统一状态
func (c *Client) AllTunnelStatus() []TunnelStatus {
	c.mu.RLock()
	tunnels := make([]Tunnel, len(c.tunnels))
	copy(tunnels, c.tunnels)
	c.mu.RUnlock()

	trafficStats := proxy.TunnelTrafficStats()
	connected := c.Connected()

	result := make([]TunnelStatus, 0, len(tunnels))
	for _, t := range tunnels {
		result = append(result, c.buildTunnelStatus(t, connected, trafficStats))
	}
	return result
}

// TunnelStatusByName 返回单个隧道的统一状态
func (c *Client) TunnelStatusByName(name string) (TunnelStatus, error) {
	c.mu.RLock()
	var found *Tunnel
	for i := range c.tunnels {
		if c.tunnels[i].Name == name {
			found = &c.tunnels[i]
			break
		}
	}
	if found == nil {
		c.mu.RUnlock()
		return TunnelStatus{}, fmt.Errorf("tunnel %q not found", name)
	}
	t := *found
	c.mu.RUnlock()

	return c.buildTunnelStatus(t, c.Connected(), proxy.TunnelTrafficStats()), nil
}

// ===== VPN Manager API =====

func (c *Client) VPNManager() *vpn.Manager {
	return c.vpnMgr
}

func (c *Client) VPNList() []vpn.Status {
	return c.vpnMgr.List()
}

func (c *Client) VPNStatus(name string) (vpn.Status, error) {
	return c.vpnMgr.Status(name)
}

func (c *Client) VPNStart(name string) error {
	return c.vpnMgr.Start(name)
}

func (c *Client) VPNStop(name string) error {
	return c.vpnMgr.Stop(name)
}

func (c *Client) VPNCrashLogs(name string) ([]vpn.CrashLog, error) {
	return c.vpnMgr.CrashLogs(name)
}

func (c *Client) VPNVNTData(name string) (*vpn.VNTInfo, []vpn.VNTDeviceItem, []vpn.VNTRouteItem, *vpn.VNTBuildInfo, error) {
	return c.vpnMgr.VNTData(name)
}

func (c *Client) VPNVNTChart(name string) (*vpn.VNTChartA, error) {
	return c.vpnMgr.VNTChart(name)
}

// ===== Ser2MQ Manager API =====

func (c *Client) Ser2MQManager() *ser2mq.Manager {
	return c.ser2mqMgr
}

func (c *Client) Ser2MQList() []ser2mq.Ser2MQStats {
	return c.ser2mqMgr.List()
}

func (c *Client) Ser2MQStatus(name string) (ser2mq.Ser2MQStats, error) {
	return c.ser2mqMgr.Status(name)
}

func (c *Client) Ser2MQStreamHub() *ser2mq.StreamHub {
	return c.ser2mqMgr.StreamHub()
}

// ===== Ser2Net Manager API =====

func (c *Client) Ser2NetManager() *ser2net.Manager {
	return c.ser2netMgr
}

func (c *Client) Ser2NetStreamHub() *ser2net.StreamHub {
	if c.ser2netMgr == nil {
		return nil
	}
	return c.ser2netMgr.StreamHub()
}

func (c *Client) Ser2NetList() []ser2net.Stats {
	return c.ser2netMgr.List()
}

func (c *Client) Ser2NetStatus(name string) (ser2net.Stats, error) {
	return c.ser2netMgr.Status(name)
}
