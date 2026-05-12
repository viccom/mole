package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/feishu"

	"github.com/nalgeon/redka"
)

type FeishuHandler struct {
	feishuClient *feishu.Client
	authSvc      *auth.AuthService
	bindingRepo  core.FeishuBindingRepo
	userRepo     core.UserRepo
	jwtMgr       *auth.JWTManager
	rbac         *auth.RBACEngine
	db           *redka.DB
}

func NewFeishuHandler(
	feishuClient *feishu.Client,
	authSvc *auth.AuthService,
	bindingRepo core.FeishuBindingRepo,
	userRepo core.UserRepo,
	jwtMgr *auth.JWTManager,
	rbac *auth.RBACEngine,
	db *redka.DB,
) *FeishuHandler {
	return &FeishuHandler{
		feishuClient: feishuClient,
		authSvc:      authSvc,
		bindingRepo:  bindingRepo,
		userRepo:     userRepo,
		jwtMgr:       jwtMgr,
		rbac:         rbac,
		db:           db,
	}
}

func (h *FeishuHandler) getRoleNames(userID string) []string {
	roles, _ := h.rbac.GetUserRoles(userID)
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = r.Name
	}
	return names
}

type feishuCallbackRequest struct {
	Code string `json:"code"`
}

type feishuCallbackResponse struct {
	NeedBind    bool   `json:"need_bind"`
	FeishuToken string `json:"feishu_token,omitempty"`
	FeishuName  string `json:"feishu_name,omitempty"`
	Token       string `json:"token,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	User        any    `json:"user,omitempty"`
}

func (h *FeishuHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.feishuClient.Enabled() {
		ResponseError(w, http.StatusServiceUnavailable, 503, "Feishu integration not configured")
		return
	}

	var req feishuCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Code == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Missing code")
		return
	}

	userInfo, err := h.feishuClient.GetUserByCode(r.Context(), req.Code)
	if err != nil {
		slog.Warn("Feishu get user by code failed", "error", err)
		ResponseError(w, http.StatusUnauthorized, 401, "Failed to get Feishu user info")
		return
	}

	binding, err := h.bindingRepo.GetByOpenID(userInfo.OpenID)
	if err == nil && binding != nil {
		claims := &core.Claims{
			UserID:   binding.UserID,
			Username: binding.UserID,
			Roles:    h.getRoleNames(binding.UserID),
		}

		userObj, uerr := h.userRepo.GetByID(binding.UserID)
		if uerr == nil {
			claims.Username = userObj.Username
		}

		token, exp, err := h.jwtMgr.GenerateToken(claims)
		if err != nil {
			ResponseError(w, http.StatusInternalServerError, 500, "Failed to generate token")
			return
		}

		slog.Info("Feishu SSO login", "open_id", userInfo.OpenID, "userId", binding.UserID)
		ResponseOK(w, feishuCallbackResponse{
			NeedBind:  false,
			Token:     token,
			ExpiresAt: exp.Format(time.RFC3339),
			User:      map[string]string{"id": claims.UserID, "username": claims.Username},
		})
		return
	}

	bindToken, err := h.generateBindToken(userInfo)
	slog.Info("Feishu bind token generated", "token_prefix", bindToken[:8], "open_id", userInfo.OpenID)
	if err != nil {
		slog.Error("Failed to generate bind token", "error", err)
		ResponseError(w, http.StatusInternalServerError, 500, "Internal error")
		return
	}

	ResponseOK(w, feishuCallbackResponse{
		NeedBind:    true,
		FeishuToken: bindToken,
		FeishuName:  userInfo.Name,
	})
}

type feishuBindRequest struct {
	FeishuToken string `json:"feishu_token"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

func (h *FeishuHandler) Bind(w http.ResponseWriter, r *http.Request) {
	if !h.feishuClient.Enabled() {
		ResponseError(w, http.StatusServiceUnavailable, 503, "Feishu integration not configured")
		return
	}

	var req feishuBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.FeishuToken == "" || req.Username == "" || req.Password == "" {
		ResponseError(w, http.StatusBadRequest, 400, "All fields required")
		return
	}

	slog.Info("Feishu bind attempt", "token_prefix", req.FeishuToken[:min(8, len(req.FeishuToken))], "username", req.Username)
	bindData, err := h.consumeBindToken(req.FeishuToken)
	if err != nil {
		slog.Warn("Feishu consume bind token failed", "error", err, "token_prefix", req.FeishuToken[:min(8, len(req.FeishuToken))])
		ResponseError(w, http.StatusBadRequest, 400, "Invalid or expired feishu token")
		return
	}

	user, err := h.userRepo.GetByUsername(req.Username)
	if err != nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Invalid username or password")
		return
	}
	if user.Status == core.UserStatusDisabled {
		ResponseError(w, http.StatusUnauthorized, 401, "Invalid username or password")
		return
	}

	hash, err := h.userRepo.GetPasswordHash(user.ID)
	if err != nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Invalid username or password")
		return
	}
	if !auth.VerifyPassword(req.Password, hash) {
		ResponseError(w, http.StatusUnauthorized, 401, "Invalid username or password")
		return
	}

	existing, _ := h.bindingRepo.GetByUserID(user.ID)
	if existing != nil {
		ResponseError(w, http.StatusConflict, 409, "Account already bound to another Feishu identity")
		return
	}

	binding := &core.FeishuBinding{
		OpenID:     bindData.OpenID,
		UserID:     user.ID,
		FeishuName: bindData.FeishuName,
		AvatarURL:  bindData.AvatarURL,
		BoundAt:    time.Now().UTC(),
	}
	if err := h.bindingRepo.Create(binding); err != nil {
		slog.Error("Failed to create feishu binding", "error", err)
		ResponseError(w, http.StatusInternalServerError, 500, "Internal server error")
		return
	}

	claims := &core.Claims{
		UserID:   user.ID,
		Username: user.Username,
		Roles:    h.getRoleNames(user.ID),
	}
	token, exp, err := h.jwtMgr.GenerateToken(claims)
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Internal server error")
		return
	}

	slog.Info("Feishu account bound", "open_id", bindData.OpenID, "userId", user.ID, "username", user.Username)
	ResponseOK(w, map[string]any{
		"token":      token,
		"expires_at": exp.Format(time.RFC3339),
		"user":       map[string]string{"id": user.ID, "username": user.Username},
	})
}

