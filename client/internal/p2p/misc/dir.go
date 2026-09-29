//go:build p2p

package misc

import (
	"errors"
	"fmt"
	"os"
)

// ValidateReceiveDir 校验文件接收目录：空串合法（= CWD，保持原行为）；
// 非空必须存在且为目录。用于应用层（desktop/CLI）在设置接收目录时拒绝无效路径，
// 不让无效 dir 进入 session 核心（R10）。session 核心只负责 join，校验单一来源在此。
func ValidateReceiveDir(path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("接收目录不可用: %w", err)
	}
	if !info.IsDir() {
		return errors.New("路径不是目录: " + path)
	}
	return nil
}
