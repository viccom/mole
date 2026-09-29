//go:build !p2p

package moleAgent_client

// newP2PController 在默认构建下返回 nil（零 P2P 代码参与编译）。
// 调用点全部 nil-safe：配置可识别、存储、透传 p2p 类型，只是不运行。
func newP2PController(_ *Client) p2pController {
	return nil
}
