package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nalgeon/redka"

	"moleAgent_Serv/internal/core"
)

type AuthService struct {
	jwtMgr   *JWTManager
	userRepo core.UserRepo
	roleRepo core.RoleRepo
	rbac     *RBACEngine
	db       *redka.DB
}

func NewAuthService(jwtMgr *JWTManager, userRepo core.UserRepo, roleRepo core.RoleRepo, rbac *RBACEngine, db *redka.DB) *AuthService {
	return &AuthService{
		jwtMgr:   jwtMgr,
		userRepo: userRepo,
		roleRepo: roleRepo,
		rbac:     rbac,
		db:       db,
	}
}

// Login 用户登录
func (s *AuthService) Login(ctx context.Context, username, password string) (string, string, error) {
	user, err := s.userRepo.GetByUsername(username)
	if err != nil {
		slog.Warn("User login failed", "username", username, "reason", "user not found")
		return "", "", core.ErrInvalidCredentials
	}

	if user.Status == core.UserStatusDisabled {
		slog.Warn("User login failed", "username", username, "reason", "user disabled")
		return "", "", core.ErrUserDisabled
	}

	hash, err := s.userRepo.GetPasswordHash(user.ID)
	if err != nil {
		slog.Warn("User login failed", "username", username, "reason", "no password")
		return "", "", core.ErrInvalidCredentials
	}

	if !VerifyPassword(password, hash) {
		slog.Warn("User login failed", "username", username, "reason", "wrong password")
		return "", "", core.ErrInvalidCredentials
	}

	// 获取角色
	roles, _ := s.rbac.GetUserRoles(user.ID)
	roleNames := make([]string, len(roles))
	for i, r := range roles {
		roleNames[i] = r.Name
	}

	claims := &core.Claims{
		UserID:   user.ID,
		Username: user.Username,
		Roles:    roleNames,
	}

	token, exp, err := s.jwtMgr.GenerateToken(claims)
	if err != nil {
		return "", "", err
	}

	// 存储会话
	sessionData, err := json.Marshal(map[string]any{
		"token":      token,
		"expires_at": exp.Format(time.RFC3339),
	})
	if err != nil {
		slog.Warn("Failed to marshal session data", "error", err)
	}
	if err := s.db.Str().SetExpire("sessions:"+user.ID, string(sessionData), 24*60*60); err != nil {
		slog.Warn("Failed to store session", "error", err)
	}

	slog.Info("User login", "userId", user.ID, "username", username)
	return token, exp.Format(time.RFC3339), nil
}

// VerifyToken 验证 token
func (s *AuthService) VerifyToken(token string) (*core.Claims, error) {
	return s.jwtMgr.VerifyToken(token)
}

// RefreshToken 刷新 token
func (s *AuthService) RefreshToken(tokenStr string) (string, error) {
	claims, err := s.jwtMgr.VerifyToken(tokenStr)
	if err != nil {
		return "", err
	}
	token, _, err := s.jwtMgr.GenerateToken(claims)
	return token, err
}

// ChangePassword 修改密码
func (s *AuthService) ChangePassword(ctx context.Context, userID, oldPass, newPass string) error {
	hash, err := s.userRepo.GetPasswordHash(userID)
	if err != nil {
		return core.ErrUserNotFound
	}
	if !VerifyPassword(oldPass, hash) {
		return core.ErrInvalidCredentials
	}
	newHash, err := HashPassword(newPass, 12)
	if err != nil {
		return err
	}
	return s.userRepo.SetPasswordHash(userID, newHash)
}

// VerifyMQTTCredentials 验证 MQTT 凭据
func (s *AuthService) VerifyMQTTCredentials(username, password string) bool {
	// 检查 mqtt_users 表
	val, err := s.db.Hash().Get("mqtt_users", username)
	if err != nil {
		return false
	}
	var mqttUser struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(val.String()), &mqttUser); err != nil {
		return false
	}
	return VerifyPassword(password, mqttUser.Password)
}
