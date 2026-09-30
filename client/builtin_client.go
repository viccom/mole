package moleAgent_client

import (
	"moleAgent_client/internal/proxy/ser2mq"
	"moleAgent_client/internal/proxy/ser2net"
	"moleAgent_client/internal/proxy/vpn"
)

// BuiltinClient 是内置 HTTP 管理服务（架构审查 🔴4：原 internal/builtin 迁出到
// 包内置服务目录）所需的最小客户端能力面。内置服务曾位于 internal/ 却反向依赖
// 根包，违反「internal 是根包私有实现层」的约定；迁出后以本接口收窄依赖，
// 内置服务不再 import 根包具体实现。
type BuiltinClient interface {
	Stats() Stats
	AllTunnelStatus() []TunnelStatus
	TunnelStatusByName(name string) (TunnelStatus, error)
	AddTunnel(t Tunnel) error
	RemoveTunnel(name string) error
	VPNStart(name string) error
	VPNStop(name string) error
	VPNCrashLogs(name string) ([]vpn.CrashLog, error)
	VPNVNTData(name string) (*vpn.VNTInfo, []vpn.VNTDeviceItem, []vpn.VNTRouteItem, *vpn.VNTBuildInfo, error)
	VPNVNTChart(name string) (*vpn.VNTChartA, error)
	Ser2MQStreamHub() *ser2mq.StreamHub
	Ser2NetStreamHub() *ser2net.StreamHub
}
