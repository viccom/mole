package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

// ---------------------------------------------------------------------------
// connAuthLimiter 单元测试（假时钟）
// ---------------------------------------------------------------------------

func TestConnAuthLimiterLockAndUnlock(t *testing.T) {
	base := time.Now()
	cur := base
	l := newConnAuthLimiter()
	l.now = func() time.Time { return cur }

	const ip = "10.0.0.9"
	for i := 0; i < connAuthMaxFailures; i++ {
		if !l.Allowed(ip) {
			t.Fatalf("failure #%d must still be allowed", i+1)
		}
		l.RecordFailure(ip)
	}
	if l.Allowed(ip) {
		t.Fatal("must be locked after threshold failures")
	}

	// 锁定期内不解锁
	cur = cur.Add(connAuthLockDuration - time.Second)
	if l.Allowed(ip) {
		t.Fatal("must stay locked within lock duration")
	}

	// 锁定期满：解锁且计数从零开始（单次失败不再立即锁）
	cur = cur.Add(time.Second)
	if !l.Allowed(ip) {
		t.Fatal("lock must expire after lock duration")
	}
	l.RecordFailure(ip)
	if !l.Allowed(ip) {
		t.Fatal("single failure after unlock must not relock")
	}
}

func TestConnAuthLimiterSlidingWindow(t *testing.T) {
	base := time.Now()
	cur := base
	l := newConnAuthLimiter()
	l.now = func() time.Time { return cur }

	const ip = "10.0.0.9"
	// 前 5 次失败随后滑出窗口
	for i := 0; i < 5; i++ {
		l.RecordFailure(ip)
	}
	cur = cur.Add(connAuthWindow + time.Minute)
	// 窗口外再 9 次（合计 14 次但窗口内仅 9 次）：不锁
	for i := 0; i < connAuthMaxFailures-1; i++ {
		l.RecordFailure(ip)
	}
	if !l.Allowed(ip) {
		t.Fatal("stale failures must not count toward lock")
	}
	// 窗口内第 10 次：锁
	l.RecordFailure(ip)
	if l.Allowed(ip) {
		t.Fatal("10 failures within window must lock")
	}
}

func TestConnAuthLimiterPerIPAndSuccessClears(t *testing.T) {
	l := newConnAuthLimiter()

	const a, b = "10.0.0.1", "10.0.0.2"
	for i := 0; i < connAuthMaxFailures; i++ {
		l.RecordFailure(a)
	}
	if l.Allowed(a) {
		t.Fatal("IP a must be locked")
	}
	if !l.Allowed(b) {
		t.Fatal("IP b must not be affected by IP a's failures")
	}

	// 成功清零（对齐 login_limiter 语义：成功即清）
	l.RecordSuccess(a)
	if !l.Allowed(a) {
		t.Fatal("successful auth must clear failure count")
	}
}

// ---------------------------------------------------------------------------
// handleConnection 集成：认证失败限速（SEC-13）
// ---------------------------------------------------------------------------

// selectiveAuth 只接受指定 token 的认证桩
type selectiveAuth struct{ accept string }

func (s selectiveAuth) AuthenticateNodeToken(_ context.Context, raw string) (*core.NodeAccessGrant, error) {
	if raw == s.accept {
		return &core.NodeAccessGrant{UserID: "user-x"}, nil
	}
	return nil, errors.New("no match")
}

func (s selectiveAuth) AuthenticateNodeProof(context.Context, string, []byte) (*core.NodeAccessGrant, error) {
	return nil, errors.New("no match")
}

// addrConn 覆盖 RemoteAddr 的连接包装（net.Pipe 的地址无 IP 语义）
type addrConn struct {
	net.Conn
	remote string
}

func (c *addrConn) RemoteAddr() net.Addr { return addrNetAddr(c.remote) }

type addrNetAddr string

func (a addrNetAddr) Network() string { return "tcp" }
func (a addrNetAddr) String() string  { return string(a) }

// attemptAuth 从指定远端地址执行一次认证握手。服务端在发送 challenge 前
// 断开（限速命中）时返回 dropped=true
func attemptAuth(t *testing.T, cs *ControlServer, remote, authLine string) (resp ControlResponse, dropped bool) {
	t.Helper()
	client, server := net.Pipe()
	server = &addrConn{Conn: server, remote: remote}
	defer client.Close()
	go cs.handleConnection(context.Background(), server, "tcp")

	challenge := make([]byte, 32)
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(client, challenge); err != nil {
		return ControlResponse{}, true
	}
	if _, err := client.Write([]byte(authLine + "\n")); err != nil {
		t.Fatalf("write auth line: %v", err)
	}
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &resp); err != nil {
		t.Fatalf("parse response %q: %v", line, err)
	}
	return resp, false
}

func newAuthLimiterTestServer() *ControlServer {
	cs := NewControlServer("", nil, node.NewShardedNodeManager(4), "global-token", nil)
	cs.SetAuthenticator(selectiveAuth{accept: "good-token"})
	return cs
}

// 同 IP 10 次认证失败后，第 11 次连接在认证前被断开；其他 IP 不受影响
func TestHandleConnectionAuthLockout(t *testing.T) {
	cs := newAuthLimiterTestServer()

	for i := 0; i < connAuthMaxFailures; i++ {
		resp, dropped := attemptAuth(t, cs, "10.0.0.1:1000", `{"token":"bad"}`)
		if dropped {
			t.Fatalf("attempt #%d must complete the handshake, not be dropped", i+1)
		}
		if resp.Cmd != "err" || resp.Msg != "invalid token" {
			t.Fatalf("attempt #%d: expected err/invalid token, got %+v", i+1, resp)
		}
	}

	// 第 11 次：即使携带正确 token，也在认证前被断开
	if _, dropped := attemptAuth(t, cs, "10.0.0.1:1000", `{"token":"good-token"}`); !dropped {
		t.Fatal("11th connection from locked IP must be dropped before auth")
	}

	// 其他 IP 不受影响
	resp, dropped := attemptAuth(t, cs, "10.0.0.2:2000", `{"token":"good-token"}`)
	if dropped {
		t.Fatal("different IP must not be dropped")
	}
	if resp.Cmd != "ok" {
		t.Fatalf("different IP must authenticate, got %+v", resp)
	}
}

// 成功认证清零失败计数：9 次失败 + 1 次成功后，后续失败不会立即触发锁定
func TestHandleConnectionAuthSuccessClears(t *testing.T) {
	cs := newAuthLimiterTestServer()

	for i := 0; i < connAuthMaxFailures-1; i++ {
		resp, dropped := attemptAuth(t, cs, "10.0.0.3:3000", `{"token":"bad"}`)
		if dropped || resp.Cmd != "err" {
			t.Fatalf("failure #%d must complete with err, dropped=%v resp=%+v", i+1, dropped, resp)
		}
	}
	resp, dropped := attemptAuth(t, cs, "10.0.0.3:3000", `{"token":"good-token"}`)
	if dropped || resp.Cmd != "ok" {
		t.Fatalf("success must pass, dropped=%v resp=%+v", dropped, resp)
	}

	// 成功已清零：再来一次失败仍能完成握手（而非被锁断开）
	resp, dropped = attemptAuth(t, cs, "10.0.0.3:3000", `{"token":"bad"}`)
	if dropped {
		t.Fatal("failure after successful auth must not be dropped (count was cleared)")
	}
	if resp.Cmd != "err" {
		t.Fatalf("expected err, got %+v", resp)
	}
}
