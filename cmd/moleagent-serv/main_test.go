package main

import (
	"net/http"
	"testing"
	"time"
)

// SEC-05：flag 与 MA_NODE_TOKEN 均未设置时必须拒绝启动——
// 公开仓库中的默认 token 等于向所有能访问控制端口的人开放节点注册
func TestResolveNodeToken(t *testing.T) {
	t.Run("未配置时返回错误", func(t *testing.T) {
		t.Setenv("MA_NODE_TOKEN", "")
		_, err := resolveNodeToken("")
		if err == nil {
			t.Fatal("expected error when neither flag nor env is set")
		}
	})

	t.Run("flag 优先", func(t *testing.T) {
		t.Setenv("MA_NODE_TOKEN", "env-token")
		got, err := resolveNodeToken("flag-token")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "flag-token" {
			t.Fatalf("flag must take precedence, got %q", got)
		}
	})

	t.Run("env 兜底", func(t *testing.T) {
		t.Setenv("MA_NODE_TOKEN", "env-token")
		got, err := resolveNodeToken("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "env-token" {
			t.Fatalf("expected env-token, got %q", got)
		}
	})
}

// REL-05：网关 HTTP 服务必须带头读取限时（slowloris 防护，与 apiSrv
// 对齐）与空闲超时；且不设整体 ReadTimeout——网关承载 WebSSH/WS 长连接
func TestNewGatewayServerTimeouts(t *testing.T) {
	srv := newGatewayServer(":9980", http.NotFoundHandler())

	if srv.Addr != ":9980" {
		t.Errorf("Addr = %q, want :9980", srv.Addr)
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s", srv.IdleTimeout)
	}
	if srv.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %v, want 0（WebSSH/WS 长连接不设整体读超时）", srv.ReadTimeout)
	}
	if srv.Handler == nil {
		t.Error("Handler must be set")
	}
}
