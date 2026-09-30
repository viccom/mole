package transport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/xtaci/smux"

	"mole/shared/noisechan"
	"mole/shared/proto"
)

// 连接和协议常量
const (
	DefaultConnectTimeout = 10 * time.Second
	DefaultAuthTimeout    = 10 * time.Second
	DefaultSmuxVersion    = 2

	SmuxKeepAliveInterval = 30 * time.Second
	SmuxKeepAliveTimeout  = 90 * time.Second
	SmuxMaxFrameSize      = 32768
	SmuxMaxReceiveBuffer  = 32 * 1024 * 1024 // 32MB total session buffer
	SmuxMaxStreamBuffer   = 4 * 1024 * 1024  // 4MB per-stream window for smooth RDP

	// maxAuthRespBytes 认证应答行的长度上限，与服务端 readBoundedLine 的
	// maxAuthLineBytes（64KB）对等（值单源至 mole/shared/proto）。
	// 合法应答 {"cmd":"ok","msg":"..."} 仅几十字节，64KB 余量充足零误伤
	maxAuthRespBytes = proto.MaxAuthLineBytes
)

// errAuthRespTooLarge 认证应答行超过长度上限（协议违规）
var errAuthRespTooLarge = errors.New("auth response too large")

// readBoundedAuthLine 读取以 '\n' 定界的认证应答行，累计长度超限即报
// errAuthRespTooLarge。不能 import 根包复用 readBoundedCmdLine（依赖倒挂：
// transport 是根包的下游），本包内实现。手法与服务端 readBoundedLine /
// 根包 readBoundedCmdLine 一致：ReadSlice 分片 append 累计、每轮检查累计
// 长度，只限制"这一行"——绝不能用 io.LimitedReader 包装连接（N 计数会
// 覆盖连接全生命周期，认证后的 smux 流量继续扣减导致连接死亡）。
func readBoundedAuthLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		line = append(line, frag...)
		if len(line) > limit {
			return nil, errAuthRespTooLarge
		}
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue // 单个 bufio 缓冲内无 '\n'，继续累计
		}
		return line, err
	}
}

// TLSConfig TLS 配置
type TLSConfig struct {
	Enabled            bool
	InsecureSkipVerify bool
}

// 通道加密协商模式（client Config.EncMode 的合法取值）。SessionManager 零值
// 视为 EncModeOn：未经装配处显式配置的直连构造默认尝试加密协商，旧服务端
// 自动回落明文（安全缺省）。
const (
	EncModeOn  = "on"
	EncModeOff = "off"
)

// DialFunc 连接函数类型（支持测试 mock）
type DialFunc func(ctx context.Context, addr string) (net.Conn, error)

// DefaultDialer 返回默认连接器
func DefaultDialer(tlsCfg *TLSConfig) DialFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: DefaultConnectTimeout}
		var conn net.Conn
		var err error
		if tlsCfg != nil && tlsCfg.Enabled {
			conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
				InsecureSkipVerify: tlsCfg.InsecureSkipVerify,
			})
		} else {
			conn, err = dialer.DialContext(ctx, "tcp", addr)
		}
		if err != nil {
			return nil, err
		}
		// Disable Nagle's algorithm for low-latency RDP and interactive traffic
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			tcpConn.SetNoDelay(true)
		}
		return conn, nil
	}
}

// SessionManager 管理 smux 会话生命周期
// session/conn 由 Connect（重连循环）、Close（远程重启/SIGINT）并发读写，
// 必须持 mu 访问；closed 防止 Close 之后在途的 Connect 发布"幽灵会话"
type SessionManager struct {
	mu           sync.Mutex
	dial         DialFunc
	session      *smux.Session
	conn         net.Conn
	closed       bool
	smuxOverride *smux.Config
	encMode      string // 通道加密协商模式（零值视为 on）；SetChannelEncryption 写入
	useTLS       bool   // 传输层是否已走 TLS（TLS 连接不发 enc 位，见 Connect 内安全边界注释）
	// warnedPlaintext 按服务端地址记录明文回落 WARN 是否已发（每地址每进程
	// 一次，防旧服务端场景下 5s 重连循环刷屏）；mu 保护
	warnedPlaintext map[string]bool
}

// NewSessionManager 创建会话管理器
func NewSessionManager(dial DialFunc) *SessionManager {
	return &SessionManager{dial: dial}
}

func (sm *SessionManager) SetSmuxOverride(cfg *smux.Config) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.smuxOverride = cfg
}

