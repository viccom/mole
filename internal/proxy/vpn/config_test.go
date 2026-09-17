package vpn

import (
	"strings"
	"testing"
)

func TestBuildArgsIncludesRestPortAndPreservesUnknownArgs(t *testing.T) {
	cfg := Config{
		Args: []string{
			"-k", "old-token",
			"--rest-port", "7000",
			"--disable-stats",
			"--tap",
		},
		VNT: &VNTConfig{
			Enabled:  true,
			Token:    "new-token",
			Server:   "vpn.example.com",
			DeviceID: "device-1",
			RestPort: 6123,
		},
	}

	got := cfg.BuildArgs()

	assertArgSequence(t, got, "-k", "new-token")
	assertArgSequence(t, got, "-s", "vpn.example.com")
	assertArgSequence(t, got, "-d", "device-1")
	assertArgSequence(t, got, "--rest-port", "6123")

	if containsArgSequence(got, "-k", "old-token") {
		t.Fatalf("BuildArgs() should override stale token args, got %v", got)
	}
	if containsArgSequence(got, "--rest-port", "7000") {
		t.Fatalf("BuildArgs() should override stale rest-port args, got %v", got)
	}
	if !containsArgSequence(got, "--disable-stats") {
		t.Fatalf("BuildArgs() should preserve unknown args, got %v", got)
	}
	if !containsArgSequence(got, "--tap") {
		t.Fatalf("BuildArgs() should preserve flag-only unknown args, got %v", got)
	}
}

func assertArgSequence(t *testing.T, args []string, seq ...string) {
	t.Helper()
	if !containsArgSequence(args, seq...) {
		t.Fatalf("args %v do not contain sequence %v", args, seq)
	}
}

func containsArgSequence(args []string, seq ...string) bool {
	if len(seq) == 0 {
		return true
	}
	for i := 0; i <= len(args)-len(seq); i++ {
		match := true
		for j := range seq {
			if args[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// 日志脱敏：-k 令牌与 -w 密码不得出现在打印用的参数副本里。
// 回归背景：process.go 打印 BuildArgs() 全量 argv，凭据落入 stderr/journald。
func TestRedactedArgsDoesNotLeakVNTSecrets(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Args: []string{"-k", "stale-token", "--disable-stats", "-w", "stale-pass", "--tap"},
		VNT: &VNTConfig{
			Enabled:  true,
			Token:    "SECRET-TOKEN-123",
			Server:   "vpn.example.com",
			Password: "SECRET-PASSWORD-456",
			RestPort: 6123,
		},
	}
	args := cfg.BuildArgs()
	red := redactedArgs(args)

	// 非敏感项必须原样保留——脱敏不能让可观测性整体丢失
	for _, seq := range [][]string{
		{"-s", "vpn.example.com"},
		{"--rest-port", "6123"},
		{"--disable-stats"},
		{"--tap"},
	} {
		assertArgSequence(t, red, seq...)
	}

	// 敏感值必须消失
	joined := strings.Join(red, " ")
	for _, secret := range []string{"SECRET-TOKEN-123", "SECRET-PASSWORD-456"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("redactedArgs() leaked %q: %v", secret, red)
		}
	}

	// 占位符数量与真实敏感项数量一致
	if n := strings.Count(joined, "<redacted>"); n != 2 {
		t.Errorf("redaction count = %d, want 2: %v", n, red)
	}

	// 关键：真实 argv 必须完好——就地改写会把 <redacted> 真的传给子进程
	if !containsArgSequence(args, "-k", "SECRET-TOKEN-123") {
		t.Fatalf("BuildArgs() result was mutated (token), got %v", args)
	}
	if !containsArgSequence(args, "-w", "SECRET-PASSWORD-456") {
		t.Fatalf("BuildArgs() result was mutated (password), got %v", args)
	}
}

// 非 VNT 路径：BuildArgs 原样返回 c.Args，手写的 -k/-w 同样要脱敏
func TestRedactedArgsCoversNonVNTPath(t *testing.T) {
	t.Parallel()

	args := []string{"--some-flag", "-k", "handwritten-token", "-w", "handwritten-pass", "--tail"}
	red := redactedArgs(args)

	joined := strings.Join(red, " ")
	for _, secret := range []string{"handwritten-token", "handwritten-pass"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("redactedArgs() leaked %q on non-VNT path: %v", secret, red)
		}
	}
	// 非敏感项保留
	assertArgSequence(t, red, "--some-flag")
	assertArgSequence(t, red, "--tail")
}

// 末尾孤立标志（无值）不得 panic 或越界
func TestRedactedArgsHandlesTrailingSensitiveFlag(t *testing.T) {
	t.Parallel()

	red := redactedArgs([]string{"--flag", "-k"})
	if len(red) != 2 {
		t.Fatalf("redactedArgs() = %v, want passthrough of 2 elements", red)
	}
}
