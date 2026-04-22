package ser2mq

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	KeySize   = 32                      // 32 字节密钥
	NonceSize = chacha20poly1305.NonceSize // 24 字节 nonce (ChaCha20-Poly1305)
)

// Crypto 加解密模块，使用 ChaCha20-Poly1305
type Crypto struct {
	key []byte
}

// NewCrypto 创建加解密模块，key 必须是 32 字节的 hex 字符串
func NewCrypto(keyHex string) (*Crypto, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid key hex: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes, got %d", KeySize, len(key))
	}
	return &Crypto{key: key}, nil
}

// Encrypt 加密数据
// 返回: 版本号(1字节) + Nonce(24字节) + 密文+Tag
func (c *Crypto) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	aead, err := chacha20poly1305.NewX(c.key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	out := make([]byte, 0, 1+NonceSize+len(plaintext)+aead.Overhead())
	out = append(out, 0x01)         // 版本号
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, plaintext, nil)
	return out, nil
}

// Decrypt 解密数据
// 输入: 版本号(1字节) + Nonce(24字节) + 密文+Tag
func (c *Crypto) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 1+NonceSize+chacha20poly1305.Overhead {
		return nil, fmt.Errorf("ciphertext too short")
	}

	version := ciphertext[0]
	if version != 0x01 {
		return nil, fmt.Errorf("unsupported version: 0x%02x", version)
	}

	nonce := ciphertext[1 : 1+NonceSize]
	cipherData := ciphertext[1+NonceSize:]

	aead, err := chacha20poly1305.NewX(c.key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	return aead.Open(cipherData[:0], nonce, cipherData, nil)
}

// GenerateKey 生成随机密钥（32字节 hex 字符串）
func GenerateKey() (string, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return hex.EncodeToString(key), nil
}
