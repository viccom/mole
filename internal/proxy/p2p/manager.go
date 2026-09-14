//go:build p2p

package p2p

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
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
	credsFn func(ctx context.Context, name string) (username, password string, expiresAt int64, err error)

	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
	mu       sync.Mutex
	configs  map[string]P2PConfig
	handlers map[string]*Handler
	cancels  map[string]context.CancelFunc
	// lastSTUN/lastMQTT 记录最近写入 easyp2p 全局的服务器列表：未变化则跳过
	// 写入，消除包级全局的并发写与跨隧道覆盖（复审 F2 主场景）
	lastSTUN []string
	lastMQTT []string
	// brokerSig 本节点已启动 p2p 隧道的 MQTT broker 签名：混合 broker 配置
	// 不受支持（fork 包级全局为单值，混用会导致跨隧道信令污染与凭据泄漏），
	// 签名不一致的新隧道拒绝启动（复审 F2）
	brokerSig string
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
func (m *Manager) SetCredsProvider(fn func(ctx context.Context, name string) (username, password string, expiresAt int64, err error)) {
	m.mu.Lock()
	m.credsFn = fn
	m.mu.Unlock()
}

// OnTunnelUpdate 全量配置分发入口（对齐 ser2mq/ser2net/webssh 的 OnTunnelUpdate 惯例，
// 由根包 notifyManagers 过滤 Enable 并解析 Para 后调用——p2p 包不能反向 import 根包）。
// 增量启停：新配置启动；删除/禁用（从 map 消失）停机；同名 Para 变更（JSON 不等）重启。
func (m *Manager) OnTunnelUpdate(configs map[string]P2PConfig) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return // Close 之后的分发不再启动任何 handler（复审 F7 WaitGroup 误用）
	}
	var stopped []*Handler
	for name, h := range m.handlers {
		nc, exists := configs[name]
		if !exists || !configEqual(m.configs[name], nc) {
			if cancel, ok := m.cancels[name]; ok {
				cancel()
				delete(m.cancels, name)
			}
			delete(m.handlers, name)
			stopped = append(stopped, h)
		}
	}
	// 起：新增的 + 变更重启的。按名排序保证确定性；发起端 local_port
	// 全节点唯一——重复端口的败者会以 Connected 状态空转并不断重试绑定
	//（复审 F11），配置期直接跳过并告警
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	usedPorts := make(map[int]string)
	for hname, h := range m.handlers {
		if oc, ok := m.configs[hname]; ok && oc.TargetHost != "" && oc.LocalPort > 0 {
			usedPorts[oc.LocalPort] = hname
		}
		_ = h
	}
	for _, name := range names {
		if _, running := m.handlers[name]; running {
			continue
		}
		cfg := configs[name]
		if cfg.TargetHost != "" && cfg.LocalPort > 0 {
			if owner, dup := usedPorts[cfg.LocalPort]; dup {
				slog.Error("p2p tunnel skipped: local_port already used on this node",
					"tunnel", name, "port", cfg.LocalPort, "owner", owner)
				continue
			}
			usedPorts[cfg.LocalPort] = name
		}
		m.startLocked(name, cfg)
	}
	m.configs = cloneConfigs(configs)
	if len(m.handlers) == 0 {
		m.brokerSig = "" // 全部下线后解除同质约束（复审 F2）
	}
	m.mu.Unlock()

	// 阻塞的会话拆除在锁外执行（复审 F1：m.mu 不得跨越 h.Close）
	for _, h := range stopped {
		h.Close()
	}
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
	if m.closed {
		return errors.New("p2p manager closed")
	}
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
	h, ok := m.handlers[name]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	cancel := m.cancels[name]
	delete(m.cancels, name)
	delete(m.handlers, name)
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	h.Close()
	return nil
}

