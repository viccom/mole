package moleAgent_client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_client/internal/protocol"
)

// chunkReader 按固定分片依次返回数据，模拟 TCP 分包到达。
// 每次调用 Read 最多吐出一个分片；分片大于调用方缓冲时分多次返回
// （与真实 conn 的部分读语义一致）。
type chunkReader struct {
	chunks [][]byte
	pos    int
	off    int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for r.pos < len(r.chunks) {
		chunk := r.chunks[r.pos]
		if r.off >= len(chunk) {
			r.pos++
			r.off = 0
			continue
		}
		n := copy(p, chunk[r.off:])
		r.off += n
		if r.off >= len(chunk) {
			r.pos++
			r.off = 0
		}
		return n, nil
	}
	return 0, io.EOF
}

// splitChunks 把字符串按固定大小切块
func splitChunks(s string, size int) [][]byte {
	var out [][]byte
	for len(s) > 0 {
		n := size
		if n > len(s) {
			n = len(s)
		}
		out = append(out, []byte(s[:n]))
		s = s[n:]
	}
	return out
}

// TestWriteCmd_AppendsNewline 命令必须以 '\n' 结尾，与服务端
// writeJSONLine 的 JSON Lines 约定一致；旧服务端 json.Unmarshal
// 兼容尾部空白，不影响存量服务端。
func TestWriteCmd_AppendsNewline(t *testing.T) {
	var buf bytes.Buffer
	cmd := protocol.ControlCmd{Cmd: "register", NodeID: "Test0001", Token: "t"}
	if err := writeCmd(&buf, cmd); err != nil {
		t.Fatalf("writeCmd error: %v", err)
	}
	raw := buf.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Errorf("writeCmd output must be newline-terminated, got %q", raw)
	}
	var got protocol.ControlCmd
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Cmd != "register" || got.NodeID != "Test0001" {
		t.Errorf("unexpected cmd: %+v", got)
	}
}

// TestWriteResp_AppendsNewline 推送应答同样走 JSON Lines 约定。
func TestWriteResp_AppendsNewline(t *testing.T) {
	var buf bytes.Buffer
	writeResp(&buf, "ok", "tunnels updated")
	raw := buf.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Errorf("writeResp output must be newline-terminated, got %q", raw)
	}
	var got protocol.ControlResponse
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Cmd != "ok" || got.Msg != "tunnels updated" {
		t.Errorf("unexpected resp: %+v", got)
	}
}

// TestReadResponse_Fragmented 服务端响应跨多个 Read 分片到达。
// 这是本次修复的核心场景：旧实现单次 Read 后立即解析，分包直接报错。
func TestReadResponse_Fragmented(t *testing.T) {
	msg := `{"cmd":"ok","msg":"registered"}` + "\n"
	r := &chunkReader{chunks: splitChunks(msg, 5)}
	resp, err := readResponse(r, time.Second)
	if err != nil {
		t.Fatalf("readResponse error: %v", err)
	}
	if resp.Cmd != "ok" || resp.Msg != "registered" {
		t.Errorf("unexpected resp: %+v", resp)
	}
}

// TestReadResponse_BareJSONNoNewline 兼容无换行的裸 JSON 响应，
// 保证 readControlMsg 不会被格式收紧破坏。
func TestReadResponse_BareJSONNoNewline(t *testing.T) {
	r := &chunkReader{chunks: [][]byte{[]byte(`{"cmd":"pong"}`)}}
	resp, err := readResponse(r, time.Second)
	if err != nil {
		t.Fatalf("readResponse error: %v", err)
	}
	if resp.Cmd != "pong" {
		t.Errorf("expected cmd=pong, got %q", resp.Cmd)
	}
	if resp.Msg != "" {
		t.Errorf("expected empty msg, got %q", resp.Msg)
	}
}

// TestReadControlMsg_OversizeRejected 客户端侧同样必须拒绝超限消息，
// 防止异常服务端把客户端内存耗尽。
func TestReadControlMsg_OversizeRejected(t *testing.T) {
	msg := `{"name":"` + strings.Repeat("x", maxControlMsgSize) + `"}`
	_, err := readControlMsg(&chunkReader{chunks: [][]byte{[]byte(msg)}}, maxControlMsgSize)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}

