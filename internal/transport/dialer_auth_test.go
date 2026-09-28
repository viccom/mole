package transport

import (
	"bufio"
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

// dialFakeAuth 拨到假服务端并执行 authenticate（走真实 TCP 路径）
func dialFakeAuth(t *testing.T, addr, token string) error {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial fake server: %v", err)
	}
	defer conn.Close()
	_, err = authenticate(conn, token)
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
