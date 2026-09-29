package tunnelvalidate

import (
	"encoding/json"
	"strings"
	"testing"

	"mole/shared/proto"
)

// 锁统一文案（裁决表）：任何一条变化都意味着双端错误输出变化，必须过 decisions.md
func TestUnifiedMessages(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"name-required", Validate(Tunnel{}, ServerOptions()), "tunnel name is required"},
		{"name-ctrl", Validate(Tunnel{Name: "a\nb"}, ServerOptions()),
			`tunnel name must not contain \x00, \n or \r`},
		{"unknown-type", Validate(Tunnel{Name: "n", Type: "bogus"}, ServerOptions()),
			`unknown tunnel type "bogus"`},
		{"target-required", Validate(Tunnel{Name: "n", Type: proto.TunnelTypeHTTP}, ServerOptions()),
			"tunnel target is required"},
		{"target-format", Validate(Tunnel{Name: "n", Type: proto.TunnelTypeHTTP, Target: "noport"}, ServerOptions()),
			`tunnel target must be host:port format (e.g. 127.0.0.1:8080), got "noport"`},
		{"target-host-empty", Validate(Tunnel{Name: "n", Type: proto.TunnelTypeHTTP, Target: ":8080"}, ServerOptions()),
			`tunnel target host is required, got ":8080"`},
		{"target-port", Validate(Tunnel{Name: "n", Type: proto.TunnelTypeHTTP, Target: "h:70000"}, ServerOptions()),
			`tunnel target port must be 1-65535, got "70000"`},
		{"scheme", Validate(Tunnel{Name: "n", Type: proto.TunnelTypeHTTP, Target: "http://127.0.0.1:8080"}, ClientOptions()),
			`http(s) tunnel target must be host:port without scheme (e.g. 127.0.0.1:8080), got "http://127.0.0.1:8080"`},
		{"listen-range", Validate(Tunnel{Name: "n", Type: proto.TunnelTypeTCP, Target: "127.0.0.1:80", ListenPort: 0}, ServerOptions()),
			"listen_port must be 1-65535, got 0"},
		{"listen-reserved", Validate(Tunnel{Name: "n", Type: proto.TunnelTypeTCP, Target: "127.0.0.1:80", ListenPort: 9983}, ServerOptions()),
			"listen_port 9983 is reserved for the server itself"},
		{"rl-conns", ValidateRateLimit(&RateLimit{MaxConns: 200000}),
			"max_conns must be 1-100000, got 200000"},
		{"rl-bandwidth", ValidateRateLimit(&RateLimit{MaxBandwidth: 1 << 40}),
			"max_bandwidth must be 1-10737418240 bytes/sec, got 1099511627776"},
		{"rl-allzero", ValidateRateLimit(&RateLimit{}),
			"rate_limit must have at least one non-zero field, use null to clear"},
		{"p2p-room", ValidateP2PPara(json.RawMessage(`{"room":"short"}`)),
			"p2p room must be 8-32 chars of [a-zA-Z0-9_-]"},
		{"p2p-mode", ValidateP2PPara(json.RawMessage(`{"room":"abcdefgh","modes":["nope"]}`)),
			`p2p unknown mode "nope"`},
		{"p2p-relay", ValidateP2PPara(json.RawMessage(`{"room":"abcdefgh","modes":["v4-relay"]}`)),
			"p2p modes contains v4-relay but relay_server is empty"},
		{"p2p-mapping-proto", ValidateP2PPara(json.RawMessage(`{"room":"abcdefgh","mappings":[{"protocol":"sctp","local_port":1,"target_host":"h","target_port":2}]}`)),
			"p2p mappings[0].protocol must be tcp or udp"},
		{"p2p-mapping-dup", ValidateP2PPara(json.RawMessage(`{"room":"abcdefgh","mappings":[{"protocol":"tcp","local_port":5,"target_host":"h","target_port":2},{"protocol":"tcp","local_port":5,"target_host":"h","target_port":3}]}`)),
			"p2p mappings[1].local_port 5 duplicated in same para"},
		{"dup-name", ValidateList([]Tunnel{{Name: "a", Type: proto.TunnelTypeTCP, Target: "127.0.0.1:1", ListenPort: 10000}, {Name: "a", Type: proto.TunnelTypeTCP, Target: "127.0.0.1:1", ListenPort: 10001}}, ServerOptions()),
			`duplicate tunnel name "a"`},
		{"dup-port", ValidateList([]Tunnel{{Name: "a", Type: proto.TunnelTypeTCP, Target: "127.0.0.1:1", ListenPort: 10000}, {Name: "b", Type: proto.TunnelTypeTCP, Target: "127.0.0.1:1", ListenPort: 10000}}, ServerOptions()),
			`duplicate listen_port 10000 (tunnel "a" and "b")`},
	}
	for _, c := range cases {
		if c.err == nil || c.err.Error() != c.want {
			t.Errorf("%s:\n  got  %v\n  want %q", c.name, c.err, c.want)
		}
	}
	// p2p JSON 非法：cause 文本来自 encoding/json（跨版本不稳定），前缀断言
	if err := ValidateP2PPara(json.RawMessage(`{bad`)); err == nil ||
		!strings.HasPrefix(err.Error(), "p2p para is not valid JSON: ") {
		t.Errorf("p2p-json: got %v", err)
	}
}

