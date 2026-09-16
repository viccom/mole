package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func p2pParaJSON(t *testing.T, s string) json.RawMessage {
	t.Helper()
	return json.RawMessage(s)
}

// p2p Para 校验是 p2p 隧道落库前的第一道闸：room 是共享密钥材料
// （知道 room 即可加入信令并推导 payload key），格式必须严格把关。
// 测试向量来自协议契约 §0.3 共享表（I* = 必拒，V* = 必收），
// 与客户端测试逐条对应，两端通过/拒绝结果必须一致。
func TestValidateP2PPara(t *testing.T) {
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
			err := ValidateP2PPara(p2pParaJSON(t, tt.para))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateP2PPara(%s) error = %v, wantErr %v", tt.para, err, tt.wantErr)
			}
		})
	}
}

// nil Para 必须拒绝：p2p 隧道没有 room 无法配对，等价于配置缺失
func TestValidateP2PParaNilPara(t *testing.T) {
	if err := ValidateP2PPara(nil); err == nil {
		t.Fatal("nil para must be rejected")
	}
}

// P2PRoom 供 service 层配对校验提取 room，解析失败必须显式报错而非返回空串
func TestP2PRoom(t *testing.T) {
	room, err := P2PRoom(json.RawMessage(`{"room":"abcd1234xyz"}`))
	if err != nil {
		t.Fatalf("P2PRoom: %v", err)
	}
	if room != "abcd1234xyz" {
		t.Fatalf("P2PRoom = %q", room)
	}
	if _, err := P2PRoom(json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing room must error")
	}
	if _, err := P2PRoom(nil); err == nil {
		t.Fatal("nil para must error")
	}
}