// countingReader 统计底层实际被消费的字节数（bufio 会整块预读，
// consumed 即"读路径总共从流里拿走了多少"）。
type countingReader struct {
	r        io.Reader
	consumed int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.consumed += int64(n)
	return n, err
}

// TestReadBoundedCmdLine_OversizeStopsConsuming 单元级精确度量：超过上限
// 仍无换行时，读路径从流里消费的字节数必须停在上限 + 单次 bufio 预读
// 余量处，而不是把发送方备好的数据全部读入。
func TestReadBoundedCmdLine_OversizeStopsConsuming(t *testing.T) {
	junk := strings.Repeat("a", maxCmdLineBytes+256*1024) // 无换行
	cr := &countingReader{r: &chunkReader{chunks: splitChunks(junk, 64*1024)}}

	_, err := readBoundedCmdLine(bufio.NewReader(cr), maxCmdLineBytes)
	if !errors.Is(err, errCmdLineTooLarge) {
		t.Fatalf("expected errCmdLineTooLarge, got %v", err)
	}
	// 上限 + bufio 默认缓冲（4096）是停止消费的确定性上界
	if cr.consumed > int64(maxCmdLineBytes+bufio.NewReader(nil).Size()) {
		t.Fatalf("读侧应在上限处停止消费，实际消费 %d 字节（上限 %d）", cr.consumed, maxCmdLineBytes)
	}
}

// TestReadBoundedCmdLine_ExactLimitWithNewlineAccepted 恰好等于上限且以
// 换行结尾的行是合法的——上限判断是 > 而非 >=，不得误伤边界行。
func TestReadBoundedCmdLine_ExactLimitWithNewlineAccepted(t *testing.T) {
	line := strings.Repeat("a", maxCmdLineBytes-1) + "\n" // 含 \n 共 maxCmdLineBytes 字节
	got, err := readBoundedCmdLine(bufio.NewReader(strings.NewReader(line)), maxCmdLineBytes)
	if err != nil {
		t.Fatalf("exact-limit line must be accepted, got %v", err)
	}
	if len(got) != maxCmdLineBytes {
		t.Fatalf("line length = %d, want %d", len(got), maxCmdLineBytes)
	}
}

// newSmuxStreamPair 经 net.Pipe 建立一对 smux 会话并开好一条流：
// 服务端侧模拟"假服务端"向客户端推流。返回 teardown 关闭全部会话
// （幂等，可先于 t.Cleanup 手动调用以解除写侧阻塞）。
// Version 2 与生产 DefaultSmuxVersion 一致；MaxStreamBuffer 特意取小
// （生产为 RDP 流畅开到 4MB）——v2 有按流窗口流控，读侧停止消费后
// 写侧随即被反压，"读侧不再吞数据"才能被写侧写入量度量。
func newSmuxStreamPair(t *testing.T) (cliStream, srvStream *smux.Stream, teardown func()) {
	t.Helper()
	cliConn, srvConn := net.Pipe()
	cfg := &smux.Config{
		Version:           2,
		KeepAliveDisabled: true,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  1 << 20,
		MaxStreamBuffer:   64 * 1024,
	}
	srvSess, err := smux.Server(srvConn, cfg)
	if err != nil {
		t.Fatalf("smux.Server: %v", err)
	}
	cliSess, err := smux.Client(cliConn, cfg)
	if err != nil {
		t.Fatalf("smux.Client: %v", err)
	}
	srv, err := srvSess.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	cli, err := cliSess.AcceptStream()
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	teardown = func() {
		cliSess.Close()
		srvSess.Close()
	}
	t.Cleanup(teardown)
	return cli, srv, teardown
}

