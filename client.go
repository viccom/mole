package moleAgent_client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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

// Version 客户端版本，编译时通过 -ldflags 注入
var Version = "dev"

// startTime 进程启动时间，供 sysinfo uptime 使用
var startTime = time.Now()

// ===== 公共 API =====

// Client 是 moleAgent_client 的核心类型。
// 外部程序通过 New() 创建、Run() 启动、AddTunnel/RemoveTunnel 管理隧道。
type Client struct {
	cfg       *Config
	transport *transport.SessionManager
	events    *EventBus

	mu      sync.RWMutex
	tunnels []Tunnel

	ctrlMu  sync.Mutex     // 控制命令发送锁
	tunReqs chan tunnelReq // 隧道更新请求队列
	cancel  context.CancelFunc

	// closed 在 Run 退出 / Close 时关闭，用于唤醒阻塞在 requestTunnelMutation
	// 等待应答的调用方（连接断开后 processTunnelUpdates 不再消费队列）。
	// 本客户端为单次 Run 生命周期设计（复用场景请重新 New()）。
	closed     chan struct{}
	closedOnce sync.Once

	restartRequested atomic.Bool // 服务端请求重启，Run() 不再重连

	// ser2mq 隧道管理器
	ser2mqMgr *ser2mq.Manager

	// ser2net 隧道管理器（ser2tcp/ser2udp）
	ser2netMgr *ser2net.Manager

	// vpn-manager 进程管理器
	vpnMgr *vpn.Manager

	// webssh 远程终端管理器
	websshMgr *webssh.Manager

	// p2p 隧道控制器（无 tag hook：默认构建为 nil、调用点 nil-safe，-tags p2p 时为真实现）
	p2p p2pController

	p2pWarnOnce sync.Once // 默认构建下 p2p 隧道静默失效的一次性告警（复审 F9）
}

type tunnelMutationKind int

const (
	tunnelMutationAdd tunnelMutationKind = iota + 1
	tunnelMutationRemove
	tunnelMutationReplaceAll
)

type tunnelMutation struct {
	kind    tunnelMutationKind
	tunnel  Tunnel
	name    string
	tunnels []Tunnel
}

type tunnelReq struct {
	mutation tunnelMutation
	resp     chan error
}

// New 创建客户端实例
func New(cfg *Config) (*Client, error) {
	// -id 参数或配置文件显式指定的 node_id 优先级最高：
	// 短路自动生成逻辑，不读也不写 node.id 文件。
	explicitNodeID := cfg.NodeID != ""

	// 自定义 node.id 路径需在 ApplyDefaults（内部会读文件解析 nodeID）之前生效
	SetNodeIDFile(cfg.NodeIDFile)

	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// 未显式指定时，以 node.id 文件为真相源解析并落盘（首次生成 / 锁定复用）。
	if !explicitNodeID {
		cfg.NodeID = EnsureNodeIDPersisted()
		log.Printf("node.id file: %s (node ID %s)", nodeIDFile, cfg.NodeID)
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
	// nodeID 已解析完毕：显式指定则沿用，否则由 EnsureNodeIDPersisted 从 node.id 文件解析
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
	client := &Client{
		cfg:        cfg,
		transport:  sm,
		events:     newEventBus(),
		tunnels:    append([]Tunnel{}, cfg.Tunnels...),
		tunReqs:    make(chan tunnelReq, 16),
		closed:     make(chan struct{}),
		ser2mqMgr:  ser2mqMgr,
		ser2netMgr: ser2netMgr,
		vpnMgr:     vpnMgr,
		websshMgr:  websshMgr,
	}
	// p2p hook：默认构建为 nil；-tags p2p 构建真实例（依赖 cfg.ServerAddr 派生默认服务器）
	client.p2p = newP2PController(client)
	return client, nil
}

// Run 连接服务端并运行主循环（阻塞，直到 ctx 取消）
func (c *Client) Run(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)
	defer c.cancel()
	defer c.closedOnce.Do(func() { close(c.closed) })

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
			// 注册窗口内入队的变更请求（Connected 已真、消费者未启动）必须排空，
			// 否则滞留至重连后被迟滞应用，调用方却早已放弃
			c.drainTunnelReqs()
			c.sleep(ctx, c.cfg.ReconnectInterval)
			continue
		}

		// 启动心跳和隧道更新处理
		hbCtx, hbCancel := context.WithCancel(ctx)
		updCtx, updCancel := context.WithCancel(ctx)
		go c.heartbeat(hbCtx)
		go c.processTunnelUpdates(updCtx)

		// 注册后立即上报一次隧道状态
		go c.sendTunnelStatus()

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

		// 排空未处理的隧道变更请求并回复错误：
		// 否则滞留请求会在重连后被新 consumer 迟滞应用，且调用方早已超时放弃
		c.drainTunnelReqs()

		if c.restartRequested.Load() {
			log.Println("Restart requested by server, exiting Run()")
			return nil
		}
		log.Printf("Disconnected, reconnecting in %s...", c.cfg.ReconnectInterval)
		c.sleep(ctx, c.cfg.ReconnectInterval)
	}
}

