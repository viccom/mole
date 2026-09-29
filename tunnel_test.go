package moleAgent_client

import (
	"encoding/json"
	"strings"
	"testing"
)

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
// Para 必须携带合法 room（REL：room 是共享密钥材料，服务端 ValidateP2PPara
// 同样把关，两端对齐）
func TestValidateP2PTunnel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		tun     Tunnel
		wantErr bool
	}{
		{"纯会话端空 Target 带合法 room 应通过", Tunnel{Name: "p2p-t", Type: TunnelTypeP2P, Para: json.RawMessage(`{"room":"roomOK123456"}`)}, false},
		{"发起端带 Target 带合法 room 应通过", Tunnel{Name: "p2p-t", Type: TunnelTypeP2P, Target: "127.0.0.1:8080", Para: json.RawMessage(`{"room":"roomOK123456"}`)}, false},
		{"缺 Para（无 room）应拒绝", Tunnel{Name: "p2p-t", Type: TunnelTypeP2P}, true},
		{"缺 name 仍应拒绝", Tunnel{Type: TunnelTypeP2P, Para: json.RawMessage(`{"room":"roomOK123456"}`)}, true},
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

// F3 REL-01：TCP/UDP 隧道 listen_port 必须落在 1-65535 且避开服务自身保留
// 端口集合 {9980,9981,9982,9983,1882,1883}（与服务端 core.ValidateTunnelListenPort
// 同规则——客户端放行的畸形值会在 register 时被服务端整单拒绝）
func TestValidateListenPortRangeAndReserved(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"保留端口 9983（api_port）", 9983, true},
		{"保留端口 9980（gateway_port）", 9980, true},
		{"保留端口 9981（control_port）", 9981, true},
		{"保留端口 9982（控制面 WS 附加传输）", 9982, true},
		{"保留端口 1882（mqtt ws_port）", 1882, true},
		{"保留端口 1883（mqtt tcp_port）", 1883, true},
		{"零值", 0, true},
		{"负值", -1, true},
		{"越界 65536", 65536, true},
		{"下边界 1", 1, false},
		{"合法 19100", 19100, false},
		{"上边界 65535", 65535, false},
	}
	for _, c := range cases {
		for _, typ := range []TunnelType{TunnelTypeTCP, TunnelTypeUDP} {
			err := (Tunnel{Name: "t", Type: typ, Target: "127.0.0.1:8080", ListenPort: c.port}).Validate()
			if (err != nil) != c.wantErr {
				t.Errorf("%s %s listen_port=%d: error = %v, wantErr %v", c.name, typ, c.port, err, c.wantErr)
			}
			if c.wantErr && err != nil && !strings.Contains(err.Error(), "listen_port") {
				t.Errorf("%s %s listen_port=%d: 错误文案应指明 listen_port，got %v", c.name, typ, c.port, err)
			}
		}
	}

	// HTTP/HTTPS 走域名路由不占系统端口：listen_port 零值合法（服务端同语义）
	if err := (Tunnel{Name: "h", Type: TunnelTypeHTTP, Target: "127.0.0.1:8080"}).Validate(); err != nil {
		t.Errorf("http 隧道无 listen_port 必须通过: %v", err)
	}
}

