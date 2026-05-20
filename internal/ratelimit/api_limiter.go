package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// JWTVerifier extracts userID from JWT tokens.
type JWTVerifier interface {
	VerifyToken(tokenString string) (userID string, ok bool)
}

// JWTVerifierFunc adapts a function to JWTVerifier.
type JWTVerifierFunc func(tokenString string) (userID string, ok bool)

func (f JWTVerifierFunc) VerifyToken(tokenString string) (string, bool) {
	return f(tokenString)
}

// APILimiter rate-limits REST API requests per user and per IP.
type APILimiter struct {
	mu      sync.Mutex
	users   map[string]*rate.Limiter
	ips     map[string]*rate.Limiter
	perUser rate.Limit
	perIP   rate.Limit
	burst   int
	jwt     JWTVerifier
}

// NewAPILimiter creates an API rate limiter. perUser/perIP of 0 means unlimited.
func NewAPILimiter(cfg APIRateLimitConfig, jwt JWTVerifier) *APILimiter {
	perUser := rate.Limit(0)
	if cfg.PerUser > 0 {
		perUser = rate.Limit(cfg.PerUser)
	}
	perIP := rate.Limit(0)
	if cfg.PerIP > 0 {
		perIP = rate.Limit(cfg.PerIP)
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = 20
	}
	return &APILimiter{
		users:   make(map[string]*rate.Limiter),
		ips:     make(map[string]*rate.Limiter),
		perUser: perUser,
		perIP:   perIP,
		burst:   burst,
		jwt:     jwt,
	}
}

// Middleware returns an HTTP middleware that enforces rate limits.
func (l *APILimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ip := l.extractIdentity(r)

		if userID != "" && l.perUser > 0 {
			limiter := l.getOrCreate(l.users, userID, l.perUser)
			if !limiter.Allow() {
				writeRateLimitExceeded(w)
				return
			}
		}

		if l.perIP > 0 {
			limiter := l.getOrCreate(l.ips, ip, l.perIP)
			if !limiter.Allow() {
				writeRateLimitExceeded(w)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func writeRateLimitExceeded(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte(`{"code":429,"msg":"rate limit exceeded"}`))
}

func (l *APILimiter) extractIdentity(r *http.Request) (userID, ip string) {
	// Try Bearer token
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if uid, ok := l.jwt.VerifyToken(strings.TrimPrefix(h, "Bearer ")); ok {
			return uid, remoteIP(r)
		}
	}
	// Try Cookie
	if c, err := r.Cookie("token"); err == nil {
		if uid, ok := l.jwt.VerifyToken(c.Value); ok {
			return uid, remoteIP(r)
		}
	}
	return "", remoteIP(r)
}

func remoteIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func (l *APILimiter) getOrCreate(m map[string]*rate.Limiter, key string, r rate.Limit) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limiter, ok := m[key]; ok {
		return limiter
	}
	limiter := rate.NewLimiter(r, l.burst)
	m[key] = limiter
	return limiter
}
