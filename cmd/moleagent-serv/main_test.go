package main

import "testing"

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