// F3：p2p Para 校验对齐服务端 core.ValidateP2PPara——room 是共享密钥材料
// （知道 room 即可加入信令并推导 payload key），modes/mappings 的非法形态
// 在运行期必然失败，必须在配置期拦截
func TestValidateP2PParaRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		para    string
		wantErr bool
	}{
		{"合法最小形态（仅 room）", `{"room":"roomOK123456"}`, false},
		{"合法含 mappings", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":18080,"target_host":"127.0.0.1","target_port":80}]}`, false},
		{"room 太短", `{"room":"short"}`, true},
		{"room 含非法字符", `{"room":"room with space!!"}`, true},
		{"room 缺失", `{}`, true},
		{"非 JSON", `not-json`, true},
		{"未知 mode", `{"room":"roomOK123456","modes":["bogus"]}`, true},
		{"v4-relay 无 relay_server", `{"room":"roomOK123456","modes":["v4-relay"]}`, true},
		{"v4-relay 带 relay_server", `{"room":"roomOK123456","modes":["v4-relay"],"relay_server":"1.2.3.4:9980"}`, false},
		{"mapping protocol 非法", `{"room":"roomOK123456","mappings":[{"protocol":"sctp","local_port":1,"target_host":"h","target_port":1}]}`, true},
		{"mapping local_port 越界", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":0,"target_host":"h","target_port":1}]}`, true},
		{"mapping target_host 空", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":1,"target_host":"","target_port":1}]}`, true},
		{"mapping target_port 越界", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":1,"target_host":"h","target_port":65536}]}`, true},
		{"mapping local_port 重复", `{"room":"roomOK123456","mappings":[{"protocol":"tcp","local_port":18080,"target_host":"h","target_port":1},{"protocol":"udp","local_port":18080,"target_host":"h","target_port":2}]}`, true},
	}
	for _, c := range cases {
		tun := Tunnel{Name: "p", Type: TunnelTypeP2P, Para: json.RawMessage(c.para)}
		err := tun.Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: Validate() error = %v, wantErr %v (para=%s)", c.name, err, c.wantErr, c.para)
		}
	}
}

// F1 列表级校验对齐服务端 core.ValidateTunnels：单条合法但列表内重名或
// TCP/UDP listen_port 重复的提交会被服务端整单拒绝（register/tunnel_update），
// 必须在本地提交前拦下（重名区分大小写精确匹配；HTTP/HTTPS 走域名路由，
// 零 listen_port 不占端口位）
func TestValidateTunnelListRules(t *testing.T) {
	t.Parallel()

	tun := func(name string, typ TunnelType, port int) Tunnel {
		tt := Tunnel{Name: name, Type: typ}
		switch typ {
		case TunnelTypeHTTP, TunnelTypeHTTPS, TunnelTypeTCP, TunnelTypeUDP:
			tt.Target = "127.0.0.1:8080"
			tt.ListenPort = port
		}
		return tt
	}

	cases := []struct {
		name         string
		tunnels      []Tunnel
		wantErr      bool
		wantContains string
	}{
		{"无重复放行", []Tunnel{tun("a", TunnelTypeTCP, 19100), tun("b", TunnelTypeUDP, 19101), tun("c", TunnelTypeHTTP, 0)}, false, ""},
		{"重名拒绝", []Tunnel{tun("dup", TunnelTypeTCP, 19100), tun("dup", TunnelTypeUDP, 19101)}, true, "duplicate tunnel name"},
		{"大小写不同不算重名（区分大小写精确匹配）", []Tunnel{tun("Foo", TunnelTypeTCP, 19100), tun("foo", TunnelTypeTCP, 19101)}, false, ""},
		{"TCP 同 listen_port 拒绝", []Tunnel{tun("a", TunnelTypeTCP, 19100), tun("b", TunnelTypeTCP, 19100)}, true, "duplicate listen_port"},
		{"TCP+UDP 同 listen_port 拒绝", []Tunnel{tun("a", TunnelTypeTCP, 19100), tun("b", TunnelTypeUDP, 19100)}, true, "duplicate listen_port"},
		{"两条 HTTP 零 listen_port 放行", []Tunnel{tun("h1", TunnelTypeHTTP, 0), tun("h2", TunnelTypeHTTP, 0)}, false, ""},
	}
	for _, c := range cases {
		err := validateTunnelList(c.tunnels)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: validateTunnelList() error = %v, wantErr %v", c.name, err, c.wantErr)
			continue
		}
		if c.wantErr && !strings.Contains(err.Error(), c.wantContains) {
			t.Errorf("%s: 错误文案应含 %q, got %q", c.name, c.wantContains, err.Error())
		}
	}

	// 单条非法仍按单条错误报：第二项缺 target（单条错误）且与第一项同
	// listen_port（列表错误），单条校验先短路——与现状逐条 Validate 语义一致
	err := validateTunnelList([]Tunnel{tun("ok", TunnelTypeTCP, 19100), {Name: "bad", Type: TunnelTypeTCP, ListenPort: 19100}})
	if err == nil || strings.Contains(err.Error(), "duplicate") {
		t.Errorf("单条非法应报单条错误而非列表级 duplicate, got %v", err)
	}
}