// Close 优雅关闭
func (c *Client) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	c.closedOnce.Do(func() { close(c.closed) })
	// 终态关闭必须锁存（防在途 Connect 发布幽灵会话）；
	// 不能走 c.close()——那是重连周期的非锁存断开
	c.transport.Close()

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
	if c.p2p != nil {
		c.p2p.Close()
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
	if !c.Connected() {
		return fmt.Errorf("not connected to server, tunnel operations require active connection")
	}
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
		// 等待应答时须同时监听关闭信号：Connected() 是 TOCTOU 检查，
		// 入队后连接可能立刻断开，processTunnelUpdates 退出后无人应答
		select {
		case err := <-req.resp:
			return err
		case <-c.closed:
			return fmt.Errorf("client is shutting down, tunnel update aborted")
		}
	default:
		return fmt.Errorf("tunnel update queue full")
	}
}

// drainTunnelReqs 排空未处理的隧道变更请求并回复错误。
// processTunnelUpdates 不在运行的窗口（注册失败重连间隙、断连清理期）
// 滞留的请求会在重连后被迟滞应用，而调用方早已放弃
func (c *Client) drainTunnelReqs() {
	for {
		select {
		case req := <-c.tunReqs:
			req.resp <- fmt.Errorf("connection lost, tunnel update aborted")
			continue
		default:
		}
		break
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
	// p2p：Enable 过滤与 Para 解析在控制器内部做（全量列表传入，
	// 禁用的隧道以「从 map 消失」表达停机信号）
	if c.p2p != nil {
		c.p2p.Notify(tunnels)
	} else {
		// 复审 F9：默认构建收到 p2p 隧道 = 配置有效但本构建不运行，
		// 必须告警而不是静默无 connected（仅告警一次）
		for _, t := range tunnels {
			if t.Type == TunnelTypeP2P {
				c.p2pWarnOnce.Do(func() {
					log.Printf("警告: 本二进制未启用 p2p（需 -tags p2p 编译），p2p 隧道将不会被运行")
				})
				break
			}
		}
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
		SysInfo: collectSysInfo(),
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

	beatCount := 0
	sysinfoInterval := 5 // 每 5 次心跳上报一次 sysinfo
	statusInterval := 2  // 每 2 次心跳上报一次 tunnel_status（约 60s）

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
				// 非锁存断开：随后 Run 会走重连循环
				c.transport.Disconnect()
				return
			}
			c.events.Emit(Event{Type: EventHeartbeatOK})
			beatCount++
			if beatCount%sysinfoInterval == 0 {
				go c.sendSysInfo()
			}
			if beatCount%statusInterval == 0 {
				go c.sendTunnelStatus()
			}
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

	cmd := protocol.ControlCmd{Cmd: "ping", Ts: time.Now().UnixMilli()}
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
	if resp.Ts > 0 {
		rtt := time.Now().UnixMilli() - resp.Ts
		c.events.Emit(Event{Type: EventRTT, Data: map[string]any{"rtt": rtt}})
	}
	return nil
}

// collectSysInfo 收集系统信息
func collectSysInfo() *protocol.SysInfo {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	hostname, _ := os.Hostname()
	return &protocol.SysInfo{
		OS:           runtime.GOOS + "/" + runtime.GOARCH,
		Hostname:     hostname,
		Uptime:       int64(time.Since(startTime).Seconds()),
		GoVersion:    runtime.Version(),
		AgentVersion: Version,
		NumCPU:       runtime.NumCPU(),
		MemTotalMB:   int64(m.Sys / 1024 / 1024),
		MemUsedMB:    int64(m.Alloc / 1024 / 1024),
	}
}

// sendSysInfo 上报系统信息
func (c *Client) sendSysInfo() {
	c.ctrlMu.Lock()
	defer c.ctrlMu.Unlock()

	session := c.transport.Session()
	if session == nil || session.IsClosed() {
		return
	}

	stream, err := session.OpenStream()
	if err != nil {
		return
	}
	defer stream.Close()

	cmd := protocol.ControlCmd{
		Cmd:     "sysinfo",
		NodeID:  c.cfg.NodeID,
		SysInfo: collectSysInfo(),
	}
	if err := writeCmd(stream, cmd); err != nil {
		return
	}

	resp, err := readResponse(stream, c.cfg.HeartbeatTimeout)
	logReadResponse("sysinfo", resp, err)
}

// collectTunnelStatuses 收集各 Manager 的隧道运行时状态
func (c *Client) collectTunnelStatuses() []protocol.TunnelStatus {
	c.mu.RLock()
	tunnels := c.tunnels
	c.mu.RUnlock()

	statuses := make([]protocol.TunnelStatus, 0, len(tunnels))
	for _, t := range tunnels {
		st := protocol.TunnelStatus{
			Name: t.Name,
			Type: string(t.Type),
		}
		switch t.Type {
		case TunnelTypeSer2MQ:
			if stats, err := c.ser2mqMgr.Status(t.Name); err == nil {
				st.Running = stats.Running
				st.Connected = stats.MQTTConnected && stats.SerialOpen
				st.SerialOpen = stats.SerialOpen
				st.MQTTConnected = stats.MQTTConnected
				st.BytesIn = stats.BytesIn
				st.BytesOut = stats.BytesOut
				st.Error = stats.Error
			}
		case TunnelTypeSer2TCP, TunnelTypeSer2UDP:
			if stats, err := c.ser2netMgr.Status(t.Name); err == nil {
				st.Running = stats.Running
				st.Connected = stats.SerialOpen
				st.SerialOpen = stats.SerialOpen
				st.Clients = stats.Clients
				st.BytesIn = stats.BytesIn
				st.BytesOut = stats.BytesOut
			}
		case TunnelTypeVPNMgr:
			if stats, err := c.vpnMgr.Status(t.Name); err == nil {
				st.Running = stats.Running
				st.Connected = stats.Running
				st.PID = stats.PID
				if stats.StartTime > 0 {
					st.UptimeSeconds = time.Now().Unix() - stats.StartTime
				}
				st.Error = stats.Error
			}
		case TunnelTypeHTTP, TunnelTypeHTTPS, TunnelTypeTCP, TunnelTypeUDP:
			st.Running = true
			st.Connected = true
		case TunnelTypeWebSSH:
			if stats, err := c.websshMgr.Status(t.Name); err == nil {
				st.Running = stats.Running
				st.Connected = stats.Sessions > 0
				st.Clients = stats.Sessions
			}
		case TunnelTypeP2P:
			if c.p2p != nil {
				if rt, err := c.p2p.StatusByName(t.Name); err == nil {
					st.Running = rt.Running
					st.Connected = rt.Connected
					st.BytesIn = rt.BytesIn
					st.BytesOut = rt.BytesOut
					st.Error = rt.Error
				}
			}
		}
		statuses = append(statuses, st)
	}
	return statuses
}

// sendTunnelStatus 上报隧道运行时状态
func (c *Client) sendTunnelStatus() {
	// 先在锁外收集状态：collectTunnelStatuses 会取各 Manager 的内部锁，
	// 持 ctrlMu 跨 Manager 锁会与凭据请求构成死锁环（复审 F1）
	statuses := c.collectTunnelStatuses()

	c.ctrlMu.Lock()
	defer c.ctrlMu.Unlock()

	session := c.transport.Session()
	if session == nil || session.IsClosed() {
		return
	}

	stream, err := session.OpenStream()
	if err != nil {
		return
	}
	defer stream.Close()

	cmd := protocol.ControlCmd{
		Cmd:      "tunnel_status",
		NodeID:   c.cfg.NodeID,
		Statuses: statuses,
	}
	if err := writeCmd(stream, cmd); err != nil {
		return
	}

	resp, err := readResponse(stream, c.cfg.HeartbeatTimeout)
	logReadResponse("tunnel_status", resp, err)
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
		if c.handleServerCmd(stream, br) {
			return
		}
	}

	// 检查 TCP/UDP 代理协议头：\x00<tunnel-name>\n
	if peek[0] == 0x00 {
		line, err := br.ReadBytes('\n')
		if err == nil && len(line) > 1 {
			tunnelName := string(line[1 : len(line)-1]) // 跳过 \x00 和 \n
			stream.SetReadDeadline(time.Time{})
			proxy.HandleRawStream(stream, br, func() (string, string) {
				c.mu.RLock()
				defer c.mu.RUnlock()
				for _, t := range c.tunnels {
					if t.Name == tunnelName && t.IsEnabled() && (t.Type == TunnelTypeTCP || t.Type == TunnelTypeUDP) {
						return t.Target, string(t.Type)
					}
				}
				return "", ""
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
	proxy.HandleRawStream(stream, br, func() (string, string) {
		c.mu.RLock()
		defer c.mu.RUnlock()
		for _, t := range c.tunnels {
			if t.IsEnabled() && (t.Type == TunnelTypeTCP || t.Type == TunnelTypeUDP) {
				return t.Target, string(t.Type)
			}
		}
		return "", ""
	}, "")
}

// handleServerCmd 处理服务端推送的控制命令（tunnel_push / tunnel_action / restart）
func (c *Client) handleServerCmd(stream *smux.Stream, br *bufio.Reader) bool {
	stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := br.ReadBytes('\n')
	if err != nil {
		// No newline found — use whatever we have so far (trimmed).
		// Don't wait for \x00 which would block until deadline.
		line = bytes.TrimRight(line, "\x00")
		if len(line) == 0 {
			return false
		}
	}

	var cmd struct {
		Cmd     string            `json:"cmd"`
		Name    string            `json:"name"`
		Action  string            `json:"action"`
		Delay   int               `json:"delay_seconds"`
		Reason  string            `json:"reason"`
		Tunnels []protocol.Tunnel `json:"tunnels"`
	}
	if json.Unmarshal(line, &cmd) != nil {
		return false
	}

	switch cmd.Cmd {
	case "tunnel_push":
		tunnels := fromProtocols(cmd.Tunnels)
		// 服务端推送路径不改变配置，仅记录校验不通过的项。
		// 服务端侧 validateTunnels 已在 pushToClient 前把关，此处是纵深防御：
		// 若服务端校验回退、或历史脏数据经兼容路径推来，客户端留诊断线索。
		// 刻意不过滤——过滤会让被跳过的隧道在后续 tunnel_update 时从服务端
		// 持久化中消失，把「一条配置有问题」放大成「配置丢失」。
		warnInvalidPushedTunnels(tunnels)
		c.mu.Lock()
		c.tunnels = make([]Tunnel, len(tunnels))
		copy(c.tunnels, tunnels)
		c.mu.Unlock()

		c.notifyManagers(tunnels)

		log.Printf("Received tunnel_push from server: %d tunnel(s)", len(tunnels))
		c.events.Emit(Event{Type: EventTunnelUpdated, Data: map[string]any{"tunnels": len(tunnels)}})

		writeResp(stream, "ok", "tunnels updated")

		// 配置变更后上报最新隧道状态
		go c.sendTunnelStatus()
		return true

	case "tunnel_action":
		c.handleTunnelAction(stream, cmd.Name, cmd.Action)
		return true

	case "restart":
		c.handleRestart(stream, cmd.Delay, cmd.Reason)
		return true

	default:
		return false
	}
}

// findTunnel 按名称查找隧道配置
func (c *Client) findTunnel(name string) (Tunnel, bool) {
	for _, t := range c.tunnels {
		if t.Name == name {
			return t, true
		}
	}
	return Tunnel{}, false
}

// handleTunnelAction 处理服务端远程隧道操作
func (c *Client) handleTunnelAction(stream *smux.Stream, name, action string) {
	c.mu.RLock()
	t, found := c.findTunnel(name)
	c.mu.RUnlock()

	if !found {
		writeResp(stream, "err", "tunnel not found: "+name)
		return
	}

	tunnelType := string(t.Type)
	var err error
	switch tunnelType {
	case "vpn-manager":
		switch action {
		case "start":
			err = c.vpnMgr.Start(name)
		case "stop":
			err = c.vpnMgr.Stop(name)
		case "restart":
			if err = c.vpnMgr.Stop(name); err == nil {
				err = c.vpnMgr.Start(name)
			}
		default:
			err = fmt.Errorf("unsupported action: %s", action)
		}
	case "ser2mq":
		switch action {
		case "stop":
			c.mu.RLock()
			configs := c.buildSer2MQConfigs()
			delete(configs, name)
			c.mu.RUnlock()
			c.ser2mqMgr.OnTunnelUpdate(configs)
		case "restart":
			// Stop first by removing from configs, then re-add
			c.mu.RLock()
			configs := c.buildSer2MQConfigs()
			c.mu.RUnlock()
			delete(configs, name)
			c.ser2mqMgr.OnTunnelUpdate(configs)
			// Now start with full configs
			c.mu.RLock()
			configs = c.buildSer2MQConfigs()
			c.mu.RUnlock()
			c.ser2mqMgr.OnTunnelUpdate(configs)
		case "start":
			c.mu.RLock()
			configs := c.buildSer2MQConfigs()
			c.mu.RUnlock()
			c.ser2mqMgr.OnTunnelUpdate(configs)
		default:
			err = fmt.Errorf("unsupported action: %s", action)
		}
	case "ser2tcp", "ser2udp":
		switch action {
		case "stop":
			c.mu.RLock()
			configs := c.buildSer2NetConfigs()
			delete(configs, name)
			c.mu.RUnlock()
			c.ser2netMgr.OnTunnelUpdate(configs)
		case "restart":
			// Stop first by removing from configs, then re-add
			c.mu.RLock()
			configs := c.buildSer2NetConfigs()
			c.mu.RUnlock()
			delete(configs, name)
			c.ser2netMgr.OnTunnelUpdate(configs)
			// Now start with full configs
			c.mu.RLock()
			configs = c.buildSer2NetConfigs()
			c.mu.RUnlock()
			c.ser2netMgr.OnTunnelUpdate(configs)
		case "start":
			c.mu.RLock()
			configs := c.buildSer2NetConfigs()
			c.mu.RUnlock()
			c.ser2netMgr.OnTunnelUpdate(configs)
		default:
			err = fmt.Errorf("unsupported action: %s", action)
		}
	default:
		err = fmt.Errorf("action not supported for type: %s", tunnelType)
	}

	if err != nil {
		writeResp(stream, "err", err.Error())
	} else {
		log.Printf("Tunnel action: %s %s (type=%s)", action, name, tunnelType)
		writeResp(stream, "ok", action+" done")
	}
}

// maxRestartDelay 服务端远程重启的最大延迟秒数，防止误配/恶意的过大值
const maxRestartDelay = 300

// handleRestart 处理服务端远程重启请求。
//
// 仅标记请求并延时中断主循环；资源回收由 main 在 Run 退出后通过 Close() 统一执行，
// 随后 os.Exit 退出进程，由外部进程管理器（systemd/docker --restart 等）拉起以完成重启。
// 这里不提前关闭 managers，避免与 main 的 Close() 重复关闭冲突。
func (c *Client) handleRestart(stream *smux.Stream, delay int, reason string) {
	// CAS 防重复：同一连接多次 restart 只生效一次
	if !c.restartRequested.CompareAndSwap(false, true) {
		writeResp(stream, "err", "restart already in progress")
		return
	}

	// 限制延迟范围：负值立即执行，上限 maxRestartDelay 秒
	if delay < 0 {
		delay = 0
	}
	if delay > maxRestartDelay {
		delay = maxRestartDelay
	}

	writeResp(stream, "ok", fmt.Sprintf("restarting in %ds", delay))

	log.Printf("Server requested restart (delay=%ds, reason=%s)", delay, reason)

	go func() {
		time.Sleep(time.Duration(delay) * time.Second)
		// 中断主循环：transport 断开 → acceptLoop 返回 → Run() 因 restartRequested 退出。
		// main 检测到 Run 退出后执行 Close + os.Exit，由外部拉起。
		// 用非锁存断开：桌面端 relaunch 走全新 Client，锁存无意义且有害
		c.transport.Disconnect()
	}()
}

// RestartRequested 返回服务端是否请求了重启。
// 供调用方（如 main）在 Run 退出后判断是否需要 os.Exit 以便外部进程管理器拉起。
func (c *Client) RestartRequested() bool {
	return c.restartRequested.Load()
}

// buildSer2MQConfigs 从当前隧道列表构建 ser2mq 配置 map
func (c *Client) buildSer2MQConfigs() map[string]ser2mq.Ser2MQConfig {
	configs := make(map[string]ser2mq.Ser2MQConfig)
	for _, t := range c.tunnels {
		if t.Type == TunnelTypeSer2MQ && t.IsEnabled() && t.Para != nil {
			var cfg ser2mq.Ser2MQConfig
			if err := json.Unmarshal(t.Para, &cfg); err == nil {
				cfg.Enable = t.IsEnabled()
				configs[t.Name] = cfg
			}
		}
	}
	return configs
}

// buildSer2NetConfigs 从当前隧道列表构建 ser2net 配置 map
func (c *Client) buildSer2NetConfigs() map[string]ser2net.TunnelConfig {
	configs := make(map[string]ser2net.TunnelConfig)
	for _, t := range c.tunnels {
		if (t.Type == TunnelTypeSer2TCP || t.Type == TunnelTypeSer2UDP) && t.IsEnabled() && t.Para != nil {
			var cfg ser2net.Ser2NetConfig
			if err := json.Unmarshal(t.Para, &cfg); err == nil {
				cfg.Enable = t.IsEnabled()
				configs[t.Name] = ser2net.TunnelConfig{Type: string(t.Type), Config: cfg}
			}
		}
	}
	return configs
}

// close 关闭当前连接（非锁存断开：重连循环的每周期路径，
// 终态关停必须走 Close → transport.Close）
func (c *Client) close() {
	c.transport.Disconnect()
}

// sleep 可取消的休眠
func (c *Client) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// ===== 协议工具函数 =====

// maxControlMsgSize 单条控制消息的大小上限
const maxControlMsgSize = 1 << 20 // 1MB

// readControlMsg 从流中读取一条完整的 JSON 控制消息。
// 每条流仅承载一条消息；服务端响应以 '\n' 结尾（writeJSONLine），
// 同时兼容无换行的裸 JSON：每读到一批数据就尝试解析，解析成功即视为
// 消息完整。修复旧实现单次 Read 假设整条消息一次到达导致的分包失败。
func readControlMsg(r io.Reader, maxSize int) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			if len(buf) > maxSize {
				return nil, fmt.Errorf("control message too large: %d bytes", len(buf))
			}
			if json.Valid(buf) {
				return buf, nil
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

// writeResp 向服务端流写 JSON 响应行（与服务端 writeJSONLine 一致，以 '\n' 结尾）
func writeResp(w io.Writer, cmd, msg string) {
	resp, _ := json.Marshal(map[string]string{"cmd": cmd, "msg": msg})
	// 写失败不能静默：服务端会等到超时（tunnel_push 的探活 3s、其余 10s），
	// 甚至把本节点判为失活。调用方无法补救（流可能已半关），但必须留痕。
	if _, err := w.Write(append(resp, '\n')); err != nil {
		log.Printf("write control response (%s) failed: %v", cmd, err)
	}
}

// logReadResponse 记录控制命令响应的读取/拒绝错误，供周期任务使用。
//
// 调用点由心跳派生（sendSysInfo / sendTunnelStatus）：链路断开后每次心跳
// 都会失败，故刻意只记日志、不改控制流——断开由 sendPing 的失败路径负责，
// 在此重复触发会与心跳竞争断开状态。日志量由心跳周期天然限流
// （默认 20s / 50s 一次）。
//
// 成功响应不产生任何输出（防刷屏）。
func logReadResponse(kind string, resp *protocol.ControlResponse, err error) {
	if err != nil {
		log.Printf("%s response read failed: %v", kind, err)
		return
	}
	if resp != nil && resp.Cmd != "ok" {
		log.Printf("%s rejected by server: %s", kind, resp.Msg)
	}
}

func writeCmd(w io.Writer, cmd protocol.ControlCmd) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
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

	raw, err := readControlMsg(r, maxControlMsgSize)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var resp protocol.ControlResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &resp, nil
}

type Stats struct {
	NodeID       string                `json:"node_id"`
	Connected    bool                  `json:"connected"`
	ServerAddr   string                `json:"server_addr"`
	TCPBytesIn   uint64                `json:"tcp_bytes_in"`
	TCPBytesOut  uint64                `json:"tcp_bytes_out"`
	HTTPBytesIn  uint64                `json:"http_bytes_in"`
	HTTPBytesOut uint64                `json:"http_bytes_out"`
	Tunnels      []proxy.TunnelTraffic `json:"tunnels"`
}

func (c *Client) Stats() Stats {
	tunnelStats := proxy.TunnelTrafficStats()
	current := c.Tunnels() // 统一走锁内快照，禁止绕过 c.mu 直接摸 c.tunnels
	tunnels := make([]proxy.TunnelTraffic, 0, len(current))
	for _, t := range current {
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
		ServerAddr:   c.cfg.ServerAddr,
		TCPBytesIn:   proxy.GetTCPBytesIn(),
		TCPBytesOut:  proxy.GetTCPBytesOut(),
		HTTPBytesIn:  proxy.GetHTTPBytesIn(),
		HTTPBytesOut: proxy.GetHTTPBytesOut(),
		Tunnels:      tunnels,
	}
}

// ===== 统一隧道状态 =====

// TunnelStatus 统一隧道状态（合并配置 + 运行时）
type TunnelStatus struct {
	Name       string          `json:"name"`
	Type       TunnelType      `json:"type"`
	Target     string          `json:"target"`
	Domain     string          `json:"domain,omitempty"`
	ListenPort int             `json:"listen_port,omitempty"`
	Enabled    bool            `json:"enabled"`
	Connected  bool            `json:"connected"`
	BytesIn    uint64          `json:"bytes_in"`
	BytesOut   uint64          `json:"bytes_out"`
	Status     any             `json:"status,omitempty"` // 类型特定状态（Ser2MQStats / vpn.Status）
	Para       json.RawMessage `json:"para,omitempty"`   // 扩展配置（前端编辑表单需要）
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
		if stats, err := c.ser2mqMgr.Status(t.Name); err == nil {
			// 与 collectTunnelStatuses 同一语义：MQTT 与串口同时在线才算连通。
			// Running 是「曾启动成功」的锁存位（handler 无运行期失败回写），拿它
			// 当在线标志会把 MQTT 掉线的链路显示成在线，并与同一响应里的
			// mqtt_connected 徽章自相矛盾（ser2mq.js 的 onlineCount/运行中）
			ts.Connected = stats.MQTTConnected && stats.SerialOpen
			ts.BytesIn = stats.BytesIn
			ts.BytesOut = stats.BytesOut
			ts.Status = stats
		}
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
			ts.Connected = stats.Sessions > 0
			ts.BytesIn = stats.BytesIn
			ts.BytesOut = stats.BytesOut
			ts.Status = stats
		}
	case TunnelTypeP2P:
		if c.p2p != nil {
			if rt, err := c.p2p.StatusByName(t.Name); err == nil {
				ts.Connected = rt.Connected
				ts.BytesIn = rt.BytesIn
				ts.BytesOut = rt.BytesOut
				ts.Status = rt
			}
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
