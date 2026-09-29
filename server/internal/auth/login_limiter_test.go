package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeClock 可注入的假时钟
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func newTestLoginLimiter() (*LoginLimiter, *fakeClock) {
	l := NewLoginLimiter()
	clock := &fakeClock{t: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	l.now = clock.Now
	return l, clock
}

// 连续 10 次失败后锁定；锁内即使密码正确也拒绝；锁期满恢复（SEC-07）
func TestLoginLimiter_LocksAfterTenFailures(t *testing.T) {
	l, clock := newTestLoginLimiter()

	for i := 0; i < 9; i++ {
		l.RecordFailure("1.2.3.4", "alice")
		if !l.Allowed("1.2.3.4", "alice") {
			t.Fatalf("must stay allowed before 10th failure, blocked at #%d", i+1)
		}
	}
	l.RecordFailure("1.2.3.4", "alice")
	if l.Allowed("1.2.3.4", "alice") {
		t.Fatal("must be locked after 10 failures")
	}
	// 锁同样命中同用户名的其他 IP（per-用户名计数）与其他用户名的同 IP（per-IP 计数）
	if l.Allowed("5.6.7.8", "alice") {
		t.Fatal("per-username counter must also lock other IPs")
	}
	if l.Allowed("1.2.3.4", "bob") {
		t.Fatal("per-IP counter must also lock other usernames")
	}

	// 锁内时间推进不足：仍拒绝
	clock.Advance(4 * time.Minute)
	if l.Allowed("1.2.3.4", "alice") {
		t.Fatal("must stay locked within lock duration")
	}
	// 过锁期：恢复
	clock.Advance(time.Minute + time.Second)
	if !l.Allowed("1.2.3.4", "alice") {
		t.Fatal("must be allowed after lock expires")
	}
}

// 成功登录清零计数：错 9 次 → 成功 → 再错 10 次才锁
func TestLoginLimiter_SuccessResetsCounters(t *testing.T) {
	l, _ := newTestLoginLimiter()

	for i := 0; i < 9; i++ {
		l.RecordFailure("1.2.3.4", "alice")
	}
	l.RecordSuccess("1.2.3.4", "alice")

	for i := 0; i < 9; i++ {
		l.RecordFailure("1.2.3.4", "alice")
		if !l.Allowed("1.2.3.4", "alice") {
			t.Fatalf("counters must restart after success, blocked at #%d", i+1)
		}
	}
	l.RecordFailure("1.2.3.4", "alice")
	if l.Allowed("1.2.3.4", "alice") {
		t.Fatal("10 failures after reset must lock again")
	}
}

// 滑动窗口：失败陆续发生后随时间过期，不计入新窗口
func TestLoginLimiter_FailuresExpireWithWindow(t *testing.T) {
	l, clock := newTestLoginLimiter()

	for i := 0; i < 9; i++ {
		l.RecordFailure("1.2.3.4", "alice")
		clock.Advance(60 * time.Second)
	}
	// 最早的失败（480s 前）已滑出 5 分钟窗口，第 10 次失败后仅 9 次在窗口内
	l.RecordFailure("1.2.3.4", "alice")
	if !l.Allowed("1.2.3.4", "alice") {
		t.Fatal("failures older than the window must not count toward the lock")
	}
}

// loginHandlerStub 模拟登录结果：密码 right → 200，否则 401
func loginHandlerStub() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Password == "right-pass" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}
}

func newLoginRequest(ip, username, password string) *http.Request {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
	req.RemoteAddr = ip + ":51234"
	return req
}

// Wrap 集成：10 次错密后第 11 次（含正确密码）→ 429 且不触达登录逻辑；
// 锁期满后正确密码可登录
func TestLoginLimiter_WrapLocksAndRecovers(t *testing.T) {
	l, clock := newTestLoginLimiter()
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		loginHandlerStub().ServeHTTP(w, r)
	})
	wrapped := l.Wrap(next)

	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, newLoginRequest("1.2.3.4", "alice", "wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt #%d: expected 401, got %d", i+1, w.Code)
		}
	}

	// 第 11 次：即使密码正确也 429，且登录逻辑不被调用
	before := called
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, newLoginRequest("1.2.3.4", "alice", "right-pass"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when locked, got %d", w.Code)
	}
	if called != before {
		t.Fatal("wrapped handler must not reach login logic while locked")
	}
	if !strings.Contains(w.Body.String(), "Too many") {
		t.Fatalf("429 body should explain the lock, got %s", w.Body.String())
	}

	// 过锁期后正确密码恢复登录
	clock.Advance(5*time.Minute + time.Second)
	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, newLoginRequest("1.2.3.4", "alice", "right-pass"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after lock expiry with correct password, got %d", w.Code)
	}
}

// Wrap 成功清零：错 9 次 → 成功 → 再错 9 次不锁
func TestLoginLimiter_WrapSuccessResets(t *testing.T) {
	l, _ := newTestLoginLimiter()
	wrapped := l.Wrap(loginHandlerStub())

	for i := 0; i < 9; i++ {
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, newLoginRequest("1.2.3.4", "alice", "wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt #%d: expected 401, got %d", i+1, w.Code)
		}
	}
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, newLoginRequest("1.2.3.4", "alice", "right-pass"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct password, got %d", w.Code)
	}
	for i := 0; i < 9; i++ {
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, newLoginRequest("1.2.3.4", "alice", "wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("post-reset attempt #%d: expected 401, got %d", i+1, w.Code)
		}
	}
}
