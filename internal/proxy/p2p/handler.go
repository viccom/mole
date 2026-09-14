//go:build p2p

package p2p

import (
	"context"
	"fmt"
	"sync"
	"time"

	"moleAgent_client/internal/p2p/engine"
	"moleAgent_client/internal/p2p/session"
	"moleAgent_client/internal/p2p/tunnel"
)

// SignalCredentials MQTT 信令凭据（阶段 6 激活注入；零值 = 匿名，
// 仅当显式配置 mqtt_brokers 指向公共/自定义 broker 时匿名合法）
type SignalCredentials struct {
	Username string
	Password string
}

// Runtime 是 Handler 的运行时快照（两处状态收集共用：tunnel_status 上报 + 本地 REST）。
// json tag 与其它隧道类型的状态子结构一致（snake_case，复审 F10）
type Runtime struct {
	Running   bool   `json:"running"`
	Connected bool   `json:"connected"`
	BytesIn   uint64 `json:"bytes_in"`
	BytesOut  uint64 `json:"bytes_out"`
	Error     string `json:"error,omitempty"`
}

// Handler 单条 p2p 隧道的连接编排：吸收 p2punch cmd/cli/connection.go 的 glue——
// engine.Registry 逐 mode 尝试 → session.NewSession → Run → 发起端 CreateTunnel，
// 外加重连退避循环。secure 配置与 yamux 配对在 fork 的 engine 包内部完成，此处不感知。
type Handler struct {
	cfg     P2PConfig
	host    string                                               // serverHost：派生默认 mqtt/stun 服务器地址
	credsFn func(ctx context.Context) (SignalCredentials, error) // 信令凭据拉取（nil = 匿名，公共/自定义 broker 场景）

	mu        sync.Mutex
	sess      session.Session
	lastErr   string
	closeOnce sync.Once
	cancel    context.CancelFunc
	stopped   chan struct{}

	// modeAttemptTimeout 单次连接尝试（打洞+信令+会话建立）的上限：
	// paho ConnectRetry(true) 在 broker 持续拒绝时永不返回，无上限会把
	// Handler 永久楔死在 connectFn 里（复审 F4）
	modeAttemptTimeout time.Duration

	// 字节累计（复审 F15）：会话级计数器随重连归零，Handler 维护跨重连的
	// 累计值——carried 为已结束会话的总和，last 为当前会话上次读数
	carriedIn  uint64
	carriedOut uint64
	lastIn     uint64
	lastOut    uint64
}

// NewHandler 构造 Handler（Start 前不产生任何 goroutine）
func NewHandler(cfg P2PConfig, serverHost string, credsFn func(ctx context.Context) (SignalCredentials, error)) *Handler {
	return &Handler{cfg: cfg, host: serverHost, credsFn: credsFn, stopped: make(chan struct{}), modeAttemptTimeout: 120 * time.Second}
}

// connectFn / backoffFn 是测试注入点（同包测试替换，生产用默认实现）
var connectFn = defaultConnect
var backoffFn = reconnectBackoff

// tunnelRetryDelay CreateTunnel 重试间隔（对齐上游 1s）
var tunnelRetryDelay = time.Second

// Start 启动连接循环（幂等：已启动返回 nil）
func (h *Handler) Start(parent context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	h.cancel = cancel
	go func() {
		defer close(h.stopped)
		defer h.closeCurrentSession() // 复审 F6：Run 退出后补关竞态窗口内新建的会话
		_ = h.Run(ctx)
	}()
}

// Close 幂等停机：取消连接循环并关闭 session（NegotiatedConn.Close 级联关底层 socket，
// CloseTunnel 先行只是显式收掉本地 listener）
func (h *Handler) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		cancel := h.cancel
		h.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		h.closeCurrentSession()
		<-h.stopped
		// 复审 F6：connectFn 返回与 setSession 之间的竞态窗口可能新建会话，
		// 其 Run 不响应 ctx——等待退出后再补关一次
		h.closeCurrentSession()
	})
}

// closeCurrentSession 关闭当前会话（幂等；取出引用防重复关闭）
func (h *Handler) closeCurrentSession() {
	h.mu.Lock()
	sess := h.sess
	h.sess = nil
	h.mu.Unlock()
	println("DBG closeCurrent sess_nil=", sess == nil)
	if sess == nil {
		return
	}
	for _, ti := range sess.ListTunnels() {
		_ = sess.CloseTunnel(ti.ID)
	}
	_ = sess.Close()
}