// SetChannelEncryption 配置通道加密协商输入：mode 取 EncModeOn/EncModeOff
// （零值视为 on）；useTLS 记录本连接传输层是否已加密。装配处（client.New
// 构造 dial 后）调用一次。
func (sm *SessionManager) SetChannelEncryption(mode string, useTLS bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.encMode = mode
	sm.useTLS = useTLS
}

// warnPlaintextFallbackOnce 明文回落告警：每服务端地址每进程一次（方案 B
// 异常矩阵 #1，防旧服务端场景下 5s 重连循环刷屏），之后静默。文案是 E2E
// 断言的观测锚点，不可改动。
func (sm *SessionManager) warnPlaintextFallbackOnce(addr string) {
	sm.mu.Lock()
	if sm.warnedPlaintext[addr] {
		sm.mu.Unlock()
		return
	}
	if sm.warnedPlaintext == nil {
		sm.warnedPlaintext = make(map[string]bool)
	}
	sm.warnedPlaintext[addr] = true
	sm.mu.Unlock()
	log.Printf("WARNING: plaintext fallback: server %s did not offer channel encryption (connection continues in plaintext)", addr)
}

// Connect 连接服务器、执行认证、建立 smux 会话
func (sm *SessionManager) Connect(ctx context.Context, addr, token string) error {
	sm.mu.Lock()
	if sm.closed {
		sm.mu.Unlock()
		return fmt.Errorf("session manager closed")
	}
	smuxOverride := sm.smuxOverride
	// 是否在认证行携带通道加密请求位（enc:1）：encMode=on 且传输未走 TLS。
	// 安全边界：UseTLS 不发 enc = 单连接单加密（防 TCP→TLS→Noise→smux 双重
	// 加密、全部隧道载荷白付两份加解密开销；TLS 连接的机密性由 TLS 层承担，
	// 且此时 ok 应答按服务端组合规则也不会带 enc 字段）
	requestEnc := sm.encMode != EncModeOff && !sm.useTLS
	sm.mu.Unlock()

	conn, err := sm.dial(ctx, addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	// Challenge-Response 认证（requestEnc 时认证行携带 enc 请求位）
	br, encCap, err := authenticate(conn, token, requestEnc)
	if err != nil {
		conn.Close()
		return fmt.Errorf("auth: %w", err)
	}

	// 通道加密分派（方案 B 决策矩阵）：应答带 enc（V==EncProtocolV1）且本端
	// 请求了加密 → Noise 升级，sessionConn 换 noiseConn；其余路径明文。
	var sessionConn net.Conn
	if requestEnc && encCap != nil && encCap.V == proto.EncProtocolV1 {
		// 安全边界：握手失败绝不回落——服务端已宣告能力后失败即攻击/损坏，
		// 此处回落等于把明文交给破坏 msg2 的中间人。错误直接上抛走重连循环
		//（专属错误串由 noisechan.ErrHandshake 的文案前缀承担）。
		psk := sha256.Sum256([]byte(token))
		noiseConn, err := noisechan.UpgradeInitiator(conn, br, psk[:], DefaultAuthTimeout)
		if err != nil {
			conn.Close() // UpgradeInitiator 失败路径已关 conn；此处兜底幂等
			return fmt.Errorf("channel encryption upgrade: %w", err)
		}
		sessionConn = noiseConn
	} else {
		// ok 缺 enc 字段（或版本不识别）= 旧服务端 / 未启用：回落明文 +
		// WARN 每服务端地址每进程一次。requestEnc=false 时静默（enc=off 是
		// 显式调试选择、UseTLS 传输已加密，均不告警）
		if requestEnc {
			sm.warnPlaintextFallbackOnce(addr)
		}
		// Wrap conn so smux sees both bufio buffered data and raw conn reads
		sessionConn = &bufferedConn{Conn: conn, reader: br}
	}

	// 建立 smux 会话
	smuxCfg := &smux.Config{
		Version:           DefaultSmuxVersion,
		KeepAliveDisabled: false,
		KeepAliveInterval: SmuxKeepAliveInterval,
		KeepAliveTimeout:  SmuxKeepAliveTimeout,
		MaxFrameSize:      SmuxMaxFrameSize,
		MaxReceiveBuffer:  SmuxMaxReceiveBuffer,
		MaxStreamBuffer:   SmuxMaxStreamBuffer,
	}
	if smuxOverride != nil {
		smuxCfg = smuxOverride
	}
	session, err := smux.Client(sessionConn, smuxCfg)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smux client: %w", err)
	}

	// 发布前复查：Close 可能发生在 dial/auth 握手期间（如桌面端切换节点），
	// 此时必须丢弃新会话，否则旧客户端会以幽灵会话抢占节点注册
	sm.mu.Lock()
	if sm.closed {
		sm.mu.Unlock()
		session.Close()
		conn.Close()
		return fmt.Errorf("session manager closed during connect")
	}
	sm.conn = conn
	sm.session = session
	sm.mu.Unlock()
	return nil
}

