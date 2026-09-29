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

// biCopyBufPool biCopy 缓冲池（PERF-01）：每次调用需要两个 1MB 缓冲，高连接
// churn 下重复 make 是可观的分配压力。池化只省分配，尺寸保持 1MB——零行为
// 变化；未实测到吞吐差异前不引入降尺寸变量（若后续实测 256KB 吞吐差异可
// 忽略，再评估降尺寸以缩小驻留内存）。
// 取放约定：存 *[]byte 避免每次 Put 装箱切片头；归还前不清零——读路径
// 全量覆写缓冲内容，残留数据无害。
var biCopyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, biCopyBufferSize)
		return &b
	},
}

// biCopy 双向转发数据，任一方向结束即解除双方阻塞。
// 半关/空闲连接下，一侧 io.CopyBuffer 读到 EOF 退出时，另一侧仍阻塞在 Read；
// 若等两侧都结束（旧的 <-done;<-done），biCopy 永不返回，调用方 defer 的
// conn.Close/stream.Close 不执行 → fd + goroutine + buffer 永久泄漏。
// 缓冲从 sync.Pool 取还：两个方向各自 Get 一个、defer Put 一次，
// 函数恰有一个退出路径，不存在 double-Put。
func biCopy(a, b io.ReadWriter) {
	// 必须保留 Get 返回的原 *[]byte 指针用于 Put（池内存放的就是指针）：
	// 解引用后再取局部变量地址（&bufX）会把切片头逃逸到堆、每次 Put 新分配
	// 一个装箱头，池化「免装箱」的收益被抵消（QUA-06 附带 PERF 兑现）
	pA := biCopyBufPool.Get().(*[]byte)
	pB := biCopyBufPool.Get().(*[]byte)
	defer biCopyBufPool.Put(pA)
	defer biCopyBufPool.Put(pB)
	bufA := *pA
	bufB := *pB
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
