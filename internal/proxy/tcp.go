package proxy

import (
	"bufio"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const rawDialTimeout = 10 * time.Second

// HandleRawStream 处理 TCP/UDP 原始数据转发
func HandleRawStream(stream io.ReadWriteCloser, br *bufio.Reader, findTarget func() string, tunnelName string) {
	target := findTarget()
	if target == "" {
		return
	}

	backendConn, err := net.DialTimeout("tcp", target, rawDialTimeout)
	if err != nil {
		log.Printf("Dial backend %s failed: %v", target, err)
		return
	}
	defer backendConn.Close()

	// Disable Nagle's algorithm for low-latency interactive traffic (SSH/RDP),
	// matching the tunnel-side setting in transport/dialer.go.
	if tcpConn, ok := backendConn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}

	bufA := make([]byte, 1024*1024) // 1MB
	bufB := make([]byte, 1024*1024)
	// 任一侧 io.Copy 结束即解除双方阻塞，避免半关/空闲连接导致另一侧永久挂起
	// （否则 HandleRawStream 永不返回，backend fd + smux stream + buffer + goroutine 全部泄漏）
	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }
	var tcpIn, tcpOut uint64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer closeDone()
		var n int64
		if br != nil && br.Buffered() > 0 {
			n, _ = io.CopyBuffer(backendConn, br, bufA)
		} else {
			n, _ = io.CopyBuffer(backendConn, stream, bufA)
		}
		tcpOut = uint64(n)
	}()
	go func() {
		defer wg.Done()
		defer closeDone()
		n, _ := io.CopyBuffer(stream, backendConn, bufB)
		tcpIn = uint64(n)
	}()
	<-done
	// 一侧已结束：关闭 backend + 对 stream 设过期 deadline，强制另一侧阻塞的 io.Copy 退出。
	// 不直接 Close(stream)，避免与 dispatchStream 的 defer stream.Close() 二次关闭风险。
	backendConn.Close()
	if sd, ok := stream.(interface{ SetReadDeadline(time.Time) error }); ok {
		sd.SetReadDeadline(time.Now())
	}
	wg.Wait()

	RecordTCPBytes(tunnelName, tcpIn, tcpOut)
}
