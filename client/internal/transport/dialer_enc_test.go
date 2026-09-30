package transport

// 方案 B（通道加密协商，自适应回落版）客户端决策矩阵测试：
//   1. enc=on × 服务端宣告 enc → Noise 升级成功全链路（smux 架在加密层上）
//   2. ok 缺 enc（旧服务端）→ 回落明文；同地址两次 Connect 只 WARN 一条
//   3. ok 带 enc 后握手失败 → 硬失败绝不回落（专属错误串）
//   4. enc=off → 认证行无 enc 键；收到 enc 应答也忽略，明文成功无 WARN
//   5. encMode=on + UseTLS → 认证行无 enc 键（单连接单加密），明文成功无 WARN
//
// 捕获全局 log 的用例不得 t.Parallel()（captureTransportLog 改全局 log 输出）。

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"mole/shared/noisechan"
	"mole/shared/proto"
)

// captureTransportLog 捕获全局 log 输出（WARN-once 断言用），测试结束恢复。
// 全局状态捕获不可与并行测试共存——调用方不得 t.Parallel()。
func captureTransportLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	oldW, oldF := log.Writer(), log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
	})
	return &buf
}

// startEncTestServer 起多连接假服务端（支持同地址重连）：每条连接写 32B
// challenge、读认证行后交由 handle。br 即读认证行的 bufio.Reader——假服务端
// 的 Noise 升级必须穿针复用它（与真实服务端 control.go 的约束一致）。
func startEncTestServer(t *testing.T, handle func(conn net.Conn, authLine []byte, br *bufio.Reader)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, err := conn.Write(make([]byte, 32)); err != nil { // challenge
					return
				}
				br := bufio.NewReader(conn)
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				handle(conn, []byte(line), br)
			}()
		}
	}()
	return ln.Addr().String()
}

// okRespLine 构造认证应答行：enc 非 nil 时即「新服务端宣告能力」形态，
// nil 时为旧服务端形态（无 enc 字段）。
func okRespLine(msg string, enc *proto.EncCapability) []byte {
	resp, err := json.Marshal(proto.ControlResponse{Cmd: proto.RespOK, Msg: msg, Enc: enc})
	if err != nil {
		panic(err)
	}
	return append(resp, '\n')
}

// serveSmuxEcho 在 conn 上挂 smux 服务端：accept 流并对含 "ping" 的控制行
// 回单行 pong（证明 session 之上的控制流完整可用）。
func serveSmuxEcho(conn net.Conn) {
	sess, err := smux.Server(conn, testSmuxConfig())
	if err != nil {
		return
	}
	for {
		s, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go func(s *smux.Stream) {
			defer s.Close()
			s.SetReadDeadline(time.Now().Add(5 * time.Second))
			line, err := bufio.NewReader(s).ReadString('\n')
			if err != nil {
				return
			}
			if strings.Contains(line, `"ping"`) {
				s.Write([]byte(`{"cmd":"pong"}` + "\n"))
			}
		}(s)
	}
}

// encDialer 构造拨到假服务端的 DialFunc（走真实 TCP 路径）
func encDialer() DialFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		d := &net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, "tcp", addr)
	}
}

// newEncSessionManager 构造指向 addr 的会话管理器（测试用 smux 配置）
func newEncSessionManager(mode string, useTLS bool) *SessionManager {
	sm := NewSessionManager(encDialer())
	sm.SetChannelEncryption(mode, useTLS)
	sm.SetSmuxOverride(testSmuxConfig())
	return sm
}

// sessionPingPong 在已建立的会话上收发一条控制行（ping→pong）
func sessionPingPong(t *testing.T, sm *SessionManager) {
	t.Helper()
	sess := sm.Session()
	if sess == nil {
		t.Fatalf("no session after connect")
	}
	stream, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Write([]byte(`{"cmd":"ping"}` + "\n")); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	line, err := bufio.NewReader(stream).ReadString('\n')
	if err != nil {
		t.Fatalf("read pong: %v", err)
	}
	if !strings.Contains(line, `"pong"`) {
		t.Fatalf("pong line = %q", line)
	}
}

