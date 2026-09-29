package core

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// p2pModeSet 与 p2punch engine/dep.go 的 AllModes 集合一致（lan/tcp-v6/udp-v6/udp-v4/tcp-v4/v4-relay）。
// 服务端硬编码同款集合：room/modes 的把关必须在落库前，不能依赖客户端自觉。
var p2pModeSet = map[string]bool{
	"lan":      true,
	"tcp-v6":   true,
	"udp-v6":   true,
	"udp-v4":   true,
	"tcp-v4":   true,
	"v4-relay": true,
}

// room 格式：8-32 字符 [a-zA-Z0-9_-]（与客户端 FromPara 校验一致）
var p2pRoomRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,32}$`)

// p2pMapping / p2pPara 是 p2p 隧道 Para 的两层结构（协议契约 §0.2，与客户端逐字一致）：
// 连接参数（room/modes/relay_server/mqtt_brokers/stun_servers）两端对称；
// 端口映射 mappings[] 仅访问发起端配置，随 TUNNEL:OPEN 在线传给对端，对端无需配置。
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

// ValidateP2PPara 校验 p2p 隧道 Para 的合法性（协议契约 §0.3，与客户端逐字一致）。
// room 是共享密钥材料（知道 room 即可加入信令并推导 payload key），格式必须把关：
//   - room 必填，8-32 字符 [a-zA-Z0-9_-]
//   - modes 可选（缺省/空 = 客户端默认链），每个值必须 ∈ AllModes；
//     含 v4-relay 时 relay_server 必填，否则忽略
//   - mappings 可选（缺省/空数组 = 纯会话端，仅建会话不在本机监听端口），
//     逐条校验：protocol ∈ {tcp,udp}；local_port/target_port 均 1-65535；
//     target_host 非空；同一 para 内 local_port 不得重复
//
// 旧单组映射的顶层字段（protocol/local_port/target_host/target_port）已废弃；
// 线上无存量 p2p 数据，不写兼容代码。
func ValidateP2PPara(para json.RawMessage) error {
	var p p2pPara
	if err := json.Unmarshal(para, &p); err != nil {
		return fmt.Errorf("%w: p2p para is not valid JSON", ErrTunnelInvalid)
	}
	if !p2pRoomRegexp.MatchString(p.Room) {
		return fmt.Errorf("%w: p2p room must be 8-32 chars of [a-zA-Z0-9_-]", ErrTunnelInvalid)
	}
	for _, m := range p.Modes {
		if !p2pModeSet[m] {
			return fmt.Errorf("%w: p2p unknown mode %q", ErrTunnelInvalid, m)
		}
		if m == "v4-relay" && p.RelayServer == "" {
			// 复审 F12：v4-relay 缺 relay_server 在运行期必然永久失败，配置期拦截
			return fmt.Errorf("%w: p2p modes contains v4-relay but relay_server is empty", ErrTunnelInvalid)
		}
	}
	seen := make(map[int]bool, len(p.Mappings))
	for i, m := range p.Mappings {
		if m.Protocol != "tcp" && m.Protocol != "udp" {
			return fmt.Errorf("%w: p2p mappings[%d].protocol must be tcp or udp", ErrTunnelInvalid, i)
		}
		if m.LocalPort < 1 || m.LocalPort > 65535 {
			return fmt.Errorf("%w: p2p mappings[%d].local_port must be 1-65535", ErrTunnelInvalid, i)
		}
		if m.TargetHost == "" {
			return fmt.Errorf("%w: p2p mappings[%d].target_host is required", ErrTunnelInvalid, i)
		}
		if m.TargetPort < 1 || m.TargetPort > 65535 {
			return fmt.Errorf("%w: p2p mappings[%d].target_port must be 1-65535", ErrTunnelInvalid, i)
		}
		if seen[m.LocalPort] {
			return fmt.Errorf("%w: p2p mappings[%d].local_port %d duplicated in same para", ErrTunnelInvalid, i, m.LocalPort)
		}
		seen[m.LocalPort] = true
	}
	return nil
}

// P2PRoom 提取 p2p 隧道 Para 中的 room（供配对校验使用）。
// Para 缺失/非 JSON/无 room 返回错误——room 是 p2p 配对的必填密钥材料。
func P2PRoom(para json.RawMessage) (string, error) {
	var p struct {
		Room string `json:"room"`
	}
	if err := json.Unmarshal(para, &p); err != nil {
		return "", fmt.Errorf("parse p2p para: %w", err)
	}
	if p.Room == "" {
		return "", fmt.Errorf("p2p room is required")
	}
	return p.Room, nil
}
