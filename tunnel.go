package moleAgent_client

import (
	"encoding/json"
	"fmt"

	"moleAgent_client/internal/protocol"
)

// TunnelType 隧道类型
type TunnelType string

const (
	TunnelTypeHTTP    TunnelType = "http"
	TunnelTypeHTTPS   TunnelType = "https"
	TunnelTypeTCP     TunnelType = "tcp"
	TunnelTypeUDP     TunnelType = "udp"
	TunnelTypeSer2MQ  TunnelType = "ser2mq"      // 串口转 MQTT
	TunnelTypeVPNMgr  TunnelType = "vpn-manager"  // VPN 程序管理
)

// Tunnel 隧道配置（统一类型，替代原 tunnelConfig 和 protocol.Tunnel 两套定义）
type Tunnel struct {
	Name       string          `json:"name"`
	Type       TunnelType     `json:"type"`
	Target     string          `json:"target"`
	Domain     string          `json:"domain,omitempty"`
	ListenPort int            `json:"listen_port,omitempty"`
	Enabled    *bool           `json:"enabled,omitempty"` // 启用开关，nil/true=启用，false=禁用
	Para       json.RawMessage `json:"para,omitempty"`    // 扩展配置（ser2mq/vpn-manager 等）
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
	switch t.Type {
	case TunnelTypeHTTP, TunnelTypeHTTPS, TunnelTypeTCP, TunnelTypeUDP, TunnelTypeSer2MQ, TunnelTypeVPNMgr:
	default:
		return fmt.Errorf("invalid tunnel type: %s", t.Type)
	}
	if t.Target == "" && t.Type != TunnelTypeVPNMgr {
		return fmt.Errorf("tunnel target is required")
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