// 1. 决策矩阵主路径：enc=on × 服务端宣告 enc → Noise 升级全链路成功。
// 认证行断言：携带 "enc":1（数字形式）且 proof 值与独立复算一致（proof
// 计算不变基准）；会话收发一条控制行证明 smux 架在加密层上完整可用。
func TestConnect_EncNegotiationUpgradesToNoise(t *testing.T) {
	const token = "enc-fullchain-token"

	addr := startEncTestServer(t, func(conn net.Conn, line []byte, br *bufio.Reader) {
		var fields struct {
			Proof string          `json:"proof"`
			Enc   json.RawMessage `json:"enc"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(line), &fields); err != nil {
			t.Errorf("auth line is not JSON: %v (line=%q)", err, line)
			return
		}
		// enc 必须是数字 1（不是字符串 "1"——map[string]string 形态的错误输出）
		if string(fields.Enc) != "1" {
			t.Errorf("auth line enc = %s, want numeric 1 (line=%q)", fields.Enc, line)
		}
		if want := wantProof(token, make([]byte, 32)); fields.Proof != want {
			t.Errorf("proof = %q, want %q（proof 计算不变）", fields.Proof, want)
		}
		if _, err := conn.Write(okRespLine("authenticated", &proto.EncCapability{V: proto.EncProtocolV1})); err != nil {
			return
		}
		// 假服务端 Noise 应答方升级：psk = sha256(token)，reader 穿针
		psk := sha256.Sum256([]byte(token))
		noiseConn, err := noisechan.UpgradeResponder(conn, br, psk[:], 5*time.Second)
		if err != nil {
			t.Errorf("fake server noise upgrade: %v", err)
			return
		}
		serveSmuxEcho(noiseConn)
	})

	sm := newEncSessionManager(EncModeOn, false)
	defer sm.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sm.Connect(ctx, addr, token); err != nil {
		t.Fatalf("connect with enc negotiation: %v", err)
	}
	sessionPingPong(t, sm)
}

// 2. 回落：ok 缺 enc 字段（旧服务端）→ Connect 成功明文（bufferedConn 路径）；
// 同地址两次 Connect 只 WARN 一条（每服务端地址每进程一次，防 5s 重连刷屏）
func TestConnect_PlaintextFallbackWarnsOncePerAddr(t *testing.T) {
	buf := captureTransportLog(t)

	addr := startEncTestServer(t, func(conn net.Conn, line []byte, br *bufio.Reader) {
		if !bytes.Contains(line, []byte(`"enc":1`)) {
			t.Errorf("enc=on 时认证行必须携带 \"enc\":1, got %q", line)
		}
		// ok 无 enc 字段 = 旧服务端信号
		if _, err := conn.Write(okRespLine("authenticated", nil)); err != nil {
			return
		}
		// 明文 smux（服务端侧同样穿针 bufferedConn，防 reader 预吞）
		serveSmuxEcho(&bufferedConn{Conn: conn, reader: br})
	})

	sm := newEncSessionManager(EncModeOn, false)
	defer sm.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sm.Connect(ctx, addr, "tok"); err != nil {
		t.Fatalf("first connect (plaintext fallback): %v", err)
	}
	sessionPingPong(t, sm)

	sm.Disconnect()
	if err := sm.Connect(ctx, addr, "tok"); err != nil {
		t.Fatalf("second connect: %v", err)
	}

	if n := strings.Count(buf.String(), "plaintext fallback: server"); n != 1 {
		t.Fatalf("同地址两次 Connect 必须 WARN 恰好一条，got %d:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), addr) {
		t.Fatalf("WARN 必须含服务端地址:\n%s", buf.String())
	}
}

// 3. 安全边界：ok 带 enc 宣告后握手阶段失败（此处：破坏握手帧的垃圾字节）
// → Connect 硬失败返回错误、绝不回落明文连上；错误文案含专属错误串且可被
// errors.Is(noisechan.ErrHandshake) 识别
func TestConnect_EncHandshakeFailureHardFailNoFallback(t *testing.T) {
	addr := startEncTestServer(t, func(conn net.Conn, line []byte, br *bufio.Reader) {
		// 宣告 enc 能力后写垃圾字节（模拟破坏 msg2 的中间人/损坏链路）
		if _, err := conn.Write(okRespLine("authenticated", &proto.EncCapability{V: proto.EncProtocolV1})); err != nil {
			return
		}
		conn.Write(bytes.Repeat([]byte{'X'}, 64))
	})

	sm := newEncSessionManager(EncModeOn, false)
	defer sm.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := sm.Connect(ctx, addr, "tok")
	if err == nil {
		t.Fatalf("握手失败必须硬失败（Connect 返回错误），绝不能回落明文连上")
	}
	if !strings.Contains(err.Error(), "channel encryption handshake failed") {
		t.Fatalf("错误文案必须含专属错误串 \"channel encryption handshake failed\", got %v", err)
	}
	if !errors.Is(err, noisechan.ErrHandshake) {
		t.Errorf("错误必须可被 errors.Is(noisechan.ErrHandshake) 识别, got %v", err)
	}
	if sm.Session() != nil {
		t.Fatalf("不得发布会话（明文幽灵会话）")
	}
}

// 4. enc=off（调试杆）：认证行无 enc 键；服务端误答 enc 也忽略 → 明文成功；
// 显式明文不 WARN
func TestConnect_EncOffOmitsEncBitAndIgnoresOffer(t *testing.T) {
	buf := captureTransportLog(t)

	addr := startEncTestServer(t, func(conn net.Conn, line []byte, br *bufio.Reader) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(bytes.TrimSpace(line), &fields); err != nil {
			t.Errorf("auth line is not JSON: %v (line=%q)", err, line)
			return
		}
		if _, ok := fields["enc"]; ok {
			t.Errorf("enc=off 时认证行不得携带 enc 键: %q", line)
		}
		// 服务端误答 enc（enc=off 客户端必须忽略，走明文）
		if _, err := conn.Write(okRespLine("authenticated", &proto.EncCapability{V: proto.EncProtocolV1})); err != nil {
			return
		}
		serveSmuxEcho(&bufferedConn{Conn: conn, reader: br})
	})

	sm := newEncSessionManager(EncModeOff, false)
	defer sm.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sm.Connect(ctx, addr, "tok"); err != nil {
		t.Fatalf("enc=off connect: %v", err)
	}
	sessionPingPong(t, sm)

	if strings.Contains(buf.String(), "plaintext fallback") {
		t.Fatalf("enc=off 显式明文不应 WARN:\n%s", buf.String())
	}
}

// 5. UseTLS 等价路径（encMode=on + useTLS=true）：认证行无 enc 键——单连接
// 单加密规则（防 TCP→TLS→Noise→smux 双重加密）。本地不搭真实 TLS 监听：
// useTLS 在 dialer 内只驱动「是否发 enc」判定，dial 为注入的明文管道，故在
// 线级直接断言该判定即可等价覆盖；UseTLS 回落明文不 WARN（传输已加密）。
func TestConnect_UseTLSOmitsEncBitNoWarn(t *testing.T) {
	buf := captureTransportLog(t)

	addr := startEncTestServer(t, func(conn net.Conn, line []byte, br *bufio.Reader) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(bytes.TrimSpace(line), &fields); err != nil {
			t.Errorf("auth line is not JSON: %v (line=%q)", err, line)
			return
		}
		if _, ok := fields["enc"]; ok {
			t.Errorf("UseTLS 时认证行不得携带 enc 键（单连接单加密）: %q", line)
		}
		// TLS 传输的服务端按组合规则不答 enc
		if _, err := conn.Write(okRespLine("authenticated", nil)); err != nil {
			return
		}
		serveSmuxEcho(&bufferedConn{Conn: conn, reader: br})
	})

	sm := newEncSessionManager(EncModeOn, true)
	defer sm.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sm.Connect(ctx, addr, "tok"); err != nil {
		t.Fatalf("useTLS connect: %v", err)
	}
	sessionPingPong(t, sm)

	if strings.Contains(buf.String(), "plaintext fallback") {
		t.Fatalf("UseTLS 不 WARN（传输已加密）:\n%s", buf.String())
	}
}
