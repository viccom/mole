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

// 对抗用例：密码含 '/' 时 url.Parse 会伪成功（把 user:pa 误当 host），
// 成功路径的端口校验必须识破——此用例在只信任 url.Parse 的实现上会泄漏
// 用户名与密码片段（复核轮发现的真实缺陷）。
func TestRedactedBrokerAdversarialPasswordWithSlash(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"mqtt://user:pa://ss@host:1883": "host:1883", // 密码含 ://
		"mqtt://us@er:pass@host:1883":   "host:1883", // 用户名与密码都含 @
		"mqtt://user:pass@@host:1883":   "host:1883", // 密码尾部 @
		"mqtt://user:pass@":             "",          // @ 后无 host
	}
	for raw, wantHost := range cases {
		got := RedactedBroker(raw)
		for _, leak := range []string{"user", "us@er", "pa://ss", "pass"} {
			if strings.Contains(got, leak) {
				t.Errorf("RedactedBroker(%q) = %q, 泄漏 %q", raw, got, leak)
			}
		}
		if wantHost != "" && !strings.Contains(got, wantHost) {
			t.Errorf("RedactedBroker(%q) = %q, 应保留 host %q", raw, got, wantHost)
		}
	}
}

// 路径里含 @ 的正常 URL：url.Parse 成功且 host 合法，走成功路径
func TestRedactedBrokerPathWithAt(t *testing.T) {
	t.Parallel()

	if got := RedactedBroker("mqtt://host:1883/path@x"); got != "mqtt://host:1883" {
		t.Errorf("RedactedBroker 路径含 @ 的合法 URL = %q, want mqtt://host:1883", got)
	}
	if got := RedactedBroker("[::1]"); got != "<redacted>" && !strings.Contains(got, "::1") {
		// IPv6 无端口：不崩即可
		_ = got
	}
}
