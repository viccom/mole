// Package tunnelvalidate 是隧道校验规则的单源核心（server core 与 client 根包共用）。
//
// 设计依据 docs/decisions.md「隧道校验差异裁决表」：双端行为语义分叉以 Options
// 参数化保留现状，错误文案统一为单一版本（变化端见裁决表标注）；p2p Para 校验与
// 列表级去重整体单源；server 独有跨节点校验、client 独有 dropInvalidTunnels
// 留在各端。错误均为裸错误——server 适配层用 ErrTunnelInvalid 包装，client 裸用。
package tunnelvalidate

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"mole/shared/listenport"
	"mole/shared/proto"
)

// 条数与数值上限单源（双端 re-export 保持各自既有导出名）
const (
	// MaxRegisterTunnels register 命令携带隧道列表的条数上限（REL-02）
	MaxRegisterTunnels = 256
	// MaxConnsUpperBound 单隧道最大并发连接上限
	MaxConnsUpperBound = 100000
	// MaxBandwidthUpperBound 单隧道最大带宽上限（10 GB/s，显式 int64 防 32 位溢出）
	MaxBandwidthUpperBound int64 = 10737418240
)

// Options 参数化双端语义分叉（裁决表「语义分叉」节）
type Options struct {
	// AllowEmptyTarget 允许空 target 的类型集合（值用 proto.TunnelType* 常量）。
	// server 传五类本地隧道 + p2p；client 传 vpn-manager + p2p（对四类本地
	// 隧道收紧 target 必填的现状保留）。
	AllowEmptyTarget map[string]bool
	// RejectSchemeInTarget 为 true 时 http/https 的 target 拒绝携带 "://"
	// （client 现状；server false 保持放行）
	RejectSchemeInTarget bool
}

// ServerOptions / ClientOptions 返回各端现状的 Options（防手写集合漂移）。
func ServerOptions() Options {
	return Options{AllowEmptyTarget: map[string]bool{
		proto.TunnelTypeSer2MQ: true, proto.TunnelTypeVPNMgr: true,
		proto.TunnelTypeSer2TCP: true, proto.TunnelTypeSer2UDP: true,
		proto.TunnelTypeWebSSH: true, proto.TunnelTypeP2P: true,
	}}
}

func ClientOptions() Options {
	return Options{
		AllowEmptyTarget: map[string]bool{
			proto.TunnelTypeVPNMgr: true, proto.TunnelTypeP2P: true,
		},
		RejectSchemeInTarget: true,
	}
}

// Tunnel 是校验视图：字段名与双端一致；RateLimit 为已解析形态（server 侧
// *core.TunnelRateLimit 转换而来，client 侧由 json.RawMessage 校验时转换、
// 存储仍透传原文）。
type Tunnel struct {
	Name       string
	Type       string
	Target     string
	ListenPort int
	Para       json.RawMessage
	RateLimit  *RateLimit
}

// RateLimit per-tunnel 限速配置（数值范围与全零规则单源）
type RateLimit struct {
	MaxConns     int
	MaxBandwidth int64
}