func (h *FeishuHandler) GetBinding(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}

	binding, err := h.bindingRepo.GetByUserID(claims.UserID)
	if err != nil {
		ResponseOK(w, map[string]any{"bound": false})
		return
	}

	ResponseOK(w, map[string]any{
		"bound":       true,
		"feishu_name": binding.FeishuName,
		"avatar_url":  binding.AvatarURL,
		"bound_at":    binding.BoundAt.Format(time.RFC3339),
	})
}

func (h *FeishuHandler) Unbind(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}

	if err := h.bindingRepo.DeleteByUserID(claims.UserID); err != nil {
		ResponseError(w, http.StatusNotFound, 404, "No binding found")
		return
	}

	slog.Info("Feishu account unbound", "userId", claims.UserID)
	ResponseOK(w, "unbound")
}

func (h *FeishuHandler) Config(w http.ResponseWriter, r *http.Request) {
	appID := ""
	if h.feishuClient.Enabled() {
		appID = h.feishuClient.AppID()
	}
	ResponseOK(w, map[string]string{"app_id": appID})
}

type bindTokenData struct {
	OpenID     string `json:"open_id"`
	FeishuName string `json:"feishu_name"`
	AvatarURL  string `json:"avatar_url"`
}

func (h *FeishuHandler) generateBindToken(userInfo *feishu.UserInfo) (string, error) {
	token := randomHex(32)
	data, err := json.Marshal(bindTokenData{
		OpenID:     userInfo.OpenID,
		FeishuName: userInfo.Name,
		AvatarURL:  userInfo.AvatarURL,
	})
	if err != nil {
		return "", err
	}
	if err := h.db.Str().SetExpire("feishu_bind_tokens:"+token, string(data), 300*time.Second); err != nil {
		return "", err
	}
	return token, nil
}

func (h *FeishuHandler) consumeBindToken(token string) (*bindTokenData, error) {
	key := "feishu_bind_tokens:" + token
	val, err := h.db.Str().Get(key)
	if err != nil || val.String() == "" {
		return nil, fmt.Errorf("bind token not found or expired")
	}
	h.db.Key().Delete(key)

	var data bindTokenData
	if err := json.Unmarshal([]byte(val.String()), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	rand.Read(b)
	return hex.EncodeToString(b)
}
