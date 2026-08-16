package moleAgent_client

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"moleAgent_client/internal/protocol"
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

// TestWriteCmd_AppendsNewline 命令必须以 '\n' 结尾，与服务端
// writeJSONLine 的 JSON Lines 约定一致；旧服务端 json.Unmarshal
// 兼容尾部空白，不影响存量服务端。
func TestWriteCmd_AppendsNewline(t *testing.T) {
	var buf bytes.Buffer
	cmd := protocol.ControlCmd{Cmd: "register", NodeID: "Test0001", Token: "t"}
	if err := writeCmd(&buf, cmd); err != nil {
		t.Fatalf("writeCmd error: %v", err)
	}
	raw := buf.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Errorf("writeCmd output must be newline-terminated, got %q", raw)
	}
	var got protocol.ControlCmd
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Cmd != "register" || got.NodeID != "Test0001" {
		t.Errorf("unexpected cmd: %+v", got)
	}
}

// TestWriteResp_AppendsNewline 推送应答同样走 JSON Lines 约定。
func TestWriteResp_AppendsNewline(t *testing.T) {
	var buf bytes.Buffer
	writeResp(&buf, "ok", "tunnels updated")
	raw := buf.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Errorf("writeResp output must be newline-terminated, got %q", raw)
	}
	var got protocol.ControlResponse
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Cmd != "ok" || got.Msg != "tunnels updated" {
		t.Errorf("unexpected resp: %+v", got)
	}
}

// TestReadResponse_Fragmented 服务端响应跨多个 Read 分片到达。
// 这是本次修复的核心场景：旧实现单次 Read 后立即解析，分包直接报错。
func TestReadResponse_Fragmented(t *testing.T) {
	msg := `{"cmd":"ok","msg":"registered"}` + "\n"
	r := &chunkReader{chunks: splitChunks(msg, 5)}
	resp, err := readResponse(r, time.Second)
	if err != nil {
		t.Fatalf("readResponse error: %v", err)
	}
	if resp.Cmd != "ok" || resp.Msg != "registered" {
		t.Errorf("unexpected resp: %+v", resp)
	}
}

// TestReadResponse_BareJSONNoNewline 兼容无换行的裸 JSON 响应，
// 保证 readControlMsg 不会被格式收紧破坏。
func TestReadResponse_BareJSONNoNewline(t *testing.T) {
	r := &chunkReader{chunks: [][]byte{[]byte(`{"cmd":"pong"}`)}}
	resp, err := readResponse(r, time.Second)
	if err != nil {
		t.Fatalf("readResponse error: %v", err)
	}
	if resp.Cmd != "pong" {
		t.Errorf("expected cmd=pong, got %q", resp.Cmd)
	}
	if resp.Msg != "" {
		t.Errorf("expected empty msg, got %q", resp.Msg)
	}
}

// TestReadControlMsg_OversizeRejected 客户端侧同样必须拒绝超限消息，
// 防止异常服务端把客户端内存耗尽。
func TestReadControlMsg_OversizeRejected(t *testing.T) {
	msg := `{"name":"` + strings.Repeat("x", maxControlMsgSize) + `"}`
	_, err := readControlMsg(&chunkReader{chunks: [][]byte{[]byte(msg)}}, maxControlMsgSize)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}