// Close 关闭全部 handler 并等待其 goroutine 退出（幂等）。
// m.mu 只保护路由表，绝不跨 h.Close 持有——会话拆除可能阻塞数十秒，
// 持锁等待会与 Status/tunnel_push/凭据请求构成死锁环（复审 F1/F6）。
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	handlers := make([]*Handler, 0, len(m.handlers))
	for _, h := range m.handlers {
		handlers = append(handlers, h)
	}
	m.handlers = make(map[string]*Handler)
	m.cancels = make(map[string]context.CancelFunc)
	m.mu.Unlock()

	var closeWG sync.WaitGroup
	for _, h := range handlers {
		closeWG.Add(1)
		go func(h *Handler) {
			defer closeWG.Done()
			h.Close()
		}(h)
	}
	closeWG.Wait()
	m.cancel()
}

// startLocked 启动单个 handler。服务器注入收口在此（写 easyp2p 包级全局，
// 持 m.mu 串行执行）。注：easyp2p 全局读取无锁，多 tunnel 并发打洞时理论上有
// 竞争——MVP 接受：默认部署下各 tunnel 派生值相同（p2punch 上游本身也是单连接假设）。
func (m *Manager) startLocked(name string, cfg P2PConfig) {
	stunEff := mergeServers(cfg.STUNServers, DefaultSTUNServers(m.serverHost))
	mqttEff := mergeServers(cfg.MQTTBrokers, DefaultMQTTBrokers(m.serverHost))

	// broker 同质校验：本节点所有 p2p 隧道必须使用同一 broker 集——fork 的
	// 信令凭据与服务器列表均为包级单值，混用会造成跨隧道信令污染，甚至把
	// 节点 token 发给另一隧道配置的第三方 broker（复审 F2，安全）
	sig := MQTTBrokerSignature(mqttEff)
	if m.brokerSig == "" {
		m.brokerSig = sig
	} else if sig != m.brokerSig {
		slog.Error("p2p tunnel rejected: mqtt_brokers differ from tunnels already running on this node",
			"tunnel", name, "want", m.brokerSig)
		return
	}

	// 幂等写：服务器列表未变化时不重写包级全局，消除并发打洞的读写竞争面
	if !slices.Equal(stunEff, m.lastSTUN) || !slices.Equal(mqttEff, m.lastMQTT) {
		engine.SetServers(stunEff, mqttEff)
		m.lastSTUN = append([]string(nil), stunEff...)
		m.lastMQTT = append([]string(nil), mqttEff...)
	}

	var credsFn func(context.Context) (SignalCredentials, error)
	if m.credsFn != nil {
		fn := m.credsFn
		tunnelName := name
		credsFn = func(ctx context.Context) (SignalCredentials, error) {
			u, p, _, err := fn(ctx, tunnelName)
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
	// Handler.Start 自己派生 goroutine 并以 h.stopped 通知完成；
	// 不再包第二层 goroutine + WaitGroup（复审 F7 双层包裹）
	h.Start(hctx)
}

// configEqual Para 级变更检测：深比较（对齐 vpn Manager 的变更重启惯例）
func configEqual(a, b P2PConfig) bool {
	return reflect.DeepEqual(a, b)
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

// MQTTBrokerSignature 归一化 broker 列表的比较签名：小写 scheme/host、
// 去尾斜杠、tcp 系缺端口补 :1883、排序后拼接。用于判断两组写法不同但
// 指向相同 broker 集的配置是否等价（复审 F14）。
func MQTTBrokerSignature(brokers []string) string {
	if len(brokers) == 0 {
		return ""
	}
	norm := make([]string, 0, len(brokers))
	for _, b := range brokers {
		norm = append(norm, normalizeBroker(b))
	}
	slices.Sort(norm)
	return strings.Join(norm, "|")
}

func normalizeBroker(b string) string {
	b = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(b), "/"))
	u, err := url.Parse(b)
	if err != nil {
		return strings.ToLower(b)
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	if u.Port() == "" && (scheme == "tcp" || scheme == "ssl" || scheme == "tls") {
		host += ":1883"
	}
	return scheme + "://" + host
}
