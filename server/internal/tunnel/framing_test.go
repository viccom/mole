package tunnel

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// chunkReader 按固定分片依次返回数据，模拟 TCP 分包到达。
// 每次调用 Read 最多吐出一个分片；分片大于调用方缓冲时分多次返回
// （与真实 conn 的部分读语义一致）。
type chunkReader struct {
	chunks [][]byte
	pos    int
	off    int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for r.pos < len(r.chunks) {
		chunk := r.chunks[r.pos]
		if r.off >= len(chunk) {
			r.pos++
			r.off = 0
			continue
		}
		n := copy(p, chunk[r.off:])
		r.off += n
		if r.off >= len(chunk) {
			r.pos++
			r.off = 0
		}
		return n, nil
	}
	return 0, io.EOF
}

// splitChunks 把字符串按固定大小切块
func splitChunks(s string, size int) [][]byte {
	var out [][]byte
	for len(s) > 0 {
		n := size
		if n > len(s) {
			n = len(s)
		}
		out = append(out, []byte(s[:n]))
		s = s[n:]
	}
	return out
}

// TestReadControlMsg_SingleChunkBareJSON 旧版客户端一条消息单包到达、无换行。
// readControlMsg 必须兼容这种写法（存量节点升级滞后）。
func TestReadControlMsg_SingleChunkBareJSON(t *testing.T) {
	r := &chunkReader{chunks: [][]byte{
		[]byte(`{"cmd":"register","node_id":"Abcd1234","token":"t"}`),
	}}
	data, err := readControlMsg(r, maxControlMsgSize)
	if err != nil {
		t.Fatalf("readControlMsg error: %v", err)
	}
	var cmd ControlCmd
	if err := json.Unmarshal(data, &cmd); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, data)
	}
	if cmd.Cmd != "register" || cmd.NodeID != "Abcd1234" {
		t.Errorf("unexpected cmd: %+v", cmd)
	}
}

// TestReadControlMsg_FragmentedAcrossChunks 消息跨多个 Read 分片到达。
// 这是本次修复的核心场景：旧实现单次 Read 后立即解析，分包直接报 invalid json。
func TestReadControlMsg_FragmentedAcrossChunks(t *testing.T) {
	msg := `{"cmd":"register","node_id":"Abcd1234","token":"t","name":"edge-node-1"}`
	r := &chunkReader{chunks: splitChunks(msg, 7)}
	data, err := readControlMsg(r, maxControlMsgSize)
	if err != nil {
		t.Fatalf("readControlMsg error: %v", err)
	}
	var cmd ControlCmd
	if err := json.Unmarshal(data, &cmd); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, data)
	}
	if cmd.Cmd != "register" || cmd.NodeID != "Abcd1234" || cmd.Name != "edge-node-1" {
		t.Errorf("unexpected cmd: %+v", cmd)
	}
}

// TestReadControlMsg_NewlineTerminated 新写侧格式（writeJSONLine）：
// 完整 JSON + 换行一次到达。
func TestReadControlMsg_NewlineTerminated(t *testing.T) {
	r := &chunkReader{chunks: [][]byte{
		[]byte("{\"cmd\":\"ok\"}\n"),
	}}
	data, err := readControlMsg(r, maxControlMsgSize)
	if err != nil {
		t.Fatalf("readControlMsg error: %v", err)
	}
	var resp ControlResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal: %v (raw: %q)", err, data)
	}
	if resp.Cmd != "ok" {
		t.Errorf("expected cmd=ok, got %q", resp.Cmd)
	}
}

// TestReadControlMsg_LargeMessageOver4KB 超过旧实现 4096 字节缓冲的大消息
// （典型场景：register 携带大量 Tunnels + SysInfo）。旧实现必然截断失败。
func TestReadControlMsg_LargeMessageOver4KB(t *testing.T) {
	msg := `{"cmd":"register","node_id":"Abcd1234","token":"t","name":"` +
		strings.Repeat("x", 8192) + `"}`
	if len(msg) <= 4096 {
		t.Fatal("test message must exceed 4096 bytes to reproduce the old truncation")
	}
	// 单包到达 + 分包到达都必须成功
	for _, tc := range []struct {
		name   string
		chunks [][]byte
	}{
		{"single chunk", [][]byte{[]byte(msg)}},
		{"fragmented", splitChunks(msg, 1000)},
	} {
		data, err := readControlMsg(&chunkReader{chunks: tc.chunks}, maxControlMsgSize)
		if err != nil {
			t.Fatalf("%s: readControlMsg error: %v", tc.name, err)
		}
		var cmd ControlCmd
		if err := json.Unmarshal(data, &cmd); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if len(cmd.Name) != 8192 {
			t.Errorf("%s: name truncated: len=%d", tc.name, len(cmd.Name))
		}
	}
}

// TestReadControlMsg_OversizeRejected 超过上限的异常消息必须报错，
// 防止异常客户端无限发送数据耗尽服务端内存。
func TestReadControlMsg_OversizeRejected(t *testing.T) {
	msg := `{"name":"` + strings.Repeat("x", maxControlMsgSize) + `"}`
	_, err := readControlMsg(&chunkReader{chunks: [][]byte{[]byte(msg)}}, maxControlMsgSize)
	if err == nil {
		t.Fatal("expected oversize error, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestReadControlMsg_EOFBeforeCompleteJSON 流在 JSON 完整前关闭：
// 返回 io.EOF，调用方按流关闭处理，而不是误解析半条消息。
func TestReadControlMsg_EOFBeforeCompleteJSON(t *testing.T) {
	r := &chunkReader{chunks: [][]byte{[]byte(`{"cmd":"regi`)}}
	_, err := readControlMsg(r, maxControlMsgSize)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}
