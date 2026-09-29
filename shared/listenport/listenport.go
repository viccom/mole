// Package listenport 是「服务自身保留监听端口」规则的单一事实源：由原
// server/internal/core/listen_port.go 与 client/tunnel.go 的手工同值副本
// 抽取合并。server 侧经 core.ValidateTunnelListenPort 补 ErrTunnelInvalid
// 前缀包装后使用，client 侧经 validateListenPort 直用——双端错误文案主体
// 与本包输出逐字节一致。
package listenport

import "fmt"

// ReservedPorts 服务自身监听的保留端口集合（网关/控制/API/控制面 WS 附加
// 传输/MQTT-TCP/MQTT-WS）。隧道 listen_port 落在其中意味着与自身端口冲突，
// 必须在配置期拒绝（REL-01）。双端共用同一集合，改动必须两端同步评审——
// 漂移的后果是客户端放行、服务端整单拒绝，节点无法上线。
var ReservedPorts = map[int]struct{}{
	9980: {}, // gateway_port
	9981: {}, // control_port
	9982: {}, // 控制面 WS 附加传输默认端口
	9983: {}, // api_port
	1882: {}, // mqtt ws_port
	1883: {}, // mqtt tcp_port
}

// Validate 校验单条 TCP/UDP 隧道的监听端口（REL-01）：1-65535，且不得落在
// ReservedPorts 上。错误文案为无前缀主体：
//   - "listen_port must be 1-65535, got %d"
//   - "listen_port %d is reserved for the server itself"
//
// 调用方按各自错误体系处理：server 侧包装 ErrTunnelInvalid 前缀，client
// 侧直出。
func Validate(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("listen_port must be 1-65535, got %d", port)
	}
	if _, reserved := ReservedPorts[port]; reserved {
		return fmt.Errorf("listen_port %d is reserved for the server itself", port)
	}
	return nil
}
