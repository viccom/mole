package moleAgent_client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newStatusTestClient 构造带真实 manager 的 Client（不建立连接）。
// NodeID 显式指定 + NodeIDFile 指向临时目录：避免写入 $HOME，也跳过
// 硬件指纹探测（New 内部对显式 NodeID 短路持久化）。
func newStatusTestClient(t *testing.T) *Client {
	t.Helper()
	cfg := DefaultConfig()
	cfg.NodeID = "Test0001"
	cfg.NodeIDFile = filepath.Join(t.TempDir(), "node.id")

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// 两条状态收集路径对同一 ser2mq 隧道必须给出一致的 connected 语义。
//
// 回归背景：collectTunnelStatuses（上报服务端）用
// MQTTConnected && SerialOpen，而 buildTunnelStatus（本地 REST /api/tunnels）
// 用 Running——同一个隧道在 admin 与节点本地页面显示不同的连通状态，
// 且本地页面会与同一响应里的 mqtt_connected 徽章自相矛盾。
// Running 只是「曾启动成功」的锁存位，不是在线标志。
func TestSer2MQConnectedSemanticsMatchAcrossPaths(t *testing.T) {
	c := newStatusTestClient(t)

	enabled := true
	c.mu.Lock()
	c.tunnels = []Tunnel{{
		Name:    "s1",
		Type:    TunnelTypeSer2MQ,
		Target:  "COM3",
		Enabled: &enabled,
		Para: []byte(`{
			"broker":"mqtt://b:1883",
			"secret":"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
			"serial":{"port":"COM3","baudrate":9600,"databits":8,"stopbits":1,"parity":"N","timeout":100}
		}`),
	}}
	c.mu.Unlock()

	// 隧道未启动：两条路径都必须报 false
	var fromAdmin, fromLocal bool
	var foundAdmin, foundLocal bool
	for _, st := range c.collectTunnelStatuses() {
		if st.Name == "s1" {
			fromAdmin, foundAdmin = st.Connected, true
		}
	}
	for _, ts := range c.AllTunnelStatus() {
		if ts.Name == "s1" {
			fromLocal, foundLocal = ts.Connected, true
		}
	}
	if !foundAdmin || !foundLocal {
		t.Fatalf("tunnel not found in status output (admin=%v local=%v)", foundAdmin, foundLocal)
	}
	if fromAdmin != fromLocal {
		t.Fatalf("connected 语义分歧: admin=%v local=%v", fromAdmin, fromLocal)
	}
	if fromLocal || fromAdmin {
		t.Fatalf("隧道未启动时两条路径都必须报未连通: admin=%v local=%v", fromAdmin, fromLocal)
	}
}

// buildTunnelStatus 的 ser2mq 分支不得再引用 Running 锁存位。
// 这是上一条的语义守卫：若有人把实现改回 stats.Running，本测试通过
// 源码断言失败——因为无法在不导出 ser2mq.Handler 内部的前提下构造
// 「Running=true 但 MQTT 掉线」的运行时状态。
func TestSer2MQLocalStatusUsesConjunctionNotRunning(t *testing.T) {
	src, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatalf("read client.go: %v", err)
	}
	text := string(src)
	// 先定位 buildTunnelStatus 函数体——`case TunnelTypeSer2MQ:` 在
	// collectTunnelStatuses 里也出现，必须从正确的函数起算
	fnIdx := strings.Index(text, "func (c *Client) buildTunnelStatus(")
	if fnIdx < 0 {
		t.Fatal("buildTunnelStatus function not found")
	}
	body := text[fnIdx:]
	idx := strings.Index(body, "case TunnelTypeSer2MQ:")
	if idx < 0 {
		t.Fatal("ser2mq branch not found inside buildTunnelStatus")
	}
	rest := body[idx:]
	if next := strings.Index(rest, "case TunnelTypeSer2TCP"); next > 0 {
		rest = rest[:next]
	}
	if strings.Contains(rest, "ts.Connected = stats.Running") {
		t.Fatal("buildTunnelStatus 的 ser2mq 分支回退到了 Running 锁存位，应为 MQTTConnected && SerialOpen")
	}
	if !strings.Contains(rest, "MQTTConnected && stats.SerialOpen") {
		t.Fatal("buildTunnelStatus 的 ser2mq 分支缺少合取语义")
	}
}
