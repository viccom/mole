package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/dingtalk"

	"github.com/nalgeon/redka"
)

type DingTalkHandler struct {
	dingtalkClient *dingtalk.Client
	authSvc        *auth.AuthService
	bindingRepo    core.DingTalkBindingRepo
	userRepo       core.UserRepo
	jwtMgr         *auth.JWTManager
	rbac           *auth.RBACEngine
	db             *redka.DB
}

func NewDingTalkHandler(
	dingtalkClient *dingtalk.Client,
	authSvc *auth.AuthService,
	bindingRepo core.DingTalkBindingRepo,
	userRepo core.UserRepo,
	jwtMgr *auth.JWTManager,
	rbac *auth.RBACEngine,
	db *redka.DB,
) *DingTalkHandler {
	return &DingTalkHandler{
		dingtalkClient: dingtalkClient,
		authSvc:        authSvc,
		bindingRepo:    bindingRepo,
		userRepo:       userRepo,
		jwtMgr:         jwtMgr,
		rbac:           rbac,
		db:             db,
	}
}

func (h *DingTalkHandler) getRoleNames(userID string) []string {
	roles, _ := h.rbac.GetUserRoles(userID)
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = r.Name
	}
	return names
}

type dingtalkCallbackRequest struct {
	Code   string `json:"code"`
	Source string `json:"source"` // "h5" or "oauth2"
}

