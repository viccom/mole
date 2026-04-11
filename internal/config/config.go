package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 全局配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	MQTT     MQTTConfig     `yaml:"mqtt"`
	Auth     AuthConfig     `yaml:"auth"`
	Database DatabaseConfig `yaml:"database"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type ServerConfig struct {
	ControlPort   string         `yaml:"control_port"`    // Node 控制端口，如 :9981
	GatewayPort   string         `yaml:"gateway_port"`   // 外部请求网关端口，如 :9980
	APIPort       string         `yaml:"api_port"`        // REST API 端口，如 :9983
	MaxNodes      int            `yaml:"max_nodes"`       // 最大节点数
	MaxConcurrent int            `yaml:"max_concurrent"`  // 最大并发连接数
	TLS           TLSConfig      `yaml:"tls"`             // TLS 配置
	Gateway       GatewayConfig  `yaml:"gateway"`         // 网关配置
}

// TLSConfig 控制端口 TLS 配置
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`   // 是否启用 TLS
	CertFile string `yaml:"cert_file"` // 证书文件路径
	KeyFile  string `yaml:"key_file"`  // 私钥文件路径
}

// GatewayConfig 网关配置
type GatewayConfig struct {
	HyphenRouting bool `yaml:"hyphen_routing"` // 泛域名分隔符：true=hyphen(-), false=dot(.)
}

type MQTTConfig struct {
	Enabled bool   `yaml:"enabled"`
	TCPPort string `yaml:"tcp_port"` // 如 :1883
	WSPort  string `yaml:"ws_port"`  // 如 :1882
}

type AuthConfig struct {
	JWTSecret  string `yaml:"jwt_secret"`  // 必须配置
	JWTExpiry  string `yaml:"jwt_expiry"`  // 如 24h
	BcryptCost int    `yaml:"bcrypt_cost"` // 默认 12
}

type DatabaseConfig struct {
	Path string `yaml:"path"` // 如 data/config.db
}

type LoggingConfig struct {
	Level   string           `yaml:"level"`  // debug, info, warn, error
	Format  string           `yaml:"format"` // text, json
	File    FileLogConfig    `yaml:"file"`
	Console ConsoleLogConfig `yaml:"console"`
}

type FileLogConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Path       string `yaml:"path"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAgeDays int    `yaml:"max_age_days"`
	Compress   bool   `yaml:"compress"`
}

type ConsoleLogConfig struct {
	Enabled bool `yaml:"enabled"`
	Color   bool `yaml:"color"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			ControlPort:   ":9981",
			GatewayPort:   ":9980",
			APIPort:       ":9983",
			MaxNodes:      10000,
			MaxConcurrent: 50000,
			Gateway: GatewayConfig{
				HyphenRouting: true, // 默认使用 hyphen(-) 作为泛域名分隔符
			},
		},
		MQTT: MQTTConfig{
			Enabled: true,
			TCPPort: ":1883",
			WSPort:  ":1882",
		},
		Auth: AuthConfig{
			JWTSecret:  "",
			JWTExpiry:  "24h",
			BcryptCost: 12,
		},
		Database: DatabaseConfig{
			Path: "data/config.db",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
			File: FileLogConfig{
				Enabled:    true,
				Path:       "logs/moleagent.log",
				MaxSizeMB:  100,
				MaxBackups: 10,
				MaxAgeDays: 30,
				Compress:   true,
			},
			Console: ConsoleLogConfig{
				Enabled: true,
				Color:   true,
			},
		},
	}
}

// Load 从文件加载配置并合并默认值和环境变量
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	// 加载 YAML 文件
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config file: %w", err)
		}
	}

	// 环境变量覆盖
	applyEnvOverrides(cfg)

	// 验证
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("MA_JWT_SECRET"); v != "" {
		cfg.Auth.JWTSecret = v
	}
	if v := os.Getenv("MA_ADMIN_USER"); v != "" {
		// 种子数据使用，不在 config 结构中存储
		_ = v
	}
	if v := os.Getenv("MA_ADMIN_PASS"); v != "" {
		_ = v
	}
	if v := os.Getenv("MA_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("MA_CONTROL_PORT"); v != "" {
		cfg.Server.ControlPort = v
	}
	if v := os.Getenv("MA_GATEWAY_PORT"); v != "" {
		cfg.Server.GatewayPort = v
	}
	if v := os.Getenv("MA_API_PORT"); v != "" {
		cfg.Server.APIPort = v
	}
	if v := os.Getenv("MA_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := os.Getenv("MA_TLS_ENABLED"); v != "" {
		cfg.Server.TLS.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MA_TLS_CERT"); v != "" {
		cfg.Server.TLS.CertFile = v
	}
	if v := os.Getenv("MA_TLS_KEY"); v != "" {
		cfg.Server.TLS.KeyFile = v
	}
}

func (c *Config) validate() error {
	if c.Auth.JWTSecret == "" {
		return fmt.Errorf("auth.jwt_secret is required (set via yaml or MA_JWT_SECRET env)")
	}
	if len(c.Auth.JWTSecret) < 16 {
		return fmt.Errorf("auth.jwt_secret must be at least 16 characters")
	}

	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[strings.ToLower(c.Logging.Level)] {
		return fmt.Errorf("invalid logging level: %s (must be debug/info/warn/error)", c.Logging.Level)
	}

	validFormats := map[string]bool{"text": true, "json": true}
	if !validFormats[strings.ToLower(c.Logging.Format)] {
		return fmt.Errorf("invalid logging format: %s (must be text/json)", c.Logging.Format)
	}

	if c.Auth.BcryptCost < 4 || c.Auth.BcryptCost > 31 {
		return fmt.Errorf("bcrypt_cost must be between 4 and 31")
	}

	if c.Server.TLS.Enabled {
		if c.Server.TLS.CertFile == "" {
			return fmt.Errorf("server.tls.cert_file is required when TLS is enabled")
		}
		if c.Server.TLS.KeyFile == "" {
			return fmt.Errorf("server.tls.key_file is required when TLS is enabled")
		}
	}

	return nil
}

// AdminUser 从环境变量获取种子管理员凭据
func AdminUser() (username, password string) {
	u := os.Getenv("MA_ADMIN_USER")
	p := os.Getenv("MA_ADMIN_PASS")
	if u == "" {
		u = "admin"
	}
	if p == "" {
		p = "admin"
	}
	return u, p
}
