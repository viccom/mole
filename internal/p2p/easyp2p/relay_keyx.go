//go:build p2p

package easyp2p

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"moleAgent_client/internal/p2p/crypto"
)

// relayKeyExchange 是 MQTT 交换的公钥 payload（AES 加密由 MQTT_SecureExchangeWithSession 负责）。
type relayKeyExchange struct {
	PubKey string // base64(uncompressed P-256 public key, 65 字节)
}

// ExchangeRelayKey 通过 MQTT 交换 ECDHE 公钥，派生 sharedKey + 判定 isClient。
// 复用 MQTT_SecureExchangeWithSession（p2p.go:196）+ crypto.ECDHEKey。
// 模式参考 v6_direct.go:27-37（同款 signal session + EXMODE_mutual 交换，已验证可用）。
// 用 crypto.ECDHEKey（crypto/ecdh + HKDF），与 Do_autoP2PEx2 的 ecdsa+ScalarMult 路径
// 不同，但产出 *[32]byte 兼容（secureUpgrade 只作 PSK 字节，不关心派生路径）。
// sessionUid = room（与现有 mode 一致，room 即 PSK）。
func ExchangeRelayKey(ctx context.Context, signal *MQTTSignalSession, sessionUid string, timeout time.Duration) (
	sharedKey *[crypto.KeyLen]byte, isClient bool, err error,
) {
	myKey, err := crypto.GenerateECDHEKey()
	if err != nil {
		return nil, false, err
	}
	myPub := myKey.PublicKeyBytes()
	payload := relayKeyExchange{PubKey: base64.StdEncoding.EncodeToString(myPub)}
	if err := signal.prepareTopic(ctx, "p2punch-relay-keyx", sessionUid); err != nil { // 专属 topic salt
		return nil, false, err
	}
	remote, _, err := MQTT_SecureExchangeWithSession[relayKeyExchange](
		ctx, signal, EXMODE_mutual, payload,
		MQTT_GenerateClientID(TopicDesc_Signal, sessionUid, 0),
		"p2punch-relay-keyx", sessionUid, timeout, nil)
	if err != nil {
		return nil, false, err
	}
	peerPubBytes, err := base64.StdEncoding.DecodeString(remote.PubKey)
	if err != nil {
		return nil, false, fmt.Errorf("decode peer pubkey: %w", err)
	}
	peerPub, err := crypto.FromPublicKeyBytes(peerPubBytes)
	if err != nil {
		return nil, false, fmt.Errorf("parse peer pubkey: %w", err)
	}
	sharedKey, err = myKey.DeriveSharedKey(peerPub) // HKDF-SHA256 → *[32]byte
	if err != nil {
		return nil, false, err
	}
	isClient = bytes.Compare(myPub, peerPubBytes) < 0 // 公钥字典序，确定性互补
	return sharedKey, isClient, nil
}

// RelayPairingToken 从 sharedKey 派生 relay 配对 token（两端一致，relay server 零状态配对）。
// DeriveKey 返回 (*[32]byte, error)（HKDF-SHA256，key.go:17），取前 16 字节 hex = 32 字符。
func RelayPairingToken(sharedKey *[crypto.KeyLen]byte) (string, error) {
	t, err := crypto.DeriveKey(string(sharedKey[:]), "p2punch-relay-pairing-v1")
	if err != nil {
		return "", err
	}
	return hex.EncodeToString((*t)[:16]), nil
}
