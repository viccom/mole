package tunnel

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

// isValidNodeID 验证节点 ID 格式：固定 8 个 ASCII 字符，首字符字母，其余字母或数字。
// 逐字节校验（不用 unicode.IsLetter），避免多字节字符因字节长度恰好为 8 而混过。
// 必须与客户端 moleAgent_client.ValidateNodeID 保持一致。
func isValidNodeID(id string) bool {
	if len(id) != 8 {
		return false
	}
	for i := 0; i < 8; i++ {
		c := id[i]
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if i == 0 {
			if !isLetter {
				return false
			}
			continue
		}
		if !isLetter && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// ControlProtocol 命令类型
type ControlCmd struct {
	Cmd      string         `json:"cmd"`                     // register, ping, tunnel_update, tunnel_status, sysinfo, tunnel_action, restart
	NodeID   string         `json:"node_id,omitempty"`       // 注册时使用
	Name     string         `json:"name,omitempty"`          // 节点名称 / 隧道名称
	Token    string         `json:"token,omitempty"`         // 节点令牌
	Tunnels  []core.Tunnel  `json:"tunnels,omitempty"`       // 隧道配置
	Ts       int64          `json:"ts,omitempty"`            // Unix 毫秒（ping RTT）
	Action   string         `json:"action,omitempty"`        // tunnel_action: start/stop/restart
	Delay    int            `json:"delay_seconds,omitempty"` // restart 延迟秒数
	Reason   string         `json:"reason,omitempty"`        // restart 原因
	Statuses []TunnelStatus `json:"statuses,omitempty"`      // tunnel_status 上报
	SysInfo  *SysInfo       `json:"sysinfo,omitempty"`       // 系统信息上报
}

type ControlResponse struct {
	Cmd  string          `json:"cmd"` // ok, pong, err
	Msg  string          `json:"msg,omitempty"`
	Ts   int64           `json:"ts,omitempty"`   // 原样回传（ping RTT）
	Data json.RawMessage `json:"data,omitempty"` // 结构化数据
}

// TunnelStatus 客户端上报的隧道运行时状态
type TunnelStatus struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Running       bool   `json:"running"`
	Connected     bool   `json:"connected,omitempty"`
	SerialOpen    bool   `json:"serial_open,omitempty"`
	MQTTConnected bool   `json:"mqtt_connected,omitempty"`
	Clients       int    `json:"clients,omitempty"`
	PID           int    `json:"pid,omitempty"`
	UptimeSeconds int64  `json:"uptime_seconds,omitempty"`
	BytesIn       uint64 `json:"bytes_in,omitempty"`
	BytesOut      uint64 `json:"bytes_out,omitempty"`
	Error         string `json:"error,omitempty"`
}

// SysInfo 客户端上报的系统信息
type SysInfo struct {
	OS           string `json:"os,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	Uptime       int64  `json:"uptime_seconds,omitempty"`
	GoVersion    string `json:"go_version,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	NumCPU       int    `json:"num_cpu,omitempty"`
	MemTotalMB   int64  `json:"mem_total_mb,omitempty"`
	MemUsedMB    int64  `json:"mem_used_mb,omitempty"`
}

func (s *SysInfo) toCore() *core.SysInfo {
	if s == nil {
		return nil
	}
	return &core.SysInfo{
		OS:           s.OS,
		Hostname:     s.Hostname,
		Uptime:       s.Uptime,
		GoVersion:    s.GoVersion,
		AgentVersion: s.AgentVersion,
		NumCPU:       s.NumCPU,
		MemTotalMB:   s.MemTotalMB,
		MemUsedMB:    s.MemUsedMB,
	}
}

func (s *TunnelStatus) toCore() core.ClientTunnelStatus {
	return core.ClientTunnelStatus{
		Name:          s.Name,
		Type:          s.Type,
		Running:       s.Running,
		Connected:     s.Connected,
		SerialOpen:    s.SerialOpen,
		MQTTConnected: s.MQTTConnected,
		Clients:       s.Clients,
		PID:           s.PID,
		UptimeSeconds: s.UptimeSeconds,
		BytesIn:       s.BytesIn,
		BytesOut:      s.BytesOut,
		Error:         s.Error,
	}
}

// connState 连接状态，用 mutex 保护节点指针的并发访问
// 替代原来的 **core.Node 双重指针模式
type connState struct {
	mu         sync.Mutex
	node       *core.Node
	session    *smux.Session
	grant      *core.NodeAccessGrant // 认证结果（含归属信息）
	remoteAddr string                // 客户端连接地址
}

func (s *connState) setGrant(grant *core.NodeAccessGrant) {
	s.mu.Lock()
	s.grant = grant
	s.mu.Unlock()
}

func (s *connState) getGrant() *core.NodeAccessGrant {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grant
}

func (s *connState) set(node *core.Node) {
	s.mu.Lock()
	s.node = node
	s.mu.Unlock()
}

func (s *connState) get() *core.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.node
}

