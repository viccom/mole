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

// ValidateP2PPara 校验 p2p 隧道 Para 的合法性。
// room 是共享密钥材料（知道 room 即可加入信令并推导 payload key），格式必须把关；
// 规则与客户端 internal/proxy/p2p 的 Validate 保持一致：
//   - room 必填，8-32 字符 [a-zA-Z0-9_-]
//   - modes 可选（缺省=客户端默认链），每个值必须 ∈ AllModes
//   - protocol 必填且 ∈ {tcp, udp}
//   - 发起端（target_host 非空）：local_port、target_port 均 1-65535
//   - 纯会话端（target_host 空）：target_port 必须为 0
func ValidateP2PPara(para json.RawMessage) error {
	var p struct {
		Room        string   `json:"room"`
		Modes       []string `json:"modes"`
		RelayServer string   `json:"relay_server"`
		Protocol    string   `json:"protocol"`
		LocalPort   int      `json:"local_port"`
		TargetHost  string   `json:"target_host"`
		TargetPort  int      `json:"target_port"`
	}
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
	if p.Protocol != "tcp" && p.Protocol != "udp" {
		return fmt.Errorf("%w: p2p protocol must be tcp or udp", ErrTunnelInvalid)
	}
	if p.TargetHost != "" {
		if p.LocalPort < 1 || p.LocalPort > 65535 {
			return fmt.Errorf("%w: p2p local_port must be 1-65535 for initiator side", ErrTunnelInvalid)
		}
		if p.TargetPort < 1 || p.TargetPort > 65535 {
			return fmt.Errorf("%w: p2p target_port must be 1-65535 for initiator side", ErrTunnelInvalid)
		}
	} else if p.TargetPort != 0 {
		return fmt.Errorf("%w: p2p target_port must be 0 for pure session side (no target_host)", ErrTunnelInvalid)
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