// Validate 校验单条隧道配置（规则矩阵见裁决表；文案为统一版）。
func Validate(t Tunnel, opts Options) error {
	if t.Name == "" {
		return fmt.Errorf("tunnel name is required")
	}
	// 名称进入代理协议头 `\x00<name>\n`，自带控制字符会使流路由按行解析错位
	if strings.ContainsAny(t.Name, "\x00\n\r") {
		return fmt.Errorf("tunnel name must not contain \\x00, \\n or \\r")
	}

	switch t.Type {
	case proto.TunnelTypeHTTP, proto.TunnelTypeHTTPS, proto.TunnelTypeTCP, proto.TunnelTypeUDP:
		if t.Target == "" {
			return fmt.Errorf("tunnel target is required")
		}
		if opts.RejectSchemeInTarget && (t.Type == proto.TunnelTypeHTTP || t.Type == proto.TunnelTypeHTTPS) &&
			strings.Contains(t.Target, "://") {
			return fmt.Errorf("http(s) tunnel target must be host:port without scheme (e.g. 127.0.0.1:8080), got %q", t.Target)
		}
		if err := validateHostPort(t.Target); err != nil {
			return err
		}
		// listen_port 只对 TCP/UDP 生效：HTTP/HTTPS 走域名路由不占系统端口，
		// 零值合法；TCP/UDP 必须落在合法区间且避开服务自身保留端口（REL-01）
		if t.Type == proto.TunnelTypeTCP || t.Type == proto.TunnelTypeUDP {
			if err := listenport.Validate(t.ListenPort); err != nil {
				return err
			}
		}

	case proto.TunnelTypeSer2MQ, proto.TunnelTypeVPNMgr, proto.TunnelTypeSer2TCP, proto.TunnelTypeSer2UDP, proto.TunnelTypeWebSSH, proto.TunnelTypeP2P:
		// 本地类型/p2p 的 target 语义由 opts.AllowEmptyTarget 分叉保留：
		// 集合外（client 对四类本地隧道）空 target 仍必填
		if t.Target == "" && !opts.AllowEmptyTarget[t.Type] {
			return fmt.Errorf("tunnel target is required")
		}
		if t.Type == proto.TunnelTypeP2P {
			// room 是密钥材料，Para 必须严格校验
			if err := ValidateP2PPara(t.Para); err != nil {
				return err
			}
		}

	default:
		return fmt.Errorf("unknown tunnel type %q", t.Type)
	}

	if err := ValidateRateLimit(t.RateLimit); err != nil {
		return err
	}
	return nil
}

// ValidateList 在单条 Validate 之上补两条列表级规则：
//   - 隧道名在列表内唯一（区分大小写，精确匹配）
//   - 仅 TCP/UDP 的 listen_port 参与列表内去重（HTTP/HTTPS 走域名路由，
//     零值不占端口位）
func ValidateList(tunnels []Tunnel, opts Options) error {
	names := make(map[string]bool, len(tunnels))
	ports := make(map[int]string, len(tunnels))
	for i := range tunnels {
		if err := Validate(tunnels[i], opts); err != nil {
			return err
		}
		if names[tunnels[i].Name] {
			return fmt.Errorf("duplicate tunnel name %q", tunnels[i].Name)
		}
		names[tunnels[i].Name] = true
		if tunnels[i].Type == proto.TunnelTypeTCP || tunnels[i].Type == proto.TunnelTypeUDP {
			if prev, dup := ports[tunnels[i].ListenPort]; dup {
				return fmt.Errorf("duplicate listen_port %d (tunnel %q and %q)", tunnels[i].ListenPort, prev, tunnels[i].Name)
			}
			ports[tunnels[i].ListenPort] = tunnels[i].Name
		}
	}
	return nil
}

// validateHostPort target 统一为 host:port 格式（host 非空 + 端口 1-65535）
func validateHostPort(target string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("tunnel target must be host:port format (e.g. 127.0.0.1:8080), got %q", target)
	}
	if host == "" {
		return fmt.Errorf("tunnel target host is required, got %q", target)
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("tunnel target port must be 1-65535, got %q", port)
	}
	return nil
}

// ValidateRateLimit 校验限速配置（拒绝负值/超上限/全零）
func ValidateRateLimit(rl *RateLimit) error {
	if rl == nil {
		return nil
	}
	if rl.MaxConns < 0 || rl.MaxConns > MaxConnsUpperBound {
		return fmt.Errorf("max_conns must be 1-%d, got %d", MaxConnsUpperBound, rl.MaxConns)
	}
	if rl.MaxBandwidth < 0 || rl.MaxBandwidth > MaxBandwidthUpperBound {
		return fmt.Errorf("max_bandwidth must be 1-%d bytes/sec, got %d", MaxBandwidthUpperBound, rl.MaxBandwidth)
	}
	if rl.MaxConns == 0 && rl.MaxBandwidth == 0 {
		return fmt.Errorf("rate_limit must have at least one non-zero field, use null to clear")
	}
	return nil
}

