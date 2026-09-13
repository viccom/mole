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

// P2PConfig 对应服务端存储的 p2p 隧道 Para（json tag 与服务端 core.ValidateP2PPara 校验字段一致）。
//
// 隧道两端不对称（tunnel.go 语义）：
//   - 发起端（TargetHost 非空）：session 建立后 CreateTunnel 监听 LocalPort，
//     用户连 发起端:LocalPort，由对端 dial TargetHost:TargetPort
//   - 纯会话端（TargetHost 空）：只建 session 供对端 OPEN，自身不 CreateTunnel
//
// Enable 不入结构体：Manager 以「配置从 map 消失」为停机信号（同 ser2mq/ser2net/webssh），
// 由上层 notifyManagers 用 IsEnabled() 过滤后再下发。
type P2PConfig struct {
	Room        string   `json:"room"`                   // 8-32 [a-zA-Z0-9_-]，共享密钥材料
	Modes       []string `json:"modes,omitempty"`        // 空 = engine.DefaultModes
	RelayServer string   `json:"relay_server,omitempty"` // v4-relay 预留
	MQTTBrokers []string `json:"mqtt_brokers,omitempty"` // 空 = tcp://<serverHost>:1883
	STUNServers []string `json:"stun_servers,omitempty"` // 空 = 公共列表 + serverHost:3478
	Protocol    string   `json:"protocol"`               // "tcp" / "udp"，恒填
	LocalPort   int      `json:"local_port,omitempty"`   // 仅发起端：本端监听端口
	TargetHost  string   `json:"target_host,omitempty"`  // 仅发起端填写；空 = 纯会话端
	TargetPort  int      `json:"target_port,omitempty"`  // 仅发起端；纯会话端必须为 0
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

// Validate 校验规则（与 server core.ValidateP2PPara 保持一致）：
//   - room 8-32 [a-zA-Z0-9_-]
//   - modes ⊆ engine.AllModes（空 = 默认链）
//   - protocol ∈ {tcp, udp} 恒校验
//   - 发起端：local_port、target_port 均 1-65535
//   - 纯会话端：target_port 必须为 0
func (c P2PConfig) Validate() error {
	if !roomRegexp.MatchString(c.Room) {
		return fmt.Errorf("p2p room must be 8-32 chars of [a-zA-Z0-9_-]")
	}
	for _, m := range c.Modes {
		if !engine.AllModes[m] {
			return fmt.Errorf("p2p unknown mode %q", m)
		}
	}
	if c.Protocol != "tcp" && c.Protocol != "udp" {
		return fmt.Errorf("p2p protocol must be tcp or udp")
	}
	if c.TargetHost != "" {
		if c.LocalPort < 1 || c.LocalPort > 65535 {
			return fmt.Errorf("p2p local_port must be 1-65535 for initiator side")
		}
		if c.TargetPort < 1 || c.TargetPort > 65535 {
			return fmt.Errorf("p2p target_port must be 1-65535 for initiator side")
		}
	} else if c.TargetPort != 0 {
		return fmt.Errorf("p2p target_port must be 0 for pure session side (no target_host)")
	}
	return nil
}
