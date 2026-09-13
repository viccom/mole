//go:build p2p

package p2p

import (
	"encoding/json"
	"testing"
)

// FromPara 是服务端 tunnel_push 分发的 Para → 结构体入口：
// room 是密钥材料（校验必须与服务端 core.ValidateP2PPara 同规则），
// 发起端/纯会话端不对称语义在此把关。
func TestFromPara(t *testing.T) {
	valid := func(room string) string {
		return `{"room":"` + room + `","modes":["lan","udp-v4"],"protocol":"tcp","local_port":18080,"target_host":"127.0.0.1","target_port":8080}`
	}

	t.Run("发起端合法", func(t *testing.T) {
		cfg, err := FromPara(json.RawMessage(valid("roomOK123456")))
		if err != nil {
			t.Fatalf("FromPara: %v", err)
		}
		if cfg.Room != "roomOK123456" || cfg.Protocol != "tcp" || cfg.LocalPort != 18080 ||
			cfg.TargetHost != "127.0.0.1" || cfg.TargetPort != 8080 {
			t.Fatalf("parsed = %+v", cfg)
		}
	})

	t.Run("纯会话端合法", func(t *testing.T) {
		cfg, err := FromPara(json.RawMessage(`{"room":"roomOK123456","protocol":"udp"}`))
		if err != nil {
			t.Fatalf("FromPara: %v", err)
		}
		if cfg.TargetHost != "" || cfg.TargetPort != 0 {
			t.Fatalf("pure session side must have no target: %+v", cfg)
		}
	})

	t.Run("room 校验", func(t *testing.T) {
		bad := []string{
			`{"room":"ab","protocol":"tcp"}`,            // 过短
			`{"room":"a23456789!","protocol":"tcp"}`,    // 非法字符
			`{"protocol":"tcp"}`,                        // 缺失
		}
		for _, para := range bad {
			if _, err := FromPara(json.RawMessage(para)); err == nil {
				t.Errorf("room invalid case must fail: %s", para)
			}
		}
	})

	t.Run("modes 未知值拒绝", func(t *testing.T) {
		if _, err := FromPara(json.RawMessage(`{"room":"roomOK123456","modes":["tcp-v5"],"protocol":"tcp"}`)); err == nil {
			t.Fatal("unknown mode must be rejected")
		}
	})

	t.Run("protocol 恒校验", func(t *testing.T) {
		if _, err := FromPara(json.RawMessage(`{"room":"roomOK123456"}`)); err == nil {
			t.Fatal("missing protocol must be rejected")
		}
	})

	t.Run("发起端端口必填", func(t *testing.T) {
		if _, err := FromPara(json.RawMessage(`{"room":"roomOK123456","protocol":"tcp","target_host":"127.0.0.1","target_port":80}`)); err == nil {
			t.Fatal("initiator without local_port must be rejected")
		}
		if _, err := FromPara(json.RawMessage(`{"room":"roomOK123456","protocol":"tcp","local_port":18080,"target_host":"127.0.0.1"}`)); err == nil {
			t.Fatal("initiator without target_port must be rejected")
		}
	})

	t.Run("纯会话端 target_port 必须为 0", func(t *testing.T) {
		if _, err := FromPara(json.RawMessage(`{"room":"roomOK123456","protocol":"tcp","target_port":8080}`)); err == nil {
			t.Fatal("pure session side with target_port must be rejected")
		}
	})
}

// ToPara→FromPara 必须无损往返：客户端 tunnel_update 上行时字段不能丢
func TestToParaRoundtrip(t *testing.T) {
	want := P2PConfig{
		Room:       "roomOK123456",
		Modes:      []string{"lan", "udp-v4"},
		Protocol:   "tcp",
		LocalPort:  18080,
		TargetHost: "127.0.0.1",
		TargetPort: 8080,
	}
	para, err := want.ToPara()
	if err != nil {
		t.Fatalf("ToPara: %v", err)
	}
	got, err := FromPara(para)
	if err != nil {
		t.Fatalf("FromPara(roundtrip): %v", err)
	}
	if got.Room != want.Room || got.Protocol != want.Protocol || got.LocalPort != want.LocalPort ||
		got.TargetHost != want.TargetHost || got.TargetPort != want.TargetPort {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got, want)
	}
	if len(got.Modes) != 2 || got.Modes[0] != "lan" {
		t.Fatalf("modes lost in roundtrip: %v", got.Modes)
	}
}
