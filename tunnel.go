package moleAgent_client

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"

	"moleAgent_client/internal/protocol"
)

// TunnelType 隧道类型
type TunnelType string

// maxRegisterTunnels register 命令携带隧道列表的条数上限（REL-02）：
// 与服务端 core.MaxRegisterTunnels 同值同义——防异常客户端用超大列表
// 冲击控制面内存与注册路径。超限在 register 构造前本地报错。
const maxRegisterTunnels = 256

const (
	TunnelTypeHTTP    TunnelType = "http"
	TunnelTypeHTTPS   TunnelType = "https"
	TunnelTypeTCP     TunnelType = "tcp"
	TunnelTypeUDP     TunnelType = "udp"
	TunnelTypeSer2MQ  TunnelType = "ser2mq"      // 串口转 MQTT
	TunnelTypeSer2TCP TunnelType = "ser2tcp"     // 串口转 TCP
	TunnelTypeSer2UDP TunnelType = "ser2udp"     // 串口转 UDP
	TunnelTypeVPNMgr  TunnelType = "vpn-manager" // VPN 程序管理
	TunnelTypeWebSSH  TunnelType = "webssh"      // WebSSH 远程终端
	TunnelTypeP2P     TunnelType = "p2p"         // P2P 直连隧道（需 -tags p2p 构建才运行）
)

// Tunnel 隧道配置（统一类型，替代原 tunnelConfig 和 protocol.Tunnel 两套定义）
type Tunnel struct {
	Name       string          `json:"name"`
	Type       TunnelType      `json:"type"`
	Target     string          `json:"target"`
	Domain     string          `json:"domain,omitempty"`
	ListenPort int             `json:"listen_port,omitempty"`
	Enabled    *bool           `json:"enabled,omitempty"`    // 启用开关，nil/true=启用，false=禁用
	Para       json.RawMessage `json:"para,omitempty"`       // 扩展配置（ser2mq/vpn-manager 等）
	RateLimit  json.RawMessage `json:"rate_limit,omitempty"` // 服务端限速配置，原样透传（防止本地变更全量回传时清空服务端限速）
}

// IsEnabled 返回隧道是否启用。零值（nil）视为启用，兼容旧数据。
func (t Tunnel) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// boolPtr 返回 bool 指针
func boolPtr(b bool) *bool {
	return &b
}

// Validate 校验隧道配置合法性
func (t Tunnel) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("tunnel name is required")
	}
	// 名称会进入 TCP/UDP/WebSSH 代理头（\x00<name>\n，按首个 \n 定界），
	// 含控制字符会使转发路由解析错位
	if strings.ContainsAny(t.Name, "\x00\n\r") {
		return fmt.Errorf("tunnel name must not contain \\x00, \\n or \\r")
	}
	switch t.Type {
	case TunnelTypeHTTP, TunnelTypeHTTPS, TunnelTypeTCP, TunnelTypeUDP, TunnelTypeSer2MQ, TunnelTypeSer2TCP, TunnelTypeSer2UDP, TunnelTypeVPNMgr, TunnelTypeWebSSH, TunnelTypeP2P:
	default:
		return fmt.Errorf("invalid tunnel type: %s", t.Type)
	}
	// p2p 与 vpn-manager 同享空 Target 豁免：p2p 纯会话端无 Target（访问目标由发起端在 Para/OPEN 消息中指定）
	if t.Target == "" && t.Type != TunnelTypeVPNMgr && t.Type != TunnelTypeP2P {
		return fmt.Errorf("tunnel target is required")
	}
	// 标准四类 target 与服务端 validateTunnel 对齐（host:port，无 scheme）：
	// 客户端接受的畸形形态不会被本地拦截，而是在 tunnel_update 时被服务端
	// 整单拒绝——本次全部本地变更静默丢失。scheme 由 dispatchStream 缺省补齐
	switch t.Type {
	case TunnelTypeHTTP, TunnelTypeHTTPS:
		if strings.Contains(t.Target, "://") {
			return fmt.Errorf("http(s) tunnel target must be host:port without scheme (e.g. 127.0.0.1:8080), got %q", t.Target)
		}
		if err := validateHostPort(t.Target); err != nil {
			return err
		}
	case TunnelTypeTCP, TunnelTypeUDP:
		if err := validateHostPort(t.Target); err != nil {
			return err
		}
		// listen_port 只对 TCP/UDP 生效（REL-01）：HTTP/HTTPS 走域名路由不占
		// 系统端口，零值合法；TCP/UDP 必须落在合法区间且避开服务自身保留端口
		if err := validateListenPort(t.ListenPort); err != nil {
			return err
		}
	case TunnelTypeP2P:
		// p2p 的 Para 含共享密钥材料 room 与连接参数，规则与服务端
		// core.ValidateP2PPara 对齐（本地放行会被 register 整单拒绝）
		if err := validateP2PPara(t.Para); err != nil {
			return err
		}
	}
	// rate_limit 形态校验（与服务端 validateRateLimit 对齐）：客户端侧是零校验的
	// RawMessage，畸形值会让服务端对整条控制命令 JSON 反序列化失败——register 被
	// 拒即节点永远无法注册；tunnel_update 被拒即整个快照丢失
	if len(t.RateLimit) > 0 && string(t.RateLimit) != "null" {
		var rl struct {
			MaxConns     int   `json:"max_conns"`
			MaxBandwidth int64 `json:"max_bandwidth"`
		}
		if err := json.Unmarshal(t.RateLimit, &rl); err != nil {
			return fmt.Errorf("invalid rate_limit (must be an object with max_conns/max_bandwidth): %w", err)
		}
		if rl.MaxConns < 0 || rl.MaxConns > 100000 || rl.MaxBandwidth < 0 || rl.MaxBandwidth > 10737418240 {
			return fmt.Errorf("rate_limit out of range: max_conns 0-100000, max_bandwidth 0-10737418240 bytes/sec")
		}
		if rl.MaxConns == 0 && rl.MaxBandwidth == 0 {
			return fmt.Errorf("rate_limit must have at least one non-zero field (omit or null to clear)")
		}
	}
	return nil
}

