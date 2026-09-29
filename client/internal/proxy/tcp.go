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
// findTarget 返回 (后端地址, 网络)；network 为 "udp" 时按 UDP 语义桥接
// （与服务端既有约定一致：单次 smux 写 ≈ 单个数据报，仅对 ≤MaxFrameSize=32768
// 字节成立——smux 单帧消费保边界；更大数据报会被分帧拆碎，属两端协议层
// 已知限制，需要长度前缀协议改造才能支持）
func HandleRawStream(stream io.ReadWriteCloser, br *bufio.Reader, findTarget func() (string, string), tunnelName string) {
	target, network := findTarget()
	if target == "" {
		return
	}

	var backendConn net.Conn
	var err error
	if network == "udp" {
		backendConn, err = dialUDPBackend(target, rawDialTimeout)
	} else {
		backendConn, err = net.DialTimeout("tcp", target, rawDialTimeout)
	}
	if err != nil {
		log.Printf("Dial %s backend %s failed: %v", network, target, err)
		return
	}
	defer backendConn.Close()

	// Disable Nagle's algorithm for low-latency interactive traffic (SSH/RDP),
	// matching the tunnel-side setting in transport/dialer.go.
	if tcpConn, ok := backendConn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}

	bufSize := 1024 * 1024 // 1MB
	if network == "udp" {
		bufSize = 65507 // UDP 数据报上限，避免 Write 报 message too long
	}
	bufA := make([]byte, bufSize)
	bufB := make([]byte, bufSize)
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

// dialUDPBackend 建立到 UDP 后端的连接（connected UDP socket）：
// 每次 Write 对应一个数据报、每次 Read 返回一个数据报，
// 使 TCP 的双向 io.Copy 桥接结构可以原样复用
func dialUDPBackend(target string, timeout time.Duration) (net.Conn, error) {
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: timeout}
	return d.Dial("udp", raddr.String())
}
