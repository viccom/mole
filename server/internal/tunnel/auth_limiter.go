package tunnel

import (
	"log/slog"
	"net"
	"sync"
	"time"
)

// 控制端口认证失败限速参数（SEC-13）：与 auth.LoginLimiter 同默认值
const (
	connAuthMaxFailures  = 10              // 窗口内失败次数上限
	connAuthWindow       = 5 * time.Minute // 失败计数滑动窗口
	connAuthLockDuration = 5 * time.Minute // 锁定时长
	connAuthEntriesCap   = 1024            // 计数表超过该规模时触发清理
)

// connAuthLimiter 控制端口认证失败 per-IP 限速器（SEC-13）。
// 复用 auth.LoginLimiter 的模式（滑动窗口失败计数 + 锁定 + 成功清零），
// 但只按 IP 计数——节点认证是 token 制，无用户名维度；直接实例化
// LoginLimiter 会把它全部失败聚合到共享的 user: key 上，语义不符，
// 故在 tunnel 包内实现同模式的小结构。
type connAuthLimiter struct {
	mu           sync.Mutex
	now          func() time.Time // 时间源可注入（测试用假时钟）
	maxFailures  int
	window       time.Duration
	lockDuration time.Duration
	entries      map[string]*connAuthFailEntry // ip → 失败记录
}

type connAuthFailEntry struct {
	failures    []time.Time
	lockedUntil time.Time
}

func newConnAuthLimiter() *connAuthLimiter {
	return &connAuthLimiter{
		now:          time.Now,
		maxFailures:  connAuthMaxFailures,
		window:       connAuthWindow,
		lockDuration: connAuthLockDuration,
		entries:      make(map[string]*connAuthFailEntry),
	}
}

// Allowed 该 IP 当前是否允许进入认证流程（锁内直接断开）
func (l *connAuthLimiter) Allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[ip]
	if !ok {
		return true
	}
	return !l.now().Before(e.lockedUntil)
}

// RecordFailure 记录一次认证失败，滑动窗口内达到阈值即锁定
func (l *connAuthLimiter) RecordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.cleanupLocked(now)
	e := l.entries[ip]
	if e == nil {
		e = &connAuthFailEntry{}
		l.entries[ip] = e
	}
	e.failures = pruneConnAuthFailures(e.failures, now, l.window)
	e.failures = append(e.failures, now)
	if len(e.failures) >= l.maxFailures {
		e.lockedUntil = now.Add(l.lockDuration)
		e.failures = nil // 锁定期满后从零重新计数
		slog.Warn("Control auth failures exceeded threshold, locking IP", "ip", ip, "until", e.lockedUntil)
	}
}

// RecordSuccess 认证成功：清零该 IP 的失败计数（对齐 login_limiter 语义）
func (l *connAuthLimiter) RecordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, ip)
}

// cleanupLocked 防止计数表无界增长（调用方需持锁）
func (l *connAuthLimiter) cleanupLocked(now time.Time) {
	if len(l.entries) < connAuthEntriesCap {
		return
	}
	for ip, e := range l.entries {
		if len(e.failures) == 0 && !now.Before(e.lockedUntil) {
			delete(l.entries, ip)
		}
	}
}

// pruneConnAuthFailures 剔除滑出窗口的失败记录
func pruneConnAuthFailures(failures []time.Time, now time.Time, window time.Duration) []time.Time {
	out := make([]time.Time, 0, len(failures))
	for _, f := range failures {
		if now.Sub(f) < window {
			out = append(out, f)
		}
	}
	return out
}

// connRemoteIP 从连接远端地址提取 IP（去端口；解析失败返回原串，
// 与 auth 包 loginClientIP 同风格，IPv6 字面量由 SplitHostPort 处理）
func connRemoteIP(remoteAddr string) string {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return ip
}
