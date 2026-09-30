package tunnel

// 方案 B（通道加密协商）服务端行为测试：协商决策矩阵（B.2）的每一行
// 至少一条用例——成功全链路 / 旧客户端明文放行 WARN 限频 / require 拒绝 /
// 已加密传输放行（组合规则）/ 握手破坏绝不回落 / psk 错配双端失败。

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
	"mole/shared/noisechan"
	"mole/shared/proto"
)

// encTestToken 测试桩持有的 token 明文（proof 与 psk 推导共用）
const encTestToken = "mat_encchanneltest000000000000"

// tokenPSK 返回 sha256(token) 的 32 字节原始值（双端 psk 推导公式）
func tokenPSK(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// clientProof 按客户端公式计算 proof hex（算法对齐 client dialer.go:245-248：
// h=sha256(token); proof=hex(HMAC-SHA256(key=h, msg=challenge))）
func clientProof(token string, challenge []byte) string {
	h := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, h[:])
	mac.Write(challenge)
	return hex.EncodeToString(mac.Sum(nil))
}

// pskAuthenticator 固定 token 的认证桩：proof 路径按客户端公式验算，命中
// 返回 grant + psk=sha256(token)；明文 token 路径直接比对（复刻 service 层语义）
type pskAuthenticator struct {
	token string
}

func (a *pskAuthenticator) AuthenticateNodeToken(_ context.Context, rawToken string) (*core.NodeAccessGrant, []byte, error) {
	if rawToken != a.token {
		return nil, nil, errors.New("invalid token")
	}
	return &core.NodeAccessGrant{UserID: "user-enc"}, tokenPSK(rawToken), nil
}

func (a *pskAuthenticator) AuthenticateNodeProof(_ context.Context, proofHex string, challenge []byte) (*core.NodeAccessGrant, []byte, error) {
	want, err := hex.DecodeString(proofHex)
	if err != nil || len(want) != sha256.Size {
		return nil, nil, errors.New("invalid token")
	}
	h := sha256.Sum256([]byte(a.token))
	mac := hmac.New(sha256.New, h[:])
	mac.Write(challenge)
	if !hmac.Equal(mac.Sum(nil), want) {
		return nil, nil, errors.New("invalid token")
	}
	return &core.NodeAccessGrant{UserID: "user-enc"}, tokenPSK(a.token), nil
}

// syncLogBuf 并发安全的日志缓冲：服务端 goroutine 异步写、测试侧轮询读
type syncLogBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs 将默认 slog 替换为写同步缓冲的 text handler，测试结束还原
func captureLogs(t *testing.T) *syncLogBuf {
	t.Helper()
	b := &syncLogBuf{}
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(b, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return b
}

// pollLogContains 轮询等待日志出现子串（服务端 goroutine 异步写日志，
// 读到对端响应/EOF 不保证日志已落盘，必须轮询而非立即断言）
func pollLogContains(t *testing.T, logs *syncLogBuf, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if strings.Contains(logs.String(), substr) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log must contain %q within %v, got:\n%s", substr, timeout, logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// freePort 探测一个空闲 TCP 端口（探测后释放，存在微小复用窗口，测试可接受）
func freePort(t *testing.T) int {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	return port
}

// startEncTestServer 在 127.0.0.1 起 TCP 控制服务，注入认证桩与通道加密开关
func startEncTestServer(t *testing.T, enabled, require bool) (*ControlServer, string) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	nodeMgr := node.NewShardedNodeManager(4)
	cs := NewControlServer(addr, NewTCPTransport(nil), nodeMgr, "unused-global-token", nil)
	cs.SetAuthenticator(&pskAuthenticator{token: encTestToken})
	cs.SetChannelEncryption(enabled, require)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = cs.Start(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return cs, addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("control server did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dialEncClient 拨号并完成 challenge/auth 行交换，返回连接、穿针 reader 与原始应答行
func dialEncClient(t *testing.T, addr, token string, enc int) (net.Conn, *bufio.Reader, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	challenge := make([]byte, 32)
	if _, err := io.ReadFull(conn, challenge); err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	authMsg, err := json.Marshal(proto.NodeAuthLine{Proof: clientProof(token, challenge), Enc: enc})
	if err != nil {
		t.Fatalf("marshal auth line: %v", err)
	}
	if _, err := conn.Write(append(authMsg, '\n')); err != nil {
		t.Fatalf("write auth line: %v", err)
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read auth response: %v", err)
	}
	conn.SetReadDeadline(time.Time{})
	return conn, reader, line
}

// runAuthHandshakeOnTransport 在 net.Pipe 上以指定 transportName 直调
// handleConnection（包内可直调），完成认证交换并返回响应与 challenge
func runAuthHandshakeOnTransport(t *testing.T, cs *ControlServer, transportName string, enc int) (ControlResponse, []byte) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	go cs.handleConnection(context.Background(), server, transportName)

	challenge := make([]byte, 32)
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(client, challenge); err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	authMsg, err := json.Marshal(proto.NodeAuthLine{Proof: clientProof(encTestToken, challenge), Enc: enc})
	if err != nil {
		t.Fatalf("marshal auth line: %v", err)
	}
	if _, err := client.Write(append(authMsg, '\n')); err != nil {
		t.Fatalf("write auth line: %v", err)
	}
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read auth response: %v", err)
	}
	var resp ControlResponse
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &resp); err != nil {
		t.Fatalf("parse response %q: %v", line, err)
	}
	return resp, challenge
}

// authLimiterEntries 直查 SEC-13 限流器计数表条目数（持有锁，测试专用）
func authLimiterEntries(t *testing.T, cs *ControlServer) int {
	t.Helper()
	cs.authLimiter.mu.Lock()
	defer cs.authLimiter.mu.Unlock()
	return len(cs.authLimiter.entries)
}

// parseAuthResp 解析服务端认证应答行
func parseAuthResp(t *testing.T, line string) ControlResponse {
	t.Helper()
	var resp ControlResponse
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &resp); err != nil {
		t.Fatalf("parse response %q: %v", line, err)
	}
	return resp
}

