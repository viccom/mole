//go:build p2p

package moleAgent_client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"mole/shared/proto"
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
	mgr.SetCredsProvider(func(ctx context.Context, name string) (string, string, int64, error) {
		if !c.usesDefaultP2PBroker(name) {
			return "", "", 0, nil
		}
		return c.requestP2PSignalToken(ctx, name)
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
	return toP2PRuntime(rt), nil
}

// toP2PRuntime 逐字段转换：Go 结构体直接转换不转换切片元素类型
// （Runtime.Mappings 是 proxy/p2p.MappingStatus，P2PRuntime 需要无 tag 镜像类型），
// 故手写；字段漂移由 TestP2PRuntimeConversionParity 以 JSON 比对兜底
func toP2PRuntime(rt p2p.Runtime) P2PRuntime {
	out := P2PRuntime{
		Running:     rt.Running,
		Connected:   rt.Connected,
		Mode:        rt.Mode,
		LocalAddr:   rt.LocalAddr,
		RemoteAddr:  rt.RemoteAddr,
		PunchMs:     rt.PunchMs,
		ConnectedAt: rt.ConnectedAt,
		Reconnects:  rt.Reconnects,
		BytesIn:     rt.BytesIn,
		BytesOut:    rt.BytesOut,
		Error:       rt.Error,
	}
	if len(rt.Mappings) > 0 {
		out.Mappings = make([]P2PMappingStatus, len(rt.Mappings))
		for i, m := range rt.Mappings {
			out.Mappings[i] = P2PMappingStatus{
				Protocol:   m.Protocol,
				LocalPort:  m.LocalPort,
				TargetHost: m.TargetHost,
				TargetPort: m.TargetPort,
				BytesIn:    m.BytesIn,
				BytesOut:   m.BytesOut,
				Up:         m.Up,
				Remote:     m.Remote,
				Error:      m.Error,
			}
		}
	}
	return out
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
			return true // 空列表 = 默认 server broker（复审 F14 修复：此早退不可省）
		}
		// 归一化签名比较（复审 F14）：写法不同但指向相同 broker 的配置
		// 不应被误判为自定义 broker 而匿名连接（会永久被 server broker 拒绝）
		return p2p.MQTTBrokerSignature(cfg.MQTTBrokers) ==
			p2p.MQTTBrokerSignature(p2p.DefaultMQTTBrokers(serverHostFromAddr(c.cfg.ServerAddr)))
	}
	return false
}

// requestP2PSignalToken 经现有已认证 smux 控制通道请求 P2P 信令凭据（C→S
// p2p_signal_token）。响应为 ad-hoc 平铺 JSON（两端各自解码，不扩 ControlResponse）；
// 凭据只存内存，进程重启/过期由调用方（Handler 每次连接尝试前拉取）自然重取。
func (c *Client) requestP2PSignalToken(ctx context.Context, name string) (username, password string, expiresAt int64, err error) {
	// 复审 F1：ctrlMu 获取必须可取消——隧道拆除时本请求若持锁排队，
	// 不可取消的等待会与 Manager.mu/ctrlMu 构成三路死锁环
	if err := c.tryLockCtrlMu(ctx); err != nil {
		return "", "", 0, err
	}
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

	if err := writeCmd(stream, protocol.ControlCmd{Cmd: proto.CmdP2PSignalToken, NodeID: c.cfg.NodeID, Name: name}); err != nil {
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
	// 响应结构已单源至 mole/shared/proto（P2PSignalTokenResp，与 server 端
	// p2pSignalTokenResp 同一类型，json tag 逐字一致）
	var resp proto.P2PSignalTokenResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", "", 0, fmt.Errorf("unmarshal p2p_signal_token response: %w", err)
	}
	if !resp.OK {
		return "", "", 0, fmt.Errorf("p2p_signal_token rejected: %s", resp.Error)
	}
	return resp.Username, resp.Password, resp.ExpiresAt, nil
}

// tryLockCtrlMu 可取消地获取 ctrlMu（sync.Mutex 无 ctx 感知，轮询 TryLock）
func (c *Client) tryLockCtrlMu(ctx context.Context) error {
	for {
		if c.ctrlMu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// serverHostFromAddr 从 ServerAddr 提取 host。ws/wss 传输允许 URL 写法
// （ws://host:port/path，见 config 校验与 ws dialer）：带 scheme 时直接
// SplitHostPort 会因 host 段含 ":" 报错并原样返回整个 URL，进而派生出
// tcp://[ws://host:port]:1883 这类非法 broker/STUN 地址，p2p 隧道永远建不起来
func serverHostFromAddr(addr string) string {
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
		if j := strings.IndexByte(addr, '/'); j >= 0 {
			addr = addr[:j]
		}
	}
	if host, _, err := net.SplitHostPort(addr); err == nil && host != "" {
		return host
	}
	return addr
}
