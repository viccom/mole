package tunnel

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"sync"
	"time"
	"unicode"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

// isValidNodeID 验证节点 ID 格式：固定 8 字符，首字符字母，其余字母或数字
func isValidNodeID(id string) bool {
	if len(id) != 8 {
		return false
	}
	for i, r := range id {
		if i == 0 {
			if !unicode.IsLetter(r) {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}

// ControlProtocol 命令类型
type ControlCmd struct {
	Cmd     string        `json:"cmd"`               // register, ping, tunnel_update
	NodeID  string        `json:"node_id,omitempty"` // 注册时使用
	Name    string        `json:"name,omitempty"`    // 节点名称
	Token   string        `json:"token,omitempty"`   // 节点令牌
	Tunnels []core.Tunnel `json:"tunnels,omitempty"` // 隧道配置
}

type ControlResponse struct {
	Cmd string `json:"cmd"` // ok, pong, err
	Msg string `json:"msg,omitempty"`
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
	addr            string
	nodeMgr         *node.ShardedNodeManager
	nodeToken       string                    // 全局节点认证令牌（兼容期保留）
	authenticator   core.NodeAccessAuthenticator // 用户级 token 认证服务
	tlsConfig       *tls.Config // TLS 配置
	listener        net.Listener
	onNodeChange    func()        // 节点变更回调
	onNodeDisconnect func(nodeID string, tunnels []core.Tunnel) // 节点断开回调
	nodeRepo        core.NodeRepo // 隧道持久化仓库
	tunnelSvc       core.TunnelConfigManager
}

// NewControlServer 创建控制端口服务
func NewControlServer(addr string, nodeMgr *node.ShardedNodeManager, nodeToken string, tlsConfig *tls.Config, nodeRepo core.NodeRepo) *ControlServer {
	return &ControlServer{
		addr:      addr,
		nodeMgr:   nodeMgr,
		nodeToken: nodeToken,
		tlsConfig: tlsConfig,
		nodeRepo:  nodeRepo,
	}
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

// Start 启动控制端口监听
func (cs *ControlServer) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", cs.addr)
	if err != nil {
		return fmt.Errorf("control listen on %s: %w", cs.addr, err)
	}
	if cs.tlsConfig != nil {
		listener = tls.NewListener(listener, cs.tlsConfig)
		slog.Info("Control server TLS enabled", "addr", cs.addr)
	}
	cs.listener = listener

	slog.Info("Control server listening", "addr", cs.addr)

	// Worker pool
	connChan := make(chan net.Conn, 1000)
	workerCount := runtime.NumCPU() * 2
	for i := 0; i < workerCount; i++ {
		go cs.connectionWorker(ctx, connChan)
	}

	go func() {
		<-ctx.Done()
		listener.Close()
		slog.Info("Control server stopped")
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Error("Accept connection failed", "error", err)
				continue
			}
		}

		select {
		case connChan <- conn:
		default:
			conn.Close()
			slog.Warn("Connection queue full, rejected", "remote", conn.RemoteAddr())
		}
	}
}

func (cs *ControlServer) connectionWorker(ctx context.Context, connChan <-chan net.Conn) {
	for conn := range connChan {
		cs.handleConnection(ctx, conn)
	}
}

func (cs *ControlServer) handleConnection(ctx context.Context, conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()
	defer conn.Close()

	// 1. Challenge-Response 认证
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		slog.Error("Failed to generate challenge", "error", err)
		return
	}
	if _, err := conn.Write(challenge); err != nil {
		slog.Error("Failed to send challenge", "remote", remoteAddr, "error", err)
		return
	}

	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	authLine, err := reader.ReadString('\n')
	if err != nil {
		slog.Warn("Node auth read failed", "remote", remoteAddr, "error", err)
		return
	}
	conn.SetReadDeadline(time.Time{})

	var authMsg struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(authLine), &authMsg); err != nil {
		writeControlResp(conn, "err", "invalid auth format")
		slog.Warn("Node auth format invalid", "remote", remoteAddr)
		return
	}

	// 统一走 authenticator 认证（内部实现：用户级 token 优先 → legacy 兜底）
	if cs.authenticator != nil {
		grant, err := cs.authenticator.AuthenticateNodeToken(ctx, authMsg.Token)
		if err != nil {
			writeControlResp(conn, "err", "invalid token")
			slog.Warn("Node auth failed", "remote", remoteAddr)
			return
		}
		writeControlResp(conn, "ok", "authenticated")
		slog.Info("Node authenticated", "remote", remoteAddr, "userId", grant.UserID, "legacy", grant.LegacyGlobal)

		// 建立 smux 会话并使用 grant
		cs.setupSmuxAndAccept(ctx, conn, remoteAddr, grant)
		return
	}

	// 无 authenticator 时回退到旧全局 token 直接比对（兼容未注入场景）
	if subtle.ConstantTimeCompare([]byte(authMsg.Token), []byte(cs.nodeToken)) == 1 {
		writeControlResp(conn, "ok", "authenticated")
		slog.Info("Node authenticated (legacy fallback)", "remote", remoteAddr)

		cs.setupSmuxAndAccept(ctx, conn, remoteAddr, &core.NodeAccessGrant{
			UserID:       "system",
			LegacyGlobal: true,
		})
		return
	}

	writeControlResp(conn, "err", "invalid token")
	slog.Warn("Node auth failed", "remote", remoteAddr, "reason", "invalid token")

}

