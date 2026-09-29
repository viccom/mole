package protocol

import (
	"encoding/json"

	"mole/shared/proto"
)

// TunnelType 隧道类型
type TunnelType string

const (
	TunnelTypeHTTP TunnelType = proto.TunnelTypeHTTP
	TunnelTypeTCP  TunnelType = proto.TunnelTypeTCP
	TunnelTypeUDP  TunnelType = proto.TunnelTypeUDP
)

// Tunnel 隧道配置
type Tunnel struct {
	Name       string          `json:"name"`
	Type       TunnelType      `json:"type"`
	Target     string          `json:"target"`
	Domain     string          `json:"domain,omitempty"`
	ListenPort int             `json:"listen_port,omitempty"`
	Enabled    *bool           `json:"enabled,omitempty"`
	Para       json.RawMessage `json:"para,omitempty"`       // 扩展配置（ser2mq/vpn-manager）
	RateLimit  json.RawMessage `json:"rate_limit,omitempty"` // 服务端专属限速配置，原样透传（tunnel_update 全量回传时防止服务端限速配置被清空）
}

// 协议结构体单源至 mole/shared/proto（双端 json tag 逐字一致），此处以类型
// 别名接入保持本包既有 API；ControlCmd 以本包 Tunnel 实例化 Tunnels 字段
// （双端 Tunnel 的 Go 类型各自独立，见 proto 包注释）
type (
	ControlCmd      = proto.ControlCmd[Tunnel]
	ControlResponse = proto.ControlResponse
	TunnelStatus    = proto.TunnelStatus
	SysInfo         = proto.SysInfo
)
