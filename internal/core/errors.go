package core

import "errors"

var (
	// 认证错误
	ErrUnauthorized       = errors.New("unauthorized")
	ErrTokenExpired       = errors.New("token expired")
	ErrTokenInvalid       = errors.New("invalid token")
	ErrInvalidCredentials = errors.New("invalid username or password")

	// 权限错误
	ErrForbidden = errors.New("forbidden: insufficient permissions")

	// 用户错误
	ErrUserNotFound = errors.New("user not found")
	ErrUserExists   = errors.New("user already exists")
	ErrUserDisabled = errors.New("user is disabled")

	// 口令策略（Create/Update/resetPassword/ChangePassword 共用，SEC-14）
	ErrPasswordTooShort = errors.New("Password must be at least 8 characters")

	// 角色错误
	ErrRoleNotFound = errors.New("role not found")
	ErrRoleExists   = errors.New("role already exists")

	// 节点错误
	ErrNodeNotFound   = errors.New("node not found")
	ErrNodeExists     = errors.New("node already exists")
	ErrNodeOffline    = errors.New("node is offline")
	ErrNodeAuthFailed = errors.New("node authentication failed")

	// 隧道错误
	ErrTunnelNotFound = errors.New("tunnel not found")
	ErrTunnelExists   = errors.New("tunnel already exists")
	ErrPortInUse      = errors.New("port already in use")
	ErrTunnelInvalid  = errors.New("invalid tunnel configuration")

	// 通用错误
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrValidation    = errors.New("validation failed")
	ErrInvalidNodeID = errors.New("node_id must be exactly 8 alphanumeric characters starting with a letter")
	ErrInternal      = errors.New("internal server error")
)
