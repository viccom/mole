package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/crypto"
	"moleAgent_Serv/internal/service"
)

type AccessTokenHandler struct {
	repo core.AccessTokenRepo
}

func NewAccessTokenHandler(repo core.AccessTokenRepo) *AccessTokenHandler {
	return &AccessTokenHandler{repo: repo}
}

func (h *AccessTokenHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}
	tokens, err := h.repo.ListByUser(claims.UserID)
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to list tokens")
		return
	}
	// 不返回 hash
	type tokenInfo struct {
		ID          string                    `json:"id"`
		Name        string                    `json:"name"`
		TokenPrefix string                    `json:"token_prefix"`
		Status      core.AccessTokenStatus    `json:"status"`
		LastUsedAt  *time.Time                `json:"last_used_at,omitempty"`
		CreatedAt   time.Time                 `json:"created_at"`
	}
	items := make([]tokenInfo, 0, len(tokens))
	for _, t := range tokens {
		items = append(items, tokenInfo{
			ID:          t.ID,
			Name:        t.Name,
			TokenPrefix: t.TokenPrefix,
			Status:      t.Status,
			LastUsedAt:  t.LastUsedAt,
			CreatedAt:   t.CreatedAt,
		})
	}
	ResponseOK(w, map[string]any{"items": items, "total": len(items)})
}

func (h *AccessTokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ResponseError(w, http.StatusBadRequest, 400, "Invalid request body")
		return
	}
	if req.Name == "" {
		ResponseError(w, http.StatusBadRequest, 400, "name is required")
		return
	}

	rawToken, err := service.GenerateAccessTokenRaw()
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to generate token")
		return
	}

	now := time.Now().UTC()
	token := &core.AccessToken{
		ID:          fmt.Sprintf("atk_%d", now.UnixNano()),
		UserID:      claims.UserID,
		Name:        req.Name,
		TokenPrefix: rawToken[:8],
		TokenHash:   crypto.GenerateTokenHash(rawToken),
		Status:      core.AccessTokenActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.repo.Create(token); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to save token")
		return
	}

	slog.Info("Access token created", "tokenId", token.ID, "userId", claims.UserID, "name", req.Name)

	ResponseOK(w, map[string]any{
		"id":         token.ID,
		"name":       token.Name,
		"token":      rawToken,
		"token_prefix": token.TokenPrefix,
		"created_at": token.CreatedAt,
	})
}

func (h *AccessTokenHandler) Delete(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/me/access-tokens/")
	id = strings.TrimRight(id, "/")

	token, err := h.repo.GetByID(id)
	if err != nil {
		ResponseError(w, http.StatusNotFound, 404, "Token not found")
		return
	}
	if token.UserID != claims.UserID {
		ResponseError(w, http.StatusNotFound, 404, "Token not found")
		return
	}
	if err := h.repo.Delete(id); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to delete token")
		return
	}
	slog.Info("Access token deleted", "tokenId", id, "userId", claims.UserID)
	ResponseOK(w, "deleted")
}

func (h *AccessTokenHandler) Rotate(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "Unauthorized")
		return
	}
	path := r.URL.Path
	id := strings.TrimPrefix(path, "/api/v1/me/access-tokens/")
	id = strings.TrimSuffix(id, "/rotate")
	id = strings.TrimRight(id, "/")

	token, err := h.repo.GetByID(id)
	if err != nil {
		ResponseError(w, http.StatusNotFound, 404, "Token not found")
		return
	}
	if token.UserID != claims.UserID {
		ResponseError(w, http.StatusNotFound, 404, "Token not found")
		return
	}

	rawToken, err := service.GenerateAccessTokenRaw()
	if err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to generate token")
		return
	}

	token.TokenHash = crypto.GenerateTokenHash(rawToken)
	token.TokenPrefix = rawToken[:8]
	if err := h.repo.Update(token); err != nil {
		ResponseError(w, http.StatusInternalServerError, 500, "Failed to rotate token")
		return
	}

	slog.Info("Access token rotated", "tokenId", id, "userId", claims.UserID)

	ResponseOK(w, map[string]any{
		"id":           token.ID,
		"name":         token.Name,
		"token":        rawToken,
		"token_prefix": token.TokenPrefix,
	})
}
