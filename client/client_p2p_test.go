//go:build p2p

package moleAgent_client

import (
	"encoding/json"
	"reflect"
	"testing"

	"moleAgent_client/internal/proxy/p2p"
)

// -tags p2p 构建下 hook 必须提供真实控制器；返回 nil 会让 p2p 隧道静默失联
// （配置分发了但没有任何 Manager 运行）
func TestNewP2PControllerNonNil(t *testing.T) {
	c := &Client{cfg: DefaultConfig()}
	if c.p2p != nil {
		t.Fatal("fresh Client must have nil p2p until New() wires it")
	}
	p := newP2PController(c)
	if p == nil {
		t.Fatal("newP2PController must return non-nil under -tags p2p")
	}
	p.Close()
}

// 控制器过滤逻辑：非 p2p / 已禁用 / Para 非法的隧道不得进入 Manager
// （enabled 且合法的完整链路由 internal/proxy/p2p 的 mock 测试覆盖；
// 这里避免真连公共 broker，不启用任何可运行隧道）
func TestP2PControllerNotifyFiltering(t *testing.T) {
	c := &Client{cfg: DefaultConfig()}
	p := newP2PController(c)
	defer p.Close()

	disabled := false
	enabled := true
	p.Notify([]Tunnel{
		{Name: "web", Type: TunnelTypeHTTP, Target: "127.0.0.1:80", Enabled: &enabled},
		{Name: "p2p-off", Type: TunnelTypeP2P, Enabled: &disabled, Para: []byte(`{"room":"roomok00001"}`)},
		{Name: "p2p-bad", Type: TunnelTypeP2P, Enabled: &enabled, Para: []byte(`not-json`)},
	})
	for _, name := range []string{"web", "p2p-off", "p2p-bad"} {
		if _, err := p.StatusByName(name); err == nil {
			t.Fatalf("tunnel %q must not be tracked", name)
		}
	}
}

// Runtime→P2PRuntime 逐字段转换的 parity 防线：toP2PRuntime 删去直接结构体转换
// 后，两处定义漂移（漏字段/tag 改名/omitempty 不对称）不再有编译期检查，
// 由 JSON 序列化比对兜底。fixture 全字段非零，避开 omitempty 抹零造成的假等
func TestP2PRuntimeConversionParity(t *testing.T) {
	rt := p2p.Runtime{
		Running: true, Connected: true, Mode: "udp-v4",
		LocalAddr: "192.168.1.10:43210", RemoteAddr: "203.0.113.7:51234", PunchMs: 1234,
		ConnectedAt: 1760000000000, Reconnects: 3,
		BytesIn: 111, BytesOut: 222, Error: "boom",
		Mappings: []p2p.MappingStatus{{
			Protocol: "tcp", LocalPort: 18080, TargetHost: "10.0.0.2", TargetPort: 80,
			BytesIn: 33, BytesOut: 44, Up: true, Remote: true, Error: "row err",
		}},
	}
	got := toP2PRuntime(rt)

	a, err := json.Marshal(rt)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var ma, mb map[string]any
	if err := json.Unmarshal(a, &ma); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &mb); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ma, mb) {
		t.Fatalf("JSON drift between p2p.Runtime and P2PRuntime:\nproxy: %s\nroot:  %s", a, b)
	}
}

// 零值路径：空 Mappings / 未连上时序列化不含 mappings / mode / connected_at 键
// （前端据此区分「纯会话端」与「映射未建」）
func TestToP2PRuntimeEmptyFieldsOmitted(t *testing.T) {
	got := toP2PRuntime(p2p.Runtime{Running: true})
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mappings", "mode", "local_addr", "remote_addr", "punch_ms", "connected_at", "error"} {
		if _, ok := m[key]; ok {
			t.Fatalf("zero-value runtime must omit %q, got %s", key, b)
		}
	}
}