// Run 是重连循环：逐 mode 尝试，失败退避重连（5/10/20/40/60s，防 conntrack 爆表）；
// 连上过 session 后 3s 快速重连。ctx 取消时返回。
func (h *Handler) Run(ctx context.Context) error {
	var failures int
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		connected := h.tryConnect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if connected {
			// 连上过 session（已结束）：重置退避，快速重连
			failures = 0
			if !sleepCtx(ctx, 3*time.Second) {
				return ctx.Err()
			}
		} else {
			failures++
			if !sleepCtx(ctx, backoffFn(failures)) {
				return ctx.Err()
			}
		}
	}
}

// reconnectBackoff 连续打洞失败 N 次后的退避：5s→10s→20s→40s→60s（上限）。
// 打洞每轮向对端随机端口发大量 UDP 包，每包在路由器建一条 conntrack；
// 失败越多退避越长，间隔 ≥20s 才稳态安全。
func reconnectBackoff(failures int) time.Duration {
	shift := failures - 1
	if shift > 4 {
		shift = 4
	}
	d := time.Duration(5<<uint(shift)) * time.Second
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// tryConnect 跑一轮 mode 链。返回 true = 连上过 session（已结束）。
func (h *Handler) tryConnect(ctx context.Context) bool {
	for _, modeName := range h.modes() {
		if ctx.Err() != nil {
			return false
		}
		// 每次连接尝试前拉取信令凭据：始终持有当前有效 secret（服务端签发即轮换），
		// 控制面不可达时按匿名继续（server broker 会拒，下一轮退避后重拉）
		creds := SignalCredentials{}
		if h.credsFn != nil {
			c, err := h.credsFn(ctx)
			if err != nil {
				h.setFailure("signal creds: " + err.Error())
				// 复审 F3：显式回退匿名——沿用他隧道残留在包级全局的凭据，
				// 可能把 token/secret 发给第三方 broker
				engine.SetSignalCredentials("", "")
			} else {
				creds = c
				// 写入 easyp2p 包级注入点（同节点多 token 对 broker 等价，见 fork 说明）
				engine.SetSignalCredentials(creds.Username, creds.Password)
			}
		}
		// 复审 F4：单次尝试上限（看门狗 cancel）——paho ConnectRetry(true) 在
		// broker 持续拒绝时永不返回，无上限会把 Handler 永久楔死在 connectFn
		attemptCtx, cancel := context.WithCancel(ctx)
		timer := time.AfterFunc(h.modeAttemptTimeout, func() {
			h.setFailure(modeName + " attempt timeout")
			cancel()
		})
		sess, err := connectFn(attemptCtx, modeName, h.cfg, h.host, creds)
		timerFired := !timer.Stop()

		if err != nil {
			cancel()
			// 看门狗打断导致的返回要保持 timeout 语义（复审 F4）
			if attemptCtx.Err() != nil && ctx.Err() == nil {
				h.setFailure(modeName + " attempt timeout")
			} else {
				h.setFailure(modeName + " failed: " + err.Error())
			}
			continue
		}
		// 停机竞态守卫（复审 F6）：connectFn 返回与 setSession 之间若已停机，
		// 必须就地关闭刚建立的会话——session.Run 不响应 ctx，遗弃会留 35s 幽灵
		// 连接，且 Handler.Close 的补关看到的是 nil 引用
		if ctx.Err() != nil {
			cancel()
			_ = sess.Close()
			return false
		}
		if timerFired || attemptCtx.Err() != nil {
			// 尝试超时被打断（复审 F4）：关闭半成品会话，进入退避
			cancel()
			_ = sess.Close()
			h.setFailure(modeName + " attempt timeout")
			continue
		}
		// 成功路径不 cancel attemptCtx（会话内部可能绑定该 ctx），
		// 其生命周期随 handler ctx 终结统一回收
		h.setSession(sess)

		// 先启动 session.Run（内部 reader/ticker），再建隧道：
		// CreateTunnel 的 OPEN→OK 握手依赖 reader 处理 OK 并建本地 listener
		runDone := make(chan struct{})
		go func() {
			_ = sess.Run(ctx)
			close(runDone)
		}()

		// 发起端建隧道持续失败视同未连通：必须走退避而不是 3s 快速重连——
		// 否则完整重打洞（conntrack 洪泛源）无限循环（复审 F5）
		tunnelOK := true
		if h.cfg.TargetHost != "" && !h.restoreTunnel(ctx, sess) {
			tunnelOK = false
		}

		<-runDone
		h.endSession(sess)

		if !tunnelOK {
			return false
		}
		return true
	}
	return false
}

// restoreTunnel 发起端建隧道，3 次重试（对齐上游 connection.go 重连自愈惯例）
func (h *Handler) restoreTunnel(ctx context.Context, sess session.Session) bool {
	params := tunnel.Params{
		Protocol:   h.cfg.Protocol,
		LocalPort:  h.cfg.LocalPort,
		TargetHost: h.cfg.TargetHost,
		TargetPort: h.cfg.TargetPort,
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := sess.CreateTunnel(params); err != nil {
			lastErr = err
			select {
			case <-ctx.Done():
				return false
			case <-time.After(tunnelRetryDelay):
			}
			continue
		}
		return true
	}
	h.setFailure(fmt.Sprintf("tunnel :%d FAILED after 3 retries: %v", h.cfg.LocalPort, lastErr))
	return false
}

// Status 运行时快照：Connected = session 已建立；字节聚合自 ListTunnels
// （TunnelInfo 自带 BytesIn/Out，fork 内 atomic 对齐已处理 32 位平台）
func (h *Handler) Status() Runtime {
	h.mu.Lock()
	defer h.mu.Unlock()
	rt := Runtime{Running: true, Error: h.lastErr}
	if h.sess != nil {
		rt.Connected = true
		var in, out uint64
		for _, ti := range h.sess.ListTunnels() {
			in += ti.BytesIn
			out += ti.BytesOut
		}
		// 会话计数器回退 = 发生了重连：旧会话读数并入累计值（复审 F15）
		if in < h.lastIn || out < h.lastOut {
			h.carriedIn += h.lastIn
			h.carriedOut += h.lastOut
		}
		h.lastIn, h.lastOut = in, out
		rt.BytesIn = h.carriedIn + in
		rt.BytesOut = h.carriedOut + out
	}
	return rt
}

func (h *Handler) modes() []string {
	if len(h.cfg.Modes) > 0 {
		return h.cfg.Modes
	}
	return engine.DefaultModes
}

func (h *Handler) setSession(sess session.Session) {
	h.mu.Lock()
	h.sess = sess
	h.lastErr = ""
	h.mu.Unlock()
}

// endSession 会话生命周期终结（对端断开/本端停机）：关闭其全部隧道与会话本体，
// 并清除 Handler 引用。幂等——session.Close 与 NegotiatedConn.Close 均可重入
// （fork 硬约束 §3.2.6）。此前的 clearSession 只清引用不关闭，会话关闭完全
// 依赖 h.Close 的快照，异步拆除下会泄漏（复审 F6 根因）。
func (h *Handler) endSession(sess session.Session) {
	h.mu.Lock()
	if h.sess == sess {
		h.sess = nil
	}
	var in, out uint64
	for _, ti := range sess.ListTunnels() {
		in += ti.BytesIn
		out += ti.BytesOut
	}
	// 最终计数并入累计值，重连后显示不再从 0 塌缩（复审 F15）
	h.carriedIn += in
	h.carriedOut += out
	h.lastIn, h.lastOut = 0, 0
	h.mu.Unlock()
	for _, ti := range sess.ListTunnels() {
		_ = sess.CloseTunnel(ti.ID)
	}
	_ = sess.Close()
}

func (h *Handler) setFailure(msg string) {
	h.mu.Lock()
	h.lastErr = msg
	h.mu.Unlock()
}

// sleepCtx 可取消睡眠；返回 false = ctx 已取消
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// defaultConnect 生产连接实现：engine mode → secure/yamux（engine 内部）→ session。
func defaultConnect(ctx context.Context, modeName string, cfg P2PConfig, _ string, _ SignalCredentials) (session.Session, error) {
	modeFn := engine.Registry[modeName]
	if modeFn == nil {
		return nil, fmt.Errorf("unknown p2p mode %q", modeName)
	}
	deps := engine.Deps{
		Room:        cfg.Room,
		Modes:       []string{modeName},
		RelayServer: cfg.RelayServer,
		ListenPort:  0, // 打洞本地端口随机
		Verbose:     false,
	}
	outcome, _, err := modeFn(ctx, deps)
	if err != nil {
		return nil, err
	}
	sess, err := session.NewSession(ctx, outcome)
	if err != nil {
		// 释放失败 Outcome 的底层 conn（Mux.Close 级联关闭 yamux + 打洞 socket）
		outcome.Mux.Close()
		return nil, err
	}
	return sess, nil
}

// mergeServers 用户偏好服务器前置 + 默认兜底，去重（对齐上游 connection.go 同名函数）。
// 两端都空时返 nil（避免传空 slice 让 easyp2p 走空集分支出错）。
func mergeServers(custom, defaults []string) []string {
	if len(custom) == 0 && len(defaults) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(custom)+len(defaults))
	out := make([]string, 0, len(custom)+len(defaults))
	for _, s := range custom {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range defaults {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
