//go:build p2p

package moleAgent_client

import "testing"

// -tags p2p 构建下 hook 必须提供真实控制器；返回 nil 会让 p2p 隧道静默失联
//（配置分发了但没有任何 Manager 运行）
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
//（enabled 且合法的完整链路由 internal/proxy/p2p 的 mock 测试覆盖；
// 这里避免真连公共 broker，不启用任何可运行隧道）
func TestP2PControllerNotifyFiltering(t *testing.T) {
	c := &Client{cfg: DefaultConfig()}
	p := newP2PController(c)
	defer p.Close()

	disabled := false
	enabled := true
	p.Notify([]Tunnel{
		{Name: "web", Type: TunnelTypeHTTP, Target: "127.0.0.1:80", Enabled: &enabled},
		{Name: "p2p-off", Type: TunnelTypeP2P, Enabled: &disabled, Para: []byte(`{"room":"roomok00001","protocol":"tcp"}`)},
		{Name: "p2p-bad", Type: TunnelTypeP2P, Enabled: &enabled, Para: []byte(`not-json`)},
	})
	for _, name := range []string{"web", "p2p-off", "p2p-bad"} {
		if _, err := p.StatusByName(name); err == nil {
			t.Fatalf("tunnel %q must not be tracked", name)
		}
	}
}
