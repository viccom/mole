//go:build p2p

package engine

import (
	"testing"

	"moleAgent_client/internal/p2p/easyp2p"
)

func TestParseModes_Default(t *testing.T) {
	got, err := ParseModes("")
	if err != nil {
		t.Fatal(err)
	}
	// tcp-v4 不在默认链（国内 <10% 成功率 + TCP SYN 爆破致网络瘫痪），可手选
	if len(got) != 4 || got[0] != "lan" || got[1] != "tcp-v6" || got[2] != "udp-v6" || got[3] != "udp-v4" {
		t.Fatalf("default chain wrong: %v", got)
	}
}

func TestParseModes_Explicit(t *testing.T) {
	got, _ := ParseModes("lan,tcp-v6")
	if len(got) != 2 || got[0] != "lan" || got[1] != "tcp-v6" {
		t.Fatalf("got %v", got)
	}
}

func TestParseModes_AllowsRelay(t *testing.T) {
	got, err := ParseModes("v4-relay")
	if err != nil {
		t.Fatalf("v4-relay should be allowed: %v", err)
	}
	if len(got) != 1 || got[0] != "v4-relay" {
		t.Fatalf("got %v", got)
	}
}

func TestDefaultModes_ExcludesRelay(t *testing.T) {
	got, _ := ParseModes("")
	for _, m := range got {
		if m == "v4-relay" {
			t.Fatal("default chain must not include v4-relay")
		}
	}
}

func TestParseModes_Unknown(t *testing.T) {
	if _, err := ParseModes("bogus"); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestParseModes_Duplicate(t *testing.T) {
	if _, err := ParseModes("lan,lan"); err == nil {
		t.Fatal("expected error for duplicate")
	}
}

// TestSetServers 验证前端填的自定义服务器能真正注入 easyp2p 包级全局
// （Easy_P2P_MP 内部读全局，不接收参数）。这是「自定义 STUN/MQTT 生效」的唯一入口——
// 若 SetServers 接错地方（如写到无人读的 Deps 字段），此测试会失败暴露。
func TestSetServers(t *testing.T) {
	origSTUN := append([]string(nil), easyp2p.STUNServers...)
	origMQTT := append([]string(nil), easyp2p.MQTTBrokerServers...)
	defer func() { // 还原全局，避免污染同包其他测试
		easyp2p.STUNServers = origSTUN
		easyp2p.MQTTBrokerServers = origMQTT
	}()

	SetServers([]string{"custom-stun:3478"}, []string{"tcp://custom-mqtt:1883"})
	if len(easyp2p.STUNServers) != 1 || easyp2p.STUNServers[0] != "custom-stun:3478" {
		t.Fatalf("STUN 未注入到 easyp2p 全局: %v", easyp2p.STUNServers)
	}
	if len(easyp2p.MQTTBrokerServers) != 1 || easyp2p.MQTTBrokerServers[0] != "tcp://custom-mqtt:1883" {
		t.Fatalf("MQTT 未注入到 easyp2p 全局: %v", easyp2p.MQTTBrokerServers)
	}

	// 空入参不覆盖（保留上一行已注入的值）——保证 connection.go 传空 custom 时
	// 不会清空 easyp2p 内置默认。
	SetServers(nil, nil)
	if len(easyp2p.STUNServers) != 1 || easyp2p.STUNServers[0] != "custom-stun:3478" {
		t.Fatalf("空入参不应覆盖已注入值: %v", easyp2p.STUNServers)
	}
}

// TestDefaultServers 验证默认列表 getter 返回 easyp2p 全局的拷贝（外部修改不影响全局）。
func TestDefaultServers(t *testing.T) {
	stun := DefaultSTUNServers()
	mqtt := DefaultMQTTBrokers()
	if len(stun) != len(easyp2p.STUNServers) || len(mqtt) != len(easyp2p.MQTTBrokerServers) {
		t.Fatalf("default 列表长度不一致: stun=%d/%d mqtt=%d/%d",
			len(stun), len(easyp2p.STUNServers), len(mqtt), len(easyp2p.MQTTBrokerServers))
	}
	// 改返回值不应影响全局（拷贝语义）
	if len(stun) > 0 {
		stun[0] = "tampered"
		if easyp2p.STUNServers[0] == "tampered" {
			t.Fatal("DefaultSTUNServers 应返回拷贝，外部修改泄漏到全局")
		}
	}
}