// TestHandleServerCmd_OversizeLineRejected 恶意/故障服务端在 5s deadline 内
// 灌入超过上限的无换行数据——handleServerCmd 必须视为协议违规返回 false，
// 且读侧必须在上限处停止消费（写侧因 smux v2 按流窗口被反压），而不是
// 把数据全部吞进内存。防内存尖峰是本修复的全部意义，故除 false 外还
// 断言写侧成功写入量与耗时。
func TestHandleServerCmd_OversizeLineRejected(t *testing.T) {
	c := newStatusTestClient(t)
	cliStream, srvStream, teardown := newSmuxStreamPair(t)

	// 写侧共备 4MB 无换行垃圾数据，远超 1MB 上限；首字节 '{' 与
	// dispatchStream 的 peek 分发判定保持一致
	const totalSend = 4 << 20
	written := make(chan int, 1)
	go func() {
		chunk := bytes.Repeat([]byte{'a'}, 64*1024)
		chunk[0] = '{'
		var total int
		for total < totalSend {
			n, err := srvStream.Write(chunk)
			total += n
			if err != nil {
				break
			}
		}
		written <- total
	}()

	start := time.Now()
	ok := c.handleServerCmd(cliStream, bufio.NewReader(cliStream))
	elapsed := time.Since(start)

	if ok {
		t.Fatal("超过上限的无换行数据必须判协议违规（返回 false），got true")
	}
	teardown() // 关闭会话解除写侧阻塞，取回实际写入量
	total := <-written
	if total > 2*maxCmdLineBytes {
		t.Fatalf("读侧应在上限处停止消费，写侧却成功写入 %d 字节（上限 %d）", total, maxCmdLineBytes)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("超限应立即断开而不是等满 5s deadline，耗时 %v", elapsed)
	}
}

// TestHandleServerCmd_NormalCommandStillWorks 带换行、小于上限的正常命令
// 行为不变：tunnel_push 被处理（true）并回 ok 应答。
func TestHandleServerCmd_NormalCommandStillWorks(t *testing.T) {
	c := newStatusTestClient(t)
	cliStream, srvStream, _ := newSmuxStreamPair(t)

	cmd := `{"cmd":"tunnel_push","tunnels":[]}` + "\n"
	if _, err := srvStream.Write([]byte(cmd)); err != nil {
		t.Fatalf("write command: %v", err)
	}

	if ok := c.handleServerCmd(cliStream, bufio.NewReader(cliStream)); !ok {
		t.Fatal("正常 tunnel_push 命令必须被处理（返回 true）")
	}

	// 成功路径必须回 ok 应答（writeResp 走 JSON Lines 约定）
	_ = srvStream.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(srvStream).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp struct {
		Cmd string `json:"cmd"`
		Msg string `json:"msg"`
	}
	if json.Unmarshal(line, &resp) != nil || resp.Cmd != "ok" {
		t.Fatalf("expected ok response, got %q", line)
	}
}

