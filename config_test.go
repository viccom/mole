package moleAgent_client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// F3 配置加载容错：配置文件里混入非法隧道（如保留端口）时，必须本地剔除
// 并记 WARN 日志（含名称与原因），合法隧道继续——把「服务端整单拒绝 →
// 无限重连 → 整节点瘫」变成「本地剔除 → 节点带合法隧道上线」。
// 捕获全局 log（captureLog 不可并行），故本测试不 t.Parallel()。
func TestLoadConfigFileDropsInvalidTunnels(t *testing.T) {
	buf := captureLog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "client.json")
	content := `{
  "server_addr": "127.0.0.1:9981",
  "token": "tok",
  "tunnels": [
    {"name": "ok-tcp", "type": "tcp", "target": "127.0.0.1:22", "listen_port": 19100},
    {"name": "bad-tcp", "type": "tcp", "target": "127.0.0.1:23", "listen_port": 9983}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if len(cfg.Tunnels) != 1 || cfg.Tunnels[0].Name != "ok-tcp" {
		t.Fatalf("非法隧道必须被剔除、只留合法者，got %d 条: %+v", len(cfg.Tunnels), cfg.Tunnels)
	}
	if !strings.Contains(buf.String(), "bad-tcp") {
		t.Fatalf("剔除必须记 WARN 日志（含隧道名与原因）:\n%s", buf.String())
	}

	// 剔除后的配置能构建客户端，隧道列表只含合法者（register 上报 1 条）
	cfg.NodeID = "Test0001"
	cfg.NodeIDFile = filepath.Join(dir, "node.id")
	// New 会把 NodeIDFile 写进包级全局（SetNodeIDFile），恢复防污染
	//（同 newStatusTestClient 的处理）
	oldFile := nodeIDFile
	t.Cleanup(func() { nodeIDFile = oldFile })

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New（剔除后必须成功）: %v", err)
	}
	defer c.Close()
	if got := len(c.Tunnels()); got != 1 {
		t.Fatalf("客户端隧道列表应只含 1 条合法隧道（register 上报 1 条），got %d", got)
	}
}
