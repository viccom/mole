//go:build p2p

package p2p

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"

	"moleAgent_client/internal/p2p/engine"
)

// ErrNotFound 请求的 p2p 隧道不存在（未下发或已删除）
var ErrNotFound = errors.New("p2p tunnel not found")

// Manager 多 p2p 隧道生命周期管理（ser2mq 风格：单 map 分发 + 单隧道操作）。
// 独立 ctx，不挂 client 的 smux session ctx——P2P session 与控制面生命周期互不影响。
type Manager struct {
	serverHost string // 派生默认 mqtt_brokers/stun_servers（对齐 ser2mq.NewManager(ctx, nodeID) 的注入模式）

	// credsFn 按 tunnel name 经控制通道拉取信令凭据（根包适配层注入；
	// 仅当使用默认 server broker 时非 nil——自定义/公共 broker 直接匿名）
	credsFn func(name string) (username, password string, expiresAt int64, err error)

	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	configs  map[string]P2PConfig
	handlers map[string]*Handler
	cancels  map[string]context.CancelFunc
}

// NewManager 构造 Manager。serverHost 用于派生默认服务器列表：
// mqtt_brokers 空 → tcp://<serverHost>:1883；stun_servers 空 → 公共列表 + <serverHost>:3478。
func NewManager(serverHost string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		serverHost: serverHost,
		ctx:        ctx,
		cancel:     cancel,
		configs:    make(map[string]P2PConfig),
		handlers:   make(map[string]*Handler),
		cancels:    make(map[string]context.CancelFunc),
	}
}

// SetCredsProvider 注入信令凭据拉取函数（阶段 6.5：经 smux 控制通道请求
// p2p_signal_token；nil = 匿名）。Handler 每次连接尝试前调用。
func (m *Manager) SetCredsProvider(fn func(name string) (username, password string, expiresAt int64, err error)) {
	m.mu.Lock()
	m.credsFn = fn
	m.mu.Unlock()
}

// OnTunnelUpdate 全量配置分发入口（对齐 ser2mq/ser2net/webssh 的 OnTunnelUpdate 惯例，
// 由根包 notifyManagers 过滤 Enable 并解析 Para 后调用——p2p 包不能反向 import 根包）。
// 增量启停：新配置启动；删除/禁用（从 map 消失）停机；同名 Para 变更（JSON 不等）重启。
func (m *Manager) OnTunnelUpdate(configs map[string]P2PConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 停：删除的 + Para 变更的（变更重启由下方 start 循环完成）
	for name, h := range m.handlers {
		nc, exists := configs[name]
		if !exists || !configEqual(m.configs[name], nc) {
			m.stopLocked(name, h)
		}
	}
	// 起：新增的 + 变更重启的
	for name, cfg := range configs {
		if _, running := m.handlers[name]; running {
			continue
		}
		m.startLocked(name, cfg)
	}
	m.configs = cloneConfigs(configs)
}

// Status 返回隧道运行时快照
func (m *Manager) Status(name string) (Runtime, error) {
	m.mu.Lock()
	h, ok := m.handlers[name]
	m.mu.Unlock()
	if !ok {
		return Runtime{}, ErrNotFound
	}
	return h.Status(), nil
}

// Start 启动已配置但已停止的隧道（幂等；配置不存在返 ErrNotFound）
func (m *Manager) Start(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, running := m.handlers[name]; running {
		return nil
	}
	cfg, ok := m.configs[name]
	if !ok {
		return ErrNotFound
	}
	m.startLocked(name, cfg)
	return nil
}

// Stop 停止隧道（幂等；保留配置供 Start 重启，服务端下次全量分发仍会拉起）
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.handlers[name]
	if !ok {
		return nil
	}
	m.stopLocked(name, h)
	return nil
}

// Close 关闭全部 handler 并等待 Run goroutine 退出（幂等）
func (m *Manager) Close() {
	m.mu.Lock()
	for name, h := range m.handlers {
		m.stopLocked(name, h)
	}
	m.mu.Unlock()
	m.cancel()
	m.wg.Wait()
}

// startLocked 启动单个 handler。服务器注入收口在此（写 easyp2p 包级全局，
// 持 m.mu 串行执行）。注：easyp2p 全局读取无锁，多 tunnel 并发打洞时理论上有
// 竞争——MVP 接受：默认部署下各 tunnel 派生值相同（p2punch 上游本身也是单连接假设）。
func (m *Manager) startLocked(name string, cfg P2PConfig) {
	engine.SetServers(
		mergeServers(cfg.STUNServers, DefaultSTUNServers(m.serverHost)),
		mergeServers(cfg.MQTTBrokers, DefaultMQTTBrokers(m.serverHost)),
	)
	var credsFn func() (SignalCredentials, error)
	if m.credsFn != nil {
		fn := m.credsFn
		tunnelName := name
		credsFn = func() (SignalCredentials, error) {
			u, p, _, err := fn(tunnelName)
			if err != nil {
				return SignalCredentials{}, err
			}
			return SignalCredentials{Username: u, Password: p}, nil
		}
	}
	h := NewHandler(cfg, m.serverHost, credsFn)
	hctx, cancel := context.WithCancel(m.ctx)
	m.handlers[name] = h
	m.cancels[name] = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		h.Start(hctx)
	}()
}

func (m *Manager) stopLocked(name string, h *Handler) {
	if cancel, ok := m.cancels[name]; ok {
		cancel()
		delete(m.cancels, name)
	}
	delete(m.handlers, name)
	h.Close()
}

// configEqual Para 级变更检测：JSON 字节比较（对齐 vpn Manager 的变更重启惯例）
func configEqual(a, b P2PConfig) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

func cloneConfigs(src map[string]P2PConfig) map[string]P2PConfig {
	out := make(map[string]P2PConfig, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// defaultSTUNServers 公共列表（国内优先）在前 + server :3478 内嵌兜底追加
func DefaultSTUNServers(serverHost string) []string {
	return append(engine.DefaultSTUNServers(), net.JoinHostPort(serverHost, "3478"))
}

// defaultMQTTBrokers 默认指向 server 内嵌 broker
func DefaultMQTTBrokers(serverHost string) []string {
	return []string{"tcp://" + net.JoinHostPort(serverHost, "1883")}
}
