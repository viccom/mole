package proxy

import (
	"io"
)

// Relay 双向数据转发，直到一侧连接关闭
func Relay(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(a, b)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(b, a)
	}()
	<-done
	<-done
}
