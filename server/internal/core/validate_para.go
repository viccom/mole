package core

import (
	"encoding/json"
	"fmt"

	"mole/shared/tunnelvalidate"
)

// p2p Para 校验规则（modeSet/room 正则/mappings 与全部文案）已单源化至
// mole/shared/tunnelvalidate（双端同源）。本文件保留 server 侧薄适配与
// P2PRoom 提取函数（配对校验专用，非校验器的一部分）。

// ValidateP2PPara 校验 p2p 隧道 Para 的合法性（协议契约 §0.3）。
// room 是共享密钥材料（知道 room 即可加入信令并推导 payload key），格式必须把关。
func ValidateP2PPara(para json.RawMessage) error {
	if err := tunnelvalidate.ValidateP2PPara(para); err != nil {
		return fmt.Errorf("%w: %w", ErrTunnelInvalid, err)
	}
	return nil
}

// P2PRoom 提取 p2p 隧道 Para 中的 room（供配对校验使用）。
// Para 缺失/非 JSON/无 room 返回错误——room 是 p2p 配对的必填密钥材料。
func P2PRoom(para json.RawMessage) (string, error) {
	var p struct {
		Room string `json:"room"`
	}
	if err := json.Unmarshal(para, &p); err != nil {
		return "", fmt.Errorf("parse p2p para: %w", err)
	}
	if p.Room == "" {
		return "", fmt.Errorf("p2p room is required")
	}
	return p.Room, nil
}
