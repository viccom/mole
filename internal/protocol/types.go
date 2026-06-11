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
	Cmd      string          `json:"cmd"`
	NodeID   string          `json:"node_id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Token    string          `json:"token,omitempty"`
	Tunnels  []Tunnel        `json:"tunnels,omitempty"`
	Ts       int64           `json:"ts,omitempty"`            // Unix 毫秒（ping RTT）
	Action   string          `json:"action,omitempty"`        // tunnel_action: start/stop/restart
	Delay    int             `json:"delay_seconds,omitempty"` // restart 延迟秒数
	Reason   string          `json:"reason,omitempty"`        // restart 原因
	Statuses []TunnelStatus  `json:"statuses,omitempty"`      // tunnel_status 上报
	SysInfo  *SysInfo        `json:"sysinfo,omitempty"`       // 系统信息上报
}

// ControlResponse 控制响应
type ControlResponse struct {
	Cmd  string          `json:"cmd"`
	Msg  string          `json:"msg,omitempty"`
	Ts   int64           `json:"ts,omitempty"`    // 原样回传（ping RTT）
	Data json.RawMessage `json:"data,omitempty"`  // 结构化数据
}

// TunnelStatus 隧道运行时状态（tunnel_status 用）
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

// SysInfo 系统信息（sysinfo 用）
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
