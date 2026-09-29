package ratelimit

// RateLimitConfig top-level rate limiting configuration.
type RateLimitConfig struct {
	API     APIRateLimitConfig     `yaml:"api"`
	Gateway GatewayRateLimitConfig `yaml:"gateway"`
}

// APIRateLimitConfig controls REST API request rate limiting.
type APIRateLimitConfig struct {
	Enabled bool `yaml:"enabled"` // master switch
	PerUser int  `yaml:"per_user"` // requests/sec per user (0 = unlimited)
	PerIP   int  `yaml:"per_ip"`   // requests/sec per IP (0 = unlimited)
	Burst          int      `yaml:"burst"`                  // token bucket burst size
	TrustedProxies []string `yaml:"trusted_proxies,omitempty"` // CIDRs of trusted reverse proxies for X-Forwarded-For
}

// GatewayRateLimitConfig controls gateway connection and bandwidth limiting.
type GatewayRateLimitConfig struct {
	Enabled           bool  `yaml:"enabled"`             // master switch
	MaxConnsPerNode   int   `yaml:"max_conns_per_node"`  // max concurrent connections per node (0 = unlimited)
	MaxConnsPerTunnel int   `yaml:"max_conns_per_tunnel"` // max concurrent connections per tunnel (0 = unlimited)
	MaxBandwidthPerTunnel int64 `yaml:"max_bandwidth_per_tunnel"` // default bandwidth limit bytes/sec (0 = unlimited)
	BWBurst           int   `yaml:"bw_burst"`            // bandwidth token bucket burst bytes
}

// TunnelRateConfig per-tunnel rate limit overrides (stored in Tunnel config).
type TunnelRateConfig struct {
	MaxConns int   `json:"max_conns,omitempty"` // override max_conns_per_tunnel
	MaxBandwidth int64 `json:"max_bandwidth,omitempty"` // override bandwidth limit (bytes/sec)
}

// NodeRateConfig per-node rate limit overrides (stored in Node config).
type NodeRateConfig struct {
	MaxConns int `json:"max_conns,omitempty"` // override max_conns_per_node
}