// TestHandleServerCmd_BareJSONNoNewlineCompat 兼容旧服务端无换行的裸 JSON：
// 读失败（EOF/deadline）时"取已有内容、TrimRight \x00、空则 false"的兜底
// 语义必须保持。子用例二覆盖空数据路径（直接断开 → false）。
func TestHandleServerCmd_BareJSONNoNewlineCompat(t *testing.T) {
	t.Run("裸JSON无换行EOF", func(t *testing.T) {
		c := newStatusTestClient(t)
		cliStream, srvStream, _ := newSmuxStreamPair(t)
		if _, err := srvStream.Write([]byte(`{"cmd":"tunnel_push","tunnels":[]}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		_ = srvStream.Close() // 触发读侧 EOF
		if ok := c.handleServerCmd(cliStream, bufio.NewReader(cliStream)); !ok {
			t.Fatal("无换行裸 JSON 的兼容语义回退（应取已有内容继续解析）")
		}
	})

	t.Run("空数据EOF", func(t *testing.T) {
		c := newStatusTestClient(t)
		cliStream, srvStream, _ := newSmuxStreamPair(t)
		_ = srvStream.Close()
		if ok := c.handleServerCmd(cliStream, bufio.NewReader(cliStream)); ok {
			t.Fatal("空数据 EOF 必须返回 false")
		}
	})
}

// dispatchOversizeProbe 跑一次"假服务端经 dispatchStream 推超限头"的探测：
// firstByte 为协议头首字节（\x00 隧道名 / \x01 WebSSH），写侧共备 4MB
// 无换行垃圾数据。返回 dispatchStream 耗时与写侧成功写入量。
// 共用断言逻辑：超限必须立即断开本流（不等满 5s deadline）且读侧停止
// 消费（写侧被 smux v2 按流窗口反压）——防内存尖峰的核心可观测。
func dispatchOversizeProbe(t *testing.T, c *Client, firstByte byte) (elapsed time.Duration, total int) {
	t.Helper()
	cliStream, srvStream, teardown := newSmuxStreamPair(t)

	const totalSend = 4 << 20
	written := make(chan int, 1)
	go func() {
		chunk := bytes.Repeat([]byte{'a'}, 64*1024)
		chunk[0] = firstByte
		var n int
		for n < totalSend {
			w, err := srvStream.Write(chunk)
			n += w
			if err != nil {
				break
			}
		}
		written <- n
	}()

	done := make(chan struct{})
	go func() {
		c.dispatchStream(cliStream)
		close(done)
	}()

	// 旧实现里头读取超时后还会落入 http.ReadRequest（其重置新的 5s
	// deadline），全程 ~10s；20s 上限只为兜底挂死，正常路径远快于此
	start := time.Now()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("dispatchStream 未返回")
	}
	elapsed = time.Since(start)
	teardown() // 关闭会话解除写侧阻塞，取回实际写入量
	return elapsed, <-written
}

// TestDispatchStream_OversizeTunnelNameHeaderRejected \x00 隧道名头超过
// 上限仍无换行——协议违规，dispatchStream 必须断开本流。
func TestDispatchStream_OversizeTunnelNameHeaderRejected(t *testing.T) {
	c := newStatusTestClient(t)
	elapsed, total := dispatchOversizeProbe(t, c, 0x00)
	if total > 2*maxCmdLineBytes {
		t.Fatalf("读侧应在 1MB 上限处停止消费，写侧却成功写入 %d 字节", total)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("超限应立即断开而不是等满 5s deadline，耗时 %v", elapsed)
	}
}

// TestDispatchStream_OversizeWebSSHHeaderRejected \x01 WebSSH 名头超过
// 上限仍无换行——协议违规，dispatchStream 必须断开本流。
func TestDispatchStream_OversizeWebSSHHeaderRejected(t *testing.T) {
	c := newStatusTestClient(t)
	elapsed, total := dispatchOversizeProbe(t, c, 0x01)
	if total > 2*maxCmdLineBytes {
		t.Fatalf("读侧应在 1MB 上限处停止消费，写侧却成功写入 %d 字节", total)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("超限应立即断开而不是等满 5s deadline，耗时 %v", elapsed)
	}
}

// TestDispatchStream_NormalTunnelNameHeaderForwards 正常短隧道名头
// （\x00<name>\n + 负载）必须按名路由转发——上限不得误伤正常路径。
func TestDispatchStream_NormalTunnelNameHeaderForwards(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { backend.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := backend.Accept(); err == nil {
			accepted <- conn
		}
	}()

	c := newStatusTestClient(t)
	enabled := true
	c.mu.Lock()
	c.tunnels = []Tunnel{{Name: "t1", Type: TunnelTypeTCP, Target: backend.Addr().String(), Enabled: &enabled}}
	c.mu.Unlock()

	cliStream, srvStream, _ := newSmuxStreamPair(t)
	go c.dispatchStream(cliStream)

	if _, err := srvStream.Write([]byte("\x00t1\nhello-proxy")); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case conn := <-accepted:
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("backend read: %v", err)
		}
		if string(buf[:n]) != "hello-proxy" {
			t.Fatalf("backend got %q, want %q", buf[:n], "hello-proxy")
		}
		_ = conn.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("backend 未收到连接：正常短名头未被转发")
	}
}

// TestDispatchStream_NormalWebSSHHeaderRouted 正常短 WebSSH 名头
// （\x01<name>\n）必须路由进 websshMgr：未注册名会留 "not found" 告警
// 并关闭流（EOF）——证明头被完整解析而不是被上限拦截。
func TestDispatchStream_NormalWebSSHHeaderRouted(t *testing.T) {
	buf := captureLog(t) // 替换全局 log 输出，不可并行
	c := newStatusTestClient(t)
	cliStream, srvStream, _ := newSmuxStreamPair(t)

	go c.dispatchStream(cliStream)
	if _, err := srvStream.Write([]byte("\x01web1\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = srvStream.SetReadDeadline(time.Now().Add(2 * time.Second))
	tmp := make([]byte, 16)
	if _, err := srvStream.Read(tmp); err != io.EOF {
		t.Fatalf("websshMgr 关闭流后服务端应收到 EOF，got %v", err)
	}
	if !strings.Contains(buf.String(), "webssh tunnel not found") {
		t.Fatalf("正常短名头必须路由到 websshMgr（未见 not found 告警）:\n%s", buf.String())
	}
}
