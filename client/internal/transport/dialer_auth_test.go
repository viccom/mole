package transport

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// startFakeAuthServer 起本地 net.Listener 假服务端：accept 一条连接交由 handle 处理
func startFakeAuthServer(t *testing.T, handle func(conn net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn)
	}()
	return ln.Addr().String()
}

// wantProof 独立复算协议期望值：HMAC-SHA256(key=sha256(token), msg=challenge) 的 hex
func wantProof(token string, challenge []byte) string {
	h := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, h[:])
	mac.Write(challenge)
	return hex.EncodeToString(mac.Sum(nil))
}

// dialFakeAuth 拨到假服务端并执行 authenticate（走真实 TCP 路径；
// requestEnc=false 保持 proof-only 输出，即现行基线形态）
func dialFakeAuth(t *testing.T, addr, token string) error {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial fake server: %v", err)
	}
	defer conn.Close()
	_, _, err = authenticate(conn, token, false)
	return err
}

// R1 认证格式守卫：挑战应答必须只发 proof 字段
// （HMAC-SHA256(key=sha256(token), msg=challenge) 的 hex），严禁明文 token 上线
func TestAuthenticate_SendsProofNotToken(t *testing.T) {
	const testToken = "secret-token-for-test"
	challenge := make([]byte, 32)
	for i := range challenge {
		challenge[i] = byte(i)
	}

	srvAddr := startFakeAuthServer(t, func(conn net.Conn) {
		if _, err := conn.Write(challenge); err != nil {
			return
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil {
			t.Errorf("server read auth line: %v", err)
			return
		}
		var fields map[string]string
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &fields); err != nil {
			t.Errorf("auth line is not a JSON object: %v (line=%q)", err, line)
			return
		}
		if _, ok := fields["token"]; ok {
			t.Errorf("auth line must not contain token field: %q", line)
		}
		if len(fields) != 1 {
			t.Errorf("auth line must contain exactly one field, got %d: %v", len(fields), fields)
			return
		}
		got, ok := fields["proof"]
		if !ok {
			t.Errorf("auth line missing proof field: %q", line)
			return
		}
		if want := wantProof(testToken, challenge); got != want {
			t.Errorf("proof = %q, want %q", got, want)
		}
		resp, _ := json.Marshal(map[string]string{"cmd": "ok"})
		conn.Write(append(resp, '\n'))
	})

	if err := dialFakeAuth(t, srvAddr, testToken); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
}

// 服务端回 {"cmd":"err"} 时 authenticate 必须返回错误
func TestAuthenticate_ServerErrReturnsError(t *testing.T) {
	const testToken = "secret-token-for-test"

	srvAddr := startFakeAuthServer(t, func(conn net.Conn) {
		if _, err := conn.Write(make([]byte, 32)); err != nil { // challenge
			return
		}
		if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil { // auth 行
			return
		}
		resp, _ := json.Marshal(map[string]string{"cmd": "err", "msg": "xxx"})
		conn.Write(append(resp, '\n'))
	})

	err := dialFakeAuth(t, srvAddr, testToken)
	if err == nil || !strings.Contains(err.Error(), "auth failed") {
		t.Fatalf("authenticate must fail with auth error, got %v", err)
	}
}

// F1 认证应答读取必须加上限：恶意/故障服务端在应答阶段灌入超限的无换行
// 数据时，authenticate 必须立即以超限错误返回，而不是无界累计到 10s
// deadline 兜底（裸连接未过 smux，无界读是内存放大点）
func TestAuthenticate_OversizeResponseRejected(t *testing.T) {
	const testToken = "secret-token-for-test"

	srvAddr := startFakeAuthServer(t, func(conn net.Conn) {
		if _, err := conn.Write(make([]byte, 32)); err != nil { // challenge
			return
		}
		if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil { // auth 行
			return
		}
		// 应答阶段：灌入 128KB 无换行垃圾（远超 64KB 上限）。单次大块写入：
		// 超限触发点（第 65537 字节）落在字节流中部，随 full-size 段即时
		// 到达——若拆成 64KB+1B 两次写，最后的 1 字节小段会被 Nagle
		// hold 住，超限触发被人为拖延（首版实现实测拖 2s 即此坑）。
		// 写完即返回触发 FIN：绿路径必须靠客户端读到超限字节自行报错，
		// 而不是等服务端关连接或 deadline 兜底
		if _, err := conn.Write(bytes.Repeat([]byte{'a'}, 128*1024)); err != nil {
			return
		}
	})

	start := time.Now()
	err := dialFakeAuth(t, srvAddr, testToken)
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("authenticate must fail with too-large error, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("超限应立即返回而不是吃满 deadline，耗时 %v", elapsed)
	}
}

// F1 边界：恰好 64KB（含换行）的应答行在上限内必须放行——上限只拦
// 「超限仍无换行」的无界累计，不截断合法边界。走完整 authenticate 路径
// 验证（超大但合法的 JSON 会因 cmd!=ok 报 auth failed，读侧不报超限即绿）
func TestAuthenticate_ExactLimitWithNewlineAccepted(t *testing.T) {
	const testToken = "secret-token-for-test"

	srvAddr := startFakeAuthServer(t, func(conn net.Conn) {
		if _, err := conn.Write(make([]byte, 32)); err != nil { // challenge
			return
		}
		if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil { // auth 行
			return
		}
		// 65535 字节 msg + JSON 包装 + 换行，整行恰好 64KB
		resp, _ := json.Marshal(map[string]string{"cmd": "err", "msg": strings.Repeat("m", 65535-len(`{"cmd":"err","msg":""}`))})
		line := append(resp, '\n')
		if len(line) != 64*1024 {
			t.Errorf("test fixture: line length = %d, want %d", len(line), 64*1024)
		}
		conn.Write(line)
	})

	err := dialFakeAuth(t, srvAddr, testToken)
	// 恰好 64KB 含换行：读侧必须放行，最终因 cmd!=ok 报 auth failed
	if err == nil || !strings.Contains(err.Error(), "auth failed") {
		t.Fatalf("exact-limit line must be accepted then rejected by cmd, got %v", err)
	}
}