// ---------------------------------------------------------------------------
// 协商成功全链路：ok 带 enc 能力宣告 → Noise XXpsk2 升级 → smux → ping/pong
// ---------------------------------------------------------------------------

func TestChannelEncryption_NegotiationEndToEnd(t *testing.T) {
	logs := captureLogs(t)
	_, addr := startEncTestServer(t, true, false)

	conn, reader, line := dialEncClient(t, addr, encTestToken, proto.EncProtocolV1)
	defer conn.Close()

	resp := parseAuthResp(t, line)
	if resp.Cmd != proto.RespOK {
		t.Fatalf("expected ok, got %+v (line %q)", resp, line)
	}
	if resp.Enc == nil || resp.Enc.V != proto.EncProtocolV1 {
		t.Fatalf("ok response must carry enc capability v=1, got line %q", line)
	}
	if !strings.Contains(line, `"enc":{"v":1}`) {
		t.Fatalf("raw line must contain \"enc\":{\"v\":1}, got %q", line)
	}

	// 客户端发起方升级（psk = sha256(token)，reader 穿针为认证行读取的同一 reader）
	noiseConn, err := noisechan.UpgradeInitiator(conn, reader, tokenPSK(encTestToken), 10*time.Second)
	if err != nil {
		t.Fatalf("upgrade initiator: %v", err)
	}

	// smux 架在加密层上，ping→pong 证明控制会话可用。
	// Version 等参数对齐 client 侧 dialer 的会话配置（服务端 Version: 2，
	// nil 默认 Version 1 会导致握手不匹配）
	sess, err := smux.Client(noiseConn, &smux.Config{
		Version:           2,
		KeepAliveDisabled: false,
		KeepAliveInterval: 30 * time.Second,
		KeepAliveTimeout:  90 * time.Second,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  32 * 1024 * 1024,
		MaxStreamBuffer:   4 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("smux client: %v", err)
	}
	defer sess.Close()
	stream, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Write([]byte(`{"cmd":"ping","ts":42}` + "\n")); err != nil {
		t.Fatalf("send ping: %v", err)
	}
	pongLine, err := bufio.NewReader(stream).ReadString('\n')
	if err != nil {
		t.Fatalf("read pong: %v", err)
	}
	var pong ControlResponse
	if err := json.Unmarshal([]byte(strings.TrimRight(pongLine, "\n")), &pong); err != nil {
		t.Fatalf("parse pong %q: %v", pongLine, err)
	}
	if pong.Cmd != proto.RespPong || pong.Ts != 42 {
		t.Fatalf("expected pong ts=42, got %+v", pong)
	}

	// 认证日志必须带 enc=true（收口期观测点）
	pollLogContains(t, logs, "Node authenticated", 3*time.Second)
	if !strings.Contains(logs.String(), "enc=true") {
		t.Fatalf("Node authenticated log must carry enc=true, logs:\n%s", logs.String())
	}
}

// ---------------------------------------------------------------------------
// enabled=false + enc:1 → ok 无 enc 键（新客户端自动回落明文的信号）
// ---------------------------------------------------------------------------

func TestChannelEncryption_DisabledServerOkWithoutEnc(t *testing.T) {
	_, addr := startEncTestServer(t, false, false)

	conn, _, line := dialEncClient(t, addr, encTestToken, proto.EncProtocolV1)
	defer conn.Close()

	resp := parseAuthResp(t, line)
	if resp.Cmd != proto.RespOK {
		t.Fatalf("expected ok, got %+v (line %q)", resp, line)
	}
	if resp.Enc != nil {
		t.Fatalf("disabled server must not carry enc, got %+v (line %q)", resp.Enc, line)
	}
	if strings.Contains(line, "enc") {
		t.Fatalf("enc key must be entirely absent from ok response, got %q", line)
	}
}

// ---------------------------------------------------------------------------
// enabled + 无 enc 客户端 → ok 无 enc + 明文放行 WARN 同 IP 限频（每 5 分钟一条）
// ---------------------------------------------------------------------------

func TestChannelEncryption_PlaintextAllowedWarnRateLimited(t *testing.T) {
	logs := captureLogs(t)
	_, addr := startEncTestServer(t, true, false)

	// 同 IP（127.0.0.1）两次明文连接
	for i := 0; i < 2; i++ {
		conn, _, line := dialEncClient(t, addr, encTestToken, 0)
		resp := parseAuthResp(t, line)
		if resp.Cmd != proto.RespOK {
			t.Fatalf("connection #%d: plaintext client must be allowed, got %+v", i+1, resp)
		}
		if resp.Enc != nil || strings.Contains(line, "enc") {
			t.Fatalf("connection #%d: no enc expected for plaintext client, got %q", i+1, line)
		}
		conn.Close()
	}

	// 等两次认证收尾日志都落盘后再计数（warn 先于 Node authenticated 写出）
	pollLogContains(t, logs, "Node authenticated", 3*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for {
		l := logs.String()
		if strings.Count(l, "Node authenticated") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("both connections must complete, logs:\n%s", l)
		}
		time.Sleep(10 * time.Millisecond)
	}

	count := strings.Count(logs.String(), "Plaintext control connection allowed")
	if count != 1 {
		t.Fatalf("plaintext WARN must fire exactly once for same IP within 5min, got %d, logs:\n%s", count, logs.String())
	}
}

// ---------------------------------------------------------------------------
// require + 无 enc → err 应答含关键词 + conn 关闭 + limiter 未计数
// ---------------------------------------------------------------------------

func TestChannelEncryption_RequireRejectsPlaintextClient(t *testing.T) {
	logs := captureLogs(t)
	cs, addr := startEncTestServer(t, true, true)

	conn, _, line := dialEncClient(t, addr, encTestToken, 0)
	defer conn.Close()

	resp := parseAuthResp(t, line)
	if resp.Cmd != proto.RespErr {
		t.Fatalf("require must reject plaintext client, got %+v (line %q)", resp, line)
	}
	if !strings.Contains(resp.Msg, "channel encryption required") {
		t.Fatalf("error message must contain the keyword, got %q", resp.Msg)
	}

	// 服务端关闭连接：后续读取得到 EOF
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 16)); err != io.EOF {
		t.Fatalf("server must close conn after rejection, got %v", err)
	}

	// require 拒绝不计入 SEC-13 限流（被拒者是合法节点，只是版本旧）
	if n := authLimiterEntries(t, cs); n != 0 {
		t.Fatalf("require rejection must not touch auth limiter, entries=%d", n)
	}

	// 专属日志关键词（收口期观测点，对应 R2 Legacy node auth rejected 角色）
	pollLogContains(t, logs, "channel encryption required", 3*time.Second)
}

