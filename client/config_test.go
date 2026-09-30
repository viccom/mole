package moleAgent_client

import (
	"moleAgent_client/internal/nodeid"
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
	oldFile := nodeid.NodeIDFile()
	t.Cleanup(func() { nodeid.SetNodeIDFile(oldFile) })

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New（剔除后必须成功）: %v", err)
	}
	defer c.Close()
	if got := len(c.Tunnels()); got != 1 {
		t.Fatalf("客户端隧道列表应只含 1 条合法隧道（register 上报 1 条），got %d", got)
	}
}

// F1 列表级剔除（后到重复者）：服务端 core.ValidateTunnels 对列表内重名与
// TCP/UDP listen_port 重复整单拒绝。dropInvalidTunnels 只逐条校验时，两条
// 各带 listen_port:19100 的合法 TCP 隧道会全部保留 → register 被整单拒绝 →
// 无限重连（正是该函数声称要消灭的病理）。必须剔除后到重复者；被剔除项
// 不占位，其名称/端口可被后续合法项复用。
// 捕获全局 log（captureLog 不可并行），故不 t.Parallel()。
func TestLoadConfigFileDropsDuplicateTunnels(t *testing.T) {
	buf := captureLog(t)

	cases := []struct {
		name    string
		keep    string // 存活隧道的 "name=target" 按序拼接（同时断言存活者与顺序）
		content string
	}{
		{"同 listen_port 的两条 TCP 保留第一条剔除第二条",
			"first=127.0.0.1:22",
			`[{"name":"first","type":"tcp","target":"127.0.0.1:22","listen_port":19100},{"name":"second","type":"tcp","target":"127.0.0.1:23","listen_port":19100}]`},
		{"同名剔除后者（存活者为前者）",
			"dup=127.0.0.1:22",
			`[{"name":"dup","type":"tcp","target":"127.0.0.1:22","listen_port":19100},{"name":"dup","type":"tcp","target":"127.0.0.1:23","listen_port":19101}]`},
		{"被剔除项的端口可被后续合法项复用",
			"B=127.0.0.1:22",
			`[{"name":"A","type":"tcp","target":"bad-target","listen_port":19100},{"name":"B","type":"tcp","target":"127.0.0.1:22","listen_port":19100}]`},
		{"HTTP 两条零 listen_port 都保留",
			"h1=127.0.0.1:8080,h2=127.0.0.1:8081",
			`[{"name":"h1","type":"http","target":"127.0.0.1:8080"},{"name":"h2","type":"http","target":"127.0.0.1:8081"}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "client.json")
			if err := os.WriteFile(path, []byte(`{"tunnels":`+c.content+`}`), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			cfg, err := LoadConfigFile(path)
			if err != nil {
				t.Fatalf("LoadConfigFile: %v", err)
			}
			var got []string
			for _, tun := range cfg.Tunnels {
				got = append(got, tun.Name+"="+tun.Target)
			}
			if strings.Join(got, ",") != c.keep {
				t.Fatalf("存活隧道（名称与顺序）= %v, want %v", got, c.keep)
			}
		})
	}

	// 剔除重复者必须记 WARN 日志：listen_port 冲突要指明冲突对方（kept "first"）
	if !strings.Contains(buf.String(), "duplicate listen_port") || !strings.Contains(buf.String(), "second") || !strings.Contains(buf.String(), `first`) {
		t.Fatalf("listen_port 重复剔除必须记 WARN（含被剔除者与冲突对方名称）:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "duplicate tunnel name") {
		t.Fatalf("重名剔除必须记含关键词的 WARN 日志:\n%s", buf.String())
	}
}

// F2 空 target 豁免：服务端对 ser2mq/ser2tcp/ser2udp/webssh 四类完全不校验
// target（空 target 合法落库，见 serverAllowsEmptyTarget）。配置文件漏写
// target 的这四类隧道必须保留且 Target 仍为空；豁免只免 target 检查——
// 其余规则（rate_limit 等）照常剔除；豁免不外溢到标准四类（tcp 空 target
// 仍剔除）。AddTunnel/UpdateTunnels 的单条严格度不受影响（另一层语义）。
// 捕获全局 log，故不 t.Parallel()。
func TestLoadConfigFileKeepsEmptyTargetLocalTunnels(t *testing.T) {
	buf := captureLog(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "client.json")
	content := `{
  "tunnels": [
    {"name": "mq-local", "type": "ser2mq"},
    {"name": "mq-bad-rate", "type": "ser2mq", "rate_limit": {"max_conns": -1}},
    {"name": "tcp-no-target", "type": "tcp", "listen_port": 19100}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if len(cfg.Tunnels) != 1 || cfg.Tunnels[0].Name != "mq-local" || cfg.Tunnels[0].Target != "" {
		t.Fatalf("空 target 的 ser2mq 必须按原样保留（Target 仍为空），got %+v", cfg.Tunnels)
	}
	if strings.Contains(buf.String(), "mq-local") {
		t.Fatalf("合法空 target ser2mq 不应被剔除告警:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "mq-bad-rate") {
		t.Fatalf("豁免只免 target 检查：非法 rate_limit 仍须剔除告警:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "tcp-no-target") {
		t.Fatalf("豁免不外溢：tcp 空 target 仍须剔除告警:\n%s", buf.String())
	}
}
