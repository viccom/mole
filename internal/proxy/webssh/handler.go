package webssh

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// WebSocket 消息类型
const (
	msgData   byte = 0x01 // 终端输入数据
	msgResize byte = 0x02 // 窗口大小调整
	msgKeep   byte = 0x03 // 心跳
)

// Handler 管理单个 WebSSH 隧道的 SSH 连接和数据桥接
type Handler struct {
	name string
	cfg  WebSSHConfig

	sshClient *ssh.Client
	sshMu     sync.Mutex

	// TOFU 主机密钥缓存（跨连接复用，避免每次重连都是"首次信任"）
	hostKeys   map[string]ssh.PublicKey
	hostKeysMu sync.Mutex

	bytesIn  atomic.Uint64 // SSH 输出字节（目标→客户端）
	bytesOut atomic.Uint64 // SSH 输入字节（客户端→目标）
	running  atomic.Bool
	sessions atomic.Int32  // 当前活跃会话数
	lastErr  atomic.Value  // string
	lastRxMs atomic.Int64
	lastTxMs atomic.Int64
}

// Stats WebSSH 隧道运行时状态
type Stats struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Running  bool   `json:"running"`
	Sessions int    `json:"sessions"`
	BytesIn  uint64 `json:"bytes_in"`
	BytesOut uint64 `json:"bytes_out"`
	LastRxMs int64  `json:"last_rx_unix_ms,omitempty"`
	LastTxMs int64  `json:"last_tx_unix_ms,omitempty"`
	Error    string `json:"error,omitempty"`
}

func newHandler(name string, cfg WebSSHConfig) *Handler {
	h := &Handler{name: name, cfg: cfg, hostKeys: make(map[string]ssh.PublicKey)}
	h.lastErr.Store("")
	return h
}

func (h *Handler) Stats() Stats {
	errText := ""
	if v := h.lastErr.Load(); v != nil {
		if s, ok := v.(string); ok {
			errText = s
		}
	}
	return Stats{
		Name:     h.name,
		Host:     h.cfg.Host,
		Port:     h.cfg.Port,
		User:     h.cfg.User,
		Running:  h.running.Load(),
		Sessions: int(h.sessions.Load()),
		BytesIn:  h.bytesIn.Load(),
		BytesOut: h.bytesOut.Load(),
		LastRxMs: h.lastRxMs.Load(),
		LastTxMs: h.lastTxMs.Load(),
		Error:    errText,
	}
}

