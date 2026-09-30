package core

// IsValidNodeID 验证节点 ID 格式：固定 8 个 ASCII 字符，首字符字母，其余字母或数字。
// 逐字节校验（不用 unicode.IsLetter），避免多字节字符因字节长度恰好为 8 而混过。
// 必须与客户端 moleAgent_client.ValidateNodeID 保持一致。
// 单一事实源：register 路径（tunnel/control.go 的 isValidNodeID 委托此函数）
// 与 REST 建节点（api/node_handler.go 的 Create）共用，避免两路口径漂移。
func IsValidNodeID(id string) bool {
	if len(id) != 8 {
		return false
	}
	for i := 0; i < 8; i++ {
		c := id[i]
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if i == 0 {
			if !isLetter {
				return false
			}
			continue
		}
		if !isLetter && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