// ControlServer 控制端口服务
type ControlServer struct {
	addr             string
	transport        Transport      // 传输层（TCP/TLS/KCP/WS 等）
	extraTargets     []listenTarget // 额外传输层监听
	nodeMgr          *node.ShardedNodeManager
	nodeToken        string                       // 全局节点认证令牌（兼容期保留）
	authenticator    core.NodeAccessAuthenticator // 用户级 token 认证服务
	listener         net.Listener
	listeners        []net.Listener                             // 所有活跃 listener（含 primary）
	onNodeChange     func()                                     // 节点变更回调
	onNodeDisconnect func(nodeID string, tunnels []core.Tunnel) // 节点断开回调
	nodeRepo         core.NodeRepo                              // 隧道持久化仓库
	tunnelSvc        core.TunnelConfigManager
	p2pIssuer        P2PSignalTokenIssuer // P2P 信令凭据签发（nil = 命令返回未配置）
}

// P2PSignalTokenIssuer 签发 P2P 信令凭据（service 包实现；tunnel 包不依赖 service）
type P2PSignalTokenIssuer interface {
	IssueP2PSignalToken(nodeID, tunnelName string) (username, password string, expiresAt int64, err error)
}

// SetP2PSignalTokenIssuer 注入 P2P 信令凭据签发服务
func (cs *ControlServer) SetP2PSignalTokenIssuer(issuer P2PSignalTokenIssuer) {
	cs.p2pIssuer = issuer
}

type listenTarget struct {
	addr      string
	transport Transport
}

// NewControlServer 创建控制端口服务
func NewControlServer(addr string, transport Transport, nodeMgr *node.ShardedNodeManager, nodeToken string, nodeRepo core.NodeRepo) *ControlServer {
	return &ControlServer{
		addr:      addr,
		transport: transport,
		nodeMgr:   nodeMgr,
		nodeToken: nodeToken,
		nodeRepo:  nodeRepo,
	}
}

// AddTransport 添加额外传输层监听（如 WS、KCP）
func (cs *ControlServer) AddTransport(addr string, transport Transport) {
	cs.extraTargets = append(cs.extraTargets, listenTarget{addr, transport})
}

// connEntry 携带传输协议标识的连接（用于日志和审计）
type connEntry struct {
	conn          net.Conn
	transportName string
}

// SetOnNodeChange 设置节点变更回调
func (cs *ControlServer) SetOnNodeChange(fn func()) {
	cs.onNodeChange = fn
}

// SetOnNodeDisconnect 设置节点断开回调（用于清理隧道运行时资源）
func (cs *ControlServer) SetOnNodeDisconnect(fn func(nodeID string, tunnels []core.Tunnel)) {
	cs.onNodeDisconnect = fn
}

// SetTunnelConfigManager 设置隧道配置统一服务
func (cs *ControlServer) SetTunnelConfigManager(svc core.TunnelConfigManager) {
	cs.tunnelSvc = svc
}

// SetAuthenticator 设置节点接入认证服务
func (cs *ControlServer) SetAuthenticator(auth core.NodeAccessAuthenticator) {
	cs.authenticator = auth
}