// p2pModeSet 与 p2punch engine/dep.go 的 AllModes 集合一致
// （lan/tcp-v6/udp-v6/udp-v4/tcp-v4/v4-relay）。
var p2pModeSet = map[string]bool{
	"lan":      true,
	"tcp-v6":   true,
	"udp-v6":   true,
	"udp-v4":   true,
	"tcp-v4":   true,
	"v4-relay": true,
}

// p2pRoomRegexp room 格式：8-32 字符 [a-zA-Z0-9_-]
var p2pRoomRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,32}$`)

// P2PMapping / P2PPara 是 p2p 隧道 Para 的两层结构（协议契约 §0.2）：
// 连接参数（room/modes/relay_server/mqtt_brokers/stun_servers）两端对称；
// 端口映射 mappings[] 仅访问发起端配置，随 TUNNEL:OPEN 在线传给对端。
type P2PMapping struct {
	Protocol   string `json:"protocol"`
	LocalPort  int    `json:"local_port"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}

type P2PPara struct {
	Room        string       `json:"room"`
	Modes       []string     `json:"modes"`
	RelayServer string       `json:"relay_server"`
	MQTTBrokers []string     `json:"mqtt_brokers"`
	STUNServers []string     `json:"stun_servers"`
	Mappings    []P2PMapping `json:"mappings"`
}

// ValidateP2PPara 校验 p2p 隧道 Para 的合法性（协议契约 §0.3）。
// room 是共享密钥材料（知道 room 即可加入信令并推导 payload key）：
//   - room 必填，8-32 字符 [a-zA-Z0-9_-]
//   - modes 可选（缺省/空 = 客户端默认链），每个值必须 ∈ AllModes；
//     含 v4-relay 时 relay_server 必填，否则忽略
//   - mappings 可选（缺省/空数组 = 纯会话端），逐条校验：protocol ∈ {tcp,udp}；
//     local_port/target_port 均 1-65535；target_host 非空；同一 para 内
//     local_port 不得重复
func ValidateP2PPara(para json.RawMessage) error {
	var p P2PPara
	if err := json.Unmarshal(para, &p); err != nil {
		return fmt.Errorf("p2p para is not valid JSON: %w", err)
	}
	if !p2pRoomRegexp.MatchString(p.Room) {
		return fmt.Errorf("p2p room must be 8-32 chars of [a-zA-Z0-9_-]")
	}
	for _, m := range p.Modes {
		if !p2pModeSet[m] {
			return fmt.Errorf("p2p unknown mode %q", m)
		}
		if m == "v4-relay" && p.RelayServer == "" {
			// v4-relay 缺 relay_server 在运行期必然永久失败，配置期拦截
			return fmt.Errorf("p2p modes contains v4-relay but relay_server is empty")
		}
	}
	seen := make(map[int]bool, len(p.Mappings))
	for i, m := range p.Mappings {
		if m.Protocol != "tcp" && m.Protocol != "udp" {
			return fmt.Errorf("p2p mappings[%d].protocol must be tcp or udp", i)
		}
		if m.LocalPort < 1 || m.LocalPort > 65535 {
			return fmt.Errorf("p2p mappings[%d].local_port must be 1-65535", i)
		}
		if m.TargetHost == "" {
			return fmt.Errorf("p2p mappings[%d].target_host is required", i)
		}
		if m.TargetPort < 1 || m.TargetPort > 65535 {
			return fmt.Errorf("p2p mappings[%d].target_port must be 1-65535", i)
		}
		if seen[m.LocalPort] {
			return fmt.Errorf("p2p mappings[%d].local_port %d duplicated in same para", i, m.LocalPort)
		}
		seen[m.LocalPort] = true
	}
	return nil
}
