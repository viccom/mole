//go:build p2p

package session

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// newSessionPair 建一对 secure session（net.Pipe + yamux）：
// server 侧是被测方且已 Run；client 侧不 Run（由用例直接操控 mux/mainStream，
// 以便只杀掉主控流而保留 multiplex 连接——模拟「对端进程冻结/失联但链路还在」）。
func newSessionPair(t *testing.T) (srv, cli *secureSession) {
	t.Helper()
	a, b := net.Pipe()
	var srvMux, cliMux StreamMux
	srvErr, cliErr := make(chan error, 1), make(chan error, 1)
	go func() {
		s, e := yamux.Server(a, nil)
		if e == nil {
			srvMux = NewYamuxStreamMux(s)
		}
		srvErr <- e
	}()
	go func() {
		s, e := yamux.Client(b, nil)
		if e == nil {
			cliMux = NewYamuxStreamMux(s)
		}
		cliErr <- e
	}()
	if e := <-srvErr; e != nil {
		t.Fatalf("yamux server: %v", e)
	}
	if e := <-cliErr; e != nil {
		t.Fatalf("yamux client: %v", e)
	}

	type ns struct {
		s Session
		e error
	}
	srvCh, cliCh := make(chan ns, 1), make(chan ns, 1)
	go func() {
		s, e := NewSession(context.Background(), &Outcome{Mux: srvMux, IsClient: false})
		srvCh <- ns{s, e}
	}()
	go func() {
		s, e := NewSession(context.Background(), &Outcome{Mux: cliMux, IsClient: true})
		cliCh <- ns{s, e}
	}()
	sn, cn := <-srvCh, <-cliCh
	if sn.e != nil {
		t.Fatalf("server NewSession: %v", sn.e)
	}
	if cn.e != nil {
		t.Fatalf("client NewSession: %v", cn.e)
	}

	srv = sn.s.(*secureSession)
	cli = cn.s.(*secureSession)
	go srv.Run(context.Background())
	t.Cleanup(func() { srv.Close(); cli.Close() })
	time.Sleep(150 * time.Millisecond) // 等 reader / accept goroutine 就绪
	return srv, cli
}

// TestRunReturnsWhenPeerStallsDataStream 守护「对端开一条流写 4 字节后停住」
// 的场景：分发出的处理函数（文件/测速/隧道数据）内部没有读 deadline，对端不
// 写不关就会永久阻塞，而 Run 结尾的 streamWG.Wait() 会因此永远等不到 ——
// Run 不返回则上层 handler 的 <-runDone 卡死，该隧道从此不再重连（退避循环
// 再也不执行），Status 还一直报 connected。
// 会话 ctx 结束（主控流断开 = 对端失联）时必须用过期读 deadline 打断这些阻塞。
func TestRunReturnsWhenPeerStallsDataStream(t *testing.T) {
	srv, cli := newSessionPair(t)

	// 对端开数据流：写 4 字节未知 magic（落到 default 分支 = 测速/未知处理）后停住
	st, err := cli.mux.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer st.Close()
	if _, err := st.Write([]byte{0, 0, 0, 0}); err != nil {
		t.Fatalf("write magic: %v", err)
	}

	time.Sleep(200 * time.Millisecond) // 等 server accept 该流并阻塞在读上

	// 只断开主控流（保留 mux）：server 侧 reader 见 EOF → s.cancel()，
	// accept 循环随之退出，此后只剩 streamWG.Wait() 等这条停住的流
	if err := cli.mainStream.Close(); err != nil {
		t.Fatalf("close peer main stream: %v", err)
	}

	select {
	case <-srv.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("对端数据流停住时 Run 不返回：streamWG.Wait 永久阻塞（该隧道不会重连）")
	}
}
