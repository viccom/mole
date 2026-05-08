package vpn

import "testing"

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
