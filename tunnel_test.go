package moleAgent_client

import "testing"

// TunnelTypeP2P 常量必须注册为 "p2p"（与服务端 core.TunnelTypeP2P 约定一致，
// 两端靠 JSON 值对齐，常量值漂移会导致 tunnel_push 分发后类型不识别）
func TestTunnelTypeP2PConstant(t *testing.T) {
	t.Parallel()

	if TunnelTypeP2P != "p2p" {
		t.Fatalf("TunnelTypeP2P = %q, want %q", TunnelTypeP2P, "p2p")
	}
}

// p2p 注册进 Validate：合法类型，且允许空 Target。
// p2p 隧道两端不对称：纯会话端没有 Target（target 信息在 Para 里、由发起端指定），
// 因此与 vpn-manager 同享空 Target 豁免，否则纯会话端配置永远过不了校验。
func TestValidateP2PTunnel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		tun     Tunnel
		wantErr bool
	}{
		{"纯会话端空 Target 应通过", Tunnel{Name: "p2p-t", Type: TunnelTypeP2P}, false},
		{"发起端带 Target 也应通过", Tunnel{Name: "p2p-t", Type: TunnelTypeP2P, Target: "127.0.0.1:8080"}, false},
		{"缺 name 仍应拒绝", Tunnel{Type: TunnelTypeP2P}, true},
	}
	for _, c := range cases {
		err := c.tun.Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: Validate() error = %v, wantErr %v", c.name, err, c.wantErr)
		}
	}
}

// p2p 加入豁免名单不得连带放松既有类型的校验（豁免必须精确，防止误开其他类型的空 Target）
func TestValidateExemptionScopedToP2PAndVPN(t *testing.T) {
	t.Parallel()

	if err := (Tunnel{Name: "x", Type: TunnelTypeTCP}).Validate(); err == nil {
		t.Error("tcp 无 Target 仍必须被拒绝（豁免不应波及 tcp）")
	}
	if err := (Tunnel{Name: "x", Type: TunnelTypeVPNMgr}).Validate(); err != nil {
		t.Errorf("vpn-manager 空 Target 豁免被破坏: %v", err)
	}
}
