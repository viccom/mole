package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// LoginLimiter 登录失败独立限速器（SEC-07）：per-IP 与 per-用户名双计数，
// 默认 10 次失败 / 5 分钟窗口，触发后锁 5 分钟——锁内即使密码正确也拒绝，
// 成功登录清零对应 IP+用户名计数。
// 不依赖 ratelimit.api.enabled 总开关：登录爆破防护不可被 API 限速配置顺带关闭。
type LoginLimiter struct {
	mu           sync.Mutex
	now          func() time.Time // 时间源可注入（测试用假时钟）
	maxFailures  int
	window       time.Duration
	lockDuration time.Duration
	entries      map[string]*loginFailEntry
}

type loginFailEntry struct {
	failures    []time.Time
	lockedUntil time.Time
}

const (
	loginMaxFailures  = 10              // 窗口内失败次数上限
	loginWindow       = 5 * time.Minute // 失败计数滑动窗口
	loginLockDuration = 5 * time.Minute // 锁定时长
	loginEntriesCap   = 1024            // 计数表超过该规模时触发清理
)

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{
		now:          time.Now,
		maxFailures:  loginMaxFailures,
		window:       loginWindow,
		lockDuration: loginLockDuration,
		entries:      make(map[string]*loginFailEntry),
	}
}

// loginKeys 一次失败计入的两个维度：per-IP（聚合该 IP 对全部用户名的爆破）
// 与 per-用户名（聚合全部 IP 对同一账号的分布式爆破）
func loginKeys(ip, username string) []string {
	return []string{"ip:" + ip, "user:" + username}
}

// Allowed 判断该 IP+用户名当前是否允许尝试登录
func (l *LoginLimiter) Allowed(ip, username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, key := range loginKeys(ip, username) {
		e, ok := l.entries[key]
		if !ok {
			continue
		}
		if now.Before(e.lockedUntil) {
			return false
		}
	}
	return true
}

// RecordFailure 记录一次失败登录（任一维度达到阈值即锁定该维度）
func (l *LoginLimiter) RecordFailure(ip, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.cleanupLocked(now)
	for _, key := range loginKeys(ip, username) {
		e := l.entries[key]
		if e == nil {
			e = &loginFailEntry{}
			l.entries[key] = e
		}
		e.failures = pruneLoginFailures(e.failures, now, l.window)
		e.failures = append(e.failures, now)
		if len(e.failures) >= l.maxFailures {
			e.lockedUntil = now.Add(l.lockDuration)
			e.failures = nil // 锁定期满后从零重新计数
		}
	}
}

// RecordSuccess 成功登录：清零该 IP+用户名的全部计数
func (l *LoginLimiter) RecordSuccess(ip, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range loginKeys(ip, username) {
		delete(l.entries, key)
	}
}

// Wrap 包装登录 handler：进入前判定锁定（锁内直接 429，不触达登录逻辑），
// 出去后按状态码回填计数——200 清零、401 计失败（其余状态码不计）
func (l *LoginLimiter) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := loginClientIP(r)
		username := peekLoginUsername(r)

		if !l.Allowed(ip, username) {
			slog.Warn("Login rate limited", "ip", ip, "username", username)
			writeError(w, http.StatusTooManyRequests, 429, "Too many failed login attempts; retry later")
			return
		}

		rec := &loginStatusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rec, r)

		switch rec.statusCode {
		case http.StatusOK:
			l.RecordSuccess(ip, username)
		case http.StatusUnauthorized:
			l.RecordFailure(ip, username)
		}
	}
}

// cleanupLocked 防止计数表无界增长：超过阈值时清掉已无价值的条目
// （计数为空且锁已过期）。调用方需持锁
func (l *LoginLimiter) cleanupLocked(now time.Time) {
	if len(l.entries) < loginEntriesCap {
		return
	}
	for key, e := range l.entries {
		if len(e.failures) == 0 && !now.Before(e.lockedUntil) {
			delete(l.entries, key)
		}
	}
}

// pruneLoginFailures 剔除滑出窗口的失败记录
func pruneLoginFailures(failures []time.Time, now time.Time, window time.Duration) []time.Time {
	out := make([]time.Time, 0, len(failures))
	for _, f := range failures {
		if now.Sub(f) < window {
			out = append(out, f)
		}
	}
	return out
}

// loginClientIP 提取客户端 IP（与 ratelimit 包的 remoteIP 同风格）
func loginClientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// peekLoginUsername 预读请求体中的用户名（读后回填 body，不影响下游解析）
func peekLoginUsername(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req struct {
		Username string `json:"username"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	return req.Username
}

// loginStatusRecorder 捕获下游状态码的 ResponseWriter 包装
type loginStatusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *loginStatusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}
