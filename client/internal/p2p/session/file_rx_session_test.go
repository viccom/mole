//go:build p2p

package session

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// 验证 WithFileReceiveDir 接线：getter 返回的目录真正生效到 HandleFileRX，
// 且连接建立后 getter 动态读取（非 NewSession 时固定）。覆盖整条
// client SendFile → accept loop → HandleFileRX(dir=getter()) 链路。
func TestSecureSession_FileRX_WithDir(t *testing.T) {
	recvDir := t.TempDir()

	// 发送端临时文件（SendFile 需要磁盘路径，os.Open）。
	sendPath := filepath.Join(t.TempDir(), "send.txt")
	if err := os.WriteFile(sendPath, []byte("from-peer"), 0o600); err != nil {
		t.Fatalf("write send file: %v", err)
	}

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

	got := make(chan FileTransferResult, 1)
	type ns struct {
		s Session
		e error
	}
	srvCh, cliCh := make(chan ns, 1), make(chan ns, 1)
	go func() {
		s, e := NewSession(context.Background(), &Outcome{Mux: srvMux, IsClient: false},
			WithFileReceiveDir(func() string { return recvDir }),
			WithOnFileReceived(func(r FileTransferResult) { got <- r }))
		srvCh <- ns{s, e}
	}()
	go func() {
		s, e := NewSession(context.Background(), &Outcome{Mux: cliMux, IsClient: true})
		cliCh <- ns{s, e}
	}()
	srv := <-srvCh
	cli := <-cliCh
	if srv.e != nil {
		t.Fatalf("server NewSession: %v", srv.e)
	}
	if cli.e != nil {
		t.Fatalf("client NewSession: %v", cli.e)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	go srv.s.Run(ctx)
	go cli.s.Run(ctx)

	time.Sleep(150 * time.Millisecond) // 等 reader / accept goroutine 就绪
	if _, err := cli.s.SendFile(sendPath); err != nil {
		t.Fatalf("SendFile: %v", err)
	}

	select {
	case r := <-got:
		if filepath.Dir(r.Path) != recvDir {
			t.Fatalf("file saved outside recvDir: got=%q dir=%q", r.Path, recvDir)
		}
		if r.FileName != "send.txt" {
			t.Errorf("FileName = %q, want send.txt (basename, not full path)", r.FileName)
		}
		b, err := os.ReadFile(r.Path)
		if err != nil || string(b) != "from-peer" {
			t.Fatalf("recv file wrong: %v %q", err, string(b))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for file receive")
	}
	srv.s.Close()
	cli.s.Close()
}
