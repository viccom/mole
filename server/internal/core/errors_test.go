package core

import (
	"errors"
	"testing"
)

func TestDomainErrors(t *testing.T) {
	errs := map[string]error{
		"ErrUnauthorized":       ErrUnauthorized,
		"ErrTokenExpired":       ErrTokenExpired,
		"ErrTokenInvalid":       ErrTokenInvalid,
		"ErrInvalidCredentials": ErrInvalidCredentials,
		"ErrForbidden":          ErrForbidden,
		"ErrUserNotFound":       ErrUserNotFound,
		"ErrUserExists":         ErrUserExists,
		"ErrUserDisabled":       ErrUserDisabled,
		"ErrRoleNotFound":       ErrRoleNotFound,
		"ErrRoleExists":         ErrRoleExists,
		"ErrNodeNotFound":       ErrNodeNotFound,
		"ErrNodeExists":         ErrNodeExists,
		"ErrNodeOffline":        ErrNodeOffline,
		"ErrNodeAuthFailed":     ErrNodeAuthFailed,
		"ErrTunnelNotFound":     ErrTunnelNotFound,
		"ErrTunnelExists":       ErrTunnelExists,
		"ErrPortInUse":          ErrPortInUse,
		"ErrNotFound":           ErrNotFound,
		"ErrConflict":           ErrConflict,
		"ErrValidation":         ErrValidation,
		"ErrInternal":           ErrInternal,
	}

	for name, err := range errs {
		if err == nil {
			t.Errorf("%s should not be nil", name)
		}
		if err.Error() == "" {
			t.Errorf("%s should have a non-empty error message", name)
		}
	}

	// 测试 errors.Is
	if !errors.Is(ErrUnauthorized, ErrUnauthorized) {
		t.Error("errors.Is should work for domain errors")
	}
}
