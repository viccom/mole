package api

import (
	"encoding/json"
	"net/http"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
)

type AuthHandler struct {
	svc *auth.AuthService
}

func NewAuthHandler(svc *auth.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		ResponseError(w, http.StatusMethodNotAllowed, 405, "Method not allowed")
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Username == "" || req.Password == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Username and password required")
		return
	}

	token, expiresAt, err := h.svc.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		switch err {
		case core.ErrInvalidCredentials, core.ErrUserDisabled:
			ResponseError(w, http.StatusUnauthorized, 401, "Invalid username or password")
		default:
			ResponseError(w, http.StatusInternalServerError, 500, "Internal server error")
		}
		return
	}

	claims, _ := h.svc.VerifyToken(token)
	ResponseOK(w, map[string]any{
		"token":      token,
		"expires_at": expiresAt,
		"user":       map[string]string{"id": claims.UserID, "username": claims.Username},
	})
}

// Logout 处理登出请求
// 由于使用无状态 JWT，服务端不维护 session，token 在过期前仍然有效。
// 客户端应自行删除本地存储的 token。
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	ResponseOK(w, "logged out")
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}

	token := ""
	if authVal := r.Header.Get("Authorization"); len(authVal) > 7 {
		token = authVal[7:]
	}
	if token == "" {
		if c, err := r.Cookie("token"); err == nil {
			token = c.Value
		}
	}

	newToken, err := h.svc.RefreshToken(token)
	if err != nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Failed to refresh token")
		return
	}

	ResponseOK(w, map[string]string{"token": newToken})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}
	ResponseOK(w, map[string]any{
		"id":       claims.UserID,
		"username": claims.Username,
		"roles":    claims.Roles,
	})
}

func (h *AuthHandler) ChangePass(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		ResponseError(w, http.StatusMethodNotAllowed, 405, "Method not allowed")
		return
	}
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}

	if err := h.svc.ChangePassword(r.Context(), claims.UserID, req.OldPassword, req.NewPassword); err != nil {
		switch err {
		case core.ErrInvalidCredentials:
			ResponseError(w, http.StatusUnauthorized, 401, "Old password is incorrect")
		default:
			ResponseError(w, http.StatusInternalServerError, 500, "Internal server error")
		}
		return
	}

	ResponseOK(w, "password changed")
}