// validateHostPort 与服务端 validateTunnel 的 target 校验完全对齐
// （host 非空 + 端口 1-65535）：仅 SplitHostPort 会放行 "host:"、":port"、
// "host:70000" 等畸形形态，穿透后被服务端整单拒绝
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

// reservedListenPorts 服务端自身监听的保留端口集合（网关/控制/API/控制面
// WS 附加传输/MQTT-TCP/MQTT-WS），语义与服务端 core/listen_port.go 的
// reservedListenPorts 一致：TCP/UDP 隧道 listen_port 落在其中意味着与服务
// 自身端口冲突，服务端在配置期整单拒绝（REL-01）。客户端不能 import 服务端
// core 包，按协议约定在此维护同值副本——漂移的后果是客户端放行、服务端
// 整单拒绝，节点无法上线。
var reservedListenPorts = map[int]struct{}{
	9980: {}, // gateway_port
	9981: {}, // control_port
	9982: {}, // 控制面 WS 附加传输默认端口
	9983: {}, // api_port
	1882: {}, // mqtt ws_port
	1883: {}, // mqtt tcp_port
}

// validateListenPort 校验单条 TCP/UDP 隧道的监听端口（REL-01），与服务端
// core.ValidateTunnelListenPort 同规则同文案：1-65535 且不落保留端口集合
func validateListenPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("listen_port must be 1-65535, got %d", port)
	}
	if _, reserved := reservedListenPorts[port]; reserved {
		return fmt.Errorf("listen_port %d is reserved for the server itself", port)
	}
	return nil
}

// p2pPara / p2pMapping 是 p2p 隧道 Para 的两层结构（协议契约 §0.2）：
// 连接参数（room/modes/relay_server/mqtt_brokers/stun_servers）两端对称；
// 端口映射 mappings[] 仅访问发起端配置，随 TUNNEL:OPEN 在线传给对端。
type p2pMapping struct {
	Protocol   string `json:"protocol"`
	LocalPort  int    `json:"local_port"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}

type p2pPara struct {
	Room        string       `json:"room"`
	Modes       []string     `json:"modes"`
	RelayServer string       `json:"relay_server"`
	MQTTBrokers []string     `json:"mqtt_brokers"`
	STUNServers []string     `json:"stun_servers"`
	Mappings    []p2pMapping `json:"mappings"`
}

// p2pRoomRegexp room 格式：8-32 字符 [a-zA-Z0-9_-]，与服务端
// core/validate_para.go 的 p2pRoomRegexp、internal/proxy/p2p（-tags p2p）
// 的 roomRegexp 同一规则
var p2pRoomRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,32}$`)

// p2pModeSet 与 p2punch engine 的 AllModes 集合一致（lan/tcp-v6/udp-v6/
// udp-v4/tcp-v4/v4-relay），与服务端 core/validate_para.go 的 p2pModeSet
// 同值
var p2pModeSet = map[string]bool{
	"lan":      true,
	"tcp-v6":   true,
	"udp-v6":   true,
	"udp-v4":   true,
	"tcp-v4":   true,
	"v4-relay": true,
}

