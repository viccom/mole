package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/xtaci/smux"
)

// 连接和协议常量
const (
	DefaultConnectTimeout = 10 * time.Second
	DefaultAuthTimeout    = 10 * time.Second
	DefaultSmuxVersion    = 2

	SmuxKeepAliveInterval = 30 * time.Second
	SmuxKeepAliveTimeout  = 90 * time.Second
	SmuxMaxFrameSize      = 32768
	SmuxMaxReceiveBuffer  = 4194304
	SmuxMaxStreamBuffer   = 256 * 1024
)

// TLSConfig TLS 配置
type TLSConfig struct {
	Enabled            bool
	InsecureSkipVerify bool
}

// DialFunc 连接函数类型（支持测试 mock）
type DialFunc func(ctx context.Context, addr string) (net.Conn, error)

// DefaultDialer 返回默认连接器
func DefaultDialer(tlsCfg *TLSConfig) DialFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: DefaultConnectTimeout}
		if tlsCfg != nil && tlsCfg.Enabled {
			return tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
				InsecureSkipVerify: tlsCfg.InsecureSkipVerify,
			})
		}
		return dialer.DialContext(ctx, "tcp", addr)
	}
}

// SessionManager 管理 smux 会话生命周期
type SessionManager struct {
	dial    DialFunc
	session *smux.Session
	conn    net.Conn
}

// NewSessionManager 创建会话管理器
func NewSessionManager(dial DialFunc) *SessionManager {
	return &SessionManager{dial: dial}
}

// Connect 连接服务器、执行认证、建立 smux 会话
func (sm *SessionManager) Connect(ctx context.Context, addr, token string) error {
	conn, err := sm.dial(ctx, addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	// Challenge-Response 认证
	if err := authenticate(conn, token); err != nil {
		conn.Close()
		return fmt.Errorf("auth: %w", err)
	}

	// 建立 smux 会话
	session, err := smux.Client(conn, &smux.Config{
		Version:           DefaultSmuxVersion,
		KeepAliveDisabled: false,
		KeepAliveInterval: SmuxKeepAliveInterval,
		KeepAliveTimeout:  SmuxKeepAliveTimeout,
		MaxFrameSize:      SmuxMaxFrameSize,
		MaxReceiveBuffer:  SmuxMaxReceiveBuffer,
		MaxStreamBuffer:   SmuxMaxStreamBuffer,
	})
	if err != nil {
		conn.Close()
		return fmt.Errorf("smux client: %w", err)
	}

	sm.conn = conn
	sm.session = session
	return nil
}

// Session 返回当前 smux 会话
func (sm *SessionManager) Session() *smux.Session {
	return sm.session
}

// IsConnected 返回会话是否存活
func (sm *SessionManager) IsConnected() bool {
	return sm.session != nil && !sm.session.IsClosed()
}

// Close 关闭会话和底层连接
func (sm *SessionManager) Close() {
	if sm.session != nil {
		sm.session.Close()
		sm.session = nil
	}
	if sm.conn != nil {
		sm.conn.Close()
		sm.conn = nil
	}
}

// authenticate 执行 Challenge-Response 认证
func authenticate(conn net.Conn, token string) error {
	// 读取 32 字节 challenge
	conn.SetReadDeadline(time.Now().Add(DefaultAuthTimeout))
	challenge := make([]byte, 32)
	if _, err := io.ReadFull(conn, challenge); err != nil {
		return fmt.Errorf("read challenge: %w", err)
	}

	// 发送认证消息
	authMsg, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return fmt.Errorf("marshal auth: %w", err)
	}
	if _, err := conn.Write(append(authMsg, '\n')); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}

	// 读取认证响应
	reader := bufio.NewReader(conn)
	authResp, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}
	conn.SetReadDeadline(time.Time{})

	var result struct {
		Cmd string `json:"cmd"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(authResp)), &result); err != nil {
		return fmt.Errorf("parse auth response: %w", err)
	}
	if result.Cmd != "ok" {
		return fmt.Errorf("auth failed: %s", result.Msg)
	}

	return nil
}