// Start 启动控制端口监听（主传输层 + 额外传输层）
func (cs *ControlServer) Start(ctx context.Context) error {
	// Worker pool（所有 listener 共用）
	connChan := make(chan connEntry, 1000)
	workerCount := runtime.NumCPU() * 2
	for i := 0; i < workerCount; i++ {
		go cs.connectionWorker(ctx, connChan)
	}

	// 启动主 listener
	primaryLn, err := cs.transport.Listen(cs.addr)
	if err != nil {
		return fmt.Errorf("control listen on %s (%s): %w", cs.addr, cs.transport.Name(), err)
	}
	cs.listener = primaryLn
	cs.listeners = append(cs.listeners, primaryLn)
	slog.Info("Control server listening", "addr", cs.addr, "transport", cs.transport.Name())
	go cs.acceptLoop(ctx, primaryLn, connChan, cs.transport.Name())

	// 启动额外 listener（WS、KCP 等）
	for _, lt := range cs.extraTargets {
		ln, err := lt.transport.Listen(lt.addr)
		if err != nil {
			// 清理已启动的 listener
			for _, l := range cs.listeners {
				l.Close()
			}
			return fmt.Errorf("control listen on %s (%s): %w", lt.addr, lt.transport.Name(), err)
		}
		cs.listeners = append(cs.listeners, ln)
		slog.Info("Control server listening", "addr", lt.addr, "transport", lt.transport.Name())
		go cs.acceptLoop(ctx, ln, connChan, lt.transport.Name())
	}

	// 等待关闭
	<-ctx.Done()
	for _, ln := range cs.listeners {
		ln.Close()
	}
	slog.Info("Control server stopped")
	return nil
}

func (cs *ControlServer) acceptLoop(ctx context.Context, ln net.Listener, connChan chan<- connEntry, transportName string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				slog.Error("Accept connection failed", "transport", transportName, "error", err)
				continue
			}
		}

		select {
		case connChan <- connEntry{conn: conn, transportName: transportName}:
		default:
			conn.Close()
			slog.Warn("Connection queue full, rejected", "remote", conn.RemoteAddr(), "transport", transportName)
		}
	}
}

func (cs *ControlServer) connectionWorker(ctx context.Context, connChan <-chan connEntry) {
	for entry := range connChan {
		cs.handleConnection(ctx, entry.conn, entry.transportName)
	}
}

func (cs *ControlServer) handleConnection(ctx context.Context, conn net.Conn, transportName string) {
	remoteAddr := conn.RemoteAddr().String()
	slog.Info("Handling new connection", "remote", remoteAddr, "transport", transportName)

	// KCP probe: client sends a probe byte to trigger Accept; discard it before auth.
	if transportName == "kcp" {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		probe := make([]byte, 1)
		if _, err := io.ReadFull(conn, probe); err != nil {
			slog.Warn("KCP probe read failed", "remote", remoteAddr, "error", err)
			conn.Close()
			return
		}
		conn.SetReadDeadline(time.Time{})
		slog.Debug("KCP probe read OK", "remote", remoteAddr)
	}

	// 1. Challenge-Response 认证
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		slog.Error("Failed to generate challenge", "error", err)
		conn.Close()
		return
	}
	if _, err := conn.Write(challenge); err != nil {
		slog.Error("Failed to send challenge", "remote", remoteAddr, "error", err)
		conn.Close()
		return
	}
	slog.Debug("Challenge sent", "remote", remoteAddr, "transport", transportName)

	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	authLine, err := reader.ReadString('\n')
	if err != nil {
		slog.Warn("Node auth read failed", "remote", remoteAddr, "transport", transportName, "error", err)
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})

	var authMsg struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(authLine), &authMsg); err != nil {
		writeControlResp(conn, "err", "invalid auth format")
		slog.Warn("Node auth format invalid", "remote", remoteAddr, "transport", transportName)
		conn.Close()
		return
	}

	// 统一走 authenticator 认证（内部实现：用户级 token 优先 → legacy 兜底）
	if cs.authenticator != nil {
		grant, err := cs.authenticator.AuthenticateNodeToken(ctx, authMsg.Token)
		if err != nil {
			writeControlResp(conn, "err", "invalid token")
			slog.Warn("Node auth failed", "remote", remoteAddr, "transport", transportName)
			conn.Close()
			return
		}
		writeControlResp(conn, "ok", "authenticated")
		slog.Info("Node authenticated", "remote", remoteAddr, "transport", transportName, "userId", grant.UserID, "legacy", grant.LegacyGlobal)

		// 建立 smux 会话并使用 grant（conn 生命周期转移给 goroutine）
		cs.setupSmuxAndAccept(ctx, &bufferedConn{Conn: conn, reader: reader}, remoteAddr, grant, transportName)
		return
	}

	// 无 authenticator 时回退到旧全局 token 直接比对（兼容未注入场景）
	if subtle.ConstantTimeCompare([]byte(authMsg.Token), []byte(cs.nodeToken)) == 1 {
		writeControlResp(conn, "ok", "authenticated")
		slog.Info("Node authenticated (legacy fallback)", "remote", remoteAddr, "transport", transportName)

		cs.setupSmuxAndAccept(ctx, &bufferedConn{Conn: conn, reader: reader}, remoteAddr, &core.NodeAccessGrant{
			UserID:       "system",
			LegacyGlobal: true,
		}, transportName)
		return
	}

	writeControlResp(conn, "err", "invalid token")
	slog.Warn("Node auth failed", "remote", remoteAddr, "transport", transportName, "reason", "invalid token")
	conn.Close()
}

