package moleAgent_client

import "testing"

func TestDefaultBuiltinHTTPPort(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if cfg.BuiltinHTTP != "127.0.0.1:59870" {
		t.Fatalf("DefaultConfig().BuiltinHTTP = %q, want %q", cfg.BuiltinHTTP, "127.0.0.1:59870")
	}
}

func TestApplyDefaultsUsesNewBuiltinHTTPPort(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	cfg.ApplyDefaults()
	if cfg.BuiltinHTTP != "127.0.0.1:59870" {
		t.Fatalf("ApplyDefaults().BuiltinHTTP = %q, want %q", cfg.BuiltinHTTP, "127.0.0.1:59870")
	}
}
