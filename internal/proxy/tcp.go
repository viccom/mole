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
func HandleRawStream(stream io.ReadWriteCloser, br *bufio.Reader, findTarget func() string) {
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

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		if br != nil && br.Buffered() > 0 {
			io.Copy(backendConn, br)
		} else {
			io.Copy(backendConn, stream)
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(stream, backendConn)
	}()
	<-done
	<-done
}
