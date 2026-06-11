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

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// 消息类型
const (
	msgData   byte = 0x01 // 终端数据
	msgResize byte = 0x02 // 窗口大小调整
	msgKeep   byte = 0x03 // 心跳

	// 文件操作请求（浏览器 → 客户端）
	msgFileListReq     byte = 0x04
	msgFileUploadReq   byte = 0x05
	msgFileUploadData  byte = 0x06
	msgFileUploadEnd   byte = 0x07
	msgFileDownloadReq byte = 0x08
	msgFileDeleteReq   byte = 0x09
	msgFileMkdirReq    byte = 0x0A
	msgFileReadReq     byte = 0x0B // 文件内容读取请求

	// 文件操作响应（客户端 → 浏览器）
	msgFileListResp byte = 0x0C
	msgFileDataResp byte = 0x0D
	msgFileAckResp  byte = 0x0E
	msgFileReadResp byte = 0x0F // 文件内容读取响应
)

// Handler 管理单个 WebSSH 隧道的 SSH 连接和数据桥接
type Handler struct {
	name string
	cfg  WebSSHConfig

	sshClient  *ssh.Client
	sshMu      sync.Mutex
	sftpClient *sftp.Client
	sftpMu     sync.Mutex

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
	// mutexWriter 保护 stream 写入：终端输出和文件响应可能并发写同一 stream
	writeMu := &sync.Mutex{}
	sw := &mutexWriter{mu: writeMu, w: stream}
	session.Stdout = &byteCounter{mu: writeMu, w: stream, counter: &h.bytesIn, lastMs: &h.lastRxMs}
	session.Stderr = &byteCounter{mu: writeMu, w: stream, counter: &h.bytesIn, lastMs: &h.lastRxMs}

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
		h.readLoop(stream, stdin, session, sw)
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
func (h *Handler) readLoop(stream io.Reader, stdin io.WriteCloser, session *ssh.Session, sw *mutexWriter) {
	var uploadFile *sftp.File
	var uploadErr error
	defer func() {
		if uploadFile != nil {
			uploadFile.Close()
		}
	}()
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
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			if len(payload) == 0 {
				continue
			}
			if _, err := stdin.Write(payload); err != nil {
				slog.Debug("webssh write stdin error", "name", h.name, "error", err)
				return
			}
			h.bytesOut.Add(uint64(len(payload)))
			h.lastTxMs.Store(time.Now().UnixMilli())

		case msgResize:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			if len(payload) == 0 || len(payload) > 256 {
				continue
			}
			var size struct {
				Cols int `json:"cols"`
				Rows int `json:"rows"`
			}
			if err := json.Unmarshal(payload, &size); err != nil {
				slog.Debug("webssh parse resize error", "name", h.name, "error", err)
				continue
			}
			if size.Cols > 0 && size.Rows > 0 {
				if err := session.WindowChange(size.Rows, size.Cols); err != nil {
					slog.Debug("webssh window change error", "name", h.name, "error", err)
				}
			}

		case msgKeep:
			// 消费 2 字节长度前缀
			var keepLen [2]byte
			if _, err := io.ReadFull(stream, keepLen[:]); err != nil {
				return
			}

		case msgFileListReq:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			h.handleFileListReq(payload, sw)

		case msgFileUploadReq:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			uploadFile = h.handleFileUploadReq(payload, sw)
			uploadErr = nil

		case msgFileUploadData:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			if uploadErr == nil {
				if _, uploadErr = h.handleFileUploadData(payload, uploadFile); uploadErr != nil {
					slog.Warn("webssh upload write error", "name", h.name, "error", uploadErr)
				}
			}

		case msgFileUploadEnd:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			h.handleFileUploadEnd(payload, uploadFile, sw, uploadErr)
			uploadFile = nil
			uploadErr = nil

		case msgFileDownloadReq:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			go h.handleFileDownloadReq(payload, sw)

		case msgFileDeleteReq:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			h.handleFileDeleteReq(payload, sw)

		case msgFileMkdirReq:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			h.handleFileMkdirReq(payload, sw)

		case msgFileReadReq:
			payload, err := readPayload(stream)
			if err != nil {
				return
			}
			go h.handleFileReadReq(payload, sw)

		default:
			// 消费 [len 2B][payload]，避免流损坏
			if _, err := readPayload(stream); err != nil {
				return
			}
			slog.Debug("webssh unknown msg type", "name", h.name, "type", fmt.Sprintf("0x%02x", msgType[0]))
		}
	}
}

// Close 关闭 SSH 和 SFTP 连接
func (h *Handler) Close() {
	h.sftpMu.Lock()
	if h.sftpClient != nil {
		h.sftpClient.Close()
		h.sftpClient = nil
	}
	h.sftpMu.Unlock()

	h.sshMu.Lock()
	defer h.sshMu.Unlock()
	if h.sshClient != nil {
		h.sshClient.Close()
		h.sshClient = nil
	}
	h.running.Store(false)
}

// mutexWriter 保护并发写入同一 stream
type mutexWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (mw *mutexWriter) Write(p []byte) (int, error) {
	mw.mu.Lock()
	defer mw.mu.Unlock()
	return mw.w.Write(p)
}

// byteCounter 包装 io.Writer 统计写入字节数，用 writeWebSSHMsg 封装终端输出
// 自行持锁保证 writeWebSSHMsg 的 header+payload 原子写入，避免与文件 handler 交错
type byteCounter struct {
	mu      *sync.Mutex
	w       io.Writer // 原始 stream（非 mutexWriter）
	counter *atomic.Uint64
	lastMs  *atomic.Int64
}

func (bc *byteCounter) Write(p []byte) (int, error) {
	bc.mu.Lock()
	err := writeWebSSHMsg(bc.w, msgData, p)
	bc.mu.Unlock()
	if err != nil {
		return 0, err
	}
	n := len(p)
	bc.counter.Add(uint64(n))
	bc.lastMs.Store(time.Now().UnixMilli())
	return n, nil
}

// writeWebSSHMsg 写入 [type 1B][len 2B BE][payload] 到流
func writeWebSSHMsg(w io.Writer, msgType byte, payload []byte) error {
	if len(payload) > 65535 {
		return fmt.Errorf("payload too large: %d bytes (max 65535)", len(payload))
	}
	var hdr [3]byte
	hdr[0] = msgType
	binary.BigEndian.PutUint16(hdr[1:3], uint16(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// readPayload 从流读取 [len 2B BE][payload]，已消费完 type 字节后调用
func readPayload(r io.Reader) ([]byte, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint16(lenBuf[:])
	if length == 0 {
		return nil, nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeFileAck 发送操作确认/错误响应
func writeFileAck(w *mutexWriter, ok bool, msg string) {
	resp, _ := json.Marshal(map[string]any{"ok": ok, "msg": msg})
	w.mu.Lock()
	defer w.mu.Unlock()
	writeWebSSHMsg(w.w, msgFileAckResp, resp)
}
