package ser2mq

import (
	"net/url"
	"strings"
	"testing"
)

// RedactedBroker 剥离 userinfo，保留 scheme://host:port。
// 回归背景：Broker 文档形态为 mqtt://user:pass@host:port，userinfo 是凭据，
// 而 ser2mq.go 的启动日志原样打印整个 Broker 串。
func TestRedactedBrokerStripsUserinfo(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"mqtt://user:pass@broker.example.com:1883", "mqtt://broker.example.com:1883"},
		{"mqtt://alice@broker.example.com:1883", "mqtt://broker.example.com:1883"},
		{"tcp://broker.example.com:1883", "tcp://broker.example.com:1883"},
		{"mqtt://broker.example.com", "mqtt://broker.example.com"},
		{"", ""},
	}
	for _, tc := range cases {
		got := RedactedBroker(tc.in)
		if got != tc.want {
			t.Errorf("RedactedBroker(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, "pass") || strings.Contains(got, "alice@") {
			t.Errorf("RedactedBroker(%q) leaked userinfo: %q", tc.in, got)
		}
	}
}

// 解析失败时绝不回退原串——原串正是含有凭据、且被解析拒绝的那个。
// 若实现写成 `if err != nil { return raw }`，本测试会立即失败。
func TestRedactedBrokerNeverReturnsRawOnParseError(t *testing.T) {
	t.Parallel()

	// 解析失败的输入：userinfo 必须剥离，host 保留（排查需要）
	cases := []struct{ in, wantHost string }{
		{"mqtt://user:pa%ss@broker.example.com:1883", "broker.example.com:1883"},
		{"mqtt://user:pa ss@broker.example.com:1883", "broker.example.com:1883"},
		{"mqtt://user:p@ss@broker.example.com:1883", "broker.example.com:1883"},
	}
	for _, tc := range cases {
		got := RedactedBroker(tc.in)
		if got == tc.in {
			t.Fatalf("RedactedBroker(%q) returned the raw string on parse failure", tc.in)
		}
		for _, leak := range []string{"user:", "p@ss", "pa%ss", "pa ss"} {
			if strings.Contains(got, leak) {
				t.Errorf("RedactedBroker(%q) = %q leaks %q", tc.in, got, leak)
			}
		}
		if !strings.Contains(got, tc.wantHost) {
			t.Errorf("RedactedBroker(%q) = %q, want it to retain host %q", tc.in, got, tc.wantHost)
		}
	}
}

// brokerError 的错误文本不得包含凭据。
// 回归背景：url.Parse 返回的 *url.Error 其 Error() 形如
// `parse "mqtt://user:pass@host:1883": <cause>`，直接上抛会让凭据随
// manager 的 log.Printf 落盘。此测试在修复前失败（错误文本含 user:pass）。
func TestBrokerErrorDoesNotLeakCredentials(t *testing.T) {
	t.Parallel()

	raw := "mqtt://leakuser:leakpass@broker.example.com:1883%zz"
	// 构造真实的 url.Parse 错误（非法转义）
	_, err := url.Parse(raw)
	if err == nil {
		t.Fatal("expected url.Parse to fail for malformed escape")
	}
	wrapped := brokerError(raw, err)

	msg := wrapped.Error()
	for _, leak := range []string{"leakuser", "leakpass", "leakuser:leakpass@"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("brokerError() leaked %q in error text: %s", leak, msg)
		}
	}
	// 仍需保留可诊断性：host 与内层 cause 应在
	if !strings.Contains(msg, "broker.example.com") {
		t.Errorf("brokerError() dropped host, msg = %s", msg)
	}
}
