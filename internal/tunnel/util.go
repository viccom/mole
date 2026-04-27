package tunnel

import (
	"encoding/json"
	"io"
	"strings"
)

// biCopyBufferSize defines the buffer size for bidirectional data forwarding.
// A large buffer reduces the frequency of token return cycles in the underlying
// smux session, preventing the recvLoop token bucket from draining to zero when
// the external peer (e.g. RDP client) consumes data slower than the tunnel backend
// produces it.
const biCopyBufferSize = 1024 * 1024 // 1MB

// biCopy 双向转发数据，任一方向完成后等待另一方向完成
func biCopy(a, b io.ReadWriter) {
	bufA := make([]byte, biCopyBufferSize)
	bufB := make([]byte, biCopyBufferSize)
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		io.CopyBuffer(a, b, bufA)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		io.CopyBuffer(b, a, bufB)
	}()
	<-done
	<-done
}

// writeJSONLine 将对象序列化为 JSON 并写入，末尾加换行符
func writeJSONLine(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// stripPort 去除 host 中的端口号部分
func stripPort(host string) string {
	if idx := strings.Index(host, ":"); idx != -1 {
		return host[:idx]
	}
	return host
}
