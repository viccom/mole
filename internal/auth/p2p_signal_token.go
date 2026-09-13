package auth

import "strings"

// P2PSignalUsernamePrefix P2P 信令 MQTT 用户名前缀（哨兵字符串本身即 broker 侧
// 身份载体：authHook 据此走 token 校验，aclHook 据此限定 nat-exchange/*）
const P2PSignalUsernamePrefix = "p2p-signal:"

// IsP2PSignalUsername 判断 MQTT 用户名是否为 P2P 信令哨兵
func IsP2PSignalUsername(username string) bool {
	return strings.HasPrefix(username, P2PSignalUsernamePrefix)
}

// P2PSignalTokenVerifier MQTT broker 校验 P2P 信令凭据的接口（service 包实现）。
// P2P token 是单一用途凭据：仅授予 nat-exchange/* 读写，与用户体系（JWT/RBAC）
// 彻底隔离——即使泄露也不能登录 admin 或触达其他节点。
type P2PSignalTokenVerifier interface {
	VerifyP2PSignalToken(tokenID, password string) bool
}
