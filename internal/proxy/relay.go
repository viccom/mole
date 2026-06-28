package proxy

import (
	"io"
	"sync"
	"sync/atomic"
)

var (
	tcpBytesIn   uint64
	tcpBytesOut  uint64
	httpBytesIn  uint64
	httpBytesOut uint64

	tunnelMu    sync.RWMutex
	tunnelStats = make(map[string]*tunnelTraffic)
)

type tunnelTraffic struct {
	Name        string
	TCPBytesIn  uint64
	TCPBytesOut uint64
	HTTPBytesIn uint64
	HTTPBytesOut uint64
}

// TunnelTraffic is an exported alias for external access
type TunnelTraffic = tunnelTraffic

func GetTCPBytesIn()   uint64 { return atomic.LoadUint64(&tcpBytesIn) }
func GetTCPBytesOut()  uint64 { return atomic.LoadUint64(&tcpBytesOut) }
func GetHTTPBytesIn()  uint64 { return atomic.LoadUint64(&httpBytesIn) }
func GetHTTPBytesOut() uint64 { return atomic.LoadUint64(&httpBytesOut) }

func AddHTTPBytes(tunnelName string, in, out uint64) {
	atomic.AddUint64(&httpBytesIn, in)
	atomic.AddUint64(&httpBytesOut, out)
	if tunnelName != "" {
		tunnelMu.Lock()
		if _, ok := tunnelStats[tunnelName]; !ok {
			tunnelStats[tunnelName] = &tunnelTraffic{Name: tunnelName}
		}
		tunnelStats[tunnelName].HTTPBytesIn += in
		tunnelStats[tunnelName].HTTPBytesOut += out
		tunnelMu.Unlock()
	}
}

// TunnelTrafficStats returns a snapshot of all tunnel traffic stats
func TunnelTrafficStats() map[string]tunnelTraffic {
	tunnelMu.RLock()
	defer tunnelMu.RUnlock()
	result := make(map[string]tunnelTraffic, len(tunnelStats))
	for k, v := range tunnelStats {
		result[k] = *v
	}
	return result
}

// RecordHTTPBytes records HTTP traffic for a tunnel
func RecordHTTPBytes(tunnelName string, bytesIn, bytesOut uint64) {
	atomic.AddUint64(&httpBytesIn, bytesIn)
	atomic.AddUint64(&httpBytesOut, bytesOut)
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	if _, ok := tunnelStats[tunnelName]; !ok {
		tunnelStats[tunnelName] = &tunnelTraffic{Name: tunnelName}
	}
	tunnelStats[tunnelName].HTTPBytesIn += bytesIn
	tunnelStats[tunnelName].HTTPBytesOut += bytesOut
}

// RecordTCPBytes records TCP traffic for a tunnel
func RecordTCPBytes(tunnelName string, bytesIn, bytesOut uint64) {
	atomic.AddUint64(&tcpBytesIn, bytesIn)
	atomic.AddUint64(&tcpBytesOut, bytesOut)
	if tunnelName == "" {
		return
	}
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	if _, ok := tunnelStats[tunnelName]; !ok {
		tunnelStats[tunnelName] = &tunnelTraffic{Name: tunnelName}
	}
	tunnelStats[tunnelName].TCPBytesIn += bytesIn
	tunnelStats[tunnelName].TCPBytesOut += bytesOut
}

// Relay 双向数据转发，直到一侧连接关闭
func Relay(a, b io.ReadWriteCloser) {
	bufA := make([]byte, 1024*1024)
	bufB := make([]byte, 1024*1024)
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
