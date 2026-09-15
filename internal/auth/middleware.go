package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"moleAgent_Serv/internal/core"
)

type ctxKey string

const claimsCtxKey ctxKey = "claims"

// AuthMiddleware 认证中间件
type AuthMiddleware struct {
	jwtMgr      *JWTManager
	rbac        *RBACEngine
	accessKey   func() string // 获取全局 AccessKey
}

func NewAuthMiddleware(jwtMgr *JWTManager, rbac *RBACEngine, accessKeyFn func() string) *AuthMiddleware {
	return &AuthMiddleware{
		jwtMgr:    jwtMgr,
		rbac:      rbac,
		accessKey: accessKeyFn,
	}
}

// RequireAuth 需要认证的中间件
func (am *AuthMiddleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := am.authenticate(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, 401, "Unauthorized: missing or invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), claimsCtxKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePermission 需要特定权限的中间件
func (am *AuthMiddleware) RequirePermission(resource, action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := am.authenticate(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, 401, "Unauthorized")
				return
			}

			// AccessKey 绕过 RBAC
			if claims.UserID != "access_key" {
				allowed, err := am.rbac.CheckPermission(claims.UserID, resource, action)
				if err != nil {
					writeError(w, http.StatusInternalServerError, 500, "Internal server error")
					return
				}
				if !allowed {
					writeError(w, http.StatusForbidden, 403, "Forbidden: insufficient permissions")
					return
				}
			}

			ctx := context.WithValue(r.Context(), claimsCtxKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (am *AuthMiddleware) authenticate(r *http.Request) (*core.Claims, bool) {
	// 1. AccessKey
	if key := r.Header.Get("W-Access-Key"); key != "" {
		if ak := am.accessKey(); ak != "" && subtle.ConstantTimeCompare([]byte(key), []byte(ak)) == 1 {
			return &core.Claims{UserID: "access_key", Username: "access_key"}, true
		}
	}

	// 2. Bearer Token
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := am.jwtMgr.VerifyToken(token)
		if err == nil {
			return claims, true
		}
	}

	// 3. Cookie
	if cookie, err := r.Cookie("token"); err == nil {
		claims, err := am.jwtMgr.VerifyToken(cookie.Value)
		if err == nil {
			return claims, true
		}
	}

	// 4. Query parameter（WebSocket 连接使用）
	if token := r.URL.Query().Get("token"); token != "" {
		claims, err := am.jwtMgr.VerifyToken(token)
		if err == nil {
			return claims, true
		}
	}

	return nil, false
}

// GetClaims 从 context 获取 claims
func GetClaims(ctx context.Context) *core.Claims {
	if claims, ok := ctx.Value(claimsCtxKey).(*core.Claims); ok {
		return claims
	}
	return nil
}

// SetClaims 向 context 注入 claims（用于测试）
func SetClaims(ctx context.Context, claims *core.Claims) context.Context {
	return context.WithValue(ctx, claimsCtxKey, claims)
}

func writeError(w http.ResponseWriter, httpStatus, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(core.ApiResponse{Code: code, Msg: msg})
}
