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

	// 主用例：坏转义在密码里、host 干净（最真实的触发形态）
	raw := "mqtt://leakuser:leak%zzpass@broker.example.com:1883"
	_, err := url.Parse(raw)
	if err == nil {
		t.Fatal("expected url.Parse to fail for malformed escape in password")
	}
	msg := brokerError(raw, err).Error()
	for _, leak := range []string{"leakuser", "leakpass", "leakuser:leak%zzpass@"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("brokerError() leaked %q in error text: %s", leak, msg)
		}
	}
	// host 干净时必须保留（排查连接问题所需）
	if !strings.Contains(msg, "broker.example.com:1883") {
		t.Errorf("brokerError() dropped clean host, msg = %s", msg)
	}

	// 变体：坏转义在 host 段——host 本身不可信，允许整体遮蔽，但绝不能泄漏
	raw2 := "mqtt://leakuser:leakpass@broker.example.com:1883%zz"
	_, err2 := url.Parse(raw2)
	if err2 == nil {
		t.Fatal("expected url.Parse to fail for malformed port")
	}
	msg2 := brokerError(raw2, err2).Error()
	for _, leak := range []string{"leakuser", "leakpass"} {
		if strings.Contains(msg2, leak) {
			t.Fatalf("brokerError() leaked %q for malformed-host input: %s", leak, msg2)
		}
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

// 路径里含 @ 的 URL：显示会错（含 @ 的输入一律走手工剥离，取最后一个 @
// 之后的内容）——已知代价：broker 连接只用 host，路径无意义；此测试钉住
// 该行为，防有人"修复"显示时把泄漏带回来
func TestRedactedBrokerPathWithAt(t *testing.T) {
	t.Parallel()

	if got := RedactedBroker("mqtt://host:1883/path@x"); got != "mqtt://x" {
		t.Errorf("RedactedBroker 路径含 @ = %q, want mqtt://x（显示错误的已知代价）", got)
	}
	// IPv6 无端口裸串：残片不可信，整体遮蔽（F15：原断言为空操作，已修正）
	if got := RedactedBroker("[::1]"); got != "<redacted>" {
		t.Errorf("RedactedBroker(裸 IPv6) = %q, want <redacted>", got)
	}
	// 合法带端口的 IPv6 正常展示
	if got := RedactedBroker("mqtt://[::1]:1883"); got != "mqtt://[::1]:1883" {
		t.Errorf("RedactedBroker(IPv6:port) = %q, want mqtt://[::1]:1883", got)
	}
}

// 独立审查复核轮的三条泄漏（均已实证）：
//  1. 无 @ 且解析失败 → 原实现原样返回 "mqtt://admin:S3cr3t"
//  2. 密码首段全数字 → url.Parse 伪成功 Host="alice:1883" 带出用户名
//  3. 密码含 "://" 且无合法 scheme → 从密码切出的伪 scheme 前缀含用户名
func TestRedactedBrokerReviewRoundLeaks(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"mqtt://admin:S3cr3t", "<redacted>"},                              // 1: 残片不可信
		{"mqtt://alice:1883/secret@realhost:9000", "mqtt://realhost:9000"}, // 2
		{"user:pa://ss@host:1883", "host:1883"},                            // 3: 伪 scheme 被拒
		{"mqtt://user:/path", "<redacted>"},                                // 尾随冒号空端口残片
	}
	for _, tc := range cases {
		got := RedactedBroker(tc.in)
		if got != tc.want {
			t.Errorf("RedactedBroker(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for _, leak := range []string{"admin", "S3cr3t", "alice", "user:pa", "user:"} {
			if strings.Contains(got, leak) && !strings.Contains(tc.want, leak) {
				t.Errorf("RedactedBroker(%q) = %q 泄漏 %q", tc.in, got, leak)
			}
		}
	}
}