// setupSmuxAndAccept 建立 smux 会话并在独立 goroutine 中接收流。
// conn 的生命周期由内部 goroutine 管理，调用方不再负责关闭。
func (cs *ControlServer) setupSmuxAndAccept(ctx context.Context, conn net.Conn, remoteAddr string, grant *core.NodeAccessGrant, transportName string) {
	keepAliveInterval := 30 * time.Second
	keepAliveTimeout := 90 * time.Second
	if transportName == "kcp" {
		keepAliveInterval = 5 * time.Second
		keepAliveTimeout = 15 * time.Second
	}

	session, err := smux.Server(conn, &smux.Config{
		Version:           2,
		KeepAliveDisabled: false,
		KeepAliveInterval: keepAliveInterval,
		KeepAliveTimeout:  keepAliveTimeout,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  32 * 1024 * 1024,
		MaxStreamBuffer:   4 * 1024 * 1024,
	})
	if err != nil {
		slog.Error("Failed to create smux session", "remote", remoteAddr, "transport", transportName, "error", err)
		conn.Close()
		return
	}
	slog.Info("Smux session created", "remote", remoteAddr, "transport", transportName,
		"keepAliveInterval", keepAliveInterval, "keepAliveTimeout", keepAliveTimeout)

	state := &connState{session: session, grant: grant, remoteAddr: remoteAddr}

	// AcceptStream 循环在独立 goroutine 中运行，不阻塞 worker
	go func() {
		defer func() {
			node := state.get()
			if node != nil {
				// Guard: if another connection displaced us (same nodeID,
				// different session), skip cleanup to avoid removing the
				// newly registered node.
				displaced := false
				if _, ok := cs.nodeMgr.Get(ctx, node.ID); ok {
					if currentSess, err := cs.nodeMgr.GetSession(ctx, node.ID); err == nil && currentSess != session {
						displaced = true
					}
				}

				if displaced {
					slog.Debug("Node re-registered by new connection, skipping cleanup", "nodeId", node.ID)
				} else {
					tunnels := append([]core.Tunnel(nil), node.Tunnels...)

					cs.nodeMgr.Update(ctx, node.ID, func(n *core.Node) {
						n.Status = core.NodeStatusOffline
					})
					cs.nodeMgr.Remove(ctx, node.ID)

					if cs.onNodeDisconnect != nil {
						cs.onNodeDisconnect(node.ID, tunnels)
					}
					slog.Info("Node disconnected", "nodeId", node.ID, "remote", remoteAddr)
				}
			}
			session.Close()
			conn.Close()
			slog.Info("Connection cleanup complete", "remote", remoteAddr, "transport", transportName)
		}()

		for {
			stream, err := session.AcceptStream()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
				slog.Debug("AcceptStream error", "error", err)
				return
			}

			go cs.handleStream(ctx, stream, state)
		}
	}()
}

// maxControlMsgSize 单条控制消息的大小上限，防止异常客户端耗尽服务端内存
const maxControlMsgSize = 1 << 20 // 1MB

