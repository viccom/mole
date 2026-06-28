package proxy

import (
	"bufio"
	"io"
	"log"
	"net"
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

	bufA := make([]byte, 1024*1024) // 1MB
	bufB := make([]byte, 1024*1024)
	done := make(chan struct{}, 2)
	var tcpIn, tcpOut uint64
	go func() {
		defer func() { done <- struct{}{} }()
		var n int64
		if br != nil && br.Buffered() > 0 {
			n, _ = io.CopyBuffer(backendConn, br, bufA)
		} else {
			n, _ = io.CopyBuffer(backendConn, stream, bufA)
		}
		tcpOut = uint64(n)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		n, _ := io.CopyBuffer(stream, backendConn, bufB)
		tcpIn = uint64(n)
	}()
	<-done
	<-done

	RecordTCPBytes(tunnelName, tcpIn, tcpOut)
}
