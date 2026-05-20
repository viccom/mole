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