type dingtalkCallbackResponse struct {
	NeedBind      bool   `json:"need_bind"`
	DingtalkToken string `json:"dingtalk_token,omitempty"`
	DingtalkName  string `json:"dingtalk_name,omitempty"`
	Token         string `json:"token,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	User          any    `json:"user,omitempty"`
}

func (h *DingTalkHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.dingtalkClient.Enabled() {
		ResponseError(w, http.StatusServiceUnavailable, 503, "DingTalk integration not configured")
		return
	}

	var req dingtalkCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Code == "" {
		ResponseError(w, http.StatusBadRequest, 400, "Missing code")
		return
	}

	var userInfo *dingtalk.UserInfo
	var err error

	if req.Source == "oauth2" {
		userInfo, err = h.dingtalkClient.GetUserByOAuthCode(r.Context(), req.Code)
	} else {
		userInfo, err = h.dingtalkClient.GetUserByCode(r.Context(), req.Code)
	}
	if err != nil {
		slog.Warn("DingTalk get user failed", "error", err, "source", req.Source)
		ResponseError(w, http.StatusUnauthorized, 401, "Failed to get DingTalk user info")
		return
	}

	binding, err := h.bindingRepo.GetByUnionID(userInfo.UnionID)
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

		slog.Info("DingTalk SSO login", "union_id", userInfo.UnionID, "userId", binding.UserID)
		ResponseOK(w, dingtalkCallbackResponse{
			NeedBind:  false,
			Token:     token,
			ExpiresAt: exp.Format(time.RFC3339),
			User:      map[string]string{"id": claims.UserID, "username": claims.Username},
		})
		return
	}

	bindToken, err := h.generateDingTalkBindToken(userInfo)
	if err != nil {
		slog.Error("Failed to generate dingtalk bind token", "error", err)
		ResponseError(w, http.StatusInternalServerError, 500, "Internal error")
		return
	}

	slog.Info("DingTalk bind token generated", "token_prefix", bindToken[:8], "union_id", userInfo.UnionID)
	ResponseOK(w, dingtalkCallbackResponse{
		NeedBind:      true,
		DingtalkToken: bindToken,
		DingtalkName:  userInfo.Name,
	})
}

type dingtalkBindRequest struct {
	DingtalkToken string `json:"dingtalk_token"`
	Username      string `json:"username"`
	Password      string `json:"password"`
}

func (h *DingTalkHandler) Bind(w http.ResponseWriter, r *http.Request) {
	if !h.dingtalkClient.Enabled() {
		ResponseError(w, http.StatusServiceUnavailable, 503, "DingTalk integration not configured")
		return
	}

	var req dingtalkBindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.DingtalkToken == "" || req.Username == "" || req.Password == "" {
		ResponseError(w, http.StatusBadRequest, 400, "All fields required")
		return
	}

	slog.Info("DingTalk bind attempt", "token_prefix", req.DingtalkToken[:min(8, len(req.DingtalkToken))], "username", req.Username)
	bindData, err := h.consumeDingTalkBindToken(req.DingtalkToken)
	if err != nil {
		slog.Warn("DingTalk consume bind token failed", "error", err)
		ResponseError(w, http.StatusBadRequest, 400, "Invalid or expired dingtalk token")
		return
	}

	user, err := h.userRepo.GetByUsername(req.Username)
	if err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid username or password")
		return
	}
	if user.Status == core.UserStatusDisabled {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid username or password")
		return
	}

	hash, err := h.userRepo.GetPasswordHash(user.ID)
	if err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid username or password")
		return
	}
	if !auth.VerifyPassword(req.Password, hash) {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid username or password")
		return
	}

	existing, _ := h.bindingRepo.GetByUserID(user.ID)
	if existing != nil {
		ResponseError(w, http.StatusConflict, 409, "Account already bound to another DingTalk identity")
		return
	}

	binding := &core.DingTalkBinding{
		UnionID:   bindData.UnionID,
		UserID:    user.ID,
		DingName:  bindData.DingName,
		AvatarURL: bindData.AvatarURL,
		BoundAt:   time.Now().UTC(),
	}
	if err := h.bindingRepo.Create(binding); err != nil {
		slog.Error("Failed to create dingtalk binding", "error", err)
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

	slog.Info("DingTalk account bound", "union_id", bindData.UnionID, "userId", user.ID, "username", user.Username)
	ResponseOK(w, map[string]any{
		"token":      token,
		"expires_at": exp.Format(time.RFC3339),
		"user":       map[string]string{"id": user.ID, "username": user.Username},
	})
}

func (h *DingTalkHandler) GetBinding(w http.ResponseWriter, r *http.Request) {
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
		"bound":      true,
		"ding_name":  binding.DingName,
		"avatar_url": binding.AvatarURL,
		"bound_at":   binding.BoundAt.Format(time.RFC3339),
	})
}

func (h *DingTalkHandler) Unbind(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}

	if err := h.bindingRepo.DeleteByUserID(claims.UserID); err != nil {
		ResponseError(w, http.StatusNotFound, 404, "No binding found")
		return
	}

	slog.Info("DingTalk account unbound", "userId", claims.UserID)
	ResponseOK(w, "unbound")
}

func (h *DingTalkHandler) Config(w http.ResponseWriter, r *http.Request) {
	corpID := ""
	appKey := ""
	if h.dingtalkClient.Enabled() {
		corpID = h.dingtalkClient.CorpID()
		appKey = h.dingtalkClient.AppKey()
	}
	ResponseOK(w, map[string]string{"corp_id": corpID, "app_key": appKey})
}

type dingtalkBindTokenData struct {
	UnionID   string `json:"union_id"`
	DingName  string `json:"ding_name"`
	AvatarURL string `json:"avatar_url"`
}

func (h *DingTalkHandler) generateDingTalkBindToken(userInfo *dingtalk.UserInfo) (string, error) {
	token := randomHex(32)
	data, err := json.Marshal(dingtalkBindTokenData{
		UnionID:   userInfo.UnionID,
		DingName:  userInfo.Name,
		AvatarURL: userInfo.AvatarURL,
	})
	if err != nil {
		return "", err
	}
	if err := h.db.Str().SetExpire("dingtalk_bind_tokens:"+token, string(data), 300*time.Second); err != nil {
		return "", err
	}
	return token, nil
}

func (h *DingTalkHandler) consumeDingTalkBindToken(token string) (*dingtalkBindTokenData, error) {
	key := "dingtalk_bind_tokens:" + token
	val, err := h.db.Str().Get(key)
	if err != nil || val.String() == "" {
		return nil, fmt.Errorf("bind token not found or expired")
	}
	h.db.Key().Delete(key)

	var data dingtalkBindTokenData
	if err := json.Unmarshal([]byte(val.String()), &data); err != nil {
		return nil, err
	}
	return &data, nil
}