// ---------------------------------------------------------------------------
// require + 已加密传输（tls/wss）→ 放行（组合规则 2）；即便客户端误发 enc
// 也不应答升级（组合规则 1：单连接单加密）
// ---------------------------------------------------------------------------

func TestChannelEncryption_RequireAllowsEncryptedTransport(t *testing.T) {
	for _, transportName := range []string{"tls", "wss"} {
		t.Run(transportName+" 无 enc 客户端放行", func(t *testing.T) {
			nodeMgr := node.NewShardedNodeManager(4)
			cs := NewControlServer("", nil, nodeMgr, "unused", nil)
			cs.SetAuthenticator(&pskAuthenticator{token: encTestToken})
			cs.SetChannelEncryption(true, true)

			resp, _ := runAuthHandshakeOnTransport(t, cs, transportName, 0)
			if resp.Cmd != proto.RespOK {
				t.Fatalf("require must allow already-encrypted transport %q, got %+v", transportName, resp)
			}
			if resp.Enc != nil {
				t.Fatalf("encrypted transport must not negotiate noise upgrade (single encryption per conn), got %+v", resp.Enc)
			}
		})
		t.Run(transportName+" 误发 enc 也不应答升级", func(t *testing.T) {
			nodeMgr := node.NewShardedNodeManager(4)
			cs := NewControlServer("", nil, nodeMgr, "unused", nil)
			cs.SetAuthenticator(&pskAuthenticator{token: encTestToken})
			cs.SetChannelEncryption(true, true)

			resp, _ := runAuthHandshakeOnTransport(t, cs, transportName, proto.EncProtocolV1)
			if resp.Cmd != proto.RespOK {
				t.Fatalf("require must allow encrypted transport even with enc bit, got %+v", resp)
			}
			if resp.Enc != nil {
				t.Fatalf("must not answer enc capability on encrypted transport (rule 1), got %+v", resp.Enc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// enc:1 + enabled + 握手破坏（认证行后发垃圾帧）→ 服务端断开 + WARN + 不计 authFail
// ---------------------------------------------------------------------------

func TestChannelEncryption_HandshakeCorruptionHardFails(t *testing.T) {
	logs := captureLogs(t)
	cs, addr := startEncTestServer(t, true, false)

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 手动走认证交换：challenge → auth 行(enc:1) → ok（必须先宣告 enc 能力）
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	challenge := make([]byte, 32)
	if _, err := io.ReadFull(conn, challenge); err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	authMsg, err := json.Marshal(proto.NodeAuthLine{Proof: clientProof(encTestToken, challenge), Enc: proto.EncProtocolV1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := conn.Write(append(authMsg, '\n')); err != nil {
		t.Fatalf("write auth line: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read ok response: %v", err)
	}
	if !strings.Contains(line, `"enc":{"v":1}`) {
		t.Fatalf("server must announce enc capability before handshake, got %q", line)
	}

	// 垃圾帧：长度前缀声明 5 字节密文体（低于 16 字节最小合法帧）→ 帧流错位立即失败
	if _, err := conn.Write([]byte{0x00, 0x05, 1, 2, 3, 4, 5}); err != nil {
		t.Fatalf("write garbage frame: %v", err)
	}

	// 服务端断开：读到 EOF
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 16)); err != io.EOF {
		t.Fatalf("server must disconnect on corrupted handshake, got %v", err)
	}

	// WARN（服务端已宣告能力后失败 = 攻击/损坏，绝不回落）
	pollLogContains(t, logs, "Channel encryption handshake failed", 3*time.Second)

	// 握手失败不计入 SEC-13 限流（客户端已通过 proof 认证，计入会在攻击下误锁合法节点）
	if n := authLimiterEntries(t, cs); n != 0 {
		t.Fatalf("handshake failure must not count as auth failure, entries=%d", n)
	}
}

// ---------------------------------------------------------------------------
// psk 错配端到端：客户端 proof 用正确 token（认证通过）但升级用错 psk → 双端失败
// ---------------------------------------------------------------------------

func TestChannelEncryption_PSKMismatchBothEndsFail(t *testing.T) {
	logs := captureLogs(t)
	cs, addr := startEncTestServer(t, true, false)
	_ = cs

	conn, reader, line := dialEncClient(t, addr, encTestToken, proto.EncProtocolV1)
	defer conn.Close()
	if !strings.Contains(line, `"enc":{"v":1}`) {
		t.Fatalf("server must announce enc capability, got %q", line)
	}

	// 客户端用错误 token 的 psk 升级：msg2 解密失败 → 硬失败断开
	wrongPSK := tokenPSK("totally-wrong-token")
	_, err := noisechan.UpgradeInitiator(conn, reader, wrongPSK, 10*time.Second)
	if err == nil {
		t.Fatal("client with wrong psk must fail the handshake")
	}
	if !errors.Is(err, noisechan.ErrHandshake) {
		t.Fatalf("client error must wrap ErrHandshake, got %v", err)
	}

	// 服务端同样失败（客户端 msg2 解密失败断开 → 服务端 msg3 读取失败）→ WARN
	pollLogContains(t, logs, "Channel encryption handshake failed", 5*time.Second)

	// 不计入 authFail
	if n := authLimiterEntries(t, cs); n != 0 {
		t.Fatalf("psk mismatch must not count as auth failure, entries=%d", n)
	}
}
