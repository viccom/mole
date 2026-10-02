package webssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// 本文件锁两条高收益不变量（2026-10-02 审查 A-2：webssh 1111 行 / 0 测试，
// 其中解互锁链此前只有注释一道防线）：
//  1. readPayload 的边界与畸形输入
//  2. HandleStream 各路径不泄漏 goroutine——特别是「远端不读 stdin」时
//     写超时 → readLoop 退出 → defer session.Close() 解互锁这条链
//     （handler.go writeStdinWithTimeout 注释描述的机制）

// waitForGoroutines 断言 goroutine 数回落到 baseline 及以下（goleak 式语义，
// 不引依赖：轮询 + 截止时间）
func waitForGoroutines(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		now := runtime.NumGoroutine()
		if now <= baseline {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			n := runtime.Stack(buf, true)
			t.Fatalf("goroutines did not settle: baseline=%d now=%d\n%s", baseline, now, buf[:n])
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestReadPayload 边界与畸形输入：readPayload 是远端数据的第一个解析点
func TestReadPayload(t *testing.T) {
	t.Run("normal payload", func(t *testing.T) {
		frame := []byte{0x00, 0x05, 'h', 'e', 'l', 'l', 'o'}
		got, err := readPayload(bytesReader(frame))
		if err != nil || string(got) != "hello" {
			t.Fatalf("got %q, %v; want hello, nil", got, err)
		}
	})
	t.Run("zero length", func(t *testing.T) {
		got, err := readPayload(bytesReader([]byte{0x00, 0x00}))
		if err != nil || got != nil {
			t.Fatalf("got %q, %v; want nil, nil", got, err)
		}
	})
	t.Run("max length 65535", func(t *testing.T) {
		frame := make([]byte, 2+65535)
		binary.BigEndian.PutUint16(frame, 65535)
		got, err := readPayload(bytesReader(frame))
		if err != nil || len(got) != 65535 {
			t.Fatalf("got len=%d, %v; want 65535, nil", len(got), err)
		}
	})
	t.Run("missing length bytes", func(t *testing.T) {
		if _, err := readPayload(bytesReader([]byte{0x00})); err == nil {
			t.Fatal("单字节长度头必须报错")
		}
	})
	t.Run("truncated payload", func(t *testing.T) {
		frame := []byte{0x00, 0x0A, 'x', 'y'} // 声明 10 字节只给 2 字节
		if _, err := readPayload(bytesReader(frame)); err == nil {
			t.Fatal("载荷截断必须报错，不得静默返回短数据")
		}
	})
	t.Run("immediate EOF", func(t *testing.T) {
		if _, err := readPayload(bytesReader(nil)); err == nil {
			t.Fatal("空流必须报 io.EOF")
		}
	})
}

func bytesReader(b []byte) io.Reader { return &oneByteReader{b: b} }

// oneByteReader 每次最多吐 1 字节：防止 io.ReadFull 语义掩盖分片到达路径
type oneByteReader struct{ b []byte }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.b[0]
	r.b = r.b[1:]
	return 1, nil
}

// TestWriteStdinWithTimeout_BlockedWriterReleased 单元级锁注释机制：
// 写阻塞 → 超时返回；随后 stdin 关闭必须唤醒仍阻塞的内部 goroutine
// （不得永久泄漏）。超时经 var 注入缩短，不等真实 30s。
func TestWriteStdinWithTimeout_BlockedWriterReleased(t *testing.T) {
	old := writeStdinTimeout
	writeStdinTimeout = 100 * time.Millisecond
	t.Cleanup(func() { writeStdinTimeout = old })

	pr, pw := io.Pipe() // 无 reader 消费，Write 永久阻塞

	errCh := make(chan error, 1)
	go func() { errCh <- writeStdinWithTimeout(pw, []byte("payload")) }()
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("阻塞写必须超时报错，got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writeStdinWithTimeout 未在注入超时内返回")
	}

	// stdin pipe 关闭后，仍阻塞在 Write 的内部 goroutine 必须被唤醒退出
	innerDone := make(chan struct{})
	go func() {
		_, _ = pw.Write([]byte("again")) // 复用同一 pipe：若内部 goroutine 已泄，此写也阻塞
		close(innerDone)
	}()
	pr.Close() // 唤醒机制：pipe 关闭使所有阻塞 Write 返回 ErrClosedPipe
	select {
	case <-innerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("pipe 关闭后阻塞的 Write 未被唤醒（内部 goroutine 泄漏）")
	}
}

