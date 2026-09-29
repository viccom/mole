package core

import "fmt"

// reservedListenPorts 服务自身监听的保留端口集合（网关/控制/API/控制面 WS 附加
// 传输/MQTT-TCP/MQTT-WS）。隧道 listen_port 落在其中意味着与自身端口冲突，
// 必须在配置期拒绝（REL-01）。
var reservedListenPorts = map[int]struct{}{
	9980: {}, // gateway_port
	9981: {}, // control_port
	9982: {}, // 控制面 WS 附加传输默认端口
	9983: {}, // api_port
	1882: {}, // mqtt ws_port
	1883: {}, // mqtt tcp_port
}

// ValidateTunnelListenPort 校验单条 TCP/UDP 隧道的监听端口（REL-01）：
// 必须在 1-65535，且不得落在服务自身保留端口集合上。
// service 层校验与 control 面 register 校验共用，避免两处规则漂移。
func ValidateTunnelListenPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: listen_port must be 1-65535, got %d", ErrTunnelInvalid, port)
	}
	if _, reserved := reservedListenPorts[port]; reserved {
		return fmt.Errorf("%w: listen_port %d is reserved for the server itself", ErrTunnelInvalid, port)
	}
	return nil
}