// setupSmuxAndAccept 建立 smux 会话并开始接收流
func (cs *ControlServer) setupSmuxAndAccept(ctx context.Context, conn net.Conn, remoteAddr string, grant *core.NodeAccessGrant) {
	session, err := smux.Server(conn, &smux.Config{
		Version:           2,
		KeepAliveDisabled: false,
		KeepAliveInterval: 30 * time.Second,
		KeepAliveTimeout:  90 * time.Second,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  32 * 1024 * 1024,
		MaxStreamBuffer:   4 * 1024 * 1024,
	})
	if err != nil {
		slog.Error("Failed to create smux session", "remote", remoteAddr, "error", err)
		return
	}

	state := &connState{session: session, grant: grant, remoteAddr: remoteAddr}

	defer func() {
		node := state.get()
		if node != nil {
			// 快照隧道列表（Remove 后将无法从 nodeMgr 获取）
			tunnels := append([]core.Tunnel(nil), node.Tunnels...)

			cs.nodeMgr.Update(ctx, node.ID, func(n *core.Node) {
				n.Status = core.NodeStatusOffline
			})
			cs.nodeMgr.Remove(ctx, node.ID)

			// 回调清理隧道运行时资源（监听器、路由索引、统计）
			if cs.onNodeDisconnect != nil {
				cs.onNodeDisconnect(node.ID, tunnels)
			}
			slog.Info("Node disconnected", "nodeId", node.ID, "remote", remoteAddr)
		}
		session.Close()
	}()

	// 接收流（注册、心跳）
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
}

func (cs *ControlServer) handleStream(ctx context.Context, stream *smux.Stream, state *connState) {
	defer stream.Close()

	buf := make([]byte, 4096)
	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := stream.Read(buf)
	if err != nil {
		slog.Debug("Stream read error", "error", err)
		return
	}
	stream.SetReadDeadline(time.Time{})

	var cmd ControlCmd
	if err := json.Unmarshal(buf[:n], &cmd); err != nil {
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
			})
		}
		writeControlResp(stream, "pong", "")
	case "tunnel_update":
		cs.handleTunnelUpdate(ctx, cmd, state, stream)
	default:
		writeControlResp(stream, "err", "unknown command")
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
	}

	// 从认证结果中读取归属信息
	if grant := state.grant; grant != nil {
		node.OwnerUserID = grant.UserID
		node.AccessTokenID = grant.AccessTokenID
	}

	if err := cs.nodeMgr.Add(ctx, node); err != nil {
		writeControlResp(stream, "err", err.Error())
		return
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

// writeControlResp 向控制流写入 JSON 响应行
func writeControlResp(w interface{ Write([]byte) (int, error) }, cmd, msg string) {
	resp := ControlResponse{Cmd: cmd, Msg: msg}
	if err := writeJSONLine(w, resp); err != nil {
		slog.Debug("Failed to write control response", "cmd", cmd, "error", err)
	}
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

	buf := make([]byte, 4096)
	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	nr, err := stream.Read(buf)
	if err != nil {
		return fmt.Errorf("read tunnel_push response: %w", err)
	}

	var resp ControlResponse
	if err := json.Unmarshal(buf[:nr], &resp); err != nil {
		return fmt.Errorf("parse tunnel_push response: %w", err)
	}
	if resp.Cmd != "ok" {
		return fmt.Errorf("tunnel_push rejected: %s", resp.Msg)
	}

	return nil
}
