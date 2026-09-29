package tunnel

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
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
	"mole/shared/proto"
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

// ControlProtocol 命令类型——协议结构体单源至 mole/shared/proto（双端 json tag
// 逐字一致），此处以类型别名接入保持包内既有名字。ControlCmd 以 core.Tunnel
// 实例化 Tunnels 字段（双端 Tunnel 的 Go 类型各自独立，见 proto 包注释）；
// p2pSignalTokenResp 同为 shared 导出类型的包内别名（lowercase 名不变）。
type (
	ControlCmd         = proto.ControlCmd[core.Tunnel]
	ControlResponse    = proto.ControlResponse
	TunnelStatus       = proto.TunnelStatus
	SysInfo            = proto.SysInfo
	p2pSignalTokenResp = proto.P2PSignalTokenResp
)

// sysInfoToCore 转换为 core 运行态模型。原为 (*SysInfo).toCore 方法——SysInfo
// 已是 shared 别名（非本地类型不能再挂方法），改为包内函数，逻辑不变
func sysInfoToCore(s *SysInfo) *core.SysInfo {
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

// tunnelStatusToCore 转换为 core 运行态模型（原 (*TunnelStatus).toCore 方法
// 改函数，原因同 sysInfoToCore，逻辑不变）
func tunnelStatusToCore(s TunnelStatus) core.ClientTunnelStatus {
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
	authLimiter      *connAuthLimiter     // 认证失败 per-IP 限速（SEC-13，NewControlServer 默认启用）
	// registerOwnerCheck register 时校验 node_id 归属（SEC-02，默认 true，kill-switch）
	registerOwnerCheck bool
	// legacyFormatEnabled 是否接受旧版明文 token 认证格式（SEC-01 兼容期，默认 true）
	legacyFormatEnabled bool
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

// NewControlServer 创建控制端口服务。
// 两个安全开关默认开启（与 config.NodeAuthConfig 默认一致），
// 经 SetNodeAuthOptions 由配置注入覆盖
func NewControlServer(addr string, transport Transport, nodeMgr *node.ShardedNodeManager, nodeToken string, nodeRepo core.NodeRepo) *ControlServer {
	return &ControlServer{
		addr:                addr,
		transport:           transport,
		nodeMgr:             nodeMgr,
		nodeToken:           nodeToken,
		nodeRepo:            nodeRepo,
		registerOwnerCheck:  true,
		legacyFormatEnabled: true,
		authLimiter:         newConnAuthLimiter(),
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

// SetNodeAuthOptions 设置节点认证行为开关（来自 node_auth 配置节）
func (cs *ControlServer) SetNodeAuthOptions(registerOwnerCheck, legacyFormatEnabled bool) {
	cs.registerOwnerCheck = registerOwnerCheck
	cs.legacyFormatEnabled = legacyFormatEnabled
}

// Start 启动控制端口监听（主传输层 + 额外传输层）
func (cs *ControlServer) Start(ctx context.Context) error {
	// acceptLoop 生命周期 ctx（审查①）：shutdown 必须能独立于外部 ctx 让
	// acceptLoop 退出——extra listener Listen 失败路径下外部 ctx 未 Done，
	// acceptLoop 只认 ctx 会永久自旋，Start 卡死在 acceptors.Wait()
	sctx, scancel := context.WithCancel(ctx)
	// cancel 幂等；成功路径 Start 阻塞至 ctx.Done 后返回，泄漏窗口与
	// Start 调用同生命周期（每 Start 调用一次派生，无累积）
	defer scancel()

	// Worker pool（所有 listener 共用）
	connChan := make(chan connEntry, 1000)
	workerCount := runtime.NumCPU() * 2
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			cs.connectionWorker(ctx, connChan)
		}()
	}

	// acceptLoop 追踪：关闭序列必须等所有 acceptLoop 退出后再 close(connChan)，
	// 否则残留的发送分支会向已关闭 channel 写入而 panic（QUA-11a）
	var acceptors sync.WaitGroup

	// shutdown 统一关闭序列（QUA-11a）：先 cancel acceptLoop 的 sctx → 关
	// listener → 等 acceptLoop 全部退出 → close(connChan)（connectionWorker
	// 的 range 自然退出）→ 等 worker 排空。close 仅存在于这一条路径
	// （每次 Start 持有独立 connChan，无 double-close）
	shutdown := func() {
		scancel()
		for _, ln := range cs.listeners {
			ln.Close()
		}
		acceptors.Wait()
		close(connChan)
		workers.Wait()
	}

	// 启动主 listener
	primaryLn, err := cs.transport.Listen(cs.addr)
	if err != nil {
		shutdown()
		return fmt.Errorf("control listen on %s (%s): %w", cs.addr, cs.transport.Name(), err)
	}
	cs.listener = primaryLn
	cs.listeners = append(cs.listeners, primaryLn)
	slog.Info("Control server listening", "addr", cs.addr, "transport", cs.transport.Name())
	acceptors.Add(1)
	go func() {
		defer acceptors.Done()
		cs.acceptLoop(sctx, primaryLn, connChan, cs.transport.Name())
	}()

	// 启动额外 listener（WS、KCP 等）
	for _, lt := range cs.extraTargets {
		ln, err := lt.transport.Listen(lt.addr)
		if err != nil {
			// 清理已启动的 listener 与 worker 池
			shutdown()
			return fmt.Errorf("control listen on %s (%s): %w", lt.addr, lt.transport.Name(), err)
		}
		cs.listeners = append(cs.listeners, ln)
		slog.Info("Control server listening", "addr", lt.addr, "transport", lt.transport.Name())
		acceptors.Add(1)
		go func(ln net.Listener, name string) {
			defer acceptors.Done()
			cs.acceptLoop(sctx, ln, connChan, name)
		}(ln, lt.transport.Name())
	}

	// 等待关闭
	<-ctx.Done()
	shutdown()
	slog.Info("Control server stopped")
	return nil
}

func (cs *ControlServer) acceptLoop(ctx context.Context, ln net.Listener, connChan chan<- connEntry, transportName string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			// listener 已关闭：立即退出（双保险——即便 ctx 分支失效也不得自旋）
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
				slog.Error("Accept connection failed", "transport", transportName, "error", err)
				// EMFILE 等瞬时/持续性错误立即重试会紧转烧 CPU，退避一下
				time.Sleep(100 * time.Millisecond)
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

	// SEC-13：认证失败 per-IP 限速——锁内 IP 在进入认证流程前直接断开
	//（不发 challenge、不读 auth 行）。authLimiter 为 nil 仅出现在测试
	// 直构 ControlServer 的场景，生产路径经 NewControlServer 默认启用
	ip := connRemoteIP(remoteAddr)
	if cs.authLimiter != nil && !cs.authLimiter.Allowed(ip) {
		slog.Warn("Control connection dropped: auth failure lock active", "remote", remoteAddr, "transport", transportName)
		conn.Close()
		return
	}

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

	// 预认证读取必须有长度上限：ReadString 遇超长行会无限扩容（OOM）。
	// 注意不能用 io.LimitedReader 包装连接——其 N 计数覆盖连接全生命周期，
	// 认证完成后节点→服务端的所有流量继续扣减，累计 64KB 后全部读取返回
	// EOF，整条节点连接死亡（生产事故根因）。用 readBoundedLine 只限制本行。
	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	authLine, err := readBoundedLine(reader, maxAuthLineBytes)
	if err != nil {
		if errors.Is(err, errAuthLineTooLarge) {
			slog.Warn("Node auth line too large", "remote", remoteAddr, "transport", transportName, "limit", maxAuthLineBytes)
		} else {
			slog.Warn("Node auth read failed", "remote", remoteAddr, "transport", transportName, "error", err)
		}
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})

	var authMsg struct {
		Token string `json:"token"`
		Proof string `json:"proof"`
	}
	if err := json.Unmarshal([]byte(authLine), &authMsg); err != nil {
		writeControlResp(conn, proto.RespErr, "invalid auth format")
		slog.Warn("Node auth format invalid", "remote", remoteAddr, "transport", transportName)
		conn.Close()
		return
	}

	// 统一走 authenticator 认证（内部实现：用户级 token 优先 → legacy 兜底）。
	// 双格式（SEC-01）：新客户端发 {"proof"}（challenge-response HMAC，proof 绑定
	// 本次连接的一次性 challenge，明文 token 不上线，客户端不发任何标识符）；
	// 旧客户端发 {"token"}（明文格式，legacy_format_enabled 关闭后拒绝，
	// 全部客户端升级到 proof 格式后由 R2 配置收口）
	if cs.authenticator != nil {
		var grant *core.NodeAccessGrant
		switch {
		case authMsg.Proof != "":
			var authErr error
			grant, authErr = cs.authenticator.AuthenticateNodeProof(ctx, authMsg.Proof, challenge)
			if authErr != nil {
				// 计数必须先于应答：客户端读到响应即可发起下一次连接，
				// 若后计数，第 N+1 次连接可能赶在失败入账前通过锁检查
				cs.authFail(ip)
				writeControlResp(conn, proto.RespErr, "invalid token")
				slog.Warn("Node auth failed", "remote", remoteAddr, "transport", transportName)
				conn.Close()
				return
			}
		case authMsg.Token != "":
			if !cs.legacyFormatEnabled {
				cs.authFail(ip)
				writeControlResp(conn, proto.RespErr, "legacy auth disabled")
				slog.Warn("Legacy node auth rejected", "remote", remoteAddr, "transport", transportName)
				conn.Close()
				return
			}
			var authErr error
			grant, authErr = cs.authenticator.AuthenticateNodeToken(ctx, authMsg.Token)
			if authErr != nil {
				cs.authFail(ip)
				writeControlResp(conn, proto.RespErr, "invalid token")
				slog.Warn("Node auth failed", "remote", remoteAddr, "transport", transportName)
				conn.Close()
				return
			}
		default:
			writeControlResp(conn, proto.RespErr, "invalid auth format")
			slog.Warn("Node auth format invalid", "remote", remoteAddr, "transport", transportName)
			conn.Close()
			return
		}
		cs.authOK(ip) // 同理：清零先于应答，防「成功后紧接的失败连接」误锁
		writeControlResp(conn, proto.RespOK, "authenticated")
		slog.Info("Node authenticated", "remote", remoteAddr, "transport", transportName, "userId", grant.UserID, "legacy", grant.LegacyGlobal)

		// 建立 smux 会话并使用 grant（conn 生命周期转移给 goroutine）
		cs.setupSmuxAndAccept(ctx, &bufferedConn{Conn: conn, reader: reader}, remoteAddr, grant, transportName)
		return
	}

	// 无 authenticator 时回退到旧全局 token 直接比对（兼容未注入场景）
	if subtle.ConstantTimeCompare([]byte(authMsg.Token), []byte(cs.nodeToken)) == 1 {
		cs.authOK(ip)
		writeControlResp(conn, proto.RespOK, "authenticated")
		slog.Info("Node authenticated (legacy fallback)", "remote", remoteAddr, "transport", transportName)

		cs.setupSmuxAndAccept(ctx, &bufferedConn{Conn: conn, reader: reader}, remoteAddr, &core.NodeAccessGrant{
			UserID:       "system",
			LegacyGlobal: true,
		}, transportName)
		return
	}

	cs.authFail(ip)
	writeControlResp(conn, proto.RespErr, "invalid token")
	slog.Warn("Node auth failed", "remote", remoteAddr, "transport", transportName, "reason", "invalid token")
	conn.Close()
}

// authFail/authOK 认证结果计入 per-IP 限速器（SEC-13）：
// 仅「提交了凭据但被拒/通过」计入——载荷无法解析（invalid format）与
// 读取失败不构成认证尝试，对齐 auth.LoginLimiter 只计 401 的语义；
// limiter 未注入（测试直构）时为空操作
func (cs *ControlServer) authFail(ip string) {
	if cs.authLimiter != nil {
		cs.authLimiter.RecordFailure(ip)
	}
}

func (cs *ControlServer) authOK(ip string) {
	if cs.authLimiter != nil {
		cs.authLimiter.RecordSuccess(ip)
	}
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
				// 判定必须是「当前会话仍是我」才清理，其余一律让位：
				//  - 节点已不在管理器：置换流程已 Remove，重复清理会误停新节点监听器
				//  - GetSession 失败（Add→AddSession 窗口）：新连接正在注册途中
				//  - 会话指针不同：新连接已接管
				// 旧实现把「无会话」当未置换，TOCTOU 下会整体删除刚注册的新节点
				displaced := false
				if _, ok := cs.nodeMgr.Get(ctx, node.ID); !ok {
					displaced = true
				} else if currentSess, err := cs.nodeMgr.GetSession(ctx, node.ID); err != nil || currentSess != session {
					displaced = true
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
// （值单源至 mole/shared/proto）
const maxControlMsgSize = proto.MaxControlMsgSize // 1MB

// maxAuthLineBytes 预认证 auth 行的长度上限（token JSON 远小于此值）
// （值单源至 mole/shared/proto）
const maxAuthLineBytes = proto.MaxAuthLineBytes

// errAuthLineTooLarge 认证行超过长度上限
var errAuthLineTooLarge = errors.New("auth line too large")

// readBoundedLine 读取以 \n 定界的一行，累计长度超限即报错。
// 修复 OOM 必须只限制"这一行"的长度：绝不能用 io.LimitedReader 包装连接——
// 其 N 计数覆盖连接全生命周期，认证完成后节点→服务端的所有流量继续扣减，
// 累计 64KB 后全部读取返回 EOF，整条节点连接死亡（生产事故根因）。
// 跨 bufio 缓冲（4KB）的行由多个 ReadSlice 分片组成：必须 append 累计全部
// 分片（与客户端 readBoundedCmdLine 同手法）——只返回末片会把 4KB-64KB
// 间的合法行静默截断成残缺 JSON
func readBoundedLine(r *bufio.Reader, limit int) (string, error) {
	var total int
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		total += len(frag)
		if total > limit {
			return "", errAuthLineTooLarge
		}
		line = append(line, frag...)
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				continue // 单个 bufio 缓冲无 \n，继续累计
			}
			return "", err
		}
		return string(line), nil
	}
}

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
		writeControlResp(stream, proto.RespErr, "invalid json")
		return
	}

	switch cmd.Cmd {
	case proto.CmdRegister:
		cs.handleRegister(ctx, cmd, state, stream)
	case proto.CmdPing:
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
		writeControlRespTs(stream, proto.RespPong, "", cmd.Ts)
	case proto.CmdTunnelUpdate:
		cs.handleTunnelUpdate(ctx, cmd, state, stream)
	case proto.CmdSysInfo:
		node := state.get()
		if node != nil && cmd.SysInfo != nil {
			si := sysInfoToCore(cmd.SysInfo)
			if err := cs.nodeMgr.Update(ctx, node.ID, func(n *core.Node) {
				n.SysInfo = si
			}); err != nil {
				slog.Debug("sysinfo update failed", "node", node.ID, "error", err)
			}
		}
		writeControlResp(stream, proto.RespOK, "sysinfo received")
	case proto.CmdTunnelStatus:
		node := state.get()
		if node != nil && len(cmd.Statuses) > 0 {
			statuses := make([]core.ClientTunnelStatus, len(cmd.Statuses))
			for i, s := range cmd.Statuses {
				statuses[i] = tunnelStatusToCore(s)
			}
			if err := cs.nodeMgr.Update(ctx, node.ID, func(n *core.Node) {
				n.ClientStatuses = statuses
			}); err != nil {
				slog.Debug("tunnel_status update failed", "node", node.ID, "error", err)
			}
		}
		writeControlResp(stream, proto.RespOK, "status received")
	case proto.CmdP2PSignalToken:
		cs.handleP2PSignalToken(ctx, cmd, state, stream)
	default:
		writeControlResp(stream, proto.RespErr, "unknown command")
	}
}

