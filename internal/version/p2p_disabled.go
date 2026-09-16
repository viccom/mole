//go:build !p2p

package version

// buildP2PEnabled 构建能力标志：默认构建不含任何 P2P 代码
// （p2pController 为 nil，p2p 隧道静默不启动）
const buildP2PEnabled = false