// validateP2PPara 校验 p2p 隧道 Para 的合法性（协议契约 §0.3，与服务端
// core.ValidateP2PPara 同规则同文案）。room 是共享密钥材料（知道 room 即可
// 加入信令并推导 payload key），必须在配置期把关：
//   - room 必填，8-32 字符 [a-zA-Z0-9_-]
//   - modes 可选（缺省/空 = 客户端默认链），每个值必须 ∈ AllModes；
//     含 v4-relay 时 relay_server 必填，否则忽略
//   - mappings 可选（缺省/空数组 = 纯会话端，仅建会话不在本机监听端口），
//     逐条校验：protocol ∈ {tcp,udp}；local_port/target_port 均 1-65535；
//     target_host 非空；同一 para 内 local_port 不得重复
//
// 独立实现而不复用 internal/proxy/p2p 的 FromPara：该包整体
// //go:build p2p，默认构建不存在；Validate 属默认构建路径。
func validateP2PPara(para json.RawMessage) error {
	var p p2pPara
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

// toProtocol 转换为协议层类型（用于发送到服务端）
func (t Tunnel) toProtocol() protocol.Tunnel {
	return protocol.Tunnel{
		Name:       t.Name,
		Type:       protocol.TunnelType(t.Type),
		Target:     t.Target,
		Domain:     t.Domain,
		ListenPort: t.ListenPort,
		Enabled:    t.Enabled,
		Para:       t.Para,
		RateLimit:  t.RateLimit,
	}
}

// toProtocols 批量转换
func toProtocols(tunnels []Tunnel) []protocol.Tunnel {
	result := make([]protocol.Tunnel, len(tunnels))
	for i, t := range tunnels {
		result[i] = t.toProtocol()
	}
	return result
}

// fromProtocol 从协议层类型转换
func fromProtocol(t protocol.Tunnel) Tunnel {
	return Tunnel{
		Name:       t.Name,
		Type:       TunnelType(t.Type),
		Target:     t.Target,
		Domain:     t.Domain,
		ListenPort: t.ListenPort,
		Enabled:    t.Enabled,
		Para:       t.Para,
		RateLimit:  t.RateLimit,
	}
}

// fromProtocols 批量从协议层转换
func fromProtocols(tunnels []protocol.Tunnel) []Tunnel {
	result := make([]Tunnel, len(tunnels))
	for i, t := range tunnels {
		result[i] = fromProtocol(t)
	}
	return result
}

// warnInvalidPushedTunnels 对服务端推送的隧道逐条跑 Validate，把不通过的
// 项记入日志（含隧道名与原因）。**不改动配置、不改变返回值**。
//
// 定位：纵深防御 + 诊断线索。服务端侧 validateTunnels 已在 pushToClient
// 前把关，故正常情况下不会触发；但若服务端校验回退、或历史脏数据经
// control.go 的兼容路径（nodeRepo 直推，未过校验）推来，这里留下痕迹。
//
// 刻意不做过滤：过滤会让被跳过的隧道在后续 sendTunnelUpdate 上报时从
// 服务端持久化中消失——把「一条配置有问题」放大成「配置丢失」，
// 而收益仅覆盖一个生产不可达的路径。
//
// 已知的两端分歧不告警（独立审查复核轮发现）：服务端对客户端本地类型
// （ser2mq/ser2tcp/ser2udp/webssh）不校验 target——空 target 是服务端
// 合法形态，而客户端 Validate 更严（要求非空）。对这类配置告警会在每次
// 推送时重复出现并误归因于服务端；模块自身的问题会经各自错误路径暴露。
func warnInvalidPushedTunnels(tunnels []Tunnel) {
	for _, t := range tunnels {
		if t.Target == "" && serverAllowsEmptyTarget(t.Type) {
			continue
		}
		if err := t.Validate(); err != nil {
			log.Printf("tunnel_push: server pushed invalid tunnel %q (type %s): %v", t.Name, t.Type, err)
		}
	}
}

// serverAllowsEmptyTarget 服务端 validateTunnel 对这些类型不做 target
// 校验（空 target 合法落库）。与 moleAgent_Serv 的 clientLocalTypes 对应；
// vpn-manager/p2p 客户端也豁免，无分歧，不在表内。
func serverAllowsEmptyTarget(typ TunnelType) bool {
	switch typ {
	case TunnelTypeSer2MQ, TunnelTypeSer2TCP, TunnelTypeSer2UDP, TunnelTypeWebSSH:
		return true
	}
	return false
}
