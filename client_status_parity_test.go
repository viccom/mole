package moleAgent_client

import (
	"bytes"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moleAgent_client/internal/protocol"
	"moleAgent_client/internal/proxy/ser2mq"
)

// captureLog 捕获全局 log 输出，测试结束自动恢复。
// 全局状态捕获不可与并行测试共存——调用方不得 t.Parallel()。
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	oldW, oldF := log.Writer(), log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
	})
	return &buf
}

// newStatusTestClient 构造带真实 manager 的 Client（不建立连接）。
// NodeID 显式指定 + NodeIDFile 指向临时目录：避免写入 $HOME，也跳过
// 硬件指纹探测（New 内部对显式 NodeID 短路持久化）。
func newStatusTestClient(t *testing.T) *Client {
	t.Helper()
	cfg := DefaultConfig()
	cfg.NodeID = "Test0001"
	cfg.NodeIDFile = filepath.Join(t.TempDir(), "node.id")

	// New 会把 cfg.NodeIDFile 写进包级全局（SetNodeIDFile），测试结束后
	// t.TempDir 被删除——不恢复会让后续未显式指定 NodeIDFile 的测试
	// 解析到已删除的路径（独立审查复核轮发现）
	oldFile := nodeIDFile
	t.Cleanup(func() { nodeIDFile = oldFile })

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
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

// ser2mqConnected 的行为真值表（取代原先的源码 grep 断言）：
// 核心语义——Running 锁存位不参与判定，MQTT 掉线必须判离线。
// 这正是本地路径曾经用错的地方（buildTunnelStatus 用了 Running）。
func TestSer2MQConnectedTruthTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		running      bool
		mqtt, serial bool
		want         bool
	}{
		{"全在线", true, true, true, true},
		{"MQTT 掉线（Running 仍锁存 true）", true, false, true, false},
		{"串口关闭", true, true, false, false},
		{"从未启动", false, false, false, false},
	}
	for _, tc := range cases {
		stats := ser2mq.Ser2MQStats{Running: tc.running, MQTTConnected: tc.mqtt, SerialOpen: tc.serial}
		if got := ser2mqConnected(stats); got != tc.want {
			t.Errorf("%s: ser2mqConnected = %v, want %v", tc.name, got, tc.want)
		}
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
	buf := captureLog(t)

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
	buf := captureLog(t)

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

// 服务端拒绝控制命令响应时必须留痕——旧实现丢弃 readResponse 的两个
// 返回值，上报被拒时客户端完全无感。
func TestReadResponseRejectionIsLogged(t *testing.T) {
	buf := captureLog(t)

	logReadResponse("tunnel_status", &protocol.ControlResponse{Cmd: "err", Msg: "too many tunnels"}, nil)
	if !strings.Contains(buf.String(), "too many tunnels") {
		t.Fatalf("server rejection must be logged, got %q", buf.String())
	}

	// 成功响应不得产生日志（防刷屏：心跳周期任务每次都会走到这里）
	buf.Reset()
	logReadResponse("tunnel_status", &protocol.ControlResponse{Cmd: "ok", Msg: "status received"}, nil)
	if buf.Len() != 0 {
		t.Fatalf("ok response must not log, got %q", buf.String())
	}
}

// 读取失败同样必须留痕
func TestReadResponseErrorIsLogged(t *testing.T) {
	buf := captureLog(t)

	logReadResponse("sysinfo", nil, errors.New("read: EOF"))
	if !strings.Contains(buf.String(), "EOF") {
		t.Fatalf("read error must be logged, got %q", buf.String())
	}
}

// writeResp 写失败必须留痕（服务端会等到超时，甚至判本节点失活）
func TestWriteRespLogsWriteFailure(t *testing.T) {
	buf := captureLog(t)

	writeResp(&failingWriter{}, "ok", "tunnels updated")
	if !strings.Contains(buf.String(), "write control response") {
		t.Fatalf("writeResp failure must be logged, got %q", buf.String())
	}

	// 成功时不得产生日志
	buf.Reset()
	var ok bytes.Buffer
	writeResp(&ok, "ok", "tunnels updated")
	if buf.Len() != 0 {
		t.Fatalf("successful writeResp must not log, got %q", buf.String())
	}
	if !strings.Contains(ok.String(), `"cmd":"ok"`) {
		t.Fatalf("response body malformed: %q", ok.String())
	}
}

// failingWriter 永远写失败
type failingWriter struct{}

func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("stream closed") }

// EnableDebugLogging 后 slog.Debug 必须落到标准 log（此前被无条件丢弃）。
// 全局状态，不可并行；用返回的旧级别恢复。
func TestEnableDebugLoggingRoutesSlogDebugToLog(t *testing.T) {
	oldW, oldF := log.Writer(), log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	oldLevel := slog.SetLogLoggerLevel(slog.LevelInfo)
	t.Cleanup(func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
		slog.SetLogLoggerLevel(oldLevel)
	})

	slog.Debug("before-enable marker")
	if buf.Len() != 0 {
		t.Fatalf("slog.Debug must be dropped at Info level, got %q", buf.String())
	}

	EnableDebugLogging()
	slog.Debug("after-enable marker", "k", "v")
	if !strings.Contains(buf.String(), "after-enable marker") {
		t.Fatalf("EnableDebugLogging() did not surface Debug output, got %q", buf.String())
	}
}

// 接线守卫：buildTunnelStatus 的 ser2mq 分支必须调用 ser2mqConnected。
// 行为层无法构造「Running=true 但 MQTT 掉线」的运行时状态（需要真串口
// 与真 broker），未启动时两种语义都为 false——没有本断言，回退到
// stats.Running 不会红。源码断言的脆弱性（重命名/重构会误报）是已知
// 代价，换取回退保护。
func TestBuildTunnelStatusWiresSer2MQConnectedHelper(t *testing.T) {
	src, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatalf("read client.go: %v", err)
	}
	text := string(src)
	fnIdx := strings.Index(text, "func (c *Client) buildTunnelStatus(")
	if fnIdx < 0 {
		t.Fatal("buildTunnelStatus not found")
	}
	body := text[fnIdx:]
	end := strings.Index(body, "\nfunc ")
	if end > 0 {
		body = body[:end]
	}
	seg := body[strings.Index(body, "case TunnelTypeSer2MQ:"):]
	if next := strings.Index(seg, "case TunnelTypeSer2TCP"); next > 0 {
		seg = seg[:next]
	}
	if !strings.Contains(seg, "ser2mqConnected(stats)") {
		t.Fatal("buildTunnelStatus 的 ser2mq 分支未接线 ser2mqConnected（可能回退到了 Running 锁存位）")
	}
	if strings.Contains(seg, "stats.Running") {
		t.Fatal("buildTunnelStatus 的 ser2mq 分支引用了 Running 锁存位")
	}
}
