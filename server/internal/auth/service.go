package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/nalgeon/redka"
	"golang.org/x/crypto/bcrypt"

	"moleAgent_Serv/internal/core"
)

// dummyBcryptHash 登录时序防用户名枚举（QUA-06）：包初始化时按生产默认
// cost 生成一次，Login 全部「bcrypt 前返回」的快路径对输入口令执行等价
// bcrypt 比较，消除与「密码错误」路径的响应时序差。
// cost 取 12：与 internal/config DefaultConfig 的 bcrypt_cost 默认值（字面量
// 12，无导出常量）一致。未引用 config 是有意取舍——auth 是低层包，为取一个
// int 引入整个 YAML 配置包（含其依赖树）不成比例；config 默认值变更时需
// 同步此处（config_test 对 4-31 范围有校验，值漂移可被其单测捕获）
var dummyBcryptHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("timing-equalizer-dummy-password"), 12)
	if err != nil {
		panic("auth: generate dummy bcrypt hash: " + err.Error())
	}
	return h
}()

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
		// QUA-06：执行一次注定失败的 bcrypt 比较，让本路径与「密码错误」
		// 路径做等量哈希功（快路径的响应时序差会被用来枚举用户名）
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		slog.Warn("User login failed", "username", username, "reason", "user not found")
		return "", "", core.ErrInvalidCredentials
	}

	if user.Status == core.UserStatusDisabled {
		// QUA-06 残余：禁用路径同样先做等量 bcrypt 功再返回——本路径在哈希
		// 比较前返回，快路径的时序差可被用来枚举用户名（文案保持不变）
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		slog.Warn("User login failed", "username", username, "reason", "user disabled")
		return "", "", core.ErrUserDisabled
	}

	hash, err := s.userRepo.GetPasswordHash(user.ID)
	if err != nil {
		// QUA-06 残余：哈希缺失路径同理（用户存在但从未设密）
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
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
	// 必须复查用户当前状态：禁用/删除的用户若凭未过期 JWT 无限续期，
	// "禁用账号" 形同虚设（服务端无会话吊销机制，refresh 是唯一收口点）
	user, err := s.userRepo.GetByID(claims.UserID)
	if err != nil {
		return "", core.ErrUserNotFound
	}
	if user.Status == core.UserStatusDisabled {
		return "", core.ErrUserDisabled
	}
	// 角色同样必须回库重查：旧 claims 的 Roles 是签发时快照，撤销/变更后
	// 照抄续期会让权限收回形同虚设（与 Login 同源重建，风格一致）
	roles, _ := s.rbac.GetUserRoles(claims.UserID)
	roleNames := make([]string, len(roles))
	for i, r := range roles {
		roleNames[i] = r.Name
	}
	claims.Roles = roleNames
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
	// SEC-14：新密码与 Create/Update/resetPassword 同一强度校验，
	// 自助路径不得成为弱口令入口
	if err := ValidatePasswordStrength(newPass); err != nil {
		return err
	}
	newHash, err := HashPassword(newPass, 12)
	if err != nil {
		return err
	}
	return s.userRepo.SetPasswordHash(userID, newHash)
}

// VerifyMQTTCredentials 验证 MQTT 凭据，返回 userID（统一权限主体）
func (s *AuthService) VerifyMQTTCredentials(username, password string) (string, bool) {
	user, err := s.userRepo.GetByUsername(username)
	if err != nil {
		return "", false
	}
	if user.Status != core.UserStatusActive {
		return "", false
	}
	hash, err := s.userRepo.GetPasswordHash(user.ID)
	if err != nil {
		return "", false
	}
	if !VerifyPassword(password, hash) {
		return "", false
	}
	return user.ID, true
}
