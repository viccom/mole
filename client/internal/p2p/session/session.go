//go:build p2p

package session

import (
	"context"
	"errors"
	"sync"

	"moleAgent_client/internal/p2p/tunnel"
)

// Session is the unified application-layer API over an established P2P
// connection. 所有传输（udp-v4/udp-v6/tcp-v4/tcp-v6/lan）经 secure 协商 + yamux
// 多路复用后由 secureSession 统一实现，全部能力可用（文本/文件/测速/TCP+UDP 隧道）。
//
// Calling any method after Close returns ErrSessionClosed.
type Session interface {
	// Transport returns the underlying transport name: "secure".
	Transport() string

	// ---- Messaging ----
	SendText(text string) error
	OnMessage(cb func(text string))

	// ---- File transfer ----
	SendFile(path string) (FileTransferResult, error)
	OnFileReceived(cb func(r FileTransferResult))

	// ---- Speedtest ----
	RunSpeedtest(sizeMB int) (SpeedtestResult, error)
	OnSpeedtestReceived(cb func(r SpeedtestResult))

	// ---- Tunnels ----
	// CreateTunnel starts the local listener internally once the peer
	// acknowledges; the returned TunnelInfo.ID is the handle for CloseTunnel.
	CreateTunnel(p tunnel.Params) (TunnelInfo, error)
	CloseTunnel(id uint32) error
	ListTunnels() []TunnelInfo

	// ---- Lifecycle ----
	// Run blocks until ctx is cancelled, Close is called, or the underlying
	// connection breaks. Implementations spawn internal goroutines (reader,
	// heartbeat ticker, stream accept) and join them before returning.
	Run(ctx context.Context) error
	// Close is idempotent. It signals Run to return and closes the underlying
	// connection. Close does NOT block waiting for internal goroutines to
	// exit — callers that need to wait should select on Done() after Close.
	Close() error
	// Done returns a channel closed when the session fully terminates.
	// Callers MUST call Run before waiting on Done.
	Done() <-chan struct{}
}

// Errors returned by Session methods.
var (
	ErrUnsupported   = errors.New("engine: operation not supported on this transport")
	ErrSessionClosed = errors.New("engine: session closed")
)

// TunnelInfo is the transport-agnostic summary of an active tunnel.
type TunnelInfo struct {
	ID         uint32
	Label      string
	Protocol   string
	LocalPort  int
	TargetHost string
	TargetPort int
	BytesIn    uint64
	BytesOut   uint64
}

// ---- Options ----

// Option configures a Session.
type Option func(*sessionOpts)

type sessionOpts struct {
	cb      callbacks
	fileDir func() string // 动态接收目录；nil/返回"" → CWD（保持原行为）
}

// callbacks holds optional RX-side hooks. Implementations invoke them
// synchronously from the reader goroutine; callers must keep them fast.
type callbacks struct {
	mu              sync.RWMutex
	onMessage       func(string)
	onFileReceived  func(FileTransferResult)
	onSpeedtestRecv func(SpeedtestResult)
}

func (c *callbacks) fireMessage(text string) {
	c.mu.RLock()
	cb := c.onMessage
	c.mu.RUnlock()
	if cb != nil {
		cb(text)
	}
}

func (c *callbacks) fireFile(r FileTransferResult) {
	c.mu.RLock()
	cb := c.onFileReceived
	c.mu.RUnlock()
	if cb != nil {
		cb(r)
	}
}

func (c *callbacks) fireSpeedtest(r SpeedtestResult) {
	c.mu.RLock()
	cb := c.onSpeedtestRecv
	c.mu.RUnlock()
	if cb != nil {
		cb(r)
	}
}

// WithOnMessage registers a callback invoked when a text message arrives.
func WithOnMessage(cb func(string)) Option {
	return func(o *sessionOpts) { o.cb.onMessage = cb }
}

// WithOnFileReceived registers a callback invoked after a file is fully received.
func WithOnFileReceived(cb func(FileTransferResult)) Option {
	return func(o *sessionOpts) { o.cb.onFileReceived = cb }
}

// WithOnSpeedtestReceived registers a callback invoked after an incoming
// speedtest completes.
func WithOnSpeedtestReceived(cb func(SpeedtestResult)) Option {
	return func(o *sessionOpts) { o.cb.onSpeedtestRecv = cb }
}

// WithFileReceiveDir 注册动态接收目录 getter。每次接收文件时调用，
// 返回空串则保存到 CWD（保持原行为）。连接建立后改目录无需重连——
// getter 动态读取最新值（R11：getter 只做快照读，并发安全由调用方 mu 保证）。
func WithFileReceiveDir(getter func() string) Option {
	return func(o *sessionOpts) { o.fileDir = getter }
}

// NewSession creates a Session for the given Outcome.
//
// 统一 secure 后端：Outcome.Mux 是 engine mode 经 secureUpgrade（secure 协商 +
// yamux）产出的 StreamMux。NewSession 同步 open/accept 主控 stream 后返回，session
// 立即可用（SendText / CreateTunnel 可在 Run 前调用）。
func NewSession(ctx context.Context, out *Outcome, opts ...Option) (Session, error) {
	o := &sessionOpts{}
	for _, opt := range opts {
		opt(o)
	}
	return newSecureSession(ctx, out, o)
}
