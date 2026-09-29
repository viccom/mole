package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// MaxRegisterTunnels register 命令携带隧道列表的条数上限（REL-02）：
// 防异常客户端用超大列表冲击控制面内存与注册路径
const MaxRegisterTunnels = 256

const (
	maxConnsUpperBound           = 100000
	maxBandwidthUpperBound int64 = 10737418240 // 10 GB/s (显式 int64，避免 32-bit 平台 int 溢出)
)

// MaxConnsUpperBound 暴露给 service 层节点级限速校验复用
const MaxConnsUpperBound = maxConnsUpperBound

// ValidateTunnel 校验单条隧道配置的合法性。
// 实现位于 core：service 落库入口与 control 面 register 校验共用同一规则，
// 避免两处副本漂移（层分叉教训）
func ValidateTunnel(t Tunnel) error {
	if t.Name == "" {
		return fmt.Errorf("%w: tunnel name is required", ErrTunnelInvalid)
	}
	// 名称进入代理协议头 `\x00<name>\n`，自带控制字符会使流路由按行解析错位
	// （客户端 tunnel.Validate 同款拒绝，两端规则对齐，跨端审查 🟡-4）
	if strings.ContainsAny(t.Name, "\x00\n\r") {
		return fmt.Errorf("%w: tunnel name must not contain \\x00, \\n or \\r", ErrTunnelInvalid)
	}

	switch t.Type {
	case TunnelTypeHTTP, TunnelTypeHTTPS, TunnelTypeTCP, TunnelTypeUDP:
		// target 统一为 host:port 格式
		if t.Target == "" {
			return fmt.Errorf("%w: tunnel target is required", ErrTunnelInvalid)
		}
		host, port, err := net.SplitHostPort(t.Target)
		if err != nil {
			return fmt.Errorf("%w: tunnel target must be host:port format (e.g. 127.0.0.1:8080), got %q", ErrTunnelInvalid, t.Target)
		}
		if host == "" {
			return fmt.Errorf("%w: tunnel target host is required", ErrTunnelInvalid)
		}
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return fmt.Errorf("%w: tunnel target port must be 1-65535, got %q", ErrTunnelInvalid, port)
		}

		// listen_port 只对 TCP/UDP 生效：HTTP/HTTPS 走域名路由不占系统端口，
		// 零值合法；TCP/UDP 必须落在合法区间且避开服务自身保留端口（REL-01）
		if t.Type == TunnelTypeTCP || t.Type == TunnelTypeUDP {
			if err := ValidateTunnelListenPort(t.ListenPort); err != nil {
				return err
			}
		}

	// 客户端本地类型：服务端不验证 target 格式，只做基本校验
	case TunnelTypeSer2MQ, TunnelTypeVPNMgr, TunnelTypeSer2TCP, TunnelTypeSer2UDP, TunnelTypeWebSSH:
		// 这些类型的配置在 Para 字段中，客户端自己处理
		// 服务端只需要确保 Name 不为空即可

	case TunnelTypeP2P:
		// p2p 与上述本地类型同类（仅客户端处理），但 room 是密钥材料，Para 必须严格校验
		if err := ValidateP2PPara(t.Para); err != nil {
			return err
		}

	default:
		return fmt.Errorf("%w: unknown tunnel type %q", ErrTunnelInvalid, t.Type)
	}

	if err := ValidateRateLimit(t.RateLimit); err != nil {
		return err
	}

	return nil
}

// ValidateTunnels 校验隧道列表
func ValidateTunnels(tunnels []Tunnel) error {
	names := make(map[string]bool, len(tunnels))
	// listen_port -> 隧道名：TCP/UDP 网关监听是全局端口空间，
	// 同一次提交的列表内 listen_port 必须唯一（对齐隧道名唯一机制，REL-01）
	ports := make(map[int]string, len(tunnels))
	for i := range tunnels {
		if err := ValidateTunnel(tunnels[i]); err != nil {
			return err
		}
		if names[tunnels[i].Name] {
			return fmt.Errorf("%w: duplicate tunnel name %q", ErrTunnelInvalid, tunnels[i].Name)
		}
		names[tunnels[i].Name] = true
		if tunnels[i].Type == TunnelTypeTCP || tunnels[i].Type == TunnelTypeUDP {
			if prev, dup := ports[tunnels[i].ListenPort]; dup {
				return fmt.Errorf("%w: duplicate listen_port %d (tunnel %q and %q)", ErrTunnelInvalid, tunnels[i].ListenPort, prev, tunnels[i].Name)
			}
			ports[tunnels[i].ListenPort] = tunnels[i].Name
		}
	}
	return nil
}

// ValidateRateLimit 校验限速配置（拒绝零值/负值/极大值）
func ValidateRateLimit(rl *TunnelRateLimit) error {
	if rl == nil {
		return nil
	}
	if rl.MaxConns < 0 || rl.MaxConns > maxConnsUpperBound {
		return fmt.Errorf("%w: max_conns must be 1-%d, got %d", ErrTunnelInvalid, maxConnsUpperBound, rl.MaxConns)
	}
	if rl.MaxBandwidth < 0 || rl.MaxBandwidth > maxBandwidthUpperBound {
		return fmt.Errorf("%w: max_bandwidth must be 1-%d bytes/sec, got %d", ErrTunnelInvalid, maxBandwidthUpperBound, rl.MaxBandwidth)
	}
	if rl.MaxConns == 0 && rl.MaxBandwidth == 0 {
		return fmt.Errorf("%w: rate_limit must have at least one non-zero field, use null to clear", ErrTunnelInvalid)
	}
	return nil
}
