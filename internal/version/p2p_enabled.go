//go:build p2p

package version

// buildP2PEnabled 构建能力标志：-tags p2p 构建含 P2P 打洞代码。
// 与根包 client_p2p.go/client_p2p_stub.go 的成对 tag 文件模式同构
const buildP2PEnabled = true
