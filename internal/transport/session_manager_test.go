package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/xtaci/smux"
)

// testSmuxConfig 与 SessionManager 默认配置同版本，避免两侧 smux 版本错配
func testSmuxConfig() *smux.Config {
	return &smux.Config{
		Version:           DefaultSmuxVersion,
		KeepAliveDisabled: true,
		MaxFrameSize:      SmuxMaxFrameSize,
		MaxReceiveBuffer:  4194304,
		MaxStreamBuffer:   65536,
	}
}

// serveAuthSide 模拟服务端：challenge-response 认证成功后挂起 smux 会话
func serveAuthSide(conn net.Conn) {
	defer conn.Close()
	if _, err := conn.Write(make([]byte, 32)); err != nil { // challenge
		return
	}
	br := bufio.NewReader(conn)
	if _, err := br.ReadString('\n'); err != nil { // auth 行
		return
	}
	resp, _ := json.Marshal(map[string]string{"cmd": "ok"})
	if _, err := conn.Write(append(resp, '\n')); err != nil {
		return
	}
	sess, err := smux.Server(conn, testSmuxConfig())
	if err != nil {
		return
	}
	for {
		s, err := sess.Accept()
		if err != nil {
			return
		}
		s.Close()
	}
}

func newTestSessionManager() (*SessionManager, *int) {
	dials := 0
	sm := NewSessionManager(func(ctx context.Context, addr string) (net.Conn, error) {
		dials++
		c1, c2 := net.Pipe()
		go serveAuthSide(c2)
		return c1, nil
	})
	sm.SetSmuxOverride(testSmuxConfig())
	return sm, &dials
}

// 回归守卫（R1-1）：每周期断开（Disconnect）后必须能再次 Connect——
// Close 的锁存语义若泄漏进重连路径，客户端断一次线就永久离线
func TestSessionManager_DisconnectAllowsReconnect(t *testing.T) {
	sm, dials := newTestSessionManager()
	ctx := context.Background()

	if err := sm.Connect(ctx, "test:5201", "tok"); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	if *dials != 1 {
		t.Fatalf("dials = %d, want 1", *dials)
	}

	sm.Disconnect()
	if err := sm.Connect(ctx, "test:5201", "tok"); err != nil {
		t.Fatalf("reconnect after Disconnect: %v", err)
	}
	if *dials != 2 {
		t.Fatalf("dials = %d, want 2", *dials)
	}
}

// Close 锁存关闭态：终态关停后在途 Connect 不得再发布幽灵会话
func TestSessionManager_CloseLatchesConnect(t *testing.T) {
	sm, _ := newTestSessionManager()
	ctx := context.Background()

	if err := sm.Connect(ctx, "test:5201", "tok"); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	sm.Close()

	err := sm.Connect(ctx, "test:5201", "tok")
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Connect after Close must fail with closed error, got %v", err)
	}
}
