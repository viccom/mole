package proto

import (
	"reflect"
	"testing"
)

// dummyTunnel 仅用于实例化泛型 ControlCmd 做反射断言
// （类型参数不影响字段集与 json tag）
type dummyTunnel struct {
	Name string `json:"name"`
}

// TestCommandWords 逐个锁定全部命令字 / 响应字 / 认证 key 的字符串值
func TestCommandWords(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		// C→S 命令字
		{"CmdRegister", CmdRegister, "register"},
		{"CmdPing", CmdPing, "ping"},
		{"CmdTunnelUpdate", CmdTunnelUpdate, "tunnel_update"},
		{"CmdSysInfo", CmdSysInfo, "sysinfo"},
		{"CmdTunnelStatus", CmdTunnelStatus, "tunnel_status"},
		{"CmdP2PSignalToken", CmdP2PSignalToken, "p2p_signal_token"},
		// S→C 命令字
		{"CmdTunnelPush", CmdTunnelPush, "tunnel_push"},
		{"CmdTunnelAction", CmdTunnelAction, "tunnel_action"},
		{"CmdRestart", CmdRestart, "restart"},
		// 响应 cmd
		{"RespOK", RespOK, "ok"},
		{"RespErr", RespErr, "err"},
		{"RespPong", RespPong, "pong"},
		// 认证行 JSON key
		{"AuthKeyProof", AuthKeyProof, "proof"},
		{"AuthKeyToken", AuthKeyToken, "token"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestStreamPrefixes 锁定流前缀字节
func TestStreamPrefixes(t *testing.T) {
	if PrefixTCPUDP != 0x00 {
		t.Errorf("PrefixTCPUDP = %#x, want 0x00", PrefixTCPUDP)
	}
	if PrefixWebSSH != 0x01 {
		t.Errorf("PrefixWebSSH = %#x, want 0x01", PrefixWebSSH)
	}
}

// TestLimitsAndPath 锁定大小上限与 WS 升级路径
func TestLimitsAndPath(t *testing.T) {
	if MaxControlMsgSize != 1<<20 {
		t.Errorf("MaxControlMsgSize = %d, want %d", MaxControlMsgSize, 1<<20)
	}
	if MaxAuthLineBytes != 64<<10 {
		t.Errorf("MaxAuthLineBytes = %d, want %d", MaxAuthLineBytes, 64<<10)
	}
	if WSUpgradePath != "/ws" {
		t.Errorf("WSUpgradePath = %q, want %q", WSUpgradePath, "/ws")
	}
}

// TestTunnelTypes 逐个锁定 10 个隧道类型枚举值
func TestTunnelTypes(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"TunnelTypeHTTP", TunnelTypeHTTP, "http"},
		{"TunnelTypeHTTPS", TunnelTypeHTTPS, "https"},
		{"TunnelTypeTCP", TunnelTypeTCP, "tcp"},
		{"TunnelTypeUDP", TunnelTypeUDP, "udp"},
		{"TunnelTypeSer2MQ", TunnelTypeSer2MQ, "ser2mq"},
		{"TunnelTypeSer2TCP", TunnelTypeSer2TCP, "ser2tcp"},
		{"TunnelTypeSer2UDP", TunnelTypeSer2UDP, "ser2udp"},
		{"TunnelTypeVPNMgr", TunnelTypeVPNMgr, "vpn-manager"},
		{"TunnelTypeWebSSH", TunnelTypeWebSSH, "webssh"},
		{"TunnelTypeP2P", TunnelTypeP2P, "p2p"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// assertTags 断言结构体每个字段的 json tag（含字段顺序与个数）与预期完全一致
func assertTags[T any](t *testing.T, want []string) {
	t.Helper()
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if typ.NumField() != len(want) {
		t.Fatalf("%s: 字段数 = %d, want %d", typ.Name(), typ.NumField(), len(want))
	}
	for i, w := range want {
		if tag := typ.Field(i).Tag.Get("json"); tag != w {
			t.Errorf("%s 字段 %s 的 json tag = %q, want %q", typ.Name(), typ.Field(i).Name, tag, w)
		}
	}
}

// TestControlCmdTags 锁定 ControlCmd 的 11 个 json tag（含顺序）
func TestControlCmdTags(t *testing.T) {
	assertTags[ControlCmd[dummyTunnel]](t, []string{
		"cmd",
		"node_id,omitempty",
		"name,omitempty",
		"token,omitempty",
		"tunnels,omitempty",
		"ts,omitempty",
		"action,omitempty",
		"delay_seconds,omitempty",
		"reason,omitempty",
		"statuses,omitempty",
		"sysinfo,omitempty",
	})
}

// TestControlResponseTags 锁定 ControlResponse 的 5 个 json tag
// （enc,omitempty 为方案 B 新增能力宣告位，nil 时键缺席，旧端零感知）
func TestControlResponseTags(t *testing.T) {
	assertTags[ControlResponse](t, []string{
		"cmd",
		"msg,omitempty",
		"ts,omitempty",
		"data,omitempty",
		"enc,omitempty",
	})
}

// TestTunnelStatusTags 锁定 TunnelStatus 的 12 个 json tag
func TestTunnelStatusTags(t *testing.T) {
	assertTags[TunnelStatus](t, []string{
		"name",
		"type",
		"running",
		"connected,omitempty",
		"serial_open,omitempty",
		"mqtt_connected,omitempty",
		"clients,omitempty",
		"pid,omitempty",
		"uptime_seconds,omitempty",
		"bytes_in,omitempty",
		"bytes_out,omitempty",
		"error,omitempty",
	})
}

// TestSysInfoTags 锁定 SysInfo 的 8 个 json tag
func TestSysInfoTags(t *testing.T) {
	assertTags[SysInfo](t, []string{
		"os,omitempty",
		"hostname,omitempty",
		"uptime_seconds,omitempty",
		"go_version,omitempty",
		"agent_version,omitempty",
		"num_cpu,omitempty",
		"mem_total_mb,omitempty",
		"mem_used_mb,omitempty",
	})
}

// TestP2PSignalTokenRespTags 锁定 P2PSignalTokenResp 的 6 个 json tag
func TestP2PSignalTokenRespTags(t *testing.T) {
	assertTags[P2PSignalTokenResp](t, []string{
		"cmd",
		"ok",
		"error,omitempty",
		"username,omitempty",
		"password,omitempty",
		"expires_at,omitempty",
	})
}
