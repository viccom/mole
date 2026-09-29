package proxy

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
)

// errWriter 永远写失败，用于验证错误路径的可观测性。
type errWriter struct{ err error }

func (e errWriter) Write([]byte) (int, error) { return 0, e.err }

// 错误响应写不出去时必须留痕。
// 回归背景：writeHTTPError 用 `_ = resp.Write(w)` 吞掉错误——客户端拿不到
// 状态码时完全静默，而该包此前没有任何测试。
func TestWriteHTTPErrorLogsWriteFailure(t *testing.T) {
	oldW, oldF := log.Writer(), log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
	})

	writeHTTPError(errWriter{err: errors.New("broken pipe")}, 502, "no tunnel matched")

	out := buf.String()
	if !strings.Contains(out, "broken pipe") {
		t.Fatalf("write failure must be logged, got %q", out)
	}
	if !strings.Contains(out, "502") {
		t.Errorf("log should identify the status code, got %q", out)
	}
}

// 写成功时不得产生日志（防刷屏：错误响应在正常代理流程里是常见路径）
func TestWriteHTTPErrorSilentOnSuccess(t *testing.T) {
	oldW, oldF := log.Writer(), log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
	})

	var ok bytes.Buffer
	writeHTTPError(&ok, 404, "not found")

	if buf.Len() != 0 {
		t.Fatalf("successful write must not log, got %q", buf.String())
	}
	// 响应内容仍要正确写出
	if !strings.Contains(ok.String(), "404") || !strings.Contains(ok.String(), "not found") {
		t.Fatalf("response body malformed: %q", ok.String())
	}
}
