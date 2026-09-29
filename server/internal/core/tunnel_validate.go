package core

import (
	"fmt"

	"mole/shared/tunnelvalidate"
)

// 校验规则已单源化至 mole/shared/tunnelvalidate（双端同源，差异经 Options
// 参数化——见 docs/decisions.md 隧道校验差异裁决表）。本文件是 server 侧薄
// 适配层：保持既有导出名与 ErrTunnelInvalid 包装语义，调用方零改动。
//
// server 语义 = tunnelvalidate.ServerOptions()：五类客户端本地隧道 + p2p
// 空 target 合法；http/https target 不做 scheme 检查（畸形形态仍被
// host:port 格式校验拒绝）。

// MaxRegisterTunnels register 命令携带隧道列表的条数上限（REL-02）：
// 防异常客户端用超大列表冲击控制面内存与注册路径
const MaxRegisterTunnels = tunnelvalidate.MaxRegisterTunnels

// MaxConnsUpperBound 暴露给 service 层节点级限速校验复用
const MaxConnsUpperBound = tunnelvalidate.MaxConnsUpperBound

// toValidateView 转换为共享校验视图（字段同名直拷；RateLimit 结构转换）
func toValidateView(t Tunnel) tunnelvalidate.Tunnel {
	tv := tunnelvalidate.Tunnel{
		Name:       t.Name,
		Type:       string(t.Type),
		Target:     t.Target,
		ListenPort: t.ListenPort,
		Para:       t.Para,
	}
	if t.RateLimit != nil {
		tv.RateLimit = &tunnelvalidate.RateLimit{
			MaxConns:     t.RateLimit.MaxConns,
			MaxBandwidth: t.RateLimit.MaxBandwidth,
		}
	}
	return tv
}

// ValidateTunnel 校验单条隧道配置的合法性。
// 实现位于 core：service 落库入口与 control 面 register 校验共用同一规则，
// 避免两处副本漂移（层分叉教训）
func ValidateTunnel(t Tunnel) error {
	if err := tunnelvalidate.Validate(toValidateView(t), tunnelvalidate.ServerOptions()); err != nil {
		return fmt.Errorf("%w: %w", ErrTunnelInvalid, err)
	}
	return nil
}

// ValidateTunnels 校验隧道列表
func ValidateTunnels(tunnels []Tunnel) error {
	views := make([]tunnelvalidate.Tunnel, len(tunnels))
	for i := range tunnels {
		views[i] = toValidateView(tunnels[i])
	}
	if err := tunnelvalidate.ValidateList(views, tunnelvalidate.ServerOptions()); err != nil {
		return fmt.Errorf("%w: %w", ErrTunnelInvalid, err)
	}
	return nil
}

// ValidateRateLimit 校验限速配置（拒绝零值/负值/极大值）
func ValidateRateLimit(rl *TunnelRateLimit) error {
	var view *tunnelvalidate.RateLimit
	if rl != nil {
		view = &tunnelvalidate.RateLimit{
			MaxConns:     rl.MaxConns,
			MaxBandwidth: rl.MaxBandwidth,
		}
	}
	if err := tunnelvalidate.ValidateRateLimit(view); err != nil {
		return fmt.Errorf("%w: %w", ErrTunnelInvalid, err)
	}
	return nil
}