// readControlMsg 从流中读取一条完整的 JSON 控制消息。
// 每条流仅承载一条消息；新版写侧以 '\n' 结尾（writeJSONLine），
// 同时兼容旧客户端无换行的裸 JSON：每读到一批数据就尝试解析，
// 解析成功即视为消息完整。修复旧实现单次 Read 假设整条消息
// 一次到达导致的分包失败 / 4KB 截断问题。
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

func (cs *ControlServer) handleStream(ctx context.Context, stream *smux.Stream, state *connState) {
	defer stream.Close()

	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	data, err := readControlMsg(stream, maxControlMsgSize)
	if err != nil {
		slog.Debug("Stream read error", "error", err)
		return
	}
	stream.SetReadDeadline(time.Time{})

	var cmd ControlCmd
	if err := json.Unmarshal(data, &cmd); err != nil {
		writeControlResp(stream, "err", "invalid json")
		return
	}

	switch cmd.Cmd {
	case "register":
		cs.handleRegister(ctx, cmd, state, stream)
	case "ping":
		node := state.get()
		if node != nil {
			cs.nodeMgr.Update(ctx, node.ID, func(n *core.Node) {
				now := time.Now()
				n.LastHeartbeat = &now
				if cmd.Ts > 0 {
					n.RTT = now.UnixMilli() - cmd.Ts
				}
			})
		}
		writeControlRespTs(stream, "pong", "", cmd.Ts)
	case "tunnel_update":
		cs.handleTunnelUpdate(ctx, cmd, state, stream)
	case "sysinfo":
		node := state.get()
		if node != nil && cmd.SysInfo != nil {
			si := cmd.SysInfo.toCore()
			if err := cs.nodeMgr.Update(ctx, node.ID, func(n *core.Node) {
				n.SysInfo = si
			}); err != nil {
				slog.Debug("sysinfo update failed", "node", node.ID, "error", err)
			}
		}
		writeControlResp(stream, "ok", "sysinfo received")
	case "tunnel_status":
		node := state.get()
		if node != nil && len(cmd.Statuses) > 0 {
			statuses := make([]core.ClientTunnelStatus, len(cmd.Statuses))
			for i, s := range cmd.Statuses {
				statuses[i] = s.toCore()
			}
			if err := cs.nodeMgr.Update(ctx, node.ID, func(n *core.Node) {
				n.ClientStatuses = statuses
			}); err != nil {
				slog.Debug("tunnel_status update failed", "node", node.ID, "error", err)
			}
		}
		writeControlResp(stream, "ok", "status received")
	case "p2p_signal_token":
		cs.handleP2PSignalToken(ctx, cmd, state, stream)
	default:
		writeControlResp(stream, "err", "unknown command")
	}
}

