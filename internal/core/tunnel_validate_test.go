package core

import (
	"strings"
	"testing"
)

// 隧道名进入代理协议头 `\x00<name>\n`（tunnel_push 下发后客户端按行解析路由），
// 名称自身含 \x00 \n \r 会使流路由解析错位——客户端 tunnel.Validate 已拒绝，
// 服务端必须在源头（落库/register 同一校验点）同样拒绝（跨端审查 🟡-4）。
func TestValidateTunnelNameControlChars(t *testing.T) {
	base := func(name string) Tunnel {
		return Tunnel{Name: name, Type: TunnelTypeHTTP, Target: "127.0.0.1:8080"}
	}

	// 三个控制字符逐个拒绝（嵌入名字中间，避免首尾歧义）
	for _, ch := range []string{"\x00", "\n", "\r"} {
		if err := ValidateTunnel(base("bad" + ch + "name")); err == nil {
			t.Errorf("ValidateTunnel(name 含 %q) = nil, want reject", ch)
		}
	}

	// 回归：空名仍拒绝
	if err := ValidateTunnel(base("")); err == nil {
		t.Error("ValidateTunnel(空名) = nil, want reject")
	}

	// 回归：正常名仍通过（含中文/连字符/数字）
	for _, name := range []string{"web", "ssh-01", "生产隧道"} {
		if err := ValidateTunnel(base(name)); err != nil {
			t.Errorf("ValidateTunnel(%q) = %v, want nil", name, err)
		}
	}

	// 文案对齐客户端：明确指出三类控制字符
	err := ValidateTunnel(base("x\ny"))
	if err == nil || !strings.Contains(err.Error(), "tunnel name must not contain \\x00, \\n or \\r") {
		t.Errorf("ValidateTunnel(name 含 \\n) 错误文案 = %v, want 含 %q", err, "tunnel name must not contain \\x00, \\n or \\r")
	}

	// ValidateTunnels（register 路径同源）列表内同样拒绝
	if err := ValidateTunnels([]Tunnel{base("list\nname")}); err == nil {
		t.Error("ValidateTunnels(name 含 \\n) = nil, want reject")
	}
}