// Session 返回当前 smux 会话
func (sm *SessionManager) Session() *smux.Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.session
}

// IsConnected 返回会话是否存活
func (sm *SessionManager) IsConnected() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.session != nil && !sm.session.IsClosed()
}

// Close 终态关闭：锁存 closed，此后 Connect 永久拒绝（防止在途 Connect
// 发布幽灵会话）。仅供 Client.Close()（进程级关停/节点切换）调用；
// 重连循环的每周期断开必须走 Disconnect，否则断一次线就永久离线
func (sm *SessionManager) Close() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.closed = true
	if sm.session != nil {
		sm.session.Close()
		sm.session = nil
	}
	if sm.conn != nil {
		sm.conn.Close()
		sm.conn = nil
	}
}

// Disconnect 关闭当前会话和底层连接，但不锁存关闭态：
// 重连循环每周期断开后需要能再次 Connect
func (sm *SessionManager) Disconnect() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.session != nil {
		sm.session.Close()
		sm.session = nil
	}
	if sm.conn != nil {
		sm.conn.Close()
		sm.conn = nil
	}
}

// authenticate 执行 Challenge-Response 认证，返回 bufio.Reader 保留缓冲数据
// 与 ok 应答携带的通道加密能力宣告（nil = 服务端未宣告）。requestEnc 为
// true 时认证行携带 enc 请求位（proto.EncProtocolV1）；返回的 reader 是本
// 函数创建、读认证应答所用的同一个 bufio.Reader——Noise 升级必须穿针复用
// 它（缓冲可能已吞下后续握手字节，绕开即帧流错位）。
func authenticate(conn net.Conn, token string, requestEnc bool) (*bufio.Reader, *proto.EncCapability, error) {
	log.Println("  auth: waiting for challenge...")
	// 读取 32 字节 challenge
	conn.SetReadDeadline(time.Now().Add(DefaultAuthTimeout))
	challenge := make([]byte, 32)
	if _, err := io.ReadFull(conn, challenge); err != nil {
		return nil, nil, fmt.Errorf("read challenge: %w", err)
	}
	log.Println("  auth: challenge received, sending credentials")

	// proof 认证（防嗅探/防重放）：token 先做 sha256 得 32 字节原始 digest 作为
	// HMAC key，对 challenge 计算 HMAC-SHA256 后 hex 编码发送——线上不出现明文
	// token；challenge 由服务端每次随机下发，同一 token 的 proof 不可重放
	h := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, h[:])
	mac.Write(challenge)
	proof := hex.EncodeToString(mac.Sum(nil))

	// 发送认证消息（wire 契约单源至 mole/shared/proto 的 NodeAuthLine；
	// requestEnc=false 时 Enc 为 0，omitempty 使输出与旧 proof-only 形态
	// 逐字节一致）
	encBit := 0
	if requestEnc {
		encBit = proto.EncProtocolV1
	}
	authMsg, err := json.Marshal(proto.NodeAuthLine{Proof: proof, Enc: encBit})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal auth: %w", err)
	}
	if _, err := conn.Write(append(authMsg, '\n')); err != nil {
		return nil, nil, fmt.Errorf("send auth: %w", err)
	}
	log.Println("  auth: credentials sent, waiting for response")

	// 读取认证响应（有界读：裸连接未过 smux，无界累计是内存放大点，
	// 恶意/故障服务端可用超长无换行数据撑爆缓冲——超限立即断开）
	reader := bufio.NewReader(conn)
	authResp, err := readBoundedAuthLine(reader, maxAuthRespBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("read auth response: %w", err)
	}
	conn.SetReadDeadline(time.Time{})
	log.Println("  auth: response received")

	var result struct {
		Cmd string               `json:"cmd"`
		Msg string               `json:"msg"`
		Enc *proto.EncCapability `json:"enc,omitempty"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(authResp), &result); err != nil {
		return nil, nil, fmt.Errorf("parse auth response: %w", err)
	}
	if result.Cmd != proto.RespOK {
		return nil, nil, fmt.Errorf("auth failed: %s", result.Msg)
	}

	return reader, result.Enc, nil
}

// bufferedConn wraps net.Conn to first drain bufio.Reader buffered data,
// preventing data loss between auth and smux handshake.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}
