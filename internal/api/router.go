package api

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/ratelimit"
)

// Router API 路由器
type Router struct {
	mw      *auth.AuthMiddleware
	limiter *ratelimit.APILimiter
	routes  []routeEntry
}

type routeEntry struct {
	method  string
	prefix  string
	handler http.Handler
}

func NewRouter(mw *auth.AuthMiddleware, limiter *ratelimit.APILimiter) *Router {
	return &Router{mw: mw, limiter: limiter}
}

func (r *Router) Register(method, pattern string, handler http.HandlerFunc, resource, action string) {
	var h http.Handler = handler
	if resource != "" && action != "" {
		h = r.mw.RequirePermission(resource, action)(h)
	} else {
		h = r.mw.RequireAuth(h)
	}
	r.routes = append(r.routes, routeEntry{method: method, prefix: pattern, handler: h})
}

// RegisterStrict 注册拒绝 AccessKey 旁路的权限路由（SEC-06）：用于高危操作
// （删用户/AccessKey 管理/自更新），这些不允许以共享密钥身份执行
func (r *Router) RegisterStrict(method, pattern string, handler http.HandlerFunc, resource, action string) {
	h := r.mw.RequirePermissionNoAccessKey(resource, action)(handler)
	r.routes = append(r.routes, routeEntry{method: method, prefix: pattern, handler: h})
}

func (r *Router) RegisterPublic(method, pattern string, handler http.HandlerFunc) {
	r.routes = append(r.routes, routeEntry{method: method, prefix: pattern, handler: handler})
}

func (r *Router) RegisterAuth(method, pattern string, handler http.HandlerFunc) {
	var h http.Handler = r.mw.RequireAuth(handler)
	r.routes = append(r.routes, routeEntry{method: method, prefix: pattern, handler: h})
}

// Build constructs the http.Handler with optional rate limiting.
func (r *Router) Build() http.Handler {
	core := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// 3-phase matching:
		// 1. exact path + method
		// 2. longest prefix + method
		// 3. method mismatch detection

		var bestMatch *routeEntry
		bestMatchLen := 0
		var pathMatchedButMethodMismatch bool

		for i := range r.routes {
			e := &r.routes[i]

			if req.URL.Path == e.prefix {
				// QUA-12：OPTIONS 预检命中路由直接 204 + CORS 头，不调用
				// 真实 handler——预检不携带凭据，走认证会 401，走放行路由
				// 则会产生副作用
				if req.Method == http.MethodOptions {
					handlePreflight(w, req)
					return
				}
				if req.Method == e.method {
					e.handler.ServeHTTP(w, req)
					return
				}
				pathMatchedButMethodMismatch = true
			}

			if strings.HasPrefix(req.URL.Path, e.prefix) && len(e.prefix) > bestMatchLen {
				if req.Method == e.method || req.Method == http.MethodOptions {
					bestMatch = e
					bestMatchLen = len(e.prefix)
				}
			}
		}

		if bestMatch != nil {
			if req.Method == http.MethodOptions {
				handlePreflight(w, req)
				return
			}
			bestMatch.handler.ServeHTTP(w, req)
			return
		}

		if pathMatchedButMethodMismatch {
			ResponseError(w, http.StatusMethodNotAllowed, 405, "Method not allowed")
			return
		}

		ResponseError(w, http.StatusNotFound, 404, "Not found: "+req.URL.Path)
	}))

	if r.limiter != nil {
		mw := r.limiter.Middleware(core)
		core = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/api/v1/health" {
				core.ServeHTTP(w, req)
				return
			}
			mw.ServeHTTP(w, req)
		})
	}
	return core
}

// handlePreflight 统一应答 OPTIONS 预检（QUA-12）：仅回 CORS 头 + 204，
// 不经过认证中间件、不触发真实 handler。服务端无既有 CORS 中间件
// （管理台同源部署），此处为预检的最小实现
func handlePreflight(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	if hdrs := r.Header.Get("Access-Control-Request-Headers"); hdrs != "" {
		w.Header().Set("Access-Control-Allow-Headers", hdrs)
	}
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v1/health" || strings.HasPrefix(req.URL.Path, "/admin/") {
			next.ServeHTTP(w, req)
			return
		}

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, req)

		level := slog.LevelInfo
		if rw.statusCode >= 400 {
			level = slog.LevelWarn
		}
		if rw.statusCode >= 500 {
			level = slog.LevelError
		}

		slog.Log(req.Context(), level, "HTTP request",
			"method", req.Method,
			"path", req.URL.Path,
			"status", rw.statusCode,
			"remote", req.RemoteAddr,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return rw.ResponseWriter.(http.Hijacker).Hijack()
}
