package moleAgent_client

import (
	"os"
	"path/filepath"
	"testing"

	"moleAgent_client/internal/nodeid"
)

// 以下测试验证根包 New() 对 node.id 文件的集成行为。
// nodeid 包外移后留在此处：它们依赖根包 New/DefaultConfig 与 nodeid 导出 API
// 的组合，属集成测试（nodeid 包内单元测试覆盖解析细节）。

// 配置文件里的 node_id_file 生效：New 应把 node.id 写到该路径
func TestNew_NodeIDFileFromConfig(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "from-config.json")
	cfg := DefaultConfig()
	cfg.NodeIDFile = custom
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("node.id not written to configured path %q: %v", custom, err)
	}
	if c.NodeID() == "" {
		t.Fatal("node ID must be resolved from configured file")
	}
}

// 显式 node_id 优先级最高：不读也不写 node.id 文件
func TestNew_ExplicitNodeIDSkipsFileLogic(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "node.id")
	cfg := DefaultConfig()
	cfg.NodeIDFile = custom
	cfg.NodeID = "custom12"
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if got := c.NodeID(); got != "custom12" {
		t.Fatalf("explicit node ID must win, got %q want custom12", got)
	}
	// 文件不应被显式值写入
	if content, err := os.ReadFile(custom); err == nil && len(content) > 0 {
		t.Fatalf("explicit node ID must not write node.id, got %q", string(content))
	}
}

// 未显式指定时，New 以文件为真相源且在重启间锁定
func TestNew_AutoNodeIDUsesFile(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "node.id")

	cfg1 := DefaultConfig()
	cfg1.NodeIDFile = custom
	c1, err := New(cfg1)
	if err != nil {
		t.Fatalf("first New failed: %v", err)
	}

	cfg2 := DefaultConfig()
	cfg2.NodeIDFile = custom
	c2, err := New(cfg2)
	if err != nil {
		t.Fatalf("second New failed: %v", err)
	}
	if c1.NodeID() != c2.NodeID() {
		t.Fatalf("auto node ID must be locked across restarts: %q vs %q", c1.NodeID(), c2.NodeID())
	}
	if !nodeid.ValidateNodeID(c2.NodeID()) {
		t.Fatalf("resolved node ID invalid: %q", c2.NodeID())
	}
}
