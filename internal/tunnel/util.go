package tunnel

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

// biCopyBufferSize defines the buffer size for bidirectional data forwarding.
// A large buffer reduces the frequency of token return cycles in the underlying
// smux session, preventing the recvLoop token bucket from draining to zero when
// the external peer (e.g. RDP client) consumes data slower than the tunnel backend
// produces it.
const biCopyBufferSize = 1024 * 1024 // 1MB

// biCopy 双向转发数据，任一方向结束即解除双方阻塞。
// 半关/空闲连接下，一侧 io.CopyBuffer 读到 EOF 退出时，另一侧仍阻塞在 Read；
// 若等两侧都结束（旧的 <-done;<-done），biCopy 永不返回，调用方 defer 的
// conn.Close/stream.Close 不执行 → fd + goroutine + buffer 永久泄漏。
func biCopy(a, b io.ReadWriter) {
	bufA := make([]byte, biCopyBufferSize)
	bufB := make([]byte, biCopyBufferSize)
	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer closeDone()
		io.CopyBuffer(a, b, bufA)
	}()
	go func() {
		defer wg.Done()
		defer closeDone()
		io.CopyBuffer(b, a, bufB)
	}()
	<-done
	// 一侧已结束：对双方设过期 ReadDeadline，强制另一侧阻塞的 io.CopyBuffer 退出
	unblockRead(a)
	unblockRead(b)
	wg.Wait()
}

// unblockRead 对实现了 SetReadDeadline 的连接设过期 deadline，唤醒阻塞的 Read。
// smux.Stream 和 countingConn(嵌入 net.Conn) 均实现该接口。
func unblockRead(rw io.ReadWriter) {
	if sd, ok := rw.(interface{ SetReadDeadline(time.Time) error }); ok {
		sd.SetReadDeadline(time.Now())
	}
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
