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
	apiEntryTTL        = 10 * time.Minute
	apiCleanupInterval = 2 * time.Minute
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
	jwt         JWTVerifier
	trustedNets []*net.IPNet

	stopCh chan struct{}
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
	l := &APILimiter{
		users:       make(map[string]*limiterEntry),
		ips:         make(map[string]*limiterEntry),
		perUser:     perUser,
		perIP:       perIP,
		burst:       burst,
		jwt:         jwt,
		trustedNets: parseTrustedProxies(cfg.TrustedProxies),
		stopCh:      make(chan struct{}),
	}
	go l.cleanupLoop()
	return l
}

// Close stops the background cleanup goroutine.
func (l *APILimiter) Close() {
	close(l.stopCh)
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
	if l.jwt != nil {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			if uid, ok := l.jwt.VerifyToken(strings.TrimPrefix(h, "Bearer ")); ok {
				return uid, l.remoteIP(r)
			}
		}
		if c, err := r.Cookie("token"); err == nil {
			if uid, ok := l.jwt.VerifyToken(c.Value); ok {
				return uid, l.remoteIP(r)
			}
		}
	}
	return "", l.remoteIP(r)
}

func (l *APILimiter) remoteIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if len(l.trustedNets) == 0 {
		return ip
	}
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil || !isTrustedProxy(parsedIP, l.trustedNets) {
		return ip
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return ip
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		candidateIP := net.ParseIP(candidate)
		if candidateIP == nil {
			continue
		}
		if !isTrustedProxy(candidateIP, l.trustedNets) {
			return candidate
		}
	}
	return ip
}

func isTrustedProxy(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func parseTrustedProxies(proxies []string) []*net.IPNet {
	var nets []*net.IPNet
	for _, p := range proxies {
		p = strings.TrimSpace(p)
		if strings.Contains(p, "/") {
			_, ipNet, err := net.ParseCIDR(p)
			if err != nil {
				continue
			}
			nets = append(nets, ipNet)
		} else {
			ip := net.ParseIP(p)
			if ip == nil {
				continue
			}
			if ipv4 := ip.To4(); ipv4 != nil {
				nets = append(nets, &net.IPNet{IP: ipv4, Mask: net.CIDRMask(32, 32)})
			} else {
				nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
			}
		}
	}
	return nets
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

func (l *APILimiter) cleanupLoop() {
	ticker := time.NewTicker(apiCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.mu.Lock()
			l.cleanupLocked(l.users)
			l.cleanupLocked(l.ips)
			l.mu.Unlock()
		}
	}
}
