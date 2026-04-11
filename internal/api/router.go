package api

import (
	"log/slog"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/auth"
)

// Router API 路由器
type Router struct {
	mw     *auth.AuthMiddleware
	routes []routeEntry
}

type routeEntry struct {
	method  string
	prefix  string
	handler http.Handler
}

func NewRouter(mw *auth.AuthMiddleware) *Router {
	return &Router{mw: mw}
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

// Build 构建 http.Handler
func (r *Router) Build() http.Handler {
	return loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// 三阶段匹配：
		// 1. 精确路径 + method 一致
		// 2. 最长前缀 + method 一致（优先选择不冲突的最长匹配）
		// 3. Method 不匹配检测

		var bestMatch *routeEntry
		bestMatchLen := 0
		var pathMatchedButMethodMismatch bool

		for i := range r.routes {
			e := &r.routes[i]

			// 精确匹配
			if req.URL.Path == e.prefix {
				if req.Method == e.method || req.Method == "OPTIONS" {
					e.handler.ServeHTTP(w, req)
					return
				}
				pathMatchedButMethodMismatch = true
			}

			// 前缀匹配
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