// HandleStream 处理来自服务端的 smux 流，桥接到 SSH 会话
func (h *Handler) HandleStream(stream io.ReadWriteCloser) {
	h.sessions.Add(1)
	defer h.sessions.Add(-1)

	client, err := h.getSSHClient()
	if err != nil {
		slog.Error("webssh connect failed", "name", h.name, "error", err)
		h.lastErr.Store(err.Error())
		h.running.Store(false)
		return
	}

	session, err := client.NewSession()
	if err != nil {
		slog.Error("webssh new session failed", "name", h.name, "error", err)
		h.lastErr.Store(err.Error())
		// SSH 连接可能已断开，清除缓存
		h.sshMu.Lock()
		if h.sshClient == client {
			h.sshClient.Close()
			h.sshClient = nil
		}
		h.sshMu.Unlock()
		return
	}
	defer session.Close()

	// 请求 PTY（默认 80x24，客户端会立即发送 resize）
	modes := ssh.TerminalModes{
		ssh.ECHO:          1, // 回显输入
		ssh.ICRNL:         1, // 输入 CR 转 NL
		ssh.ONLCR:         1, // 输出 NL 转 CR-NL
		ssh.OPOST:         1, // 启用输出处理
		ssh.ISIG:          1, // 启用信号字符 (^C, ^Z)
		ssh.IUTF8:         1, // UTF-8 输入模式 (RFC 8160)
		ssh.TTY_OP_ISPEED: 115200,
		ssh.TTY_OP_OSPEED: 115200,
	}
	if err := session.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		slog.Error("webssh pty request failed", "name", h.name, "error", err)
		h.lastErr.Store(err.Error())
		return
	}

	// 设置 stdin/stdout/stderr 为 stream
	stdin, err := session.StdinPipe()
	if err != nil {
		slog.Error("webssh stdin pipe failed", "name", h.name, "error", err)
		return
	}
	session.Stdout = &byteCounter{w: stream, counter: &h.bytesIn, lastMs: &h.lastRxMs}
	session.Stderr = &byteCounter{w: stream, counter: &h.bytesIn, lastMs: &h.lastRxMs}

	if err := session.Shell(); err != nil {
		slog.Error("webssh shell start failed", "name", h.name, "error", err)
		h.lastErr.Store(err.Error())
		return
	}

	h.running.Store(true)
	h.lastErr.Store("")
	slog.Info("webssh session started", "name", h.name, "user", h.cfg.User, "host", h.cfg.Host, "port", h.cfg.Port)

	// 从 stream 读取消息写入 SSH stdin，同时处理 resize
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.readLoop(stream, stdin, session)
	}()

	// 等待 session 结束或 stream 关闭
	sessionDone := make(chan error, 1)
	go func() {
		sessionDone <- session.Wait()
	}()

	select {
	case <-done:
		// stream 读取结束（客户端断开）
	case err := <-sessionDone:
		if err != nil {
			slog.Warn("webssh session ended", "name", h.name, "error", err)
		}
	}

	slog.Info("webssh session closed", "name", h.name)
}

// getSSHClient 获取或创建 SSH 连接（带连接池复用）
func (h *Handler) getSSHClient() (*ssh.Client, error) {
	h.sshMu.Lock()
	defer h.sshMu.Unlock()

	if h.sshClient != nil {
		// 检查连接是否仍然存活
		_, _, err := h.sshClient.SendRequest("keepalive@openssh.com", true, nil)
		if err == nil {
			return h.sshClient, nil
		}
		h.sshClient.Close()
		h.sshClient = nil
	}

	client, err := h.dialSSH()
	if err != nil {
		return nil, err
	}
	h.sshClient = client
	return client, nil
}

