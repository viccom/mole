package core

import (
	"fmt"

	"mole/shared/listenport"
)

// ValidateTunnelListenPort 校验单条 TCP/UDP 隧道的监听端口（REL-01）：
// 必须在 1-65535，且不得落在服务自身保留端口集合上。
// service 层校验与 control 面 register 校验共用，避免两处规则漂移。
// 规则与保留端口集合已单源化至 mole/shared/listenport（client 侧
// validateListenPort 同源）；本函数是薄适配层，仅按服务端错误体系补
// ErrTunnelInvalid 前缀包装，错误文案与单源化前逐字节一致。
func ValidateTunnelListenPort(port int) error {
	if err := listenport.Validate(port); err != nil {
		return fmt.Errorf("%w: %w", ErrTunnelInvalid, err)
	}
	return nil
}
