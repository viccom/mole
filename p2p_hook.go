package moleAgent_client

// p2pController 抽象 P2P 管理器能力。默认构建下 newP2PController() 返回 nil，
// 所有调用点 nil-safe，零 P2P 代码参与编译；-tags p2p 时由 client_p2p.go 提供真实现。
//
// 跨 tag 调用的成对文件模式（hook 无 tag，实现各带 p2p / !p2p）：
//
//	p2p_hook.go       本文件——接口 + 状态数据结构，无 tag
//	client_p2p.go     //go:build p2p      真实现
//	client_p2p_stub.go //go:build !p2p    空实现
type p2pController interface {
	// Notify 全量配置分发：实现内部过滤 TunnelTypeP2P + IsEnabled() 并解析 Para
	Notify(tunnels []Tunnel)
	// StatusByName 按隧道名查询运行时状态；不存在返 error
	StatusByName(name string) (P2PRuntime, error)
	// Close 关闭全部 handler
	Close()
}

// P2PRuntime P2P 隧道运行时状态（无 tag，collectTunnelStatuses 与
// buildTunnelStatus 两处状态收集共用；字段与 proxy/p2p.Runtime 一致，可直接转换）
type P2PRuntime struct {
	Running   bool
	Connected bool
	BytesIn   uint64
	BytesOut  uint64
	Error     string
}
