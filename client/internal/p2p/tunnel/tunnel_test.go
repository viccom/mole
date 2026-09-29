//go:build p2p

package tunnel

import (
	"net"
	"testing"
)

// TestParseTunnelOpen_IPv4 验证 IPv4 target 的基本解析。
func TestParseTunnelOpen_IPv4(t *testing.T) {
	f, err := ParseTunnelOpen("5:tcp:8080:1.2.3.4:443")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := []string{"5", "tcp", "8080", "1.2.3.4", "443"}
	for i, w := range want {
		if f[i] != w {
			t.Fatalf("field[%d]: got %q, want %q", i, f[i], w)
		}
	}
}

// TestParseTunnelOpen_IPv6Bracket 是 M5 的回归核心：bracket 形式 IPv6 target
// 必须解析出裸 host（fe80::1），且该 host 喂回 dial 处用的 net.JoinHostPort 后
// 能被 net.SplitHostPort 还原。修复前（从右拆两冒号算法）对 IPv6 完全失败。
func TestParseTunnelOpen_IPv6Bracket(t *testing.T) {
	f, err := ParseTunnelOpen("5:tcp:8080:[fe80::1]:443")
	if err != nil {
		t.Fatalf("IPv6 bracket parse error: %v", err)
	}
	if f[3] != "fe80::1" {
		t.Fatalf("host: got %q, want fe80::1", f[3])
	}
	if f[4] != "443" {
		t.Fatalf("port: got %q, want 443", f[4])
	}
	// 与 tcp.go/udp.go dial 处一致：JoinHostPort → SplitHostPort 往返
	joined := net.JoinHostPort(f[3], f[4])
	h, p, err := net.SplitHostPort(joined)
	if err != nil || h != "fe80::1" || p != "443" {
		t.Fatalf("roundtrip JoinHostPort/SplitHostPort: joined=%q h=%q p=%q err=%v", joined, h, p, err)
	}
	t.Logf("IPv6 bracket target %s — PASS", joined)
}

// TestParseTunnelOpen_Errors 验证畸形输入报错（字段不足、坏 host:port）。
func TestParseTunnelOpen_Errors(t *testing.T) {
	bad := []string{
		"",               // 空
		"5:tcp:8080",     // 字段不足（<4 段）
		"5:tcp:8080:bad", // 坏 host:port（SplitHostPort 失败）
	}
	for _, c := range bad {
		if _, err := ParseTunnelOpen(c); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}
