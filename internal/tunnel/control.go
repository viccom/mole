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
	"time"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

// ControlProtocol 命令类型
type ControlCmd struct {
	Cmd      string            `json:"cmd"`                // register, ping
	NodeID   string            `json:"node_id,omitempty"`  // 注册时使用
	Name     string            `json:"name,omitempty"`     // 节点名称
	Token    string            `json:"token,omitempty"`    // 节点令牌
	Tunnels  []core.Tunnel     `json:"tunnels,omitempty"`  // 隧道配置
}

type ControlResponse struct {
	Cmd  string `json:"cmd"`  // ok, pong, err
	Msg  string `json:"msg,omitempty"`
}

// ControlServer 控制端口服务
type ControlServer struct {
	addr       string
	nodeMgr    *node.ShardedNodeManager
	nodeToken  string          // 全局节点认证令牌（可改为 per-node）
	tlsConfig  *tls.Config      // TLS 配置，为 nil 则不使用 TLS
	listener   net.Listener
}

// NewControlServer 创建控制端口服务
func NewControlServer(addr string, nodeMgr *node.ShardedNodeManager, nodeToken string, tlsConfig *tls.Config) *ControlServer {
	return &ControlServer{
		addr:      addr,
		nodeMgr:   nodeMgr,
		nodeToken: nodeToken,
		tlsConfig: tlsConfig,
	}
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

	// 读取客户端响应：JSON 格式 {"token":"xxx"}\n
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
		conn.Write([]byte(`{"cmd":"err","msg":"invalid auth format"}` + "\n"))
		slog.Warn("Node auth format invalid", "remote", remoteAddr)
		return
	}

	// 验证 token（使用恒定时间比较防止时序攻击）
	if subtle.ConstantTimeCompare([]byte(authMsg.Token), []byte(cs.nodeToken)) != 1 {
		conn.Write([]byte(`{"cmd":"err","msg":"invalid token"}` + "\n"))
		slog.Warn("Node auth failed", "remote", remoteAddr, "reason", "invalid token")
		return
	}
	conn.Write([]byte(`{"cmd":"ok","msg":"authenticated"}` + "\n"))
	slog.Info("Node authenticated", "remote", remoteAddr)

	// 2. 建立 smux 会话
	session, err := smux.Server(conn, &smux.Config{
		Version:           2,
		KeepAliveDisabled: false,
		KeepAliveInterval: 30 * time.Second,
		KeepAliveTimeout:  90 * time.Second,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  4194304,
		MaxStreamBuffer:   256 * 1024,
	})
	if err != nil {
		slog.Error("Failed to create smux session", "remote", remoteAddr, "error", err)
		return
	}

	var registeredNode *core.Node

	defer func() {
		if registeredNode != nil {
			registeredNode.Status = core.NodeStatusOffline
			cs.nodeMgr.Remove(ctx, registeredNode.ID)
			slog.Info("Node disconnected", "nodeId", registeredNode.ID, "remote", remoteAddr)
		}
		session.Close()
	}()

	// 3. 接收流（注册、心跳）
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			// smux session error
			slog.Debug("AcceptStream error", "error", err)
			return
		}

		go cs.handleStream(ctx, stream, session, remoteAddr, &registeredNode)
	}
}

func (cs *ControlServer) handleStream(ctx context.Context, stream *smux.Stream, session *smux.Session, remoteAddr string, regNode **core.Node) {
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
		resp := ControlResponse{Cmd: "err", Msg: "invalid json"}
		data, _ := json.Marshal(resp)
		stream.Write(append(data, '\n'))
		return
	}

	switch cmd.Cmd {
	case "register":
		cs.handleRegister(ctx, cmd, session, remoteAddr, regNode, stream)
	case "ping":
		now := time.Now()
		if *regNode != nil {
			(*regNode).LastHeartbeat = &now
		}
		resp := ControlResponse{Cmd: "pong"}
		data, _ := json.Marshal(resp)
		stream.Write(append(data, '\n'))
	default:
		resp := ControlResponse{Cmd: "err", Msg: "unknown command"}
		data, _ := json.Marshal(resp)
		stream.Write(append(data, '\n'))
	}
}

func (cs *ControlServer) handleRegister(ctx context.Context, cmd ControlCmd, session *smux.Session, remoteAddr string, regNode **core.Node, stream *smux.Stream) {
	if cmd.NodeID == "" {
		resp := ControlResponse{Cmd: "err", Msg: "node_id is required"}
		data, _ := json.Marshal(resp)
		stream.Write(append(data, '\n'))
		return
	}

	now := time.Now()
	node := &core.Node{
		ID:            cmd.NodeID,
		Name:          cmd.Name,
		Token:         cmd.Token,
		Status:        core.NodeStatusOnline,
		Tunnels:       cmd.Tunnels,
		RemoteAddr:    remoteAddr,
		ConnectedAt:   &now,
		LastHeartbeat: &now,
		YamuxSession:  session,
	}

	if err := cs.nodeMgr.Add(ctx, node); err != nil {
		resp := ControlResponse{Cmd: "err", Msg: err.Error()}
		data, _ := json.Marshal(resp)
		stream.Write(append(data, '\n'))
		return
	}

	*regNode = node
	slog.Info("Node registered",
		"nodeId", cmd.NodeID,
		"name", cmd.Name,
		"tunnels", len(cmd.Tunnels),
		"remote", remoteAddr,
	)

	resp := ControlResponse{Cmd: "ok", Msg: "registered"}
	data, _ := json.Marshal(resp)
	stream.Write(append(data, '\n'))
}