// dialSSH 建立新的 SSH 连接
func (h *Handler) dialSSH() (*ssh.Client, error) {
	hostKeyCallback := h.buildHostKeyCallback()
	config := &ssh.ClientConfig{
		User:            h.cfg.User,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	switch h.cfg.AuthType {
	case "password":
		config.Auth = []ssh.AuthMethod{ssh.Password(h.cfg.Password)}
	case "key":
		signer, err := ssh.ParsePrivateKey([]byte(h.cfg.PrivKey))
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}

	addr := net.JoinHostPort(h.cfg.Host, fmt.Sprintf("%d", h.cfg.Port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	return client, nil
}

// buildHostKeyCallback 构建主机密钥验证回调
// 优先使用 known_hosts 文件，不存在时采用 TOFU（首次信任）策略
func (h *Handler) buildHostKeyCallback() ssh.HostKeyCallback {
	// 尝试从可执行文件同目录加载 known_hosts
	exePath, _ := os.Executable()
	knownHostsPath := filepath.Join(filepath.Dir(exePath), "config", "known_hosts")
	if _, err := os.Stat(knownHostsPath); err == nil {
		cb, err := knownhosts.New(knownHostsPath)
		if err == nil {
			slog.Debug("webssh using known_hosts", "path", knownHostsPath)
			return cb
		}
		slog.Warn("webssh failed to load known_hosts, falling back to TOFU", "path", knownHostsPath, "error", err)
	}

	// TOFU 策略：首次连接信任并记录，后续连接验证（缓存跨连接复用）
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		h.hostKeysMu.Lock()
		defer h.hostKeysMu.Unlock()

		keyBytes := key.Marshal()
		addr := remote.String()

		if prev, ok := h.hostKeys[addr]; ok {
			if !bytes.Equal(prev.Marshal(), keyBytes) {
				return fmt.Errorf("webssh: host key mismatch for %s (possible MITM)", hostname)
			}
			return nil
		}

		h.hostKeys[addr] = key
		slog.Info("webssh trusted host key (TOFU)", "hostname", hostname, "key_type", key.Type())
		return nil
	}
}

// readLoop 从 smux 流读取消息，解析类型后分发
func (h *Handler) readLoop(stream io.Reader, stdin io.WriteCloser, session *ssh.Session) {
	for {
		// 读取消息类型（1 字节）
		var msgType [1]byte
		if _, err := io.ReadFull(stream, msgType[:]); err != nil {
			if err != io.EOF {
				slog.Debug("webssh read msg type error", "name", h.name, "error", err)
			}
			return
		}

		switch msgType[0] {
		case msgData:
			// 读取 2 字节大端序长度
			var lenBuf [2]byte
			if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
				slog.Debug("webssh read data length error", "name", h.name, "error", err)
				return
			}
			length := binary.BigEndian.Uint16(lenBuf[:])
			if length == 0 {
				continue
			}

			// 读取数据并写入 SSH stdin
			buf := make([]byte, length)
			if _, err := io.ReadFull(stream, buf); err != nil {
				slog.Debug("webssh read data error", "name", h.name, "error", err)
				return
			}
			if _, err := stdin.Write(buf); err != nil {
				slog.Debug("webssh write stdin error", "name", h.name, "error", err)
				return
			}
			h.bytesOut.Add(uint64(len(buf)))
			h.lastTxMs.Store(time.Now().UnixMilli())

		case msgResize:
			// 读取 2 字节大端序 JSON 长度
			var lenBuf [2]byte
			if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
				slog.Debug("webssh read resize length error", "name", h.name, "error", err)
				return
			}
			length := binary.BigEndian.Uint16(lenBuf[:])
			if length == 0 || length > 256 {
				continue
			}
			buf := make([]byte, length)
			if _, err := io.ReadFull(stream, buf); err != nil {
				slog.Debug("webssh read resize data error", "name", h.name, "error", err)
				return
			}
			var size struct {
				Cols int `json:"cols"`
				Rows int `json:"rows"`
			}
			if err := json.Unmarshal(buf, &size); err != nil {
				slog.Debug("webssh parse resize error", "name", h.name, "error", err)
				continue
			}
			if size.Cols > 0 && size.Rows > 0 {
				if err := session.WindowChange(size.Rows, size.Cols); err != nil {
					slog.Debug("webssh window change error", "name", h.name, "error", err)
				}
			}

		case msgKeep:
			// 消费 2 字节长度前缀（writeWebSSHMsg 总是写入 [type][len 2B][payload]）
			var keepLen [2]byte
			if _, err := io.ReadFull(stream, keepLen[:]); err != nil {
				return
			}

		default:
			slog.Debug("webssh unknown msg type", "name", h.name, "type", fmt.Sprintf("0x%02x", msgType[0]))
		}
	}
}

// Close 关闭 SSH 连接
func (h *Handler) Close() {
	h.sshMu.Lock()
	defer h.sshMu.Unlock()
	if h.sshClient != nil {
		h.sshClient.Close()
		h.sshClient = nil
	}
	h.running.Store(false)
}

// byteCounter 包装 io.Writer 统计写入字节数
type byteCounter struct {
	w       io.Writer
	counter *atomic.Uint64
	lastMs  *atomic.Int64
}

func (bc *byteCounter) Write(p []byte) (int, error) {
	n, err := bc.w.Write(p)
	if n > 0 {
		bc.counter.Add(uint64(n))
		bc.lastMs.Store(time.Now().UnixMilli())
	}
	return n, err
}
