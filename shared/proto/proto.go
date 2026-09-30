// Package proto 定义跨端（server / client）共享的控制面协议常量与纯传输
// 结构体——只放「双端必须逐字节一致的值」，不含任何行为函数。
//
// 接入方式：双端以类型别名（type X = proto.X）与常量 re-export
// （const X = proto.X）消费本包，保持各自既有 API 面与调用方零改动；
// 值的正确性由 proto_test.go 的锁值单测兜底（命令字、json tag、前缀
// 字节逐项断言，防止未来漂移）。
package proto

import "encoding/json"

// ---------------------------------------------------------------------------
// 控制命令字（控制流 JSON 的 "cmd" 字段取值）
// ---------------------------------------------------------------------------

// C→S 命令（客户端 → 服务端）
const (
	CmdRegister       = "register"         // 注册节点（携带隧道列表与系统信息）
	CmdPing           = "ping"             // 心跳（Ts 为 Unix 毫秒，用于 RTT 测量）
	CmdTunnelUpdate   = "tunnel_update"    // 客户端主动上报隧道配置快照
	CmdSysInfo        = "sysinfo"          // 系统信息上报
	CmdTunnelStatus   = "tunnel_status"    // 隧道运行时状态上报（Statuses 列表）
	CmdP2PSignalToken = "p2p_signal_token" // 请求 P2P 信令凭据（响应见 P2PSignalTokenResp）
)

// S→C 命令（服务端 → 客户端）
const (
	CmdTunnelPush   = "tunnel_push"   // 推送隧道配置（客户端回 ok）
	CmdTunnelAction = "tunnel_action" // 隧道操作（Action: start/stop/restart）
	CmdRestart      = "restart"       // 节点级重启（Delay 延迟秒数 + Reason 原因）
)

// 控制响应 "cmd" 字段取值（ControlResponse.Cmd）
const (
	RespOK   = "ok"   // 成功
	RespErr  = "err"  // 失败（Msg 携带原因）
	RespPong = "pong" // ping 应答（Ts 原样回传，用于 RTT）
)

// ---------------------------------------------------------------------------
// 认证行 JSON key（预认证阶段 challenge 之后客户端发送的单行 JSON）
// ---------------------------------------------------------------------------

// 双格式（SEC-01）：新客户端发 {"proof"}（challenge-response HMAC，proof 绑定
// 本次连接的一次性 challenge，明文 token 不上线）；旧客户端发 {"token"}
// （明文 legacy 格式，服务端兼容期接受，由 legacy_format_enabled 开关收口）。
// 注意：消费侧 struct tag 只能是字面量（Go 不支持常量进 tag），本常量供
// map key / 测试锁值等场景使用。
const (
	AuthKeyProof = "proof" // 新格式：HMAC-SHA256 hex
	AuthKeyToken = "token" // legacy 格式：明文 token（仅服务端接受）
)

// ---------------------------------------------------------------------------
// 流前缀字节（smux 数据流首字节，客户端 dispatchStream 据此路由）
// ---------------------------------------------------------------------------

const (
	PrefixTCPUDP byte = 0x00 // TCP/UDP 代理流：PrefixTCPUDP + tunnel name + '\n'
	PrefixWebSSH byte = 0x01 // WebSSH 代理流：PrefixWebSSH + tunnel name + '\n'
)

// ---------------------------------------------------------------------------
// 大小上限与路径
// ---------------------------------------------------------------------------

const (
	MaxControlMsgSize = 1 << 20  // 单条控制消息（JSON）的大小上限：1MB
	MaxAuthLineBytes  = 64 << 10 // 预认证 auth 行的长度上限：64KB
	WSUpgradePath     = "/ws"    // WebSocket 控制面 HTTP 升级路径
)

// ---------------------------------------------------------------------------
// 隧道类型枚举（跨端单源）
// ---------------------------------------------------------------------------

// TunnelType 的 10 个合法取值。声明为 untyped 常量：双端各自定义本地
// TunnelType 类型（均为 string 派生），以 const TunnelTypeX TunnelType =
// proto.TunnelTypeX 形式 re-export，值由此处单源锁定。
const (
	TunnelTypeHTTP    = "http"        // HTTP 域名路由
	TunnelTypeHTTPS   = "https"       // HTTPS 域名路由
	TunnelTypeTCP     = "tcp"         // TCP 网关端口监听
	TunnelTypeUDP     = "udp"         // UDP 网关端口监听
	TunnelTypeSer2MQ  = "ser2mq"      // 串口转 MQTT
	TunnelTypeSer2TCP = "ser2tcp"     // 串口转 TCP
	TunnelTypeSer2UDP = "ser2udp"     // 串口转 UDP
	TunnelTypeVPNMgr  = "vpn-manager" // VPN 程序管理
	TunnelTypeWebSSH  = "webssh"      // WebSSH 远程终端
	TunnelTypeP2P     = "p2p"         // P2P 直连隧道（客户端需 -tags p2p 构建才运行）
)

// ---------------------------------------------------------------------------
// Tunnel 载荷 wire 契约（三镜像 struct 的字段名 + json tag 单源锁定）
// ---------------------------------------------------------------------------