// TestHandleStreamDialFailureNoLeak 连接失败路径（与认证失败同一 HandleStream
// 早退分支：ssh.Dial 内握手/认证任一失败都从 getSSHClient 返回）不泄漏
// goroutine、会话计数归零。
func TestHandleStreamDialFailureNoLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	h := newHandler("t-dialfail", WebSSHConfig{
		Enable: true, Host: "127.0.0.1", Port: 1, // 保留端口，连接立即拒绝
		User: "u", AuthType: "password", Password: "p",
	})

	c1, c2 := net.Pipe()
	defer c2.Close()

	h.HandleStream(c1) // 同步返回（无 goroutine 依赖）

	if st := h.Stats(); st.Sessions != 0 {
		t.Fatalf("会话计数应归零，got %d", st.Sessions)
	}
	if st := h.Stats(); st.Error == "" {
		t.Fatal("连接失败必须留痕 lastErr")
	}
	waitForGoroutines(t, baseline)
}

// startStubSSHServer 启动接受任意口令的本地 SSH 服务端；onSession 在收到
// session 通道后回调（stuck-stdin 场景刻意不从通道读数据）。
// 返回值 shutdown 关闭全部已接受连接（goroutine 断言前必须调用：连接池
// 保留的 SSH 连接按设计存活，其 goroutine 属正常存量而非泄漏）。
func startStubSSHServer(t *testing.T, onSession func(ch ssh.Channel, reqs <-chan *ssh.Request)) (addr string, shutdown func()) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil // 接受任意口令
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	var mu sync.Mutex
	conns := make(map[*ssh.ServerConn]struct{})
	shutdown = func() {
		mu.Lock()
		defer mu.Unlock()
		for c := range conns {
			c.Close()
		}
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					conn.Close()
					return
				}
				mu.Lock()
				conns[sconn] = struct{}{}
				mu.Unlock()
				go ssh.DiscardRequests(reqs)
				for newChan := range chans {
					if newChan.ChannelType() != "session" {
						newChan.Reject(ssh.UnknownChannelType, "stub only accepts session")
						continue
					}
					ch, chReqs, err := newChan.Accept()
					if err != nil {
						continue
					}
					go onSession(ch, chReqs)
				}
			}()
		}
	}()
	return ln.Addr().String(), shutdown
}

// replySessionReqs 应答 pty/shell/exec 等 session 级请求（全部接受）
func replySessionReqs(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		if req.WantReply {
			req.Reply(req.Type == "pty-req" || req.Type == "shell" || req.Type == "exec" || req.Type == "env", nil)
		}
	}
}

// TestHandleStreamStuckStdin_ExitsAndReleases 端到端锁 writeStdinWithTimeout
// 注释描述的解互锁链：远端进程不读 stdin（本测试中 stub 服务端从不读通道
// 数据）→ SSH channel window 耗尽 → stdin.Write 永久阻塞 → 写超时 →
// readLoop 退出 → close(done) → HandleStream select 退出 → defer
// session.Close() 解互锁并唤醒阻塞的写 goroutine。任何一环断裂都会表现为
// HandleStream 不返回或 goroutine 不回落。
func TestHandleStreamStuckStdin_ExitsAndReleases(t *testing.T) {
	addr, shutdownSrv := startStubSSHServer(t, func(ch ssh.Channel, reqs <-chan *ssh.Request) {
		// 刻意不从 ch 读任何数据：客户端写入在 2MB channel window 耗尽后阻塞
		replySessionReqs(ch, reqs)
	})

	old := writeStdinTimeout
	writeStdinTimeout = 200 * time.Millisecond
	t.Cleanup(func() { writeStdinTimeout = old })

	host, portStr, _ := net.SplitHostPort(addr)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse stub server port: %v", err)
	}

	h := newHandler("t-stuck", WebSSHConfig{
		Enable: true, Host: host, Port: port,
		User: "u", AuthType: "password", Password: "p",
	})

	c1, c2 := net.Pipe()
	defer c2.Close()

	baseline := runtime.NumGoroutine()

	done := make(chan struct{})
	go func() {
		h.HandleStream(c1)
		close(done)
	}()
	// HandleStream 一返回就关流（对应 dispatchStream 的 defer stream.Close）：
	// 同时让写循环中阻塞的帧写立即以错误返回，不等 5s deadline
	go func() {
		<-done
		c1.Close()
	}()

	// 喂超过 2MB channel window 的数据：64KB × 40 帧，服务端永不读
	payload := make([]byte, 65535)
	frame := make([]byte, 3+len(payload))
	frame[0] = msgData
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(payload)))
	copy(frame[3:], payload)
	for i := 0; i < 40; i++ {
		c2.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := c2.Write(frame); err != nil {
			break // HandleStream 已退出（管道对端关闭），属预期路径
		}
	}

	// 核心断言：解互锁链完整时 HandleStream 必然返回
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("HandleStream 未退出：远端不读 stdin 时写超时→readLoop→session.Close 解互锁链断裂（goroutine/session 泄漏）")
	}

	if st := h.Stats(); st.Sessions != 0 {
		t.Fatalf("会话计数应归零，got %d", st.Sessions)
	}

	// 走生产清理路径（h.Close = Manager.Close 对每个 handler 的动作）后再
	// 断言：连接池保留的 SSH 连接存活期间其 goroutine 属正常存量
	h.Close()
	shutdownSrv()
	waitForGoroutines(t, baseline)
}
