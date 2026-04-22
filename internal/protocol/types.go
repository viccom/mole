package protocol

import "encoding/json"

// TunnelType 隧道类型
type TunnelType string

const (
	TunnelTypeHTTP TunnelType = "http"
	TunnelTypeTCP  TunnelType = "tcp"
	TunnelTypeUDP  TunnelType = "udp"
)

// Tunnel 隧道配置
type Tunnel struct {
	Name       string          `json:"name"`
	Type       TunnelType      `json:"type"`
	Target     string          `json:"target"`
	Domain     string          `json:"domain,omitempty"`
	ListenPort int             `json:"listen_port,omitempty"`
	Enabled    *bool           `json:"enabled,omitempty"`
	Para       json.RawMessage `json:"para,omitempty"` // 扩展配置（ser2mq/vpn-manager）
}

// ControlCmd 控制命令
type ControlCmd struct {
	Cmd     string  `json:"cmd"`
	NodeID  string  `json:"node_id,omitempty"`
	Name    string  `json:"name,omitempty"`
	Token   string  `json:"token,omitempty"`
	Tunnels []Tunnel `json:"tunnels,omitempty"`
}

// ControlResponse 控制响应
type ControlResponse struct {
	Cmd string `json:"cmd"`
	Msg string `json:"msg,omitempty"`
}