// 双端语义分叉保留（裁决表参数化）：同一输入在 Server/Client opts 下判定不同
func TestOptionsFork(t *testing.T) {
	// 四类本地隧道空 target：server 放行、client 拒
	for _, ty := range []string{proto.TunnelTypeSer2MQ, proto.TunnelTypeSer2TCP, proto.TunnelTypeSer2UDP, proto.TunnelTypeWebSSH} {
		tv := Tunnel{Name: "n", Type: ty}
		if err := Validate(tv, ServerOptions()); err != nil {
			t.Errorf("server should allow empty target for %s: %v", ty, err)
		}
		if err := Validate(tv, ClientOptions()); err == nil || err.Error() != "tunnel target is required" {
			t.Errorf("client should reject empty target for %s, got %v", ty, err)
		}
	}
	// vpn-manager/p2p 空 target：双端都放行
	for _, ty := range []string{proto.TunnelTypeVPNMgr, proto.TunnelTypeP2P} {
		tv := Tunnel{Name: "n", Type: ty, Para: validP2PPara}
		if err := Validate(tv, ServerOptions()); err != nil {
			t.Errorf("server should allow empty target for %s: %v", ty, err)
		}
		if err := Validate(tv, ClientOptions()); err != nil {
			t.Errorf("client should allow empty target for %s: %v", ty, err)
		}
	}
	// scheme：双端判定一致都拒绝（net.SplitHostPort 对多冒号报错——差异清单
	// a-5「server 放行」论断经实测推翻），差异仅在文案：server 报 format 错、
	// client 报更明确的 scheme 错（RejectSchemeInTarget 保留该文案分叉）
	tv := Tunnel{Name: "n", Type: proto.TunnelTypeHTTP, Target: "http://127.0.0.1:8080"}
	if err := Validate(tv, ServerOptions()); err == nil || !strings.Contains(err.Error(), "must be host:port format") {
		t.Errorf("server should reject scheme target with format error, got %v", err)
	}
	if err := Validate(tv, ClientOptions()); err == nil || !strings.Contains(err.Error(), "without scheme") {
		t.Errorf("client should reject scheme target with scheme error, got %v", err)
	}
}

var validP2PPara = json.RawMessage(`{"room":"abcdefgh"}`)

// 上限常量单源锁值（防止未来漂移）
func TestBounds(t *testing.T) {
	if MaxRegisterTunnels != 256 {
		t.Errorf("MaxRegisterTunnels = %d, want 256", MaxRegisterTunnels)
	}
	if MaxConnsUpperBound != 100000 {
		t.Errorf("MaxConnsUpperBound = %d, want 100000", MaxConnsUpperBound)
	}
	if MaxBandwidthUpperBound != 10737418240 {
		t.Errorf("MaxBandwidthUpperBound = %d, want 10737418240", MaxBandwidthUpperBound)
	}
	// 边界内合法：恰好上限
	if err := ValidateRateLimit(&RateLimit{MaxConns: 100000, MaxBandwidth: 10737418240}); err != nil {
		t.Errorf("upper bound should be inclusive: %v", err)
	}
}

// 基本放行路径：标准四类合法样本双 opts 均通过
func TestHappyPath(t *testing.T) {
	for _, ty := range []string{proto.TunnelTypeHTTP, proto.TunnelTypeHTTPS, proto.TunnelTypeTCP, proto.TunnelTypeUDP} {
		tv := Tunnel{Name: "n", Type: ty, Target: "127.0.0.1:8080", ListenPort: 19100}
		if err := Validate(tv, ServerOptions()); err != nil {
			t.Errorf("server happy path %s: %v", ty, err)
		}
		if err := Validate(tv, ClientOptions()); err != nil {
			t.Errorf("client happy path %s: %v", ty, err)
		}
	}
}
