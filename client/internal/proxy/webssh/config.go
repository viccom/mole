package webssh

import "fmt"

// WebSSHConfig WebSSH 隧道配置
type WebSSHConfig struct {
	Enable   bool   `json:"enable"`
	Host     string `json:"host"`               // 目标 SSH 主机（IP 或域名）
	Port     int    `json:"port"`               // SSH 端口，默认 22
	User     string `json:"user"`               // SSH 用户名
	AuthType string `json:"auth_type"`          // "password" 或 "key"
	Password string `json:"password,omitempty"` // 密码认证
	PrivKey  string `json:"priv_key,omitempty"` // PEM 格式私钥内容
}

// Equal 比较两个配置是否相同（避免 == 在未来添加 slice/map 字段时出错）
func (c WebSSHConfig) Equal(other WebSSHConfig) bool {
	return c.Enable == other.Enable &&
		c.Host == other.Host &&
		c.Port == other.Port &&
		c.User == other.User &&
		c.AuthType == other.AuthType &&
		c.Password == other.Password &&
		c.PrivKey == other.PrivKey
}

// Validate 校验配置合法性
func (c *WebSSHConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("webssh: host is required")
	}
	if c.User == "" {
		return fmt.Errorf("webssh: user is required")
	}
	if c.Port <= 0 {
		c.Port = 22
	}
	switch c.AuthType {
	case "password":
		if c.Password == "" {
			return fmt.Errorf("webssh: password is required for password auth")
		}
	case "key":
		if c.PrivKey == "" {
			return fmt.Errorf("webssh: priv_key is required for key auth")
		}
	case "":
		return fmt.Errorf("webssh: auth_type is required (password or key)")
	default:
		return fmt.Errorf("webssh: unsupported auth_type %q", c.AuthType)
	}
	return nil
}
