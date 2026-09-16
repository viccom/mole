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

// P2PMappingStatus 单条端口映射运行时状态（无 tag 镜像）。字段名、顺序、json tag
// 与 proxy/p2p.MappingStatus 逐字一致——本文件不能 import p2p tag 包，故镜像定义；
// 漂移由 client_p2p_test.go 的 TestP2PRuntimeConversionParity（JSON 比对）防住
type P2PMappingStatus struct {
	Protocol   string `json:"protocol"`
	LocalPort  int    `json:"local_port"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
	BytesIn    uint64 `json:"bytes_in"`
	BytesOut   uint64 `json:"bytes_out"`
	Up         bool   `json:"up"`
	Remote     bool   `json:"remote,omitempty"`
	Error      string `json:"error,omitempty"`
}

// P2PRuntime P2P 隧道运行时状态（无 tag，collectTunnelStatuses 与 buildTunnelStatus
// 两处状态收集共用）。字段与 proxy/p2p.Runtime 逐字段镜像：Go 结构体直接转换不转换
// 切片元素类型（Mappings 是 p2p tag 包类型），由 client_p2p.go 的 toP2PRuntime
// 逐字段转换 + TestP2PRuntimeConversionParity 防漂移
type P2PRuntime struct {
	Running     bool               `json:"running"`
	Connected   bool               `json:"connected"`
	Mode        string             `json:"mode,omitempty"`
	LocalAddr   string             `json:"local_addr,omitempty"`
	RemoteAddr  string             `json:"remote_addr,omitempty"`
	PunchMs     int64              `json:"punch_ms,omitempty"`
	ConnectedAt int64              `json:"connected_at,omitempty"`
	Reconnects  int                `json:"reconnects"`
	BytesIn     uint64             `json:"bytes_in"`
	BytesOut    uint64             `json:"bytes_out"`
	Error       string             `json:"error,omitempty"`
	Mappings    []P2PMappingStatus `json:"mappings,omitempty"`
}
