//go:build p2p

package moleAgent_client

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"

	"moleAgent_client/internal/protocol"
	"moleAgent_client/internal/proxy/p2p"
)

// client_p2p.go 是 -tags p2p 构建下的 p2pController 真实现：
// 持有 proxy/p2p.Manager，负责 Para 解析过滤与信令凭据拉取决策。

type p2pManagerController struct {
	mgr *p2p.Manager
	c   *Client // 凭据拉取走 smux 控制通道
}

// newP2PController 构造真实控制器。serverHost 从 ServerAddr 派生，
// 用于 Manager 生成默认 mqtt_brokers/stun_servers。
func newP2PController(c *Client) p2pController {
	mgr := p2p.NewManager(serverHostFromAddr(c.cfg.ServerAddr))
	// 信令凭据拉取策略（阶段 6.5）：仅当使用默认 server broker 时经控制通道
	// 拉取 P2PSignalToken；自定义/公共 broker 直接匿名，不依赖控制面
	mgr.SetCredsProvider(func(name string) (string, string, int64, error) {
		if !c.usesDefaultP2PBroker(name) {
			return "", "", 0, nil
		}
		return c.requestP2PSignalToken(name)
	})
	return &p2pManagerController{mgr: mgr, c: c}
}

func (p *p2pManagerController) Notify(tunnels []Tunnel) {
	configs := make(map[string]p2p.P2PConfig)
	for _, t := range tunnels {
		if t.Type != TunnelTypeP2P || !t.IsEnabled() || t.Para == nil {
			continue
		}
		cfg, err := p2p.FromPara(t.Para)
		if err != nil {
			log.Printf("notifyManagers: unmarshal p2p %q failed: %v", t.Name, err)
			continue
		}
		configs[t.Name] = cfg
	}
	p.mgr.OnTunnelUpdate(configs)
}

func (p *p2pManagerController) StatusByName(name string) (P2PRuntime, error) {
	rt, err := p.mgr.Status(name)
	if err != nil {
		return P2PRuntime{}, err
	}
	// 字段一致的匿名结构体可直接转换（编译期校验两处定义不漂移）
	return P2PRuntime(rt), nil
}

func (p *p2pManagerController) Close() {
	p.mgr.Close()
}

// usesDefaultP2PBroker 判断隧道是否使用默认 server broker（mqtt_brokers 为空
// 或恰好等于派生默认值）。仅此情形拉取 P2P token；自定义/公共 broker 匿名。
func (c *Client) usesDefaultP2PBroker(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, t := range c.tunnels {
		if t.Name != name || t.Type != TunnelTypeP2P {
			continue
		}
		cfg, err := p2p.FromPara(t.Para)
		if err != nil {
			return false
		}
		if len(cfg.MQTTBrokers) == 0 {
			return true
		}
		return equalStringSlices(cfg.MQTTBrokers, p2p.DefaultMQTTBrokers(serverHostFromAddr(c.cfg.ServerAddr)))
	}
	return false
}

// requestP2PSignalToken 经现有已认证 smux 控制通道请求 P2P 信令凭据（C→S
// p2p_signal_token）。响应为 ad-hoc 平铺 JSON（两端各自解码，不扩 ControlResponse）；
// 凭据只存内存，进程重启/过期由调用方（Handler 每次连接尝试前拉取）自然重取。
func (c *Client) requestP2PSignalToken(name string) (username, password string, expiresAt int64, err error) {
	c.ctrlMu.Lock()
	defer c.ctrlMu.Unlock()

	session := c.transport.Session()
	if session == nil || session.IsClosed() {
		return "", "", 0, fmt.Errorf("session not available")
	}
	stream, err := session.OpenStream()
	if err != nil {
		return "", "", 0, fmt.Errorf("open p2p_signal_token stream: %w", err)
	}
	defer stream.Close()

	if err := writeCmd(stream, protocol.ControlCmd{Cmd: "p2p_signal_token", NodeID: c.cfg.NodeID, Name: name}); err != nil {
		return "", "", 0, fmt.Errorf("send p2p_signal_token: %w", err)
	}
	// 平铺响应走 readControlMsg 裸读，deadline 在读前设置（对齐 readResponse 惯例）
	if c.cfg.HeartbeatTimeout > 0 {
		_ = stream.SetReadDeadline(time.Now().Add(c.cfg.HeartbeatTimeout))
		defer func() { _ = stream.SetReadDeadline(time.Time{}) }()
	}
	raw, err := readControlMsg(stream, maxControlMsgSize)
	if err != nil {
		return "", "", 0, fmt.Errorf("read p2p_signal_token response: %w", err)
	}
	var resp struct {
		Cmd       string `json:"cmd"`
		OK        bool   `json:"ok"`
		Error     string `json:"error,omitempty"`
		Username  string `json:"username,omitempty"`
		Password  string `json:"password,omitempty"`
		ExpiresAt int64  `json:"expires_at,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", 0, fmt.Errorf("unmarshal p2p_signal_token response: %w", err)
	}
	if !resp.OK {
		return "", "", 0, fmt.Errorf("p2p_signal_token rejected: %s", resp.Error)
	}
	return resp.Username, resp.Password, resp.ExpiresAt, nil
}

// serverHostFromAddr 从 ServerAddr（host:port）提取 host
func serverHostFromAddr(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		return host
	}
	return addr
}

// equalStringSlices 顺序敏感的切片相等比较
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
