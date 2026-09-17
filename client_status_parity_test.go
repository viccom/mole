package moleAgent_client

import (
	"bytes"
	"log"
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

// tunnel_push 收到校验不通过的项时必须留日志，但**不得改变配置**。
// 降级设计（原「过滤无效项」方案会让脏数据在后续上报表时从服务端
// 持久化中消失，而服务端校验本已把关，收益不抵风险）。
//
// 另有一条源码断言（TestTunnelPushHandlerCallsWarning）确保 handleServerCmd
// 的 tunnel_push 分支真的调用了此函数——单元测试无法覆盖接线是否接上。
func TestWarnInvalidPushedTunnelsLogsButKeepsConfig(t *testing.T) {
	// 替换全局 log 输出，不可并行
	oldW, oldF := log.Writer(), log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
	})

	tunnels := []Tunnel{
		{Name: "good", Type: TunnelTypeTCP, Target: "127.0.0.1:8080"},
		{Name: "bad-scheme", Type: TunnelTypeHTTP, Target: "http://127.0.0.1:8080"},
		{Name: "bad-type", Type: "nonsense", Target: "127.0.0.1:1"},
		{Name: "", Type: TunnelTypeTCP, Target: "127.0.0.1:9"},
	}
	warnInvalidPushedTunnels(tunnels)

	out := buf.String()
	// 三条无效项都要有痕迹
	for _, want := range []string{`"bad-scheme"`, `"bad-type"`, `""`} {
		if !strings.Contains(out, want) {
			t.Errorf("warning log missing entry for %s:\n%s", want, out)
		}
	}
	// 有效项不得产生告警
	if strings.Contains(out, `"good"`) {
		t.Errorf("valid tunnel must not be warned about:\n%s", out)
	}
	// 配置本身不被改动（长度与顺序保持）——这是与「过滤」方案的关键区别
	if len(tunnels) != 4 {
		t.Fatalf("warnInvalidPushedTunnels must not mutate the slice, len = %d", len(tunnels))
	}
}

// 全部合法时不产生任何日志（防刷屏：tunnel_push 每次重连都会来一次）
func TestWarnInvalidPushedTunnelsSilentWhenAllValid(t *testing.T) {
	oldW, oldF := log.Writer(), log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
	})

	warnInvalidPushedTunnels([]Tunnel{
		{Name: "a", Type: TunnelTypeTCP, Target: "127.0.0.1:8080"},
		{Name: "b", Type: TunnelTypeSer2MQ, Target: "COM3"},
	})
	if buf.Len() != 0 {
		t.Fatalf("valid tunnels must not log, got %q", buf.String())
	}
}

// 接线守卫：handleServerCmd 的 tunnel_push 分支必须调用告警函数。
// 单元测试只覆盖函数本身，接不接得上需要源码断言（否则删掉调用也不会红）。
func TestTunnelPushHandlerCallsWarning(t *testing.T) {
	src, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatalf("read client.go: %v", err)
	}
	text := string(src)
	idx := strings.Index(text, `case "tunnel_push":`)
	if idx < 0 {
		t.Fatal("tunnel_push branch not found")
	}
	rest := text[idx:]
	if next := strings.Index(rest, `case "tunnel_action":`); next > 0 {
		rest = rest[:next]
	}
	if !strings.Contains(rest, "warnInvalidPushedTunnels(tunnels)") {
		t.Fatal("tunnel_push 分支未调用 warnInvalidPushedTunnels——纵深防御告警未接线")
	}
}
