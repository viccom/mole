package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

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

const (
	apiEntryTTL     = 10 * time.Minute
	apiCleanupThreshold = 1000
)

type limiterEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// APILimiter rate-limits REST API requests per user and per IP.
type APILimiter struct {
	mu      sync.Mutex
	users   map[string]*limiterEntry
	ips     map[string]*limiterEntry
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
		users:   make(map[string]*limiterEntry),
		ips:     make(map[string]*limiterEntry),
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
			entry := l.getOrCreate(l.users, userID, l.perUser)
			if !entry.limiter.Allow() {
				writeRateLimitExceeded(w)
				return
			}
		}

		if l.perIP > 0 {
			entry := l.getOrCreate(l.ips, ip, l.perIP)
			if !entry.limiter.Allow() {
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

func (l *APILimiter) getOrCreate(m map[string]*limiterEntry, key string, r rate.Limit) *limiterEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry, ok := m[key]; ok {
		entry.lastAccess = time.Now()
		return entry
	}
	entry := &limiterEntry{
		limiter:    rate.NewLimiter(r, l.burst),
		lastAccess: time.Now(),
	}
	m[key] = entry

	if len(m) >= apiCleanupThreshold {
		l.cleanupLocked(m)
	}
	return entry
}

func (l *APILimiter) cleanupLocked(m map[string]*limiterEntry) {
	now := time.Now()
	for k, entry := range m {
		if now.Sub(entry.lastAccess) > apiEntryTTL {
			delete(m, k)
		}
	}
}