// p2pSignalTokenResp 是 p2p_signal_token 命令的 ad-hoc 响应（与 pong 携带 ts 同款做法：
// 协议层无共享 types，两端各自解码，不扩 ControlResponse）
type p2pSignalTokenResp struct {
	Cmd       string `json:"cmd"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// handleP2PSignalToken 处理 C→S p2p_signal_token：校验连接已认证 + name 归属该校验连接
// 的节点且类型为 p2p（纵深防御，token 本身只授 nat-exchange/*），签发并平铺 JSON 返回。
// 不写 Para（凭据不能进配置，admin/持久化面不接触明文 secret）。
func (cs *ControlServer) handleP2PSignalToken(ctx context.Context, cmd ControlCmd, state *connState, stream *smux.Stream) {
	fail := func(msg string) {
		_ = writeJSONLine(stream, p2pSignalTokenResp{Cmd: "p2p_signal_token", Error: msg})
	}
	node := state.get()
	if node == nil {
		fail("not authenticated")
		return
	}
	if cs.p2pIssuer == nil {
		fail("p2p signal token issuer not configured")
		return
	}
	if cmd.Name == "" {
		fail("name is required")
		return
	}
	// 发现检查以持久层真相源为准：nodeMgr 内存可能包含注册竞态产物
	// （handleRegister 先写内存、SyncFromClient 配对拒绝后仅记警告不回滚，
	// 审查 #1），凭据签发不能认它。nodeRepo 为 nil 时回退内存（测试场景）。
	var tunnels []core.Tunnel
	if cs.nodeRepo != nil {
		if persisted, err := cs.nodeRepo.GetByID(node.ID); err == nil && persisted != nil {
			tunnels = persisted.Tunnels
		}
	} else if cur, ok := cs.nodeMgr.Get(ctx, node.ID); ok {
		tunnels = cur.Tunnels
	}
	found := false
	for _, t := range tunnels {
		// 禁用隧道不签发：「禁用即切断信令」（审查 #8）
		if t.Name == cmd.Name && t.Type == core.TunnelTypeP2P && t.IsEnabled() {
			found = true
			break
		}
	}
	if !found {
		fail("p2p tunnel not found on node")
		return
	}
	username, password, expiresAt, err := cs.p2pIssuer.IssueP2PSignalToken(node.ID, cmd.Name)
	if err != nil {
		slog.Error("issue p2p signal token failed", "node", node.ID, "tunnel", cmd.Name, "error", err)
		fail("issue failed")
		return
	}
	if err := writeJSONLine(stream, p2pSignalTokenResp{
		Cmd: "p2p_signal_token", OK: true,
		Username: username, Password: password, ExpiresAt: expiresAt,
	}); err != nil {
		slog.Debug("write p2p_signal_token response failed", "error", err)
	}
}

func (cs *ControlServer) handleRegister(ctx context.Context, cmd ControlCmd, state *connState, stream *smux.Stream) {
	if cmd.NodeID == "" {
		writeControlResp(stream, "err", "node_id is required")
		return
	}
	if !isValidNodeID(cmd.NodeID) {
		writeControlResp(stream, "err", "node_id must be exactly 8 alphanumeric characters starting with a letter")
		return
	}

	// 获取 Session：从 connState 获取（在 handleConnection 中建立 smux 会话时存入）
	smuxSession := state.session

	now := time.Now()
	node := &core.Node{
		ID:            cmd.NodeID,
		Name:          cmd.Name,
		Token:         cmd.Token,
		Status:        core.NodeStatusOnline,
		Tunnels:       cmd.Tunnels,
		RemoteAddr:    state.remoteAddr,
		ConnectedAt:   &now,
		LastHeartbeat: &now,
		SysInfo:       cmd.SysInfo.toCore(),
	}

	// 从认证结果中读取归属信息
	if grant := state.grant; grant != nil {
		node.OwnerUserID = grant.UserID
		node.AccessTokenID = grant.AccessTokenID
	}

	if err := cs.nodeMgr.Add(ctx, node); err != nil {
		if err == core.ErrNodeExists {
			// 同一节点重连：先探测旧 session 是否真活
			oldNode, oldOk := cs.nodeMgr.Get(ctx, cmd.NodeID)
			if oldOk && cs.probeOldSession(ctx, cmd.NodeID, oldNode.Tunnels) {
				slog.Info("Node already online, old session alive", "nodeId", cmd.NodeID)
				writeControlResp(stream, "err", "node already online")
				return
			}
			// 旧 session 假死，置换
			slog.Info("Node reconnected, displacing dead session", "nodeId", cmd.NodeID)
			var oldTunnels []core.Tunnel
			if oldOk {
				oldTunnels = append([]core.Tunnel(nil), oldNode.Tunnels...)
			}
			cs.nodeMgr.Remove(ctx, cmd.NodeID)
			if cs.onNodeDisconnect != nil {
				cs.onNodeDisconnect(cmd.NodeID, oldTunnels)
			}
			if err2 := cs.nodeMgr.Add(ctx, node); err2 != nil {
				writeControlResp(stream, "err", err2.Error())
				return
			}
		} else {
			writeControlResp(stream, "err", err.Error())
			return
		}
	}

	// 独立绑定运行态会话（与 Node 领域模型分离）
	cs.nodeMgr.AddSession(ctx, cmd.NodeID, smuxSession)

	state.set(node)
	slog.Info("Node registered",
		"nodeId", cmd.NodeID,
		"name", cmd.Name,
		"tunnels", len(cmd.Tunnels),
		"ownerUserId", node.OwnerUserID,
	)

	if cs.onNodeChange != nil {
		cs.onNodeChange()
	}

	writeControlResp(stream, "ok", "registered")

	// 注册成功后，优先由统一服务处理持久化配置加载；未注入时走兼容路径。
	if cs.tunnelSvc != nil {
		loaded, err := cs.tunnelSvc.LoadPersisted(ctx, cmd.NodeID)
		if err != nil {
			slog.Warn("Failed to load persisted tunnels", "nodeId", cmd.NodeID, "error", err)
		} else if len(loaded) == 0 {
			if err := cs.tunnelSvc.SyncFromClient(ctx, cmd.NodeID, cmd.Tunnels); err != nil {
				slog.Warn("Failed to persist initial node tunnels", "nodeId", cmd.NodeID, "error", err)
			}
		}
	} else if cs.nodeRepo != nil {
		persisted, err := cs.nodeRepo.GetByID(cmd.NodeID)
		if err == nil && len(persisted.Tunnels) > 0 {
			cs.nodeMgr.Update(ctx, cmd.NodeID, func(n *core.Node) {
				n.Tunnels = persisted.Tunnels
			})
			go func() {
				if err := cs.PushTunnelUpdate(ctx, cmd.NodeID, persisted.Tunnels); err != nil {
					slog.Warn("Failed to push persisted tunnels", "nodeId", cmd.NodeID, "error", err)
				} else {
					slog.Info("Pushed persisted tunnels to node",
						"nodeId", cmd.NodeID, "tunnels", len(persisted.Tunnels))
				}
			}()
		}
		if n, ok := cs.nodeMgr.Get(ctx, cmd.NodeID); ok {
			cs.persistNode(n)
		}
	}
}

// probeOldSession 通过旧 smux session 向旧客户端发送 tunnel_push 并等待响应。
// 3 秒内有响应说明旧客户端真活；否则判定为假死。
func (cs *ControlServer) probeOldSession(ctx context.Context, nodeID string, tunnels []core.Tunnel) bool {
	sess, err := cs.nodeMgr.GetSession(ctx, nodeID)
	if err != nil {
		return false
	}

	stream, err := sess.OpenStream()
	if err != nil {
		return false
	}
	defer stream.Close()

	// 发送 tunnel_push 作为探测（客户端会回复 {"cmd":"ok",...}）
	stream.SetWriteDeadline(time.Now().Add(3 * time.Second))
	cmd := ControlCmd{Cmd: "tunnel_push", Tunnels: tunnels}
	data, _ := json.Marshal(cmd)
	if _, err := stream.Write(append(data, '\n')); err != nil {
		return false
	}

	// 等待客户端响应
	stream.SetReadDeadline(time.Now().Add(3 * time.Second))
	raw, err := readControlMsg(stream, maxControlMsgSize)
	if err != nil {
		return false
	}

	var resp ControlResponse
	return json.Unmarshal(raw, &resp) == nil && resp.Cmd == "ok"
}

// writeControlResp 向控制流写入 JSON 响应行
func writeControlResp(w interface{ Write([]byte) (int, error) }, cmd, msg string) {
	resp := ControlResponse{Cmd: cmd, Msg: msg}
	if err := writeJSONLine(w, resp); err != nil {
		slog.Debug("Failed to write control response", "cmd", cmd, "error", err)
	}
}

// writeControlRespTs 向控制流写入带时间戳的 JSON 响应行（ping RTT 用）
func writeControlRespTs(w interface{ Write([]byte) (int, error) }, cmd, msg string, ts int64) {
	resp := ControlResponse{Cmd: cmd, Msg: msg, Ts: ts}
	if err := writeJSONLine(w, resp); err != nil {
		slog.Debug("Failed to write control response", "cmd", cmd, "error", err)
	}
}

// sendToNode 向指定节点发送命令并等待响应（通用 S→C 方法）
func (cs *ControlServer) sendToNode(ctx context.Context, nodeID string, cmd ControlCmd, timeout time.Duration) (*ControlResponse, error) {
	session, err := cs.nodeMgr.GetSession(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	stream, err := session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()

	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	if _, err := stream.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	stream.SetReadDeadline(time.Now().Add(timeout))
	scanner := bufio.NewScanner(io.LimitReader(stream, 1<<20)) // 1MB max response
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("read response: EOF")
	}

	var resp ControlResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &resp, nil
}

// TriggerTunnelAction 向指定节点发送隧道操作命令（start/stop/restart）
func (cs *ControlServer) TriggerTunnelAction(ctx context.Context, nodeID, name, action string) error {
	cmd := ControlCmd{
		Cmd:    "tunnel_action",
		Name:   name,
		Action: action,
	}
	resp, err := cs.sendToNode(ctx, nodeID, cmd, 10*time.Second)
	if err != nil {
		return err
	}
	if resp.Cmd != "ok" {
		return fmt.Errorf("action rejected: %s", resp.Msg)
	}
	return nil
}

// RestartNode 向指定节点发送重启命令
func (cs *ControlServer) RestartNode(ctx context.Context, nodeID string, delay int, reason string) error {
	if delay < 0 {
		delay = 0
	} else if delay > 300 {
		delay = 300
	}
	cmd := ControlCmd{
		Cmd:    "restart",
		Delay:  delay,
		Reason: reason,
	}
	resp, err := cs.sendToNode(ctx, nodeID, cmd, 10*time.Second)
	if err != nil {
		return err
	}
	if resp.Cmd != "ok" {
		return fmt.Errorf("restart rejected: %s", resp.Msg)
	}
	return nil
}

func (cs *ControlServer) handleTunnelUpdate(ctx context.Context, cmd ControlCmd, state *connState, stream *smux.Stream) {
	node := state.get()
	if node == nil {
		writeControlResp(stream, "err", "node not registered")
		return
	}

	nodeID := node.ID
	if err := cs.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		now := time.Now()
		n.LastHeartbeat = &now
	}); err != nil {
		writeControlResp(stream, "err", err.Error())
		return
	}

	if cs.tunnelSvc != nil {
		if err := cs.tunnelSvc.SyncFromClient(ctx, nodeID, cmd.Tunnels); err != nil {
			writeControlResp(stream, "err", err.Error())
			return
		}
	} else {
		if cs.onNodeChange != nil {
			cs.onNodeChange()
		}
		if cs.nodeRepo != nil {
			if n, ok := cs.nodeMgr.Get(ctx, nodeID); ok {
				cs.persistNode(n)
			}
		}
	}

	writeControlResp(stream, "ok", "tunnels updated")
}

// persistNode 持久化节点信息（主要是隧道配置）
func (cs *ControlServer) persistNode(n *core.Node) {
	if cs.nodeRepo == nil || n == nil {
		return
	}
	// Create or Update：先尝试 GetByID 判断是否已存在
	if existing, err := cs.nodeRepo.GetByID(n.ID); err != nil || existing == nil {
		if err := cs.nodeRepo.Create(n); err != nil {
			slog.Debug("Failed to persist node (create)", "nodeId", n.ID, "error", err)
		}
	} else {
		if err := cs.nodeRepo.Update(n); err != nil {
			slog.Debug("Failed to persist node (update)", "nodeId", n.ID, "error", err)
		}
	}
}

// PushTunnelUpdate 通过 smux 会话向指定节点推送隧道配置更新
func (cs *ControlServer) PushTunnelUpdate(ctx context.Context, nodeID string, tunnels []core.Tunnel) error {
	session, err := cs.nodeMgr.GetSession(ctx, nodeID)
	if err != nil {
		return err
	}

	stream, err := session.OpenStream()
	if err != nil {
		return fmt.Errorf("open push stream: %w", err)
	}
	defer stream.Close()

	cmd := ControlCmd{
		Cmd:     "tunnel_push",
		Tunnels: tunnels,
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal tunnel_push: %w", err)
	}
	if _, err := stream.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("send tunnel_push: %w", err)
	}

	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	raw, err := readControlMsg(stream, maxControlMsgSize)
	if err != nil {
		return fmt.Errorf("read tunnel_push response: %w", err)
	}

	var resp ControlResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse tunnel_push response: %w", err)
	}
	if resp.Cmd != "ok" {
		return fmt.Errorf("tunnel_push rejected: %s", resp.Msg)
	}

	return nil
}

// bufferedConn wraps net.Conn to drain bufio.Reader buffered data first,
// preventing data loss between auth and smux handshake.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}
