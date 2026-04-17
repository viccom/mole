package proxy

import (
	"bufio"
	"io"
	"log"
	"net"
	"sync/atomic"
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

	bufA := make([]byte, 32*1024)
	bufB := make([]byte, 32*1024)
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		var n int64
		if br != nil && br.Buffered() > 0 {
			n, _ = io.CopyBuffer(backendConn, br, bufA)
		} else {
			n, _ = io.CopyBuffer(backendConn, stream, bufA)
		}
		atomic.AddUint64(&tcpBytesOut, uint64(n))
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		n, _ := io.CopyBuffer(stream, backendConn, bufB)
		atomic.AddUint64(&tcpBytesIn, uint64(n))
	}()
	<-done
	<-done
}
