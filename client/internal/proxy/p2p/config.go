//go:build p2p

// Package p2p 把 p2punch 核心包集成为 moleAgent 的 TunnelTypeP2P 隧道类型：
// Manager 接收配置分发（ser2mq 风格单 map），Handler 吸收 cmd 层连接编排 glue。
// 本包整体 //go:build p2p，默认构建不含任何 P2P 代码。
package p2p

import (
	"encoding/json"
	"fmt"
	"regexp"

	"moleAgent_client/internal/p2p/engine"
)

// Mapping 单条端口映射（对端无需配置：参数随 TUNNEL:OPEN 在线传给对端，
// 对端 AcceptRemote 被动接受——p2punch 原生行为）。
type Mapping struct {
	Protocol   string `json:"protocol"`    // "tcp" / "udp"
	LocalPort  int    `json:"local_port"`  // 本端监听端口
	TargetHost string `json:"target_host"` // 对端解析的目标地址
	TargetPort int    `json:"target_port"` // 对端目标端口
}

// P2PConfig 对应服务端存储的 p2p 隧道 Para（json tag 与服务端 core.ValidateP2PPara
// 校验字段一致）。两层结构（协议契约 §0.2）：
//   - 连接参数（Room/Modes/RelayServer/MQTTBrokers/STUNServers）两端对称，同 room 配对
//   - Mappings 仅访问发起端配置：会话建立后逐条 CreateTunnel；
//     空 = 纯会话端（只建 session 供对端 OPEN，本机不监听任何端口）
//
// Enable 不入结构体：Manager 以「配置从 map 消失」为停机信号（同 ser2mq/ser2net/webssh），
// 由上层 notifyManagers 用 IsEnabled() 过滤后再下发。
type P2PConfig struct {
	Room        string    `json:"room"`                   // 8-32 [a-zA-Z0-9_-]，共享密钥材料
	Modes       []string  `json:"modes,omitempty"`        // 空 = engine.DefaultModes
	RelayServer string    `json:"relay_server,omitempty"` // modes 含 v4-relay 时必填
	MQTTBrokers []string  `json:"mqtt_brokers,omitempty"` // 空 = 公共 broker 优先 + tcp://<serverHost>:1883 兜底
	STUNServers []string  `json:"stun_servers,omitempty"` // 空 = 公共列表 + serverHost:3478
	Mappings    []Mapping `json:"mappings,omitempty"`     // 空 = 纯会话端
}

// room 校验自实现（p2punch 的 validateRoom 在 cmd 层，fork 不带）；
// 与服务端 core.ValidateP2PPara 同规则：8-32 字符 [a-zA-Z0-9_-]
var roomRegexp = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,32}$`)

// FromPara 解析并校验 Para。校验规则与 server 端一致，本地二次把关（纵深防御）。
func FromPara(para json.RawMessage) (P2PConfig, error) {
	var cfg P2PConfig
	if err := json.Unmarshal(para, &cfg); err != nil {
		return cfg, fmt.Errorf("p2p para: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ToPara 序列化回 Para（客户端 tunnel_update 上行用，字段不能丢）
func (c P2PConfig) ToPara() (json.RawMessage, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("p2p para marshal: %w", err)
	}
	return b, nil
}

// Validate 校验规则（与 server core.ValidateP2PPara 保持一致，协议契约 §0.3）：
//   - room 8-32 [a-zA-Z0-9_-]
//   - modes ⊆ engine.AllModes（空 = 默认链）；含 v4-relay 时 relay_server 必填
//   - mappings 逐条：protocol ∈ {tcp, udp}；local_port/target_port 均 1-65535；
//     target_host 非空；同 para 内 local_port 不得重复（I12）
//   - mappings 空 = 纯会话端，合法（只建会话不监听端口）
//
// 旧单组映射的顶层字段（protocol/local_port/target_host/target_port）已废弃，
// 无存量数据，不做兼容。
func (c P2PConfig) Validate() error {
	if !roomRegexp.MatchString(c.Room) {
		return fmt.Errorf("p2p room must be 8-32 chars of [a-zA-Z0-9_-]")
	}
	for _, m := range c.Modes {
		if !engine.AllModes[m] {
			return fmt.Errorf("p2p unknown mode %q", m)
		}
		if m == "v4-relay" && c.RelayServer == "" {
			// 复审 F12：v4-relay 无 relay_server 在运行期必然永久失败，
			// 且运行期错误文案引用的是不存在的 CLI flag，必须在配置期拦截
			return fmt.Errorf("p2p modes contains v4-relay but relay_server is empty (para field relay_server)")
		}
	}
	seen := make(map[int]bool, len(c.Mappings))
	for i, m := range c.Mappings {
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