// TunnelWireFields 是 Tunnel 载荷在 wire 上的字段契约（"字段名:json tag"，
// 顺序敏感）。双端三份镜像 struct 必须与此逐字一致：
//   - server/internal/core.Tunnel（moleAgent_Serv）
//   - client 根包 Tunnel（moleAgent_client）
//   - client/internal/protocol.Tunnel
// 注意：字段【类型】允许按端分叉（如 RateLimit：server 为 *TunnelRateLimit
// 结构化，client 为 json.RawMessage 透传）——锁的是字段名与 json tag，
// 它们才决定 wire 兼容。任一端加字段另一端漏改时，JSON 反序列化会静默
// 丢字段（编译与普通测试均不报错），三模块各自的 *_tags_test.go 锁测试
// 用 reflect 与本契约逐项比对兜底。
// 变更流程：先改此契约，再同步三份 struct 与各自锁测试；漏改端会在其
// 模块测试里红。
var TunnelWireFields = []string{
	"Name:name",
	"Type:type",
	"Target:target",
	"Domain:domain,omitempty",
	"ListenPort:listen_port,omitempty",
	"Enabled:enabled,omitempty",
	"Para:para,omitempty",
	"RateLimit:rate_limit,omitempty",
}

// ---------------------------------------------------------------------------
// 纯传输结构体（json tag 双端逐字一致；字段名/顺序/tag 不可改动，改动即协议破坏）
// ---------------------------------------------------------------------------

// ControlCmd 控制命令。Cmd 字段的合法取值共 9 个：
//   - C→S：register / ping / tunnel_update / sysinfo / tunnel_status / p2p_signal_token
//   - S→C：tunnel_push / tunnel_action / restart
//
// 类型参数 T 为隧道配置条目类型：双端 Tunnel 结构体的 json tag 逐字一致，
// 但 Go 类型各自独立（server 为 core.Tunnel，其 rate_limit 是
// *TunnelRateLimit；client 为 protocol.Tunnel，其 rate_limit 是
// json.RawMessage 原样透传），无法共用一个具体类型而不改变某一端的行为。
// 故 Tunnels 字段以泛型参数承载：server 实例化为 ControlCmd[core.Tunnel]，
// client 实例化为 ControlCmd[protocol.Tunnel]，序列化结果与抽取前双端
// 各自的 concrete 定义完全一致。
type ControlCmd[T any] struct {
	Cmd      string         `json:"cmd"`                     // 命令字（取值见上）
	NodeID   string         `json:"node_id,omitempty"`       // 注册时使用
	Name     string         `json:"name,omitempty"`          // 节点名称 / 隧道名称
	Token    string         `json:"token,omitempty"`         // 节点令牌
	Tunnels  []T            `json:"tunnels,omitempty"`       // 隧道配置
	Ts       int64          `json:"ts,omitempty"`            // Unix 毫秒（ping RTT）
	Action   string         `json:"action,omitempty"`        // tunnel_action: start/stop/restart
	Delay    int            `json:"delay_seconds,omitempty"` // restart 延迟秒数
	Reason   string         `json:"reason,omitempty"`        // restart 原因
	Statuses []TunnelStatus `json:"statuses,omitempty"`      // tunnel_status 上报
	SysInfo  *SysInfo       `json:"sysinfo,omitempty"`       // 系统信息上报
}

// ControlResponse 控制响应
type ControlResponse struct {
	Cmd  string          `json:"cmd"` // ok, pong, err
	Msg  string          `json:"msg,omitempty"`
	Ts   int64           `json:"ts,omitempty"`   // 原样回传（ping RTT）
	Data json.RawMessage `json:"data,omitempty"` // 结构化数据
}

// TunnelStatus 客户端上报的隧道运行时状态
type TunnelStatus struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Running       bool   `json:"running"`
	Connected     bool   `json:"connected,omitempty"`
	SerialOpen    bool   `json:"serial_open,omitempty"`
	MQTTConnected bool   `json:"mqtt_connected,omitempty"`
	Clients       int    `json:"clients,omitempty"`
	PID           int    `json:"pid,omitempty"`
	UptimeSeconds int64  `json:"uptime_seconds,omitempty"`
	BytesIn       uint64 `json:"bytes_in,omitempty"`
	BytesOut      uint64 `json:"bytes_out,omitempty"`
	Error         string `json:"error,omitempty"`
}

// SysInfo 客户端上报的系统信息
type SysInfo struct {
	OS           string `json:"os,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	Uptime       int64  `json:"uptime_seconds,omitempty"`
	GoVersion    string `json:"go_version,omitempty"`
	AgentVersion string `json:"agent_version,omitempty"`
	NumCPU       int    `json:"num_cpu,omitempty"`
	MemTotalMB   int64  `json:"mem_total_mb,omitempty"`
	MemUsedMB    int64  `json:"mem_used_mb,omitempty"`
}

// P2PSignalTokenResp 是 p2p_signal_token 命令的 ad-hoc 响应（与 pong 携带 ts
// 同款做法：协议层平铺 JSON，两端各自解码，不扩 ControlResponse）
type P2PSignalTokenResp struct {
	Cmd       string `json:"cmd"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}
