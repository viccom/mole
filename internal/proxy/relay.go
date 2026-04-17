package proxy

import (
	"io"
	"sync/atomic"
)

var (
	tcpBytesIn  uint64
	tcpBytesOut uint64
)

func GetTCPBytesIn()  uint64 { return atomic.LoadUint64(&tcpBytesIn) }
func GetTCPBytesOut() uint64 { return atomic.LoadUint64(&tcpBytesOut) }

// Relay 双向数据转发，直到一侧连接关闭
func Relay(a, b io.ReadWriteCloser) {
	bufA := make([]byte, 32*1024)
	bufB := make([]byte, 32*1024)
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		n, _ := io.CopyBuffer(a, b, bufA)
		atomic.AddUint64(&tcpBytesOut, uint64(n))
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		n, _ := io.CopyBuffer(b, a, bufB)
		atomic.AddUint64(&tcpBytesIn, uint64(n))
	}()
	<-done
	<-done
}
