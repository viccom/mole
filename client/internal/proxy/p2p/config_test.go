//go:build p2p

package p2p

import (
	"encoding/json"
	"strings"
	"testing"
)

// FromPara 是服务端 tunnel_push 分发的 Para → 结构体入口：
// room 是密钥材料，校验必须与服务端 core.ValidateP2PPara 同规则。
// 测试向量来自协议契约 §0.3 共享表（I* = 必拒，V* = 必收），
// 与服务端测试逐条对应，两端通过/拒绝结果必须一致（防漂移硬机制）。
func TestFromPara(t *testing.T) {
	tests := []struct {
		name    string
		para    string
		wantErr bool
	}{
		// ── 必收 V1-V7 ──
		{"V1 纯会话端（仅连接参数）", `{"room":"roomOK123456"}`, false},
		{"V2 一组映射", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":18080,"target_host":"127.0.0.1","target_port":80}]}`, false},
		{"V3 多组映射（端口各异）", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":18080,"target_host":"127.0.0.1","target_port":80},{"protocol":"tcp","local_port":18081,"target_host":"127.0.0.1","target_port":81}]}`, false},
		{"V4 带 modes", `{"room":"roomOK123456","modes":["lan","udp-v4"]}`, false},
		{"V5 v4-relay 带 relay_server", `{"room":"roomOK123456","modes":["v4-relay"],"relay_server":"relay.example.com:9999"}`, false},
		{"V6 自定义 mqtt/stun", `{"room":"roomOK123456","mqtt_brokers":["tcp://b:1883"],"stun_servers":["s:3478"]}`, false},
		{"V7 udp 映射", `{"room":"roomOK123456","mappings":[{"protocol":"udp","local_port":53,"target_host":"1.1.1.1","target_port":53}]}`, false},
		{"空 mappings 数组 = 纯会话端", `{"room":"roomOK123456","mappings":[]}`, false},

		// ── 必拒 I1-I12 ──
		{"I1 room 过短（7 字符）", `{"room":"abcd123"}`, true},
		{"I2 room 过长（33 字符）", `{"room":"` + strings.Repeat("a", 33) + `"}`, true},
		{"I3 room 含非法字符（空格）", `{"room":"abcd 1234"}`, true},
		{"I4 room 缺失", `{}`, true},
		{"I5 modes 含未知值", `{"room":"roomOK123456","modes":["bogus"]}`, true},
		{"I6 v4-relay 缺 relay_server", `{"room":"roomOK123456","modes":["v4-relay"]}`, true},
		{"I7 mapping.protocol 非 tcp/udp", `{"room":"roomOK123456","mappings":[{"protocol":"quic","local_port":1,"target_host":"h","target_port":2}]}`, true},
		{"I8 mapping.local_port = 0", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":0,"target_host":"h","target_port":2}]}`, true},
		{"I9 mapping.local_port > 65535", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":70000,"target_host":"h","target_port":2}]}`, true},
		{"I10 mapping.target_port = 0", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":1,"target_host":"h","target_port":0}]}`, true},
		{"I11 mapping.target_host 为空", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":1,"target_host":"","target_port":2}]}`, true},
		{"I12 同一 para 内 local_port 重复", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":18080,"target_host":"a","target_port":1},{"protocol":"udp","local_port":18080,"target_host":"b","target_port":2}]}`, true},

		// ── §0.3 语义内的补充边界 ──
		{"room 含非法字符（中文）", `{"room":"房间号abcd1234"}`, true},
		{"room 含非法字符（感叹号）", `{"room":"abcd1234!"}`, true},
		{"modes 大小写敏感", `{"room":"roomOK123456","modes":["UDP-V4"]}`, true},
		{"mapping.protocol 大小写敏感", `{"room":"roomOK123456","mappings":[{"protocol":"TCP","local_port":1,"target_host":"h","target_port":2}]}`, true},
		{"mapping.target_port > 65535", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":1,"target_host":"h","target_port":70000}]}`, true},
		{"para 非 JSON", `not-json`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FromPara(json.RawMessage(tt.para))
			if (err != nil) != tt.wantErr {
				t.Fatalf("FromPara(%s) error = %v, wantErr %v", tt.para, err, tt.wantErr)
			}
		})
	}
}

// V2 向量解析后的字段值必须逐项落位（不只「没报错」）
func TestFromParaV2Fields(t *testing.T) {
	cfg, err := FromPara(json.RawMessage(`{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":18080,"target_host":"127.0.0.1","target_port":80}]}`))
	if err != nil {
		t.Fatalf("FromPara: %v", err)
	}
	if cfg.Room != "roomOK123456" || len(cfg.Mappings) != 1 {
		t.Fatalf("parsed = %+v", cfg)
	}
	m := cfg.Mappings[0]
	if m.Protocol != "tcp" || m.LocalPort != 18080 || m.TargetHost != "127.0.0.1" || m.TargetPort != 80 {
		t.Fatalf("mapping = %+v", m)
	}
}

// ToPara→FromPara 必须无损往返：客户端 tunnel_update 上行时字段不能丢
func TestToParaRoundtrip(t *testing.T) {
	want := P2PConfig{
		Room:     "roomOK123456",
		Modes:    []string{"lan", "udp-v4"},
		Mappings: []Mapping{{Protocol: "tcp", LocalPort: 18080, TargetHost: "127.0.0.1", TargetPort: 8080}},
	}
	para, err := want.ToPara()
	if err != nil {
		t.Fatalf("ToPara: %v", err)
	}
	got, err := FromPara(para)
	if err != nil {
		t.Fatalf("FromPara(roundtrip): %v", err)
	}
	if got.Room != want.Room || len(got.Mappings) != 1 ||
		got.Mappings[0] != want.Mappings[0] {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got, want)
	}
	if len(got.Modes) != 2 || got.Modes[0] != "lan" {
		t.Fatalf("modes lost in roundtrip: %v", got.Modes)
	}
	// 纯会话端往返：mappings 为空时序列化省略（omitempty），解析回来仍为纯会话端
	pure := P2PConfig{Room: "roomOK123456"}
	ppara, err := pure.ToPara()
	if err != nil {
		t.Fatalf("ToPara(pure): %v", err)
	}
	if strings.Contains(string(ppara), "mappings") {
		t.Fatalf("pure session side must omit mappings: %s", ppara)
	}
	if got, err := FromPara(ppara); err != nil || len(got.Mappings) != 0 {
		t.Fatalf("pure roundtrip = %+v, %v", got, err)
	}
}
