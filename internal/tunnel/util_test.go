package tunnel

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// ===== PERF-01：biCopy 缓冲池化回归守卫 =====

// TestBiCopyRoundTrip 验证双向转发的数据完整性：载荷大于单块缓冲
// （1.2MB > 1MB），覆盖跨块读写路径；两端关闭后 biCopy 必须返回（不泄漏）
func TestBiCopyRoundTrip(t *testing.T) {
	// echo 端点：net.Pipe + 自回显
	echoConn, echoPeer := net.Pipe()
	go io.Copy(echoPeer, echoPeer)
	// 客户端端点：net.Pipe，对侧由测试驱动
	clientConn, clientPeer := net.Pipe()

	done := make(chan struct{})
	go func() {
		biCopy(clientConn, echoConn)
		close(done)
	}()

	payload := bytes.Repeat([]byte("mole"), 300000) // 1.2MB
	writeErr := make(chan error, 1)
	go func() {
		_, err := clientPeer.Write(payload)
		writeErr <- err
	}()

	got := make([]byte, len(payload))
	clientPeer.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.ReadFull(clientPeer, got); err != nil {
		t.Fatalf("read echoed data: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("round-trip data mismatch")
	}

	// 关闭两端：一侧 EOF 触发 biCopy 返回（另一侧被过期 deadline 解除阻塞）
	clientPeer.Close()
	echoPeer.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("biCopy did not return after both ends closed")
	}
}

// TestBiCopyConcurrentStress 并发压力：多路 biCopy 同时从池中取还缓冲，
// -race 下验证无 double-Put / 缓冲串用导致的数据错乱
func TestBiCopyConcurrentStress(t *testing.T) {
	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			echoConn, echoPeer := net.Pipe()
			go io.Copy(echoPeer, echoPeer)
			clientConn, clientPeer := net.Pipe()

			done := make(chan struct{})
			go func() {
				biCopy(clientConn, echoConn)
				close(done)
			}()

			// 每路用不同填充字节：若池化引入缓冲串用，回显数据会错乱
			payload := bytes.Repeat([]byte{byte('A' + seq)}, 128*1024)
			go clientPeer.Write(payload)

			got := make([]byte, len(payload))
			clientPeer.SetReadDeadline(time.Now().Add(10 * time.Second))
			if _, err := io.ReadFull(clientPeer, got); err != nil {
				t.Errorf("worker %d: read: %v", seq, err)
				return
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("worker %d: round-trip mismatch (buffer cross-contamination?)", seq)
				return
			}

			clientPeer.Close()
			echoPeer.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Errorf("worker %d: biCopy did not return", seq)
			}
		}(i)
	}
	wg.Wait()
}