// p2pSignalTokenResp 类型已单源至 mole/shared/proto（P2PSignalTokenResp，
// 双端 json tag 逐字一致），包内别名见文件头部类型组。

// handleP2PSignalToken 处理 C→S p2p_signal_token：校验连接已认证 + name 归属该校验连接
// 的节点且类型为 p2p（纵深防御，token 本身只授 nat-exchange/*），签发并平铺 JSON 返回。
// 不写 Para（凭据不能进配置，admin/持久化面不接触明文 secret）。
func (cs *ControlServer) handleP2PSignalToken(ctx context.Context, cmd ControlCmd, state *connState, stream *smux.Stream) {
	fail := func(msg string) {
		_ = writeJSONLine(stream, p2pSignalTokenResp{Cmd: proto.CmdP2PSignalToken, Error: msg})
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
	// 原子快路径：隧道存在性校验与签发同一临界区（pairingMu），
	// 消除「检查在锁外、签发在锁内」与 RemoveTunnel 删除-吊销交错的竞态
	// （R5 残余窗口：可为已删除隧道产出永不吊销的孤儿凭据）
	if cs.nodeRepo != nil && cs.tunnelSvc != nil {
		if issuer, ok := cs.tunnelSvc.(interface {
			IssueP2PTokenForTunnel(nodeID, tunnelName string) (string, string, int64, error)
		}); ok {
			username, password, expiresAt, err := issuer.IssueP2PTokenForTunnel(node.ID, cmd.Name)
			if err != nil {
				if errors.Is(err, core.ErrTunnelNotFound) {
					fail("p2p tunnel not found on node")
				} else {
					slog.Error("issue p2p signal token failed", "node", node.ID, "tunnel", cmd.Name, "error", err)
					fail("temporary storage error, retry later")
				}
				return
			}
			if err := writeJSONLine(stream, p2pSignalTokenResp{
				Cmd: proto.CmdP2PSignalToken, OK: true,
				Username: username, Password: password, ExpiresAt: expiresAt,
			}); err != nil {
				slog.Debug("write p2p_signal_token response failed", "error", err)
			}
			return
		}
	}

	// 发现检查以持久层真相源为准：nodeMgr 内存可能包含注册竞态产物
	// （handleRegister 先写内存、SyncFromClient 配对拒绝后仅记警告不回滚，
	// 审查 #1），凭据签发不能认它。nodeRepo 为 nil 时回退内存（测试场景）。
	var tunnels []core.Tunnel
	if cs.nodeRepo != nil {
		persisted, err := cs.nodeRepo.GetByID(node.ID)
		switch {
		case err == nil && persisted != nil:
			tunnels = persisted.Tunnels
		case errors.Is(err, core.ErrNodeNotFound):
			// 节点无持久记录：维持拒绝（审查 #1）。register 落库在应答前完成，
			// 客户端每次连接尝试重拉凭据，罕见竞态下一轮自愈
		default:
			// 瞬时存储错误与「不存在」必须区分：误判缺失会让客户端拿不到凭据（复审 R3）
			slog.Warn("p2p signal token: read persisted node failed", "node", node.ID, "error", err)
			fail("temporary storage error, retry later")
			return
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
		Cmd: proto.CmdP2PSignalToken, OK: true,
		Username: username, Password: password, ExpiresAt: expiresAt,
	}); err != nil {
		slog.Debug("write p2p_signal_token response failed", "error", err)
	}
}

func (cs *ControlServer) handleRegister(ctx context.Context, cmd ControlCmd, state *connState, stream *smux.Stream) {
	if cmd.NodeID == "" {
		writeControlResp(stream, proto.RespErr, "node_id is required")
		return
	}
	if !isValidNodeID(cmd.NodeID) {
		writeControlResp(stream, proto.RespErr, "node_id must be exactly 8 alphanumeric characters starting with a letter")
		return
	}

	// REL-02：register 携带的隧道列表必须先过服务端校验（与 REST 落库入口
	// 同一规则，含 listen_port 范围/保留端口/唯一性）+ 条数上限，任一失败
	// 即拒绝注册——阻止非法配置进入内存运行态（注册竞态产物的源头）
	if len(cmd.Tunnels) > core.MaxRegisterTunnels {
		slog.Warn("Register rejected: tunnel list exceeds limit",
			"nodeId", cmd.NodeID, "tunnels", len(cmd.Tunnels), "limit", core.MaxRegisterTunnels)
		writeControlResp(stream, proto.RespErr, fmt.Sprintf("tunnel list exceeds limit of %d", core.MaxRegisterTunnels))
		return
	}
	if len(cmd.Tunnels) > 0 {
		if err := core.ValidateTunnels(cmd.Tunnels); err != nil {
			slog.Warn("Register rejected: invalid tunnel list",
				"nodeId", cmd.NodeID, "tunnels", len(cmd.Tunnels), "error", err)
			writeControlResp(stream, proto.RespErr, err.Error())
			return
		}
	}

	// SEC-02：注册归属校验——认证身份与 node_id 的持久化归属者不同时拒绝注册，
	// 防止用户 B 的 token 抢注用户 A 的 node_id（覆盖隧道配置、劫持流量）。
	// 放行：无持久记录（新节点）/ 同主 / 持久归属为 system（legacy 时代记录，
	// 允许任意用户接管完成迁移）/ grant 为 legacy 全局 token（UserID=system）/
	// 开关关闭（kill-switch）
	if cs.registerOwnerCheck && cs.nodeRepo != nil {
		if grant := state.grant; grant != nil {
			if persisted, err := cs.nodeRepo.GetByID(cmd.NodeID); err == nil && persisted != nil &&
				persisted.OwnerUserID != "" && persisted.OwnerUserID != "system" &&
				persisted.OwnerUserID != grant.UserID && grant.UserID != "system" {
				slog.Warn("Node register rejected: registered by another user",
					"nodeId", cmd.NodeID, "grantUser", grant.UserID, "persistedOwner", persisted.OwnerUserID)
				writeControlResp(stream, proto.RespErr, "node registered by another user")
				return
			}
		}
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
		SysInfo:       sysInfoToCore(cmd.SysInfo),
	}

	// 从认证结果中读取归属信息
	if grant := state.grant; grant != nil {
		node.OwnerUserID = grant.UserID
		node.AccessTokenID = grant.AccessTokenID
	}

	// 恢复节点级限速配置：register 命令不携带该字段，内存态留 nil 的话，
	// 之后任何整节点落库（ApplyTunnel/SyncFromClient 经 persistUpdatedNode）
	// 都会把持久层已存的节点级 rate_limit 抹掉，重启后 LoadPersisted 也无从恢复
	if cs.nodeRepo != nil {
		if persisted, err := cs.nodeRepo.GetByID(cmd.NodeID); err == nil && persisted != nil && persisted.RateLimit != nil {
			node.RateLimit = persisted.RateLimit
		}
	}

	if err := cs.nodeMgr.Add(ctx, node); err != nil {
		if err == core.ErrNodeExists {
			// 同一节点重连：先探测旧 session 是否真活
			oldNode, oldOk := cs.nodeMgr.Get(ctx, cmd.NodeID)
			if oldOk && cs.probeOldSession(ctx, cmd.NodeID, oldNode.Tunnels) {
				slog.Info("Node already online, old session alive", "nodeId", cmd.NodeID)
				writeControlResp(stream, proto.RespErr, "node already online")
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
				// QUA-02：Add 失败属内部错误，对端回 generic，详情进日志
				slog.Error("Register re-add after displacement failed", "nodeId", cmd.NodeID, "error", err2)
				writeControlResp(stream, proto.RespErr, "internal server error")
				return
			}
		} else {
			slog.Error("Register node add failed", "nodeId", cmd.NodeID, "error", err)
			writeControlResp(stream, proto.RespErr, "internal server error")
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

	writeControlResp(stream, proto.RespOK, "registered")

	// 注册成功后，优先由统一服务处理持久化配置加载；未注入时走兼容路径。
	if cs.tunnelSvc != nil {
		loaded, err := cs.tunnelSvc.LoadPersisted(ctx, cmd.NodeID)
		if err != nil {
			// 持久化加载失败不能只 Warn 后放弃：applyRuntimeTunnels 不会执行，
			// TCP/UDP 监听器缺失直到下次自愈（表现为网关端口 connection refused）。
			// 回退为「仅运行态激活客户端配置」：监听器立即可用，持久层保持不动，
			// 下次重连重试持久化加载（不落库，避免覆盖管理端配置）。
			slog.Warn("Failed to load persisted tunnels; activating client config for runtime only",
				"nodeId", cmd.NodeID, "error", err)
			if actErr := cs.tunnelSvc.ActivateClientTunnels(ctx, cmd.NodeID, cmd.Tunnels); actErr != nil {
				slog.Warn("Failed to activate client tunnels as fallback", "nodeId", cmd.NodeID, "error", actErr)
			}
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

// probeOldSessionTimeout 旧会话判死阈值（REL-06）。
// 原值 3s 在慢链路（高 RTT、瞬时拥塞、弱网移动端）下，探针响应稍慢即把
// 真活的旧客户端误判为假死而强制置换——表现为在线节点被无谓断线重建。
// 探针内容是幂等的 tunnel_push（推送的就是该节点现存隧道，重复应用无害），
// 放宽到 8s 只增加罕见假死场景的重连等待，不引入正确性风险。
const probeOldSessionTimeout = 8 * time.Second

// probeOldSession 通过旧 smux session 向旧客户端发送 tunnel_push 并等待响应。
// probeOldSessionTimeout 内有响应说明旧客户端真活；否则判定为假死。
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
	stream.SetWriteDeadline(time.Now().Add(probeOldSessionTimeout))
	cmd := ControlCmd{Cmd: proto.CmdTunnelPush, Tunnels: tunnels}
	data, _ := json.Marshal(cmd)
	if _, err := stream.Write(append(data, '\n')); err != nil {
		return false
	}

	// 等待客户端响应
	stream.SetReadDeadline(time.Now().Add(probeOldSessionTimeout))
	raw, err := readControlMsg(stream, maxControlMsgSize)
	if err != nil {
		return false
	}

	var resp ControlResponse
	return json.Unmarshal(raw, &resp) == nil && resp.Cmd == proto.RespOK
}

// writeControlResp 向控制流写入 JSON 响应行
func writeControlResp(w interface{ Write([]byte) (int, error) }, cmd, msg string) {
	resp := ControlResponse{Cmd: cmd, Msg: msg}
	if err := writeJSONLine(w, resp); err != nil {
		slog.Debug("Failed to write control response", "cmd", cmd, "error", err)
	}
}

// sanitizeControlErr 控制面错误脱敏（QUA-02）：协议/业务校验错误（对端可
// 理解并修正，如 ErrTunnelInvalid/ErrPortInUse）保留原文；内部错误（存储、
// 内存态等）只回 generic 消息，真实详情由调用方记日志
func sanitizeControlErr(err error) (msg string, internal bool) {
	if errors.Is(err, core.ErrTunnelInvalid) || errors.Is(err, core.ErrPortInUse) {
		return err.Error(), false
	}
	return "internal server error", true
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
	// 写侧同样需要 deadline：对端冻结（SIGSTOP/零窗口）时无界写会挂住
	// 调用方（REST 请求）直到 smux keepalive 判死（TCP 最长约 90s）
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := stream.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}
	stream.SetWriteDeadline(time.Time{})

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
		Cmd:    proto.CmdTunnelAction,
		Name:   name,
		Action: action,
	}
	resp, err := cs.sendToNode(ctx, nodeID, cmd, 10*time.Second)
	if err != nil {
		return err
	}
	if resp.Cmd != proto.RespOK {
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
		Cmd:    proto.CmdRestart,
		Delay:  delay,
		Reason: reason,
	}
	resp, err := cs.sendToNode(ctx, nodeID, cmd, 10*time.Second)
	if err != nil {
		return err
	}
	if resp.Cmd != proto.RespOK {
		return fmt.Errorf("restart rejected: %s", resp.Msg)
	}
	return nil
}

func (cs *ControlServer) handleTunnelUpdate(ctx context.Context, cmd ControlCmd, state *connState, stream *smux.Stream) {
	node := state.get()
	if node == nil {
		writeControlResp(stream, proto.RespErr, "node not registered")
		return
	}

	nodeID := node.ID
	if err := cs.nodeMgr.Update(ctx, nodeID, func(n *core.Node) {
		now := time.Now()
		n.LastHeartbeat = &now
	}); err != nil {
		// QUA-02：内部错误脱敏，详情进日志
		slog.Error("tunnel_update heartbeat failed", "nodeId", nodeID, "error", err)
		writeControlResp(stream, proto.RespErr, "internal server error")
		return
	}

	if cs.tunnelSvc != nil {
		if err := cs.tunnelSvc.SyncFromClient(ctx, nodeID, cmd.Tunnels); err != nil {
			// QUA-02：业务校验错误（ErrTunnelInvalid/ErrPortInUse 等包装链）
			// 保留原文供客户端修正；内部错误只回 generic、详情进日志
			msg, internal := sanitizeControlErr(err)
			if internal {
				slog.Error("tunnel_update sync failed", "nodeId", nodeID, "error", err)
			}
			writeControlResp(stream, proto.RespErr, msg)
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
		slog.Warn("tunnel_update received but TunnelConfigManager not configured; change only persisted from registration snapshot", "nodeId", nodeID)
	}

	writeControlResp(stream, proto.RespOK, "tunnels updated")
}

// persistNode 持久化节点信息（主要是隧道配置）
func (cs *ControlServer) persistNode(n *core.Node) {
	if cs.nodeRepo == nil || n == nil {
		return
	}
	// 运行态字段不属于持久化契约：清除后再落库，避免 sysinfo/状态列表随每次
	// 持久化写入 blob 持续膨胀、重启后离线节点带陈旧运行态"复活"；
	// 接入 token 同样剥离（SEC-03）——明文节点凭据不得进持久层
	cp := *n
	cp.SysInfo = nil
	cp.ClientStatuses = nil
	cp.RTT = 0
	cp.Token = ""
	// Create or Update：先尝试 GetByID 判断是否已存在
	if existing, err := cs.nodeRepo.GetByID(n.ID); err != nil || existing == nil {
		if err := cs.nodeRepo.Create(&cp); err != nil {
			slog.Debug("Failed to persist node (create)", "nodeId", n.ID, "error", err)
		}
	} else {
		if err := cs.nodeRepo.Update(&cp); err != nil {
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
		Cmd:     proto.CmdTunnelPush,
		Tunnels: tunnels,
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal tunnel_push: %w", err)
	}
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := stream.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("send tunnel_push: %w", err)
	}
	stream.SetWriteDeadline(time.Time{})

	stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	raw, err := readControlMsg(stream, maxControlMsgSize)
	if err != nil {
		return fmt.Errorf("read tunnel_push response: %w", err)
	}

	var resp ControlResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("parse tunnel_push response: %w", err)
	}
	if resp.Cmd != proto.RespOK {
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
