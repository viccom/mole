package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestAPILimiter_IPRateLimit(t *testing.T) {
	cfg := APIRateLimitConfig{Enabled: true, PerIP: 2, Burst: 1}
	limiter := NewAPILimiter(cfg, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := limiter.Middleware(inner)

	// First request should pass
	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.RemoteAddr = "1.2.3.4:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", w.Code)
	}

	// Rapid requests should eventually get 429
	got429 := false
	for i := 0; i < 20; i++ {
		req = httptest.NewRequest("GET", "/api/v1/nodes", nil)
		req.RemoteAddr = "1.2.3.4:12345"
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("expected 429 after burst of requests")
	}
}

func TestAPILimiter_UserRateLimit(t *testing.T) {
	jwt := JWTVerifierFunc(func(token string) (string, bool) {
		if token == "valid-token" {
			return "user1", true
		}
		return "", false
	})
	cfg := APIRateLimitConfig{Enabled: true, PerUser: 2, PerIP: 0, Burst: 1}
	limiter := NewAPILimiter(cfg, jwt)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := limiter.Middleware(inner)

	// Authenticated request should pass
	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first authenticated request: expected 200, got %d", w.Code)
	}

	// Burst should trigger 429
	got429 := false
	for i := 0; i < 20; i++ {
		req = httptest.NewRequest("GET", "/api/v1/nodes", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("expected 429 for user rate limit")
	}
}

func TestAPILimiter_DifferentUsersIndependent(t *testing.T) {
	jwt := JWTVerifierFunc(func(token string) (string, bool) {
		return token, true // token == userID
	})
	cfg := APIRateLimitConfig{Enabled: true, PerUser: 1, Burst: 1}
	limiter := NewAPILimiter(cfg, jwt)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := limiter.Middleware(inner)

	// Exhaust user1 limit
	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer user1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// user2 should still be allowed
	req = httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.Header.Set("Authorization", "Bearer user2")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("user2 should not be affected by user1 limit, got %d", w.Code)
	}
}

func TestAPILimiter_UnlimitedWhenZero(t *testing.T) {
	cfg := APIRateLimitConfig{Enabled: true, PerUser: 0, PerIP: 0, Burst: 20}
	limiter := NewAPILimiter(cfg, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := limiter.Middleware(inner)

	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
		req.RemoteAddr = "1.2.3.4:12345"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200 with unlimited config, got %d", i, w.Code)
		}
	}
}

func TestAPILimiter_ExtractIdentityNoToken(t *testing.T) {
	cfg := APIRateLimitConfig{Enabled: true, PerUser: 0, PerIP: 1, Burst: 1}
	limiter := NewAPILimiter(cfg, nil)

	userID, ip := limiter.extractIdentity(&http.Request{RemoteAddr: "10.0.0.1:9999"})
	if userID != "" {
		t.Error("expected empty userID when no token")
	}
	if ip != "10.0.0.1" {
		t.Errorf("expected IP 10.0.0.1, got %s", ip)
	}
}

func TestJWTVerifierFunc(t *testing.T) {
	f := JWTVerifierFunc(func(token string) (string, bool) {
		if token == "abc" {
			return "user1", true
		}
		return "", false
	})

	uid, ok := f.VerifyToken("abc")
	if !ok || uid != "user1" {
		t.Error("expected user1, true")
	}

	uid, ok = f.VerifyToken("bad")
	if ok || uid != "" {
		t.Error("expected empty, false")
	}
}

func TestNewAPILimiter_DefaultBurst(t *testing.T) {
	cfg := APIRateLimitConfig{Enabled: true, PerIP: 10, Burst: 0}
	l := NewAPILimiter(cfg, nil)
	if l.burst != 20 {
		t.Errorf("expected default burst 20, got %d", l.burst)
	}
	if l.perIP != rate.Limit(10) {
		t.Error("perIP should be 10")
	}
}

func TestAPILimiter_TrustedProxyXFF(t *testing.T) {
	cfg := APIRateLimitConfig{
		Enabled:        true,
		PerIP:          2,
		Burst:          1,
		TrustedProxies: []string{"10.0.0.0/8"},
	}
	limiter := NewAPILimiter(cfg, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := limiter.Middleware(inner)

	// Request from trusted proxy with XFF should use XFF IP
	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first request via trusted proxy: expected 200, got %d", w.Code)
	}

	// Burst should trigger 429 on the XFF IP
	got429 := false
	for i := 0; i < 20; i++ {
		req = httptest.NewRequest("GET", "/api/v1/nodes", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("expected 429 for XFF IP rate limit")
	}
}

func TestAPILimiter_UntrustedProxyIgnoresXFF(t *testing.T) {
	cfg := APIRateLimitConfig{
		Enabled:        true,
		PerIP:          2,
		Burst:          1,
		TrustedProxies: []string{"10.0.0.0/8"},
	}
	limiter := NewAPILimiter(cfg, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := limiter.Middleware(inner)

	// Request from untrusted proxy should ignore XFF
	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", w.Code)
	}

	// Rate limit should apply to RemoteAddr IP (192.168.1.1), not XFF
	got429 := false
	for i := 0; i < 20; i++ {
		req = httptest.NewRequest("GET", "/api/v1/nodes", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("expected 429 for RemoteAddr IP rate limit (untrusted proxy)")
	}
}

func TestAPILimiter_MultiHopXFF(t *testing.T) {
	cfg := APIRateLimitConfig{
		Enabled:        true,
		PerIP:          2,
		Burst:          1,
		TrustedProxies: []string{"10.0.0.0/8", "172.16.0.0/12"},
	}
	limiter := NewAPILimiter(cfg, nil)

	// Multi-hop: client=1.2.3.4 → proxy1(10.0.0.1) → proxy2(172.16.0.1) → server
	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	req.RemoteAddr = "172.16.0.1:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")

	_, ip := limiter.extractIdentity(req)
	if ip != "1.2.3.4" {
		t.Errorf("expected first non-trusted IP 1.2.3.4, got %s", ip)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		wantLen int
	}{
		{"nil", nil, 0},
		{"empty", []string{}, 0},
		{"CIDR", []string{"10.0.0.0/8"}, 1},
		{"plain IPv4", []string{"1.2.3.4"}, 1},
		{"plain IPv6", []string{"::1"}, 1},
		{"mixed", []string{"10.0.0.0/8", "1.2.3.4"}, 2},
		{"invalid", []string{"not-an-ip"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nets := parseTrustedProxies(tt.input)
			if len(nets) != tt.wantLen {
				t.Errorf("parseTrustedProxies(%v) returned %d nets, want %d", tt.input, len(nets), tt.wantLen)
			}
		})
	}
}
