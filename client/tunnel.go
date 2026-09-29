package moleAgent_client

import (
	"encoding/json"
	"fmt"
	"log"

	"mole/shared/proto"
	"mole/shared/tunnelvalidate"
	"moleAgent_client/internal/protocol"
)

// TunnelType 隧道类型
type TunnelType string

// maxRegisterTunnels register 命令携带隧道列表的条数上限（REL-02）：
// 与服务端 core.MaxRegisterTunnels 同值同义（单源 mole/shared/tunnelvalidate）
// ——防异常客户端用超大列表冲击控制面内存与注册路径。超限在 register 构造前本地报错。
const maxRegisterTunnels = tunnelvalidate.MaxRegisterTunnels

// 隧道类型枚举单源至 mole/shared/proto（跨端一致的值），此处以既有名字 re-export
const (
	TunnelTypeHTTP    TunnelType = proto.TunnelTypeHTTP    // HTTP 域名路由
	TunnelTypeHTTPS   TunnelType = proto.TunnelTypeHTTPS   // HTTPS 域名路由
	TunnelTypeTCP     TunnelType = proto.TunnelTypeTCP     // TCP 网关端口监听
	TunnelTypeUDP     TunnelType = proto.TunnelTypeUDP     // UDP 网关端口监听
	TunnelTypeSer2MQ  TunnelType = proto.TunnelTypeSer2MQ  // 串口转 MQTT
	TunnelTypeSer2TCP TunnelType = proto.TunnelTypeSer2TCP // 串口转 TCP
	TunnelTypeSer2UDP TunnelType = proto.TunnelTypeSer2UDP // 串口转 UDP
	TunnelTypeVPNMgr  TunnelType = proto.TunnelTypeVPNMgr  // VPN 程序管理
	TunnelTypeWebSSH  TunnelType = proto.TunnelTypeWebSSH  // WebSSH 远程终端
	TunnelTypeP2P     TunnelType = proto.TunnelTypeP2P     // P2P 直连隧道（需 -tags p2p 构建才运行）
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

// 校验规则已单源化至 mole/shared/tunnelvalidate（双端同源，差异经 Options
// 参数化——见 docs/decisions.md 隧道校验差异裁决表）。client 语义 =
// tunnelvalidate.ClientOptions()：四类本地隧道空 target 本地收紧必填、
// http/https target 拒 scheme（给出更明确的文案）。错误为裸文案（无前缀，
// client 风格现状）。

// toValidateView 转换为共享校验视图；rate_limit 仅校验时解析（RawMessage
// 存储原样透传，防止本地变更全量回传时清空服务端限速）
func (t Tunnel) toValidateView() (tunnelvalidate.Tunnel, error) {
	tv := tunnelvalidate.Tunnel{
		Name:       t.Name,
		Type:       string(t.Type),
		Target:     t.Target,
		ListenPort: t.ListenPort,
		Para:       t.Para,
	}
	if len(t.RateLimit) > 0 && string(t.RateLimit) != "null" {
		var rl struct {
			MaxConns     int   `json:"max_conns"`
			MaxBandwidth int64 `json:"max_bandwidth"`
		}
		if err := json.Unmarshal(t.RateLimit, &rl); err != nil {
			return tv, fmt.Errorf("invalid rate_limit (must be an object with max_conns/max_bandwidth): %w", err)
		}
		tv.RateLimit = &tunnelvalidate.RateLimit{
			MaxConns:     rl.MaxConns,
			MaxBandwidth: rl.MaxBandwidth,
		}
	}
	return tv, nil
}

// Validate 校验隧道配置合法性（client 语义：四类本地隧道 target 必填、
// http/https 拒 scheme、rate_limit 范围与全零规则）
func (t Tunnel) Validate() error {
	tv, err := t.toValidateView()
	if err != nil {
		return err
	}
	return tunnelvalidate.Validate(tv, tunnelvalidate.ClientOptions())
}

// validateTunnelList 在单条 Validate 之上补两条列表级规则（隧道名唯一、
// TCP/UDP listen_port 唯一）——单条合法但列表级违规的提交会被服务端整单
// 拒绝（register/tunnel_update），必须在本地提交前拦下
func validateTunnelList(tunnels []Tunnel) error {
	views := make([]tunnelvalidate.Tunnel, len(tunnels))
	for i := range tunnels {
		v, err := tunnels[i].toValidateView()
		if err != nil {
			return err
		}
		views[i] = v
	}
	return tunnelvalidate.ValidateList(views, tunnelvalidate.ClientOptions())
}

// p2p Para 校验（room/modes/relay/mappings 全部规则与文案）已单源化至
// mole/shared/tunnelvalidate（双端同源，经 shared.Validate 的 p2p 分支调用）。
// 曾在此处的 p2pPara/p2pMapping/p2pRoomRegexp/p2pModeSet 镜像副本已删除。

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
