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
				if req.Method == e.method || req.Method == "OPTIONS" {
					e.handler.ServeHTTP(w, req)
					return
				}
				pathMatchedButMethodMismatch = true
			}

			if strings.HasPrefix(req.URL.Path, e.prefix) && len(e.prefix) > bestMatchLen {
				if req.Method == e.method || req.Method == "OPTIONS" {
					bestMatch = e
					bestMatchLen = len(e.prefix)
				}
			}
		}

		if bestMatch != nil {
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
		core = r.limiter.Middleware(core)
	}
	return core
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
